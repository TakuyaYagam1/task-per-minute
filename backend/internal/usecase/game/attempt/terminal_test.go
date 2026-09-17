package attempt_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt/mocks"
)

func TestFailedAttemptTerminalization(t *testing.T) {
	t.Parallel()

	t.Run("commits void result, unchanged score, and Wave route together", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 0, 0, 0, time.UTC)
		authority, command := failedAttemptFixture(t, now)
		harness := newFailedAttemptRepositoryHarness(t, authority, nil)
		usecase := attemptusecase.AttemptNewUseCase(harness.repository, attemptNewGameClock(t, now))

		record, changed, err := usecase.Terminalize(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, record.Validate())
		require.Equal(t, domain.GameStateVoid, record.Game.State)
		require.Equal(t, domain.GameResultReasonNoSolve, record.Game.ResultReason)
		require.Nil(t, record.Game.WinnerID)
		require.Equal(t, authority.Series.Series.Score, record.ScoreRevision.ScoreBefore)
		require.Equal(t, authority.Series.Series.Score, record.ScoreRevision.ScoreAfter)
		require.Equal(t, domain.SeriesStateReplayRequired, record.Series.Series.State)
		require.Equal(t, command.Revisions.RouteEvidenceID, record.WaveRoute.ID)
		require.Equal(t, 1, harness.writeCount())

		harness.markWaveCompleted()
		repeated, changed, err := usecase.Terminalize(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, record, repeated)
		require.Equal(t, 1, harness.writeCount())
	})

	t.Run("rejects a changed category cutoff without writing", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 5, 0, 0, time.UTC)
		authority, command := failedAttemptFixture(t, now)
		command.Expected.Category = domain.CategoryCrypto
		harness := newFailedAttemptRepositoryHarness(t, authority, nil)

		record, changed, err := attemptusecase.AttemptNewUseCase(
			harness.repository,
			attemptNewGameClock(t, now),
		).Terminalize(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, attemptusecase.ErrFailedAttemptConflict)
		require.Equal(t, 0, harness.writeCount())
	})

	t.Run("concurrent duplicates commit one terminal record", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 10, 0, 0, time.UTC)
		authority, command := failedAttemptFixture(t, now)
		barrier := &sync.WaitGroup{}
		barrier.Add(2)
		harness := newFailedAttemptRepositoryHarness(t, authority, barrier)
		clock := attemptNewGameClock(t, now)
		results := make(chan failedAttemptResult, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				record, changed, err := attemptusecase.AttemptNewUseCase(
					harness.repository,
					clock,
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
		require.Equal(t, 1, harness.writeCount())
	})
}

type failedAttemptResult struct {
	record  *attemptusecase.AttemptRecord
	changed bool
	err     error
}

type failedAttemptRepositoryState struct {
	mu           sync.Mutex
	authority    attemptusecase.AttemptAuthority
	loadBarrier  *sync.WaitGroup
	barrierLoads int
	writes       int
}

type failedAttemptRepositoryHarness struct {
	repository *gamemocks.MockAttemptRepository
	state      *failedAttemptRepositoryState
}

func newFailedAttemptRepositoryHarness(
	t *testing.T,
	authority attemptusecase.AttemptAuthority,
	loadBarrier *sync.WaitGroup,
) *failedAttemptRepositoryHarness {
	t.Helper()

	state := &failedAttemptRepositoryState{
		authority:   authority,
		loadBarrier: loadBarrier,
	}
	repository := gamemocks.NewMockAttemptRepository(t)
	repository.EXPECT().LoadFailedAttemptAuthority(mock.Anything, authority.Scope).
		RunAndReturn(func(context.Context, domain.FailedAttemptScope) (attemptusecase.AttemptAuthority, error) {
			state.mu.Lock()
			loaded := state.authority
			wait := state.loadBarrier != nil && state.barrierLoads < 2
			if wait {
				state.barrierLoads++
			}
			state.mu.Unlock()
			if wait {
				state.loadBarrier.Done()
				state.loadBarrier.Wait()
			}
			return loaded, nil
		}).Maybe()
	repository.EXPECT().CommitFailedAttempt(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			record attemptusecase.AttemptRecord,
		) (*attemptusecase.AttemptRecord, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if record.ExpectedAuthorityRevision != state.authority.Revision ||
				state.authority.Current != nil {
				return nil, false, domain.ErrConflict
			}
			stored := record
			state.authority.Revision++
			state.authority.Series = stored.Series
			state.authority.CurrentOrdinal = stored.ScoreRevision.Ordinal
			state.authority.CurrentProjectionRevision = stored.Evidence.ProjectionRevision
			state.authority.CurrentGameResultRevisionIDs = append(
				[]domain.OfficialResultRevisionID(nil),
				stored.ScoreRevision.GameResultRevisionIDs...,
			)
			state.authority.Current = &stored
			state.writes++
			return &stored, true, nil
		}).Maybe()
	return &failedAttemptRepositoryHarness{repository: repository, state: state}
}

