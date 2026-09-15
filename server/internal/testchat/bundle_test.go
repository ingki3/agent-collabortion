package testchat

// S-71 (PR #200 review NN1 · NN3): the pure functions of this package, without
// a database. NN1's finding was that the runtime_kind guard in bundleResume
// could be turned off (`if false`) and every package stayed green — the
// integration tests only measure the resume that IS carried, never the one
// that must NOT be (E8-08: a ref from another runtime kind is not loadable).

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

func storedRef(t *testing.T, kind contracts.RuntimeKind, sessionID string) []byte {
	t.Helper()
	raw, err := json.Marshal(contracts.RuntimeSessionRef{
		RuntimeKind: kind, AdapterVersion: "1.0.0", SessionID: sessionID, CWD: "/w", CreatedAt: time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBundleResume(t *testing.T) {
	t.Run("same kind → the stored ref rides along", func(t *testing.T) {
		got := bundleResume(storedRef(t, contracts.RuntimeClaudeCode, "sess-1"), "claude_code")
		if got == nil || got.SessionID != "sess-1" || got.RuntimeKind != contracts.RuntimeClaudeCode || got.CWD != "/w" {
			t.Fatalf("resume = %+v", got)
		}
	})
	t.Run("another kind → nil (E8-08, NN1)", func(t *testing.T) {
		// The chat was answered by Claude Code last turn; the profile now runs
		// on Hermes. Hermes cannot session/load a Claude Code id.
		if got := bundleResume(storedRef(t, contracts.RuntimeClaudeCode, "sess-1"), "hermes"); got != nil {
			t.Fatalf("a claude_code ref was handed to hermes: %+v", got)
		}
		if got := bundleResume(storedRef(t, contracts.RuntimeHermes, "sess-2"), "claude_code"); got != nil {
			t.Fatalf("a hermes ref was handed to claude_code: %+v", got)
		}
	})
	t.Run("nothing stored → nil", func(t *testing.T) {
		if bundleResume(nil, "claude_code") != nil || bundleResume([]byte{}, "claude_code") != nil {
			t.Fatal("empty column produced a resume")
		}
	})
	t.Run("unreadable or id-less ref → nil, not a crash", func(t *testing.T) {
		if bundleResume([]byte(`{not json`), "claude_code") != nil {
			t.Fatal("garbage produced a resume")
		}
		if bundleResume([]byte(`{"runtime_kind":"claude_code","session_id":""}`), "claude_code") != nil {
			t.Fatal("empty session_id produced a resume")
		}
		if bundleResume([]byte(`null`), "claude_code") != nil {
			t.Fatal("JSON null produced a resume")
		}
	})
}

// TestWorkdirPath pins the §4.5 shape `<workdir_root>/.colab/testchat/<id>` —
// the daemon deletes on gc only under `.colab/testchat/`, so this is contract.
func TestWorkdirPath(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	if got := WorkdirPath("/Users/a/colab", id); got != "/Users/a/colab/.colab/testchat/11111111-2222-3333-4444-555555555555" {
		t.Fatalf("path = %s", got)
	}
	if got := WorkdirPath("/root/", id); got != "/root/.colab/testchat/11111111-2222-3333-4444-555555555555" {
		t.Fatalf("trailing slash not normalised: %s", got)
	}
}

// TestCurrent is the (id, attempt) check every daemon report goes through.
func TestCurrent(t *testing.T) {
	r := &Row{TurnNo: 3, TurnStatus: TurnRunning}
	if err := current(r, 3); err != nil {
		t.Fatalf("current turn refused: %v", err)
	}
	if err := current(r, 2); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("earlier turn: %v, want ErrStaleAttempt", err)
	}
	if err := current(r, 4); !errors.Is(err, ErrStaleAttempt) {
		t.Fatalf("never-issued turn: %v, want ErrStaleAttempt", err)
	}
	for _, st := range []string{TurnIdle, TurnQueued} {
		if err := current(&Row{TurnNo: 3, TurnStatus: st}, 3); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s: %v, want ErrInvalidTransition", st, err)
		}
	}
	for _, st := range []string{TurnDispatched, TurnPreparing, TurnRunning} {
		if err := current(&Row{TurnNo: 3, TurnStatus: st}, 3); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	if (&Row{TurnStatus: TurnIdle}).InFlight() || !(&Row{TurnStatus: TurnQueued}).InFlight() {
		t.Fatal("InFlight is 'anything but idle'")
	}
}

// TestFailureText: every failure_kind has a sentence in the screens' language
// (Hangul, no internal nouns), and the sentences differ — a person must be
// able to tell an expired login from a lost network from the text alone.
func TestFailureText(t *testing.T) {
	hangul := regexp.MustCompile(`[가-힣]`)
	banned := regexp.MustCompile(`(?i)\b(runtime|task|attempt|lane|workdir|stall|HITL)\b`)
	seen := map[string]contracts.FailureKind{}
	for _, k := range []contracts.FailureKind{
		contracts.FailAuth, contracts.FailQuota, contracts.FailRateLimited, contracts.FailConfig, contracts.FailNetwork,
		contracts.FailRuntimeOffline, contracts.FailStall, contracts.FailTimeout, contracts.FailCancelled, contracts.FailOther,
		contracts.FailureKind("never-heard-of"),
	} {
		s := FailureText(k)
		if !hangul.MatchString(s) || banned.MatchString(s) {
			t.Errorf("%s: %q is not in the user's words", k, s)
		}
		if prev, dup := seen[s]; dup && k != contracts.FailOther && prev != contracts.FailOther {
			t.Errorf("%s and %s share a sentence: %q", k, prev, s)
		}
		seen[s] = k
	}
	if FailureText(contracts.FailOther) != FailureText("never-heard-of") {
		t.Error("an unknown kind should read like `other`")
	}
}

func TestUsageHelpers(t *testing.T) {
	u := usageFromPayload(map[string]any{"input_tokens": 12, "output_tokens": 3, "cost_usd": 0.5, "ignored": "x"})
	if u.InputTokens != 12 || u.OutputTokens != 3 || u.CostUSD != 0.5 {
		t.Fatalf("usage = %+v", u)
	}
	if usageEmpty(u) || !usageEmpty(contracts.Usage{}) || !usageEmpty(usageFromPayload(nil)) {
		t.Fatal("usageEmpty")
	}
	if !usageEmpty(usageFromPayload(map[string]any{"input_tokens": "twelve"})) {
		t.Fatal("a mistyped payload should read as empty, not panic")
	}
}
