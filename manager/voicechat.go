package manager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"tgmultibot/ntgcalls"
	"tgmultibot/ubot"

	"github.com/amarnathcjd/gogram/telegram"
)

type VoiceChatSession struct {
	Ubot       *ubot.Context
	ChatID     any
	ChatIDNum  int64
	OwnerID    int64
	AccountID  int64
	MediaFile  string
	IsVideo    bool
	DurationMs uint64

	mu         sync.Mutex
	startedAt  time.Time
	positionMs uint64
	resumedAt  time.Time
	paused     bool
	muted      bool
	ended      bool
	loopCount  int
	cancel     context.CancelFunc
}

// StartedAt returns the wall-clock time this session's first stream began.
func (s *VoiceChatSession) StartedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedAt
}

type vcKey struct {
	ownerID   int64
	accountID int64
}

// chatKey identifies playback state that is shared across every account
// streaming into the same chat (the same media file is reused by all).
type chatKey struct {
	ownerID   int64
	chatIDNum int64
}

// StoppedMedia is a media file parked after playback ends (naturally or via the
// Stop button) so the owner can still Replay it or set a Loop before it is
// cleaned up after stoppedMediaTTL.
type StoppedMedia struct {
	MediaFile  string
	IsVideo    bool
	DurationMs uint64
	LoopCount  int
}

const stoppedMediaTTL = 5 * time.Minute

type VoiceChatManager struct {
	mu       sync.RWMutex
	sessions map[vcKey]*VoiceChatSession
	stopped  map[chatKey]*StoppedMedia
}

func NewVoiceChatManager() *VoiceChatManager {
	return &VoiceChatManager{
		sessions: make(map[vcKey]*VoiceChatSession),
		stopped:  make(map[chatKey]*StoppedMedia),
	}
}

var vcManager = NewVoiceChatManager()

func GetVoiceChatManager() *VoiceChatManager {
	return vcManager
}

type JoinVoiceChatResult struct {
	AccountID         int64
	Err               error
	NoActiveGroupCall bool
	WrongPeerType     bool
}

const vcMaxRetries = 5
const vcJoinTimeout = 30 * time.Second

var errWrongPeerType = errors.New("resolved peer is not a channel, but InputPeerUser")

// joinVCWithTimeout runs the join with a hard timeout so a hung WebRTC
// connection can never block the caller forever. On timeout the binding is
// stopped to clean up the half-open call.
func joinVCWithTimeout(ubotInstance *ubot.Context, chatID any, media ntgcalls.MediaDescription, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- ubotInstance.Play(chatID, media)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = ubotInstance.Stop(chatID)
		return fmt.Errorf("timed out joining voice chat")
	}
}

// joinVCWithRetry attempts to join up to vcMaxRetries times on timeout errors
// and FLOOD_WAIT errors. An empty MediaDescription joins as a listener (no stream).
func joinVCWithRetry(ubotInstance *ubot.Context, chatID any, media ntgcalls.MediaDescription) error {
	var lastErr error
	for attempt := 1; attempt <= vcMaxRetries; attempt++ {
		err := joinVCWithTimeout(ubotInstance, chatID, media, vcJoinTimeout)
		if err == nil {
			return nil
		}

		lastErr = err

		// Clean up the half-open ntgcalls call so a retry performs a full join.
		_ = ubotInstance.Stop(chatID)

		// FLOOD_WAIT: sleep for the required time + jitter then retry
		if wait := FloodWaitSeconds(err); wait > 0 {
			jitter := time.Duration(rand.Intn(3)) * time.Second
			time.Sleep(time.Duration(wait+1)*time.Second + jitter)
			continue
		}

		// Only retry on timeout (-503 / connection timeout)
		if !isVCTimeout(err) {
			return err
		}
		if attempt < vcMaxRetries {
			time.Sleep(700 * time.Millisecond)
		}
	}
	return lastErr
}

func isVCTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "TIMEOUT") || strings.Contains(msg, "-503")
}

