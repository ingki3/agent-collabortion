package wording

// 데몬 문구 자물쇠 — COMPONENTS §8.4 를 데몬이 만드는 task_event.detail 에 못박는다(D-25).
// 규칙과 수집 방식은 server/internal/wording/wording_test.go 와 같고, sink 만 데몬 것이다.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ── 범위 ────────────────────────────────────────────────────────────────

var scanDirs = []string{"internal", "cmd", "acpfake"}

// ── sink 정의 ────────────────────────────────────────────────────────────

// sinkFields 는 사람이 읽는 칸 이름 — acp.Failure.Detail 이 finish.stop_reason 과
// runtime/error 이벤트의 detail 둘 다가 된다.
var sinkFields = map[string]bool{"Detail": true}

var sinkMapKeys = map[string]bool{"detail": true}

var sinkLocalVars = map[string]bool{"detail": true}

// sinkHelpers 는 사람 문장을 인자로 받는 메서드/함수 — 이름 → 문장 인자의 위치.
var sinkHelpers = map[string][]int{
	"fail":       {1}, // (r *Runner) fail(kind, detail, notBefore)
	"CancelNote": {0}, // (r *Runner) CancelNote(detail)
}

// sinkFuncs 는 본문 전체가 사람 문장을 조립하는 함수.
var sinkFuncs = map[string]bool{
	"workdirDetail": true, // loop: §4.1 데몬 방어의 detail
	"Verify":        true, // workdir: 그 err.Error() 가 workdirDetail 의 머리가 된다
}

// sinkVars 는 값이 곧 문장 조각인 패키지 상수 — 예산 초과 문장의 "넘긴 쪽".
var sinkVars = map[string]bool{"sideTask": true, "sideOverride": true, "sideSession": true}

type sentence struct {
	file string
	line int
	text string
}

func (s sentence) String() string { return s.file + ":" + strconv.Itoa(s.line) + "  " + s.text }

// ── 수집 ────────────────────────────────────────────────────────────────

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func repoRoot(t *testing.T) string { return filepath.Clean(filepath.Join(moduleRoot(t), "..")) }

func sourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, d := range scanDirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				rel, _ := filepath.Rel(root, p)
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

type collector struct {
	fset   *token.FileSet
	file   string
	consts map[string]ast.Expr
	out    []sentence
	seen   map[string]bool
}

func (c *collector) add(e ast.Expr) {
	for _, lit := range c.literals(e, 0) {
		pos := c.fset.Position(lit.Pos())
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}
		c.out = append(c.out, sentence{file: c.file, line: pos.Line, text: s})
	}
}

// literals 는 식 안의 문자열 리터럴을 전부 모은다 — 연결(+)·Sprintf/Errorf·상수까지.
func (c *collector) literals(e ast.Expr, depth int) []*ast.BasicLit {
	if depth > 8 || e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			return []*ast.BasicLit{x}
		}
	case *ast.ParenExpr:
		return c.literals(x.X, depth+1)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return append(c.literals(x.X, depth+1), c.literals(x.Y, depth+1)...)
		}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "fmt" && (strings.HasPrefix(sel.Sel.Name, "Sprint") || sel.Sel.Name == "Errorf") ||
				id.Name == "errors" && sel.Sel.Name == "New") {
				var out []*ast.BasicLit
				for _, a := range x.Args {
					out = append(out, c.literals(a, depth+1)...)
				}
				return out
			}
		}
	case *ast.Ident:
		if decl, ok := c.consts[x.Name]; ok {
			return c.literals(decl, depth+1)
		}
	}
	return nil
}

func funcName(call *ast.CallExpr) (recv, name string) {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return "", f.Name
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name, f.Sel.Name
		}
		return "?", f.Sel.Name
	}
	return "", ""
}

