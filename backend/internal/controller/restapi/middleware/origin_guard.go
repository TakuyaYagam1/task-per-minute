package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// OriginGuard blocks browser-driven cross-site unsafe requests before cookie
// authenticated handlers can mutate state. Non-browser clients that do not send
// Origin/Referer are allowed.
func OriginGuard(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, raw := range allowedOrigins {
		if origin, ok := normalizeOrigin(raw); ok {
			allowed[origin] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			origin, hasOrigin, ok := requestSourceOrigin(r)
			if !ok {
				writeProblem(w, r, http.StatusForbidden, http.StatusText(http.StatusForbidden), "origin not allowed")
				return
			}
			if !hasOrigin {
				next.ServeHTTP(w, r)
				return
			}

			if _, ok := allowed[origin]; ok || sameRequestOrigin(r, origin) {
				next.ServeHTTP(w, r)
				return
			}

			writeProblem(w, r, http.StatusForbidden, http.StatusText(http.StatusForbidden), "origin not allowed")
		})
	}
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func requestSourceOrigin(r *http.Request) (string, bool, bool) {
	if r == nil {
		return "", false, true
	}
	if rawOrigin := strings.TrimSpace(r.Header.Get("Origin")); rawOrigin != "" {
		origin, ok := normalizeOrigin(rawOrigin)
		return origin, true, ok
	}
	if rawReferer := strings.TrimSpace(r.Header.Get("Referer")); rawReferer != "" {
		origin, ok := originFromURL(rawReferer)
		return origin, true, ok
	}
	return "", false, true
}

func sameRequestOrigin(r *http.Request, origin string) bool {
	requestOrigin, ok := requestOrigin(r)
	return ok && requestOrigin == origin
}

func requestOrigin(r *http.Request) (string, bool) {
	if r == nil || strings.TrimSpace(r.Host) == "" {
		return "", false
	}
	return requestScheme(r) + "://" + canonicalHost(r.Host), true
}

func originFromURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return parsed.Scheme + "://" + canonicalHost(parsed.Host), isAllowedOriginScheme(parsed.Scheme)
}

func normalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if !isAllowedOriginScheme(parsed.Scheme) {
		return "", false
	}
	return parsed.Scheme + "://" + canonicalHost(parsed.Host), true
}

func isAllowedOriginScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

func canonicalHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if h, p, err := net.SplitHostPort(host); err == nil {
		return net.JoinHostPort(strings.TrimSuffix(h, "."), p)
	}
	return strings.TrimSuffix(host, ".")
}
