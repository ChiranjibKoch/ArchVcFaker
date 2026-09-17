import asyncio
import logging

from pyrogram import Client, filters
from pyrogram.errors import (
    ApiIdInvalid,
    PhoneNumberInvalid,
    PhoneCodeInvalid,
    PhoneCodeExpired,
    SessionPasswordNeeded,
    PasswordHashInvalid,
)
from pyrogram.types import Message, InlineKeyboardMarkup, InlineKeyboardButton, CallbackQuery

from config.config import API_ID, API_HASH, OWNER_ID, DEV_ID
from ArchVcfight.database.mymongo import (
    save_account,
    get_account,
    list_accounts,
    clear_vc_memberships,
    delete_account,
    get_account_count,
    add_sudo,
    remove_sudo,
    get_sudos,
    is_sudo,
    get_sudo_count,
    reset_deployment,
    deployment_days_remaining,
    get_user_language,
    _MAX_ACCOUNTS,
)
from ArchVcfight.assistant import userbot as ub
from ArchVcfight.guard import deployment_guard, invalidate_expiry_cache
from strings import t

logger = logging.getLogger("accounts")

_owner = filters.user(OWNER_ID)
_dev = filters.user(DEV_ID) if DEV_ID else filters.user(-1)

_pending: dict[int, dict] = {}
_join_pending: dict[int, dict] = {}


def _norm_phone(raw: str) -> str:
    raw = raw.strip()
    if not raw.startswith("+"):
        raw = "+" + raw
    return raw


async def _is_auth(user_id: int) -> bool:
    import os

    oid = int(os.environ.get("OWNER_ID", str(OWNER_ID)))
    if user_id == oid or (DEV_ID and user_id == DEV_ID):
        return True
    return await is_sudo(user_id)


