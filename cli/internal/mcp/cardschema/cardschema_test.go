package cardschema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedMatchesContract is the drift test colab-cli v0.9.10 §3 asks
// for: card_schemas.gen.go is exactly what openapi.yaml generates today.
// 회귀 주입: openapi TaskCardInput 의 goal maxLength 를 바꾸거나 gen 파일을 손으로
// 고치면 FAIL.
func TestGeneratedMatchesContract(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := Generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "card_schemas.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("internal/mcp/card_schemas.gen.go is stale — run `cd cli && go generate ./internal/mcp`")
	}
}

// TestGeneratedMemoryMatchesContract is the ledger tools' drift test
// (colab-cli v0.9.12 §3): memory_schemas.gen.go is exactly what openapi.yaml
// generates today.
// 회귀 주입: openapi MemoryItemInput 의 content maxLength 나 MemoryKind enum 을
// 바꾸거나 gen 파일을 손으로 고치면 FAIL.
func TestGeneratedMemoryMatchesContract(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := GenerateMemory(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "memory_schemas.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("internal/mcp/memory_schemas.gen.go is stale — run `cd cli && go generate ./internal/mcp`")
	}
}

// The generated ledger schemas carry the contract's constraints — the
// MemoryKind enum, content 1..300, certainty's enum-or-null, the required
// lists — so a generator that silently dropped a field would fail here even
// with a freshly regenerated file.
func TestGenerateMemoryShape(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := GenerateMemory(spec)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"\tschemaMemoryNote ", "\tschemaMemorySupersede ", "\tschemaMemoryRetire ", "\tschemaMemoryGet ",
		`\"enum\":[\"fact\",\"assignment\",\"open_question\",\"lesson\",\"plan\",\"progress\"]`,
		`\"enum\":[\"given\",\"to_verify\",\"derived\",\"guess\"]`,
		`\"enum\":[\"dead_end\",\"corrected\",\"useful\"]`,
		`\"enum\":[\"active\",\"superseded\",\"retired\",\"all\"]`,
		`\"maxLength\":300,\"minLength\":1`,
		`\"required\":[\"kind\",\"content\"]`,
		`\"required\":[\"memory\",\"content\"]`,
		`\"required\":[\"memory\",\"reason\"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("generated memory schemas lack %s", want)
		}
	}
}
