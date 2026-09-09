package correction

import (
	"crypto/sha256"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

const maxCorrectionProjectionDecisions = 4096

type ProjectionSupersession struct {
	Artifact              domain.ArtifactRef
	PreviousRevisionID    domain.DerivedRevisionID
	SuccessorRevisionID   domain.DerivedRevisionID
	PreviousDecisionID    *uuid.UUID
	ReplacementDecisionID uuid.UUID
}

type ReadinessTransition struct {
	Expected Readiness
	Next     Readiness
	ClosedAt time.Time
}

type ReservationRelease struct {
	ReservationID     uuid.UUID
	TournamentID      uuid.UUID
	OwnerID           uuid.UUID
	SourceRevisionID  domain.DerivedRevisionID
	ExpectedRevision  int64
	ExpectedUsed      bool
	ExpectedDisclosed bool
	NextRevision      int64
	NextReleased      bool
	EvidenceDigest    [sha256.Size]byte
	ReleasedAt        time.Time
}

func (r ReservationRelease) ValidateCurrent(current Reservation) error {
	if !validReservationRelease(r) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid reservation release condition",
		)
	}
	if !reservationReleaseMatches(r, current) {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "reservation changed before release",
		)
	}
	return nil
}

func validReservationRelease(release ReservationRelease) bool {
	return release.ReservationID != uuid.Nil && release.TournamentID != uuid.Nil &&
		release.OwnerID != uuid.Nil && !release.SourceRevisionID.IsZero() &&
		release.ExpectedRevision > 0 && release.ExpectedRevision != math.MaxInt64 &&
		release.NextRevision == release.ExpectedRevision+1 && !release.ExpectedUsed &&
		!release.ExpectedDisclosed && release.NextReleased &&
		release.EvidenceDigest != ([sha256.Size]byte{}) && validCorrectionTime(release.ReleasedAt)
}

func reservationReleaseMatches(release ReservationRelease, current Reservation) bool {
	return current.ID == release.ReservationID && current.TournamentID == release.TournamentID &&
		current.OwnerID == release.OwnerID && current.SourceRevisionID == release.SourceRevisionID &&
		current.Revision == release.ExpectedRevision && current.Used == release.ExpectedUsed &&
		current.Disclosed == release.ExpectedDisclosed && current.EvidenceDigest == release.EvidenceDigest
}

// CorrectionCutoffCondition is the immutable read condition that must be
// checked under the same transaction lock as every write in the plan.
type CutoffCondition struct {
	tournamentID               uuid.UUID
	expectedTournamentState    domain.TournamentState
	expectedTournamentRevision int64
	target                     domain.DerivedRevision
	affected                   []domain.DerivedRevision
	observedEventsDigest       [sha256.Size]byte
}

func (c CutoffCondition) TournamentID() uuid.UUID {
	return c.tournamentID
}

func (c CutoffCondition) ExpectedTournamentState() domain.TournamentState {
	return c.expectedTournamentState
}

func (c CutoffCondition) ExpectedTournamentRevision() int64 {
	return c.expectedTournamentRevision
}

func (c CutoffCondition) TargetRevision() domain.DerivedRevision {
	return cloneCorrectionDerivedRevision(c.target)
}

func (c CutoffCondition) AffectedRevisions() []domain.DerivedRevision {
	return append([]domain.DerivedRevision(nil), c.affected...)
}

func (c CutoffCondition) ObservedEventsDigest() [sha256.Size]byte {
	return c.observedEventsDigest
}

// ValidateCurrent must run under the same transaction lock as plan execution.
// The events argument is the complete current tournament cutoff event set.
func (c CutoffCondition) ValidateCurrent(
	tournamentState domain.TournamentState,
	tournamentRevision int64,
	events []CutoffEvent,
) error {
	if !validCurrentCutoffShape(c, tournamentState, tournamentRevision, events) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction cutoff condition",
		)
	}
	if tournamentState.IsTerminal() {
		return rejectCorrection(
			RejectionCutoff, ErrCutoff, "tournament became terminal",
		)
	}
	if tournamentState != c.expectedTournamentState {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "tournament state changed",
		)
	}
	affected, reserved, err := currentCutoffRevisions(c)
	if err != nil {
		return err
	}
	blocked, err := currentCutoffBlocked(c.tournamentID, events, affected, reserved)
	if err != nil {
		return err
	}
	if blocked {
		return rejectCorrection(
			RejectionCutoff, ErrCutoff, "irreversible event exists",
		)
	}
	if tournamentRevision != c.expectedTournamentRevision {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "tournament revision changed",
		)
	}
	if correctionCutoffEventSetDigest(events) != c.observedEventsDigest {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "cutoff event set changed",
		)
	}
	return nil
}

