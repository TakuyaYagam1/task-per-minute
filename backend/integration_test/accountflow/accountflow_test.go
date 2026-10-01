//go:build integration && account_e2e

package accountflow

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
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
	accountFlowPassword       = "Account-test-password-9284"
)

type problemResponse struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

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

	wrongPasswordResend := postJSON(t, client, server.URL, "/api/v1/players/login/resend-verification", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": "Wrong-test-password-284",
	})
	defer wrongPasswordResend.Body.Close()
	require.Equal(t, http.StatusUnauthorized, wrongPasswordResend.StatusCode)
	require.Empty(t, wrongPasswordResend.Header.Values("Set-Cookie"))
	require.Empty(t, wrongPasswordResend.Header.Get(middleware.CSRFHeaderName))
	var wrongResendProblem problemResponse
	require.NoError(t, json.NewDecoder(wrongPasswordResend.Body).Decode(&wrongResendProblem))
	require.Equal(t, http.StatusUnauthorized, wrongResendProblem.Status)
	require.Equal(t, "admin.invalid_credentials", wrongResendProblem.Code)
	require.Equal(t, "invalid credentials", wrongResendProblem.Detail)
	drainAndClose(t, wrongPasswordResend.Body)
	requireNoVerificationMessage(t, client, server.URL)

	cooldownResend := postJSON(t, client, server.URL, "/api/v1/players/login/resend-verification", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": accountFlowPassword,
	})
	defer cooldownResend.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, cooldownResend.StatusCode)
	require.Equal(t, "60", cooldownResend.Header.Get("Retry-After"))
	require.Empty(t, cooldownResend.Header.Values("Set-Cookie"))
	require.Empty(t, cooldownResend.Header.Get(middleware.CSRFHeaderName))
	var cooldownProblem problemResponse
	require.NoError(t, json.NewDecoder(cooldownResend.Body).Decode(&cooldownProblem))
	require.Equal(t, http.StatusTooManyRequests, cooldownProblem.Status)
	require.Equal(t, "rate_limited", cooldownProblem.Code)
	drainAndClose(t, cooldownResend.Body)
	requireNoVerificationMessage(t, client, server.URL)

	duplicateName := postJSON(t, client, server.URL, "/api/v1/players/register", accountFlowFrontendOrigin, map[string]string{
		"username": username,
		"email":    "other-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@example.invalid",
		"password": accountFlowPassword,
	})
	defer duplicateName.Body.Close()
	require.Equal(t, http.StatusConflict, duplicateName.StatusCode)
	drainAndClose(t, duplicateName.Body)

	pendingWrongPassword := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": "Wrong-test-password-284",
	})
	defer pendingWrongPassword.Body.Close()
	require.Equal(t, http.StatusUnauthorized, pendingWrongPassword.StatusCode)
	require.Empty(t, pendingWrongPassword.Header.Values("Set-Cookie"))
	require.Empty(t, pendingWrongPassword.Header.Get(middleware.CSRFHeaderName))
	var wrongPendingProblem problemResponse
	require.NoError(t, json.NewDecoder(pendingWrongPassword.Body).Decode(&wrongPendingProblem))
	require.Equal(t, http.StatusUnauthorized, wrongPendingProblem.Status)
	require.Equal(t, "admin.invalid_credentials", wrongPendingProblem.Code)
	require.Equal(t, "invalid credentials", wrongPendingProblem.Detail)
	drainAndClose(t, pendingWrongPassword.Body)

	pendingLogin := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": accountFlowPassword,
	})
	defer pendingLogin.Body.Close()
	require.Equal(t, http.StatusUnauthorized, pendingLogin.StatusCode)
	require.Empty(t, pendingLogin.Header.Values("Set-Cookie"))
	require.Empty(t, pendingLogin.Header.Get(middleware.CSRFHeaderName))
	var pendingProblem problemResponse
	require.NoError(t, json.NewDecoder(pendingLogin.Body).Decode(&pendingProblem))
	require.Equal(t, http.StatusUnauthorized, pendingProblem.Status)
	require.Equal(t, "player.email_unverified", pendingProblem.Code)
	require.Equal(t, "activate your account by verifying your email before signing in", pendingProblem.Detail)
	drainAndClose(t, pendingLogin.Body)

	var accountHasPlayer, hasSession bool
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT player_id IS NOT NULL,
			EXISTS (
				SELECT 1
				FROM players
				WHERE players.id = player_accounts.player_id
					AND players.session_token IS NOT NULL
			)
		FROM player_accounts
		WHERE email_normalized = lower($1)`, email).Scan(&accountHasPlayer, &hasSession))
	require.False(t, accountHasPlayer)
	require.False(t, hasSession)

	_, err = fixture.Pool.Exec(ctx, `
		UPDATE player_accounts
		SET verification_sent_at = verification_sent_at - interval '2 minutes'
		WHERE email_normalized = lower($1)`, email)
	require.NoError(t, err)
	successfulResend := postJSON(t, client, server.URL, "/api/v1/players/login/resend-verification", accountFlowFrontendOrigin, map[string]string{
		"login":    username,
		"password": accountFlowPassword,
	})
	defer successfulResend.Body.Close()
	require.Equal(t, http.StatusAccepted, successfulResend.StatusCode)
	require.Empty(t, successfulResend.Header.Values("Set-Cookie"))
	require.Empty(t, successfulResend.Header.Get(middleware.CSRFHeaderName))
	var resendAccepted struct {
		Accepted bool `json:"accepted"`
	}
	require.NoError(t, json.NewDecoder(successfulResend.Body).Decode(&resendAccepted))
	drainAndClose(t, successfulResend.Body)
	require.True(t, resendAccepted.Accepted)

	resendMessageCtx, resendMessageCancel := context.WithTimeout(ctx, 5*time.Second)
	defer resendMessageCancel()
	resendMessage, err := fixture.Mailer.Receive(resendMessageCtx)
	require.NoError(t, err)
	require.Equal(t, email, resendMessage.Recipient)
	resendActivation, err := url.Parse(resendMessage.VerificationURL)
	require.NoError(t, err)
	resendToken, hasResendToken := strings.CutPrefix(resendActivation.Fragment, "token=")
	require.True(t, hasResendToken)
	require.NotEmpty(t, resendToken)

	invalidatedVerification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
		"token": token,
	})
	defer invalidatedVerification.Body.Close()
	require.Equal(t, http.StatusBadRequest, invalidatedVerification.StatusCode)
	drainAndClose(t, invalidatedVerification.Body)

	verification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
		"token": resendToken,
	})
	defer verification.Body.Close()
	require.Equal(t, http.StatusNoContent, verification.StatusCode)
	drainAndClose(t, verification.Body)

	replayedVerification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
		"token": resendToken,
	})
	defer replayedVerification.Body.Close()
	require.Equal(t, http.StatusBadRequest, replayedVerification.StatusCode)
	drainAndClose(t, replayedVerification.Body)

	verifiedResend := postJSON(t, client, server.URL, "/api/v1/players/login/resend-verification", accountFlowFrontendOrigin, map[string]string{
		"login":    username,
		"password": accountFlowPassword,
	})
	defer verifiedResend.Body.Close()
	require.Equal(t, http.StatusConflict, verifiedResend.StatusCode)
	require.Empty(t, verifiedResend.Header.Values("Set-Cookie"))
	require.Empty(t, verifiedResend.Header.Get(middleware.CSRFHeaderName))
	var verifiedResendProblem problemResponse
	require.NoError(t, json.NewDecoder(verifiedResend.Body).Decode(&verifiedResendProblem))
	require.Equal(t, http.StatusConflict, verifiedResendProblem.Status)
	require.Equal(t, "player.email_already_verified", verifiedResendProblem.Code)
	drainAndClose(t, verifiedResend.Body)
	requireNoVerificationMessage(t, client, server.URL)

	wrongPassword := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    username,
		"password": "Wrong-test-password-2874",
	})
	defer wrongPassword.Body.Close()
	require.Equal(t, http.StatusUnauthorized, wrongPassword.StatusCode)
	var wrongPasswordProblem problemResponse
	require.NoError(t, json.NewDecoder(wrongPassword.Body).Decode(&wrongPasswordProblem))
	require.Equal(t, "admin.invalid_credentials", wrongPasswordProblem.Code)
	require.Equal(t, "invalid credentials", wrongPasswordProblem.Detail)
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

func TestAccountSettingsAndAvatarHTTPFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fixture, err := New(ctx, accountFlowFrontendOrigin)
	require.NoError(t, err)
	t.Cleanup(fixture.Close)

	server := httptest.NewServer(fixture.Handler)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 10 * time.Second}
	username := "settings-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	oldEmail := "settings-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@example.invalid"
	newEmail := "changed-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@example.invalid"
	registration := postJSON(t, client, server.URL, "/api/v1/players/register", accountFlowFrontendOrigin, map[string]string{
		"username": username,
		"email":    oldEmail,
		"password": accountFlowPassword,
	})
	require.Equal(t, http.StatusAccepted, registration.StatusCode)
	drainAndClose(t, registration.Body)
	verificationCtx, verificationCancel := context.WithTimeout(ctx, 5*time.Second)
	defer verificationCancel()
	verificationMessage, err := fixture.Mailer.Receive(verificationCtx)
	require.NoError(t, err)
	verification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
		"token": verificationTokenFromURL(t, verificationMessage.VerificationURL),
	})
	require.Equal(t, http.StatusNoContent, verification.StatusCode)
	drainAndClose(t, verification.Body)

	login := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    oldEmail,
		"password": accountFlowPassword,
	})
	require.Equal(t, http.StatusOK, login.StatusCode)
	session, csrfCookie := responseCookies(t, login)
	csrf := login.Header.Get(middleware.CSRFHeaderName)
	drainAndClose(t, login.Body)
	require.NotEmpty(t, csrf)
	require.Equal(t, csrfCookie.Value, csrf)

	settings := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account", session, "")
	require.Equal(t, http.StatusOK, settings.StatusCode)
	require.Equal(t, "private, no-store", settings.Header.Get("Cache-Control"))
	var initialSettings struct {
		Username string `json:"username"`
		Email    string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(settings.Body).Decode(&initialSettings))
	drainAndClose(t, settings.Body)
	require.Equal(t, username, initialSettings.Username)
	require.Equal(t, oldEmail, initialSettings.Email)

	missingAvatar := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account/avatar", session, "")
	require.Equal(t, http.StatusNotFound, missingAvatar.StatusCode)
	drainAndClose(t, missingAvatar.Body)

	pngData := testAvatarPNG(t)
	avatarBody, avatarContentType := avatarMultipart(t, pngData)
	avatarUpload := requestWithSessionBody(t, client, server.URL, http.MethodPut, "/api/v1/players/account/avatar", avatarBody, avatarContentType, session, csrf)
	require.Equal(t, http.StatusNoContent, avatarUpload.StatusCode)
	drainAndClose(t, avatarUpload.Body)

	avatarRead := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account/avatar", session, "")
	require.Equal(t, http.StatusOK, avatarRead.StatusCode)
	require.Equal(t, "image/png", avatarRead.Header.Get("Content-Type"))
	require.Equal(t, "private, no-store", avatarRead.Header.Get("Cache-Control"))
	decodedAvatar, err := png.Decode(avatarRead.Body)
	require.NoError(t, err)
	drainAndClose(t, avatarRead.Body)
	require.Equal(t, color.RGBA{R: 30, G: 90, B: 160, A: 255}, color.RGBAModel.Convert(decodedAvatar.At(0, 0)))

	avatarDelete := requestWithSession(t, client, server.URL, http.MethodDelete, "/api/v1/players/account/avatar", session, csrf)
	require.Equal(t, http.StatusNoContent, avatarDelete.StatusCode)
	drainAndClose(t, avatarDelete.Body)
	deletedAvatar := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account/avatar", session, "")
	require.Equal(t, http.StatusNotFound, deletedAvatar.StatusCode)
	drainAndClose(t, deletedAvatar.Body)

	codeNotReady := getEmailChangeCode(t, client, server.URL, newEmail)
	require.Equal(t, http.StatusNoContent, codeNotReady.StatusCode)
	require.Equal(t, "no-store", codeNotReady.Header.Get("Cache-Control"))
	drainAndClose(t, codeNotReady.Body)

	wrongCurrentPassword := postJSONWithSession(t, client, server.URL, "/api/v1/players/account/email", session, csrf, map[string]string{
		"current_password": "wrong-current-password",
		"new_email":        newEmail,
	})
	require.Equal(t, http.StatusUnprocessableEntity, wrongCurrentPassword.StatusCode)
	var wrongPasswordProblem problemResponse
	require.NoError(t, json.NewDecoder(wrongCurrentPassword.Body).Decode(&wrongPasswordProblem))
	drainAndClose(t, wrongCurrentPassword.Body)
	require.Equal(t, "player.current_password_invalid", wrongPasswordProblem.Code)

	beginEmailChange := postJSONWithSession(t, client, server.URL, "/api/v1/players/account/email", session, csrf, map[string]string{
		"current_password": accountFlowPassword,
		"new_email":        newEmail,
	})
	require.Equal(t, http.StatusAccepted, beginEmailChange.StatusCode)
	require.Equal(t, "private, no-store", beginEmailChange.Header.Get("Cache-Control"))
	var pendingSettings struct {
		Email        string  `json:"email"`
		PendingEmail *string `json:"pending_email"`
	}
	require.NoError(t, json.NewDecoder(beginEmailChange.Body).Decode(&pendingSettings))
	drainAndClose(t, beginEmailChange.Body)
	require.Equal(t, oldEmail, pendingSettings.Email)
	require.NotNil(t, pendingSettings.PendingEmail)
	require.Equal(t, newEmail, *pendingSettings.PendingEmail)

	capturedCode := getEmailChangeCode(t, client, server.URL, newEmail)
	require.Equal(t, http.StatusOK, capturedCode.StatusCode)
	require.Equal(t, "no-store", capturedCode.Header.Get("Cache-Control"))
	var codeResponse map[string]string
	require.NoError(t, json.NewDecoder(capturedCode.Body).Decode(&codeResponse))
	drainAndClose(t, capturedCode.Body)
	require.Len(t, codeResponse, 1)
	code := codeResponse["code"]
	require.Regexp(t, `^[0-9]{6}$`, code)

	wrongCodeValue := "000000"
	if code == wrongCodeValue {
		wrongCodeValue = "000001"
	}
	wrongCode := postJSONWithSession(t, client, server.URL, "/api/v1/players/account/email/confirm", session, csrf, map[string]string{
		"code": wrongCodeValue,
	})
	require.Equal(t, http.StatusUnprocessableEntity, wrongCode.StatusCode)
	var wrongCodeProblem problemResponse
	require.NoError(t, json.NewDecoder(wrongCode.Body).Decode(&wrongCodeProblem))
	drainAndClose(t, wrongCode.Body)
	require.Equal(t, "player.email_change_code_invalid", wrongCodeProblem.Code)

	confirmEmailChange := postJSONWithSession(t, client, server.URL, "/api/v1/players/account/email/confirm", session, csrf, map[string]string{
		"code": code,
	})
	require.Equal(t, http.StatusOK, confirmEmailChange.StatusCode)
	var confirmed struct {
		Email                 string `json:"email"`
		PreviousEmailNotified bool   `json:"previous_email_notified"`
	}
	require.NoError(t, json.NewDecoder(confirmEmailChange.Body).Decode(&confirmed))
	newSession, newCSRFCookie := responseCookies(t, confirmEmailChange)
	newCSRF := confirmEmailChange.Header.Get(middleware.CSRFHeaderName)
	drainAndClose(t, confirmEmailChange.Body)
	require.Equal(t, newEmail, confirmed.Email)
	require.True(t, confirmed.PreviousEmailNotified)
	require.NotEqual(t, session.Value, newSession.Value)
	require.Equal(t, newCSRFCookie.Value, newCSRF)
	require.Len(t, fixture.Mailer.EmailChangedNotices(), 1)
	require.Equal(t, CapturedEmailChangeNotice{PreviousEmail: oldEmail, NewEmail: newEmail}, fixture.Mailer.EmailChangedNotices()[0])

	oldEmailSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account", session, "")
	require.Equal(t, http.StatusUnauthorized, oldEmailSession.StatusCode)
	drainAndClose(t, oldEmailSession.Body)
	updatedSettings := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account", newSession, "")
	require.Equal(t, http.StatusOK, updatedSettings.StatusCode)
	var currentSettings struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(updatedSettings.Body).Decode(&currentSettings))
	drainAndClose(t, updatedSettings.Body)
	require.Equal(t, newEmail, currentSettings.Email)

	newPassword := "Account-next-password-5938!"
	changePassword := postJSONWithSession(t, client, server.URL, "/api/v1/players/account/password", newSession, newCSRF, map[string]string{
		"current_password": accountFlowPassword,
		"new_password":     newPassword,
	})
	require.Equal(t, http.StatusNoContent, changePassword.StatusCode)
	passwordSession, passwordCSRFCookie := responseCookies(t, changePassword)
	passwordCSRF := changePassword.Header.Get(middleware.CSRFHeaderName)
	drainAndClose(t, changePassword.Body)
	require.NotEqual(t, newSession.Value, passwordSession.Value)
	require.Equal(t, passwordCSRFCookie.Value, passwordCSRF)
	prePasswordSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/account", newSession, "")
	require.Equal(t, http.StatusUnauthorized, prePasswordSession.StatusCode)
	drainAndClose(t, prePasswordSession.Body)

	oldPasswordLogin := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    newEmail,
		"password": accountFlowPassword,
	})
	require.Equal(t, http.StatusUnauthorized, oldPasswordLogin.StatusCode)
	drainAndClose(t, oldPasswordLogin.Body)
	newPasswordLogin := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    newEmail,
		"password": newPassword,
	})
	require.Equal(t, http.StatusOK, newPasswordLogin.StatusCode)
	drainAndClose(t, newPasswordLogin.Body)
}

func TestAdminDeletePlayerRevokesSessionAndReleasesIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fixture, err := New(ctx, accountFlowFrontendOrigin)
	require.NoError(t, err)
	t.Cleanup(fixture.Close)

	server := httptest.NewServer(fixture.Handler)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 10 * time.Second}
	username := "delete-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	email := "delete-" + strings.ReplaceAll(uuid.NewString(), "-", "") + "@example.invalid"
	registerAndVerify := func() {
		t.Helper()
		registration := postJSON(t, client, server.URL, "/api/v1/players/register", accountFlowFrontendOrigin, map[string]string{
			"username": username,
			"email":    email,
			"password": accountFlowPassword,
		})
		require.Equal(t, http.StatusAccepted, registration.StatusCode)
		drainAndClose(t, registration.Body)

		messageCtx, messageCancel := context.WithTimeout(ctx, 5*time.Second)
		defer messageCancel()
		message, receiveErr := fixture.Mailer.Receive(messageCtx)
		require.NoError(t, receiveErr)
		require.Equal(t, email, message.Recipient)
		token := verificationTokenFromURL(t, message.VerificationURL)
		verification := postJSON(t, client, server.URL, "/api/v1/players/verify-email", accountFlowFrontendOrigin, map[string]string{
			"token": token,
		})
		require.Equal(t, http.StatusNoContent, verification.StatusCode)
		drainAndClose(t, verification.Body)
	}
	registerAndVerify()

	login := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    email,
		"password": accountFlowPassword,
	})
	require.Equal(t, http.StatusOK, login.StatusCode)
	playerSession, _ := responseCookies(t, login)
	var initialLogin struct {
		PlayerID uuid.UUID `json:"player_id"`
	}
	require.NoError(t, json.NewDecoder(login.Body).Decode(&initialLogin))
	drainAndClose(t, login.Body)
	require.NotEqual(t, uuid.Nil, initialLogin.PlayerID)

	adminLogin := postJSON(t, client, server.URL, "/api/v1/admin/login", accountFlowFrontendOrigin, map[string]string{
		"password": accountFlowAdminPassword,
	})
	require.Equal(t, http.StatusOK, adminLogin.StatusCode)
	adminAccess, adminCSRF := adminResponseCookies(t, adminLogin)
	drainAndClose(t, adminLogin.Body)

	_, err = fixture.Pool.Exec(ctx, `
		INSERT INTO player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
		VALUES ($1, 1, 100)`, initialLogin.PlayerID)
	require.NoError(t, err)
	var tournamentID uuid.UUID
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		INSERT INTO tournaments DEFAULT VALUES
		RETURNING id`).Scan(&tournamentID))
	_, err = fixture.Pool.Exec(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id)
		VALUES ($1, $2)`, initialLogin.PlayerID, tournamentID)
	require.NoError(t, err)

	blockedDelete := deletePlayerAsAdmin(t, client, server.URL, initialLogin.PlayerID, adminAccess, adminCSRF)
	if blockedDelete.StatusCode != http.StatusConflict {
		body, readErr := io.ReadAll(blockedDelete.Body)
		require.NoError(t, readErr)
		require.NoError(t, blockedDelete.Body.Close())
		t.Fatalf("admin player delete returned %d: %s", blockedDelete.StatusCode, strings.TrimSpace(string(body)))
	}
	var blockedProblem problemResponse
	require.NoError(t, json.NewDecoder(blockedDelete.Body).Decode(&blockedProblem))
	drainAndClose(t, blockedDelete.Body)
	require.Equal(t, http.StatusConflict, blockedProblem.Status)
	require.Equal(t, "conflict", blockedProblem.Code)

	var accountCount, usernameReservationCount, overrideCount, tombstoneCount int
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM player_accounts
		WHERE player_id = $1 AND username_normalized = lower($2) AND email_normalized = lower($3)`,
		initialLogin.PlayerID, username, email).Scan(&accountCount))
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*) FROM player_username_reservations WHERE normalized_username = lower($1)`, username).Scan(&usernameReservationCount))
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*) FROM player_leaderboard_overrides WHERE player_id = $1`, initialLogin.PlayerID).Scan(&overrideCount))
	require.NoError(t, fixture.Pool.QueryRow(ctx, `SELECT count(*) FROM player_session_tombstones`).Scan(&tombstoneCount))
	require.Equal(t, 1, accountCount)
	require.Equal(t, 1, usernameReservationCount)
	require.Equal(t, 1, overrideCount)
	require.Zero(t, tombstoneCount)
	retainedSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", playerSession, "")
	require.Equal(t, http.StatusOK, retainedSession.StatusCode)
	drainAndClose(t, retainedSession.Body)

	_, err = fixture.Pool.Exec(ctx, `DELETE FROM participant_reservations WHERE player_id = $1`, initialLogin.PlayerID)
	require.NoError(t, err)
	deleted := deletePlayerAsAdmin(t, client, server.URL, initialLogin.PlayerID, adminAccess, adminCSRF)
	require.Equal(t, http.StatusNoContent, deleted.StatusCode)
	drainAndClose(t, deleted.Body)

	deletedSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", playerSession, "")
	require.Equal(t, http.StatusUnauthorized, deletedSession.StatusCode)
	var deletedProblem problemResponse
	require.NoError(t, json.NewDecoder(deletedSession.Body).Decode(&deletedProblem))
	clearedPlayerCookies := make(map[string]bool)
	for _, cookie := range deletedSession.Cookies() {
		if cookie.Name == middleware.PlayerSessionCookieName || cookie.Name == middleware.PlayerCSRFCookieName {
			require.Equal(t, -1, cookie.MaxAge)
			clearedPlayerCookies[cookie.Name] = true
		}
	}
	drainAndClose(t, deletedSession.Body)
	require.Equal(t, http.StatusUnauthorized, deletedProblem.Status)
	require.Equal(t, "player.account_deleted", deletedProblem.Code)
	require.True(t, clearedPlayerCookies[middleware.PlayerSessionCookieName])
	require.True(t, clearedPlayerCookies[middleware.PlayerCSRFCookieName])

	var softDeleted, sessionCleared bool
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT deleted_at IS NOT NULL, session_token IS NULL
		FROM players WHERE id = $1`, initialLogin.PlayerID).Scan(&softDeleted, &sessionCleared))
	require.True(t, softDeleted)
	require.True(t, sessionCleared)
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*) FROM player_accounts
		WHERE player_id = $1 OR username_normalized = lower($2) OR email_normalized = lower($3)`,
		initialLogin.PlayerID, username, email).Scan(&accountCount))
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*) FROM player_username_reservations WHERE normalized_username = lower($1)`, username).Scan(&usernameReservationCount))
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*) FROM player_leaderboard_overrides WHERE player_id = $1`, initialLogin.PlayerID).Scan(&overrideCount))
	require.NoError(t, fixture.Pool.QueryRow(ctx, `SELECT count(*) FROM player_session_tombstones`).Scan(&tombstoneCount))
	var auditCount int
	require.NoError(t, fixture.Pool.QueryRow(ctx, `
		SELECT count(*) FROM admin_player_audit_events WHERE player_id = $1 AND action = 'delete'`, initialLogin.PlayerID).Scan(&auditCount))
	require.Zero(t, accountCount)
	require.Zero(t, usernameReservationCount)
	require.Zero(t, overrideCount)
	require.Equal(t, 1, auditCount)
	require.Equal(t, 1, tombstoneCount)

	registerAndVerify()
	replacementLogin := postJSON(t, client, server.URL, "/api/v1/players/login", accountFlowFrontendOrigin, map[string]string{
		"login":    username,
		"password": accountFlowPassword,
	})
	require.Equal(t, http.StatusOK, replacementLogin.StatusCode)
	newSession, _ := responseCookies(t, replacementLogin)
	var replacement struct {
		PlayerID uuid.UUID `json:"player_id"`
	}
	require.NoError(t, json.NewDecoder(replacementLogin.Body).Decode(&replacement))
	drainAndClose(t, replacementLogin.Body)
	require.NotEqual(t, uuid.Nil, replacement.PlayerID)
	require.NotEqual(t, initialLogin.PlayerID, replacement.PlayerID)

	oldSessionAfterRegistration := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", playerSession, "")
	require.Equal(t, http.StatusUnauthorized, oldSessionAfterRegistration.StatusCode)
	var oldSessionProblem problemResponse
	require.NoError(t, json.NewDecoder(oldSessionAfterRegistration.Body).Decode(&oldSessionProblem))
	drainAndClose(t, oldSessionAfterRegistration.Body)
	require.Equal(t, "player.account_deleted", oldSessionProblem.Code)
	_, err = fixture.Pool.Exec(ctx, `
		UPDATE player_session_tombstones
		SET expires_at = now() - interval '1 second'`)
	require.NoError(t, err)
	expiredDeletedSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", playerSession, "")
	require.Equal(t, http.StatusUnauthorized, expiredDeletedSession.StatusCode)
	var expiredDeletedProblem problemResponse
	require.NoError(t, json.NewDecoder(expiredDeletedSession.Body).Decode(&expiredDeletedProblem))
	drainAndClose(t, expiredDeletedSession.Body)
	require.NotEqual(t, "player.account_deleted", expiredDeletedProblem.Code)
	unknownSession := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me",
		&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: uuid.NewString()}, "")
	require.Equal(t, http.StatusUnauthorized, unknownSession.StatusCode)
	var unknownProblem problemResponse
	require.NoError(t, json.NewDecoder(unknownSession.Body).Decode(&unknownProblem))
	drainAndClose(t, unknownSession.Body)
	require.NotEqual(t, "player.account_deleted", unknownProblem.Code)
	newSessionResponse := requestWithSession(t, client, server.URL, http.MethodGet, "/api/v1/players/me", newSession, "")
	require.Equal(t, http.StatusOK, newSessionResponse.StatusCode)
	var current struct {
		Player struct {
			ID       uuid.UUID `json:"id"`
			Username string    `json:"username"`
		} `json:"player"`
	}
	require.NoError(t, json.NewDecoder(newSessionResponse.Body).Decode(&current))
	drainAndClose(t, newSessionResponse.Body)
	require.Equal(t, replacement.PlayerID, current.Player.ID)
	require.Equal(t, username, current.Player.Username)
}

