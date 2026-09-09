package admin_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/mocks"
)

func TestOperatorResultWorkflowNoShowUsesDatabaseTimeAndOperatorScope(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	command, authority := operatorNoShowFixture(t, now)
	transactions, repository := operatorResultMocks(t)
	postseason := tournamentadminmocks.NewMockAdminPostseasonWorkflow(t)
	repository.EXPECT().LockOperatorResultAuthority(mock.Anything, command.TournamentID, command.SeriesID).
		Return(operatorWorkflowAuthority(command.CommandScope, command.SeriesID, command.ExpectedAuthorityRevision), nil)
	repository.EXPECT().FindOperatorResultCommand(mock.Anything, command.CommandID).Return(nil, nil)
	repository.EXPECT().ReadOperatorResultTime(mock.Anything).Return(now, nil)
	repository.EXPECT().LoadOperatorNoShowAuthority(mock.Anything, command).Return(authority, nil)

	var committed gameusecase.NoShowResolution
	repository.EXPECT().CommitOperatorNoShow(mock.Anything, command, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			_ tournamentadmin.NoShowCommand,
			digest [32]byte,
			resolution gameusecase.NoShowResolution,
		) (*gameusecase.NoShowResolution, bool, error) {
			require.NotEqual(t, [32]byte{}, digest)
			committed = resolution
			return &resolution, true, nil
		})
	postseason.EXPECT().AdvanceAfterSeriesSettlement(
		mock.Anything,
		playoff.TerminalSeriesCommand{TournamentID: command.TournamentID, SeriesID: command.SeriesID},
	).Return(playoff.TerminalReceipt{}, nil).Once()

	err := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: transactions, Repository: repository, Postseason: postseason,
	}).ResolveNoShow(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, now, committed.ResolvedAt)
	require.Equal(t, command.CommandID, committed.CommandID)
	require.Equal(t, command.ExpectedAuthorityRevision, committed.ExpectedAuthorityRevision)
	require.Equal(t, domain.SeriesStateCompleted, committed.Series.Series.State)
	require.Equal(t, authority.Series.Series.FirstParticipantID, *committed.Series.Series.WinnerID)
}

func TestOperatorResultWorkflowForfeitRetainsEvidenceWithoutInventingGameResult(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	command, authority := operatorForfeitFixture()
	transactions, repository := operatorResultMocks(t)
	postseason := tournamentadminmocks.NewMockAdminPostseasonWorkflow(t)
	repository.EXPECT().LockOperatorResultAuthority(mock.Anything, command.TournamentID, command.SeriesID).
		Return(operatorWorkflowAuthority(command.CommandScope, command.SeriesID, command.ExpectedAuthorityRevision), nil)
	repository.EXPECT().FindOperatorResultCommand(mock.Anything, command.CommandID).Return(nil, nil)
	repository.EXPECT().ReadOperatorResultTime(mock.Anything).Return(now, nil)
	repository.EXPECT().LoadOperatorForfeitAuthority(mock.Anything, command).Return(authority, nil)

	var committed gameusecase.ForfeitResolution
	repository.EXPECT().CommitOperatorForfeit(mock.Anything, command, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			_ tournamentadmin.ForfeitCommand,
			digest [32]byte,
			resolution gameusecase.ForfeitResolution,
		) (*gameusecase.ForfeitResolution, bool, error) {
			require.NotEqual(t, [32]byte{}, digest)
			committed = resolution
			return &resolution, true, nil
		})
	postseason.EXPECT().AdvanceAfterSeriesSettlement(
		mock.Anything,
		playoff.TerminalSeriesCommand{TournamentID: command.TournamentID, SeriesID: command.SeriesID},
	).Return(playoff.TerminalReceipt{}, nil).Once()

	err := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: transactions, Repository: repository, Postseason: postseason,
	}).RecordForfeit(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, now, committed.ResolvedAt)
	require.Equal(t, command.Operator.ActorID, committed.ActorID)
	require.Equal(t, command.ForfeitingParticipantID, committed.ForfeitingParticipantID)
	require.Nil(t, committed.Game)
	require.Nil(t, committed.GameRevision)
	require.Equal(t, command.Reason, committed.OperatorEvidence.Reason)
	require.Equal(t, command.RuleID, committed.OperatorEvidence.RuleID)
	require.Equal(t, command.EvidenceIDs, committed.OperatorEvidence.EvidenceIDs)
}

