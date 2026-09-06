package client

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// P2 operations — contracts/openapi.yaml x-colab-cli, x-phase P2.
//
// `Idempotency-Key` is optional on all four write operations here
// (components.parameters.IdempotencyKeyOptional), so the CLI sends it only
// when the caller supplies one. The derived UUIDv5(task:<id>:<seq>) counter
// stays reserved for `message post`, whose X-Colab-Client-Seq the server
// folds into last_seq = max(client_seq) (colab-cli.md §1 v0.3).

// idemHeader returns the header set for an optional Idempotency-Key.
func idemHeader(key string) http.Header {
	if key == "" {
		return nil
	}
	h := http.Header{}
	h.Set("Idempotency-Key", key)
	return h
}

// AgentByName resolves a participant name (or agent id) to its agent id from
// the /cli/context roster. ok is false when the name is not a participant —
// the caller turns that into `3 not_participant` (FR-1.5, E15-02).
func (cc *CliContext) AgentByName(name string) (Participant, bool) {
	n := strings.TrimPrefix(strings.TrimSpace(name), "@")
	for _, p := range cc.Participants {
		if strings.EqualFold(p.Name, n) || p.AgentID == n {
			return p, true
		}
	}
	return Participant{}, false
}

// ParticipantNames lists the roster names, for error messages.
func (cc *CliContext) ParticipantNames() []string {
	out := make([]string, 0, len(cc.Participants))
	for _, p := range cc.Participants {
		out = append(out, p.Name)
	}
	return out
}

// DelegateLane — POST /sessions/{S}/lanes (delegateLane). Always a new lane
// (resolution rule 2); the server sets delegated_from_task_id to the calling
// task and posts the mention message itself.
func (c *Client) DelegateLane(ctx context.Context, sessionID string, body LaneDelegateCreate, key string) (*LaneDelegateResult, error) {
	if body.DependsOn == nil {
		body.DependsOn = []string{}
	}
	res, err := c.Do(ctx, http.MethodPost, "/sessions/"+url.PathEscape(sessionID)+"/lanes", nil, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	var out LaneDelegateResult
	if err := decode(res, &out); err != nil {
		return nil, err
	}
	out.Raw = json.RawMessage(res.Body)
	return &out, nil
}

// SetTaskStatus — POST /tasks/{T}/status (setTaskStatus).
func (c *Client) SetTaskStatus(ctx context.Context, taskID string, body TaskStatusCreate) (*TaskStatusResult, error) {
	res, err := c.Do(ctx, http.MethodPost, "/tasks/"+url.PathEscape(taskID)+"/status", nil, body, nil)
	if err != nil {
		return nil, err
	}
	var out TaskStatusResult
	if err := decode(res, &out); err != nil {
		return nil, err
	}
	out.Raw = json.RawMessage(res.Body)
	return &out, nil
}

// RecordDecision — POST /sessions/{S}/decisions (recordDecision). The 201
// body is the Decision itself; it is returned raw so every field reaches
// --json.
func (c *Client) RecordDecision(ctx context.Context, sessionID string, body DecisionCreate, key string) (json.RawMessage, error) {
	res, err := c.Do(ctx, http.MethodPost, "/sessions/"+url.PathEscape(sessionID)+"/decisions", nil, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	// Decoded only to turn an unparseable body into exit 5 bad_response; the
	// raw bytes are what reaches --json, so no field is lost.
	var probe map[string]any
	if err := decode(res, &probe); err != nil {
		return nil, err
	}
	return json.RawMessage(res.Body), nil
}

// SubmitArtifact — POST /sessions/{S}/artifacts (submitArtifact), encoded as
// multipart/form-data with the contract's parts: name · type · file ·
// description.
func (c *Client) SubmitArtifact(ctx context.Context, sessionID string, up ArtifactUpload, key string) (*ArtifactSubmitResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("name", up.Name); err != nil {
		return nil, Usage("encode multipart: %v", err)
	}
	if err := mw.WriteField("type", up.Type); err != nil {
		return nil, Usage("encode multipart: %v", err)
	}
	if up.Description != "" {
		if err := mw.WriteField("description", up.Description); err != nil {
			return nil, Usage("encode multipart: %v", err)
		}
	}
	ct := up.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	fn := up.FileName
	if fn == "" {
		fn = up.Name
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data",
		map[string]string{"name": "file", "filename": fn}))
	h.Set("Content-Type", ct)
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, Usage("encode multipart: %v", err)
	}
	if _, err := part.Write(up.Data); err != nil {
		return nil, Usage("encode multipart: %v", err)
	}
	if err := mw.Close(); err != nil {
		return nil, Usage("encode multipart: %v", err)
	}
	body := &RawBody{ContentType: mw.FormDataContentType(), Data: buf.Bytes()}
	res, err := c.Do(ctx, http.MethodPost, "/sessions/"+url.PathEscape(sessionID)+"/artifacts", nil, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	var out ArtifactSubmitResult
	if err := decode(res, &out); err != nil {
		return nil, err
	}
	out.Raw = json.RawMessage(res.Body)
	return &out, nil
}

// GetArtifact — GET /artifacts/{id} (getArtifact): metadata only. Cross-lane
// reads go through artifacts and nothing else (FR-6.1).
func (c *Client) GetArtifact(ctx context.Context, artifactID string) (json.RawMessage, error) {
	var probe map[string]any // see RecordDecision: decoded only to validate
	res, err := c.GetJSON(ctx, "/artifacts/"+url.PathEscape(artifactID), nil, &probe)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(res.Body), nil
}

// OpenArtifactContent — GET /artifacts/{id}/content (downloadArtifact),
// returned as an open stream. Artifact bodies are the one payload with no
// useful size ceiling (`artifact submit` alone accepts 50 MB), so they are
// never buffered: the caller copies the stream straight to its destination
// and checks the byte count against Stream.Length. The caller must Close it.
func (c *Client) OpenArtifactContent(ctx context.Context, artifactID string) (*Stream, error) {
	return c.DoStream(ctx, http.MethodGet, "/artifacts/"+url.PathEscape(artifactID)+"/content", nil)
}

// ReviewArtifact — POST /artifacts/{id}/review (reviewArtifact). A reviewer
// the completion condition did not designate gets 403
// not_designated_reviewer → exit 3 (E6-06); nothing is stored.
func (c *Client) ReviewArtifact(ctx context.Context, artifactID string, body ReviewCreate, key string) (*ReviewResult, error) {
	res, err := c.Do(ctx, http.MethodPost, "/artifacts/"+url.PathEscape(artifactID)+"/review", nil, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	var out ReviewResult
	if err := decode(res, &out); err != nil {
		return nil, err
	}
	out.Raw = json.RawMessage(res.Body)
	return &out, nil
}

// TaskID resolves the task: explicit arg → COLAB_TASK_ID → /cli/context.
// `status set` is the only P2 command whose path is task-scoped.
func (c *Client) TaskID(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if c.cfg.TaskID != "" {
		return c.cfg.TaskID, nil
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return "", err
	}
	if cc.TaskID == "" {
		return "", &Error{Exit: ExitUnreachable, Code: "bad_response", Title: "cli context has no task_id"}
	}
	return cc.TaskID, nil
}
