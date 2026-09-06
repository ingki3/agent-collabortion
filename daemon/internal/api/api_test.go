package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// fakeServer is an httptest daemon API: pairing, one claimable task, and an
// idempotent events store that can be told to fail N times.
type fakeServer struct {
	mu        sync.Mutex
	paired    []PairRequest
	probes    []contracts.Probe
	claims    int
	seqs      map[int]bool
	eventsMax int
	failNext  int
	batches   [][]int
	finish    []FinishRequest
	phases    []PhaseRequest
	hb        []HeartbeatRequest
	hbRaw     [][]byte
	commands  []contracts.Command
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/daemon/pair", func(w http.ResponseWriter, r *http.Request) {
		var req PairRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.paired = append(f.paired, req)
		f.mu.Unlock()
		if req.PairingCode != "good" {
			http.Error(w, `{"error":"invalid_code"}`, 404)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(PairResponse{RuntimeID: "rt_1", DaemonToken: "cdt_secret"})
	})
	auth := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer cdt_secret" }
	mux.HandleFunc("POST /v1/daemon/runtimes/{id}/probe", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			w.WriteHeader(401)
			return
		}
		var p contracts.Probe
		json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		f.probes = append(f.probes, p)
		f.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/daemon/runtimes/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			w.WriteHeader(401)
			return
		}
		var req ClaimRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.claims++
		n := f.claims
		cmds := f.commands
		f.commands = nil
		f.mu.Unlock()
		res := ClaimResponse{Tasks: []contracts.TaskBundle{}, Commands: cmds}
		if n == 1 {
			res.Tasks = append(res.Tasks, contracts.TaskBundle{Task: contracts.BundleTask{ID: "t1", Attempt: 1}, TaskToken: "ctk_1", Prompt: "hi"})
		} else {
			time.Sleep(time.Duration(req.WaitMS) * time.Millisecond) // long-poll
		}
		json.NewEncoder(w).Encode(res)
	})
	mux.HandleFunc("POST /v1/daemon/tasks/{task}/attempts/{n}/phase", func(w http.ResponseWriter, r *http.Request) {
		var req PhaseRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.phases = append(f.phases, req)
		f.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/daemon/tasks/{task}/attempts/{n}/events", func(w http.ResponseWriter, r *http.Request) {
		var req EventsRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failNext > 0 {
			f.failNext--
			http.Error(w, "boom", 503)
			return
		}
		var got []int
		for _, ev := range req.Events {
			got = append(got, ev.Seq)
			f.seqs[ev.Seq] = true
			if ev.Seq > f.eventsMax {
				f.eventsMax = ev.Seq
			}
		}
		f.batches = append(f.batches, got)
		json.NewEncoder(w).Encode(EventsResponse{AcceptedSeqMax: f.eventsMax, Commands: []contracts.Command{{Type: contracts.CmdProbe}}})
	})
	mux.HandleFunc("POST /v1/daemon/tasks/{task}/attempts/{n}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req HeartbeatRequest
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		f.hb = append(f.hb, req)
		f.hbRaw = append(f.hbRaw, raw)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(HeartbeatResponse{Commands: []contracts.Command{{Type: contracts.CmdCancel, TaskID: "t1", Attempt: 1, Reason: "director"}}})
	})
	mux.HandleFunc("POST /v1/daemon/tasks/{task}/attempts/{n}/finish", func(w http.ResponseWriter, r *http.Request) {
		var req FinishRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.finish = append(f.finish, req)
		f.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	return mux
}

func newFake(t *testing.T) (*fakeServer, *httptest.Server) {
	f := &fakeServer{seqs: map[int]bool{}}
	s := httptest.NewServer(f.handler())
	t.Cleanup(s.Close)
	return f, s
}

func TestPairProbeClaimPhaseHeartbeatFinish(t *testing.T) {
	f, s := newFake(t)
	ctx := context.Background()
	anon := New(s.URL, "")
	if _, err := anon.Pair(ctx, PairRequest{PairingCode: "bad"}); err == nil {
		t.Fatal("bad code should fail")
	}
	pr, err := anon.Pair(ctx, PairRequest{PairingCode: "good", Hostname: "h", OS: "darwin", DaemonVersion: "t"})
	if err != nil || pr.RuntimeID != "rt_1" || pr.DaemonToken != "cdt_secret" {
		t.Fatalf("%+v %v", pr, err)
	}
	c := New(s.URL, pr.DaemonToken)
	if err := c.Probe(ctx, pr.RuntimeID, contracts.Probe{DaemonVersion: "t", Capabilities: []contracts.Capability{{Kind: contracts.RuntimeClaudeCode}}, ColabCLI: contracts.ColabCLI{Present: true, Version: "0.1.0"}}); err != nil {
		t.Fatal(err)
	}
	if err := New(s.URL, "wrong").Probe(ctx, "rt_1", contracts.Probe{}); err == nil {
		t.Fatal("wrong token should 401")
	}
	cr, err := c.Claim(ctx, pr.RuntimeID, ClaimRequest{Capacity: 1, WaitMS: 100})
	if err != nil || len(cr.Tasks) != 1 || cr.Tasks[0].TaskToken != "ctk_1" {
		t.Fatalf("%+v %v", cr, err)
	}
	start := time.Now()
	cr, err = c.Claim(ctx, pr.RuntimeID, ClaimRequest{Capacity: 1, WaitMS: 200})
	if err != nil || len(cr.Tasks) != 0 || time.Since(start) < 150*time.Millisecond {
		t.Fatalf("long-poll: %+v %v after %v", cr, err, time.Since(start))
	}
	if err := c.Phase(ctx, "t1", 1, PhaseRequest{Phase: "preparing", PGID: 42, WorkdirPath: "/w"}); err != nil {
		t.Fatal(err)
	}
	hr, err := c.Heartbeat(ctx, "t1", 1, HeartbeatRequest{LastSeq: 3, Preview: &HeartbeatPreview{Text: "par"}})
	if err != nil || len(hr.Commands) != 1 || hr.Commands[0].Type != contracts.CmdCancel {
		t.Fatalf("%+v %v", hr, err)
	}
	if err := c.Finish(ctx, "t1", 1, FinishRequest{Finish: contracts.Finish{Outcome: "completed", StopReason: "end_turn", LastSeq: 3}}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.phases) != 1 || f.phases[0].PGID != 42 || len(f.hb) != 1 || f.hb[0].Preview == nil || f.hb[0].Preview.Text != "par" || len(f.finish) != 1 || f.finish[0].Outcome != "completed" {
		t.Fatalf("server saw phases=%+v hb=%+v finish=%+v", f.phases, f.hb, f.finish)
	}
}

func ev(seq int) contracts.TaskEvent {
	return contracts.TaskEvent{TaskID: "t1", Attempt: 1, Seq: seq, TS: time.Now(), Class: "message", Verb: "say", Outcome: "ok"}
}

// §4.2 — batches ≤100, unacked events resent after a 503, no gaps, and the
// server's dedupe means the resend is harmless (E8-04 daemon half).
func TestBatcherBatchesAndResends(t *testing.T) {
	f, s := newFake(t)
	c := New(s.URL, "cdt_secret")
	ctx := context.Background()
	var cmds []contracts.Command
	var cmu sync.Mutex
	b := NewBatcherWith(ctx, c, "t1", 1, 100, 50*time.Millisecond)
	b.OnCommands = func(cs []contracts.Command) { cmu.Lock(); cmds = append(cmds, cs...); cmu.Unlock() }
	f.mu.Lock()
	f.failNext = 2
	f.mu.Unlock()
	for i := 1; i <= 250; i++ {
		b.Emit(ev(i))
	}
	b.Preview("partial text")
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seqs) != 250 || f.eventsMax != 250 {
		t.Fatalf("server has %d seqs max %d", len(f.seqs), f.eventsMax)
	}
	for _, batch := range f.batches {
		if len(batch) > 100 || len(batch) == 0 {
			t.Fatalf("batch size %d", len(batch))
		}
		for i := 1; i < len(batch); i++ {
			if batch[i] != batch[i-1]+1 {
				t.Fatalf("gap in batch %v", batch)
			}
		}
	}
	if p := b.TakePreview(); b.Unacked() != 0 || b.LastSeq() != 250 || p == nil || p.Text != "partial text" || p.MessageID != "" {
		t.Fatalf("batcher state unacked=%d last=%d", b.Unacked(), b.LastSeq())
	}
	cmu.Lock()
	defer cmu.Unlock()
	if len(cmds) == 0 || cmds[0].Type != contracts.CmdProbe {
		t.Fatalf("commands from events response not forwarded: %+v", cmds)
	}
}

