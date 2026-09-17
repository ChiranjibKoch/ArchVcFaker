package modules

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"

	"tgmultibot/config"
	"tgmultibot/database"

	"github.com/amarnathcjd/gogram/telegram"
)

const helpText = `🎛 <b>Bot Commands</b>

<b>General</b>
• <code>/start</code> — Show the main menu
• <code>/cancel</code> — Cancel the current process
• <code>/help</code> — Show this help

<b>Features (menu buttons)</b>
• ➕ <b>Add Client</b> — attach a client via phone number or session file
• 📋 <b>My Clients</b> — view/manage your attached clients
• ❣️ <b>Send Reaction</b> — send a reaction to posts
• 🔗 <b>Join Channel</b> — join a group or channel
• 🎙 <b>Join Voice Chat</b> — join a voice chat
• 🔇 <b>Leave Voice Chat</b> — leave all voice chats
• 🎵 <b>Player</b> — play audio/video in voice chats with pause, resume, mute, seek and stop controls
• 🔁 <b>Auto React</b> — auto-react to new posts
• 👁 <b>Auto View</b> — auto-view posts
• 🔑 <b>Access</b> — request bot access, get/enter access tokens, manage delegates
• 🔄 <b>Switch Access</b> — act as another user's clients

<b>Owner only</b>
• <code>/approved</code> — List approved users
• <code>/approve &lt;user&gt;</code> — Add an approved user
• <code>/disapprove &lt;user&gt;</code> — Remove an approved user

<i>&lt;user&gt; = Telegram ID, @username or t.me link. If omitted, reply to that user's message instead.</i>`

// ownerWrap guards a command so it is only handled for the bot owner; everyone
// else is silently ignored. It also recovers from panics and logs tracebacks.
func ownerWrap(fn func(*telegram.NewMessage) error) func(*telegram.NewMessage) error {
	return func(m *telegram.NewMessage) error {
		if !m.IsPrivate() {
			return nil
		}
		if config.Owner == nil || m.SenderID() != config.Owner.ID {
			return nil
		}
		defer func() {
			if r := recover(); r != nil {
				trace := fmt.Sprintf("panic in owner handler: %v\n%s", r, debug.Stack())
				sendTraceback(trace)
			}
		}()
		if err := fn(m); err != nil && err != telegram.ErrEndGroup {
			sendTraceback(fmt.Sprintf("error in owner handler: %v\n%s", err, debug.Stack()))
		}
		return nil
	}
}

func handleHelp(m *telegram.NewMessage) error {
	_, err := m.Reply(helpText, &telegram.SendOptions{ParseMode: telegram.HTML})
	return err
}

