package v1

import (
	"errors"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// (POST /api/v1/players/join).
func (s *Server) JoinPlayer(w http.ResponseWriter, r *http.Request) {
	if !s.enterPublicRequest(w, r, s.playerJoinPolicy()) {
		return
	}
	s.logSecurityEvent(r, "player.join", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodePlayerJoinRetired))
	errmap.HandleError(w, r, domain.ErrPlayerJoinRetired)
}

// (GET /api/v1/players/me).
func (s *Server) GetCurrentPlayer(w http.ResponseWriter, r *http.Request) {
	if s.players == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player.SessionToken == nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return
	}

	me, err := s.players.GetCurrentPlayer(r.Context(), *player.SessionToken)
	if err != nil {
		if errors.Is(err, domain.ErrAccountDeleted) {
			middleware.ClearPlayerSessionCookie(w, r)
			middleware.ClearPlayerCSRFCookie(w, r)
		}
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

	response.WriteJSON(w, http.StatusOK, response.CurrentPlayer(me))
}

// (POST /api/v1/players/logout).
func (s *Server) LogoutPlayer(w http.ResponseWriter, r *http.Request, _ api.LogoutPlayerParams) {
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
