package correction_test

import (
	"context"
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
