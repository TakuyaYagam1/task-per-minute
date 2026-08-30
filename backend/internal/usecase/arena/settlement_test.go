package arena_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestConcurrentWinnerSettlement(t *testing.T) {
	t.Parallel()

	for iteration := 0; iteration < 20; iteration++ {
		now := time.Date(2026, 8, 30, 20, 30, 0, iteration, time.UTC)
		authority, commands := task037SettlementFixture(t, now)
		repository := newConcurrentWinnerRepositoryFake(authority, 2)
		results := make(chan concurrentWinnerResult, len(commands))
		var group sync.WaitGroup
		for _, command := range commands {
			group.Add(1)
			go func() {
				defer group.Done()
				settlement, changed, err := arena.NewConcurrentWinnerSettlementUseCase(
					repository,
					fixedArenaClock{now: now},
				).Settle(context.Background(), command)
				results <- concurrentWinnerResult{settlement: settlement, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)

		var observed []*arena.ConcurrentWinnerSettlement
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
		require.Equal(t, 1, repository.writeCount())
		require.Equal(t, observed[0], observed[1])

		winner := observed[0]
		require.Equal(t, int64(1), winner.WinningSubmission.Sequence)
		require.Equal(t, authority.Submissions[0].ParticipantID, winner.WinningSubmission.ParticipantID)
		require.Equal(t, domain.ArenaGameStateCompleted, winner.Game.State)
		require.Equal(t, domain.ArenaGameResultReasonSolved, winner.Game.ResultReason)
		require.Equal(t, winner.WinningSubmission.ParticipantID, *winner.Game.WinnerID)
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 1}, winner.ScoreRevision.ScoreAfter)
		require.Equal(t, authority.CurrentProjectionRevision+1, winner.Evidence.ProjectionRevision)
		require.NotEqual(t, winner.Evidence.AuditEventID, winner.Evidence.OutboxEventID)
	}
}

func TestConcurrentWinnerSettlementRejectsMissingWinnerOrInvalidEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 20, 45, 0, 0, time.UTC)

	t.Run("no correct submission", func(t *testing.T) {
		t.Parallel()

		authority, commands := task037SettlementFixture(t, now)
		for index := range authority.Submissions {
			authority.Submissions[index].Correct = false
		}
		repository := newConcurrentWinnerRepositoryFake(authority, 0)
		settlement, changed, err := arena.NewConcurrentWinnerSettlementUseCase(
			repository,
			fixedArenaClock{now: now},
		).Settle(t.Context(), commands[0])
		require.Nil(t, settlement)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrConcurrentWinnerUnavailable)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("duplicate evidence identity", func(t *testing.T) {
		t.Parallel()

		authority, commands := task037SettlementFixture(t, now)
		commands[0].OutboxEventID = commands[0].AuditEventID
		repository := newConcurrentWinnerRepositoryFake(authority, 0)
		settlement, changed, err := arena.NewConcurrentWinnerSettlementUseCase(
			repository,
			fixedArenaClock{now: now},
		).Settle(t.Context(), commands[0])
		require.Nil(t, settlement)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidConcurrentWinnerSettlement)
		require.Equal(t, 0, repository.writeCount())
	})
}

type concurrentWinnerResult struct {
	settlement *arena.ConcurrentWinnerSettlement
	changed    bool
	err        error
}

type concurrentWinnerRepositoryFake struct {
	mu        sync.Mutex
	authority arena.ConcurrentWinnerAuthority
	barrier   *sync.WaitGroup
	writes    int
}

func newConcurrentWinnerRepositoryFake(
	authority arena.ConcurrentWinnerAuthority,
	contenders int,
) *concurrentWinnerRepositoryFake {
	repository := &concurrentWinnerRepositoryFake{authority: authority}
	if contenders > 0 {
		repository.barrier = &sync.WaitGroup{}
		repository.barrier.Add(contenders)
	}
	return repository
}

func (r *concurrentWinnerRepositoryFake) LoadConcurrentWinnerAuthority(
	_ context.Context,
	_ arena.ArenaSubmissionScope,
) (arena.ConcurrentWinnerAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneConcurrentWinnerAuthority(r.authority), nil
}

