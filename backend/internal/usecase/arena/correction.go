package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type CorrectionProjectionSupersession struct {
	Artifact              domain.ArenaArtifactRef
	PreviousRevisionID    domain.ArenaDerivedRevisionID
	SuccessorRevisionID   domain.ArenaDerivedRevisionID
	PreviousDecisionID    *uuid.UUID
	ReplacementDecisionID uuid.UUID
}

type CorrectionReadinessTransition struct {
	Expected CorrectionReadiness
	Next     CorrectionReadiness
	ClosedAt time.Time
}

type CorrectionReservationRelease struct {
	ReservationID     uuid.UUID
	TournamentID      uuid.UUID
	OwnerID           uuid.UUID
	SourceRevisionID  domain.ArenaDerivedRevisionID
	ExpectedRevision  int64
	ExpectedUsed      bool
	ExpectedDisclosed bool
	NextRevision      int64
	NextReleased      bool
	EvidenceDigest    [sha256.Size]byte
	ReleasedAt        time.Time
}

//nolint:gocyclo // The CAS boundary deliberately checks every persisted field before release.
func (r CorrectionReservationRelease) ValidateCurrent(current CorrectionReservation) error {
	if r.ReservationID == uuid.Nil || r.TournamentID == uuid.Nil || r.OwnerID == uuid.Nil ||
		r.SourceRevisionID.IsZero() || r.ExpectedRevision <= 0 ||
		r.ExpectedRevision == math.MaxInt64 || r.NextRevision != r.ExpectedRevision+1 ||
		r.ExpectedUsed || r.ExpectedDisclosed ||
		!r.NextReleased || r.EvidenceDigest == ([sha256.Size]byte{}) ||
		!validCorrectionTime(r.ReleasedAt) {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid reservation release condition",
		)
	}
	if current.ID != r.ReservationID || current.TournamentID != r.TournamentID ||
		current.OwnerID != r.OwnerID || current.SourceRevisionID != r.SourceRevisionID ||
		current.Revision != r.ExpectedRevision || current.Used != r.ExpectedUsed ||
		current.Disclosed != r.ExpectedDisclosed || current.EvidenceDigest != r.EvidenceDigest {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "reservation changed before release",
		)
	}
	return nil
}

// CorrectionCutoffCondition is the immutable read condition that must be
// checked under the same transaction lock as every write in the plan.
type CorrectionCutoffCondition struct {
	tournamentID               uuid.UUID
	expectedTournamentState    domain.ArenaTournamentState
	expectedTournamentRevision int64
	target                     domain.ArenaDerivedRevision
	affected                   []domain.ArenaDerivedRevision
	observedEventsDigest       [sha256.Size]byte
}

func (c CorrectionCutoffCondition) TournamentID() uuid.UUID {
	return c.tournamentID
}

func (c CorrectionCutoffCondition) ExpectedTournamentState() domain.ArenaTournamentState {
	return c.expectedTournamentState
}

func (c CorrectionCutoffCondition) ExpectedTournamentRevision() int64 {
	return c.expectedTournamentRevision
}

func (c CorrectionCutoffCondition) TargetRevision() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.target)
}

func (c CorrectionCutoffCondition) AffectedRevisions() []domain.ArenaDerivedRevision {
	return append([]domain.ArenaDerivedRevision(nil), c.affected...)
}

func (c CorrectionCutoffCondition) ObservedEventsDigest() [sha256.Size]byte {
	return c.observedEventsDigest
}

// ValidateCurrent must run under the same transaction lock as plan execution.
// The events argument is the complete current tournament cutoff event set.
//
//nolint:gocyclo // The same-lock cutoff check is intentionally fail-closed for every evidence defect.
func (c CorrectionCutoffCondition) ValidateCurrent(
	tournamentState domain.ArenaTournamentState,
	tournamentRevision int64,
	events []CorrectionCutoffEvent,
) error {
	if c.tournamentID == uuid.Nil || c.target.ID().IsZero() || len(c.affected) == 0 ||
		!arenaDerivedRevisionsEqual(c.target, c.affected[0]) ||
		!c.expectedTournamentState.IsValid() || !tournamentState.IsValid() ||
		c.expectedTournamentRevision <= 0 || tournamentRevision <= 0 ||
		len(events) > maxCorrectionCutoffEvents {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction cutoff condition",
		)
	}
	if tournamentState.IsTerminal() {
		return rejectCorrection(
			CorrectionRejectionCutoff, ErrCorrectionCutoff, "tournament became terminal",
		)
	}
	if tournamentState != c.expectedTournamentState {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "tournament state changed",
		)
	}
	affected := make(map[domain.ArenaDerivedRevisionID]domain.ArenaDerivedRevision, len(c.affected))
	reserved := make(map[uuid.UUID]struct{}, len(c.affected))
	for _, revision := range c.affected {
		if revision.ID().IsZero() || revision.TournamentID() != c.tournamentID {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid affected correction revision",
			)
		}
		if _, duplicate := affected[revision.ID()]; duplicate {
			return correctionIdentityAlias()
		}
		affected[revision.ID()] = revision
		reserved[revision.ID().UUID()] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(events))
	blocked := false
	for _, event := range events {
		if event.TournamentID != c.tournamentID {
			return rejectCorrection(
				CorrectionRejectionCrossTournament, ErrInvalidCorrection, "cutoff event belongs to another tournament",
			)
		}
		if event.ID == uuid.Nil || !validCorrectionCutoffKind(event.Kind) ||
			!validCorrectionTime(event.OccurredAt) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid current cutoff event",
			)
		}
		if _, duplicate := seen[event.ID]; duplicate {
			return correctionIdentityAlias()
		}
		if _, alias := reserved[event.ID]; alias {
			return correctionIdentityAlias()
		}
		seen[event.ID] = struct{}{}
		if source, isAffected := affected[event.SourceRevisionID]; isAffected {
			if event.OccurredAt.Before(source.CreatedAt()) {
				return rejectCorrection(
					CorrectionRejectionMalformed, ErrInvalidCorrection, "cutoff event predates its source revision",
				)
			}
			blocked = true
		}
	}
	if blocked {
		return rejectCorrection(
			CorrectionRejectionCutoff, ErrCorrectionCutoff, "irreversible event exists",
		)
	}
	if tournamentRevision != c.expectedTournamentRevision {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "tournament revision changed",
		)
	}
	if correctionCutoffEventSetDigest(events) != c.observedEventsDigest {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "cutoff event set changed",
		)
	}
	return nil
}

