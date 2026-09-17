package modules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tgmultibot/manager"

	"github.com/amarnathcjd/gogram/telegram"
)

func handleReactPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID)

	if cm.Count(ownerID) == 0 {
		_, err := m.Reply(
			"❌ <b>No clients found.</b>\n\nTap <b>➕ Add Client</b> to add one first.",
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
		return err
	}

	ctx, ok := guardProcess(m, "Send Reaction")
	if !ok {
		return nil
	}
	cg.SetListening(userID)

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err := m.Reply(
		"❣️ <b>Send Reactions</b>\n\n"+
			"Send me the <b>message link</b> you want to react to.\n\n"+
			"<i>Examples:</i>\n"+
			"• <code>https://t.me/username/123</code>\n"+
			"• <code>https://t.me/c/1234567890/123</code> (private channel)\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{
			ParseMode:   telegram.HTML,
			ReplyMarkup: kb.BuildReply(ropts),
		},
	)
	if err != nil {
		ps.done(userID)
		return err
	}

	go reactLoop(ctx, userID, ownerID)
	return nil
}

func reactLoop(ctx context.Context, userID, ownerID int64) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in reactLoop: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	for {
		if ctx.Err() != nil {
			return
		}

		msgs, ok := cg.Recv(userID, 5*time.Minute)
		if !ok {
			if ctx.Err() == nil && cg.IsListening(userID) {
				c.SendMessage(userID,
					"⏰ <b>Timed out.</b> Tap <b>❣️ Send Reaction</b> to try again.",
					&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
				)
			}
			return
		}

		if ctx.Err() != nil {
			return
		}

		msg := msgs[0]
		link := strings.TrimSpace(msg.Text())
		if link == "" {
			c.SendMessage(userID, "📎 <b>Please send a message link, not a file.</b>",
				&telegram.SendOptions{ParseMode: telegram.HTML})
			cg.SetListening(userID)
			continue
		}

		peer, msgID, err := resolveMsgLink(link)
		if err != nil {
			c.SendMessage(userID,
				"❌ <b>Invalid link.</b>\n\n"+
					"Expected formats:\n"+
					"• <code>https://t.me/username/123</code>\n"+
					"• <code>https://t.me/c/1234567890/123</code>",
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			cg.SetListening(userID)
			continue
		}

		if ctx.Err() != nil {
			return
		}

		targetMsg, err := c.GetMessageByID(peer, int32(msgID))
		if err != nil || targetMsg == nil {
			c.SendMessage(userID,
				"❌ <b>Could not fetch that message.</b>\n\n"+
					"Make sure the bot has access to that channel/group.",
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			cg.SetListening(userID)
			continue
		}

		prog, _ := c.SendMessage(userID,
			fmt.Sprintf("⏳ <b>Sending reactions from %d client(s)...</b>", cm.Count(ownerID)),
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)

		errs := cm.SendReactions(ctx, ownerID, peer, int32(msgID))

		kb := telegram.NewKeyboard()
		kb.AddRow(telegram.Button.Text(BtnBackToMenu))
		opts := &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)}

		if ctx.Err() != nil {
			cancelled := cm.Count(ownerID) - len(errs)
			result := fmt.Sprintf(
				"🚫 <b>Cancelled.</b>\n\n❣️ <b>Reacted before cancel:</b> %d\n❌ <b>Failed:</b> %d",
				cancelled, len(errs),
			)
			if prog != nil {
				prog.Edit(result, opts)
			} else {
				c.SendMessage(userID, result, opts)
			}
			return
		}

		total := cm.Count(ownerID)
		failed := len(errs)
		done := total - failed

		result := fmt.Sprintf(
			"✅ <b>Done!</b>\n\n❣️ <b>Reacted:</b> %d client(s)\n❌ <b>Failed:</b> %d client(s)",
			done, failed,
		)

		if len(errs) > 0 {
			var accountErrors []manager.AccountError
			for _, e := range errs {
				accountErrors = append(accountErrors, manager.AccountError{Err: e})
			}
			summary := manager.SummarizeErrors(accountErrors)
			result += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
		}

		if prog != nil {
			prog.Edit(result, opts)
		} else {
			c.SendMessage(userID, result, opts)
		}

		c.SendMessage(userID,
			"🔁 <b>Send another message link</b> to react again, or tap <b>🔙 Back to Menu</b> to stop.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
		)
		cg.SetListening(userID)
	}
}

func resolveMsgLink(link string) (any, int, error) {
	link = strings.TrimPrefix(link, "https://")
	link = strings.TrimPrefix(link, "http://")
	link = strings.TrimPrefix(link, "t.me/")
	parts := strings.Split(strings.Trim(link, "/"), "/")

	switch {
	case len(parts) == 3 && parts[0] == "c":
		var chanID int64
		if _, err := fmt.Sscanf(parts[1], "%d", &chanID); err != nil {
			return nil, 0, fmt.Errorf("invalid channel id")
		}
		var msgID int
		if _, err := fmt.Sscanf(parts[2], "%d", &msgID); err != nil {
			return nil, 0, fmt.Errorf("invalid msg id")
		}
		return chanIDToPeer(chanID), msgID, nil

	case len(parts) == 2:
		var msgID int
		if _, err := fmt.Sscanf(parts[1], "%d", &msgID); err != nil {
			return nil, 0, fmt.Errorf("invalid msg id")
		}
		return parts[0], msgID, nil

	default:
		return nil, 0, fmt.Errorf("unrecognised link format")
	}
}

func chanIDToPeer(id int64) int64 {
	var result int64
	s := fmt.Sprintf("-100%d", id)
	fmt.Sscanf(s, "%d", &result)
	return result
}
