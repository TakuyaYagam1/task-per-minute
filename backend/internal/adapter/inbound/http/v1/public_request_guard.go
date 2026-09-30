package v1

import (
	"context"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type requestRateLimiter = middleware.RateLimiter

type publicRequestPolicy struct {
	event          string
	validationCode domain.ErrorCode
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

			if !requestAllowed(policy.limiter, middleware.ClientIPFromRequest(r)) {
				setRetryAfter(w, policy.limiter)
				s.logSecurityEvent(r, policy.event, securityOutcomeRateLimited, nil)
				errmap.HandleError(w, r, domain.ErrRateLimited)
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

	if requestAllowed(policy.limiter, middleware.ClientIPFromRequest(r)) {
		return true
	}

	setRetryAfter(w, policy.limiter)
	s.logSecurityEvent(r, policy.event, securityOutcomeRateLimited, nil)
	errmap.HandleError(w, r, domain.ErrRateLimited)
	return false
}

func requestAllowed(limiter requestRateLimiter, scope string) bool {
	return limiter == nil || limiter.Allow(scope)
}

func setRetryAfter(w http.ResponseWriter, limiter requestRateLimiter) {
	if limiter == nil {
		return
	}
	if retryAfter := limiter.RetryAfter(); retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
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
	case "/api/v1/players/register":
		return s.playerRegisterPolicy(), true
	case "/api/v1/players/login":
		return s.playerLoginPolicy(), true
	case "/api/v1/players/verify-email":
		return s.playerVerifyPolicy(), true
	case "/api/v1/players/resend-verification":
		return s.playerResendPolicy(), true
	default:
		return publicRequestPolicy{}, false
	}
}

func (s *Server) adminLoginPolicy() publicRequestPolicy {
	return publicRequestPolicy{
		event:          "admin.login",
		validationCode: domain.ErrorCodeInvalidCredentials,
		limiter:        s.loginLimiter,
	}
}

func (s *Server) adminRefreshPolicy() publicRequestPolicy {
	return publicRequestPolicy{
		event:          "admin.refresh",
		validationCode: domain.ErrorCodeInvalidCredentials,
		limiter:        s.refreshLimiter,
	}
}

func (s *Server) playerJoinPolicy() publicRequestPolicy {
	return publicRequestPolicy{
		event:          "player.join",
		validationCode: domain.ErrorCodePlayerJoinRetired,
		limiter:        s.joinLimiter,
	}
}

func (s *Server) playerRegisterPolicy() publicRequestPolicy {
	return publicRequestPolicy{event: "player.register", validationCode: domain.ErrorCodeValidation, limiter: s.joinLimiter}
}

func (s *Server) playerLoginPolicy() publicRequestPolicy {
	return publicRequestPolicy{event: "player.login", validationCode: domain.ErrorCodeInvalidCredentials, limiter: s.joinLimiter}
}

func (s *Server) playerVerifyPolicy() publicRequestPolicy {
	return publicRequestPolicy{event: "player.verify_email", validationCode: domain.ErrorCodeVerificationTokenInvalid, limiter: s.joinLimiter}
}

func (s *Server) playerResendPolicy() publicRequestPolicy {
	return publicRequestPolicy{event: "player.resend_verification", validationCode: domain.ErrorCodeValidation, limiter: s.joinLimiter}
}
