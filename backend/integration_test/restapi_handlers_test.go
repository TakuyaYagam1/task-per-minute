//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/inmem"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
	pgclient "github.com/TakuyaYagam1/task-per-minute/pkg/postgres"
	redisclient "github.com/TakuyaYagam1/task-per-minute/pkg/redis"
)

const restAdminPassword = "admin-password"

type restFixture struct {
	*duelFixture
	handler   http.Handler
	auth      *adminusecase.AuthUsecase
	redis     *goredis.Client
	validator routers.Router
}

func TestRESTHandlers_OpenAPIResponseShapes(t *testing.T) {
	f := newRESTFixture(t)
	ctx := context.Background()

	loginReq, loginResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/login", `{"password":"`+restAdminPassword+`"}`, "")
	require.Equal(t, http.StatusOK, loginResp.Code)
	f.validateResponse(t, loginReq, loginResp)
	adminToken := decodeJSON[openapi.AdminTokenResponse](t, loginResp).AccessToken

	refreshReq, refreshResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/refresh",
		`{"refresh_token":"`+decodeJSON[openapi.AdminTokenResponse](t, loginResp).RefreshToken+`"}`, "")
	require.Equal(t, http.StatusOK, refreshResp.Code)
	f.validateResponse(t, refreshReq, refreshResp)
	adminToken = decodeJSON[openapi.AdminTokenResponse](t, refreshResp).AccessToken

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", `{
		"title":"`+uniq("rest_task")+`",
		"description":"created through REST",
		"category":"forensics",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{rest}",
		"hints":["first hint","second hint","third hint"]
	}`, bearer(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	createdTask := decodeJSON[openapi.TaskResponse](t, createResp)
	require.Equal(t, []string{"first hint", "second hint", "third hint"}, createdTask.Hints)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "list tasks", method: http.MethodGet, path: "/api/v1/admin/tasks", want: http.StatusOK},
		{name: "get task", method: http.MethodGet, path: "/api/v1/admin/tasks/" + createdTask.Id.String(), want: http.StatusOK},
		{name: "update task", method: http.MethodPut, path: "/api/v1/admin/tasks/" + createdTask.Id.String(), body: `{"title":"updated ` + uniq("task") + `"}`, want: http.StatusOK},
	} {
		req, resp := f.doJSON(t, tc.method, tc.path, tc.body, bearer(adminToken))
		require.Equal(t, tc.want, resp.Code, tc.name)
		f.validateResponse(t, req, resp)
	}

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'})
	uploadReq, uploadResp := f.do(t, http.MethodPost, "/api/v1/admin/tasks/"+createdTask.Id.String()+"/source", zipBody, contentType, bearer(adminToken))
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)

	aliceReq, aliceResp := f.doJSON(t, http.MethodPost, "/api/v1/players/join", `{"username":"`+uniq("alice")+`"}`, "")
	require.Equal(t, http.StatusOK, aliceResp.Code)
	f.validateResponse(t, aliceReq, aliceResp)
	aliceJoin := decodeJSON[openapi.JoinResponse](t, aliceResp)

	bobReq, bobResp := f.doJSON(t, http.MethodPost, "/api/v1/players/join", `{"username":"`+uniq("bob")+`"}`, "")
	require.Equal(t, http.StatusOK, bobResp.Code)
	f.validateResponse(t, bobReq, bobResp)
	bobJoin := decodeJSON[openapi.JoinResponse](t, bobResp)

	duelID := f.createRESTDuel(t, aliceJoin.PlayerId, bobJoin.PlayerId)
	require.NoError(t, redisrepo.NewLeaderboardRedis(f.redis, "leaderboard:rest:"+uniq("z")).IncrementWin(ctx, "alice_rest"))

	meReq, meResp := f.doJSON(t, http.MethodGet, "/api/v1/players/me", "", session(aliceJoin.SessionToken))
	require.Equal(t, http.StatusOK, meResp.Code)
	f.validateResponse(t, meReq, meResp)

	duelReq, duelResp := f.doJSON(t, http.MethodGet, "/api/v1/duels/"+duelID.String(), "", session(aliceJoin.SessionToken))
	require.Equal(t, http.StatusOK, duelResp.Code)
	f.validateResponse(t, duelReq, duelResp)

	boardReq, boardResp := f.doJSON(t, http.MethodGet, "/api/v1/leaderboard", "", "")
	require.Equal(t, http.StatusOK, boardResp.Code)
	f.validateResponse(t, boardReq, boardResp)

	healthReq, healthResp := f.doJSON(t, http.MethodGet, "/health", "", "")
	require.Equal(t, http.StatusOK, healthResp.Code)
	f.validateResponse(t, healthReq, healthResp)
	require.Greater(t, decodeJSON[openapi.HealthResponse](t, healthResp).SchemaVersion, int64(0))

	deleteReq, deleteResp := f.doJSON(t, http.MethodDelete, "/api/v1/admin/tasks/"+createdTask.Id.String(), "", bearer(adminToken))
	require.Equal(t, http.StatusNoContent, deleteResp.Code)
	f.validateResponse(t, deleteReq, deleteResp)
}

func TestRESTHandlers_DeleteMissingTaskReturns404(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	taskID := uuid.New()

	req, resp := f.doJSON(t, http.MethodDelete, "/api/v1/admin/tasks/"+taskID.String(), "", bearer(adminToken))
	require.Equal(t, http.StatusNotFound, resp.Code)
	f.validateResponse(t, req, resp)
}

func TestRESTHandlers_DeleteReferencedTaskReturns409(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	ctx := context.Background()

	task := f.makeTask(t, uniq("referenced"), domain.DifficultyEasy)
	alice := f.joinPlayerViaUsecase(t, uniq("alice"))
	bob := f.joinPlayerViaUsecase(t, uniq("bob"))
	duel, err := f.duels.Create(ctx, alice.ID, bob.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, task.ID))
	_, err = f.duels.Finish(ctx, duel.ID, nil, time.Now().UTC(), domain.DuelStatusFinished)
	require.NoError(t, err)

	req, resp := f.doJSON(t, http.MethodDelete, "/api/v1/admin/tasks/"+task.ID.String(), "", bearer(adminToken))
	require.Equal(t, http.StatusConflict, resp.Code)
	f.validateResponse(t, req, resp)
}

func TestRESTHandlers_UpdateTaskURLPreserveSetAndClear(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	initialURL := "https://tasks.example.com/" + uniq("task")

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", fmt.Sprintf(`{
		"title":%q,
		"description":"task url update semantics",
		"category":"web",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{task_url}",
		"hints":["first hint","second hint","third hint"],
		"task_url":%q
	}`, uniq("task_url"), initialURL), bearer(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[openapi.TaskResponse](t, createResp)
	require.NotNil(t, created.TaskUrl)
	require.Equal(t, initialURL, *created.TaskUrl)

	preserveReq, preserveResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"title":"preserve task url"}`,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, preserveResp.Code)
	f.validateResponse(t, preserveReq, preserveResp)
	preserved := decodeJSON[openapi.TaskResponse](t, preserveResp)
	require.NotNil(t, preserved.TaskUrl)
	require.Equal(t, initialURL, *preserved.TaskUrl)

	clearReq, clearResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"task_url":null}`,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, clearResp.Code)
	f.validateResponse(t, clearReq, clearResp)
	cleared := decodeJSON[openapi.TaskResponse](t, clearResp)
	require.Nil(t, cleared.TaskUrl)

	nextURL := "pwn.example.com:31337"
	setReq, setResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		fmt.Sprintf(`{"task_url":%q}`, nextURL),
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, setResp.Code)
	f.validateResponse(t, setReq, setResp)
	updated := decodeJSON[openapi.TaskResponse](t, setResp)
	require.NotNil(t, updated.TaskUrl)
	require.Equal(t, nextURL, *updated.TaskUrl)
}

func TestRESTHandlers_TaskURLAllowedForForensics(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	initialURL := "https://tasks.example.com/" + uniq("task")

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", fmt.Sprintf(`{
		"title":%q,
		"description":"forensics can keep task url",
		"category":"forensics",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{forensics_url}",
		"hints":["first hint","second hint","third hint"],
		"task_url":%q
	}`, uniq("forensics_url"), initialURL), bearer(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	forensics := decodeJSON[openapi.TaskResponse](t, createResp)
	require.Equal(t, openapi.Forensics, forensics.Category)
	require.NotNil(t, forensics.TaskUrl)
	require.Equal(t, initialURL, *forensics.TaskUrl)

	webCreateReq, webCreateResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", fmt.Sprintf(`{
		"title":%q,
		"description":"web task with url",
		"category":"web",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{web_url}",
		"hints":["first hint","second hint","third hint"],
		"task_url":%q
	}`, uniq("web_url"), initialURL), bearer(adminToken))
	require.Equal(t, http.StatusCreated, webCreateResp.Code)
	f.validateResponse(t, webCreateReq, webCreateResp)
	created := decodeJSON[openapi.TaskResponse](t, webCreateResp)
	require.NotNil(t, created.TaskUrl)

	updateReq, updateResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"category":"forensics"}`,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, updateResp.Code)
	f.validateResponse(t, updateReq, updateResp)
	updated := decodeJSON[openapi.TaskResponse](t, updateResp)
	require.Equal(t, openapi.Forensics, updated.Category)
	require.NotNil(t, updated.TaskUrl)
	require.Equal(t, initialURL, *updated.TaskUrl)
}

