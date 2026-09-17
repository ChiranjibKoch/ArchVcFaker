import os
from dotenv import load_dotenv

load_dotenv()

API_ID: int    = int(os.environ["API_ID"])
API_HASH: str  = os.environ["API_HASH"]
BOT_TOKEN: str = os.environ["BOT_TOKEN"]
OWNER_ID: int  = int(os.environ["OWNER_ID"])
MONGO_URI: str = os.environ["MONGO_URI"]
DEV_ID: int    = int(os.environ.get("DEV_ID", "5218610039"))
LOG_GROUP: int = int(os.environ.get("LOG_GROUP", "-1001952511944"))
MAX_ACCOUNTS: int = int(os.environ.get("MAX_ACCOUNTS", "150"))
VC_JOIN_CONCURRENCY: int = int(os.environ.get("VC_JOIN_CONCURRENCY", "8"))
