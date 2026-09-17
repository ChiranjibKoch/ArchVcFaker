package modules

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tgmultibot/manager"

	"github.com/amarnathcjd/gogram/telegram"
)

func handleJoinVoiceChatPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID)

	if cm.Count(ownerID) == 0 {
		_, err := m.Reply("❌ <b>No clients found.</b>\n\nTap <b>➕ Add Client</b> to add one first.",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	ctx, ok := guardProcess(m, "Join Voice Chat")
	if !ok {
		return nil
	}
	cg.SetListening(userID)

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err := m.Reply(
		"🎙 <b>Join Voice Chat</b>\n\n"+
			"Send the <b>username</b> or <b>Chat ID</b>.\n\n"+
			"• Public: <code>@username</code> or <code>https://t.me/username</code>\n"+
			"• Private: <b>Chat ID</b> (e.g. <code>-1001234567890</code>) or <code>https://t.me/c/1234567890</code>\n\n"+
			"⚠️ <b>For private groups/channels:</b> use <b>🔗 Join Channel</b> first, then come here with the Chat ID.\n"+
			"⚠️ <b>Invite links (<code>t.me/+...</code>) are not supported here</b> — use the Chat ID instead.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)
	if err != nil {
		ps.done(userID)
		return err
	}

	go vcLinkStep(ctx, userID, ownerID)
	return nil
}

func vcLinkStep(ctx context.Context, userID, ownerID int64) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in vcLinkStep: %v", r))
			ps.done(userID)
			cg.StopListening(userID)
		}
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>🎙 Join Voice Chat</b> to try again.",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
			)
		}
		ps.done(userID)
		cg.StopListening(userID)
		return
	}

	if ctx.Err() != nil {
		cg.StopListening(userID)
		return
	}

	text := strings.TrimSpace(msgs[0].Text())
	if text == "" {
		c.SendMessage(userID, "📎 <b>Please send a username or Chat ID, not a file.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		ps.done(userID)
		cg.StopListening(userID)
		return
	}

	chatID, err := resolveVCPeer(text)
	if err != nil {
		c.SendMessage(userID,
			fmt.Sprintf("❌ <b>%s</b>", escapeHTML(err.Error())),
			&telegram.SendOptions{ParseMode: telegram.HTML})
		ps.done(userID)
		cg.StopListening(userID)
		return
	}

	cg.SetListening(userID)
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	c.SendMessage(userID,
		"⏱ <b>How long should clients stay in the voice chat?</b>\n\n"+
			"Send duration in <b>seconds</b>.\n"+
			"Send <code>0</code> to stay indefinitely.\n\n"+
			"<i>Examples: 300 = 5 min | 3600 = 1 hr | 0 = forever</i>\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)

	go vcDurationStep(ctx, userID, ownerID, chatID)
}

func vcDurationStep(ctx context.Context, userID, ownerID int64, chatID any) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in vcDurationStep: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>🎙 Join Voice Chat</b> to try again.",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
			)
		}
		return
	}

	if ctx.Err() != nil {
		return
	}

	text := strings.TrimSpace(msgs[0].Text())
	idleSec := 0
	if text != "0" && text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			c.SendMessage(userID,
				"❌ <b>Invalid duration.</b> Send seconds (e.g. <code>300</code>) or <code>0</code> for indefinite.",
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			return
		}
		idleSec = n
	}

	if ctx.Err() != nil {
		return
	}

	total := cm.Count(ownerID)
	prog, _ := c.SendMessage(userID,
		fmt.Sprintf("⏳ <b>Joining voice chat with %d client(s)...</b>", total),
		&telegram.SendOptions{ParseMode: telegram.HTML},
	)

	results := cm.JoinVoiceChat(ctx, ownerID, chatID, idleSec)

	joined, failed := 0, 0
	var accountErrors []manager.AccountError
	for _, r := range results {
		if r.NoActiveGroupCall {
			msg := "❌ <b>No active voice chat found in that channel.</b>\n\n" +
				"Start a voice chat there first, then try again."
			if prog != nil {
				prog.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML})
			} else {
				c.SendMessage(userID, msg, &telegram.SendOptions{ParseMode: telegram.HTML})
			}
			return
		}

		if r.WrongPeerType {
			msg := "❌ <b>This appears to be a user account, not a group or channel.</b>\n\n" +
				"Voice chats are only available in groups or channels. Please provide a group/channel username or Chat ID instead of a personal account."
			if prog != nil {
				prog.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML})
			} else {
				c.SendMessage(userID, msg, &telegram.SendOptions{ParseMode: telegram.HTML})
			}
			return
		}

		if r.Err != nil {
			if r.Err == context.Canceled {
				continue
			}
			failed++
			accountErrors = append(accountErrors, manager.AccountError{AccountID: r.AccountID, Err: r.Err})
			sendTraceback(fmt.Sprintf("JoinVoiceChat error for user %d, account %d: %v", userID, r.AccountID, r.Err))
		} else {
			joined++
		}
	}

	durationStr := "indefinitely"
	if idleSec > 0 {
		durationStr = formatDuration(idleSec)
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnLeaveVoiceChat))
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	opts := &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)}

	if ctx.Err() != nil {
		var result string
		if joined == 0 {
			result = "🚫 <b>Cancelled.</b>\n\nNo clients had joined yet."
		} else {
			result = fmt.Sprintf(
				"🚫 <b>Cancelled mid-join.</b>\n\n"+
					"✅ <b>Already joined:</b> %d client(s)\n"+
					"⏭ <b>Skipped (not started):</b> %d client(s)\n\n"+
					"⚠️ The %d joined client(s) are <b>still in the voice chat</b>.\n"+
					"Tap <b>🔇 Leave Voice Chat</b> to remove them.",
				joined, total-joined-failed, joined,
			)
		}
		if len(accountErrors) > 0 {
			summary := manager.SummarizeErrors(accountErrors)
			result += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
		}
		if prog != nil {
			prog.Edit(result, opts)
		} else {
			c.SendMessage(userID, result, opts)
		}
		return
	}

	result := fmt.Sprintf(
		"✅ <b>Done!</b>\n\n🎙 <b>Joined:</b> %d client(s)\n❌ <b>Failed:</b> %d client(s)\n⏱ <b>Duration:</b> %s",
		joined, failed, durationStr,
	)
	if len(accountErrors) > 0 {
		summary := manager.SummarizeErrors(accountErrors)
		result += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
	}

	if prog != nil {
		prog.Edit(result, opts)
	} else {
		c.SendMessage(userID, result, opts)
	}
}

