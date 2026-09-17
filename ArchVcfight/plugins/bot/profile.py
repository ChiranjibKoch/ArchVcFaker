from pyrogram import Client, filters
from pyrogram.types import Message
from config.config import OWNER_ID
from ArchVcfight.assistant import userbot as ub
from ArchVcfight.database.mymongo import is_sudo
from ArchVcfight.guard import deployment_guard

async def _auth(uid: int) -> bool:
    return uid == OWNER_ID or await is_sudo(uid)

def _target(args):
    return ub.resolve_session(args[0]) if args else None

@Client.on_message(filters.command("changename"))
@deployment_guard
async def changename(_, msg: Message):
    if not await _auth(msg.from_user.id): return
    if len(msg.command) < 2: return await msg.reply("❌ `/changename <first> [last] <account>`")
    s = _target(msg.command[-1:])
    args = msg.command[1:-1] if s else msg.command[1:]
    if not s or not args: return await msg.reply("❌ Usage: `/changename First Last <account>`")
    parts = " ".join(args).split(None, 1)
    await s.client.update_profile(first_name=parts[0], last_name=parts[1] if len(parts)>1 else "")
    await msg.reply("✅ Profile updated.")

@Client.on_message(filters.command("changebio"))
@deployment_guard
async def changebio(_, msg: Message):
    if not await _auth(msg.from_user.id): return
    if len(msg.command) < 3: return await msg.reply("❌ Usage: `/changebio <bio> <account>`")
    s = _target(msg.command[-1:])
    if not s: return await msg.reply("❌ Account not found.")
    await s.client.update_profile(bio=" ".join(msg.command[1:-1]))
    await msg.reply("✅ Bio updated.")
