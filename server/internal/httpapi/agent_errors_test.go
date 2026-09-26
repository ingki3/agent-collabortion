package httpapi

// T-AGENTFIX B5: a TaskToken caller gets an error it can act on — which
// argument, what it must be, what it sent — while a person's session keeps
// the screens' sentence byte for byte.
//
// 실측(게임 제작 방): Writer 가 colab_artifact_get 에 아티팩트 이름을 넣고
// 「요청 형식이 올바르지 않습니다 — 화면을 새로고침한 뒤 다시 시도해 주세요」만 받았다.
//
// 회귀 주입: server.go ErrorHandlerFunc 의 isTaskCaller 분기를 지우면 FAIL.

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentErrors_TaskTokenGetsActionableSentence(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.wUUID, "W")
	agent := &client{t: t, srv: f.api.srv, bearer: tok}

	// The observed call: the artifact's NAME where its id belongs.
	st, raw, _ := agent.raw("GET", f.p+"/artifacts/"+url.PathEscape("게임 기획서.md"), nil)
	if st != 422 {
		t.Fatalf("status = %d %s, want 422", st, raw)
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Errors []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "invalid_parameter" {
		t.Errorf("code = %q — the CLI's exit mapping keys on it; it must not change", p.Code)
	}
	for _, s := range []string{p.Detail, p.Errors[0].Message} {
		if strings.Contains(s, "새로고침") {
			t.Errorf("agent got the screen sentence: %q", s)
		}
		for _, want := range []string{"artifact id(uuid)", "게임 기획서.md"} {
			if !strings.Contains(s, want) {
				t.Errorf("agent sentence %q lacks %q (which argument · what it sent)", s, want)
			}
		}
	}
	if len(p.Errors) != 1 || p.Errors[0].Field != "artifactId" {
		t.Errorf("errors = %+v, want field artifactId", p.Errors)
	}

	// Any other id parameter names itself.
	st, raw, _ = agent.raw("GET", f.p+"/rooms/회의실/messages", nil)
	if st != 422 || !strings.Contains(string(raw), "`roomId` 는 id(uuid)여야 합니다") || !strings.Contains(string(raw), "회의실") {
		t.Errorf("roomId: %d %s", st, raw)
	}
	// A body that is not JSON says so, not 「새로고침」.
	st, raw, _ = agent.raw("POST", f.p+"/rooms/"+f.sessionID+"/messages", nil, "Idempotency-Key", uuid.NewString())
	if st != 422 || strings.Contains(string(raw), "새로고침") || !strings.Contains(string(raw), "올바른 JSON 이 아닙니다") {
		t.Errorf("malformed body: %d %s", st, raw)
	}
	// A reused Idempotency-Key with another body: the agent is told the key
	// is the CLI's to make, not to refresh a screen.
	key := uuid.NewString()
	agent.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages", map[string]any{"content": "하나"}, "Idempotency-Key", key)
	st, raw, _ = agent.raw("POST", f.p+"/rooms/"+f.sessionID+"/messages", map[string]any{"content": "둘"}, "Idempotency-Key", key)
	if st != 422 || !strings.Contains(string(raw), agentIdempotencyReused) {
		t.Errorf("idempotency_key_reused: %d %s", st, raw)
	}
}

// The person's sentence is the screens' — unchanged.
func TestAgentErrors_PersonKeepsScreenSentence(t *testing.T) {
	f := newP2Fixture(t)
	st, raw, _ := f.api.raw("GET", f.p+"/artifacts/not-a-uuid", nil)
	if st != 422 || !strings.Contains(string(raw), "요청 형식이 올바르지 않습니다 — 화면을 새로고침한 뒤 다시 시도해 주세요") {
		t.Fatalf("person: %d %s", st, raw)
	}
	if strings.Contains(string(raw), "artifact id(uuid)") {
		t.Fatalf("person got the agent sentence: %s", raw)
	}
	key := uuid.NewString()
	f.api.must(201, "POST", f.p+"/rooms/"+f.sessionID+"/messages", map[string]any{"content": "하나"}, "Idempotency-Key", key)
	st, raw, _ = f.api.raw("POST", f.p+"/rooms/"+f.sessionID+"/messages", map[string]any{"content": "둘"}, "Idempotency-Key", key)
	if st != 422 || !strings.Contains(string(raw), "같은 요청 키로 다른 내용을 보냈습니다 — 화면을 새로고침한 뒤 다시 시도해 주세요") {
		t.Fatalf("person idempotency_key_reused: %d %s", st, raw)
	}
}
