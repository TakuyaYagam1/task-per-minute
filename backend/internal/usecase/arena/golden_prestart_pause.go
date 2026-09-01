package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const retainedGoldenPrestartCommitAttempts = 3

var (
	ErrInvalidGoldenPrestartPause         = errors.New("invalid retained Golden pre-start pause")
	ErrGoldenPrestartAuthorityConflict    = errors.New("retained Golden pre-start authority conflict")
	ErrGoldenPrestartConflict             = errors.New("retained Golden pre-start commit conflict")
	ErrGoldenPrestartCommandReuse         = errors.New("retained Golden pre-start command identifier was reused")
	ErrGoldenPrestartAlreadyStarted       = errors.New("golden attempt already started")
	ErrGoldenPrestartSessionStateConflict = errors.New("retained Golden pre-start session state conflict")
)

type GoldenPrestartPauseReason string

const GoldenPrestartPauseOperatorManual GoldenPrestartPauseReason = "operator_manual"

type RetainedGoldenPrestartState string

const (
	RetainedGoldenPrestartPaused RetainedGoldenPrestartState = "paused"
	RetainedGoldenPrestartReady  RetainedGoldenPrestartState = "ready"
)

type GoldenPrestartOperatorAuthorizationExpectation struct {
	TournamentID  uuid.UUID
	ActorID       uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	PayloadDigest [sha256.Size]byte
}

func (e GoldenPrestartOperatorAuthorizationExpectation) Equal(
	other GoldenPrestartOperatorAuthorizationExpectation,
) bool {
	return e == other
}

type GoldenPrestartOperatorAuthorization struct {
	TournamentID  uuid.UUID
	ActorID       uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	PayloadDigest [sha256.Size]byte
}

func NewGoldenPrestartOperatorAuthorization(
	tournamentID uuid.UUID,
	actorID uuid.UUID,
	revisionID uuid.UUID,
	revision int64,
) (GoldenPrestartOperatorAuthorization, error) {
	authorization := GoldenPrestartOperatorAuthorization{
		TournamentID: tournamentID, ActorID: actorID, RevisionID: revisionID, Revision: revision,
	}
	payload, err := goldenPrestartAuthorizationPayload(authorization)
	if err != nil {
		return GoldenPrestartOperatorAuthorization{}, goldenPrestartError("encode operator authorization")
	}
	authorization.PayloadDigest = sha256.Sum256(payload)
	if authorization.Validate() != nil {
		return GoldenPrestartOperatorAuthorization{}, goldenPrestartError("invalid operator authorization")
	}
	return authorization, nil
}

func (a GoldenPrestartOperatorAuthorization) Expectation() GoldenPrestartOperatorAuthorizationExpectation {
	return GoldenPrestartOperatorAuthorizationExpectation(a)
}

func (a GoldenPrestartOperatorAuthorization) Validate() error {
	if a.TournamentID == uuid.Nil || a.ActorID == uuid.Nil || a.RevisionID == uuid.Nil || a.Revision < 1 ||
		!uniqueNonZeroUUIDs([]uuid.UUID{a.TournamentID, a.ActorID, a.RevisionID}) {
		return goldenPrestartError("invalid operator authorization head")
	}
	payload, err := goldenPrestartAuthorizationPayload(a)
	if err != nil || a.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != a.PayloadDigest {
		return goldenPrestartError("operator authorization digest changed")
	}
	return nil
}

type RetainedGoldenPrestartExpectation struct {
	Scope         GoldenStateScope
	SessionID     uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	State         RetainedGoldenPrestartState
	PayloadDigest [sha256.Size]byte
}

func (e RetainedGoldenPrestartExpectation) Equal(other RetainedGoldenPrestartExpectation) bool {
	return e == other
}

