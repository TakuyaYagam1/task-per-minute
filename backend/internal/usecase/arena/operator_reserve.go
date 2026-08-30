package arena

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const operatorReserveAttempts = 2

var (
	ErrInvalidOperatorReserve  = errors.New("invalid arena operator reserve")
	ErrOperatorReserveConflict = errors.New("arena operator reserve conflict")
	ErrOperatorReserveReuse    = errors.New("arena operator reserve command was reused")
)

type OperatorReserveCommand struct {
	Scope                       ReplayReplacementScope
	CommandID                   uuid.UUID
	ExpectedExhaustionCommandID uuid.UUID
	ExpectedRevisions           ReserveAssignmentSourceRevisions
	ProposedTaskID              uuid.UUID
	ProposedVersion             int
	ProposedSnapshotID          uuid.UUID
	Reserve                     ReserveAssignmentCommand
}

type OperatorReserveAuthority struct {
	Scope      ReplayReplacementScope
	Revision   int64
	Exhaustion ReplayReserveExhaustion
	Reserve    ReserveAssignmentAuthority
	Current    *OperatorReserve
}

type OperatorReserve struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Exhaustion                ReplayReserveExhaustion
	Reserve                   ReserveAssignmentRecord
	Series                    SeriesExecution
}

// OperatorReserveRepository owns one transaction that locks the paused Series
// and reserve-exhaustion evidence, revalidates current pool, Arena history,
// category, artifact, health and participant-reservation revisions, reserves
// the proposed version, then resumes replay_required.
type OperatorReserveRepository interface {
	LoadOperatorReserveAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (OperatorReserveAuthority, error)
	CommitOperatorReserve(
		ctx context.Context,
		record OperatorReserve,
	) (*OperatorReserve, bool, error)
}

type OperatorReserveUseCase struct {
	repository OperatorReserveRepository
}

func NewOperatorReserveUseCase(repository OperatorReserveRepository) *OperatorReserveUseCase {
	return &OperatorReserveUseCase{repository: repository}
}

