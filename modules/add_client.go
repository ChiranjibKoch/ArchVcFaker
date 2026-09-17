package modules

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tgmultibot/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

const maxAttempts = 3

func handleAddClientPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()

	if !isPersonalCtx(userID) {
		_, err := m.Reply(
			"❌ <b>Add Client is only available in Personal context.</b>\n\n"+
				"Tap <b>🔄 Switch Access → ✅ Personal</b> to switch back.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)},
		)
		return err
	}

	if existing := ps.current(userID); existing != "" {
		_, err := m.Reply(
			fmt.Sprintf(
				"⚙️ <b>Process already running:</b> <code>%s</code>\n\nSend /cancel to stop it first.",
				existing,
			),
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)},
		)
		return err
	}

	_, err := m.Reply(
		"➕ <b>Add Clients</b>\n\n"+
			"Choose how to add a Telegram account:\n\n"+
			"• 📱 <b>Add by Phone Number</b> — log in with phone + OTP\n"+
			"• 📤 <b>Upload Session</b> — send a <code>.session</code> file",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: addClientMethodKB().BuildReply(ropts)},
	)
	return err
}

func addClientLoop(ctx context.Context, userID int64) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in addClientLoop: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}

		msgs, ok := cg.Recv(userID, 5*time.Minute)
		if !ok {
			if ctx.Err() == nil && cg.IsListening(userID) {
				c.SendMessage(userID,
					"⏰ <b>Timed out.</b> Tap <b>➕ Add Client</b> to try again.",
					&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)},
				)
			}
			return
		}

		if ctx.Err() != nil {
			return
		}

		warn := validateMessages(msgs, attempt)
		if warn != "" {
			c.SendMessage(userID, warn, &telegram.SendOptions{ParseMode: telegram.HTML})
			if attempt == maxAttempts {
				c.SendMessage(userID,
					"🚫 <b>Too many invalid attempts.</b> Tap <b>➕ Add Client</b> to start over.",
					&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
				)
			} else {
				cg.SetListening(userID)
			}
			continue
		}

		if err := processSessionFiles(ctx, userID, msgs); err != nil {
			sendTraceback(fmt.Sprintf("processSessionFiles error for user %d: %v", userID, err))
		}
		return
	}
}

