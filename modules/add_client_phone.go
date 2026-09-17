package modules

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"tgmultibot/config"

	"github.com/amarnathcjd/gogram/telegram"
)

var otpRunRE = regexp.MustCompile(`\d{5,6}`)

// addClientMethodKB builds the reply keyboard shown after tapping Add Client.
func addClientMethodKB() *telegram.KeyboardBuilder {
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnAddByPhone))
	kb.AddRow(telegram.Button.Text(BtnUploadSession))
	kb.AddRow(telegram.Button.Text(BtnAddClientCancel))
	return kb
}

func buildAddClientBackKB() telegram.ReplyMarkup {
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	return kb.BuildReply(ropts)
}

// handleAddClientBack is fired when the user taps 🔙 Back while waiting for a
// session file / phone number / OTP. It stops the listener (via the media
// collector) and returns to the main menu.
func handleAddClientBack(m *telegram.NewMessage) error {
	return handleStart(m)
}

func handleAddClientCancel(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ps.done(userID)
	_, err := m.Reply(
		"🚫 <b>Add Client cancelled.</b>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)},
	)
	return err
}

func handleAddByPhonePrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	if !isPersonalCtx(userID) {
		_, err := m.Reply(
			"❌ <b>Add Client is only available in Personal context.</b>\n\n"+
				"Tap <b>🔄 Switch Access → ✅ Personal</b> to switch back.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)},
		)
		return err
	}
	ctx, ok := guardProcess(m, "Add Client")
	if !ok {
		return nil
	}

	cg.SetListening(userID)

	_, err := m.Reply(
		"📱 <b>Add by Phone Number</b>\n\nFollow the steps below — check your messages.",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildAddClientBackKB()},
	)
	if err != nil {
		ps.done(userID)
		cg.StopListening(userID)
		return err
	}

	go phoneLoginLoop(ctx, userID)
	return nil
}

func handleUploadSessionPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	if !isPersonalCtx(userID) {
		_, err := m.Reply(
			"❌ <b>Add Client is only available in Personal context.</b>\n\n"+
				"Tap <b>🔄 Switch Access → ✅ Personal</b> to switch back.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)},
		)
		return err
	}
	ctx, ok := guardProcess(m, "Add Client")
	if !ok {
		return nil
	}

	cg.SetListening(userID)

	_, err := m.Reply(
		"📂 <b>Upload Session</b>\n\n"+
			"Send your Telegram session file(s):\n\n"+
			"• Single <code>.session</code> file\n"+
			"• A <code>.zip</code> containing multiple <code>.session</code> files\n"+
			"• Multiple <code>.session</code> files at once (as an album)\n\n"+
			"<i>Tap 🔙 Back to cancel, or send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildAddClientBackKB()},
	)
	if err != nil {
		ps.done(userID)
		cg.StopListening(userID)
		return err
	}

	go addClientLoop(ctx, userID)
	return nil
}

