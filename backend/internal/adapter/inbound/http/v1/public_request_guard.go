package v1

import (
	"context"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
)

type requestRateLimiter interface {
	Allow(string) bool
	RetryAfter() string
}

type publicRequestPolicy struct {
	event          string
	validationCode apperr.Code
	limiter        requestRateLimiter
}

type publicRequestState struct {
	handlerReached bool
}

type publicRequestStateKey struct{}

func (s *Server) publicRequestGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			policy, ok := s.publicRequestPolicy(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			if !policy.limiter.Allow(middleware.ClientIPFromRequest(r)) {
				w.Header().Set("Retry-After", policy.limiter.RetryAfter())
				s.logSecurityEvent(r, policy.event, securityOutcomeRateLimited, nil)
				errmap.HandleError(w, r, apperr.ErrRateLimited)
				return
			}

			state := &publicRequestState{}
			r = r.WithContext(context.WithValue(r.Context(), publicRequestStateKey{}, state))
			next.ServeHTTP(w, r)
			if !state.handlerReached {
				s.logSecurityEvent(
					r,
					policy.event,
					securityOutcomeFailure,
					logkitFields("error_code", policy.validationCode),
				)
			}
		})
	}
}

func (s *Server) enterPublicRequest(w http.ResponseWriter, r *http.Request, policy publicRequestPolicy) bool {
	if state, ok := r.Context().Value(publicRequestStateKey{}).(*publicRequestState); ok && state != nil {
		state.handlerReached = true
		return true
	}

	if policy.limiter.Allow(middleware.ClientIPFromRequest(r)) {
		return true
	}

	w.Header().Set("Retry-After", policy.limiter.RetryAfter())
	s.logSecurityEvent(r, policy.event, securityOutcomeRateLimited, nil)
	errmap.HandleError(w, r, apperr.ErrRateLimited)
	return false
}

func (s *Server) publicRequestPolicy(r *http.Request) (publicRequestPolicy, bool) {
	if s == nil || r == nil || r.Method != http.MethodPost {
		return publicRequestPolicy{}, false
	}

	switch r.URL.Path {
	case "/api/v1/admin/login":
		return s.adminLoginPolicy(), true
	case "/api/v1/admin/refresh":
		return s.adminRefreshPolicy(), true
	case "/api/v1/players/join":
		return s.playerJoinPolicy(), true
	default:
		return publicRequestPolicy{}, false
	}
}

func (s *Server) adminLoginPolicy() publicRequestPolicy {
	return publicRequestPolicy{
		event:          "admin.login",
		validationCode: apperr.CodeInvalidCredentials,
		limiter:        s.loginLimiter,
	}
}

func (s *Server) adminRefreshPolicy() publicRequestPolicy {
	return publicRequestPolicy{
		event:          "admin.refresh",
		validationCode: apperr.CodeInvalidCredentials,
		limiter:        s.refreshLimiter,
	}
}

func (s *Server) playerJoinPolicy() publicRequestPolicy {
	return publicRequestPolicy{
		event:          "player.join",
		validationCode: apperr.CodeValidation,
		limiter:        s.joinLimiter,
	}
}
