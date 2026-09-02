package v1

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ArenaPrivateMetricsHandler exposes only the bootstrap-owned private registry.
func (s *Server) ArenaPrivateMetricsHandler() http.Handler {
	if s == nil || s.arenaMetrics == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(s.arenaMetrics, promhttp.HandlerOpts{EnableOpenMetrics: true})
}
