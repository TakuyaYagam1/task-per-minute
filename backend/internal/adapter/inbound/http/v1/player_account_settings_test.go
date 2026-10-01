package v1

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestPlayerAccountSettingsHandlersRotateSessionOnlyAfterSuccessfulCredentialMutation(t *testing.T) {
	playerID := uuid.New()
	oldToken := uuid.New()
	newToken := uuid.New()
	settings := &accountSettingsStub{
		passwordPlayer: &domain.Player{ID: playerID, Username: "alice", SessionToken: &newToken},
		passwordErr:    domain.ErrCurrentPasswordInvalid,
	}
	server := New(Dependencies{
		AccountSettings:         settings,
		AccountSensitiveLimiter: newAllowingRateLimiter(t),
	})
	handler := newPlayerSettingsHTTPHandler(t, server, playerID, oldToken)

	request := playerSettingsJSONRequest(http.MethodPost, "/api/v1/players/account/password", `{"current_password":"old","new_password":"New-password-123!"}`)
	attachPlayerSessionAndCSRF(t, request, oldToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	require.Empty(t, response.Result().Cookies())
	require.Empty(t, response.Header().Get("X-CSRF-Token"))

	settings.passwordErr = nil
	request = playerSettingsJSONRequest(http.MethodPost, "/api/v1/players/account/password", `{"current_password":"old","new_password":"New-password-123!"}`)
	attachPlayerSessionAndCSRF(t, request, oldToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.NotEmpty(t, response.Header().Get("X-CSRF-Token"))
	var sessionCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == middleware.PlayerSessionCookieName {
			sessionCookie = cookie
		}
		if cookie.Name == middleware.PlayerCSRFCookieName {
			csrfCookie = cookie
		}
	}
	require.NotNil(t, sessionCookie)
	require.Equal(t, newToken.String(), sessionCookie.Value)
	require.NotNil(t, csrfCookie)
	require.Equal(t, response.Header().Get("X-CSRF-Token"), csrfCookie.Value)
}

func TestPlayerAccountSettingsGetReturnsPrivateNoStoreAndCanonicalPendingState(t *testing.T) {
	playerID := uuid.New()
	token := uuid.New()
	pendingEmail := "new@example.test"
	settings := &accountSettingsStub{settings: &domain.AccountSettings{
		Username:               "alice",
		Email:                  "old@example.test",
		PendingEmail:           &pendingEmail,
		EmailResendAvailableAt: nil,
	}}
	server := New(Dependencies{AccountSettings: settings})
	handler := newPlayerSettingsHTTPHandler(t, server, playerID, token)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/players/account", nil)
	request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.NotEmpty(t, response.Header().Get(middleware.CSRFHeaderName))
	require.Contains(t, response.Body.String(), `"email":"old@example.test"`)
	require.Contains(t, response.Body.String(), `"pending_email":"new@example.test"`)
	require.NotContains(t, response.Body.String(), "email_change_code")
}

func TestPlayerAvatarHTTPRouteAcceptsUploadLargerThanGenericOpenAPIBodyLimit(t *testing.T) {
	playerID := uuid.New()
	token := uuid.New()
	avatars := &playerAvatarStub{}
	server := New(Dependencies{
		PlayerAvatars:         avatars,
		AvatarMutationLimiter: newAllowingRateLimiter(t),
	})
	handler := newPlayerSettingsHTTPHandler(t, server, playerID, token)
	const imageBytes = (1 << 20) + 128
	body, contentType := avatarMultipartBody(t, imageBytes)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/players/account/avatar", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	attachPlayerSessionAndCSRF(t, request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.Len(t, avatars.replacedData, imageBytes)
	require.Equal(t, "image/png", avatars.replacedContentType)
}

func TestPlayerAvatarHTTPRouteRejectsUploadOverFiveMiB(t *testing.T) {
	playerID := uuid.New()
	token := uuid.New()
	avatars := &playerAvatarStub{}
	server := New(Dependencies{
		PlayerAvatars:         avatars,
		AvatarMutationLimiter: newAllowingRateLimiter(t),
	})
	handler := newPlayerSettingsHTTPHandler(t, server, playerID, token)
	body, contentType := avatarMultipartBody(t, int(maxAvatarFileBytes)+1)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/players/account/avatar", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	attachPlayerSessionAndCSRF(t, request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code, response.Body.String())
	require.Empty(t, avatars.replacedData)
}

func TestPlayerAvatarHTTPRouteAcceptsMP4AndMapsUnavailableProcessor(t *testing.T) {
	playerID := uuid.New()
	token := uuid.New()
	avatars := &playerAvatarStub{replaceErr: domain.ErrAvatarVideoUnavailable}
	server := New(Dependencies{
		PlayerAvatars:         avatars,
		AvatarMutationLimiter: newAllowingRateLimiter(t),
	})
	handler := newPlayerSettingsHTTPHandler(t, server, playerID, token)
	body, contentType := avatarMultipartBodyWithType(t, 4, "profile.mp4", "video/mp4")
	request := httptest.NewRequest(http.MethodPut, "/api/v1/players/account/avatar", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	attachPlayerSessionAndCSRF(t, request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Equal(t, "video/mp4", avatars.replacedContentType)
	require.Contains(t, response.Body.String(), `"code":"player.avatar_video_unavailable"`)
	require.Contains(t, response.Body.String(), `"title":"Service Unavailable"`)
}

func newPlayerSettingsHTTPHandler(t *testing.T, server *Server, playerID, token uuid.UUID) http.Handler {
	t.Helper()
	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)
	reader := &playerSettingsSessionReader{player: &domain.Player{ID: playerID, SessionToken: &token}}
	return middleware.CSRFGuard()(NewHandler(server, HandlerOptions{
		Router:           chi.NewRouter(),
		PlayerRepo:       reader,
		RequestValidator: validator,
	}))
}

func playerSettingsJSONRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func attachPlayerSessionAndCSRF(t *testing.T, request *http.Request, sessionToken uuid.UUID) {
	t.Helper()
	csrf, err := middleware.NewPlayerCSRFToken(sessionToken)
	require.NoError(t, err)
	request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
	request.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrf})
	request.Header.Set(middleware.CSRFHeaderName, csrf)
}

