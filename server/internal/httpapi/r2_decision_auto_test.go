package httpapi

import (
	"testing"
	"time"
)

// TestR2DecisionAuto: openapi Decision.auto (E7-12 「자동」) was a column
// (0014) that none of the three readers selected — listDecisions, readRoom
// and the `decision.created` frame all answered without it, so the web could
// not tell an auto_answered default from a person's choice (#304 리뷰 범위 밖
// 발견). They now scan sessions.DecisionColumns into one DecisionRow.
func TestR2DecisionAuto(t *testing.T) {
	f := newRoomReadFixture(t)
	autoOf := func(d map[string]any) any {
		v, ok := d["auto"]
		if !ok {
			return "<absent>"
		}
		return v
	}

	// listDecisions: both values, as stored.
	f.exec(t, `INSERT INTO decision (session_id, summary, source, auto, created_at) VALUES ($1, '기한 만료 기본값', 'hitl', true, now())`, f.sessionID)
	f.exec(t, `INSERT INTO decision (session_id, summary, source, auto, created_at) VALUES ($1, '사람이 고름', 'hitl', false, now())`, f.sessionID)
	want := map[string]any{"기한 만료 기본값": true, "사람이 고름": false}
	seen := 0
	for _, raw := range f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/decisions", nil) {
		d := raw.(map[string]any)
		if w, ok := want[str(d, "summary")]; ok {
			seen++
			if autoOf(d) != w {
				t.Fatalf("listDecisions %q auto = %v, want %v", str(d, "summary"), autoOf(d), w)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("listDecisions returned %d of the 2 planted decisions", seen)
	}

	// readRoom (FR-4.5): another room's decision, same mapping.
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	f.exec(t, `INSERT INTO decision (session_id, summary, source, auto, created_at) VALUES ($1, '인프라 자동 결정', 'hitl', true, now())`, f.b)
	f.fake.Advance(time.Second)
	st, out := f.read(t, tok, f.b, "")
	if st != 200 {
		t.Fatalf("read = %d %v", st, out)
	}
	found := false
	for _, raw := range out["decisions"].([]any) {
		if d := raw.(map[string]any); str(d, "summary") == "인프라 자동 결정" {
			found = true
			if autoOf(d) != true {
				t.Fatalf("readRoom decision auto = %v, want true", autoOf(d))
			}
		}
	}
	if !found {
		t.Fatal("readRoom did not return the planted decision")
	}

	// decision.created: an agent's recordDecision is never auto, and says so.
	st, rec := f.rawPost(t, f.p+"/sessions/"+f.sessionID+"/decisions", tok, map[string]any{"summary": "에이전트 결정"})
	if st != 201 {
		t.Fatalf("recordDecision = %d %v", st, rec)
	}
	if autoOf(rec) != false {
		t.Fatalf("recordDecision 201 auto = %v, want false", autoOf(rec))
	}
	fr := f.frame(t, "decision.created", str(rec, "id"))
	if p := fr["payload"].(map[string]any); autoOf(p) != false {
		t.Fatalf("decision.created payload auto = %v, want false", autoOf(p))
	}
}
