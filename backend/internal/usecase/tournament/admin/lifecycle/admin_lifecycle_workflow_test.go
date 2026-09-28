package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	tournamentadminmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle/mocks"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestLifecycleWorkflowRejectsGoldenWithoutPersistedProgressionProof(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateSwiss)
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartGolden, authority)

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestLifecycleWorkflowRejectsPlayoffsWithoutPersistedProgressionProof(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateSwiss)
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartPlayoffs, authority)

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestLifecycleWorkflowReturnsRecordedCommandBeforeProjectionConflict(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateGolden)
	authority.ProjectionRevision++
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartGolden, authority)
	command.ExpectedProjectionRevision--
	result := authority.Tournament
	result.Revision = authority.Tournament.Revision + 1
	result.UpdatedAt = authority.Tournament.UpdatedAt.Add(time.Minute)
	recorded := &tournamentadmin.LifecycleCommandRecord{
		CommandScope: command.CommandScope, Action: command.Action,
		SourceProjectionRevisionID: authority.ProjectionRevisionID,
		SourceProjectionRevision:   command.ExpectedProjectionRevision,
		SourceTournamentRevision:   authority.Tournament.Revision,
		SourceTournamentState:      domain.TournamentStateSwiss,
		Result:                     result, ExecutedAt: result.UpdatedAt,
	}

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(recorded, nil)

	view, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	require.NoError(t, err)
	require.Equal(t, result, view)
}

func TestLifecycleWorkflowRejectsIncompletePauseSnapshot(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateSwiss)
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionPause, authority)
	command.Reason = "operator intervention"
	snapshot := tournamentadmin.LifecycleExecutionSnapshot{
		Document:         []byte(`{"waves":[],"incomplete_count":1}`),
		GraphRevision:    authority.ProjectionRevision,
		ExpectedChildren: 2, ObservedChildren: 2, IncompleteChildren: 1,
	}

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	harness.repository.EXPECT().LockExecutionSnapshot(mock.Anything, authority).Return(snapshot, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	var conflict *tournamentadmin.RevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestLifecycleWorkflowRejectsSwissWithoutCompleteRoster(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateRosterLocked)
	authority.Tournament.RosterSize = domain.TournamentMinParticipants - 1
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartSwiss, authority)

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	var conflict *tournamentadmin.RevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestLifecycleWorkflowRejectsSwissWithoutLockedRoster(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateRosterLocked)
	authority.RosterLocked = false
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartSwiss, authority)

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	var conflict *tournamentadmin.RevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestLifecycleWorkflowRejectsSwissWithoutCompleteRosterProof(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateRosterLocked)
	authority.RosterReadyForSwiss = false
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartSwiss, authority)

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	var conflict *tournamentadmin.RevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestLifecycleWorkflowStartsSwissWithLockedCompleteRoster(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateRosterLocked)
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionStartSwiss, authority)
	startedAt := authority.Tournament.UpdatedAt.Add(time.Minute)
	transitioned := &lifecycleusecase.LifecycleTournamentRecord{
		ID: command.TournamentID, State: domain.TournamentStateSwiss,
		Revision: authority.Tournament.Revision + 1, UpdatedAt: startedAt, StartedAt: &startedAt,
	}

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	harness.transitions.EXPECT().Transition(
		mock.Anything,
		lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: command.TournamentID, ExpectedRevision: authority.Tournament.Revision,
			NextState: domain.TournamentStateSwiss,
		},
	).Return(transitioned, true, nil)
	harness.repository.EXPECT().SaveLifecycleCommand(mock.Anything, mock.Anything).Return(nil)

	view, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStateSwiss, view.State)
	require.True(t, view.StartedAt.Equal(startedAt))
}

