//go:build integration

package game_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	gamerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

func TestGameRepositoryUsesScopedCASAndStableAttemptHistory(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	players := createMigrationPlayers(ctx, t, 2)
	participants := createSwissMigrationParticipants(ctx, t, rosterID, players)
	seriesID := createMigrationSeries(ctx, t, tournamentID, rosterID, participants, "bo3")
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	repository := gamerepo.NewGamePostgres(postgres.NewTxManager(sharedPool))

	slotID := uuid.New()
	slot, err := repository.CreateSlot(ctx, gamerepo.GameSlotInput{
		Slot: domain.GameSlot{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
		},
		RosterID: rosterID, CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, slot.Revision)

	gameID := uuid.New()
	game := domain.Game{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStatePlanned}
	_, err = repository.AppendAttempt(ctx, gamerepo.GameAttemptInput{
		Game: game, SeriesID: seriesID, RosterID: rosterID, CreatedAt: createdAt,
	})
	require.NoError(t, err)
	_, err = repository.AppendAttempt(ctx, gamerepo.GameAttemptInput{
		Game: game, SeriesID: seriesID, RosterID: rosterID, CreatedAt: createdAt,
	})
	require.Error(t, err, "attempt identity and ordinal must remain unique")

	scope := gamedomain.Scope{TournamentID: tournamentID, SeriesID: seriesID, SlotID: slotID, GameID: gameID}
	next := game
	next.State = domain.GameStateReady
	type result struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, changed, transitionErr := repository.Transition(
				ctx, scope, 1, domain.GameStatePlanned, next, createdAt.Add(time.Second),
			)
			results <- result{changed: changed, err: transitionErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for item := range results {
		require.NoError(t, item.err)
		if item.changed {
			winners++
		}
	}
	require.Equal(t, 1, winners)

	loaded, err := repository.GetAttemptRecord(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateReady, loaded.Game.State)
	require.EqualValues(t, 2, loaded.Revision)
	loadedSlot, err := repository.GetSlot(ctx, tournamentID, seriesID, slotID)
	require.NoError(t, err)
	require.Len(t, loadedSlot.Attempts, 1)
	require.Equal(t, gameID, loadedSlot.Attempts[0].Game.ID)

	wrongScope := scope
	wrongScope.TournamentID = uuid.New()
	_, err = repository.GetAttemptRecord(ctx, wrongScope)
	require.ErrorIs(t, err, gamerepo.ErrGameNotFound)
}
