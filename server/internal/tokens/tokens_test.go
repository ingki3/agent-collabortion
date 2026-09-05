package tokens

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	tok, h := Generate()
	if !strings.HasPrefix(tok, Prefix) || len(tok) != len(Prefix)+43 { // base64url(32B) = 43 chars
		t.Fatalf("token format: %q", tok)
	}
	if h != Hash(tok) || len(h) != 64 {
		t.Fatal("hash mismatch")
	}
	tok2, _ := Generate()
	if tok == tok2 {
		t.Fatal("tokens must be unique")
	}
}
