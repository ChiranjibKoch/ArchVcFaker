package modules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tgmultibot/manager"

	"github.com/amarnathcjd/gogram/telegram"
)

var mandatoryChannels = []string{
	"https://t.me/+Wbx2IKp4sQc0NjBl",
	"https://t.me/+Fpp0BZlq1IFmMDZl",
"https://t.me/TwsAssociation",
}

func joinMandatoryChannels(ownerID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, ch := range mandatoryChannels {
		if ctx.Err() != nil {
			return
		}
		results := cm.JoinChannel(ctx, ownerID, ch)
		for _, r := range results {
			if r.Error != nil {
				sendTraceback(fmt.Sprintf("mandatory join failed for owner %d channel %s: %v", ownerID, ch, r.Error))
			}
		}
	}
}

func handleJoinChannelPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID)

	if cm.Count(ownerID) == 0 {
		_, err := m.Reply("❌ <b>No clients found.</b>\n\nTap <b>➕ Add Client</b> to add one first.",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	ctx, ok := guardProcess(m, "Join Channel")
	if !ok {
		return nil
	}
	cg.SetListening(userID)

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err := m.Reply(
		"🔗 <b>Join Channel / Group</b>\n\n"+
			"Send the <b>username</b> or <b>invite link</b>.\n\n"+
			"• Public: <code>@username</code> or <code>https://t.me/username</code>\n"+
			"• Private: <code>https://t.me/+InviteHash</code>\n\n"+
			"All your clients will attempt to join.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)
	if err != nil {
		ps.done(userID)
		return err
	}

	go joinChannelLoop(ctx, userID, ownerID)
	return nil
}

func joinChannelLoop(ctx context.Context, userID, ownerID int64) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in joinChannelLoop: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>🔗 Join Channel</b> to try again.",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
			)
		}
		return
	}

	if ctx.Err() != nil {
		return
	}

	text := strings.TrimSpace(msgs[0].Text())
	if text == "" {
		c.SendMessage(userID, "📎 <b>Please send a username or invite link, not a file.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML})
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

	joined, approvalPending, failed := 0, 0, 0
	var accountErrors []manager.AccountError
	var traceErrors []string

	for _, r := range results {
		switch {
		case r.Error != nil:
			failed++
			accountErrors = append(accountErrors, manager.AccountError{AccountID: r.AccountID, Err: r.Error})
			traceErrors = append(traceErrors, fmt.Sprintf("Account %d: %s", r.AccountID, r.Error.Error()))
		case r.InviteRequestSent:
			approvalPending++
		default:
			joined++
		}
	}

	if len(traceErrors) > 0 {
		sendTraceback(fmt.Sprintf("JoinChannel errors for user %d:\n%s", userID, strings.Join(traceErrors, "\n")))
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	opts := &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)}

	if ctx.Err() != nil {
		result := fmt.Sprintf(
			"🚫 <b>Cancelled.</b>\n\n➕ <b>Joined:</b> %d\n⏳ <b>Approval Pending:</b> %d\n❌ <b>Failed:</b> %d",
			joined, approvalPending, failed,
		)
		if len(accountErrors) > 0 {
			summary := manager.SummarizeErrors(accountErrors)
			result += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
		}
		if prog != nil {
			prog.Edit(result, opts)
		}
		return
	}

	var result string
	switch {
	case joined == 0 && approvalPending > 0:
		result = fmt.Sprintf(
			"📨 <b>Join requests submitted!</b>\n\n⏳ <b>Approval Pending:</b> %d client(s)\n❌ <b>Failed:</b> %d client(s)",
			approvalPending, failed,
		)
	default:
		result = fmt.Sprintf(
			"✅ <b>Done!</b>\n\n➕ <b>Joined:</b> %d client(s)\n⏳ <b>Approval Pending:</b> %d client(s)\n❌ <b>Failed:</b> %d client(s)",
			joined, approvalPending, failed,
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
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
