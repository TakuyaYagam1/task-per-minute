package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validGoldenFailureReplayCommand(command GoldenFailureReplayCommand) bool {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || command.FailureID == uuid.Nil ||
		command.Expected.Scope != command.Scope || command.ClosedWaveRevisionID.IsZero() ||
		len(command.PrivateAssignments) < 2 || len(command.PrivateAssignments) > domain.TournamentMaxParticipants {
		return false
	}
	ids := []uuid.UUID{
		command.CommandID, command.FailureID, command.ClosedWaveRevisionID.UUID(), command.NextAttemptID,
		command.NextAssignmentID, command.NextAssignmentRevisionID, command.NextWaveID,
		command.NextWaveRevisionID.UUID(), command.NextWindowID, command.NextWaveWindowRevisionID.UUID(),
		command.NextWindowRevisionID, command.NextReadinessRevisionID, command.NextPresenceRevisionID,
		command.NextMembershipID, command.NextMembershipRevisionID,
	}
	for _, assignment := range command.PrivateAssignments {
		ids = append(ids, assignment.AssignmentID)
	}
	return ValidIdentitySet(ids)
}

func commitGoldenFailure(
	ctx context.Context,
	repository FailureRepository,
	expected GoldenFailureExpectation,
	record GoldenFailureRecord,
) (*GoldenFailureRecord, bool, bool, error) {
	commit := GoldenFailureCommit{
		Expected: expected, Route: record.Route,
		NewIdentityIDs: append([]uuid.UUID(nil), record.NewIdentityIDs...), Record: record.Snapshot(),
	}
	committed, changed, err := repository.CommitGoldenFailure(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("golden failure - commit route: %w", err)
	}
	if committed == nil || committed.Validate() != nil || committed.CommandID != record.CommandID ||
		committed.CommandDigest != record.CommandDigest || committed.Scope != record.Scope ||
		committed.Route != record.Route ||
		(changed && committed.PayloadDigest != record.PayloadDigest) {
		return nil, false, false, domain.ErrInternal
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}

func reconcileGoldenFailure(
	record GoldenFailureRecord,
	scope GoldenSubmissionScope,
	commandID uuid.UUID,
	route GoldenFailureRoute,
	digest [sha256.Size]byte,
) (*GoldenFailureRecord, bool, error) {
	if record.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	if record.CommandID != commandID {
		return nil, false, domain.ErrInternal
	}
	if record.Scope != scope || record.Route != route || record.CommandDigest != digest {
		return nil, false, ErrGoldenFailureCommandReuse
	}
	clone := record.Snapshot()
	return &clone, false, nil
}

func goldenFailureAliasesAuthority(authority GoldenFailureAuthority, identities []uuid.UUID) bool {
	reserved := IdentitySetFromExpectation(authority.Active.Expectation)
	roles := CoreIdentityRoles(authority.State)
	roles = append(roles, PlanIdentityRoles(authority.State)...)
	roles = append(roles, WindowIdentityRoles(authority.State)...)
	roles = append(roles, TransitionIdentityRoles(authority.State)...)
	for _, role := range roles {
		reserved[role.Value] = struct{}{}
	}
	values := []uuid.UUID{
		authority.Active.Scope.State.TournamentID, authority.Active.Scope.State.GroupID,
		authority.Active.Scope.State.GroupRevisionID.UUID(), authority.Active.Scope.AttemptID,
		authority.Active.Scope.WaveID, authority.Active.Scope.AssignmentID,
		authority.Active.Scope.SnapshotID, authority.Active.Scope.TaskID,
		authority.Submissions.RevisionID, authority.Positions.RevisionID, authority.SwissPoints.RevisionID,
		authority.Plan.PlanID, authority.Plan.PlanRevisionID,
		authority.Classification.FailedEdge.ID, authority.Classification.FailedEdge.ReservationID,
		authority.Classification.FailedEdge.SnapshotID, authority.Classification.FailedEdge.TaskID,
	}
	for _, group := range authority.Plan.Groups {
		for _, edge := range group.Edges {
			values = append(values, edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID, edge.Snapshot.TaskID)
		}
	}
	if authority.Classification.NextEdge != nil {
		values = append(values, authority.Classification.NextEdge.ID, authority.Classification.NextEdge.ReservationID,
			authority.Classification.NextEdge.SnapshotID, authority.Classification.NextEdge.TaskID)
	}
	for _, value := range values {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
	for _, identity := range identities {
		if _, found := reserved[identity]; found {
			return true
		}
	}
	return false
}

func sealGoldenFailureRecord(record GoldenFailureRecord) (GoldenFailureRecord, error) {
	record.PayloadDigest = [sha256.Size]byte{}
	payload, err := goldenFailureRecordPayload(record)
	if err != nil {
		return GoldenFailureRecord{}, goldenFailureError("encode failure receipt")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return GoldenFailureRecord{}, err
	}
	return record.Snapshot(), nil
}

func goldenFailureClassificationPayload(classification GoldenFailureClassification) ([]byte, error) {
	clone := classification.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return Encode(clone)
}

func goldenFailureRecordPayload(record GoldenFailureRecord) ([]byte, error) {
	clone := record.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return Encode(clone)
}

func goldenFailureReplayCommandDigest(command GoldenFailureReplayCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenFailureError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenFailure, message)
}
