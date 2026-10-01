package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestPublicPlayerAvatarRouteIsPublicAndPrivateAvatarRouteStillRequiresSession(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	version := strings.Repeat("a", 64)
	imageBytes := []byte("\x89PNG\r\n\x1a\npublic-avatar")
	avatars := &publicPlayerAvatarStub{content: inbound.PlayerAvatarContent{
		ContentType: "image/png",
		Data:        imageBytes,
	}}
	server := New(Dependencies{
		PublicPlayerAvatars:     avatars,
		PublicAvatarReadLimiter: newAllowingRateLimiter(t),
	})
	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)
	handler := NewHandler(server, HandlerOptions{
		Router:           chi.NewRouter(),
		PlayerRepo:       &playerSettingsSessionReader{},
		RequestValidator: validator,
	})

	publicRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/players/"+playerID.String()+"/avatar?v="+version, nil)
	publicResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicResponse, publicRequest)

	require.Equal(t, http.StatusOK, publicResponse.Code, publicResponse.Body.String())
	require.Equal(t, "image/png", publicResponse.Header().Get("Content-Type"))
	require.Equal(t, "no-store", publicResponse.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", publicResponse.Header().Get("X-Content-Type-Options"))
	require.Equal(t, imageBytes, publicResponse.Body.Bytes())
	require.Equal(t, playerID, avatars.playerID)
	require.Equal(t, version, avatars.version)

	privateRequest := httptest.NewRequest(http.MethodGet, "/api/v1/players/account/avatar", nil)
	privateResponse := httptest.NewRecorder()
	handler.ServeHTTP(privateResponse, privateRequest)
	require.Equal(t, http.StatusUnauthorized, privateResponse.Code)
	require.Equal(t, 1, avatars.calls, "the private route must not dispatch through the public service")
}

func TestPublicPlayerAvatarRouteRejectsInvalidVersionAndBoundsConcurrentReads(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	avatars := &publicPlayerAvatarStub{content: inbound.PlayerAvatarContent{
		ContentType: "image/gif",
		Data:        []byte("GIF89a"),
	}}
	server := New(Dependencies{
		PublicPlayerAvatars:     avatars,
		PublicAvatarReadLimiter: newAllowingRateLimiter(t),
	})
	invalid := httptest.NewRecorder()
	server.GetPublicPlayerAvatar(invalid, httptest.NewRequest(http.MethodGet, "/", nil), playerID, api.GetPublicPlayerAvatarParams{V: "ABC"})
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Equal(t, "no-store", invalid.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", invalid.Header().Get("X-Content-Type-Options"))
	require.Zero(t, avatars.calls)

	for range cap(server.publicAvatarReads) {
		server.publicAvatarReads <- struct{}{}
	}
	version := strings.Repeat("b", 64)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.GetPublicPlayerAvatar(response, request, playerID, api.GetPublicPlayerAvatarParams{V: version})
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.Equal(t, "1", response.Header().Get("Retry-After"))
	require.Zero(t, avatars.calls)
}

func TestPublicPlayerAvatarRouteUsesPerIPRateLimiter(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	avatars := &publicPlayerAvatarStub{content: inbound.PlayerAvatarContent{
		ContentType: "image/gif",
		Data:        []byte("GIF89a"),
	}}
	server := New(Dependencies{
		PublicPlayerAvatars:     avatars,
		PublicAvatarReadLimiter: newOneRequestRateLimiter(t, "30"),
	})
	version := strings.Repeat("c", 64)
	params := api.GetPublicPlayerAvatarParams{V: version}

	first := httptest.NewRecorder()
	server.GetPublicPlayerAvatar(first, httptest.NewRequest(http.MethodGet, "/", nil), playerID, params)
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	server.GetPublicPlayerAvatar(second, httptest.NewRequest(http.MethodGet, "/", nil), playerID, params)
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "30", second.Header().Get("Retry-After"))
	require.Equal(t, 1, avatars.calls)
}

type publicPlayerAvatarStub struct {
	content  inbound.PlayerAvatarContent
	err      error
	calls    int
	playerID uuid.UUID
	version  string
}

func (stub *publicPlayerAvatarStub) GetPublicAvatar(_ context.Context, playerID uuid.UUID, version string) (inbound.PlayerAvatarContent, error) {
	stub.calls++
	stub.playerID = playerID
	stub.version = version
	if stub.err != nil {
		return inbound.PlayerAvatarContent{}, stub.err
	}
	return stub.content, nil
}

var _ inbound.PublicPlayerAvatarService = (*publicPlayerAvatarStub)(nil)
