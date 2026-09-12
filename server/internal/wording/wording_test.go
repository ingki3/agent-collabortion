package wording

// 문구 자물쇠 — COMPONENTS §8.4 용어표를 서버 문장에 대해 못박는다(S-67).
//
// web/lib/wording.test.ts 가 화면 문자열에 대해 한 일을 서버가 만드는 문장에
// 대해 한다. 소스를 go/ast 로 훑어 **사람이 읽는 자리(sink)** 에 닿는 문자열
// 리터럴만 모은다 — SQL·로그·식별자는 문구가 아니다.
//
// sink 는 일곱 종류다.
//   - apperr 생성자: New(…, detail) · Unauthorized/Forbidden/Conflict/Gone(code, detail)
//     · NotFound(noun) · Field(field, code, message)
//   - 세션에 게시되는 시스템 메시지: *.SystemPost(ctx, tx, id, content)
//   - 사람이 읽는 칸 이름: Detail · Title · Hint · Message · Note · FeedNote ·
//     Question · Reason · CLIError · ErrorMessage · Problems · Content · Summary ·
//     Rationale, 그리고 task_event payload 의 "detail"·"note" 키(S-52) 와
//     `e.Payload[…] =` 대입(마스킹 치환문)
//   - 지역 헬퍼: reject(code, field, msg) (hitl.Plan) · unreadable(field, code, msg, err) (httpapi)
//     · field(name, code, msg) (agents) · insertDecision(…, summary, rationale, …) (httpapi)
//   - `INSERT INTO decision` 을 직접 쓰는 Exec/QueryRow 의 값 인자 — decision.summary·
//     rationale 은 openapi 공개 칸이고 S7 "결정" 절이 그린다(PR #192 리뷰 NN2·NN3)
//   - 본문 전체가 문장을 조립하는 함수(sinkFuncs): LimitText · GCReasonText ·
//     BuildSummaryBody · decisionLine · CardBody · hitlTypeLabel · ValidateTree ·
//     apperr.Title/StatusLabel/NotFound/Validation/Internal — 반환값·switch 가지·Fprintf 조각까지
//   - 표로 둔 패키지 변수(sinkVars): apperr.titles · statusLabels · NotFoundNouns · sessions.ErrInvalidTree
//
// 문자열 연결(+)·fmt.Sprintf·패키지 상수·nullable.NewNullableWithValue 는 안쪽까지 따라간다.

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

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// ── 범위 ────────────────────────────────────────────────────────────────

// scanDirs 는 서버 소스 전부다. `gen/` 은 계약에서 생성된 코드, `testdb` 는 픽스처.
var scanDirs = []string{"internal", "cmd"}

var skipDir = map[string]bool{"gen": true, "testdb": true}

// daemonAPI 는 **데몬이 읽는** 응답을 만드는 파일이다(daemon-protocol 의 /daemon/v1 —
// claim·phase·events·heartbeat·finish 와 그 배치 검증). 그 Problem 은 데몬 로그로 가지
// 화면에 닿지 않으므로 apperr 인자는 세지 않는다. 좁히려는 것이 아니라 기계의 글이다.
// 이 파일들이 task_event 로 남기는 "note"·"detail" 은 사람이 읽으므로 그대로 센다.
// queue/bundle.go 의 brief 본문(에이전트 턴 프롬프트)은 sink 가 아니라 자연히 빠진다.
var daemonAPI = map[string]bool{
	"internal/httpapi/daemon.go":          true,
	"internal/eventschema/eventschema.go": true,
	"internal/events/events.go":           true,
}

// ── sink 정의 ────────────────────────────────────────────────────────────

// apperrArg 는 생성자별로 사람이 읽는 인자의 위치.
var apperrArg = map[string]int{
	"New": 2, "Unauthorized": 1, "Forbidden": 1, "Conflict": 1, "Gone": 1, "Field": 2,
}

var sinkFields = map[string]bool{
	"Detail": true, "Title": true, "Hint": true, "Message": true, "Note": true, "FeedNote": true,
	"Question": true, "Reason": true, "CLIError": true, "ErrorMessage": true, "Problems": true,
	"Content": true, "Summary": true, "Rationale": true,
	"Decisions": true, "Artifacts": true, "Timeline": true, // sessions.SummaryFacts → 세션 요약 본문
}

var sinkMapKeys = map[string]bool{"detail": true, "note": true}

var sinkLocalVars = map[string]bool{"question": true, "detail": true, "note": true, "reason": true, "hint": true, "header": true, "body": true, "summary": true, "title": true}

