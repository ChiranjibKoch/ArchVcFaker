<h1 align="center">
  🎙️ ArchVcFight
</h1>

<p align="center">
  <b>A powerful Telegram Voice Chat Bridge Bot</b><br>
  Stream audio from YouTube & Telegram directly into Telegram Voice Chats using a userbot assistant.
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Python-3.11+-3776AB?style=for-the-badge&logo=python&logoColor=white" />
  <img src="https://img.shields.io/badge/Pyrogram-2.x-blue?style=for-the-badge&logo=telegram&logoColor=white" />
  <img src="https://img.shields.io/badge/MongoDB-Motor-47A248?style=for-the-badge&logo=mongodb&logoColor=white" />
  <img src="https://img.shields.io/badge/Deploy-Railway-0B0D0E?style=for-the-badge&logo=railway&logoColor=white" />
  <img src="https://img.shields.io/badge/Deploy-Heroku-430098?style=for-the-badge&logo=heroku&logoColor=white" />
</p>

---

## ✨ Features

- 🎵 **VC Streaming** — Play audio from YouTube URLs or Telegram audio/video files directly in Voice Chats
- 📡 **VC Bridge** — Bridge audio between two Telegram Voice Chats using a userbot assistant
- 📅 **Scheduler** — Schedule bridge sessions at a specific time automatically
- 🎙️ **Recording** — Record ongoing Voice Chat sessions
- 👤 **Account Management** — Add, remove, and manage multiple userbot accounts
- 📊 **Stats** — View bot usage and deployment statistics
- 🔐 **Deployment Guard** — License/plan expiry system that locks commands when a deployment expires
- 👑 **Owner Controls** — Transfer ownership, promote/demote admins, manage access
- 🔄 **Owner Persistence** — Owner ID changes survive restarts via MongoDB

---
*
## 🏗️ Project Structure


## Multi-account VC manager

The bridge/record forwarding system has been removed. Every logged-in account is an independent VC participant.

- `MAX_ACCOUNTS` defaults to `150`
- `VC_JOIN_CONCURRENCY` defaults to `8`
- `/addaccount +phone` — login an account
- `/listaccs` — list logged-in accounts
- `/join <chat_id>` — all logged-in accounts join
- `/join <chat_id> <phone|user_id|@username> ...` — selected accounts join
- `/leave <chat_id>` — all logged-in accounts leave
- `/leave <chat_id> <phone|user_id|@username> ...` — selected accounts leave
- `/leaveaccount <chat_id> <phone|user_id|@username>` — remove one specific account from a VC
- `/vcaccounts <chat_id>` — show accounts currently managed in that VC
- `/vcstatus` — show account/VC status

VC memberships are persisted in MongoDB and restored after restart.
