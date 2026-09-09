package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/mocks"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestRosterWorkflowRunPreflightPersistsFailedEvidence(t *testing.T) {
	t.Parallel()
	harness := newRosterWorkflowHarness(t, 1)
	authority := rosterAuthorityFixture()
	command := tournamentadmin.PreflightCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: rosterTestID(40)},
			TournamentID: authority.Roster.TournamentID, CommandID: rosterTestID(41),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
	}
	evaluatedAt := authority.Roster.UpdatedAt.Add(time.Minute)
	input := failedRosterPreflightInput(authority, evaluatedAt)

	harness.repository.EXPECT().LockRosterAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindRosterOperation(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	harness.repository.EXPECT().ReadRosterTime(mock.Anything).Return(evaluatedAt, nil)
	harness.repository.EXPECT().LoadPreflightInput(mock.Anything, authority, evaluatedAt).Return(input, nil)
	harness.repository.EXPECT().SaveRosterOperation(
		mock.Anything,
		mock.MatchedBy(func(record tournamentadmin.RosterOperationRecord) bool {
			var saved tournamentpreflight.ReportRevision
			//nolint:musttag // ReportRevision owns a stable, explicitly tagged evidence document.
			return record.Action == tournamentadmin.RosterOperationPreflight &&
				record.CommandID == command.CommandID && record.RosterID == authority.Roster.ID &&
				record.SourceProjectionRevisionID == authority.ProjectionRevisionID &&
				record.SourceProjectionRevision == authority.ProjectionRevision &&
				record.SourceRosterRevision == authority.Roster.Revision &&
				json.Unmarshal(record.ResultDocument, &saved) == nil && saved.Validate() == nil && !saved.Passed()
		}),
	).Return(nil)

	report, err := harness.workflow.RunPreflight(context.Background(), command)
	require.NoError(t, err)
	require.NoError(t, report.Validate())
	require.False(t, report.Passed())
	require.Equal(t, command.CommandID, report.ID)
}

func TestRosterWorkflowRunPreflightSamplesRuntimeHealthBeforeTransaction(t *testing.T) {
	t.Parallel()

	authority := rosterAuthorityFixture()
	command := tournamentadmin.PreflightCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: rosterTestID(42)},
			TournamentID: authority.Roster.TournamentID,
			CommandID:    rosterTestID(43),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
	}
	evaluatedAt := authority.Roster.UpdatedAt.Add(time.Minute)
	input := failedRosterPreflightInput(authority, evaluatedAt)
	transactions := tournamentadminmocks.NewMockRosterTransactionManager(t)
	repository := tournamentadminmocks.NewMockRosterWorkflowRepository(t)
	insideTransaction := false
	transactions.EXPECT().Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			insideTransaction = true
			defer func() { insideTransaction = false }()
			return fn(ctx)
		}).Once()
	repository.EXPECT().LockRosterAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	repository.EXPECT().FindRosterOperation(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	repository.EXPECT().ReadRosterTime(mock.Anything).Return(evaluatedAt, nil)
	repository.EXPECT().LoadPreflightInput(mock.Anything, authority, evaluatedAt).Return(input, nil)
	repository.EXPECT().SaveRosterOperation(mock.Anything, mock.Anything).Return(nil)
	workflow := tournamentadmin.NewRosterWorkflow(tournamentadmin.RosterWorkflowDependencies{
		Transactions: transactions,
		Repository:   repository,
		RuntimeHealth: tournamentadmin.PreflightRuntimeHealthSourceFunc(func(context.Context) tournamentpreflight.RuntimeHealth {
			require.False(t, insideTransaction)
			return tournamentpreflight.RuntimeHealth{}
		}),
	})

	_, err := workflow.RunPreflight(t.Context(), command)
	require.NoError(t, err)
}

