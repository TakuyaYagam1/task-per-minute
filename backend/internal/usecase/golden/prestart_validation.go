package golden

import (
	"crypto/sha256"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r RetainedGoldenPrestartRecord) Validate() error {
	if !validRetainedGoldenPrestartIdentity(r) || !validRetainedGoldenPrestartAuthorization(r) ||
		!validRetainedGoldenPrestartSource(r) || !validRetainedGoldenPrestartAssignment(r) ||
		!validRetainedGoldenPrestartMembership(r) || !validRetainedGoldenPrestartWindow(r) ||
		!validRetainedGoldenPrestartNewIdentities(r) {
		return goldenPrestartError("invalid retained pre-start receipt")
	}
	if !validRetainedGoldenPrestartGroup(r) {
		return goldenPrestartError("retained group lost its unstarted attempt")
	}
	if err := validateRetainedGoldenPrestartState(r); err != nil {
		return err
	}
	payload, err := goldenPrestartRecordPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenPrestartError("retained pre-start receipt digest changed")
	}
	return nil
}

func validRetainedGoldenPrestartIdentity(r RetainedGoldenPrestartRecord) bool {
	return ValidStateScope(r.Scope) && r.SessionID != uuid.Nil && r.CommandID != uuid.Nil &&
		r.CommandDigest != [sha256.Size]byte{} && r.ActorID != uuid.Nil && r.RevisionID != uuid.Nil &&
		r.Revision >= 1 && prestartValidRevisionPredecessor(r.RevisionID, r.Revision, r.PreviousRevisionID) &&
		domain.IsValidServerTime(r.OccurredAt) && r.Reason == GoldenPrestartPauseOperatorManual
}

func validRetainedGoldenPrestartAuthorization(r RetainedGoldenPrestartRecord) bool {
	return r.Authorization.TournamentID == r.Scope.TournamentID && r.Authorization.ActorID == r.ActorID &&
		r.Authorization.RevisionID != uuid.Nil && r.Authorization.Revision >= 1 &&
		r.Authorization.PayloadDigest != [sha256.Size]byte{}
}

func validRetainedGoldenPrestartSource(r RetainedGoldenPrestartRecord) bool {
	return r.SourceExecution.Scope == r.Scope && !r.SourceExecution.Started && r.Attempt.Validate() == nil &&
		r.Attempt.ID == r.SourceExecution.AttemptID && r.Attempt.GroupID == r.Scope.GroupID &&
		r.Attempt.GroupRevisionID == r.Scope.GroupRevisionID && r.Attempt.StartedAt == nil &&
		r.Attempt.RetainedAt != nil && r.WaveID == r.SourceExecution.WaveID
}

func validRetainedGoldenPrestartAssignment(r RetainedGoldenPrestartRecord) bool {
	return r.Assignment.Validate() == nil && r.Assignment.ID == r.SourceExecution.AssignmentID &&
		r.Assignment.WaveID == r.WaveID && r.Assignment.RevisionID == r.SourceExecution.AssignmentRevisionID &&
		r.Assignment.Revision == r.SourceExecution.AssignmentRevision && r.Assignment.Scope == r.Scope &&
		r.Assignment.AttemptID == r.Attempt.ID && r.Assignment.MembershipID == r.SourceExecution.MembershipID &&
		r.Assignment.Plan == r.SourceExecution.Source.Plan &&
		r.Assignment.ExecutionPayloadDigest == r.SourceExecution.AssignmentDigest &&
		EqualIDs(PrivateAssignmentParticipantIDs(r.Assignment.Private), r.Membership.ParticipantIDs)
}

func validRetainedGoldenPrestartMembership(r RetainedGoldenPrestartRecord) bool {
	return r.Membership.ID == r.SourceExecution.MembershipID &&
		r.Membership.RevisionID == r.SourceExecution.MembershipRevisionID &&
		r.Membership.Revision == r.SourceExecution.MembershipRevision &&
		r.Membership.PayloadDigest == r.SourceExecution.MembershipDigest &&
		MembershipRevisionsEqual(r.Membership.Source, r.SourceExecution.Source.Membership) &&
		IDsCanonical(r.Membership.ParticipantIDs) &&
		r.Membership.PayloadDigest == DigestIDs(r.Membership.ParticipantIDs) &&
		EqualIDs(r.Membership.ParticipantIDs, r.Attempt.ParticipantIDs)
}

