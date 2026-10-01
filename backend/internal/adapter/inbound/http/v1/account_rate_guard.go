package v1

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (s *Server) allowAccountSensitiveOperation(w http.ResponseWriter, r *http.Request, event string) bool {
	return s.allowPlayerOperation(w, r, event, s.accountSensitiveLimiter)
}

func (s *Server) allowAvatarMutation(w http.ResponseWriter, r *http.Request) bool {
	return s.allowPlayerOperation(w, r, "player.avatar.mutation", s.avatarMutationLimiter)
}

func (s *Server) allowPlayerOperation(
	w http.ResponseWriter,
	r *http.Request,
	event string,
	limiter requestRateLimiter,
) bool {
	player, ok := middleware.GetPlayerFromCtx(r.Context())
	if !ok || player == nil || player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return false
	}
	if limiter == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return false
	}
	ip := strings.TrimSpace(middleware.ClientIPFromRequest(r))
	if ip == "" {
		errmap.HandleError(w, r, domain.ErrInternal)
		return false
	}

	scopes := [...]string{
		"player:" + player.ID.String(),
		"session:" + player.SessionToken.String(),
		"ip:" + ip,
	}
	allowed := true
	for _, scope := range scopes {
		if !limiter.Allow(scope) {
			allowed = false
		}
	}
	if allowed {
		return true
	}

	retryAfter := strings.TrimSpace(limiter.RetryAfter())
	if retryAfter == "" {
		retryAfter = "60"
	}
	w.Header().Set("Retry-After", retryAfter)
	s.logSecurityEvent(r, event, securityOutcomeRateLimited, nil)
	errmap.HandleError(w, r, domain.ErrRateLimited)
	return false
}

func (s *Server) acquireAvatarProcessingSlot(w http.ResponseWriter, r *http.Request) bool {
	if s == nil || s.avatarProcessing == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return false
	}
	select {
	case s.avatarProcessing <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		errmap.HandleError(w, r, domain.ErrAvatarBusy)
		return false
	}
}

func (s *Server) releaseAvatarProcessingSlot() {
	if s == nil || s.avatarProcessing == nil {
		return
	}
	<-s.avatarProcessing
}
