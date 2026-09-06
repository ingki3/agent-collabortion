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
