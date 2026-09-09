//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/routers"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

const restAdminPassword = "admin-password"

type restFixture struct {
	*databaseFixture

	handler   http.Handler
	auth      *authusecase.UseCase
	validator routers.Router
}

func TestRESTHandlers_OpenAPIResponseShapes(t *testing.T) {
	f := newRESTFixture(t)

	loginReq, loginResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/login", `{"password":"`+restAdminPassword+`"}`, "")
	require.Equal(t, http.StatusOK, loginResp.Code)
	f.validateResponse(t, loginReq, loginResp)
	loginSession := decodeJSON[api.AdminSessionResponse](t, loginResp)
	require.Positive(t, loginSession.ExpiresIn)
	require.NotContains(t, loginResp.Body.String(), "access_token")
	require.NotContains(t, loginResp.Body.String(), "refresh_token")
	refreshToken := responseCookieValue(t, loginResp, middleware.AdminRefreshCookieName)

	refreshReq, refreshResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/refresh", "", adminRefreshSession(refreshToken))
	require.Equal(t, http.StatusOK, refreshResp.Code)
	f.validateResponse(t, refreshReq, refreshResp)
	refreshSession := decodeJSON[api.AdminSessionResponse](t, refreshResp)
	require.Positive(t, refreshSession.ExpiresIn)
	require.NotContains(t, refreshResp.Body.String(), "access_token")
	require.NotContains(t, refreshResp.Body.String(), "refresh_token")
	adminToken := responseCookieValue(t, refreshResp, middleware.AdminAccessCookieName)

	createReq, createResp := f.doJSON(t, http.MethodPost, "/api/v1/admin/tasks", `{
		"title":"`+uniq("rest_task")+`",
		"description":"created through REST",
		"category":"forensics",
		"difficulty":"easy",
		"time_limit":60,
		"flag":"FLAG{rest}",
		"hints":["first hint","second hint","third hint"]
	}`, adminSession(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	createdTask := decodeJSON[api.TaskDetails](t, createResp)
	require.Equal(t, nullableOpenAPIHints([]string{"first hint", "second hint", "third hint"}), createdTask.Hints)

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
		req, resp := f.doJSON(t, tc.method, tc.path, tc.body, adminSession(adminToken))
		require.Equal(t, tc.want, resp.Code, tc.name)
		f.validateResponse(t, req, resp)
	}

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'})
	uploadReq, uploadResp := f.do(t, http.MethodPost, "/api/v1/admin/tasks/"+createdTask.Id.String()+"/source", zipBody, contentType, adminSession(adminToken))
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)

	aliceReq, aliceResp := f.doJSON(t, http.MethodPost, "/api/v1/players/join", `{"username":"`+uniq("alice")+`"}`, "")
	require.Equal(t, http.StatusOK, aliceResp.Code)
	f.validateResponse(t, aliceReq, aliceResp)
	aliceSession := playerSessionCookieValue(t, aliceResp)

	meReq, meResp := f.doJSON(t, http.MethodGet, "/api/v1/players/me", "", cookieSession(aliceSession))
	require.Equal(t, http.StatusOK, meResp.Code)
	f.validateResponse(t, meReq, meResp)

	boardReq, boardResp := f.doJSON(t, http.MethodGet, "/api/v1/leaderboard", "", "")
	require.Equal(t, http.StatusOK, boardResp.Code, boardResp.Body.String())
	f.validateResponse(t, boardReq, boardResp)

	healthReq, healthResp := f.doJSON(t, http.MethodGet, "/health", "", "")
	require.Equal(t, http.StatusOK, healthResp.Code)
	f.validateResponse(t, healthReq, healthResp)
	require.Positive(t, decodeJSON[api.HealthResponse](t, healthResp).SchemaVersion)

	deleteReq, deleteResp := f.doJSON(t, http.MethodDelete, "/api/v1/admin/tasks/"+createdTask.Id.String(), "", adminSession(adminToken))
	require.Equal(t, http.StatusNoContent, deleteResp.Code)
	f.validateResponse(t, deleteReq, deleteResp)
}

func TestRESTHandlers_DeleteMissingTaskReturns404(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	taskID := uuid.New()

	req, resp := f.doJSON(t, http.MethodDelete, "/api/v1/admin/tasks/"+taskID.String(), "", adminSession(adminToken))
	require.Equal(t, http.StatusNotFound, resp.Code)
	f.validateResponse(t, req, resp)
}