func verificationTokenFromURL(t *testing.T, value string) string {
	t.Helper()
	activation, err := url.Parse(value)
	require.NoError(t, err)
	token, ok := strings.CutPrefix(activation.Fragment, "token=")
	require.True(t, ok)
	require.NotEmpty(t, token)
	return token
}

func adminResponseCookies(t *testing.T, response *http.Response) (*http.Cookie, *http.Cookie) {
	t.Helper()
	var access, csrf *http.Cookie
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case middleware.AdminAccessCookieName:
			access = cookie
		case middleware.AdminAccessCSRFCookieName:
			if cookie.Value != "" && cookie.MaxAge >= 0 {
				csrf = cookie
			}
		}
	}
	require.NotNil(t, access)
	require.NotNil(t, csrf)
	require.NotEmpty(t, csrf.Value)
	return access, csrf
}

func deletePlayerAsAdmin(
	t *testing.T,
	client *http.Client,
	baseURL string,
	playerID uuid.UUID,
	access, csrf *http.Cookie,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodDelete,
		baseURL+"/api/v1/admin/players/"+playerID.String(), http.NoBody)
	require.NoError(t, err)
	request.Header.Set("Origin", accountFlowFrontendOrigin)
	request.Header.Set(middleware.CSRFHeaderName, csrf.Value)
	request.AddCookie(access)
	request.AddCookie(csrf)
	response, err := client.Do(request)
	require.NoError(t, err)
	return response
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

func postJSONWithSession(
	t *testing.T,
	client *http.Client,
	baseURL, path string,
	session *http.Cookie,
	csrf string,
	body any,
) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return requestWithSessionBody(t, client, baseURL, http.MethodPost, path, encoded, "application/json", session, csrf)
}

func requestWithSessionBody(
	t *testing.T,
	client *http.Client,
	baseURL, method, path string,
	body []byte,
	contentType string,
	session *http.Cookie,
	csrf string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", contentType)
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

func avatarMultipart(t *testing.T, imageBytes []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="avatar.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(imageBytes)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func testAvatarPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.SetRGBA(0, 0, color.RGBA{R: 30, G: 90, B: 160, A: 255})
	require.NoError(t, png.Encode(&data, imageData))
	return data.Bytes()
}

func getEmailChangeCode(t *testing.T, client *http.Client, baseURL, email string) *http.Response {
	t.Helper()
	response, err := client.Get(baseURL + "/__test/email-change-code?email=" + url.QueryEscape(email))
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

func requireNoVerificationMessage(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	response, err := client.Get(baseURL + "/__test/verification-link")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	drainAndClose(t, response.Body)
}
