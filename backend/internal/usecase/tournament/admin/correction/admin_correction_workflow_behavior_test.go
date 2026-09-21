package correction_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	admincorrection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	correctionmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction/mocks"
)

func TestCorrectionWorkflowBuildsAndCommitsServerOwnedPlan(t *testing.T) {
	t.Parallel()

	command, authority, requestedAt := correctionWorkflowFixture(t)
	transactions, repository := correctionWorkflowMocks(t)
	repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
	repository.EXPECT().LockCorrectionAuthority(
		mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
	).Return(authority, nil)
	repository.EXPECT().ReadCorrectionTime(mock.Anything).Return(requestedAt, nil)
	repository.EXPECT().CommitCorrection(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			mutation admincorrection.CorrectionMutation,
		) (admincorrection.CorrectionEvidence, bool, error) {
			require.Equal(t, command, mutation.Command)
			require.Equal(t, authority.RosterID, mutation.Authority.RosterID)
			require.Equal(t, correctionWorkflowRequestDigest(t, command), mutation.RequestDigest)
			require.NoError(t, mutation.Plan.Validate())
			require.Equal(t, requestedAt, mutation.Plan.Audit().RequestedAt)
			require.Equal(t, command.CommandID, mutation.Evidence.CommandID)
			require.Equal(t, command.Fields, mutation.Evidence.Fields)
			require.Len(t, mutation.Plan.ProjectionRevisions(), len(command.ProjectionIntents))
			for index, projection := range mutation.Plan.ProjectionRevisions() {
				require.Equal(t, command.ProjectionIntents[index].PayloadDigest, projection.Revision().PayloadDigest())
			}
			return mutation.Evidence, true, nil
		})

	evidence, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
		Transactions: transactions, Repository: repository,
	}).CorrectGameResult(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, command.CommandID, evidence.CommandID)
	require.Equal(t, command.TournamentID, evidence.TournamentID)
	require.Equal(t, command.SeriesID, evidence.SeriesID)
	require.Equal(t, command.GameID, evidence.GameID)
	require.NotZero(t, evidence.ValidationDigest)
	require.Len(t, evidence.Supersessions, 6)
	require.Empty(t, evidence.UnlockIntents)
}

func TestCorrectionWorkflowPreparesDeterministicServerOwnedIntents(t *testing.T) {
	t.Parallel()

	command, authority, _ := correctionWorkflowFixture(t)
	command.ProjectionIntents = nil
	command.UnlockIntents = nil
	prepare := func() admincorrection.CorrectionCommand {
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)
		prepared, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).PrepareGameResultCorrection(t.Context(), command)
		require.NoError(t, err)
		return prepared
	}

	first, second := prepare(), prepare()
	require.Equal(t, first.SourceResultRevision, second.SourceResultRevision)
	require.Equal(t, first.ProjectionIntents, second.ProjectionIntents)
	require.Equal(t, first.UnlockIntents, second.UnlockIntents)
	require.Len(t, first.ProjectionIntents, 7)
	require.NotZero(t, first.ProjectionIntents[0].PayloadDigest)
	require.NotEqual(t, command.ProjectionIntents, first.ProjectionIntents)
}

func TestCorrectionWorkflowPreflightRejectsStaleSourceAndProjection(t *testing.T) {
	t.Parallel()

	t.Run("stale source", func(t *testing.T) {
		command, authority, _ := correctionWorkflowFixture(t)
		command.ProjectionIntents = nil
		command.UnlockIntents = nil
		command.SourceResultRevision = correctionWorkflowID(999)
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).PrepareGameResultCorrection(t.Context(), command)
		var conflict *admincorrection.CorrectionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, admincorrection.CorrectionRejectionStaleResult, conflict.Code)
	})

	t.Run("stale projection", func(t *testing.T) {
		command, authority, _ := correctionWorkflowFixture(t)
		command.ProjectionIntents = nil
		command.UnlockIntents = nil
		command.ExpectedProjectionRevision++
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).PrepareGameResultCorrection(t.Context(), command)
		var conflict *admincorrection.CorrectionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, admincorrection.CorrectionRejectionStaleProjection, conflict.Code)
	})
}

