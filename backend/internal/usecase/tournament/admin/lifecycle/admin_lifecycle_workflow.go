package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type LifecycleTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type AdminLifecycleClock interface {
	Now() time.Time
}

type LifecycleTransitioner interface {
	Transition(
		ctx context.Context,
		command lifecycleusecase.TournamentLifecycleCommand,
	) (*lifecycleusecase.LifecycleTournamentRecord, bool, error)
}

type LifecyclePauser interface {
	EnterTechnicalPause(
		ctx context.Context,
		command tournamentpause.TournamentTechnicalPauseCommand,
	) (*tournamentpause.TournamentTechnicalPauseRecord, bool, error)
}

type LifecycleCanceller interface {
	Cancel(
		ctx context.Context,
		command tournamentcancellation.TournamentCancellationCommand,
	) (*tournamentcancellation.TournamentCancellationRecord, bool, error)
}

// LifecycleProgression is mandatory for stage changes. Unlike ordinary
// lifecycle transitions, it persists and CASes exact stage proof before the
// tournament state row can move.
type LifecycleProgression interface {
	Advance(
		ctx context.Context,
		command tournamentprogression.Command,
		authority tournamentprogression.Authority,
	) (tournamentprogression.Receipt, error)
}

type LifecycleAuthority struct {
	Tournament           usecase.TournamentView
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	RosterLocked         bool
	RosterReadyForSwiss  bool
}

type LifecycleExecutionSnapshot struct {
	Document           json.RawMessage
	GraphRevision      int64
	ExpectedChildren   int
	ObservedChildren   int
	IncompleteChildren int
	ActiveGolden       bool
}

type LifecycleResumeInput struct {
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	ExpectedTournamentRevision int64
	ExpectedProjectionRevision int64
	ExecutionSnapshot          json.RawMessage
	PauseRevisionID            uuid.UUID
	Reason                     string
	ResumedAt                  time.Time
}

type LifecycleResumeResult struct {
	Tournament           lifecycleusecase.LifecycleTournamentRecord
	PauseID              uuid.UUID
	SourcePauseCommandID uuid.UUID
}

type LifecyclePauseCancellationInput struct {
	TournamentID                uuid.UUID
	RosterID                    uuid.UUID
	ResultingTournamentRevision int64
	PauseRevisionID             uuid.UUID
	Reason                      string
	CancelledAt                 time.Time
}

type LifecycleCommandRecord struct {
	CommandScope

	Action                     TournamentAction
	SourceProjectionRevisionID uuid.UUID
	SourceProjectionRevision   int64
	SourceTournamentRevision   int64
	SourceTournamentState      domain.TournamentState
	Result                     usecase.TournamentView
	Reason                     string
	PauseID                    uuid.UUID
	SourcePauseCommandID       uuid.UUID
	ExecutionSnapshot          json.RawMessage
	ExecutedAt                 time.Time
}

type LifecycleWorkflowRepository interface {
	LockLifecycleAuthority(ctx context.Context, tournamentID uuid.UUID) (LifecycleAuthority, error)
	FindLifecycleCommand(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*LifecycleCommandRecord, error)
	LockExecutionSnapshot(
		ctx context.Context,
		authority LifecycleAuthority,
	) (LifecycleExecutionSnapshot, error)
	ResumeTechnicalPause(ctx context.Context, input LifecycleResumeInput) (LifecycleResumeResult, error)
	CancelTechnicalPause(ctx context.Context, input LifecyclePauseCancellationInput) error
	SaveLifecycleCommand(ctx context.Context, record LifecycleCommandRecord) error
}

type LifecycleWorkflowDependencies struct {
	Transactions  LifecycleTransactionManager
	Repository    LifecycleWorkflowRepository
	Transitions   LifecycleTransitioner
	Pauses        LifecyclePauser
	Cancellations LifecycleCanceller
	Progressions  LifecycleProgression
	Clock         AdminLifecycleClock
}

type LifecycleWorkflow struct {
	transactions  LifecycleTransactionManager
	repository    LifecycleWorkflowRepository
	transitions   LifecycleTransitioner
	pauses        LifecyclePauser
	cancellations LifecycleCanceller
	progressions  LifecycleProgression
	clock         AdminLifecycleClock
}

func NewLifecycleWorkflow(deps LifecycleWorkflowDependencies) *LifecycleWorkflow {
	return &LifecycleWorkflow{
		transactions:  deps.Transactions,
		repository:    deps.Repository,
		transitions:   deps.Transitions,
		pauses:        deps.Pauses,
		cancellations: deps.Cancellations,
		progressions:  deps.Progressions,
		clock:         deps.Clock,
	}
}

