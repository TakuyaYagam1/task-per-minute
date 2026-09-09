//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTournamentCreateReceiptInsertUsesTypedSnapshotParameters(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })
	prepareTournamentCreateReceiptContent(ctx, t)

	command := tournamentCreateReceiptCommand(time.Now().UTC().Truncate(time.Microsecond))
	result, err := newTournamentCreateReceiptStore().Create(ctx, command)
	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, command.TournamentID, result.Tournament.ID)
	require.Equal(t, command.RosterID, result.Tournament.RosterID)
	require.Equal(t, domain.TournamentStateDraft, result.Tournament.State)

	var schemaVersion int16
	var preset, state string
	var revision int64
	var rosterSize int32
	var changed bool
	err = sharedPool.QueryRow(ctx, `
		SELECT result_schema_version,
			result_preset,
			result_state,
			result_revision,
			result_roster_size,
			result_changed
		FROM tournament_create_command_receipts
		WHERE command_id = $1`, command.IdempotencyKey).
		Scan(&schemaVersion, &preset, &state, &revision, &rosterSize, &changed)
	require.NoError(t, err)
	require.EqualValues(t, 1, schemaVersion)
	require.Equal(t, string(domain.TournamentPresetV1), preset)
	require.Equal(t, string(domain.TournamentStateDraft), state)
	require.EqualValues(t, 1, revision)
	require.Zero(t, rosterSize)
	require.True(t, changed)
}
