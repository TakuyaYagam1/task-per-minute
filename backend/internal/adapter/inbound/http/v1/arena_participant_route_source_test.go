package v1

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaParticipantRouteSource(t *testing.T) {
	routes, document := loadArenaRouteSource(t)

	readOperations := []struct {
		path           string
		operationID    string
		responseSchema string
	}{
		{"/api/v1/arena/tournaments/{tournament_id}/participant/lobby", "getArenaParticipantLobby", "ArenaParticipantLobbyResponse"},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/assignments/{assignment_id}", "getArenaParticipantAssignment", "ArenaParticipantAssignmentResponse"},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/snapshot", "getArenaParticipantSnapshot", "ArenaParticipantRecoverySnapshot"},
	}
	for _, expected := range readOperations {
		operation := requireArenaRouteOperation(t, routes, expected.path, "get")
		require.Equal(t, expected.operationID, operation.OperationID)
		requireArenaRouteSecurity(t, operation, "SessionTokenAuth", "participant:tournament")
		require.Equal(t, "#/components/schemas/"+expected.responseSchema, successResponseSchemaRef(t, operation))
		for _, status := range []string{"401", "403", "404"} {
			require.Contains(t, operation.Responses, status)
		}
	}

	mutationOperations := []struct {
		path          string
		operationID   string
		requestSchema string
		confirmed     bool
	}{
		{"/api/v1/arena/tournaments/{tournament_id}/participant/waves/{wave_id}/ready", "setArenaParticipantReady", "ArenaParticipantReadyRequest", false},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/series/{series_id}/draft/actions", "submitArenaParticipantDraftAction", "ArenaParticipantDraftActionRequest", false},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/series/{series_id}/games/{game_id}/submissions", "submitArenaParticipantFlag", "ArenaParticipantSubmissionRequest", false},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/series/{series_id}/surrender", "surrenderArenaParticipantSeries", "ArenaParticipantSurrenderRequest", true},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/series/{series_id}/post-series", "applyArenaParticipantPostSeriesAction", "ArenaParticipantPostSeriesRequest", false},
	}
	for _, expected := range mutationOperations {
		operation := requireArenaRouteOperation(t, routes, expected.path, "post")
		require.Equal(t, expected.operationID, operation.OperationID)
		requireArenaRouteSecurity(t, operation, "SessionTokenAuth", "participant:tournament")
		requireParticipantMutationParameters(t, operation)
		requireArenaRequestSchema(t, operation, expected.requestSchema)
		for _, status := range []string{"401", "403", "404", "409"} {
			require.Contains(t, operation.Responses, status)
		}

		requestSchema := requireArenaRouteSchema(t, document, expected.requestSchema)
		require.Contains(t, requestSchema.Required, "expected_projection_revision")
		require.NotContains(t, requestSchema.Properties, "command_id")
		if expected.confirmed {
			require.Contains(t, requestSchema.Required, "confirmed")
		}
		for _, forbidden := range []string{"participant_id", "player_id", "actor_id", "operator_id"} {
			require.NotContains(t, requestSchema.Properties, forbidden, expected.requestSchema)
		}
	}

	submission := requireArenaRouteSchema(t, document, "ArenaParticipantSubmissionRequest")
	require.True(t, submission.Properties["submitted_flag"].WriteOnly)
	require.NotContains(t, requireArenaRouteSchema(t, document, "ArenaParticipantSubmissionResponse").Properties, "submitted_flag")

	assignmentPath := "/api/v1/arena/tournaments/{tournament_id}/participant/assignments/{assignment_id}"
	assignmentOperation := requireArenaRouteOperation(t, routes, assignmentPath, "get")
	require.Equal(t,
		"#/components/schemas/ArenaParticipantAssignmentResponse",
		successResponseSchemaRef(t, assignmentOperation),
	)
	assignment := requireArenaRouteSchema(t, document, "ArenaParticipantAssignmentResponse")
	require.Contains(t, assignment.Properties, "assignment")
	require.Equal(t,
		"../components/schemas/arena_schemas.yml#/ArenaAssignment",
		assignment.Properties["assignment"].Ref,
	)
	require.True(t, document.Components.Parameters["ArenaIdempotencyKey"].Required)
	require.True(t, document.Components.Parameters["ArenaCSRFToken"].Required)
}

func requireParticipantMutationParameters(t *testing.T, operation arenaRouteOperation) {
	t.Helper()

	refs := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		refs = append(refs, parameter.Ref)
	}
	for _, ref := range []string{
		"#/components/parameters/ArenaIdempotencyKey",
		"#/components/parameters/ArenaCSRFToken",
	} {
		require.True(t, slices.Contains(refs, ref), operation.OperationID)
	}
}

func successResponseSchemaRef(t *testing.T, operation arenaRouteOperation) string {
	t.Helper()

	response, ok := operation.Responses["200"]
	require.Truef(t, ok, "%s is missing response 200", operation.OperationID)
	return response.Content["application/json"].Schema.Ref
}
