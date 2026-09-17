import asyncio
import logging
import os
import tempfile
from pyrogram import Client, filters
from pyrogram.types import Message
from pyrogram.errors import (
    RPCError, FloodWait,
    UserNotParticipant, ChannelPrivate, PeerIdInvalid, UsernameNotOccupied
)
from pyrogram.handlers import MessageHandler

from config.config import OWNER_ID, DEV_ID
from ArchVcfight.database.mymongo import list_accounts, get_account, is_sudo
from ArchVcfight.assistant import userbot as ub
from ArchVcfight.guard import deployment_guard

logger = logging.getLogger("ub_actions")

async def _is_auth(user_id: int) -> bool:
    if user_id == OWNER_ID or (DEV_ID and user_id == DEV_ID):
        return True
    return await is_sudo(user_id)

def _get_ub_client():
    sessions = ub.get_all_sessions()
    if not sessions:
        raise RuntimeError("❌ No logged-in userbot account. Use /addaccount first.")
    return next(iter(sessions.values())).client

def parse_tg_link(link: str):
    link = link.strip()
    if "://" in link:
        link = link.split("://", 1)[1]
    if link.startswith("t.me/"):
        parts = link.split("/")
        if len(parts) >= 3:
            try:
                message_id = int(parts[-1])
            except ValueError:
                return None, None
            if parts[1] == "c":
                try:
                    chat_id = int("-100" + parts[2])
                except ValueError:
                    return None, None
                return chat_id, message_id
            else:
                username = parts[1]
                return f"@{username}", message_id
    parts = link.split()
    if len(parts) == 2 and parts[0].lstrip('-').isdigit() and parts[1].isdigit():
        return parts[0], int(parts[1])
    return None, None

_active_forwarders = {}

async def _forward_handler(client: Client, message: Message, forward_to: int, username_filter: str):
    if username_filter:
        if not message.from_user or message.from_user.username.lower() != username_filter.lower():
            return
    try:
        await message.copy(chat_id=forward_to)
    except RPCError as e:
        logger.error(f"Forward error: {e}")

async def _ensure_membership(ub_client: Client, chat_id) -> str | None:
    """Return None if ok, else an error message string."""
    try:
        await ub_client.get_chat(chat_id)
        return None
    except (UserNotParticipant, ChannelPrivate):
        return "❌ Userbot is not a member of that chat. Use /ubjoin first."
    except PeerIdInvalid:
        return "❌ Invalid chat ID. For supergroups/channels use -100 prefix (e.g., -1001234567890)."
    except UsernameNotOccupied:
        return "❌ Username not found or not public."
    except RPCError as e:
        return f"❌ Can't access chat: `{e}`"

