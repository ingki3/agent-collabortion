package acp_test

// S-66 관측 — 실기 claude_code 한 턴에서 "모델이 긴 tool 입력을 생성하는 동안
// 데몬에 무엇이 도착하는가"를 표로 남긴다 (plan/P2_BACKLOG.md S-66, T-D12).
//
// 이 파일은 유닛 테스트가 아니라 **계측기**다. COLAB_S66_OBSERVE=1 이 아니면
// 건너뛴다 — 실제 어댑터(npx)·실제 로그인·실제 모델 호출이 필요하다.
//
//	COLAB_S66_OBSERVE=1 COLAB_S66_OUT=/tmp/s66.md COLAB_S66_CHARS=3000 \
//	  go test ./internal/harness/acp -run TestS66ObserveLongWrite -v -count=1 -timeout 20m
//	COLAB_S66_KIND=hermes … 로 hermes 판(원시 스트림 없음, Lead 요청 2026-09-13).
//
// 재는 것: 모든 수신(session/update 종류별 · session/request_permission ·
// _claude/sdkMessage 의 type/subtype/event.type/delta.type)을 시각과 함께
// 기록하고, (1) session/update 만 봤을 때의 최대 공백, (2) 권한 요청까지 봤을
// 때의 최대 공백, (3) 원시 스트림까지 봤을 때의 최대 공백을 낸다. 지금의 stall
// 워처는 (2) 를 세므로 (2) 가 stall_seconds 를 넘으면 그것이 S-66 의 원인이다.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

type s66Event struct {
	at   time.Duration
	lane string // update | permission | raw
	kind string
	size int
}