func TestOperatorResultWorkflowExactReplaySkipsMutation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)
	command, _ := operatorNoShowFixture(t, now)
	digest := operatorResultTestDigest(t, tournamentadmin.OperatorResultActionNoShow, command)
	transactions, repository := operatorResultMocks(t)
	postseason := tournamentadminmocks.NewMockAdminPostseasonWorkflow(t)
	repository.EXPECT().LockOperatorResultAuthority(mock.Anything, command.TournamentID, command.SeriesID).
		Return(operatorWorkflowAuthority(command.CommandScope, command.SeriesID, command.ExpectedAuthorityRevision+1), nil)
	repository.EXPECT().FindOperatorResultCommand(mock.Anything, command.CommandID).
		Return(&tournamentadmin.OperatorResultCommandRecord{
			CommandScope: command.CommandScope, SeriesID: command.SeriesID,
			Action:                    tournamentadmin.OperatorResultActionNoShow,
			ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
			RequestDigest:             digest, CommitID: operatorResultTestID(900),
			ResultEventID: operatorResultTestID(901), ExecutedAt: now,
		}, nil)

	err := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: transactions, Repository: repository, Postseason: postseason,
	}).ResolveNoShow(t.Context(), command)
	require.NoError(t, err)
}

func TestOperatorResultWorkflowRejectsCommandReuse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 15, 0, 0, 0, time.UTC)
	command, _ := operatorNoShowFixture(t, now)
	transactions, repository := operatorResultMocks(t)
	postseason := tournamentadminmocks.NewMockAdminPostseasonWorkflow(t)
	authority := operatorWorkflowAuthority(command.CommandScope, command.SeriesID, command.ExpectedAuthorityRevision+1)
	repository.EXPECT().LockOperatorResultAuthority(mock.Anything, command.TournamentID, command.SeriesID).
		Return(authority, nil)
	repository.EXPECT().FindOperatorResultCommand(mock.Anything, command.CommandID).
		Return(&tournamentadmin.OperatorResultCommandRecord{
			CommandScope: command.CommandScope, SeriesID: command.SeriesID,
			Action:                    tournamentadmin.OperatorResultActionForfeit,
			ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
			RequestDigest:             operatorResultTestDigest(t, tournamentadmin.OperatorResultActionNoShow, command),
			CommitID:                  operatorResultTestID(910), ResultEventID: operatorResultTestID(911), ExecutedAt: now,
		}, nil)

	err := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: transactions, Repository: repository, Postseason: postseason,
	}).ResolveNoShow(t.Context(), command)
	var conflict *tournamentadmin.RevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, command.ExpectedAuthorityRevision, conflict.ExpectedRevision)
	require.Equal(t, authority.AuthorityRevision, conflict.CurrentRevision)
}

func operatorResultMocks(t *testing.T) (
	*tournamentadminmocks.MockOperatorResultTransactionManager,
	*tournamentadminmocks.MockOperatorResultWorkflowRepository,
) {
	t.Helper()
	transactions := tournamentadminmocks.NewMockOperatorResultTransactionManager(t)
	transactions.EXPECT().Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	return transactions, tournamentadminmocks.NewMockOperatorResultWorkflowRepository(t)
}

func operatorWorkflowAuthority(
	scope tournamentadmin.CommandScope,
	seriesID uuid.UUID,
	revision int64,
) tournamentadmin.OperatorResultAuthority {
	return tournamentadmin.OperatorResultAuthority{
		TournamentID: scope.TournamentID, RosterID: operatorResultTestID(2), SeriesID: seriesID,
		TournamentState: domain.TournamentStateSwiss, AuthorityRevision: revision,
		ProjectionRevisionID: operatorResultTestID(3), ProjectionRevision: 7,
	}
}

