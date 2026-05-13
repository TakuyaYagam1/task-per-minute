package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

const (
	AdminAccessCookieName  = "tpm_admin_access"
	AdminRefreshCookieName = "tpm_admin_refresh"
)

func AdminAccessTokenFromRequest(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	if token, ok := tokenCookieValue(r, AdminAccessCookieName); ok {
		return token, true
	}
	if IsBrowserSourcedRequest(r) {
		return "", false
	}
	return bearerToken(r.Header.Get("Authorization"))
}

func AdminRefreshTokenFromRequest(r *http.Request) (string, bool) {
	return tokenCookieValue(r, AdminRefreshCookieName)
}

func SetAdminSessionCookies(w http.ResponseWriter, r *http.Request, pair *usecase.TokenPair) error {
	if pair == nil {
		return nil
	}
	accessCSRF, err := NewAdminCSRFToken(AdminAccessCSRFCookieName, pair.AccessToken)
	if err != nil {
		return err
	}
	refreshCSRF, err := NewAdminCSRFToken(AdminRefreshCSRFCookieName, pair.RefreshToken)
	if err != nil {
		return err
	}

	http.SetCookie(w, adminSessionCookie(r, AdminAccessCookieName, pair.AccessToken, pair.AccessExpiresAt))
	http.SetCookie(w, adminSessionCookie(r, AdminRefreshCookieName, pair.RefreshToken, pair.RefreshExpiresAt))
	SetAdminCSRFCookie(w, r, AdminAccessCSRFCookieName, accessCSRF, maxAgeUntil(pair.AccessExpiresAt))
	SetAdminCSRFCookie(w, r, AdminRefreshCSRFCookieName, refreshCSRF, maxAgeUntil(pair.RefreshExpiresAt))
	return nil
}

func ClearAdminSessionCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{AdminAccessCookieName, AdminRefreshCookieName} {
		//nolint:gosec // G124: helper sets HttpOnly/SameSite and derives Secure from trusted TLS/proxy scheme for local HTTP compatibility.
		cookie := adminSessionCookie(r, name, "", time.Now().Add(-time.Hour))
		cookie.Expires = expiredCookieTime()
		cookie.MaxAge = -1
		http.SetCookie(w, cookie)
	}
	ClearAdminCSRFCookies(w, r)
}

func tokenCookieValue(r *http.Request, name string) (string, bool) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(cookie.Value)
	if value == "" {
		return "", false
	}
	return value, true
}

func IsBrowserSourcedRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Origin")) != "" ||
		strings.TrimSpace(r.Header.Get("Referer")) != "" ||
		hasFetchMetadata(r)
}

func hasFetchMetadata(r *http.Request) bool {
	for _, name := range []string{"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
		if strings.TrimSpace(r.Header.Get(name)) != "" {
			return true
		}
	}
	return false
}

func adminSessionCookie(r *http.Request, name, value string, expires time.Time) *http.Cookie {
	//nolint:gosec // G124: Secure is intentionally request-scheme aware; production HTTPS is resolved from TLS or trusted X-Forwarded-Proto.
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/api/v1/admin",
		Expires:  expires,
		MaxAge:   maxAgeUntil(expires),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	}
}

func maxAgeUntil(expires time.Time) int {
	if expires.IsZero() {
		return 0
	}
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 0 {
		return -1
	}
	return maxAge
}