func TestBatcherKeepsPendingWhileServerDown(t *testing.T) {
	c := New("http://127.0.0.1:1", "cdt_secret") // nothing listens
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	b := NewBatcherWith(ctx, c, "t1", 1, 100, 50*time.Millisecond)
	b.Emit(ev(1))
	err := b.Close(ctx)
	if err == nil || !IsNetwork(err) || b.Unacked() != 1 {
		t.Fatalf("err=%v unacked=%d", err, b.Unacked())
	}
	if !strings.Contains(err.Error(), "events") {
		t.Fatalf("err %v", err)
	}
}

// §4.2 v0.3 (G3 C-1): the heartbeat carries `preview` as an object with a
// required `text`, and omits the key entirely when there is no partial
// output — the server used to 422 the whole heartbeat on the old string
// shape, losing a live attempt to re-queueing.
func TestHeartbeatPreviewContractShape(t *testing.T) {
	f, s := newFake(t)
	c := New(s.URL, "cdt_secret")
	ctx := context.Background()
	b := NewBatcherWith(ctx, c, "t1", 1, 100, time.Hour)
	t.Cleanup(func() { b.Close(ctx) })

	// (b) no partial output yet → no `preview` key at all (never {}).
	if p := b.TakePreview(); p != nil {
		t.Fatalf("empty preview should be nil, got %+v", p)
	}
	if _, err := c.Heartbeat(ctx, "t1", 1, HeartbeatRequest{LastSeq: 0, Preview: b.TakePreview()}); err != nil {
		t.Fatal(err)
	}
	// (a) partial output → preview.text is exactly that string.
	b.Preview("hello wor")
	if _, err := c.Heartbeat(ctx, "t1", 1, HeartbeatRequest{LastSeq: 7, Preview: b.TakePreview()}); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hbRaw) != 2 {
		t.Fatalf("want 2 heartbeats, got %d", len(f.hbRaw))
	}
	// (c) the wire bytes unmarshal into the server-side contract shape.
	type serverPreview struct {
		Text      string `json:"text"`
		MessageID string `json:"message_id"`
	}
	type serverHeartbeat struct {
		Usage   map[string]any `json:"usage"`
		LastSeq int            `json:"last_seq"`
		Preview *serverPreview `json:"preview"`
	}
	var keys []map[string]json.RawMessage
	for i, raw := range f.hbRaw {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("heartbeat %d not an object: %v (%s)", i, err, raw)
		}
		keys = append(keys, m)
		var sh serverHeartbeat
		if err := json.Unmarshal(raw, &sh); err != nil {
			t.Fatalf("heartbeat %d does not fit the contract shape: %v (%s)", i, err, raw)
		}
		if i == 1 {
			if sh.Preview == nil || sh.Preview.Text != "hello wor" || sh.Preview.MessageID != "" {
				t.Fatalf("preview: %+v (%s)", sh.Preview, raw)
			}
			if sh.LastSeq != 7 {
				t.Fatalf("last_seq %d", sh.LastSeq)
			}
		}
	}
	if _, ok := keys[0]["preview"]; ok {
		t.Fatalf("no partial output must omit the preview key: %s", f.hbRaw[0])
	}
	if _, ok := keys[1]["preview"]; !ok {
		t.Fatalf("partial output must send a preview: %s", f.hbRaw[1])
	}
}

