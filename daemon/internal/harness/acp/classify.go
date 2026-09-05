package acp

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Failure is a classified harness error (harness §8).
type Failure struct {
	Kind      contracts.FailureKind
	NotBefore *time.Time // rate_limited only
	Detail    string
}

func (f Failure) Error() string { return string(f.Kind) + ": " + f.Detail }

// ClassifyInput is everything §8 looks at, in priority order.
type ClassifyInput struct {
	Err           error          // the request error (session/prompt etc.)
	LastRateLimit *RateLimitMeta // most recent usage_update._meta["_claude/rateLimit"]
	Stderr        string         // process stderr tail
	Now           time.Time      // for reset-time parsing
}

// Classify maps an error to a FailureKind following the §8 priority:
// rate_limited (1) rateLimit.status==rejected (2) -32603 + errorKind
// rate_limit|overloaded (3) usage-limit prefix → reset time or quota;
// then auth / config / other.
func Classify(in ClassifyInput) Failure {
	var rpc *RPCError
	isRPC := in.Err != nil && errors.As(in.Err, &rpc)
	msg := ""
	if in.Err != nil {
		msg = in.Err.Error()
	}

	// (1) structured signal from the last usage_update
	if in.LastRateLimit != nil && in.LastRateLimit.Status == "rejected" {
		nb := in.Now.Add(contracts.RateLimitFallback)
		if in.LastRateLimit.ResetsAt > 0 {
			nb = time.Unix(in.LastRateLimit.ResetsAt, 0).UTC()
		}
		return Failure{Kind: contracts.FailRateLimited, NotBefore: &nb, Detail: "rateLimit.status=rejected"}
	}
	if isRPC && rpc.Code == -32603 {
		// (2) errorKind
		switch rpc.ErrorKind() {
		case "rate_limit", "overloaded":
			nb := in.Now.Add(contracts.RateLimitFallback)
			if in.LastRateLimit != nil && in.LastRateLimit.ResetsAt > 0 {
				nb = time.Unix(in.LastRateLimit.ResetsAt, 0).UTC()
			}
			return Failure{Kind: contracts.FailRateLimited, NotBefore: &nb, Detail: "errorKind=" + rpc.ErrorKind()}
		case "authentication_failed":
			return Failure{Kind: contracts.FailAuth, Detail: rpc.Message}
		case "billing_error", "account_on_hold":
			return Failure{Kind: contracts.FailQuota, Detail: rpc.Message}
		case "model_not_found":
			return Failure{Kind: contracts.FailConfig, Detail: rpc.Message}
		}
		// (3) SDK usage-limit prefixes
		text := strings.TrimPrefix(rpc.Message, "Internal error: ")
		for _, p := range contracts.UsageLimitPrefixes {
			if strings.HasPrefix(text, p) {
				if t, ok := ParseResetTime(text, in.Now); ok {
					return Failure{Kind: contracts.FailRateLimited, NotBefore: &t, Detail: text}
				}
				return Failure{Kind: contracts.FailQuota, Detail: text}
			}
		}
	}
	if in.Err != nil && errors.Is(in.Err, ErrProtocolVersion) {
		return Failure{Kind: contracts.FailConfig, Detail: msg}
	}
	if isRPC && strings.Contains(strings.ToLower(rpc.Message), "session not found") {
		return Failure{Kind: contracts.FailOther, Detail: msg}
	}
	low := strings.ToLower(msg + "\n" + in.Stderr)
	if strings.Contains(low, "login expired") || strings.Contains(low, "unauthorized") || strings.Contains(low, "not logged in") || strings.Contains(low, "please run /login") || strings.Contains(low, "authentication_error") || strings.Contains(low, "invalid api key") {
		return Failure{Kind: contracts.FailAuth, Detail: firstLine(msg, in.Stderr)}
	}
	if in.Err != nil && errors.Is(in.Err, ErrProcessExited) {
		return Failure{Kind: contracts.FailOther, Detail: "UnexpectedExit: " + firstLine(msg, in.Stderr)}
	}
	return Failure{Kind: contracts.FailOther, Detail: firstLine(msg, in.Stderr)}
}

func firstLine(parts ...string) string {
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		}
		if len(s) > 500 {
			s = s[:500]
		}
		return s
	}
	return ""
}

// resetRe matches the SDK wording "resets 11am (Asia/Seoul)" / "resets 3:30pm (UTC)".
var resetRe = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*\(([^)]+)\)`)

// ParseResetTime extracts the next occurrence of the reset clock time in the
// named zone after now (harness §8 (3)).
func ParseResetTime(text string, now time.Time) (time.Time, bool) {
	m := resetRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(m[4])
	if err != nil {
		return time.Time{}, false
	}
	h, _ := strconv.Atoi(m[1])
	min := 0
	if m[2] != "" {
		min, _ = strconv.Atoi(m[2])
	}
	if strings.EqualFold(m[3], "pm") && h < 12 {
		h += 12
	}
	if strings.EqualFold(m[3], "am") && h == 12 {
		h = 0
	}
	local := now.In(loc)
	t := time.Date(local.Year(), local.Month(), local.Day(), h, min, 0, 0, loc)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t.UTC(), true
}