func TestRESTHandlers_UpdateTaskSourceFileURLClear(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", fmt.Sprintf(`{
		"title":%q,
		"description":"source url clear semantics",
		"category":"forensics",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{source_url}",
		"hints":["first hint","second hint","third hint"]
	}`, uniq("source_url")), bearer(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[openapi.TaskResponse](t, createResp)

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'})
	uploadReq, uploadResp := f.do(
		t,
		http.MethodPost,
		"/api/v1/admin/tasks/"+created.Id.String()+"/source",
		zipBody,
		contentType,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)
	uploaded := decodeJSON[openapi.UploadSourceResponse](t, uploadResp)
	beforeClear := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer beforeClear.Body.Close()
	require.Equal(t, http.StatusOK, beforeClear.StatusCode)

	preserveReq, preserveResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"title":"preserve source url"}`,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, preserveResp.Code)
	f.validateResponse(t, preserveReq, preserveResp)
	preserved := decodeJSON[openapi.TaskResponse](t, preserveResp)
	require.NotNil(t, preserved.SourceFileUrl)

	clearReq, clearResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"source_file_url":null}`,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, clearResp.Code)
	f.validateResponse(t, clearReq, clearResp)
	cleared := decodeJSON[openapi.TaskResponse](t, clearResp)
	require.Nil(t, cleared.SourceFileUrl)

	afterClear := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer afterClear.Body.Close()
	require.Equal(t, http.StatusNotFound, afterClear.StatusCode)
}

func TestRESTHandlers_UpdateForensicsTaskToWebPreservesSource(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", fmt.Sprintf(`{
		"title":%q,
		"description":"source category clear semantics",
		"category":"forensics",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{source_category}",
		"hints":["first hint","second hint","third hint"]
	}`, uniq("source_category")), bearer(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[openapi.TaskResponse](t, createResp)

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'c', 'a', 't'})
	uploadReq, uploadResp := f.do(
		t,
		http.MethodPost,
		"/api/v1/admin/tasks/"+created.Id.String()+"/source",
		zipBody,
		contentType,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)
	uploaded := decodeJSON[openapi.UploadSourceResponse](t, uploadResp)

	updateReq, updateResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"category":"web","task_url":"https://tasks.example/web"}`,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, updateResp.Code)
	f.validateResponse(t, updateReq, updateResp)
	updated := decodeJSON[openapi.TaskResponse](t, updateResp)
	require.Equal(t, openapi.Web, updated.Category)
	require.NotNil(t, updated.TaskUrl)
	require.NotNil(t, updated.SourceFileUrl)

	afterUpdate := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer afterUpdate.Body.Close()
	require.Equal(t, http.StatusOK, afterUpdate.StatusCode)
}