type CorrectionAuditRecord struct {
	TournamentID     uuid.UUID
	SeriesID         uuid.UUID
	GameID           uuid.UUID
	CommandID        uuid.UUID
	CascadeCommandID uuid.UUID
	OperatorID       uuid.UUID
	Confirmed        bool
	Reason           CorrectionReason
	Explanation      string
	RequestedAt      time.Time
	Fields           []CorrectionField
	ValidationDigest [sha256.Size]byte
}

type CorrectionSolveTransition struct {
	Expected CorrectionSolveMetadata
	Next     CorrectionSolveMetadata
}

type AtomicCorrectionPlan struct {
	validation   CorrectionValidation
	cutoff       CorrectionCutoffCondition
	audit        CorrectionAuditRecord
	gameResult   OfficialResultRevisionPlan
	score        SeriesScoreRevisionPlan
	seriesResult OfficialResultRevisionPlan
	series       domain.ArenaSeries
	solve        CorrectionSolveTransition
	projections  []domain.ArenaProjectionRevision
	superseded   []CorrectionProjectionSupersession
	readiness    CorrectionReadinessTransition
	releases     []CorrectionReservationRelease
	decisions    []RecordedProjectionDecision
	dag          RevisionDAG
	rebuild      ProjectionRebuild
	payload      []byte
	binding      [sha256.Size]byte
}

func PlanAtomicCorrection(
	command CorrectionCommand,
	authority CorrectionAuthority,
) (AtomicCorrectionPlan, error) {
	validation, err := ValidateCorrection(command, authority)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	plan, err := buildAtomicCorrection(validation)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	payload, err := marshalAtomicCorrectionPlan(plan)
	if err != nil {
		return AtomicCorrectionPlan{}, rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "marshal correction plan",
		)
	}
	plan.payload = payload
	plan.binding = sha256.Sum256(payload)
	if err := validateAtomicCorrectionLinks(plan); err != nil {
		return AtomicCorrectionPlan{}, err
	}
	return plan, nil
}

func (p AtomicCorrectionPlan) Validate() error {
	if err := p.validation.Validate(); err != nil {
		return err
	}
	if err := validateAtomicCorrectionLinks(p); err != nil {
		return err
	}
	if err := p.gameResult.Validate(); err != nil {
		return invalidBuiltCorrection("invalid Game result revision", err)
	}
	if err := p.score.Validate(); err != nil {
		return invalidBuiltCorrection("invalid score revision", err)
	}
	if err := p.seriesResult.Validate(); err != nil {
		return invalidBuiltCorrection("invalid Series result revision", err)
	}
	if p.series.Validate() != nil || p.dag.Validate() != nil {
		return invalidBuiltCorrection("invalid projected authority", nil)
	}
	rebuilt, err := RebuildOfficialProjections(ProjectionRebuildInput{
		DAG: p.dag, Decisions: p.decisions,
	})
	if err != nil || !bytes.Equal(rebuilt.Bytes(), p.rebuild.Bytes()) {
		return invalidBuiltCorrection("projection rebuild changed", err)
	}
	payload, err := marshalAtomicCorrectionPlan(p)
	if err != nil || !bytes.Equal(payload, p.payload) || sha256.Sum256(payload) != p.binding {
		return invalidBuiltCorrection("correction plan was spliced", err)
	}
	return nil
}