func TestRosterWorkflowRejectsLockWhenPersistedPreflightFailed(t *testing.T) {
	t.Parallel()
	harness := newRosterWorkflowHarness(t, 1)
	authority := rosterAuthorityFixture()
	reportID := rosterTestID(50)
	evaluatedAt := authority.Roster.UpdatedAt.Add(time.Minute)
	report, err := tournamentpreflight.NewReportRevision(reportID, evaluatedAt, failedRosterPreflightInput(authority, evaluatedAt))
	require.NoError(t, err)
	require.False(t, report.Passed())
	//nolint:musttag // ReportRevision owns a stable, explicitly tagged evidence document.
	document, err := json.Marshal(report)
	require.NoError(t, err)
	checkedIn := checkedInRosterPlayerIDs(authority.Roster)
	record := &tournamentadmin.RosterOperationRecord{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: rosterTestID(51)},
			TournamentID: authority.Roster.TournamentID, CommandID: reportID,
		},
		RosterID: authority.Roster.ID, Action: tournamentadmin.RosterOperationPreflight,
		SourceProjectionRevisionID:  authority.ProjectionRevisionID,
		SourceProjectionRevision:    authority.ProjectionRevision,
		SourceTournamentRevision:    authority.TournamentRevision,
		SourceTournamentState:       authority.TournamentState,
		ResultingTournamentRevision: authority.TournamentRevision,
		ResultingTournamentState:    authority.TournamentState,
		SourceRosterRevision:        authority.Roster.Revision,
		ResultingRosterRevision:     authority.Roster.Revision,
		RequestDigest:               [32]byte{1}, CheckedInPlayerIDs: checkedIn,
		ResultDocument: document, ExecutedAt: evaluatedAt,
	}
	command := tournamentadmin.LockRosterCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: rosterTestID(52)},
			TournamentID: authority.Roster.TournamentID, CommandID: rosterTestID(53),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision,
		PreflightRevisionID:        reportID, CheckedInPlayerIDs: checkedIn,
	}

	harness.repository.EXPECT().LockRosterAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindRosterOperation(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	harness.repository.EXPECT().FindRosterOperation(mock.Anything, command.TournamentID, reportID).
		Return(record, nil)

	_, err = harness.workflow.LockRoster(context.Background(), command)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestRosterWorkflowReplaysReplaceBeforeCurrentProjectionCheck(t *testing.T) {
	t.Parallel()
	harness := newRosterWorkflowHarness(t, 2)
	authority := rosterAuthorityFixture()
	command := tournamentadmin.NewReplaceRosterCommand(
		tournamentadmin.OperatorIdentity{ActorID: rosterTestID(60)}, authority.Roster.TournamentID,
		rosterTestID(61), authority.ProjectionRevision, rosterReplacementFixture(),
	)
	executedAt := authority.Roster.UpdatedAt.Add(time.Minute)
	replaced := rosterViewForInputs(authority.Roster, command.Participants, executedAt)
	var saved *tournamentadmin.RosterOperationRecord

	harness.repository.EXPECT().LockRosterAuthority(mock.Anything, command.TournamentID).Return(authority, nil).Once()
	harness.repository.EXPECT().FindRosterOperation(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil).Once()
	harness.repository.EXPECT().ReadRosterTime(mock.Anything).Return(executedAt, nil).Once()
	harness.repository.EXPECT().ReplaceRosterParticipants(mock.Anything, authority, command.Participants, executedAt).
		Return(replaced, nil).Once()
	harness.repository.EXPECT().SaveRosterOperation(
		mock.Anything,
		mock.MatchedBy(func(record tournamentadmin.RosterOperationRecord) bool {
			copied := record
			saved = &copied
			return record.Action == tournamentadmin.RosterOperationReplace && record.CommandID == command.CommandID
		}),
	).Return(nil).Once()

	first, err := harness.workflow.ReplaceRoster(context.Background(), command)
	require.NoError(t, err)
	require.Equal(t, replaced, first)
	require.NotNil(t, saved)

	advanced := authority
	advanced.ProjectionRevision++
	harness.repository.EXPECT().LockRosterAuthority(mock.Anything, command.TournamentID).Return(advanced, nil).Once()
	harness.repository.EXPECT().FindRosterOperation(mock.Anything, command.TournamentID, command.CommandID).
		Return(saved, nil).Once()

	replayed, err := harness.workflow.ReplaceRoster(context.Background(), command)
	require.NoError(t, err)
	require.Equal(t, first, replayed)
}

type rosterWorkflowHarness struct {
	workflow   *tournamentadmin.RosterWorkflow
	repository *tournamentadminmocks.MockRosterWorkflowRepository
}

func newRosterWorkflowHarness(t *testing.T, transactionCount int) rosterWorkflowHarness {
	t.Helper()
	transactions := tournamentadminmocks.NewMockRosterTransactionManager(t)
	repository := tournamentadminmocks.NewMockRosterWorkflowRepository(t)
	transactions.EXPECT().Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).
		Times(transactionCount)
	return rosterWorkflowHarness{
		workflow: tournamentadmin.NewRosterWorkflow(tournamentadmin.RosterWorkflowDependencies{
			Transactions: transactions, Repository: repository,
		}),
		repository: repository,
	}
}

