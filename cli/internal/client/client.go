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
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	cfg       Config
	http      *http.Client
	transport http.RoundTripper // shared with DoStream; never nil
	ctx       *CliContext       // cached GET /cli/context
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
	tr := transportFor(hc, cfg.Timeout)
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout, Transport: tr}
	}
	return &Client{cfg: cfg, http: hc, transport: tr}
}

// transportFor resolves the transport both Do and DoStream use. Do relies on
// http.Client.Timeout for its whole-request bound, but DoStream cannot (a
// large artifact would be cut off mid-body), so the bound it needs —
// "the server must at least start answering" — has to live in the transport
// where both share it. An explicitly configured client keeps its own.
func transportFor(hc *http.Client, timeout time.Duration) http.RoundTripper {
	if hc != nil && hc.Transport != nil {
		return hc.Transport
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := tr.Clone()
	t.ResponseHeaderTimeout = timeout // status line + headers must arrive
	t.TLSHandshakeTimeout = timeout
	t.ExpectContinueTimeout = timeout
	return t
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

// Do performs one authenticated request and buffers the response, which must
// therefore be a JSON or problem document (MaxJSONResponse). Anything that can
// be large — an artifact body — goes through DoStream instead.
//
// path is relative to the API prefix. body (if non-nil) is JSON-encoded.
// Headers are merged into the request.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, headers http.Header) (*Response, error) {
	if c.cfg.Token == "" {
		return nil, ErrNoToken
	}
	if c.cfg.ServerURL == "" {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: EnvServerURL + " is not set"}
	}
	req, err := c.newRequest(ctx, method, path, query, body, headers)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: err.Error()}
	}
	defer res.Body.Close()
	raw, err := readBounded(res.Body)
	if err != nil {
		return nil, err
	}
	out := &Response{Status: res.StatusCode, Header: res.Header, Body: raw,
		Replayed: strings.EqualFold(res.Header.Get("Idempotent-Replayed"), "true")}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return out, nil
	}
	return out, problemError(res.StatusCode, raw)
}

// MaxJSONResponse bounds the buffered responses. It applies only to JSON and
// problem documents; artifact bodies are streamed (DoStream) and are never
// subject to it. The bound refuses rather than truncates: a cut-short JSON
// document cannot be mistaken for a complete one.
const MaxJSONResponse = 16 << 20

// readBounded reads a response body that is meant to be small. Reading one
// byte past the bound is an error, never a silent truncation — io.LimitReader
// alone returns a clean EOF at the limit, which is what let a 17 MiB artifact
// land on disk at 16 MiB and report success.
func readBounded(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, MaxJSONResponse+1))
	if err != nil {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "read response", Detail: err.Error()}
	}
	if len(raw) > MaxJSONResponse {
		return nil, &Error{Exit: ExitUnreachable, Code: "response_too_large",
			Title:  "response too large",
			Detail: fmt.Sprintf("the server sent more than %d bytes of JSON", MaxJSONResponse)}
	}
	return raw, nil
}

// newRequest builds one authenticated request. path is relative to the API
// prefix; body is JSON-encoded unless it is a *RawBody.
func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body any, headers http.Header) (*http.Request, error) {
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
	req.Header.Set("User-Agent", "colab-cli")
	req.Header.Set("Accept", "application/json, application/problem+json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req, nil
}

// Stream is a response whose body is still open, for payloads that must not
// be held in memory (artifact downloads). The caller must Close it.
type Stream struct {
	Header      http.Header
	Body        io.ReadCloser
	ContentType string
	// Length is the declared Content-Length, or -1 when the server did not
	// send one (chunked). A caller that writes the body to a file must check
	// what it wrote against this.
	Length   int64
	FileName string // from Content-Disposition, when present
}

// Close releases the connection.
func (s *Stream) Close() error {
	if s == nil || s.Body == nil {
		return nil
	}
	return s.Body.Close()
}

// ErrStalled reports a transfer that went quiet: no byte arrived for the idle
// timeout. Callers map it to exit 5 like any other transport failure.
var ErrStalled = errors.New("transfer stalled: no data received")

// idleReader bounds the silence in a stream rather than its total duration.
// An artifact can legitimately take minutes; what it may not do is stop
// sending. A watchdog closes the underlying body when no progress is made for
// the timeout, which unblocks the Read that is parked in the kernel — there
// is no way to set a deadline on a plain io.Reader.
type idleReader struct {
	body     io.ReadCloser
	timeout  time.Duration
	progress atomic.Int64
	stalled  atomic.Bool
	stop     chan struct{}
	once     sync.Once
}

func newIdleReader(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if timeout <= 0 {
		return body
	}
	ir := &idleReader{body: body, timeout: timeout, stop: make(chan struct{})}
	go ir.watch()
	return ir
}

func (ir *idleReader) watch() {
	// Poll at half the bound so the worst-case overshoot is timeout/2.
	tick := time.NewTicker(ir.timeout / 2)
	defer tick.Stop()
	last, idle := ir.progress.Load(), time.Duration(0)
	for {
		select {
		case <-ir.stop:
			return
		case <-tick.C:
			if cur := ir.progress.Load(); cur != last {
				last, idle = cur, 0
				continue
			}
			if idle += ir.timeout / 2; idle >= ir.timeout {
				ir.stalled.Store(true)
				ir.body.Close() // frees the parked Read
				return
			}
		}
	}
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.body.Read(p)
	if n > 0 {
		ir.progress.Add(int64(n))
	}
	if err != nil && ir.stalled.Load() {
		// The watchdog closed the body; report why, not "use of closed …".
		return n, fmt.Errorf("%w for %s", ErrStalled, ir.timeout)
	}
	return n, err
}

func (ir *idleReader) Close() error {
	ir.once.Do(func() { close(ir.stop) })
	return ir.body.Close()
}

// DoStream performs an authenticated request and hands back the response body
// unread, so an arbitrarily large payload never has to fit in memory. A
// non-2xx response is a problem document — small — so it is read, closed and
// returned as *Error, exactly as Do would.
func (c *Client) DoStream(ctx context.Context, method, path string, query url.Values) (*Stream, error) {
	if c.cfg.Token == "" {
		return nil, ErrNoToken
	}
	if c.cfg.ServerURL == "" {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: EnvServerURL + " is not set"}
	}
	req, err := c.newRequest(ctx, method, path, query, nil, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	// Config.Timeout is a whole-request deadline, body included, which would
	// cut a large artifact short on a slow link — that bound is what silently
	// truncated downloads. But it was also the only thing that ended a wedged
	// transfer, so dropping it outright turns truncation into an unbounded
	// hang. The two jobs are separated instead: the transport bounds the wait
	// for response headers, and the body is bounded by *silence* rather than
	// by total duration (see idleReader), which is the only bound that suits
	// a payload of unknown size.
	hc := &http.Client{Transport: c.transport, CheckRedirect: c.http.CheckRedirect, Jar: c.http.Jar}
	res, err := hc.Do(req)
	if err != nil {
		return nil, &Error{Exit: ExitUnreachable, Code: "unreachable", Title: "server unreachable", Detail: err.Error()}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		raw, err := readBounded(res.Body)
		if err != nil {
			return nil, err
		}
		return nil, problemError(res.StatusCode, raw)
	}
	out := &Stream{Header: res.Header, Body: newIdleReader(res.Body, c.cfg.Timeout),
		ContentType: res.Header.Get("Content-Type"), Length: res.ContentLength}
	if cd := res.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			out.FileName = params["filename"]
		}
	}
	return out, nil
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
