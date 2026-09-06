// Package api is the daemon → server client (contracts/daemon-protocol.md).
// Everything the loop needs is behind the Server interface so tests run
// against httptest or an in-memory fake.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// PairRequest — POST /v1/daemon/pair (§2).
type PairRequest struct {
	PairingCode   string `json:"pairing_code"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	DaemonVersion string `json:"daemon_version"`
}

type PairResponse struct {
	RuntimeID   string `json:"runtime_id"`
	DaemonToken string `json:"daemon_token"`
}

// ClaimRequest — POST /v1/daemon/runtimes/{id}/claim (§4.1).
type ClaimRequest struct {
	Capacity int `json:"capacity"`
	WaitMS   int `json:"wait_ms"`
}

type ClaimResponse struct {
	Tasks    []contracts.TaskBundle `json:"tasks"`
	Commands []contracts.Command    `json:"commands"`
}

// The §4.3 `gc` payload's `workdirs: [{id, path}]` now lives on
// `contracts.Command` itself (contracts/protocol.go, daemon-protocol v0.7), so
// the daemon reads server commands as the contract type directly — the local
// wrapper that carried the field while the contract PR was pending is gone
// (D-14).
//
// The path is on the wire because the daemon has never held a uuid ↔ path
// mapping. §6 reports carry an optional `id`, but nothing ever hands the
// daemon the id the server minted (the report response has no body), so a `gc
// {workdir_ids}` naming DB uuids was undeliverable work: the daemon could not
// tell which directory to remove. Lead's answer is that the server names the
// path.

// PhaseRequest — POST …/phase (§4.2).
type PhaseRequest struct {
	Phase       string `json:"phase"` // preparing | running
	PGID        int    `json:"pgid,omitempty"`
	WorkdirPath string `json:"workdir_path,omitempty"`
}

type EventsRequest struct {
	Events []contracts.TaskEvent `json:"events"`
}

type EventsResponse struct {
	AcceptedSeqMax int                 `json:"accepted_seq_max"`
	Commands       []contracts.Command `json:"commands"`
}

// HeartbeatPreview is the non-persisted partial message (§4.2 v0.3, G3 C-1):
// {"text": …, "message_id": …} — text required, message_id only when the
// daemon knows which already-posted message it is continuing.
type HeartbeatPreview struct {
	Text      string `json:"text"`
	MessageID string `json:"message_id,omitempty"`
}

// HeartbeatRequest — POST …/heartbeat (§4.2). Preview is omitted entirely
// when there is no partial output; it is never sent as an empty object.
type HeartbeatRequest struct {
	Usage   contracts.Usage   `json:"usage"`
	LastSeq int               `json:"last_seq"`
	Preview *HeartbeatPreview `json:"preview,omitempty"`
}

type HeartbeatResponse struct {
	Commands []contracts.Command `json:"commands"`
}

// FinishRequest — POST …/finish (§4.4).
type FinishRequest struct {
	contracts.Finish
	Workdir *FinishWorkdir `json:"workdir,omitempty"`
}

// FinishWorkdir is §4.4's `workdir: {path, git: {branch, dirty, ahead}?}`.
// The git block is what tells the server whether the checkout it may collect
// in 14 days has unmerged commits or uncommitted work (E13-12·13) — the
// numbers arrive with the attempt that made them, not only on the next probe.
type FinishWorkdir struct {
	Path string           `json:"path"`
	Git  *workdir.GitInfo `json:"git,omitempty"`
}

type WorkdirsRequest struct {
	Workdirs []workdir.Info `json:"workdirs"`
}

// Server is everything the daemon calls on the server.
type Server interface {
	Pair(ctx context.Context, req PairRequest) (PairResponse, error)
	Probe(ctx context.Context, runtimeID string, p contracts.Probe) error
	Claim(ctx context.Context, runtimeID string, req ClaimRequest) (ClaimResponse, error)
	Phase(ctx context.Context, taskID string, attempt int, req PhaseRequest) error
	Events(ctx context.Context, taskID string, attempt int, events []contracts.TaskEvent) (EventsResponse, error)
	Heartbeat(ctx context.Context, taskID string, attempt int, req HeartbeatRequest) (HeartbeatResponse, error)
	Finish(ctx context.Context, taskID string, attempt int, req FinishRequest) error
	Workdirs(ctx context.Context, runtimeID string, req WorkdirsRequest) error
	// Download fetches an artifact body to dest. It is the only GET the
	// daemon makes and the only one whose response is not JSON: a
	// `rebind_prepare` command names artifact URLs the daemon must have on
	// disk before the rebound session's first turn (§4.3, FR-9.2).
	Download(ctx context.Context, rawURL, dest string) error
}

// HTTPError is a non-2xx response.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return "server: " + strconv.Itoa(e.Status) + ": " + e.Body }

// Client is the HTTPS implementation.
type Client struct {
	BaseURL string
	Token   string // daemon token cdt_…; empty for Pair
	HTTP    *http.Client
}

// New creates a client. The http.Client has no timeout of its own — claim
// long-polls up to 30s — callers bound requests with ctx.
func New(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	u, err := url.JoinPath(c.BaseURL, path)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(rb))}
	}
	if out != nil && len(bytes.TrimSpace(rb)) > 0 {
		if err := json.Unmarshal(rb, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

func attemptPath(taskID string, attempt int, leaf string) string {
	return "/v1/daemon/tasks/" + url.PathEscape(taskID) + "/attempts/" + strconv.Itoa(attempt) + "/" + leaf
}

func (c *Client) Pair(ctx context.Context, req PairRequest) (PairResponse, error) {
	var res PairResponse
	err := c.do(ctx, http.MethodPost, "/v1/daemon/pair", req, &res)
	return res, err
}

func (c *Client) Probe(ctx context.Context, runtimeID string, p contracts.Probe) error {
	return c.do(ctx, http.MethodPost, "/v1/daemon/runtimes/"+url.PathEscape(runtimeID)+"/probe", p, nil)
}

func (c *Client) Claim(ctx context.Context, runtimeID string, req ClaimRequest) (ClaimResponse, error) {
	if req.WaitMS > int(contracts.ClaimMaxWait/time.Millisecond) {
		req.WaitMS = int(contracts.ClaimMaxWait / time.Millisecond)
	}
	var res ClaimResponse
	err := c.do(ctx, http.MethodPost, "/v1/daemon/runtimes/"+url.PathEscape(runtimeID)+"/claim", req, &res)
	return res, err
}

func (c *Client) Phase(ctx context.Context, taskID string, attempt int, req PhaseRequest) error {
	return c.do(ctx, http.MethodPost, attemptPath(taskID, attempt, "phase"), req, nil)
}

func (c *Client) Events(ctx context.Context, taskID string, attempt int, events []contracts.TaskEvent) (EventsResponse, error) {
	var res EventsResponse
	err := c.do(ctx, http.MethodPost, attemptPath(taskID, attempt, "events"), EventsRequest{Events: events}, &res)
	return res, err
}

func (c *Client) Heartbeat(ctx context.Context, taskID string, attempt int, req HeartbeatRequest) (HeartbeatResponse, error) {
	var res HeartbeatResponse
	err := c.do(ctx, http.MethodPost, attemptPath(taskID, attempt, "heartbeat"), req, &res)
	return res, err
}

func (c *Client) Finish(ctx context.Context, taskID string, attempt int, req FinishRequest) error {
	return c.do(ctx, http.MethodPost, attemptPath(taskID, attempt, "finish"), req, nil)
}

func (c *Client) Workdirs(ctx context.Context, runtimeID string, req WorkdirsRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/daemon/runtimes/"+url.PathEscape(runtimeID)+"/workdirs", req, nil)
}

// Download fetches rawURL into dest (created with its parent directories).
// Relative URLs are resolved against BaseURL and carry the daemon token;
// an absolute URL to another host (object storage with its own signature)
// does not — sending the daemon token to a third party would leak it.
func (c *Client) Download(ctx context.Context, rawURL, dest string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	sameHost := !u.IsAbs()
	if u.IsAbs() {
		if base, err := url.Parse(c.BaseURL); err == nil {
			sameHost = base.Host == u.Host
		}
	} else {
		joined, err := url.JoinPath(c.BaseURL, rawURL)
		if err != nil {
			return err
		}
		rawURL = joined
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if c.Token != "" && sameHost {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &HTTPError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(rb))}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// IsNetwork reports whether err is a transport failure (→ failure_kind
// network, retried) rather than a server verdict.
func IsNetwork(err error) bool {
	if err == nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status >= 500
	}
	return !errors.Is(err, context.Canceled)
}
