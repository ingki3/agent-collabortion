package runtimes

import (
	"context"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// daemon-protocol §3 v0.10.3: the probe's daemon_features is stored as the
// LAST probe sent it — an old daemon re-probing after a downgrade loses
// prompt_cold, and with it the resumed-turn delta (queue.runtimeKnowsPromptCold).
//
// 회귀 주입: Probe 의 UPDATE 에서 daemon_features = $9 를 빼면 첫 확인이, NULL 을
// COALESCE 로 지키면(빈 목록이 지난 값을 못 지우면) 둘째 확인이 FAIL.
func TestProbeStoresDaemonFeaturesAsSent(t *testing.T) {
	pool := testdb.New(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := testdb.Plant(t, pool, now)
	svc := New(pool, clock.NewFake(now), nil, "")
	ctx := context.Background()
	read := func() []string {
		var f []string
		if err := pool.QueryRow(ctx, `SELECT daemon_features FROM runtime WHERE id = $1`, s.RuntimeID).Scan(&f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err := svc.Probe(ctx, s.RuntimeID, contracts.Probe{DaemonVersion: "new", DaemonFeatures: []string{contracts.DaemonFeaturePromptCold}}); err != nil {
		t.Fatal(err)
	}
	if f := read(); len(f) != 1 || f[0] != contracts.DaemonFeaturePromptCold {
		t.Fatalf("daemon_features = %v, want [prompt_cold]", f)
	}
	if err := svc.Probe(ctx, s.RuntimeID, contracts.Probe{DaemonVersion: "old"}); err != nil {
		t.Fatal(err)
	}
	if f := read(); len(f) != 0 {
		t.Fatalf("daemon_features after an old daemon's probe = %v, want empty", f)
	}
}
