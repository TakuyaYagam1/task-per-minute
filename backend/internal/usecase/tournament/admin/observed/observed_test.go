package observed_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	lifecycleworkflow "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	observabilityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observability"
	observedusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observed"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestObservedServiceEmitsOneRosterTerminalEvent(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	command := rosterusecase.ReplaceRosterCommand{CommandScope: observedCommandScope()}
	view := rosterusecase.RosterView{
		ID: command.TournamentID, TournamentID: command.TournamentID, Revision: 7,
	}
	next := &serviceMock{replaceRoster: func(context.Context, rosterusecase.ReplaceRosterCommand) (rosterusecase.RosterView, error) {
		return view, nil
	}}
	clock := &clockMock{values: []time.Time{startedAt, startedAt.Add(25 * time.Millisecond)}}
	observer := &observerMock{}
	service := observedusecase.NewObservedService(next, clock, observer)

	actual, err := service.ReplaceRoster(t.Context(), command)

	require.NoError(t, err)
	require.Equal(t, view, actual)
	require.Equal(t, []observabilityusecase.OperationEvent{{
		Operation: observabilityusecase.OperationRosterReplace, CommandID: command.CommandID,
		TournamentID: command.TournamentID, EntityID: command.TournamentID,
		Outcome:    observabilityusecase.OperationOutcomeSuccess,
		ReasonCode: "completed", Revision: 7, Duration: 25 * time.Millisecond,
	}}, observer.events)
}

func TestObservedServiceMapsConflictWithoutLeakingCommandReason(t *testing.T) {
	t.Parallel()

	command := replayusecase.ReplayCommand{
		CommandScope:              observedCommandScope(),
		ReplacementGameID:         uuid.MustParse("39000000-0000-4000-8000-000000000004"),
		Reason:                    "private operator explanation",
		ExpectedAuthorityRevision: 9,
	}
	next := &serviceMock{replayGame: func(context.Context, replayusecase.ReplayCommand) error {
		return domain.ErrConflict
	}}
	observer := &observerMock{}
	service := observedusecase.NewObservedService(next, nil, observer)

	err := service.ReplayGame(t.Context(), command)

	require.ErrorIs(t, err, domain.ErrConflict)
	require.Equal(t, []observabilityusecase.OperationEvent{{
		Operation: observabilityusecase.OperationGameReplay, CommandID: command.CommandID,
		TournamentID: command.TournamentID, EntityID: command.ReplacementGameID,
		Outcome:    observabilityusecase.OperationOutcomeRejected,
		ReasonCode: "stale_revision", Revision: 9,
	}}, observer.events)
}

func TestObservedServiceIsolatesObserverPanic(t *testing.T) {
	t.Parallel()

	command := replayusecase.ReserveCommand{
		CommandScope: observedCommandScope(),
		AssignmentID: uuid.MustParse("39000000-0000-4000-8000-000000000005"),
	}
	next := &serviceMock{assignReserve: func(context.Context, replayusecase.ReserveCommand) error { return nil }}
	observer := &observerMock{panicOnObserve: true}
	service := observedusecase.NewObservedService(next, nil, observer)

	require.NotPanics(t, func() {
		require.NoError(t, service.AssignReserve(t.Context(), command))
	})
	require.Len(t, observer.events, 1)
}

func TestObservedServiceEmitsOneLifecycleTerminalEventAfterCommit(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	command := lifecycleworkflow.TournamentActionCommand{
		CommandScope: observedCommandScope(),
		Action:       lifecycleworkflow.TournamentActionStartSwiss,
	}
	view := inbound.TournamentView{ID: command.TournamentID, Revision: 8}
	next := &serviceMock{applyTournamentAction: func(context.Context, lifecycleworkflow.TournamentActionCommand) (inbound.TournamentView, error) {
		return view, nil
	}}
	clock := &clockMock{values: []time.Time{startedAt, startedAt.Add(25 * time.Millisecond)}}
	observer := &observerMock{}
	service := observedusecase.NewObservedService(next, clock, observer)

	actual, err := service.ApplyTournamentAction(t.Context(), command)

	require.NoError(t, err)
	require.Equal(t, view, actual)
	require.Equal(t, []observabilityusecase.OperationEvent{{
		Operation:    observabilityusecase.Operation("tournament_action"),
		CommandID:    command.CommandID,
		TournamentID: command.TournamentID,
		EntityID:     command.TournamentID,
		Outcome:      observabilityusecase.OperationOutcomeSuccess,
		ReasonCode:   "completed",
		Revision:     8,
		Duration:     25 * time.Millisecond,
	}}, observer.events)
}

func observedCommandScope() operationusecase.CommandScope {
	return operationusecase.CommandScope{
		Operator: operationusecase.OperatorIdentity{
			ActorID: uuid.MustParse("39000000-0000-4000-8000-000000000001"),
		},
		TournamentID: uuid.MustParse("39000000-0000-4000-8000-000000000002"),
		CommandID:    uuid.MustParse("39000000-0000-4000-8000-000000000003"),
	}
}

