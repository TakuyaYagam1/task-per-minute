package middleware

import (
	"net/http"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func AdminJWT(auth *admin.AuthUsecase) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := AdminAccessTokenFromRequest(r)
			if !ok {
				writeUnauthorized(w, r, "missing admin session")
				return
			}

			claims, err := auth.VerifyAccess(r.Context(), token)
			if err != nil {
				writeUnauthorized(w, r, "invalid access token")
				return
			}

			next.ServeHTTP(w, r.WithContext(withAdminClaims(r.Context(), claims)))
		})
	}
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}
