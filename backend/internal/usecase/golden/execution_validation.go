package golden

import (
	"crypto/sha256"
	"reflect"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"

	"github.com/google/uuid"
)

func (e GoldenWaveExecution) Validate() error {
	if err := validateGoldenWaveExecutionIdentity(e); err != nil {
		return err
	}
	if err := validateGoldenWaveExecutionBindings(e); err != nil {
		return err
	}
	if err := validateGoldenWaveReceiptChain(e); err != nil {
		return err
	}
	receiptsDigest, err := goldenWaveReceiptsDigest(e.Receipts)
	if err != nil || e.ReceiptsDigest == [sha256.Size]byte{} || e.ReceiptsDigest != receiptsDigest ||
		goldenWavePayloadDigest(e) != e.PayloadDigest {
		return goldenWaveError("execution payload digest changed")
	}
	return nil
}

func validateGoldenWaveExecutionIdentity(e GoldenWaveExecution) error {
	if !ValidStateScope(e.Scope) || e.Source.Scope != e.Scope || e.Source.PayloadDigest == [sha256.Size]byte{} ||
		e.RevisionID == uuid.Nil || e.Revision < 1 || !waveValidGoldenRevisionPredecessor(
		e.RevisionID, e.Revision, e.PreviousRevisionID,
	) || !domain.IsValidReadyWindowInterval(e.OpenedAt, e.Deadline) {
		return goldenWaveError("invalid execution identity, source or interval")
	}
	return nil
}

func validateGoldenWaveExecutionBindings(e GoldenWaveExecution) error {
	if err := validateGoldenRetainedGroup(e); err != nil {
		return err
	}
	if err := validateGoldenRetainedWave(e); err != nil {
		return err
	}
	if err := validateGoldenExecutionWindow(e); err != nil {
		return err
	}
	if err := validateGoldenMembershipBinding(e); err != nil {
		return err
	}
	if err := validateGoldenAttemptAssignment(e); err != nil {
		return err
	}
	if err := validateGoldenStartRecord(e); err != nil {
		return err
	}
	if err := validateGoldenExactExecutionCoverage(e); err != nil {
		return err
	}
	return validateGoldenExecutionIdentityRoles(e)
}

func validateGoldenRetainedGroup(e GoldenWaveExecution) error {
	if _, err := domain.NewGoldenGroup(e.Group); err != nil || e.Attempt.Validate() != nil {
		return goldenWaveError("invalid retained group or attempt")
	}
	if e.Group.ID != e.Scope.GroupID || e.Group.RevisionID != e.Scope.GroupRevisionID ||
		e.Attempt.ID == uuid.Nil || len(e.Group.Attempts) == 0 ||
		!reflect.DeepEqual(e.Group.Attempts[len(e.Group.Attempts)-1], e.Attempt) {
		return goldenWaveError("group does not retain the current attempt")
	}
	return validateGoldenGroupBindingEvidence(e)
}

func validateGoldenGroupBindingEvidence(e GoldenWaveExecution) error {
	groupBindingDigest, err := ExecutionGroupBindingDigest(e.Group, e.OpeningParticipationEstablished)
	if err != nil || e.GroupBindingDigest == [sha256.Size]byte{} || groupBindingDigest != e.GroupBindingDigest {
		return goldenWaveError("immutable group binding changed")
	}
	if (e.Start == nil && e.Group.ParticipationEstablished != e.OpeningParticipationEstablished) ||
		(e.Start != nil && !e.Group.ParticipationEstablished) {
		return goldenWaveError("group participation evidence changed outside start")
	}
	return nil
}

func validateGoldenRetainedWave(e GoldenWaveExecution) error {
	if err := e.Wave.Validate(); err != nil || e.Wave.ID == uuid.Nil || e.Wave.TournamentID != e.Scope.TournamentID ||
		e.Wave.ReadyWindow == nil || e.Wave.ReadyWindow.ID != e.Window.ID ||
		!e.Wave.ReadyWindow.OpenedAt.Equal(e.OpenedAt) || !e.Wave.ReadyWindow.Deadline.Equal(e.Deadline) {
		return goldenWaveError("invalid retained Wave")
	}
	return nil
}

