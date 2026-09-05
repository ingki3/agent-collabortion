package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// idempotencyTTL is the key retention (openapi.md §1: 24h).
const idempotencyTTL = 24 * time.Hour

// readBody returns the body bytes and re-arms r.Body for decoding.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, *Problem) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		return nil, apperr.Validation(apperr.Field("body", "too_large", err.Error()))
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

func requestHash(r *http.Request, body []byte) string {
	h := sha256.New()
	h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// idempotent replays the stored response for (scope, key) or runs fn and
// stores its result. A reused key with a different body is 422
// idempotency_key_reused. Concurrent first calls with the same key are
// serialised by the primary key: the second insert fails and replays.
func (s *Server) idempotent(ctx context.Context, w http.ResponseWriter, scope, key, reqHash string, fn func() (int, any, *Problem)) {
	if key == "" {
		status, body, p := fn()
		if p != nil {
			writeProblem(w, p)
			return
		}
		writeJSON(w, status, body)
		return
	}
	now := s.Clock.Now()
	var storedHash string
	var storedStatus int
	var stored json.RawMessage
	err := s.DB.QueryRow(ctx, `SELECT request_hash, status, response FROM idempotency_key WHERE scope = $1 AND key = $2 AND expires_at > $3`, scope, key, now).
		Scan(&storedHash, &storedStatus, &stored)
	if err == nil {
		if storedHash != reqHash {
			writeProblem(w, &Problem{Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Title: "Validation failed",
				Detail: "Idempotency-Key was already used with a different request"})
			return
		}
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(storedStatus)
		_, _ = w.Write(stored)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeProblem(w, apperr.Internal(err))
		return
	}
	status, body, p := fn()
	if p != nil {
		writeProblem(w, p)
		return
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeProblem(w, apperr.Internal(err))
		return
	}
	if _, err := s.DB.Exec(ctx, `
		INSERT INTO idempotency_key (scope, key, request_hash, status, response, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (scope, key) DO NOTHING`,
		scope, key, reqHash, status, raw, now, now.Add(idempotencyTTL)); err != nil {
		s.Log.Warn("idempotency store failed", "err", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// PurgeIdempotency drops expired keys (scheduler).
func (s *Server) PurgeIdempotency(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM idempotency_key WHERE expires_at < $1`, s.Clock.Now())
	return err
}