@Client.on_message(filters.command("ubjoin") & (filters.private | filters.group))
@deployment_guard
async def ub_join_cmd(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    if len(msg.command) < 2:
        return await msg.reply("❌ Usage: `/ubjoin @username` or `/ubjoin invite_link`")
    target = msg.command[1]
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    try:
        await ub_client.join_chat(target)
        await msg.reply(f"✅ Userbot joined `{target}`.")
    except RPCError as e:
        await msg.reply(f"❌ Join failed: `{e}`")

@Client.on_message(filters.command("ubleave") & (filters.private | filters.group))
@deployment_guard
async def ub_leave_cmd(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    if len(msg.command) < 2:
        return await msg.reply("❌ Usage: `/ubleave @username` or `/ubleave chat_id`")
    target = msg.command[1]
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    try:
        await ub_client.leave_chat(target)
        await msg.reply(f"👋 Userbot left `{target}`.")
    except RPCError as e:
        await msg.reply(f"❌ Error: `{e}`")

@Client.on_message(filters.command("ubgetmembers") & (filters.private | filters.group))
@deployment_guard
async def ub_get_members(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    if len(msg.command) < 2:
        return await msg.reply("❌ Usage: `/ubgetmembers chat_id`")
    chat_id = msg.command[1]
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    err = await _ensure_membership(ub_client, chat_id)
    if err:
        return await msg.reply(err)
    try:
        chat = await ub_client.get_chat(chat_id)
        if chat.type in ("private", "bot"):
            return await msg.reply("❌ Can't get members of a private chat/bot.")
        if getattr(chat, "is_members_hidden", False):
            return await msg.reply("🔒 Members list is hidden in this chat.")
        lines = [f"**👥 Members of `{chat.title or chat_id}`**"]
        count = 0
        async for member in ub_client.get_chat_members(chat_id, limit=30):
            count += 1
            user = member.user
            status = member.status.name
            emoji = {"administrator": "🔧", "owner": "👑", "member": "👤"}.get(status, "❓")
            lines.append(f"{emoji} `{user.id}` {user.first_name} {user.last_name or ''} [{status}]")
        if count == 0:
            lines.append("No members found (maybe not a group).")
        await msg.reply("\n".join(lines))
    except RPCError as e:
        await msg.reply(f"❌ Error: `{e}`")
    except Exception as e:
        await msg.reply(f"❌ Unexpected error: `{e}`")

@Client.on_message(filters.command("ubmembercount") & (filters.private | filters.group))
@deployment_guard
async def ub_member_count(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    if len(msg.command) < 2:
        return await msg.reply("❌ Usage: `/ubmembercount chat_id`")
    chat_id = msg.command[1]
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    err = await _ensure_membership(ub_client, chat_id)
    if err:
        return await msg.reply(err)
    try:
        chat = await ub_client.get_chat(chat_id)
        if chat.type == "private":
            return await msg.reply("❌ Can't get member count of a private chat.")
        if getattr(chat, "is_members_hidden", False):
            return await msg.reply("🔒 Members list is hidden – cannot count admins/bots.")
        total = chat.members_count if hasattr(chat, 'members_count') and chat.members_count else None
        admins = 0
        bots = 0
        async for m in ub_client.get_chat_members(chat_id, filter="administrators"):
            admins += 1
        async for m in ub_client.get_chat_members(chat_id, filter="bots"):
            bots += 1
        text = f"**📊 Chat: `{chat.title or chat_id}`**\n"
        if total:
            text += f"👥 Total members: `{total}`\n"
        else:
            text += "👥 Total members: unknown (use /ubgetmembers to inspect)\n"
        text += f"🔧 Admins (incl. owner): `{admins}`\n"
        text += f"🤖 Bots: `{bots}`"
        await msg.reply(text)
    except RPCError as e:
        await msg.reply(f"❌ Error: `{e}`")

@Client.on_message(filters.command("forward") & (filters.private | filters.group))
@deployment_guard
async def start_forward(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    args = msg.command[1:]
    if len(args) < 1:
        return await msg.reply("❌ Usage: `/forward chat_id [username]`")
    chat_id = args[0]
    username = args[1] if len(args) > 1 else None
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    err = await _ensure_membership(ub_client, chat_id)
    if err:
        return await msg.reply(err)
    user_id = msg.from_user.id
    if user_id in _active_forwarders:
        old_chat, old_handler, _ = _active_forwarders.pop(user_id)
        try:
            ub_client.remove_handler(old_handler, group=1)
        except Exception:
            pass
    async def handler_wrapper(c: Client, m: Message):
        await _forward_handler(c, m, forward_to=user_id, username_filter=username)
    handler = MessageHandler(handler_wrapper, filters.chat(chat_id))
    ub_client.add_handler(handler, group=1)
    _active_forwarders[user_id] = (chat_id, handler, username)
    await msg.reply(
        f"🔁 Copying messages from `{chat_id}` to your DM.\n"
        + (f"Filter: `@{username}`" if username else "All messages") +
        "\nUse /stopforward to stop."
    )

@Client.on_message(filters.command("stopforward") & (filters.private | filters.group))
@deployment_guard
async def stop_forward(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    user_id = msg.from_user.id
    if user_id not in _active_forwarders:
        return await msg.reply("ℹ️ No active copying.")
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    chat_id, handler, _ = _active_forwarders.pop(user_id)
    try:
        ub_client.remove_handler(handler, group=1)
    except Exception as e:
        logger.error(f"Handler removal error: {e}")
    await msg.reply(f"✅ Stopped copying from `{chat_id}`.")

@Client.on_message(filters.command("replymessage") & (filters.private | filters.group))
@deployment_guard
async def reply_message_cmd(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    args = msg.command[1:]
    if len(args) < 3:
        return await msg.reply("❌ Usage: `/replymessage chat_id username [message_id] <reply text>`")
    chat_id = args[0]
    username = args[1].lstrip("@")
    if args[2].isdigit():
        message_id = int(args[2])
        text_start = 3
    else:
        message_id = None
        text_start = 2
    reply_text = " ".join(args[text_start:])
    if not reply_text:
        return await msg.reply("❌ Provide a reply text.")
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    err = await _ensure_membership(ub_client, chat_id)
    if err:
        return await msg.reply(err)
    try:
        if message_id:
            target_msg = await ub_client.get_messages(chat_id, message_id)
            if not target_msg:
                return await msg.reply("❌ Message not found.")
            if not target_msg.from_user or target_msg.from_user.username.lower() != username.lower():
                return await msg.reply("❌ That message is not from the specified user.")
        else:
            target_msg = None
            async for m in ub_client.get_chat_history(chat_id, limit=50):
                if m.from_user and m.from_user.username and m.from_user.username.lower() == username.lower():
                    target_msg = m
                    break
            if not target_msg:
                return await msg.reply(f"❌ No recent message found from @{username}.")
        await ub_client.send_message(
            chat_id,
            reply_text,
            reply_to_message_id=target_msg.id
        )
        await msg.reply(f"✅ Replied to @{username} in `{chat_id}`.")
    except RPCError as e:
        await msg.reply(f"❌ Error: `{e}`")

@Client.on_message(filters.command("ubdownload") & (filters.private | filters.group))
@deployment_guard
async def cmd_download(client: Client, msg: Message):
    if not await _is_auth(msg.from_user.id):
        return await msg.reply("🚫 Not authorized.")
    if len(msg.command) < 2:
        return await msg.reply("❌ Usage: `/ubdownload <t.me link> [target_chat_id]`")
    link = msg.command[1]
    target_chat = msg.command[2] if len(msg.command) > 2 else None
    user_id = msg.from_user.id
    chat_id, message_id = parse_tg_link(link)
    if not chat_id or not message_id:
        return await msg.reply("❌ Invalid link. Use a full t.me message link.")
    try:
        ub_client = _get_ub_client()
    except RuntimeError as e:
        return await msg.reply(str(e))
    err = await _ensure_membership(ub_client, chat_id)
    if err:
        return await msg.reply(err)
    try:
        target_msg = await ub_client.get_messages(chat_id, message_id)
        if not target_msg:
            return await msg.reply("❌ Message not found.")
    except RPCError as e:
        return await msg.reply(f"❌ Error fetching message: `{e}`")
    file_size = 0
    if target_msg.document:
        file_size = target_msg.document.file_size or 0
    elif target_msg.video:
        file_size = target_msg.video.file_size or 0
    elif target_msg.audio:
        file_size = target_msg.audio.file_size or 0
    elif target_msg.voice:
        file_size = target_msg.voice.file_size or 0
    elif target_msg.animation:
        file_size = target_msg.animation.file_size or 0
    elif target_msg.photo:
        file_size = target_msg.photo.file_size if target_msg.photo else 0
    if not any([target_msg.document, target_msg.video, target_msg.audio, target_msg.photo, target_msg.voice, target_msg.video_note, target_msg.animation]):
        return await msg.reply("❌ No downloadable media in that message.")
    if file_size > 2 * 1024 * 1024 * 1024:
        me = await ub_client.get_me()
        if not me.is_premium:
            return await msg.reply("⚠️ File >2 GB and userbot is not premium. Cannot upload.")
    out_chat = target_chat if target_chat else user_id
    status_msg = await msg.reply("⏳ Downloading…")
    try:
        with tempfile.TemporaryDirectory() as tmpdir:
            downloaded = await ub_client.download_media(target_msg, file_name=os.path.join(tmpdir, ""))
            if not downloaded:
                return await status_msg.edit_text("❌ Download failed.")
            await status_msg.edit_text("📤 Uploading…")
            caption = target_msg.caption if target_msg.caption else ""
            await ub_client.send_document(
                out_chat,
                downloaded,
                caption=caption,
                force_document=True
            )
            await status_msg.delete()
            await msg.reply(f"✅ File sent to `{out_chat}`.")
    except FloodWait as e:
        await status_msg.edit_text(f"⏳ FloodWait: sleeping {e.value} seconds…")
        await asyncio.sleep(e.value)
        try:
            await ub_client.send_document(out_chat, downloaded, caption=caption, force_document=True)
            await status_msg.edit_text(f"✅ File sent to `{out_chat}` (after flood wait).")
        except Exception as e2:
            await status_msg.edit_text(f"❌ Failed after retry: `{e2}`")
    except RPCError as e:
        await status_msg.edit_text(f"❌ Error: `{e}`")
    except Exception as e:
        await status_msg.edit_text(f"❌ Unexpected error: `{e}`")
