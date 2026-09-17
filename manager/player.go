package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"tgmultibot/ntgcalls"
)

// PlayerState is a point-in-time snapshot of playback for one owner across all
// of their accounts. PositionMs is the current playback position (ms).
type PlayerState struct {
	Active     int
	Total      int
	Playing    bool
	Paused     bool
	Muted      bool
	Ended      bool
	PositionMs uint64
	DurationMs uint64
}

// Player is the playback control surface used by the bot's modules. Every
// control fan-outs to all active voice chat sessions owned by the given owner.
type Player interface {
	Play(ctx context.Context, ownerID int64, chatID any, mediaFile string, isVideo bool, durationMs uint64) []JoinVoiceChatResult
	Pause(ownerID int64) []error
	Resume(ownerID int64) []error
	Mute(ownerID int64) []error
	Unmute(ownerID int64) []error
	SeekTo(ownerID int64, positionMs uint64) []error
	Stop(ownerID int64) []error
	Status(ownerID int64) PlayerState
}

var _ Player = (*ClientManager)(nil)

// startPlayback resets the session's position tracking when a stream begins.
// The first stream time is remembered so the playlist can order chats by when
// they started playing.
func (s *VoiceChatSession) startPlayback() {
	s.mu.Lock()
	if s.startedAt.IsZero() {
		s.startedAt = time.Now()
	}
	s.positionMs = 0
	s.resumedAt = time.Now()
	s.paused = false
	s.muted = false
	s.ended = false
	s.mu.Unlock()
}

// handleStreamEnd runs when the ntgcalls stream reaches EOF. If a loop count is
// still pending the stream restarts from the beginning; otherwise the session
// is retired and notify is called so the owner can decide what to do next
// (replay, loop, or play another media).
func (s *VoiceChatSession) handleStreamEnd(notify func()) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	if !s.paused {
		s.positionMs += uint64(time.Since(s.resumedAt).Milliseconds())
	}
	s.ended = true
	s.paused = false
	loop := s.loopCount
	if loop > 0 {
		s.loopCount = loop - 1
	}
	s.mu.Unlock()

	if loop > 0 {
		go func() {
			if err := s.seek(0); err != nil {
				if s.retire() {
					notify()
				}
			}
		}()
		return
	}

	if s.retire() {
		notify()
	}
}

// retire clears the session record while parking the media file for a short
// grace period so the owner can still Replay it or set a Loop before it is
// deleted. It reports whether this retirement was the one that parked the media
// (only one account per chat parks it), so callers can fire a single
// notification per chat.
func (s *VoiceChatSession) retire() bool {
	key := vcKey{ownerID: s.OwnerID, accountID: s.AccountID}
	ck := chatKey{ownerID: s.OwnerID, chatIDNum: s.ChatIDNum}

	if s.cancel != nil {
		s.cancel()
	}

	parked := false
	vcManager.mu.Lock()
	if cur, ok := vcManager.sessions[key]; ok && cur == s {
		delete(vcManager.sessions, key)
	}
	if s.MediaFile != "" {
		if _, exists := vcManager.stopped[ck]; !exists {
			vcManager.stopped[ck] = &StoppedMedia{
				MediaFile:  s.MediaFile,
				IsVideo:    s.IsVideo,
				DurationMs: s.DurationMs,
			}
			scheduleStoppedCleanup(ck, s.MediaFile)
			parked = true
		}
	}
	vcManager.mu.Unlock()
	return parked
}

func scheduleStoppedCleanup(ck chatKey, mediaFile string) {
	go func() {
		time.Sleep(stoppedMediaTTL)
		vcManager.mu.Lock()
		if sm, ok := vcManager.stopped[ck]; ok && sm.MediaFile == mediaFile {
			delete(vcManager.stopped, ck)
			_ = os.Remove(mediaFile)
		}
		vcManager.mu.Unlock()
	}()
}

// pause freezes playback and snapshots the elapsed time. It is a no-op when
// already paused or when the stream has ended.
func (s *VoiceChatSession) pause() error {
	s.mu.Lock()
	if s.paused || s.ended {
		s.mu.Unlock()
		return nil
	}
	s.positionMs += uint64(time.Since(s.resumedAt).Milliseconds())
	s.paused = true
	s.mu.Unlock()
	_, err := s.Ubot.Pause(s.ChatIDNum)
	return err
}