// sinkHelpers 는 사람 문장을 인자로 받는 지역 함수 — 이름 → 문장 인자의 위치들.
// 패키지 한정자 없이 불리는 것만(pkg == "") 본다.
var sinkHelpers = map[string][]int{
	"reject":         {2}, // hitl.Plan: reject(code, field, msg)
	"unreadable":     {2}, // httpapi: unreadable(field, code, msg, err)
	"field":          {2}, // agents: field(name, code, msg)
	"insertDecision": {3, 4},
}

// sinkFuncs 는 본문 전체가 사람 문장을 조립하는 함수 — 안의 문자열 리터럴을 전부 센다
// (반환값·switch 가지·strings.Builder 에 쓰는 조각). 리뷰 NN2 의 "함수 반환값·여러 줄 조립".
var sinkFuncs = map[string]bool{
	"LimitText": true, "GCReasonText": true, "BuildSummaryBody": true, "CardBody": true,
	"hitlTypeLabel": true, "Title": true, "StatusLabel": true, "NotFound": true,
	"Validation": true, "Internal": true,
	"ValidateTree": true, // sessions: err.Error() 가 그대로 Field message 가 된다
	"decisionLine": true, // sessions: 요약의 결정 기록 한 줄
}

// sinkVars 는 값이 곧 화면 문장인 패키지 변수(표).
var sinkVars = map[string]bool{"titles": true, "statusLabels": true, "NotFoundNouns": true,
	"ErrInvalidTree": true} // sessions: %w 로 Field message 의 머리가 된다

// decisionSQL 은 decision 행을 직접 쓰는 SQL — 그 Exec/QueryRow 의 값 인자는 사람이 읽는다.
var decisionSQL = regexp.MustCompile(`INSERT\s+INTO\s+decision\b`)

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

func sourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, d := range scanDirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if skipDir[e.Name()] {
					return filepath.SkipDir
				}
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
	consts map[string]ast.Expr // 패키지 상수 · 변수 (같은 파일)
	out    []sentence
	// notFoundNouns 는 apperr.NotFound 에 건네진 키 — 한국어 명사표에 있어야 한다.
	notFoundNouns []sentence
	// apperrExcluded 면 apperr 생성자는 세지 않는다(데몬 API — daemonAPI).
	apperrExcluded bool
	// seen 은 실제로 만난 sinkFuncs·sinkVars 이름 — 이름이 바뀌면 자물쇠가 조용히 풀리므로 TestScope 가 대조한다.
	seen map[string]bool
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

// literals 는 식 안의 문자열 리터럴을 전부 모은다 — 연결(+)·Sprintf·상수까지.
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
			if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Sprint") ||
				id.Name == "nullable" && sel.Sel.Name == "NewNullableWithValue") {
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

func funcName(call *ast.CallExpr) (pkg, name string) {
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
		pkg, name := funcName(x)
		switch {
		case pkg == "apperr" && !c.apperrExcluded:
			if name == "NotFound" && len(x.Args) == 1 {
				if lit, ok := x.Args[0].(*ast.BasicLit); ok {
					s, _ := strconv.Unquote(lit.Value)
					c.notFoundNouns = append(c.notFoundNouns, sentence{c.file, c.fset.Position(lit.Pos()).Line, s})
				}
			} else if i, ok := apperrArg[name]; ok && len(x.Args) > i {
				c.add(x.Args[i])
			}
		case name == "SystemPost" && len(x.Args) >= 4:
			c.add(x.Args[3])
		case pkg == "" && sinkHelpers[name] != nil:
			for _, i := range sinkHelpers[name] {
				if i < len(x.Args) {
					c.add(x.Args[i])
				}
			}
		case (name == "Exec" || name == "QueryRow" || name == "Query") && len(x.Args) >= 2:
			// tx.Exec(ctx, `INSERT INTO decision …`, id, summary, rationale, …) — 값 인자 전부
			if lit, ok := x.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if sql, err := strconv.Unquote(lit.Value); err == nil && decisionSQL.MatchString(sql) {
					for _, a := range x.Args[2:] {
						c.add(a)
					}
				}
			}
		case pkg == "" && name == "append" && len(x.Args) >= 2:
			if sel, ok := x.Args[0].(*ast.SelectorExpr); ok && sinkFields[sel.Sel.Name] {
				for _, a := range x.Args[1:] {
					c.add(a)
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
			case *ast.IndexExpr: // e.Payload[key] = "[마스킹됨 · …]" — 피드가 그대로 보여 준다
				if sel, ok := l.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Payload" {
					c.add(x.Rhs[i])
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

// addAll 은 노드 아래의 문자열 리터럴을 전부 문장으로 센다(sinkFuncs·sinkVars).
func (c *collector) addAll(n ast.Node) {
	ast.Inspect(n, func(m ast.Node) bool {
		if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			c.add(lit)
		}
		return true
	})
}

func collect(t *testing.T) (pool []sentence, nouns []sentence, files []string) {
	pool, nouns, files, _ = collectSeen(t)
	return pool, nouns, files
}

func collectSeen(t *testing.T) (pool []sentence, nouns []sentence, files []string, seen map[string]bool) {
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
		c := &collector{fset: fset, file: f, consts: map[string]ast.Expr{},
			apperrExcluded: daemonAPI[f], seen: seen}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range vs.Names {
					if i < len(vs.Values) {
						c.consts[n.Name] = vs.Values[i]
					}
				}
			}
		}
		ast.Inspect(af, c.visit)
		pool = append(pool, c.out...)
		nouns = append(nouns, c.notFoundNouns...)
	}
	return pool, nouns, files, seen
}

