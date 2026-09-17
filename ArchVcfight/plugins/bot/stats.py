from datetime import datetime, timezone

from pyrogram import Client, filters
from pyrogram.types import Message

from config.config import OWNER_ID
from ArchVcfight.database.mymongo import col
from ArchVcfight.guard import deployment_guard, cb_deployment_guard

_owner = filters.user(OWNER_ID)


async def track_play(chat_id: int, url: str, duration: float = 0.0) -> None:
    now = datetime.now(timezone.utc).isoformat()
    await col("stats").update_one(
        {"_id": "global"},
        {"$inc": {"total_plays": 1, "total_seconds": duration}, "$set": {"last_play": now}},
        upsert=True,
    )
    await col("chat_stats").update_one(
        {"_id": chat_id},
        {"$inc": {"plays": 1, "seconds": duration}, "$push": {"history": {"url": url[:100], "at": now}}},
        upsert=True,
    )


@deployment_guard
@Client.on_message(filters.command("stats"))
async def cmd_stats(client: Client, msg: Message):
    doc = await col("stats").find_one({"_id": "global"}) or {}
    plays = doc.get("total_plays", 0)
    sec = int(doc.get("total_seconds", 0))
    h, rem = divmod(sec, 3600)
    m, s = divmod(rem, 60)
    top = await col("commands").find().sort("count", -1).limit(5).to_list(5)
    cmds = "\n".join(f"  `/{c['_id']}` — {c['count']}×" for c in top) or "  _None_"
    await msg.reply(
        f"**📊 Stats**\n🎵 Plays: `{plays}` | ⏱ `{h}h{m}m{s}s`\n\n**Top:**\n{cmds}"
    )


@deployment_guard
@Client.on_message(filters.command("chatstats"))
async def cmd_chatstats(client: Client, msg: Message):
    args = msg.command[1:]
    cid = int(args[0]) if args else msg.chat.id
    doc = await col("chat_stats").find_one({"_id": cid}) or {}
    plays = doc.get("plays", 0)
    sec = int(doc.get("seconds", 0))
    h, rem = divmod(sec, 3600)
    m, s = divmod(rem, 60)
    history = doc.get("history", [])[-5:]
    recent = "\n".join(f"• `{r['url'][:50]}` {r['at'][:10]}" for r in reversed(history)) or "_None_"
    await msg.reply(f"**📊 `{cid}`**\n🎵 `{plays}` | ⏱ `{h}h{m}m{s}s`\n\n{recent}")


@deployment_guard
@Client.on_message(filters.command("resetstats") & _owner)
async def cmd_resetstats(client: Client, msg: Message):
    for c in ("stats", "chat_stats", "commands"):
        await col(c).drop()
    await msg.reply("🗑 Stats reset.")
