package v1

import (
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const arenaOpenAPIPath = "../../../../../api/openapi.yml"

type arenaOperatorCommandSchema struct {
	Type                 string                                        `yaml:"type"`
	Required             []string                                      `yaml:"required"`
	Properties           map[string]arenaOperatorCommandSchemaProperty `yaml:"properties"`
	AdditionalProperties *bool                                         `yaml:"additionalProperties"`
}

type arenaOperatorCommandSchemaProperty struct {
	Type        string                                        `yaml:"type"`
	Format      string                                        `yaml:"format"`
	Ref         string                                        `yaml:"$ref"`
	Minimum     int64                                         `yaml:"minimum"`
	MinLength   int                                           `yaml:"minLength"`
	MaxLength   int                                           `yaml:"maxLength"`
	MinItems    int                                           `yaml:"minItems"`
	MaxItems    int                                           `yaml:"maxItems"`
	UniqueItems bool                                          `yaml:"uniqueItems"`
	Nullable    bool                                          `yaml:"nullable"`
	Items       *arenaOperatorCommandSchemaProperty           `yaml:"items"`
	Properties  map[string]arenaOperatorCommandSchemaProperty `yaml:"properties"`
}

type arenaOperatorCommandDocument struct {
	Components struct {
		Schemas map[string]arenaOperatorCommandSchema `yaml:"schemas"`
	} `yaml:"components"`
}

type arenaOpenAPIRoot struct {
	Paths map[string]struct {
		Ref string `yaml:"$ref"`
	} `yaml:"paths"`
}

func TestArenaOperatorCommandRouteSource(t *testing.T) {
	t.Parallel()

	routes, _ := loadArenaRouteSource(t)
	document := loadArenaOperatorCommandDocument(t)
	root := loadArenaOpenAPIRoot(t)

	operations := []struct {
		path          string
		rootRef       string
		operationID   string
		requestSchema string
		required      []string
	}{
		{
			path:          "/api/v1/arena/operator/tournaments/{tournament_id}/waves/{wave_id}/no-shows",
			rootRef:       "routes/arena.yml#/~1api~1v1~1arena~1operator~1tournaments~1{tournament_id}~1waves~1{wave_id}~1no-shows",
			operationID:   "resolveArenaNoShow",
			requestSchema: "ArenaOperatorNoShowRequest",
			required: []string{
				"tournament_id", "wave_id", "window_id", "series_id", "confirmed", "reason",
				"expected_authority_revision", "expected_wave_revision_id", "expected_window_revision_id",
				"expected_series_state", "game_result_revision_ids", "score_revision_id",
				"series_result_revision_id",
			},
		},
		{
			path:          "/api/v1/arena/operator/tournaments/{tournament_id}/series/{series_id}/assignments/{assignment_id}/operator-reserves",
			rootRef:       "routes/arena.yml#/~1api~1v1~1arena~1operator~1tournaments~1{tournament_id}~1series~1{series_id}~1assignments~1{assignment_id}~1operator-reserves",
			operationID:   "assignArenaOperatorReserve",
			requestSchema: "ArenaOperatorReserveRequest",
			required: []string{
				"tournament_id", "old_wave_id", "series_id", "slot_id", "assignment_id",
				"assignment_attempt_id", "confirmed", "reason", "expected_authority_revision",
				"expected_exhaustion_command_id", "expected_assignment_revision",
				"expected_pool_revision_id", "expected_pool_revision", "expected_history_revision_id",
				"expected_history_revision", "expected_artifact_revision_id", "expected_artifact_revision",
				"expected_reservation_revision_id", "expected_reservation_revision",
				"expected_category_revision_id", "expected_category_revision", "proposed_task_id",
				"proposed_version", "proposed_snapshot_id", "expected_snapshot_id", "evidence_id",
			},
		},
		{
			path:          "/api/v1/arena/operator/tournaments/{tournament_id}/series/{series_id}/operator-forfeits",
			rootRef:       "routes/arena.yml#/~1api~1v1~1arena~1operator~1tournaments~1{tournament_id}~1series~1{series_id}~1operator-forfeits",
			operationID:   "recordArenaOperatorForfeit",
			requestSchema: "ArenaOperatorForfeitRequest",
			required: []string{
				"tournament_id", "series_id", "forfeiting_participant_id", "confirmed", "reason",
				"expected_authority_revision", "expected_game", "basis", "rule_id", "evidence_ids",
				"game_result_revision_id", "score_revision_id", "series_result_revision_id", "audit_event_id",
				"outbox_event_id", "projection_revision_id",
			},
		},
		{
			path:          "/api/v1/arena/operator/tournaments/{tournament_id}/series/{series_id}/games/{game_id}/replays",
			rootRef:       "routes/arena.yml#/~1api~1v1~1arena~1operator~1tournaments~1{tournament_id}~1series~1{series_id}~1games~1{game_id}~1replays",
			operationID:   "replayArenaOperatorGame",
			requestSchema: "ArenaOperatorReplayRequest",
			required: []string{
				"tournament_id", "old_wave_id", "series_id", "slot_id", "assignment_id", "failed_game_id",
				"confirmed", "reason", "expected_authority_revision", "expected_closure_revision_id",
				"assignment_attempt_id", "replacement_game_id", "replacement_wave_id",
				"replacement_wave_revision_id", "ready_window_id", "ready_window_revision_id",
			},
		},
	}

	for _, expected := range operations {
		t.Run(expected.operationID, func(t *testing.T) {
			require.Equal(t, expected.rootRef, root.Paths[expected.path].Ref)

			operation := requireArenaRouteOperation(t, routes, expected.path, "post")
			require.Equal(t, expected.operationID, operation.OperationID)
			requireArenaRouteSecurity(t, operation, "BearerAuth", "operator:tournament")
			requireArenaMutationParameters(t, operation)
			requireArenaRequestSchema(t, operation, expected.requestSchema)
			for _, status := range []string{"204", "400", "401", "403", "404", "409"} {
				require.Contains(t, operation.Responses, status)
			}

			schema := document.Components.Schemas[expected.requestSchema]
			require.Equal(t, "object", schema.Type)
			require.NotNil(t, schema.AdditionalProperties)
			require.False(t, *schema.AdditionalProperties)
			require.ElementsMatch(t, expected.required, schema.Required)
			require.ElementsMatch(t, expected.required, schemaPropertyNames(schema.Properties))
			require.Equal(t, "boolean", schema.Properties["confirmed"].Type)
			require.Equal(t, "string", schema.Properties["reason"].Type)
			require.GreaterOrEqual(t, schema.Properties["reason"].MinLength, 1)
			require.GreaterOrEqual(t, schema.Properties["reason"].MaxLength, schema.Properties["reason"].MinLength)
			require.Equal(t, "integer", schema.Properties["expected_authority_revision"].Type)
			require.Equal(t, int64(1), schema.Properties["expected_authority_revision"].Minimum)
			require.NotContains(t, schema.Properties, "command_id")
			require.NotContains(t, schema.Properties, "operator_id")
		})
	}

	noShow := document.Components.Schemas["ArenaOperatorNoShowRequest"]
	require.Equal(t, "array", noShow.Properties["game_result_revision_ids"].Type)
	require.Equal(t, 1, noShow.Properties["game_result_revision_ids"].MinItems)
	require.Equal(t, 3, noShow.Properties["game_result_revision_ids"].MaxItems)
	require.True(t, noShow.Properties["game_result_revision_ids"].UniqueItems)

	forfeit := document.Components.Schemas["ArenaOperatorForfeitRequest"]
	require.True(t, forfeit.Properties["expected_game"].Nullable)
	require.True(t, forfeit.Properties["game_result_revision_id"].Nullable)
	require.Equal(t, 1, forfeit.Properties["evidence_ids"].MinItems)
	require.Equal(t, 16, forfeit.Properties["evidence_ids"].MaxItems)
	require.True(t, forfeit.Properties["evidence_ids"].UniqueItems)
}

func loadArenaOperatorCommandDocument(t *testing.T) arenaOperatorCommandDocument {
	t.Helper()

	data, err := os.ReadFile(arenaRoutesPath)
	require.NoError(t, err)
	var document arenaOperatorCommandDocument
	require.NoError(t, yaml.Unmarshal(data, &document))
	return document
}

func loadArenaOpenAPIRoot(t *testing.T) arenaOpenAPIRoot {
	t.Helper()

	data, err := os.ReadFile(arenaOpenAPIPath)
	require.NoError(t, err)
	var root arenaOpenAPIRoot
	require.NoError(t, yaml.Unmarshal(data, &root))
	return root
}

func schemaPropertyNames(properties map[string]arenaOperatorCommandSchemaProperty) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