//nolint:gocyclo // Detached validation explicitly cross-links every executable plan component.
func validateAtomicCorrectionLinks(plan AtomicCorrectionPlan) error {
	command := plan.validation.command
	expectedCutoff := buildCorrectionCutoffCondition(plan.validation)
	if len(plan.projections) != len(command.ProjectionIntents) ||
		len(plan.superseded) != len(plan.projections)-1 ||
		plan.audit.ValidationDigest != plan.validation.bindingDigest ||
		plan.audit.TournamentID != command.TournamentID || plan.audit.SeriesID != command.SeriesID ||
		plan.audit.GameID != command.GameID || plan.audit.CommandID != command.CommandID ||
		plan.audit.CascadeCommandID != command.CascadeCommandID ||
		plan.audit.OperatorID != command.OperatorID || plan.audit.Confirmed != command.Confirmed ||
		plan.audit.Reason != command.Reason || plan.audit.Explanation != command.Explanation ||
		!plan.audit.RequestedAt.Equal(command.RequestedAt) ||
		!correctionFieldsEqual(plan.audit.Fields, command.Fields) ||
		correctionSolveMetadataDigest(plan.solve.Expected) != command.Expected.CurrentSolveDigest ||
		!correctionCutoffConditionsEqual(plan.cutoff, expectedCutoff) ||
		correctionSolveMetadataDigest(plan.solve.Next) !=
			correctionSolveMetadataDigest(command.Patch.SolveMetadata) {
		return invalidBuiltCorrection("correction audit or cardinality changed", nil)
	}
	if len(plan.releases) != len(plan.validation.unlocks) {
		return invalidBuiltCorrection("reservation release cardinality changed", nil)
	}
	for index, intent := range plan.validation.unlocks {
		release := plan.releases[index]
		if intent.ExpectedUsed || intent.ExpectedDisclosed ||
			release.ExpectedUsed || release.ExpectedDisclosed ||
			release.ReservationID != intent.ReservationID ||
			release.TournamentID != intent.TournamentID || release.OwnerID != intent.OwnerID ||
			release.SourceRevisionID != intent.SourceRevisionID ||
			release.ExpectedRevision != intent.ExpectedRevision ||
			release.ExpectedUsed != intent.ExpectedUsed ||
			release.ExpectedDisclosed != intent.ExpectedDisclosed ||
			release.NextRevision != intent.ExpectedRevision+1 || !release.NextReleased ||
			release.EvidenceDigest != intent.EvidenceDigest ||
			!release.ReleasedAt.Equal(command.RequestedAt) {
			return invalidBuiltCorrection("reservation release link changed", nil)
		}
	}
	if correctionReadinessDigest(plan.readiness.Expected) !=
		correctionReadinessDigest(plan.validation.authority.Readiness) ||
		plan.readiness.Next.TournamentID != plan.readiness.Expected.TournamentID ||
		plan.readiness.Next.OwnerID != plan.readiness.Expected.OwnerID ||
		plan.readiness.Next.WaveID != plan.readiness.Expected.WaveID ||
		plan.readiness.Next.WindowID != plan.readiness.Expected.WindowID ||
		plan.readiness.Next.RevisionID != command.NextReadinessRevisionID ||
		plan.readiness.Next.Revision != plan.readiness.Expected.Revision+1 ||
		plan.readiness.Next.State != CorrectionReadinessClosed ||
		!correctionParticipantsEqual(
			plan.readiness.Next.ParticipantIDs, plan.readiness.Expected.ParticipantIDs,
		) || !plan.readiness.ClosedAt.Equal(command.RequestedAt) {
		return invalidBuiltCorrection("readiness transition link changed", nil)
	}
	gameRevision := plan.gameResult.revision
	scoreRevision := plan.score.revision
	seriesRevision := plan.seriesResult.revision
	if gameRevision.id != command.NextResultRevisionID ||
		!officialResultRevisionIDPointersEqual(
			gameRevision.previousRevisionID, &plan.validation.authority.GameResult.ID,
		) || gameRevision.commandID != command.CommandID ||
		!arenaDerivedRevisionsEqual(gameRevision.sourceProjection, plan.projections[0].Revision()) ||
		!officialResultOutcomesEqual(gameRevision.outcome, OfficialResultOutcome{
			GameState: command.Patch.State, GameReason: command.Patch.Reason,
			WinnerID: cloneUUIDPointer(command.Patch.WinnerID),
		}) || !gameRevision.recordedAt.Equal(command.RequestedAt) {
		return invalidBuiltCorrection("Game result revision link changed", nil)
	}
	var scoreProjection, seriesProjection domain.ArenaDerivedRevision
	for _, projection := range plan.projections {
		//nolint:exhaustive // Only the two correction source artifacts are selected here.
		switch projection.Revision().Artifact().Kind {
		case domain.ArenaArtifactKindSeriesScore:
			scoreProjection = projection.Revision()
		case domain.ArenaArtifactKindSeriesResult:
			seriesProjection = projection.Revision()
		default:
		}
	}
	if scoreRevision.id != command.NextScoreRevisionID || scoreRevision.commandID != command.CommandID ||
		scoreRevision.operation != SeriesScoreRevisionOperationReplaceResult ||
		!seriesScoreRevisionIDPointersEqual(
			scoreRevision.previousRevisionID, &plan.validation.authority.Score.ID,
		) || !arenaDerivedRevisionsEqual(scoreRevision.sourceProjection, scoreProjection) ||
		!scoreRevision.recordedAt.Equal(command.RequestedAt) ||
		seriesRevision.id != command.NextSeriesResultRevisionID ||
		seriesRevision.commandID != command.CascadeCommandID ||
		!officialResultRevisionIDPointersEqual(
			seriesRevision.previousRevisionID, &plan.validation.authority.SeriesResult.ID,
		) || !arenaDerivedRevisionsEqual(seriesRevision.sourceProjection, seriesProjection) ||
		seriesRevision.outcome.SeriesReason != ArenaSeriesResultReasonOperatorCorrection ||
		!seriesRevision.recordedAt.Equal(command.RequestedAt) {
		return invalidBuiltCorrection("score or Series revision link changed", nil)
	}
	decisionOffset := len(plan.decisions) - len(command.ProjectionIntents)
	if decisionOffset < 0 {
		return invalidBuiltCorrection("projection decision cardinality changed", nil)
	}
	for index, intent := range command.ProjectionIntents {
		decision := plan.decisions[decisionOffset+index]
		if decision.ID != intent.DecisionID || decision.Sequence != decisionOffset+index+1 ||
			decision.ProjectionRevisionID != intent.NextRevisionID ||
			!decision.RecordedAt.Equal(command.RequestedAt) ||
			decision.PayloadDigest != intent.PayloadDigest || !bytes.Equal(decision.Payload, intent.Payload) {
			return invalidBuiltCorrection("projection decision link changed", nil)
		}
	}
	for index, intent := range command.ProjectionIntents {
		revision := plan.projections[index].Revision()
		previous := revision.PreviousRevisionID()
		if previous == nil || *previous != intent.ExpectedRevision.ID() ||
			revision.ID() != intent.NextRevisionID || revision.Artifact() != intent.ExpectedRevision.Artifact() ||
			revision.PayloadDigest() != intent.PayloadDigest {
			return invalidBuiltCorrection("projection successor link changed", nil)
		}
		if index == 0 {
			continue
		}
		supersession := plan.superseded[index-1]
		if supersession.PreviousRevisionID.IsZero() || supersession.SuccessorRevisionID.IsZero() ||
			supersession.ReplacementDecisionID == uuid.Nil ||
			supersession.Artifact != intent.ExpectedRevision.Artifact() ||
			supersession.PreviousRevisionID != intent.ExpectedRevision.ID() ||
			supersession.SuccessorRevisionID != revision.ID() ||
			supersession.ReplacementDecisionID != intent.DecisionID {
			return invalidBuiltCorrection("projection supersession link changed", nil)
		}
	}
	return nil
}