func rosterAuthorityFixture() tournamentadmin.RosterAuthority {
	createdAt := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
	tournamentID := rosterTestID(1)
	rosterID := rosterTestID(2)
	inputs := rosterReplacementFixture()
	view := tournamentadmin.RosterView{
		ID: rosterID, TournamentID: tournamentID, Revision: 4,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	view = rosterViewForInputs(view, inputs, createdAt)
	view.Revision = 4
	return tournamentadmin.RosterAuthority{
		Roster: view, TournamentPreset: domain.TournamentPresetV1,
		TournamentState: domain.TournamentStateRegistration, TournamentRevision: 3,
		ProjectionRevisionID: rosterTestID(3), ProjectionRevision: 9,
	}
}

func rosterReplacementFixture() []tournamentadmin.RosterParticipantInput {
	return []tournamentadmin.RosterParticipantInput{
		{PlayerID: rosterTestID(10), Seed: 1, Attendance: domain.AttendanceStateCheckedIn},
		{PlayerID: rosterTestID(11), Seed: 2, Attendance: domain.AttendanceStateCheckedIn},
		{PlayerID: rosterTestID(12), Seed: 3, Attendance: domain.AttendanceStateCheckedIn},
		{PlayerID: rosterTestID(13), Seed: 4, Attendance: domain.AttendanceStateCheckedIn},
	}
}

func rosterViewForInputs(
	base tournamentadmin.RosterView,
	inputs []tournamentadmin.RosterParticipantInput,
	updatedAt time.Time,
) tournamentadmin.RosterView {
	view := base
	view.Revision++
	view.UpdatedAt = updatedAt
	view.Participants = make([]tournamentadmin.RosterParticipantView, len(inputs))
	for index, input := range inputs {
		view.Participants[index] = tournamentadmin.RosterParticipantView{
			ID: rosterTestID(100 + index), RosterID: base.ID, TournamentID: base.TournamentID,
			PlayerID: input.PlayerID, Seed: input.Seed, Attendance: input.Attendance,
			CreatedAt: base.CreatedAt, UpdatedAt: updatedAt,
		}
	}
	return view
}

func failedRosterPreflightInput(
	authority tournamentadmin.RosterAuthority,
	evaluatedAt time.Time,
) tournamentpreflight.ReportInput {
	participants := make([]tournamentpreflight.Participant, len(authority.Roster.Participants))
	for index, participant := range authority.Roster.Participants {
		participants[index] = tournamentpreflight.Participant{
			ParticipantID: participant.ID, PlayerID: participant.PlayerID, Seed: participant.Seed,
			Attendance: participant.Attendance, ReservedTournamentID: authority.Roster.TournamentID,
		}
	}
	return tournamentpreflight.ReportInput{
		RosterRevision: authority.Roster.Revision, PairingRevision: 1,
		Structural: tournamentpreflight.StructuralInput{
			TournamentID: authority.Roster.TournamentID, Preset: authority.TournamentPreset,
			ExpectedRosterSize: len(participants), Participants: participants,
		},
		TaskHealth: tournamentpreflight.TaskHealthInput{
			NormalPool: domain.TaskPoolRevision{Kind: domain.AssignmentTaskKindNormal},
			GoldenPool: domain.TaskPoolRevision{Kind: domain.AssignmentTaskKindGolden},
		},
		Runtime: tournamentpreflight.RuntimeInput{
			TournamentID: authority.Roster.TournamentID, Preset: authority.TournamentPreset,
			RosterSize: len(participants),
			Clock:      tournamentpreflight.ClockHealth{ObservedAt: evaluatedAt, MaxSkew: time.Second},
		},
	}
}

func checkedInRosterPlayerIDs(view tournamentadmin.RosterView) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(view.Participants))
	for _, participant := range view.Participants {
		if participant.Attendance == domain.AttendanceStateCheckedIn {
			result = append(result, participant.PlayerID)
		}
	}
	return result
}

func rosterTestID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("f2000000-0000-0000-0000-%012d", value))
}
