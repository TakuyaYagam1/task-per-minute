package idempotent

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

func TestReplaceRosterReceiptUsesDurableCanonicalOrder(t *testing.T) {
	t.Parallel()

	command := idempotentReceiptReplaceRosterCommand()
	reordered := command
	reordered.Participants = []rosterusecase.RosterParticipantInput{
		command.Participants[3], command.Participants[2], command.Participants[1], command.Participants[0],
	}

	first, err := replaceRosterReceipt(command)
	require.NoError(t, err)
	second, err := replaceRosterReceipt(reordered)
	require.NoError(t, err)
	require.Equal(t, first, second)

	store := idempotencymocks.NewMockStore(t)
	store.EXPECT().Begin(mock.Anything, first, mock.Anything).
		Return(idempotency.BeginResult{Disposition: idempotency.BeginSucceeded}, nil).Once()
	replayed := false
	_, err = idempotency.Execute(context.Background(), idempotency.NewCoordinator(store), second, func(context.Context) (struct{}, error) {
		replayed = true
		return struct{}{}, nil
	})
	require.NoError(t, err)
	require.True(t, replayed)

	changed := command
	changed.ExpectedProjectionRevision++
	third, err := replaceRosterReceipt(changed)
	require.NoError(t, err)
	require.NotEqual(t, first.PayloadDigest, third.PayloadDigest)
}

func TestCorrectionReceiptUsesDurableCanonicalOrder(t *testing.T) {
	t.Parallel()

	command := idempotentReceiptCorrectionCommand()
	reordered := command
	reordered.Fields = []string{"winner_id", "reason"}
	reordered.ProjectionIntents = []correctionusecase.CorrectionProjectionIntent{
		command.ProjectionIntents[1], command.ProjectionIntents[0],
	}
	reordered.UnlockIntents = []correctionusecase.CorrectionUnlockIntent{
		command.UnlockIntents[1], command.UnlockIntents[0],
	}

	first, err := correctionReceipt(command)
	require.NoError(t, err)
	second, err := correctionReceipt(reordered)
	require.NoError(t, err)
	require.Equal(t, first, second)

	changed := command
	changed.Explanation = "different evidence"
	third, err := correctionReceipt(changed)
	require.NoError(t, err)
	require.NotEqual(t, first.PayloadDigest, third.PayloadDigest)
}

func TestTournamentActionReceiptEncodesEachSemanticField(t *testing.T) {
	t.Parallel()

	command := lifecycleusecase.TournamentActionCommand{
		CommandScope: operationusecase.CommandScope{
			Operator:     operationusecase.OperatorIdentity{ActorID: uuid.MustParse("7c000000-0000-4000-8000-000000000001")},
			TournamentID: uuid.MustParse("7c000000-0000-4000-8000-000000000002"),
			CommandID:    uuid.MustParse("7c000000-0000-4000-8000-000000000003"),
		},
		ExpectedProjectionRevision: 7,
		Action:                     lifecycleusecase.TournamentActionPause,
		Confirmed:                  true,
		Reason:                     "operator review",
	}

	first, err := tournamentActionReceipt(command)
	require.NoError(t, err)
	second, err := tournamentActionReceipt(command)
	require.NoError(t, err)
	require.Equal(t, first, second)

	changed := command
	changed.Reason = "other review"
	third, err := tournamentActionReceipt(changed)
	require.NoError(t, err)
	require.NotEqual(t, first.PayloadDigest, third.PayloadDigest)
}

func idempotentReceiptReplaceRosterCommand() rosterusecase.ReplaceRosterCommand {
	operatorID := uuid.MustParse("7a000000-0000-4000-8000-000000000001")
	tournamentID := uuid.MustParse("7a000000-0000-4000-8000-000000000002")
	return rosterusecase.NewReplaceRosterCommand(
		operationusecase.OperatorIdentity{ActorID: operatorID},
		tournamentID,
		uuid.MustParse("7a000000-0000-4000-8000-000000000003"),
		4,
		[]rosterusecase.RosterParticipantInput{
			{PlayerID: uuid.MustParse("7a000000-0000-4000-8000-000000000011"), Seed: 1, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: uuid.MustParse("7a000000-0000-4000-8000-000000000012"), Seed: 2, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: uuid.MustParse("7a000000-0000-4000-8000-000000000013"), Seed: 3, Attendance: domain.AttendanceStateCheckedIn},
			{PlayerID: uuid.MustParse("7a000000-0000-4000-8000-000000000014"), Seed: 4, Attendance: domain.AttendanceStateCheckedIn},
		},
	)
}

func idempotentReceiptCorrectionCommand() correctionusecase.CorrectionCommand {
	return correctionusecase.CorrectionCommand{
		CommandScope: operationusecase.CommandScope{
			Operator:     operationusecase.OperatorIdentity{ActorID: uuid.MustParse("7b000000-0000-4000-8000-000000000001")},
			TournamentID: uuid.MustParse("7b000000-0000-4000-8000-000000000002"),
			CommandID:    uuid.MustParse("7b000000-0000-4000-8000-000000000003"),
		},
		SeriesID:                   uuid.MustParse("7b000000-0000-4000-8000-000000000004"),
		GameID:                     uuid.MustParse("7b000000-0000-4000-8000-000000000005"),
		ExpectedProjectionRevision: 6,
		Confirmed:                  true,
		Reason:                     "official correction",
		Explanation:                "evidence reviewed",
		Fields:                     []string{"reason", "winner_id"},
		ProjectionIntents: []correctionusecase.CorrectionProjectionIntent{
			{ExpectedRevision: correctionusecase.ProjectionRevisionExpectation{ID: uuid.MustParse("7b000000-0000-4000-8000-000000000012")}},
			{ExpectedRevision: correctionusecase.ProjectionRevisionExpectation{ID: uuid.MustParse("7b000000-0000-4000-8000-000000000011")}},
		},
		UnlockIntents: []correctionusecase.CorrectionUnlockIntent{
			{ReservationID: uuid.MustParse("7b000000-0000-4000-8000-000000000022")},
			{ReservationID: uuid.MustParse("7b000000-0000-4000-8000-000000000021")},
		},
	}
}