// daemon-protocol §4.2 v0.5 — preview.message_id belongs to the SERVER; the
// daemon leaves it empty. This test is the ONLY thing standing between us and
// a wrong attribution: acp.Sink no longer has a parameter for an id, and
// HeartbeatPreview.MessageID is `omitempty`, so the day someone gives the
// batcher a non-empty previewMessageID the compiler and the schema both stay
// quiet — the key simply appears on the wire and the server hangs the delta
// off another agent's message. Assert on the bytes, not on the struct.
func TestDaemonNeverFillsPreviewMessageID(t *testing.T) {
	f, s := newFake(t)
	c := New(s.URL, "cdt_secret")
	ctx := context.Background()
	b := NewBatcherWith(ctx, c, "t1", 1, 100, time.Hour)
	t.Cleanup(func() { b.Close(ctx) })

	for _, partial := range []string{"part one ", "part one part two"} {
		b.Preview(partial)
		if _, err := c.Heartbeat(ctx, "t1", 1, HeartbeatRequest{LastSeq: 1, Preview: b.TakePreview()}); err != nil {
			t.Fatal(err)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hbRaw) != 2 {
		t.Fatalf("want 2 heartbeats, got %d", len(f.hbRaw))
	}
	for i, raw := range f.hbRaw {
		var hb struct {
			Preview map[string]json.RawMessage `json:"preview"`
		}
		if err := json.Unmarshal(raw, &hb); err != nil {
			t.Fatalf("heartbeat %d: %v (%s)", i, err, raw)
		}
		if _, ok := hb.Preview["text"]; !ok {
			t.Fatalf("heartbeat %d has no preview.text: %s", i, raw)
		}
		if _, ok := hb.Preview["message_id"]; ok {
			t.Fatalf("heartbeat %d put message_id on the wire — §4.2 v0.5 makes it the server's: %s", i, raw)
		}
	}
}
