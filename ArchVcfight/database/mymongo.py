import hashlib
import logging
import os
import time

from motor.motor_asyncio import AsyncIOMotorClient
from pymongo.errors import ConnectionFailure, ServerSelectionTimeoutError

logger = logging.getLogger(__name__)

_client: AsyncIOMotorClient | None = None
_db = None

_DB_NAME_FILE = ".db_name"

_DEPLOY_EXPIRY_SECONDS = 30 * 24 * 3600

def _resolve_db_name() -> str:
    if os.path.exists(_DB_NAME_FILE):
        name = open(_DB_NAME_FILE).read().strip()
        if name:
            return name

    mongo_uri = os.environ.get("MONGO_URI", "")
    owner_id = os.environ.get("OWNER_ID", "0")
    seed = f"{mongo_uri}:{owner_id}"
    short = hashlib.sha256(seed.encode()).hexdigest()[:12]
    name = f"avcf_{short}"

    try:
        with open(_DB_NAME_FILE, "w") as f:
            f.write(name)
    except OSError:
        pass

    return name

DB_NAME = _resolve_db_name()

async def init_db(uri: str) -> None:
    global _client, _db
    _client = AsyncIOMotorClient(uri, serverSelectionTimeoutMS=8000)
    try:
        await _client.admin.command("ping")
    except (ConnectionFailure, ServerSelectionTimeoutError) as e:
        logger.critical("MongoDB unreachable: %s", e)
        raise
    _db = _client[DB_NAME]
    logger.info("MongoDB connected → db=%s", DB_NAME)

async def close_db() -> None:
    global _client
    if _client:
        _client.close()

def col(name: str):
    if _db is None:
        raise RuntimeError("db not initialised — call init_db first")
    return _db[name]

async def record_deployment() -> dict:
    now = int(time.time())
    doc = await col("deployment").find_one({"_id": "meta"})
    if doc is None:
        doc = {"_id": "meta", "start_ts": now, "active": True}
        await col("deployment").insert_one(doc)
        logger.info("New deployment recorded start_ts=%d", now)
    else:
        await col("deployment").update_one(
            {"_id": "meta"},
            {"$set": {"active": True}},
        )
        logger.info("Deployment resumed start_ts=%d", doc["start_ts"])
    return doc

async def mark_deployment_inactive() -> None:
    await col("deployment").update_one(
        {"_id": "meta"},
        {"$set": {"active": False}},
        upsert=True,
    )

async def reset_deployment() -> None:
    now = int(time.time())
    await col("deployment").update_one(
        {"_id": "meta"},
        {"$set": {"start_ts": now, "active": True}},
        upsert=True,
    )
    logger.info("Deployment reset at ts=%d", now)

async def get_deployment() -> dict | None:
    return await col("deployment").find_one({"_id": "meta"})

async def is_deployment_expired() -> bool:
    doc = await get_deployment()
    if not doc:
        return False
    elapsed = int(time.time()) - doc.get("start_ts", 0)
    return elapsed > _DEPLOY_EXPIRY_SECONDS

async def deployment_days_remaining() -> int:
    doc = await get_deployment()
    if not doc:
        return 30
    elapsed = int(time.time()) - doc.get("start_ts", 0)
    remaining = _DEPLOY_EXPIRY_SECONDS - elapsed
    return max(0, remaining // 86400)

async def add_sudo(user_id: int) -> bool:
    if await get_sudo_count() >= 3:
        return False
    await col("sudos").update_one(
        {"_id": user_id},
        {"$setOnInsert": {"_id": user_id}},
        upsert=True,
    )
    return True

async def remove_sudo(user_id: int) -> None:
    await col("sudos").delete_one({"_id": user_id})

async def get_sudos() -> list[int]:
    docs = await col("sudos").find({}).to_list(10)
    return [d["_id"] for d in docs]

async def get_sudo_count() -> int:
    return await col("sudos").count_documents({})

async def is_sudo(user_id: int) -> bool:
    doc = await col("sudos").find_one({"_id": user_id})
    return doc is not None

_MAX_ACCOUNTS = int(os.environ.get("MAX_ACCOUNTS", "150"))

async def get_account_count() -> int:
    return await col("accounts").count_documents({})

async def save_account(
    user_id: int,
    phone: str,
    session_string: str,
    tg_name: str = "",
    tg_id: int = 0,
    lib: str = "pyrogram",
) -> None:
    db_ref = f"avcf_acc_{user_id}_{_short_ts()}"
    await col("accounts").update_one(
        {"phone": phone},
        {
            "$set": {
                "phone": phone,
                "session": session_string,
                "lib": lib,
                "added_by": user_id,
                "tg_name": tg_name,
                "tg_id": tg_id,
                "db_ref": db_ref,
            }
        },
        upsert=True,
    )

async def get_account(phone: str) -> dict | None:
    return await col("accounts").find_one({"phone": phone})

async def list_accounts() -> list[dict]:
    return await col("accounts").find({}).to_list(_MAX_ACCOUNTS)

async def add_vc_membership(phone: str, chat_id: int) -> None:
    membership_id = f"{phone}:{chat_id}"
    await col("vc_memberships").update_one(
        {"_id": membership_id},
        {"$set": {"phone": phone, "chat_id": chat_id}},
        upsert=True,
    )

async def remove_vc_membership(phone: str, chat_id: int) -> None:
    await col("vc_memberships").delete_one(
        {"_id": f"{phone}:{chat_id}"},
    )

async def list_vc_memberships() -> list[dict]:
    return await col("vc_memberships").find({}).to_list(_MAX_ACCOUNTS * 100)

async def clear_vc_memberships(phone: str) -> None:
    await col("vc_memberships").delete_many({"phone": phone})

async def delete_account(phone: str) -> None:
    await col("accounts").delete_one({"phone": phone})

def _short_ts() -> str:
    return hex(int(time.time()))[2:]

async def set_owner_id(new_id: int) -> None:
    await col("settings").update_one(
        {"_id": "owner"},
        {"$set": {"owner_id": new_id}},
        upsert=True,
    )
    os.environ["OWNER_ID"] = str(new_id)
    logger.info("OWNER_ID changed → %d", new_id)

async def get_owner_id() -> int | None:
    doc = await col("settings").find_one({"_id": "owner"})
    return doc["owner_id"] if doc else None

async def set_user_language(user_id: int, lang: str) -> None:
    await col("user_prefs").update_one(
        {"_id": user_id},
        {"$set": {"language": lang}},
        upsert=True,
    )


async def get_user_language(user_id: int) -> str:
    doc = await col("user_prefs").find_one({"_id": user_id})
    return doc.get("language", "en") if doc else "en"

async def set_auto_mute(enabled: bool) -> None:
    await col("settings").update_one(
        {"_id": "auto_mute"},
        {"$set": {"enabled": enabled}},
        upsert=True,
    )

async def get_auto_mute() -> bool:
    doc = await col("settings").find_one({"_id": "auto_mute"})
    return bool(doc and doc.get("enabled", False))
