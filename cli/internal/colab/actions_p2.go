// P2 commands of contracts/colab-cli.md §2.2·2.3 (openapi.yaml x-colab-cli,
// x-phase P2): lane delegate · status set · decision record ·
// artifact submit/get · review approve/reject. The CLI (cmd/colab) and the
// MCP server (internal/mcp) both call these, so a tool's arguments and result
// are exactly the command's.
package colab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// ───────────────────────────── lane delegate ─────────────────────────────

// LaneDelegateArgs — `colab lane delegate --agent <name> --brief <text>
// [--depends-on <lane_id>] [--profile <name>]` / colab_lane_delegate.
type LaneDelegateArgs struct {
	Session   string   `json:"session,omitempty"`
	Agent     string   `json:"agent"`                // participant name (or agent id)
	Brief     string   `json:"brief"`                // goes into the delegate's turn prompt verbatim
	DependsOn []string `json:"depends_on,omitempty"` // lane ids; v1 stores them, DAG execution is v1.1
	Profile   string   `json:"profile,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// LaneDelegateResult — delegateLane 201 plus the resolved target.
type LaneDelegateResult struct {
	LaneID    string          `json:"lane_id"`
	AgentID   string          `json:"agent_id"`
	AgentName string          `json:"agent_name"`
	MessageID string          `json:"message_id,omitempty"`
	Lane      json.RawMessage `json:"lane"`
	Message   *client.Message `json:"message,omitempty"`
	Task      json.RawMessage `json:"task,omitempty"`
}

// NotParticipantHint is the alternative route E15-02 requires the CLI to name
// when the delegate target is outside the session (FR-1.5: agents cannot
// create participants).
const NotParticipantHint = "ask the Director to add them as a participant with `colab hitl ask` " +
	"(agents cannot add participants — FR-1.5); then retry `colab lane delegate`"

// LaneDelegate — POST /sessions/{S}/lanes. Always a new lane (resolution
// rule 2); `delegated_from_task_id` = the calling task, which is the rejoin
// group key (FR-6.5). The target must already be a session participant —
// otherwise exit 3 `not_participant` with NotParticipantHint (E15-02).
func LaneDelegate(ctx context.Context, c *client.Client, a LaneDelegateArgs) (*LaneDelegateResult, error) {
	if strings.TrimSpace(a.Agent) == "" {
		return nil, client.Usage("--agent is required")
	}
	if strings.TrimSpace(a.Brief) == "" {
		return nil, client.Usage("--brief is required")
	}
	sid, err := c.SessionID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return nil, err
	}
	p, ok := cc.AgentByName(a.Agent)
	if !ok {
		return nil, &client.Error{
			Exit: client.ExitRefused, Code: "not_participant",
			Title: "@" + strings.TrimPrefix(a.Agent, "@") + " is not a session participant",
			Detail: "cannot delegate to a non-participant. participants: " +
				strings.Join(cc.ParticipantNames(), ", ") + ". " + NotParticipantHint,
		}
	}
	body := client.LaneDelegateCreate{AgentID: p.AgentID, Brief: a.Brief, DependsOn: splitList(a.DependsOn)}
	if a.Profile != "" {
		prof := a.Profile
		body.Profile = &prof
	}
	res, err := c.DelegateLane(ctx, sid, body, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	out := &LaneDelegateResult{
		AgentID: p.AgentID, AgentName: p.Name,
		Lane: res.Lane, Message: res.Message, Task: res.Task,
	}
	out.LaneID = rawField(res.Lane, "id")
	if res.Message != nil {
		out.MessageID = res.Message.ID
	}
	return out, nil
}

// ───────────────────────────── status set ─────────────────────────────

// StatusSetArgs — `colab status set working|blocked|done [--note <text>]`.
type StatusSetArgs struct {
	Task   string `json:"task,omitempty"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// StatusSetResult mirrors setTaskStatus 200 field for field.
//
// `turn_end_required` is passed through exactly as the server sent it and is
// never swallowed or renamed: the agent reads this field to decide to end its
// turn (FR-6.2.1). It is deliberately the *only* name for the flag — ACP's
// `end_turn` stopReason means the opposite direction (a turn that already
// ended) and sharing a name across server, daemon and CLI is how P1's
// kind ↔ runtime_kind defect happened (Lead ruling, contract PR #59).
type StatusSetResult struct {
	Status            string          `json:"status"`
	TurnEndRequired   bool            `json:"turn_end_required"`
	QuestionMessageID *string         `json:"question_message_id"`
	Task              json.RawMessage `json:"task,omitempty"`
	Lane              json.RawMessage `json:"lane,omitempty"`
}

// StatusSet — POST /tasks/{T}/status.
//
//   - `working` is a feed note, near no-op.
//   - `blocked` is the FR-6.2.1 route: the server sets the lane `blocked`,
//     posts the question card on the lane thread and wakes the delegator
//     immediately (Director inbox `lane_blocked` when there is none, E3-08).
//     `--note` is the question body and is required.
//   - `done` declares this turn's work finished; the server judges the lane
//     and the rejoin group (FR-6.5).
func StatusSet(ctx context.Context, c *client.Client, a StatusSetArgs) (*StatusSetResult, error) {
	status := strings.ToLower(strings.TrimSpace(a.Status))
	switch status {
	case client.StatusWorking, client.StatusDone:
	case client.StatusBlocked:
		if strings.TrimSpace(a.Note) == "" {
			return nil, client.Usage("status set blocked: --note is required (it is the question the delegator answers)")
		}
	case "":
		return nil, client.Usage("status is required: working | blocked | done")
	default:
		return nil, client.Usage("unknown status %q (working | blocked | done)", a.Status)
	}
	tid, err := c.TaskID(ctx, a.Task)
	if err != nil {
		return nil, err
	}
	res, err := c.SetTaskStatus(ctx, tid, client.TaskStatusCreate{Status: status, Note: a.Note})
	if err != nil {
		return nil, err
	}
	return &StatusSetResult{
		Status: status, TurnEndRequired: res.TurnEndRequired,
		QuestionMessageID: res.QuestionMessageID, Task: res.Task, Lane: res.Lane,
	}, nil
}

// ───────────────────────────── decision record ─────────────────────────────

// DecisionRecordArgs — `colab decision record --summary <s> [--rationale <r>]`.
// `--title`/`--body` are accepted as aliases.
type DecisionRecordArgs struct {
	Session   string `json:"session,omitempty"`
	Summary   string `json:"summary"`
	Rationale string `json:"rationale,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// DecisionRecordResult — recordDecision 201 (the Decision, verbatim).
type DecisionRecordResult struct {
	DecisionID string          `json:"decision_id"`
	Decision   json.RawMessage `json:"decision"`
}

// DecisionRecord — POST /sessions/{S}/decisions, source=agent, ref_id=task.
// The record lands in brief [7] (FR-1.9, FR-4.2).
//
// The Decision schema is summary + rationale and nothing else. colab-cli.md
// v0.3 also listed `--options`/`--chosen`; those flags are gone in v0.4
// rather than folded into the rationale text — a structured-looking string
// nobody can query is worse than not accepting the input at all.
func DecisionRecord(ctx context.Context, c *client.Client, a DecisionRecordArgs) (*DecisionRecordResult, error) {
	if strings.TrimSpace(a.Summary) == "" {
		return nil, client.Usage("--summary is required")
	}
	sid, err := c.SessionID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	raw, err := c.RecordDecision(ctx, sid,
		client.DecisionCreate{Summary: a.Summary, Rationale: a.Rationale}, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return &DecisionRecordResult{DecisionID: rawField(raw, "id"), Decision: raw}, nil
}

// ───────────────────────────── artifact submit ─────────────────────────────

// MaxArtifactBytes is the submitArtifact size ceiling (openapi: 50 MB → 413).
const MaxArtifactBytes = 50 << 20

// ArtifactSubmitArgs — `colab artifact submit --type <t> [--file <p>]
// [--name <n>] [--description <d>] [--base <rev>]`. The field is named for
// openapi's multipart part (`file`); `--path` is accepted as an alias.
type ArtifactSubmitArgs struct {
	Session     string `json:"session,omitempty"`
	Name        string `json:"name,omitempty"` // defaults to the file's base name
	Type        string `json:"type"`           // open set: file · diff · branch · doc …
	File        string `json:"file"`
	Description string `json:"description,omitempty"`
	// Base is `--base`: what a `--type diff` submission is diffed against
	// (default: the repository's own default branch). Meaningless for any
	// other type and refused there rather than ignored.
	Base string `json:"base,omitempty"`

	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// workdir is the directory git runs in for `--type diff`. It is
	// deliberately unexported-by-JSON (`-`): the daemon spawns the attempt
	// with cwd = the agent's workdir (harness.md §5), and neither a flag nor
	// an MCP tool argument may point the diff at another repository
	// (PRD FR-6.1). Tests set it directly.
	Workdir string `json:"-"`
}

// dir is where git runs: the process working directory unless a test said
// otherwise.
func (a ArtifactSubmitArgs) dir() string {
	if a.Workdir != "" {
		return a.Workdir
	}
	return "."
}

// ArtifactSubmitResult — submitArtifact 201.
type ArtifactSubmitResult struct {
	ArtifactID         string          `json:"artifact_id"`
	Name               string          `json:"name"`
	Type               string          `json:"type"`
	SizeBytes          int             `json:"size_bytes"`
	Artifact           json.RawMessage `json:"artifact"`
	CompletionProgress json.RawMessage `json:"completion_progress,omitempty"`
	// Diff is present when the CLI built the body itself (`--type diff`
	// without `--file`): what it diffed and what it left out.
	Diff *DiffSummary `json:"diff,omitempty"`
}

// DiffSummary echoes the metadata the CLI stamped into the description's
// first line and the diff body's `# colab-diff:` comment, so the agent can
// quote it in the message it posts without re-running git.
type DiffSummary struct {
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Commit string `json:"commit"`
	// UntrackedNotIncluded lists files git does not track. They are NOT in
	// the diff — the agent has to `git add` them and submit again.
	UntrackedNotIncluded []string `json:"untracked_not_included,omitempty"`
}

// ArtifactSubmit — POST /sessions/{S}/artifacts (multipart: name · type ·
// file · description — the whole body openapi defines, nothing else).
// Re-submitting the same name is version+1 (FR-4.3); the response's
// completion_progress says whether the `artifact_submitted` completion
// condition is now met (FR-2.2, E6-01).
//
// `--type diff` may omit `--file`, and then the CLI builds the unified diff
// of the workdir this task runs in (see diff.go). Because the multipart body
// has no field for branch/base/commit, that metadata goes into the two
// places the contract already has: the description's first line
// (`diff <branch>@<commit> vs <base>`, the caller's own --description
// following it) and one `# colab-diff:` comment at the top of the body.
// Nothing else about the request changes, and any other type behaves exactly
// as it did.
func ArtifactSubmit(ctx context.Context, c *client.Client, a ArtifactSubmitArgs) (*ArtifactSubmitResult, error) {
	typ := strings.TrimSpace(a.Type)
	if typ == "" {
		return nil, client.Usage("--type is required (file · diff · branch · doc · …)")
	}
	isDiff := strings.EqualFold(typ, ArtifactTypeDiff)
	if !isDiff && strings.TrimSpace(a.Base) != "" {
		return nil, client.Usage("--base applies to --type diff only (this is --type %s)", typ)
	}

	var (
		data        []byte
		name        = strings.TrimSpace(a.Name)
		fileName    string
		contentType string
		description = a.Description
		summary     *DiffSummary
	)
	switch {
	case strings.TrimSpace(a.File) != "":
		// A file the caller produced is uploaded byte for byte, diff or not:
		// rewriting someone's patch is how a patch stops applying.
		b, err := readArtifactFile(a.File)
		if err != nil {
			return nil, err
		}
		data, fileName, contentType = b, filepath.Base(a.File), partContentType(a.File)
		if name == "" {
			name = filepath.Base(a.File)
		}
		if isDiff {
			m, ok, err := repoMeta(a.dir(), a.Base)
			if err != nil {
				return nil, err
			}
			if ok {
				description = stampDescription(m, description)
				summary = &DiffSummary{Branch: m.Branch, Base: m.Base, Commit: m.Commit}
			}
		}
	case isDiff:
		// No --file: build the diff of this worktree (scenario B step 4).
		d, err := buildDiff(a.dir(), a.Base)
		if err != nil {
			return nil, err
		}
		data = d.Body
		if len(data) > MaxArtifactBytes {
			return nil, client.Usage("the generated diff is %d bytes; the limit is %d (50 MB) — "+
				"narrow it with --base, or submit a file with --file", len(data), MaxArtifactBytes)
		}
		if name == "" {
			name = defaultDiffName(d.Meta.Branch, c.Config().AgentName)
		}
		fileName, contentType = diffFileName(name), "text/plain"
		description = stampDescription(d.Meta, description)
		summary = &DiffSummary{Branch: d.Meta.Branch, Base: d.Meta.Base, Commit: d.Meta.Commit,
			UntrackedNotIncluded: d.Untracked}
	default:
		return nil, client.Usage("--file is required (only `--type diff` can build its own body)")
	}

	sid, err := c.SessionID(ctx, a.Session)
	if err != nil {
		return nil, err
	}
	res, err := c.SubmitArtifact(ctx, sid, client.ArtifactUpload{
		Name: name, Type: typ, Description: description,
		FileName: fileName, ContentType: contentType, Data: data,
	}, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return &ArtifactSubmitResult{
		ArtifactID: rawField(res.Artifact, "id"), Name: name, Type: typ, SizeBytes: len(data),
		Artifact: res.Artifact, CompletionProgress: res.CompletionProgress, Diff: summary,
	}, nil
}

// readArtifactFile reads --file with the contract's guards (a real file,
// within the 50 MB submitArtifact ceiling).
func readArtifactFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, client.Usage("--file %s: %v", path, err)
	}
	if st.IsDir() {
		return nil, client.Usage("--file %s is a directory", path)
	}
	if st.Size() > MaxArtifactBytes {
		return nil, client.Usage("--file %s is %d bytes; the limit is %d (50 MB)", path, st.Size(), MaxArtifactBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, client.Usage("--file %s: %v", path, err)
	}
	return data, nil
}

// partContentType picks the `file` part's Content-Type from the contract's
// allowed set (openapi encoding.file.contentType:
// application/octet-stream, text/plain, application/json).
func partContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".txt", ".md", ".diff", ".patch", ".csv", ".log", ".yaml", ".yml":
		return "text/plain"
	}
	if t := mime.TypeByExtension(filepath.Ext(path)); strings.HasPrefix(t, "text/") {
		return "text/plain"
	}
	return "application/octet-stream"
}

