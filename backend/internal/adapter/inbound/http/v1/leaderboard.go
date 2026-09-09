package v1

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// GetLeaderboard handles GET /api/v1/leaderboard.
func (s *Server) GetLeaderboard(w http.ResponseWriter, r *http.Request) {
	if s.leaderboard == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if !s.leaderboardLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.leaderboardLimiter.RetryAfter())
		errmap.HandleError(w, r, domain.ErrRateLimited)
		return
	}

	entries, err := s.leaderboard.Top50(r.Context())
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.Leaderboard(entries))
}
