package api

import (
	"context"
	"sync"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Batcher ships task_events in batches (≤100 events or 1s, daemon-protocol
// §4.2), keeps everything not yet covered by accepted_seq_max, and resends
// it on the next flush — the server is idempotent on (task, attempt, seq).
// Commands returned by the server are forwarded to OnCommands.
type Batcher struct {
	srv        Server
	taskID     string
	attempt    int
	OnCommands func([]contracts.Command)
	MaxBatch   int
	Interval   time.Duration

	mu       sync.Mutex
	pending  []contracts.TaskEvent // unacked, ascending seq
	acked    int
	preview  string
	lastSeq  int
	kick     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	lastErr  error
}

// NewBatcher starts the flush loop with the §4.2 defaults (100 events / 1s).
func NewBatcher(ctx context.Context, srv Server, taskID string, attempt int) *Batcher {
	return NewBatcherWith(ctx, srv, taskID, attempt, 100, time.Second)
}

// NewBatcherWith starts the flush loop with explicit batch size / interval.
func NewBatcherWith(ctx context.Context, srv Server, taskID string, attempt int, maxBatch int, interval time.Duration) *Batcher {
	b := &Batcher{srv: srv, taskID: taskID, attempt: attempt, MaxBatch: maxBatch, Interval: interval, kick: make(chan struct{}, 1), stopped: make(chan struct{})}
	b.wg.Add(1)
	go b.loop(ctx)
	return b
}

// Emit implements acp.Sink.
func (b *Batcher) Emit(ev contracts.TaskEvent) {
	b.mu.Lock()
	b.pending = append(b.pending, ev)
	if ev.Seq > b.lastSeq {
		b.lastSeq = ev.Seq
	}
	n := len(b.pending)
	b.mu.Unlock()
	if n >= b.MaxBatch {
		b.poke()
	}
}

// Preview implements acp.Sink: the partial message goes out with the next
// heartbeat, never as an event.
func (b *Batcher) Preview(text string) {
	b.mu.Lock()
	b.preview = text
	b.mu.Unlock()
}

// TakePreview returns the latest partial output in the §4.2 v0.3 shape, or
// nil when there is none — the heartbeat then omits `preview` rather than
// sending an empty object. message_id stays unset: the daemon does not know
// the server-side message id.
func (b *Batcher) TakePreview() *HeartbeatPreview {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.preview == "" {
		return nil
	}
	return &HeartbeatPreview{Text: b.preview}
}

// LastSeq is the highest seq emitted so far.
func (b *Batcher) LastSeq() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastSeq
}

// Unacked returns how many events the server has not confirmed.
func (b *Batcher) Unacked() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

func (b *Batcher) poke() {
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

func (b *Batcher) loop(ctx context.Context) {
	defer b.wg.Done()
	t := time.NewTicker(b.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stopped:
			return
		case <-t.C:
		case <-b.kick:
		}
		b.flush(ctx)
	}
}

// flush sends every unacked event (a resend includes older ones the server
// did not confirm yet).
func (b *Batcher) flush(ctx context.Context) {
	for {
		b.mu.Lock()
		if len(b.pending) == 0 {
			b.mu.Unlock()
			return
		}
		n := len(b.pending)
		if n > b.MaxBatch {
			n = b.MaxBatch
		}
		batch := append([]contracts.TaskEvent{}, b.pending[:n]...)
		b.mu.Unlock()
		res, err := b.srv.Events(ctx, b.taskID, b.attempt, batch)
		b.mu.Lock()
		b.lastErr = err
		if err != nil {
			b.mu.Unlock()
			return // keep pending; retry on the next tick
		}
		if res.AcceptedSeqMax > b.acked {
			b.acked = res.AcceptedSeqMax
		}
		kept := b.pending[:0]
		for _, ev := range b.pending {
			if ev.Seq > b.acked {
				kept = append(kept, ev)
			}
		}
		b.pending = kept
		more := len(b.pending) >= b.MaxBatch
		b.mu.Unlock()
		if b.OnCommands != nil && len(res.Commands) > 0 {
			b.OnCommands(res.Commands)
		}
		if !more {
			return
		}
	}
}

// Close flushes until everything is acked or ctx expires, then stops.
func (b *Batcher) Close(ctx context.Context) error {
	b.stopOnce.Do(func() { close(b.stopped) })
	b.wg.Wait()
	for attempt := 0; ; attempt++ {
		b.flush(ctx)
		b.mu.Lock()
		left, err := len(b.pending), b.lastErr
		b.mu.Unlock()
		if left == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
}
