package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

const (
	PlayerSessionCookieName = "tpm_player_session"
)

func PlayerSession(players usecase.PlayerRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := PlayerSessionTokenFromRequest(r)
			if !ok {
				writeUnauthorized(w, r, "missing session token")
				return
			}

			player, err := players.GetBySessionToken(r.Context(), token)
			if err != nil || player == nil {
				writeUnauthorized(w, r, "invalid session token")
				return
			}

			next.ServeHTTP(w, r.WithContext(withPlayer(r.Context(), player)))
		})
	}
}

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

func SetPlayerSessionCookie(w http.ResponseWriter, r *http.Request, token uuid.UUID) {
	http.SetCookie(w, playerSessionCookie(r, token.String(), 0))
}

func ClearPlayerSessionCookie(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec,nolintlint // G124 in newer gosec: helper sets HttpOnly/SameSite; Secure follows trusted TLS/proxy scheme for local HTTP compatibility.
	cookie := playerSessionCookie(r, "", -1)
	cookie.Expires = expiredCookieTime()
	http.SetCookie(w, cookie)
}

func playerSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	//nolint:gosec,nolintlint // G124 in newer gosec: Secure is intentionally request-scheme aware via TLS or trusted X-Forwarded-Proto.
	return &http.Cookie{
		Name:     PlayerSessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	}
}

func isSecureRequest(r *http.Request) bool {
	return requestScheme(r) == "https"
}

func expiredCookieTime() time.Time {
	return time.Unix(0, 0).UTC()
}
