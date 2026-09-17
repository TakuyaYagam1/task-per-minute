package correction

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	planusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/plan"
)

type Reason = planusecase.Reason
type Field = planusecase.Field
type SolveMetadata = planusecase.SolveMetadata
type Patch = planusecase.Patch
type ReadinessState = planusecase.ReadinessState
type Readiness = planusecase.Readiness
type Reservation = planusecase.Reservation
type UnlockIntent = planusecase.UnlockIntent
type ProjectionIntent = planusecase.ProjectionIntent
type Expectation = planusecase.Expectation
type Command = planusecase.Command
type Authority = planusecase.Authority
type Validation = planusecase.Validation

type ProjectionSupersession = planusecase.ProjectionSupersession
type ReadinessTransition = planusecase.ReadinessTransition
type ReservationRelease = planusecase.ReservationRelease
type CutoffCondition = planusecase.CutoffCondition
type AuditRecord = planusecase.AuditRecord
type SolveTransition = planusecase.SolveTransition
type Plan = planusecase.Plan

const (
	ReasonScorekeepingError  = planusecase.ReasonScorekeepingError
	ReasonVerifiedSubmission = planusecase.ReasonVerifiedSubmission
	ReasonOperatorRuling     = planusecase.ReasonOperatorRuling

	FieldWinner        = planusecase.FieldWinner
	FieldResultReason  = planusecase.FieldResultReason
	FieldSolveMetadata = planusecase.FieldSolveMetadata

	ReadinessOpen   = planusecase.ReadinessOpen
	ReadinessClosed = planusecase.ReadinessClosed
)

func NewUnlockIntent(reservation Reservation) UnlockIntent {
	return planusecase.NewUnlockIntent(reservation)
}

func NewProjectionIntent(
	expected domain.DerivedRevision,
	nextRevisionID domain.DerivedRevisionID,
	decisionID uuid.UUID,
	payload []byte,
) ProjectionIntent {
	return planusecase.NewProjectionIntent(expected, nextRevisionID, decisionID, payload)
}

func Validate(command Command, authority Authority) (Validation, error) {
	return planusecase.Validate(command, authority)
}

func NewExpectation(authority Authority, targetRevisionID domain.DerivedRevisionID) (Expectation, error) {
	return planusecase.NewExpectation(authority, targetRevisionID)
}

func BuildPlan(command Command, authority Authority) (Plan, error) {
	return planusecase.BuildPlan(command, authority)
}
