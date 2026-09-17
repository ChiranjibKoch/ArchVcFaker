package modules

import (
	"sync"
	"time"

	"github.com/amarnathcjd/gogram/telegram"
)

type ConversationManager struct {
	mu    sync.RWMutex
	state map[int64]chan []*telegram.NewMessage
}

var cg = &ConversationManager{
	state: make(map[int64]chan []*telegram.NewMessage),
}

func (c *ConversationManager) SetListening(userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.state[userID]; !ok {
		c.state[userID] = make(chan []*telegram.NewMessage, 10)
	}
}

func (c *ConversationManager) StopListening(userID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.state[userID]; ok {
		close(ch)
		delete(c.state, userID)
	}
}

func (c *ConversationManager) IsListening(userID int64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.state[userID]
	return ok
}

func (c *ConversationManager) Send(userID int64, msgs []*telegram.NewMessage) {
	c.mu.RLock()
	ch, ok := c.state[userID]
	c.mu.RUnlock()
	if ok {
		select {
		case ch <- msgs:
		default:
		}
	}
}

func (c *ConversationManager) Recv(userID int64, timeout time.Duration) ([]*telegram.NewMessage, bool) {
	c.mu.RLock()
	ch, ok := c.state[userID]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	select {
	case msgs, open := <-ch:
		return msgs, open
	case <-time.After(timeout):
		return nil, false
	}
}
