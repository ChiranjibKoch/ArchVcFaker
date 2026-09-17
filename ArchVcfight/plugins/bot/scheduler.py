import asyncio
import logging
import time
from datetime import datetime, timezone
from uuid import uuid4

from pyrogram import Client, filters
from pyrogram.types import Message

from config.config import OWNER_ID
from ArchVcfight.assistant import userbot as ub
from ArchVcfight.guard import deployment_guard, cb_deployment_guard

logger = logging.getLogger("scheduler")
_owner = filters.user(OWNER_ID)

_jobs: dict[str, dict] = {}


def _ts(hhmm: str) -> float:
    now = datetime.now(timezone.utc)
    h, m = map(int, hhmm.split(":"))
    t = now.replace(hour=h, minute=m, second=0, microsecond=0)
    return t.timestamp() if t.timestamp() > now.timestamp() else t.replace(day=t.day + 1).timestamp()


async def _runner(job_id: str, at: float, coro):
    delay = at - time.time()
    if delay > 0:
        await asyncio.sleep(delay)
    if job_id not in _jobs:
        return
    try:
        await coro
    except Exception as e:
        logger.error("job %s failed: %s", job_id, e)
    finally:
        _jobs.pop(job_id, None)


@deployment_guard
@Client.on_message(filters.command("splay") & _owner)
async def cmd_splay(client: Client, msg: Message):
    args = msg.command[1:]
    if len(args) < 3:
        return await msg.reply("❌ `/splay <chat_id> <HH:MM UTC> <url>`")
    cid, hhmm, url = int(args[0]), args[1], args[2]
    at = _ts(hhmm)
    jid = str(uuid4())[:8]
    task = asyncio.create_task(_runner(jid, at, ub.player.play_url(cid, url)))
    _jobs[jid] = {"type": "play", "chat_id": cid, "at": at, "info": url[:50], "task": task}
    ts = datetime.fromtimestamp(at, timezone.utc).strftime("%H:%M UTC")
    await msg.reply(f"📅 Play `{cid}` at `{ts}` | ID: `{jid}`")


@deployment_guard
@Client.on_message(filters.command("smsg") & _owner)
async def cmd_smsg(client: Client, msg: Message):
    args = msg.command[1:]
    if len(args) < 3:
        return await msg.reply("❌ `/smsg <chat_id> <HH:MM UTC> <text>`")
    cid, hhmm = int(args[0]), args[1]
    text = " ".join(args[2:])
    at = _ts(hhmm)
    jid = str(uuid4())[:8]
    task = asyncio.create_task(_runner(jid, at, client.send_message(cid, text)))
    _jobs[jid] = {"type": "msg", "chat_id": cid, "at": at, "info": text[:40], "task": task}
    ts = datetime.fromtimestamp(at, timezone.utc).strftime("%H:%M UTC")
    await msg.reply(f"📅 Msg `{cid}` at `{ts}` | ID: `{jid}`")


@deployment_guard
@Client.on_message(filters.command("slist") & _owner)
async def cmd_slist(client: Client, msg: Message):
    if not _jobs:
        return await msg.reply("📭 No jobs.")
    lines = ["**📅 Scheduled**"]
    for jid, j in _jobs.items():
        ts = datetime.fromtimestamp(j["at"], timezone.utc).strftime("%H:%M UTC")
        lines.append(f"• `{jid}` {j['type']} → `{j['chat_id']}` @ `{ts}` | _{j['info']}_")
    await msg.reply("\n".join(lines))


@deployment_guard
@Client.on_message(filters.command("scancel") & _owner)
async def cmd_scancel(client: Client, msg: Message):
    args = msg.command[1:]
    if not args:
        return await msg.reply("❌ `/scancel <job_id>`")
    j = _jobs.pop(args[0], None)
    if not j:
        return await msg.reply(f"⚠️ `{args[0]}` not found.")
    j["task"].cancel()
    await msg.reply(f"🗑 `{args[0]}` cancelled.")
