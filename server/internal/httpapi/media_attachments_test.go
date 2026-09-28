package httpapi

// T-MEDIA (openapi v0.3.7, PRD v0.19.10 FR-4.3.1 · FR-3.7, harness v0.9.12):
// the server judges what an artifact is, serves a preview only for the
// preview list with the sandbox headers and single byte ranges, and a message
// carries the files it names — same room only, at the version it named — to
// the list, getMessage, SSE and the woken agent's <trigger>.
//
// 회귀 주입(각 줄을 끄면 그 괄호가 FAIL):
//   artifacts.Submit 이 DetectContentType 대신 in.ContentType 을 저장 → (judge) (spoof)
//   DownloadArtifact 의 Previewable 검사를 지움 → (html-inline)
//   nosniff / CSP sandbox 헤더를 지움 → (headers)
//   parseSingleRange 의 "," 거부를 지움 → (multi-range)
//   Range 분기를 지움(늘 200) → (range)
//   router.attach 의 방 검사를 지움 → (other-room) (token-room)
//   NormalizeAttachments 의 중복 제거를 지움 → (dedupe)
//   SubmitArtifact 의 `row.Type != ArtifactTypeAttachment` 를 지움 → (condition)
//   message_attachment 가 이름·최신 버전으로 풀리게 바꿈 → (version)
//   triggerAttachments 호출을 지움 → (trigger)
//   Section2 의 MediaRule 을 지움 → (brief)
//   judgeLegacy 호출을 지움 → (legacy)

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