type serviceMock struct {
	replaceRoster         func(context.Context, rosterusecase.ReplaceRosterCommand) (rosterusecase.RosterView, error)
	replayGame            func(context.Context, replayusecase.ReplayCommand) error
	assignReserve         func(context.Context, replayusecase.ReserveCommand) error
	applyTournamentAction func(context.Context, lifecycleworkflow.TournamentActionCommand) (inbound.TournamentView, error)
	deleteTournament      func(context.Context, lifecycleworkflow.TournamentDeletionCommand) (lifecycleworkflow.TournamentDeletionRecord, error)
}

func (service *serviceMock) GetRoster(context.Context, rosterusecase.RosterQuery) (rosterusecase.RosterView, error) {
	return rosterusecase.RosterView{}, nil
}

func (service *serviceMock) ReplaceRoster(ctx context.Context, command rosterusecase.ReplaceRosterCommand) (rosterusecase.RosterView, error) {
	if service.replaceRoster != nil {
		return service.replaceRoster(ctx, command)
	}
	return rosterusecase.RosterView{}, nil
}

func (service *serviceMock) RunPreflight(context.Context, rosterusecase.PreflightCommand) (tournamentpreflight.ReportRevision, error) {
	return tournamentpreflight.ReportRevision{}, nil
}

func (service *serviceMock) LockRoster(context.Context, rosterusecase.LockRosterCommand) (rosterusecase.RosterView, error) {
	return rosterusecase.RosterView{}, nil
}

func (service *serviceMock) UnlockRoster(context.Context, rosterusecase.UnlockRosterCommand) (rosterusecase.RosterView, error) {
	return rosterusecase.RosterView{}, nil
}

func (service *serviceMock) ConfigurePairings(context.Context, pairingusecase.PairingCommand) (executionusecase.SwissRoundView, error) {
	return executionusecase.SwissRoundView{}, nil
}

func (service *serviceMock) ApplyTournamentAction(ctx context.Context, command lifecycleworkflow.TournamentActionCommand) (inbound.TournamentView, error) {
	if service.applyTournamentAction != nil {
		return service.applyTournamentAction(ctx, command)
	}
	return inbound.TournamentView{}, nil
}

func (service *serviceMock) DeleteTournament(ctx context.Context, command lifecycleworkflow.TournamentDeletionCommand) (lifecycleworkflow.TournamentDeletionRecord, error) {
	if service.deleteTournament != nil {
		return service.deleteTournament(ctx, command)
	}
	return lifecycleworkflow.TournamentDeletionRecord{}, nil
}

func (service *serviceMock) ControlWave(context.Context, executionusecase.WaveCommand) (executionusecase.WaveView, error) {
	return executionusecase.WaveView{}, nil
}

func (service *serviceMock) ResolveNoShow(context.Context, resultusecase.NoShowCommand) error {
	return nil
}

func (service *serviceMock) AssignReserve(ctx context.Context, command replayusecase.ReserveCommand) error {
	if service.assignReserve != nil {
		return service.assignReserve(ctx, command)
	}
	return nil
}

func (service *serviceMock) RecordForfeit(context.Context, resultusecase.ForfeitCommand) error {
	return nil
}

func (service *serviceMock) ReplayGame(ctx context.Context, command replayusecase.ReplayCommand) error {
	if service.replayGame != nil {
		return service.replayGame(ctx, command)
	}
	return nil
}

func (service *serviceMock) CorrectGameResult(context.Context, correctionusecase.CorrectionCommand) (correctionusecase.CorrectionEvidence, error) {
	return correctionusecase.CorrectionEvidence{}, nil
}

func (service *serviceMock) ListAudit(context.Context, incidentusecase.AuditQuery) (audit.AuditPage, error) {
	return audit.AuditPage{}, nil
}

func (service *serviceMock) ExportIncident(context.Context, incidentusecase.IncidentQuery) (audit.IncidentBundle, error) {
	return audit.IncidentBundle{}, nil
}

func (service *serviceMock) GetOperatorSnapshot(context.Context, snapshotusecase.SnapshotQuery) (snapshotusecase.OperatorSnapshotView, error) {
	return snapshotusecase.OperatorSnapshotView{}, nil
}

type clockMock struct {
	values []time.Time
	index  int
}

func (clock *clockMock) Now() time.Time {
	value := clock.values[clock.index]
	clock.index++
	return value
}

type observerMock struct {
	events         []observabilityusecase.OperationEvent
	panicOnObserve bool
}

func (observer *observerMock) ObserveTournamentAdminOperation(_ context.Context, event observabilityusecase.OperationEvent) {
	observer.events = append(observer.events, event)
	if observer.panicOnObserve {
		panic("observer failed")
	}
}

var _ observedusecase.Service = (*serviceMock)(nil)
