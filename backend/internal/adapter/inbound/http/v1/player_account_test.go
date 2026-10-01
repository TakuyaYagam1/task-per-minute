package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestLoginPlayerReturnsActivationCodeWithoutIssuingCookies(t *testing.T) {
	accounts := &pendingLoginAccountFake{}
	server := New(Dependencies{PlayerAccounts: accounts})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/players/login",
		strings.NewReader(`{"login":"alice","password":"correct legacy password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.LoginPlayer(response, request)

	require.True(t, accounts.loginCalled)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Empty(t, response.Header().Get("Set-Cookie"))
	require.Empty(t, response.Result().Cookies())

	var problem api.ProblemDetails
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.NotNil(t, problem.Code)
	require.Equal(t, string(domain.ErrorCodeEmailUnverified), *problem.Code)
	require.NotNil(t, problem.Detail)
	require.Equal(t, domain.ErrEmailUnverified.Message, *problem.Detail)
}

func TestResendPlayerVerificationForLoginReturnsAcceptedWithoutIssuingCookies(t *testing.T) {
	accounts := &pendingLoginAccountFake{}
	server := New(Dependencies{PlayerAccounts: accounts})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/players/login/resend-verification",
		strings.NewReader(`{"login":"alice","password":"correct legacy password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: "existing-session"})
	response := httptest.NewRecorder()

	server.ResendPlayerVerificationForLogin(response, request)

	require.True(t, accounts.resendCalled)
	require.Equal(t, inbound.LoginPlayerCommand{Login: "alice", Password: "correct legacy password"}, accounts.resendCommand)
	require.Equal(t, http.StatusAccepted, response.Code)
	require.Empty(t, response.Header().Get("Set-Cookie"))
	require.Empty(t, response.Result().Cookies())
	require.JSONEq(t, `{"accepted":true}`, response.Body.String())
}

func TestResendPlayerVerificationForLoginMapsErrorsWithoutIssuingCookies(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       domain.ErrorCode
		retryAfter string
	}{
		{name: "wrong password", err: domain.ErrInvalidCredentials, status: http.StatusUnauthorized, code: domain.ErrorCodeInvalidCredentials},
		{name: "already verified", err: domain.ErrEmailAlreadyVerified, status: http.StatusConflict, code: domain.ErrorCodeEmailAlreadyVerified},
		{name: "cooldown", err: domain.ErrRateLimited, status: http.StatusTooManyRequests, code: domain.ErrRateLimited.Code, retryAfter: "60"},
		{name: "mail failure", err: domain.ErrVerificationUnavailable, status: http.StatusServiceUnavailable, code: domain.ErrVerificationUnavailable.Code},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accounts := &pendingLoginAccountFake{resendErr: test.err}
			server := New(Dependencies{PlayerAccounts: accounts})
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/players/login/resend-verification",
				strings.NewReader(`{"login":"alice","password":"provided password"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			server.ResendPlayerVerificationForLogin(response, request)

			require.True(t, accounts.resendCalled)
			require.Equal(t, test.status, response.Code)
			require.Empty(t, response.Header().Get("Set-Cookie"))
			require.Empty(t, response.Result().Cookies())
			require.Equal(t, test.retryAfter, response.Header().Get("Retry-After"))
			var problem api.ProblemDetails
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
			require.NotNil(t, problem.Code)
			require.Equal(t, string(test.code), *problem.Code)
		})
	}
}

type pendingLoginAccountFake struct {
	loginCalled   bool
	resendCalled  bool
	resendCommand inbound.LoginPlayerCommand
	resendErr     error
}

func (*pendingLoginAccountFake) Register(context.Context, inbound.RegisterPlayerCommand) error {
	return nil
}

func (fake *pendingLoginAccountFake) Login(context.Context, inbound.LoginPlayerCommand) (*domain.Player, error) {
	fake.loginCalled = true
	return nil, domain.ErrEmailUnverified
}

func (*pendingLoginAccountFake) VerifyEmail(context.Context, string) error { return nil }

func (*pendingLoginAccountFake) ResendVerification(context.Context, string) error { return nil }

func (fake *pendingLoginAccountFake) ResendVerificationForLogin(_ context.Context, command inbound.LoginPlayerCommand) error {
	fake.resendCalled = true
	fake.resendCommand = command
	return fake.resendErr
}