func (w *LifecycleWorkflow) ApplyTournamentAction(
	ctx context.Context,
	command TournamentActionCommand,
) (usecase.TournamentView, error) {
	if ctx == nil || !validTournamentActionCommand(command) || !validLifecycleReason(command) {
		return usecase.TournamentView{}, domain.ErrValidation
	}
	if !w.available() {
		return usecase.TournamentView{}, domain.ErrInternal
	}

	var result usecase.TournamentView
	err := w.transactions.Do(ctx, func(txCtx context.Context) error {
		view, err := w.applyLocked(txCtx, command)
		if err != nil {
			return err
		}
		result = view
		return nil
	})
	if err != nil {
		return usecase.TournamentView{}, err
	}
	return result, nil
}

func (w *LifecycleWorkflow) available() bool {
	return w != nil && w.transactions != nil && w.repository != nil && w.transitions != nil &&
		w.pauses != nil && w.cancellations != nil && w.progressions != nil && w.clock != nil
}

func (w *LifecycleWorkflow) applyLocked(
	ctx context.Context,
	command TournamentActionCommand,
) (usecase.TournamentView, error) {
	authority, err := w.repository.LockLifecycleAuthority(ctx, command.TournamentID)
	if err != nil {
		return usecase.TournamentView{}, err
	}
	if !validLifecycleAuthority(authority, command.TournamentID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}

	recorded, err := w.repository.FindLifecycleCommand(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return usecase.TournamentView{}, err
	}
	if recorded != nil {
		if !lifecycleCommandMatches(*recorded, command) {
			return usecase.TournamentView{}, lifecycleConflict(command, authority)
		}
		return adminCloneTournamentView(recorded.Result), nil
	}
	if authority.ProjectionRevision != command.ExpectedProjectionRevision {
		return usecase.TournamentView{}, lifecycleConflict(command, authority)
	}

	record, err := w.executeAction(ctx, command, authority)
	if err != nil {
		return usecase.TournamentView{}, w.actionError(err, command, authority)
	}
	if err := validateLifecycleCommandRecord(record); err != nil {
		return usecase.TournamentView{}, err
	}
	if err := w.repository.SaveLifecycleCommand(ctx, record); err != nil {
		return usecase.TournamentView{}, w.actionError(err, command, authority)
	}
	return adminCloneTournamentView(record.Result), nil
}

func (w *LifecycleWorkflow) executeAction(
	ctx context.Context,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) (LifecycleCommandRecord, error) {
	switch command.Action {
	case TournamentActionPause:
		return w.pause(ctx, command, authority)
	case TournamentActionResume:
		return w.resume(ctx, command, authority)
	case TournamentActionCancel:
		return w.cancel(ctx, command, authority)
	case TournamentActionStartGolden, TournamentActionStartPlayoffs:
		return w.progress(ctx, command, authority)
	case TournamentActionOpenRegistration, TournamentActionStartSwiss, TournamentActionComplete:
		return w.transition(ctx, command, authority)
	default:
		return LifecycleCommandRecord{}, domain.ErrValidation
	}
}

func (w *LifecycleWorkflow) progress(
	ctx context.Context,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) (LifecycleCommandRecord, error) {
	action, ok := lifecycleProgressionAction(command.Action)
	if !ok {
		return LifecycleCommandRecord{}, domain.ErrValidation
	}
	receipt, err := w.progressions.Advance(ctx, tournamentprogression.Command{
		CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: authority.Tournament.RosterID,
		ActorID: command.Operator.ActorID, ExpectedProjectionRevision: command.ExpectedProjectionRevision, Action: action,
	}, tournamentprogression.Authority{
		Tournament:           adminCloneTournamentView(authority.Tournament),
		ProjectionRevisionID: authority.ProjectionRevisionID,
		ProjectionRevision:   authority.ProjectionRevision,
	})
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if receipt.CommandID != command.CommandID {
		return LifecycleCommandRecord{}, domain.ErrConflict
	}
	return lifecycleRecord(command, authority, receipt.Result), nil
}

func (w *LifecycleWorkflow) transition(
	ctx context.Context,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) (LifecycleCommandRecord, error) {
	next, ok := lifecycleActionState(command.Action)
	if !ok {
		return LifecycleCommandRecord{}, domain.ErrValidation
	}
	if !lifecycleTransitionAdmitted(command.Action, authority) {
		return LifecycleCommandRecord{}, lifecycleusecase.ErrTournamentGuardedTransition
	}
	record, changed, err := w.transitions.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
		TournamentID:     command.TournamentID,
		ExpectedRevision: authority.Tournament.Revision,
		NextState:        next,
	})
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if !changed {
		return LifecycleCommandRecord{}, domain.ErrConflict
	}
	result, err := lifecycleTournamentView(authority.Tournament, record)
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	return lifecycleRecord(command, authority, result), nil
}