// resume continues playback from where it was paused, or replays from the
// start if the stream had already reached its end.
func (s *VoiceChatSession) resume() error {
	s.mu.Lock()
	ended := s.ended
	paused := s.paused
	s.mu.Unlock()

	if ended {
		return s.seek(0)
	}
	if !paused {
		return nil
	}

	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return s.seek(0)
	}
	s.paused = false
	s.resumedAt = time.Now()
	s.mu.Unlock()
	_, err := s.Ubot.Resume(s.ChatIDNum)
	return err
}

func (s *VoiceChatSession) mute() error {
	s.mu.Lock()
	s.muted = true
	s.mu.Unlock()
	_, err := s.Ubot.Mute(s.ChatIDNum)
	return err
}

func (s *VoiceChatSession) unmute() error {
	s.mu.Lock()
	s.muted = false
	s.mu.Unlock()
	_, err := s.Ubot.UnMute(s.ChatIDNum)
	return err
}

// seek restarts the stream at the given offset. ntgcalls has no direct seek
// API, so the stream is replaced with a fresh ffmpeg source started at
// positionMs (see buildMediaDescription).
func (s *VoiceChatSession) seek(positionMs uint64) error {
	if s.MediaFile == "" {
		return errors.New("no media attached to this session")
	}
	desc := buildMediaDescription(s.MediaFile, s.IsVideo, positionMs)
	if err := s.Ubot.Play(s.ChatIDNum, desc); err != nil {
		return err
	}
	s.mu.Lock()
	s.positionMs = positionMs
	s.resumedAt = time.Now()
	s.paused = false
	s.ended = false
	s.mu.Unlock()
	return nil
}

// position returns the current playback position in milliseconds.
func (s *VoiceChatSession) position() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paused && !s.ended {
		return s.positionMs + uint64(time.Since(s.resumedAt).Milliseconds())
	}
	return s.positionMs
}

func (m *ClientManager) Play(ctx context.Context, ownerID int64, chatID any, mediaFile string, isVideo bool, durationMs uint64) []JoinVoiceChatResult {
	return m.joinVoiceChats(ctx, ownerID, chatID, 0, mediaFile, isVideo, durationMs, 0)
}

func (m *ClientManager) controlAll(ownerID int64, op func(*VoiceChatSession) error) []error {
	var errs []error
	for _, sess := range m.ActiveVoiceChats(ownerID) {
		if err := op(sess); err != nil {
			errs = append(errs, fmt.Errorf("account %d: %w", sess.AccountID, err))
		}
	}
	return errs
}

func (m *ClientManager) Pause(ownerID int64) []error {
	return m.controlAll(ownerID, func(s *VoiceChatSession) error { return s.pause() })
}

func (m *ClientManager) Resume(ownerID int64) []error {
	return m.controlAll(ownerID, func(s *VoiceChatSession) error { return s.resume() })
}

func (m *ClientManager) Mute(ownerID int64) []error {
	return m.controlAll(ownerID, func(s *VoiceChatSession) error { return s.mute() })
}

func (m *ClientManager) Unmute(ownerID int64) []error {
	return m.controlAll(ownerID, func(s *VoiceChatSession) error { return s.unmute() })
}

func (m *ClientManager) SeekTo(ownerID int64, positionMs uint64) []error {
	return m.controlAll(ownerID, func(s *VoiceChatSession) error { return s.seek(positionMs) })
}

func (m *ClientManager) Stop(ownerID int64) []error {
	return m.LeaveVoiceChat(ownerID)
}

func (m *ClientManager) Status(ownerID int64) PlayerState {
	active := m.ActiveVoiceChats(ownerID)
	st := PlayerState{
		Active: len(active),
		Total:  m.Count(ownerID),
	}
	if len(active) == 0 {
		return st
	}

	sess := active[0]
	sess.mu.Lock()
	st.Paused = sess.paused
	st.Muted = sess.muted
	st.Ended = sess.ended
	st.Playing = !sess.paused && !sess.ended
	st.PositionMs = sess.positionMs
	if st.Playing {
		st.PositionMs += uint64(time.Since(sess.resumedAt).Milliseconds())
	}
	st.DurationMs = sess.DurationMs
	sess.mu.Unlock()
	return st
}

// SessionsInChat returns the sessions streaming into the given chat for ownerID.
func (m *ClientManager) SessionsInChat(ownerID int64, chatIDNum int64) []*VoiceChatSession {
	var res []*VoiceChatSession
	for _, s := range m.ActiveVoiceChats(ownerID) {
		if s.ChatIDNum == chatIDNum {
			res = append(res, s)
		}
	}
	return res
}

