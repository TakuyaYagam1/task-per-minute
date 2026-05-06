//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/app"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
)

type e2eApp struct {
	baseURL string
	client  *http.Client
	errCh   <-chan error
	cancel  context.CancelFunc
	cleanup func()
}

func startE2EApp(t *testing.T) *e2eApp {
	t.Helper()

	TruncateTables(t, sharedPool)
	clearE2ERedis(t)

	port := reservePort(t)
	setAppEnv(t, port)

	ctx, cancel := context.WithCancel(context.Background())
	cfg, err := config.Load()
	require.NoError(t, err)
	application, cleanup, err := app.Initialize(ctx, cfg, logkit.Noop())
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	waitForHealth(t, port, errCh)

	app := &e2eApp{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client:  &http.Client{Timeout: 10 * time.Second},
		errCh:   errCh,
		cancel:  cancel,
		cleanup: cleanup,
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			require.NoError(t, application.Shutdown(context.Background()))
		}
		cleanup()
		clearE2ERedis(t)
		TruncateTables(t, sharedPool)
	})
	return app
}

func clearE2ERedis(t *testing.T) {
	t.Helper()
	redis := sharedRedis(t).client
	err := redis.Del(
		context.Background(),
		redisrepo.DefaultLeaderboardKey,
		redisrepo.DefaultMatchmakingQueueKey,
	).Err()
	require.NoError(t, err)
}

type e2eJoin struct {
	PlayerID     uuid.UUID
	SessionToken uuid.UUID
	Username     string
}

func (a *e2eApp) joinPlayer(t *testing.T, username string) e2eJoin {
	t.Helper()
	got := e2ePostJSON(t, a, "/api/v1/players/join", openapi.JoinRequest{Username: username}, "", http.StatusOK, openapi.JoinResponse{})
	return e2eJoin{
		PlayerID:     got.PlayerId,
		SessionToken: got.SessionToken,
		Username:     username,
	}
}

func (a *e2eApp) adminLogin(t *testing.T) string {
	t.Helper()
	password := restAdminPassword
	got := e2ePostJSON(
		t,
		a,
		"/api/v1/admin/login",
		openapi.AdminLoginRequest{Password: &password},
		"",
		http.StatusOK,
		openapi.AdminTokenResponse{},
	)
	require.NotEmpty(t, got.AccessToken)
	require.NotEmpty(t, got.RefreshToken)
	return got.AccessToken
}

func (a *e2eApp) createAdminTask(t *testing.T, title, flag string) openapi.TaskResponse {
	t.Helper()
	token := a.adminLogin(t)
	return a.createTask(t, token, openapi.CreateTaskRequest{
		Title:       title,
		Description: "created by e2e setup",
		Category:    openapi.Web,
		Difficulty:  openapi.Easy,
		TimeLimit:   90,
		Flag:        flag,
		Hints:       defaultTaskHints(title),
	})
}

func (a *e2eApp) createTask(t *testing.T, token string, body openapi.CreateTaskRequest) openapi.TaskResponse {
	t.Helper()
	return e2ePostJSON(t, a, "/api/v1/admin/tasks", body, bearer(token), http.StatusCreated, openapi.TaskResponse{})
}

func (a *e2eApp) uploadTaskSource(
	t *testing.T,
	token string,
	taskID uuid.UUID,
	payload []byte,
) openapi.UploadSourceResponse {
	t.Helper()
	body, contentType := multipartBody(t, payload)
	req := a.newRequest(t, http.MethodPost, "/api/v1/admin/tasks/"+taskID.String()+"/source", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", bearer(token))
	resp := a.do(t, req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return e2eDecodeJSON[openapi.UploadSourceResponse](t, resp.Body)
}

func (a *e2eApp) listTasks(t *testing.T, token string) []openapi.TaskResponse {
	t.Helper()
	req := a.newRequest(t, http.MethodGet, "/api/v1/admin/tasks", nil)
	req.Header.Set("Authorization", bearer(token))
	resp := a.do(t, req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return e2eDecodeJSON[[]openapi.TaskResponse](t, resp.Body)
}

func (a *e2eApp) deleteTask(t *testing.T, token string, taskID uuid.UUID) {
	t.Helper()
	req := a.newRequest(t, http.MethodDelete, "/api/v1/admin/tasks/"+taskID.String(), nil)
	req.Header.Set("Authorization", bearer(token))
	resp := a.do(t, req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func (a *e2eApp) getLeaderboard(t *testing.T) openapi.LeaderboardResponse {
	t.Helper()
	req := a.newRequest(t, http.MethodGet, "/api/v1/leaderboard", nil)
	resp := a.do(t, req)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return e2eDecodeJSON[openapi.LeaderboardResponse](t, resp.Body)
}

func e2ePostJSON[T any](
	t *testing.T,
	app *e2eApp,
	path string,
	body any,
	authHeader string,
	wantStatus int,
	zero T,
) T {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))

	req := app.newRequest(t, http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp := app.do(t, req)
	defer resp.Body.Close()
	require.Equal(t, wantStatus, resp.StatusCode)
	if resp.StatusCode == http.StatusNoContent {
		return zero
	}
	return e2eDecodeJSON[T](t, resp.Body)
}

func (a *e2eApp) connectWS(t *testing.T, token uuid.UUID) *coderws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, wsURL(a.baseURL, token), nil)
	require.NoError(t, err)
	return conn
}

func (a *e2eApp) newRequest(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, a.baseURL+path, body)
	require.NoError(t, err)
	return req
}

func (a *e2eApp) do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := a.client.Do(req)
	require.NoError(t, err)
	return resp
}

func e2eDecodeJSON[T any](t *testing.T, body io.Reader) T {
	t.Helper()
	var out T
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &out), "body: %s", data)
	return out
}

func containsTaskID(tasks []openapi.TaskResponse, id uuid.UUID) bool {
	for _, task := range tasks {
		if task.Id == id {
			return true
		}
	}
	return false
}
