package golden

import (
	"crypto/sha256"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenDiscardedOrderEntry struct {
	SubmissionID   uint64
	ParticipantID  uuid.UUID
	CommittedAt    time.Time
	EvidenceDigest [sha256.Size]byte
}

type GoldenFailureReplacement struct {
	Group      domain.GoldenGroupState
	Attempt    domain.GoldenAttempt
	Assignment GoldenReserveAttemptAssignment
	Membership GoldenWaveMembershipBinding
	Wave       domain.Wave
	Window     GoldenReadyWindow
	OpenedAt   time.Time
	Deadline   time.Time
}

func (r GoldenFailureReplacement) Snapshot() GoldenFailureReplacement {
	clone := r
	clone.Group = CloneGroup(r.Group)
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Membership = CloneMembershipBinding(r.Membership)
	clone.Wave = CloneExecution(r.Wave)
	clone.Window = CloneReadyWindow(r.Window)
	return clone
}

func (r GoldenFailureReplacement) Validate() error {
	if !validGoldenFailureReplacementAttempt(r) || !validGoldenFailureReplacementAssignment(r) {
		return goldenFailureError("invalid replacement attempt or assignment")
	}
	if !validGoldenFailureReplacementWave(r) || !validGoldenFailureReplacementWindow(r) ||
		!validGoldenFailureReplacementMembership(r) {
		return goldenFailureError("invalid unstarted replacement Wave")
	}
	return nil
}

func validGoldenFailureReplacementAttempt(replacement GoldenFailureReplacement) bool {
	if _, err := domain.NewGoldenGroup(replacement.Group); err != nil ||
		replacement.Attempt.Validate() != nil || len(replacement.Group.Attempts) == 0 {
		return false
	}
	return replacement.Attempt.State == domain.GoldenAttemptStatePlanned &&
		reflect.DeepEqual(replacement.Group.Attempts[len(replacement.Group.Attempts)-1], replacement.Attempt)
}

func validGoldenFailureReplacementAssignment(replacement GoldenFailureReplacement) bool {
	assignment := replacement.Assignment
	return assignment.Validate() == nil && assignment.AttemptID == replacement.Attempt.ID &&
		assignment.Scope.GroupID == replacement.Attempt.GroupID &&
		assignment.Scope.GroupRevisionID == replacement.Attempt.GroupRevisionID &&
		EqualIDs(replacement.Attempt.ParticipantIDs, PrivateAssignmentParticipantIDs(assignment.Private))
}

func validGoldenFailureReplacementWave(replacement GoldenFailureReplacement) bool {
	wave := replacement.Wave
	if wave.Validate() != nil || wave.State != domain.WaveStateReadyWindowOpen || wave.StartedAt != nil ||
		wave.ReadyWindow == nil || wave.ReadyWindow.RevisionID.IsZero() {
		return false
	}
	return wave.ReadyWindow.ID == replacement.Window.ID &&
		wave.ReadyWindow.OpenedAt.Equal(replacement.OpenedAt) && wave.ReadyWindow.Deadline.Equal(replacement.Deadline) &&
		EqualIDs(MemberIDs(wave), replacement.Attempt.ParticipantIDs) &&
		len(ReadyMemberIDs(wave)) == 0
}

func validGoldenFailureReplacementWindow(replacement GoldenFailureReplacement) bool {
	window := replacement.Window
	return domain.IsValidReadyWindowInterval(replacement.OpenedAt, replacement.Deadline) &&
		window.State == GoldenReadyWindowOpen && ValidateReadyWindowIdentity(window) == nil &&
		ValidateReadyWindowParticipants(window) == nil && len(window.ReadyParticipantIDs) == 0 &&
		EqualIDs(window.PresentParticipantIDs, replacement.Attempt.ParticipantIDs) &&
		EqualIDs(window.BasePresentParticipantIDs, replacement.Attempt.ParticipantIDs)
}

func validGoldenFailureReplacementMembership(replacement GoldenFailureReplacement) bool {
	membership := replacement.Membership
	return membership.ID != uuid.Nil && membership.RevisionID != uuid.Nil && membership.Revision == 1 &&
		membership.Source.RevisionID != uuid.Nil && membership.Source.Revision >= 1 &&
		membership.PayloadDigest == DigestIDs(membership.ParticipantIDs) &&
		EqualIDs(membership.ParticipantIDs, replacement.Attempt.ParticipantIDs)
}

type GoldenGroupOperationalState string

const GoldenGroupStateTechnicalPause GoldenGroupOperationalState = "technical_pause"

type GoldenGroupTechnicalPause struct {
	GroupID                uuid.UUID
	GroupRevisionID        domain.DerivedRevisionID
	State                  GoldenGroupOperationalState
	RequiredOperatorAction string
	PausedAt               time.Time
}

type GoldenFailureRecord struct {
	ID                   uuid.UUID
	CommandID            uuid.UUID
	CommandDigest        [sha256.Size]byte
	Route                GoldenFailureRoute
	Scope                GoldenSubmissionScope
	Expected             GoldenFailureExpectation
	Classification       GoldenFailureClassification
	FailedAssignment     GoldenAttemptAssignmentEvidence
	FailedAt             time.Time
	Attempt              domain.GoldenAttempt
	Group                domain.GoldenGroupState
	OldWave              domain.Wave
	DiscardedSubmissions GoldenSubmissionLedgerExpectation
	DiscardedOrder       []GoldenSubmissionRecord
	PriorPositions       GoldenPositionLedger
	Positions            GoldenPositionLedger
	Replacement          *GoldenFailureReplacement
	TechnicalPause       *GoldenGroupTechnicalPause
	NewIdentityIDs       []uuid.UUID
	PayloadDigest        [sha256.Size]byte
}

func (r GoldenFailureRecord) Snapshot() GoldenFailureRecord {
	clone := r
	clone.Expected.State = CloneExpectation(r.Expected.State)
	clone.Expected.Execution = CloneExecutionExpectation(r.Expected.Execution)
	clone.Classification = r.Classification.Snapshot()
	clone.FailedAssignment = r.FailedAssignment.Snapshot()
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Group = CloneGroup(r.Group)
	clone.OldWave = CloneExecution(r.OldWave)
	clone.DiscardedOrder = append([]GoldenSubmissionRecord(nil), r.DiscardedOrder...)
	clone.PriorPositions = r.PriorPositions.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	if r.Replacement != nil {
		replacement := r.Replacement.Snapshot()
		clone.Replacement = &replacement
	}
	if r.TechnicalPause != nil {
		pause := *r.TechnicalPause
		clone.TechnicalPause = &pause
	}
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	return clone
}

func (r GoldenFailureRecord) Validate() error {
	if !validGoldenFailureRecordIdentity(r) || !validGoldenFailureRecordExpectedHeads(r) ||
		!validGoldenFailureRecordRetainedHeads(r) || !validGoldenFailureRecordLedgers(r) ||
		!validGoldenFailureRecordIdentitySet(r) {
		return goldenFailureError("invalid failure receipt header or retained heads")
	}
	if !validGoldenFailureRecordAttempt(r) {
		return goldenFailureError("failed attempt was not retained as void")
	}
	if !validGoldenFailureRecordOldWave(r) {
		return goldenFailureError("old Wave was not completed")
	}
	if err := validateGoldenFailurePriorPositions(r.Group, r.Positions); err != nil {
		return err
	}
	if !validGoldenFailureRecordAssignment(r) {
		return goldenFailureError("failed assignment does not match locked edge")
	}
	if err := validateGoldenDiscardedOrder(r); err != nil {
		return err
	}
	if err := validateGoldenFailureRecordRoute(r); err != nil {
		return err
	}
	payload, err := goldenFailureRecordPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenFailureError("failure receipt digest changed")
	}
	return nil
}