func validateGoldenExactExecutionCoverage(e GoldenWaveExecution) error {
	active := goldenExecutionActiveIDs(e.Group)
	waveMembers := MemberIDs(e.Wave)
	if !EqualIDs(active, e.Attempt.ParticipantIDs) ||
		!EqualIDs(active, e.Membership.ParticipantIDs) ||
		!EqualIDs(active, waveMembers) || !EqualIDs(active, PrivateAssignmentParticipantIDs(e.Assignment.Private)) ||
		!EqualIDs(active, e.Window.BasePresentParticipantIDs) {
		return goldenWaveError("execution does not cover the exact active membership")
	}
	return nil
}

func validateGoldenExecutionWindow(e GoldenWaveExecution) error {
	w := e.Window
	if err := validateGoldenExecutionWindowIdentity(e, w); err != nil {
		return err
	}
	if err := ValidateReadyWindowParticipants(w); err != nil {
		return err
	}
	waveReady := ReadyMemberIDs(e.Wave)
	if !EqualIDs(waveReady, w.ReadyParticipantIDs) {
		return goldenWaveError("Wave readiness does not match Golden readiness")
	}
	return validateGoldenWindowState(e, w)
}

func validateGoldenExecutionWindowIdentity(e GoldenWaveExecution, w GoldenReadyWindow) error {
	if w.ID == uuid.Nil || w.RevisionID == uuid.Nil || w.Revision < 1 ||
		w.AttemptID != e.Attempt.ID || w.AttemptNo != e.Attempt.AttemptNo ||
		!w.OpenedAt.Equal(e.OpenedAt) || !w.Deadline.Equal(e.Deadline) ||
		w.ReadinessRevisionID == uuid.Nil || w.ReadinessRevision < 1 ||
		w.PresenceRevisionID == uuid.Nil || w.PresenceRevision < 1 {
		return goldenWaveError("invalid ready-window identity")
	}
	if !waveValidGoldenRevisionPredecessor(w.RevisionID, w.Revision, w.PreviousRevisionID) ||
		!waveValidGoldenRevisionPredecessor(w.ReadinessRevisionID, w.ReadinessRevision, w.ReadinessPreviousRevisionID) ||
		!waveValidGoldenRevisionPredecessor(w.PresenceRevisionID, w.PresenceRevision, w.PresencePreviousRevisionID) {
		return goldenWaveError("invalid ready-window revision lineage")
	}
	return nil
}