func lifecycleProgressionAction(action TournamentAction) (tournamentprogression.Action, bool) {
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch action {
	case TournamentActionStartGolden:
		return tournamentprogression.ActionStartGolden, true
	case TournamentActionStartPlayoffs:
		return tournamentprogression.ActionStartPlayoffs, true
	default:
		return "", false
	}
}

func lifecycleTransitionAdmitted(action TournamentAction, authority LifecycleAuthority) bool {
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch action {
	case TournamentActionOpenRegistration:
		return authority.Tournament.State == domain.TournamentStateDraft
	case TournamentActionStartSwiss:
		return authority.Tournament.State == domain.TournamentStateRosterLocked && authority.RosterLocked &&
			authority.RosterReadyForSwiss &&
			authority.Tournament.RosterSize >= domain.TournamentMinParticipants &&
			authority.Tournament.RosterSize <= domain.TournamentMaxParticipants
	case TournamentActionStartGolden, TournamentActionStartPlayoffs:
		// Stage progression is admitted only after the persisted proof path has
		// locked the current projection, canonical ledger, and stage evidence.
		return false
	case TournamentActionComplete:
		// Final publication verifies the terminal final and its champion before completion.
		return false
	default:
		return false
	}
}

func (w *LifecycleWorkflow) pause(
	ctx context.Context,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) (LifecycleCommandRecord, error) {
	snapshot, err := w.repository.LockExecutionSnapshot(ctx, authority)
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if err := validateLifecycleExecutionSnapshot(snapshot, authority); err != nil {
		return LifecycleCommandRecord{}, err
	}
	pauseID := lifecycleEvidenceID(command.CommandID, "pause")
	record, changed, err := w.pauses.EnterTechnicalPause(ctx, tournamentpause.TournamentTechnicalPauseCommand{
		TournamentID:     command.TournamentID,
		ExpectedRevision: authority.Tournament.Revision,
		CommandID:        command.CommandID,
		PauseID:          pauseID,
		ActorID:          command.Operator.ActorID,
		Reason:           strings.TrimSpace(command.Reason),
		Confirmed:        command.Confirmed,
	})
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if !changed {
		return LifecycleCommandRecord{}, domain.ErrConflict
	}
	result, err := pausedTournamentView(authority.Tournament, record)
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	out := lifecycleRecord(command, authority, result)
	out.PauseID = pauseID
	out.ExecutionSnapshot = cloneJSON(snapshot.Document)
	return out, nil
}

func (w *LifecycleWorkflow) resume(
	ctx context.Context,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) (LifecycleCommandRecord, error) {
	if authority.Tournament.State != domain.TournamentStateTechnicalPause || authority.Tournament.PausedFromState == nil {
		return LifecycleCommandRecord{}, domain.ErrConflict
	}
	snapshot, err := w.repository.LockExecutionSnapshot(ctx, authority)
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if err := validateLifecycleExecutionSnapshot(snapshot, authority); err != nil {
		return LifecycleCommandRecord{}, err
	}
	resumedAt := w.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(resumedAt) || resumedAt.Before(authority.Tournament.UpdatedAt) {
		return LifecycleCommandRecord{}, domain.ErrValidation
	}
	resumed, err := w.repository.ResumeTechnicalPause(ctx, LifecycleResumeInput{
		TournamentID:               command.TournamentID,
		RosterID:                   authority.Tournament.RosterID,
		ExpectedTournamentRevision: authority.Tournament.Revision,
		ExpectedProjectionRevision: authority.ProjectionRevision,
		ExecutionSnapshot:          cloneJSON(snapshot.Document),
		PauseRevisionID:            lifecycleEvidenceID(command.CommandID, "pause-revision"),
		Reason:                     strings.TrimSpace(command.Reason),
		ResumedAt:                  resumedAt,
	})
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	result, err := lifecycleTournamentView(authority.Tournament, &resumed.Tournament)
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	out := lifecycleRecord(command, authority, result)
	out.PauseID = resumed.PauseID
	out.SourcePauseCommandID = resumed.SourcePauseCommandID
	out.ExecutionSnapshot = cloneJSON(snapshot.Document)
	return out, nil
}

