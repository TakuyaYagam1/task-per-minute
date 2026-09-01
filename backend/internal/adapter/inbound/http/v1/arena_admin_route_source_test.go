package v1

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const arenaRoutesPath = "../../../../../api/routes/arena.yml"

type arenaRouteDocument struct {
	Components arenaRouteComponents `yaml:"components"`
}

type arenaRouteComponents struct {
	Schemas    map[string]arenaRouteSchema    `yaml:"schemas"`
	Parameters map[string]arenaRouteParameter `yaml:"parameters"`
	Responses  map[string]arenaRouteResponse  `yaml:"responses"`
}

type arenaRouteSchema struct {
	Type                 string                              `yaml:"type"`
	Ref                  string                              `yaml:"$ref"`
	Required             []string                            `yaml:"required"`
	Properties           map[string]arenaRouteSchemaProperty `yaml:"properties"`
	Items                *arenaRouteSchemaProperty           `yaml:"items"`
	AdditionalProperties *bool                               `yaml:"additionalProperties"`
}

type arenaRouteSchemaProperty struct {
	Type      string                    `yaml:"type"`
	Format    string                    `yaml:"format"`
	Ref       string                    `yaml:"$ref"`
	WriteOnly bool                      `yaml:"writeOnly"`
	Nullable  bool                      `yaml:"nullable"`
	Items     *arenaRouteSchemaProperty `yaml:"items"`
}

type arenaRouteParameter struct {
	Ref      string                   `yaml:"$ref"`
	Name     string                   `yaml:"name"`
	In       string                   `yaml:"in"`
	Required bool                     `yaml:"required"`
	Schema   arenaRouteSchemaProperty `yaml:"schema"`
}

type arenaRouteResponse struct {
	Ref     string                         `yaml:"$ref"`
	Content map[string]arenaRouteMediaType `yaml:"content"`
}

type arenaRouteMediaType struct {
	Schema arenaRouteSchemaProperty `yaml:"schema"`
}

type arenaRoutePathItem struct {
	Parameters []arenaRouteParameter `yaml:"parameters"`
	Get        *arenaRouteOperation  `yaml:"get"`
	Post       *arenaRouteOperation  `yaml:"post"`
	Put        *arenaRouteOperation  `yaml:"put"`
}

type arenaRouteOperation struct {
	OperationID string                        `yaml:"operationId"`
	Scope       string                        `yaml:"x-arena-auth-scope"`
	Security    *[]map[string][]string        `yaml:"security"`
	Parameters  []arenaRouteParameter         `yaml:"parameters"`
	RequestBody *arenaRouteRequestBody        `yaml:"requestBody"`
	Responses   map[string]arenaRouteResponse `yaml:"responses"`
}

type arenaRouteRequestBody struct {
	Required bool                           `yaml:"required"`
	Content  map[string]arenaRouteMediaType `yaml:"content"`
}

func TestArenaAdminRouteSource(t *testing.T) {
	routes, document := loadArenaRouteSource(t)

	readOperations := []struct {
		path        string
		method      string
		operationID string
	}{
		{"/api/v1/arena/operator/tournaments", "get", "listArenaOperatorTournaments"},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/roster", "get", "getArenaOperatorRoster"},
	}
	for _, expected := range readOperations {
		operation := requireArenaRouteOperation(t, routes, expected.path, expected.method)
		require.Equal(t, expected.operationID, operation.OperationID)
		requireArenaRouteSecurity(t, operation, "BearerAuth", "operator:")
		require.Contains(t, operation.Responses, "401")
		require.Contains(t, operation.Responses, "403")
	}

	mutationOperations := []struct {
		path          string
		method        string
		operationID   string
		requestSchema string
		confirmed     bool
		notFound      bool
	}{
		{"/api/v1/arena/operator/tournaments", "post", "createArenaTournament", "ArenaCreateTournamentRequest", false, false},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/roster", "put", "replaceArenaTournamentRoster", "ArenaReplaceRosterRequest", false, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/roster/preflight", "post", "runArenaRosterPreflight", "ArenaPreflightRequest", false, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/roster/lock", "post", "lockArenaTournamentRoster", "ArenaLockRosterRequest", false, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/roster/unlock", "post", "unlockArenaTournamentRoster", "ArenaUnlockRosterRequest", true, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/pairings", "post", "configureArenaTournamentPairings", "ArenaPairingConfigurationRequest", false, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/actions", "post", "applyArenaTournamentAction", "ArenaTournamentActionRequest", true, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/waves/{wave_id}/actions", "post", "controlArenaTournamentWave", "ArenaWaveControlRequest", true, true},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/series/{series_id}/games/{game_id}/corrections", "post", "correctArenaGameResult", "ArenaOperatorCorrectionRequest", true, true},
	}
	for _, expected := range mutationOperations {
		operation := requireArenaRouteOperation(t, routes, expected.path, expected.method)
		require.Equal(t, expected.operationID, operation.OperationID)
		requireArenaRouteSecurity(t, operation, "BearerAuth", "operator:")
		requireArenaMutationParameters(t, operation)
		requireArenaRequestSchema(t, operation, expected.requestSchema)
		require.Contains(t, operation.Responses, "401")
		require.Contains(t, operation.Responses, "403")
		if expected.notFound {
			require.Contains(t, operation.Responses, "404")
		}
		require.Equal(t, "#/components/responses/ArenaRevisionConflictResponse", operation.Responses["409"].Ref)

		requestSchema := requireArenaRouteSchema(t, document, expected.requestSchema)
		require.Contains(t, requestSchema.Required, "expected_projection_revision")
		require.NotContains(t, requestSchema.Properties, "command_id")
		if expected.confirmed {
			require.Contains(t, requestSchema.Required, "confirmed")
		}
		for _, forbidden := range []string{"actor_id", "operator_id"} {
			require.NotContains(t, requestSchema.Properties, forbidden)
		}
	}

	conflict := document.Components.Responses["ArenaRevisionConflictResponse"]
	require.Equal(t,
		"../components/schemas/arena_schemas.yml#/ArenaRevisionConflict",
		conflict.Content["application/json"].Schema.Ref,
	)
	require.True(t, document.Components.Parameters["ArenaIdempotencyKey"].Required)
	require.False(t, document.Components.Parameters["ArenaAdminCSRFToken"].Required)
}