// phoneLoginLoop drives the add-by-phone-number flow: phone → send code → OTP
// (digits extracted from any mixed text) → optional 2FA → save session to DB.
func phoneLoginLoop(ctx context.Context, userID int64) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in phoneLoginLoop: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	phone, ok := askText(ctx, userID,
		"📱 <b>Add by Phone Number</b>\n\nSend the Telegram account phone number with country code, e.g. <code>+1234567890</code>.\n\n<i>Tap 🔙 Back to cancel, or send /cancel to abort.</i>",
		isValidPhone,
		"❌ Invalid phone number. Send it with the country code, e.g. <code>+1234567890</code>.")
	if !ok {
		return
	}

	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:         int32(config.ApiID),
		AppHash:       config.ApiHash,
		MemorySession: true,
	})
	if err != nil {
		sendPhoneError(userID, "create client", err)
		return
	}
	if err := client.Connect(); err != nil {
		client.Stop()
		sendPhoneError(userID, "connect", err)
		return
	}

	hash, err := client.SendCode(phone)
	if err != nil {
		client.Stop()
		sendPhoneError(userID, "send code", err)
		return
	}

	otpMsg, ok := askText(ctx, userID,
		fmt.Sprintf("🔑 <b>OTP sent to <code>%s</code></b>\n\nSend the login code. You can paste the full message — I'll extract the digits automatically.\n\n<i>Tap 🔙 Back to cancel, or send /cancel to abort.</i>", phone),
		func(s string) bool { return extractOTPDigits(s) != "" },
		"❌ No code detected. Send the OTP (digits only, or paste the full message).")
	if !ok {
		client.Stop()
		return
	}
	otp := extractOTPDigits(otpMsg)

	auth, err := client.AuthSignIn(phone, hash, otp, nil)
	switch {
	case err != nil && telegram.MatchError(err, "SESSION_PASSWORD_NEEDED"):
		password, ok := askText(ctx, userID,
			"🔐 <b>2FA is enabled</b> for this account.\n\nSend the 2FA password.\n\n<i>Tap 🔙 Back to cancel, or send /cancel to abort.</i>",
			func(s string) bool { return strings.TrimSpace(s) != "" },
			"❌ Password cannot be empty.")
		if !ok {
			client.Stop()
			return
		}
		accPassword, gErr := client.AccountGetPassword()
		if gErr != nil {
			client.Stop()
			sendPhoneError(userID, "fetch 2FA settings", gErr)
			return
		}
		inputPassword, cErr := telegram.GetInputCheckPassword(password, accPassword)
		if cErr != nil {
			client.Stop()
			sendPhoneError(userID, "compute 2FA password", cErr)
			return
		}
		if _, cpErr := client.AuthCheckPassword(inputPassword); cpErr != nil {
			client.Stop()
			sendPhoneError(userID, "2FA password", cpErr)
			return
		}
	case err != nil:
		client.Stop()
		sendPhoneError(userID, "login", err)
		return
	default:
		if _, signUp := auth.(*telegram.AuthAuthorizationSignUpRequired); signUp {
			client.Stop()
			c.SendMessage(userID,
				"❌ <b>This number is not registered on Telegram.</b>",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
			return
		}
	}

	sess := client.ExportSession()
	client.Stop()

	mc, err := cm.AddClient(userID, sess)
	if err != nil {
		sendTraceback(fmt.Sprintf("phoneLoginLoop AddClient error for user %d: %v", userID, err))
		c.SendMessage(userID,
			"❌ <b>Failed to save account.</b>\n\n<pre>"+escapeHTML(err.Error())+"</pre>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
		return
	}

	me := mc.Client.Me()
	name := "Unknown"
	if me != nil {
		name = strings.TrimSpace(me.FirstName + " " + me.LastName)
	}
	c.SendMessage(userID, fmt.Sprintf(
		"✅ <b>Account added via Phone Number!</b>\n\n"+
			"👤 <b>Name:</b> %s\n"+
			"📞 <b>Phone:</b> <code>+%s</code>\n"+
			"🆔 <b>ID:</b> <code>%d</code>",
		escapeHTML(name), me.Phone, me.ID,
	), &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})

	go joinMandatoryChannels(userID)
}

// askText asks the user for a text input, re-prompting up to maxAttempts times
// until validate() passes. Returns false on timeout / back button / /cancel.
func askText(ctx context.Context, userID int64, prompt string, validate func(string) bool, invalidMsg string) (string, bool) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return "", false
		}
		c.SendMessage(userID, prompt,
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildAddClientBackKB()})

		msg, ok := recvText(ctx, userID)
		if !ok {
			return "", false
		}
		if msg == "" || (validate != nil && !validate(msg)) {
			if attempt < maxAttempts {
				c.SendMessage(userID,
					fmt.Sprintf("%s\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
						invalidMsg, attempt, maxAttempts, maxAttempts-attempt),
					&telegram.SendOptions{ParseMode: telegram.HTML})
			} else {
				c.SendMessage(userID,
					"🚫 <b>Too many invalid attempts.</b> Tap <b>➕ Add Client</b> to start over.",
					&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
			}
			continue
		}
		return msg, true
	}
	return "", false
}

// recvText waits for the next non-empty text message. Returns false on
// timeout, back button, /cancel or context cancellation.
func recvText(ctx context.Context, userID int64) (string, bool) {
	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>➕ Add Client</b> to try again.",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
		}
		return "", false
	}
	for _, m := range msgs {
		if t := strings.TrimSpace(m.Text()); t != "" {
			return t, true
		}
	}
	return "", false
}

func isValidPhone(s string) bool {
	digits := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 8 && digits <= 15
}

// extractOTPDigits pulls the login code out of a message that may mix digits
// and letters (e.g. "18 BCD 2 3 5 3 C ZF 6"). It prefers a contiguous 5-6
// digit run, otherwise falls back to concatenating every digit found.
func extractOTPDigits(s string) string {
	if m := otpRunRE.FindString(s); m != "" {
		return m
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sendPhoneError(userID int64, stage string, err error) {
	sendTraceback(fmt.Sprintf("phone login (%s) error for user %d: %v", stage, userID, err))
	c.SendMessage(userID,
		fmt.Sprintf("❌ <b>Login failed (%s):</b>\n\n<pre>%s</pre>", stage, escapeHTML(err.Error())),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
}
