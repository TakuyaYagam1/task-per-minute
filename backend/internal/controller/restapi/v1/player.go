package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/request"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

// (POST /api/v1/players/join).
func (s *Server) JoinPlayer(w http.ResponseWriter, r *http.Request) {
	if s.players == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	if !s.joinLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.joinLimiter.RetryAfter())
		errmap.HandleError(w, r, apperr.ErrRateLimited)
		return
	}

	var body openapi.JoinRequest
	if err := request.DecodeJSON(r, &body); err != nil {
		errmap.HandleError(w, r, apperr.ErrValidation)
		return
	}

	player, err := s.players.Join(r.Context(), body.Username)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	response.WriteJSON(w, http.StatusOK, openapi.JoinResponse{
		PlayerId:     player.ID,
		SessionToken: *player.SessionToken,
	})
}

// (GET /api/v1/players/me).
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	if s.players == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.SessionToken == nil {
		errmap.HandleError(w, r, apperr.ErrInvalidSession)
		return
	}

	me, err := s.players.GetMe(r.Context(), *player.SessionToken)
	if err != nil {
		if errors.Is(err, apperr.ErrPlayerNotFound) {
			err = apperr.ErrInvalidSession
		}
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.PlayerMe(me))
}
