package modules

import (
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

var (
	groupBuf   = make(map[int64][]*telegram.NewMessage)
	groupMu    sync.Mutex
	groupTimer = make(map[int64]*time.Timer)
)

func handleMediaCollector(m *telegram.NewMessage) error {
	userID := m.SenderID()
	if !cg.IsListening(userID) {
		return nil
	}

	// back button pressed while waiting — cancel the running process so its
	// goroutine cannot race out a "Timed out" message, then let the specific
	// handler fire (start/access panel/add-client menu).
	if m.Text() == BtnBackToMenu || m.Text() == BtnBackToAccess || m.Text() == BtnAddClientBack {
		ps.cancel(userID)
		return nil
	}
	if m.Text() == "/cancel" {
		cg.StopListening(userID)
		return nil
	}

	groupedID := m.Message.GroupedID

	if groupedID == 0 {
		cg.Send(userID, []*telegram.NewMessage{m})
	} else {
		groupMu.Lock()
		groupBuf[groupedID] = append(groupBuf[groupedID], m)

		if t, ok := groupTimer[groupedID]; ok {
			t.Reset(1 * time.Second)
			groupMu.Unlock()
		} else {
			groupTimer[groupedID] = time.AfterFunc(1*time.Second, func() {
				groupMu.Lock()
				msgs := groupBuf[groupedID]
				delete(groupBuf, groupedID)
				delete(groupTimer, groupedID)
				groupMu.Unlock()

				if len(msgs) > 0 {
					cg.Send(msgs[0].SenderID(), msgs)
				}
			})
			groupMu.Unlock()
		}
	}

	// stop propagation — prevent any group 0 handler from firing
	return telegram.ErrEndGroup
}
