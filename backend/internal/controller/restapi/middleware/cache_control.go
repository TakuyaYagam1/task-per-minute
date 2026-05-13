package middleware

import (
	"net/http"
	"strings"
)

const (
	noStoreCacheControl = "no-store"
	noCachePragma       = "no-cache"
	expiredHTTPDate     = "0"
)

// NoStoreSensitiveResponses prevents auth/session/admin/duel responses from
// being persisted by browsers or intermediary caches. Public cacheable
// endpoints, such as the leaderboard, are intentionally left untouched.
func NoStoreSensitiveResponses() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSensitiveResponsePath(r) {
				header := w.Header()
				header.Set("Cache-Control", noStoreCacheControl)
				header.Set("Pragma", noCachePragma)
				header.Set("Expires", expiredHTTPDate)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSensitiveResponsePath(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	path := r.URL.Path
	return strings.HasPrefix(path, "/api/v1/admin/") ||
		strings.HasPrefix(path, "/api/v1/players/") ||
		strings.HasPrefix(path, "/api/v1/duels/")
}
