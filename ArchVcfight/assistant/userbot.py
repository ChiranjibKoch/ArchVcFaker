import asyncio
import atexit
import logging
import os
import random
import signal
import time

from pyrogram import Client
from pyrogram.errors import FloodWait
from ntgcalls import MediaSource
from pytgcalls import PyTgCalls
from pytgcalls import filters as fl
from pytgcalls.types import (
    AudioQuality,
    Device,
    Direction,
    MediaStream,
    VideoQuality,
)
from pytgcalls.types.raw import AudioParameters, AudioStream, Stream
from pytgcalls.exceptions import NoActiveGroupCall, NotInCallError

from config.config import API_ID, API_HASH, LOG_GROUP
from ArchVcfight.database.mymongo import (
    list_accounts,
    add_vc_membership,
    remove_vc_membership,
    list_vc_memberships,
)
from ArchVcfight.assistant import queue as Q

logger = logging.getLogger("userbot")

_JOIN_CONCURRENCY = max(1, int(os.environ.get("VC_JOIN_CONCURRENCY", "8")))
_JOIN_STAGGER_MIN = float(os.environ.get("VC_JOIN_STAGGER_MIN", "0.3"))
_JOIN_STAGGER_MAX = float(os.environ.get("VC_JOIN_STAGGER_MAX", "1.5"))
_JOIN_MAX_RETRIES = int(os.environ.get("VC_JOIN_MAX_RETRIES", "2"))
_HEALTH_CHECK_INTERVAL = int(os.environ.get("VC_HEALTH_CHECK_INTERVAL", "60"))

_AUTO_MUTE_ENABLED = False
_AUTO_MUTE_DELAY = float(os.environ.get("VC_AUTO_MUTE_DELAY", "0.5"))


def set_auto_mute(enabled: bool, delay: float = 0.5) -> None:
    global _AUTO_MUTE_ENABLED, _AUTO_MUTE_DELAY
    _AUTO_MUTE_ENABLED = enabled
    _AUTO_MUTE_DELAY = delay


class Session:
    __slots__ = (
        "session_string", "account_phone", "client", "pycall", "user_id",
        "name", "username", "_running", "_joined", "_lock", "_health_task",
    )

    def __init__(self, session_string: str, account_phone: str):
        self.session_string = session_string
        self.account_phone = account_phone
        self.client: Client | None = None
        self.pycall: PyTgCalls | None = None
        self.user_id: int | None = None
        self.name = ""
        self.username = ""
        self._running = False
        self._joined: set[int] = set()
        self._lock = asyncio.Lock()
        self._health_task: asyncio.Task | None = None

    async def start(self) -> None:
        self.client = Client(
            f"session_{self.account_phone}",
            session_string=self.session_string,
            api_id=API_ID,
            api_hash=API_HASH,
        )
        self.pycall = PyTgCalls(self.client)
        await asyncio.wait_for(self.client.start(), 30)
        await asyncio.wait_for(self.pycall.start(), 20)
        me = await self.client.get_me()
        self.user_id = me.id
        self.name = f"{me.first_name or ''} {me.last_name or ''}".strip() or "Unknown"
        self.username = me.username or ""
        self._running = True
        self._health_task = asyncio.create_task(self._health_check_loop())
        logger.info("account started phone=%s id=%s", self.account_phone, self.user_id)

    async def stop(self) -> None:
        if not self._running:
            return
        self._running = False
        if self._health_task:
            self._health_task.cancel()
            self._health_task = None
        for chat_id in list(self._joined):
            try:
                await self.pycall.leave_call(chat_id)
            except Exception:
                pass
        self._joined.clear()
        try:
            await self.pycall.stop()
        except Exception:
            pass
        try:
            await self.client.stop()
        except Exception:
            pass

    async def _health_check_loop(self) -> None:
        while self._running:
            await asyncio.sleep(_HEALTH_CHECK_INTERVAL)
            if not self._running:
                break
            for chat_id in list(self._joined):
                try:
                    await self.pycall.play(chat_id)
                except Exception as e:
                    logger.warning(
                        "reconnect check failed phone=%s chat=%d: %s",
                        self.account_phone, chat_id, e,
                    )
                    await _log_error(
                        f"🔴 {self.account_phone} lost connection to chat {chat_id} "
                        f"and failed to rejoin: {e}"
                    )

    async def join_vc(self, chat_id: int, auto_mute: bool | None = None) -> bool:
        async with self._lock:
            if chat_id in self._joined:
                return False
            await self.pycall.play(chat_id)
            self._joined.add(chat_id)
            await add_vc_membership(self.account_phone, chat_id)
        should_mute = _AUTO_MUTE_ENABLED if auto_mute is None else auto_mute
        if should_mute:
            await asyncio.sleep(_AUTO_MUTE_DELAY)
            try:
                await self.pycall.mute(chat_id)
            except Exception as e:
                logger.warning(
                    "auto-mute failed phone=%s chat=%d: %s",
                    self.account_phone, chat_id, e,
                )
        return True

    async def leave_vc(self, chat_id: int, persist: bool = True) -> bool:
        async with self._lock:
            try:
                await self.pycall.leave_call(chat_id)
            except (NoActiveGroupCall, NotInCallError):
                self._joined.discard(chat_id)
                if persist:
                    await remove_vc_membership(self.account_phone, chat_id)
                return False
            self._joined.discard(chat_id)
            if persist:
                await remove_vc_membership(self.account_phone, chat_id)
            return True

    def joined(self, chat_id: int) -> bool:
        return chat_id in self._joined

    def joined_chats(self) -> set[int]:
        return set(self._joined)

    async def mute_vc(self, chat_id: int) -> bool:
        async with self._lock:
            if chat_id not in self._joined:
                return False
            await self.pycall.mute(chat_id)
            return True

    async def unmute_vc(self, chat_id: int) -> bool:
        async with self._lock:
            if chat_id not in self._joined:
                return False
            await self.pycall.unmute(chat_id)
            return True