func TestCorrectionWorkflowRejectsIncompletePreparedIntentSetsAtomically(t *testing.T) {
	t.Parallel()

	command, authority, requestedAt := correctionWorkflowFixture(t)
	authority.Core.Reservations = []correctionusecase.Reservation{{
		ID: correctionWorkflowID(998), TournamentID: command.TournamentID,
		OwnerID: command.SeriesID, SourceRevisionID: authority.Core.GameResult.SourceProjection.ID(),
		Revision: 3, EvidenceDigest: sha256.Sum256([]byte("reserved task")),
	}}
	draft := command
	draft.ProjectionIntents = nil
	draft.UnlockIntents = nil
	prepareTransactions, prepareRepository := correctionWorkflowMocks(t)
	prepareRepository.EXPECT().LockCorrectionAuthority(
		mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
	).Return(authority, nil)
	prepared, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
		Transactions: prepareTransactions, Repository: prepareRepository,
	}).PrepareGameResultCorrection(t.Context(), draft)
	require.NoError(t, err)
	require.NotEmpty(t, prepared.ProjectionIntents)
	require.Len(t, prepared.UnlockIntents, 1)

	for _, testCase := range []struct {
		name string
		code admincorrection.CorrectionRejectionCode
		edit func(*admincorrection.CorrectionCommand)
	}{
		{
			name: "projection", code: admincorrection.CorrectionRejectionIncompleteProjection,
			edit: func(value *admincorrection.CorrectionCommand) {
				value.ProjectionIntents = value.ProjectionIntents[:len(value.ProjectionIntents)-1]
			},
		},
		{
			name: "unlock", code: admincorrection.CorrectionRejectionIncompleteUnlock,
			edit: func(value *admincorrection.CorrectionCommand) { value.UnlockIntents = nil },
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := prepared
			candidate.ProjectionIntents = append([]admincorrection.CorrectionProjectionIntent(nil), prepared.ProjectionIntents...)
			candidate.UnlockIntents = append([]admincorrection.CorrectionUnlockIntent(nil), prepared.UnlockIntents...)
			testCase.edit(&candidate)
			transactions, repository := correctionWorkflowMocks(t)
			repository.EXPECT().FindCorrectionCommand(mock.Anything, candidate.CommandID).Return(nil, nil)
			repository.EXPECT().LockCorrectionAuthority(
				mock.Anything, candidate.TournamentID, candidate.SeriesID, candidate.GameID,
			).Return(authority, nil)
			repository.EXPECT().ReadCorrectionTime(mock.Anything).Return(requestedAt, nil)

			_, correctionErr := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
				Transactions: transactions, Repository: repository,
			}).CorrectGameResult(t.Context(), candidate)
			var conflict *admincorrection.CorrectionConflictError
			require.ErrorAs(t, correctionErr, &conflict)
			require.Equal(t, testCase.code, conflict.Code)
		})
	}
}

func TestCorrectionWorkflowPreflightPreservesTerminalAndCutoffCodes(t *testing.T) {
	t.Parallel()

	t.Run("terminal", func(t *testing.T) {
		command, _, _ := correctionWorkflowFixture(t)
		command.ProjectionIntents = nil
		command.UnlockIntents = nil
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(admincorrection.CorrectionWorkflowAuthority{}, domain.ErrConflict)

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).PrepareGameResultCorrection(t.Context(), command)
		var conflict *admincorrection.CorrectionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, admincorrection.CorrectionRejectionTournamentTerminal, conflict.Code)
	})

	t.Run("wave cutoff", func(t *testing.T) {
		command, _, _ := correctionWorkflowFixture(t)
		command.ProjectionIntents = nil
		command.UnlockIntents = nil
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(admincorrection.CorrectionWorkflowAuthority{}, fmt.Errorf("wave_started: %w", correctionusecase.ErrCutoff))

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).PrepareGameResultCorrection(t.Context(), command)
		var conflict *admincorrection.CorrectionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, admincorrection.CorrectionRejectionCutoffWaveStarted, conflict.Code)
	})
}

