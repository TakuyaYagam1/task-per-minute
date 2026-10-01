package v1

import (
	"errors"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

// GetLeaderboard handles GET /api/v1/leaderboard.
func (s *Server) GetLeaderboard(w http.ResponseWriter, r *http.Request, params api.GetLeaderboardParams) {
	if s.leaderboard == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if !s.leaderboardLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.leaderboardLimiter.RetryAfter())
		errmap.HandleError(w, r, domain.ErrRateLimited)
		return
	}
	if (params.Page != nil && *params.Page < 1) ||
		(params.PerPage != nil && (*params.PerPage < 1 || *params.PerPage > leaderboardusecase.MaxPageSize)) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	query := leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.DefaultPageSize,
	}
	if params.Search != nil {
		query.Search = *params.Search
	}
	if params.Wins != nil {
		query.Wins = leaderboardusecase.WinsFilter(*params.Wins)
	}
	if params.Page != nil {
		query.Page = *params.Page
	}
	if params.PerPage != nil {
		query.PerPage = *params.PerPage
	}
	page, err := s.leaderboard.Page(r.Context(), query)
	if err != nil {
		if errors.Is(err, leaderboardusecase.ErrInvalidPageQuery) {
			errmap.HandleError(w, r, domain.ErrValidation)
			return
		}
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.Leaderboard(page))
}