func TestRESTHandlers_UpdateTaskSourceFileURLRejectsInvalidString(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	task := f.makeTask(t, uniq("source_url_invalid"), domain.DifficultyEasy)

	for _, body := range []string{
		`{"source_file_url":"not-a-url"}`,
		`{"source_file_url":"https://files.example/source.zip"}`,
	} {
		req, resp := f.doJSON(
			t,
			http.MethodPut,
			"/api/v1/admin/tasks/"+task.ID.String(),
			body,
			bearer(adminToken),
		)

		require.Equal(t, http.StatusBadRequest, resp.Code)
		f.validateResponse(t, req, resp)
	}
}

func TestRESTHandlers_DeleteTaskWithSourceDeletesStoredObject(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", fmt.Sprintf(`{
		"title":%q,
		"description":"source cleanup on delete",
		"category":"forensics",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{source_delete}",
		"hints":["first hint","second hint","third hint"]
	}`, uniq("source_delete")), bearer(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[openapi.TaskResponse](t, createResp)

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'd', 'e', 'l'})
	uploadReq, uploadResp := f.do(
		t,
		http.MethodPost,
		"/api/v1/admin/tasks/"+created.Id.String()+"/source",
		zipBody,
		contentType,
		bearer(adminToken),
	)
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)
	uploaded := decodeJSON[openapi.UploadSourceResponse](t, uploadResp)
	beforeDelete := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer beforeDelete.Body.Close()
	require.Equal(t, http.StatusOK, beforeDelete.StatusCode)

	deleteReq, deleteResp := f.doJSON(
		t,
		http.MethodDelete,
		"/api/v1/admin/tasks/"+created.Id.String(),
		"",
		bearer(adminToken),
	)
	require.Equal(t, http.StatusNoContent, deleteResp.Code)
	f.validateResponse(t, deleteReq, deleteResp)

	afterDelete := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer afterDelete.Body.Close()
	require.Equal(t, http.StatusNotFound, afterDelete.StatusCode)
}

func TestRESTHandlers_GetDuelWithForeignSessionReturns403(t *testing.T) {
	f := newRESTFixture(t)

	alice := f.joinPlayerViaUsecase(t, uniq("alice"))
	bob := f.joinPlayerViaUsecase(t, uniq("bob"))
	stranger := f.joinPlayerViaUsecase(t, uniq("charlie"))
	duelID := f.createRESTDuel(t, alice.ID, bob.ID)

	req, resp := f.doJSON(t, http.MethodGet, "/api/v1/duels/"+duelID.String(), "", session(*stranger.SessionToken))

	require.Equal(t, http.StatusForbidden, resp.Code)
	f.validateResponse(t, req, resp)
}

func TestRESTHandlers_CORSPreflightAllowedOrigin(t *testing.T) {
	f := newRESTFixture(t)
	handler := middleware.CORS([]string{"http://localhost:3000"})(f.handler)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/players/join", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization, X-Session-Token")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	require.Equal(t, http.StatusNoContent, resp.Code)
	require.Equal(t, "http://localhost:3000", resp.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Headers"), "X-Session-Token")
}

func TestRESTHandlers_UploadSourceOverLimitReturns413Or400(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	task := f.makeTask(t, uniq("oversize"), domain.DifficultyEasy)
	body, contentType, contentLength := largeMultipartBody(adminusecase.MaxSourceFileSize + 1<<20)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks/"+task.ID.String()+"/source", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", bearer(adminToken))
	req.ContentLength = contentLength
	resp := httptest.NewRecorder()

	f.handler.ServeHTTP(resp, req)

	require.Contains(t, []int{http.StatusRequestEntityTooLarge, http.StatusBadRequest}, resp.Code)
	f.validateResponse(t, req, resp)
}

func TestRESTHandlers_UploadSourceForWebReturns200(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	task := f.makeTask(t, uniq("web_source"), domain.DifficultyEasy)
	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'w', 'e', 'b'})

	req, resp := f.do(
		t,
		http.MethodPost,
		"/api/v1/admin/tasks/"+task.ID.String()+"/source",
		zipBody,
		contentType,
		bearer(adminToken),
	)

	require.Equal(t, http.StatusOK, resp.Code)
	f.validateResponse(t, req, resp)
	uploaded := decodeJSON[openapi.UploadSourceResponse](t, resp)
	require.Contains(t, uploaded.SourceFileUrl, "X-Amz-Signature")
}

func newRESTFixture(t *testing.T) *restFixture {
	t.Helper()

	f := newDuelFixture()
	redisClient := sharedRedis(t).client
	st := newSeaweedStorage(t)
	auth := adminusecase.NewAuthUsecase(adminusecase.AuthConfig{
		Secret:        []byte("01234567890123456789012345678901"),
		AccessTTL:     15 * time.Minute,
		RefreshTTL:    7 * 24 * time.Hour,
		AdminPassword: []byte(restAdminPassword),
	}, clock.Real{}, inmem.NewRevocation(clock.Real{}))

	board := redisrepo.NewLeaderboardRedis(redisClient, "leaderboard:rest:"+uniq("z"))
	server := restv1.New(restv1.Dependencies{
		Players:     playerusecase.NewPlayerUsecase(f.mgr, f.players, f.duels),
		AdminAuth:   auth,
		Tasks:       adminusecase.NewTaskUsecase(f.tasks),
		Upload:      adminusecase.NewUploadUsecase(f.tasks, st),
		Leaderboard: leaderboardusecase.NewLeaderboardUsecase(board, f.board, clock.Real{}),
		Duels:       duelusecase.NewReadUsecase(f.duels),
		Health: restv1.HealthChecks{
			DB: usecase.HealthCheckerFunc(func(ctx context.Context) error {
				return pgclient.HealthCheck(ctx, sharedPool)
			}),
			Redis: usecase.HealthCheckerFunc(func(ctx context.Context) error {
				return redisclient.HealthCheck(ctx, redisClient)
			}),
			SeaweedFS: usecase.HealthCheckerFunc(func(ctx context.Context) error {
				return st.EnsureBucket(ctx)
			}),
			SchemaVersion: persistent.NewSchemaVersionPostgres(sharedPool),
		},
	})

	handler := restv1.NewHandler(server, restv1.HandlerOptions{
		AdminAuth:  auth,
		PlayerRepo: f.players,
		Middlewares: []openapi.MiddlewareFunc{
			middleware.Build(logkit.Noop()),
		},
	})

	return &restFixture{
		duelFixture: f,
		handler:     handler,
		auth:        auth,
		redis:       redisClient,
		validator:   newOpenAPIResponseValidator(t),
	}
}

func newOpenAPIResponseValidator(t *testing.T) routers.Router {
	t.Helper()
	spec, err := openapi.GetSwagger()
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
		if strings.HasPrefix(authHeader, "Bearer ") {
			req.Header.Set("Authorization", authHeader)
		} else {
			req.Header.Set("X-Session-Token", authHeader)
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
	uc := playerusecase.NewPlayerUsecase(f.mgr, f.players, f.duels)
	player, err := uc.Join(context.Background(), username)
	require.NoError(t, err)
	require.NotNil(t, player.SessionToken)
	return player
}

func (f *restFixture) createRESTDuel(t *testing.T, player1ID, player2ID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	task1 := f.makeTask(t, uniq("duel_task_a"), domain.DifficultyEasy)
	task2 := f.makeTask(t, uniq("duel_task_b"), domain.DifficultyEasy)
	duel, err := f.duels.Create(ctx, player1ID, player2ID, time.Now().Add(5*time.Minute).UTC())
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, player1ID, task1.ID))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, player2ID, task2.ID))
	_, err = f.players.UpdateStatus(ctx, player1ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)
	_, err = f.players.UpdateStatus(ctx, player2ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)
	return duel.ID
}

func decodeJSON[T any](t *testing.T, resp *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	return out
}

func bearer(token string) string {
	return "Bearer " + token
}

func session(token uuid.UUID) string {
	return token.String()
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

func TestRESTHandlers_HealthDegradedShape(t *testing.T) {
	server := restv1.New(restv1.Dependencies{
		Health: restv1.HealthChecks{
			DB: usecase.HealthCheckerFunc(func(context.Context) error {
				return errors.New("db down")
			}),
			Redis: usecase.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SeaweedFS: usecase.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
		},
	})
	handler := restv1.NewHandler(server, restv1.HandlerOptions{})
	validator := newOpenAPIResponseValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	require.Equal(t, http.StatusServiceUnavailable, resp.Code)
	route, pathParams, err := validator.FindRoute(req)
	require.NoError(t, err)
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: resp.Code,
		Header: resp.Result().Header,
	}
	input.SetBodyBytes(resp.Body.Bytes())
	require.NoError(t, openapi3filter.ValidateResponse(context.Background(), input))
}

func TestRESTHandlers_HealthSchemaVersionZeroIsDegraded(t *testing.T) {
	server := restv1.New(restv1.Dependencies{
		Health: restv1.HealthChecks{
			DB: usecase.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			Redis: usecase.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SeaweedFS: usecase.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SchemaVersion: usecase.SchemaVersionReaderFunc(func(context.Context) (int64, error) {
				return 0, nil
			}),
		},
	})
	handler := restv1.NewHandler(server, restv1.HandlerOptions{})
	validator := newOpenAPIResponseValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	require.Equal(t, http.StatusServiceUnavailable, resp.Code)
	got := decodeJSON[openapi.HealthResponse](t, resp)
	require.Equal(t, openapi.HealthResponseStatusDegraded, got.Status)
	require.Equal(t, int64(0), got.SchemaVersion)

	route, pathParams, err := validator.FindRoute(req)
	require.NoError(t, err)
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: resp.Code,
		Header: resp.Result().Header,
	}
	input.SetBodyBytes(resp.Body.Bytes())
	require.NoError(t, openapi3filter.ValidateResponse(context.Background(), input))
}
