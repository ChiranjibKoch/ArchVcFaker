package modules

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tgmultibot/database"

	"github.com/amarnathcjd/gogram/telegram"
)

type autoPanel string

const (
	panelReact autoPanel = "react"
	panelView  autoPanel = "view"
)

var (
	panelMu    sync.RWMutex
	userPanels = make(map[int64]autoPanel)
)

func setUserPanel(userID int64, p autoPanel) {
	panelMu.Lock()
	userPanels[userID] = p
	panelMu.Unlock()
}

func getUserPanel(userID int64) autoPanel {
	panelMu.RLock()
	defer panelMu.RUnlock()
	return userPanels[userID]
}

type panelConfig struct {
	title      string
	actionWord string
	addFn      func(userID, chatID int64) error
	removeFn   func(userID, chatID int64) error
	listFn     func(userID int64) ([]int64, error)
}

var panelConfigs = map[autoPanel]panelConfig{
	panelReact: {
		title:      "🔁 Auto React",
		actionWord: "reactions",
		addFn:      database.AddAutoReactChat,
		removeFn:   database.RemoveAutoReactChat,
		listFn:     database.GetAutoReactChats,
	},
	panelView: {
		title:      "👁 Auto View",
		actionWord: "views",
		addFn:      database.AddAutoViewChat,
		removeFn:   database.RemoveAutoViewChat,
		listFn:     database.GetAutoViewChats,
	},
}

func panelKeyboardBuilder() *telegram.KeyboardBuilder {
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnPanelList), telegram.Button.Text(BtnPanelAdd))
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	return kb
}

func handleAutoReactPrompt(m *telegram.NewMessage) error {
	return openPanel(m, panelReact)
}

func handleAutoViewPrompt(m *telegram.NewMessage) error {
	return openPanel(m, panelView)
}

func openPanel(m *telegram.NewMessage, p autoPanel) error {
	userID := m.SenderID()
	setUserPanel(userID, p)
	cfg := panelConfigs[p]

	_, err := m.Reply(
		fmt.Sprintf(
			"<b>%s Panel</b>\n\n"+
				"📋 <b>List Chats</b> — view chats with auto-%s enabled, and remove any of them\n"+
				"➕ <b>Add Chat</b> — enable auto-%s in a new chat",
			cfg.title, cfg.actionWord, cfg.actionWord,
		),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: panelKeyboardBuilder().BuildReply(ropts)},
	)
	return err
}

func handlePanelListPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	p := getUserPanel(userID)
	if p == "" {
		p = panelReact
		setUserPanel(userID, p)
	}
	cfg := panelConfigs[p]

	// Auto-react/view chats are always keyed by userID (personal).
	chats, err := cfg.listFn(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("%s listFn error for user %d: %v", cfg.title, userID, err))
		_, err := m.Reply(
			"❌ <b>Failed to load chats, please try again.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: panelKeyboardBuilder().BuildReply(ropts)},
		)
		return err
	}

	text, kb := buildAutoChatListPage(p, chats)
	_, err = m.Reply(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
	return err
}

func buildAutoChatListPage(p autoPanel, chats []int64) (string, *telegram.KeyboardBuilder) {
	cfg := panelConfigs[p]
	kb := telegram.NewKeyboard()

	if len(chats) == 0 {
		kb.AddRow(telegram.Button.Data("— no chats added yet —", "autochat:noop"))
		return fmt.Sprintf(
			"📋 <b>%s — Chats</b>\n\nNo chats added yet. Tap <b>➕ Add Chat</b> to add one.",
			cfg.title,
		), kb
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 <b>%s — Chats</b> — <code>%d</code> total\n\n", cfg.title, len(chats)))
	for i, chatID := range chats {
		sb.WriteString(fmt.Sprintf("%d. <code>%d</code>\n", i+1, chatID))
		kb.AddRow(telegram.Button.Data(
			fmt.Sprintf("🗑 Remove #%d", i+1),
			fmt.Sprintf("autochat:%s:remove:%d", p, chatID),
		))
	}
	return sb.String(), kb
}

func handleAutoChatCallback(cb *telegram.CallbackQuery) error {
	data := cb.DataString()
	if !strings.HasPrefix(data, "autochat:") {
		return nil
	}

	if data == "autochat:noop" {
		_, err := cb.Answer("")
		return err
	}

	parts := strings.SplitN(strings.TrimPrefix(data, "autochat:"), ":", 3)
	if len(parts) != 3 || parts[1] != "remove" {
		return nil
	}

	p := autoPanel(parts[0])
	cfg, ok := panelConfigs[p]
	if !ok {
		return nil
	}

	var chatID int64
	if _, err := fmt.Sscanf(parts[2], "%d", &chatID); err != nil {
		_, err := cb.Answer("Invalid chat.")
		return err
	}

	userID := cb.GetSenderID()
	if err := cfg.removeFn(userID, chatID); err != nil {
		sendTraceback(fmt.Sprintf("%s removeFn error for user %d chat %d: %v", cfg.title, userID, chatID, err))
		_, err := cb.Answer("Failed to remove, try again.")
		return err
	}

	chats, err := cfg.listFn(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("%s listFn error for user %d: %v", cfg.title, userID, err))
	}

	text, kb := buildAutoChatListPage(p, chats)
	if _, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}); err != nil {
		return err
	}
	_, err = cb.Answer("Removed.")
	return err
}

func handlePanelAddPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID) // clients to join with
	p := getUserPanel(userID)
	if p == "" {
		p = panelReact
		setUserPanel(userID, p)
	}
	cfg := panelConfigs[p]

	if cm.Count(ownerID) == 0 {
		_, err := m.Reply(
			"❌ <b>No clients found.</b>\n\nTap <b>➕ Add Client</b> to add one first.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: panelKeyboardBuilder().BuildReply(ropts)},
		)
		return err
	}

	ctx, ok := guardProcessWithKeyboard(m, cfg.title+" — Add Chat", panelKeyboardBuilder())
	if !ok {
		return nil
	}
	cg.SetListening(userID)

	_, err := m.Reply(
		fmt.Sprintf(
			"<b>%s — Add Chat</b>\n\n"+
				"Send the <b>username</b> or <b>invite link</b> of the group/channel.\n\n"+
				"• Public: <code>@username</code> or <code>https://t.me/username</code>\n"+
				"• Private: <code>https://t.me/+InviteHash</code>\n\n"+
				"All your clients will join it now.\n\n"+
				"<i>Send /cancel to abort.</i>",
			cfg.title,
		),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: panelKeyboardBuilder().BuildReply(ropts)},
	)
	if err != nil {
		ps.done(userID)
		return err
	}

	go autoAddJoinStep(ctx, userID, ownerID, p)
	return nil
}

func autoAddJoinStep(ctx context.Context, userID, ownerID int64, p autoPanel) {
	cfg := panelConfigs[p]

	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in autoAddJoinStep (%s): %v", cfg.title, r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	panelOpts := &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: panelKeyboardBuilder().BuildReply(ropts)}

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID, "⏰ <b>Timed out.</b> Tap <b>➕ Add Chat</b> to try again.", panelOpts)
		}
		return
	}
	if ctx.Err() != nil {
		return
	}

	text := strings.TrimSpace(msgs[0].Text())
	if text == "" {
		c.SendMessage(userID, "📎 <b>Please send a username or invite link, not a file.</b>", panelOpts)
		return
	}

	prog, _ := c.SendMessage(userID,
		fmt.Sprintf("⏳ <b>Joining with %d client(s)...</b>", cm.Count(ownerID)),
		&telegram.SendOptions{ParseMode: telegram.HTML},
	)
	if ctx.Err() != nil {
		if prog != nil {
			prog.Edit("🚫 <b>Cancelled before joining.</b>", &telegram.SendOptions{ParseMode: telegram.HTML})
		}
		return
	}

	results := cm.JoinChannel(ctx, ownerID, text)

	var chatID int64
	joined, pending, failed := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Channel != nil:
			joined++
			if chatID == 0 {
				chatID = r.Channel.ID
			}
		case r.InviteRequestSent:
			pending++
		default:
			failed++
		}
	}

	if chatID == 0 {
		var result string
		if pending > 0 {
			result = fmt.Sprintf(
				"⏳ <b>Join request sent.</b>\n\n🕓 <b>Pending approval:</b> %d client(s)\n❌ <b>Failed:</b> %d client(s)\n\n"+
					"An admin needs to approve the request before the chat can be added. Try <b>➕ Add Chat</b> again only after it's approved.",
				pending, failed,
			)
		} else {
			result = fmt.Sprintf(
				"❌ <b>Could not join with any client.</b>\n\n❌ <b>Failed:</b> %d client(s)\n\nAborting — chat not added.",
				failed,
			)
		}
		if prog != nil {
			prog.Edit(result, panelOpts)
		} else {
			c.SendMessage(userID, result, panelOpts)
		}
		return
	}

	// Auto-react/view chats are stored under userID (personal), not ownerID.
	if err := cfg.addFn(userID, chatID); err != nil {
		sendTraceback(fmt.Sprintf("%s addFn error for user %d chat %d: %v", cfg.title, userID, chatID, err))
		result := "❌ <b>Joined but failed to save, please try again.</b>"
		if prog != nil {
			prog.Edit(result, panelOpts)
		} else {
			c.SendMessage(userID, result, panelOpts)
		}
		return
	}

	result := fmt.Sprintf(
		"✅ <b>%s enabled!</b>\n\n➕ <b>Joined:</b> %d client(s)\n🕓 <b>Pending approval:</b> %d client(s)\n❌ <b>Failed:</b> %d client(s)\n\n"+
			"🆔 <b>Chat ID:</b> <code>%d</code>\n\nEvery new message there will now get automatic %s from all your clients.",
		cfg.title, joined, pending, failed, chatID, cfg.actionWord,
	)
	if prog != nil {
		prog.Edit(result, panelOpts)
	} else {
		c.SendMessage(userID, result, panelOpts)
	}
}
