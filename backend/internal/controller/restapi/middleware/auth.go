package middleware

import (
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

// Auth applies the generated OpenAPI security scopes to the matching auth middleware.
func Auth(auth *admin.AuthUsecase, players usecase.PlayerRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Value(openapi.BearerAuthScopes).([]string); ok {
				if auth == nil {
					writeUnauthorized(w, r, "missing admin auth dependency")
					return
				}
				AdminJWT(auth)(next).ServeHTTP(w, r)
				return
			}

			if _, ok := r.Context().Value(openapi.SessionTokenAuthScopes).([]string); ok {
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
