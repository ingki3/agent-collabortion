// Package queue is daemon-protocol.md §7: the Queue interface the server
// sees, and its v1 Postgres SELECT … FOR UPDATE SKIP LOCKED implementation.
package queue

import (
	"context"
	"sync"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Queue is the contract interface (§7). `now` is always passed in so tests
// drive time through contracts/clock.
type Queue interface {
	Claim(ctx context.Context, runtimeID string, capacity int, now time.Time) ([]contracts.TaskBundle, error)
	Heartbeat(ctx context.Context, taskID string, attempt int, now time.Time) error
	Requeue(ctx context.Context, taskID string, reason contracts.FailureKind, notBefore *time.Time, now time.Time) error
	ExpireStale(ctx context.Context, now time.Time) (requeued int, err error)
}

// Notifier wakes long-polling claims when a task is queued (E17-01 ≤ 2s).
type Notifier struct {
	mu sync.Mutex
	ch chan struct{}
}

func NewNotifier() *Notifier { return &Notifier{ch: make(chan struct{})} }

// Wait returns a channel closed by the next Notify.
func (n *Notifier) Wait() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// Notify wakes every waiter.
func (n *Notifier) Notify() {
	n.mu.Lock()
	close(n.ch)
	n.ch = make(chan struct{})
	n.mu.Unlock()
}
