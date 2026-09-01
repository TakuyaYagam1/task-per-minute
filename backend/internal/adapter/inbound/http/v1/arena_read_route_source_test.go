package v1

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaReadRouteSource(t *testing.T) {
	routes, document := loadArenaRouteSource(t)

	publicOperations := []struct {
		path           string
		operationID    string
		responseSchema string
	}{
		{"/api/v1/arena/public/tournaments/{tournament_id}", "getArenaPublicTournament", "ArenaPublicTournamentResponse"},
		{"/api/v1/arena/public/tournaments/{tournament_id}/scoreboard", "getArenaPublicScoreboard", "ArenaPublicScoreboardResponse"},
		{"/api/v1/arena/public/tournaments/{tournament_id}/bracket", "getArenaPublicBracket", "ArenaPublicBracketResponse"},
		{"/api/v1/arena/public/tournaments/{tournament_id}/live-draft", "getArenaPublicLiveDraft", "ArenaPublicLiveDraftResponse"},
		{"/api/v1/arena/public/tournaments/{tournament_id}/snapshot", "getArenaPublicSnapshot", "ArenaPublicRecoverySnapshot"},
	}
	for _, expected := range publicOperations {
		operation := requireArenaRouteOperation(t, routes, expected.path, "get")
		require.Equal(t, expected.operationID, operation.OperationID)
		require.NotNil(t, operation.Security, operation.OperationID)
		require.Empty(t, *operation.Security, operation.OperationID)
		require.Equal(t, "public", operation.Scope)
		require.Equal(t, "#/components/schemas/"+expected.responseSchema, successResponseSchemaRef(t, operation))
		require.Contains(t, operation.Responses, "404")
	}

	publicAllowlists := map[string][]string{
		"ArenaPublicTournamentResponse": {"tournament_id", "preset", "state", "roster_size", "started_at", "finished_at", "projection_revision"},
		"ArenaPublicScoreboardEntry":    {"rank", "display_name", "points", "buchholz", "effective_time_ms"},
		"ArenaPublicScoreboardResponse": {"tournament_id", "projection_revision", "entries"},
		"ArenaPublicBracketMatch":       {"stage", "position", "first_display_name", "second_display_name", "score", "state"},
		"ArenaPublicBracketResponse":    {"tournament_id", "projection_revision", "matches"},
		"ArenaPublicDraftAction":        {"turn", "actor_display_name", "action", "category", "occurred_at"},
		"ArenaPublicLiveDraftResponse":  {"tournament_id", "series_id", "projection_revision", "format", "state", "pool", "actions", "selected_categories"},
		"ArenaPublicRecoveryCursor":     {"projection_revision", "event_sequence"},
		"ArenaPublicRecoverySnapshot":   {"tournament", "scoreboard", "bracket", "live_draft", "next_cursor"},
	}
	privateFields := []string{
		"assignment", "active_snapshot", "task", "task_url", "source_file_url", "submitted_flag",
		"content_digest", "redacted_payload", "actor_id", "participant_id", "player_id", "operator_id",
	}
	for schemaName, allowed := range publicAllowlists {
		schema := requireArenaRouteSchema(t, document, schemaName)
		require.NotNil(t, schema.AdditionalProperties, schemaName)
		require.False(t, *schema.AdditionalProperties, schemaName)
		for propertyName := range schema.Properties {
			require.Truef(t, slices.Contains(allowed, propertyName), "%s exposes %s", schemaName, propertyName)
		}
		for _, privateField := range privateFields {
			require.NotContains(t, schema.Properties, privateField, schemaName)
		}
	}

	audit := requireArenaRouteOperation(t, routes, "/api/v1/arena/operator/audit", "get")
	require.Equal(t, "listArenaOperatorAudit", audit.OperationID)
	requireArenaRouteSecurity(t, audit, "BearerAuth", "operator:tournament")
	parameterNames := resolveArenaParameterNames(t, document, audit.Parameters)
	for _, field := range []string{
		"tournament_id", "entity_kind", "entity_id", "event_type", "actor_kind", "actor_id",
		"result_reason", "occurred_from", "occurred_to", "cursor", "page_size",
	} {
		require.Truef(t, slices.Contains(parameterNames, field), "audit route is missing %s", field)
	}
	require.Equal(t,
		"../components/schemas/arena_schemas.yml#/ArenaAuditPage",
		successResponseSchemaRef(t, audit),
	)

	incident := requireArenaRouteOperation(t, routes, "/api/v1/arena/operator/tournaments/{tournament_id}/incident-export", "get")
	require.Equal(t, "exportArenaOperatorIncident", incident.OperationID)
	requireArenaRouteSecurity(t, incident, "BearerAuth", "operator:tournament")
	require.Equal(t,
		"../components/schemas/arena_schemas.yml#/ArenaIncidentBundle",
		successResponseSchemaRef(t, incident),
	)
	arenaSchemas := loadArenaSchemaSource(t)
	incidentSchema := requireArenaSchema(t, arenaSchemas, "ArenaIncidentBundle")
	for _, field := range []string{"generated_at", "sha256"} {
		require.Contains(t, incidentSchema.Required, field)
		require.Contains(t, incidentSchema.Properties, field)
	}

	recoveryOperations := []struct {
		path            string
		responseSchema  string
		cursorParameter string
	}{
		{"/api/v1/arena/public/tournaments/{tournament_id}/snapshot", "ArenaPublicRecoverySnapshot", "ArenaPublicCursor"},
		{"/api/v1/arena/tournaments/{tournament_id}/participant/snapshot", "ArenaParticipantRecoverySnapshot", "ArenaParticipantCursor"},
		{"/api/v1/arena/operator/tournaments/{tournament_id}/snapshot", "ArenaOperatorRecoverySnapshot", "ArenaOperatorCursor"},
	}
	seenResponses := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	for _, expected := range recoveryOperations {
		operation := requireArenaRouteOperation(t, routes, expected.path, "get")
		require.Equal(t, "#/components/schemas/"+expected.responseSchema, successResponseSchemaRef(t, operation))
		refs := arenaParameterRefs(operation.Parameters)
		require.True(t, slices.Contains(refs, "#/components/parameters/"+expected.cursorParameter), operation.OperationID)
		seenResponses[expected.responseSchema] = struct{}{}
		seenCursors[expected.cursorParameter] = struct{}{}
	}
	require.Len(t, seenResponses, 3)
	require.Len(t, seenCursors, 3)
}

func arenaParameterRefs(parameters []arenaRouteParameter) []string {
	refs := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		refs = append(refs, parameter.Ref)
	}
	return refs
}

func resolveArenaParameterNames(
	t *testing.T,
	document arenaRouteDocument,
	parameters []arenaRouteParameter,
) []string {
	t.Helper()

	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if parameter.Ref == "" {
			names = append(names, parameter.Name)
			continue
		}
		const prefix = "#/components/parameters/"
		require.True(t, len(parameter.Ref) > len(prefix) && parameter.Ref[:len(prefix)] == prefix, parameter.Ref)
		componentName := parameter.Ref[len(prefix):]
		component, ok := document.Components.Parameters[componentName]
		require.Truef(t, ok, "missing parameter %s", componentName)
		names = append(names, component.Name)
	}
	return names
}
