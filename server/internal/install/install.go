// Package install serves the daemon installer the S12 pairing card tells a new
// person to run (S-63).
//
// WHY THIS PACKAGE EXISTS AT ALL. `Pairing.install_commands` line 1 has said
// `curl -fsSL <서버 오리진>/install.sh | sh` since P1, and nothing answered that
// path — a person who had never seen this repository met a 404 on the FIRST
// step of F1 (Director 실사용 2026-09-08). The two halves lived in different
// packages (`runtimes` printed the line, `httpapi` owned the routes) and each
// was "correct" on its own, which is exactly how the gap survived every gate:
// the integration runs called `bin/daemon pair` directly and never read the
// card. So the path is ONE constant here, both halves import it, and a unit
// pins that the served route is the same string the card prints.
package install

import (
	_ "embed"
	"strings"
)

// Path is the route the pairing card points at. `runtimes.installCommands`
// builds line 1 from it and `httpapi.Handler` registers it — there is no second
// literal to drift.
const Path = "/install.sh"

// ContentType is what `curl … | sh` gets. Not text/plain: a browser opening the
// same URL should offer the script rather than render it as a page.
const ContentType = "text/x-shellscript; charset=utf-8"

//go:embed install.sh
var script string

// Script renders the installer for THIS server. serverURL is the origin the
// server was started with (COLAB_SERVER_URL); the script must never carry a
// hardcoded origin, because the machine running it is being pointed at this
// deployment and no other.
//
// ref is the commit (or tag) the SERVER was built from (buildinfo.Ref), and
// becomes the script's default `COLAB_INSTALL_REF` (S-64). Empty means the
// server does not know — a binary built outside a checkout — and the script
// then says so and takes the repository's default branch, which is the P5
// behaviour and the one thing five participants in one G8 session must not
// get silently: each would receive whatever `main` was at that minute.
//
// goMin is the Go the source build needs (buildinfo.GoMin — the go.work line);
// the script refuses an older toolchain up front instead of failing halfway
// through a build (S-65). Empty disables the check.
func Script(serverURL, ref, goMin string) string {
	s := strings.ReplaceAll(script, "@@COLAB_SERVER_URL@@", strings.TrimRight(serverURL, "/"))
	s = strings.ReplaceAll(s, "@@COLAB_INSTALL_REF@@", sanitizeRef(ref))
	return strings.ReplaceAll(s, "@@COLAB_GO_MIN@@", sanitizeRef(goMin))
}

// sanitizeRef keeps the ref to what a git ref or sha can be. The value comes
// from the linker or the VCS stamp, never a request, but it is being written
// into a shell script served over HTTP — a stray quote there is a script that
// does not parse for every person who runs it.
func sanitizeRef(ref string) string {
	var b strings.Builder
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '/':
			b.WriteRune(r)
		default:
			return ""
		}
	}
	return b.String()
}

// CurlCommand is line 1 of `Pairing.install_commands`. Kept next to Path so the
// command and the route cannot be edited apart.
func CurlCommand(serverURL string) string {
	return "curl -fsSL " + strings.TrimRight(serverURL, "/") + Path + " | sh"
}
