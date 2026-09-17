package roster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type RosterWorkflow struct {
	transactions  RosterTransactionManager
	repository    RosterWorkflowRepository
	runtimeHealth PreflightRuntimeHealthSource
}

func NewRosterWorkflow(deps RosterWorkflowDependencies) *RosterWorkflow {
	return &RosterWorkflow{
		transactions: deps.Transactions, repository: deps.Repository, runtimeHealth: deps.RuntimeHealth,
	}
}

func (w *RosterWorkflow) GetRoster(ctx context.Context, query RosterQuery) (RosterView, error) {
	if ctx == nil || !validRosterQuery(query) {
		return RosterView{}, domain.ErrValidation
	}
	if !w.available() {
		return RosterView{}, domain.ErrInternal
	}
	view, err := w.repository.GetRoster(ctx, query.TournamentID)
	if err != nil {
		return RosterView{}, err
	}
	if !validRosterView(view, query.TournamentID) {
		return RosterView{}, domain.ErrInternal
	}
	return cloneRosterView(view), nil
}

func (w *RosterWorkflow) ReplaceRoster(
	ctx context.Context,
	command ReplaceRosterCommand,
) (RosterView, error) {
	if ctx == nil || !validReplaceRosterCommand(command) {
		return RosterView{}, domain.ErrValidation
	}
	digest, err := rosterRequestDigest(RosterOperationReplace, command)
	if err != nil {
		return RosterView{}, err
	}
	return w.mutateRoster(ctx, command.CommandScope, command.ExpectedProjectionRevision, digest,
		RosterOperationReplace, rosterOperationEvidence{},
		func(txCtx context.Context, authority RosterAuthority) (RosterView, error) {
			executedAt, err := w.repository.ReadRosterTime(txCtx)
			if err != nil {
				return RosterView{}, err
			}
			return w.repository.ReplaceRosterParticipants(txCtx, authority, command.Participants, executedAt)
		})
}

func (w *RosterWorkflow) RunPreflight(
	ctx context.Context,
	command PreflightCommand,
) (tournamentpreflight.ReportRevision, error) {
	if ctx == nil || !validPreflightCommand(command) {
		return tournamentpreflight.ReportRevision{}, domain.ErrValidation
	}
	if !w.available() {
		return tournamentpreflight.ReportRevision{}, domain.ErrInternal
	}
	digest, err := rosterRequestDigest(RosterOperationPreflight, command)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	// Runtime state is sampled before the transaction. The source may use
	// bounded probes here, but is never called while tournament, roster, and
	// projection locks are held.
	runtimeHealth := w.preflightRuntimeHealth(ctx)

	var result tournamentpreflight.ReportRevision
	err = w.transactions.Do(ctx, func(txCtx context.Context) error {
		var lockedErr error
		result, lockedErr = w.runPreflightLocked(txCtx, command, digest, runtimeHealth)
		return lockedErr
	})
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	return clonePreflightReport(result), nil
}

func (w *RosterWorkflow) runPreflightLocked(
	ctx context.Context,
	command PreflightCommand,
	digest [32]byte,
	runtimeHealth tournamentpreflight.RuntimeHealth,
) (tournamentpreflight.ReportRevision, error) {
	authority, err := w.lockAuthority(ctx, command.TournamentID)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	recorded, err := w.repository.FindRosterOperation(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	if recorded != nil {
		replayed, replayErr := replayPreflight(*recorded, command, digest)
		if replayErr != nil {
			return tournamentpreflight.ReportRevision{}, rosterConflict(command.ExpectedProjectionRevision, authority)
		}
		return replayed, nil
	}
	if authority.ProjectionRevision != command.ExpectedProjectionRevision ||
		!validRosterActionAuthority(RosterOperationPreflight, authority) {
		return tournamentpreflight.ReportRevision{}, rosterConflict(command.ExpectedProjectionRevision, authority)
	}
	evaluatedAt, err := w.repository.ReadRosterTime(ctx)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	input, err := w.repository.LoadPreflightInput(ctx, authority, evaluatedAt)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	input.Runtime = tournamentpreflight.ApplyRuntimeHealth(input.Runtime, runtimeHealth)
	if !validPreflightInputAuthority(input, authority) {
		return tournamentpreflight.ReportRevision{}, domain.ErrInternal
	}
	report, err := tournamentpreflight.NewReportRevision(command.CommandID, evaluatedAt, input)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, fmt.Errorf("RosterWorkflow - RunPreflight - compose report: %w", err)
	}
	record, err := newRosterOperationRecord(
		command.CommandScope, RosterOperationPreflight, authority, authority.Roster.Revision,
		digest, rosterOperationEvidence{checkedInPlayerIDs: preflightCheckedInPlayerIDs(input)}, report, evaluatedAt,
	)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	if err := w.repository.SaveRosterOperation(ctx, record); err != nil {
		return tournamentpreflight.ReportRevision{}, err
	}
	return report, nil
}

