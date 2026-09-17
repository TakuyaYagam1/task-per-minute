package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func buildRetainedGoldenPauseRecord(
	execution GoldenWaveExecution,
	command RetainedGoldenPrestartPauseCommand,
	digest [sha256.Size]byte,
	pausedAt time.Time,
) (RetainedGoldenPrestartRecord, error) {
	group, err := domain.NewGoldenGroup(execution.Group)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("load retained attempt")
	}
	changed, err := group.RetainAttemptBeforeStart(execution.Attempt.ID, pausedAt)
	if err != nil || !changed {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("retain unstarted attempt")
	}
	retainedGroup := group.Snapshot()
	attempt := retainedGroup.Attempts[len(retainedGroup.Attempts)-1]
	assignment, err := BuildAttemptAssignmentEvidence(execution.Assignment)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("sanitize retained assignment")
	}
	identities := []uuid.UUID{command.CommandID, command.SessionID, command.NextSessionRevisionID}
	SortIDs(identities)
	if !ValidIdentitySet(identities) ||
		goldenPrestartIDsAlias(identities, command.ActorID, command.ExpectedAuthorization.RevisionID) ||
		goldenPrestartAliasesExecution(execution, identities) {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("retained pause identity is reused")
	}
	record := RetainedGoldenPrestartRecord{
		SessionID: command.SessionID, CommandID: command.CommandID, CommandDigest: digest,
		ActorID: command.ActorID, Authorization: command.ExpectedAuthorization,
		Scope: command.Scope, RevisionID: command.NextSessionRevisionID, Revision: 1,
		State: RetainedGoldenPrestartPaused, Reason: command.Reason, OccurredAt: pausedAt,
		SourceExecution: execution.Expectation(), Group: retainedGroup, Attempt: attempt, WaveID: execution.Wave.ID,
		Assignment:       assignment,
		Membership:       CloneMembershipBinding(execution.Membership),
		SupersededWindow: CloneReadyWindow(execution.Window), NewIdentityIDs: identities,
	}
	return sealRetainedGoldenPrestartRecord(record)
}

func buildRetainedGoldenResumeRecord(
	paused RetainedGoldenPrestartRecord,
	archived GoldenWaveExecution,
	command RetainedGoldenPrestartResumeCommand,
	digest [sha256.Size]byte,
	resumedAt time.Time,
) (RetainedGoldenPrestartRecord, GoldenWaveExecution, error) {
	if resumedAt.Before(paused.OccurredAt) {
		return RetainedGoldenPrestartRecord{}, GoldenWaveExecution{}, goldenPrestartError("resume time precedes retained pause")
	}
	identities := []uuid.UUID{
		command.CommandID, command.NextSessionRevisionID, command.NextExecutionRevisionID,
		command.NextWaveRevisionID.UUID(), command.NextWindowID, command.NextWaveWindowRevisionID.UUID(),
		command.NextWindowRevisionID, command.NextReadinessRevisionID, command.NextPresenceRevisionID,
	}
	SortIDs(identities)
	if !ValidIdentitySet(identities) ||
		goldenPrestartIDsAlias(identities,
			command.ActorID, command.ExpectedAuthorization.RevisionID,
			command.Scope.TournamentID, command.Scope.GroupID, command.Scope.GroupRevisionID.UUID()) ||
		goldenPrestartAliasesRecord(paused, identities) {
		return RetainedGoldenPrestartRecord{}, GoldenWaveExecution{}, goldenPrestartError("retained resume identity is reused")
	}
	if archived.Validate() != nil || !archived.Expectation().Equal(paused.SourceExecution) {
		return RetainedGoldenPrestartRecord{}, GoldenWaveExecution{}, goldenPrestartError("archived execution authority changed")
	}
	execution, err := buildFreshRetainedGoldenExecution(paused, archived, command, digest, resumedAt)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, GoldenWaveExecution{}, err
	}
	record := paused.Snapshot()
	record.CommandID = command.CommandID
	record.CommandDigest = digest
	record.ActorID = command.ActorID
	record.Authorization = command.ExpectedAuthorization
	record.PreviousRevisionID = UUIDPointer(record.RevisionID)
	record.RevisionID = command.NextSessionRevisionID
	record.Revision++
	record.State = RetainedGoldenPrestartReady
	record.OccurredAt = resumedAt
	record.FreshExecutionRevisionID = execution.RevisionID
	record.FreshWaveRevisionID = execution.Wave.RevisionID
	record.FreshWaveWindowRevisionID = execution.Wave.ReadyWindow.RevisionID
	freshWindow := CloneReadyWindow(execution.Window)
	record.FreshWindow = &freshWindow
	freshExecution := execution.Expectation()
	record.FreshExecution = &freshExecution
	record.NewIdentityIDs = identities
	record.PayloadDigest = [sha256.Size]byte{}
	sealed, err := sealRetainedGoldenPrestartRecord(record)
	return sealed, execution, err
}

