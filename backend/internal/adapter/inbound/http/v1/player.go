package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// (POST /api/v1/players/join).
func (s *Server) JoinPlayer(w http.ResponseWriter, r *http.Request) {
	if s.players == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	if !s.enterPublicRequest(w, r, s.playerJoinPolicy()) {
		return
	}

	var body api.JoinRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeValidation))
		return
	}

	player, err := s.players.Join(r.Context(), body.Username)
	if err != nil {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}
	if player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInternal))
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	csrfToken, err := middleware.NewPlayerCSRFToken(*player.SessionToken)
	if err != nil {
		s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInternal))
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	middleware.SetPlayerSessionCookie(w, r, *player.SessionToken)
	middleware.SetPlayerCSRFCookie(w, r, csrfToken)
	s.logSecurityEvent(r, "player.join", securityOutcomeSuccess, logkitFields("player_id", player.ID.String()))
	response.WriteJSON(w, http.StatusOK, api.JoinResponse{
		PlayerId: player.ID,
	})
}

// (GET /api/v1/players/me).
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	if s.players == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.SessionToken == nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return
	}

	me, err := s.players.GetMe(r.Context(), *player.SessionToken)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			err = domain.ErrInvalidSession
		}
		errmap.HandleError(w, r, err)
		return
	}
	if err := middleware.EnsurePlayerCSRFCookie(w, r, *player.SessionToken); err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
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