func (p AtomicCorrectionPlan) GameResultRevision() OfficialResultRevisionPlan {
	return p.gameResult
}

func (p AtomicCorrectionPlan) Audit() CorrectionAuditRecord {
	return cloneCorrectionAuditRecord(p.audit)
}

func (p AtomicCorrectionPlan) CutoffCondition() CorrectionCutoffCondition {
	return cloneCorrectionCutoffCondition(p.cutoff)
}

func (p AtomicCorrectionPlan) ScoreRevision() SeriesScoreRevisionPlan {
	return p.score
}

func (p AtomicCorrectionPlan) SeriesResultRevision() OfficialResultRevisionPlan {
	return p.seriesResult
}

func (p AtomicCorrectionPlan) Series() domain.ArenaSeries {
	return cloneRevisionArenaSeries(p.series)
}

func (p AtomicCorrectionPlan) SolveMetadata() CorrectionSolveMetadata {
	return p.solve.Next.Clone()
}

func (p AtomicCorrectionPlan) SolveTransition() CorrectionSolveTransition {
	return cloneCorrectionSolveTransition(p.solve)
}

func (p AtomicCorrectionPlan) ProjectionRevisions() []domain.ArenaProjectionRevision {
	return append([]domain.ArenaProjectionRevision(nil), p.projections...)
}

func (p AtomicCorrectionPlan) Supersessions() []CorrectionProjectionSupersession {
	return cloneCorrectionSupersessions(p.superseded)
}

func (p AtomicCorrectionPlan) Readiness() CorrectionReadinessTransition {
	return cloneCorrectionReadinessTransition(p.readiness)
}

func (p AtomicCorrectionPlan) Releases() []CorrectionReservationRelease {
	return append([]CorrectionReservationRelease(nil), p.releases...)
}

func (p AtomicCorrectionPlan) Decisions() []RecordedProjectionDecision {
	return cloneRecordedProjectionDecisions(p.decisions)
}

func (p AtomicCorrectionPlan) DAGSnapshot() RevisionDAGSnapshot {
	return p.dag.Snapshot()
}

func (p AtomicCorrectionPlan) RebuildBytes() []byte {
	return p.rebuild.Bytes()
}

func (p AtomicCorrectionPlan) Bytes() []byte {
	return append([]byte(nil), p.payload...)
}

func buildAtomicCorrection(validation CorrectionValidation) (AtomicCorrectionPlan, error) {
	projections, byPrevious, err := buildCorrectionProjectionSuccessors(validation)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	series, references, err := buildCorrectionSeries(validation)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	gameResult, score, seriesResult, err := buildCorrectionRevisionPlans(
		validation, series, references, byPrevious,
	)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	dag, err := buildCorrectionDAG(validation, projections, byPrevious, gameResult, score, seriesResult)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	decisions, previousDecisions, err := buildCorrectionDecisions(validation, byPrevious)
	if err != nil {
		return AtomicCorrectionPlan{}, err
	}
	rebuild, err := RebuildOfficialProjections(ProjectionRebuildInput{DAG: dag, Decisions: decisions})
	if err != nil {
		return AtomicCorrectionPlan{}, invalidBuiltCorrection("rebuild corrected projections", err)
	}
	return AtomicCorrectionPlan{
		validation: validation, cutoff: buildCorrectionCutoffCondition(validation),
		audit:      buildCorrectionAuditRecord(validation),
		gameResult: gameResult, score: score, seriesResult: seriesResult,
		series: series, solve: CorrectionSolveTransition{
			Expected: validation.authority.CurrentSolve.Clone(),
			Next:     validation.command.Patch.SolveMetadata.Clone(),
		},
		projections: projections,
		superseded:  buildCorrectionSupersessions(validation, byPrevious, previousDecisions),
		readiness:   buildCorrectionReadinessTransition(validation),
		releases:    buildCorrectionReleases(validation), decisions: decisions,
		dag: dag, rebuild: rebuild,
	}, nil
}

func buildCorrectionAuditRecord(validation CorrectionValidation) CorrectionAuditRecord {
	command := validation.command
	return CorrectionAuditRecord{
		TournamentID: command.TournamentID, SeriesID: command.SeriesID, GameID: command.GameID,
		CommandID: command.CommandID, CascadeCommandID: command.CascadeCommandID,
		OperatorID: command.OperatorID, Confirmed: command.Confirmed, Reason: command.Reason,
		Explanation: command.Explanation, RequestedAt: command.RequestedAt,
		Fields:           append([]CorrectionField(nil), command.Fields...),
		ValidationDigest: validation.bindingDigest,
	}
}

