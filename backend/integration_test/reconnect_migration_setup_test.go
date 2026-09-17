//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/pauseseed"
)

func createReconnectMigrationFixture(
	ctx context.Context, tb testing.TB,
) reconnectMigrationFixture {
	tb.Helper()
	return createReconnectMigrationFixtureWithSlotLimit(ctx, tb, 2)
}

func createReconnectMigrationFixtureWithSlotLimit(
	ctx context.Context, tb testing.TB,
	slotLimit int,
) reconnectMigrationFixture {
	tb.Helper()

	draft := createDraftMigrationFixture(ctx, tb)
	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	_ = lockMigrationSeries(ctx, tb, draft, lockedAt)
	slotID := createMigrationGameSlot(ctx, tb, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveMigrationAttempt(
		ctx, tb,
		slotID,
		draft.seriesID,
		draft.rosterID,
		lockedAt.Add(time.Second),
	)
	presenceAt := lockedAt.Add(2 * time.Second)
	for _, participantID := range draft.participantIDs {
		_, err := sharedPool.Exec(
			ctx, `
			INSERT INTO presence_states (
				tournament_id, roster_id, series_id, participant_id,
				state, connected_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 'connected', $5, $5)`,
			draft.tournamentID,
			draft.rosterID,
			draft.seriesID,
			participantID,
			presenceAt,
		)
		require.NoError(tb, err)
	}

	rootPauseID, rootRevisionID := createMigrationPause(
		ctx, tb,
		draft,
		"series",
		draft.seriesID,
		nil,
		nil,
		0,
		"operator",
		"locked",
		presenceAt.Add(time.Second),
		slotLimit,
	)
	gamePauseID, gameRevisionID := createMigrationPause(
		ctx, tb,
		draft,
		"game_attempt",
		attemptID,
		&attemptID,
		&rootPauseID,
		1,
		"disconnect",
		"active",
		presenceAt.Add(2*time.Second),
		slotLimit,
	)

	return reconnectMigrationFixture{
		draft:               draft,
		attemptID:           attemptID,
		rootPauseID:         rootPauseID,
		rootPauseRevisionID: rootRevisionID,
		gamePauseID:         gamePauseID,
		gamePauseRevisionID: gameRevisionID,
		pausedAt:            presenceAt.Add(2 * time.Second),
	}
}

func createMigrationPause(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	scopeKind string,
	scopeID uuid.UUID,
	gameAttemptID *uuid.UUID,
	parentPauseID *uuid.UUID,
	depth int,
	reason string,
	pausedFromState string,
	pausedAt time.Time,
	slotLimit int,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	seed, err := pauseseed.CreatePause(ctx, sharedPool, pauseseed.Input{
		TournamentID:    draft.tournamentID,
		RosterID:        draft.rosterID,
		SeriesID:        draft.seriesID,
		ParticipantIDs:  draft.participantIDs,
		ScopeKind:       scopeKind,
		ScopeID:         scopeID,
		GameAttemptID:   gameAttemptID,
		ParentPauseID:   parentPauseID,
		Depth:           depth,
		Reason:          reason,
		PausedFromState: pausedFromState,
		PausedAt:        pausedAt,
		SlotLimit:       slotLimit,
	})
	require.NoError(tb, err)
	return seed.PauseID, seed.RevisionID
}
