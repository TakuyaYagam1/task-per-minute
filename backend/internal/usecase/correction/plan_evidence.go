package correction

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func marshalAtomicCorrectionPlan(plan Plan) ([]byte, error) {
	type projectionDocument struct {
		Revision correctionRevisionDocument
		Payload  []byte
	}
	projections := make([]projectionDocument, len(plan.projections))
	for index, projection := range plan.projections {
		projections[index] = projectionDocument{
			Revision: correctionRevisionDigestDocument(projection.Revision()),
			Payload:  projection.Payload(),
		}
	}
	type supersessionDocument struct {
		Kind, EntityID, Previous, Successor string
		PreviousDecision                    *uuid.UUID
		ReplacementDecision                 uuid.UUID
	}
	supersessions := make([]supersessionDocument, len(plan.superseded))
	for index, supersession := range plan.superseded {
		supersessions[index] = supersessionDocument{
			Kind: string(supersession.Artifact.Kind), EntityID: supersession.Artifact.EntityID.String(),
			Previous:            supersession.PreviousRevisionID.UUID().String(),
			Successor:           supersession.SuccessorRevisionID.UUID().String(),
			PreviousDecision:    cloneCorrectionUUIDPointer(supersession.PreviousDecisionID),
			ReplacementDecision: supersession.ReplacementDecisionID,
		}
	}
	seriesPayload, err := json.Marshal(plan.series)
	if err != nil {
		return nil, err
	}
	decisionPayload, err := json.Marshal(plan.decisions)
	if err != nil {
		return nil, err
	}
	readinessPayload, err := json.Marshal(plan.readiness)
	if err != nil {
		return nil, err
	}
	releasePayload, err := json.Marshal(plan.releases)
	if err != nil {
		return nil, err
	}
	solvePayload, err := json.Marshal(plan.solve)
	if err != nil {
		return nil, err
	}
	auditPayload, err := json.Marshal(plan.audit)
	if err != nil {
		return nil, err
	}
	cutoffPayload, err := json.Marshal(newCorrectionCutoffConditionDocument(plan.cutoff))
	if err != nil {
		return nil, err
	}
	document := struct {
		Schema                                                       string `json:"schema"`
		Validation, GameResult, Score, SeriesResult                  [sha256.Size]byte
		Cutoff, Audit, Series, Solve, Readiness, Releases, Decisions []byte
		Projections                                                  []projectionDocument
		Supersessions                                                []supersessionDocument
		DAG                                                          [sha256.Size]byte
		Rebuild                                                      []byte
	}{
		Schema:       "result-correction-plan-v1",
		Validation:   correctionValidationDigest(plan.validation),
		GameResult:   correctionOfficialPlanDigest(plan.gameResult),
		Score:        correctionScorePlanDigest(plan.score),
		SeriesResult: correctionOfficialPlanDigest(plan.seriesResult),
		Cutoff:       cutoffPayload, Audit: auditPayload, Series: seriesPayload, Solve: solvePayload, Projections: projections,
		Supersessions: supersessions, Readiness: readinessPayload,
		Releases: releasePayload, Decisions: decisionPayload,
		DAG: correctionDAGDigest(plan.dag.Snapshot()), Rebuild: plan.rebuild.Bytes(),
	}
	return json.Marshal(document)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionOfficialPlanDigest(plan resultusecase.OfficialResultRevisionPlan) [sha256.Size]byte {
	condition := plan.Condition()
	revision := plan.Revision()
	seriesPayload, _ := json.Marshal(condition.ExpectedSeries())
	currentDigest := [sha256.Size]byte{}
	if current := condition.ExpectedCurrentHead(); current != nil {
		currentDigest = correctionHeadDigest(*current)
	}
	document := struct {
		Scope                           resultusecase.OfficialResultScope
		Series                          []byte
		Current                         [sha256.Size]byte
		Source                          correctionRevisionDocument
		SeriesRevision, AttemptRevision int64
		Planned                         uuid.UUID
		Revision                        [sha256.Size]byte
	}{
		Scope: condition.Scope(), Series: seriesPayload, Current: currentDigest,
		Source:          correctionRevisionDigestDocument(condition.ExpectedSourceProjection()),
		SeriesRevision:  int64(condition.ExpectedSeriesRevision()),
		AttemptRevision: int64(condition.ExpectedAttemptRevision()),
		Planned:         revision.ID().UUID(),
		Revision:        correctionHeadDigest(revision.Head()),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionScorePlanDigest(plan resultusecase.SeriesScoreRevisionPlan) [sha256.Size]byte {
	condition := plan.Condition()
	revision := plan.Revision()
	seriesPayload, _ := json.Marshal(condition.ExpectedSeries())
	currentDigest := [sha256.Size]byte{}
	if current := condition.ExpectedCurrentHead(); current != nil {
		currentDigest = correctionScoreHeadDigest(*current)
	}
	document := struct {
		Scope                           resultusecase.SeriesScoreRevisionScope
		Series                          []byte
		Current                         [sha256.Size]byte
		Source                          correctionRevisionDocument
		SeriesRevision, AttemptRevision int64
		Planned                         uuid.UUID
		Operation                       resultusecase.SeriesScoreRevisionOperation
		Revision                        [sha256.Size]byte
	}{
		Scope: condition.Scope(), Series: seriesPayload, Current: currentDigest,
		Source:          correctionRevisionDigestDocument(condition.ExpectedSourceProjection()),
		SeriesRevision:  int64(condition.ExpectedSeriesRevision()),
		AttemptRevision: int64(condition.ExpectedAttemptRevision()),
		Planned:         revision.ID().UUID(), Operation: revision.Operation(),
		Revision: correctionScoreHeadDigest(revision.Head()),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

func cloneCorrectionReadiness(readiness Readiness) Readiness {
	clone := readiness
	clone.ParticipantIDs = append([]uuid.UUID(nil), readiness.ParticipantIDs...)
	return clone
}

func cloneCorrectionAuditRecord(record AuditRecord) AuditRecord {
	clone := record
	clone.Fields = append([]Field(nil), record.Fields...)
	return clone
}

func cloneCorrectionSolveTransition(
	transition SolveTransition,
) SolveTransition {
	return SolveTransition{
		Expected: transition.Expected.Clone(), Next: transition.Next.Clone(),
	}
}

func cloneCorrectionCutoffCondition(
	condition CutoffCondition,
) CutoffCondition {
	clone := condition
	clone.affected = append([]domain.DerivedRevision(nil), condition.affected...)
	return clone
}

func correctionCutoffConditionsEqual(
	first CutoffCondition,
	second CutoffCondition,
) bool {
	if first.tournamentID != second.tournamentID ||
		first.expectedTournamentState != second.expectedTournamentState ||
		first.expectedTournamentRevision != second.expectedTournamentRevision ||
		!correctionDerivedRevisionsEqual(first.target, second.target) ||
		first.observedEventsDigest != second.observedEventsDigest ||
		len(first.affected) != len(second.affected) {
		return false
	}
	for index := range first.affected {
		if !correctionDerivedRevisionsEqual(first.affected[index], second.affected[index]) {
			return false
		}
	}
	return true
}

type correctionCutoffConditionDocument struct {
	TournamentID               uuid.UUID
	ExpectedTournamentState    domain.TournamentState
	ExpectedTournamentRevision int64
	Target                     correctionRevisionDocument
	Affected                   []correctionRevisionDocument
	ObservedEventsDigest       [sha256.Size]byte
}

func newCorrectionCutoffConditionDocument(
	condition CutoffCondition,
) correctionCutoffConditionDocument {
	affected := make([]correctionRevisionDocument, len(condition.affected))
	for index, revision := range condition.affected {
		affected[index] = correctionRevisionDigestDocument(revision)
	}
	return correctionCutoffConditionDocument{
		TournamentID:               condition.tournamentID,
		ExpectedTournamentState:    condition.expectedTournamentState,
		ExpectedTournamentRevision: condition.expectedTournamentRevision,
		Target:                     correctionRevisionDigestDocument(condition.target),
		Affected:                   affected,
		ObservedEventsDigest:       condition.observedEventsDigest,
	}
}

func cloneCorrectionReadinessTransition(
	transition ReadinessTransition,
) ReadinessTransition {
	clone := transition
	clone.Expected = cloneCorrectionReadiness(transition.Expected)
	clone.Next = cloneCorrectionReadiness(transition.Next)
	return clone
}

func correctionParticipantsEqual(first, second []uuid.UUID) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func cloneCorrectionSupersessions(
	input []ProjectionSupersession,
) []ProjectionSupersession {
	clone := make([]ProjectionSupersession, len(input))
	for index := range input {
		clone[index] = input[index]
		clone[index].PreviousDecisionID = cloneCorrectionUUIDPointer(input[index].PreviousDecisionID)
	}
	return clone
}

func invalidBuiltCorrection(message string, cause error) error {
	if cause != nil {
		message = fmt.Sprintf("%s: %v", message, cause)
	}
	return rejectCorrection(RejectionMalformed, ErrInvalid, message)
}
