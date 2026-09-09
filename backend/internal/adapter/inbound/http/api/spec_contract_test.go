package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestOpenAPIUnsafeOperationsDocumentOriginRejection(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	for path, item := range spec.Paths.Map() {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			operation := item.GetOperation(method)
			if operation == nil {
				continue
			}
			requireResponse(t, operation, http.StatusForbidden, method+" "+path)
		}
	}
}

func TestOpenAPIOperationsDocumentUnexpectedServerProblems(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	for path, item := range spec.Paths.Map() {
		for method, operation := range item.Operations() {
			label := method + " " + path
			require.NotNil(t, operation.Responses, label)
			response := operation.Responses.Default()
			require.NotNil(t, response, label)
			require.NotNil(t, response.Value, label)
			_, exists := response.Value.Content["application/problem+json"]
			require.True(t, exists, label)
		}
	}
}

func TestOpenAPIAdminMutationsDocumentCSRFContract(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	for path, item := range spec.Paths.Map() {
		if !strings.HasPrefix(path, "/api/v1/admin/") || path == "/api/v1/admin/login" {
			continue
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			operation := item.GetOperation(method)
			if operation == nil {
				continue
			}
			label := method + " " + path
			parameter := operation.Parameters.GetByInAndName("header", "X-CSRF-Token")
			require.NotNil(t, parameter, label)
			require.True(t, parameter.Required, label)
			require.Nil(
				t,
				operation.Parameters.GetByInAndName("header", "X-Admin-Refresh-CSRF-Token"),
				label,
			)
			requireResponse(t, operation, http.StatusBadRequest, label)
			requireResponse(t, operation, http.StatusForbidden, label)
		}
	}
}

func TestOpenAPIPlayerLogoutDocumentsConditionalCSRFHeader(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	operation := requireOperation(t, spec, http.MethodPost, "/api/v1/players/logout")
	parameter := operation.Parameters.GetByInAndName("header", "X-CSRF-Token")
	require.NotNil(t, parameter)
	require.False(t, parameter.Required)
}

func TestOpenAPIJSONMutationsDocumentDecoderFailures(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	operations := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/players/join"},
		{http.MethodPost, "/api/v1/admin/login"},
		{http.MethodPut, "/api/v1/admin/players/{id}"},
		{http.MethodPost, "/api/v1/admin/tasks"},
		{http.MethodPut, "/api/v1/admin/tasks/{id}"},
		{http.MethodPost, "/api/v1/admin/tournaments"},
	}

	for _, candidate := range operations {
		operation := requireOperation(t, spec, candidate.method, candidate.path)
		label := candidate.method + " " + candidate.path
		requireResponse(t, operation, http.StatusBadRequest, label)
		requireResponse(t, operation, http.StatusRequestEntityTooLarge, label)
		requireResponse(t, operation, http.StatusUnsupportedMediaType, label)
	}
}

func TestOpenAPISessionAndRateLimitHeaders(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	for _, candidate := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/admin/login"},
		{http.MethodPost, "/api/v1/admin/refresh"},
		{http.MethodPost, "/api/v1/players/join"},
		{http.MethodGet, "/api/v1/leaderboard"},
	} {
		operation := requireOperation(t, spec, candidate.method, candidate.path)
		requireResponseHeader(t, operation, http.StatusTooManyRequests, "Retry-After", candidate.path)
	}

	for _, candidate := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/players/join"},
		{http.MethodGet, "/api/v1/players/me"},
	} {
		operation := requireOperation(t, spec, candidate.method, candidate.path)
		requireResponseHeader(t, operation, http.StatusOK, "X-CSRF-Token", candidate.path)
	}

	for _, path := range []string{"/api/v1/admin/login", "/api/v1/admin/refresh"} {
		operation := requireOperation(t, spec, http.MethodPost, path)
		requireResponseHeader(t, operation, http.StatusOK, "X-CSRF-Token", path)
		requireResponseHeader(t, operation, http.StatusOK, "X-Admin-Refresh-CSRF-Token", path)
	}
}

func TestOpenAPIOperationIDsAndTagsUseDomainNaming(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	expected := []struct {
		method      string
		path        string
		operationID string
		tag         string
	}{
		{http.MethodPost, "/api/v1/admin/login", "loginAdmin", "admin"},
		{http.MethodPost, "/api/v1/admin/refresh", "refreshAdminSession", "admin"},
		{http.MethodPost, "/api/v1/admin/logout", "logoutAdmin", "admin"},
		{http.MethodGet, "/api/v1/admin/players", "listPlayers", "player"},
		{http.MethodGet, "/api/v1/admin/players/events", "streamPlayerEvents", "player"},
		{http.MethodGet, "/api/v1/admin/players/{id}/audit", "listPlayerAuditEvents", "player"},
		{http.MethodPut, "/api/v1/admin/players/{id}", "updatePlayer", "player"},
		{http.MethodDelete, "/api/v1/admin/players/{id}", "deletePlayer", "player"},
		{http.MethodGet, "/api/v1/admin/tasks", "listTasks", "task"},
		{http.MethodGet, "/api/v1/admin/tournaments", "listTournaments", "tournament"},
	}
	for _, candidate := range expected {
		operation := requireOperation(t, spec, candidate.method, candidate.path)
		require.Equal(t, candidate.operationID, operation.OperationID, candidate.path)
		require.Equal(t, []string{candidate.tag}, operation.Tags, candidate.path)
	}
}