func validRetainedGoldenPrestartWindow(r RetainedGoldenPrestartRecord) bool {
	return r.SupersededWindow.ID == r.SourceExecution.Window.WindowID &&
		r.SupersededWindow.AttemptID == r.Attempt.ID && r.SupersededWindow.AttemptNo == r.Attempt.AttemptNo &&
		r.SupersededWindow.Expectation() == r.SourceExecution.Window &&
		ValidateReadyWindowIdentity(r.SupersededWindow) == nil &&
		ValidateReadyWindowParticipants(r.SupersededWindow) == nil &&
		EqualIDs(r.SupersededWindow.BasePresentParticipantIDs, r.Membership.ParticipantIDs)
}

func validRetainedGoldenPrestartNewIdentities(r RetainedGoldenPrestartRecord) bool {
	return IDsCanonical(r.NewIdentityIDs) && ValidIdentitySet(r.NewIdentityIDs) &&
		EqualIDs(r.NewIdentityIDs, retainedGoldenRecordIdentityIDs(r))
}

func validRetainedGoldenPrestartGroup(r RetainedGoldenPrestartRecord) bool {
	_, err := domain.NewGoldenGroup(r.Group)
	return err == nil && len(r.Group.Attempts) > 0 && r.Group.ID == r.Scope.GroupID &&
		r.Group.RevisionID == r.Scope.GroupRevisionID && r.Group.TournamentID == r.Scope.TournamentID &&
		r.Group.SourceProjectionRevisionID == r.SourceExecution.Source.SourceProjectionRevisionID &&
		EqualIDs(retainedGoldenActiveMemberIDs(r.Group.Members), r.Membership.ParticipantIDs) &&
		reflect.DeepEqual(r.Group.Attempts[len(r.Group.Attempts)-1], r.Attempt)
}

func validateRetainedGoldenPrestartState(r RetainedGoldenPrestartRecord) error {
	switch r.State {
	case RetainedGoldenPrestartPaused:
		return validateRetainedGoldenPausedRecord(r)
	case RetainedGoldenPrestartReady:
		return validateRetainedGoldenReadyRecord(r)
	default:
		return goldenPrestartError("unknown retained pre-start state")
	}
}

func validateRetainedGoldenPausedRecord(r RetainedGoldenPrestartRecord) error {
	if r.Revision != 1 || r.PreviousRevisionID != nil || r.FreshWindow != nil ||
		r.FreshExecution != nil || r.FreshExecutionRevisionID != uuid.Nil || !r.FreshWaveRevisionID.IsZero() ||
		!r.FreshWaveWindowRevisionID.IsZero() || !r.Attempt.RetainedAt.Equal(r.OccurredAt) {
		return goldenPrestartError("paused receipt published a live successor")
	}
	return nil
}

func validateRetainedGoldenReadyRecord(r RetainedGoldenPrestartRecord) error {
	if r.Revision < 2 || r.PreviousRevisionID == nil || r.FreshWindow == nil || r.FreshExecution == nil {
		return goldenPrestartError("ready receipt did not publish the retained execution")
	}
	if !validRetainedGoldenFreshExecution(r) || !validRetainedGoldenFreshWindow(r) {
		return goldenPrestartError("ready receipt did not publish the retained execution")
	}
	return nil
}

func validRetainedGoldenFreshExecution(r RetainedGoldenPrestartRecord) bool {
	return r.FreshExecutionRevisionID == r.FreshExecution.RevisionID &&
		r.FreshWaveRevisionID == r.FreshExecution.WaveRevisionID && r.FreshExecution.Scope == r.Scope &&
		!r.FreshWaveWindowRevisionID.IsZero() && r.FreshExecution.AttemptID == r.Attempt.ID &&
		r.FreshExecution.WaveID == r.WaveID && r.FreshExecution.AssignmentID == r.Assignment.ID &&
		r.FreshExecution.AssignmentDigest == r.Assignment.ExecutionPayloadDigest &&
		r.FreshExecution.MembershipID == r.Membership.ID &&
		r.FreshExecution.MembershipDigest == r.Membership.PayloadDigest &&
		r.FreshExecution.Window == r.FreshWindow.Expectation()
}

func validRetainedGoldenFreshWindow(r RetainedGoldenPrestartRecord) bool {
	return !r.OccurredAt.Before(*r.Attempt.RetainedAt) && len(r.FreshWindow.ReadyParticipantIDs) == 0 &&
		EqualIDs(r.FreshWindow.PresentParticipantIDs, r.Membership.ParticipantIDs)
}

type RetainedGoldenPrestartAuthority struct {
	Execution         *GoldenWaveExecution
	ArchivedExecution *GoldenWaveExecution
	Authorization     GoldenPrestartOperatorAuthorization
	Current           *RetainedGoldenPrestartRecord
}

