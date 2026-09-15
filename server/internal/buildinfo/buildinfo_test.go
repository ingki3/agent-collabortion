package buildinfo

import (
	"sync"
	"testing"
)

// The linker value wins over the VCS stamp; with neither, Ref is "" rather
// than an invented "main" — the installer prints its fallback itself.
func TestRefPrefersLinkerValue(t *testing.T) {
	ref = "v9.9.9-test"
	once = sync.Once{}
	resolved, modified = "", false
	if got := Ref(); got != "v9.9.9-test" {
		t.Fatalf("Ref() = %q, want the -X value", got)
	}
	ref = ""
	once = sync.Once{}
	resolved = ""
	// `go test` binaries carry no vcs stamp, so this is the "" branch.
	if got := Ref(); got != "" && len(got) < 7 {
		t.Fatalf("Ref() = %q, want a vcs revision or empty", got)
	}
}

func TestGoVersionNumber(t *testing.T) {
	for in, want := range map[string]string{
		"go1.25.1": "1.25.1", "go1.26rc1 X:nocoverageredesign": "1.26", "devel go1.27-abc": "", "": "",
	} {
		if got := goVersionNumber(in); got != want {
			t.Errorf("goVersionNumber(%q) = %q, want %q", in, got, want)
		}
	}
	if got := GoMin(); got == "" {
		t.Fatal("GoMin() = \"\" under `go test` — the toolchain fallback must answer")
	}
}
