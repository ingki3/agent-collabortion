package main

// colab-cli v0.9.1 through the binary's flag surface: the default reply
// position is COLAB_THREAD_ID, --top-level overrides it, and giving
// --reply-to with --top-level is exit 2 with nothing posted.

import (
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func TestMessagePostThreadFlags(t *testing.T) {
	const root = "55555555-5555-4555-8555-555555555555"
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		parent any
	}{
		{"default = the turn's thread", []string{"message", "post", "--body", "답"}, 0, root},
		{"--top-level = main timeline", []string{"message", "post", "--body", "답", "--top-level"}, 0, nil},
		{"--reply-to wins", []string{"message", "post", "--body", "답", "--reply-to", "root-2"}, 0, "root-2"},
		{"both = exit 2", []string{"message", "post", "--body", "답", "--reply-to", "root-2", "--top-level"}, 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clienttest.New(t)
			env := s.Env(t.TempDir())
			env["COLAB_THREAD_ID"] = root
			code, v, stderr := exec(t, env, tc.args...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d (%v %s)", code, tc.code, v, stderr)
			}
			if tc.code != 0 {
				if len(s.Posted) != 0 {
					t.Fatalf("exit %d but a message was posted", code)
				}
				return
			}
			if got := s.Posted[0].Body["parent_id"]; got != tc.parent {
				t.Fatalf("parent_id = %v, want %v", got, tc.parent)
			}
		})
	}
}