_sessions: dict[str, Session] = {}
player = None


async def _log_error(message: str) -> None:
    logger.error(message)
    if not LOG_GROUP:
        return
    for session in _sessions.values():
        if session._running and session.client:
            try:
                await session.client.send_message(LOG_GROUP, message)
            except Exception as e:
                logger.error("failed to deliver log message to group: %s", e)
            return


def get_all_sessions() -> dict[str, Session]:
    return _sessions


def resolve_session(identifier: str) -> Session | None:
    if identifier in _sessions:
        return _sessions[identifier]
    value = identifier.strip()
    for session in _sessions.values():
        if str(session.user_id) == value:
            return session
        if session.username and session.username.lower().lstrip("@") == value.lower().lstrip("@"):
            return session
    return None


async def start_account(acc: dict) -> Session:
    session = Session(acc["session"], acc["phone"])
    await session.start()
    _sessions[acc["phone"]] = session
    global player
    if player is None:
        player = MediaPlayer(session.pycall)
        await player.start()
    return session


def _resolve_sessions(identifiers: list[str] | None) -> list[Session]:
    sessions = list(_sessions.values())
    if identifiers:
        selected = []
        for item in identifiers:
            s = resolve_session(item)
            if s and s not in selected:
                selected.append(s)
        sessions = selected
    return sessions


async def join_accounts(
    chat_id: int,
    identifiers: list[str] | None = None,
    auto_mute: bool | None = None,
) -> dict:
    sessions = _resolve_sessions(identifiers)
    sem = asyncio.Semaphore(_JOIN_CONCURRENCY)
    results = {"joined": [], "already": [], "failed": []}

    async def one(session: Session):
        async with sem:
            await asyncio.sleep(random.uniform(_JOIN_STAGGER_MIN, _JOIN_STAGGER_MAX))
            attempt = 0
            while True:
                try:
                    if session.joined(chat_id):
                        results["already"].append(session.account_phone)
                        return
                    ok = await session.join_vc(chat_id, auto_mute=auto_mute)
                    (results["joined"] if ok else results["already"]).append(session.account_phone)
                    return
                except FloodWait as e:
                    attempt += 1
                    if attempt > _JOIN_MAX_RETRIES:
                        msg = f"FloodWait {e.value}s exceeded retries"
                        results["failed"].append((session.account_phone, msg))
                        await _log_error(
                            f"🔴 {session.account_phone} failed to join chat {chat_id}: {msg}"
                        )
                        return
                    await asyncio.sleep(e.value + 1)
                except Exception as e:
                    results["failed"].append((session.account_phone, str(e)))
                    return

    await asyncio.gather(*(one(s) for s in sessions))
    return results


async def get_vc_status(chat_id: int) -> dict:
    joined = []
    not_joined = []
    for phone, session in _sessions.items():
        if session.joined(chat_id):
            joined.append(phone)
        else:
            not_joined.append(phone)
    return {
        "total_accounts": len(_sessions),
        "joined_count": len(joined),
        "not_joined_count": len(not_joined),
        "joined": joined,
        "not_joined": not_joined,
    }