var (
	pngBytes  = append([]byte("\x89PNG\x0D\x0A\x1A\x0A"), bytes.Repeat([]byte{0}, 60)...)
	mp3Bytes  = append([]byte("ID3\x03\x00\x00\x00\x00\x00\x00"), bytes.Repeat([]byte{0xAB}, 4000)...)
	htmlBytes = []byte("<!DOCTYPE html><html><body><script>alert(document.cookie)</script></body></html>")
	svgBytes  = []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><script>alert(1)</script><rect width="4" height="4"/></svg>`)
)

// uploadAs is a submitArtifact body whose `file` part CLAIMS contentType —
// the value the server must not believe.
func (f *p2Fixture) uploadAs(t *testing.T, room, tok, name, typ, contentType string, data []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", name)
	_ = mw.WriteField("type", typ)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	return f.raw(t, "POST", f.p+"/rooms/"+room+"/artifacts", tok, mw.FormDataContentType(), buf.Bytes())
}

func (f *p2Fixture) uploadID(t *testing.T, room, tok, name, typ, contentType string, data []byte) string {
	t.Helper()
	st, out := f.uploadAs(t, room, tok, name, typ, contentType, data)
	if st != 201 {
		t.Fatalf("upload %s = %d %v", name, st, out)
	}
	return str(out["artifact"].(map[string]any), "id")
}

// fetch GETs a download URL with the fixture cookie (or a bearer token).
func (f *p2Fixture) fetch(t *testing.T, path, tok string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.api.srv.URL+path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else {
		req.Header.Set("Cookie", f.api.cookie)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestMediaContentTypeIsTheServersJudgment(t *testing.T) {
	f := newP2Fixture(t)
	for _, c := range []struct {
		name, claim string
		data        []byte
		want        string
	}{
		// (judge) the bytes decide, whatever the part said.
		{"shot.png", "application/octet-stream", pngBytes, "image/png"},
		{"bgm.mp3", "application/octet-stream", mp3Bytes, "audio/mpeg"},
		{"logo.svg", "text/plain", svgBytes, "image/svg+xml"},
		// (spoof) an HTML page that says it is a PNG, named .png.
		{"evil.png", "image/png", htmlBytes, "text/html; charset=utf-8"},
		// Text never becomes media by its name.
		{"notes.mp3", "audio/mpeg", []byte("just some text"), "text/plain; charset=utf-8"},
	} {
		st, out := f.uploadAs(t, f.sessionID, "", c.name, "file", c.claim, c.data)
		if st != 201 {
			t.Fatalf("%s upload = %d %v", c.name, st, out)
		}
		if got := str(out["artifact"].(map[string]any), "content_type"); got != c.want {
			t.Errorf("(judge/spoof) %s claimed %s → content_type %q, want %q", c.name, c.claim, got, c.want)
		}
	}
}

func TestMediaDownloadInlineHeadersAndRange(t *testing.T) {
	f := newP2Fixture(t)
	png := f.uploadID(t, f.sessionID, "", "shot.png", "file", "application/octet-stream", pngBytes)
	html := f.uploadID(t, f.sessionID, "", "evil.png", "file", "image/png", htmlBytes)
	svg := f.uploadID(t, f.sessionID, "", "logo.svg", "file", "text/plain", svgBytes)
	mp3 := f.uploadID(t, f.sessionID, "", "bgm.mp3", "file", "application/octet-stream", mp3Bytes)
	dl := func(id string) string { return f.p + "/artifacts/" + id + "/content" }

	// A preview type with ?inline=true is inline, with its judged type.
	res, _ := f.fetch(t, dl(png)+"?inline=true", "")
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("png inline = %d %q %q", res.StatusCode, res.Header.Get("Content-Disposition"), res.Header.Get("Content-Type"))
	}
	// Without the query it stays a download.
	if res, _ := f.fetch(t, dl(png), ""); !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("png without inline = %q, want attachment", res.Header.Get("Content-Disposition"))
	}
	// (html-inline) the HTML page claiming image/png is never inline, and is
	// served as text/html — never as an image.
	res, _ = f.fetch(t, dl(html)+"?inline=true", "")
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") || strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		t.Fatalf("(html-inline) spoofed html inline = %q %q", res.Header.Get("Content-Disposition"), res.Header.Get("Content-Type"))
	}
	// (headers) every response — the SVG opened as a document included —
	// carries nosniff and CSP sandbox: the SVG's <script> runs, if at all, in
	// an opaque origin with scripts disabled.
	for _, id := range []string{png, html, svg, mp3} {
		for _, q := range []string{"", "?inline=true"} {
			res, _ := f.fetch(t, dl(id)+q, "")
			if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Content-Security-Policy") != "sandbox" {
				t.Fatalf("(headers) %s%s: nosniff=%q csp=%q", id, q, res.Header.Get("X-Content-Type-Options"), res.Header.Get("Content-Security-Policy"))
			}
		}
	}
	res, _ = f.fetch(t, dl(svg)+"?inline=true", "")
	if res.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("svg inline = %q csp %q", res.Header.Get("Content-Type"), res.Header.Get("Content-Security-Policy"))
	}

	// (range) one range → 206 with the exact slice and Content-Range.
	size := len(mp3Bytes)
	res, body := f.fetch(t, dl(mp3)+"?inline=true", "", "Range", "bytes=100-199")
	if res.StatusCode != 206 || !bytes.Equal(body, mp3Bytes[100:200]) || res.Header.Get("Content-Length") != "100" ||
		res.Header.Get("Content-Range") != "bytes 100-199/"+fmtInt(size) {
		t.Fatalf("(range) 100-199 = %d len=%d cl=%q cr=%q", res.StatusCode, len(body), res.Header.Get("Content-Length"), res.Header.Get("Content-Range"))
	}
	if res.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q", res.Header.Get("Accept-Ranges"))
	}
	res, body = f.fetch(t, dl(mp3), "", "Range", "bytes=-10")
	if res.StatusCode != 206 || !bytes.Equal(body, mp3Bytes[size-10:]) {
		t.Fatalf("(range) suffix = %d %d", res.StatusCode, len(body))
	}
	res, body = f.fetch(t, dl(mp3), "", "Range", "bytes=4000-")
	if res.StatusCode != 206 || !bytes.Equal(body, mp3Bytes[4000:]) {
		t.Fatalf("(range) open end = %d %d", res.StatusCode, len(body))
	}
	// (multi-range) and unsatisfiable ranges are 416 with bytes */size.
	for _, rh := range []string{"bytes=0-1,5-6", "bytes=" + fmtInt(size) + "-", "bytes=9-3", "items=0-1"} {
		res, _ := f.fetch(t, dl(mp3), "", "Range", rh)
		if res.StatusCode != 416 || res.Header.Get("Content-Range") != "bytes */"+fmtInt(size) {
			t.Fatalf("(multi-range) %q = %d %q, want 416", rh, res.StatusCode, res.Header.Get("Content-Range"))
		}
	}
	// No Range: the whole body with its length, as before.
	res, body = f.fetch(t, dl(mp3), "")
	if res.StatusCode != 200 || len(body) != size || res.Header.Get("Content-Length") != fmtInt(size) {
		t.Fatalf("full = %d %d %q", res.StatusCode, len(body), res.Header.Get("Content-Length"))
	}
}

func fmtInt(n int) string { return strconv.Itoa(n) }

func TestMessageAttachments(t *testing.T) {
	f := newP2Fixture(t)
	ref := f.uploadID(t, f.sessionID, "", "ref.png", "attachment", "application/octet-stream", pngBytes)
	bgm := f.uploadID(t, f.sessionID, "", "bgm.mp3", "attachment", "application/octet-stream", mp3Bytes)

	// (dedupe) order kept, repeat once.
	out := f.post(t, map[string]any{"content": "(파일 2개)", "attachment_ids": []string{ref, bgm, ref}})
	m := out["message"].(map[string]any)
	atts := m["attachments"].([]any)
	if len(atts) != 2 || str(atts[0].(map[string]any), "artifact_id") != ref || str(atts[1].(map[string]any), "artifact_id") != bgm {
		t.Fatalf("(dedupe) attachments = %v", atts)
	}
	a0 := atts[0].(map[string]any)
	if str(a0, "content_type") != "image/png" || str(a0, "type") != "attachment" || str(a0, "name") != "ref.png" || a0["version"].(float64) != 1 {
		t.Fatalf("AttachmentRef = %v", a0)
	}
	id := str(m, "id")

	// (version) a new version of the same name does not move the message.
	v2 := f.uploadID(t, f.sessionID, "", "ref.png", "attachment", "application/octet-stream", pngBytes)
	got := f.api.must(200, "GET", f.p+"/messages/"+id, nil)
	g0 := got["attachments"].([]any)[0].(map[string]any)
	if str(g0, "artifact_id") != ref || g0["version"].(float64) != 1 || v2 == ref {
		t.Fatalf("(version) getMessage attachment = %v (v2 %s)", g0, v2)
	}
	// listMessages carries them too.
	page := f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages", nil)
	found := false
	for _, raw := range page["items"].([]any) {
		it := raw.(map[string]any)
		if str(it, "id") == id {
			found = len(it["attachments"].([]any)) == 2
		} else if it["attachments"] == nil {
			t.Fatalf("a message without files must carry attachments: [] (got nil) %v", it)
		}
	}
	if !found {
		t.Fatal("listMessages lost the attachments")
	}
	// SSE: the message.created frame is the same Message.
	var frame string
	if err := f.pool.QueryRow(t.Context(), `SELECT payload::text FROM stream_event WHERE type = 'message.created' AND payload::text LIKE '%' || $1 || '%' ORDER BY id DESC LIMIT 1`, id).Scan(&frame); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frame, `"attachments"`) || !strings.Contains(frame, bgm) {
		t.Fatalf("SSE frame lacks attachments: %s", frame)
	}

	// (other-room) another room's artifact is 422 attachment_not_in_room and
	// nothing is stored.
	other := sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{"title": "B", "goal": "g", "isolation": map[string]any{"kind": "none"}})
	foreign := f.uploadID(t, str(other, "id"), "", "x.png", "attachment", "image/png", pngBytes)
	before := f.countMessages(t)
	for _, ids := range [][]string{{ref, foreign}, {uuid.NewString()}} {
		st, body, _ := f.api.do("POST", f.p+"/rooms/"+f.sessionID+"/messages",
			map[string]any{"content": "x", "attachment_ids": ids}, "Idempotency-Key", uuid.NewString())
		if st != 422 || !hasFieldError(body, "attachment_ids", "attachment_not_in_room") {
			t.Fatalf("(other-room) %v = %d %v", ids, st, body)
		}
	}
	// Eleven distinct files is 422 too.
	eleven := []string{}
	for i := 0; i < 11; i++ {
		eleven = append(eleven, f.uploadID(t, f.sessionID, "", "f"+fmtInt(i)+".png", "attachment", "image/png", pngBytes))
	}
	st, body, _ := f.api.do("POST", f.p+"/rooms/"+f.sessionID+"/messages",
		map[string]any{"content": "x", "attachment_ids": eleven}, "Idempotency-Key", uuid.NewString())
	if st != 422 || !hasFieldError(body, "attachment_ids", "too_many_attachments") {
		t.Fatalf("11 files = %d %v", st, body)
	}
	if n := f.countMessages(t); n != before {
		t.Fatalf("a refused post stored a message (%d → %d)", before, n)
	}

	// (token-room) an agent's TaskToken attaches only its own room's files.
	tok, _ := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	st, body, _ = c.do("POST", f.p+"/rooms/"+f.sessionID+"/messages",
		map[string]any{"content": "시안", "attachment_ids": []string{foreign}}, "Idempotency-Key", uuid.NewString())
	if st != 422 || !hasFieldError(body, "attachment_ids", "attachment_not_in_room") {
		t.Fatalf("(token-room) agent attaching another room's file = %d %v", st, body)
	}
	own := f.uploadID(t, f.sessionID, tok, "draft.mp3", "file", "application/octet-stream", mp3Bytes)
	ok := f.agentPost(t, tok, map[string]any{"content": "시안 올렸습니다", "attachment_ids": []string{own}})
	if n := len(ok["message"].(map[string]any)["attachments"].([]any)); n != 1 {
		t.Fatalf("agent attach = %d", n)
	}
}

func (f *p2Fixture) countMessages(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE session_id = $1`, f.sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// (condition) PRD FR-3.7 rule 4: a file of type `attachment` fills
// `artifact_submitted` for nobody — not even the designated agent.
func TestAttachmentDoesNotSatisfyArtifactSubmitted(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"},
	}})
	tok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	for _, who := range []string{tok, ""} {
		st, out := f.uploadAs(t, sess, who, "ref.png", "attachment", "image/png", pngBytes)
		if st != 201 {
			t.Fatalf("upload = %d %v", st, out)
		}
		if met := int(out["completion_progress"].(map[string]any)["met"].(float64)); met != 0 {
			t.Fatalf("(condition) an attachment met %d conditions (uploader token=%v)", met, who != "")
		}
	}
	// The same agent's real deliverable still counts.
	st, out := f.uploadAs(t, sess, tok, "final.png", "file", "image/png", pngBytes)
	if st != 201 || int(out["completion_progress"].(map[string]any)["met"].(float64)) != 1 {
		t.Fatalf("a file submission no longer counts: %d %v", st, out["completion_progress"])
	}
}

