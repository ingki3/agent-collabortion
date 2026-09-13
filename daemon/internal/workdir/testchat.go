package workdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// A test chat (daemon-protocol v0.8 §4.5) runs its turns in a temporary
// directory under `<workdir_root>/.colab/testchat/<test_chat_id>` — never a
// checkout, never a lane folder, never in the §6 list. Three operations and
// one guard live here: prepare (mkdir -p), remove (the server's `gc`), the
// start-up sweep (§4.5 "방어"), and "is this path one of ours" — which every
// one of them asks before touching the disk, because the path comes off the
// wire.

// TestChatMaxAge is §4.5 (g): a test chat directory older than this at
// daemon start is removed without a gc — the server that owed the gc died.
const TestChatMaxAge = 24 * time.Hour

// TestChatDir is `<root>/.colab/testchat`, the one directory test chats live in.
func TestChatDir(root string) string { return filepath.Join(root, ".colab", "testchat") }

// TestChatPath is the §4.5 default when the bundle names no path.
func TestChatPath(root, testChatID string) string {
	return filepath.Join(TestChatDir(root), safe(testChatID))
}

// ErrNotTestChatDir is the refusal reason for a path that is not directly
// under `<root>/.colab/testchat/`. It is a gc receipt `reason`, so it is short.
var ErrNotTestChatDir = errors.New("not under .colab/testchat of this computer's work folder")

// underTestChatDir reports whether p is `<root>/.colab/testchat/<one segment>`
// — a chat's directory itself, not the parent and not something deeper, and
// with symlinks resolved on both sides (realPath, T-D10b) so a link out of
// the root cannot pass as inside it.
func underTestChatDir(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	base := realPath(TestChatDir(root))
	abs := realPath(p)
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "..") {
		return false
	}
	return !strings.Contains(rel, string(filepath.Separator))
}

// PrepareTestChat is §4.5 (b): the bundle path (or the default) must be a
// test chat directory of THIS root, and is created with mkdir -p. Same
// signature as Prepare so the loop can pick either.
//
// It does not touch the workdir index (§6): the directory is not a workdir
// row, and a sidecar for it would make `Describe` invent a session for it.
func PrepareTestChat(root string, b contracts.TaskBundle) (string, error) {
	if root == "" {
		return "", errors.New("workdir: empty root")
	}
	id := b.Task.TestChatID
	if id == "" {
		id = b.Task.ID
	}
	path := ResolvePath(root, b.Workdir.Path)
	if path == "" {
		path = TestChatPath(root, id)
	}
	if !underTestChatDir(root, path) {
		return "", fmt.Errorf("test chat directory %q is not under %s", path, TestChatDir(root))
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// RemoveTestChat is §4.5 (f): rm -rf, but only of a test chat directory of
// this root. A refusal is an error the caller puts on the gc receipt.
func RemoveTestChat(root, p string) error {
	if !underTestChatDir(root, p) {
		return fmt.Errorf("refusing to remove %q: %w", p, ErrNotTestChatDir)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	return os.RemoveAll(abs)
}

// SweepTestChats is §4.5 (g): remove every test chat directory whose newest
// file (or the directory itself, when empty) is older than maxAge as of now.
// Returns the paths removed. A missing `.colab/testchat` is nothing to do.
func SweepTestChats(root string, now time.Time, maxAge time.Duration) []string {
	entries, err := os.ReadDir(TestChatDir(root))
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(TestChatDir(root), e.Name())
		_, last := DiskUsage(p)
		if last.IsZero() {
			if fi, err := os.Stat(p); err == nil {
				last = fi.ModTime()
			}
		}
		if last.IsZero() || now.Sub(last) < maxAge {
			continue
		}
		if err := RemoveTestChat(root, p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}
