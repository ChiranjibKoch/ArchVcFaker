import asyncio
import logging
from typing import Optional

logger = logging.getLogger("platforms.youtube")

_YDL_OPTS_AUDIO = {
    "format": "bestaudio/best",
    "quiet": True,
    "no_warnings": True,
    "noplaylist": True,
    "skip_download": True,
}

_YDL_OPTS_VIDEO = {
    "format": "bestvideo[ext=mp4]+bestaudio/best[ext=mp4]/best",
    "quiet": True,
    "no_warnings": True,
    "noplaylist": True,
    "skip_download": True,
}

_FFMPEG_RECONNECT = (
    "-reconnect 1 -reconnect_streamed 1 -reconnect_delay_max 5"
)


def _extract_sync(url: str, opts: dict) -> dict:
    import yt_dlp
    with yt_dlp.YoutubeDL(opts) as ydl:
        return ydl.extract_info(url, download=False)


async def resolve_audio(url: str) -> tuple[str, dict]:
    info = await asyncio.to_thread(_extract_sync, url, _YDL_OPTS_AUDIO)
    formats = info.get("formats") or []
    cdn = info.get("url", "")

    if formats:
        best = max(
            (f for f in formats if f.get("acodec") != "none"),
            key=lambda f: f.get("abr") or 0,
            default=None,
        )
        if best:
            cdn = best.get("url", cdn)

    headers = info.get("http_headers", {})
    logger.info("resolved audio url for %s", url)
    return cdn, headers


async def resolve_video(url: str) -> tuple[str, dict]:
    info = await asyncio.to_thread(_extract_sync, url, _YDL_OPTS_VIDEO)
    formats = info.get("formats") or []
    cdn = info.get("url", "")

    if formats:
        best = max(
            (f for f in formats if f.get("vcodec") != "none"),
            key=lambda f: f.get("height") or 0,
            default=None,
        )
        if best:
            cdn = best.get("url", cdn)

    headers = info.get("http_headers", {})
    logger.info("resolved video url for %s", url)
    return cdn, headers


async def stream_youtube_audio(player, chat_id: int, url: str) -> dict:
    cdn, headers = await resolve_audio(url)
    await player.play_url(
        chat_id,
        cdn,
        ffmpeg_params=_FFMPEG_RECONNECT,
        headers=headers,
    )
    logger.info("streaming youtube audio chat=%d", chat_id)
    return {"cdn": cdn, "headers": headers}


async def stream_youtube_video(player, chat_id: int, url: str) -> dict:
    cdn, headers = await resolve_video(url)
    await player.play_url(
        chat_id,
        cdn,
        ffmpeg_params=_FFMPEG_RECONNECT,
        headers=headers,
    )
    logger.info("streaming youtube video chat=%d", chat_id)
    return {"cdn": cdn, "headers": headers}


def is_youtube_url(url: str) -> bool:
    return any(
        h in url
        for h in ("youtube.com/watch", "youtu.be/", "youtube.com/shorts/")
    )