async def _set_mute_accounts(chat_id: int, muted: bool, identifiers: list[str] | None = None) -> dict:
    sessions = _resolve_sessions(identifiers)
    sem = asyncio.Semaphore(_JOIN_CONCURRENCY)
    results = {"changed": [], "not_joined": [], "failed": []}

    async def one(session: Session):
        async with sem:
            try:
                ok = await (session.mute_vc(chat_id) if muted else session.unmute_vc(chat_id))
                (results["changed"] if ok else results["not_joined"]).append(session.account_phone)
            except Exception as e:
                results["failed"].append((session.account_phone, str(e)))

    await asyncio.gather(*(one(s) for s in sessions))
    return results


async def mute_accounts(chat_id: int, identifiers: list[str] | None = None) -> dict:
    return await _set_mute_accounts(chat_id, True, identifiers)


async def unmute_accounts(chat_id: int, identifiers: list[str] | None = None) -> dict:
    return await _set_mute_accounts(chat_id, False, identifiers)


async def leave_accounts(chat_id: int, identifiers: list[str] | None = None) -> dict:
    sessions = _resolve_sessions(identifiers)
    sem = asyncio.Semaphore(_JOIN_CONCURRENCY)
    results = {"left": [], "not_joined": [], "failed": []}

    async def one(session: Session):
        async with sem:
            try:
                ok = await session.leave_vc(chat_id)
                (results["left"] if ok else results["not_joined"]).append(session.account_phone)
            except Exception as e:
                results["failed"].append((session.account_phone, str(e)))

    await asyncio.gather(*(one(s) for s in sessions))
    return results


async def start() -> None:
    accounts = await list_accounts()
    if not accounts:
        logger.error("no user accounts in DB")
        return

    sem = asyncio.Semaphore(_JOIN_CONCURRENCY)

    async def boot(acc):
        async with sem:
            try:
                await start_account(acc)
            except Exception as e:
                logger.warning("failed to start account %s: %s", acc.get("phone"), e)
                await _log_error(f"🔴 Account {acc.get('phone')} failed to start: {e}")

    await asyncio.gather(*(boot(acc) for acc in accounts))
    if not _sessions:
        logger.error("all accounts failed to start")
        return
    if len(_sessions) < len(accounts):
        await _log_error(
            f"⚠️ {len(accounts) - len(_sessions)}/{len(accounts)} accounts failed to start."
        )

    memberships = await list_vc_memberships()
    by_phone: dict[str, list[int]] = {}
    for item in memberships:
        by_phone.setdefault(item["phone"], []).append(int(item["chat_id"]))

    async def restore(session: Session):
        async with sem:
            for chat_id in by_phone.get(session.account_phone, []):
                try:
                    await session.join_vc(chat_id)
                except Exception as e:
                    logger.warning(
                        "restore failed phone=%s chat=%d: %s",
                        session.account_phone, chat_id, e,
                    )

    await asyncio.gather(*(restore(s) for s in _sessions.values()))
    logger.info("account manager ready: %d accounts", len(_sessions))


async def stop() -> None:
    global player
    if player:
        await player.stop()
        player = None
    await asyncio.gather(*(s.stop() for s in list(_sessions.values())), return_exceptions=True)
    _sessions.clear()


class _StreamControl:

    def __init__(self):
        self._streams: dict[int, dict] = {}

    async def set(self, chat_id: int, pycall: PyTgCalls, started_at: float) -> None:
        self._streams[chat_id] = {
            "pycall": pycall,
            "started_at": started_at,
            "paused_at": None,
        }

    async def remove(self, chat_id: int) -> None:
        self._streams.pop(chat_id, None)

    async def pause(self, chat_id: int) -> bool:
        stream = self._streams.get(chat_id)
        if not stream or stream["paused_at"] is not None:
            return False
        await stream["pycall"].pause(chat_id)
        stream["paused_at"] = time.time()
        return True

    async def resume(self, chat_id: int) -> bool:
        stream = self._streams.get(chat_id)
        if not stream or stream["paused_at"] is None:
            return False
        await stream["pycall"].resume(chat_id)
        paused_for = time.time() - stream["paused_at"]
        stream["started_at"] += paused_for
        stream["paused_at"] = None
        return True

    async def seek(self, chat_id: int, pos: float) -> bool:
        return False

    async def progress(self, chat_id: int) -> float:
        stream = self._streams.get(chat_id)
        if not stream:
            return 0.0
        end = stream["paused_at"] or time.time()
        return max(0.0, end - stream["started_at"])


