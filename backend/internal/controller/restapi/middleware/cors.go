package middleware

import (
	"net/http"
	"strings"
)

const (
	corsAllowedMethods = "GET, POST, PUT, DELETE, OPTIONS"
	corsAllowedHeaders = "Content-Type, Authorization, X-CSRF-Token"
	corsExposedHeaders = "Retry-After, X-CSRF-Token, X-Admin-Refresh-CSRF-Token"
)

// CORS allows REST requests from a configured exact-origin allowlist.
// An empty allowlist keeps the backend in same-origin mode and leaves requests untouched.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if _, ok := allowed[origin]; !ok {
				if isCORSPreflight(r) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			writeCORSHeaders(w.Header(), origin)
			if isCORSPreflight(r) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func writeCORSHeaders(header http.Header, origin string) {
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Allow-Credentials", "true")
	header.Set("Access-Control-Allow-Methods", corsAllowedMethods)
	header.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
	header.Set("Access-Control-Expose-Headers", corsExposedHeaders)
	addVary(header, "Origin")
	addVary(header, "Access-Control-Request-Method")
	addVary(header, "Access-Control-Request-Headers")
}

func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