func validCurrentCutoffShape(
	condition CutoffCondition,
	tournamentState domain.TournamentState,
	tournamentRevision int64,
	events []CutoffEvent,
) bool {
	return condition.tournamentID != uuid.Nil && !condition.target.ID().IsZero() &&
		len(condition.affected) > 0 && correctionDerivedRevisionsEqual(condition.target, condition.affected[0]) &&
		condition.expectedTournamentState.IsValid() && tournamentState.IsValid() &&
		condition.expectedTournamentRevision > 0 && tournamentRevision > 0 &&
		len(events) <= maxCorrectionCutoffEvents
}

func currentCutoffRevisions(
	condition CutoffCondition,
) (map[domain.DerivedRevisionID]domain.DerivedRevision, map[uuid.UUID]struct{}, error) {
	affected := make(map[domain.DerivedRevisionID]domain.DerivedRevision, len(condition.affected))
	reserved := make(map[uuid.UUID]struct{}, len(condition.affected))
	for _, revision := range condition.affected {
		if revision.ID().IsZero() || revision.TournamentID() != condition.tournamentID {
			return nil, nil, rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid affected correction revision",
			)
		}
		if _, duplicate := affected[revision.ID()]; duplicate {
			return nil, nil, correctionIdentityAlias()
		}
		affected[revision.ID()] = revision
		reserved[revision.ID().UUID()] = struct{}{}
	}
	return affected, reserved, nil
}

func currentCutoffBlocked(
	tournamentID uuid.UUID,
	events []CutoffEvent,
	affected map[domain.DerivedRevisionID]domain.DerivedRevision,
	reserved map[uuid.UUID]struct{},
) (bool, error) {
	seen := make(map[uuid.UUID]struct{}, len(events))
	blocked := false
	for _, event := range events {
		if event.TournamentID != tournamentID {
			return false, rejectCorrection(
				RejectionCrossTournament, ErrInvalid, "cutoff event belongs to another tournament",
			)
		}
		if event.ID == uuid.Nil || !validCorrectionCutoffKind(event.Kind) ||
			!validCorrectionTime(event.OccurredAt) {
			return false, rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid current cutoff event",
			)
		}
		if _, duplicate := seen[event.ID]; duplicate {
			return false, correctionIdentityAlias()
		}
		if _, alias := reserved[event.ID]; alias {
			return false, correctionIdentityAlias()
		}
		seen[event.ID] = struct{}{}
		if source, isAffected := affected[event.SourceRevisionID]; isAffected {
			if event.OccurredAt.Before(source.CreatedAt()) {
				return false, rejectCorrection(
					RejectionMalformed, ErrInvalid, "cutoff event predates its source revision",
				)
			}
			blocked = true
		}
	}
	return blocked, nil
}

type AuditRecord struct {
	TournamentID     uuid.UUID
	SeriesID         uuid.UUID
	GameID           uuid.UUID
	CommandID        uuid.UUID
	CascadeCommandID uuid.UUID
	OperatorID       uuid.UUID
	Confirmed        bool
	Reason           Reason
	Explanation      string
	RequestedAt      time.Time
	Fields           []Field
	ValidationDigest [sha256.Size]byte
}

type SolveTransition struct {
	Expected SolveMetadata
	Next     SolveMetadata
}

type Plan struct {
	validation   Validation
	cutoff       CutoffCondition
	audit        AuditRecord
	gameResult   resultusecase.OfficialResultRevisionPlan
	score        resultusecase.SeriesScoreRevisionPlan
	seriesResult resultusecase.OfficialResultRevisionPlan
	series       domain.Series
	solve        SolveTransition
	projections  []domain.ProjectionRevision
	superseded   []ProjectionSupersession
	readiness    ReadinessTransition
	releases     []ReservationRelease
	decisions    []resultprojection.RecordedProjectionDecision
	dag          resultprojection.RevisionDAG
	rebuild      resultprojection.ProjectionRebuild
	payload      []byte
	binding      [sha256.Size]byte
}

func BuildPlan(
	command Command,
	authority Authority,
) (Plan, error) {
	validation, err := Validate(command, authority)
	if err != nil {
		return Plan{}, err
	}
	plan, err := buildAtomicCorrection(validation)
	if err != nil {
		return Plan{}, err
	}
	payload, err := marshalAtomicCorrectionPlan(plan)
	if err != nil {
		return Plan{}, rejectCorrection(
			RejectionMalformed, ErrInvalid, "marshal correction plan",
		)
	}
	plan.payload = payload
	plan.binding = sha256.Sum256(payload)
	if err := validateAtomicCorrectionLinks(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}