// validateVCPeer makes sure chatID resolves to a group/channel and not a
// personal account — the ntgcalls join would otherwise attempt a private call.
// It returns the parsed numeric chat id (as used by the ntgcalls binding) so
// stream-end callbacks can be matched back to the owning session.
func validateVCPeer(client *telegram.Client, chatID any) (int64, error) {
	peer, err := client.ResolvePeer(chatID)
	if err != nil {
		return 0, err
	}
	var id int64
	switch p := peer.(type) {
	case *telegram.InputPeerChat:
		id = -p.ChatID
	case *telegram.InputPeerChannel:
		id = -1000000000000 - p.ChannelID
	case *telegram.InputPeerUser:
		id = p.UserID
	default:
		return 0, errWrongPeerType
	}
	return id, nil
}

// JoinVoiceChat joins the voice chat in chatID with all clients owned by ownerID.
// ctx is only used to cancel the JOIN OPERATION itself (e.g. if user cancels before
// joining starts). Once a client has successfully joined, its session lifetime is
// independent — it will only leave when LeaveVoiceChat is called or idleSec expires.
func (m *ClientManager) JoinVoiceChat(
	ctx context.Context,
	ownerID int64,
	chatID any,
	idleSec int,
) []JoinVoiceChatResult {
	return m.joinVoiceChats(ctx, ownerID, chatID, idleSec, "", false, 0, 0)
}

// PlayMediaInVoiceChat joins the voice chat in chatID with all clients owned by
// ownerID and immediately starts streaming the given media file (audio/video).
// mediaFile, when non-empty, is the on-disk path backing the media and is
// removed once the session is cleaned up.
func (m *ClientManager) PlayMediaInVoiceChat(
	ctx context.Context,
	ownerID int64,
	chatID any,
	idleSec int,
	mediaFile string,
	isVideo bool,
	durationMs uint64,
) []JoinVoiceChatResult {
	return m.joinVoiceChats(ctx, ownerID, chatID, idleSec, mediaFile, isVideo, durationMs, 0)
}

// PlayWithLoop plays media like Play, but replays the stream up to loopCount
// times after it reaches the end.
func (m *ClientManager) PlayWithLoop(
	ctx context.Context,
	ownerID int64,
	chatID any,
	mediaFile string,
	isVideo bool,
	durationMs uint64,
	loopCount int,
) []JoinVoiceChatResult {
	return m.joinVoiceChats(ctx, ownerID, chatID, 0, mediaFile, isVideo, durationMs, loopCount)
}

