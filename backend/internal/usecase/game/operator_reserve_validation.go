package game

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

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
	want, changed, err := seriesdomain.Transition(
		r.Exhaustion.Series,
		seriesdomain.TransitionCommand{NextState: domain.SeriesStateReplayRequired},
	)
	if err != nil || !changed || !replaySeriesExecutionsEqual(want, r.Series) {
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
		!assignmentusecase.ValidReserveAssignmentSourceRevisions(command.ExpectedRevisions) ||
		command.ProposedTaskID == uuid.Nil || command.ProposedVersion < 1 ||
		command.ProposedSnapshotID == uuid.Nil ||
		assignmentusecase.ValidateReserveAssignmentCommand(command.Reserve) != nil ||
		command.Reserve.Mode != assignmentusecase.ReserveAssignmentModeOperator ||
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
	_, decision, err := assignmentusecase.NormalizeReserveAssignmentAuthority(reserve.Scope, reserve)
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
	reserve assignmentusecase.ReserveAssignmentAuthority,
) bool {
	return reserve.Scope.TournamentID == authority.Scope.TournamentID &&
		reserve.Scope.AssignmentID == authority.Scope.AssignmentID &&
		reserve.Scope.AttemptID == authority.Exhaustion.AssignmentAttemptID &&
		reserve.Scope.SlotID == authority.Scope.SlotID &&
		reserve.CurrentSnapshotID == authority.Exhaustion.ActiveSnapshotID &&
		reserve.RequiredCategory == authority.Exhaustion.Category &&
		reserve.CandidateSnapshot.Kind == domain.AssignmentTaskKindNormal &&
		reserve.CandidateSnapshot.Category == authority.Exhaustion.Category &&
		reserve.CategoryExhaustion == nil
}

func validOperatorReserveCandidate(record OperatorReserve) bool {
	return record.Reserve.RequiredCategory == record.Exhaustion.Category &&
		record.Reserve.Snapshot.Kind == domain.AssignmentTaskKindNormal &&
		record.Reserve.Snapshot.Category == record.Exhaustion.Category &&
		record.Reserve.CandidateHealth.TaskID == record.Reserve.Snapshot.TaskID &&
		record.Reserve.CandidateHealth.Version == record.Reserve.Snapshot.Version &&
		record.Reserve.CandidateHealth.Exists && record.Reserve.CandidateHealth.Enabled &&
		record.Reserve.CandidateHealth.Healthy && record.Reserve.CandidateHealth.MutationLocked &&
		!record.Reserve.CandidateHealth.PubliclyExposed && record.Reserve.CategoryExhaustion == nil &&
		record.Reserve.Evidence.Mode == assignmentusecase.ReserveAssignmentModeOperator
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
	want, err := assignmentusecase.BuildReserveAssignmentRecord(
		reserveAssignmentCommandFromRecord(record.Reserve),
		authority.Reserve,
	)
	return err == nil && want.ProofDigest == record.Reserve.ProofDigest
}

func reserveAssignmentCommandFromRecord(
	record assignmentusecase.ReserveAssignmentRecord,
) assignmentusecase.ReserveAssignmentCommand {
	return assignmentusecase.ReserveAssignmentCommand{
		Scope:              record.Scope,
		ExpectedSnapshotID: record.FromSnapshotID,
		EvidenceID:         record.Evidence.ID,
		Mode:               record.Evidence.Mode,
		OperatorID:         record.Evidence.OperatorID,
		Reason:             record.Evidence.Reason,
		PromotedAt:         record.PromotedAt,
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

func operatorReserveError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOperatorReserve, fmt.Sprintf(format, arguments...))
}
