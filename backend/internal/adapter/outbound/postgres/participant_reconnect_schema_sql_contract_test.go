package postgres

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParticipantReconnectSchemaSQLContract(t *testing.T) {
	t.Parallel()

	schema := string(readParticipantReconnectMigration(t))
	for _, fragment := range []string{
		"-- +goose Up",
		"-- +goose Down",
		"CREATE TABLE public.participant_connection_leases",
		"connection_id uuid NOT NULL",
		"connection_generation bigint NOT NULL",
		"assignment_id uuid,",
		"series_id uuid,",
		"game_attempt_id uuid,",
		"state character varying(16) DEFAULT 'active'::character varying NOT NULL",
		"participant_connection_leases_uuid_check CHECK",
		"participant_connection_leases_binding_check CHECK",
		"participant_connection_leases_current_fence_key",
		"WHERE state = 'active'::character varying",
		"participant_connection_leases_active_participant_idx",
		"FOREIGN KEY (roster_id, tournament_id)",
		"REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT",
		"FOREIGN KEY (roster_id, participant_id)",
		"REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT",
		"FOREIGN KEY (player_id) REFERENCES public.players(id) ON DELETE RESTRICT",
		"FOREIGN KEY (assignment_id, game_attempt_id, roster_id)",
		"REFERENCES public.assignments(id, attempt_id, roster_id) ON DELETE RESTRICT",
		"FOREIGN KEY (series_id, tournament_id, roster_id)",
		"REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT",
		"FOREIGN KEY (game_attempt_id, series_id, roster_id)",
		"REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT",
		"participant_connection_lease_guard",
		"NEW.revision <> OLD.revision + 1",
		"disconnect_participant_connection_lease",
		"tournament_id = p_tournament_id",
		"roster_id = p_roster_id",
		"participant_id = p_participant_id",
		"connection_id = p_connection_id",
		"connection_generation = p_connection_generation",
		"state = 'active'::character varying",
		"CREATE TABLE public.reconnect_command_receipts",
		"command_id uuid NOT NULL",
		"CONSTRAINT reconnect_command_receipts_pkey PRIMARY KEY (command_id)",
		"wave_id uuid NOT NULL",
		"mutation_kind character varying(16) NOT NULL",
		"interval_id uuid,",
		"expected_authority_revision bigint NOT NULL",
		"result_authority_revision bigint NOT NULL",
		"schema_version smallint DEFAULT 1 NOT NULL",
		"record_document jsonb NOT NULL",
		"reconnect_command_receipts_uuid_check CHECK",
		"reconnect_command_receipts_mutation_check CHECK",
		"reconnect_command_receipts_revision_check CHECK",
		"reconnect_command_receipts_document_check CHECK",
		"jsonb_typeof(record_document) = 'object'::text",
		"reconnect_command_receipts_participant_idx",
		"reconnect_command_receipts_wave_idx",
		"FOREIGN KEY (wave_id, tournament_id, roster_id)",
		"REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT",
		"FOREIGN KEY (interval_id) REFERENCES public.reconnect_intervals(id) ON DELETE RESTRICT",
		"reconnect_command_receipt_guard",
		"Reconnect command receipts are immutable replay evidence",
		"BEFORE INSERT OR DELETE OR UPDATE ON public.reconnect_command_receipts",
	} {
		require.Contains(t, schema, fragment)
	}

	// Terminal disconnect may have no interval when the reconnect limit is
	// exhausted.  Keep the column nullable and never turn that case into a
	// mandatory interval foreign key.
	require.Contains(t, schema, "interval_id uuid,")
	require.NotContains(t, schema, "interval_id uuid NOT NULL")

	receiptStart := strings.Index(schema, "CREATE TABLE public.reconnect_command_receipts")
	receiptEndOffset := strings.Index(schema[receiptStart:], "CREATE INDEX reconnect_command_receipts_participant_idx")
	require.GreaterOrEqual(t, receiptStart, 0)
	require.Positive(t, receiptEndOffset)
	receiptDDL := schema[receiptStart : receiptStart+receiptEndOffset]
	for _, forbidden := range []string{
		"mutation_id",
		"player_id",
		"lease_id",
		"connection_id",
		"connection_generation",
	} {
		require.NotContainsf(t, receiptDDL, forbidden, "receipt schema must not own lease identity %q", forbidden)
	}
}

func TestParticipantReconnectMigrationHasUniqueGooseID(t *testing.T) {
	t.Parallel()

	migrationDir := filepath.Join("..", "..", "..", "..", "db", "migrations")
	entries, err := os.ReadDir(migrationDir)
	require.NoError(t, err)

	idPattern := regexp.MustCompile(`^([0-9]{6})_[^/]+\.sql$`)
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		match := idPattern.FindStringSubmatch(entry.Name())
		require.Lenf(t, match, 2, "migration filename %q must use the six-digit goose prefix", entry.Name())
		require.NotContainsf(t, seen, match[1], "migration ID %s is already used by %s", match[1], seen[match[1]])
		seen[match[1]] = entry.Name()
	}

	require.Equal(t, "000021_participant_reconnect_lifecycle.sql", seen["000021"])
	require.Equal(t, "000022_participant_connection_authority_recovery.sql", seen["000022"])
	require.Equal(t, "000023_reconnect_outbox_source.sql", seen["000023"])
}

func TestParticipantConnectionAuthorityRecoveryMigrationContract(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "..", "db", "migrations",
		"000022_participant_connection_authority_recovery.sql",
	))
	require.NoError(t, err)
	schema := string(contents)
	for _, fragment := range []string{
		"DROP TRIGGER participant_connection_leases_guard",
		"WITH inferred_authority AS",
		"evidence.renewed_at <= lease.connected_at",
		"lease.connected_at < evidence.expires_at",
		"CREATE TRIGGER participant_connection_leases_guard",
		"participant_connection_leases_authority_required_check",
		"authority_holder_id IS NOT NULL",
		"participant_connection_leases_authority_guard",
		"Participant connection lease authority stamp is required",
	} {
		require.Contains(t, schema, fragment)
	}
}

func readParticipantReconnectMigration(t *testing.T) []byte {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000021_participant_reconnect_lifecycle.sql"))
	require.NoError(t, err)
	return contents
}
