package middleware

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

// Auth applies the generated OpenAPI security scopes to the matching auth middleware.
func Auth(auth AdminAccessVerifier, players PlayerSessionReader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Value(api.BearerAuthScopes).([]string); ok {
				if auth == nil {
					writeUnauthorized(w, r, "missing admin auth dependency")
					return
				}
				AdminJWT(auth)(next).ServeHTTP(w, r)
				return
			}

			if _, ok := r.Context().Value(api.SessionTokenAuthScopes).([]string); ok {
				if players == nil {
					writeUnauthorized(w, r, "missing player auth dependency")
					return
				}
				PlayerSession(players)(next).ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
