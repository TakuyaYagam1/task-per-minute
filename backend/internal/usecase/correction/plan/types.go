package plan

import (
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	cutoffusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/cutoff"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

type CutoffKind = cutoffusecase.CutoffKind
type CutoffEvent = cutoffusecase.CutoffEvent
type CutoffInput = cutoffusecase.CutoffInput
type Cutoff = cutoffusecase.Cutoff
type RejectionCode = cutoffusecase.RejectionCode
type Error = cutoffusecase.Error

const (
	CutoffWaveStarted     = cutoffusecase.CutoffWaveStarted
	CutoffTaskDelivered   = cutoffusecase.CutoffTaskDelivered
	CutoffNoShowRecorded  = cutoffusecase.CutoffNoShowRecorded
	CutoffForfeitRecorded = cutoffusecase.CutoffForfeitRecorded
	CutoffGoldenAllocated = cutoffusecase.CutoffGoldenAllocated

	RejectionMalformed       = cutoffusecase.RejectionMalformed
	RejectionStale           = cutoffusecase.RejectionStale
	RejectionIncomplete      = cutoffusecase.RejectionIncomplete
	RejectionIdentityAlias   = cutoffusecase.RejectionIdentityAlias
	RejectionTerminal        = cutoffusecase.RejectionTerminal
	RejectionCrossTournament = cutoffusecase.RejectionCrossTournament
	RejectionCutoff          = cutoffusecase.RejectionCutoff
)

var (
	ErrInvalid = cutoffusecase.ErrInvalid
	ErrCutoff  = cutoffusecase.ErrCutoff
)

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
	clone.SubmissionID = cloneUUIDPointer(m.SubmissionID)
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

func rejectCorrection(code RejectionCode, cause error, detail string) error {
	return cutoffusecase.Reject(code, cause, detail)
}