func buildCorrectionCutoffCondition(validation CorrectionValidation) CorrectionCutoffCondition {
	affected := make([]domain.ArenaDerivedRevision, 0, len(validation.cutoff.descendants)+1)
	affected = append(affected, validation.target)
	affected = append(affected, validation.cutoff.Descendants()...)
	return CorrectionCutoffCondition{
		tournamentID:               validation.command.TournamentID,
		expectedTournamentState:    validation.authority.TournamentState,
		expectedTournamentRevision: validation.authority.TournamentRevision,
		target:                     validation.target,
		affected:                   affected,
		observedEventsDigest:       correctionCutoffEventSetDigest(validation.authority.CutoffEvents),
	}
}

func buildCorrectionProjectionSuccessors(
	validation CorrectionValidation,
) ([]domain.ArenaProjectionRevision, map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision, error) {
	intents := validation.command.ProjectionIntents
	if len(intents) > maxCorrectionDAGProjections-len(validation.snapshot.Projections) {
		return nil, nil, rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "correction exceeds projection bounds",
		)
	}
	payloadBytes := 0
	for _, projection := range validation.snapshot.Projections {
		size := len(projection.Payload())
		if size > maxCorrectionDAGPayloadBytes-payloadBytes {
			return nil, nil, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "correction exceeds DAG payload bounds",
			)
		}
		payloadBytes += size
	}
	for _, intent := range intents {
		if len(intent.Payload) > maxCorrectionDAGPayloadBytes-payloadBytes {
			return nil, nil, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "correction exceeds DAG payload bounds",
			)
		}
		payloadBytes += len(intent.Payload)
	}
	projections := make([]domain.ArenaProjectionRevision, len(intents))
	byPrevious := make(map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision, len(intents))
	for index, intent := range intents {
		previous := intent.ExpectedRevision.ID()
		projection, err := domain.NewArenaProjectionRevision(
			intent.NextRevisionID, validation.command.TournamentID,
			intent.ExpectedRevision.Artifact(), intent.ExpectedRevision.RevisionNo()+1,
			&previous, validation.command.RequestedAt, intent.Payload,
		)
		if err != nil || projection.Revision().PayloadDigest() != intent.PayloadDigest {
			return nil, nil, invalidBuiltCorrection("build projection successor", err)
		}
		projections[index] = projection
		byPrevious[previous] = projection
	}
	return projections, byPrevious, nil
}

func buildCorrectionSeries(
	validation CorrectionValidation,
) (domain.ArenaSeries, []SeriesScoreAttemptReference, error) {
	series := cloneRevisionArenaSeries(validation.authority.Series)
	game, found := findArenaSeriesGamePointer(&series, validation.command.GameID)
	if !found {
		return domain.ArenaSeries{}, nil, rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "corrected Game disappeared",
		)
	}
	game.State = validation.command.Patch.State
	game.ResultReason = validation.command.Patch.Reason
	game.WinnerID = cloneUUIDPointer(validation.command.Patch.WinnerID)
	gameResultID := validation.command.NextResultRevisionID
	game.ResultRevisionID = &gameResultID
	references, err := seriesScoreAttemptReferencesFromSeries(series)
	if err != nil {
		return domain.ArenaSeries{}, nil, invalidBuiltCorrection("derive corrected score attempts", err)
	}
	score, err := scoreFromAttemptReferences(
		references, series.FirstParticipantID, series.SecondParticipantID, series.Format,
	)
	if err != nil {
		return domain.ArenaSeries{}, nil, invalidBuiltCorrection("derive corrected score", err)
	}
	winner := score.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format)
	if series.State == domain.ArenaSeriesStateCompleted && winner == nil {
		return domain.ArenaSeries{}, nil, rejectCorrection(
			CorrectionRejectionTerminal, ErrInvalidCorrection, "corrected Series is not terminal",
		)
	}
	series.Score = score
	if series.State == domain.ArenaSeriesStateCompleted {
		series.WinnerID = winner
	} else {
		series.WinnerID = nil
	}
	scoreID := validation.command.NextScoreRevisionID
	resultID := validation.command.NextSeriesResultRevisionID
	series.CurrentScoreRevisionID = &scoreID
	series.CurrentResultRevisionID = &resultID
	if err := series.Validate(); err != nil {
		return domain.ArenaSeries{}, nil, invalidBuiltCorrection("invalid corrected Series", err)
	}
	return series, references, nil
}

