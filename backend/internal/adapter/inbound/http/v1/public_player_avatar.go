package v1

import (
	"encoding/hex"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// GetPublicPlayerAvatar handles GET /api/v1/players/{player_id}/avatar.
func (s *Server) GetPublicPlayerAvatar(
	w http.ResponseWriter,
	r *http.Request,
	playerID openapi_types.UUID,
	params api.GetPublicPlayerAvatarParams,
) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s == nil || s.publicPlayerAvatars == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if !validPublicAvatarVersion(params.V) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	if !requestAllowed(s.publicAvatarReadLimiter, middleware.ClientIPFromRequest(r)) {
		setRetryAfter(w, s.publicAvatarReadLimiter)
		errmap.HandleError(w, r, domain.ErrRateLimited)
		return
	}
	if s.publicAvatarReads == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	select {
	case s.publicAvatarReads <- struct{}{}:
		defer func() { <-s.publicAvatarReads }()
	default:
		w.Header().Set("Retry-After", "1")
		errmap.HandleError(w, r, domain.ErrAvatarBusy)
		return
	}

	content, err := s.publicPlayerAvatars.GetPublicAvatar(r.Context(), playerID, params.V)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if !isPublicAvatarContentType(content.ContentType) || len(content.Data) == 0 {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	w.Header().Set("Content-Type", content.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Data)
}

func validPublicAvatarVersion(version string) bool {
	if len(version) != 64 {
		return false
	}
	digest, err := hex.DecodeString(version)
	return err == nil && len(digest) == 32 && strings.ToLower(version) == version
}

func isPublicAvatarContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "video/mp4":
		return true
	default:
		return false
	}
}