func buildFreshRetainedGoldenExecution(
	paused RetainedGoldenPrestartRecord,
	archived GoldenWaveExecution,
	command RetainedGoldenPrestartResumeCommand,
	digest [sha256.Size]byte,
	resumedAt time.Time,
) (GoldenWaveExecution, error) {
	deadline := resumedAt.Add(domain.ReadyWindowDuration)
	attempt := CloneAttempt(paused.Attempt)
	group := CloneGroup(paused.Group)
	groupBindingDigest, err := ExecutionGroupBindingDigest(group, group.ParticipationEstablished)
	if err != nil {
		return GoldenWaveExecution{}, goldenPrestartError("encode retained group binding")
	}
	wave := domain.Wave{
		ID: paused.WaveID, TournamentID: paused.Scope.TournamentID,
		RevisionID: command.NextWaveRevisionID, State: domain.WaveStatePlanned,
		Members: BuildMembers(paused.Membership.ParticipantIDs),
	}
	if err := wave.OpenReadyWindow(command.NextWindowID, command.NextWaveWindowRevisionID, resumedAt, deadline); err != nil {
		return GoldenWaveExecution{}, goldenPrestartError("open retained Wave window")
	}
	window := GoldenReadyWindow{
		ID: command.NextWindowID, RevisionID: command.NextWindowRevisionID, Revision: 1,
		AttemptID: attempt.ID, AttemptNo: attempt.AttemptNo, OpenedAt: resumedAt, Deadline: deadline,
		State: GoldenReadyWindowOpen, ReadinessRevisionID: command.NextReadinessRevisionID, ReadinessRevision: 1,
		PresenceRevisionID: command.NextPresenceRevisionID, PresenceRevision: 1,
		BasePresentParticipantIDs: append([]uuid.UUID(nil), paused.Membership.ParticipantIDs...),
		PresentParticipantIDs:     append([]uuid.UUID(nil), paused.Membership.ParticipantIDs...),
		ReadinessDigest:           DigestIDs(nil),
		PresenceDigest:            DigestIDs(paused.Membership.ParticipantIDs),
	}
	execution := GoldenWaveExecution{
		Scope: paused.Scope, Source: CloneExpectation(paused.SourceExecution.Source),
		RevisionID: command.NextExecutionRevisionID, Revision: 1,
		Group: group, GroupBindingDigest: groupBindingDigest,
		OpeningParticipationEstablished: group.ParticipationEstablished,
		Attempt:                         attempt, Wave: wave, Membership: CloneMembershipBinding(archived.Membership),
		Assignment: cloneRetainedGoldenAssignment(archived.Assignment), Window: window,
		OpenedAt: resumedAt, Deadline: deadline,
	}
	execution.Receipts = []GoldenWaveCommandReceipt{{
		CommandID: command.CommandID, Scope: command.Scope, Kind: GoldenWaveCommandOpened,
		CommandDigest: digest, Result: execution.Expectation(), OccurredAt: resumedAt,
	}}
	if err := SealExecution(&execution); err != nil || execution.Validate() != nil {
		return GoldenWaveExecution{}, goldenPrestartError("seal retained execution")
	}
	return execution.Snapshot(), nil
}

