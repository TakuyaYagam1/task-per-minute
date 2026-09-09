//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func newRESTFixture(t *testing.T) *restFixture {
	t.Helper()

	f := newDatabaseFixture()
	st := newSeaweedStorage(t)
	clock := realIntegrationClock()
	redis := sharedRedis(t)
	revocations := redisadapter.NewRevocationRedis(
		redis.client,
		"integration:rest:revocation:"+uniq("fixture")+":",
	)
	auth := authusecase.NewUseCase(authusecase.Config{
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 7 * 24 * time.Hour,
	}, clock, revocations, authadapter.NewJWTCodec(authadapter.JWTConfig{
		Secret: []byte("01234567890123456789012345678901"),
		Now:    clock.Now,
	}), authadapter.NewPasswordVerifier([]byte(restAdminPassword)))

	leaderboardUC := leaderboardusecase.NewCache(leaderboardusecase.NewRanking(f.board), clock)
	server := restv1.New(restv1.Dependencies{
		Players:      playerusecase.SessionNewUseCase(f.mgr, f.players, clock),
		AdminAuth:    auth,
		Tasks:        taskusecase.NewUseCase(f.tasks),
		AdminPlayers: playerusecase.ManagementNewUseCase(f.mgr, f.players, leaderboardUC, clock),
		Upload:       taskusecase.NewSourceFiles(taskusecase.NewUseCase(f.tasks), st, nil),
		Leaderboard:  leaderboardUC,
		LeaderboardLimiter: redisadapter.NewRateLimiter(
			redis.client,
			"integration-rest-leaderboard-"+uniq("limiter"),
			100,
			time.Minute,
		),
		Health: restv1.HealthChecks{
			DB: restv1.HealthCheckerFunc(func(ctx context.Context) error {
				return postgres.HealthCheck(ctx, sharedPool)
			}),
			Redis: restv1.HealthCheckerFunc(func(_ context.Context) error {
				return nil
			}),
			SeaweedFS: restv1.HealthCheckerFunc(func(ctx context.Context) error {
				return st.EnsureBucket(ctx)
			}),
			SchemaVersion: postgres.NewSchemaVersionPostgres(sharedPool),
			Tournament: observability.TournamentHealthSourceFunc(
				func(context.Context) observability.TournamentHealthSnapshot {
					return observability.HealthyTournamentHealthSnapshot()
				},
			),
		},
	})

	handler := restv1.NewHandler(server, restv1.HandlerOptions{
		AdminAuth:  auth,
		PlayerRepo: f.players,
		Middlewares: []api.MiddlewareFunc{
			middleware.Build(logkit.Noop()),
		},
	})

	return &restFixture{
		databaseFixture: f,
		handler:         handler,
		auth:            auth,
		validator:       newOpenAPIResponseValidator(t),
	}
}

func newOpenAPIResponseValidator(t *testing.T) routers.Router {
	t.Helper()
	spec, err := api.GetSwagger()
	require.NoError(t, err)
	spec.Servers = openapi3.Servers{}
	require.NoError(t, spec.Validate(context.Background()))
	router, err := legacy.NewRouter(spec)
	require.NoError(t, err)
	return router
}

func (f *restFixture) doJSON(t *testing.T, method, path, body, authHeader string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	contentType := ""
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
		contentType = "application/json"
	}
	return f.do(t, method, path, reader, contentType, authHeader)
}

func (f *restFixture) do(t *testing.T, method, path string, body io.Reader, contentType, authHeader string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authHeader != "" {
		switch {
		case strings.HasPrefix(authHeader, "AdminSession "):
			addAdminSession(t, req, strings.TrimPrefix(authHeader, "AdminSession "))
		case strings.HasPrefix(authHeader, "AdminRefreshSession "):
			addAdminRefreshSession(t, req, strings.TrimPrefix(authHeader, "AdminRefreshSession "))
		case strings.HasPrefix(authHeader, "Cookie "):
			req.Header.Set("Cookie", strings.TrimPrefix(authHeader, "Cookie "))
		}
	}
	resp := httptest.NewRecorder()
	f.handler.ServeHTTP(resp, req)
	return req, resp
}

