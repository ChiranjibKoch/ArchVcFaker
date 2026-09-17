package manager

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"tgmultibot/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

var freeReactions = []string{
	"❤",
	"👍",
	"🔥",
	"🥰",
	"👏",
	"😁",
	"🤔",
	"🎉",
	"🤩",
	"🙏",
	"👌",
	"🕊",
	"😍",
	"❤‍🔥",
	"💯",
	"🤣",
	"⚡",
	"🏆",
	"🤓",
	"👀",
	"😇",
	"🤗",
	"🫡",
	"💘",
	"😘",
	"😎",
}

var blockedReactions = map[string]struct{}{
	"👎": {},
	"🤮": {},
	"💩": {},
	"😢": {},
	"😭": {},
	"😡": {},
	"🤬": {},
	"😱": {},
	"😨": {},
	"🤯": {},
}

var allowedReactionsCache = utils.NewCache[int64, []string](3 * time.Hour)

func (m *ClientManager) GetAllowedReactions(
	client *telegram.Client,
	peer any,
) ([]string, error) {
	resolvedPeer, err := client.ResolvePeer(peer)
	if err != nil {
		return nil, err
	}

	inPeer, ok := resolvedPeer.(*telegram.InputPeerChannel)
	if !ok {
		return nil, fmt.Errorf(
			"resolved peer is %T, not InputPeerChannel",
			resolvedPeer,
		)
	}

	cacheKey := inPeer.ChannelID

	if reactions, ok := allowedReactionsCache.Get(cacheKey); ok {
		return reactions, nil
	}

	full, err := client.ChannelsGetFullChannel(
		&telegram.InputChannelObj{
			ChannelID:  inPeer.ChannelID,
			AccessHash: inPeer.AccessHash,
		},
	)
	if err != nil {
		if wait := FloodWaitSeconds(err); wait > 0 {
			time.Sleep(time.Duration(wait+1) * time.Second)
			full, err = client.ChannelsGetFullChannel(
				&telegram.InputChannelObj{
					ChannelID:  inPeer.ChannelID,
					AccessHash: inPeer.AccessHash,
				},
			)
			if err == nil {
				goto processFull
			}
		}
		if ShouldDisableClient(err) {
			return nil, fmt.Errorf("account disabled: %s", ReasonHint(ClassifyError(err)))
		}
		return nil, err
	}

processFull:
	var available telegram.ChatReactions

	switch chat := full.FullChat.(type) {
	case *telegram.ChannelFull:
		available = chat.AvailableReactions

	case *telegram.ChatFullObj:
		available = chat.AvailableReactions

	default:
		return nil, fmt.Errorf(
			"unsupported full chat type %T",
			chat,
		)
	}

	switch reactions := available.(type) {
	case *telegram.ChatReactionsAll:
		allowedReactionsCache.Set(cacheKey, freeReactions)
		return freeReactions, nil

	case *telegram.ChatReactionsNone:
		return nil, fmt.Errorf("reactions are disabled")

	case *telegram.ChatReactionsSome:
		var allowed []string

		for _, r := range reactions.Reactions {
			emoji, ok := r.(*telegram.ReactionEmoji)
			if !ok {
				continue
			}

			if _, blocked := blockedReactions[emoji.Emoticon]; blocked {
				continue
			}

			allowed = append(allowed, emoji.Emoticon)
		}

		if len(allowed) == 0 {
			return nil, fmt.Errorf("no usable reactions available")
		}

		allowedReactionsCache.Set(cacheKey, allowed)
		return allowed, nil
	}

	return nil, fmt.Errorf(
		"unknown reaction type %T",
		available,
	)
}

// reactionState tracks which distinct reactions have already landed
// successfully on a message during a single SendReactions call, so
// that once Telegram's per-message distinct-reaction cap is hit
// (REACTIONS_TOO_MANY), remaining clients fall back to repeating an
// already-used reaction instead of trying a new distinct one.
type reactionState struct {
	mu      sync.Mutex
	used    []string
	usedSet map[string]struct{}
	tooMany bool
}

func newReactionState() *reactionState {
	return &reactionState{usedSet: make(map[string]struct{})}
}