func (a RetainedGoldenPrestartAuthority) Snapshot() RetainedGoldenPrestartAuthority {
	clone := a
	if a.Execution != nil {
		execution := a.Execution.Snapshot()
		clone.Execution = &execution
	}
	if a.ArchivedExecution != nil {
		execution := a.ArchivedExecution.Snapshot()
		clone.ArchivedExecution = &execution
	}
	if a.Current != nil {
		current := a.Current.Snapshot()
		clone.Current = &current
	}
	return clone
}

func (a RetainedGoldenPrestartAuthority) Validate() error {
	if err := validateRetainedGoldenPrestartAuthorityDocuments(a); err != nil {
		return err
	}
	if err := validateRetainedGoldenPrestartAuthorityGeneration(a); err != nil {
		return err
	}
	return validateRetainedGoldenPrestartAuthorityScope(a)
}

func validateRetainedGoldenPrestartAuthorityDocuments(a RetainedGoldenPrestartAuthority) error {
	if a.Authorization.Validate() != nil {
		return goldenPrestartError("malformed pre-start authority")
	}
	if a.Execution != nil && a.Execution.Validate() != nil {
		return goldenPrestartError("malformed live pre-start execution")
	}
	if a.ArchivedExecution != nil && a.ArchivedExecution.Validate() != nil {
		return goldenPrestartError("malformed archived pre-start execution")
	}
	if a.Current != nil && a.Current.Validate() != nil {
		return goldenPrestartError("malformed retained pre-start session")
	}
	return nil
}

func validateRetainedGoldenPrestartAuthorityGeneration(a RetainedGoldenPrestartAuthority) error {
	switch {
	case a.Current == nil:
		if a.Execution == nil || a.ArchivedExecution != nil {
			return goldenPrestartError("pre-start authority has an invalid initial generation")
		}
	case a.Current.State == RetainedGoldenPrestartPaused:
		if a.Execution != nil || a.ArchivedExecution == nil ||
			!retainedGoldenArchivedExecutionMatchesRecord(*a.ArchivedExecution, *a.Current) {
			return goldenPrestartError("paused session lost exact archived execution authority")
		}
	case a.Current.State == RetainedGoldenPrestartReady:
		if a.Execution == nil || a.ArchivedExecution == nil || a.Current.FreshExecution == nil ||
			!retainedGoldenArchivedExecutionMatchesRecord(*a.ArchivedExecution, *a.Current) ||
			!a.Execution.Expectation().Equal(*a.Current.FreshExecution) {
			return goldenPrestartError("ready session does not own its archived and live generations")
		}
	default:
		return goldenPrestartError("pre-start authority has an unknown retained state")
	}
	return nil
}

func validateRetainedGoldenPrestartAuthorityScope(a RetainedGoldenPrestartAuthority) error {
	if a.Execution != nil && a.Execution.Scope.TournamentID != a.Authorization.TournamentID {
		return goldenPrestartError("operator authorization belongs to another tournament")
	}
	if a.Current != nil && a.Current.Scope.TournamentID != a.Authorization.TournamentID {
		return goldenPrestartError("retained session belongs to another tournament")
	}
	return nil
}

func retainedGoldenArchivedExecutionMatchesRecord(
	archived GoldenWaveExecution,
	record RetainedGoldenPrestartRecord,
) bool {
	if archived.Validate() != nil || !archived.Expectation().Equal(record.SourceExecution) ||
		archived.Scope != record.Scope || archived.Wave.ID != record.WaveID ||
		!reflect.DeepEqual(archived.Membership, record.Membership) ||
		!reflect.DeepEqual(archived.Window, record.SupersededWindow) {
		return false
	}
	assignment, err := BuildAttemptAssignmentEvidence(archived.Assignment)
	if err != nil || !reflect.DeepEqual(assignment, record.Assignment) {
		return false
	}
	attempt := CloneAttempt(record.Attempt)
	attempt.RetainedAt = prestartCloneTimePointer(archived.Attempt.RetainedAt)
	if !reflect.DeepEqual(attempt, archived.Attempt) {
		return false
	}
	group := CloneGroup(record.Group)
	group.Attempts[len(group.Attempts)-1] = attempt
	return reflect.DeepEqual(group, archived.Group)
}

func retainedGoldenActiveMemberIDs(members []domain.GoldenMember) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		if !member.Excluded {
			ids = append(ids, member.ParticipantID)
		}
	}
	SortIDs(ids)
	return ids
}