// joinVoiceChats is the shared implementation behind JoinVoiceChat and
// PlayMediaInVoiceChat.
func (m *ClientManager) joinVoiceChats(
	ctx context.Context,
	ownerID int64,
	chatID any,
	idleSec int,
	mediaFile string,
	isVideo bool,
	durationMs uint64,
	loopCount int,
) []JoinVoiceChatResult {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	if len(clients) == 0 {
		return []JoinVoiceChatResult{{Err: fmt.Errorf("no clients found for ownerID %d", ownerID)}}
	}

	results := make([]JoinVoiceChatResult, len(clients))
	var wg sync.WaitGroup

	joinCtx, cancelJoin := context.WithCancel(ctx)
	defer cancelJoin()

	joinDelay := calculateJoinDelay(len(clients))

	for i, mc := range clients {
		wg.Add(1)
		go func(idx int, mc *ManagedClient) {
			defer wg.Done()

			if idx > 0 {
				select {
				case <-time.After(joinDelay):
				case <-joinCtx.Done():
					results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: joinCtx.Err()}
					return
				}
			}

			if joinCtx.Err() != nil {
				results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: joinCtx.Err()}
				return
			}

			ubotInstance, err := m.getUbot(mc.Client)
			if err != nil {
				results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: err}
				return
			}

			chatIDNum, err := validateVCPeer(mc.Client, chatID)
			if err != nil {
				if isWrongPeerType(err) {
					cancelJoin()
					results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: err, WrongPeerType: true}
					return
				}
				results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: err}
				return
			}

			callCtx, cancel := context.WithCancel(context.Background())

			media := ntgcalls.MediaDescription{}
			if mediaFile != "" {
				media = buildMediaDescription(mediaFile, isVideo, 0)
			}

			if err := joinVCWithRetry(ubotInstance, chatIDNum, media); err != nil {
				cancel()
				if isNoActiveGroupCall(err) {
					cancelJoin()
					results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: err, NoActiveGroupCall: true}
					return
				}
				if ShouldDisableClient(err) {
					m.DisableClient(mc.OwnerID, mc.AccountID, string(ClassifyError(err)))
				}
				results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: err}
				return
			}

			key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
			sess := &VoiceChatSession{
				Ubot:       ubotInstance,
				ChatID:     chatID,
				ChatIDNum:  chatIDNum,
				OwnerID:    ownerID,
				AccountID:  mc.AccountID,
				MediaFile:  mediaFile,
				IsVideo:    isVideo,
				DurationMs: durationMs,
				loopCount:  loopCount,
				cancel:     cancel,
			}
			sess.startPlayback()

			vcManager.mu.Lock()
			// A fresh play supersedes any parked stopped media for this chat.
			ck := chatKey{ownerID: ownerID, chatIDNum: chatIDNum}
			if sm, ok := vcManager.stopped[ck]; ok {
				delete(vcManager.stopped, ck)
				if sm.MediaFile != "" {
					_ = os.Remove(sm.MediaFile)
				}
			}
			if old, ok := vcManager.sessions[key]; ok && old != sess {
				if old.cancel != nil {
					old.cancel()
				}
				if old.MediaFile != "" && old.MediaFile != sess.MediaFile {
					_ = os.Remove(old.MediaFile)
				}
			}
			vcManager.sessions[key] = sess
			vcManager.mu.Unlock()

			if idleSec > 0 {
				go func() {
					select {
					case <-callCtx.Done():
					case <-time.After(time.Duration(idleSec) * time.Second):
						_ = ubotInstance.Stop(chatIDNum)
						vcManager.mu.Lock()
						delete(vcManager.sessions, key)
						vcManager.mu.Unlock()
						if sess.MediaFile != "" {
							_ = os.Remove(sess.MediaFile)
						}
						cancel()
					}
				}()
			}

			results[idx] = JoinVoiceChatResult{AccountID: mc.AccountID, Err: nil}
		}(i, mc)
	}

	wg.Wait()

	if idleSec == 0 {
		go m.autoRejoinMonitor(ownerID, chatID, mediaFile, isVideo, durationMs, loopCount, joinDelay)
	}

	return results
}

// calculateJoinDelay returns a per-account delay that scales with client count
// to avoid flooding the Telegram API when many accounts join simultaneously.
func calculateJoinDelay(clientCount int) time.Duration {
	switch {
	case clientCount <= 5:
		return 100 * time.Millisecond
	case clientCount <= 20:
		return 250 * time.Millisecond
	case clientCount <= 50:
		return 400 * time.Millisecond
	default:
		return 500 * time.Millisecond
	}
}

