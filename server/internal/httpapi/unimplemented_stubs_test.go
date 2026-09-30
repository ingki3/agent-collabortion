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
//
// 가정(#400 재리뷰 400c NN3): 생성기와 이 잠금은 같은 꼴 — 비테스트 파일의
// `func (s *Server) Op(` — 만 「구현됨」으로 센다. 수신자 이름이 다르거나 값
// 수신자·임베드 승격으로 구현하면 둘 다 놓친다. 그래서 *Server/Server 에 다른
// 수신자 꼴이 하나라도 있으면 여기서 FAIL 한다(두 쪽의 정규식을 함께 고칠 때까지).
func TestUnimplementedStubsOnlyUnimplementedOps(t *testing.T) {
	stubRe := regexp.MustCompile(`(?m)^func \(unimplemented\) ([A-Z]\w+)\(`)
	implRe := regexp.MustCompile(`(?m)^func \(s \*Server\) ([A-Z]\w+)\(`)
	otherRecvRe := regexp.MustCompile(`(?m)^func \((\w*\s*\*?Server)\) ([A-Z]\w+)\(`)
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
		for _, m := range otherRecvRe.FindAllStringSubmatch(string(src), -1) {
			if m[1] != "s *Server" {
				t.Errorf("%s: receiver (%s) on %s — the generator and this lock only count (s *Server); fix both", f, m[1], m[2])
			}
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