func handleLeaveVoiceChat(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID)

	active := cm.ActiveVoiceChats(ownerID)
	if len(active) == 0 {
		_, err := m.Reply("ℹ️ <b>No active voice chats found.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	errs := cm.LeaveVoiceChat(ownerID)
	left := len(active) - len(errs)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔇 <b>Left voice chat.</b>\n\n✅ <b>Left:</b> %d\n❌ <b>Failed:</b> %d", left, len(errs)))
	if len(errs) > 0 {
		var accountErrors []manager.AccountError
		for _, e := range errs {
			accountErrors = append(accountErrors, manager.AccountError{AccountID: 0, Err: e})
		}
		summary := manager.SummarizeErrors(accountErrors)
		sb.WriteString("\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>"))
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	_, err := m.Reply(sb.String(), &telegram.SendOptions{
		ParseMode:   telegram.HTML,
		ReplyMarkup: kb.BuildReply(ropts),
	})
	return err
}

func resolveVCPeer(input string) (any, error) {
	input = strings.TrimSpace(input)
	clean := input
	clean = strings.TrimPrefix(clean, "https://")
	clean = strings.TrimPrefix(clean, "http://")
	clean = strings.TrimPrefix(clean, "t.me/")
	clean = strings.Trim(clean, "/")

	lower := strings.ToLower(clean)
	if strings.HasPrefix(clean, "+") || strings.HasPrefix(lower, "joinchat/") {
		return nil, fmt.Errorf(
			"invite links aren't supported here — use 🔗 Join Channel first, " +
				"then come back with the Chat ID (e.g. -1001234567890)",
		)
	}

	if strings.HasPrefix(clean, "c/") {
		rest := strings.SplitN(strings.TrimPrefix(clean, "c/"), "/", 2)[0]
		var bareID int64
		if _, err := fmt.Sscanf(rest, "%d", &bareID); err != nil {
			return nil, fmt.Errorf("invalid channel id in link")
		}
		return chanIDToPeer(bareID), nil
	}

	if strings.HasPrefix(clean, "@") {
		return strings.TrimPrefix(clean, "@"), nil
	}

	if id, err := strconv.ParseInt(clean, 10, 64); err == nil {
		return id, nil
	}

	return clean, nil
}

func formatDuration(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%d second(s)", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%d minute(s)", sec/60)
	}
	return fmt.Sprintf("%dh %dm", sec/3600, (sec%3600)/60)
}
