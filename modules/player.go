package modules

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tgmultibot/manager"

	"github.com/amarnathcjd/gogram/telegram"
)

const (
	playMediaDir = "/tmp/tgmultibot_media"
	playMaxRetry = 3
)

// handlePlayerPrompt is the 🎵 Player menu entry. It shows the player menu with
// Play Media, Playlist and Back buttons. Playback is started through the
// Play Media flow; per-chat controls live behind the Playlist.
func handlePlayerPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID)

	if cm.Count(ownerID) == 0 {
		_, err := m.Reply(noClientsMsg(userID), &telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("▶️ Play Media", "player:play"),
		telegram.Button.Data("📋 List Chats", "player:list"),
	).AddRow(
		telegram.Button.Data("🔙 Back to Menu", "player:menu"),
	)
	_, err := m.Reply(
		"🎵 <b>Player</b>\n\nSelect an option to play media in a voice chat or manage current playback.",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
	return err
}

func showPlayerMenu(cb *telegram.CallbackQuery) {
	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("▶️ Play Media", "player:play"),
		telegram.Button.Data("📋 Playlist", "player:list"),
	).AddRow(
		telegram.Button.Data("🔙 Back to Menu", "player:menu"),
	)
	_, _ = cb.Edit(
		"🎵 <b>Player</b>\n\nSelect an option to play media in a voice chat or manage current playback.",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
}

// startPlayFlow begins the play-media flow: chat input first, then media.
func startPlayFlow(userID, ownerID int64) {
	if cm.Count(ownerID) == 0 {
		c.SendMessage(userID, noClientsMsg(userID), &telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	if existing := ps.current(userID); existing != "" {
		kb := telegram.NewKeyboard()
		kb.AddRow(telegram.Button.Text(BtnBackToMenu))
		c.SendMessage(userID,
			fmt.Sprintf("⚙️ <b>Process already running:</b> <code>%s</code>\n\nSend /cancel to stop it first.", existing),
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)})
		return
	}

	ctx, ok := ps.tryStart(userID, "Player")
	if !ok {
		return
	}
	cg.SetListening(userID)

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err := c.SendMessage(userID,
		"🎵 <b>Play Media</b>\n\n"+
			"Send the <b>username</b> or <b>Chat ID</b> of the voice chat to play in.\n\n"+
			"• Public: <code>@username</code> or <code>https://t.me/username</code>\n"+
			"• Private: <b>Chat ID</b> (e.g. <code>-1001234567890</code>) or <code>https://t.me/c/1234567890</code>\n\n"+
			"⚠️ <b>For private groups/channels:</b> use <b>🔗 Join Channel</b> first to join all your clients, then come back here with the Chat ID.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)
	if err != nil {
		ps.done(userID)
		return
	}

	go playChatInputStep(ctx, userID, ownerID, 1)
}

// playChatInputStep waits for the username/Chat ID, then either shows the live
// controls (when a song is already playing there) or asks for the media file.
func playChatInputStep(ctx context.Context, userID, ownerID int64, attempt int) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in playChatInputStep: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>🎵 Player</b> to try again.",
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
		if attempt < playMaxRetry {
			c.SendMessage(userID,
				fmt.Sprintf("📎 <b>Please send a username or Chat ID, not a file.</b>\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
					attempt, playMaxRetry, playMaxRetry-attempt),
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			cg.SetListening(userID)
			go playChatInputStep(ctx, userID, ownerID, attempt+1)
			return
		}
		c.SendMessage(userID,
			"🚫 <b>Too many invalid attempts.</b> Tap <b>🎵 Player</b> to start over.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
		)
		return
	}

	peer, err := resolveVCPeer(text)
	if err != nil {
		c.SendMessage(userID,
			fmt.Sprintf("❌ <b>%s</b>", escapeHTML(err.Error())),
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	chatIDNum, err := cm.ResolveChatID(ownerID, peer)
	if err != nil {
		c.SendMessage(userID,
			fmt.Sprintf("❌ <b>%s</b>\n\n<i>For private groups, use 🔗 Join Channel first, then come back here with the Chat ID.</i>",
				escapeHTML(err.Error())),
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	if ctx.Err() != nil {
		return
	}

	if len(cm.SessionsInChat(ownerID, chatIDNum)) > 0 {
		text, kb := buildChatControlPanel(ownerID, chatIDNum)
		c.SendMessage(userID,
			"🎵 <b>Song already playing here.</b>\n\n"+text,
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
		return
	}

	cg.SetListening(userID)
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err = c.SendMessage(userID,
		"🎵 <b>Play Media</b>\n\n"+
			"Now send the <b>audio</b> or <b>video</b> file to play.\n\n"+
			"Accepted: <b>music</b>, <b>voice messages</b>, <b>videos</b> and audio/video documents.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)
	if err != nil {
		ps.done(userID)
		return
	}

	go playMediaForChatStep(ctx, userID, ownerID, chatIDNum, 1)
}

// playMediaForChatStep waits for the media file, then downloads and plays it in
// the given chat. On success the owner gets the "Media played" message plus the
// per-chat control panel.
func playMediaForChatStep(ctx context.Context, userID, ownerID, chatIDNum int64, attempt int) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in playMediaForChatStep: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>🎵 Player</b> to try again.",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
			)
		}
		return
	}

	if ctx.Err() != nil {
		return
	}

	m := msgs[0]
	if !m.IsMedia() {
		if attempt < playMaxRetry {
			c.SendMessage(userID,
				fmt.Sprintf("❌ <b>No file found!</b> Send an audio or video file.\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
					attempt, playMaxRetry, playMaxRetry-attempt),
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			cg.SetListening(userID)
			go playMediaForChatStep(ctx, userID, ownerID, chatIDNum, attempt+1)
			return
		}
		c.SendMessage(userID,
			"🚫 <b>Too many invalid attempts.</b> Tap <b>🎵 Player</b> to start over.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
		)
		return
	}

	isVideo, _, err := detectPlayMedia(m)
	if err != nil {
		if attempt < playMaxRetry {
			c.SendMessage(userID,
				fmt.Sprintf("🚫 <b>%s</b>\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
					escapeHTML(err.Error()), attempt, playMaxRetry, playMaxRetry-attempt),
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			cg.SetListening(userID)
			go playMediaForChatStep(ctx, userID, ownerID, chatIDNum, attempt+1)
			return
		}
		c.SendMessage(userID,
			"🚫 <b>Too many invalid attempts.</b> Tap <b>🎵 Player</b> to start over.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
		)
		return
	}

	if ctx.Err() != nil {
		return
	}

	prog, _ := c.SendMessage(userID, "⏳ <b>Downloading media...</b>", &telegram.SendOptions{ParseMode: telegram.HTML})

	filePath, err := downloadPlayMedia(ctx, m)
	if err != nil {
		msg := fmt.Sprintf("❌ <b>Download failed:</b> %s", escapeHTML(err.Error()))
		if prog != nil {
			prog.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		} else {
			c.SendMessage(userID, msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		}
		return
	}

	if ctx.Err() != nil {
		os.Remove(filePath)
		return
	}

	if prog != nil {
		prog.Edit("✅ <b>Media downloaded.</b>", &telegram.SendOptions{ParseMode: telegram.HTML})
	}

	durationMs := probeMediaDuration(filePath)
	total := cm.Count(ownerID)

	if prog != nil {
		prog.Edit(fmt.Sprintf("⏳ <b>Playing media with %d client(s)...</b>", total), &telegram.SendOptions{ParseMode: telegram.HTML})
	} else {
		c.SendMessage(userID, fmt.Sprintf("⏳ <b>Playing media with %d client(s)...</b>", total), &telegram.SendOptions{ParseMode: telegram.HTML})
	}

	results := cm.Play(ctx, ownerID, chatIDNum, filePath, isVideo, durationMs)

	joined, _, hardErr, playAccountErrors := applyPlayResults(results)
	if hardErr != "" {
		if prog != nil {
			prog.Edit(hardErr, &telegram.SendOptions{ParseMode: telegram.HTML})
		} else {
			c.SendMessage(userID, hardErr, &telegram.SendOptions{ParseMode: telegram.HTML})
		}
		return
	}

	if ctx.Err() != nil {
		var result string
		if joined == 0 {
			result = "🚫 <b>Cancelled.</b>\n\nNo clients had joined yet."
		} else {
			result = fmt.Sprintf(
				"🚫 <b>Cancelled mid-join.</b>\n\n"+
					"✅ <b>Already joined:</b> %d client(s)\n\n"+
					"⚠️ The joined client(s) are <b>still in the voice chat</b>.\n"+
					"Tap <b>🔇 Leave Voice Chat</b> to remove them.",
				joined,
			)
		}
		if len(playAccountErrors) > 0 {
			summary := manager.SummarizeErrors(playAccountErrors)
			result += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
		}
		if prog != nil {
			prog.Edit(result, &telegram.SendOptions{ParseMode: telegram.HTML})
		} else {
			c.SendMessage(userID, result, &telegram.SendOptions{ParseMode: telegram.HTML})
		}
		return
	}

	if joined == 0 {
		msg := "❌ <b>Failed to play media.</b>"
		if len(playAccountErrors) > 0 {
			summary := manager.SummarizeErrors(playAccountErrors)
			msg += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
		}
		if prog != nil {
			prog.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		} else {
			c.SendMessage(userID, msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		}
		return
	}

	text, kb := buildChatControlPanel(ownerID, chatIDNum)
	msg := "✅ <b>Media played.</b>\n\n" + text
	if len(playAccountErrors) > 0 {
		summary := manager.SummarizeErrors(playAccountErrors)
		msg += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
	}
	opts := &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}
	if prog != nil {
		prog.Edit(msg, opts)
	} else {
		c.SendMessage(userID, msg, opts)
	}
}

// applyPlayResults folds per-client join results into a joined count, per-client
// failure messages and an optional hard error (no active call / wrong peer).
func applyPlayResults(results []manager.JoinVoiceChatResult) (joined int, failMsgs []string, hardErr string, accountErrors []manager.AccountError) {
	for _, r := range results {
		switch {
		case r.NoActiveGroupCall:
			return 0, nil, "❌ <b>No active voice chat found in that channel.</b>\n\n" +
				"Start a voice chat there first, then try again.", nil
		case r.WrongPeerType:
			return 0, nil, "❌ <b>This appears to be a user account, not a group or channel.</b>\n\n" +
				"Voice chats are only available in groups or channels. Please provide a group/channel username or Chat ID.", nil
		case r.Err != nil:
			if r.Err == context.Canceled {
				continue
			}
			accountErrors = append(accountErrors, manager.AccountError{AccountID: r.AccountID, Err: r.Err})
			sendTraceback(fmt.Sprintf("Player error (account %d): %v", r.AccountID, r.Err))
		default:
			joined++
		}
	}
	return joined, failMsgs, "", accountErrors
}

// handlePlayerCallback drives the player menu, playlist, per-chat controls and
// the stop/replay/loop/play-another screens.
func handlePlayerCallback(cb *telegram.CallbackQuery) error {
	data := cb.DataString()
	if !strings.HasPrefix(data, "player:") {
		return nil
	}

	userID := cb.GetSenderID()
	ownerID := effectiveOwnerID(userID)

	switch data {
	case "player:play":
		_, _ = cb.Answer("📥 Send a username or Chat ID first.")
		startPlayFlow(userID, ownerID)
		return nil

	case "player:list":
		_, _ = cb.Answer("")
		showPlaylist(cb, ownerID)
		return nil

	case "player:main":
		_, _ = cb.Answer("")
		showPlayerMenu(cb)
		return nil

	case "player:menu":
		_, _ = cb.Answer("")
		_, err := c.SendMessage(userID, "🏠 <b>Main Menu</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
		return err
	}

	if id, ok := chatIDFromData(data, "player:view:"); ok {
		_, _ = cb.Answer("")
		text, kb := buildChatControlPanel(ownerID, id)
		_, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
		return err
	}

	if id, ok := chatIDFromData(data, "player:pause:"); ok {
		return refreshChatControls(cb, ownerID, id, playerErrToast("⏸ Paused.", cm.PauseChat(ownerID, id)))
	}

	if id, ok := chatIDFromData(data, "player:resume:"); ok {
		return refreshChatControls(cb, ownerID, id, playerErrToast("▶️ Resumed.", cm.ResumeChat(ownerID, id)))
	}

	if id, ok := chatIDFromData(data, "player:mute:"); ok {
		return refreshChatControls(cb, ownerID, id, playerErrToast("🔇 Muted.", cm.MuteChat(ownerID, id)))
	}

	if id, ok := chatIDFromData(data, "player:unmute:"); ok {
		return refreshChatControls(cb, ownerID, id, playerErrToast("🔊 Unmuted.", cm.UnmuteChat(ownerID, id)))
	}

	if id, ok := chatIDFromData(data, "player:seek:back:"); ok {
		target := uint64(0)
		if st, found := cm.StatusChat(ownerID, id); found && st.PositionMs > 10000 {
			target = st.PositionMs - 10000
		}
		return refreshChatControls(cb, ownerID, id, playerErrToast("⏪ -10s.", cm.SeekChat(ownerID, id, target)))
	}

	if id, ok := chatIDFromData(data, "player:seek:fwd:"); ok {
		target := uint64(0)
		if st, found := cm.StatusChat(ownerID, id); found {
			target = st.PositionMs + 10000
			if st.DurationMs > 0 && target > st.DurationMs {
				target = st.DurationMs
			}
		}
		return refreshChatControls(cb, ownerID, id, playerErrToast("⏩ +10s.", cm.SeekChat(ownerID, id, target)))
	}

	if id, ok := chatIDFromData(data, "player:stop:"); ok {
		return stopChat(cb, ownerID, id)
	}

	if id, ok := chatIDFromData(data, "player:replay:"); ok {
		replayMedia(cb, userID, ownerID, id)
		return nil
	}

	if id, ok := chatIDFromData(data, "player:loop:"); ok {
		startLoopFlow(cb, userID, ownerID, id)
		return nil
	}

	if id, ok := chatIDFromData(data, "player:another:"); ok {
		_, _ = cb.Answer("📥 Send a media file to play.")
		startPlayAnother(userID, ownerID, id)
		return nil
	}

	if id, ok := chatIDFromData(data, "player:back:"); ok {
		_, _ = cb.Answer("")
		ps.cancel(userID)
		returnBackFromLoop(cb, ownerID, id)
		return nil
	}

	_, err := cb.Answer("")
	return err
}

func chatIDFromData(data, prefix string) (int64, bool) {
	rest := strings.TrimPrefix(data, prefix)
	if rest == data {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func refreshChatControls(cb *telegram.CallbackQuery, ownerID, chatIDNum int64, toast string) error {
	text, kb := buildChatControlPanel(ownerID, chatIDNum)
	if _, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}); err != nil {
		return err
	}
	_, err := cb.Answer(toast)
	return err
}

func playerErrToast(action string, errs []error) string {
	if len(errs) == 0 {
		return action
	}
	return fmt.Sprintf("⚠️ %s %d client(s) failed.", action, len(errs))
}

// showPlaylist lists every chat where media is currently playing, oldest first,
// with a button per chat that opens its control panel.
func showPlaylist(cb *telegram.CallbackQuery, ownerID int64) {
	entries := cm.Playlist(ownerID)
	if len(entries) == 0 {
		kb := telegram.NewKeyboard()
		kb.AddRow(telegram.Button.Data("🔙 Back", "player:main"))
		_, _ = cb.Edit("🎵 <b>Playlist</b>\n\nNo song is playing right now.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "🎵 <b>Playlist</b>\n\nCurrently playing in <b>%d</b> chat(s):\n\n", len(entries))
	kb := telegram.NewKeyboard()
	for i, e := range entries {
		title, err := cm.ChatTitle(ownerID, e.ChatIDNum)
		if err != nil || title == "" {
			title = fmt.Sprintf("chat %d", e.ChatIDNum)
		}
		title = truncateTitle(title)
		fmt.Fprintf(&sb, "%d. <b>%s</b>\n", i+1, escapeHTML(title))
		kb.AddRow(telegram.Button.Data("🎵 "+title, fmt.Sprintf("player:view:%d", e.ChatIDNum)))
	}
	kb.AddRow(telegram.Button.Data("🔙 Back", "player:main"))
	_, _ = cb.Edit(sb.String(), &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
}

func truncateTitle(s string) string {
	const max = 18
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// buildChatControlPanel renders the per-chat playback status and inline controls.
func buildChatControlPanel(ownerID, chatIDNum int64) (string, *telegram.KeyboardBuilder) {
	title, err := cm.ChatTitle(ownerID, chatIDNum)
	if err != nil || title == "" {
		title = fmt.Sprintf("chat %d", chatIDNum)
	}

	st, ok := cm.StatusChat(ownerID, chatIDNum)
	if !ok {
		kb := telegram.NewKeyboard()
		kb.AddRow(telegram.Button.Data("🔙 Back", "player:list"))
		return fmt.Sprintf("🎵 <b>%s</b>\n\n⏹ <b>Nothing is playing here.</b>", escapeHTML(title)), kb
	}

	stateIcon, stateText := "▶️", "Playing"
	switch {
	case st.Ended:
		stateIcon, stateText = "⏹", "Ended"
	case st.Paused:
		stateIcon, stateText = "⏸", "Paused"
	}

	muteIcon, muteText := "🔊", "Unmuted"
	if st.Muted {
		muteIcon, muteText = "🔇", "Muted"
	}

	timeText := formatPlayerTime(st.PositionMs)
	if st.DurationMs > 0 {
		timeText += " / " + formatPlayerTime(st.DurationMs)
	}

	text := fmt.Sprintf(
		"🎵 <b>Now Playing</b>\n\n"+
			"📢 <b>Chat:</b> %s\n"+
			"%s <b>Status:</b> %s\n"+
			"%s <b>Mic:</b> %s\n"+
			"⏱ <b>Time:</b> <code>%s</code>",
		escapeHTML(title), stateIcon, stateText, muteIcon, muteText, timeText,
	)
	return text, buildChatControlKB(chatIDNum, st)
}

// buildChatControlKB renders the control row set: pause/resume and mute/unmute
// are swapped dynamically based on the current state. There is no Play or
// New Media button — a new song is started from the player menu instead.
func buildChatControlKB(chatIDNum int64, st manager.PlayerState) *telegram.KeyboardBuilder {
	kb := telegram.NewKeyboard()

	pauseBtn := telegram.Button.Data("⏸ Pause", fmt.Sprintf("player:pause:%d", chatIDNum))
	if st.Paused {
		pauseBtn = telegram.Button.Data("▶️ Resume", fmt.Sprintf("player:resume:%d", chatIDNum))
	}
	muteBtn := telegram.Button.Data("🔇 Mute", fmt.Sprintf("player:mute:%d", chatIDNum))
	if st.Muted {
		muteBtn = telegram.Button.Data("🔊 Unmute", fmt.Sprintf("player:unmute:%d", chatIDNum))
	}

	kb.AddRow(pauseBtn, muteBtn).AddRow(
		telegram.Button.Data("⏪ -10s", fmt.Sprintf("player:seek:back:%d", chatIDNum)),
		telegram.Button.Data("⏩ +10s", fmt.Sprintf("player:seek:fwd:%d", chatIDNum)),
	).AddRow(
		telegram.Button.Data("🛑 Stop", fmt.Sprintf("player:stop:%d", chatIDNum)),
	).AddRow(
		telegram.Button.Data("🔙 Back", "player:list"),
		telegram.Button.Data("🏠 Main Menu", "player:menu"),
	)
	return kb
}

// buildStopScreen renders the post-stop menu with Replay, Loop and Play Another.
func buildStopScreen(ownerID, chatIDNum int64) (string, *telegram.KeyboardBuilder) {
	title, err := cm.ChatTitle(ownerID, chatIDNum)
	if err != nil || title == "" {
		title = fmt.Sprintf("chat %d", chatIDNum)
	}

	text := fmt.Sprintf(
		"⏹ <b>Song stopped.</b>\n\n"+
			"📢 <b>Chat:</b> %s\n\n"+
			"<i>What would you like to do next?</i>",
		escapeHTML(title),
	)

	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("🔁 Replay", fmt.Sprintf("player:replay:%d", chatIDNum)),
		telegram.Button.Data("🔂 Loop", fmt.Sprintf("player:loop:%d", chatIDNum)),
	).AddRow(
		telegram.Button.Data("▶️ Play Another", fmt.Sprintf("player:another:%d", chatIDNum)),
	).AddRow(
		telegram.Button.Data("🔙 Player Menu", "player:main"),
		telegram.Button.Data("🏠 Main Menu", "player:menu"),
	)
	return text, kb
}

func stopChat(cb *telegram.CallbackQuery, ownerID, chatIDNum int64) error {
	errs := cm.StopChat(ownerID, chatIDNum)
	toast := "⏹ Stopped."
	if len(errs) > 0 {
		toast = "⚠️ Some clients failed to stop."
	}

	text, kb := buildStopScreen(ownerID, chatIDNum)
	if _, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}); err != nil {
		return err
	}
	_, err := cb.Answer(toast)
	return err
}

// replayMedia restarts the most recently stopped media in the same chat.
func replayMedia(cb *telegram.CallbackQuery, userID, ownerID, chatIDNum int64) {
	_, _ = cb.Answer("🔄 Replaying...")

	results, err := cm.ReplayMedia(ownerID, chatIDNum)
	if err != nil {
		msg := fmt.Sprintf("❌ <b>Cannot replay:</b> %s", escapeHTML(err.Error()))
		if _, e := cb.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML}); e == nil {
			return
		}
		c.SendMessage(userID, msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	joined, _, hardErr, replayAccountErrors := applyPlayResults(results)
	if hardErr != "" {
		_, _ = cb.Edit(hardErr, &telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}
	if joined == 0 {
		msg := "❌ <b>Failed to replay media.</b>"
		if len(replayAccountErrors) > 0 {
			summary := manager.SummarizeErrors(replayAccountErrors)
			msg += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
		}
		_, _ = cb.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	text, kb := buildChatControlPanel(ownerID, chatIDNum)
	msg := "✅ <b>Media replayed.</b>\n\n" + text
	if len(replayAccountErrors) > 0 {
		summary := manager.SummarizeErrors(replayAccountErrors)
		msg += "\n\n" + manager.FormatErrorSummary(summary, "<b>Errors:</b>")
	}
	_, _ = cb.Edit(msg, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
}

// startPlayAnother begins a media-input flow for a specific chat (used by the
// Play Another button on the stop screen).
func startPlayAnother(userID, ownerID, chatIDNum int64) {
	if cm.Count(ownerID) == 0 {
		c.SendMessage(userID, noClientsMsg(userID), &telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	if existing := ps.current(userID); existing != "" {
		kb := telegram.NewKeyboard()
		kb.AddRow(telegram.Button.Text(BtnBackToMenu))
		c.SendMessage(userID,
			fmt.Sprintf("⚙️ <b>Process already running:</b> <code>%s</code>\n\nSend /cancel to stop it first.", existing),
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)})
		return
	}

	ctx, ok := ps.tryStart(userID, "Player")
	if !ok {
		return
	}
	cg.SetListening(userID)

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))

	_, err := c.SendMessage(userID,
		"🎵 <b>Play Another</b>\n\n"+
			"Send the <b>audio</b> or <b>video</b> file to play in the same chat.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)
	if err != nil {
		ps.done(userID)
		return
	}

	go playMediaForChatStep(ctx, userID, ownerID, chatIDNum, 1)
}

// startLoopFlow asks how many times the song should loop and waits for the
// number. A back button cancels the input and returns to the previous screen.
func startLoopFlow(cb *telegram.CallbackQuery, userID, ownerID, chatIDNum int64) {
	if existing := ps.current(userID); existing != "" {
		_, _ = cb.Answer("⚙️ Another process is running.", &telegram.CallbackOptions{Alert: true})
		return
	}

	ctx, ok := ps.tryStart(userID, "Player Loop")
	if !ok {
		_, _ = cb.Answer("⚙️ Another process is running.", &telegram.CallbackOptions{Alert: true})
		return
	}
	cg.SetListening(userID)

	_, _ = cb.Answer("🔂 Set loop count.")

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Data("🔙 Back", fmt.Sprintf("player:back:%d", chatIDNum)))
	_, _ = cb.Edit(
		"🔂 <b>Set Loop Count</b>\n\n"+
			"How many times should this song repeat after it ends?\n\n"+
			"• Send a number: <code>1</code>, <code>2</code>, <code>5</code>, <code>10</code>...\n"+
			"• Send <code>0</code> to disable loop.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)

	go loopInputStep(ctx, userID, ownerID, chatIDNum, 1)
}

func loopInputStep(ctx context.Context, userID, ownerID, chatIDNum int64, attempt int) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in loopInputStep: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil && cg.IsListening(userID) {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b>",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
			)
		}
		return
	}

	if ctx.Err() != nil {
		return
	}

	n, err := strconv.Atoi(strings.TrimSpace(msgs[0].Text()))
	if err != nil || n < 0 {
		if attempt < playMaxRetry {
			c.SendMessage(userID,
				fmt.Sprintf("❌ <b>Invalid number.</b> Send a whole number like <code>1</code>, <code>2</code> or <code>10</code> (or <code>0</code> to disable).\n\n🔄 <i>Attempt %d/%d — %d remaining.</i>",
					attempt, playMaxRetry, playMaxRetry-attempt),
				&telegram.SendOptions{ParseMode: telegram.HTML},
			)
			cg.SetListening(userID)
			go loopInputStep(ctx, userID, ownerID, chatIDNum, attempt+1)
			return
		}
		c.SendMessage(userID,
			"🚫 <b>Too many invalid attempts.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: telegram.Button.Clear()},
		)
		return
	}

	if err := cm.SetLoop(ownerID, chatIDNum, n); err != nil {
		c.SendMessage(userID,
			fmt.Sprintf("❌ <b>%s</b>", escapeHTML(err.Error())),
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Data("🔙 Back", fmt.Sprintf("player:back:%d", chatIDNum)))
	var msg string
	if n == 0 {
		msg = "✅ <b>Loop disabled.</b>"
	} else {
		msg = fmt.Sprintf("✅ <b>Loop set to %d time(s).</b>\n\n<i>The song will repeat after it ends.</i>", n)
	}
	c.SendMessage(userID, msg, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
}

// returnBackFromLoop closes the loop input and returns to the controls (if the
// song is playing again) or to the stop screen.
func returnBackFromLoop(cb *telegram.CallbackQuery, ownerID, chatIDNum int64) {
	if _, ok := cm.StatusChat(ownerID, chatIDNum); ok {
		text, kb := buildChatControlPanel(ownerID, chatIDNum)
		_, _ = cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
		return
	}
	text, kb := buildStopScreen(ownerID, chatIDNum)
	_, _ = cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
}

// notifyPlayerPlaybackEnd is called when a stream ends naturally and no further
// loop is pending. It surfaces the stop screen so the owner can replay, loop or
// play another media.
func notifyPlayerPlaybackEnd(ownerID, chatIDNum int64) {
	if cm.Count(ownerID) == 0 {
		return
	}
	text, kb := buildStopScreen(ownerID, chatIDNum)
	text = "🎵 <b>Song finished.</b>\n\n" + text
	c.SendMessage(ownerID, text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
}

func formatPlayerTime(ms uint64) string {
	sec := ms / 1000
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// probeMediaDuration returns the media duration in milliseconds using ffprobe.
// It returns 0 when the duration cannot be determined (live/broadcast files).
func probeMediaDuration(filePath string) uint64 {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", filePath).Output()
	if err != nil {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return uint64(f * 1000)
}

// detectPlayMedia returns whether the media is a video (vs audio) and its MIME
// type. Photos, stickers, animations and non-audio/video documents are rejected.
func detectPlayMedia(m *telegram.NewMessage) (isVideo bool, mime string, err error) {
	if !m.IsMedia() {
		return false, "", fmt.Errorf("no media found in message")
	}

	if video := m.Video(); video != nil {
		return true, video.MimeType, nil
	}

	if audio := m.Audio(); audio != nil {
		return false, audio.MimeType, nil
	}

	if doc := m.Document(); doc != nil {
		for _, attr := range doc.Attributes {
			if _, ok := attr.(*telegram.DocumentAttributeAnimated); ok {
				return false, "", fmt.Errorf("unsupported file: send an audio or video file")
			}
			if _, ok := attr.(*telegram.DocumentAttributeSticker); ok {
				return false, "", fmt.Errorf("unsupported file: send an audio or video file")
			}
		}

		ext := strings.ToLower(filepath.Ext(docFileName(doc)))
		if isPlayableAudioExt(ext) || isPlayableVideoExt(ext) || isPlayableMime(doc.MimeType) {
			return isPlayableVideoExt(ext) || strings.HasPrefix(strings.ToLower(doc.MimeType), "video/"), doc.MimeType, nil
		}
	}

	return false, "", fmt.Errorf("unsupported file: send an audio or video file")
}

func docFileName(doc *telegram.DocumentObj) string {
	for _, attr := range doc.Attributes {
		if f, ok := attr.(*telegram.DocumentAttributeFilename); ok && f.FileName != "" {
			return f.FileName
		}
	}
	return ""
}

func isPlayableAudioExt(ext string) bool {
	switch ext {
	case ".mp3", ".ogg", ".oga", ".opus", ".flac", ".wav", ".m4a", ".aac", ".wma", ".mid", ".amr", ".aiff", ".ape":
		return true
	}
	return false
}

func isPlayableVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mkv", ".webm", ".avi", ".mov", ".m4v", ".flv", ".wmv", ".ts", ".3gp":
		return true
	}
	return false
}

func isPlayableMime(mime string) bool {
	lower := strings.ToLower(mime)
	return strings.HasPrefix(lower, "audio/") || strings.HasPrefix(lower, "video/")
}

func downloadPlayMedia(ctx context.Context, m *telegram.NewMessage) (string, error) {
	if err := os.MkdirAll(playMediaDir, 0o755); err != nil {
		return "", err
	}
	return m.Download(&telegram.DownloadOptions{
		FileName: playMediaDir + string(os.PathSeparator),
		Ctx:      ctx,
	})
}