class MediaPlayer:

    def __init__(self, pycall: PyTgCalls):
        self.pycall = pycall
        self.ctrl = _StreamControl()
        self._procs: dict[int, asyncio.subprocess.Process] = {}
        self._fifos: dict[int, str] = {}

    async def start(self) -> None:
        self._register_handlers()
        atexit.register(self._cleanup_sync)

    def _register_handlers(self) -> None:
        @self.pycall.on_update(fl.stream_end())
        async def _on_end(_, upd):
            chat_id = upd.chat_id
            await self.ctrl.remove(chat_id)
            await self._kill(chat_id)
            from ArchVcfight.assistant import queue as Q
            from ArchVcfight.platforms.youtube import stream_youtube_audio, is_youtube_url
            q = Q.get(chat_id)
            track = await q.pop()
            if not track:
                return
            try:
                if is_youtube_url(track):
                    await stream_youtube_audio(self, chat_id, track)
                elif track.startswith(("http://", "https://")):
                    await self.play_url(chat_id, track)
                else:
                    await self.play_file(chat_id, track)
            except Exception as e:
                logger.warning("auto-queue failed chat=%d: %s", chat_id, e)

    async def stop(self) -> None:
        pass

    async def play_url(
        self,
        chat_id: int,
        url: str,
        audio_q: AudioQuality = AudioQuality.HIGH,
        video_q: VideoQuality = VideoQuality.HD_720p,
        ffmpeg_params: str = "",
        headers: dict | None = None,
    ) -> None:
        kw: dict = {"audio_quality": audio_q, "video_quality": video_q}
        if ffmpeg_params:
            kw["ffmpeg_parameters"] = ffmpeg_params
        if headers:
            kw["headers"] = headers
        await self.pycall.play(chat_id, MediaStream(url, **kw))
        await self.ctrl.set(chat_id, self.pycall, time.time())

    async def play_file(self, chat_id: int, path: str) -> None:
        await self.pycall.play(chat_id, MediaStream(path, video_flags=MediaStream.Flags.IGNORE))
        await self.ctrl.set(chat_id, self.pycall, time.time())

    async def play_raw(self, chat_id: int, src: str) -> None:
        fifo = f"fifo_{chat_id}.raw"
        if os.path.exists(fifo):
            os.remove(fifo)
        os.mkfifo(fifo)
        self._fifos[chat_id] = fifo
        proc = await asyncio.create_subprocess_shell(
            f"ffmpeg -y -i {src} -f s16le -ac 1 -ar 48000 -acodec pcm_s16le {fifo}",
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.DEVNULL,
        )
        self._procs[chat_id] = proc
        while not os.path.exists(fifo):
            await asyncio.sleep(0.05)
        await self.pycall.play(
            chat_id,
            Stream(AudioStream(MediaSource.FILE, fifo, AudioParameters(bitrate=48000))),
        )
        await self.ctrl.set(chat_id, self.pycall, time.time())

    async def pause(self, chat_id: int) -> bool:
        return await self.ctrl.pause(chat_id)

    async def resume(self, chat_id: int) -> bool:
        return await self.ctrl.resume(chat_id)

    async def seek(self, chat_id: int, pos: float) -> bool:
        return await self.ctrl.seek(chat_id, pos)

    async def progress(self, chat_id: int) -> float:
        return await self.ctrl.progress(chat_id)

    async def leave(self, chat_id: int) -> None:
        await self.pycall.leave_call(chat_id)
        await self.ctrl.remove(chat_id)
        await self._kill(chat_id)

    async def _kill(self, chat_id: int) -> None:
        proc = self._procs.pop(chat_id, None)
        if proc:
            try:
                proc.send_signal(signal.SIGINT)
                await asyncio.wait_for(proc.wait(), 3.0)
            except Exception:
                proc.kill()
                await proc.wait()
        fifo = self._fifos.pop(chat_id, None)
        if fifo and os.path.exists(fifo):
            os.remove(fifo)

    def _cleanup_sync(self) -> None:
        for p in self._procs.values():
            try:
                p.send_signal(signal.SIGINT)
                p.wait(timeout=3)
            except Exception:
                p.kill()
        for f in self._fifos.values():
            if os.path.exists(f):
                os.remove(f)
