package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// unimplemented.go must stub only ops *Server does not implement
// (scripts/gen_unimplemented.sh skips the implemented ones). A stub for an
// implemented op is shadowed and harmless at run time, but the web's drift
// tests read the file as "this op is not implemented" (#400 CI web:
// GetWorkspaceObservations reappeared after a full regeneration). Also
// checks every stub is still an op of gen.ServerInterface.
func TestUnimplementedStubsOnlyUnimplementedOps(t *testing.T) {
	stubRe := regexp.MustCompile(`(?m)^func \(unimplemented\) ([A-Z]\w+)\(`)
	implRe := regexp.MustCompile(`(?m)^func \(s \*Server\) ([A-Z]\w+)\(`)
	raw, err := os.ReadFile("unimplemented.go")
	if err != nil {
		t.Fatal(err)
	}
	stubs := map[string]bool{}
	for _, m := range stubRe.FindAllStringSubmatch(string(raw), -1) {
		stubs[m[1]] = true
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "unimplemented.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range implRe.FindAllStringSubmatch(string(src), -1) {
			if stubs[m[1]] {
				t.Errorf("%s is implemented in %s but still stubbed in unimplemented.go — rerun scripts/gen_unimplemented.sh", m[1], f)
			}
		}
	}
	gen, err := os.ReadFile("gen/api.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	iface := strings.SplitN(strings.SplitN(string(gen), "type ServerInterface interface", 2)[1], "\n}", 2)[0]
	for s := range stubs {
		if !strings.Contains(iface, "\t"+s+"(") {
			t.Errorf("stub %s is not an op of gen.ServerInterface", s)
		}
	}
}