func TestCorrectionWorkflowRollsBackPausedGoldenBeforeCommittingCorrection(t *testing.T) {
	t.Parallel()

	command, authority, requestedAt := correctionWorkflowFixture(t)
	authority.Stage = correctionWorkflowPausedGoldenStage(authority, requestedAt)
	transactions, repository := correctionWorkflowMocks(t)
	repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
	repository.EXPECT().LockCorrectionAuthority(
		mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
	).Return(authority, nil)
	repository.EXPECT().ReadCorrectionTime(mock.Anything).Return(requestedAt, nil)
	repository.EXPECT().CommitCorrection(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			mutation admincorrection.CorrectionMutation,
		) (admincorrection.CorrectionEvidence, bool, error) {
			require.Equal(t, correctionusecase.StageGoldenToPlayoff, mutation.Stage.Transition)
			require.True(t, mutation.Stage.CreatePlayoff)
			require.Len(t, mutation.Stage.GroupSupersessions, 1)
			require.Len(t, mutation.Stage.CancelledAttempts, 1)
			require.Equal(t, domain.GoldenAttemptStateCancelled, mutation.Stage.CancelledAttempts[0].State)
			require.Equal(t, requestedAt, *mutation.Stage.CancelledAttempts[0].FinishedAt)
			return mutation.Evidence, true, nil
		})

	_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
		Transactions: transactions, Repository: repository,
	}).CorrectGameResult(t.Context(), command)
	require.NoError(t, err)
}

func TestCorrectionWorkflowRejectsClientDigestMismatchWithoutMutation(t *testing.T) {
	t.Parallel()

	command, authority, requestedAt := correctionWorkflowFixture(t)
	command.ProjectionIntents[1].PayloadDigest[0] ^= 0xff
	transactions, repository := correctionWorkflowMocks(t)
	repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
	repository.EXPECT().LockCorrectionAuthority(
		mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
	).Return(authority, nil)
	repository.EXPECT().ReadCorrectionTime(mock.Anything).Return(requestedAt, nil)

	_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
		Transactions: transactions, Repository: repository,
	}).CorrectGameResult(t.Context(), command)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestCorrectionWorkflowReturnsDurableEvidenceOnExactReplay(t *testing.T) {
	t.Parallel()

	command, authority, requestedAt := correctionWorkflowFixture(t)
	evidence := admincorrection.CorrectionEvidence{
		CommandID: command.CommandID, TournamentID: command.TournamentID,
		SeriesID: command.SeriesID, GameID: command.GameID,
		OperatorID: command.Operator.ActorID, Reason: command.Reason,
		Fields: append([]string(nil), command.Fields...), RequestedAt: requestedAt,
		ValidationDigest: [32]byte{1}, Supersessions: []admincorrection.ProjectionSupersessionView{},
		UnlockIntents: []admincorrection.CorrectionUnlockIntent{},
	}
	transactions, repository := correctionWorkflowMocks(t)
	repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).
		Return(&admincorrection.CorrectionCommandRecord{
			CommandID: command.CommandID, TournamentID: command.TournamentID,
			RosterID: authority.RosterID, SeriesID: command.SeriesID, GameID: command.GameID,
			OperatorID:                 command.Operator.ActorID,
			ExpectedProjectionRevision: command.ExpectedProjectionRevision,
			RequestDigest:              correctionWorkflowRequestDigest(t, command), Evidence: evidence,
			ExecutedAt: requestedAt,
		}, nil)

	replayed, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
		Transactions: transactions, Repository: repository,
	}).CorrectGameResult(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, evidence, replayed)
}

func TestCorrectionWorkflowAcceptsRetainedConcurrentReplayOnlyWhenEvidenceMatches(t *testing.T) {
	t.Parallel()

	t.Run("exact retained evidence", func(t *testing.T) {
		t.Parallel()

		command, authority, requestedAt := correctionWorkflowFixture(t)
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)
		repository.EXPECT().ReadCorrectionTime(mock.Anything).Return(requestedAt, nil)
		repository.EXPECT().CommitCorrection(mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				mutation admincorrection.CorrectionMutation,
			) (admincorrection.CorrectionEvidence, bool, error) {
				return mutation.Evidence, false, nil
			})

		evidence, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).CorrectGameResult(t.Context(), command)
		require.NoError(t, err)
		require.Equal(t, command.CommandID, evidence.CommandID)
	})

	t.Run("retained other command payload", func(t *testing.T) {
		t.Parallel()

		command, authority, requestedAt := correctionWorkflowFixture(t)
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)
		repository.EXPECT().ReadCorrectionTime(mock.Anything).Return(requestedAt, nil)
		repository.EXPECT().CommitCorrection(mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				mutation admincorrection.CorrectionMutation,
			) (admincorrection.CorrectionEvidence, bool, error) {
				stored := mutation.Evidence
				stored.Reason = "different_reason"
				return stored, false, nil
			})

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).CorrectGameResult(t.Context(), command)
		var conflict *admincorrection.RevisionConflictError
		require.ErrorAs(t, err, &conflict)
	})
}

