package runtimes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// CandidateQuery is listRuntimeCandidates' input (S6 4단계 · S17 재바인딩).
type CandidateQuery struct {
	Isolation string
	RemoteURL string
	SessionID *uuid.UUID
}

// Candidates answers listRuntimeCandidates (FR-2.1, FR-9.2 F).
//
// The isolation kind decides the rule, not the caller: `none` takes every
// online runtime and allows "자동 선택(첫 실행 시 고정)"; `worktree` takes only
// online runtimes that already have a clone of the **same remote URL** —
// comparing remote URLs, never paths (E14-04·05). Runtimes that do not
// qualify come back too, with `eligible: false` and a reason, because S6 draws
// them disabled with the reason next to them rather than hiding them.
func (s *Service) Candidates(ctx context.Context, wsID uuid.UUID, q CandidateQuery) (bool, []gen.RuntimeCandidate, error) {
	isolation, remote := q.Isolation, strings.TrimSpace(q.RemoteURL)
	if q.SessionID != nil {
		k, r, err := s.sessionIsolation(ctx, wsID, *q.SessionID)
		if err != nil {
			return false, nil, err
		}
		isolation = k
		if remote == "" {
			remote = r
		}
	}
	switch isolation {
	case "none", "worktree", "container":
	default:
		return false, nil, apperr.Validation(apperr.Field("isolation", "enum", "isolation must be worktree, container or none"))
	}
	if isolation == "worktree" && remote == "" {
		return false, nil, apperr.Validation(apperr.Field("remote_url", "required",
			"worktree isolation needs the repository's remote_url (or a session_id to read it from)"))
	}

	rows, err := s.DB.Query(ctx, `SELECT id FROM runtime WHERE workspace_id = $1 ORDER BY created_at`, wsID)
	if err != nil {
		return false, nil, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, nil, err
	}

	want := NormalizeRemote(remote)
	out := make([]gen.RuntimeCandidate, 0, len(ids))
	for _, id := range ids {
		d, err := s.Get(ctx, id)
		if err != nil {
			return false, nil, err
		}
		c := gen.RuntimeCandidate{Runtime: d.Runtime, Reason: nullable.NewNullNullable[string]()}
		switch {
		case d.Runtime.Status != gen.RuntimeStatus("online"):
			c.Reason = nullable.NewNullableWithValue("오프라인 — 이 컴퓨터의 데몬이 연결돼 있지 않습니다")
		case isolation != "worktree":
			c.Eligible = true
		default:
			if repo := matchRepo(d.Runtime.Repos, want); repo != nil {
				c.Eligible, c.MatchedRepo = true, repo
			} else if len(d.Runtime.Repos) == 0 {
				c.Reason = nullable.NewNullableWithValue("이 컴퓨터에서 감지된 저장소가 없습니다")
			} else {
				c.Reason = nullable.NewNullableWithValue("같은 remote URL 의 저장소가 없습니다 — " + remote)
			}
		}
		out = append(out, c)
	}
	// SCREEN §4.4 4행: "자동 선택(첫 실행 시 고정)" 은 `none` 만이다. worktree 는
	// 저장소가 있는 머신을 사람이 골라야 하므로 비활성(FR-2.1 M10).
	return isolation == "none", out, nil
}

// sessionIsolation reads the isolation kind and repository of an existing
// session (S17 rebinding: the candidate rule comes from the session, not the URL).
func (s *Service) sessionIsolation(ctx context.Context, wsID, sessionID uuid.UUID) (string, string, error) {
	var raw []byte
	var ws uuid.UUID
	var runtimeID *uuid.UUID
	err := s.DB.QueryRow(ctx, `SELECT workspace_id, isolation, runtime_id FROM session WHERE id = $1`, sessionID).Scan(&ws, &raw, &runtimeID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ws != wsID) {
		return "", "", apperr.NotFound("session")
	}
	if err != nil {
		return "", "", fmt.Errorf("runtimes: session isolation: %w", err)
	}
	var iso struct {
		Kind      string `json:"kind"`
		RepoPath  string `json:"repo_path"`
		RemoteURL string `json:"remote_url"`
	}
	if err := json.Unmarshal(raw, &iso); err != nil {
		return "", "", fmt.Errorf("runtimes: session isolation: %w", err)
	}
	remote := iso.RemoteURL
	// The session stores the repo by path on its own runtime; the candidate
	// rule needs the remote URL, so resolve it through that runtime's probe.
	if remote == "" && iso.RepoPath != "" && runtimeID != nil {
		if d, err := s.Get(ctx, *runtimeID); err == nil {
			for _, rp := range d.Runtime.Repos {
				if rp.Path == iso.RepoPath && rp.RemoteUrl.IsSpecified() && !rp.RemoteUrl.IsNull() {
					remote = rp.RemoteUrl.MustGet()
					break
				}
			}
		}
	}
	return iso.Kind, remote, nil
}

func matchRepo(repos []gen.RuntimeRepo, want string) *gen.RuntimeRepo {
	if want == "" {
		return nil
	}
	for i := range repos {
		u := repos[i].RemoteUrl
		if !u.IsSpecified() || u.IsNull() {
			continue
		}
		if NormalizeRemote(u.MustGet()) == want {
			return &repos[i]
		}
	}
	return nil
}

// NormalizeRemote makes "the same repository" a decidable question. The same
// clone is reached as `git@github.com:o/r.git`, `https://github.com/o/r` and
// `ssh://git@github.com/o/r.git/`; comparing the raw strings would call three
// copies of one repo three different repos, and E14-04·05 are exactly that
// case. Path comparison is explicitly not the rule (openapi listRuntimeCandidates).
func NormalizeRemote(u string) string {
	s := strings.TrimSpace(strings.ToLower(u))
	if s == "" {
		return ""
	}
	for _, scheme := range []string{"ssh://", "git+ssh://", "https://", "http://", "git://", "file://"} {
		if strings.HasPrefix(s, scheme) {
			s = s[len(scheme):]
			break
		}
	}
	if at := strings.Index(s, "@"); at >= 0 && !strings.Contains(s[:at], "/") {
		s = s[at+1:] // strip the scp-style or URL user
	}
	// scp-style `host:owner/repo` → `host/owner/repo`. A `:port` stays a port.
	if c := strings.Index(s, ":"); c >= 0 && !strings.Contains(s[:c], "/") {
		rest := s[c+1:]
		if rest != "" && (rest[0] < '0' || rest[0] > '9') {
			s = s[:c] + "/" + rest
		}
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
}
