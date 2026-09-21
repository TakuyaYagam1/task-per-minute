package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTournamentConfigurationLoadUsesPublishedStandingsAndParticipantSeeds(t *testing.T) {
	t.Parallel()

	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_configuration_edits.sql"))
	require.NoError(t, err)
	source := string(queries)
	standingsQuery := tournamentConfigurationQueryContract(t, source, "-- name: GetTournamentConfigurationEditPublishedStandings :one")
	participantsQuery := tournamentConfigurationQueryContract(t, source, "-- name: ListTournamentConfigurationEditParticipants :many")
	require.NotContains(t, standingsQuery, "FOR UPDATE")
	require.NotContains(t, participantsQuery, "FOR UPDATE")

	for _, fragment := range []string{
		"-- name: GetTournamentConfigurationEditPublishedStandings :one",
		"projection.id = sqlc.arg(projection_revision_id)::UUID",
		"projection.revision_number = sqlc.arg(expected_projection_revision)::BIGINT",
		"projection.state = 'published'",
		"standings.payload AS standings_payload",
		"-- name: ListTournamentConfigurationEditParticipants :many",
		"participant.seed",
		"participant.attendance",
		"ORDER BY participant.seed, participant.id",
		"AS round_participant",
		"SELECT link.bye_participant_id",
		"bye_participant_id",
		"bye_revision_id",
	} {
		require.Contains(t, source, fragment)
	}
}

func TestTournamentConfigurationReserveCountIsVersionedAndCopied(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..", "..", "..", "..")
	migration, err := os.ReadFile(filepath.Join(root, "db", "migrations", "000027_configurable_assignment_reserves.sql"))
	require.NoError(t, err)
	contentQueries, err := os.ReadFile(filepath.Join(root, "db", "queries", "task_content.sql"))
	require.NoError(t, err)
	editQueries, err := os.ReadFile(filepath.Join(root, "db", "queries", "tournament_configuration_edits.sql"))
	require.NoError(t, err)
	schema, err := os.ReadFile(filepath.Join(root, "api", "components", "schemas", "tournament_configuration_schemas.yml"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"ALTER TABLE public.tournament_content_configurations",
		"ALTER TABLE public.assignment_plans",
		"ALTER TABLE public.assignments",
		"ADD COLUMN reserve_count smallint NOT NULL DEFAULT 2",
		"ALTER COLUMN reserve_count SET DEFAULT 0",
		"CHECK (reserve_count BETWEEN 0 AND 2)",
	} {
		require.Contains(t, string(migration), fragment)
	}
	for _, fragment := range []string{
		"configuration.reserve_count",
		"sqlc.arg(reserve_count)",
	} {
		require.Contains(t, string(contentQueries), fragment)
	}
	for _, fragment := range []string{
		"sqlc.arg(reserve_count)::SMALLINT",
		"JOIN tournament_content_configurations AS current_configuration",
		"RETURNING id,\n    tournament_id,\n    revision,\n    state,\n    reserve_count",
	} {
		require.Contains(t, string(editQueries), fragment)
	}
	for _, fragment := range []string{
		"reserve_count:",
		"minimum: 0",
		"maximum: 2",
		"readOnly: true",
	} {
		require.Contains(t, string(schema), fragment)
	}
}

func TestTournamentConfigurationSwissByeCASRequiresExactPreStartFence(t *testing.T) {
	t.Parallel()

	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_configuration_edits.sql"))
	require.NoError(t, err)
	query := tournamentConfigurationQueryContract(t, string(queries), "-- name: UpdateTournamentConfigurationEditSwissWaveLinkByeCAS :one")

	for _, fragment := range []string{
		"SET bye_participant_id = sqlc.narg(next_bye_participant_id)::UUID",
		"bye_revision_id = sqlc.narg(next_bye_revision_id)::UUID",
		"link.bye_participant_id IS NOT DISTINCT FROM sqlc.narg(expected_bye_participant_id)::UUID",
		"link.bye_revision_id IS NOT DISTINCT FROM sqlc.narg(expected_bye_revision_id)::UUID",
		"round.revision = sqlc.arg(expected_round_revision)::BIGINT",
		"round.generation_kind = 'manual'",
		"round.lock_revision IS NULL",
		"round.locked_at IS NULL",
		"wave.state = 'planned'",
		"wave.started_at IS NULL",
		"wave.paused_at IS NULL",
		"wave.closed_at IS NULL",
		"FROM swiss_round_lock_proofs AS proof",
		"(sqlc.narg(expected_bye_participant_id)::UUID IS NULL) = (sqlc.narg(expected_bye_revision_id)::UUID IS NULL)",
		"(sqlc.narg(next_bye_participant_id)::UUID IS NULL) = (sqlc.narg(next_bye_revision_id)::UUID IS NULL)",
	} {
		require.Contains(t, query, fragment)
	}
	for _, forbidden := range []string{
		"DELETE FROM swiss_opponent_history",
		"DELETE FROM swiss_pairings",
		"DELETE FROM swiss_pairing_members",
	} {
		require.NotContains(t, query, forbidden)
	}
}

func TestTournamentConfigurationDetachesOnlySupersededPreStartWaveSeries(t *testing.T) {
	t.Parallel()

	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_configuration_edits.sql"))
	require.NoError(t, err)
	query := tournamentConfigurationQueryContract(t, string(queries), "-- name: DeleteTournamentConfigurationEditSupersededWaveSeriesCAS :one")

	for _, fragment := range []string{
		"DELETE FROM wave_series AS membership",
		"source.state = 'superseded'",
		"source.superseded_by_series_id = sqlc.arg(successor_series_id)::UUID",
		"wave.state = 'planned'",
		"wave.started_at IS NULL",
		"FROM wave_member_routes AS route",
		"FROM swiss_round_lock_proof_series AS proof_series",
	} {
		require.Contains(t, query, fragment)
	}
}

func tournamentConfigurationQueryContract(t *testing.T, queries, marker string) string {
	t.Helper()
	start := strings.Index(queries, marker)
	require.NotEqual(t, -1, start)
	remainder := queries[start+len(marker):]
	end := strings.Index(remainder, "-- name:")
	if end == -1 {
		return remainder
	}
	return remainder[:end]
}