func commitRetainedGoldenPrestart(
	ctx context.Context,
	repository PrestartRepository,
	commit RetainedGoldenPrestartCommit,
	expected RetainedGoldenPrestartRecord,
) (*RetainedGoldenPrestartRecord, bool, bool, error) {
	committed, changed, err := repository.CommitRetainedGoldenPrestart(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("retained Golden pre-start - commit: %w", err)
	}
	if committed == nil || committed.Validate() != nil || committed.CommandID != expected.CommandID ||
		committed.CommandDigest != expected.CommandDigest || committed.Scope != expected.Scope ||
		committed.SessionID != expected.SessionID || committed.State != expected.State ||
		(changed && committed.PayloadDigest != expected.PayloadDigest) {
		return nil, false, false, domain.ErrInternal
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}

func reconcileRetainedGoldenPrestart(
	record RetainedGoldenPrestartRecord,
	scope GoldenStateScope,
	commandID uuid.UUID,
	digest [sha256.Size]byte,
) (*RetainedGoldenPrestartRecord, bool, error) {
	if record.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	if record.CommandID != commandID {
		return nil, false, domain.ErrInternal
	}
	if record.Scope != scope || record.CommandDigest != digest {
		return nil, false, ErrGoldenPrestartCommandReuse
	}
	clone := record.Snapshot()
	return &clone, false, nil
}

func validRetainedGoldenPauseCommand(command RetainedGoldenPrestartPauseCommand) bool {
	ids := []uuid.UUID{command.CommandID, command.SessionID, command.NextSessionRevisionID}
	return ValidStateScope(command.Scope) && command.CommandID != uuid.Nil && command.SessionID != uuid.Nil &&
		command.ActorID != uuid.Nil && command.Reason == GoldenPrestartPauseOperatorManual &&
		command.ExpectedExecution.Scope == command.Scope &&
		command.ExpectedAuthorization.TournamentID == command.Scope.TournamentID &&
		command.ExpectedAuthorization.ActorID == command.ActorID && command.NextSessionRevisionID != uuid.Nil &&
		ValidIdentitySet(ids) &&
		!goldenPrestartIDsAlias(ids, command.ActorID, command.ExpectedAuthorization.RevisionID)
}

func validRetainedGoldenResumeCommand(command RetainedGoldenPrestartResumeCommand) bool {
	ids := []uuid.UUID{
		command.CommandID, command.NextSessionRevisionID, command.NextExecutionRevisionID,
		command.NextWaveRevisionID.UUID(), command.NextWindowID, command.NextWaveWindowRevisionID.UUID(),
		command.NextWindowRevisionID, command.NextReadinessRevisionID, command.NextPresenceRevisionID,
	}
	return ValidStateScope(command.Scope) && command.CommandID != uuid.Nil && command.SessionID != uuid.Nil &&
		command.ActorID != uuid.Nil && command.ExpectedSession.Scope == command.Scope &&
		command.ExpectedSession.SessionID == command.SessionID &&
		command.ExpectedAuthorization.TournamentID == command.Scope.TournamentID &&
		command.ExpectedAuthorization.ActorID == command.ActorID && ValidIdentitySet(ids) &&
		!goldenPrestartIDsAlias(ids,
			command.ActorID, command.ExpectedAuthorization.RevisionID,
			command.Scope.TournamentID, command.Scope.GroupID, command.Scope.GroupRevisionID.UUID())
}

func goldenPrestartIDsAlias(ids []uuid.UUID, reserved ...uuid.UUID) bool {
	for _, id := range ids {
		for _, reservedID := range reserved {
			if id == reservedID {
				return true
			}
		}
	}
	return false
}

func goldenPrestartAliasesExecution(execution GoldenWaveExecution, ids []uuid.UUID) bool {
	reserved := RetainedIdentitySet(execution)
	for _, id := range ids {
		if _, found := reserved[id]; found {
			return true
		}
	}
	return false
}

func cloneRetainedGoldenAssignment(assignment GoldenAttemptAssignment) GoldenAttemptAssignment {
	return CloneAttemptAssignment(assignment)
}

func goldenPrestartAliasesRecord(record RetainedGoldenPrestartRecord, ids []uuid.UUID) bool {
	reserved := IdentitySetFromExpectation(record.SourceExecution)
	for _, id := range []uuid.UUID{
		record.SessionID, record.CommandID, record.RevisionID, record.ActorID,
		record.Scope.TournamentID, record.Scope.GroupID, record.Scope.GroupRevisionID.UUID(), record.WaveID,
		record.Assignment.ID, record.Assignment.RevisionID, record.Assignment.EdgeID,
		record.Assignment.ReservationID, record.Assignment.SnapshotID, record.Assignment.TaskID,
		record.Membership.ID, record.Membership.RevisionID, record.Membership.Source.RevisionID,
		record.SupersededWindow.ID, record.SupersededWindow.RevisionID,
		record.SupersededWindow.ReadinessRevisionID, record.SupersededWindow.PresenceRevisionID,
		record.Authorization.RevisionID,
	} {
		if id != uuid.Nil {
			reserved[id] = struct{}{}
		}
	}
	for _, assignment := range record.Assignment.Private {
		reserved[assignment.ID] = struct{}{}
		reserved[assignment.ParticipantID] = struct{}{}
	}
	for _, participantID := range record.Membership.ParticipantIDs {
		reserved[participantID] = struct{}{}
	}
	for _, id := range record.NewIdentityIDs {
		reserved[id] = struct{}{}
	}
	for _, id := range ids {
		if _, found := reserved[id]; found {
			return true
		}
	}
	return false
}