// autoRejoinMonitor periodically checks whether all expected sessions for an
// indefinite-duration voice chat are still alive, and re-joins any that dropped.
func (m *ClientManager) autoRejoinMonitor(
	ownerID int64,
	chatID any,
	mediaFile string,
	isVideo bool,
	durationMs uint64,
	loopCount int,
	joinDelay time.Duration,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Verify the voice chat is still wanted — if the owner manually
		// left all sessions, stop monitoring.
		m.mu.RLock()
		clients := m.clients[ownerID]
		m.mu.RUnlock()
		if len(clients) == 0 {
			return
		}

		chatIDNum, err := validateVCPeer(clients[0].Client, chatID)
		if err != nil {
			return
		}

		vcManager.mu.RLock()
		activeCount := 0
		for _, mc := range clients {
			key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
			if _, ok := vcManager.sessions[key]; ok {
				activeCount++
			}
		}
		vcManager.mu.RUnlock()

		expectedCount := len(clients)
		if activeCount >= expectedCount {
			continue
		}

		toRejoin := make([]*ManagedClient, 0, expectedCount-activeCount)
		for _, mc := range clients {
			key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
			vcManager.mu.RLock()
			_, active := vcManager.sessions[key]
			vcManager.mu.RUnlock()
			if !active {
				toRejoin = append(toRejoin, mc)
			}
		}

		if len(toRejoin) == 0 {
			continue
		}

		log.Printf("autoRejoin: owner %d chat %d — %d/%d accounts dropped, rejoining...", ownerID, chatIDNum, len(toRejoin), expectedCount)

		for i, mc := range toRejoin {
			if i > 0 {
				time.Sleep(joinDelay)
			}

			ubotInstance, err := m.getUbot(mc.Client)
			if err != nil {
				log.Printf("autoRejoin: getUbot failed for acc %d: %v", mc.AccountID, err)
				continue
			}

			media := ntgcalls.MediaDescription{}
			if mediaFile != "" {
				media = buildMediaDescription(mediaFile, isVideo, 0)
			}

			if err := joinVCWithRetry(ubotInstance, chatIDNum, media); err != nil {
				log.Printf("autoRejoin: join failed for acc %d: %v", mc.AccountID, err)
				continue
			}

			callCtx, cancel := context.WithCancel(context.Background())
			key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
			sess := &VoiceChatSession{
				Ubot:       ubotInstance,
				ChatID:     chatID,
				ChatIDNum:  chatIDNum,
				OwnerID:    ownerID,
				AccountID:  mc.AccountID,
				MediaFile:  mediaFile,
				IsVideo:    isVideo,
				DurationMs: durationMs,
				loopCount:  loopCount,
				cancel:     cancel,
			}
			sess.startPlayback()

			vcManager.mu.Lock()
			vcManager.sessions[key] = sess
			vcManager.mu.Unlock()

			go func() {
				<-callCtx.Done()
			}()

			log.Printf("autoRejoin: acc %d re-joined chat %d successfully", mc.AccountID, chatIDNum)
		}
	}
}

func (m *ClientManager) LeaveVoiceChat(ownerID int64) []error {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	var errs []error
	for _, mc := range clients {
		key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
		vcManager.mu.Lock()
		sess, ok := vcManager.sessions[key]
		if ok {
			delete(vcManager.sessions, key)
		}
		vcManager.mu.Unlock()

		if ok {
			if sess.cancel != nil {
				sess.cancel()
			}
			if err := sess.Ubot.Stop(sess.ChatID); err != nil {
				errs = append(errs, fmt.Errorf("account %d: %w", mc.AccountID, err))
			}
			if sess.MediaFile != "" {
				_ = os.Remove(sess.MediaFile)
			}
		}
	}
	return errs
}

func (m *ClientManager) ActiveVoiceChats(ownerID int64) []*VoiceChatSession {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	var sessions []*VoiceChatSession
	vcManager.mu.RLock()
	for _, mc := range clients {
		key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
		if sess, ok := vcManager.sessions[key]; ok {
			sessions = append(sessions, sess)
		}
	}
	vcManager.mu.RUnlock()
	return sessions
}

// PlaybackEntry describes one chat where media is currently streaming.
type PlaybackEntry struct {
	ChatIDNum int64
	StartedAt time.Time
}

// Playlist returns the unique chats where the owner is currently playing media,
// ordered by when playback started (oldest first).
func (m *ClientManager) Playlist(ownerID int64) []PlaybackEntry {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	seen := make(map[int64]bool)
	var entries []PlaybackEntry
	vcManager.mu.RLock()
	for _, mc := range clients {
		sess, ok := vcManager.sessions[vcKey{ownerID: ownerID, accountID: mc.AccountID}]
		if !ok || seen[sess.ChatIDNum] {
			continue
		}
		seen[sess.ChatIDNum] = true
		entries = append(entries, PlaybackEntry{ChatIDNum: sess.ChatIDNum, StartedAt: sess.StartedAt()})
	}
	vcManager.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt.Before(entries[j].StartedAt)
	})
	return entries
}

