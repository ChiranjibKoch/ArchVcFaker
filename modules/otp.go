package modules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

const otpTimeout = 5 * time.Minute

func handleOTPCallback(cb *telegram.CallbackQuery, userID, accountID int64) error {
	mc := cm.GetClientByID(userID, accountID)
	if mc == nil {
		_, err := cb.Answer("❌ Client not found.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	ctx, ok := ps.tryStart(userID, fmt.Sprintf("Get OTP [%d]", accountID))
	if !ok {
		existing := ps.current(userID)
		_, err := cb.Answer(
			fmt.Sprintf("Process already running: %s\nSend /cancel to stop it.", existing),
			&telegram.CallbackOptions{Alert: true},
		)
		return err
	}

	me := mc.Client.Me()
	phone := "—"
	if me != nil && me.Phone != "" {
		phone = "+" + me.Phone
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err := cb.Edit(
		fmt.Sprintf(
			"📲 <b>OTP Listener Active</b>\n\n"+
				"📞 <b>Phone:</b> <code>%s</code>\n"+
				"🆔 <b>Account:</b> <code>%d</code>\n\n"+
				"Login to Telegram on this number now.\n"+
				"I will send you the OTP as soon as it arrives.\n\n"+
				"⏳ <i>Waiting for 5 minutes...</i>\n"+
				"<i>Send /cancel to abort.</i>",
			phone, accountID,
		),
		&telegram.SendOptions{
			ParseMode:   telegram.HTML,
			ReplyMarkup: kb.BuildReply(ropts),
		},
	)
	if err != nil {
		ps.done(userID)
		return err
	}

	go listenForOTP(ctx, userID, accountID, phone)
	return nil
}

func listenForOTP(ctx context.Context, userID, accountID int64, phone string) {
	defer ps.done(userID)

	mc := cm.GetClientByID(userID, accountID)
	if mc == nil {
		kb := telegram.NewKeyboard()
		kb.AddRow(telegram.Button.Text(BtnBackToMenu))
		c.SendMessage(userID,
			"❌ <b>OTP Error:</b> Client disconnected.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
		)
		return
	}

	done := make(chan string, 1)

	hn := mc.Client.On(telegram.OnMessage, func(m *telegram.NewMessage) error {
		if m.SenderID() != 777000 {
			return nil
		}
		text := m.Text()
		if text == "" {
			return nil
		}
		select {
		case done <- text:
		default:
		}
		return nil
	})

	opts := &telegram.SendOptions{ParseMode: telegram.HTML,                 ReplyMarkup: buildMenuKB(userID)}

	select {
	case msg := <-done:
		mc.Client.RemoveHandle(hn)
		otp := extractOTP(msg)
		c.SendMessage(userID,
			fmt.Sprintf(
				"✅ <b>OTP Received!</b>\n\n"+
					"📞 <b>Phone:</b> <code>%s</code>\n"+
					"🆔 <b>Account:</b> <code>%d</code>\n\n"+
					"🔑 <b>OTP:</b> <code>%s</code>\n\n"+
					"<i>Full message:</i>\n<blockquote>%s</blockquote>",
				phone, accountID, otp, escapeHTML(msg),
			),
			opts,
		)

	case <-ctx.Done():
		mc.Client.RemoveHandle(hn)

	case <-time.After(otpTimeout):
		mc.Client.RemoveHandle(hn)
		c.SendMessage(userID,
			fmt.Sprintf(
				"⏰ <b>OTP Timeout</b>\n\n"+
					"📞 <b>Phone:</b> <code>%s</code>\n\n"+
					"No OTP received within 5 minutes.\n"+
					"Tap <b>📲 Get OTP</b> to try again.",
				phone,
			),
			opts,
		)
	}
}

func extractOTP(msg string) string {
	words := strings.Fields(msg)
	for _, w := range words {
		clean := strings.Trim(w, ".,;:!?\"'()")
		clean = strings.ReplaceAll(clean, "-", "")
		if len(clean) >= 5 {
			allDigits := true
			for _, ch := range clean {
				if ch < '0' || ch > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return clean
			}
		}
	}
	return msg
}