// prose 는 사람이 읽는 글처럼 생긴 것 — 한글이 있거나 영어 단어가 둘 이상.
// `"%s: %s"` 나 `"director"` 같은 것은 코드다.
var (
	hangul   = regexp.MustCompile(`[가-힣]`)
	twoWords = regexp.MustCompile(`[A-Za-z]{2,}\s+[A-Za-z]{2,}`)
)

func isProse(s string) bool {
	return hangul.MatchString(s) || twoWords.MatchString(s)
}

func hits(pool []sentence, re *regexp.Regexp, allow func(sentence) bool) []string {
	var out []string
	for _, s := range pool {
		if re.MatchString(s.text) && (allow == nil || !allow(s)) {
			out = append(out, s.String())
		}
	}
	return out
}

// ── 범위 — 자물쇠가 조용히 헐거워지지 않게 ─────────────────────────────────

func TestScope(t *testing.T) {
	pool, nouns, files, seen := collectSeen(t)
	var prose []sentence
	for _, s := range pool {
		if isProse(s.text) {
			prose = append(prose, s)
		}
	}
	t.Logf("files=%d sinks=%d prose=%d notFound=%d", len(files), len(pool), len(prose), len(nouns))
	if os.Getenv("WORDING_DUMP") != "" { // 전수 표를 뽑을 때: WORDING_DUMP=1 go test -run TestScope -v ./internal/wording
		for _, s := range prose {
			t.Log(s.String())
		}
	}

	if len(files) < 60 {
		t.Errorf("소스 %d개만 훑었다 — internal·cmd 전부가 범위여야 한다", len(files))
	}
	if len(prose) < 330 { // PR #192 252 → T-S13b 349(sinkFuncs·sinkVars·decision·Content …)
		t.Errorf("사람이 읽는 문장이 %d개뿐 — sink 규칙이 빠졌다(apperr·SystemPost·detail/note 칸·sinkFuncs·decision)", len(prose))
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
	// NN1 — daemonAPI 예외 목록은 여기 못박힌다. 파일을 더하려면 이 줄을 고쳐야 하고,
	// 그러면 리뷰가 본다(web/lib/wording.test.ts 의 expect(EXCLUDE).toEqual 과 같은 자리).
	// PR #192 리뷰 INJ6b: agents.go 를 목록에 넣고 Field 문장을 영어로 — 이 단정이 잡는다.
	wantDaemonAPI := []string{ // 정렬 순
		"internal/events/events.go",
		"internal/eventschema/eventschema.go",
		"internal/httpapi/daemon.go",
	}
	var gotDaemonAPI []string
	for f := range daemonAPI {
		gotDaemonAPI = append(gotDaemonAPI, f)
	}
	sort.Strings(gotDaemonAPI)
	if strings.Join(gotDaemonAPI, "\n") != strings.Join(wantDaemonAPI, "\n") {
		t.Errorf("daemonAPI 예외 목록이 바뀌었다:\n  got  %v\n  want %v", gotDaemonAPI, wantDaemonAPI)
	}
	for _, f := range wantDaemonAPI {
		found := false
		for _, g := range files {
			if g == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("daemonAPI %q 가 소스에 없다 — 예외가 유령을 가리킨다", f)
		}
	}
	if len(nouns) < 40 {
		t.Errorf("apperr.NotFound 호출이 %d개뿐 — 수집이 새고 있다", len(nouns))
	}
	// 리뷰가 짚은 자리들 — 여기가 풀에 없으면 자물쇠는 잠긴 척만 한다.
	for _, f := range []string{
		"internal/sessions/sessions.go", // "Session started. Goal:" 이 있던 자리
		"internal/httpapi/handlers_participants.go",
		"internal/httpapi/handlers_completion.go",
		"internal/httpapi/principal.go",
		"internal/sessions/pause.go",   // Hint
		"internal/tasks/cancel.go",     // FeedNote · Reason
		"internal/workdirs/gc.go",      // GCReasonText · Problems
		"internal/httpapi/budget.go",   // task_event detail · HITL question
		"internal/queue/bundle.go",     // S-62 진단 이벤트 detail
		"internal/httpapi/daemon.go",   // task_event note (gc 거부 — v0.7.4 문장)
		"internal/runtimes/offline.go", // CandidateVerdict.Reason
		// PR #192 리뷰 NN2 — sink 밖에 있던 자리들
		"internal/runtimes/candidates.go",          // Reason = nullable.NewNullableWithValue(…)
		"internal/router/loop.go",                  // LimitText 반환값
		"internal/sessions/summary.go",             // BuildSummaryBody · decisionLine
		"internal/sessions/completion.go",          // ValidateTree → Field message
		"internal/httpapi/handlers_sessions_p3.go", // INSERT INTO decision (NN3)
		"internal/httpapi/hitl_sweep.go",           // insertDecision rationale
		"internal/httpapi/handlers_inbox.go",       // title =
		"internal/httpapi/handlers_artifacts.go",   // MessageCreate.Content
		"internal/messages/hitlcard.go",            // CardBody · hitlTypeLabel
		"internal/events/mask.go",                  // e.Payload[key] =
		"internal/apperr/apperr.go",                // titles · statusLabels · Title/NotFound/…
		"internal/agents/agents.go",                // field(…) 지역 헬퍼
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

// ── NotFound 명사표 ─────────────────────────────────────────────────────

func TestNotFoundNounsAreKorean(t *testing.T) {
	_, nouns, _ := collect(t)
	for _, n := range nouns {
		if _, ok := apperr.NotFoundNouns[n.text]; !ok {
			t.Errorf("%s — apperr.NotFound(%q) 에 한국어 명사가 없다(apperr.NotFoundNouns 에 추가)", n.String(), n.text)
		}
	}
	for k, v := range apperr.NotFoundNouns {
		if !hangul.MatchString(v) {
			t.Errorf("NotFoundNouns[%q] = %q — 한국어여야 한다", k, v)
		}
	}
}

// ── 화면의 말 = 한국어 ──────────────────────────────────────────────────

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

// ── §8.4 용어표 + 내부 용어 — 옛말 0건 ─────────────────────────────────

func TestNoInternalTerms(t *testing.T) {
	pool, _, _ := collect(t)
	var prose []sentence
	for _, s := range pool {
		if isProse(s.text) {
			prose = append(prose, s)
		}
	}
	rows := []struct {
		label string
		re    *regexp.Regexp
	}{
		// §8.4 표
		{"Workdir → 작업 폴더", regexp.MustCompile(`(?i)\bworkdirs?\b`)},
		{"Runtime(s) → 컴퓨터", regexp.MustCompile(`(?i)\bruntimes?\b`)},
		{"런타임 → 컴퓨터 (산문까지 전부)", regexp.MustCompile(`런타임`)},
		{"머신 → 컴퓨터", regexp.MustCompile(`머신`)},
		{"Inbox → 받은 요청", regexp.MustCompile(`\bInbox\b`)},
		{"owner·admin → 소유자·관리자", regexp.MustCompile(`\b(owner|admin)\b`)},
		// 내부 용어 (web/lib/wording.test.ts 의 INTERNAL 과 같은 목록 + 데몬 프로토콜 동사)
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
		// 사양 번호·계약 이름은 주석에 — 사용자의 다음 행동을 바꾸지 않는다.
		{"FR-x.y · E1-02 · §", regexp.MustCompile(`\bFR-\d|\bE\d{1,2}-\d{2}\b|§|\bPRD\b|daemon-protocol|openapi`)},
		// camelCase API 연산 이름 (rebindSession · ArchiveAgent)
		{"API 연산 이름", regexp.MustCompile(`\b[a-z]+(?:[A-Z][a-z]+)+\b`)},
		// 한국어 문장 안에 내부 키를 괄호로 노출하지 않는다 (PR #188 리뷰 NN5)
		{"한글(snake_key)", regexp.MustCompile(`[가-힣]\s*\([a-z][a-z0-9]*_[a-z0-9_]+\)`)},
	}
	for _, r := range rows {
		if h := hits(prose, r.re, nil); len(h) > 0 {
			t.Errorf("%s — %d건:\n  %s", r.label, len(h), strings.Join(h, "\n  "))
		}
	}
}

// ── 예외 하나 — 역할명은 영어를 유지한다 ──────────────────────────────

func TestRoleNamesStayEnglish(t *testing.T) {
	pool, _, _ := collect(t)
	if h := hits(pool, regexp.MustCompile(`디렉터[^리]|디렉터$|감독관|연출자|부감독`), nil); len(h) > 0 {
		t.Errorf("Director·deputy 는 영어 그대로(§8.4 예외):\n  %s", strings.Join(h, "\n  "))
	}
}
