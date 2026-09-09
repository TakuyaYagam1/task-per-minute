package game_test

import (
	"context"
	"crypto/sha256"
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
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func TestConcurrentWinnerSettlement(t *testing.T) {
	t.Parallel()

	for iteration := 0; iteration < 20; iteration++ {
		now := time.Date(2026, 8, 30, 20, 30, 0, iteration, time.UTC)
		authority, commands := settlementFixture(t, now)
		harness := newConcurrentWinnerRepositoryHarness(t, authority, 2)
		results := make(chan concurrentWinnerResult, len(commands))
		var group sync.WaitGroup
		for _, command := range commands {
			group.Add(1)
			go func() {
				defer group.Done()
				settlement, changed, err := gameusecase.SettlementNewUseCase(
					harness.repository,
				).Settle(context.Background(), command)
				results <- concurrentWinnerResult{settlement: settlement, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)

		var observed []*gameusecase.SettlementRecord
		var changedCount int
		for result := range results {
			require.NoError(t, result.err)
			require.NotNil(t, result.settlement)
			require.NoError(t, result.settlement.Validate())
			observed = append(observed, result.settlement)
			if result.changed {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
		require.Equal(t, 1, harness.writeCount())
		require.Equal(t, observed[0], observed[1])

		winner := observed[0]
		require.Equal(t, int64(1), winner.WinningSubmission.Sequence)
		require.Equal(t, winner.WinningSubmission.CommandID, winner.CommandID)
		require.Equal(t, winner.WinningSubmission.CommittedAt, winner.SettledAt)
		require.Equal(t, time.Second, winner.EffectiveSolveTime)
		require.Equal(t, authority.Submissions[0].ParticipantID, winner.WinningSubmission.ParticipantID)
		require.Equal(t, domain.GameStateCompleted, winner.Game.State)
		require.Equal(t, domain.GameResultReasonSolved, winner.Game.ResultReason)
		require.Equal(t, winner.WinningSubmission.ParticipantID, *winner.Game.WinnerID)
		require.Equal(t, domain.SeriesScore{FirstParticipantWins: 1}, winner.ScoreRevision.ScoreAfter)
		require.Equal(t, domain.SeriesStateActive, winner.Series.State)
		require.Nil(t, winner.Series.WinnerID)
		require.Nil(t, winner.Series.CurrentResultRevisionID)
		require.Equal(t, authority.CurrentProjectionRevision+1, winner.Evidence.ProjectionRevision)
		require.NotEqual(t, winner.Evidence.AuditEventID, winner.Evidence.OutboxEventID)
	}
}

func TestConcurrentWinnerSettlementCompletesWinningSeries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 40, 0, 0, time.UTC)
	authority, commands := settlementFixture(t, now)
	authority.StartedGame.Series.Series.Format = domain.SeriesFormatBO1
	harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)

	settled, changed, err := gameusecase.SettlementNewUseCase(harness.repository).Settle(t.Context(), commands[0])

	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, settled)
	require.NoError(t, settled.Validate())
	require.Equal(t, domain.SeriesStateCompleted, settled.Series.State)
	require.Equal(t, settled.WinningSubmission.ParticipantID, *settled.Series.WinnerID)
	require.NotNil(t, settled.Series.CurrentResultRevisionID)
	require.NotEqual(t, settled.SettlementGameResultRevision.ID, *settled.Series.CurrentResultRevisionID)
}

func TestConcurrentWinnerSettlementReplaysSameCommand(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 42, 0, 0, time.UTC)
	authority, commands := settlementFixture(t, now)
	harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)
	useCase := gameusecase.SettlementNewUseCase(harness.repository)

	first, changed, err := useCase.Settle(t.Context(), commands[0])
	require.NoError(t, err)
	require.True(t, changed)

	replay, changed, err := useCase.Settle(t.Context(), commands[0])
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, first, replay)
	require.Equal(t, 1, harness.writeCount())
}

func TestConcurrentWinnerSettlementUsesCommittedSubmissionTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 43, 0, 0, time.UTC)
	authority, commands := settlementFixture(t, now)
	harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)

	settled, changed, err := gameusecase.SettlementNewUseCase(harness.repository).Settle(t.Context(), commands[0])

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, authority.Submissions[0].CommittedAt, settled.SettledAt)
	require.False(t, settled.SettledAt.Before(settled.WinningSubmission.CommittedAt))
	require.Equal(t, time.Second, settled.EffectiveSolveTime)
}