func TestCorrectionWorkflowRejectsCommandReuseAndStaleProjection(t *testing.T) {
	t.Parallel()

	t.Run("command reuse", func(t *testing.T) {
		t.Parallel()

		command, authority, requestedAt := correctionWorkflowFixture(t)
		evidence := correctionReplayEvidence(command, requestedAt)
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).
			Return(&admincorrection.CorrectionCommandRecord{
				CommandID: command.CommandID, TournamentID: command.TournamentID,
				RosterID: authority.RosterID, SeriesID: command.SeriesID, GameID: command.GameID,
				OperatorID:                 command.Operator.ActorID,
				ExpectedProjectionRevision: command.ExpectedProjectionRevision,
				RequestDigest:              [32]byte{0xff}, Evidence: evidence, ExecutedAt: requestedAt,
			}, nil)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).CorrectGameResult(t.Context(), command)
		var conflict *admincorrection.RevisionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, command.ExpectedProjectionRevision, conflict.ExpectedRevision)
		require.Equal(t, authority.ProjectionRevision, conflict.CurrentRevision)
	})

	t.Run("stale projection", func(t *testing.T) {
		t.Parallel()

		command, authority, _ := correctionWorkflowFixture(t)
		authority.ProjectionRevision++
		transactions, repository := correctionWorkflowMocks(t)
		repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
		repository.EXPECT().LockCorrectionAuthority(
			mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
		).Return(authority, nil)

		_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}).CorrectGameResult(t.Context(), command)
		var conflict *admincorrection.RevisionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, command.ExpectedProjectionRevision, conflict.ExpectedRevision)
		require.Equal(t, authority.ProjectionRevision, conflict.CurrentRevision)
	})
}

func TestCorrectionWorkflowRejectsCrossTournamentAuthority(t *testing.T) {
	t.Parallel()

	command, authority, _ := correctionWorkflowFixture(t)
	authority.Core.Series.TournamentID = correctionWorkflowID(999)
	transactions, repository := correctionWorkflowMocks(t)
	repository.EXPECT().FindCorrectionCommand(mock.Anything, command.CommandID).Return(nil, nil)
	repository.EXPECT().LockCorrectionAuthority(
		mock.Anything, command.TournamentID, command.SeriesID, command.GameID,
	).Return(authority, nil)

	_, err := admincorrection.NewCorrectionWorkflow(admincorrection.CorrectionWorkflowDependencies{
		Transactions: transactions, Repository: repository,
	}).CorrectGameResult(t.Context(), command)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func correctionReplayEvidence(
	command admincorrection.CorrectionCommand,
	requestedAt time.Time,
) admincorrection.CorrectionEvidence {
	return admincorrection.CorrectionEvidence{
		CommandID: command.CommandID, TournamentID: command.TournamentID,
		SeriesID: command.SeriesID, GameID: command.GameID,
		OperatorID: command.Operator.ActorID, Reason: command.Reason,
		Fields: append([]string(nil), command.Fields...), RequestedAt: requestedAt,
		ValidationDigest: [32]byte{1}, Supersessions: []admincorrection.ProjectionSupersessionView{},
		UnlockIntents: []admincorrection.CorrectionUnlockIntent{},
	}
}

func correctionWorkflowMocks(t *testing.T) (
	*correctionmocks.MockCorrectionTransactionManager,
	*correctionmocks.MockCorrectionWorkflowRepository,
) {
	t.Helper()
	transactions := correctionmocks.NewMockCorrectionTransactionManager(t)
	transactions.EXPECT().Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	return transactions, correctionmocks.NewMockCorrectionWorkflowRepository(t)
}
