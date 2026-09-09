package correction

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

type Clock interface {
	Now() time.Time
}

type Reason string

const (
	ReasonScorekeepingError  Reason = "scorekeeping_error"
	ReasonVerifiedSubmission Reason = "verified_submission"
	ReasonOperatorRuling     Reason = "operator_ruling"
)

type Field string

const (
	FieldWinner        Field = "winner"
	FieldResultReason  Field = "result_reason"
	FieldSolveMetadata Field = "solve_metadata"
)

type SolveMetadata struct {
	SolvedAt       *time.Time
	SubmissionID   *uuid.UUID
	EvidenceDigest [sha256.Size]byte
}

func (m SolveMetadata) Clone() SolveMetadata {
	clone := m
	clone.SolvedAt = cloneTimePointer(m.SolvedAt)
	clone.SubmissionID = cloneCorrectionUUIDPointer(m.SubmissionID)
	return clone
}

type Patch struct {
	State         domain.GameState
	Reason        domain.GameResultReason
	WinnerID      *uuid.UUID
	SolveMetadata SolveMetadata
}

type ReadinessState string

const (
	ReadinessOpen   ReadinessState = "open"
	ReadinessClosed ReadinessState = "closed"
)

type Readiness struct {
	TournamentID   uuid.UUID
	OwnerID        uuid.UUID
	WaveID         uuid.UUID
	WindowID       uuid.UUID
	RevisionID     uuid.UUID
	Revision       int64
	State          ReadinessState
	ParticipantIDs []uuid.UUID
}

type Reservation struct {
	ID               uuid.UUID
	TournamentID     uuid.UUID
	OwnerID          uuid.UUID
	SourceRevisionID domain.DerivedRevisionID
	Revision         int64
	Used             bool
	Disclosed        bool
	EvidenceDigest   [sha256.Size]byte
}

type UnlockIntent struct {
	ReservationID     uuid.UUID
	TournamentID      uuid.UUID
	OwnerID           uuid.UUID
	SourceRevisionID  domain.DerivedRevisionID
	ExpectedRevision  int64
	ExpectedUsed      bool
	ExpectedDisclosed bool
	EvidenceDigest    [sha256.Size]byte
	BindingDigest     [sha256.Size]byte
}

func NewUnlockIntent(reservation Reservation) UnlockIntent {
	intent := UnlockIntent{
		ReservationID: reservation.ID, TournamentID: reservation.TournamentID,
		OwnerID: reservation.OwnerID, SourceRevisionID: reservation.SourceRevisionID,
		ExpectedRevision: reservation.Revision, ExpectedUsed: reservation.Used,
		ExpectedDisclosed: reservation.Disclosed, EvidenceDigest: reservation.EvidenceDigest,
	}
	intent.BindingDigest = correctionUnlockIntentDigest(intent)
	return intent
}

type ProjectionIntent struct {
	ExpectedRevision domain.DerivedRevision
	NextRevisionID   domain.DerivedRevisionID
	DecisionID       uuid.UUID
	Payload          []byte
	PayloadDigest    [sha256.Size]byte
}

func NewProjectionIntent(
	expected domain.DerivedRevision,
	nextRevisionID domain.DerivedRevisionID,
	decisionID uuid.UUID,
	payload []byte,
) ProjectionIntent {
	return ProjectionIntent{
		ExpectedRevision: expected, NextRevisionID: nextRevisionID, DecisionID: decisionID,
		Payload: append([]byte(nil), payload...), PayloadDigest: sha256.Sum256(payload),
	}
}

func (i ProjectionIntent) Clone() ProjectionIntent {
	clone := i
	clone.Payload = append([]byte(nil), i.Payload...)
	return clone
}

type Expectation struct {
	TournamentState        domain.TournamentState
	TournamentRevision     int64
	CutoffEventDigest      [sha256.Size]byte
	TargetProjection       domain.DerivedRevision
	ScoreProjection        domain.DerivedRevision
	SeriesProjection       domain.DerivedRevision
	ResultRevisionID       domain.OfficialResultRevisionID
	ScoreRevisionID        domain.SeriesScoreRevisionID
	SeriesResultRevisionID domain.OfficialResultRevisionID
	SeriesRevision         resultusecase.SeriesRowRevision
	AttemptRevision        resultusecase.AttemptRowRevision
	DAGDigest              [sha256.Size]byte
	ReservationDigest      [sha256.Size]byte
	DecisionDigest         [sha256.Size]byte
	ReadinessDigest        [sha256.Size]byte
	CurrentSolveDigest     [sha256.Size]byte
}

