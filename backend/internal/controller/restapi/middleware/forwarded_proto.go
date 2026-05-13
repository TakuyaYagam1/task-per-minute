package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/wahrwelt-kit/go-httpkit/httputil"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

type forwardedProtoContextKey struct{}

func ForwardedProto(trustedProxyCIDRs []string, log logkit.Logger) func(http.Handler) http.Handler {
	trustedNets, err := httputil.ParseTrustedProxyCIDRs(trustedProxyCIDRs)
	if err != nil && log != nil {
		log.Warn("invalid trusted proxy CIDRs for forwarded proto", logkit.Fields{"error": err.Error()})
	}
	if len(trustedNets) == 0 {
		trustedNets = nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if proto, ok := trustedForwardedProto(r, trustedNets); ok {
				r = r.WithContext(context.WithValue(r.Context(), forwardedProtoContextKey{}, proto))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requestScheme(r *http.Request) string {
	if r != nil {
		if r.TLS != nil {
			return "https"
		}
		if proto, ok := forwardedProtoFromContext(r.Context()); ok {
			return proto
		}
	}
	return "http"
}

func forwardedProtoFromContext(ctx context.Context) (string, bool) {
	proto, ok := ctx.Value(forwardedProtoContextKey{}).(string)
	if !ok {
		return "", false
	}
	return proto, proto == "http" || proto == "https"
}

func trustedForwardedProto(r *http.Request, trustedNets []*net.IPNet) (string, bool) {
	if r == nil || len(trustedNets) == 0 || !remoteAddrInNets(r.RemoteAddr, trustedNets) {
		return "", false
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	switch proto {
	case "http", "https":
		return proto, true
	default:
		return "", false
	}
}

func remoteAddrInNets(remoteAddr string, nets []*net.IPNet) bool {
	ipStr := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		ipStr = host
	}
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	for _, network := range nets {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