func TestS66ObserveLongWrite(t *testing.T) {
	if os.Getenv("COLAB_S66_OBSERVE") != "1" {
		t.Skip("COLAB_S66_OBSERVE=1 to run the real-runtime observation")
	}
	chars := 3000
	if v, err := strconv.Atoi(os.Getenv("COLAB_S66_CHARS")); err == nil && v > 0 {
		chars = v
	}
	model := os.Getenv("COLAB_S66_MODEL")
	if model == "" {
		model = "claude-sonnet-5"
	}
	raw := os.Getenv("COLAB_S66_RAW") != "0"
	kind := contracts.RuntimeClaudeCode
	if os.Getenv("COLAB_S66_KIND") == "hermes" {
		kind = contracts.RuntimeHermes
		raw = false // hermes 에는 원시 스트림이 없다 (harness §7 v0.8.5)
		if os.Getenv("COLAB_S66_MODEL") == "" {
			model = "anthropic:claude-sonnet-5"
		}
	}
	dir := t.TempDir()

	cmd, args := acp.Command(kind, "", nil)
	env := acp.Env(kind, acp.TaskEnv{}, nil) // token 없음 = §4.5 모양
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	c, err := acp.Spawn(ctx, acp.Config{Command: cmd, Args: args, Env: env, Dir: dir, KillAfter: 5 * time.Second})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer c.Close()

	var mu sync.Mutex
	var evs []s66Event
	start := time.Now()
	add := func(lane, kind string, size int) {
		mu.Lock()
		evs = append(evs, s66Event{at: time.Since(start), lane: lane, kind: kind, size: size})
		mu.Unlock()
	}
	c.Permission = func(p acp.RequestPermissionParams) acp.PermissionOutcome {
		add("permission", "request_permission:"+p.ToolCall.Title, 0)
		return acp.DefaultPolicy{}.Decide(p).Outcome
	}
	c.OnNotification(func(method string, params json.RawMessage) {
		switch method {
		case acp.MethodSessionUpdate:
			var p acp.SessionUpdateParams
			_ = json.Unmarshal(params, &p)
			var u acp.Update
			_ = json.Unmarshal(p.Update, &u)
			k := u.SessionUpdate
			if u.Status != "" {
				k += ":" + u.Status
			}
			add("update", k, len(p.Update))
		case acp.ExtNotificationSDKMessage:
			var p struct {
				Message json.RawMessage `json:"message"`
			}
			_ = json.Unmarshal(params, &p)
			var head struct {
				Type    string `json:"type"`
				Subtype string `json:"subtype"`
				Event   struct {
					Type  string `json:"type"`
					Delta struct {
						Type string `json:"type"`
					} `json:"delta"`
					ContentBlock struct {
						Type string `json:"type"`
					} `json:"content_block"`
				} `json:"event"`
			}
			_ = json.Unmarshal(p.Message, &head)
			k := head.Type
			if head.Subtype != "" {
				k += "/" + head.Subtype
			}
			if head.Event.Type != "" {
				k += "/" + head.Event.Type
				if head.Event.Delta.Type != "" {
					k += "/" + head.Event.Delta.Type
				}
				if head.Event.ContentBlock.Type != "" {
					k += "/" + head.Event.ContentBlock.Type
				}
			}
			add("raw", k, len(params))
		default:
			add("raw", method, len(params))
		}
	})

	if _, err := c.Initialize(ctx, "s66"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	meta := acp.Meta(kind, acp.MetaOptions{Brief: "You are a careful technical writer.", RawSDKMessages: raw})
	s, err := c.NewSession(ctx, dir, nil, meta)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if kind == contracts.RuntimeHermes {
		if err := c.SetModel(ctx, s.SessionID, model); err != nil {
			t.Logf("set model: %v (continuing)", err)
		}
	} else if _, err := c.SetConfigOption(ctx, s.SessionID, "model", model); err != nil {
		t.Logf("set model: %v (continuing)", err)
	}
	prompt := fmt.Sprintf("Use the Write tool exactly once to create the file essay.md in the current directory. "+
		"Its content must be a single continuous essay in Korean of about %d characters (no headings, no lists) about how rivers shape cities. "+
		"Do not read any file, do not run any shell command, do not explain first: call Write immediately as your first and only action. "+
		"After the tool returns, reply with exactly one word: done.", chars)
	add("prompt", "session/prompt sent", 0)
	pr, perr := c.Prompt(ctx, s.SessionID, prompt)
	add("prompt", "session/prompt answered", 0)
	if perr != nil {
		t.Logf("prompt error: %v", perr)
	} else {
		t.Logf("stopReason=%s", pr.StopReason)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "essay.md")); err == nil {
		t.Logf("essay.md written: %d bytes", len(b))
		add("prompt", fmt.Sprintf("essay.md %d bytes on disk", len(b)), len(b))
	} else {
		t.Logf("essay.md missing: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	report := s66Report(evs, string(kind), chars, model, raw)
	t.Log("\n" + report)
	if out := os.Getenv("COLAB_S66_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// s66Report renders the timeline + the three gap numbers as markdown.
func s66Report(evs []s66Event, kind string, chars int, model string, raw bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# S-66 관측 — 실기 %s, Write %d자, model=%s, raw stream=%v, %s\n\n", kind, chars, model, raw, time.Now().UTC().Format(time.RFC3339))
	// 1. counts per kind
	counts := map[string]int{}
	bytes := map[string]int{}
	for _, e := range evs {
		k := e.lane + " · " + e.kind
		counts[k]++
		bytes[k] += e.size
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("## 수신 종류별 개수\n\n| lane · kind | n | bytes |\n|---|---:|---:|\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", k, counts[k], bytes[k])
	}
	// 2. max gaps by what is counted as activity
	gap := func(lanes ...string) (max time.Duration, from, to string) {
		in := map[string]bool{}
		for _, l := range lanes {
			in[l] = true
		}
		var prev *s66Event
		for i := range evs {
			e := &evs[i]
			if !in[e.lane] && e.lane != "prompt" {
				continue
			}
			if prev != nil && e.at-prev.at > max {
				max, from, to = e.at-prev.at, prev.kind, e.kind
			}
			prev = e
		}
		return
	}
	b.WriteString("\n## 활동으로 세는 것에 따른 최대 공백\n\n| 세는 것 | 최대 공백 | 구간 |\n|---|---:|---|\n")
	g1, f1, t1 := gap("update")
	fmt.Fprintf(&b, "| session/update 만 | %s | %s → %s |\n", g1.Round(time.Millisecond), f1, t1)
	g2, f2, t2 := gap("update", "permission")
	fmt.Fprintf(&b, "| session/update + 권한 요청 (**지금 stall 워처**) | %s | %s → %s |\n", g2.Round(time.Millisecond), f2, t2)
	g3, f3, t3 := gap("update", "permission", "raw")
	fmt.Fprintf(&b, "| + 원시 `_claude/sdkMessage` | %s | %s → %s |\n", g3.Round(time.Millisecond), f3, t3)
	// 3. timeline, collapsing runs of the same kind
	b.WriteString("\n## 타임라인 (같은 종류의 연속 수신은 한 줄로 접음)\n\n| t | lane | kind | n | 구간 길이 |\n|---|---|---|---:|---:|\n")
	for i := 0; i < len(evs); {
		j := i
		for j+1 < len(evs) && evs[j+1].lane == evs[i].lane && evs[j+1].kind == evs[i].kind {
			j++
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s |\n", evs[i].at.Round(time.Millisecond), evs[i].lane, evs[i].kind, j-i+1, (evs[j].at - evs[i].at).Round(time.Millisecond))
		i = j + 1
	}
	return b.String()
}
