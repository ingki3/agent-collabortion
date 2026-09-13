package httpapi

import (
	"net/http"

	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/metrics"
)

// GetWorkspaceMetrics is PRD §11's ten indicators (openapi getWorkspaceMetrics,
// S14 「대시보드」). The array is in §11 column order and the screen only draws
// it; a metric with no sample is value null · n 0 (metrics package).
func (s *Server) GetWorkspaceMetrics(w http.ResponseWriter, r *http.Request, workspaceId gen.WorkspaceId, params gen.GetWorkspaceMetricsParams) {
	if _, _, p := s.member(r, workspaceId); p != nil {
		writeProblem(w, p)
		return
	}
	window := metrics.DefaultWindow
	if params.Window != nil && *params.Window != "" {
		window = *params.Window
	}
	d, err := metrics.ParseWindow(window)
	if err != nil {
		writeProblem(w, apperr.Validation(apperr.Field("window", "format", "집계 기간은 P30D · P2W · PT12H 같은 기간 표기로 적어 주세요")))
		return
	}
	now := s.Clock.Now()
	ms, err := metrics.Compute(r.Context(), s.DB, workspaceId, d, now)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := gen.MetricsReport{WorkspaceId: workspaceId, Window: window, ComputedAt: now.UTC(), Metrics: make([]gen.Metric, 0, len(ms))}
	for _, m := range ms {
		out.Metrics = append(out.Metrics, metricAPI(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func metricAPI(m metrics.Metric) gen.Metric {
	out := gen.Metric{
		Key: gen.MetricKey(m.Key), Label: m.Label, Unit: gen.MetricUnit(m.Unit),
		Target: float32(m.Target), TargetOp: gen.MetricTargetOp(m.TargetOp), N: m.N, Note: m.Note,
		Value: nullableFloat64(m.Value),
	}
	if m.Breakdown != nil {
		bs := make([]struct {
			Kind   gen.RuntimeKind            `json:"kind"`
			N      int                        `json:"n"`
			Target float32                    `json:"target"`
			Value  nullable.Nullable[float32] `json:"value"`
		}, 0, len(m.Breakdown))
		for _, b := range m.Breakdown {
			bs = append(bs, struct {
				Kind   gen.RuntimeKind            `json:"kind"`
				N      int                        `json:"n"`
				Target float32                    `json:"target"`
				Value  nullable.Nullable[float32] `json:"value"`
			}{Kind: gen.RuntimeKind(b.Kind), N: b.N, Target: float32(b.Target), Value: nullableFloat64(b.Value)})
		}
		out.Breakdown = &bs
	}
	return out
}

func nullableFloat64(v *float64) nullable.Nullable[float32] {
	if v == nil {
		return nullable.NewNullNullable[float32]()
	}
	return nullable.NewNullableWithValue(float32(*v))
}