// (trigger) (brief) harness v0.9.12: the woken agent's <trigger> lists the
// message's files with type, judged content type, size and id, then how to
// fetch one on its surface; brief [2] carries the fixed media line and stays
// byte-identical to a turn whose trigger had no files (E12-11).
func TestTriggerCarriesAttachmentsAndBriefMediaLine(t *testing.T) {
	f := newP2Fixture(t)
	wA := legacyWork(t, f)
	ref := f.uploadID(t, f.sessionID, "", "ae86.png", "attachment", "application/octet-stream", pngBytes)

	plain := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", wA))
	out := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 이 사진처럼", "work_id": wA, "attachment_ids": []string{ref}})
	b := f.claimBundle(t, f.triggerTask(t, out, f.rUUID))

	want := "이 사진처럼\nAttachments:\n- ae86.png (attachment, image/png, 68 B, id " + ref + ")\n" + queue.AttachFetchMCP + "\n"
	trig := b.Prompt[strings.Index(b.Prompt, "<trigger>"):]
	if !strings.Contains(trig, want) {
		t.Fatalf("(trigger) <trigger> lacks the attachment block:\n%s", trig)
	}
	if strings.Contains(plain.Prompt[strings.Index(plain.Prompt, "<trigger>"):], "Attachments:") {
		t.Fatal("a message without files grew an Attachments block")
	}
	if !strings.Contains(section(b.Brief.Text, 2), queue.MediaRuleMCP+"\n") {
		t.Fatalf("(brief) [2] lacks the media line:\n%s", section(b.Brief.Text, 2))
	}
	if stablePrefix(plain.Brief.Text) != stablePrefix(b.Brief.Text) {
		t.Fatal("E12-11: [1]~[5] differ between a turn with files and one without")
	}

	// hermes reads the shell words.
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent_profile SET runtime_kind = 'hermes' WHERE agent_id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	out = f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 하나 더", "work_id": wA, "attachment_ids": []string{ref}})
	h := f.claimBundle(t, f.triggerTask(t, out, f.rUUID))
	if !strings.Contains(h.Prompt, queue.AttachFetch+"\n") || !strings.Contains(section(h.Brief.Text, 2), queue.MediaRule+"\n") {
		t.Fatalf("(hermes) shell words missing:\n%s\n----\n%s", section(h.Brief.Text, 2), h.Prompt)
	}
}