func (r *concurrentWinnerRepositoryFake) CommitConcurrentWinnerSettlement(
	_ context.Context,
	settlement arena.ConcurrentWinnerSettlement,
) (*arena.ConcurrentWinnerSettlement, bool, error) {
	if r.barrier != nil {
		r.barrier.Done()
		r.barrier.Wait()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if settlement.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := cloneConcurrentWinnerSettlement(settlement)
	r.authority.Revision++
	r.authority.Current = &stored
	r.writes++
	return &stored, true, nil
}

func (r *concurrentWinnerRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func task037SettlementFixture(
	t *testing.T,
	now time.Time,
) (arena.ConcurrentWinnerAuthority, [2]arena.ConcurrentWinnerCommand) {
	t.Helper()

	submissionAuthority, _ := task037SubmissionFixture(t, now)
	snapshot := submissionAuthority.Snapshot.Snapshot()
	digest := submissionAuthority.Snapshot.ContentDigest()
	firstParticipant := submissionAuthority.StartedGame.ParticipantIDs[0]
	secondParticipant := submissionAuthority.StartedGame.ParticipantIDs[1]
	currentScoreRevisionID := domain.ArenaSeriesScoreRevisionID(task037ID(103))
	submissionAuthority.StartedGame.Series.Series.CurrentScoreRevisionID = &currentScoreRevisionID
	committedAt := now.Add(-time.Second)
	submissions := []arena.ArenaSubmissionRecord{
		{
			Scope: submissionAuthority.Scope, CommandID: task037ID(100),
			ParticipantID: firstParticipant, Sequence: 1, CommittedAt: committedAt,
			Correct: true, SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID,
			ContentDigest: digest,
		},
		{
			Scope: submissionAuthority.Scope, CommandID: task037ID(101),
			ParticipantID: secondParticipant, Sequence: 2, CommittedAt: committedAt,
			Correct: true, SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID,
			ContentDigest: digest,
		},
	}
	authority := arena.ConcurrentWinnerAuthority{
		Scope: submissionAuthority.Scope, Revision: 7,
		StartedGame: submissionAuthority.StartedGame, Submissions: submissions,
		CurrentScoreOrdinal: 3,
		CurrentGameResultRevisionIDs: []domain.ArenaOfficialResultRevisionID{
			domain.ArenaOfficialResultRevisionID(task037ID(102)),
		},
		CurrentProjectionRevision: 9,
	}
	commands := [2]arena.ConcurrentWinnerCommand{
		task037WinnerCommand(200), task037WinnerCommand(300),
	}
	commands[0].Scope = authority.Scope
	commands[1].Scope = authority.Scope
	return authority, commands
}

func task037WinnerCommand(base int) arena.ConcurrentWinnerCommand {
	return arena.ConcurrentWinnerCommand{
		GameResultRevisionID: domain.ArenaOfficialResultRevisionID(task037ID(base)),
		ScoreRevisionID:      domain.ArenaSeriesScoreRevisionID(task037ID(base + 1)),
		AuditEventID:         task037ID(base + 2),
		OutboxEventID:        task037ID(base + 3),
		ProjectionRevisionID: task037ID(base + 4),
	}
}

func cloneConcurrentWinnerAuthority(
	authority arena.ConcurrentWinnerAuthority,
) arena.ConcurrentWinnerAuthority {
	cloned := authority
	cloned.Submissions = append([]arena.ArenaSubmissionRecord(nil), authority.Submissions...)
	cloned.CurrentGameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	if authority.Current != nil {
		current := cloneConcurrentWinnerSettlement(*authority.Current)
		cloned.Current = &current
	}
	return cloned
}

func cloneConcurrentWinnerSettlement(
	settlement arena.ConcurrentWinnerSettlement,
) arena.ConcurrentWinnerSettlement {
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
		[]domain.ArenaOfficialResultRevisionID(nil),
		settlement.ScoreRevision.GameResultRevisionIDs...,
	)
	return cloned
}

var _ arena.ConcurrentWinnerRepository = (*concurrentWinnerRepositoryFake)(nil)