type RetainedGoldenPrestartRecord struct {
	SessionID                 uuid.UUID
	CommandID                 uuid.UUID
	CommandDigest             [sha256.Size]byte
	ActorID                   uuid.UUID
	Authorization             GoldenPrestartOperatorAuthorizationExpectation
	Scope                     GoldenStateScope
	RevisionID                uuid.UUID
	Revision                  int64
	PreviousRevisionID        *uuid.UUID
	State                     RetainedGoldenPrestartState
	Reason                    GoldenPrestartPauseReason
	OccurredAt                time.Time
	SourceExecution           GoldenWaveExecutionExpectation
	Group                     domain.ArenaGoldenGroupState
	Attempt                   domain.ArenaGoldenAttempt
	WaveID                    uuid.UUID
	Assignment                GoldenAttemptAssignmentEvidence
	Membership                GoldenWaveMembershipBinding
	SupersededWindow          GoldenReadyWindow
	FreshWindow               *GoldenReadyWindow
	FreshExecutionRevisionID  uuid.UUID
	FreshWaveRevisionID       domain.ArenaWaveRevisionID
	FreshWaveWindowRevisionID domain.ArenaReadyWindowRevisionID
	FreshExecution            *GoldenWaveExecutionExpectation
	NewIdentityIDs            []uuid.UUID
	PayloadDigest             [sha256.Size]byte
}

func (r RetainedGoldenPrestartRecord) Snapshot() RetainedGoldenPrestartRecord {
	clone := r
	clone.PreviousRevisionID = cloneGoldenUUID(r.PreviousRevisionID)
	clone.SourceExecution = cloneGoldenExecutionExpectationValue(r.SourceExecution)
	clone.Group = cloneGoldenStateGroup(r.Group)
	clone.Attempt = cloneGoldenAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Membership = cloneGoldenMembershipBinding(r.Membership)
	clone.SupersededWindow = cloneGoldenWindow(r.SupersededWindow)
	if r.FreshWindow != nil {
		fresh := cloneGoldenWindow(*r.FreshWindow)
		clone.FreshWindow = &fresh
	}
	if r.FreshExecution != nil {
		fresh := cloneGoldenExecutionExpectationValue(*r.FreshExecution)
		clone.FreshExecution = &fresh
	}
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	return clone
}

func (r RetainedGoldenPrestartRecord) Expectation() RetainedGoldenPrestartExpectation {
	return RetainedGoldenPrestartExpectation{
		Scope: r.Scope, SessionID: r.SessionID, RevisionID: r.RevisionID,
		Revision: r.Revision, State: r.State, PayloadDigest: r.PayloadDigest,
	}
}

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
	return validGoldenStateScope(r.Scope) && r.SessionID != uuid.Nil && r.CommandID != uuid.Nil &&
		r.CommandDigest != [sha256.Size]byte{} && r.ActorID != uuid.Nil && r.RevisionID != uuid.Nil &&
		r.Revision >= 1 && validGoldenRevisionPredecessor(r.RevisionID, r.Revision, r.PreviousRevisionID) &&
		validArenaServerTime(r.OccurredAt) && r.Reason == GoldenPrestartPauseOperatorManual
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
		equalGoldenIDs(goldenPrivateAssignmentIDs(r.Assignment.Private), r.Membership.ParticipantIDs)
}

func validRetainedGoldenPrestartMembership(r RetainedGoldenPrestartRecord) bool {
	return r.Membership.ID == r.SourceExecution.MembershipID &&
		r.Membership.RevisionID == r.SourceExecution.MembershipRevisionID &&
		r.Membership.Revision == r.SourceExecution.MembershipRevision &&
		r.Membership.PayloadDigest == r.SourceExecution.MembershipDigest &&
		goldenMembershipRevisionsEqual(r.Membership.Source, r.SourceExecution.Source.Membership) &&
		goldenIDsAreCanonical(r.Membership.ParticipantIDs) &&
		r.Membership.PayloadDigest == goldenParticipantSetDigest(r.Membership.ParticipantIDs) &&
		equalGoldenIDs(r.Membership.ParticipantIDs, r.Attempt.ParticipantIDs)
}

