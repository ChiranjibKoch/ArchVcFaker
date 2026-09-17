import asyncio
import logging
import os
import tempfile

from pyrogram import Client
from pyrogram.types import Message
from pytgcalls.types import AudioQuality, MediaStream, VideoQuality

logger = logging.getLogger("platforms.telegram")


async def download_and_stream(
    bot: Client,
    msg: Message,
    player,
    chat_id: int,
) -> str:
    media = (
        msg.audio
        or msg.video
        or msg.voice
        or msg.video_note
        or msg.document
    )
    if not media:
        raise ValueError("Message has no streamable media")

    suffix = ""
    if msg.audio:
        suffix = ".mp3"
    elif msg.video or msg.video_note:
        suffix = ".mp4"
    elif msg.voice:
        suffix = ".ogg"
    elif msg.document:
        fname = getattr(media, "file_name", None) or ""
        suffix = os.path.splitext(fname)[1] or ".bin"

    tmp = tempfile.NamedTemporaryFile(delete=False, suffix=suffix)
    tmp.close()

    logger.info("downloading file_id=%s → %s", media.file_id, tmp.name)
    path = await bot.download_media(msg, file_name=tmp.name)

    await player.play_file(chat_id, path)
    logger.info("streaming telegram file chat=%d path=%s", chat_id, path)
    return path


async def stream_telegram_url(
    player,
    chat_id: int,
    direct_url: str,
    headers: dict | None = None,
) -> None:
    await player.play_url(
        chat_id,
        direct_url,
        audio_q=AudioQuality.HIGH,
        video_q=VideoQuality.HD_720p,
        headers=headers,
    )
    logger.info("streaming telegram url chat=%d", chat_id)


def cleanup_file(path: str) -> None:
    try:
        os.remove(path)
        logger.debug("removed temp file %s", path)
    except OSError:
        pass
