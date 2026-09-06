// Package client is the HTTP client the colab CLI and MCP server use to reach
// the Colab server (contracts/colab-cli.md §1–2, openapi.yaml operations
// tagged x-colab-cli).
//
// Authentication is the attempt-scoped TaskToken (COLAB_TASK_TOKEN) sent as a
// Bearer header. The client never parses the token — scope is resolved by the
// server via GET /cli/context.
package client

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
	"strings"
	"time"
)

// Exit codes — contracts/colab-cli.md §2.
const (
	ExitOK          = 0
	ExitUsage       = 2 // argument error
	ExitRefused     = 3 // permission · state · policy (403/404/409/422)
	ExitNoToken     = 4 // token missing or revoked (401)
	ExitUnreachable = 5 // server unreachable (network, 5xx)
)

// Environment the daemon sets — exactly the contract table in
// contracts/colab-cli.md §1 (harness.md §2.1). Nothing else is required.
const (
	EnvToken     = "COLAB_TASK_TOKEN"
	EnvServerURL = "COLAB_SERVER_URL" // origin only; the API prefix is appended
	EnvTaskID    = "COLAB_TASK_ID"
	EnvAttempt   = "COLAB_TASK_ATTEMPT" // marks the attempt boundary for the seq state (v0.2)
	EnvLaneID    = "COLAB_LANE_ID"
	EnvSessionID = "COLAB_SESSION_ID"
	EnvAgentName = "COLAB_AGENT_NAME"
	// EnvAPIPrefix is the contract's optional override of the API root
	// appended to COLAB_SERVER_URL (openapi.yaml servers[0].url = /api/v1).
	EnvAPIPrefix = "COLAB_API_PREFIX"
)

// CLI-internal knobs — NOT part of the environment contract; the daemon never
// sets them. They exist for tests and manual retries.
const (
	// EnvStateDir overrides where the client_seq state is persisted.
	EnvStateDir = "COLAB_STATE_DIR"
	// EnvClientSeq forces the seq of the next post (re-send the same key).
	EnvClientSeq = "COLAB_CLIENT_SEQ"
)

const (
	DefaultAPIPrefix = "/api/v1"
	DefaultTimeout   = 30 * time.Second
)

// Config is everything the client needs. Zero values are filled from the
// environment by FromEnv.
type Config struct {
	Token     string
	ServerURL string // origin, e.g. http://localhost:8080
	APIPrefix string // default /api/v1
	TaskID    string
	LaneID    string
	SessionID string
	AgentName string
	Attempt   int // 0 = unknown → resolved via /cli/context
	StateDir  string
	ClientSeq int // >0 forces the next Idempotency-Key seq (internal)
	Timeout   time.Duration
	HTTP      *http.Client
}

// Getenv abstracts os.Getenv so tests can inject an environment.
type Getenv func(string) string

// FromEnv builds a Config from COLAB_* variables.
func FromEnv(getenv Getenv) Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	c := Config{
		Token:     strings.TrimSpace(getenv(EnvToken)),
		ServerURL: strings.TrimRight(strings.TrimSpace(getenv(EnvServerURL)), "/"),
		APIPrefix: getenv(EnvAPIPrefix),
		TaskID:    getenv(EnvTaskID),
		LaneID:    getenv(EnvLaneID),
		SessionID: getenv(EnvSessionID),
		AgentName: getenv(EnvAgentName),
		StateDir:  getenv(EnvStateDir),
		Timeout:   DefaultTimeout,
	}
	if v := getenv(EnvAttempt); v != "" {
		fmt.Sscanf(v, "%d", &c.Attempt)
	}
	if v := getenv(EnvClientSeq); v != "" {
		fmt.Sscanf(v, "%d", &c.ClientSeq)
	}
	return c
}

// Client talks to one Colab server with one TaskToken.
type Client struct {
	cfg  Config
	http *http.Client
	ctx  *CliContext // cached GET /cli/context
}

// New returns a Client. It does not touch the network.
func New(cfg Config) *Client {
	if cfg.APIPrefix == "" {
		cfg.APIPrefix = DefaultAPIPrefix
	}
	if !strings.HasPrefix(cfg.APIPrefix, "/") {
		cfg.APIPrefix = "/" + cfg.APIPrefix
	}
	cfg.APIPrefix = strings.TrimRight(cfg.APIPrefix, "/")
	// If the daemon already handed a URL ending in the prefix, don't double it.
	if cfg.ServerURL != "" && strings.HasSuffix(cfg.ServerURL, cfg.APIPrefix) {
		cfg.ServerURL = strings.TrimSuffix(cfg.ServerURL, cfg.APIPrefix)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, http: hc}
}

