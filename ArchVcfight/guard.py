import time
import logging
from functools import wraps

from pyrogram import Client, filters
from pyrogram.types import Message, CallbackQuery

from config.config import OWNER_ID, DEV_ID
from ArchVcfight.database.mymongo import is_deployment_expired, deployment_days_remaining

logger = logging.getLogger("guard")

_EXPIRED_MSG = (
    "⚠️ **Your plan has expired.**\n\n"
    "Please contact your bot developer to renew your subscription."
)

_deploy_expired_cache: bool | None = None
_cache_ts: float = 0.0
_CACHE_TTL = 300.0


async def _check_expired() -> bool:
    global _deploy_expired_cache, _cache_ts
    now = time.time()
    if _deploy_expired_cache is not None and (now - _cache_ts) < _CACHE_TTL:
        return _deploy_expired_cache
    result = await is_deployment_expired()
    _deploy_expired_cache = result
    _cache_ts = now
    return result


def invalidate_expiry_cache() -> None:
    global _deploy_expired_cache
    _deploy_expired_cache = None


def deployment_guard(func):
    # Deployment expiry checks are disabled for this deployment.
    return func


def cb_deployment_guard(func):
    # Deployment expiry checks are disabled for this deployment.
    return func
