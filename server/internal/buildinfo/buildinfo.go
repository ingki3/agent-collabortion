// Package buildinfo answers "which commit is this server?" — the one fact the
// installer needs that no request carries (S-64).
//
// WHY. `/install.sh` used to clone `main` whatever the server was running.
// Five people installing during a G8 session would each get whatever `main`
// was at that minute, and none of them the code the server they paired with
// was built from — a daemon and a server that disagree on the wire is the
// failure that looks like everything else (stalls, 422s, "the agent did
// nothing"). There is no release artifact yet, so the ref is the commit; the
// script pins to it and everyone installs the same tree.
//
// TWO SOURCES, IN ORDER. `ref` is set by the linker (`-ldflags -X
// …/buildinfo.ref=<sha|tag>`, Makefile `build`); when it is empty, the VCS
// stamp `go build` embeds in every binary built inside a git checkout
// (runtime/debug, vcs.revision) is used. A binary built from a tarball or with
// -buildvcs=false has neither and reports "", and the installer then falls
// back to the repository's default branch — loudly, in its own output.
package buildinfo

import (
	"runtime/debug"
	"strings"
	"sync"
)

// ref is the linker-stamped ref: a tag or a commit sha. Lowercase on purpose —
// it is an input, and Ref() is the reader.
var ref string

// goMin is the linker-stamped `go` line of the repository's go.work — the
// least Go the installer's source build needs (S-65). The Makefile reads it
// from the file so the number lives in one place.
var goMin string

var (
	once     sync.Once
	resolved string
	modified bool
)

// Ref returns the ref this binary was built from, or "" when unknown.
func Ref() string {
	once.Do(resolve)
	return resolved
}

// Modified reports whether the VCS stamp says the tree had uncommitted changes
// at build time. A pinned installer still installs the commit, not the diff —
// worth a log line, never a refusal.
func Modified() bool {
	once.Do(resolve)
	return modified
}

// GoMin returns the Go version the installer must find on the participant's
// machine: the go.work line when stamped, else the toolchain that built this
// server (a version that built the server builds the daemon and the CLI too —
// stricter than the file, never looser). "" when neither is known.
func GoMin() string {
	if goMin != "" {
		return goMin
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return goVersionNumber(bi.GoVersion)
}

// goVersionNumber turns runtime/debug's "go1.25.1" (or "go1.26rc1 X:…") into
// "1.25.1"; anything that does not start that way ("devel …") yields "".
func goVersionNumber(v string) string {
	if !strings.HasPrefix(v, "go") {
		return ""
	}
	v = v[2:]
	end := 0
	for end < len(v) && (v[end] >= '0' && v[end] <= '9' || v[end] == '.') {
		end++
	}
	return strings.TrimRight(v[:end], ".")
}

func resolve() {
	if ref != "" {
		resolved = ref
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if resolved == "" {
				resolved = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
}