func (u *OperatorReserveUseCase) Reserve(
	ctx context.Context,
	command OperatorReserveCommand,
) (*OperatorReserve, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.Reserve.Reason = strings.TrimSpace(command.Reserve.Reason)
	if err := validateOperatorReserveCommand(command); err != nil {
		return nil, false, err
	}
	for range operatorReserveAttempts {
		record, changed, retry, err := u.reserveAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrOperatorReserveConflict
}

func (u *OperatorReserveUseCase) reserveAttempt(
	ctx context.Context,
	command OperatorReserveCommand,
) (*OperatorReserve, bool, bool, error) {
	authority, err := u.repository.LoadOperatorReserveAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("OperatorReserveUseCase - load authority: %w", err)
	}
	if err := validateOperatorReserveAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, operatorReserveError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileOperatorReserve(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, err := buildOperatorReserve(command, authority)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitOperatorReserve(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("OperatorReserveUseCase - commit reserve: %w", err)
	}
	if !validCommittedOperatorReserve(committed, record, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneOperatorReserve(*committed)
	return &result, changed, false, nil
}

func (r OperatorReserve) Validate() error {
	if !validOperatorReserveHeader(r) {
		return operatorReserveError("invalid record header or retained evidence")
	}
	if !operatorReserveScopesAlign(r) {
		return operatorReserveError("reserve scope does not match exhaustion evidence")
	}
	if !validOperatorReserveCandidate(r) || !operatorReserveParticipantsMatch(r) {
		return operatorReserveError("candidate or participants changed")
	}
	want, changed, err := TransitionSeriesExecution(
		r.Exhaustion.Series,
		SeriesExecutionTransitionCommand{NextState: domain.ArenaSeriesStateReplayRequired},
	)
	if err != nil || !changed || !forfeitSeriesHeadsEqual(want, r.Series) {
		return operatorReserveError("Series did not resume replay_required")
	}
	return nil
}

func validOperatorReserveHeader(record OperatorReserve) bool {
	return record.Scope.IsValid() && record.CommandID != uuid.Nil &&
		record.ExpectedAuthorityRevision >= 1 && record.Exhaustion.Validate() == nil &&
		record.Reserve.Validate() == nil && record.Series.Validate() == nil
}

func operatorReserveScopesAlign(record OperatorReserve) bool {
	return record.Exhaustion.Scope == record.Scope &&
		record.Reserve.Scope.TournamentID == record.Scope.TournamentID &&
		record.Reserve.Scope.AssignmentID == record.Scope.AssignmentID &&
		record.Reserve.Scope.AttemptID == record.Exhaustion.AssignmentAttemptID &&
		record.Reserve.Scope.SlotID == record.Scope.SlotID &&
		record.Reserve.FromSnapshotID == record.Exhaustion.ActiveSnapshotID
}

func validateOperatorReserveCommand(command OperatorReserveCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil ||
		command.ExpectedExhaustionCommandID == uuid.Nil ||
		!validReserveAssignmentSourceRevisions(command.ExpectedRevisions) ||
		command.ProposedTaskID == uuid.Nil || command.ProposedVersion < 1 ||
		command.ProposedSnapshotID == uuid.Nil ||
		validateReserveAssignmentCommand(command.Reserve) != nil ||
		command.Reserve.Mode != ReserveAssignmentModeOperator ||
		command.CommandID == command.Reserve.EvidenceID {
		return operatorReserveError("invalid command identity, revisions, or operator evidence")
	}
	if command.Reserve.Scope.TournamentID != command.Scope.TournamentID ||
		command.Reserve.Scope.AssignmentID != command.Scope.AssignmentID ||
		command.Reserve.Scope.SlotID != command.Scope.SlotID {
		return operatorReserveError("command scopes do not align")
	}
	return nil
}

func validateOperatorReserveAuthority(authority OperatorReserveAuthority) error {
	if !validOperatorReserveAuthorityHeader(authority) {
		return operatorReserveError("invalid authority header or exhaustion evidence")
	}
	reserve := authority.Reserve
	if !operatorReserveAuthorityScopeMatches(authority, reserve) {
		return operatorReserveError("reserve authority changed scope or category")
	}
	_, decision, err := normalizeReserveAssignmentAuthority(reserve.Scope, reserve)
	if err != nil || !decision.Eligible {
		return operatorReserveError("reserve authority is stale or ineligible")
	}
	if !operatorReserveAuthorityParticipantsMatch(authority) {
		return operatorReserveError("reserve participants do not match paused Series")
	}
	if authority.Current != nil && !operatorReserveMatchesAuthority(*authority.Current, authority) {
		return operatorReserveError("invalid current operator reserve")
	}
	return nil
}

func validOperatorReserveAuthorityHeader(authority OperatorReserveAuthority) bool {
	return authority.Scope.IsValid() && authority.Revision >= 1 &&
		authority.Exhaustion.Validate() == nil && authority.Exhaustion.Scope == authority.Scope
}

func operatorReserveAuthorityScopeMatches(
	authority OperatorReserveAuthority,
	reserve ReserveAssignmentAuthority,
) bool {
	return reserve.Scope.TournamentID == authority.Scope.TournamentID &&
		reserve.Scope.AssignmentID == authority.Scope.AssignmentID &&
		reserve.Scope.AttemptID == authority.Exhaustion.AssignmentAttemptID &&
		reserve.Scope.SlotID == authority.Scope.SlotID &&
		reserve.CurrentSnapshotID == authority.Exhaustion.ActiveSnapshotID &&
		reserve.RequiredCategory == authority.Exhaustion.Category &&
		reserve.CandidateSnapshot.Kind == domain.ArenaTaskKindNormal &&
		reserve.CandidateSnapshot.Category == authority.Exhaustion.Category &&
		reserve.CategoryExhaustion == nil
}

func buildOperatorReserve(
	command OperatorReserveCommand,
	authority OperatorReserveAuthority,
) (OperatorReserve, error) {
	if authority.Exhaustion.CommandID != command.ExpectedExhaustionCommandID ||
		authority.Reserve.Revisions != command.ExpectedRevisions ||
		authority.Reserve.CurrentSnapshotID != command.Reserve.ExpectedSnapshotID ||
		authority.Reserve.CandidateSnapshot.TaskID != command.ProposedTaskID ||
		authority.Reserve.CandidateSnapshot.Version != command.ProposedVersion ||
		authority.Reserve.CandidateSnapshot.SnapshotID != command.ProposedSnapshotID {
		return OperatorReserve{}, ErrOperatorReserveConflict
	}
	reserve, err := buildReserveAssignmentRecord(command.Reserve, authority.Reserve)
	if err != nil {
		return OperatorReserve{}, err
	}
	if reserve.Snapshot.Category != authority.Exhaustion.Category ||
		reserve.CategoryExhaustion != nil || reserve.Evidence.Mode != ReserveAssignmentModeOperator {
		return OperatorReserve{}, operatorReserveError("operator reserve changed category or evidence mode")
	}
	series, changed, err := TransitionSeriesExecution(
		authority.Exhaustion.Series,
		SeriesExecutionTransitionCommand{NextState: domain.ArenaSeriesStateReplayRequired},
	)
	if err != nil || !changed {
		return OperatorReserve{}, operatorReserveError("resume Series: %v", err)
	}
	record := OperatorReserve{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		Exhaustion:                cloneReplayReserveExhaustion(authority.Exhaustion),
		Reserve:                   reserve,
		Series:                    series,
	}
	if err := record.Validate(); err != nil {
		return OperatorReserve{}, err
	}
	return cloneOperatorReserve(record), nil
}

func validOperatorReserveCandidate(record OperatorReserve) bool {
	return record.Reserve.RequiredCategory == record.Exhaustion.Category &&
		record.Reserve.Snapshot.Kind == domain.ArenaTaskKindNormal &&
		record.Reserve.Snapshot.Category == record.Exhaustion.Category &&
		record.Reserve.CandidateHealth.TaskID == record.Reserve.Snapshot.TaskID &&
		record.Reserve.CandidateHealth.Version == record.Reserve.Snapshot.Version &&
		record.Reserve.CandidateHealth.Exists && record.Reserve.CandidateHealth.Enabled &&
		record.Reserve.CandidateHealth.Healthy && record.Reserve.CandidateHealth.MutationLocked &&
		!record.Reserve.CandidateHealth.PubliclyExposed && record.Reserve.CategoryExhaustion == nil &&
		record.Reserve.Evidence.Mode == ReserveAssignmentModeOperator
}

func operatorReserveAuthorityParticipantsMatch(authority OperatorReserveAuthority) bool {
	participants := authority.Reserve.ParticipantIDs
	if len(participants) != 2 {
		return false
	}
	return replayParticipantsMatchSeries(
		[2]uuid.UUID{participants[0], participants[1]},
		authority.Exhaustion.Series.Series,
	)
}

func operatorReserveParticipantsMatch(record OperatorReserve) bool {
	participants := record.Reserve.ParticipantIDs
	if len(participants) != 2 {
		return false
	}
	return replayParticipantsMatchSeries(
		[2]uuid.UUID{participants[0], participants[1]},
		record.Exhaustion.Series.Series,
	)
}

func operatorReserveMatchesAuthority(record OperatorReserve, authority OperatorReserveAuthority) bool {
	if record.Validate() != nil || record.Scope != authority.Scope ||
		!replayReserveExhaustionsEqual(record.Exhaustion, authority.Exhaustion) {
		return false
	}
	want, err := buildReserveAssignmentRecord(
		reserveAssignmentCommandFromRecord(record.Reserve),
		authority.Reserve,
	)
	return err == nil && want.ProofDigest == record.Reserve.ProofDigest
}

func reserveAssignmentCommandFromRecord(record ReserveAssignmentRecord) ReserveAssignmentCommand {
	return ReserveAssignmentCommand{
		Scope: record.Scope, ExpectedSnapshotID: record.FromSnapshotID,
		EvidenceID: record.Evidence.ID, Mode: record.Evidence.Mode,
		OperatorID: record.Evidence.OperatorID, Reason: record.Evidence.Reason,
		PromotedAt: record.PromotedAt,
	}
}

func reconcileOperatorReserve(
	record OperatorReserve,
	command OperatorReserveCommand,
) (*OperatorReserve, error) {
	if record.Validate() != nil || record.Scope != command.Scope ||
		record.CommandID != command.CommandID ||
		record.Exhaustion.CommandID != command.ExpectedExhaustionCommandID ||
		record.Reserve.Revisions != command.ExpectedRevisions ||
		record.Reserve.Snapshot.TaskID != command.ProposedTaskID ||
		record.Reserve.Snapshot.Version != command.ProposedVersion ||
		record.Reserve.Snapshot.SnapshotID != command.ProposedSnapshotID ||
		record.Reserve.Scope != command.Reserve.Scope ||
		record.Reserve.FromSnapshotID != command.Reserve.ExpectedSnapshotID ||
		record.Reserve.Evidence.ID != command.Reserve.EvidenceID ||
		record.Reserve.Evidence.OperatorID != command.Reserve.OperatorID ||
		record.Reserve.Evidence.Reason != command.Reserve.Reason ||
		!record.Reserve.PromotedAt.Equal(command.Reserve.PromotedAt) {
		return nil, ErrOperatorReserveReuse
	}
	clone := cloneOperatorReserve(record)
	return &clone, nil
}

func validCommittedOperatorReserve(
	committed *OperatorReserve,
	proposed OperatorReserve,
	command OperatorReserveCommand,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if _, err := reconcileOperatorReserve(*committed, command); err != nil {
		return false
	}
	return !changed || operatorReservesEqual(*committed, proposed)
}

func operatorReservesEqual(first, second OperatorReserve) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		replayReserveExhaustionsEqual(first.Exhaustion, second.Exhaustion) &&
		first.Reserve.ProofDigest == second.Reserve.ProofDigest &&
		forfeitSeriesHeadsEqual(first.Series, second.Series)
}

func cloneOperatorReserve(record OperatorReserve) OperatorReserve {
	clone := record
	clone.Exhaustion = cloneReplayReserveExhaustion(record.Exhaustion)
	clone.Reserve = cloneReserveAssignmentRecord(record.Reserve)
	clone.Series = cloneSeriesExecution(record.Series)
	return clone
}

func operatorReserveError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOperatorReserve, fmt.Sprintf(format, arguments...))
}