func TestLifecycleWorkflowRejectsDirectCompletion(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStatePlayoffs)
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionComplete, authority)

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)

	_, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	var conflict *tournamentadmin.RevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestLifecycleWorkflowResumesOnlyFromRecordedSnapshot(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateTechnicalPause)
	origin := domain.TournamentStateSwiss
	authority.Tournament.PausedFromState = &origin
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionResume, authority)
	command.Reason = "incident resolved"
	snapshot := tournamentadmin.LifecycleExecutionSnapshot{
		Document:         []byte(`{"waves":[],"incomplete_count":0}`),
		GraphRevision:    authority.ProjectionRevision,
		ExpectedChildren: 0, ObservedChildren: 0,
	}
	resumedAt := authority.Tournament.UpdatedAt.Add(time.Minute)
	pauseID := uuid.New()
	sourceCommandID := uuid.New()
	resumed := tournamentadmin.LifecycleResumeResult{
		Tournament: lifecycleusecase.LifecycleTournamentRecord{
			ID: authority.Tournament.ID, State: origin, Revision: authority.Tournament.Revision + 1,
			UpdatedAt: resumedAt, StartedAt: authority.Tournament.StartedAt,
		},
		PauseID: pauseID, SourcePauseCommandID: sourceCommandID,
	}

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	harness.repository.EXPECT().LockExecutionSnapshot(mock.Anything, authority).Return(snapshot, nil)
	harness.clock.EXPECT().Now().Return(resumedAt)
	harness.repository.EXPECT().ResumeTechnicalPause(
		mock.Anything,
		mock.MatchedBy(func(input tournamentadmin.LifecycleResumeInput) bool {
			return input.TournamentID == command.TournamentID && input.RosterID == authority.Tournament.RosterID &&
				input.ExpectedTournamentRevision == authority.Tournament.Revision &&
				input.ExpectedProjectionRevision == authority.ProjectionRevision &&
				string(input.ExecutionSnapshot) == string(snapshot.Document) && input.PauseRevisionID != uuid.Nil &&
				input.Reason == command.Reason && input.ResumedAt.Equal(resumedAt)
		}),
	).Return(resumed, nil)
	harness.repository.EXPECT().SaveLifecycleCommand(
		mock.Anything,
		mock.MatchedBy(func(record tournamentadmin.LifecycleCommandRecord) bool {
			return record.Action == tournamentadmin.TournamentActionResume && record.PauseID == pauseID &&
				record.SourcePauseCommandID == sourceCommandID &&
				string(record.ExecutionSnapshot) == string(snapshot.Document) && record.Result.State == origin &&
				record.Result.PausedFromState == nil
		}),
	).Return(nil)

	view, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	require.NoError(t, err)
	require.Equal(t, origin, view.State)
	require.Nil(t, view.PausedFromState)
}

func TestLifecycleWorkflowClosesTournamentPauseDuringCancellation(t *testing.T) {
	t.Parallel()
	harness := newLifecycleWorkflowHarness(t)
	authority := lifecycleAuthorityFixture(domain.TournamentStateTechnicalPause)
	origin := domain.TournamentStateSwiss
	authority.Tournament.PausedFromState = &origin
	command := lifecycleCommandFixture(tournamentadmin.TournamentActionCancel, authority)
	command.Reason = "cancel tournament"
	cancelledAt := authority.Tournament.UpdatedAt.Add(time.Minute)
	cancelled := &tournamentcancellation.TournamentCancellationRecord{
		Tournament: tournamentcancellation.CancellationTournamentRecord{
			ID: command.TournamentID, State: domain.TournamentStateCancelled,
			Revision: authority.Tournament.Revision + 1, UpdatedAt: cancelledAt, FinishedAt: &cancelledAt,
		},
		CommandID: command.CommandID, ActorID: command.Operator.ActorID, Reason: command.Reason,
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), CancelledAt: cancelledAt,
	}

	harness.repository.EXPECT().LockLifecycleAuthority(mock.Anything, command.TournamentID).Return(authority, nil)
	harness.repository.EXPECT().FindLifecycleCommand(mock.Anything, command.TournamentID, command.CommandID).
		Return(nil, nil)
	harness.cancellations.EXPECT().Cancel(mock.Anything, mock.Anything).Return(cancelled, true, nil)
	harness.repository.EXPECT().CancelTechnicalPause(
		mock.Anything,
		mock.MatchedBy(func(input tournamentadmin.LifecyclePauseCancellationInput) bool {
			return input.TournamentID == command.TournamentID && input.RosterID == authority.Tournament.RosterID &&
				input.ResultingTournamentRevision == cancelled.Tournament.Revision && input.PauseRevisionID != uuid.Nil &&
				input.Reason == command.Reason && input.CancelledAt.Equal(cancelledAt)
		}),
	).Return(nil)
	harness.repository.EXPECT().SaveLifecycleCommand(mock.Anything, mock.Anything).Return(nil)

	view, err := harness.workflow.ApplyTournamentAction(context.Background(), command)
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStateCancelled, view.State)
	require.Nil(t, view.PausedFromState)
}

