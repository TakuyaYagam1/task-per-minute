package golden_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"

	"github.com/stretchr/testify/require"
)

func TestGoldenFailureReplay(t *testing.T) {
	t.Parallel()

	failedAt := time.Date(2026, 9, 1, 19, 0, 0, 0, time.UTC)
	authority := task050GoldenFailureAuthority(t, failedAt, false)
	repository := newTask050FailureRepositoryHarness(t, authority)
	command := task050GoldenFailureReplayCommand(authority, 21000)

	record, changed, err := goldenusecase.NewGoldenFailureReplayUseCase(
		repository,
		failureNewGoldenClock(t, failedAt),
	).Replay(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, goldenusecase.GoldenFailureRouteReplay, record.Route)
	require.Equal(t, domain.GoldenAttemptStateVoid, record.Attempt.State)
	require.Equal(t, domain.WaveStateCompleted, record.OldWave.State)
	require.Equal(t, command.ClosedWaveRevisionID, record.OldWave.RevisionID)
	require.Equal(t, authority.Submissions.Expectation(), record.DiscardedSubmissions)
	require.Equal(t, authority.Submissions.ProvisionalOrder(), record.DiscardedOrder)
	require.Equal(t, authority.Positions, record.PriorPositions)
	require.Equal(t, authority.Positions, record.Positions)
	require.NotNil(t, record.Replacement)
	require.Nil(t, record.TechnicalPause)
	require.Equal(t, authority.Classification.NextEdge.Position, record.Replacement.Assignment.EdgePosition)
	require.Equal(t, authority.Classification.NextEdge.ID, record.Replacement.Assignment.EdgeID)
	require.Equal(t, authority.Classification.ParticipantIDs, record.Replacement.Attempt.ParticipantIDs)
	require.Equal(t, domain.GoldenAttemptStatePlanned, record.Replacement.Attempt.State)
	require.Equal(t, domain.WaveStateReadyWindowOpen, record.Replacement.Wave.State)
	require.Nil(t, record.Replacement.Wave.StartedAt)
	require.Empty(t, record.Replacement.Window.ReadyParticipantIDs)
	require.Equal(t, authority.Classification.ParticipantIDs, record.Replacement.Window.PresentParticipantIDs)
	require.Equal(t, failedAt, record.FailedAt)

	replayed, replayChanged, replayErr := goldenusecase.NewGoldenFailureReplayUseCase(
		repository,
		failureNewGoldenClock(t, failedAt.Add(time.Hour)),
	).Replay(t.Context(), command)
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)
	require.Equal(t, 1, repository.writeCount())

	reused := command
	reused.NextAttemptID = failureTask049ID(21990)
	result, reusedChanged, reusedErr := goldenusecase.NewGoldenFailureReplayUseCase(
		repository,
		failureNewGoldenClock(t, failedAt),
	).Replay(t.Context(), reused)
	require.Nil(t, result)
	require.False(t, reusedChanged)
	require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenFailureCommandReuse)

	record.Positions.Positions = append(record.Positions.Positions, goldenusecase.GoldenCommittedPosition{
		ParticipantID: failureTask049ID(21991),
	})
	stored := repository.currentSnapshot()
	require.Equal(t, authority.Positions, stored.Positions)

	concurrentAuthority := task050GoldenFailureAuthority(t, failedAt, false)
	concurrentRepository := newTask050FailureRepositoryHarness(t, concurrentAuthority)
	concurrentRepository.beforeCommit = failureTask049TwoPartyBarrier(t)
	concurrentCommand := task050GoldenFailureReplayCommand(concurrentAuthority, 26000)
	type concurrentFailureResult struct {
		record  *goldenusecase.GoldenFailureRecord
		changed bool
		err     error
	}
	clock := failureNewGoldenClock(t, failedAt)
	results := make(chan concurrentFailureResult, 2)
	for range 2 {
		go func() {
			record, changed, err := goldenusecase.NewGoldenFailureReplayUseCase(
				concurrentRepository, clock,
			).Replay(t.Context(), concurrentCommand)
			results <- concurrentFailureResult{record: record, changed: changed, err: err}
		}()
	}
	changedCount := 0
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		if result.changed {
			changedCount++
		}
	}
	require.Equal(t, 1, changedCount)
}