func TestRESTHandlers_ExpiredPlayerSessionReturns401(t *testing.T) {
	f := newRESTFixture(t)
	ctx := context.Background()

	joinReq, joinResp := f.doJSON(t, http.MethodPost, "/api/v1/players/join", `{"username":"`+uniq("alice")+`"}`, "")
	require.Equal(t, http.StatusOK, joinResp.Code)
	f.validateResponse(t, joinReq, joinResp)
	joined := decodeJSON[api.JoinPlayerResponse](t, joinResp)
	sessionToken := uuid.MustParse(playerSessionCookieValue(t, joinResp))

	expiresAt := time.Now().Add(-time.Minute).UTC()
	_, err := f.players.UpdateSessionToken(ctx, joined.PlayerId, &sessionToken, &expiresAt)
	require.NoError(t, err)

	meReq, meResp := f.doJSON(t, http.MethodGet, "/api/v1/players/me", "", session(sessionToken))
	require.Equal(t, http.StatusUnauthorized, meResp.Code)
	f.validateResponse(t, meReq, meResp)

	cleared, err := f.players.GetByID(ctx, joined.PlayerId)
	require.NoError(t, err)
	require.Nil(t, cleared.SessionToken)
	require.Nil(t, cleared.SessionExpiresAt)
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
	}`, uniq("task_url"), initialURL), adminSession(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[api.TaskDetails](t, createResp)
	require.NotNil(t, created.TaskUrl)
	require.Equal(t, initialURL, *created.TaskUrl)

	preserveReq, preserveResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"title":"preserve task url"}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, preserveResp.Code)
	f.validateResponse(t, preserveReq, preserveResp)
	preserved := decodeJSON[api.TaskDetails](t, preserveResp)
	require.NotNil(t, preserved.TaskUrl)
	require.Equal(t, initialURL, *preserved.TaskUrl)

	clearReq, clearResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"task_url":null}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, clearResp.Code)
	f.validateResponse(t, clearReq, clearResp)
	cleared := decodeJSON[api.TaskDetails](t, clearResp)
	require.Nil(t, cleared.TaskUrl)

	nextURL := "pwn.example.com:31337"
	setReq, setResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		fmt.Sprintf(`{"task_url":%q}`, nextURL),
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, setResp.Code)
	f.validateResponse(t, setReq, setResp)
	updated := decodeJSON[api.TaskDetails](t, setResp)
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
	}`, uniq("forensics_url"), initialURL), adminSession(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	forensics := decodeJSON[api.TaskDetails](t, createResp)
	require.Equal(t, api.TaskCategoryForensics, forensics.Category)
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
	}`, uniq("web_url"), initialURL), adminSession(adminToken))
	require.Equal(t, http.StatusCreated, webCreateResp.Code)
	f.validateResponse(t, webCreateReq, webCreateResp)
	created := decodeJSON[api.TaskDetails](t, webCreateResp)
	require.NotNil(t, created.TaskUrl)

	updateReq, updateResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"category":"forensics"}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, updateResp.Code)
	f.validateResponse(t, updateReq, updateResp)
	updated := decodeJSON[api.TaskDetails](t, updateResp)
	require.Equal(t, api.TaskCategoryForensics, updated.Category)
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
	}`, uniq("source_url")), adminSession(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[api.TaskDetails](t, createResp)

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'})
	uploadReq, uploadResp := f.do(
		t,
		http.MethodPost,
		"/api/v1/admin/tasks/"+created.Id.String()+"/source",
		zipBody,
		contentType,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)
	uploaded := decodeJSON[api.TaskSourceUploadResponse](t, uploadResp)
	beforeClear := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer beforeClear.Body.Close()
	require.Equal(t, http.StatusOK, beforeClear.StatusCode)

	preserveReq, preserveResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"title":"preserve source url"}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, preserveResp.Code)
	f.validateResponse(t, preserveReq, preserveResp)
	preserved := decodeJSON[api.TaskDetails](t, preserveResp)
	require.NotNil(t, preserved.SourceFileUrl)

	clearReq, clearResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"clear_source_file":true}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, clearResp.Code)
	f.validateResponse(t, clearReq, clearResp)
	cleared := decodeJSON[api.TaskDetails](t, clearResp)
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
	}`, uniq("source_category")), adminSession(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[api.TaskDetails](t, createResp)

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'c', 'a', 't'})
	uploadReq, uploadResp := f.do(
		t,
		http.MethodPost,
		"/api/v1/admin/tasks/"+created.Id.String()+"/source",
		zipBody,
		contentType,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, uploadResp.Code)
	f.validateResponse(t, uploadReq, uploadResp)
	uploaded := decodeJSON[api.TaskSourceUploadResponse](t, uploadResp)

	updateReq, updateResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/tasks/"+created.Id.String(),
		`{"category":"web","task_url":"https://tasks.example/web"}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, updateResp.Code)
	f.validateResponse(t, updateReq, updateResp)
	updated := decodeJSON[api.TaskDetails](t, updateResp)
	require.Equal(t, api.TaskCategoryWeb, updated.Category)
	require.NotNil(t, updated.TaskUrl)
	require.NotNil(t, updated.SourceFileUrl)

	afterUpdate := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer afterUpdate.Body.Close()
	require.Equal(t, http.StatusOK, afterUpdate.StatusCode)
}

func TestRESTHandlers_UpdateTaskRejectsLegacySourceFileURLInput(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	task := f.makeTask(t, uniq("source_url_invalid"), domain.DifficultyEasy)

	for _, body := range []string{
		`{"source_file_url":null}`,
		`{"source_file_url":"not-a-url"}`,
		`{"source_file_url":"https://files.example/source.zip"}`,
	} {
		req, resp := f.doJSON(
			t,
			http.MethodPut,
			"/api/v1/admin/tasks/"+task.ID.String(),
			body,
			adminSession(adminToken),
		)

		require.Equal(t, http.StatusBadRequest, resp.Code)
		f.validateResponse(t, req, resp)
	}
}
