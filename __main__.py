import asyncio
import logging
import signal
import sys

from pyrogram import Client

from config.config import API_ID, API_HASH, BOT_TOKEN, MONGO_URI
from ArchVcfight.database.mymongo import (
    init_db,
    close_db,
    DB_NAME,
    record_deployment,
    mark_deployment_inactive,
    deployment_days_remaining,
    is_deployment_expired,
)
from ArchVcfight.assistant import userbot as ub

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s | %(levelname)s | %(name)s | %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
    handlers=[
        logging.StreamHandler(sys.stdout),

    ],
)
logging.getLogger("pyrogram").setLevel(logging.WARNING)
logging.getLogger("pytgcalls").setLevel(logging.WARNING)

logger = logging.getLogger("main")

bot = Client(
    "bot",
    api_id=API_ID,
    api_hash=API_HASH,
    bot_token=BOT_TOKEN,
    plugins=dict(root="ArchVcfight/plugins/bot"),
)


async def main() -> None:
    await init_db(MONGO_URI)
    logger.info("Using DB: %s", DB_NAME)

    # Restore persisted owner_id (dev may have changed it via /changeowner)
    from ArchVcfight.database.mymongo import get_owner_id
    import os, config.config as _cfg
    persisted_owner = await get_owner_id()
    if persisted_owner and persisted_owner != _cfg.OWNER_ID:
        _cfg.OWNER_ID = persisted_owner
        os.environ["OWNER_ID"] = str(persisted_owner)
        logger.info("Restored persisted OWNER_ID=%d from DB", persisted_owner)

    deploy_doc = await record_deployment()
    days_left = await deployment_days_remaining()
    expired = await is_deployment_expired()

    if expired:
        logger.warning("Deployment EXPIRED — bot running but all commands locked")
    else:
        logger.info("Deployment active — %d day(s) remaining", days_left)

    await bot.start()
    await ub.start()

    me = await bot.get_me()
    logger.info("bot=@%s ready", me.username)

    stop_event = asyncio.Event()
    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, stop_event.set)
        except (NotImplementedError, AttributeError):
            pass

    try:
        await stop_event.wait()
    finally:
        await mark_deployment_inactive()
        await ub.stop()
        await bot.stop()
        await close_db()
        logger.info("shutdown complete")


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