func TestConcurrentWinnerSettlementRejectsMissingWinnerOrInvalidEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 45, 0, 0, time.UTC)

	t.Run("no correct submission", func(t *testing.T) {
		t.Parallel()

		authority, commands := settlementFixture(t, now)
		for index := range authority.Submissions {
			authority.Submissions[index].Correct = false
		}
		harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)
		settlement, changed, err := gameusecase.SettlementNewUseCase(harness.repository).Settle(t.Context(), commands[0])
		require.Nil(t, settlement)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrConcurrentWinnerUnavailable)
		require.Equal(t, 0, harness.writeCount())
	})

	t.Run("missing command identity", func(t *testing.T) {
		t.Parallel()

		authority, commands := settlementFixture(t, now)
		commands[0].CommandID = uuid.Nil
		harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)
		settlement, changed, err := gameusecase.SettlementNewUseCase(harness.repository).Settle(t.Context(), commands[0])
		require.Nil(t, settlement)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrInvalidConcurrentWinnerSettlement)
		require.Equal(t, 0, harness.writeCount())
	})

	t.Run("duplicate retained result revision", func(t *testing.T) {
		t.Parallel()

		authority, commands := settlementFixture(t, now)
		authority.CurrentGameResultRevisionIDs = append(
			authority.CurrentGameResultRevisionIDs,
			authority.CurrentGameResultRevisionIDs[0],
		)
		harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)
		settlement, changed, err := gameusecase.SettlementNewUseCase(harness.repository).Settle(t.Context(), commands[0])
		require.Nil(t, settlement)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrInvalidConcurrentWinnerSettlement)
		require.Equal(t, 0, harness.writeCount())
	})

	t.Run("duplicate settled score revision provenance", func(t *testing.T) {
		t.Parallel()

		authority, commands := settlementFixture(t, now)
		harness := newConcurrentWinnerRepositoryHarness(t, authority, 0)
		settlement, changed, err := gameusecase.SettlementNewUseCase(harness.repository).Settle(t.Context(), commands[0])
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, settlement)
		settlement.ScoreRevision.GameResultRevisionIDs = append(
			settlement.ScoreRevision.GameResultRevisionIDs,
			settlement.SettlementGameResultRevision.ID,
		)
		require.ErrorIs(t, settlement.Validate(), gameusecase.ErrInvalidConcurrentWinnerSettlement)
		require.Equal(t, 1, harness.writeCount())
	})
}

type concurrentWinnerResult struct {
	settlement *gameusecase.SettlementRecord
	changed    bool
	err        error
}

type concurrentWinnerRepositoryState struct {
	mu        sync.Mutex
	authority gameusecase.SettlementAuthority
	barrier   *sync.WaitGroup
	writes    int
}

type concurrentWinnerRepositoryHarness struct {
	repository *gamemocks.MockSettlementRepository
	state      *concurrentWinnerRepositoryState
}

func newConcurrentWinnerRepositoryHarness(
	t *testing.T,
	authority gameusecase.SettlementAuthority,
	contenders int,
) *concurrentWinnerRepositoryHarness {
	t.Helper()

	state := &concurrentWinnerRepositoryState{
		authority: cloneConcurrentWinnerAuthority(authority),
	}
	if contenders > 0 {
		state.barrier = &sync.WaitGroup{}
		state.barrier.Add(contenders)
	}
	repository := gamemocks.NewMockSettlementRepository(t)
	repository.EXPECT().LoadConcurrentWinnerAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			gamedomain.SubmissionScope,
		) (gameusecase.SettlementAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return cloneConcurrentWinnerAuthority(state.authority), nil
		}).Maybe()
	repository.EXPECT().CommitConcurrentWinnerSettlement(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			settlement gameusecase.SettlementRecord,
		) (*gameusecase.SettlementRecord, bool, error) {
			if state.barrier != nil {
				state.barrier.Done()
				state.barrier.Wait()
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if settlement.ExpectedAuthorityRevision != state.authority.Revision ||
				state.authority.Current != nil {
				return nil, false, domain.ErrConflict
			}
			stored := cloneConcurrentWinnerSettlement(settlement)
			state.authority.Revision++
			state.authority.Current = &stored
			state.writes++
			return &stored, true, nil
		}).Maybe()
	return &concurrentWinnerRepositoryHarness{repository: repository, state: state}
}

