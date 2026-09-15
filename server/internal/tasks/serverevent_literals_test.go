package tasks

// S-54 (PR #168 리뷰 NN1): the S-52 runtime guard only sees the server-written
// task_event rows that a test run actually EXECUTES. A call site nobody's test
// reaches — a sweep that fires once a day, a refusal path — can carry a payload
// the schema rejects and stay green forever. This test walks the module's
// sources with go/ast, collects every InsertServerEvent · InsertServerEventOnce ·
// writeServerEvent · writeServerEventOnce call whose (class, verb, object_ref,
// outcome, payload) are written as literals, and validates each one against
// contracts/task_event.schema.json — the whole table, whether or not anything
// ran it.
//
// Non-literal parts (a `detail` built from a variable, a uuid.String()) are
// stood in by a placeholder string: the schema's shape rules — closed
// payloads, enums, required keys — are what S-52 was about, and those are
// visible in the literal.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/eventschema"
)

// serverEventWriters → number of trailing args after which (class, verb,
// object_ref, outcome, payload, now) follow. All four end with the same six.
var serverEventWriters = map[string]bool{
	"InsertServerEvent": true, "InsertServerEventOnce": true,
	"writeServerEvent": true, "writeServerEventOnce": true,
}

type literalSite struct {
	file                            string
	line                            int
	class, verb, objectRef, outcome string
	payload                         map[string]any
	skipped                         string // why the site could not be read as literals
}

func (s literalSite) String() string {
	return s.file + ":" + strconv.Itoa(s.line) + " " + s.class + "/" + s.verb + " " + s.objectRef + " (" + s.outcome + ")"
}

// placeholder stands in for any non-literal value.
const placeholder = "x"

type siteCollector struct {
	fset   *token.FileSet
	file   string
	consts map[string]ast.Expr
	sites  []literalSite
}

func (c *siteCollector) str(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		}
	case *ast.ParenExpr:
		return c.str(x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			l, lok := c.str(x.X)
			r, rok := c.str(x.Y)
			if !lok {
				l = placeholder
			}
			if !rok {
				r = placeholder
			}
			return l + r, true
		}
	case *ast.Ident:
		if decl, ok := c.consts[x.Name]; ok {
			return c.str(decl)
		}
	case *ast.CallExpr:
		// string(e.Type), fmt.Sprintf(…), id.String() — a string of some shape.
		return placeholder, true
	case *ast.SelectorExpr:
		// pkg.Const — contracts.FailConfig and the like; not resolvable here
		// without types, but it is a string.
		return placeholder, true
	}
	return "", false
}

func (c *siteCollector) value(e ast.Expr) any {
	switch x := e.(type) {
	case *ast.CompositeLit:
		if m := c.mapLit(x); m != nil {
			return m
		}
		return placeholder
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT:
			v, _ := strconv.Atoi(x.Value)
			return v
		case token.FLOAT:
			v, _ := strconv.ParseFloat(x.Value, 64)
			return v
		}
	case *ast.Ident:
		switch x.Name {
		case "true":
			return true
		case "false":
			return false
		}
	}
	if s, ok := c.str(e); ok {
		return s
	}
	return placeholder
}

func (c *siteCollector) mapLit(lit *ast.CompositeLit) map[string]any {
	mt, ok := lit.Type.(*ast.MapType)
	if !ok {
		return nil
	}
	if k, ok := mt.Key.(*ast.Ident); !ok || k.Name != "string" {
		return nil
	}
	out := map[string]any{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return nil
		}
		key, ok := c.str(kv.Key)
		if !ok {
			return nil
		}
		out[key] = c.value(kv.Value)
	}
	return out
}

func (c *siteCollector) visit(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return true
	}
	var name string
	switch f := call.Fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	}
	if !serverEventWriters[name] || len(call.Args) < 6 {
		return true
	}
	a := call.Args[len(call.Args)-6:]
	site := literalSite{file: c.file, line: c.fset.Position(call.Pos()).Line}
	var ok1, ok2, ok3, ok4 bool
	site.class, ok1 = c.str(a[0])
	site.verb, ok2 = c.str(a[1])
	site.objectRef, ok3 = c.str(a[2])
	site.outcome, ok4 = c.str(a[3])
	if !ok3 {
		site.objectRef = placeholder
	}
	switch {
	case !ok1 || !ok2 || !ok4 || site.class == placeholder || site.verb == placeholder || site.outcome == placeholder:
		site.skipped = "class/verb/outcome are not literals"
	default:
		lit, isLit := a[4].(*ast.CompositeLit)
		if !isLit {
			site.skipped = "payload is not a composite literal"
		} else if m := c.mapLit(lit); m == nil {
			site.skipped = "payload literal is not map[string]any"
		} else {
			site.payload = m
		}
	}
	c.sites = append(c.sites, site)
	return true
}

