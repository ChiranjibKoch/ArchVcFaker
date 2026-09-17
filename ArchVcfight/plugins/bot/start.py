import os
import time
from pyrogram import Client, filters
from pyrogram.types import Message, InlineKeyboardMarkup, InlineKeyboardButton, CallbackQuery
from config.config import OWNER_ID
from ArchVcfight.assistant import userbot as ub
from ArchVcfight.guard import deployment_guard, cb_deployment_guard
from ArchVcfight.database.mymongo import get_user_language, set_user_language, list_accounts
from strings import t

_owner = filters.user(OWNER_ID)


def _is_owner(user_id: int) -> bool:
    return user_id == int(os.environ.get("OWNER_ID", str(OWNER_ID)))


def _home_keyboard(lang: str, owner: bool = False) -> InlineKeyboardMarkup:
    rows = [[InlineKeyboardButton("➕ Add account", callback_data="menu:add")]]
    if owner:
        rows.append([
            InlineKeyboardButton("📋 Accounts", callback_data="menu:accounts"),
            InlineKeyboardButton("🟢 Online", callback_data="menu:online"),
        ])
    rows.extend([
        [InlineKeyboardButton("🎙 VC tools", callback_data="menu:vc")],
        [InlineKeyboardButton(t("btn_language", lang), callback_data="setlang")],
    ])
    return InlineKeyboardMarkup(rows)


def _home_text() -> str:
    return (
        "🤖 **Account VC Manager**\n\n"
        "Manage your user accounts and voice chats from one place.\n\n"
        "`/addaccount +phone`  Login a user account\n"
        "`/listaccs`  Show all accounts\n"
        "`/join <chat_id>`  Join with all accounts\n"
        "`/leave`  Leave all active voice chats\n"
        "`/vcstatus`  Show account status"
    )


@deployment_guard
@Client.on_message(filters.command(["start", "help"]))
async def cmd_help(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    await msg.reply(
        _home_text(),
        reply_markup=_home_keyboard(lang, _is_owner(msg.from_user.id)),
    )


@deployment_guard
@Client.on_message(filters.command("ping"))
async def cmd_ping(client: Client, msg: Message):
    t0 = time.monotonic()
    sent = await msg.reply("🏓 Pong")
    await sent.edit(f"🏓 `{round((time.monotonic() - t0) * 1000)} ms`")


@deployment_guard
@Client.on_message(filters.command("status"))
async def cmd_status(client: Client, msg: Message):
    if msg.from_user.id != OWNER_ID:
        return
    await msg.reply(f"👥 Logged-in accounts: **{len(ub.get_all_sessions())}**")


@deployment_guard
@Client.on_message(filters.command("setlang"))
async def cmd_setlang(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    kb = InlineKeyboardMarkup([
        [InlineKeyboardButton(t("lang_english", lang), callback_data="lang:en")],
        [InlineKeyboardButton(t("lang_bengali", lang), callback_data="lang:bn")],
        [InlineKeyboardButton(t("lang_hindi", lang), callback_data="lang:hi")],
    ])
    await msg.reply(t("language_select", lang), reply_markup=kb)


@cb_deployment_guard
@Client.on_callback_query(filters.regex(r"^(menu:|setlang|lang:)"))
async def cb_menu(client: Client, cq: CallbackQuery):
    uid = cq.from_user.id
    lang = await get_user_language(uid)
    if cq.data == "menu:home":
        await cq.message.edit_text(
            _home_text(),
            reply_markup=_home_keyboard(lang, _is_owner(uid)),
        )
    elif cq.data == "menu:add":
        await cq.message.edit_text(
            "➕ **Add account**\n\n"
            "Send this command with the account phone number:\n"
            "`/addaccount +1234567890`",
            reply_markup=InlineKeyboardMarkup([
                [InlineKeyboardButton("⌂ Home", callback_data="menu:home")],
            ]),
        )
    elif cq.data == "menu:accounts":
        if not _is_owner(uid):
            return await cq.answer("Owner-only account information.", show_alert=True)
        accounts = await list_accounts()
        if not accounts:
            text = "📭 **Accounts**\n\nNo accounts are logged in."
        else:
            lines = [f"📋 **Accounts: {len(accounts)}**", ""]
            for account in accounts[:30]:
                session = ub.get_all_sessions().get(account["phone"])
                state = "🟢" if session else "🔴"
                name = session.name if session else account.get("tg_name", "Unknown")
                lines.append(f"{state} {name}  `{account['phone']}`")
            if len(accounts) > 30:
                lines.append(f"\n…and {len(accounts) - 30} more. Use `/listaccs` for the full list.")
            text = "\n".join(lines)
        await cq.message.edit_text(
            text,
            reply_markup=InlineKeyboardMarkup([
                [InlineKeyboardButton("⌂ Home", callback_data="menu:home")],
            ]),
        )
    elif cq.data == "menu:online":
        if not _is_owner(uid):
            return await cq.answer("Owner-only account information.", show_alert=True)
        sessions = ub.get_all_sessions()
        if not sessions:
            text = "🟢 **Online accounts**\n\nNo accounts are currently online."
        else:
            lines = [f"🟢 **Online accounts: {len(sessions)}**", ""]
            for session in sessions.values():
                lines.append(
                    f"🟢 {session.name or 'Unknown'}  `{session.account_phone}`  "
                    f"VC: **{len(session.joined_chats())}**"
                )
            text = "\n".join(lines)
        await cq.message.edit_text(
            text,
            reply_markup=InlineKeyboardMarkup([
                [InlineKeyboardButton("⌂ Home", callback_data="menu:home")],
            ]),
        )
    elif cq.data == "menu:vc":
        await cq.message.edit_text(
            "🎙 **Voice chat tools**\n\n"
            "`/join <chat_id>`  All accounts join\n"
            "`/leave <chat_id>`  All accounts leave\n"
            "`/leave`  Leave every active voice chat\n"
            "`/mute <chat_id>`  Mute all joined accounts\n"
            "`/unmute <chat_id>`  Unmute all joined accounts\n"
            "`/vcaccounts <chat_id>`  Show active accounts\n"
            "`/vcstatus`  Show voice chat status",
            reply_markup=InlineKeyboardMarkup([
                [InlineKeyboardButton("⌂ Home", callback_data="menu:home")],
            ]),
        )
    elif cq.data == "setlang":
        kb = InlineKeyboardMarkup([
            [InlineKeyboardButton(t("lang_english", lang), callback_data="lang:en")],
            [InlineKeyboardButton(t("lang_bengali", lang), callback_data="lang:bn")],
            [InlineKeyboardButton(t("lang_hindi", lang), callback_data="lang:hi")],
            [InlineKeyboardButton("⌂ Home", callback_data="menu:home")],
        ])
        await cq.message.edit_text(t("language_select", lang), reply_markup=kb)
    else:
        new_lang = cq.data.split(":", 1)[1]
        await set_user_language(uid, new_lang)
        await cq.answer(t("setlang_success", new_lang, new_lang))
        await cq.message.edit_text(
            "✅ Language updated.",
            reply_markup=_home_keyboard(new_lang, _is_owner(uid)),
        )


@deployment_guard
@Client.on_message(filters.command("restart") & _owner)
async def cmd_restart(client: Client, msg: Message):
    import os, sys
    await msg.reply("♻️ Restarting...")
    os.execv(sys.executable, [sys.executable] + sys.argv)