func loadArenaRouteSource(t *testing.T) (map[string]arenaRoutePathItem, arenaRouteDocument) {
	t.Helper()

	data, err := os.ReadFile(arenaRoutesPath)
	require.NoError(t, err)

	var nodes map[string]yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &nodes))
	routes := make(map[string]arenaRoutePathItem)
	for name, node := range nodes {
		if !strings.HasPrefix(name, "/") {
			continue
		}
		var pathItem arenaRoutePathItem
		require.NoError(t, node.Decode(&pathItem), name)
		routes[name] = pathItem
	}
	var document arenaRouteDocument
	require.NoError(t, yaml.Unmarshal(data, &document))
	require.NotEmpty(t, routes)
	return routes, document
}

func requireArenaRouteOperation(
	t *testing.T,
	routes map[string]arenaRoutePathItem,
	path string,
	method string,
) arenaRouteOperation {
	t.Helper()

	pathItem, ok := routes[path]
	require.Truef(t, ok, "missing Arena route %s", path)
	var operation *arenaRouteOperation
	switch method {
	case "get":
		operation = pathItem.Get
	case "post":
		operation = pathItem.Post
	case "put":
		operation = pathItem.Put
	default:
		t.Fatalf("unsupported method %s", method)
	}
	require.NotNilf(t, operation, "missing %s operation for %s", method, path)
	return *operation
}

func requireArenaRouteSecurity(t *testing.T, operation arenaRouteOperation, scheme, scopePrefix string) {
	t.Helper()

	require.NotNil(t, operation.Security, operation.OperationID)
	require.Len(t, *operation.Security, 1, operation.OperationID)
	_, ok := (*operation.Security)[0][scheme]
	require.Truef(t, ok, "%s must use %s", operation.OperationID, scheme)
	require.Contains(t, operation.Scope, scopePrefix, operation.OperationID)
}

func requireArenaMutationParameters(t *testing.T, operation arenaRouteOperation) {
	t.Helper()

	refs := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		refs = append(refs, parameter.Ref)
	}
	for _, ref := range []string{
		"#/components/parameters/ArenaIdempotencyKey",
		"#/components/parameters/ArenaAdminCSRFToken",
	} {
		require.Truef(t, slices.Contains(refs, ref), "%s is missing %s", operation.OperationID, ref)
	}
}

func requireArenaRequestSchema(t *testing.T, operation arenaRouteOperation, schemaName string) {
	t.Helper()

	require.NotNil(t, operation.RequestBody, operation.OperationID)
	require.True(t, operation.RequestBody.Required, operation.OperationID)
	require.Equal(t,
		"#/components/schemas/"+schemaName,
		operation.RequestBody.Content["application/json"].Schema.Ref,
		operation.OperationID,
	)
}

func requireArenaRouteSchema(t *testing.T, document arenaRouteDocument, name string) arenaRouteSchema {
	t.Helper()

	schema, ok := document.Components.Schemas[name]
	require.Truef(t, ok, "missing route schema %s", name)
	return schema
}