func validGoldenFailureRecordIdentity(record GoldenFailureRecord) bool {
	return record.ID != uuid.Nil && record.CommandID != uuid.Nil && record.ID != record.CommandID &&
		record.CommandDigest != [sha256.Size]byte{} && record.Scope.IsValid() &&
		record.Expected.Scope == record.Scope && domain.IsValidServerTime(record.FailedAt)
}

func validGoldenFailureRecordExpectedHeads(record GoldenFailureRecord) bool {
	expected := record.Expected
	if !domain.IsValidServerTime(expected.StartedAt) || !domain.IsValidServerTime(expected.Deadline) ||
		!expected.StartedAt.Before(expected.Deadline) || expected.State.Scope != record.Scope.State ||
		expected.Execution.Scope != record.Scope.State || !expected.Execution.Started {
		return false
	}
	return expected.Execution.AttemptID == record.Scope.AttemptID && expected.Execution.WaveID == record.Scope.WaveID &&
		expected.Execution.AssignmentID == record.Scope.AssignmentID && expected.Plan == expected.State.Plan &&
		expected.Submissions.Scope == record.Scope
}

func validGoldenFailureRecordRetainedHeads(record GoldenFailureRecord) bool {
	return record.Classification.Validate() == nil &&
		record.Classification.PayloadDigest == record.Expected.ClassificationDigest &&
		record.Classification.Plan == record.Expected.Plan && record.FailedAssignment.Validate() == nil &&
		record.DiscardedSubmissions == record.Expected.Submissions
}