// handleApprovedUsers lists every approved user, resolving each one so a
// username/mention is shown alongside the ID when possible.
func handleApprovedUsers(m *telegram.NewMessage) error {
	users, err := database.ListAuthorizedUsers()
	if err != nil {
		sendTraceback(fmt.Sprintf("ListAuthorizedUsers: %v", err))
		_, err := m.Reply("❌ <b>Failed to fetch approved users.</b>", &telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	if len(users) == 0 {
		_, err := m.Reply(
			"✅ <b>Approved Users:</b> 0\n\nNo one is approved yet.\n\n➕ <i>Add:</i> <code>/approve &lt;user&gt;</code>",
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
		return err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>Approved Users:</b> %d\n\n", len(users)))
	for _, userID := range users {
		sb.WriteString(userLine(userID) + "\n")
	}
	sb.WriteString("\n➕ <i>Add:</i> <code>/approve &lt;user&gt;</code>\n")
	sb.WriteString("➖ <i>Remove:</i> <code>/disapprove &lt;user&gt;</code>")

	_, err = m.Reply(sb.String(), &telegram.SendOptions{ParseMode: telegram.HTML})
	return err
}

// userLine renders one approved user. When the user resolves successfully a
// mention/username is shown alongside the ID; otherwise only the raw ID.
func userLine(userID int64) string {
	user, err := c.GetUser(userID)
	if err != nil || user == nil {
		return fmt.Sprintf("👤 <code>%d</code>", userID)
	}

	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = fmt.Sprintf("User %d", userID)
	}

	if user.Username != "" {
		return fmt.Sprintf("👤 @%s — <code>%d</code>", escapeHTML(user.Username), userID)
	}
	return fmt.Sprintf("👤 <a href=\"tg://user?id=%d\">%s</a> — <code>%d</code>", userID, escapeHTML(name), userID)
}

func handleApproveUser(m *telegram.NewMessage) error {
	targetID, err := ownerTargetFromMessage(m)
	if err != nil {
		_, err := m.Reply(
			fmt.Sprintf("⚠️ <b>%s</b>\n\n<i>Usage:</i> <code>/approve &lt;user&gt;</code>\n<i>or reply to that user's message.</i>", escapeHTML(err.Error())),
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
		return err
	}

	if config.Owner != nil && targetID == config.Owner.ID {
		_, err := m.Reply("👑 <b>The owner is already authorized by default.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	ok, err := database.IsAuthorized(targetID)
	if err == nil && ok {
		_, err := m.Reply(fmt.Sprintf("ℹ️ <b>Already approved:</b> %s", userLine(targetID)),
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	if err := database.AddAuthorizedUser(targetID); err != nil {
		sendTraceback(fmt.Sprintf("AddAuthorizedUser(%d): %v", targetID, err))
		_, err := m.Reply("❌ <b>Failed to approve, try again.</b>", &telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	_, err = m.Reply(fmt.Sprintf("✅ <b>Approved:</b> %s\n\nThey can now send /start to use the bot.",
		userLine(targetID)), &telegram.SendOptions{ParseMode: telegram.HTML})
	return err
}

func handleDisapproveUser(m *telegram.NewMessage) error {
	targetID, err := ownerTargetFromMessage(m)
	if err != nil {
		_, err := m.Reply(
			fmt.Sprintf("⚠️ <b>%s</b>\n\n<i>Usage:</i> <code>/disapprove &lt;user&gt;</code>\n<i>or reply to that user's message.</i>", escapeHTML(err.Error())),
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
		return err
	}

	if config.Owner != nil && targetID == config.Owner.ID {
		_, err := m.Reply("👑 <b>You can't remove the owner.</b>", &telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	ok, err := database.IsAuthorized(targetID)
	if err == nil && !ok {
		_, err := m.Reply(fmt.Sprintf("ℹ️ <b>Not an approved user:</b> %s", userLine(targetID)),
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	if err := database.RemoveAuthorizedUser(targetID); err != nil {
		sendTraceback(fmt.Sprintf("RemoveAuthorizedUser(%d): %v", targetID, err))
		_, err := m.Reply("❌ <b>Failed to remove, try again.</b>", &telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	_, err = m.Reply(fmt.Sprintf("✅ <b>Removed:</b> %s\n\nThey no longer have access to the bot.",
		userLine(targetID)), &telegram.SendOptions{ParseMode: telegram.HTML})
	return err
}

// ownerTargetFromMessage resolves the target user for the owner commands.
//   - With an argument: a numeric ID is used directly, otherwise it is resolved
//     via ResolvePeer (supports @username / username / t.me links).
//   - Without an argument: the sender of the replied-to message is used.
func ownerTargetFromMessage(m *telegram.NewMessage) (int64, error) {
	raw := strings.TrimSpace(m.Text())
	raw = strings.TrimPrefix(raw, "/")
	fields := strings.Fields(raw)

	var arg string
	if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}

	if arg == "" {
		replyID := m.ReplyToMsgID()
		if replyID == 0 {
			return 0, errors.New("no target found")
		}
		msgs, err := c.GetMessages(m.ChatID(), &telegram.SearchOption{IDs: []int32{replyID}})
		if err != nil || len(msgs) == 0 {
			return 0, errors.New("could not fetch the replied message")
		}
		senderID := msgs[0].SenderID()
		if senderID == 0 {
			return 0, errors.New("could not determine the replied message's sender")
		}
		return senderID, nil
	}

	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return id, nil
	}

	clean := strings.TrimSpace(arg)
	clean = strings.TrimPrefix(clean, "https://t.me/")
	clean = strings.TrimPrefix(clean, "http://t.me/")
	clean = strings.TrimPrefix(clean, "@")
	clean = strings.Trim(clean, "/")

	peer, err := c.ResolvePeer(clean)
	if err != nil {
		return 0, fmt.Errorf("could not resolve %q", arg)
	}
	switch p := peer.(type) {
	case *telegram.InputPeerUser:
		return p.UserID, nil
	case *telegram.InputPeerChat:
		return -p.ChatID, nil
	case *telegram.InputPeerChannel:
		return -1000000000000 - p.ChannelID, nil
	}
	return 0, fmt.Errorf("could not resolve %q", arg)
}