func buildCorrectionRevisionPlans(
	validation CorrectionValidation,
	series domain.ArenaSeries,
	references []SeriesScoreAttemptReference,
	byPrevious map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision,
) (OfficialResultRevisionPlan, SeriesScoreRevisionPlan, OfficialResultRevisionPlan, error) {
	command := validation.command
	authority := validation.authority
	actorID := command.OperatorID
	actor := ArenaResultActor{Kind: ArenaResultActorOperator, PrincipalID: &actorID}
	gameProjection := byPrevious[command.Expected.TargetProjection.ID()].Revision()
	scoreProjection := byPrevious[command.Expected.ScoreProjection.ID()].Revision()
	seriesProjection := byPrevious[command.Expected.SeriesProjection.ID()].Revision()
	gameCurrentID := authority.GameResult.ID
	gamePlan, err := PlanOfficialResultRevision(OfficialResultRevisionCommand{
		Scope: authority.GameResult.Scope, CommandID: command.CommandID,
		RevisionID: command.NextResultRevisionID, Actor: actor,
		ExpectedCurrentRevisionID: &gameCurrentID, ExpectedSourceProjection: gameProjection,
		Outcome: OfficialResultOutcome{
			GameState: command.Patch.State, GameReason: command.Patch.Reason,
			WinnerID: cloneUUIDPointer(command.Patch.WinnerID),
		},
	}, OfficialResultRevisionAuthority{
		Scope: authority.GameResult.Scope, PersistedSeries: authority.Series,
		ProjectedSeries: series, SourceProjection: gameProjection,
		CurrentHead: &authority.GameResult, SeriesRevision: authority.SeriesRevision,
		AttemptRevision: authority.AttemptRevision,
	}, command.RequestedAt)
	if err != nil {
		return OfficialResultRevisionPlan{}, SeriesScoreRevisionPlan{}, OfficialResultRevisionPlan{},
			invalidBuiltCorrection("plan Game result successor", err)
	}
	var commandAttempt *SeriesScoreAttemptReference
	for index := range references {
		if references[index].GameID == command.GameID {
			attempt := cloneSeriesScoreAttemptReference(references[index])
			commandAttempt = &attempt
			break
		}
	}
	if commandAttempt == nil {
		return OfficialResultRevisionPlan{}, SeriesScoreRevisionPlan{}, OfficialResultRevisionPlan{},
			invalidBuiltCorrection("corrected score attempt is missing", nil)
	}
	scoreCurrentID := authority.Score.ID
	scorePlan, err := PlanSeriesScoreRevision(SeriesScoreRevisionCommand{
		Scope: authority.Score.Scope, Operation: SeriesScoreRevisionOperationReplaceResult,
		CommandID: command.CommandID, RevisionID: command.NextScoreRevisionID, Actor: actor,
		ExpectedCurrentRevisionID: &scoreCurrentID, ExpectedSourceProjection: scoreProjection,
		Attempt: commandAttempt,
	}, SeriesScoreRevisionAuthority{
		Scope: authority.Score.Scope, PersistedSeries: authority.Series,
		ProjectedSeries: series, SourceProjection: scoreProjection,
		CurrentHead: &authority.Score, SeriesRevision: authority.SeriesRevision,
		AttemptRevision: authority.AttemptRevision,
	}, command.RequestedAt)
	if err != nil {
		return OfficialResultRevisionPlan{}, SeriesScoreRevisionPlan{}, OfficialResultRevisionPlan{},
			invalidBuiltCorrection("plan score successor", err)
	}
	seriesCurrentID := authority.SeriesResult.ID
	seriesPlan, err := PlanOfficialResultRevision(OfficialResultRevisionCommand{
		Scope: authority.SeriesResult.Scope, CommandID: command.CascadeCommandID,
		RevisionID: command.NextSeriesResultRevisionID, Actor: actor,
		ExpectedCurrentRevisionID: &seriesCurrentID, ExpectedSourceProjection: seriesProjection,
		Outcome: OfficialResultOutcome{
			SeriesState:     series.State,
			SeriesReason:    ArenaSeriesResultReasonOperatorCorrection,
			WinnerID:        cloneUUIDPointer(series.WinnerID),
			ScoreRevisionID: cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID),
		},
	}, OfficialResultRevisionAuthority{
		Scope: authority.SeriesResult.Scope, PersistedSeries: authority.Series,
		ProjectedSeries: series, ProjectedSeriesReason: ArenaSeriesResultReasonOperatorCorrection,
		SourceProjection: seriesProjection, CurrentHead: &authority.SeriesResult,
		SeriesRevision: authority.SeriesRevision,
	}, command.RequestedAt)
	if err != nil {
		return OfficialResultRevisionPlan{}, SeriesScoreRevisionPlan{}, OfficialResultRevisionPlan{},
			invalidBuiltCorrection("plan Series result successor", err)
	}
	return gamePlan, scorePlan, seriesPlan, nil
}

//nolint:gocyclo // DAG reconstruction keeps each lineage and cross-artifact redirect check visible.
func buildCorrectionDAG(
	validation CorrectionValidation,
	projections []domain.ArenaProjectionRevision,
	byPrevious map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision,
	gameResult OfficialResultRevisionPlan,
	score SeriesScoreRevisionPlan,
	seriesResult OfficialResultRevisionPlan,
) (RevisionDAG, error) {
	snapshot := validation.snapshot
	artifacts := make(map[domain.ArenaDerivedRevisionID]domain.ArenaArtifactRef, len(snapshot.Projections)+len(projections))
	for _, projection := range snapshot.Projections {
		artifacts[projection.Revision().ID()] = projection.Revision().Artifact()
	}
	for _, projection := range projections {
		artifacts[projection.Revision().ID()] = projection.Revision().Artifact()
	}
	projectionCount := len(snapshot.Projections) + len(projections)
	dependencyCount := len(snapshot.Dependencies) + len(projections)
	for _, dependency := range snapshot.Dependencies {
		if _, affected := byPrevious[dependency.DerivedRevisionID]; affected &&
			artifacts[dependency.SourceRevisionID] != artifacts[dependency.DerivedRevisionID] {
			dependencyCount++
		}
	}
	if projectionCount > maxCorrectionDAGProjections || dependencyCount > maxCorrectionDAGDependencies {
		return RevisionDAG{}, rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "correction exceeds DAG bounds",
		)
	}
	graphProjections := make([]domain.ArenaProjectionRevision, 0, projectionCount)
	graphProjections = append(graphProjections, snapshot.Projections...)
	graphProjections = append(graphProjections, projections...)
	dependencies := append([]domain.ArenaRevisionDependency(nil), snapshot.Dependencies...)
	for previousID, projection := range byPrevious {
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: projection.Revision().ID(),
		})
	}
	for _, dependency := range snapshot.Dependencies {
		derived, affected := byPrevious[dependency.DerivedRevisionID]
		if !affected {
			continue
		}
		if artifacts[dependency.SourceRevisionID] == artifacts[dependency.DerivedRevisionID] {
			continue
		}
		sourceID := dependency.SourceRevisionID
		if source, sourceAffected := byPrevious[sourceID]; sourceAffected {
			sourceID = source.Revision().ID()
		}
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID: sourceID, DerivedRevisionID: derived.Revision().ID(),
		})
	}
	graph, err := domain.NewArenaRevisionGraph(graphProjections, dependencies)
	if err != nil {
		return RevisionDAG{}, invalidBuiltCorrection("build corrected graph", err)
	}
	inputs := make([]OfficialResultProjectionInput, len(validation.authority.DAG.results))
	for index, prior := range validation.authority.DAG.results {
		input := prior.input
		switch input.Result.Scope {
		case validation.authority.GameResult.Scope:
			input = OfficialResultProjectionInput{
				Result:           gameResult.Revision().Head(),
				ResultProjection: byPrevious[validation.command.Expected.TargetProjection.ID()],
			}
		case validation.authority.SeriesResult.Scope:
			scoreHead := score.Revision().Head()
			scoreProjection := byPrevious[validation.command.Expected.ScoreProjection.ID()]
			input = OfficialResultProjectionInput{
				Result:           seriesResult.Revision().Head(),
				ResultProjection: byPrevious[validation.command.Expected.SeriesProjection.ID()],
				Score:            &scoreHead, ScoreProjection: &scoreProjection,
			}
		}
		inputs[index] = input
	}
	dag, err := BuildRevisionDAG(RevisionDAGInput{Graph: graph, Results: inputs})
	if err != nil {
		return RevisionDAG{}, invalidBuiltCorrection("build corrected revision DAG", err)
	}
	return dag, nil
}

