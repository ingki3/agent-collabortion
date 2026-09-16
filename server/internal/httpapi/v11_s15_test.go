package httpapi

import (
	"testing"
)

// TestS15ReviewHistoryIsAppendOnly is S-15 (PR #65 리뷰 NN3): a re-review
// does not overwrite the previous judgment. reject → approve leaves TWO rows
// in artifact_review, the artifact's `review` (openapi, unchanged contract)
// is the latest, and the first row still says reject — who reversed what,
// when, is readable.
func TestS15ReviewHistoryIsAppendOnly(t *testing.T) {
	f := newP2Fixture(t)
	f.setRole(t, f.wUUID, "reviewer")
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "agent_approval", "agent_id": f.w}, {"type": "user_approval"},
	}})
	leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	qaTok, _ := f.agentToken(t, sess, f.wUUID, "W")
	st, out := f.submit(t, sess, leadTok, "draft.md", "doc", []byte("초안"))
	if st != 201 {
		t.Fatalf("submit = %d: %v", st, out)
	}
	id := str(out["artifact"].(map[string]any), "id")

	if st, out := f.rawPost(t, f.p+"/artifacts/"+id+"/review", qaTok, map[string]any{"verdict": "reject", "comments": "근거가 없습니다"}); st != 200 {
		t.Fatalf("reject = %d %v", st, out)
	}
	// Deliberately the SAME clock reading: the latest is decided by insertion
	// order (the identity id), not by a timestamp that may collide.
	st, out = f.rawPost(t, f.p+"/artifacts/"+id+"/review", qaTok, map[string]any{"verdict": "approve", "comments": "고쳐졌습니다"})
	if st != 200 {
		t.Fatalf("approve after reject = %d %v", st, out)
	}
	if rev := out["review"].(map[string]any); str(rev, "verdict") != "approve" {
		t.Fatalf("review on the wire = %v, want the LATEST (approve)", rev)
	}
	// The read model agrees, and the history has both.
	_, got := f.rawGet(t, f.p+"/artifacts/"+id, leadTok)
	if rev, _ := got["review"].(map[string]any); str(rev, "verdict") != "approve" {
		t.Fatalf("getArtifact review = %v, want approve", got["review"])
	}
	hist, err := f.srv.Artifacts.ReviewHistory(t.Context(), mustUUID(t, id))
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].Verdict != "approve" || hist[1].Verdict != "reject" {
		t.Fatalf("history = %+v, want [approve, reject] newest first — the reject must not have been overwritten (S-15)", hist)
	}
	if hist[1].Comments == nil || *hist[1].Comments != "근거가 없습니다" {
		t.Fatalf("the earlier row lost its reason: %+v", hist[1])
	}
	var rows int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM artifact_review WHERE artifact_id = $1`, mustUUID(t, id)).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("artifact_review rows = %d, want 2", rows)
	}
}