func (w *RosterWorkflow) preflightRuntimeHealth(ctx context.Context) (health tournamentpreflight.RuntimeHealth) {
	if w == nil || w.runtimeHealth == nil {
		return tournamentpreflight.RuntimeHealth{}
	}
	defer func() {
		if recover() != nil {
			health = tournamentpreflight.RuntimeHealth{}
		}
	}()
	return w.runtimeHealth.RuntimeHealth(ctx)
}

func (w *RosterWorkflow) LockRoster(ctx context.Context, command LockRosterCommand) (RosterView, error) {
	if ctx == nil || !validLockRosterCommand(command) {
		return RosterView{}, domain.ErrValidation
	}
	digest, err := rosterRequestDigest(RosterOperationLock, command)
	if err != nil {
		return RosterView{}, err
	}
	return w.mutateRoster(ctx, command.CommandScope, command.ExpectedProjectionRevision, digest,
		RosterOperationLock, rosterOperationEvidence{
			preflightRevisionID: command.PreflightRevisionID,
			checkedInPlayerIDs:  canonicalRosterIDs(command.CheckedInPlayerIDs),
		}, func(txCtx context.Context, authority RosterAuthority) (RosterView, error) {
			reportRecord, err := w.repository.FindRosterOperation(
				txCtx, command.TournamentID, command.PreflightRevisionID,
			)
			if err != nil {
				return RosterView{}, err
			}
			if !preflightAuthorizesLock(reportRecord, command, authority) {
				return RosterView{}, domain.ErrConflict
			}
			lockedAt, err := w.repository.ReadRosterTime(txCtx)
			if err != nil {
				return RosterView{}, err
			}
			return w.repository.LockRosterWithPreflight(
				txCtx, authority, canonicalRosterIDs(command.CheckedInPlayerIDs), lockedAt,
			)
		})
}

func (w *RosterWorkflow) UnlockRoster(ctx context.Context, command UnlockRosterCommand) (RosterView, error) {
	if ctx == nil || !validUnlockRosterCommand(command) {
		return RosterView{}, domain.ErrValidation
	}
	digest, err := rosterRequestDigest(RosterOperationUnlock, command)
	if err != nil {
		return RosterView{}, err
	}
	return w.mutateRoster(ctx, command.CommandScope, command.ExpectedProjectionRevision, digest,
		RosterOperationUnlock, rosterOperationEvidence{},
		func(txCtx context.Context, authority RosterAuthority) (RosterView, error) {
			updatedAt, err := w.repository.ReadRosterTime(txCtx)
			if err != nil {
				return RosterView{}, err
			}
			return w.repository.UnlockRoster(txCtx, authority, updatedAt)
		})
}

type rosterMutation func(context.Context, RosterAuthority) (RosterView, error)

type rosterOperationEvidence struct {
	preflightRevisionID uuid.UUID
	checkedInPlayerIDs  []uuid.UUID
}

func (w *RosterWorkflow) mutateRoster(
	ctx context.Context,
	scope CommandScope,
	expectedProjectionRevision int64,
	digest [32]byte,
	action RosterOperationAction,
	evidence rosterOperationEvidence,
	mutate rosterMutation,
) (RosterView, error) {
	if !w.available() {
		return RosterView{}, domain.ErrInternal
	}
	var result RosterView
	err := w.transactions.Do(ctx, func(txCtx context.Context) error {
		authority, err := w.lockAuthority(txCtx, scope.TournamentID)
		if err != nil {
			return err
		}
		recorded, err := w.repository.FindRosterOperation(txCtx, scope.TournamentID, scope.CommandID)
		if err != nil {
			return err
		}
		if recorded != nil {
			replayed, replayErr := replayRosterOperation(*recorded, scope, action, expectedProjectionRevision, digest)
			if replayErr != nil {
				return rosterConflict(expectedProjectionRevision, authority)
			}
			result = replayed
			return nil
		}
		if authority.ProjectionRevision != expectedProjectionRevision {
			return rosterConflict(expectedProjectionRevision, authority)
		}
		if !validRosterActionAuthority(action, authority) {
			return rosterConflict(expectedProjectionRevision, authority)
		}
		view, err := mutate(txCtx, authority)
		if err != nil {
			return err
		}
		if !validRosterMutationResult(view, authority) {
			return domain.ErrInternal
		}
		record, err := newRosterOperationRecord(
			scope, action, authority, view.Revision, digest, evidence, view, view.UpdatedAt,
		)
		if err != nil {
			return err
		}
		if err := w.repository.SaveRosterOperation(txCtx, record); err != nil {
			return err
		}
		result = view
		return nil
	})
	if err != nil {
		return RosterView{}, err
	}
	return cloneRosterView(result), nil
}