type Command struct {
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	GameID                     uuid.UUID
	CommandID                  uuid.UUID
	CascadeCommandID           uuid.UUID
	OperatorID                 uuid.UUID
	Confirmed                  bool
	Reason                     Reason
	Explanation                string
	RequestedAt                time.Time
	Expected                   Expectation
	Patch                      Patch
	Fields                     []Field
	NextResultRevisionID       domain.OfficialResultRevisionID
	NextScoreRevisionID        domain.SeriesScoreRevisionID
	NextSeriesResultRevisionID domain.OfficialResultRevisionID
	NextReadinessRevisionID    uuid.UUID
	ProjectionIntents          []ProjectionIntent
	UnlockIntents              []UnlockIntent
}

type Authority struct {
	TournamentState    domain.TournamentState
	TournamentRevision int64
	DAG                resultprojection.RevisionDAG
	Series             domain.Series
	GameResult         resultusecase.OfficialResultRevisionHead
	Score              resultusecase.SeriesScoreRevisionHead
	SeriesResult       resultusecase.OfficialResultRevisionHead
	SeriesRevision     resultusecase.SeriesRowRevision
	AttemptRevision    resultusecase.AttemptRowRevision
	CurrentSolve       SolveMetadata
	Readiness          Readiness
	Reservations       []Reservation
	Decisions          []resultprojection.RecordedProjectionDecision
	CutoffEvents       []CutoffEvent
}

type Validation struct {
	command       Command
	authority     Authority
	cutoff        Cutoff
	target        domain.DerivedRevision
	unlocks       []UnlockIntent
	snapshot      resultprojection.RevisionDAGSnapshot
	bindingDigest [sha256.Size]byte
}

func (v Validation) TargetRevision() domain.DerivedRevision {
	return v.target
}

func (v Validation) Descendants() []domain.DerivedRevision {
	return v.cutoff.Descendants()
}

func (v Validation) UnlockIntents() []UnlockIntent {
	return append([]UnlockIntent(nil), v.unlocks...)
}

var (
	ErrInvalid = errors.New("invalid correction")
	ErrCutoff  = errors.New("correction cutoff reached")
)

type RejectionCode string

const (
	RejectionMalformed       RejectionCode = "malformed"
	RejectionStale           RejectionCode = "stale"
	RejectionIncomplete      RejectionCode = "incomplete"
	RejectionIdentityAlias   RejectionCode = "identity_aliased"
	RejectionTerminal        RejectionCode = "terminal_incompatible"
	RejectionCrossTournament RejectionCode = "cross_tournament"
	RejectionCutoff          RejectionCode = "cutoff"
)

type Error struct {
	code   RejectionCode
	cause  error
	detail string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%v: %s: %s", e.cause, e.code, e.detail)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Code() RejectionCode {
	if e == nil {
		return ""
	}
	return e.code
}

func Code(err error) RejectionCode {
	var rejection *Error
	if errors.As(err, &rejection) {
		return rejection.Code()
	}
	return ""
}

type CutoffKind string

const (
	CutoffWaveStarted     CutoffKind = "wave_started"
	CutoffTaskDelivered   CutoffKind = "task_delivered"
	CutoffNoShowRecorded  CutoffKind = "no_show_recorded"
	CutoffForfeitRecorded CutoffKind = "forfeit_recorded"
	CutoffGoldenAllocated CutoffKind = "golden_direct_allocated"
)

type CutoffEvent struct {
	ID               uuid.UUID
	Kind             CutoffKind
	TournamentID     uuid.UUID
	SourceRevisionID domain.DerivedRevisionID
	OccurredAt       time.Time
}

type CutoffInput struct {
	DAG              resultprojection.RevisionDAG
	TournamentID     uuid.UUID
	TargetRevisionID domain.DerivedRevisionID
	TournamentState  domain.TournamentState
	Events           []CutoffEvent
}

type Cutoff struct {
	tournamentID     uuid.UUID
	targetRevisionID domain.DerivedRevisionID
	descendants      []domain.DerivedRevision
}

func (c Cutoff) TournamentID() uuid.UUID {
	return c.tournamentID
}

func (c Cutoff) TargetRevisionID() domain.DerivedRevisionID {
	return c.targetRevisionID
}

func (c Cutoff) Descendants() []domain.DerivedRevision {
	return append([]domain.DerivedRevision(nil), c.descendants...)
}

func rejectCorrection(code RejectionCode, cause error, detail string) error {
	return &Error{code: code, cause: cause, detail: detail}
}

type StageRepository interface {
	LoadCorrectionStage(ctx context.Context, tournamentID uuid.UUID) (StageSnapshot, error)
	CommitCorrectionStage(ctx context.Context, commit StageCommit) (bool, error)
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
