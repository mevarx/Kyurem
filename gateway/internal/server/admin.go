package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kyurem-project/kyurem/gateway/internal/metrics"
)

// prometheusHandler exposes the gateway's registry at /metrics.
func prometheusHandler(mtr *metrics.Metrics) http.Handler {
	reg := mtr.Registry()
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		Registry: reg,
	})
}

// recoveryMiddleware keeps admin endpoints from crash-looping the process
// on unexpected panics; panics log with a short stack and answer 500.
func recoveryMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("admin handler panic",
					"path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// jsonIndent renders the tiny structured admin responses (allow, readyz).
func jsonIndent(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}