@Client.on_message(filters.command("addaccount") & filters.private)
@deployment_guard
async def cmd_addaccount(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_auth(msg.from_user.id):
        return await msg.reply(t("not_authorized", lang))

    count = await get_account_count()
    if count >= _MAX_ACCOUNTS:
        return await msg.reply(t("addaccount_max_reached", lang, _MAX_ACCOUNTS))

    args = msg.command[1:]
    if not args:
        return await msg.reply(t("addaccount_usage", lang))

    phone = _norm_phone(args[0])
    uid = msg.from_user.id

    for a in await list_accounts():
        if a["phone"] == phone:
            return await msg.reply(t("addaccount_already_added", lang, phone))

    try:
        pg = Client(
            name=f"acc_tmp_{uid}",
            api_id=API_ID,
            api_hash=API_HASH,
            in_memory=True,
            no_updates=True,
        )
        await pg.connect()
        sent = await pg.send_code(phone)
    except PhoneNumberInvalid:
        return await msg.reply(t("addaccount_invalid_phone", lang))
    except ApiIdInvalid:
        return await msg.reply(t("addaccount_api_mismatch", lang))
    except Exception as e:
        logger.exception("send_code failed")
        return await msg.reply(t("error", lang, f"`{e}`"))

    _pending[uid] = {
        "step": "otp",
        "phone": phone,
        "client": pg,
        "code_hash": sent.phone_code_hash,
    }
    await msg.reply(t("addaccount_otp_sent", lang, phone))


@Client.on_message(filters.private & ~filters.command(
    ["start", "help", "about", "addaccount", "delacc", "listaccs",
     "addsudo", "delsudo", "listsudos", "join", "leave",
     "deployinfo", "resetdeploy", "cancel"]
))
async def handle_acc_flow(client: Client, msg: Message):
    if not msg.from_user or not msg.text:
        return
    uid = msg.from_user.id
    lang = await get_user_language(uid)
    if uid not in _pending:
        return

    text = msg.text.strip()

    if text.lower() == "/cancel":
        state = _pending.pop(uid, {})
        c = state.get("client")
        if c:
            try:
                await c.disconnect()
            except Exception:
                pass
        return await msg.reply(t("cancelled", lang))

    state = _pending[uid]
    pg: Client = state["client"]

    try:
        if state["step"] == "otp":
            code = text.replace(" ", "").replace("-", "")
            try:
                await pg.sign_in(state["phone"], state["code_hash"], code)
            except PhoneCodeInvalid:
                _pending.pop(uid, None)
                await pg.disconnect()
                return await msg.reply(t("addaccount_otp_invalid", lang))
            except PhoneCodeExpired:
                _pending.pop(uid, None)
                await pg.disconnect()
                return await msg.reply(t("addaccount_otp_expired", lang))
            except SessionPasswordNeeded:
                state["step"] = "password"
                return await msg.reply(t("addaccount_2fa_prompt", lang))
            await _finish_account(msg, uid, pg, state["phone"])

        elif state["step"] == "password":
            try:
                await pg.check_password(password=text)
            except PasswordHashInvalid:
                _pending.pop(uid, None)
                await pg.disconnect()
                return await msg.reply(t("addaccount_password_invalid", lang))
            await _finish_account(msg, uid, pg, state["phone"])

    except Exception as e:
        logger.exception("account flow error")
        _pending.pop(uid, None)
        try:
            await pg.disconnect()
        except Exception:
            pass
        await msg.reply(t("addaccount_unexpected_error", lang, e))



async def _finish_account(msg: Message, uid: int, pg: Client, phone: str):
    lang = await get_user_language(uid)
    session_string = await pg.export_session_string()
    me = await pg.get_me()
    tg_name = f"{me.first_name or ''} {me.last_name or ''}".strip() or me.username or "Unknown"
    tg_id = me.id
    await pg.disconnect()
    _pending.pop(uid, None)

    await save_account(uid, phone, session_string, tg_name=tg_name, tg_id=tg_id)

    status = "✅ Account logged in."
    try:
        if phone not in ub.get_all_sessions():
            await ub.start_account(await get_account(phone))
    except Exception as e:
        logger.exception("userbot start failed")
        status = f"⚠️ Login saved, but startup failed: `{e}`"

    accounts = await list_accounts()
    await msg.reply(
        f"{status}\n\n"
        f"👤 {tg_name}\n"
        f"🆔 `{tg_id}`\n"
        f"📱 `{phone}`\n"
        f"📦 Accounts: **{len(accounts)}/{_MAX_ACCOUNTS}**",
    )


@Client.on_message(filters.command("delacc") & filters.private & _owner)
@deployment_guard
async def cmd_delacc(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ Usage: `/delacc <phone|user_id|@username>`")
    identifier = args[0]
    session = ub.resolve_session(identifier)
    phone = session.account_phone if session else _norm_phone(identifier)
    await clear_vc_memberships(phone)
    await delete_account(phone)
    if session:
        await session.stop()
        ub.get_all_sessions().pop(phone, None)
    await msg.reply(f"✅ Account removed: `{phone}`")


@Client.on_message(filters.command("listaccs") & filters.private)
@deployment_guard
async def cmd_listaccs(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if msg.from_user.id != int(os.environ.get("OWNER_ID", str(OWNER_ID))):
        return await msg.reply(t("not_authorized", lang))
    accs = await list_accounts()
    if not accs:
        return await msg.reply("📭 No logged-in accounts.")
    lines = [f"👥 Accounts: **{len(accs)}/{_MAX_ACCOUNTS}**"]
    for i, a in enumerate(accs, 1):
        s = ub.get_all_sessions().get(a["phone"])
        state = "🟢 online" if s else "🔴 offline"
        uid = s.user_id if s else a.get("tg_id", "?")
        name = s.name if s else a.get("tg_name", "?")
        chats = len(s.joined_chats()) if s else 0
        lines.append(f"{i}. {state} **{name}** — `{uid}` — `{a['phone']}` — VC:{chats}")
    await msg.reply("\n".join(lines))


@Client.on_message(filters.command("join") & filters.private)
@deployment_guard
async def cmd_join(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_auth(msg.from_user.id):
        return await msg.reply(t("not_authorized", lang))
    args = msg.command[1:]
    if not args:
        return await msg.reply(
            "❌ Usage: `/join <chat_id> [account|account2 ...]`\n"
            "Without accounts, **all logged-in accounts** join."
        )
    try:
        chat_id = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid chat ID.")
    identifiers = args[1:] or None
    if identifiers:
        unknown = [x for x in identifiers if not ub.resolve_session(x)]
        if unknown:
            return await msg.reply(f"❌ Account not found: {', '.join(unknown)}")
    result = await ub.join_accounts(chat_id, identifiers)
    await msg.reply(
        f"📡 VC: `{chat_id}`\n"
        f"✅ Joined: **{len(result['joined'])}**\n"
        f"↩️ Already there: **{len(result['already'])}**\n"
        f"❌ Failed: **{len(result['failed'])}**"
    )


@Client.on_message(filters.command("leave") & filters.private)
@deployment_guard
async def cmd_leave(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_auth(msg.from_user.id):
        return await msg.reply(t("not_authorized", lang))
    args = msg.command[1:]
    if not args:
        chat_ids = sorted({
            chat_id
            for session in ub.get_all_sessions().values()
            for chat_id in session.joined_chats()
        })
        if not chat_ids:
            return await msg.reply("ℹ️ No active voice chats found.")
        results = await asyncio.gather(
            *(ub.leave_accounts(chat_id) for chat_id in chat_ids)
        )
        left = sum(len(result["left"]) for result in results)
        not_joined = sum(len(result["not_joined"]) for result in results)
        failed = sum(len(result["failed"]) for result in results)
        return await msg.reply(
            f"📡 Voice chats: **{len(chat_ids)}**\n"
            f"👋 Left: **{left}**\n"
            f"↩️ Not joined: **{not_joined}**\n"
            f"❌ Failed: **{failed}**"
        )
    try:
        chat_id = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid chat ID.")
    identifiers = args[1:] or None
    if identifiers:
        unknown = [x for x in identifiers if not ub.resolve_session(x)]
        if unknown:
            return await msg.reply(f"❌ Account not found: {', '.join(unknown)}")
    result = await ub.leave_accounts(chat_id, identifiers)
    await msg.reply(
        f"📡 VC: `{chat_id}`\n"
        f"👋 Left: **{len(result['left'])}**\n"
        f"↩️ Not joined: **{len(result['not_joined'])}**\n"
        f"❌ Failed: **{len(result['failed'])}**"
    )


@Client.on_message(filters.command("vcaccounts") & filters.private)
@deployment_guard
async def cmd_vcaccounts(client: Client, msg: Message):
    if msg.from_user.id != int(os.environ.get("OWNER_ID", str(OWNER_ID))):
        return
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ Usage: `/vcaccounts <chat_id>`")
    try:
        chat_id = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid chat ID.")
    lines = [f"🎙 VC `{chat_id}`"]
    for s in ub.get_all_sessions().values():
        if s.joined(chat_id):
            lines.append(f"🟢 `{s.account_phone}` — `{s.user_id}` — {s.name}")
    if len(lines) == 1:
        lines.append("No managed accounts are currently joined.")
    await msg.reply("\n".join(lines))


@Client.on_message(filters.command("addsudo") & filters.private & _owner)
@deployment_guard
async def cmd_addsudo(client: Client, msg: Message):
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ Usage: `/addsudo <user_id>`")
    try:
        target = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid user ID.")
    ok = await add_sudo(target)
    await msg.reply("✅ Sudo added." if ok else "❌ Sudo limit reached.")


@Client.on_message(filters.command("delsudo") & filters.private & _owner)
@deployment_guard
async def cmd_delsudo(client: Client, msg: Message):
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ Usage: `/delsudo <user_id>`")
    await remove_sudo(int(args[0]))
    await msg.reply("✅ Sudo removed.")


@Client.on_message(filters.command("listsudos") & filters.private & _owner)
@deployment_guard
async def cmd_listsudos(client: Client, msg: Message):
    sudos = await get_sudos()
    await msg.reply("\n".join([f"👑 Sudo users: {len(sudos)}"] + [f"• `{u}`" for u in sudos]) if sudos else "No sudo users.")


@Client.on_message(filters.command("deployinfo") & filters.private)
@deployment_guard
async def cmd_deployinfo(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    await msg.reply(f"⏳ Deployment days remaining: {await deployment_days_remaining()}")


@Client.on_message(filters.command("resetdeploy") & filters.private & _dev)
async def cmd_resetdeploy(client: Client, msg: Message):
    await reset_deployment()
    invalidate_expiry_cache()
    await msg.reply("✅ Deployment reset.")


@Client.on_message(filters.command("cancel") & filters.private)
async def cmd_cancel(client: Client, msg: Message):
    state = _pending.pop(msg.from_user.id, None)
    if state and state.get("client"):
        try:
            await state["client"].disconnect()
        except Exception:
            pass
    await msg.reply("✅ Cancelled.")
