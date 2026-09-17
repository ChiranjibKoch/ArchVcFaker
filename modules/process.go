package modules

import (
	"context"
	"sync"
)

type processEntry struct {
	name   string
	cancel context.CancelFunc
}

type processState struct {
	mu     sync.Mutex
	active map[int64]*processEntry
}

var ps = &processState{
	active: make(map[int64]*processEntry),
}

// tryStart registers a new process for the user and returns a context.
// Returns nil context if a process is already running.
func (p *processState) tryStart(userID int64, name string) (context.Context, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.active[userID]; ok {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.active[userID] = &processEntry{name: name, cancel: cancel}
	return ctx, true
}

// current returns the active process name, or "" if none.
func (p *processState) current(userID int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.active[userID]; ok {
		return e.name
	}
	return ""
}

// done cancels context and removes the process entry.
func (p *processState) done(userID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.active[userID]; ok {
		e.cancel()
		delete(p.active, userID)
	}
}

// cancel stops listening, cancels context, removes entry.
func (p *processState) cancel(userID int64) {
	cg.StopListening(userID)
	p.done(userID)
}