func validRetainedGoldenPrestartWindow(r RetainedGoldenPrestartRecord) bool {
	return r.SupersededWindow.ID == r.SourceExecution.Window.WindowID &&
		r.SupersededWindow.AttemptID == r.Attempt.ID && r.SupersededWindow.AttemptNo == r.Attempt.AttemptNo &&
		r.SupersededWindow.Expectation() == r.SourceExecution.Window &&
		validateGoldenWindowIdentity(r.SupersededWindow) == nil &&
		validateGoldenWindowParticipantSets(r.SupersededWindow) == nil &&
		equalGoldenIDs(r.SupersededWindow.BasePresentParticipantIDs, r.Membership.ParticipantIDs)
}

func validRetainedGoldenPrestartNewIdentities(r RetainedGoldenPrestartRecord) bool {
	return goldenIDsAreCanonical(r.NewIdentityIDs) && uniqueNonZeroUUIDs(r.NewIdentityIDs) &&
		equalGoldenIDs(r.NewIdentityIDs, retainedGoldenRecordIdentityIDs(r))
}

func validRetainedGoldenPrestartGroup(r RetainedGoldenPrestartRecord) bool {
	_, err := domain.NewArenaGoldenGroup(r.Group)
	return err == nil && len(r.Group.Attempts) > 0 && r.Group.ID == r.Scope.GroupID &&
		r.Group.RevisionID == r.Scope.GroupRevisionID && r.Group.TournamentID == r.Scope.TournamentID &&
		r.Group.SourceProjectionRevisionID == r.SourceExecution.Source.SourceProjectionRevisionID &&
		equalGoldenIDs(retainedGoldenActiveMemberIDs(r.Group.Members), r.Membership.ParticipantIDs) &&
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
		equalGoldenIDs(r.FreshWindow.PresentParticipantIDs, r.Membership.ParticipantIDs)
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
	assignment, err := buildGoldenAttemptAssignmentEvidence(archived.Assignment)
	if err != nil || !reflect.DeepEqual(assignment, record.Assignment) {
		return false
	}
	attempt := cloneGoldenAttempt(record.Attempt)
	attempt.RetainedAt = cloneGoldenTime(archived.Attempt.RetainedAt)
	if !reflect.DeepEqual(attempt, archived.Attempt) {
		return false
	}
	group := cloneGoldenStateGroup(record.Group)
	group.Attempts[len(group.Attempts)-1] = attempt
	return reflect.DeepEqual(group, archived.Group)
}

func retainedGoldenActiveMemberIDs(members []domain.ArenaGoldenMember) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		if !member.Excluded {
			ids = append(ids, member.ParticipantID)
		}
	}
	canonicalGoldenIDs(ids)
	return ids
}

type RetainedGoldenPrestartCommit struct {
	ExpectedExecution         *GoldenWaveExecutionExpectation
	ExpectedArchivedExecution *GoldenWaveExecutionExpectation
	ExpectedSession           *RetainedGoldenPrestartExpectation
	ExpectedAuthorization     GoldenPrestartOperatorAuthorizationExpectation
	ArchivedExecution         *GoldenWaveExecution
	PublishedExecution        *GoldenWaveExecution
	NewIdentityIDs            []uuid.UUID
	Record                    RetainedGoldenPrestartRecord
}

// RetainedGoldenPrestartRepository atomically compares every expected live,
// archived, session and authorization head; globally reserves NewIdentityIDs;
// hides the old execution on pause; and publishes one fresh execution on resume.
type RetainedGoldenPrestartRepository interface {
	FindRetainedGoldenPrestartCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*RetainedGoldenPrestartRecord, error)
	LoadRetainedGoldenPrestartAuthority(ctx context.Context, scope GoldenStateScope) (RetainedGoldenPrestartAuthority, error)
	CommitRetainedGoldenPrestart(ctx context.Context, commit RetainedGoldenPrestartCommit) (*RetainedGoldenPrestartRecord, bool, error)
}

