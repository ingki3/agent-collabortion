package mcp_test

// T-RF1: callTool's wire bytes, before and after the switch became a table.
//
// Every tool is driven through Serve (the real JSON-RPC loop) with the same
// argument set — valid arguments against the fake server, the aliased fields
// (mention · depends_on · choices as a CSV string, request_info's `question`),
// arguments of the wrong JSON type (the decode error text), and a client with
// no task token (errorResult) — plus tools/list and an unknown tool. The
// transcript is every response line, byte for byte, and is compared with
// testdata/calltool.golden, which was recorded on the pre-table code
// (origin/dev fd629d2). `go test ./internal/mcp -run TestCallToolSnapshot
// -update` rewrites it; a refactor must not need to.
//
// 회귀 주입: 표의 colab_hitl_ask 가 choices CSV 를 풀지 않게 → FAIL;
// colab_hitl_request_info 의 question 별칭을 지우면 FAIL; 디코드 오류를
// codeInvalidRequest 로 → FAIL; 표에서 colab_room_read 를 빼면 FAIL.

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/mcp"
)

var update = flag.Bool("update", false, "rewrite testdata/calltool.golden")

// rawConn is dial without decoding: it keeps each response line as bytes.
type rawConn struct {
	t    *testing.T
	w    io.WriteCloser
	r    *lineReader
	id   int
	out  *strings.Builder
	repl *strings.Replacer
}

type lineReader struct{ r io.Reader }

func (l *lineReader) line() (string, error) {
	var b []byte
	one := make([]byte, 1)
	for {
		n, err := l.r.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				return string(b), nil
			}
			b = append(b, one[0])
		}
		if err != nil {
			return string(b), err
		}
	}
}

func rawDial(t *testing.T, c *client.Client, out *strings.Builder, repl *strings.Replacer) *rawConn {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() { _ = mcp.Serve(context.Background(), c, inR, outW, "snap"); outW.Close(); close(done) }()
	t.Cleanup(func() { inW.Close(); <-done })
	return &rawConn{t: t, w: inW, r: &lineReader{outR}, out: out, repl: repl}
}

func (c *rawConn) call(label, method string, params any) {
	c.t.Helper()
	c.id++
	msg := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	line, err := c.r.line()
	if err != nil {
		c.t.Fatalf("%s: read: %v", label, err)
	}
	c.out.WriteString("## " + label + "\n" + c.repl.Replace(line) + "\n")
}

// snapArgs is each tool's valid argument set (against clienttest), and an
// argument set of the wrong JSON type.
var snapArgs = map[string]map[string]any{
	"colab_room_get":             {},
	"colab_room_messages":        {"limit": 5},
	"colab_message_post":         {"body": "snap", "mention": "@Reviewer,@Lead"},
	"colab_status_set":           {"status": "working", "note": "보는 중"},
	"colab_lane_delegate":        {"agent": "Reviewer", "brief": "봐 주세요", "depends_on": "l1,l2"},
	"colab_decision_record":      {"summary": "A 로 간다", "rationale": "싸다"},
	"colab_artifact_submit":      {"type": "doc"},
	"colab_artifact_get":         {"artifact": "00000000-0000-0000-0000-00000000a001"},
	"colab_review_approve":       {"artifact": "00000000-0000-0000-0000-00000000a001"},
	"colab_review_reject":        {"artifact": "00000000-0000-0000-0000-00000000a001", "reason": "모자람"},
	"colab_hitl_ask":             {"question": "어디까지?", "default": "A", "choices": "A,B"},
	"colab_hitl_approve_request": {"summary": "배포"},
	"colab_hitl_request_info":    {"question": "키가 필요합니다"},
	"colab_room_list":            {},
	"colab_room_read":            {"room": "00000000-0000-0000-0000-0000000000r1"},
	"colab_work_propose":         {"goal": "새 미션", "why": "필요"},
}

func TestCallToolSnapshot(t *testing.T) {
	s := clienttest.New(t)
	var out strings.Builder
	env := s.Env(t.TempDir())
	// The fake's URL and the temp dir differ per run; everything else is
	// deterministic (clienttest's ids are fixed).
	repl := strings.NewReplacer(env["COLAB_SERVER_URL"], "http://FAKE", env["COLAB_STATE_DIR"], "/STATE")
	c := rawDial(t, client.New(client.FromEnv(clienttest.Getenv(env))), &out, repl)
	c.call("tools/list", "tools/list", nil)
	for _, tl := range mcp.Tools {
		c.call(tl.Name+" valid", "tools/call", map[string]any{"name": tl.Name, "arguments": snapArgs[tl.Name]})
		c.call(tl.Name+" no-args", "tools/call", map[string]any{"name": tl.Name})
		c.call(tl.Name+" wrong-type", "tools/call", map[string]any{"name": tl.Name, "arguments": []any{1}})
	}
	// Aliased fields given the wrong type decode through the alias.
	c.call("post mention=number", "tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{"body": "x", "mention": 5}})
	c.call("delegate depends_on=object", "tools/call", map[string]any{"name": "colab_lane_delegate", "arguments": map[string]any{"agent": "Reviewer", "brief": "b", "depends_on": map[string]any{}}})
	c.call("hitl_ask choices=list", "tools/call", map[string]any{"name": "colab_hitl_ask", "arguments": map[string]any{"question": "q", "default": "B", "choices": []string{"A", "B"}}})
	c.call("request_info what wins", "tools/call", map[string]any{"name": "colab_hitl_request_info", "arguments": map[string]any{"what": "W", "question": "Q"}})
	c.call("unknown tool", "tools/call", map[string]any{"name": "colab_room_delete", "arguments": map[string]any{}})

	// No token: every tool answers through errorResult.
	noTok := s.Env(t.TempDir())
	delete(noTok, "COLAB_TASK_TOKEN")
	repl2 := strings.NewReplacer(noTok["COLAB_SERVER_URL"], "http://FAKE", noTok["COLAB_STATE_DIR"], "/STATE")
	c2 := rawDial(t, client.New(client.FromEnv(clienttest.Getenv(noTok))), &out, repl2)
	for _, tl := range mcp.Tools {
		c2.call(tl.Name+" no-token", "tools/call", map[string]any{"name": tl.Name, "arguments": snapArgs[tl.Name]})
	}

	golden := filepath.Join("testdata", "calltool.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v — record it with -update on the pre-change code", err)
	}
	if got := out.String(); got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("calltool transcript differs at line %d:\n got  %s\n want %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("calltool transcript length %d lines, golden %d", len(gl), len(wl))
	}
}
