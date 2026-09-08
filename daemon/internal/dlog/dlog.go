// Package dlog is the daemon's progress log (D-24).
//
// WHY IT EXISTS. `daemon run` printed a start-up line and two probe lines and
// then went silent for the whole session, while the DB filled with `tool/*`
// events (Director 실사용 2026-09-08, 로그 파일 287바이트). The cause was not a
// level, a buffer or a structured-logger setting — there was no logger
// configuration at all: `log.Printf` writes straight to an unbuffered
// os.Stderr, and `loop.Daemon.Log` was wired to it. What was missing was
// CALL SITES. Every `d.Log` in the claim loop sat on an error branch
// (`claim: %v`, `%s workdir: %v`, `finish %s: %v`), so a healthy attempt
// produced nothing at all until it ended.
//
// So this package is deliberately small. It supplies the ONE thing the
// missing call sites need that plain `log` does not: a second, quieter tier,
// so that "everything the attempt emitted" can be turned on for a diagnosis
// without it being the default (P5 T-D11: "과도한 잡음(도구 호출 전부)은 금지
// — 기본은 위 목록, 상세는 레벨로").
//
// Two levels, not five. `info` is the daemon-protocol §4 lifecycle — claim,
// phase, turn, finish, commands, errors — which is what a person reads to
// answer "where did it stop"; `debug` adds the per-event stream. A level in
// between would only invite arguments about which tier a line belongs to.
package dlog

import (
	"io"
	"log"
	"strings"
)

// Level is the progress log's verbosity.
type Level int

const (
	// Info is the default: the §4 lifecycle of the daemon and of each
	// attempt, plus every error. Bounded per attempt (a handful of lines),
	// so a day-long session stays readable.
	Info Level = iota
	// Debug adds what the attempt put on the wire — one line per
	// task_event, tool calls included. Off by default: a single turn emits
	// hundreds.
	Debug
)

func (l Level) String() string {
	if l == Debug {
		return "debug"
	}
	return "info"
}

// ParseLevel reads a configured level. Anything unrecognised — including the
// empty string of a daemon.json written before this existed — is Info: a
// typo in a config field must not silence the log it was trying to widen.
func ParseLevel(s string) Level {
	if strings.EqualFold(strings.TrimSpace(s), "debug") {
		return Debug
	}
	return Info
}

// Logger writes the progress log. The zero value is unusable; use New.
type Logger struct {
	l   *log.Logger
	lvl Level
}

// New writes to w with the same one-line shape the probe lines already had
// (`log.Ltime`, no prefix) — D-11 asks the new lines to look like the ones a
// reader is used to.
func New(w io.Writer, lvl Level) *Logger {
	return &Logger{l: log.New(w, "", log.Ltime), lvl: lvl}
}

// Printf writes an info line. Errors go here too: they are never the noisy
// tier, and dropping them behind a level is how the original hole was dug.
func (g *Logger) Printf(format string, args ...any) {
	if g == nil || g.l == nil {
		return
	}
	g.l.Printf(format, args...)
}

// Debugf writes a detail line, or nothing at Info.
func (g *Logger) Debugf(format string, args ...any) {
	if g == nil || g.l == nil || g.lvl < Debug {
		return
	}
	g.l.Printf(format, args...)
}

// Level reports the configured level.
func (g *Logger) Level() Level {
	if g == nil {
		return Info
	}
	return g.lvl
}