func ValidateReadyWindowParticipants(w GoldenReadyWindow) error {
	if !IDsCanonical(w.BasePresentParticipantIDs) || !IDsCanonical(w.ReadyParticipantIDs) ||
		!IDsCanonical(w.PresentParticipantIDs) || !IDsSubset(w.ReadyParticipantIDs, w.PresentParticipantIDs) ||
		!IDsSubset(w.PresentParticipantIDs, w.BasePresentParticipantIDs) ||
		w.ReadinessDigest != DigestIDs(w.ReadyParticipantIDs) ||
		w.PresenceDigest != DigestIDs(w.PresentParticipantIDs) {
		return goldenWaveError("invalid ready-window participant binding")
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
	SortIDs(ready)
	return ready
}

func validateGoldenWindowState(e GoldenWaveExecution, w GoldenReadyWindow) error {
	if w.State != GoldenReadyWindowOpen && w.State != GoldenReadyWindowConsumed {
		return goldenWaveError("invalid ready-window state")
	}
	switch w.State {
	case GoldenReadyWindowOpen:
		return validateGoldenOpenWindowState(e, w)
	case GoldenReadyWindowConsumed:
		return validateGoldenConsumedWindowState(e)
	case GoldenReadyWindowExpired:
		return goldenWaveError("expired ready window cannot back a Wave execution")
	default:
		return goldenWaveError("invalid ready-window state")
	}
}

func validateGoldenOpenWindowState(e GoldenWaveExecution, w GoldenReadyWindow) error {
	if e.Wave.ReadyWindow.State != domain.ReadyWindowStateOpen ||
		(e.Wave.State != domain.WaveStateReadyWindowOpen && e.Wave.State != domain.WaveStateReady) ||
		e.Attempt.State != domain.GoldenAttemptStateWaitingReady || e.Start != nil {
		return goldenWaveError("open Wave, window and attempt states diverged")
	}
	allReady := len(w.ReadyParticipantIDs) == len(w.BasePresentParticipantIDs)
	if (e.Wave.State == domain.WaveStateReady) != allReady {
		return goldenWaveError("Wave ready state does not match exact membership")
	}
	return nil
}

func validateGoldenConsumedWindowState(e GoldenWaveExecution) error {
	if e.Wave.ReadyWindow.State != domain.ReadyWindowStateConsumed ||
		e.Wave.State != domain.WaveStateActive || e.Attempt.State != domain.GoldenAttemptStateActive ||
		e.Start == nil {
		return goldenWaveError("started Wave, window and attempt states diverged")
	}
	return nil
}

func validateGoldenMembershipBinding(e GoldenWaveExecution) error {
	m := e.Membership
	if m.ID == uuid.Nil || m.RevisionID == uuid.Nil || m.Revision != 1 ||
		!MembershipRevisionsEqual(m.Source, e.Source.Membership) ||
		!IDsCanonical(m.ParticipantIDs) || m.PayloadDigest != DigestIDs(m.ParticipantIDs) {
		return goldenWaveError("invalid exact membership binding")
	}
	return nil
}

func validateGoldenAttemptAssignment(e GoldenWaveExecution) error {
	a := e.Assignment
	if err := validateGoldenAssignmentIdentity(e, a); err != nil {
		return err
	}
	if err := validateGoldenAssignmentArtifact(a); err != nil {
		return err
	}
	return validateGoldenPrivateAssignments(a)
}

func validateGoldenAssignmentIdentity(e GoldenWaveExecution, a GoldenAttemptAssignment) error {
	if a.ID == uuid.Nil || a.RevisionID == uuid.Nil || a.Revision != 1 || a.Scope != e.Scope ||
		a.AttemptID != e.Attempt.ID || a.WaveID != e.Wave.ID || a.MembershipID != e.Membership.ID ||
		a.Plan != e.Source.Plan || a.EdgeID == uuid.Nil || a.ReservationID == uuid.Nil {
		return goldenWaveError("invalid immutable assignment identity")
	}
	return nil
}

func validateGoldenAssignmentArtifact(a GoldenAttemptAssignment) error {
	digest, digestErr := taskexec.SnapshotDigest(a.Snapshot)
	if a.Snapshot.Validate() != nil || digestErr != nil || digest != a.ContentDigest ||
		a.ContentDigest == [sha256.Size]byte{} ||
		a.Snapshot.SnapshotID == uuid.Nil || a.PayloadDigest != goldenAssignmentDigest(a) || len(a.Private) < 2 {
		return goldenWaveError("invalid immutable assignment artifact")
	}
	return nil
}

func validateGoldenPrivateAssignments(a GoldenAttemptAssignment) error {
	seen := make(map[uuid.UUID]struct{}, len(a.Private))
	for _, private := range a.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != a.Snapshot.SnapshotID ||
			private.ContentDigest != a.ContentDigest {
			return goldenWaveError("invalid private assignment")
		}
		if _, duplicate := seen[private.ID]; duplicate {
			return goldenWaveError("private assignment identity is reused")
		}
		seen[private.ID] = struct{}{}
	}
	return nil
}
