import asyncio
import random
from collections import deque
from enum import Enum, auto


class Loop(Enum):
    OFF = auto()
    TRACK = auto()
    QUEUE = auto()


class Queue:
    __slots__ = ("items", "current", "loop", "volume", "bass", "treble", "pitch", "_lock")

    def __init__(self):
        self.items: deque[str] = deque()
        self.current: str | None = None
        self.loop = Loop.OFF
        self.volume = 100
        self.bass = 15
        self.treble = 15
        self.pitch = 0
        self._lock = asyncio.Lock()

    async def push(self, url: str) -> None:
        async with self._lock:
            self.items.append(url)

    async def pop(self) -> str | None:
        async with self._lock:
            if self.loop == Loop.TRACK and self.current:
                return self.current
            if not self.items:
                return None
            track = self.items.popleft()
            if self.loop == Loop.QUEUE:
                self.items.append(track)
            self.current = track
            return track

    async def clear(self) -> None:
        async with self._lock:
            self.items.clear()
            self.current = None

    async def shuffle(self) -> None:
        async with self._lock:
            lst = list(self.items)
            random.shuffle(lst)
            self.items = deque(lst)

    async def list(self) -> list[str]:
        async with self._lock:
            return list(self.items)

    def cycle_loop(self) -> Loop:
        modes = [Loop.OFF, Loop.TRACK, Loop.QUEUE]
        self.loop = modes[(modes.index(self.loop) + 1) % len(modes)]
        return self.loop


_queues: dict[int, Queue] = {}


def get(chat_id: int) -> Queue:
    if chat_id not in _queues:
        _queues[chat_id] = Queue()
    return _queues[chat_id]
