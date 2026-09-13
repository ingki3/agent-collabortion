package acp_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// S-66 회귀 — 3분 넘게 session/update 도 권한 요청도 없이 **원시 스트림만**
// 오는 턴(모델이 긴 Write 입력을 생성하는 중)은 stall 이 아니다.
//
// 페이크는 실기 관측(2026-09-13)의 모양 그대로 input_json_delta 를 50ms 마다
// 보내고 그 사이에 아무 session/update 도 보내지 않는다. 테스트는 페이크
// 클럭을 2분씩 세 번 돌려 총 6분을 흘린다 — 원시 스트림을 활동으로 세지
// 않으면(onRawSDK 의 noteActivity 를 빼면) 두 번째 Advance 에서 stall 이 난다.
func TestS66RawStreamIsActivity(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC))
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{Chunk: "writing"},
		{RawDeltas: &acpfake.RawDeltasStep{Count: 60, IntervalMs: 50}}, // ~3s of real time, deltas only
		{Chunk: "done"},
	}}}}
	var mu sync.Mutex
	var logs []string
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) {
		a.Clock = clk
		a.RawSDKMessages = true
		a.Log = func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf(format, args...))
			mu.Unlock()
		}
	})
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.run(); close(done) }()
	waitFor(t, func() bool { return f.sink.nPreviews() > 0 })
	// Three advances of 2 minutes each while the deltas keep arriving: the
	// stall watch sees >3 minutes of fake time with zero session/update.
	for i := 0; i < 3; i++ {
		time.Sleep(400 * time.Millisecond)
		clk.Advance(2 * time.Minute)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("turn did not finish")
	}
	if res.Outcome != "completed" || res.Failure != nil {
		t.Fatalf("raw-stream-only turn judged %s %+v — S-66", res.Outcome, res.Failure)
	}
	if res.Text != "writingdone" {
		t.Fatalf("text %q", res.Text)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	// (a): the log names what the watch counts, once per turn.
	if !strings.Contains(joined, "stall watch armed limit=3m0s counts=session/update,request_permission,raw:_claude/sdkMessage(on)") {
		t.Fatalf("stall watch did not say what it counts:\n%s", joined)
	}
	if strings.Contains(joined, "stall fired") {
		t.Fatalf("stall fired on a live turn:\n%s", joined)
	}
}

// The other half of S-66: with the raw stream OFF the same turn is silence
// to the daemon, and after 3 minutes it is (correctly, per the evidence it
// has) a stall — which is why claude_code now asks for the stream on every
// attempt (loop.usageMidturn, D-18 tier 2 removed). The firing line carries
// the ledger, so the log says WHAT was counted (NN3).
func TestS66WithoutRawStreamStalls(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC))
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{Chunk: "writing"},
		{RawDeltas: &acpfake.RawDeltasStep{Count: 400, IntervalMs: 50}}, // 20s real time — longer than the watch needs
		{Chunk: "done"},
	}}}}
	var mu sync.Mutex
	var logs []string
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) {
		a.Clock = clk
		a.RawSDKMessages = false
		a.Log = func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf(format, args...))
			mu.Unlock()
		}
	})
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.run(); close(done) }()
	waitFor(t, func() bool { return f.sink.nPreviews() > 0 })
	time.Sleep(300 * time.Millisecond)
	clk.Advance(2 * time.Minute)
	time.Sleep(300 * time.Millisecond)
	clk.Advance(2 * time.Minute)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("turn did not finish")
	}
	if res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailStall {
		t.Fatalf("expected stall with the stream off, got %s %+v", res.Outcome, res.Failure)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "stall fired idle=") || !strings.Contains(joined, "counted=") || !strings.Contains(joined, "session/update:agent_message_chunk=1") {
		t.Fatalf("stall firing line missing or without ledger:\n%s", joined)
	}
	if !strings.Contains(joined, "raw:_claude/sdkMessage(off)") {
		t.Fatalf("armed line should say the raw stream is off:\n%s", joined)
	}
}