// ───────────────────────────── artifact get ─────────────────────────────

// ArtifactGetArgs — `colab artifact get <id> [--out <path>]`.
type ArtifactGetArgs struct {
	Artifact string `json:"artifact"`
	Out      string `json:"out,omitempty"` // write the body here (file, or a directory)
}

// ArtifactGetResult — getArtifact metadata, plus the download when --out was
// given. SizeBytes is what actually reached disk, and it is only ever set
// when the whole body did.
type ArtifactGetResult struct {
	ArtifactID  string          `json:"artifact_id"`
	Artifact    json.RawMessage `json:"artifact"`
	SavedTo     string          `json:"saved_to,omitempty"`
	SizeBytes   *int64          `json:"size_bytes,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
}

// ArtifactGet — GET /artifacts/{id} (+ /content when --out is given). This is
// the only cross-lane read (FR-6.1); worktree paths are never exposed.
func ArtifactGet(ctx context.Context, c *client.Client, a ArtifactGetArgs) (*ArtifactGetResult, error) {
	id := strings.TrimSpace(a.Artifact)
	if id == "" {
		return nil, client.Usage("artifact get: <id> is required")
	}
	meta, err := c.GetArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &ArtifactGetResult{ArtifactID: rawField(meta, "id"), Artifact: meta}
	if out.ArtifactID == "" {
		out.ArtifactID = id
	}
	if strings.TrimSpace(a.Out) == "" {
		return out, nil
	}
	stream, err := c.OpenArtifactContent(ctx, id)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	dest := a.Out
	if st, err := os.Stat(dest); err == nil && st.IsDir() {
		// filepath.Base: the filename is the server's, so it never escapes
		// the directory the caller named.
		name := stream.FileName
		if name == "" {
			name = rawField(meta, "name")
		}
		if name == "" {
			name = id
		}
		dest = filepath.Join(dest, filepath.Base(name))
	}
	n, err := writeStream(dest, stream)
	if err != nil {
		return nil, err
	}
	out.SavedTo, out.SizeBytes, out.ContentType = dest, &n, stream.ContentType
	return out, nil
}

// writeStream copies an artifact body to dest and returns the bytes written.
//
// It writes to a temporary file in the destination directory and renames only
// after the whole body has arrived, so a failed or short transfer never
// leaves a partial file behind for an agent to mistake for the artifact. When
// the server declared a Content-Length, the bytes written are checked against
// it: a body that ends early is exit 5, not a smaller success.
func writeStream(dest string, s *client.Stream) (int64, error) {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".part-*")
	if err != nil {
		return 0, client.Usage("--out %s: %v", dest, err)
	}
	tmpName := tmp.Name()
	discard := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	n, copyErr := io.Copy(tmp, s.Body)
	if copyErr != nil {
		discard()
		code, title := "download_failed", "artifact download failed"
		if errors.Is(copyErr, client.ErrStalled) {
			code, title = "download_stalled", "artifact download stalled"
		}
		return 0, &client.Error{Exit: client.ExitUnreachable, Code: code, Title: title,
			Detail: fmt.Sprintf("the transfer ended after %d bytes: %v — nothing was written to %s", n, copyErr, dest)}
	}
	if s.Length >= 0 && n != s.Length {
		discard()
		return 0, &client.Error{Exit: client.ExitUnreachable, Code: "download_truncated",
			Title:  "artifact download truncated",
			Detail: fmt.Sprintf("the server declared %d bytes but sent %d — nothing was written to %s", s.Length, n, dest)}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return 0, client.Usage("--out %s: %v", dest, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return 0, client.Usage("--out %s: %v", dest, err)
	}
	return n, nil
}

// ───────────────────────────── review ─────────────────────────────

// ReviewArgs — `colab review approve [--note <t>] --artifact <id>` /
// `colab review reject --reason <text> --artifact <id>`.
//
// openapi's reviewArtifact is POST /artifacts/{artifactId}/review, so the
// artifact id is part of the path and cannot be omitted (colab-cli.md §2.3
// shows `[--artifact <id>]` as optional — reported to the Lead).
type ReviewArgs struct {
	Artifact string `json:"artifact"`
	Note     string `json:"note,omitempty"`   // approve → comments
	Reason   string `json:"reason,omitempty"` // reject → comments (required)

	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// ReviewResult — reviewArtifact 200.
type ReviewResult struct {
	ArtifactID         string          `json:"artifact_id"`
	Verdict            string          `json:"verdict"`
	Review             json.RawMessage `json:"review"`
	CompletionProgress json.RawMessage `json:"completion_progress"`
	Message            *client.Message `json:"message,omitempty"` // reject: the posted reply
}

// ReviewApprove — verdict `approve`. Feeds the `agent_approval(agent)`
// completion condition (FR-2.2); an agent the condition did not designate
// gets 403 not_designated_reviewer → exit 3 and nothing is stored (E6-06).
func ReviewApprove(ctx context.Context, c *client.Client, a ReviewArgs) (*ReviewResult, error) {
	return review(ctx, c, a, client.VerdictApprove, a.Note)
}

// ReviewReject — verdict `reject`. `--reason` is required: the server posts
// it as a reply on the artifact's lane thread (resolution rule 1 re-entry)
// and records a decision.
func ReviewReject(ctx context.Context, c *client.Client, a ReviewArgs) (*ReviewResult, error) {
	if strings.TrimSpace(a.Reason) == "" {
		return nil, client.Usage("review reject: --reason is required (it is posted on the artifact thread)")
	}
	return review(ctx, c, a, client.VerdictReject, a.Reason)
}

// ServerNotDesignatedReviewer is the server's code for "the completion
// condition names someone else"; colab-cli.md §2.3 spells the CLI-side code
// `not_reviewer`.
const ServerNotDesignatedReviewer = "not_designated_reviewer"

// CodeNotReviewer is the CLI code for E6-06 (exit 3).
const CodeNotReviewer = "not_reviewer"

func review(ctx context.Context, c *client.Client, a ReviewArgs, verdict, comments string) (*ReviewResult, error) {
	id := strings.TrimSpace(a.Artifact)
	if id == "" {
		return nil, client.Usage("review %s: --artifact <id> is required (openapi reviewArtifact is POST /artifacts/{id}/review)", verdict)
	}
	res, err := c.ReviewArtifact(ctx, id, client.ReviewCreate{Verdict: verdict, Comments: comments}, a.IdempotencyKey)
	if err != nil {
		// colab-cli.md §2.3: server `403 not_designated_reviewer` → CLI
		// `3 not_reviewer` (E6-06). The server's own problem stays in
		// `problem` so nothing is lost.
		if e := client.AsError(err); e.Code == ServerNotDesignatedReviewer {
			remapped := *e // copy: AsError can hand back a shared sentinel
			remapped.Code = CodeNotReviewer
			return nil, &remapped
		}
		return nil, err
	}
	return &ReviewResult{
		ArtifactID: id, Verdict: verdict, Review: res.Review,
		CompletionProgress: res.CompletionProgress, Message: res.Message,
	}, nil
}

// ───────────────────────────── helpers ─────────────────────────────

// splitList flattens repeated flags and comma-separated values, dropping
// blanks: ["a,b", " c "] → ["a","b","c"].
func splitList(in []string) []string {
	var out []string
	for _, raw := range in {
		for _, v := range strings.Split(raw, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// rawField reads one top-level string field out of a raw JSON object, so the
// CLI can lift an id for the summary without typing the whole schema.
func rawField(raw json.RawMessage, field string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[field], &s) != nil {
		return ""
	}
	return s
}