func buildCorrectionDecisions(
	validation CorrectionValidation,
	byPrevious map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision,
) ([]RecordedProjectionDecision, map[domain.ArenaDerivedRevisionID]uuid.UUID, error) {
	old := canonicalCorrectionDecisions(validation.authority.Decisions)
	affected := make(map[domain.ArenaDerivedRevisionID]struct{}, len(byPrevious))
	for id := range byPrevious {
		affected[id] = struct{}{}
	}
	firstAffected := len(old)
	previous := make(map[domain.ArenaDerivedRevisionID]uuid.UUID)
	for index, decision := range old {
		if _, isAffected := affected[decision.ProjectionRevisionID]; isAffected {
			previous[decision.ProjectionRevisionID] = decision.ID
			if firstAffected == len(old) {
				firstAffected = index
			}
		} else if firstAffected != len(old) {
			return nil, nil, rejectCorrection(
				CorrectionRejectionIncomplete, ErrInvalidCorrection,
				"affected projection decisions are not a causal suffix",
			)
		}
	}
	if len(validation.command.ProjectionIntents) > maxProjectionRebuildDecisions-firstAffected {
		return nil, nil, rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "correction exceeds decision bounds",
		)
	}
	decisions := cloneRecordedProjectionDecisions(old[:firstAffected])
	for index, intent := range validation.command.ProjectionIntents {
		successor := byPrevious[intent.ExpectedRevision.ID()].Revision()
		decisions = append(decisions, RecordedProjectionDecision{
			ID: intent.DecisionID, Sequence: firstAffected + index + 1,
			ProjectionRevisionID: successor.ID(), RecordedAt: validation.command.RequestedAt,
			Payload: append([]byte(nil), intent.Payload...), PayloadDigest: intent.PayloadDigest,
		})
	}
	return decisions, previous, nil
}

func buildCorrectionSupersessions(
	validation CorrectionValidation,
	byPrevious map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision,
	previousDecisions map[domain.ArenaDerivedRevisionID]uuid.UUID,
) []CorrectionProjectionSupersession {
	intents := validation.command.ProjectionIntents[1:]
	result := make([]CorrectionProjectionSupersession, len(intents))
	for index, intent := range intents {
		descendant := intent.ExpectedRevision
		var previousDecision *uuid.UUID
		if id, exists := previousDecisions[descendant.ID()]; exists {
			value := id
			previousDecision = &value
		}
		result[index] = CorrectionProjectionSupersession{
			Artifact: descendant.Artifact(), PreviousRevisionID: descendant.ID(),
			SuccessorRevisionID:   byPrevious[descendant.ID()].Revision().ID(),
			PreviousDecisionID:    previousDecision,
			ReplacementDecisionID: intent.DecisionID,
		}
	}
	return result
}

func buildCorrectionReadinessTransition(
	validation CorrectionValidation,
) CorrectionReadinessTransition {
	expected := cloneCorrectionReadiness(validation.authority.Readiness)
	next := cloneCorrectionReadiness(expected)
	next.RevisionID = validation.command.NextReadinessRevisionID
	next.Revision++
	next.State = CorrectionReadinessClosed
	return CorrectionReadinessTransition{
		Expected: expected, Next: next, ClosedAt: validation.command.RequestedAt,
	}
}

