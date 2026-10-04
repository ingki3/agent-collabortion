package colab

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The CLI's ledger enums are openapi's: MemoryKind · MemoryCertainty ·
// MemoryOutcome and listMemory's status query — a contract enum change
// fails here instead of turning a valid value into an exit 2.
func TestMemoryEnumsMatchOpenapi(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	enumOf := func(anchor string) []string {
		t.Helper()
		i := strings.Index(text, anchor)
		if i < 0 {
			t.Fatalf("openapi.yaml has no %q", anchor)
		}
		m := regexp.MustCompile(`enum: \[([^\]]+)\]`).FindStringSubmatch(text[i:])
		if m == nil {
			t.Fatalf("%q has no enum", anchor)
		}
		var out []string
		for _, v := range strings.Split(m[1], ",") {
			out = append(out, strings.TrimSpace(v))
		}
		return out
	}
	for anchor, got := range map[string][]string{
		"\n    MemoryKind:\n":      MemoryKinds,
		"\n    MemoryCertainty:\n": MemoryCertainties,
		"\n    MemoryOutcome:\n":   MemoryOutcomes,
		"operationId: listMemory":  MemoryStatuses,
	} {
		if want := enumOf(anchor); !reflect.DeepEqual(want, got) {
			t.Errorf("%s: openapi %v, CLI %v", strings.TrimSpace(anchor), want, got)
		}
	}
}