func avatarMultipartBody(t *testing.T, fileSize int) ([]byte, string) {
	t.Helper()
	return avatarMultipartBodyWithType(t, fileSize, "profile.png", "image/png")
}

func avatarMultipartBodyWithType(t *testing.T, fileSize int, filename, mediaType string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	header["Content-Type"] = []string{mediaType}
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = io.Copy(part, io.LimitReader(strings.NewReader(strings.Repeat("x", fileSize)), int64(fileSize)))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

type playerSettingsSessionReader struct {
	player *domain.Player
}

func (reader *playerSettingsSessionReader) GetBySessionToken(_ context.Context, token uuid.UUID) (*domain.Player, error) {
	if reader.player == nil || reader.player.SessionToken == nil || *reader.player.SessionToken != token {
		return nil, domain.ErrInvalidSession
	}
	return reader.player, nil
}

type accountSettingsStub struct {
	settings       *domain.AccountSettings
	passwordPlayer *domain.Player
	passwordErr    error
}

func (stub *accountSettingsStub) GetAccountSettings(context.Context, uuid.UUID, uuid.UUID) (*domain.AccountSettings, error) {
	return stub.settings, nil
}

func (*accountSettingsStub) ChangeUsername(context.Context, uuid.UUID, uuid.UUID, inbound.ChangeUsernameCommand) (*domain.Player, error) {
	return nil, domain.ErrInternal
}

func (stub *accountSettingsStub) ChangePassword(_ context.Context, _, _ uuid.UUID, command inbound.ChangePasswordCommand) (*domain.Player, error) {
	if command.CurrentPassword != "old" || command.NewPassword != "New-password-123!" {
		return nil, domain.ErrValidation
	}
	return stub.passwordPlayer, stub.passwordErr
}

func (*accountSettingsStub) BeginEmailChange(context.Context, uuid.UUID, uuid.UUID, inbound.BeginEmailChangeCommand) (*domain.AccountSettings, error) {
	return nil, domain.ErrInternal
}

func (*accountSettingsStub) ResendEmailChange(context.Context, uuid.UUID, uuid.UUID) (*domain.AccountSettings, error) {
	return nil, domain.ErrInternal
}

func (*accountSettingsStub) CancelEmailChange(context.Context, uuid.UUID, uuid.UUID) (*domain.AccountSettings, error) {
	return nil, domain.ErrInternal
}

func (*accountSettingsStub) ConfirmEmailChange(context.Context, uuid.UUID, uuid.UUID, inbound.ConfirmEmailChangeCommand) (*domain.EmailChangeResult, error) {
	return nil, domain.ErrInternal
}

type playerAvatarStub struct {
	replacedContentType string
	replacedData        []byte
	replaceErr          error
}

func (*playerAvatarStub) GetAvatar(context.Context, uuid.UUID, uuid.UUID) (inbound.PlayerAvatarContent, error) {
	return inbound.PlayerAvatarContent{}, domain.ErrAvatarNotFound
}

func (stub *playerAvatarStub) ReplaceAvatar(_ context.Context, _, _ uuid.UUID, contentType string, data []byte) error {
	stub.replacedContentType = contentType
	stub.replacedData = append([]byte(nil), data...)
	return stub.replaceErr
}

func (*playerAvatarStub) DeleteAvatar(context.Context, uuid.UUID, uuid.UUID) error { return nil }

var _ inbound.AccountSettingsService = (*accountSettingsStub)(nil)
var _ inbound.PlayerAvatarService = (*playerAvatarStub)(nil)