func validGoldenFailureRecordLedgers(record GoldenFailureRecord) bool {
	return record.PriorPositions.Validate() == nil && record.Positions.Validate() == nil &&
		record.PriorPositions.Expectation().Equal(record.Expected.Positions) &&
		reflect.DeepEqual(record.PriorPositions, record.Positions)
}

func validGoldenFailureRecordIdentitySet(record GoldenFailureRecord) bool {
	return IDsCanonical(record.NewIdentityIDs) &&
		EqualIDs(record.NewIdentityIDs, goldenFailureRecordIdentityIDs(record))
}

func validGoldenFailureRecordAttempt(record GoldenFailureRecord) bool {
	attempt := record.Attempt
	if attempt.Validate() != nil || attempt.State != domain.GoldenAttemptStateVoid ||
		attempt.ID != record.Scope.AttemptID || attempt.FinishedAt == nil || attempt.StartedAt == nil ||
		len(record.Group.Attempts) == 0 {
		return false
	}
	return attempt.FinishedAt.Equal(record.FailedAt) && attempt.StartedAt.Equal(record.Expected.StartedAt) &&
		attempt.AttemptNo == record.Classification.FailedAttemptNo &&
		EqualIDs(attempt.ParticipantIDs, record.Classification.ParticipantIDs) &&
		reflect.DeepEqual(record.Group.Attempts[len(record.Group.Attempts)-1], attempt)
}

func validGoldenFailureRecordOldWave(record GoldenFailureRecord) bool {
	if _, err := domain.NewGoldenGroup(record.Group); err != nil || record.OldWave.Validate() != nil ||
		record.OldWave.StartedAt == nil {
		return false
	}
	return record.OldWave.State == domain.WaveStateCompleted && record.OldWave.ID == record.Scope.WaveID &&
		record.OldWave.StartedAt.Equal(record.Expected.StartedAt) &&
		!record.FailedAt.Before(*record.OldWave.StartedAt) &&
		EqualIDs(MemberIDs(record.OldWave), record.Classification.ParticipantIDs)
}

func validGoldenFailureRecordAssignment(record GoldenFailureRecord) bool {
	assignment := record.FailedAssignment
	failed := record.Classification.FailedEdge
	if assignment.ID != record.Scope.AssignmentID || assignment.AttemptID != record.Scope.AttemptID ||
		assignment.WaveID != record.Scope.WaveID || assignment.EdgeID != failed.ID || assignment.Plan != record.Expected.Plan {
		return false
	}
	return assignment.ReservationID == failed.ReservationID && assignment.SnapshotID == failed.SnapshotID &&
		assignment.TaskID == failed.TaskID && assignment.ContentDigest == failed.ContentDigest &&
		assignment.ExecutionPayloadDigest == record.Expected.Execution.AssignmentDigest &&
		EqualIDs(PrivateAssignmentParticipantIDs(assignment.Private), record.Classification.ParticipantIDs)
}

func validateGoldenFailureRecordRoute(record GoldenFailureRecord) error {
	switch record.Route {
	case GoldenFailureRouteReplay:
		return validateGoldenFailureReplayRoute(record)
	case GoldenFailureRouteExhausted:
		return validateGoldenFailureExhaustedRoute(record)
	default:
		return goldenFailureError("unknown failure route")
	}
}

