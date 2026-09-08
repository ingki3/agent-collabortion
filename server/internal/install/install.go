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
func Script(serverURL string) string {
	return strings.ReplaceAll(script, "@@COLAB_SERVER_URL@@", strings.TrimRight(serverURL, "/"))
}

// CurlCommand is line 1 of `Pairing.install_commands`. Kept next to Path so the
// command and the route cannot be edited apart.
func CurlCommand(serverURL string) string {
	return "curl -fsSL " + strings.TrimRight(serverURL, "/") + Path + " | sh"
}
