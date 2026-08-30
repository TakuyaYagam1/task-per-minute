package arena_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestFailedAttemptTerminalization(t *testing.T) {
	t.Parallel()

	t.Run("commits void result, unchanged score, and Wave route together", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 0, 0, 0, time.UTC)
		authority, command := task040FailedAttemptFixture(t, now)
		repository := &failedAttemptRepositoryFake{authority: authority}
		usecase := arena.NewFailedAttemptUseCase(repository, fixedArenaClock{now: now})

		record, changed, err := usecase.Terminalize(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, record.Validate())
		require.Equal(t, domain.ArenaGameStateVoid, record.Game.State)
		require.Equal(t, domain.ArenaGameResultReasonNoSolve, record.Game.ResultReason)
		require.Nil(t, record.Game.WinnerID)
		require.Equal(t, authority.Series.Series.Score, record.ScoreRevision.ScoreBefore)
		require.Equal(t, authority.Series.Series.Score, record.ScoreRevision.ScoreAfter)
		require.Equal(t, domain.ArenaSeriesStateReplayRequired, record.Series.Series.State)
		require.Equal(t, command.Revisions.RouteEvidenceID, record.WaveRoute.ID)
		require.Equal(t, 1, repository.writeCount())

		repository.markWaveCompleted()
		repeated, changed, err := usecase.Terminalize(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, record, repeated)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("rejects a changed category cutoff without writing", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 5, 0, 0, time.UTC)
		authority, command := task040FailedAttemptFixture(t, now)
		command.Expected.Category = domain.CategoryCrypto
		repository := &failedAttemptRepositoryFake{authority: authority}

		record, changed, err := arena.NewFailedAttemptUseCase(
			repository,
			fixedArenaClock{now: now},
		).Terminalize(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrFailedAttemptConflict)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("concurrent duplicates commit one terminal record", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 10, 0, 0, time.UTC)
		authority, command := task040FailedAttemptFixture(t, now)
		barrier := &sync.WaitGroup{}
		barrier.Add(2)
		repository := &failedAttemptRepositoryFake{
			authority:   authority,
			loadBarrier: barrier,
		}
		results := make(chan failedAttemptResult, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				record, changed, err := arena.NewFailedAttemptUseCase(
					repository,
					fixedArenaClock{now: now},
				).Terminalize(context.Background(), command)
				results <- failedAttemptResult{record: record, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)

		changedCount := 0
		for result := range results {
			require.NoError(t, result.err)
			require.NotNil(t, result.record)
			require.NoError(t, result.record.Validate())
			if result.changed {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
		require.Equal(t, 1, repository.writeCount())
	})
}

type failedAttemptResult struct {
	record  *arena.FailedAttemptRecord
	changed bool
	err     error
}

type failedAttemptRepositoryFake struct {
	mu           sync.Mutex
	authority    arena.FailedAttemptAuthority
	loadBarrier  *sync.WaitGroup
	barrierLoads int
	writes       int
}

func (r *failedAttemptRepositoryFake) LoadFailedAttemptAuthority(
	_ context.Context,
	_ arena.FailedAttemptScope,
) (arena.FailedAttemptAuthority, error) {
	r.mu.Lock()
	authority := r.authority
	wait := r.loadBarrier != nil && r.barrierLoads < 2
	if wait {
		r.barrierLoads++
	}
	r.mu.Unlock()
	if wait {
		r.loadBarrier.Done()
		r.loadBarrier.Wait()
	}
	return authority, nil
}

func (r *failedAttemptRepositoryFake) CommitFailedAttempt(
	_ context.Context,
	record arena.FailedAttemptRecord,
) (*arena.FailedAttemptRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := record
	r.authority.Revision++
	r.authority.Series = stored.Series
	r.authority.CurrentOrdinal = stored.ScoreRevision.Ordinal
	r.authority.CurrentProjectionRevision = stored.Evidence.ProjectionRevision
	r.authority.CurrentGameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		stored.ScoreRevision.GameResultRevisionIDs...,
	)
	r.authority.Current = &stored
	r.writes++
	return &stored, true, nil
}

func (r *failedAttemptRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *failedAttemptRepositoryFake) markWaveCompleted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authority.Wave.State = domain.ArenaWaveStateCompleted
}

func task040FailedAttemptFixture(
	t *testing.T,
	now time.Time,
) (arena.FailedAttemptAuthority, arena.FailedAttemptCommand) {
	t.Helper()

	tournamentID := task040ID(1)
	waveID := task040ID(2)
	seriesID := task040ID(3)
	slotID := task040ID(4)
	gameID := task040ID(5)
	participants := [2]uuid.UUID{task040ID(6), task040ID(7)}
	currentScoreRevisionID := domain.ArenaSeriesScoreRevisionID(task040ID(8))
	series := arena.SeriesExecution{Series: domain.ArenaSeries{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: participants[0], SecondParticipantID: participants[1],
		Format: domain.ArenaSeriesFormatBO3, State: domain.ArenaSeriesStateActive,
		CurrentScoreRevisionID: &currentScoreRevisionID,
		Slots: []domain.ArenaGameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStateActive,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	wave := task040ActiveWave(t, tournamentID, waveID, participants, now)
	scope := arena.FailedAttemptScope{
		TournamentID: tournamentID, WaveID: waveID, SeriesID: seriesID,
		SlotID: slotID, GameID: gameID, AssignmentID: task040ID(9),
		AssignmentAttemptID: task040ID(10),
	}
	authority := arena.FailedAttemptAuthority{
		Scope: scope, Revision: 7, Wave: wave, Series: series,
		ActiveSnapshotID: task040ID(11), CurrentOrdinal: 1,
		CurrentProjectionRevision: 3,
	}
	command := arena.FailedAttemptCommand{
		Scope: scope, CommandID: task040ID(12), FailureClass: arena.NormalAttemptFailureNoSolve,
		Expected: arena.FailedAttemptExpectation{
			AttemptNo: 1, State: domain.ArenaGameStateActive,
			SnapshotID: authority.ActiveSnapshotID, Category: domain.CategoryWeb,
		},
		Revisions: arena.FailedAttemptRevisionSet{
			GameResultRevisionID: domain.ArenaOfficialResultRevisionID(task040ID(13)),
			ScoreRevisionID:      domain.ArenaSeriesScoreRevisionID(task040ID(14)),
			RouteEvidenceID:      task040ID(15), AuditEventID: task040ID(16),
			OutboxEventID: task040ID(17), ProjectionRevisionID: task040ID(18),
		},
	}
	return authority, command
}

func task040ActiveWave(
	t *testing.T,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	participants [2]uuid.UUID,
	now time.Time,
) domain.ArenaWave {
	t.Helper()

	wave := domain.ArenaWave{
		ID: waveID, TournamentID: tournamentID,
		RevisionID: domain.ArenaWaveRevisionID(task040ID(19)),
		State:      domain.ArenaWaveStatePlanned,
		Members: []domain.ArenaWaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
		},
	}
	openedAt := now.Add(-20 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		task040ID(20),
		domain.ArenaReadyWindowRevisionID(task040ID(21)),
		openedAt,
		now.Add(10*time.Second),
	))
	for _, participantID := range participants {
		_, err := wave.MarkReady(wave.ReadyWindow.ID, participantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	_, err := wave.Start(wave.ReadyWindow.ID, now.Add(-5*time.Second))
	require.NoError(t, err)
	require.NoError(t, wave.Validate())
	return wave
}

var _ arena.FailedAttemptRepository = (*failedAttemptRepositoryFake)(nil)
