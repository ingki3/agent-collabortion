package httpapi

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// S-14 — the listener has a WriteTimeout now (cmd/server), and the two
// responses meant to outlive it extend their own connection's deadline: an
// artifact download (sized by the body) and the SSE stream (none). These
// tests run the real handler behind a real http.Server whose WriteTimeout is
// far shorter than the responses, the way the bound would bite in
// production.

// boundedServer serves f.srv behind an http.Server with a tiny WriteTimeout.
func (f *p2Fixture) boundedServer(t *testing.T, writeTimeout time.Duration) *client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: f.srv.Handler(), WriteTimeout: writeTimeout, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })
	ts := &httptest.Server{URL: "http://" + ln.Addr().String()}
	return &client{t: t, srv: ts, cookie: f.api.cookie}
}

func TestS14DownloadOutlivesWriteTimeout(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("user_approval")))
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	// Bigger than the loopback socket buffers on both ends: the server has to
	// WAIT for the client for most of the body, which is where the bound bites.
	payload := bytes.Repeat([]byte("본문 조각 0123456789abcdef"), 400000) // ~12 MB
	st, out := f.submit(t, sess, tok, "big.bin", "file", payload)
	if st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")

	if got := DownloadWriteBudget(50 << 20); got < 10*time.Minute || got > 20*time.Minute {
		t.Fatalf("DownloadWriteBudget(50 MB) = %s, want roughly a quarter hour", got)
	}

	c := f.boundedServer(t, 300*time.Millisecond)
	req, _ := http.NewRequest("GET", c.srv.URL+f.p+"/artifacts/"+id+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("download = %d", res.StatusCode)
	}
	// A slow reader: the whole body takes ~1s to drain, three times the
	// listener's bound. Without the per-connection extension the server cuts
	// the connection mid-body and the read fails (or returns short).
	var got []byte
	buf := make([]byte, 256<<10)
	for {
		n, err := io.ReadFull(res.Body, buf)
		got = append(got, buf[:n]...)
		if err == io.EOF || err == io.ErrUnexpectedEOF && len(got) == len(payload) {
			break
		}
		if err != nil {
			t.Fatalf("download cut off after %d of %d bytes: %v — the download must extend the connection's write deadline (S-14)", len(got), len(payload), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %d bytes, want %d intact", len(got), len(payload))
	}
}

func TestS14StreamOutlivesWriteTimeout(t *testing.T) {
	f := newP2Fixture(t)
	c := f.boundedServer(t, 300*time.Millisecond)
	frames, stop := openStream(t, c, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	// Longer than the bound, then an event: a stream still bound by the
	// listener's WriteTimeout is closed by now and the frame never arrives.
	time.Sleep(700 * time.Millisecond)
	sid := mustUUID(t, f.sessionID)
	if err := f.srv.Hub.Publish(t.Context(), f.pool, mustUUID(t, f.wsID), &sid, "session.updated", map[string]any{"id": sid, "status": "active"}); err != nil {
		t.Fatal(err)
	}
	select {
	case fr, ok := <-frames:
		if !ok {
			t.Fatalf("the stream was closed by the listener's WriteTimeout — StreamEvents must clear its connection's write deadline (S-14)")
		}
		if fr.Type != "session.updated" {
			t.Fatalf("frame = %+v, want session.updated", fr)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no frame within 3s after the bound elapsed")
	}
	_ = strings.TrimSpace
	_ = uuid.Nil
}