func TestGoldenFailureReplayValidation(t *testing.T) {
	t.Parallel()

	failedAt := time.Date(2026, 9, 1, 19, 30, 0, 0, time.UTC)
	base := task050GoldenFailureAuthority(t, failedAt, false)

	activeCases := []struct {
		name   string
		mutate func(*goldenusecase.GoldenFailureActiveExecution)
	}{
		{name: "Wave revision", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.WaveRevisionID = domain.WaveRevisionID(failureTask049ID(29600))
		}},
		{name: "ready window", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.Window.WindowID = failureTask049ID(29601)
		}},
		{name: "ready set digest", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.Window.ReadinessDigest = sha256.Sum256([]byte("foreign readiness"))
		}},
		{name: "presence set digest", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.Window.PresenceDigest = sha256.Sum256([]byte("foreign presence"))
		}},
		{name: "assignment revision", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.AssignmentRevisionID = failureTask049ID(29602)
		}},
		{name: "assignment revision number", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.AssignmentRevision++
		}},
		{name: "membership digest", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Expectation.MembershipDigest = sha256.Sum256([]byte("foreign membership"))
		}},
		{name: "group tournament", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Group.TournamentID = failureTask049ID(29603)
		}},
		{name: "Wave tournament", mutate: func(active *goldenusecase.GoldenFailureActiveExecution) {
			active.Wave.TournamentID = failureTask049ID(29604)
		}},
	}
	for _, test := range activeCases {
		t.Run("active_"+test.name, func(t *testing.T) {
			active := base.Active.Snapshot()
			test.mutate(&active)
			require.Error(t, active.Validate())
		})
	}

	authorityCases := []struct {
		name   string
		mutate func(*goldenusecase.GoldenFailureAuthority)
	}{
		{name: "full exact plan", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Plan.CreatedAt = authority.Plan.CreatedAt.Add(time.Nanosecond)
			task050SealExactPlanProof(t, &authority.Plan)
			require.NoError(t, authority.Plan.Validate())
		}},
		{name: "position range", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Positions.PositionTo++
			failureTask049SealPositionLedger(t, &authority.Positions)
			require.NoError(t, authority.Positions.Validate())
		}},
		{name: "foreign committed participant", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			foreignID := failureTask049ID(29610)
			authority.Positions.Positions[0].ParticipantID = foreignID
			authority.Positions.Attempts[0].Order[0].ParticipantID = foreignID
			failureTask049SealOrdering(t, &authority.Positions.Attempts[0])
			failureTask049SealPositionLedger(t, &authority.Positions)
			require.NoError(t, authority.Positions.Validate())
		}},
		{name: "inactive current submitter", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			inactiveID := authority.Active.Group.Attempts[0].ParticipantIDs[0]
			require.NotContains(t, authority.Active.ParticipantIDs, inactiveID)
			authority.Submissions.Submissions[0].ParticipantID = inactiveID
			authority.Submissions.Receipts[0].ParticipantID = inactiveID
			failureTask049SealSubmissionLedger(t, &authority.Submissions)
			require.NoError(t, authority.Submissions.Validate())
		}},
		{name: "stale submission assignment", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Submissions.Submissions[0].AssignmentDigest = sha256.Sum256([]byte("stale assignment"))
			failureTask049SealSubmissionLedger(t, &authority.Submissions)
			require.NoError(t, authority.Submissions.Validate())
		}},
		{name: "submission preflight", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Submissions.Submissions = make([]goldenusecase.GoldenSubmissionRecord, domain.TournamentMaxParticipants+1)
		}},
		{name: "receipt preflight", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Submissions.Receipts = make([]goldenusecase.GoldenSubmissionReceipt, domain.TournamentMaxParticipants*2+1)
		}},
		{name: "position preflight", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Positions.Positions = make([]goldenusecase.GoldenCommittedPosition, domain.TournamentMaxParticipants+1)
		}},
		{name: "attempt preflight", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Positions.Attempts = make(
				[]goldenusecase.GoldenAttemptOrderingEvidence,
				domain.AssignmentReserveCount+2,
			)
		}},
		{name: "plan nested preflight", mutate: func(authority *goldenusecase.GoldenFailureAuthority) {
			authority.Plan.Authority.Candidates = make(
				[]goldenusecase.TaskVersion,
				domain.TournamentMaxParticipants*(domain.AssignmentReserveCount+1)+1,
			)
		}},
	}
	for _, test := range authorityCases {
		t.Run("authority_"+test.name, func(t *testing.T) {
			authority := base.Snapshot()
			test.mutate(&authority)
			require.Error(t, authority.Validate())
		})
	}

	t.Run("dishonest unchanged return", func(t *testing.T) {
		sourceAuthority := task050GoldenFailureAuthority(t, failedAt, false)
		sourceRepository := newTask050FailureRepositoryHarness(t, sourceAuthority)
		oldCommand := task050GoldenFailureReplayCommand(sourceAuthority, 27000)
		oldRecord, changed, err := goldenusecase.NewGoldenFailureReplayUseCase(
			sourceRepository, failureNewGoldenClock(t, failedAt),
		).Replay(t.Context(), oldCommand)
		require.NoError(t, err)
		require.True(t, changed)

		authority := task050GoldenFailureAuthority(t, failedAt, false)
		repository := newTask050FailureRepositoryHarness(t, authority)
		repository.returnRecord = oldRecord
		command := task050GoldenFailureReplayCommand(authority, 27100)
		result, resultChanged, resultErr := goldenusecase.NewGoldenFailureReplayUseCase(
			repository, failureNewGoldenClock(t, failedAt),
		).Replay(t.Context(), command)
		require.Nil(t, result)
		require.False(t, resultChanged)
		require.ErrorIs(t, resultErr, domain.ErrInternal)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("commit error is atomic", func(t *testing.T) {
		authority := task050GoldenFailureAuthority(t, failedAt, false)
		repository := newTask050FailureRepositoryHarness(t, authority)
		repository.commitErr = errors.New("failure store unavailable")
		before := repository.authoritySnapshot()
		result, changed, err := goldenusecase.NewGoldenFailureReplayUseCase(
			repository, failureNewGoldenClock(t, failedAt),
		).Replay(t.Context(), task050GoldenFailureReplayCommand(authority, 27200))
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorContains(t, err, "failure store unavailable")
		require.Equal(t, before, repository.authoritySnapshot())
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("unchanged concurrent winner may have another captured time", func(t *testing.T) {
		authority := task050GoldenFailureAuthority(t, failedAt, false)
		command := task050GoldenFailureReplayCommand(authority, 27300)
		source := newTask050FailureRepositoryHarness(t, authority)
		winner, changed, err := goldenusecase.NewGoldenFailureReplayUseCase(
			source, failureNewGoldenClock(t, failedAt),
		).Replay(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		repository := newTask050FailureRepositoryHarness(t, authority)
		repository.returnRecord = winner
		result, resultChanged, resultErr := goldenusecase.NewGoldenFailureReplayUseCase(
			repository, failureNewGoldenClock(t, failedAt.Add(time.Nanosecond)),
		).Replay(t.Context(), command)
		require.NoError(t, resultErr)
		require.False(t, resultChanged)
		require.Equal(t, winner.PayloadDigest, result.PayloadDigest)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("canonical task pool larger than selected demand", func(t *testing.T) {
		authority := task050GoldenFailureAuthority(t, failedAt, false)
		authority = task050ExpandFailurePlanCandidates(t, authority, 49)
		require.Greater(t, len(authority.Plan.Authority.Candidates), 48)
		require.NoError(t, authority.Plan.Validate())
		require.NoError(t, authority.State.ExactPlan.Validate())
		require.NoError(t, authority.Validate())
	})
}