func (m *ClientManager) controlChat(ownerID, chatIDNum int64, op func(*VoiceChatSession) error) []error {
	var errs []error
	for _, s := range m.SessionsInChat(ownerID, chatIDNum) {
		if err := op(s); err != nil {
			errs = append(errs, fmt.Errorf("account %d: %w", s.AccountID, err))
		}
	}
	return errs
}

func (m *ClientManager) PauseChat(ownerID, chatIDNum int64) []error {
	return m.controlChat(ownerID, chatIDNum, func(s *VoiceChatSession) error { return s.pause() })
}

func (m *ClientManager) ResumeChat(ownerID, chatIDNum int64) []error {
	return m.controlChat(ownerID, chatIDNum, func(s *VoiceChatSession) error { return s.resume() })
}

func (m *ClientManager) MuteChat(ownerID, chatIDNum int64) []error {
	return m.controlChat(ownerID, chatIDNum, func(s *VoiceChatSession) error { return s.mute() })
}

func (m *ClientManager) UnmuteChat(ownerID, chatIDNum int64) []error {
	return m.controlChat(ownerID, chatIDNum, func(s *VoiceChatSession) error { return s.unmute() })
}

func (m *ClientManager) SeekChat(ownerID, chatIDNum int64, positionMs uint64) []error {
	return m.controlChat(ownerID, chatIDNum, func(s *VoiceChatSession) error { return s.seek(positionMs) })
}

// StatusChat returns a playback snapshot for one chat. The bool is false when
// nothing is playing in that chat.
func (m *ClientManager) StatusChat(ownerID, chatIDNum int64) (PlayerState, bool) {
	sessions := m.SessionsInChat(ownerID, chatIDNum)
	if len(sessions) == 0 {
		return PlayerState{}, false
	}
	sess := sessions[0]
	sess.mu.Lock()
	st := PlayerState{
		Active:     len(sessions),
		Total:      len(sessions),
		Paused:     sess.paused,
		Muted:      sess.muted,
		Ended:      sess.ended,
		Playing:    !sess.paused && !sess.ended,
		DurationMs: sess.DurationMs,
	}
	sess.mu.Unlock()
	st.PositionMs = sess.position()
	return st, true
}

// SetLoop configures how many times the media should replay after the stream
// ends. It applies to an actively playing chat or to a stopped media that can
// still be replayed. count == 0 disables looping.
func (m *ClientManager) SetLoop(ownerID int64, chatIDNum int64, count int) error {
	if sessions := m.SessionsInChat(ownerID, chatIDNum); len(sessions) > 0 {
		sess := sessions[0]
		sess.mu.Lock()
		sess.loopCount = count
		sess.mu.Unlock()
		return nil
	}

	ck := chatKey{ownerID: ownerID, chatIDNum: chatIDNum}
	vcManager.mu.Lock()
	defer vcManager.mu.Unlock()
	sm, ok := vcManager.stopped[ck]
	if !ok {
		return errors.New("no media found to loop")
	}
	sm.LoopCount = count
	return nil
}

// buildMediaDescription returns the shell-based ffmpeg stream for the given
// media file. Using MediaSourceShell (instead of MediaSourceFile) lets us seek
// arbitrarily by restarting the stream with an -ss offset.
func buildMediaDescription(filePath string, isVideo bool, seekMs uint64) ntgcalls.MediaDescription {
	seekArg := ""
	if seekMs > 0 {
		seekArg = fmt.Sprintf("-ss %d.%03d ", seekMs/1000, seekMs%1000)
	}
	file := shellQuote(filePath)

	desc := ntgcalls.MediaDescription{
		Microphone: &ntgcalls.AudioDescription{
			MediaSource:  ntgcalls.MediaSourceShell,
			Input:        fmt.Sprintf("ffmpeg %s-i %s -f s16le -ac 2 -ar 48000 -v quiet pipe:1", seekArg, file),
			SampleRate:   48000,
			ChannelCount: 2,
		},
	}
	if isVideo {
		desc.Camera = &ntgcalls.VideoDescription{
			MediaSource: ntgcalls.MediaSourceShell,
			Input:       fmt.Sprintf("ffmpeg %s-i %s -f rawvideo -r 30 -pix_fmt yuv420p -vf scale=1280:720 -v quiet pipe:1", seekArg, file),
			Width:       1280,
			Height:      720,
			Fps:         30,
		}
	}
	return desc
}

// shellQuote wraps s in single quotes for safe use inside a shell command,
// escaping any embedded single quote.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`&|;()<>*?[]{}~#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
