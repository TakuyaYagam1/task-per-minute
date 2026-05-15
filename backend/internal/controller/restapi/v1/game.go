package v1

import (
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
)

// (GET /api/v1/leaderboard).
func (s *Server) GetLeaderboard(w http.ResponseWriter, r *http.Request) {
	if s.leaderboard == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}
	if !s.leaderboardLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.leaderboardLimiter.RetryAfter())
		errmap.HandleError(w, r, apperr.ErrRateLimited)
		return
	}

	entries, err := s.leaderboard.Top50(r.Context())
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.Leaderboard(entries))
}

// (GET /api/v1/duels/{id}).
func (s *Server) GetDuel(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if s.duels == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok {
		errmap.HandleError(w, r, apperr.ErrInvalidSession)
		return
	}

	detail, err := s.duels.GetDuel(r.Context(), id, player.ID)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.DuelDetail(detail.Duel, detail.PlayerTasks))
}