type RetainedGoldenPrestartPauseCommand struct {
	Scope                 GoldenStateScope
	CommandID             uuid.UUID
	SessionID             uuid.UUID
	ActorID               uuid.UUID
	Reason                GoldenPrestartPauseReason
	ExpectedExecution     GoldenWaveExecutionExpectation
	ExpectedAuthorization GoldenPrestartOperatorAuthorizationExpectation
	NextSessionRevisionID uuid.UUID
}

type RetainedGoldenPrestartResumeCommand struct {
	Scope                    GoldenStateScope
	CommandID                uuid.UUID
	SessionID                uuid.UUID
	ActorID                  uuid.UUID
	ExpectedSession          RetainedGoldenPrestartExpectation
	ExpectedAuthorization    GoldenPrestartOperatorAuthorizationExpectation
	NextSessionRevisionID    uuid.UUID
	NextExecutionRevisionID  uuid.UUID
	NextWaveRevisionID       domain.ArenaWaveRevisionID
	NextWindowID             uuid.UUID
	NextWaveWindowRevisionID domain.ArenaReadyWindowRevisionID
	NextWindowRevisionID     uuid.UUID
	NextReadinessRevisionID  uuid.UUID
	NextPresenceRevisionID   uuid.UUID
}

type RetainedGoldenPrestartPauseUseCase struct {
	repository RetainedGoldenPrestartRepository
	clock      Clock
}

func NewRetainedGoldenPrestartPauseUseCase(
	repository RetainedGoldenPrestartRepository,
	clock Clock,
) *RetainedGoldenPrestartPauseUseCase {
	return &RetainedGoldenPrestartPauseUseCase{repository: repository, clock: clock}
}

func (u *RetainedGoldenPrestartPauseUseCase) Pause(
	ctx context.Context,
	command RetainedGoldenPrestartPauseCommand,
) (*RetainedGoldenPrestartRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validRetainedGoldenPauseCommand(command) {
		return nil, false, domain.ErrValidation
	}
	digest := retainedGoldenPauseCommandDigest(command)
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find pause replay: %w", err)
	} else if replay != nil {
		return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
	}
	pausedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(pausedAt) {
		return nil, false, domain.ErrValidation
	}
	for range retainedGoldenPrestartCommitAttempts {
		record, changed, retry, err := u.pauseAttempt(ctx, command, digest, pausedAt)
		if !retry {
			return record, changed, err
		}
		replay, replayErr := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID)
		if replayErr != nil {
			return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find retry pause replay: %w", replayErr)
		}
		if replay != nil {
			return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		}
	}
	return nil, false, ErrGoldenPrestartConflict
}

