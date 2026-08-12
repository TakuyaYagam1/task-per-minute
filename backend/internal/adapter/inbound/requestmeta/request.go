package requestmeta

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"
)

const PlayerSessionCookieName = "tpm_player_session"

func PlayerSessionTokenFromRequest(r *http.Request) (uuid.UUID, bool) {
	if r == nil {
		return uuid.Nil, false
	}
	cookie, err := r.Cookie(PlayerSessionCookieName)
	if err != nil {
		return uuid.Nil, false
	}
	token, err := uuid.Parse(strings.TrimSpace(cookie.Value))
	if err != nil || token == uuid.Nil {
		return uuid.Nil, false
	}
	return token, true
}

func RequestIDFromContext(ctx context.Context) string {
	return httpkitmw.GetRequestID(ctx)
}
