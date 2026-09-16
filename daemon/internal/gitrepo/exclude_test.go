package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "daemon@test"},
		{"config", "user.name", "daemon test"},
	} {
		if _, err := Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Two of our patterns share .git/info/exclude with different lifetimes: the
// brief's entry is released at lane end, the workdir name tag's (K-14) stays
// for the checkout's life. Releasing one must leave the other REGISTERED AND
// BRACKETED — a bare line the next release would not recognise as ours is a
// line a person has to clean up by hand.
func TestReleasingOnePatternKeepsTheOtherBracketed(t *testing.T) {
	repo := tempRepo(t)
	p, _ := excludePath(repo)
	seed, _ := os.ReadFile(p) // git's own comment header, if this git writes one
	if err := ExcludeEnsure(repo, ".colab-workdir.json"); err != nil {
		t.Fatal(err)
	}
	if err := ExcludeEnsure(repo, "COLAB_BRIEF.md"); err != nil {
		t.Fatal(err)
	}
	if err := ExcludeRelease(repo, "COLAB_BRIEF.md", nil); err != nil {
		t.Fatal(err)
	}
	if ExcludeHas(repo, "COLAB_BRIEF.md") {
		t.Errorf("released pattern still registered")
	}
	if !ExcludeHas(repo, ".colab-workdir.json") {
		t.Errorf("the sibling pattern was released too")
	}
	body, _ := os.ReadFile(p)
	want := string(seed) + excludeStart + "\n.colab-workdir.json\n" + excludeEnd + "\n"
	if string(body) != want {
		t.Errorf("exclude file = %q, want the sibling's block intact %q", body, want)
	}
	// Releasing the last one leaves the file as git seeded it.
	if err := ExcludeRelease(repo, ".colab-workdir.json", nil); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(p)
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(seed)) {
		t.Errorf("exclude file = %q after releasing everything, want git's seed %q", body, seed)
	}
}

// A person's own lines, and a bare pattern an older daemon wrote without
// brackets, are handled as before: theirs stay, ours goes.
func TestReleaseLeavesHumanLinesAndStripsBareLegacyLine(t *testing.T) {
	repo := tempRepo(t)
	p, _ := excludePath(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("*.swp\nCOLAB_BRIEF.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExcludeRelease(repo, "COLAB_BRIEF.md", nil); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p)
	if string(body) != "*.swp\n" {
		t.Errorf("exclude file = %q, want only the person's line", body)
	}
}