func (h *concurrentWinnerRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func settlementFixture(
	t *testing.T,
	now time.Time,
) (gameusecase.SettlementAuthority, [2]gameusecase.SettlementCommand) {
	t.Helper()

	tournamentID := settlementID(1)
	seriesID := settlementID(2)
	slotID := settlementID(3)
	gameID := settlementID(4)
	firstParticipant := settlementID(10)
	secondParticipant := settlementID(11)
	digest := sha256.Sum256([]byte("task-037-settlement"))
	snapshotID := settlementID(12)
	taskID := settlementID(13)
	gameScope := gamedomain.Scope{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
		SlotID:       slotID,
		GameID:       gameID,
	}
	scope := gamedomain.SubmissionScope{
		WaveID:       settlementID(5),
		Game:         gameScope,
		AssignmentID: settlementID(6),
	}
	currentScoreRevisionID := domain.SeriesScoreRevisionID(settlementID(103))
	startedGame := gamedomain.Started{
		Scope:          gameScope,
		ParticipantIDs: [2]uuid.UUID{firstParticipant, secondParticipant},
		Series: seriesdomain.Execution{Series: domain.Series{
			ID:                     seriesID,
			TournamentID:           tournamentID,
			FirstParticipantID:     firstParticipant,
			SecondParticipantID:    secondParticipant,
			Format:                 domain.SeriesFormatBO3,
			State:                  domain.SeriesStateActive,
			CurrentScoreRevisionID: &currentScoreRevisionID,
			Slots: []domain.GameSlot{{
				ID:          slotID,
				SeriesID:    seriesID,
				Position:    1,
				Category:    domain.CategoryWeb,
				ScoreBefore: domain.SeriesScore{},
				Attempts: []domain.Game{{
					ID:        gameID,
					SlotID:    slotID,
					AttemptNo: 1,
					State:     domain.GameStateActive,
				}},
			}},
		}},
		AssignmentID:       scope.AssignmentID,
		AssignmentRevision: 1,
		PlanRevisionID:     settlementID(7),
		SnapshotID:         snapshotID,
		ContentDigest:      digest,
		DeadlineSeconds:    90,
		StartedAt:          now.Add(-2 * time.Second),
		Deadline:           now.Add(88 * time.Second),
		DeliveryEnabled:    true,
	}
	committedAt := now.Add(-time.Second)
	submissions := []gamedomain.Submission{
		{
			Scope: scope, CommandID: settlementID(100),
			ParticipantID: firstParticipant, Sequence: 1, CommittedAt: committedAt,
			Correct: true, SnapshotID: snapshotID, TaskID: taskID,
			ContentDigest: digest,
		},
		{
			Scope: scope, CommandID: settlementID(101),
			ParticipantID: secondParticipant, Sequence: 2, CommittedAt: committedAt,
			Correct: true, SnapshotID: snapshotID, TaskID: taskID,
			ContentDigest: digest,
		},
	}
	authority := gameusecase.SettlementAuthority{
		Scope: scope, Revision: 7,
		StartedGame: startedGame, Submissions: submissions,
		CurrentScoreOrdinal: 3,
		CurrentGameResultRevisionIDs: []domain.OfficialResultRevisionID{
			domain.OfficialResultRevisionID(settlementID(102)),
		},
		CurrentProjectionRevision: 9,
	}
	commands := [2]gameusecase.SettlementCommand{
		winnerCommandFixture(200), winnerCommandFixture(300),
	}
	commands[0].Scope = authority.Scope
	commands[1].Scope = authority.Scope
	return authority, commands
}

func winnerCommandFixture(base int) gameusecase.SettlementCommand {
	return gameusecase.SettlementCommand{
		CommandID: settlementID(base),
	}
}

func settlementID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0037-%012d", value))
}

func cloneConcurrentWinnerAuthority(
	authority gameusecase.SettlementAuthority,
) gameusecase.SettlementAuthority {
	cloned := authority
	cloned.Submissions = append([]gamedomain.Submission(nil), authority.Submissions...)
	cloned.CurrentGameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	if authority.Current != nil {
		current := cloneConcurrentWinnerSettlement(*authority.Current)
		cloned.Current = &current
	}
	return cloned
}

func cloneConcurrentWinnerSettlement(
	settlement gameusecase.SettlementRecord,
) gameusecase.SettlementRecord {
	cloned := settlement
	cloned.Game = settlement.Game
	if settlement.Game.WinnerID != nil {
		winnerID := *settlement.Game.WinnerID
		cloned.Game.WinnerID = &winnerID
	}
	if settlement.Game.ResultRevisionID != nil {
		resultRevisionID := *settlement.Game.ResultRevisionID
		cloned.Game.ResultRevisionID = &resultRevisionID
	}
	cloned.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		settlement.ScoreRevision.GameResultRevisionIDs...,
	)
	cloned.Series = seriesdomain.CloneExecution(seriesdomain.Execution{Series: settlement.Series}).Series
	return cloned
}