func (c *collector) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.CallExpr:
		_, name := funcName(x)
		if idx := sinkHelpers[name]; idx != nil {
			for _, i := range idx {
				if i < len(x.Args) {
					c.add(x.Args[i])
				}
			}
		}
	case *ast.KeyValueExpr:
		switch k := x.Key.(type) {
		case *ast.Ident:
			if sinkFields[k.Name] {
				c.add(x.Value)
			}
		case *ast.BasicLit:
			if s, err := strconv.Unquote(k.Value); err == nil && sinkMapKeys[s] {
				c.add(x.Value)
			}
		}
	case *ast.AssignStmt:
		for i, lhs := range x.Lhs {
			if i >= len(x.Rhs) {
				break
			}
			switch l := lhs.(type) {
			case *ast.SelectorExpr:
				if sinkFields[l.Sel.Name] {
					c.add(x.Rhs[i])
				}
			case *ast.Ident:
				if sinkLocalVars[l.Name] {
					c.add(x.Rhs[i])
				}
			case *ast.IndexExpr: // p["detail"] = …
				if lit, ok := l.Index.(*ast.BasicLit); ok {
					if s, err := strconv.Unquote(lit.Value); err == nil && sinkMapKeys[s] {
						c.add(x.Rhs[i])
					}
				}
			}
		}
	case *ast.FuncDecl:
		if sinkFuncs[x.Name.Name] && x.Body != nil {
			c.seen[x.Name.Name] = true
			c.addAll(x.Body)
			return false
		}
	case *ast.ValueSpec:
		for i, n := range x.Names {
			if sinkVars[n.Name] && i < len(x.Values) {
				c.seen[n.Name] = true
				c.addAll(x.Values[i])
			}
		}
	}
	return true
}

func (c *collector) addAll(n ast.Node) {
	ast.Inspect(n, func(m ast.Node) bool {
		if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			c.add(lit)
		}
		return true
	})
}

func collect(t *testing.T) (pool []sentence, files []string, seen map[string]bool) {
	t.Helper()
	root := moduleRoot(t)
	files = sourceFiles(t, root)
	fset := token.NewFileSet()
	seen = map[string]bool{}
	for _, f := range files {
		af, err := parser.ParseFile(fset, filepath.Join(root, f), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		c := &collector{fset: fset, file: f, consts: map[string]ast.Expr{}, seen: seen}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, sp := range gd.Specs {
				if vs, ok := sp.(*ast.ValueSpec); ok {
					for i, n := range vs.Names {
						if i < len(vs.Values) {
							c.consts[n.Name] = vs.Values[i]
						}
					}
				}
			}
		}
		ast.Inspect(af, c.visit)
		pool = append(pool, c.out...)
	}
	return pool, files, seen
}

var (
	hangul   = regexp.MustCompile(`[가-힣]`)
	twoWords = regexp.MustCompile(`[A-Za-z]{2,}\s+[A-Za-z]{2,}`)
)

// prose 는 사람이 읽는 글처럼 생긴 것 — 한글이 있거나 영어 단어가 둘 이상.
func isProse(s string) bool { return hangul.MatchString(s) || twoWords.MatchString(s) }

func hits(pool []sentence, re *regexp.Regexp) []string {
	var out []string
	for _, s := range pool {
		if re.MatchString(s.text) {
			out = append(out, s.String())
		}
	}
	return out
}

// ── 표 — COMPONENTS §8.4 에서 읽는다 ───────────────────────────────────────

// componentsOldTerms 는 COMPONENTS.md §8.4 표의 「지금」 열에 나오는 영어 낱말이다
// (Workdir · Runtimes · Inbox · lane · task · attempt · HITL …). 표를 복사하지 않고
// 테스트 시점에 계약 파일에서 읽는다 — 표가 늘면 자물쇠도 는다.
func componentsOldTerms(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "COMPONENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	i := strings.Index(text, "### 8.4")
	if i < 0 {
		t.Fatal("COMPONENTS.md 에 §8.4 가 없다")
	}
	text = text[i:]
	if j := strings.Index(text, "\n### 8.5"); j > 0 {
		text = text[:j]
	}
	word := regexp.MustCompile(`[A-Za-z]{3,}`)
	// 표의 예시 문장에 섞인, 용어가 아닌 영어 — 「Add a computer」의 Add/computer 처럼
	// 화면 문구 그 자체이거나(§8.4 가 바꾸라는 것은 그 문구 전체) 사양 이름이다.
	skip := map[string]bool{"acp": true, "protocol": true, "add": true, "computer": true, "eval": true, "user": true, "director": true, "deputy": true, "prd": true, "api": true, "enum": true}
	set := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| 지금") || strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		for _, w := range word.FindAllString(cells[1], -1) {
			lw := strings.ToLower(w)
			if !skip[lw] {
				set[strings.TrimSuffix(lw, "s")] = true
			}
		}
	}
	terms := make([]string, 0, len(set))
	for w := range set {
		terms = append(terms, w)
	}
	sort.Strings(terms)
	return terms
}

