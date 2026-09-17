import logging
from datetime import datetime, timezone

from pyrogram import Client, filters
from pyrogram.enums import ChatMemberStatus
from pyrogram.types import Message, ChatPermissions

from config.config import OWNER_ID
from ArchVcfight.database.mymongo import col, get_user_language
from ArchVcfight.guard import deployment_guard, cb_deployment_guard
from strings import t

logger = logging.getLogger("admin")
_owner = filters.user(OWNER_ID)

async def _is_admin(client: Client, chat_id: int, user_id: int) -> bool:
    try:
        m = await client.get_chat_member(chat_id, user_id)
        return m.status in (ChatMemberStatus.ADMINISTRATOR, ChatMemberStatus.OWNER)
    except Exception:
        return False

def _target(msg: Message) -> int | None:
    if msg.reply_to_message and msg.reply_to_message.from_user:
        return msg.reply_to_message.from_user.id
    if len(msg.command) > 1:
        try:
            return int(msg.command[1])
        except ValueError:
            pass
    return None

@deployment_guard
@Client.on_message(filters.command("kick") & filters.group)
async def cmd_kick(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_admin(client, msg.chat.id, msg.from_user.id):
        return await msg.reply(t("admins_only", lang))
    uid = _target(msg)
    if not uid:
        return await msg.reply(t("kick_usage", lang))
    await client.ban_chat_member(msg.chat.id, uid)
    await client.unban_chat_member(msg.chat.id, uid)
    await msg.reply(t("kick_success", lang, uid))

@deployment_guard
@Client.on_message(filters.command("ban") & filters.group)
async def cmd_ban(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_admin(client, msg.chat.id, msg.from_user.id):
        return await msg.reply(t("admins_only", lang))
    uid = _target(msg)
    if not uid:
        return await msg.reply(t("ban_usage", lang))
    await client.ban_chat_member(msg.chat.id, uid)
    await msg.reply(t("ban_success", lang, uid))

@deployment_guard
@Client.on_message(filters.command("unban") & filters.group)
async def cmd_unban(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_admin(client, msg.chat.id, msg.from_user.id):
        return await msg.reply(t("admins_only", lang))
    uid = _target(msg)
    if not uid:
        return await msg.reply(t("unban_usage", lang))
    await client.unban_chat_member(msg.chat.id, uid)
    await msg.reply(t("unban_success", lang, uid))

@deployment_guard
@Client.on_message(filters.command("gmute") & filters.group)
async def cmd_gmute(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_admin(client, msg.chat.id, msg.from_user.id):
        return await msg.reply(t("admins_only", lang))
    uid = _target(msg)
    if not uid:
        return await msg.reply(t("gmute_usage", lang))
    await client.restrict_chat_member(msg.chat.id, uid, ChatPermissions(can_send_messages=False))
    await msg.reply(t("gmute_success", lang, uid))

@deployment_guard
@Client.on_message(filters.command("gunmute") & filters.group)
async def cmd_gunmute(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_admin(client, msg.chat.id, msg.from_user.id):
        return await msg.reply(t("admins_only", lang))
    uid = _target(msg)
    if not uid:
        return await msg.reply(t("gunmute_usage", lang))
    await client.restrict_chat_member(
        msg.chat.id, uid,
        ChatPermissions(
            can_send_messages=True,
            can_send_media_messages=True,
            can_send_other_messages=True,
            can_add_web_page_previews=True,
        ),
    )
    await msg.reply(t("gunmute_success", lang, uid))

@deployment_guard
@Client.on_message(filters.command("warn") & filters.group)
async def cmd_warn(client: Client, msg: Message):
    lang = await get_user_language(msg.from_user.id)
    if not await _is_admin(client, msg.chat.id, msg.from_user.id):
        return await msg.reply(t("admins_only", lang))
    uid = _target(msg)
    if not uid:
        return await msg.reply(t("warn_usage", lang))
    reason = " ".join(msg.command[2:]) or "No reason"
    c = col("warnings")
    key = {"chat_id": msg.chat.id, "user_id": uid}
    await c.update_one(
        key,
        {
            "$inc": {"count": 1},
            "$push": {"log": {"r": reason, "by": msg.from_user.id, "at": datetime.now(timezone.utc).isoformat()}},
        },
        upsert=True,
    )
    doc = await c.find_one(key)
    count = doc["count"]
    text = f"⚠️ `{uid}` warned ({count}/3). _{reason}_"
    if count >= 3:
        await client.ban_chat_member(msg.chat.id, uid)
        await c.update_one(key, {"$set": {"count": 0}})
        text += "\n🔨 Auto-banned."
    await msg.reply(text)

@deployment_guard
@Client.on_message(filters.command("warnings") & filters.group)
async def cmd_warnings(client: Client, msg: Message):
    uid = _target(msg)
    if not uid:
        return await msg.reply("❌ Reply to user or provide ID.")
    doc = await col("warnings").find_one({"chat_id": msg.chat.id, "user_id": uid})
    if not doc:
        return await msg.reply(f"✅ `{uid}` has no warnings.")
    logs = doc.get("log", [])[-5:]
    lines = [f"⚠️ **`{uid}`** — {doc['count']} warning(s)", ""]
    for w in logs:
        lines.append(f"• _{w['r']}_ — {w['at'][:10]}")
    await msg.reply("\n".join(lines))

@deployment_guard
@Client.on_message(filters.command("announce") & _owner)
async def cmd_announce(client: Client, msg: Message):
    text = " ".join(msg.command[1:])
    if not text:
        return await msg.reply("❌ `/announce <text>`")
    chats = await col("chats").find({}).to_list(1000)
    sent = failed = 0
    for chat in chats:
        try:
            await client.send_message(chat["_id"], text)
            sent += 1
        except Exception:
            failed += 1
    await msg.reply(f"📢 Announced to {sent} chat(s). Failed: {failed}")

@deployment_guard
@Client.on_message(filters.command("chatinfo"))
async def cmd_chatinfo(client: Client, msg: Message):
    c = msg.chat
    await msg.reply(
        f"**📋 Chat**\n{c.title or 'N/A'} | `{c.id}` | `{c.type.name}`\n"
        f"@{c.username or 'N/A'} | 👥 {c.members_count or 'N/A'}"
    )

@deployment_guard
@Client.on_message(filters.command("userinfo"))
async def cmd_userinfo(client: Client, msg: Message):
    u = None
    if msg.reply_to_message and msg.reply_to_message.from_user:
        u = msg.reply_to_message.from_user
    elif len(msg.command) > 1:
        try:
            u = await client.get_users(int(msg.command[1]))
        except Exception:
            pass
    if not u:
        return await msg.reply("❌ Reply to user or provide ID.")
    await msg.reply(
        f"**👤 User**\n{u.first_name} {u.last_name or ''} | `{u.id}`\n"
        f"@{u.username or 'N/A'} | Bot: {u.is_bot}"
    )
