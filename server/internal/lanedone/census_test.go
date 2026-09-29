package lanedone

// census_test.go keeps lanedone.MarkDone the only writer of a finished lane.
//
// It reads every non-test .go file under internal/ and finds each SQL
// statement that UPDATEs the lane table and can write `done` into its status
// (a 'done' literal in an UPDATE lane … statement). Each site must be on the
// allow-list below with the reason it is not "the work finished" — so a new
// writer of `done` either goes through MarkDone (and the 작업 카드 gate) or
// says here why it does not.
//
// 회귀 주입: router/status.go 의 done 분기를 옛 인라인 UPDATE 로 되돌리면
// FAIL(허가 목록에 없는 자리); allowed 에서 quiet.go 를 지우면 FAIL; 허가
// 목록의 파일이 사라지거나 그 자리의 UPDATE 가 없어지면(유령) FAIL.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// allowed: file (relative to internal/) → how many `done`-writing UPDATE lane
// statements it may hold, and why.
var allowed = map[string]struct {
	n      int
	reason string
}{
	"lanedone/lanedone.go": {2, "MarkDone itself: AgentDone and TurnEnd"},
	"quiet/quiet.go": {1, "a held task cancelled because its mission closed (T-QUIET ①): " +
		"no turn ran, the work is no longer wanted — no result is owed"},
	"tasks/p3.go": {1, "ResumeLaneForBudget: the Director lifted a budget pause on a lane " +
		"with nothing queued — the work had already finished (and was marked by MarkDone) " +
		"before the post-turn pause (S-44)"},
}

// A statement is the text of one Go raw string literal.
var rawLit = regexp.MustCompile("(?s)`[^`]*`")
var updateLane = regexp.MustCompile(`(?is)\bUPDATE\s+lane\b`)
var doneLit = regexp.MustCompile(`'done'`)

// writesDone: the SET assigns 'done' (directly or in a CASE arm).
var writesDone = regexp.MustCompile(`(?is)SET\s+status\s*=\s*(CASE\b[^;]*'done'|'done')`)

func TestLaneDoneWriterCensus(t *testing.T) {
	root := ".." // internal/
	got := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, lit := range rawLit.FindAllString(string(b), -1) {
			if !updateLane.MatchString(lit) || !doneLit.MatchString(lit) {
				continue
			}
			// `status <> 'done'` alone reads done, it does not write it.
			if !writesDone.MatchString(lit) {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			got[filepath.ToSlash(rel)]++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Guard the guard: the scan must see MarkDone's own two statements, or it
	// is reading nothing and every assertion below passes for free.
	if got["lanedone/lanedone.go"] != 2 {
		t.Fatalf("census found %d done-writers in lanedone.go, want 2 — the scan is broken: %v", got["lanedone/lanedone.go"], got)
	}
	var files []string
	for f := range got {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		a, ok := allowed[f]
		if !ok {
			t.Errorf("%s writes lane status 'done' %d time(s) outside lanedone.MarkDone — route it through MarkDone (the 작업 카드 gate) or add it to `allowed` with the reason", f, got[f])
			continue
		}
		if got[f] != a.n {
			t.Errorf("%s: %d done-writing statements, allowed %d (%s)", f, got[f], a.n, a.reason)
		}
	}
	for f := range allowed {
		if got[f] == 0 {
			t.Errorf("allow-list entry %s has no done-writing statement any more — remove it (ghost)", f)
		}
	}
}