// rows 는 server/internal/wording/wording_test.go TestNoInternalTerms 의 표와 **같아야
// 한다** — TestSameTableAsServer 가 그 파일을 읽어 대조한다(표를 두 벌로 늙히지 않기 위해).
var rows = []struct {
	label string
	re    *regexp.Regexp
}{
	{"Workdir → 작업 폴더", regexp.MustCompile(`(?i)\bworkdirs?\b`)},
	{"Runtime(s) → 컴퓨터", regexp.MustCompile(`(?i)\bruntimes?\b`)},
	{"런타임 → 컴퓨터 (산문까지 전부)", regexp.MustCompile(`런타임`)},
	{"머신 → 컴퓨터", regexp.MustCompile(`머신`)},
	{"Inbox → 받은 요청", regexp.MustCompile(`\bInbox\b`)},
	{"owner·admin → 소유자·관리자", regexp.MustCompile(`\b(owner|admin)\b`)},
	{"lane → 작업 줄기", regexp.MustCompile(`(?i)\blanes?\b`)},
	{"task → 할 일", regexp.MustCompile(`(?i)\btasks?\b`)},
	{"attempt → 실행", regexp.MustCompile(`(?i)\battempts?\b`)},
	{"HITL → 확인 요청", regexp.MustCompile(`\bHITL\b`)},
	{"seq", regexp.MustCompile(`\bseq\b`)},
	{"payload", regexp.MustCompile(`\bpayload\b`)},
	{"idempotency", regexp.MustCompile(`(?i)idempotenc`)},
	{"probe", regexp.MustCompile(`(?i)\bprobe\b`)},
	{"uuid", regexp.MustCompile(`(?i)\buuid\b`)},
	{"slug", regexp.MustCompile(`(?i)\bslug\b`)},
	{"pgid", regexp.MustCompile(`(?i)\bpgid\b`)},
	{"stall", regexp.MustCompile(`(?i)\bstall\b`)},
	{"claim · heartbeat · finish (데몬 프로토콜 동사)", regexp.MustCompile(`(?i)\b(claim|heartbeat|finish)\b`)},
	{"GC → 작업 폴더 정리 (daemon-protocol §6 v0.7.4)", regexp.MustCompile(`\bGC\b`)},
	{"rebind → 다른 컴퓨터로 옮기기", regexp.MustCompile(`(?i)rebind|재바인딩`)},
	{"FR-x.y · E1-02 · §", regexp.MustCompile(`\bFR-\d|\bE\d{1,2}-\d{2}\b|§|\bPRD\b|daemon-protocol|openapi`)},
	{"API 연산 이름", regexp.MustCompile(`\b[a-z]+(?:[A-Z][a-z]+)+\b`)},
	{"한글(snake_key)", regexp.MustCompile(`[가-힣]\s*\([a-z][a-z0-9]*_[a-z0-9_]+\)`)},
}

// ── 테스트 ─────────────────────────────────────────────────────────────

func TestScope(t *testing.T) {
	pool, files, seen := collect(t)
	var prose []sentence
	for _, s := range pool {
		if isProse(s.text) {
			prose = append(prose, s)
		}
	}
	t.Logf("files=%d sinks=%d prose=%d", len(files), len(pool), len(prose))
	if os.Getenv("WORDING_DUMP") != "" {
		for _, s := range prose {
			t.Log(s.String())
		}
	}
	if len(files) < 30 {
		t.Errorf("소스 %d개만 훑었다", len(files))
	}
	if len(prose) < 18 { // T-D12 시점 21
		t.Errorf("사람이 읽는 문장이 %d개뿐 — sink 규칙이 빠졌다(detail 키·Detail 칸·fail·CancelNote·sinkFuncs)", len(prose))
	}
	for name := range sinkFuncs {
		if !seen[name] {
			t.Errorf("sinkFuncs %q 를 어느 파일에서도 못 만났다 — 함수 이름이 바뀌었으면 표도 같이 고쳐라", name)
		}
	}
	for name := range sinkVars {
		if !seen[name] {
			t.Errorf("sinkVars %q 를 어느 파일에서도 못 만났다", name)
		}
	}
	// D-25 가 짚은 세 자리 + 리뷰 NN 자리 — 여기가 풀에 없으면 자물쇠는 잠긴 척만 한다.
	for _, f := range []string{
		"internal/loop/loop.go",            // workdir bundle path → (D-25 1) · 자리표시자 · 드레인
		"internal/harness/acp/budget.go",   // 유효 예산 (D-25 2)
		"internal/harness/acp/runner.go",   // mcp server dropped (D-25 3) · stall · D-13 · adapter pin
		"internal/harness/acp/classify.go", // rate limit · UnexpectedExit
		"internal/workdir/workdir.go",      // Verify
	} {
		found := false
		for _, s := range prose {
			if s.file == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s 에서 문장을 하나도 못 모았다", f)
		}
	}
}

