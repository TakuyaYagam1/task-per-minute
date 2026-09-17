package execution

import (
	"crypto/sha256"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func (e GoldenWaveExecution) Validate() error {
	if err := validateExecutionIdentity(e); err != nil {
		return err
	}
	if err := validateExecutionBindings(e); err != nil {
		return err
	}
	if err := validateGoldenWaveReceiptChain(e); err != nil {
		return err
	}
	receiptsDigest, err := WaveReceiptsDigest(e.Receipts)
	if err != nil || e.ReceiptsDigest == [sha256.Size]byte{} || e.ReceiptsDigest != receiptsDigest ||
		WavePayloadDigest(e) != e.PayloadDigest {
		return executionError("execution payload digest changed")
	}
	return nil
}

func validateExecutionIdentity(e GoldenWaveExecution) error {
	if !executionValidStateScope(e.Scope) || e.Source.Scope != e.Scope || e.Source.PayloadDigest == [sha256.Size]byte{} ||
		e.RevisionID == uuid.Nil || e.Revision < 1 || !validExecutionRevisionPredecessor(
		e.RevisionID, e.Revision, e.PreviousRevisionID,
	) || !domain.IsValidReadyWindowInterval(e.OpenedAt, e.Deadline) {
		return executionError("invalid execution identity, source or interval")
	}
	return nil
}

func validateExecutionBindings(e GoldenWaveExecution) error {
	if err := validateRetainedGroup(e); err != nil {
		return err
	}
	if err := validateRetainedWave(e); err != nil {
		return err
	}
	if err := validateExecutionWindow(e); err != nil {
		return err
	}
	if err := validateMembershipBinding(e); err != nil {
		return err
	}
	if err := validateAttemptAssignment(e); err != nil {
		return err
	}
	if err := validateStartRecord(e); err != nil {
		return err
	}
	if err := validateExactExecutionCoverage(e); err != nil {
		return err
	}
	return validateGoldenExecutionIdentityRoles(e)
}

func validateRetainedGroup(e GoldenWaveExecution) error {
	if _, err := domain.NewGoldenGroup(e.Group); err != nil || e.Attempt.Validate() != nil {
		return executionError("invalid retained group or attempt")
	}
	if e.Group.ID != e.Scope.GroupID || e.Group.RevisionID != e.Scope.GroupRevisionID ||
		e.Attempt.ID == uuid.Nil || len(e.Group.Attempts) == 0 ||
		!reflect.DeepEqual(e.Group.Attempts[len(e.Group.Attempts)-1], e.Attempt) {
		return executionError("group does not retain the current attempt")
	}
	return validateGroupBindingEvidence(e)
}

func validateGroupBindingEvidence(e GoldenWaveExecution) error {
	groupBindingDigest, err := ExecutionGroupBindingDigest(e.Group, e.OpeningParticipationEstablished)
	if err != nil || e.GroupBindingDigest == [sha256.Size]byte{} || groupBindingDigest != e.GroupBindingDigest {
		return executionError("immutable group binding changed")
	}
	if (e.Start == nil && e.Group.ParticipationEstablished != e.OpeningParticipationEstablished) ||
		(e.Start != nil && !e.Group.ParticipationEstablished) {
		return executionError("group participation evidence changed outside start")
	}
	return nil
}

func validateRetainedWave(e GoldenWaveExecution) error {
	if err := e.Wave.Validate(); err != nil || e.Wave.ID == uuid.Nil || e.Wave.TournamentID != e.Scope.TournamentID ||
		e.Wave.ReadyWindow == nil || e.Wave.ReadyWindow.ID != e.Window.ID ||
		!e.Wave.ReadyWindow.OpenedAt.Equal(e.OpenedAt) || !e.Wave.ReadyWindow.Deadline.Equal(e.Deadline) {
		return executionError("invalid retained Wave")
	}
	return nil
}

func validateExactExecutionCoverage(e GoldenWaveExecution) error {
	active := executionActiveIDs(e.Group)
	waveMembers := MemberIDs(e.Wave)
	if !executionEqualIDs(active, e.Attempt.ParticipantIDs) ||
		!executionEqualIDs(active, e.Membership.ParticipantIDs) ||
		!executionEqualIDs(active, waveMembers) ||
		!executionEqualIDs(active, PrivateAssignmentParticipantIDs(e.Assignment.Private)) ||
		!executionEqualIDs(active, e.Window.BasePresentParticipantIDs) {
		return executionError("execution does not cover the exact active membership")
	}
	return nil
}

func validateExecutionWindow(e GoldenWaveExecution) error {
	w := e.Window
	if err := validateExecutionWindowIdentity(e, w); err != nil {
		return err
	}
	if err := ValidateReadyWindowParticipants(w); err != nil {
		return err
	}
	waveReady := ReadyMemberIDs(e.Wave)
	if !executionEqualIDs(waveReady, w.ReadyParticipantIDs) {
		return executionError("Wave readiness does not match Golden readiness")
	}
	return validateWindowState(e, w)
}

func validateExecutionWindowIdentity(e GoldenWaveExecution, w goldenstate.GoldenReadyWindow) error {
	if w.ID == uuid.Nil || w.RevisionID == uuid.Nil || w.Revision < 1 ||
		w.AttemptID != e.Attempt.ID || w.AttemptNo != e.Attempt.AttemptNo ||
		!w.OpenedAt.Equal(e.OpenedAt) || !w.Deadline.Equal(e.Deadline) ||
		w.ReadinessRevisionID == uuid.Nil || w.ReadinessRevision < 1 ||
		w.PresenceRevisionID == uuid.Nil || w.PresenceRevision < 1 {
		return executionError("invalid ready-window identity")
	}
	if !validExecutionRevisionPredecessor(w.RevisionID, w.Revision, w.PreviousRevisionID) ||
		!validExecutionRevisionPredecessor(w.ReadinessRevisionID, w.ReadinessRevision, w.ReadinessPreviousRevisionID) ||
		!validExecutionRevisionPredecessor(w.PresenceRevisionID, w.PresenceRevision, w.PresencePreviousRevisionID) {
		return executionError("invalid ready-window revision lineage")
	}
	return nil
}

func ValidateReadyWindowParticipants(w goldenstate.GoldenReadyWindow) error {
	if !executionIDsCanonical(w.BasePresentParticipantIDs) || !executionIDsCanonical(w.ReadyParticipantIDs) ||
		!executionIDsCanonical(w.PresentParticipantIDs) ||
		!executionIDsSubset(w.ReadyParticipantIDs, w.PresentParticipantIDs) ||
		!executionIDsSubset(w.PresentParticipantIDs, w.BasePresentParticipantIDs) ||
		w.ReadinessDigest != executionDigestIDs(w.ReadyParticipantIDs) ||
		w.PresenceDigest != executionDigestIDs(w.PresentParticipantIDs) {
		return executionError("invalid ready-window participant binding")
	}
	return nil
}

func ReadyMemberIDs(wave domain.Wave) []uuid.UUID {
	ready := make([]uuid.UUID, 0, len(wave.Members))
	for _, member := range wave.Members {
		if member.Ready {
			ready = append(ready, member.ParticipantID)
		}
	}
	executionSortIDs(ready)
	return ready
}

func validateWindowState(e GoldenWaveExecution, w goldenstate.GoldenReadyWindow) error {
	if w.State != goldenstate.GoldenReadyWindowOpen && w.State != goldenstate.GoldenReadyWindowConsumed {
		return executionError("invalid ready-window state")
	}
	switch w.State {
	case goldenstate.GoldenReadyWindowOpen:
		return validateOpenWindowState(e, w)
	case goldenstate.GoldenReadyWindowConsumed:
		return validateConsumedWindowState(e)
	case goldenstate.GoldenReadyWindowExpired:
		return executionError("expired ready window cannot back a Wave execution")
	default:
		return executionError("invalid ready-window state")
	}
}

func validateOpenWindowState(e GoldenWaveExecution, w goldenstate.GoldenReadyWindow) error {
	if e.Wave.ReadyWindow.State != domain.ReadyWindowStateOpen ||
		(e.Wave.State != domain.WaveStateReadyWindowOpen && e.Wave.State != domain.WaveStateReady) ||
		e.Attempt.State != domain.GoldenAttemptStateWaitingReady || e.Start != nil {
		return executionError("open Wave, window and attempt states diverged")
	}
	allReady := len(w.ReadyParticipantIDs) == len(w.BasePresentParticipantIDs)
	if (e.Wave.State == domain.WaveStateReady) != allReady {
		return executionError("Wave ready state does not match exact membership")
	}
	return nil
}

func validateConsumedWindowState(e GoldenWaveExecution) error {
	if e.Wave.ReadyWindow.State != domain.ReadyWindowStateConsumed ||
		e.Wave.State != domain.WaveStateActive || e.Attempt.State != domain.GoldenAttemptStateActive ||
		e.Start == nil {
		return executionError("started Wave, window and attempt states diverged")
	}
	return nil
}

func validateMembershipBinding(e GoldenWaveExecution) error {
	m := e.Membership
	if m.ID == uuid.Nil || m.RevisionID == uuid.Nil || m.Revision != 1 ||
		!goldenstate.MembershipRevisionsEqual(m.Source, e.Source.Membership) ||
		!executionIDsCanonical(m.ParticipantIDs) || m.PayloadDigest != executionDigestIDs(m.ParticipantIDs) {
		return executionError("invalid exact membership binding")
	}
	return nil
}

func validateAttemptAssignment(e GoldenWaveExecution) error {
	a := e.Assignment
	if err := validateAssignmentIdentity(e, a); err != nil {
		return err
	}
	if err := validateAssignmentArtifact(a); err != nil {
		return err
	}
	return validatePrivateAssignments(a)
}

func validateAssignmentIdentity(e GoldenWaveExecution, a GoldenAttemptAssignment) error {
	if a.ID == uuid.Nil || a.RevisionID == uuid.Nil || a.Revision != 1 || a.Scope != e.Scope ||
		a.AttemptID != e.Attempt.ID || a.WaveID != e.Wave.ID || a.MembershipID != e.Membership.ID ||
		a.Plan != e.Source.Plan || a.EdgeID == uuid.Nil || a.ReservationID == uuid.Nil {
		return executionError("invalid immutable assignment identity")
	}
	return nil
}

func validateAssignmentArtifact(a GoldenAttemptAssignment) error {
	digest, digestErr := taskexec.SnapshotDigest(a.Snapshot)
	if a.Snapshot.Validate() != nil || digestErr != nil || digest != a.ContentDigest ||
		a.ContentDigest == [sha256.Size]byte{} || a.Snapshot.SnapshotID == uuid.Nil ||
		a.PayloadDigest != AssignmentDigest(a) || len(a.Private) < 2 {
		return executionError("invalid immutable assignment artifact")
	}
	return nil
}

func validatePrivateAssignments(a GoldenAttemptAssignment) error {
	seen := make(map[uuid.UUID]struct{}, len(a.Private))
	for _, private := range a.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != a.Snapshot.SnapshotID ||
			private.ContentDigest != a.ContentDigest {
			return executionError("invalid private assignment")
		}
		if _, duplicate := seen[private.ID]; duplicate {
			return executionError("private assignment identity is reused")
		}
		seen[private.ID] = struct{}{}
	}
	return nil
}

func executionActiveIDs(group domain.GoldenGroupState) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(group.Members))
	for _, member := range group.Members {
		if !member.Excluded {
			result = append(result, member.ParticipantID)
		}
	}
	executionSortIDs(result)
	return result
}

func executionDigestIDs(values []uuid.UUID) [sha256.Size]byte {
	canonical := append([]uuid.UUID(nil), values...)
	executionSortIDs(canonical)
	payload, _ := executionEncode(canonical)
	return sha256.Sum256(payload)
}