// (legacy) a row stored before migration 0042 carries the uploader's claim;
// it is judged from its bytes the first time it is read, and until then it is
// never a preview.
func TestLegacyContentTypeIsJudgedOnRead(t *testing.T) {
	f := newP2Fixture(t)
	id := f.uploadID(t, f.sessionID, "", "old.png", "file", "image/png", htmlBytes)
	if _, err := f.pool.Exec(t.Context(), `UPDATE artifact SET content_type = 'image/png', content_type_judged = false WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	got := f.api.must(200, "GET", f.p+"/artifacts/"+id, nil)
	if ct := str(got, "content_type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("(legacy) old row read as %q, want the judged text/html", ct)
	}
	var judged bool
	var ct string
	if err := f.pool.QueryRow(t.Context(), `SELECT content_type_judged, content_type FROM artifact WHERE id = $1`, id).Scan(&judged, &ct); err != nil {
		t.Fatal(err)
	}
	if !judged || !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("(legacy) the judgment was not stored: %v %q", judged, ct)
	}
	if res, _ := f.fetch(t, f.p+"/artifacts/"+id+"/content?inline=true", ""); !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("(legacy) old spoofed row served %q", res.Header.Get("Content-Disposition"))
	}
}

// NN1 (리뷰 #375): 「판정 안 된 옛 행은 미리보기 대상이 아니다」를 직접 단정한다.
//
// TestLegacyContentTypeIsJudgedOnRead 는 옛 행이 읽힐 때 재판정되는 것을 보지만,
// 재판정이 끝난 뒤를 보므로 judged 검사 자체를 지워도 초록이었다(주입 M8). 이 표는
// 서버 없이 그 세 조건을 따로 건드린다 — 특히 판정 안 된 image/png 주장은 부른 쪽이
// inline 을 요구해도 attachment 다.
//
// 회귀 주입: previewDisposition 에서 judged 를 지우면 (unjudged) FAIL;
// Previewable 검사를 지우면 (html) FAIL; asked 를 무시하면 (not-asked) FAIL.
func TestPreviewDispositionNeedsTheServersJudgment(t *testing.T) {
	for _, c := range []struct {
		name              string
		asked, judged     bool
		contentType, want string
	}{
		// 판정 안 된 행은 무엇을 주장하든 미리보기가 아니다 — 0042 주석의 그 규칙.
		{"unjudged image/png claim", true, false, "image/png", "attachment"},
		{"unjudged audio claim", true, false, "audio/mpeg", "attachment"},
		// 판정된 값이라야 목록을 본다.
		{"judged png", true, true, "image/png", "inline"},
		{"judged mp3", true, true, "audio/mpeg", "inline"},
		{"judged svg", true, true, "image/svg+xml", "inline"},
		{"judged html", true, true, "text/html; charset=utf-8", "attachment"},
		// 부른 쪽이 묻지 않으면 언제나 내려받기다.
		{"not asked", false, true, "image/png", "attachment"},
	} {
		if got := previewDisposition(c.asked, c.judged, c.contentType); got != c.want {
			label := "(html)"
			switch {
			case !c.judged:
				label = "(unjudged)"
			case !c.asked:
				label = "(not-asked)"
			}
			t.Errorf("%s %s: previewDisposition(%v, %v, %q) = %q, want %q", label, c.name, c.asked, c.judged, c.contentType, got, c.want)
		}
	}
}
