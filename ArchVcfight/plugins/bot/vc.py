import os
from pyrogram import Client, filters
from pyrogram.types import Message
from config.config import OWNER_ID, DEV_ID
from ArchVcfight.assistant import userbot as ub
from ArchVcfight.database.mymongo import is_sudo
from ArchVcfight.guard import deployment_guard


async def _auth(user_id: int) -> bool:
    oid = int(os.environ.get("OWNER_ID", str(OWNER_ID)))
    return user_id == oid or user_id == DEV_ID or await is_sudo(user_id)


def _is_owner(user_id: int) -> bool:
    return user_id == int(os.environ.get("OWNER_ID", str(OWNER_ID)))


@Client.on_message(filters.command("vcstatus") & filters.private)
@deployment_guard
async def vcstatus(_, msg: Message):
    if not _is_owner(msg.from_user.id):
        return
    sessions = ub.get_all_sessions()
    lines = [f"👥 Accounts online: **{len(sessions)}**"]
    for s in sessions.values():
        joined = s.joined_chats()
        lines.append(
            f"{'🟢' if s._running else '🔴'} `{s.account_phone}` "
            f"— `{s.user_id}` — VC: **{len(joined)}**"
        )
    await msg.reply("\n".join(lines))


@Client.on_message(filters.command("leaveaccount") & filters.private)
@deployment_guard
async def leaveaccount(_, msg: Message):
    if not await _auth(msg.from_user.id):
        return
    args = msg.command[1:]
    if len(args) < 2:
        return await msg.reply("❌ Usage: `/leaveaccount <chat_id> <phone|user_id|@username>`")
    try:
        chat_id = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid chat ID.")
    s = ub.resolve_session(args[1])
    if not s:
        return await msg.reply("❌ Account not found.")
    ok = await s.leave_vc(chat_id)
    await msg.reply(
        f"{'✅' if ok else 'ℹ️'} `{s.account_phone}` "
        f"{'left' if ok else 'was not in'} VC `{chat_id}`."
    )


@Client.on_message(filters.command("mute") & filters.private)
@deployment_guard
async def mute(_, msg: Message):
    if not await _auth(msg.from_user.id):
        return
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ Usage: `/mute <chat_id> [account|account2 ...]`\nWithout accounts, all joined accounts are muted.")
    try:
        chat_id = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid chat ID.")
    identifiers = args[1:] or None
    if identifiers:
        unknown = [x for x in identifiers if not ub.resolve_session(x)]
        if unknown:
            return await msg.reply(f"❌ Account not found: {', '.join(unknown)}")
    result = await ub.mute_accounts(chat_id, identifiers)
    await msg.reply(
        f"🔕 VC `{chat_id}`\n"
        f"✅ Muted: **{len(result['changed'])}**\n"
        f"↩️ Not joined: **{len(result['not_joined'])}**\n"
        f"❌ Failed: **{len(result['failed'])}**"
    )


@Client.on_message(filters.command("unmute") & filters.private)
@deployment_guard
async def unmute(_, msg: Message):
    if not await _auth(msg.from_user.id):
        return
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ Usage: `/unmute <chat_id> [account|account2 ...]`\nWithout accounts, all joined accounts are unmuted.")
    try:
        chat_id = int(args[0])
    except ValueError:
        return await msg.reply("❌ Invalid chat ID.")
    identifiers = args[1:] or None
    if identifiers:
        unknown = [x for x in identifiers if not ub.resolve_session(x)]
        if unknown:
            return await msg.reply(f"❌ Account not found: {', '.join(unknown)}")
    result = await ub.unmute_accounts(chat_id, identifiers)
    await msg.reply(
        f"🔔 VC `{chat_id}`\n"
        f"✅ Unmuted: **{len(result['changed'])}**\n"
        f"↩️ Not joined: **{len(result['not_joined'])}**\n"
        f"❌ Failed: **{len(result['failed'])}**"
    )
