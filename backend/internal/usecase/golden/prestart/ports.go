package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

type GoldenStateScope = goldenstate.GoldenStateScope
type GoldenStateExpectation = goldenstate.GoldenStateExpectation
type GoldenReadyWindow = goldenstate.GoldenReadyWindow
type GoldenWaveExecutionExpectation = goldenexecution.GoldenWaveExecutionExpectation
type GoldenWaveExecution = goldenexecution.GoldenWaveExecution
type GoldenAttemptAssignment = goldenexecution.GoldenAttemptAssignment
type GoldenAttemptAssignmentEvidence = goldenexecution.GoldenAttemptAssignmentEvidence
type GoldenWaveMembershipBinding = goldenexecution.GoldenWaveMembershipBinding
type GoldenWaveCommandReceipt = goldenexecution.GoldenWaveCommandReceipt

const (
	GoldenReadyWindowOpen   = goldenstate.GoldenReadyWindowOpen
	GoldenWaveCommandOpened = goldenexecution.GoldenWaveCommandOpened
)

type PrestartClock interface {
	Now() time.Time
}

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
		!ValidIdentitySet([]uuid.UUID{a.TournamentID, a.ActorID, a.RevisionID}) {
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
	Group                     domain.GoldenGroupState
	Attempt                   domain.GoldenAttempt
	WaveID                    uuid.UUID
	Assignment                GoldenAttemptAssignmentEvidence
	Membership                GoldenWaveMembershipBinding
	SupersededWindow          GoldenReadyWindow
	FreshWindow               *GoldenReadyWindow
	FreshExecutionRevisionID  uuid.UUID
	FreshWaveRevisionID       domain.WaveRevisionID
	FreshWaveWindowRevisionID domain.ReadyWindowRevisionID
	FreshExecution            *GoldenWaveExecutionExpectation
	NewIdentityIDs            []uuid.UUID
	PayloadDigest             [sha256.Size]byte
}

func (r RetainedGoldenPrestartRecord) Snapshot() RetainedGoldenPrestartRecord {
	clone := r
	clone.PreviousRevisionID = prestartCloneUUIDPointer(r.PreviousRevisionID)
	clone.SourceExecution = CloneExecutionExpectation(r.SourceExecution)
	clone.Group = CloneGroup(r.Group)
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Membership = CloneMembershipBinding(r.Membership)
	clone.SupersededWindow = CloneReadyWindow(r.SupersededWindow)
	if r.FreshWindow != nil {
		fresh := CloneReadyWindow(*r.FreshWindow)
		clone.FreshWindow = &fresh
	}
	if r.FreshExecution != nil {
		fresh := CloneExecutionExpectation(*r.FreshExecution)
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

type PrestartRepository interface {
	FindRetainedGoldenPrestartCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*RetainedGoldenPrestartRecord, error)
	LoadRetainedGoldenPrestartAuthority(ctx context.Context, scope GoldenStateScope) (RetainedGoldenPrestartAuthority, error)
	CommitRetainedGoldenPrestart(ctx context.Context, commit RetainedGoldenPrestartCommit) (*RetainedGoldenPrestartRecord, bool, error)
}
