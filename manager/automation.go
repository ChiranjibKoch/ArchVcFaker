package manager

import (
	"context"
	"log"
	"time"

	"tgmultibot/database"
	"tgmultibot/utils"

	"github.com/amarnathcjd/gogram/telegram"
)

// autoReactKey identifies a single message being auto-reacted to,
// shared across every client's independent automationHandler call
// for that message — so they all coordinate via the same
// reactionState instead of each picking blindly.
type autoReactKey struct {
	chatID int64
	msgID  int32
}

// autoReactStates holds one reactionState per in-flight auto-react
// message, expiring on its own after autoReactStateTTL — no manual
// cleanup goroutine needed.
var autoReactStates = utils.NewCache[autoReactKey, *reactionState](2 * time.Minute)

func (m *ClientManager) automationHandler(msg *telegram.NewMessage) error {
	chatID := msg.ChatID()

	reactEnabled := len(database.GetReactOwners(chatID)) > 0
	viewEnabled := len(database.GetViewOwners(chatID)) > 0

	if !reactEnabled && !viewEnabled {
		return nil
	}

	msgID := int32(msg.Message.ID)

	if reactEnabled {
		go func() {
		reactions, err := m.GetAllowedReactions(
			msg.Client,
			chatID,
		)
		if err != nil {
			if ShouldDisableClient(err) {
				log.Printf(
					"auto-react disabled: chat=%d msg=%d err=%v",
					chatID,
					msgID,
					err,
				)
				return
			}
			log.Printf(
				"auto-react skipped: chat=%d msg=%d err=%v",
				chatID,
				msgID,
				err,
			)
			return
		}

			key := autoReactKey{chatID: chatID, msgID: msgID}

			state := autoReactStates.GetOrCreate(key, newReactionState)

			reaction := state.pick(reactions)

			err = msg.Client.SendReaction(
				msg.Peer,
				msgID,
				[]any{reaction},
				true,
			)

			if isReactionsTooMany(err) {
				state.markTooMany()

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

				if fallback, ok := state.pickFallback(ctx); ok {
					reaction = fallback

					err = msg.Client.SendReaction(
						msg.Peer,
						msgID,
						[]any{reaction},
						true,
					)
				}

				cancel()
			}

			if err != nil {
				log.Printf(
					"auto-react failed: chat=%d msg=%d reaction=%s err=%v",
					chatID,
					msgID,
					reaction,
					err,
				)
				return
			}

			state.markUsed(reaction)
		}()
	}

	if viewEnabled {
		go func() {
		_, err := msg.Client.MessagesGetMessagesViews(
			msg.Peer,
			[]int32{msgID},
			true,
		)
		if err != nil {
			if ShouldDisableClient(err) {
				log.Printf(
					"auto-view disabled: chat=%d msg=%d err=%v",
					chatID,
					msgID,
					err,
				)
				return
			}
			log.Printf(
				"auto-view failed: chat=%d msg=%d err=%v",
				chatID,
				msgID,
				err,
			)
		}
		}()
	}

	return nil
}