func TestSentencesAreInTheScreensLanguage(t *testing.T) {
	pool, _, _ := collect(t)
	var bad []string
	for _, s := range pool {
		if isProse(s.text) && !hangul.MatchString(s.text) {
			bad = append(bad, s.String())
		}
	}
	if len(bad) > 0 {
		t.Errorf("영어 문장이 사람이 읽는 자리에 %d건 — 화면과 같은 말(한국어)로:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}

func TestNoInternalTerms(t *testing.T) {
	pool, _, _ := collect(t)
	var prose []sentence
	for _, s := range pool {
		if isProse(s.text) {
			prose = append(prose, s)
		}
	}
	for _, r := range rows {
		if h := hits(prose, r.re); len(h) > 0 {
			t.Errorf("%s — %d건:\n  %s", r.label, len(h), strings.Join(h, "\n  "))
		}
	}
	// COMPONENTS §8.4 표의 「지금」 열 — 계약 파일에서 읽은 영어 용어.
	terms := componentsOldTerms(t)
	if len(terms) < 6 {
		t.Fatalf("COMPONENTS §8.4 에서 읽은 용어가 %d개뿐: %v", len(terms), terms)
	}
	t.Logf("COMPONENTS §8.4 「지금」 용어: %v", terms)
	for _, w := range terms {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(w) + `s?\b`)
		if h := hits(prose, re); len(h) > 0 {
			t.Errorf("§8.4 표의 옛말 %q — %d건:\n  %s", w, len(h), strings.Join(h, "\n  "))
		}
	}
}

// TestSameTableAsServer 는 server/internal/wording/wording_test.go 의 TestNoInternalTerms
// 표(regexp 목록)와 이 파일의 rows 가 한 글자도 다르지 않음을 잰다. 표는 서버 것이 정본이고
// 데몬은 그것을 **베끼되 늙히지 못한다**: 한쪽에 행이 늘면 이 테스트가 깨진다.
func TestSameTableAsServer(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "server", "internal", "wording", "wording_test.go"))
	if err != nil {
		t.Skipf("server wording lock not found: %v", err)
	}
	text := string(b)
	i := strings.Index(text, "func TestNoInternalTerms")
	if i < 0 {
		t.Fatal("server TestNoInternalTerms not found")
	}
	text = text[i:]
	if j := strings.Index(text, "\nfunc "); j > 0 {
		text = text[:j]
	}
	pat := regexp.MustCompile("regexp\\.MustCompile\\(`([^`]*)`\\)")
	var server []string
	for _, m := range pat.FindAllStringSubmatch(text, -1) {
		server = append(server, m[1])
	}
	var mine []string
	for _, r := range rows {
		mine = append(mine, r.re.String())
	}
	if strings.Join(server, "\n") != strings.Join(mine, "\n") {
		t.Errorf("데몬 rows 가 서버 표와 다르다 — 서버 것을 그대로 옮겨라\n서버:\n  %s\n데몬:\n  %s", strings.Join(server, "\n  "), strings.Join(mine, "\n  "))
	}
}

func TestRoleNamesStayEnglish(t *testing.T) {
	pool, _, _ := collect(t)
	if h := hits(pool, regexp.MustCompile(`디렉터[^리]|디렉터$|감독관|연출자|부감독`)); len(h) > 0 {
		t.Errorf("Director·deputy 는 영어 그대로(§8.4 예외):\n  %s", strings.Join(h, "\n  "))
	}
}
