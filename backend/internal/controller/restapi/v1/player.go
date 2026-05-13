package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
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
		s.logSecurityEvent(r, "player.join", securityOutcomeRateLimited, nil)
		errmap.HandleError(w, r, apperr.ErrRateLimited)
		return
	}

	var body openapi.JoinRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrValidation) {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", apperr.CodeValidation))
		return
	}

	player, err := s.players.Join(r.Context(), body.Username)
	if err != nil {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}
	if player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", apperr.CodeInternal))
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}
	csrfToken, err := middleware.NewPlayerCSRFToken(*player.SessionToken)
	if err != nil {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", apperr.CodeInternal))
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	middleware.SetPlayerSessionCookie(w, r, *player.SessionToken)
	middleware.SetPlayerCSRFCookie(w, r, csrfToken)
	s.logSecurityEvent(r, "player.join", securityOutcomeSuccess, logkitFields("player_id", player.ID.String()))
	response.WriteJSON(w, http.StatusOK, openapi.JoinResponse{
		PlayerId: player.ID,
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
	if err := middleware.EnsurePlayerCSRFCookie(w, r, *player.SessionToken); err != nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.PlayerMe(me))
}

// (POST /api/v1/players/logout).
func (s *Server) LogoutPlayer(w http.ResponseWriter, r *http.Request) {
	middleware.ClearPlayerSessionCookie(w, r)
	middleware.ClearPlayerCSRFCookie(w, r)
	if s.players == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if token, ok := middleware.PlayerSessionTokenFromRequest(r); ok {
		if err := s.players.Logout(r.Context(), token); err != nil {
			s.logSecurityEvent(r, "player.logout", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
			errmap.HandleError(w, r, err)
			return
		}
		s.logSecurityEvent(r, "player.logout", securityOutcomeSuccess, nil)
	} else {
		s.logSecurityEvent(r, "player.logout", securityOutcomeSuccess, logkitFields("session_present", false))
	}
	w.WriteHeader(http.StatusNoContent)
}
