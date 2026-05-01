package middleware

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

const playerSessionHeader = "X-Session-Token"

func PlayerSession(players usecase.PlayerRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawToken := strings.TrimSpace(r.Header.Get(playerSessionHeader))
			if rawToken == "" {
				writeUnauthorized(w, r, "missing session token")
				return
			}

			token, err := uuid.Parse(rawToken)
			if err != nil {
				writeUnauthorized(w, r, "invalid session token")
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