func operatorNoShowFixture(
	t *testing.T,
	now time.Time,
) (tournamentadmin.NoShowCommand, gameusecase.NoShowAuthority) {
	t.Helper()
	tournamentID := operatorResultTestID(1)
	firstParticipantID := operatorResultTestID(10)
	secondParticipantID := operatorResultTestID(11)
	wave := domain.Wave{
		ID: operatorResultTestID(20), TournamentID: tournamentID,
		RevisionID: domain.WaveRevisionID(operatorResultTestID(21)), State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{{ParticipantID: firstParticipantID}, {ParticipantID: secondParticipantID}},
	}
	windowID := operatorResultTestID(22)
	windowRevisionID := domain.ReadyWindowRevisionID(operatorResultTestID(23))
	openedAt := now.Add(-time.Minute)
	require.NoError(t, wave.OpenReadyWindow(windowID, windowRevisionID, openedAt, now.Add(-time.Second)))
	_, err := wave.MarkReady(windowID, firstParticipantID, openedAt.Add(time.Second))
	require.NoError(t, err)
	seriesID := operatorResultTestID(30)
	slotID := operatorResultTestID(31)
	gameID := operatorResultTestID(32)
	currentScoreRevisionID := domain.SeriesScoreRevisionID(operatorResultTestID(33))
	execution := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateReady,
		CurrentScoreRevisionID: &currentScoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			Attempts: []domain.Game{{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStatePlanned}},
		}},
	}}
	require.NoError(t, execution.Validate())
	scope := tournamentadmin.CommandScope{
		Operator:     tournamentadmin.OperatorIdentity{ActorID: operatorResultTestID(40)},
		TournamentID: tournamentID, CommandID: operatorResultTestID(41),
	}
	command := tournamentadmin.NoShowCommand{
		CommandScope: scope, WaveID: wave.ID, WindowID: windowID, SeriesID: seriesID,
		Confirmed: true, Reason: "ready window expired", ExpectedAuthorityRevision: 4,
		ExpectedWaveRevisionID:   wave.RevisionID.UUID(),
		ExpectedWindowRevisionID: windowRevisionID.UUID(), ExpectedSeriesState: domain.SeriesStateReady,
		GameResultRevisionIDs: []uuid.UUID{operatorResultTestID(42)},
		ScoreRevisionID:       operatorResultTestID(43), SeriesResultRevisionID: operatorResultTestID(44),
	}
	return command, gameusecase.NoShowAuthority{
		Scope: domain.NormalNoShowScope{
			TournamentID: tournamentID, WaveID: wave.ID, WindowID: windowID, SeriesID: seriesID,
		},
		Revision: 4, Wave: wave, Series: execution, CurrentOrdinal: 1,
	}
}

func operatorForfeitFixture() (tournamentadmin.ForfeitCommand, gameusecase.ForfeitAuthority) {
	tournamentID := operatorResultTestID(101)
	seriesID := operatorResultTestID(102)
	firstParticipantID := operatorResultTestID(103)
	secondParticipantID := operatorResultTestID(104)
	slotID := operatorResultTestID(105)
	gameID := operatorResultTestID(106)
	currentScoreRevisionID := domain.SeriesScoreRevisionID(operatorResultTestID(107))
	execution := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateReady,
		CurrentScoreRevisionID: &currentScoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryCrypto,
			Attempts: []domain.Game{{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStatePlanned}},
		}},
	}}
	operatorID := operatorResultTestID(108)
	command := tournamentadmin.ForfeitCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: operatorID},
			TournamentID: tournamentID, CommandID: operatorResultTestID(109),
		},
		SeriesID: seriesID, ForfeitingParticipantID: secondParticipantID,
		Confirmed: true, Reason: "participant used prohibited tooling", ExpectedAuthorityRevision: 5,
		ExpectedGame: &tournamentadmin.GameExpectation{
			SlotID: slotID, GameID: gameID, AttemptNo: 1, State: domain.GameStatePlanned,
		},
		Basis: "rule_violation", RuleID: "game.rule.7",
		EvidenceIDs:     []uuid.UUID{operatorResultTestID(110)},
		ScoreRevisionID: operatorResultTestID(111), SeriesResultRevisionID: operatorResultTestID(112),
		AuditEventID: operatorResultTestID(113), OutboxEventID: operatorResultTestID(114),
		ProjectionRevisionID: operatorResultTestID(115),
	}
	return command, gameusecase.ForfeitAuthority{
		Scope:    gameusecase.Scope{TournamentID: tournamentID, SeriesID: seriesID},
		Revision: 5, Series: execution, AuthorizedOperatorIDs: []uuid.UUID{operatorID},
		CurrentOrdinal: 1, CurrentProjectionRevision: 7,
	}
}

func operatorResultTestDigest(
	t *testing.T,
	action tournamentadmin.OperatorResultAction,
	command any,
) [sha256.Size]byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		Action  tournamentadmin.OperatorResultAction `json:"action"`
		Command any                                  `json:"command"`
	}{Action: action, Command: command})
	require.NoError(t, err)
	return sha256.Sum256(payload)
}

func operatorResultTestID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-" + fmt.Sprintf("%012d", value))
}
