//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaWaveRepositorySerializesReadinessAndStart(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	players := createArenaMigrationPlayers(t, ctx, 2)
	participants := createSwissMigrationParticipants(t, ctx, rosterID, players)
	baseTime := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	repository := postgres.NewArenaWavePostgres(postgres.NewTxManager(sharedPool))
	waveID := uuid.New()

	wave, err := repository.Create(ctx, postgres.ArenaWaveCreateInput{
		ID: waveID, TournamentID: tournamentID, RosterID: rosterID,
		RevisionID: domain.ArenaWaveRevisionID(uuid.New()), ParticipantIDs: participants, CreatedAt: baseTime,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, wave.Revision)

	windowID := uuid.New()
	openedAt := baseTime.Add(time.Second)
	deadline := openedAt.Add(30 * time.Second)
	wave, changed, err := repository.OpenReadyWindow(ctx, tournamentID, waveID, wave.Revision, postgres.ArenaReadyWindowInput{
		ID: windowID, RevisionID: domain.ArenaReadyWindowRevisionID(uuid.New()), OpenedAt: openedAt, Deadline: deadline,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 2, wave.Revision)

	type readyResult struct {
		participantID uuid.UUID
		changed       bool
		err           error
	}
	readyStart := make(chan struct{})
	readyResults := make(chan readyResult, 2)
	var workers sync.WaitGroup
	for _, participantID := range participants {
		participantID := participantID
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-readyStart
			_, changed, markErr := repository.MarkReady(
				ctx, tournamentID, waveID, windowID, participantID, 2, openedAt.Add(time.Second),
			)
			readyResults <- readyResult{participantID: participantID, changed: changed, err: markErr}
		}()
	}
	close(readyStart)
	workers.Wait()
	close(readyResults)
	var staleParticipant uuid.UUID
	readyWinners := 0
	for item := range readyResults {
		require.NoError(t, item.err)
		if item.changed {
			readyWinners++
		} else {
			staleParticipant = item.participantID
		}
	}
	require.Equal(t, 1, readyWinners)
	require.NotEqual(t, uuid.Nil, staleParticipant)

	wave, changed, err = repository.MarkReady(
		ctx, tournamentID, waveID, windowID, staleParticipant, 3, openedAt.Add(2*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaWaveStateReady, wave.Wave.State)
	require.EqualValues(t, 4, wave.Revision)

	start := make(chan struct{})
	startResults := make(chan readyResult, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, started, startErr := repository.Start(
				ctx, tournamentID, waveID, windowID, 4, openedAt.Add(3*time.Second),
			)
			startResults <- readyResult{changed: started, err: startErr}
		}()
	}
	close(start)
	workers.Wait()
	close(startResults)
	startWinners := 0
	for item := range startResults {
		require.NoError(t, item.err)
		if item.changed {
			startWinners++
		}
	}
	require.Equal(t, 1, startWinners)

	loaded, err := repository.Get(ctx, tournamentID, waveID)
	require.NoError(t, err)
	require.Equal(t, domain.ArenaWaveStateActive, loaded.Wave.State)
	require.Equal(t, domain.ArenaReadyWindowStateConsumed, loaded.Wave.ReadyWindow.State)
	require.EqualValues(t, 5, loaded.Revision)
	loaded, changed, err = repository.Close(ctx, tournamentID, waveID, loaded.Revision, openedAt.Add(4*time.Second))
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaWaveStateCompleted, loaded.Wave.State)
}
