package v1

import (
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const arenaSchemasPath = "../../../../../api/components/schemas/arena_schemas.yml"

type arenaSchemaSource map[string]arenaSchemaDefinition

type arenaSchemaDefinition struct {
	Type       string                         `yaml:"type"`
	Enum       []string                       `yaml:"enum"`
	Required   []string                       `yaml:"required"`
	Properties map[string]arenaSchemaProperty `yaml:"properties"`
	Items      *arenaSchemaProperty           `yaml:"items"`
	Minimum    *int64                         `yaml:"minimum"`
	MinItems   *int                           `yaml:"minItems"`
	MaxItems   *int                           `yaml:"maxItems"`
}

type arenaSchemaProperty struct {
	Type     string                `yaml:"type"`
	Format   string                `yaml:"format"`
	Ref      string                `yaml:"$ref"`
	Nullable bool                  `yaml:"nullable"`
	ReadOnly bool                  `yaml:"readOnly"`
	Minimum  *int64                `yaml:"minimum"`
	MinItems *int                  `yaml:"minItems"`
	MaxItems *int                  `yaml:"maxItems"`
	Items    *arenaSchemaProperty  `yaml:"items"`
	AllOf    []arenaSchemaProperty `yaml:"allOf"`
}

func TestArenaTournamentSchemaSource(t *testing.T) {
	schemas := loadArenaSchemaSource(t)

	requireArenaEnum(t, schemas, "ArenaPreset", []string{"arena_v1"})
	requireArenaEnum(t, schemas, "ArenaTournamentState", []string{
		"draft", "registration", "roster_locked", "swiss", "golden", "playoffs",
		"technical_pause", "completed", "cancelled",
	})
	requireArenaEnum(t, schemas, "ArenaAttendanceState", []string{
		"invited", "registered", "checked_in", "withdrawn",
	})
	requireArenaEnum(t, schemas, "ArenaParticipantReservationOwner", []string{
		"arena", "casual_queue", "casual_duel",
	})

	requireArenaObject(t, schemas, "ArenaTournament", []string{
		"id", "roster_id", "preset", "state", "revision", "roster_size", "created_at", "updated_at",
	})
	requireArenaRef(t, schemas, "ArenaTournament", "preset", "#/ArenaPreset")
	requireArenaRef(t, schemas, "ArenaTournament", "state", "#/ArenaTournamentState")
	requireArenaMinimum(t, schemas, "ArenaTournament", "revision", 1)
	requireArenaMinimum(t, schemas, "ArenaTournament", "roster_size", 4)
	requireArenaNullableRef(t, schemas, "ArenaTournament", "paused_from_state", "#/ArenaTournamentState")
	requireArenaNullable(t, schemas, "ArenaTournament", "started_at")
	requireArenaNullable(t, schemas, "ArenaTournament", "finished_at")

	requireArenaObject(t, schemas, "ArenaParticipant", []string{
		"id", "roster_id", "tournament_id", "player_id", "seed", "attendance", "created_at", "updated_at",
	})
	requireArenaMinimum(t, schemas, "ArenaParticipant", "seed", 1)
	requireArenaRef(t, schemas, "ArenaParticipant", "attendance", "#/ArenaAttendanceState")

	requireArenaObject(t, schemas, "ArenaRoster", []string{
		"id", "tournament_id", "revision", "participants", "locked", "execution_started", "created_at", "updated_at",
	})
	requireArenaMinimum(t, schemas, "ArenaRoster", "revision", 1)
	requireArenaArrayRef(t, schemas, "ArenaRoster", "participants", "#/ArenaParticipant", 0, 16)
	requireArenaNullable(t, schemas, "ArenaRoster", "locked_at")
	requireArenaNullable(t, schemas, "ArenaRoster", "execution_started_at")

	requireArenaObject(t, schemas, "ArenaParticipantReservation", []string{
		"player_id", "reservation_id", "owner_kind", "owner_id", "revision", "acquired_at", "updated_at",
	})
	requireArenaRef(t, schemas, "ArenaParticipantReservation", "owner_kind", "#/ArenaParticipantReservationOwner")
	requireArenaMinimum(t, schemas, "ArenaParticipantReservation", "revision", 1)

	requireArenaObject(t, schemas, "ArenaTournamentLifecycleRequest", []string{"expected_revision", "next_state"})
	requireArenaMinimum(t, schemas, "ArenaTournamentLifecycleRequest", "expected_revision", 1)
	requireArenaRef(t, schemas, "ArenaTournamentLifecycleRequest", "next_state", "#/ArenaTournamentState")
	requireArenaObject(t, schemas, "ArenaAttendanceChangeRequest", []string{
		"expected_attendance", "next_attendance",
	})
	requireArenaRef(t, schemas, "ArenaAttendanceChangeRequest", "expected_attendance", "#/ArenaAttendanceState")
	requireArenaRef(t, schemas, "ArenaAttendanceChangeRequest", "next_attendance", "#/ArenaAttendanceState")

	requireArenaObject(t, schemas, "ArenaRosterLockRequest", []string{
		"expected_revision", "preflight_revision_id", "checked_in_player_ids",
	})
	requireArenaMinimum(t, schemas, "ArenaRosterLockRequest", "expected_revision", 1)
	requireArenaUUIDArray(t, schemas, "ArenaRosterLockRequest", "checked_in_player_ids", 4, 16)
	requireArenaObject(t, schemas, "ArenaRosterUnlockRequest", []string{
		"expected_revision", "actor_id", "reason",
	})
	requireArenaMinimum(t, schemas, "ArenaRosterUnlockRequest", "expected_revision", 1)

	requireArenaObject(t, schemas, "ArenaPreflightCheck", []string{
		"code", "passed", "explanation", "evidence",
	})
	requireArenaRef(t, schemas, "ArenaPreflightCheck", "code", "#/ArenaPreflightCode")
	requireArenaObject(t, schemas, "ArenaPreflightSourceRevision", []string{"source", "value"})
	requireArenaObject(t, schemas, "ArenaPreflightReport", []string{
		"id", "tournament_id", "algorithm_version", "evaluated_at", "normalized_inputs",
		"revisions", "proof_hash", "checks", "passed",
	})
	requireArenaArrayRef(t, schemas, "ArenaPreflightReport", "revisions", "#/ArenaPreflightSourceRevision", 1, 0)
	requireArenaArrayRef(t, schemas, "ArenaPreflightReport", "checks", "#/ArenaPreflightCheck", 1, 0)

	requireArenaObject(t, schemas, "ArenaRevisionConflict", []string{
		"expected_revision", "current_revision",
	})
	requireArenaMinimum(t, schemas, "ArenaRevisionConflict", "expected_revision", 1)
	requireArenaMinimum(t, schemas, "ArenaRevisionConflict", "current_revision", 1)
}

func loadArenaSchemaSource(t *testing.T) arenaSchemaSource {
	t.Helper()

	data, err := os.ReadFile(arenaSchemasPath)
	require.NoError(t, err)

	var schemas arenaSchemaSource
	require.NoError(t, yaml.Unmarshal(data, &schemas))
	require.NotEmpty(t, schemas)
	return schemas
}

func requireArenaEnum(t *testing.T, schemas arenaSchemaSource, name string, expected []string) {
	t.Helper()
	schema := requireArenaSchema(t, schemas, name)
	require.Equal(t, "string", schema.Type, name)
	require.Equal(t, expected, schema.Enum, name)
}

func requireArenaObject(t *testing.T, schemas arenaSchemaSource, name string, required []string) {
	t.Helper()
	schema := requireArenaSchema(t, schemas, name)
	require.Equal(t, "object", schema.Type, name)
	for _, field := range required {
		require.Truef(t, slices.Contains(schema.Required, field), "%s must require %s", name, field)
		require.Contains(t, schema.Properties, field, name)
	}
}

func requireArenaSchema(t *testing.T, schemas arenaSchemaSource, name string) arenaSchemaDefinition {
	t.Helper()
	schema, ok := schemas[name]
	require.Truef(t, ok, "missing schema %s", name)
	return schema
}

func requireArenaProperty(t *testing.T, schemas arenaSchemaSource, schemaName, propertyName string) arenaSchemaProperty {
	t.Helper()
	schema := requireArenaSchema(t, schemas, schemaName)
	property, ok := schema.Properties[propertyName]
	require.Truef(t, ok, "%s is missing property %s", schemaName, propertyName)
	return property
}

func requireArenaRef(t *testing.T, schemas arenaSchemaSource, schemaName, propertyName, ref string) {
	t.Helper()
	property := requireArenaProperty(t, schemas, schemaName, propertyName)
	require.Equal(t, ref, property.Ref, schemaName+"."+propertyName)
}

func requireArenaNullableRef(t *testing.T, schemas arenaSchemaSource, schemaName, propertyName, ref string) {
	t.Helper()
	property := requireArenaProperty(t, schemas, schemaName, propertyName)
	require.Empty(t, property.Ref, schemaName+"."+propertyName+" must not use ignored $ref siblings")
	require.Len(t, property.AllOf, 1, schemaName+"."+propertyName)
	require.Equal(t, ref, property.AllOf[0].Ref, schemaName+"."+propertyName)
	require.True(t, property.Nullable, schemaName+"."+propertyName)
}

func requireArenaNullable(t *testing.T, schemas arenaSchemaSource, schemaName, propertyName string) {
	t.Helper()
	property := requireArenaProperty(t, schemas, schemaName, propertyName)
	require.True(t, property.Nullable, schemaName+"."+propertyName)
}

func requireArenaMinimum(t *testing.T, schemas arenaSchemaSource, schemaName, propertyName string, minimum int64) {
	t.Helper()
	property := requireArenaProperty(t, schemas, schemaName, propertyName)
	require.NotNil(t, property.Minimum, schemaName+"."+propertyName)
	require.Equal(t, minimum, *property.Minimum, schemaName+"."+propertyName)
}

func requireArenaArrayRef(
	t *testing.T,
	schemas arenaSchemaSource,
	schemaName string,
	propertyName string,
	ref string,
	minItems int,
	maxItems int,
) {
	t.Helper()
	property := requireArenaProperty(t, schemas, schemaName, propertyName)
	require.Equal(t, "array", property.Type, schemaName+"."+propertyName)
	require.NotNil(t, property.Items, schemaName+"."+propertyName)
	require.Equal(t, ref, property.Items.Ref, schemaName+"."+propertyName)
	if minItems > 0 {
		require.NotNil(t, property.MinItems, schemaName+"."+propertyName)
		require.Equal(t, minItems, *property.MinItems, schemaName+"."+propertyName)
	}
	if maxItems > 0 {
		require.NotNil(t, property.MaxItems, schemaName+"."+propertyName)
		require.Equal(t, maxItems, *property.MaxItems, schemaName+"."+propertyName)
	}
}

func requireArenaUUIDArray(
	t *testing.T,
	schemas arenaSchemaSource,
	schemaName string,
	propertyName string,
	minItems int,
	maxItems int,
) {
	t.Helper()
	property := requireArenaProperty(t, schemas, schemaName, propertyName)
	require.Equal(t, "array", property.Type, schemaName+"."+propertyName)
	require.NotNil(t, property.Items, schemaName+"."+propertyName)
	require.Equal(t, "uuid", property.Items.Format, schemaName+"."+propertyName)
	require.NotNil(t, property.MinItems, schemaName+"."+propertyName)
	require.NotNil(t, property.MaxItems, schemaName+"."+propertyName)
	require.Equal(t, minItems, *property.MinItems, schemaName+"."+propertyName)
	require.Equal(t, maxItems, *property.MaxItems, schemaName+"."+propertyName)
}