func (s *reactionState) markUsed(r string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.usedSet[r]; ok {
		return
	}

	s.usedSet[r] = struct{}{}
	s.used = append(s.used, r)
}

func (s *reactionState) markTooMany() {
	s.mu.Lock()
	s.tooMany = true
	s.mu.Unlock()
}

// pick returns a candidate reaction to try. Once the cap has been
// hit, it only returns previously-successful reactions.
func (s *reactionState) pick(all []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tooMany && len(s.used) > 0 {
		return s.used[rand.Intn(len(s.used))]
	}

	return all[rand.Intn(len(all))]
}

// pickFallback waits briefly for at least one reaction to have
// succeeded (handles the race where multiple clients hit
// REACTIONS_TOO_MANY before any of them has recorded a success),
// then returns a random one from the used set.
func (s *reactionState) pickFallback(ctx context.Context) (string, bool) {
	for {
		s.mu.Lock()
		if len(s.used) > 0 {
			r := s.used[rand.Intn(len(s.used))]
			s.mu.Unlock()
			return r, true
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func isReactionsTooMany(err error) bool {
	return err != nil && strings.Contains(err.Error(), "REACTIONS_TOO_MANY")
}

func (m *ClientManager) SendReactions(
	ctx context.Context,
	ownerID int64,
	peer any,
	msgID int32,
) []error {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	if len(clients) == 0 {
		return []error{
			fmt.Errorf("no clients found for owner %d", ownerID),
		}
	}

	var (
		reactions []string
		lastErr   error
	)

	for _, mc := range clients {
		rxns, err := m.GetAllowedReactions(
			mc.Client,
			peer,
		)

		if err != nil {
			msg := err.Error()

			if msg == "reactions are disabled" ||
				msg == "no usable reactions available" ||
				strings.Contains(msg, "not InputPeerChannel") ||
				strings.Contains(msg, "unsupported full chat type") {

				log.Printf(
					"GetAllowedReactions terminal error for account=%d err=%v",
					mc.AccountID,
					err,
				)

				return []error{err}
			}

			lastErr = err

			log.Printf(
				"GetAllowedReactions failed for account=%d err=%v",
				mc.AccountID,
				err,
			)

			continue
		}

		reactions = rxns
		break
	}

	if len(reactions) == 0 {
		if lastErr != nil {
			return []error{lastErr}
		}

		return []error{
			fmt.Errorf("failed to fetch allowed reactions"),
		}
	}

	state := newReactionState()

	for _, mc := range clients {

		msg, err := mc.Client.GetMessageByID(peer, msgID)

		if err != nil {

			log.Printf("SendReactions: seed fetch failed account=%d err=%v", mc.AccountID, err)

			break

		}

		if msg.Message.Reactions == nil {

			break

		}

		for _, r := range msg.Message.Reactions.Results {

			emoji, ok := r.Reaction.(*telegram.ReactionEmoji)

			if !ok {

				continue

			}

			state.markUsed(emoji.Emoticon)

		}

		break

	}
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)

	for _, mc := range clients {
		if ctx.Err() != nil {
			break
		}

		mc := mc

		wg.Add(1)

		go func() {
			defer wg.Done()

			reaction := state.pick(reactions)

			err := mc.Client.SendReaction(
				peer,
				msgID,
				[]any{reaction},
				true,
			)

			if isReactionsTooMany(err) {
				state.markTooMany()

				if fallback, ok := state.pickFallback(ctx); ok {
					reaction = fallback

					err = mc.Client.SendReaction(
						peer,
						msgID,
						[]any{reaction},
						true,
					)
				}
			}

			if err != nil {
				log.Printf(
					"SendReaction failed: owner=%d account=%d msg=%d reaction=%s err=%v",
					ownerID,
					mc.AccountID,
					msgID,
					reaction,
					err,
				)

				errsMu.Lock()
				errs = append(errs, fmt.Errorf("account %d: %w", mc.AccountID, err))
				errsMu.Unlock()

				return
			}

			state.markUsed(reaction)
		}()
	}

	wg.Wait()

	return errs
}
