package quiet

// harness v0.9.15's post-result sentence is the contract's, word for word,
// with the held recipient's name in place of X.
//
// 회귀 주입: NoticeFormat 의 한 글자를 바꾸면 FAIL.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func harness(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "harness.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNoticeMatchesContractV0915(t *testing.T) {
	m := regexp.MustCompile("보류 안내 한 줄: `([^`]+)`").FindStringSubmatch(harness(t))
	if m == nil {
		t.Fatal("harness.md has no v0.9.15 post-result line")
	}
	want := strings.Replace(m[1], "@X", "@Writer", 1)
	if got := Notice("Writer"); got != want {
		t.Fatalf("Notice = %q\nwant      %q", got, want)
	}
	if got := Notice("@Writer"); got != want {
		t.Fatalf("Notice(@Writer) = %q, want the @ not doubled", got)
	}
	if got := Notice("Writer", "Researcher"); !strings.Contains(got, "so @Writer, @Researcher was not woken") {
		t.Fatalf("Notice for two = %q", got)
	}
}
