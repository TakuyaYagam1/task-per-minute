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
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (s *Server) RegisterPlayer(w http.ResponseWriter, r *http.Request) {
	if !s.enterPublicRequest(w, r, s.playerRegisterPolicy()) {
		return
	}
	if s.playerAccounts == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.PlayerRegistrationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		s.logSecurityEvent(r, "player.register", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeValidation))
		return
	}
	if err := s.playerAccounts.Register(r.Context(), inbound.RegisterPlayerCommand{
		Username: body.Username,
		Email:    string(body.Email),
		Password: accountRequestString(body.Password),
	}); err != nil {
		s.logSecurityEvent(r, "player.register", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}
	s.logSecurityEvent(r, "player.register", securityOutcomeSuccess, nil)
	response.WriteJSON(w, http.StatusAccepted, api.PlayerAccountAcceptedResponse{
		Accepted: api.PlayerAccountAcceptedResponseAccepted(true),
	})
}

func (s *Server) LoginPlayer(w http.ResponseWriter, r *http.Request) {
	if !s.enterPublicRequest(w, r, s.playerLoginPolicy()) {
		return
	}
	if s.playerAccounts == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.PlayerLoginRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		s.logSecurityEvent(r, "player.login", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeValidation))
		return
	}
	player, err := s.playerAccounts.Login(r.Context(), inbound.LoginPlayerCommand{
		Login:    body.Login,
		Password: accountRequestString(body.Password),
	})
	if err != nil {
		s.logSecurityEvent(r, "player.login", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}
	if player == nil || player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		s.logSecurityEvent(r, "player.login", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInternal))
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	csrfToken, err := middleware.NewPlayerCSRFToken(*player.SessionToken)
	if err != nil {
		s.logSecurityEvent(r, "player.login", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInternal))
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	middleware.SetPlayerSessionCookie(w, r, *player.SessionToken)
	middleware.SetPlayerCSRFCookie(w, r, csrfToken)
	s.logSecurityEvent(r, "player.login", securityOutcomeSuccess, logkitFields("player_id", player.ID.String()))
	response.WriteJSON(w, http.StatusOK, api.JoinPlayerResponse{PlayerId: player.ID})
}

func (s *Server) ResendPlayerVerificationForLogin(w http.ResponseWriter, r *http.Request) {
	if !s.enterPublicRequest(w, r, s.playerLoginResendPolicy()) {
		return
	}
	if s.playerAccounts == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.PlayerLoginRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		s.logSecurityEvent(r, "player.login.resend_verification", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeValidation))
		return
	}
	err := s.playerAccounts.ResendVerificationForLogin(r.Context(), inbound.LoginPlayerCommand{
		Login:    body.Login,
		Password: accountRequestString(body.Password),
	})
	if err != nil {
		s.logSecurityEvent(r, "player.login.resend_verification", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		if errors.Is(err, domain.ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
		}
		errmap.HandleError(w, r, err)
		return
	}
	s.logSecurityEvent(r, "player.login.resend_verification", securityOutcomeSuccess, nil)
	response.WriteJSON(w, http.StatusAccepted, api.PlayerAccountAcceptedResponse{
		Accepted: api.PlayerAccountAcceptedResponseAccepted(true),
	})
}

func (s *Server) VerifyPlayerEmail(w http.ResponseWriter, r *http.Request) {
	if !s.enterPublicRequest(w, r, s.playerVerifyPolicy()) {
		return
	}
	if s.playerAccounts == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.PlayerVerificationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		s.logSecurityEvent(r, "player.verify_email", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeValidation))
		return
	}
	if err := s.playerAccounts.VerifyEmail(r.Context(), body.Token); err != nil {
		s.logSecurityEvent(r, "player.verify_email", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}
	s.logSecurityEvent(r, "player.verify_email", securityOutcomeSuccess, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ResendPlayerVerification(w http.ResponseWriter, r *http.Request) {
	if !s.enterPublicRequest(w, r, s.playerResendPolicy()) {
		return
	}
	if s.playerAccounts == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.PlayerResendVerificationRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		s.logSecurityEvent(r, "player.resend_verification", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeValidation))
		return
	}
	if err := s.playerAccounts.ResendVerification(r.Context(), string(body.Email)); err != nil {
		s.logSecurityEvent(r, "player.resend_verification", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}
	s.logSecurityEvent(r, "player.resend_verification", securityOutcomeSuccess, nil)
	response.WriteJSON(w, http.StatusAccepted, api.PlayerAccountAcceptedResponse{
		Accepted: api.PlayerAccountAcceptedResponseAccepted(true),
	})
}

func accountRequestString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
