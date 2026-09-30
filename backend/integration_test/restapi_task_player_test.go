//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func TestRESTHandlers_DeleteTaskWithSourcePreservesStoredArchive(t *testing.T) {
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
	}`, uniq("source_delete")), adminSession(adminToken))
	require.Equal(t, http.StatusCreated, createResp.Code)
	f.validateResponse(t, createReq, createResp)
	created := decodeJSON[api.TaskDetails](t, createResp)

	zipBody, contentType := multipartBody(t, []byte{'P', 'K', 0x03, 0x04, 'd', 'e', 'l'})
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
	beforeDelete := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer beforeDelete.Body.Close()
	require.Equal(t, http.StatusOK, beforeDelete.StatusCode)

	deleteReq, deleteResp := f.doJSON(
		t,
		http.MethodDelete,
		"/api/v1/admin/tasks/"+created.Id.String(),
		"",
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusNoContent, deleteResp.Code)
	f.validateResponse(t, deleteReq, deleteResp)

	afterDelete := httpGetWithTimeout(t, uploaded.SourceFileUrl)
	defer afterDelete.Body.Close()
	require.Equal(t, http.StatusOK, afterDelete.StatusCode)
}

func TestRESTHandlers_CORSPreflightAllowedOrigin(t *testing.T) {
	f := newRESTFixture(t)
	handler := middleware.CORS([]string{"http://localhost:3000"})(f.handler)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/players/join", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-CSRF-Token")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	require.Equal(t, http.StatusNoContent, resp.Code)
	require.Equal(t, "http://localhost:3000", resp.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	require.Contains(t, resp.Header().Get("Access-Control-Allow-Headers"), middleware.CSRFHeaderName)
	require.NotContains(t, resp.Header().Get("Access-Control-Allow-Headers"), "Authorization")
}

func TestRESTHandlers_UploadSourceOverLimitReturns413Or400(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	task := f.makeTask(t, uniq("oversize"), domain.DifficultyEasy)
	body, contentType, contentLength := largeMultipartBody(taskusecase.MaxSourceFileSize + 1<<20)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tasks/"+task.ID.String()+"/source", body)
	req.Header.Set("Content-Type", contentType)
	addAdminSession(t, req, adminToken)
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
		adminSession(adminToken),
	)

	require.Equal(t, http.StatusOK, resp.Code)
	f.validateResponse(t, req, resp)
	uploaded := decodeJSON[api.TaskSourceUploadResponse](t, resp)
	require.Contains(t, uploaded.SourceFileUrl, "X-Amz-Signature")
}

func TestRESTHandlers_AdminPlayersListUpdateDelete(t *testing.T) {
	f := newRESTFixture(t)
	adminToken := f.adminAccessToken(t)
	ctx := context.Background()
	adminClaims, err := f.auth.VerifyAccess(ctx, adminToken)
	require.NoError(t, err)

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	unauthorizedReq, unauthorizedResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players", "", "")
	require.Equal(t, http.StatusUnauthorized, unauthorizedResp.Code)
	f.validateResponse(t, unauthorizedReq, unauthorizedResp)

	bearerReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/players", nil)
	bearerReq.Header.Set("Authorization", "Bearer "+adminToken)
	bearerResp := httptest.NewRecorder()
	f.handler.ServeHTTP(bearerResp, bearerReq)
	require.Equal(t, http.StatusUnauthorized, bearerResp.Code)
	f.validateResponse(t, bearerReq, bearerResp)

	listReq, listResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, listResp.Code)
	f.validateResponse(t, listReq, listResp)
	players := decodeJSON[[]api.PlayerManagementView](t, listResp)
	require.Contains(t, adminPlayerIDs(players), alice.ID)
	require.Nil(t, adminPlayerByID(players, alice.ID).DeletedAt)

	updateReq, updateResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/players/"+alice.ID.String(),
		`{"username":"renamed_admin_player","wins":3,"average_solve_time_ms":90000}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusOK, updateResp.Code)
	f.validateResponse(t, updateReq, updateResp)
	updated := decodeJSON[api.PlayerManagementView](t, updateResp)
	require.Equal(t, "renamed_admin_player", updated.Username)
	require.Equal(t, int32(3), updated.Wins)
	require.Equal(t, int64(90000), updated.AverageSolveTimeMs)
	require.True(t, updated.StatsOverridden)
	require.Nil(t, updated.DeletedAt)

	auditReq, auditResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+alice.ID.String()+"/audit", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, auditResp.Code)
	f.validateResponse(t, auditReq, auditResp)
	auditEvents := decodeJSON[[]api.PlayerAuditEvent](t, auditResp)
	require.Len(t, auditEvents, 1)
	require.Equal(t, api.PlayerAuditActionUpdate, auditEvents[0].Action)
	require.Equal(t, "admin", auditEvents[0].ActorSubject)
	require.Equal(t, adminClaims.JTI, auditEvents[0].ActorJti)
	require.Equal(t, alice.Username, auditEvents[0].BeforeState.Username)
	require.Equal(t, int32(0), auditEvents[0].BeforeState.Wins)
	require.Equal(t, int64(0), auditEvents[0].BeforeState.AverageSolveTimeMs)
	require.False(t, auditEvents[0].BeforeState.StatsOverridden)
	require.False(t, auditEvents[0].BeforeState.Deleted)
	require.Equal(t, "renamed_admin_player", auditEvents[0].AfterState.Username)
	require.Equal(t, int32(3), auditEvents[0].AfterState.Wins)
	require.Equal(t, int64(90000), auditEvents[0].AfterState.AverageSolveTimeMs)
	require.True(t, auditEvents[0].AfterState.StatsOverridden)
	require.False(t, auditEvents[0].AfterState.Deleted)

	board, err := f.board.TopStats(ctx, 50)
	require.NoError(t, err)
	require.Contains(t, leaderboardUsernames(board), "renamed_admin_player")

	badUpdateReq, badUpdateResp := f.doJSON(
		t,
		http.MethodPut,
		"/api/v1/admin/players/"+bob.ID.String(),
		`{"username":"bad_stats_player","wins":1,"average_solve_time_ms":0}`,
		adminSession(adminToken),
	)
	require.Equal(t, http.StatusBadRequest, badUpdateResp.Code)
	f.validateResponse(t, badUpdateReq, badUpdateResp)
	bobAuditReq, bobAuditResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+bob.ID.String()+"/audit", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, bobAuditResp.Code)
	f.validateResponse(t, bobAuditReq, bobAuditResp)
	bobAuditEvents := decodeJSON[[]api.PlayerAuditEvent](t, bobAuditResp)
	require.Empty(t, bobAuditEvents)

	deleteReq, deleteResp := f.doJSON(t, http.MethodDelete, "/api/v1/admin/players/"+alice.ID.String(), "", adminSession(adminToken))
	require.Equal(t, http.StatusNoContent, deleteResp.Code)
	f.validateResponse(t, deleteReq, deleteResp)

	auditAfterDeleteReq, auditAfterDeleteResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+alice.ID.String()+"/audit", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, auditAfterDeleteResp.Code)
	f.validateResponse(t, auditAfterDeleteReq, auditAfterDeleteResp)
	auditEvents = decodeJSON[[]api.PlayerAuditEvent](t, auditAfterDeleteResp)
	require.Len(t, auditEvents, 2)
	require.Equal(t, api.PlayerAuditActionDelete, auditEvents[0].Action)
	require.Equal(t, "renamed_admin_player", auditEvents[0].BeforeState.Username)
	require.False(t, auditEvents[0].BeforeState.Deleted)
	require.True(t, auditEvents[0].AfterState.Deleted)
	require.NotEqual(t, auditEvents[0].BeforeState.Username, auditEvents[0].AfterState.Username)

	limitedAuditReq, limitedAuditResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+alice.ID.String()+"/audit?limit=1", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, limitedAuditResp.Code)
	f.validateResponse(t, limitedAuditReq, limitedAuditResp)
	limitedAudit := decodeJSON[[]api.PlayerAuditEvent](t, limitedAuditResp)
	require.Len(t, limitedAudit, 1)
	require.Equal(t, api.PlayerAuditActionDelete, limitedAudit[0].Action)

	afterDeleteReq, afterDeleteResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, afterDeleteResp.Code)
	f.validateResponse(t, afterDeleteReq, afterDeleteResp)
	require.NotContains(t, adminPlayerIDs(decodeJSON[[]api.PlayerManagementView](t, afterDeleteResp)), alice.ID)
	withDeletedReq, withDeletedResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players?include_deleted=true", "", adminSession(adminToken))
	require.Equal(t, http.StatusOK, withDeletedResp.Code)
	f.validateResponse(t, withDeletedReq, withDeletedResp)
	withDeletedPlayers := decodeJSON[[]api.PlayerManagementView](t, withDeletedResp)
	deletedPlayer := adminPlayerByID(withDeletedPlayers, alice.ID)
	require.NotNil(t, deletedPlayer.DeletedAt)

	unknownAuditReq, unknownAuditResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+uuid.NewString()+"/audit", "", adminSession(adminToken))
	require.Equal(t, http.StatusNotFound, unknownAuditResp.Code)
	f.validateResponse(t, unknownAuditReq, unknownAuditResp)
	unauthorizedAuditReq, unauthorizedAuditResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+alice.ID.String()+"/audit", "", "")
	require.Equal(t, http.StatusUnauthorized, unauthorizedAuditResp.Code)
	f.validateResponse(t, unauthorizedAuditReq, unauthorizedAuditResp)
	badLimitReq, badLimitResp := f.doJSON(t, http.MethodGet, "/api/v1/admin/players/"+alice.ID.String()+"/audit?limit=0", "", adminSession(adminToken))
	require.Equal(t, http.StatusBadRequest, badLimitResp.Code)
	f.validateResponse(t, badLimitReq, badLimitResp)

	boardAfterDelete, err := f.board.TopStats(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, leaderboardUsernames(boardAfterDelete), "renamed_admin_player")

	accountPlayer := f.joinPlayerViaUsecase(t, uniq("verified"))
	accountPath := "/api/v1/admin/players/" + accountPlayer.ID.String()
	renameReq, renameResp := f.doJSON(t, http.MethodPut, accountPath,
		`{"username":"renamed_account","wins":0,"average_solve_time_ms":0}`, adminSession(adminToken))
	require.Equal(t, http.StatusConflict, renameResp.Code)
	f.validateResponse(t, renameReq, renameResp)
	statsReq, statsResp := f.doJSON(t, http.MethodPut, accountPath,
		fmt.Sprintf(`{"username":%q,"wins":2,"average_solve_time_ms":80000}`, accountPlayer.Username), adminSession(adminToken))
	require.Equal(t, http.StatusOK, statsResp.Code)
	f.validateResponse(t, statsReq, statsResp)
	stats := decodeJSON[api.PlayerManagementView](t, statsResp)
	require.Equal(t, accountPlayer.Username, stats.Username)
	require.Equal(t, int32(2), stats.Wins)
	accountDeleteReq, accountDeleteResp := f.doJSON(t, http.MethodDelete, accountPath, "", adminSession(adminToken))
	require.Equal(t, http.StatusNoContent, accountDeleteResp.Code)
	f.validateResponse(t, accountDeleteReq, accountDeleteResp)

	meReq, meResp := f.doJSON(t, http.MethodGet, "/api/v1/players/me", "", session(*accountPlayer.SessionToken))
	require.Equal(t, http.StatusUnauthorized, meResp.Code)
	f.validateResponse(t, meReq, meResp)
}
