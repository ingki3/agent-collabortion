package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// DefaultBaseURL is the Messages API. It is overridable so the isolated stack
// can point at a stub without an API key leaving the machine.
const DefaultBaseURL = "https://api.anthropic.com"

// APIVersion is the Messages API version header.
const APIVersion = "2023-06-01"

// HTTPClient is the §8.5 client over the real Messages API.
type HTTPClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	Log     *slog.Logger
}

// FromEnv builds the platform client from the environment, or returns nil when
// no key is configured.
//
// A nil client is a supported state, not a failure: the platform's own LLM
// features degrade (the summary is assembled from rows, as it was in P2) and
// every other part of the server runs unchanged. Making the key mandatory
// would mean a workspace with no Anthropic account could not finish a session
// at all, and it would make every isolated test stack need a live key.
func FromEnv(log *slog.Logger) *HTTPClient {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil
	}
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	return &HTTPClient{
		BaseURL: base, APIKey: key, Log: log,
		HTTP: &http.Client{Timeout: 120 * time.Second},
	}
}

type wireContent struct {
	Type         string         `json:"type"`
	Text         string         `json:"text,omitempty"`
	CacheControl map[string]any `json:"cache_control,omitempty"`
}

type wireRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	// System is a content block array so the stable prefix can carry
	// `cache_control` — a plain string cannot (§8.5 캐싱 행).
	System       []wireContent  `json:"system,omitempty"`
	Messages     []wireMessage  `json:"messages"`
	Stream       bool           `json:"stream,omitempty"`
	OutputConfig map[string]any `json:"output_config,omitempty"`
	Fallbacks    string         `json:"fallbacks,omitempty"`
}

type wireMessage struct {
	Role    string        `json:"role"`
	Content []wireContent `json:"content"`
}

type wireResponse struct {
	StopReason  string `json:"stop_reason"`
	StopDetails *struct {
		Category string `json:"category"`
	} `json:"stop_details"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// Encode turns a Request into the Messages API body. It is separate from Do so
// the shape can be asserted in a unit test without a network.
func Encode(req Request) wireRequest {
	w := wireRequest{Model: req.Model, MaxTokens: req.MaxTokens, Stream: req.Streaming}
	if req.System != "" {
		blk := wireContent{Type: "text", Text: req.System}
		if req.CacheControlOnPrefix {
			blk.CacheControl = map[string]any{"type": "ephemeral"}
		}
		w.System = []wireContent{blk}
	}
	w.Messages = []wireMessage{{Role: "user", Content: []wireContent{{Type: "text", Text: req.Prompt}}}}
	if req.ForceJSON {
		w.OutputConfig = map[string]any{"format": map[string]any{"type": "json_object"}}
	}
	if req.FallbackParam != "" {
		w.Fallbacks = req.FallbackParam
	}
	return w
}

// Do runs one §8.5 job.
//
// Streaming (§8.5 스트리밍 행) is requested on the wire for long jobs, but the
// response is accumulated before it is returned: the platform's consumers —
// the summary message, the criteria_met verdict — are whole-document consumers
// with nobody watching a cursor. Asking for the stream still buys the thing
// that matters here, which is not being cut off by a proxy's idle timeout on a
// long generation.
//
// production caller: sessions.Service.summarise (JobSessionSummary).
func (c *HTTPClient) Do(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(Encode(req))
	if err != nil {
		return nil, fmt.Errorf("llm: encode: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", APIVersion)
	if req.FallbackHeader != "" {
		// §8.5 폴백 행: the beta header and `fallbacks` are one opt-in in two
		// places. Sending either alone gets no server-side fallback and says
		// nothing about it.
		httpReq.Header.Set("anthropic-beta", req.FallbackHeader)
	}
	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: %s: %w", req.Job, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: %s: read: %w", req.Job, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: %s: http %d: %s", req.Job, res.StatusCode, truncate(string(raw), 400))
	}
	var wr wireResponse
	if err := json.Unmarshal(raw, &wr); err != nil {
		return nil, fmt.Errorf("llm: %s: decode: %w", req.Job, err)
	}
	var text string
	for _, blk := range wr.Content {
		if blk.Type == "text" {
			text += blk.Text
		}
	}
	category := ""
	if wr.StopDetails != nil {
		category = wr.StopDetails.Category
	}
	out := NewResponse(wr.StopReason, category, text, Usage{
		InputTokens:              wr.Usage.InputTokens,
		OutputTokens:             wr.Usage.OutputTokens,
		CacheReadInputTokens:     wr.Usage.CacheReadInputTokens,
		CacheCreationInputTokens: wr.Usage.CacheCreationInputTokens,
	})
	if req.CacheControlOnPrefix && c.Log != nil {
		v := VerifyCache(req.PrefixTokens, MinCacheTokens(req.Model), out.Usage.CacheReadInputTokens)
		if v.WarnedTooShort {
			// Nothing errored and the bill simply did not drop — this line is
			// the only evidence that would ever exist (§8.5 캐싱 행).
			c.Log.Warn("llm: cache_control had no effect — prefix under the model minimum",
				"job", req.Job, "model", req.Model,
				"prefix_tokens", req.PrefixTokens, "min_tokens", MinCacheTokens(req.Model))
		} else if !v.Hit {
			c.Log.Debug("llm: cache miss", "job", req.Job, "prefix_tokens", req.PrefixTokens)
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