func (u *RetainedGoldenPrestartPauseUseCase) pauseAttempt(
	ctx context.Context,
	command RetainedGoldenPrestartPauseCommand,
	digest [sha256.Size]byte,
	pausedAt time.Time,
) (*RetainedGoldenPrestartRecord, bool, bool, error) {
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(
		ctx, command.Scope.TournamentID, command.CommandID,
	); err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find pause attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadRetainedGoldenPrestartAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - load pause authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Authorization.Expectation().Equal(command.ExpectedAuthorization) ||
		authority.Authorization.ActorID != command.ActorID {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if authority.Execution == nil || authority.Current != nil {
		return nil, false, false, ErrGoldenPrestartSessionStateConflict
	}
	execution := authority.Execution.Snapshot()
	if !execution.Expectation().Equal(command.ExpectedExecution) {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if execution.Start != nil || execution.Attempt.StartedAt != nil || execution.Wave.StartedAt != nil {
		return nil, false, false, ErrGoldenPrestartAlreadyStarted
	}
	if pausedAt.Before(execution.OpenedAt) {
		return nil, false, false, goldenPrestartError("pause time precedes ready window")
	}
	record, err := buildRetainedGoldenPauseRecord(execution, command, digest, pausedAt)
	if err != nil {
		return nil, false, false, err
	}
	expected := command.ExpectedExecution
	commit := RetainedGoldenPrestartCommit{
		ExpectedExecution: &expected, ExpectedAuthorization: command.ExpectedAuthorization,
		ArchivedExecution: &execution, NewIdentityIDs: append([]uuid.UUID(nil), record.NewIdentityIDs...),
		Record: record.Snapshot(),
	}
	return commitRetainedGoldenPrestart(ctx, u.repository, commit, record)
}

func (u *RetainedGoldenPrestartPauseUseCase) Resume(
	ctx context.Context,
	command RetainedGoldenPrestartResumeCommand,
) (*RetainedGoldenPrestartRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validRetainedGoldenResumeCommand(command) {
		return nil, false, domain.ErrValidation
	}
	digest := retainedGoldenResumeCommandDigest(command)
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find resume replay: %w", err)
	} else if replay != nil {
		return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
	}
	resumedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(resumedAt) {
		return nil, false, domain.ErrValidation
	}
	for range retainedGoldenPrestartCommitAttempts {
		record, changed, retry, err := u.resumeAttempt(ctx, command, digest, resumedAt)
		if !retry {
			return record, changed, err
		}
		replay, replayErr := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID)
		if replayErr != nil {
			return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find retry resume replay: %w", replayErr)
		}
		if replay != nil {
			return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		}
	}
	return nil, false, ErrGoldenPrestartConflict
}