func buildCorrectionReleases(validation CorrectionValidation) []CorrectionReservationRelease {
	releases := make([]CorrectionReservationRelease, len(validation.unlocks))
	for index, intent := range validation.unlocks {
		releases[index] = CorrectionReservationRelease{
			ReservationID: intent.ReservationID, TournamentID: intent.TournamentID,
			OwnerID: intent.OwnerID, SourceRevisionID: intent.SourceRevisionID,
			ExpectedRevision: intent.ExpectedRevision,
			ExpectedUsed:     intent.ExpectedUsed, ExpectedDisclosed: intent.ExpectedDisclosed,
			NextRevision: intent.ExpectedRevision + 1, NextReleased: true,
			EvidenceDigest: intent.EvidenceDigest, ReleasedAt: validation.command.RequestedAt,
		}
	}
	return releases
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func marshalAtomicCorrectionPlan(plan AtomicCorrectionPlan) ([]byte, error) {
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
			PreviousDecision:    cloneUUIDPointer(supersession.PreviousDecisionID),
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
		Validation, GameResult, Score, SeriesResult                  [sha256.Size]byte
		Cutoff, Audit, Series, Solve, Readiness, Releases, Decisions []byte
		Projections                                                  []projectionDocument
		Supersessions                                                []supersessionDocument
		DAG                                                          [sha256.Size]byte
		Rebuild                                                      []byte
	}{
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
func correctionOfficialPlanDigest(plan OfficialResultRevisionPlan) [sha256.Size]byte {
	condition := plan.condition
	seriesPayload, _ := json.Marshal(condition.expectedSeries)
	currentDigest := [sha256.Size]byte{}
	if condition.expectedCurrentHead != nil {
		currentDigest = correctionHeadDigest(*condition.expectedCurrentHead)
	}
	document := struct {
		Scope                           OfficialResultScope
		Series                          []byte
		Current                         [sha256.Size]byte
		Source                          correctionRevisionDocument
		SeriesRevision, AttemptRevision int64
		Planned                         uuid.UUID
		Revision                        [sha256.Size]byte
	}{
		Scope: condition.scope, Series: seriesPayload, Current: currentDigest,
		Source:          correctionRevisionDigestDocument(condition.expectedSourceProjection),
		SeriesRevision:  int64(condition.expectedSeriesRevision),
		AttemptRevision: int64(condition.expectedAttemptRevision),
		Planned:         condition.plannedRevisionID.UUID(),
		Revision:        correctionHeadDigest(plan.revision.Head()),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionScorePlanDigest(plan SeriesScoreRevisionPlan) [sha256.Size]byte {
	condition := plan.condition
	seriesPayload, _ := json.Marshal(condition.expectedSeries)
	currentDigest := [sha256.Size]byte{}
	if condition.expectedCurrentHead != nil {
		currentDigest = correctionScoreHeadDigest(*condition.expectedCurrentHead)
	}
	document := struct {
		Scope                           SeriesScoreRevisionScope
		Series                          []byte
		Current                         [sha256.Size]byte
		Source                          correctionRevisionDocument
		SeriesRevision, AttemptRevision int64
		Planned                         uuid.UUID
		Operation                       SeriesScoreRevisionOperation
		Revision                        [sha256.Size]byte
	}{
		Scope: condition.scope, Series: seriesPayload, Current: currentDigest,
		Source:          correctionRevisionDigestDocument(condition.expectedSourceProjection),
		SeriesRevision:  int64(condition.expectedSeriesRevision),
		AttemptRevision: int64(condition.expectedAttemptRevision),
		Planned:         condition.plannedRevisionID.UUID(), Operation: condition.plannedOperation,
		Revision: correctionScoreHeadDigest(plan.revision.Head()),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

func cloneCorrectionReadiness(readiness CorrectionReadiness) CorrectionReadiness {
	clone := readiness
	clone.ParticipantIDs = append([]uuid.UUID(nil), readiness.ParticipantIDs...)
	return clone
}

func cloneCorrectionAuditRecord(record CorrectionAuditRecord) CorrectionAuditRecord {
	clone := record
	clone.Fields = append([]CorrectionField(nil), record.Fields...)
	return clone
}

func cloneCorrectionSolveTransition(
	transition CorrectionSolveTransition,
) CorrectionSolveTransition {
	return CorrectionSolveTransition{
		Expected: transition.Expected.Clone(), Next: transition.Next.Clone(),
	}
}

func cloneCorrectionCutoffCondition(
	condition CorrectionCutoffCondition,
) CorrectionCutoffCondition {
	clone := condition
	clone.affected = append([]domain.ArenaDerivedRevision(nil), condition.affected...)
	return clone
}

func correctionCutoffConditionsEqual(
	first CorrectionCutoffCondition,
	second CorrectionCutoffCondition,
) bool {
	if first.tournamentID != second.tournamentID ||
		first.expectedTournamentState != second.expectedTournamentState ||
		first.expectedTournamentRevision != second.expectedTournamentRevision ||
		!arenaDerivedRevisionsEqual(first.target, second.target) ||
		first.observedEventsDigest != second.observedEventsDigest ||
		len(first.affected) != len(second.affected) {
		return false
	}
	for index := range first.affected {
		if !arenaDerivedRevisionsEqual(first.affected[index], second.affected[index]) {
			return false
		}
	}
	return true
}

type correctionCutoffConditionDocument struct {
	TournamentID               uuid.UUID
	ExpectedTournamentState    domain.ArenaTournamentState
	ExpectedTournamentRevision int64
	Target                     correctionRevisionDocument
	Affected                   []correctionRevisionDocument
	ObservedEventsDigest       [sha256.Size]byte
}

func newCorrectionCutoffConditionDocument(
	condition CorrectionCutoffCondition,
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
	transition CorrectionReadinessTransition,
) CorrectionReadinessTransition {
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
	input []CorrectionProjectionSupersession,
) []CorrectionProjectionSupersession {
	clone := make([]CorrectionProjectionSupersession, len(input))
	for index := range input {
		clone[index] = input[index]
		clone[index].PreviousDecisionID = cloneUUIDPointer(input[index].PreviousDecisionID)
	}
	return clone
}

func invalidBuiltCorrection(message string, cause error) error {
	if cause != nil {
		message = fmt.Sprintf("%s: %v", message, cause)
	}
	return rejectCorrection(CorrectionRejectionMalformed, ErrInvalidCorrection, message)
}