func (f *restFixture) validateResponse(t *testing.T, req *http.Request, resp *httptest.ResponseRecorder) {
	t.Helper()
	route, pathParams, err := f.validator.FindRoute(req)
	require.NoError(t, err)
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
		},
		Status:  resp.Code,
		Header:  resp.Result().Header,
		Options: &openapi3filter.Options{},
	}
	input.SetBodyBytes(resp.Body.Bytes())
	require.NoError(t, openapi3filter.ValidateResponse(context.Background(), input))
}

func (f *restFixture) adminAccessToken(t *testing.T) string {
	t.Helper()
	pair, err := f.auth.Login(context.Background(), restAdminPassword)
	require.NoError(t, err)
	return pair.AccessToken
}

func (f *restFixture) joinPlayerViaUsecase(t *testing.T, username string) *domain.Player {
	t.Helper()
	uc := playerusecase.SessionNewUseCase(f.mgr, f.players, realIntegrationClock())
	player, err := uc.Join(context.Background(), username)
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)
	return player
}

func adminPlayerIDs(players []api.PlayerManagementView) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(players))
	for _, player := range players {
		out = append(out, player.Id)
	}
	return out
}

func adminPlayerByID(players []api.PlayerManagementView, id uuid.UUID) api.PlayerManagementView {
	for _, player := range players {
		if player.Id == id {
			return player
		}
	}
	return api.PlayerManagementView{}
}

func leaderboardUsernames(rows []leaderboardusecase.PlayerStats) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Username)
	}
	return out
}

func decodeJSON[T any](t *testing.T, resp *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	return out
}

func adminSession(token string) string {
	return "AdminSession " + token
}

func adminRefreshSession(token string) string {
	return "AdminRefreshSession " + token
}

func addAdminSession(t *testing.T, req *http.Request, accessToken string) {
	t.Helper()

	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, accessToken)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: accessToken})
	req.AddCookie(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: csrfToken})
	req.Header.Set(middleware.CSRFHeaderName, csrfToken)
}

func addAdminRefreshSession(t *testing.T, req *http.Request, refreshToken string) {
	t.Helper()

	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminRefreshCSRFCookieName, refreshToken)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCookieName, Value: refreshToken})
	req.AddCookie(&http.Cookie{Name: middleware.AdminRefreshCSRFCookieName, Value: csrfToken})
	req.Header.Set(middleware.CSRFHeaderName, csrfToken)
}

func session(token uuid.UUID) string {
	return cookieSession(token.String())
}

func cookieSession(token string) string {
	return "Cookie " + (&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token}).String()
}

func playerSessionCookieValue(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == middleware.PlayerSessionCookieName {
			return cookie.Value
		}
	}
	t.Fatalf("missing %s cookie", middleware.PlayerSessionCookieName)
	return ""
}

func responseCookieValue(t *testing.T, resp *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	t.Fatalf("missing %s cookie", name)
	return ""
}

func multipartBody(t *testing.T, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(filePartHeader(writer.Boundary()))
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return &body, writer.FormDataContentType()
}

func filePartHeader(_ string) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="source.zip"`},
		"Content-Type":        {"application/zip"},
	}
}

func largeMultipartBody(fileSize int64) (io.Reader, string, int64) {
	boundary := "rest-large-upload-boundary"
	prefix := fmt.Sprintf(
		"--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"source.zip\"\r\nContent-Type: application/zip\r\n\r\n",
		boundary,
	)
	suffix := fmt.Sprintf("\r\n--%s--\r\n", boundary)
	file := io.MultiReader(bytes.NewReader([]byte{'P', 'K', 0x03, 0x04}), io.LimitReader(zeroReader{}, fileSize-4))
	body := io.MultiReader(strings.NewReader(prefix), file, strings.NewReader(suffix))
	return body, "multipart/form-data; boundary=" + boundary, int64(len(prefix)) + fileSize + int64(len(suffix))
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