func (h *failedAttemptRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func (h *failedAttemptRepositoryHarness) markWaveCompleted() {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.authority.Wave.State = domain.WaveStateCompleted
}

func failedAttemptFixture(
	t *testing.T,
	now time.Time,
) (attemptusecase.AttemptAuthority, attemptusecase.AttemptCommand) {
	t.Helper()

	tournamentID := attemptFixtureID(1)
	waveID := attemptFixtureID(2)
	seriesID := attemptFixtureID(3)
	slotID := attemptFixtureID(4)
	gameID := attemptFixtureID(5)
	participants := [2]uuid.UUID{attemptFixtureID(6), attemptFixtureID(7)}
	currentScoreRevisionID := domain.SeriesScoreRevisionID(attemptFixtureID(8))
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: participants[0], SecondParticipantID: participants[1],
		Format: domain.SeriesFormatBO3, State: domain.SeriesStateActive,
		CurrentScoreRevisionID: &currentScoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	wave := attemptActiveWaveFixture(t, tournamentID, waveID, participants, now)
	scope := domain.FailedAttemptScope{
		TournamentID: tournamentID, WaveID: waveID, SeriesID: seriesID,
		SlotID: slotID, GameID: gameID, AssignmentID: attemptFixtureID(9),
		AssignmentAttemptID: attemptFixtureID(10),
	}
	authority := attemptusecase.AttemptAuthority{
		Scope: scope, Revision: 7, Wave: wave, Series: series,
		ActiveSnapshotID: attemptFixtureID(11), CurrentOrdinal: 1,
		CurrentProjectionRevision: 3,
	}
	command := attemptusecase.AttemptCommand{
		Scope: scope, CommandID: attemptFixtureID(12), FailureClass: gamedomain.FailureNoSolve,
		Expected: attemptusecase.Expectation{
			AttemptNo: 1, State: domain.GameStateActive,
			SnapshotID: authority.ActiveSnapshotID, Category: domain.CategoryWeb,
		},
		Revisions: attemptusecase.AttemptRevisionSet{
			GameResultRevisionID: domain.OfficialResultRevisionID(attemptFixtureID(13)),
			ScoreRevisionID:      domain.SeriesScoreRevisionID(attemptFixtureID(14)),
			RouteEvidenceID:      attemptFixtureID(15), AuditEventID: attemptFixtureID(16),
			OutboxEventID: attemptFixtureID(17), ProjectionRevisionID: attemptFixtureID(18),
		},
	}
	return authority, command
}

func attemptActiveWaveFixture(
	t *testing.T,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	participants [2]uuid.UUID,
	now time.Time,
) domain.Wave {
	t.Helper()

	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID,
		RevisionID: domain.WaveRevisionID(attemptFixtureID(19)),
		State:      domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
		},
	}
	openedAt := now.Add(-20 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		attemptFixtureID(20),
		domain.ReadyWindowRevisionID(attemptFixtureID(21)),
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

func attemptFixtureID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "task-040-%d", value))
}

func attemptNewGameClock(t *testing.T, now time.Time) *gamemocks.MockAttemptClock {
	t.Helper()
	clock := gamemocks.NewMockAttemptClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
