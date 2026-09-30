package cardschema

import (
	"os"
	"path/filepath"
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
