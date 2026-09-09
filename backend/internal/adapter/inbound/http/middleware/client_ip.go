package middleware

import (
	"net"
	"net/http"

	"github.com/wahrwelt-kit/go-httpkit/httputil"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"
)

func NewClientIPResolver(trustedProxyCIDRs []string) (func(*http.Request) string, error) {
	trustedNets, err := httputil.ParseTrustedProxyCIDRs(trustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	return func(r *http.Request) string {
		if r == nil {
			return ""
		}
		if ip := httpkitmw.GetClientIPFromContext(r.Context()); ip != "" {
			return ip
		}
		return httputil.GetClientIPWithNets(r, trustedNets)
	}, nil
}

func ClientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := httpkitmw.GetClientIPFromContext(r.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