func (w *LifecycleWorkflow) cancel(
	ctx context.Context,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) (LifecycleCommandRecord, error) {
	cancelled, changed, err := w.cancellations.Cancel(ctx, tournamentcancellation.TournamentCancellationCommand{
		TournamentID:     command.TournamentID,
		ExpectedRevision: authority.Tournament.Revision,
		CommandID:        command.CommandID,
		ActorID:          command.Operator.ActorID,
		Reason:           strings.TrimSpace(command.Reason),
		Confirmed:        command.Confirmed,
	})
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if !changed {
		return LifecycleCommandRecord{}, domain.ErrConflict
	}
	result, err := cancelledTournamentView(authority.Tournament, cancelled)
	if err != nil {
		return LifecycleCommandRecord{}, err
	}
	if authority.Tournament.State == domain.TournamentStateTechnicalPause {
		if err := w.repository.CancelTechnicalPause(ctx, LifecyclePauseCancellationInput{
			TournamentID:                command.TournamentID,
			RosterID:                    authority.Tournament.RosterID,
			ResultingTournamentRevision: result.Revision,
			PauseRevisionID:             lifecycleEvidenceID(command.CommandID, "pause-revision"),
			Reason:                      strings.TrimSpace(command.Reason),
			CancelledAt:                 cancelled.CancelledAt,
		}); err != nil {
			return LifecycleCommandRecord{}, err
		}
	}
	return lifecycleRecord(command, authority, result), nil
}

func (w *LifecycleWorkflow) actionError(
	err error,
	command TournamentActionCommand,
	authority LifecycleAuthority,
) error {
	if err == nil || errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrTournamentNotFound) {
		return err
	}
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrTournamentTransition) ||
		errors.Is(err, lifecycleusecase.ErrTournamentGuardedTransition) ||
		errors.Is(err, tournamentpause.ErrTournamentPauseGraphPartial) ||
		errors.Is(err, tournamentpause.ErrTournamentGoldenActive) {
		return lifecycleConflict(command, authority)
	}
	return err
}

func lifecycleRecord(
	command TournamentActionCommand,
	authority LifecycleAuthority,
	result usecase.TournamentView,
) LifecycleCommandRecord {
	return LifecycleCommandRecord{
		CommandScope:               command.CommandScope,
		Action:                     command.Action,
		SourceProjectionRevisionID: authority.ProjectionRevisionID,
		SourceProjectionRevision:   authority.ProjectionRevision,
		SourceTournamentRevision:   authority.Tournament.Revision,
		SourceTournamentState:      authority.Tournament.State,
		Result:                     adminCloneTournamentView(result),
		Reason:                     strings.TrimSpace(command.Reason),
		ExecutedAt:                 result.UpdatedAt,
	}
}

func lifecycleConflict(command TournamentActionCommand, authority LifecycleAuthority) error {
	return lifecycleConflictWithDetail(command, authority, "")
}

func lifecycleConflictWithDetail(
	command TournamentActionCommand,
	authority LifecycleAuthority,
	detail string,
) error {
	return &RevisionConflictError{
		ExpectedRevision: command.ExpectedProjectionRevision,
		CurrentRevision:  authority.ProjectionRevision,
		CurrentState:     authority.Tournament.State,
		Detail:           detail,
	}
}

func lifecycleEvidenceID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("tournament-lifecycle:"+role))
}

func validLifecycleReason(command TournamentActionCommand) bool {
	reason := strings.TrimSpace(command.Reason)
	if command.Reason != reason {
		return false
	}
	switch command.Action {
	case TournamentActionPause, TournamentActionResume, TournamentActionCancel:
		return reason != ""
	case TournamentActionOpenRegistration, TournamentActionStartSwiss, TournamentActionStartGolden,
		TournamentActionStartPlayoffs, TournamentActionComplete:
		return true
	default:
		return false
	}
}

func lifecycleActionState(action TournamentAction) (domain.TournamentState, bool) {
	switch action {
	case TournamentActionOpenRegistration:
		return domain.TournamentStateRegistration, true
	case TournamentActionStartSwiss:
		return domain.TournamentStateSwiss, true
	case TournamentActionStartGolden:
		return domain.TournamentStateGolden, true
	case TournamentActionStartPlayoffs:
		return domain.TournamentStatePlayoffs, true
	case TournamentActionComplete:
		return domain.TournamentStateCompleted, true
	case TournamentActionPause, TournamentActionResume, TournamentActionCancel:
		return "", false
	default:
		return "", false
	}
}

var _ LifecyclePort = (*LifecycleWorkflow)(nil)