type lifecycleWorkflowHarness struct {
	workflow      *tournamentadmin.LifecycleWorkflow
	repository    *tournamentadminmocks.MockLifecycleWorkflowRepository
	transitions   *tournamentadminmocks.MockLifecycleTransitioner
	cancellations *tournamentadminmocks.MockLifecycleCanceller
	clock         *tournamentadminmocks.MockAdminLifecycleClock
}

func newLifecycleWorkflowHarness(t *testing.T) lifecycleWorkflowHarness {
	t.Helper()
	transactions := tournamentadminmocks.NewMockLifecycleTransactionManager(t)
	repository := tournamentadminmocks.NewMockLifecycleWorkflowRepository(t)
	transitions := tournamentadminmocks.NewMockLifecycleTransitioner(t)
	pauses := tournamentadminmocks.NewMockLifecyclePauser(t)
	cancellations := tournamentadminmocks.NewMockLifecycleCanceller(t)
	clock := tournamentadminmocks.NewMockAdminLifecycleClock(t)
	transactions.EXPECT().Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	return lifecycleWorkflowHarness{
		workflow: tournamentadmin.NewLifecycleWorkflow(tournamentadmin.LifecycleWorkflowDependencies{
			Transactions: transactions, Repository: repository, Transitions: transitions,
			Pauses: pauses, Cancellations: cancellations,
			Progressions: tournamentprogression.NewWorkflow(tournamentprogression.ProgressionDependencies{}), Clock: clock,
		}),
		repository: repository, transitions: transitions, cancellations: cancellations, clock: clock,
	}
}

func lifecycleAuthorityFixture(state domain.TournamentState) tournamentadmin.LifecycleAuthority {
	createdAt := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
	startedAt := createdAt.Add(time.Minute)
	var started *time.Time
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch state {
	case domain.TournamentStateSwiss, domain.TournamentStateGolden, domain.TournamentStatePlayoffs,
		domain.TournamentStateTechnicalPause:
		started = &startedAt
	}
	return tournamentadmin.LifecycleAuthority{
		Tournament: inbound.TournamentView{
			ID: uuid.New(), RosterID: uuid.New(), Preset: domain.TournamentPresetV1,
			State: state, Revision: 4, RosterSize: 8, CreatedAt: createdAt,
			UpdatedAt: createdAt.Add(2 * time.Minute), StartedAt: started,
		},
		ProjectionRevisionID: uuid.New(), ProjectionRevision: 9,
		RosterLocked:        state == domain.TournamentStateRosterLocked,
		RosterReadyForSwiss: state == domain.TournamentStateRosterLocked,
	}
}

func lifecycleCommandFixture(
	action tournamentadmin.TournamentAction,
	authority tournamentadmin.LifecycleAuthority,
) tournamentadmin.TournamentActionCommand {
	return tournamentadmin.TournamentActionCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     operationusecase.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: authority.Tournament.ID, CommandID: uuid.New(),
		},
		ExpectedProjectionRevision: authority.ProjectionRevision, Action: action, Confirmed: true,
	}
}
