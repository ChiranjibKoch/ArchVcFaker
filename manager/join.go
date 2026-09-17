package manager

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

type JoinChannelResult struct {
	AccountID         int64
	Channel           *telegram.Channel // non-nil only on a confirmed join
	InviteRequestSent bool              // true on INVITE_REQUEST_SENT — request submitted, awaiting approval
	Error             error
}

func isJoinRequestSent(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "INVITE_REQUEST_SENT")
}

// joinChannelWithRetry attempts to join a channel, sleeping and retrying on FLOOD_WAIT.
func joinChannelWithRetry(ctx context.Context, client *telegram.Client, channel any) (*telegram.Channel, error) {
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ch, err := client.JoinChannel(channel)
		if err == nil {
			return ch, nil
		}

		if wait := FloodWaitSeconds(err); wait > 0 {
			time.Sleep(time.Duration(wait+1) * time.Second)
			continue
		}

		return ch, err
	}
	return nil, fmt.Errorf("failed after %d retries (FLOOD_WAIT)", maxRetries)
}

func (m *ClientManager) JoinChannel(ctx context.Context, ownerID int64, channel any) []JoinChannelResult {
	m.mu.RLock()
	clients := m.clients[ownerID]
	m.mu.RUnlock()

	if len(clients) == 0 {
		return []JoinChannelResult{{Error: fmt.Errorf("no clients found for ownerID %d", ownerID)}}
	}

	results := make([]JoinChannelResult, len(clients))
	var wg sync.WaitGroup

	for i, mc := range clients {
		wg.Add(1)
		go func(idx int, mc *ManagedClient) {
			defer wg.Done()
			if ctx.Err() != nil {
				results[idx] = JoinChannelResult{AccountID: mc.AccountID, Error: ctx.Err()}
				return
			}
			ch, err := joinChannelWithRetry(ctx, mc.Client, channel)
			if isJoinRequestSent(err) {
				results[idx] = JoinChannelResult{AccountID: mc.AccountID, InviteRequestSent: true}
				return
			}
			if ShouldDisableClient(err) {
				m.DisableClient(mc.OwnerID, mc.AccountID, string(ClassifyError(err)))
			}
			results[idx] = JoinChannelResult{AccountID: mc.AccountID, Channel: ch, Error: err}
		}(i, mc)
	}

	wg.Wait()
	return results
}