// StopChat stops playback in a single chat, leaves its voice chats and parks
// the media file so the owner can still Replay it.
func (m *ClientManager) StopChat(ownerID int64, chatIDNum int64) []error {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	ck := chatKey{ownerID: ownerID, chatIDNum: chatIDNum}
	var errs []error

	vcManager.mu.Lock()
	for _, mc := range clients {
		key := vcKey{ownerID: ownerID, accountID: mc.AccountID}
		sess, ok := vcManager.sessions[key]
		if !ok || sess.ChatIDNum != chatIDNum {
			continue
		}
		delete(vcManager.sessions, key)
		if sess.cancel != nil {
			sess.cancel()
		}
		if sess.MediaFile != "" {
			if _, exists := vcManager.stopped[ck]; !exists {
				vcManager.stopped[ck] = &StoppedMedia{
					MediaFile:  sess.MediaFile,
					IsVideo:    sess.IsVideo,
					DurationMs: sess.DurationMs,
				}
				scheduleStoppedCleanup(ck, sess.MediaFile)
			}
		}
		if err := sess.Ubot.Stop(sess.ChatIDNum); err != nil {
			errs = append(errs, fmt.Errorf("account %d: %w", mc.AccountID, err))
		}
	}
	vcManager.mu.Unlock()
	return errs
}

// ReplayMedia restarts playback of the most recently stopped media in a chat.
func (m *ClientManager) ReplayMedia(ownerID int64, chatIDNum int64) ([]JoinVoiceChatResult, error) {
	ck := chatKey{ownerID: ownerID, chatIDNum: chatIDNum}

	vcManager.mu.Lock()
	sm, ok := vcManager.stopped[ck]
	if !ok {
		vcManager.mu.Unlock()
		return nil, errors.New("no stopped media found to replay")
	}
	delete(vcManager.stopped, ck)
	mediaFile, isVideo, durationMs, loopCount := sm.MediaFile, sm.IsVideo, sm.DurationMs, sm.LoopCount
	vcManager.mu.Unlock()

	if mediaFile == "" {
		return nil, errors.New("stopped media has no file on disk")
	}
	return m.PlayWithLoop(context.Background(), ownerID, chatIDNum, mediaFile, isVideo, durationMs, loopCount), nil
}

// ClearStoppedMedia deletes any parked media for a chat (e.g. when the owner
// leaves the stop screen without replaying or looping).
func (m *ClientManager) ClearStoppedMedia(ownerID int64, chatIDNum int64) {
	ck := chatKey{ownerID: ownerID, chatIDNum: chatIDNum}
	vcManager.mu.Lock()
	if sm, ok := vcManager.stopped[ck]; ok {
		delete(vcManager.stopped, ck)
		if sm.MediaFile != "" {
			_ = os.Remove(sm.MediaFile)
		}
	}
	vcManager.mu.Unlock()
}

// ResolveChatID resolves chatID to the numeric chat id used internally.
func (m *ClientManager) ResolveChatID(ownerID int64, chatID any) (int64, error) {
	clients := m.GetClients(ownerID)
	if len(clients) == 0 {
		return 0, fmt.Errorf("no clients found for ownerID %d", ownerID)
	}
	return validateVCPeer(clients[0].Client, chatID)
}

// ChatTitle resolves the title of a chat the owner's clients are in.
func (m *ClientManager) ChatTitle(ownerID int64, chatIDNum int64) (string, error) {
	clients := m.GetClients(ownerID)
	if len(clients) == 0 {
		return "", fmt.Errorf("no clients found for ownerID %d", ownerID)
	}
	client := clients[0].Client
	switch {
	case chatIDNum < -1000000000000:
		ch, err := client.GetChannel(-chatIDNum - 1000000000000)
		if err != nil {
			return "", err
		}
		return ch.Title, nil
	case chatIDNum < 0:
		ch, err := client.GetChat(-chatIDNum)
		if err != nil {
			return "", err
		}
		return ch.Title, nil
	default:
		return fmt.Sprintf("chat %d", chatIDNum), nil
	}
}

func isNoActiveGroupCall(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no active group call") ||
		strings.Contains(msg, "is closed") ||
		strings.Contains(msg, "no_active_group_call") ||
		strings.Contains(msg, "groupcall_invalid")
}

func isWrongPeerType(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errWrongPeerType)
}
