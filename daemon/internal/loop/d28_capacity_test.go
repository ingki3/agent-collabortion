package loop

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// concurrencyMeter counts, from the server's side, how many attempts this
// daemon holds at once: claimed and not yet finished. That is the number
// S13's capacity column and the E13-16 denominator assume never exceeds
// `capacity` — the daemon's own `running` map is not the measure, because
// the defect (D-28) is exactly that `running` lagged the claim.
type concurrencyMeter struct {
	mu   sync.Mutex
	open int
	max  int
	over []string
}

func (c *concurrencyMeter) claimed(n int, capacity int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.open += n
	if c.open > c.max {
		c.max = c.open
	}
	if c.open > capacity {
		c.over = append(c.over, fmt.Sprintf("open=%d after a claim of %d", c.open, n))
	}
}

func (c *concurrencyMeter) finished() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.open--
}

// meteredServer wraps memServer so the meter sees every claim's task count
// and every finish, under the server's own lock order (claim hands tasks out
// before returning; finish is recorded before returning).
type meteredServer struct {
	*memServer
	meter    *concurrencyMeter
	capacity int
}

func (m *meteredServer) Claim(ctx context.Context, rt string, req api.ClaimRequest) (api.ClaimResponse, error) {
	res, err := m.memServer.Claim(ctx, rt, req)
	if len(res.Tasks) > 0 {
		m.meter.claimed(len(res.Tasks), m.capacity)
	}
	return res, err
}

func (m *meteredServer) Finish(ctx context.Context, task string, attempt int, req contracts.Finish) error {
	m.meter.finished()
	return m.memServer.Finish(ctx, task, attempt, req)
}

// D-28 — a burst of short turns on capacity 3 never runs more than 3 at
// once, measured from the server's side (claimed − finished).
//
// The window the defect lived in is the attempt's preparation: `git
// worktree add` on a cold repository takes seconds, and until the runner
// existed the attempt was invisible to `free`. The preparer below stands in
// for that stretch with a deliberate 40 ms, long enough that the claim loop
// — which goes straight back to `Claim` after `start` — would otherwise ask
// for a full `capacity` again while the first three are still preparing.
//
// Two windows, two 회귀 주입 (both run, both caught):
//   - preparation: `occupiedLocked` → `return len(d.running) +
//     0*len(d.reserved)` → open=6 after the second claim (20 by the end);
//   - finish: the normal path's `d.reserved[k] = struct{}{}` replaced by the
//     old immediate `slotFreed` nudge → open=4..6 while finishes are in
//     flight.
func TestClaimNeverExceedsCapacityDuringPreparation(t *testing.T) {
	const capacity, turns = 3, 20
	var queue []contracts.TaskBundle
	for i := 0; i < turns; i++ {
		b := bundle(fmt.Sprintf("t%02d", i))
		b.Task.LaneID = fmt.Sprintf("lane%02d", i)
		queue = append(queue, b)
	}
	inner := &memServer{queue: queue}
	meter := &concurrencyMeter{}
	srv := &meteredServer{memServer: inner, meter: meter, capacity: capacity}
	d, _ := newDaemon(t, inner, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}})
	d.Server = srv
	d.Cfg.Capacity = capacity
	d.PrepareWorkdir = func(root string, b contracts.TaskBundle) (string, error) {
		time.Sleep(40 * time.Millisecond)
		return workdir.Prepare(root, b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 60*time.Second, func() bool { return inner.finished() == turns })
	cancel()
	<-done
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if meter.max > capacity {
		t.Fatalf("D-28: %d attempts held at once on capacity %d — %v", meter.max, capacity, meter.over)
	}
	if meter.max < capacity {
		// The burst has to actually fill the daemon, or the assertion above
		// proves nothing about the window.
		t.Fatalf("burst never filled the daemon: max %d of %d", meter.max, capacity)
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	for _, f := range inner.finishes {
		if f.Outcome != "completed" {
			t.Fatalf("finish %+v", f)
		}
	}
}

// D-28 — an attempt that fails before it reaches `running` (the workdir
// preparer says no) gives its reservation back, and the loop parked on
// `free <= 0` is woken: the queue behind it drains without waiting for the
// next probe tick.
func TestReservationReleasedOnEarlyExit(t *testing.T) {
	const capacity = 1
	var queue []contracts.TaskBundle
	for i := 0; i < 4; i++ {
		b := bundle(fmt.Sprintf("e%d", i))
		b.Task.LaneID = fmt.Sprintf("lane%d", i)
		queue = append(queue, b)
	}
	inner := &memServer{queue: queue}
	d, _ := newDaemon(t, inner, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}})
	d.Cfg.Capacity = capacity
	d.ProbeInterval = time.Hour // a leaked reservation would only clear on this tick
	d.PrepareWorkdir = func(root string, b contracts.TaskBundle) (string, error) {
		if b.Task.ID == "e0" || b.Task.ID == "e2" {
			return "", fmt.Errorf("disk full")
		}
		return workdir.Prepare(root, b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 30*time.Second, func() bool { return inner.finished() == 4 })
	cancel()
	<-done
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.reserved) != 0 || len(d.running) != 0 {
		t.Fatalf("slots leaked: reserved=%d running=%d", len(d.reserved), len(d.running))
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	failed, completed := 0, 0
	for _, f := range inner.finishes {
		switch f.Outcome {
		case "failed":
			failed++
		case "completed":
			completed++
		}
	}
	if failed != 2 || completed != 2 {
		t.Fatalf("finishes: failed=%d completed=%d %+v", failed, completed, inner.finishes)
	}
}
