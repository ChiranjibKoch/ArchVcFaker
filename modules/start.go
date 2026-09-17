package modules

import (
	"tgmultibot/database"

	"github.com/amarnathcjd/gogram/telegram"
)

func handleStart(m *telegram.NewMessage) error {
	userID := m.SenderID()

	ps.cancel(userID)

	go database.AddServedUser(userID)

	text := `👋 <b>Welcome to Client Manager Bot!</b>

Manage multiple Telegram accounts and run bulk actions across all of them.

<b>Features:</b>
<blockquote>• Add unlimited Telegram clients via Telegram session files
• View, manage and remove clients anytime
• Send reactions to any message with all clients simultaneously
• Join channels / groups with all clients at once
• Join and leave voice chats with all clients
• Auto-react and auto-view new messages in selected chats
• Share access with others via access tokens (delegated mode)</blockquote>

<b>Getting started:</b>
<blockquote>1. Tap <b>➕ Add Client</b> and send a <code>.session</code> file
2. Use <b>📋 My Clients</b> to view or remove connected accounts
3. Use <b>🔄 Switch Access</b> to act as a delegated grantor's clients</blockquote>

<i>Send /cancel anytime to stop the current process.</i>`

	_, err := m.Reply(text, &telegram.SendOptions{
		ParseMode:   telegram.HTML,
		ReplyMarkup: buildMenuKB(userID),
	})
	return err
}
