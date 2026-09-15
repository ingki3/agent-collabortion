package httpapi

import (
	"net/http"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/metrics"
	"github.com/ingki3/agent-collabortion/server/internal/observations"
)

// GetWorkspaceObservations is PRD §11's 관찰 table (openapi
// getWorkspaceObservations, K-18): five rows in table order, drawn by S14
// under the ten indicators and never judged against a target. Member-only,
// the same `window` grammar as getWorkspaceMetrics, and a row with no sample
// is n 0 with null numbers (observations package).
func (s *Server) GetWorkspaceObservations(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.GetWorkspaceObservationsParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	window := observations.DefaultWindow
	if params.Window != nil && *params.Window != "" {
		window = *params.Window
	}
	d, err := metrics.ParseWindow(window)
	if err != nil {
		writeProblem(w, apperr.Validation(apperr.Field("window", "format", "집계 기간은 P30D · P2W · PT12H 같은 기간 표기로 적어 주세요")))
		return
	}
	now := s.Clock.Now()
	rows, err := observations.Compute(r.Context(), s.DB, workspaceId, d, now)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := gen.ObservationReport{WorkspaceId: workspaceId, Window: window, ComputedAt: now.UTC(), Rows: make([]gen.ObservationRow, 0, len(rows))}
	for _, row := range rows {
		out.Rows = append(out.Rows, observationAPI(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func observationAPI(r observations.Row) gen.ObservationRow {
	out := gen.ObservationRow{
		Key: gen.ObservationRowKey(r.Key), Label: r.Label, Note: r.Note, N: r.N,
		Value: nullableFloat64(r.Value), Median: nullableFloat64(r.Median), P95: nullableFloat64(r.P95),
	}
	if r.Breakdown != nil {
		bs := make([]struct {
			Kind  string  `json:"kind"`
			N     int     `json:"n"`
			Share float32 `json:"share"`
		}, 0, len(r.Breakdown))
		for _, b := range r.Breakdown {
			bs = append(bs, struct {
				Kind  string  `json:"kind"`
				N     int     `json:"n"`
				Share float32 `json:"share"`
			}{Kind: b.Kind, N: b.N, Share: float32(b.Share)})
		}
		out.Breakdown = &bs
	}
	return out
}