func TestOpenAPIContainsNoLegacyNamespaces(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(loadSpec(t))
	require.NoError(t, err)
	contract := strings.ToLower(string(encoded))
	for _, legacyNamespace := range []string{"arena", "duel"} {
		require.NotContains(t, contract, legacyNamespace)
	}
}

func TestOpenAPIPublicSchemasUseResourceNames(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	require.NotNil(t, spec.Components)
	for _, name := range []string{
		"PlayerAuditAction",
		"PlayerAuditState",
		"PlayerAuditEvent",
		"PlayerManagementView",
		"UpdatePlayerRequest",
		"TaskDetails",
		"TaskSourceUploadResponse",
		"TournamentRevisionProblem",
	} {
		_, exists := spec.Components.Schemas[name]
		require.True(t, exists, name)
	}
	for _, name := range []string{
		"AdminPlayerAuditAction",
		"AdminPlayerAuditState",
		"AdminPlayerAuditEventResponse",
		"AdminPlayerResponse",
		"UpdateAdminPlayerRequest",
		"TaskResponse",
		"UploadSourceResponse",
		"TournamentRevisionConflict",
	} {
		_, exists := spec.Components.Schemas[name]
		require.False(t, exists, name)
	}
}

func TestOpenAPITournamentRevisionProblemContract(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	require.NotNil(t, spec.Components)
	conflict, exists := spec.Components.Schemas["TournamentRevisionProblem"]
	require.True(t, exists)
	require.NotNil(t, conflict.Value)
	require.Len(t, conflict.Value.AllOf, 2)
	expected, exists := conflict.Value.AllOf[1].Value.Properties["expected_revision"]
	require.True(t, exists)
	require.NotNil(t, expected.Value)
	require.NotNil(t, expected.Value.Min)
	require.Zero(t, *expected.Value.Min)

	operation := requireOperation(t, spec, http.MethodPost, "/api/v1/admin/tournaments")
	response := requireResponse(t, operation, http.StatusConflict, "create tournament")
	_, exists = response.Content["application/problem+json"]
	require.True(t, exists)
	_, exists = response.Content["application/json"]
	require.False(t, exists)
}

func TestOpenAPITournamentGeneratedModelsKeepRequiredFieldsAsValues(t *testing.T) {
	t.Parallel()

	login := api.AdminLoginRequest{Password: "secret"}
	tournament := api.Tournament{Revision: 1}
	request := api.CreateTournamentRequest{ExpectedRevision: 0}
	require.Equal(t, "secret", login.Password)
	require.Equal(t, int64(1), tournament.Revision)
	require.Zero(t, request.ExpectedRevision)
}

func TestOpenAPIUpdateTaskUsesExplicitSourceFileClearCommand(t *testing.T) {
	t.Parallel()

	spec := loadSpec(t)
	require.NotNil(t, spec.Components)
	request, exists := spec.Components.Schemas["UpdateTaskRequest"]
	require.True(t, exists)
	require.NotNil(t, request.Value)
	require.Contains(t, request.Value.Properties, "clear_source_file")
	require.NotContains(t, request.Value.Properties, "source_file_url")
}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := api.GetSwagger()
	require.NoError(t, err)
	require.NotNil(t, spec.Paths)
	return spec
}

func requireOperation(t *testing.T, spec *openapi3.T, method, path string) *openapi3.Operation {
	t.Helper()
	item := spec.Paths.Find(path)
	require.NotNil(t, item, method+" "+path)
	operation := item.GetOperation(method)
	require.NotNil(t, operation, method+" "+path)
	return operation
}

func requireResponse(t *testing.T, operation *openapi3.Operation, status int, label string) *openapi3.Response {
	t.Helper()
	require.NotNil(t, operation.Responses, label)
	code := strconv.Itoa(status)
	response := operation.Responses.Value(code)
	require.NotNil(t, response, label+" response "+code)
	require.NotNil(t, response.Value, label+" response "+code)
	return response.Value
}

func requireResponseHeader(t *testing.T, operation *openapi3.Operation, status int, name, label string) {
	t.Helper()
	response := requireResponse(t, operation, status, label)
	_, exists := response.Headers[name]
	require.True(t, exists, label+" response header "+name)
}
