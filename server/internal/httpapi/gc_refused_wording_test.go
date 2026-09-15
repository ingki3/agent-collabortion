package httpapi

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestGCRefusedNoteMatchesContract — daemon-protocol §6 pins the feed sentence
// for a refused gc (v0.7.4) and e2e/p3/57 greps the same words from
// task_event; the constant here has to be that sentence, not a paraphrase.
func TestGCRefusedNoteMatchesContract(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/daemon-protocol.md")
	if err != nil {
		t.Skipf("contract not reachable from this checkout: %v", err)
	}
	// §6: 서버는 피드에 **"<sentence>: <reason>"** 을 남기고 …
	re := regexp.MustCompile(`서버는 피드에 \*\*"([^"]+): <reason>"\*\*`)
	m := re.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("daemon-protocol §6 no longer carries the gc refusal sentence in the expected shape")
	}
	if want := m[1] + ": "; gcRefusedNote != want {
		t.Fatalf("gcRefusedNote = %q, contract §6 says %q", gcRefusedNote, want)
	}
	e2e, err := os.ReadFile("../../../e2e/p3/57_p4_event_schema_and_rebind_prompt_smoke.sh")
	if err != nil {
		t.Skipf("e2e/p3/57 not reachable: %v", err)
	}
	if !strings.Contains(string(e2e), "LIKE '"+gcRefusedNote[:len(gcRefusedNote)-1]+"%'") {
		t.Fatalf("e2e/p3/57 does not grep %q — change contract · server · e2e together", gcRefusedNote)
	}
}
