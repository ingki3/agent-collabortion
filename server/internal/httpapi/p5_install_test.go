package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/install"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// ---------------------------------------------------------------------------
// S-63 — the pairing card's first line must not be a 404
// ---------------------------------------------------------------------------
//
// `Pairing.install_commands[0]` has read `curl -fsSL <서버 오리진>/install.sh | sh`
// since P1 and the server answered nothing there. Every gate missed it because
// the integration runs called `bin/daemon pair` directly — only a person
// starting from the screen, with no repository checked out, meets the defect,
// which is exactly the population G8 measures (Director 실사용 2026-09-08).

// installFixture is a server with a KNOWN origin and no session on the client.
func installFixture(t *testing.T, serverURL string) (*Server, *client) {
	t.Helper()
	pool := testdb.New(t)
	s := NewServer(Deps{DB: pool, Clock: clock.NewFake(t0), ServerURL: serverURL, WebURL: "http://web.test:3000"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, &client{t: t, srv: ts}
}

// getRaw is a request with NO credential of any kind — the state a machine that
// has never seen this deployment is in.
func getRaw(t *testing.T, c *client, path string, headers ...string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest("GET", c.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body), res.Header
}

// TestP5InstallScriptIsServedAtTheCardsPath is S-63 (c): the path the card
// prints and the path the server answers are the SAME string.
//
// The URL under test is not typed here — it is parsed out of a pairing created
// through the API, so an edit to either half that moves them apart fails this
// test instead of shipping a 404 to the next new user.
func TestP5InstallScriptIsServedAtTheCardsPath(t *testing.T) {
	_, api := installFixture(t, "http://colab.test")
	const p = "/api/v1"

	_, _, hdr := api.do("POST", p+"/auth/signup", map[string]any{"display_name": "Dir", "email": "dir@example.com", "password": "password123"})
	api.cookie = hdr.Get("Set-Cookie")
	wsID := str(api.must(201, "POST", p+"/workspaces", map[string]any{"name": "Acme"}), "id")
	pairing := api.must(201, "POST", p+"/workspaces/"+wsID+"/runtimes/pairings", map[string]any{"name": "laptop"})

	cmds := pairing["install_commands"].([]any)
	line1 := cmds[0].(string)
	if line1 != "curl -fsSL http://colab.test/install.sh | sh" {
		t.Fatalf("install_commands[0] = %q, want the contract's `curl -fsSL <서버 오리진>/install.sh | sh`", line1)
	}
	// Take the path the person would actually fetch, not a literal.
	fields := strings.Fields(line1)
	rawURL := fields[2]
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("install_commands[0] does not carry a URL: %q", line1)
	}
	if u.Path != install.Path {
		t.Fatalf("the card sends people to %q but the server registers %q — this is S-63 coming back",
			u.Path, install.Path)
	}

	st, body, h := getRaw(t, &client{t: t, srv: api.srv}, u.Path)
	if st != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 WITHOUT authentication — this is the first thing a new person "+
			"runs and the machine has no account yet (S-63)", u.Path, st)
	}
	if !strings.HasPrefix(body, "#!/bin/sh") {
		t.Errorf("the body piped into `sh` does not start with a shebang: %.40q", body)
	}
	if got := h.Get("Content-Type"); got != install.ContentType {
		t.Errorf("Content-Type = %q, want %q", got, install.ContentType)
	}
}

// TestP5InstallScriptIsUnauthenticatedEvenWithAStaleCredential is the P4 lesson
// restated (PR #173 리뷰: 인증 오류 하나가 다음 오류를 가린다). A request carrying
// some other deployment's stale bearer must still get the script — an installer
// that can answer 401 is an installer that cannot be piped into `sh`.
func TestP5InstallScriptIsUnauthenticatedEvenWithAStaleCredential(t *testing.T) {
	_, api := installFixture(t, "http://colab.test")
	for _, bearer := range []string{"cdt_nonsense", "ctk_nonsense", "nonsense"} {
		st, _, _ := getRaw(t, api, install.Path, "Authorization", "Bearer "+bearer)
		if st != http.StatusOK {
			t.Errorf("GET %s with Authorization %q = %d, want 200", install.Path, bearer, st)
		}
	}
}

// TestP5InstallScriptCarriesThisServersOrigin is S-63 (a): the script points the
// new machine back at the server that served it. A hardcoded origin would pair
// every deployment's daemons to whichever host the constant named.
func TestP5InstallScriptCarriesThisServersOrigin(t *testing.T) {
	const a, b = "http://colab.test", "https://colab.example.com:8443"
	for _, c := range []struct{ origin, other string }{{a, "colab.example.com"}, {b, "colab.test"}} {
		_, api := installFixture(t, c.origin)
		st, body, _ := getRaw(t, api, install.Path)
		if st != http.StatusOK {
			t.Fatalf("GET %s = %d", install.Path, st)
		}
		if !strings.Contains(body, "COLAB_SERVER_URL:-"+c.origin+"}") {
			t.Errorf("the script served by %s does not default COLAB_SERVER_URL to that origin", c.origin)
		}
		if strings.Contains(body, c.other) {
			t.Errorf("the script served by %s names %q — S-63 (a) forbids a hardcoded host: the "+
				"machine running this is being pointed at THIS deployment", c.origin, c.other)
		}
		if strings.Contains(body, "@@") {
			t.Errorf("the script served by %s still carries an unsubstituted placeholder", c.origin)
		}
	}
}

// TestP5InstallScriptStaysInTheUsersOwnDirectories is S-63 (b)/(d): a script a
// person pipes into `sh` on the strength of a screen must not need sudo, must
// not write outside the user's own tree, and must say something a human can act
// on when the toolchain it needs is missing.
func TestP5InstallScriptStaysInTheUsersOwnDirectories(t *testing.T) {
	script := install.Script("http://colab.test")

	for _, want := range []string{
		"$HOME/.colab",       // 사용자 영역
		"command -v go",      // (b) go 검사
		"command -v git",     //     소스 경로에 필요한 나머지
		"https://go.dev/dl/", //   없을 때 사람이 읽을 수 있는 안내
		"colab-daemon",       // PATH 에 놓이는 이름
		"mktemp -d",          // 빌드는 임시 디렉터리에서
	} {
		if !strings.Contains(script, want) {
			t.Errorf("installer does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"/usr/local/bin", "rm -rf /usr"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("installer contains %q — it must not touch anything outside the user's own "+
				"directories", forbidden)
		}
	}
	// `sudo` may appear inside the advice the script PRINTS when Go is missing
	// (that is the person's own package manager), but the script itself must
	// never run it.
	for i, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "say ") {
			continue
		}
		if strings.Contains(trimmed, "sudo") {
			t.Errorf("installer line %d runs sudo: %q", i+1, trimmed)
		}
	}
	// The release-artifact branch does not exist yet; the place it goes is
	// marked so the next person does not invent a second layout (S-63 (b)).
	if !strings.Contains(script, "$COLAB_SERVER_URL/dist/") {
		t.Errorf("installer leaves no marked place for the release-artifact branch")
	}
}

// The Lead's 2026-09-08 판정 — the installer places the `colab` CLI as well as
// the daemon — is measured in server/internal/install by RUNNING the script
// against a throwaway HOME (TestScriptInstallsBothBinaries). A grep for
// `./cmd/colab` here would pass on every broken variant of that build line; the
// only check that separates a working installer from a plausible-looking one is
// the binary being there and answering `--version` afterwards, which is exactly
// what the contract's acceptance test (`colab_cli.present == true` on the first
// probe) reads.
