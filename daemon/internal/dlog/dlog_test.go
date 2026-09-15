package dlog

import (
	"bytes"
	"strings"
	"testing"
)

// The default level prints the lifecycle and drops the detail. This is the
// whole point of the level existing: D-24 asks for the §4 list by default and
// forbids "도구 호출 전부" from being on with it.
func TestInfoPrintsPrintfAndDropsDebugf(t *testing.T) {
	var buf bytes.Buffer
	g := New(&buf, Info)
	g.Printf("t-1.1 claim lane=%s", "lane-a")
	g.Debugf("t-1.1 event seq=%d tool/call", 7)
	out := buf.String()
	if !strings.Contains(out, "t-1.1 claim lane=lane-a") {
		t.Fatalf("info line missing: %q", out)
	}
	if strings.Contains(out, "event seq=") {
		t.Fatalf("detail line printed at info: %q", out)
	}
}

func TestDebugPrintsBoth(t *testing.T) {
	var buf bytes.Buffer
	g := New(&buf, Debug)
	g.Printf("t-1.1 claim")
	g.Debugf("t-1.1 event seq=%d tool/call", 7)
	out := buf.String()
	if !strings.Contains(out, "t-1.1 claim") || !strings.Contains(out, "event seq=7") {
		t.Fatalf("debug level dropped a line: %q", out)
	}
}

// Every line carries the same `15:04:05` stamp the probe lines already had —
// D-24 asks the new lines to look like the ones a reader knows.
func TestLinesAreTimeStamped(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, Info).Printf("hello")
	line := strings.TrimRight(buf.String(), "\n")
	stamp, rest, ok := strings.Cut(line, " ")
	if !ok || len(stamp) != len("15:04:05") || strings.Count(stamp, ":") != 2 {
		t.Fatalf("no Ltime stamp: %q", line)
	}
	if rest != "hello" {
		t.Fatalf("body %q", rest)
	}
}

// A typo in the config field must not silence the log it was trying to widen.
func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Level
	}{
		{"", Info},
		{"info", Info},
		{"debug", Debug},
		{"DEBUG", Debug},
		{"  debug  ", Debug},
		{"verbose", Info},
		{"trace", Info},
	} {
		if got := ParseLevel(tc.in); got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A nil logger is what a caller that never configured one holds; it must not
// panic in the middle of an attempt.
func TestNilLoggerIsSilentNotFatal(t *testing.T) {
	var g *Logger
	g.Printf("x")
	g.Debugf("y")
	if g.Level() != Info {
		t.Fatalf("nil level %v", g.Level())
	}
}
