package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// T-CARD-COMMENT (PRD v0.19.17 FR-3.8 4 · openapi v0.3.11): acceptCard needs
// a comment — missing, empty or blank is 422 judgement_comment_required with
// the same errors[] shape as the other card 422s, and the card stays
// judgeable; a comment is trimmed and lands in CardJudgement.comment (reason
// null). reviseCard is untouched: no comment, its reason as before.
func TestCardAcceptComment(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	leadTask, c1, c2 := f.twoChildren(t)
	f.report(t, c1)
	f.finishCompleted(t, c1)
	f.report(t, c2)
	f.finishCompleted(t, c2)
	if !joinFired(t, f, leadTask) {
		t.Fatal("join did not fire")
	}
	leadNext, _, _ := f.queuedOnLane(t, f.laneOfTask(t, leadTask))
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed' WHERE id = $1`, leadTask); err != nil {
		t.Fatal(err)
	}
	b := f.claimBundle(t, leadNext)
	lead := &client{t: t, srv: f.api.srv, bearer: b.TaskToken}
	card1, card2 := f.taskCard(t, c1), f.taskCard(t, c2)
	accept := f.p + "/cards/" + card1.ID.String() + "/accept"

	for name, body := range map[string]map[string]any{
		"missing": {},
		"empty":   {"comment": ""},
		"blank":   {"comment": "  \n	 "},
		// #406 리뷰 NN1 — 보이지 않는 글자만(ZWSP·BOM·WJ·한글 채움)도 빈 코멘트다.
		"invisible": {"comment": "\u200b\ufeff\u2060\u3164 \u200b"},
	} {
		st, out, _ := lead.do("POST", accept, body)
		if st != 422 || str(out, "code") != "judgement_comment_required" {
			t.Fatalf("(%s) %d %v, want 422 judgement_comment_required", name, st, out)
		}
		errs, _ := out["errors"].([]any)
		if len(errs) != 1 {
			t.Fatalf("(%s) errors[] = %v", name, out["errors"])
		}
		e := errs[0].(map[string]any)
		if e["field"] != "comment" || e["code"] != "judgement_comment_required" || e["message"] != "무엇을 확인했는지 코멘트를 적으세요" {
			t.Fatalf("(%s) errors[0] = %v", name, e)
		}
	}
	// A refused acceptance writes nothing: the card is still waiting.
	if c := f.taskCard(t, c1); c.Status != "result_submitted" || c.Judgement != nil {
		t.Fatalf("after refused accepts: %s %s", c.Status, c.Judgement)
	}
	// 600 characters is the bound — counted in characters, not bytes (#406 리뷰 NN5):
	// 601 한글 is 422; 601 four-byte emoji is 422 too.
	if st, out, _ := lead.do("POST", accept, map[string]any{"comment": strings.Repeat("가", 601)}); st != 422 {
		t.Fatalf("(too long) %d %v", st, out)
	}
	if st, out, _ := lead.do("POST", accept, map[string]any{"comment": strings.Repeat("👍", 601)}); st != 422 {
		t.Fatalf("(too long emoji) %d %v", st, out)
	}

	// Revise is not affected: reason only, no comment needed.
	if st, out, _ := lead.do("POST", f.p+"/cards/"+card2.ID.String()+"/revise", map[string]any{"reason": "출처가 없습니다"}); st != 200 {
		t.Fatalf("(revise) %d %v", st, out)
	}
	var rj json.RawMessage
	if err := f.pool.QueryRow(ctx, `SELECT versions->0->'judgement' FROM task_card WHERE id = $1`, card2.ID).Scan(&rj); err != nil {
		t.Fatal(err)
	}
	var rev map[string]any
	_ = json.Unmarshal(rj, &rev)
	if rev["action"] != "revise_requested" || rev["reason"] != "출처가 없습니다" || rev["comment"] != nil {
		t.Fatalf("(revise) judgement = %s", rj)
	}

	// Accept with a comment: trimmed, stored, answered.
	st, out, _ := lead.do("POST", accept, map[string]any{"comment": "  기준 1 은 테스트 로그로, 기준 2 는 아티팩트를 열어 확인했습니다  "})
	if st != 200 || str(out, "status") != "accepted" {
		t.Fatalf("(accept) %d %v", st, out)
	}
	j, _ := out["judgement"].(map[string]any)
	if j["action"] != "accepted" || j["comment"] != "기준 1 은 테스트 로그로, 기준 2 는 아티팩트를 열어 확인했습니다" || j["reason"] != nil {
		t.Fatalf("(accept) judgement = %v", j)
	}
	if by, _ := j["by"].(map[string]any); by["name"] != "Lead" || by["kind"] != "agent" {
		t.Fatalf("(accept) by = %v", j["by"])
	}
	// getCard reads the same judgement back.
	got := f.api.must(200, "GET", f.p+"/cards/"+card1.ID.String(), nil)
	if gj, _ := got["judgement"].(map[string]any); gj["comment"] != j["comment"] {
		t.Fatalf("getCard judgement = %v", got["judgement"])
	}

	// A person (the Director) undoing it still needs a reason (revise), and
	// the person's accept path needs a comment like the agent's.
	if st, out, _ := f.api.do("POST", f.p+"/cards/"+card1.ID.String()+"/revise", map[string]any{"reason": "다시 보자"}); st != 200 {
		t.Fatalf("(person revise) %d %v", st, out)
	}
}

// The person path (cookie) is held to the same rule.
func TestCardAcceptCommentPerson(t *testing.T) {
	f := newP2Fixture(t)
	_, c1, c2 := f.twoChildren(t)
	f.report(t, c1)
	f.finishCompleted(t, c1)
	f.report(t, c2)
	f.finishCompleted(t, c2)
	card := f.taskCard(t, c1)
	accept := f.p + "/cards/" + card.ID.String() + "/accept"
	if st, out, _ := f.api.do("POST", accept, map[string]any{"comment": " "}); st != 422 || str(out, "code") != "judgement_comment_required" {
		t.Fatalf("(person blank) %d %v", st, out)
	}
	st, out, _ := f.api.do("POST", accept, map[string]any{"comment": "화면에서 직접 돌려 봤습니다"})
	if st != 200 {
		t.Fatalf("(person accept) %d %v", st, out)
	}
	j, _ := out["judgement"].(map[string]any)
	if j["comment"] != "화면에서 직접 돌려 봤습니다" || j["reason"] != nil {
		t.Fatalf("(person accept) judgement = %v", j)
	}
	if by, _ := j["by"].(map[string]any); by["kind"] != "user" || by["name"] != "Dir" {
		t.Fatalf("(person accept) by = %v", j["by"])
	}

	// (multibyte-boundary) #406 리뷰 NN5 — the bound is 600 characters, not
	// bytes: 600 한글 (1800 bytes) wrapped in invisible runes is accepted and
	// stored whole; a byte-count check would refuse it.
	full := strings.Repeat("가", 600)
	accept2 := f.p + "/cards/" + f.taskCard(t, c2).ID.String() + "/accept"
	st, out, _ = f.api.do("POST", accept2, map[string]any{"comment": "\ufeff" + full + "\u200b"})
	if st != 200 {
		t.Fatalf("(multibyte-boundary) 600 한글 → %d %v", st, out)
	}
	if j2, _ := out["judgement"].(map[string]any); j2["comment"] != full {
		t.Fatalf("(multibyte-boundary) stored %d runes", len([]rune(fmt.Sprint(j2["comment"]))))
	}
}