func (u *RetainedGoldenPrestartPauseUseCase) resumeAttempt(
	ctx context.Context,
	command RetainedGoldenPrestartResumeCommand,
	digest [sha256.Size]byte,
	resumedAt time.Time,
) (*RetainedGoldenPrestartRecord, bool, bool, error) {
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(
		ctx, command.Scope.TournamentID, command.CommandID,
	); err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find resume attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadRetainedGoldenPrestartAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - load resume authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Authorization.Expectation().Equal(command.ExpectedAuthorization) ||
		authority.Authorization.ActorID != command.ActorID {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if authority.Execution != nil || authority.ArchivedExecution == nil || authority.Current == nil ||
		!authority.Current.Expectation().Equal(command.ExpectedSession) || authority.Current.SessionID != command.SessionID {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if authority.Current.State != RetainedGoldenPrestartPaused {
		return nil, false, false, ErrGoldenPrestartSessionStateConflict
	}
	if resumedAt.Before(authority.Current.OccurredAt) {
		return nil, false, false, goldenPrestartError("resume time precedes retained pause")
	}
	record, execution, err := buildRetainedGoldenResumeRecord(
		*authority.Current, authority.ArchivedExecution.Snapshot(), command, digest, resumedAt,
	)
	if err != nil {
		return nil, false, false, err
	}
	expectedSession := command.ExpectedSession
	expectedArchivedExecution := authority.ArchivedExecution.Expectation()
	commit := RetainedGoldenPrestartCommit{
		ExpectedArchivedExecution: &expectedArchivedExecution,
		ExpectedSession:           &expectedSession, ExpectedAuthorization: command.ExpectedAuthorization,
		PublishedExecution: &execution, NewIdentityIDs: append([]uuid.UUID(nil), record.NewIdentityIDs...),
		Record: record.Snapshot(),
	}
	return commitRetainedGoldenPrestart(ctx, u.repository, commit, record)
}

func buildRetainedGoldenPauseRecord(
	execution GoldenWaveExecution,
	command RetainedGoldenPrestartPauseCommand,
	digest [sha256.Size]byte,
	pausedAt time.Time,
) (RetainedGoldenPrestartRecord, error) {
	group, err := domain.NewArenaGoldenGroup(execution.Group)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("load retained attempt")
	}
	changed, err := group.RetainAttemptBeforeStart(execution.Attempt.ID, pausedAt)
	if err != nil || !changed {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("retain unstarted attempt")
	}
	retainedGroup := group.Snapshot()
	attempt := retainedGroup.Attempts[len(retainedGroup.Attempts)-1]
	assignment, err := buildGoldenAttemptAssignmentEvidence(execution.Assignment)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("sanitize retained assignment")
	}
	identities := []uuid.UUID{command.CommandID, command.SessionID, command.NextSessionRevisionID}
	canonicalGoldenIDs(identities)
	if !uniqueNonZeroUUIDs(identities) ||
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
		Membership:       cloneGoldenMembershipBinding(execution.Membership),
		SupersededWindow: cloneGoldenWindow(execution.Window), NewIdentityIDs: identities,
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
	canonicalGoldenIDs(identities)
	if !uniqueNonZeroUUIDs(identities) ||
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
	record.PreviousRevisionID = goldenUUID(record.RevisionID)
	record.RevisionID = command.NextSessionRevisionID
	record.Revision++
	record.State = RetainedGoldenPrestartReady
	record.OccurredAt = resumedAt
	record.FreshExecutionRevisionID = execution.RevisionID
	record.FreshWaveRevisionID = execution.Wave.RevisionID
	record.FreshWaveWindowRevisionID = execution.Wave.ReadyWindow.RevisionID
	freshWindow := cloneGoldenWindow(execution.Window)
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
	deadline := resumedAt.Add(goldenReadyWindowDuration)
	attempt := cloneGoldenAttempt(paused.Attempt)
	group := cloneGoldenStateGroup(paused.Group)
	groupBindingDigest, err := goldenExecutionGroupBindingDigest(group, group.ParticipationEstablished)
	if err != nil {
		return GoldenWaveExecution{}, goldenPrestartError("encode retained group binding")
	}
	wave := domain.ArenaWave{
		ID: paused.WaveID, TournamentID: paused.Scope.TournamentID,
		RevisionID: command.NextWaveRevisionID, State: domain.ArenaWaveStatePlanned,
		Members: goldenWaveMembers(paused.Membership.ParticipantIDs),
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
		ReadinessDigest:           goldenParticipantSetDigest(nil),
		PresenceDigest:            goldenParticipantSetDigest(paused.Membership.ParticipantIDs),
	}
	execution := GoldenWaveExecution{
		Scope: paused.Scope, Source: cloneGoldenStateExpectation(paused.SourceExecution.Source),
		RevisionID: command.NextExecutionRevisionID, Revision: 1,
		Group: group, GroupBindingDigest: groupBindingDigest,
		OpeningParticipationEstablished: group.ParticipationEstablished,
		Attempt:                         attempt, Wave: wave, Membership: cloneGoldenMembershipBinding(archived.Membership),
		Assignment: cloneRetainedGoldenAssignment(archived.Assignment), Window: window,
		OpenedAt: resumedAt, Deadline: deadline,
	}
	execution.Receipts = []GoldenWaveCommandReceipt{{
		CommandID: command.CommandID, Scope: command.Scope, Kind: GoldenWaveCommandOpened,
		CommandDigest: digest, Result: execution.Expectation(), OccurredAt: resumedAt,
	}}
	if err := sealGoldenWaveExecution(&execution); err != nil || execution.Validate() != nil {
		return GoldenWaveExecution{}, goldenPrestartError("seal retained execution")
	}
	return execution.Snapshot(), nil
}