func validateGoldenFailureReplayRoute(record GoldenFailureRecord) error {
	if record.Classification.Exhausted || record.Replacement == nil || record.TechnicalPause != nil ||
		record.Classification.NextEdge == nil || record.Replacement.Validate() != nil {
		return goldenFailureError("replay receipt does not match next reserve")
	}
	if !validGoldenFailureReplayReplacement(record) {
		return goldenFailureError("replay receipt does not match next reserve")
	}
	prefix := CloneGroup(record.Replacement.Group)
	prefix.Attempts = prefix.Attempts[:len(prefix.Attempts)-1]
	if !validGoldenFailureReplayMembership(record, prefix) {
		return goldenFailureError("replacement changed void group or membership authority")
	}
	return nil
}

func validGoldenFailureReplayReplacement(record GoldenFailureRecord) bool {
	replacement := record.Replacement
	next := record.Classification.NextEdge
	return replacement.Assignment.EdgeID == next.ID && replacement.OpenedAt.Equal(record.FailedAt) &&
		replacement.Window.OpenedAt.Equal(record.FailedAt) && replacement.Assignment.Scope == record.Scope.State &&
		replacement.Assignment.ReservationID == next.ReservationID && replacement.Assignment.SnapshotID == next.SnapshotID &&
		replacement.Assignment.TaskID == next.TaskID && replacement.Assignment.ContentDigest == next.ContentDigest &&
		EqualIDs(replacement.Attempt.ParticipantIDs, record.Classification.ParticipantIDs)
}

func validGoldenFailureReplayMembership(record GoldenFailureRecord, prefix domain.GoldenGroupState) bool {
	replacement := record.Replacement
	return reflect.DeepEqual(prefix, record.Group) &&
		MembershipRevisionsEqual(replacement.Membership.Source, record.Expected.State.Membership) &&
		IDsCanonical(replacement.Membership.ParticipantIDs) &&
		replacement.Membership.PayloadDigest == DigestIDs(replacement.Attempt.ParticipantIDs)
}

func validateGoldenFailureExhaustedRoute(record GoldenFailureRecord) error {
	if !record.Classification.Exhausted || record.Replacement != nil || record.TechnicalPause == nil {
		return goldenFailureError("exhaustion receipt lacks affected-group operator action")
	}
	pause := record.TechnicalPause
	action := strings.TrimSpace(pause.RequiredOperatorAction)
	if pause.State != GoldenGroupStateTechnicalPause || pause.GroupID != record.Scope.State.GroupID ||
		pause.GroupRevisionID != record.Scope.State.GroupRevisionID || action == "" ||
		action != pause.RequiredOperatorAction || len(action) > goldenFailureOperatorActionLimit ||
		!pause.PausedAt.Equal(record.FailedAt) {
		return goldenFailureError("exhaustion receipt lacks affected-group operator action")
	}
	return nil
}

func goldenFailureRecordIdentityIDs(record GoldenFailureRecord) []uuid.UUID {
	identities := []uuid.UUID{record.CommandID, record.ID, record.OldWave.RevisionID.UUID()}
	if record.Replacement != nil {
		replacement := record.Replacement
		identities = append(identities,
			replacement.Attempt.ID, replacement.Assignment.ID, replacement.Assignment.RevisionID,
			replacement.Wave.ID, replacement.Wave.RevisionID.UUID(), replacement.Window.ID,
			replacement.Wave.ReadyWindow.RevisionID.UUID(), replacement.Window.RevisionID,
			replacement.Window.ReadinessRevisionID, replacement.Window.PresenceRevisionID,
			replacement.Membership.ID, replacement.Membership.RevisionID,
		)
		for _, assignment := range replacement.Assignment.Private {
			identities = append(identities, assignment.ID)
		}
	}
	SortIDs(identities)
	return identities
}

func validateGoldenDiscardedOrder(record GoldenFailureRecord) error {
	if record.DiscardedSubmissions.NextSubmissionID != uint64(len(record.DiscardedOrder))+1 {
		return goldenFailureError("discarded order does not cover current submissions")
	}
	for index, submission := range record.DiscardedOrder {
		if !ValidRecord(submission, record.Scope, uint64(index+1)) {
			return goldenFailureError("invalid discarded provisional position")
		}
		if index > 0 {
			prior := record.DiscardedOrder[index-1]
			if submission.CommittedAt.Before(prior.CommittedAt) ||
				(submission.CommittedAt.Equal(prior.CommittedAt) && submission.ID <= prior.ID) {
				return goldenFailureError("discarded provisional order changed")
			}
		}
	}
	return nil
}