func (w *RosterWorkflow) lockAuthority(ctx context.Context, tournamentID uuid.UUID) (RosterAuthority, error) {
	authority, err := w.repository.LockRosterAuthority(ctx, tournamentID)
	if err != nil {
		return RosterAuthority{}, err
	}
	if !validRosterAuthority(authority, tournamentID) {
		return RosterAuthority{}, domain.ErrInternal
	}
	return authority, nil
}

func (w *RosterWorkflow) available() bool {
	return w != nil && w.transactions != nil && w.repository != nil
}

func rosterConflict(expected int64, authority RosterAuthority) error {
	return &RevisionConflictError{
		ExpectedRevision: expected,
		CurrentRevision:  authority.ProjectionRevision,
		CurrentState:     authority.TournamentState,
	}
}

func newRosterOperationRecord(
	scope CommandScope,
	action RosterOperationAction,
	authority RosterAuthority,
	resultingRosterRevision int64,
	digest [32]byte,
	evidence rosterOperationEvidence,
	result any,
	executedAt time.Time,
) (RosterOperationRecord, error) {
	document, err := json.Marshal(result)
	if err != nil {
		return RosterOperationRecord{}, fmt.Errorf("RosterWorkflow - encode operation result: %w", err)
	}
	record := RosterOperationRecord{
		CommandScope: scope, RosterID: authority.Roster.ID, Action: action,
		SourceProjectionRevisionID:  authority.ProjectionRevisionID,
		SourceProjectionRevision:    authority.ProjectionRevision,
		SourceTournamentRevision:    authority.TournamentRevision,
		SourceTournamentState:       authority.TournamentState,
		ResultingTournamentRevision: rosterResultingTournamentRevision(action, authority),
		ResultingTournamentState:    rosterResultingTournamentState(action, authority),
		SourceRosterRevision:        authority.Roster.Revision,
		ResultingRosterRevision:     resultingRosterRevision,
		RequestDigest:               digest,
		PreflightRevisionID:         evidence.preflightRevisionID,
		CheckedInPlayerIDs:          append([]uuid.UUID{}, evidence.checkedInPlayerIDs...),
		ResultDocument:              document,
		ExecutedAt:                  executedAt,
	}
	if !validRosterOperationRecord(record) {
		return RosterOperationRecord{}, domain.ErrInternal
	}
	return record, nil
}

func replayRosterOperation(
	record RosterOperationRecord,
	scope CommandScope,
	action RosterOperationAction,
	expectedProjectionRevision int64,
	digest [32]byte,
) (RosterView, error) {
	if !rosterOperationMatches(record, scope, action, expectedProjectionRevision, digest) {
		return RosterView{}, domain.ErrConflict
	}
	var view RosterView
	//nolint:musttag // ResultDocument is a versioned usecase-owned evidence snapshot validated below.
	if err := json.Unmarshal(record.ResultDocument, &view); err != nil || !validRosterView(view, scope.TournamentID) {
		return RosterView{}, domain.ErrInternal
	}
	return view, nil
}

func replayPreflight(
	record RosterOperationRecord,
	command PreflightCommand,
	digest [32]byte,
) (tournamentpreflight.ReportRevision, error) {
	if !rosterOperationMatches(
		record, command.CommandScope, RosterOperationPreflight, command.ExpectedProjectionRevision, digest,
	) {
		return tournamentpreflight.ReportRevision{}, domain.ErrConflict
	}
	var report tournamentpreflight.ReportRevision
	//nolint:musttag // ResultDocument is a versioned usecase-owned evidence snapshot validated below.
	if err := json.Unmarshal(record.ResultDocument, &report); err != nil || report.Validate() != nil ||
		report.ID != command.CommandID || report.TournamentID != command.TournamentID {
		return tournamentpreflight.ReportRevision{}, domain.ErrInternal
	}
	return report, nil
}

var (
	_ RosterPort    = (*RosterWorkflow)(nil)
	_ PreflightPort = (*RosterWorkflow)(nil)
)