func collectServerEventSites(t *testing.T) []literalSite {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	fset := token.NewFileSet()
	var sites []literalSite
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if e.Name() == "gen" || e.Name() == "testdb" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			af, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			c := &siteCollector{fset: fset, file: filepath.ToSlash(rel), consts: map[string]ast.Expr{}}
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
			sites = append(sites, c.sites...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		return sites[i].line < sites[j].line
	})
	return sites
}

// TestServerEventLiteralsMatchSchema is S-54: every literal server-written
// task_event in the module validates, whether or not a test ever reaches it.
func TestServerEventLiteralsMatchSchema(t *testing.T) {
	sites := collectServerEventSites(t)
	// The definitions themselves (serverevent.go) and the validate-only helper
	// are calls too, but carry parameters, not literals — they are the skipped
	// ones expected below.
	var checked, skipped int
	for _, s := range sites {
		if s.skipped != "" {
			skipped++
			t.Logf("skipped %s — %s", s.String(), s.skipped)
			continue
		}
		checked++
		if err := eventschema.ValidateServerEvent(s.class, s.verb, s.objectRef, s.outcome, s.payload); err != nil {
			t.Errorf("%s does not match contracts/task_event.schema.json: %v\n  payload: %v", s.String(), err, s.payload)
		}
	}
	t.Logf("server task_event literal sites: checked=%d skipped=%d", checked, skipped)
	// S-54 counted 16 sites at PR #168; the table has only grown since. A drop
	// below that means the walk stopped seeing something, not that the server
	// stopped writing feed rows.
	if checked < 16 {
		t.Errorf("only %d literal call sites validated — the collector is missing writers (S-54 counted 16)", checked)
	}
	// Every site whose class/verb/outcome are literal must have a literal
	// payload too: a payload passed as a variable is a table row this test
	// cannot read, and the S-52 runtime guard is then the only thing watching it.
	for _, s := range sites {
		if s.skipped == "payload is not a composite literal" && s.class != "" && s.class != placeholder {
			t.Errorf("%s passes its payload as a variable — write it as a literal so S-54's table can validate it", s.String())
		}
	}
}

// TestServerEventLiteralsCollectorSeesAViolation proves the walk reads the
// payload deeply enough to fail: a `status` payload with a top-level `note`
// (the exact pre-S-52 shape) must be rejected when fed through the same
// evaluator.
func TestServerEventLiteralsCollectorSeesAViolation(t *testing.T) {
	src := `package x
func f() {
	InsertServerEvent(ctx, tx, id, 1, "status", "note", "cost.unpriced", "info",
		map[string]any{"note": "사람이 읽는 문장", "model": m + " (추정)"}, now)
	InsertServerEvent(ctx, tx, id, 1, "status", "cancel", "director", "ok",
		map[string]any{"command": "lane cancel", "args": map[string]any{"note": "사람이 중단함", "requested_by": u.String()}}, now)
}`
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := &siteCollector{fset: fset, file: "x.go", consts: map[string]ast.Expr{}}
	ast.Inspect(af, c.visit)
	if len(c.sites) != 2 {
		t.Fatalf("sites = %d, want 2", len(c.sites))
	}
	if err := eventschema.ValidateServerEvent(c.sites[0].class, c.sites[0].verb, c.sites[0].objectRef, c.sites[0].outcome, c.sites[0].payload); err == nil {
		t.Errorf("the pre-S-52 shape validated: %v", c.sites[0].payload)
	}
	if err := eventschema.ValidateServerEvent(c.sites[1].class, c.sites[1].verb, c.sites[1].objectRef, c.sites[1].outcome, c.sites[1].payload); err != nil {
		t.Errorf("a well-formed nested literal was rejected: %v (%v)", err, c.sites[1].payload)
	}
	if args, _ := c.sites[1].payload["args"].(map[string]any); args["requested_by"] != placeholder {
		t.Errorf("non-literal value did not become the placeholder: %v", c.sites[1].payload)
	}
}