func processSessionFiles(ctx context.Context, userID int64, msgs []*telegram.NewMessage) error {
	type sessionEntry struct {
		name string
		data []byte
	}
	var sessionFiles []sessionEntry
	var invalid []string

	for _, m := range msgs {
		if m.File == nil {
			continue
		}
		fname := strings.ToLower(m.File.Name)
		ext := strings.ToLower(m.File.Ext)

		if isMediaExt(ext) {
			invalid = append(invalid, m.File.Name)
			continue
		}
		if !strings.HasSuffix(fname, ".session") && !strings.HasSuffix(fname, ".zip") {
			invalid = append(invalid, m.File.Name)
			continue
		}

		var buf bytes.Buffer
		if _, err := m.Download(&telegram.DownloadOptions{Buffer: &buf}); err != nil {
			return fmt.Errorf("download failed for %s: %w", m.File.Name, err)
		}
		data := buf.Bytes()

		if strings.HasSuffix(fname, ".zip") {
			extracted, err := extractSessionsFromZip(data)
			if err != nil {
				return fmt.Errorf("zip extract failed: %w", err)
			}
			for _, e := range extracted {
				sessionFiles = append(sessionFiles, sessionEntry{e.name, e.data})
			}
		} else {
			base := strings.TrimSuffix(filepath.Base(fname), ".session")
			sessionFiles = append(sessionFiles, sessionEntry{base, data})
		}
	}

	if len(invalid) > 0 {
		c.SendMessage(userID,
			fmt.Sprintf("⚠️ <b>Skipped %d invalid file(s):</b> <code>%s</code>",
				len(invalid), strings.Join(invalid, ", ")),
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
	}

	if len(sessionFiles) == 0 {
		c.SendMessage(userID, "❌ <b>No valid session files found.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return nil
	}

	total := len(sessionFiles)
	prog, _ := c.SendMessage(userID,
		fmt.Sprintf("⏳ <b>Processing %d session(s)...</b>", total),
		&telegram.SendOptions{ParseMode: telegram.HTML},
	)

	added, failed, skipped := 0, 0, 0
	var errLines []string

	for i, sf := range sessionFiles {
		if ctx.Err() != nil {
			skipped = total - i
			break
		}

		sessStr, err := utils.ConvertSessionFile(sf.data, sf.name)
		if err != nil {
			errLines = append(errLines,
				fmt.Sprintf("• <code>%s</code>: convert failed — %s", sf.name, escapeHTML(err.Error())))
			sendTraceback(fmt.Sprintf("convert failed for user %d (%s): %v", userID, sf.name, err))
			failed++
			continue
		}

		if ctx.Err() != nil {
			skipped = total - i
			break
		}

		mc, err := cm.AddClient(userID, sessStr)
		if err != nil {
			errLines = append(errLines,
				fmt.Sprintf("• <code>%s</code>: start failed — %s", sf.name, escapeHTML(err.Error())))
			sendTraceback(fmt.Sprintf("AddClient start failed for user %d (%s): %v", userID, sf.name, err))
			failed++
			continue
		}

		if mc.Client.Me() == nil {
			errLines = append(errLines,
				fmt.Sprintf("• <code>%s</code>: GetMe failed — could not fetch account info", sf.name))
			sendTraceback(fmt.Sprintf("GetMe failed for user %d (%s): Me() returned nil", userID, sf.name))
			failed++
			continue
		}

		added++

		if prog != nil && (i+1)%10 == 0 {
			prog.Edit(
				fmt.Sprintf("⏳ <b>Processing... %d/%d done</b> ✅ %d added, ❌ %d failed",
					i+1, total, added, failed),
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
		}
	}

	result := fmt.Sprintf(
		"✅ <b>Done!</b>\n\n➕ <b>Added:</b> %d\n❌ <b>Failed:</b> %d\n📊 <b>Total clients:</b> %d",
		added, failed, cm.Count(userID),
	)
	if skipped > 0 {
		result += fmt.Sprintf("\n⏭ <b>Skipped (cancelled):</b> %d", skipped)
	}
	if len(errLines) > 0 {
		result += "\n\n<b>Errors:</b>\n" + strings.Join(errLines, "\n")
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	opts := &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)}

	if prog != nil {
		prog.Edit(result, opts)
	} else {
		c.SendMessage(userID, result, opts)
	}

	if added > 0 {
		go joinMandatoryChannels(userID)
	}

	return nil
}

func validateMessages(msgs []*telegram.NewMessage, attempt int) string {
	attemptsLeft := maxAttempts - attempt
	hasAnyFile, hasValidFile := false, false

	for _, m := range msgs {
		if m.File == nil {
			continue
		}
		hasAnyFile = true
		fname := strings.ToLower(m.File.Name)
		if !isMediaExt(strings.ToLower(m.File.Ext)) &&
			(strings.HasSuffix(fname, ".session") || strings.HasSuffix(fname, ".zip")) {
			hasValidFile = true
			break
		}
	}

	if !hasAnyFile {
		if attemptsLeft > 0 {
			return fmt.Sprintf(
				"📎 <b>No file detected!</b> Send a <code>.session</code> or <code>.zip</code>.\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
				attempt, maxAttempts, attemptsLeft,
			)
		}
		return ""
	}
	if !hasValidFile {
		if attemptsLeft > 0 {
			return fmt.Sprintf(
				"🚫 <b>No valid session file!</b> Accepted: <code>.session</code> or <code>.zip</code>\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
				attempt, maxAttempts, attemptsLeft,
			)
		}
		return ""
	}
	return ""
}

func isMediaExt(ext string) bool {
	switch ext {
	case ".mp3", ".ogg", ".flac", ".wav",
		".mp4", ".avi", ".mkv", ".mov",
		".gif", ".webm", ".webp",
		".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

type entry struct {
	name string
	data []byte
}

func extractSessionsFromZip(data []byte) ([]entry, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var out []entry
	for _, f := range r.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".session") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc)
		rc.Close()
		base := strings.TrimSuffix(filepath.Base(strings.ToLower(f.Name)), ".session")
		out = append(out, entry{base, buf.Bytes()})
	}
	return out, nil
}