// Config returns the effective configuration.
func (c *Client) Config() Config { return c.cfg }

// Error is a failure the caller maps to an exit code. It wraps RFC 9457
// problem details when the server produced them.
type Error struct {
	Exit    int      `json:"exit"`
	Code    string   `json:"code,omitempty"` // machine id: token_revoked, no_token, unreachable, usage, …
	Status  int      `json:"status,omitempty"`
	Title   string   `json:"title,omitempty"`
	Detail  string   `json:"detail,omitempty"`
	Problem *Problem `json:"problem,omitempty"`
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Code != "" {
		b.WriteString(e.Code)
	} else {
		fmt.Fprintf(&b, "exit %d", e.Exit)
	}
	if e.Title != "" {
		b.WriteString(": " + e.Title)
	}
	if e.Detail != "" {
		b.WriteString(" — " + e.Detail)
	}
	return b.String()
}

// Usage returns an argument error (exit 2).
func Usage(format string, a ...any) *Error {
	return &Error{Exit: ExitUsage, Code: "usage", Title: "invalid arguments", Detail: fmt.Sprintf(format, a...)}
}

// ErrNoToken is returned when COLAB_TASK_TOKEN is unset (test chat, E15-04).
var ErrNoToken = &Error{Exit: ExitNoToken, Code: "no_token", Title: "no token",
	Detail: "COLAB_TASK_TOKEN is not set — colab commands are unavailable in this context (test chat has no token)"}

// ExitCode extracts the exit code from any error.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Exit
	}
	return ExitUnreachable
}

// AsError converts any error into *Error (unknown errors become exit 5).
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: err.Error()}
}

// RawBody is a request body that is already encoded — used for the
// multipart/form-data submitArtifact upload. Passing one to Do sets
// Content-Type to ContentType instead of application/json.
type RawBody struct {
	ContentType string
	Data        []byte
}

// Response carries the decoded body plus the headers the CLI surfaces.
type Response struct {
	Status   int
	Header   http.Header
	Body     []byte
	Replayed bool // Idempotent-Replayed: true
}

// Do performs one authenticated request. path is relative to the API prefix.
// body (if non-nil) is JSON-encoded. Headers are merged into the request.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, headers http.Header) (*Response, error) {
	if c.cfg.Token == "" {
		return nil, ErrNoToken
	}
	if c.cfg.ServerURL == "" {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: EnvServerURL + " is not set"}
	}
	u := c.cfg.ServerURL + c.cfg.APIPrefix + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	contentType := "application/json"
	if rb, ok := body.(*RawBody); ok {
		// Pre-encoded body (multipart/form-data for submitArtifact).
		rd, contentType = bytes.NewReader(rb.Data), rb.ContentType
	} else if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, Usage("encode body: %v", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, Usage("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "application/json, application/problem+json")
	req.Header.Set("User-Agent", "colab-cli")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: err.Error()}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "read response", Detail: err.Error()}
	}
	out := &Response{Status: res.StatusCode, Header: res.Header, Body: raw,
		Replayed: strings.EqualFold(res.Header.Get("Idempotent-Replayed"), "true")}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return out, nil
	}
	return out, problemError(res.StatusCode, raw)
}

// problemError maps an HTTP failure to *Error (colab-cli.md §2 exit codes).
func problemError(status int, raw []byte) *Error {
	e := &Error{Status: status}
	var p Problem
	if len(raw) > 0 && json.Unmarshal(raw, &p) == nil && (p.Title != "" || p.Code != "" || p.Status != 0) {
		e.Problem = &p
		e.Code, e.Title, e.Detail = p.Code, p.Title, p.Detail
	}
	if e.Title == "" {
		e.Title = http.StatusText(status)
	}
	switch {
	case status == http.StatusUnauthorized:
		e.Exit = ExitNoToken
		if e.Code == "" {
			e.Code = "unauthorized"
		}
	case status >= 500:
		e.Exit = ExitUnreachable
		if e.Code == "" {
			e.Code = "server_error"
		}
	default: // 403 · 404 · 409 · 422 · other 4xx
		e.Exit = ExitRefused
		if e.Code == "" {
			e.Code = "refused"
		}
	}
	return e
}

// GetJSON is Do + decode.
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, v any) (*Response, error) {
	res, err := c.Do(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return res, err
	}
	return res, decode(res, v)
}

func decode(res *Response, v any) error {
	if v == nil || len(res.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(res.Body, v); err != nil {
		return &Error{Exit: ExitUnreachable, Code: "bad_response", Title: "unparseable server response", Detail: err.Error(), Status: res.Status}
	}
	return nil
}
