//go:build integration && account_e2e

package accountflow

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	accountFlowFrontendOrigin = "http://127.0.0.1:3101"
	accountFlowPassword       = "synthetic-test-password-123"
)

func TestAccountHTTPFlowIssuesVerifiedCookieSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fixture, err := New(ctx, accountFlowFrontendOrigin)
	require.NoError(t, err)
	t.Cleanup(fixture.Close)

	server := httptest.NewServer(fixture.Handler)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 10 * time.Second}
	username := "int-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	email := "flow-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@example.invalid"
	registrationBody := map[string]string{
		"username": username,
		"email":    email,
		"password": accountFlowPassword,
	}

	hostileOrigin := postJSON(t, client, server.URL, "/api/v1/players/register", "https://example.org", registrationBody)
	defer hostileOrigin.Body.Close()
	require.Equal(t, http.StatusForbidden, hostileOrigin.StatusCode)
	drainAndClose(t, hostileOrigin.Body)

	legacyJoin := postJSON(t, client, server.URL, "/api/v1/players/join", accountFlowFrontendOrigin, map[string]string{"username": username})
	defer legacyJoin.Body.Close()
	require.Equal(t, http.StatusGone, legacyJoin.StatusCode)
	drainAndClose(t, legacyJoin.Body)

	registration := postJSON(t, client, server.URL, "/api/v1/players/register", accountFlowFrontendOrigin, registrationBody)
	defer registration.Body.Close()
	require.Equal(t, http.StatusAccepted, registration.StatusCode)
	var accepted struct {
		Accepted bool `json:"accepted"`
	}
	require.NoError(t, json.NewDecoder(registration.Body).Decode(&accepted))
	drainAndClose(t, registration.Body)
	require.True(t, accepted.Accepted)

	messageCtx, messageCancel := context.WithTimeout(ctx, 5*time.Second)
	defer messageCancel()
	message, err := fixture.Mailer.Receive(messageCtx)
	require.NoError(t, err)
	require.Equal(t, email, message.Recipient)
	activation, err := url.Parse(message.VerificationURL)
	require.NoError(t, err)
	require.Equal(t, accountFlowFrontendOrigin+"/verify-email", activation.Scheme+"://"+activation.Host+activation.Path)
	require.Empty(t, activation.RawQuery)
	token, hasToken := strings.CutPrefix(activation.Fragment, "token=")
	require.True(t, hasToken)
	require.NotEmpty(t, token)

	duplicateName := postJSON(t, client, server.URL, "/api/v1/players/register", accountFlowFrontendOrigin, map[string]string{
		"username": username,
		"email":    "other-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@example.invalid",
		"password": accountFlowPassword,
	})
	defer duplicateName.Body.Close()
	require.Equal(t, http.StatusConflict, duplicateName.StatusCode)
	drainAndClose(t, duplicateName.Body)

	pendingLogin := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": accountFlowPassword,
	})
	defer pendingLogin.Body.Close()
	require.Equal(t, http.StatusUnauthorized, pendingLogin.StatusCode)
	drainAndClose(t, pendingLogin.Body)

	verification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
		"token": token,
	})
	defer verification.Body.Close()
	require.Equal(t, http.StatusNoContent, verification.StatusCode)
	drainAndClose(t, verification.Body)

	replayedVerification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
		"token": token,
	})
	defer replayedVerification.Body.Close()
	require.Equal(t, http.StatusBadRequest, replayedVerification.StatusCode)
	drainAndClose(t, replayedVerification.Body)

	wrongPassword := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    username,
		"password": "synthetic-wrong-password-123",
	})
	defer wrongPassword.Body.Close()
	require.Equal(t, http.StatusUnauthorized, wrongPassword.StatusCode)
	drainAndClose(t, wrongPassword.Body)

	login := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": accountFlowPassword,
	})
	defer login.Body.Close()
	require.Equal(t, http.StatusOK, login.StatusCode)
	playerSession, csrfCookie := responseCookies(t, login)
	csrfHeader := login.Header.Get(middleware.CSRFHeaderName)
	drainAndClose(t, login.Body)
	require.Equal(t, middleware.PlayerSessionCookieName, playerSession.Name)
	require.True(t, playerSession.HttpOnly)
	require.Equal(t, "/", playerSession.Path)
	require.Equal(t, middleware.PlayerCSRFCookieName, csrfCookie.Name)
	require.False(t, csrfCookie.HttpOnly)
	require.NotEmpty(t, csrfHeader)
	require.Equal(t, csrfCookie.Value, csrfHeader)

	me := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", playerSession, "")
	defer me.Body.Close()
	require.Equal(t, http.StatusOK, me.StatusCode)
	var current struct {
		Player struct {
			Username string `json:"username"`
		} `json:"player"`
	}
	require.NoError(t, json.NewDecoder(me.Body).Decode(&current))
	drainAndClose(t, me.Body)
	require.Equal(t, username, current.Player.Username)

	missingCSRF := requestWithSession(t, client, server.URL, http.MethodPost, "/api/v1/players/logout", playerSession, "")
	defer missingCSRF.Body.Close()
	require.Equal(t, http.StatusForbidden, missingCSRF.StatusCode)
	drainAndClose(t, missingCSRF.Body)

	logout := requestWithSession(t, client, server.URL, http.MethodPost, "/api/v1/players/logout", playerSession, csrfHeader)
	defer logout.Body.Close()
	require.Equal(t, http.StatusNoContent, logout.StatusCode)
	drainAndClose(t, logout.Body)

	staleSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", playerSession, "")
	defer staleSession.Body.Close()
	require.Equal(t, http.StatusUnauthorized, staleSession.StatusCode)
	drainAndClose(t, staleSession.Body)
}

func postJSON(
	t *testing.T,
	client *http.Client,
	baseURL, path, origin string,
	body any,
) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+path, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	require.NoError(t, err)
	return response
}

func responseCookies(t *testing.T, response *http.Response) (*http.Cookie, *http.Cookie) {
	t.Helper()
	var session, csrf *http.Cookie
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case middleware.PlayerSessionCookieName:
			session = cookie
		case middleware.PlayerCSRFCookieName:
			csrf = cookie
		}
	}
	require.NotNil(t, session)
	require.NotNil(t, csrf)
	return session, csrf
}

func requestWithSession(
	t *testing.T,
	client *http.Client,
	baseURL string,
	method string,
	path string,
	session *http.Cookie,
	csrf string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, http.NoBody)
	require.NoError(t, err)
	request.Header.Set("Origin", accountFlowFrontendOrigin)
	request.AddCookie(session)
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrf})
		request.Header.Set(middleware.CSRFHeaderName, csrf)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	return response
}

func drainAndClose(t *testing.T, body io.ReadCloser) {
	t.Helper()
	_, err := io.Copy(io.Discard, body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
}