func commitRetainedGoldenPrestart(
	ctx context.Context,
	repository RetainedGoldenPrestartRepository,
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
	return validGoldenStateScope(command.Scope) && command.CommandID != uuid.Nil && command.SessionID != uuid.Nil &&
		command.ActorID != uuid.Nil && command.Reason == GoldenPrestartPauseOperatorManual &&
		command.ExpectedExecution.Scope == command.Scope &&
		command.ExpectedAuthorization.TournamentID == command.Scope.TournamentID &&
		command.ExpectedAuthorization.ActorID == command.ActorID && command.NextSessionRevisionID != uuid.Nil &&
		uniqueNonZeroUUIDs(ids) &&
		!goldenPrestartIDsAlias(ids, command.ActorID, command.ExpectedAuthorization.RevisionID)
}

func validRetainedGoldenResumeCommand(command RetainedGoldenPrestartResumeCommand) bool {
	ids := []uuid.UUID{
		command.CommandID, command.NextSessionRevisionID, command.NextExecutionRevisionID,
		command.NextWaveRevisionID.UUID(), command.NextWindowID, command.NextWaveWindowRevisionID.UUID(),
		command.NextWindowRevisionID, command.NextReadinessRevisionID, command.NextPresenceRevisionID,
	}
	return validGoldenStateScope(command.Scope) && command.CommandID != uuid.Nil && command.SessionID != uuid.Nil &&
		command.ActorID != uuid.Nil && command.ExpectedSession.Scope == command.Scope &&
		command.ExpectedSession.SessionID == command.SessionID &&
		command.ExpectedAuthorization.TournamentID == command.Scope.TournamentID &&
		command.ExpectedAuthorization.ActorID == command.ActorID && uniqueNonZeroUUIDs(ids) &&
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
	reserved := goldenSubmissionRetainedIdentitySet(execution)
	for _, id := range ids {
		if _, found := reserved[id]; found {
			return true
		}
	}
	return false
}

func cloneRetainedGoldenAssignment(assignment GoldenAttemptAssignment) GoldenAttemptAssignment {
	clone := assignment
	clone.Snapshot = cloneTaskSnapshot(assignment.Snapshot)
	clone.Private = append([]GoldenPrivateAssignment(nil), assignment.Private...)
	return clone
}

func goldenPrestartAliasesRecord(record RetainedGoldenPrestartRecord, ids []uuid.UUID) bool {
	reserved := goldenExecutionIdentitySetFromExpectation(record.SourceExecution)
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

func retainedGoldenRecordIdentityIDs(record RetainedGoldenPrestartRecord) []uuid.UUID {
	identities := []uuid.UUID{record.CommandID, record.RevisionID}
	if record.State == RetainedGoldenPrestartPaused {
		identities = append(identities, record.SessionID)
	} else if record.FreshWindow != nil {
		identities = append(identities,
			record.FreshExecutionRevisionID, record.FreshWaveRevisionID.UUID(), record.FreshWindow.ID,
			record.FreshWaveWindowRevisionID.UUID(), record.FreshWindow.RevisionID,
			record.FreshWindow.ReadinessRevisionID, record.FreshWindow.PresenceRevisionID,
		)
	}
	canonicalGoldenIDs(identities)
	return identities
}

func sealRetainedGoldenPrestartRecord(record RetainedGoldenPrestartRecord) (RetainedGoldenPrestartRecord, error) {
	record.PayloadDigest = [sha256.Size]byte{}
	payload, err := goldenPrestartRecordPayload(record)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("encode retained pre-start receipt")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return RetainedGoldenPrestartRecord{}, err
	}
	return record.Snapshot(), nil
}

func goldenPrestartAuthorizationPayload(authorization GoldenPrestartOperatorAuthorization) ([]byte, error) {
	authorization.PayloadDigest = [sha256.Size]byte{}
	return goldenEncode(authorization)
}

func goldenPrestartRecordPayload(record RetainedGoldenPrestartRecord) ([]byte, error) {
	clone := record.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return goldenEncode(clone)
}

func retainedGoldenPauseCommandDigest(command RetainedGoldenPrestartPauseCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func retainedGoldenResumeCommandDigest(command RetainedGoldenPrestartResumeCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func goldenPrestartError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenPrestartPause, message)
}
