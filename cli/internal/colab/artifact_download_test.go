package colab_test

// Regression tests for the silent truncation R1 found: `artifact get --out`
// used to buffer the body through a 16 MiB io.LimitReader, which returns a
// clean EOF at the cap. A 17 MiB artifact was therefore written to disk at
// 16 MiB and reported as exit 0 with a size_bytes the agent had no reason to
// distrust — and a truncated `diff` artifact that then passes
// `review approve` corrupts the agent_approval completion condition (FR-2.2).
//
// Artifacts are the only cross-lane read (FR-6.1) and `artifact submit`
// accepts 50 MB, so a body past any client read cap is ordinary, not exotic.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// A body larger than any internal read cap must arrive whole. 17 MiB is past
// the old 16 MiB cap; `artifact submit` allows 50 MB, so this is in range.
func TestArtifactGetLargeBodyIsNotTruncated(t *testing.T) {
	const size = 17 << 20
	s := clienttest.New(t)
	s.ArtifactBytes = size
	dest := filepath.Join(t.TempDir(), "big.diff")

	res, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
	if err != nil {
		t.Fatalf("a %d-byte artifact must download: %v", size, err)
	}
	if res.SizeBytes == nil || *res.SizeBytes != size {
		t.Fatalf("size_bytes = %v, want %d", res.SizeBytes, size)
	}
	st, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != size {
		t.Fatalf("wrote %d bytes to disk, want %d — the body was truncated and reported as success", st.Size(), size)
	}
}

// A transfer that ends early must fail loudly and leave nothing behind: a
// half-written file the agent believes is complete is the whole defect.
func TestArtifactGetShortTransferFailsAndRemovesFile(t *testing.T) {
	s := clienttest.New(t)
	s.ArtifactBytes = 4 << 20
	s.ShortWrite = true
	dest := filepath.Join(t.TempDir(), "partial.diff")

	_, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
	if err == nil {
		t.Fatal("a short transfer must not report success")
	}
	if got := client.ExitCode(err); got != client.ExitUnreachable {
		t.Fatalf("exit = %d, want 5 (the transfer failed, not the request)", got)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("--out must not be left holding a partial body (stat err = %v)", err)
	}
	// Nor may a temporary file be left in the destination directory.
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("leftover files in the destination directory: %v", entries)
	}
}

// The download must never be buffered whole in memory, whatever its size.
// A 64 MiB body is past `artifact submit`'s 50 MB ceiling and well past any
// read cap; it has to stream straight to the file.
func TestArtifactGetStreamsRatherThanBuffers(t *testing.T) {
	const size = 64 << 20
	s := clienttest.New(t)
	s.ArtifactBytes = size
	dest := filepath.Join(t.TempDir(), "huge.bin")

	res, err := colab.ArtifactGet(context.Background(), newClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
	if err != nil {
		t.Fatal(err)
	}
	if *res.SizeBytes != size {
		t.Fatalf("size_bytes = %d, want %d", *res.SizeBytes, size)
	}
	st, err := os.Stat(dest)
	if err != nil || st.Size() != size {
		t.Fatalf("on disk = %v bytes (%v), want %d", st.Size(), err, size)
	}
}

// ─────────────────── stalls must end, not hang (R1 follow-up) ───────────────

// Streaming the download fixed the silent truncation but dropped every
// timeout with it: Client.Timeout was a whole-request deadline, so removing
// it removed the only thing that ended a wedged transfer. The bound was doing
// two jobs — capping the size and ending the wait — and only the first should
// have gone.
//
// A stalled `artifact get` inside an agent's turn is not a visible failure:
// nothing is reported until harness §5's 3-minute stall detector cancels the
// attempt. So a body that goes quiet must fail on its own, promptly.
func TestArtifactGetStalledBodyFailsAndRemovesFile(t *testing.T) {
	s := clienttest.New(t)
	s.HangBody = true // headers arrive, then silence, connection left open
	dest := filepath.Join(t.TempDir(), "stalled.diff")

	err := withinDeadline(t, 20*time.Second, func() error {
		_, err := colab.ArtifactGet(context.Background(), shortTimeoutClient(t, s),
			colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
		return err
	})
	if err == nil {
		t.Fatal("a body that never arrives must not report success")
	}
	if got := client.ExitCode(err); got != client.ExitUnreachable {
		t.Fatalf("exit = %d, want 5: %v", got, err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("--out must be left untouched (stat err = %v)", statErr)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 0 {
		t.Fatalf("leftover .part file: %v", entries)
	}
}

// The same for a server that accepts the request and never answers at all:
// the transport's response-header bound has to end it.
func TestArtifactGetNoResponseHeadersFails(t *testing.T) {
	s := clienttest.New(t)
	s.HangHeaders = true
	dest := filepath.Join(t.TempDir(), "never.diff")

	err := withinDeadline(t, 20*time.Second, func() error {
		_, err := colab.ArtifactGet(context.Background(), shortTimeoutClient(t, s),
			colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
		return err
	})
	if err == nil {
		t.Fatal("a server that never responds must not report success")
	}
	if got := client.ExitCode(err); got != client.ExitUnreachable {
		t.Fatalf("exit = %d, want 5: %v", got, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 0 {
		t.Fatalf("leftover file: %v", entries)
	}
}

// A download slower than the idle bound but never actually idle must still
// succeed — the bound is on silence, not on total duration, because artifact
// sizes vary and a whole-transfer deadline is what truncated large ones.
func TestArtifactGetSlowButProgressingSucceeds(t *testing.T) {
	s := clienttest.New(t)
	s.ArtifactBytes = 2 << 20
	s.ChunkDelay = 40 * time.Millisecond // ~1.3s total, idle bound is 300ms
	dest := filepath.Join(t.TempDir(), "slow.diff")

	res, err := colab.ArtifactGet(context.Background(), shortTimeoutClient(t, s),
		colab.ArtifactGetArgs{Artifact: clienttest.ArtifactID, Out: dest})
	if err != nil {
		t.Fatalf("a steadily-progressing transfer must not be cut off: %v", err)
	}
	if *res.SizeBytes != 2<<20 {
		t.Fatalf("size_bytes = %d, want %d", *res.SizeBytes, 2<<20)
	}
}

// shortTimeoutClient points at the fake with a 300ms bound, so a stall test
// finishes quickly instead of waiting out the 30s default.
func shortTimeoutClient(t *testing.T, s *clienttest.Server) *client.Client {
	t.Helper()
	cfg := client.FromEnv(clienttest.Getenv(s.Env(t.TempDir())))
	cfg.Timeout = 300 * time.Millisecond
	return client.New(cfg)
}

// withinDeadline runs fn and fails the test if it has not returned in time —
// so a regression shows up as a failure rather than a hung test binary.
func withinDeadline(t *testing.T, d time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("did not return within %s — the call is unbounded", d)
		return nil
	}
}
