package plan

import (
	"bytes"
	"crypto/sha256"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func (p Plan) Validate() error {
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
	rebuilt, err := resultprojection.RebuildOfficialProjections(resultprojection.ProjectionRebuildInput{
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

func validateAtomicCorrectionLinks(plan Plan) error {
	command := plan.validation.command
	if len(plan.projections) != len(command.ProjectionIntents) ||
		len(plan.superseded) != len(plan.projections)-1 {
		return invalidBuiltCorrection("correction audit or cardinality changed", nil)
	}
	if !correctionAuditLinksMatch(plan, command) {
		return invalidBuiltCorrection("correction audit or cardinality changed", nil)
	}
	if err := validateCorrectionReleaseLinks(plan, command); err != nil {
		return err
	}
	if !correctionReadinessLinkMatches(plan, command) {
		return invalidBuiltCorrection("readiness transition link changed", nil)
	}
	if err := validateCorrectionResultLinks(plan, command); err != nil {
		return err
	}
	if err := validateCorrectionDecisionLinks(plan, command); err != nil {
		return err
	}
	return validateCorrectionProjectionLinks(plan, command)
}

func correctionAuditLinksMatch(plan Plan, command Command) bool {
	expectedCutoff := buildCorrectionCutoffCondition(plan.validation)
	return plan.audit.ValidationDigest == plan.validation.bindingDigest &&
		plan.audit.TournamentID == command.TournamentID && plan.audit.SeriesID == command.SeriesID &&
		plan.audit.GameID == command.GameID && plan.audit.CommandID == command.CommandID &&
		plan.audit.CascadeCommandID == command.CascadeCommandID &&
		plan.audit.OperatorID == command.OperatorID && plan.audit.Confirmed == command.Confirmed &&
		plan.audit.Reason == command.Reason && plan.audit.Explanation == command.Explanation &&
		plan.audit.RequestedAt.Equal(command.RequestedAt) &&
		correctionFieldsEqual(plan.audit.Fields, command.Fields) &&
		correctionSolveMetadataDigest(plan.solve.Expected) == command.Expected.CurrentSolveDigest &&
		correctionCutoffConditionsEqual(plan.cutoff, expectedCutoff) &&
		correctionSolveMetadataDigest(plan.solve.Next) ==
			correctionSolveMetadataDigest(command.Patch.SolveMetadata)
}

func validateCorrectionReleaseLinks(plan Plan, command Command) error {
	if len(plan.releases) != len(plan.validation.unlocks) {
		return invalidBuiltCorrection("reservation release cardinality changed", nil)
	}
	for index, intent := range plan.validation.unlocks {
		if !correctionReleaseLinkMatches(plan.releases[index], intent, command) {
			return invalidBuiltCorrection("reservation release link changed", nil)
		}
	}
	return nil
}

func correctionReleaseLinkMatches(release ReservationRelease, intent UnlockIntent, command Command) bool {
	return !intent.ExpectedUsed && !intent.ExpectedDisclosed &&
		!release.ExpectedUsed && !release.ExpectedDisclosed &&
		release.ReservationID == intent.ReservationID &&
		release.TournamentID == intent.TournamentID && release.OwnerID == intent.OwnerID &&
		release.SourceRevisionID == intent.SourceRevisionID &&
		release.ExpectedRevision == intent.ExpectedRevision &&
		release.ExpectedUsed == intent.ExpectedUsed &&
		release.ExpectedDisclosed == intent.ExpectedDisclosed &&
		release.NextRevision == intent.ExpectedRevision+1 && release.NextReleased &&
		release.EvidenceDigest == intent.EvidenceDigest &&
		release.ReleasedAt.Equal(command.RequestedAt)
}

func correctionReadinessLinkMatches(plan Plan, command Command) bool {
	if correctionReadinessIsEmpty(plan.validation.authority.Readiness) {
		return correctionReadinessIsEmpty(plan.readiness.Expected) &&
			correctionReadinessIsEmpty(plan.readiness.Next) && plan.readiness.ClosedAt.IsZero()
	}
	return correctionReadinessDigest(plan.readiness.Expected) ==
		correctionReadinessDigest(plan.validation.authority.Readiness) &&
		plan.readiness.Next.TournamentID == plan.readiness.Expected.TournamentID &&
		plan.readiness.Next.OwnerID == plan.readiness.Expected.OwnerID &&
		plan.readiness.Next.WaveID == plan.readiness.Expected.WaveID &&
		plan.readiness.Next.WindowID == plan.readiness.Expected.WindowID &&
		plan.readiness.Next.RevisionID == command.NextReadinessRevisionID &&
		plan.readiness.Next.Revision == plan.readiness.Expected.Revision+1 &&
		plan.readiness.Next.State == ReadinessClosed &&
		correctionParticipantsEqual(
			plan.readiness.Next.ParticipantIDs, plan.readiness.Expected.ParticipantIDs,
		) && plan.readiness.ClosedAt.Equal(command.RequestedAt)
}

func validateCorrectionResultLinks(plan Plan, command Command) error {
	gameRevision := plan.gameResult.Revision()
	if !correctionGameResultLinkMatches(plan, command, gameRevision) {
		return invalidBuiltCorrection("Game result revision link changed", nil)
	}
	scoreProjection, seriesProjection := correctionResultProjections(plan.projections)
	if !correctionSeriesResultLinksMatch(plan, command, scoreProjection, seriesProjection) {
		return invalidBuiltCorrection("score or Series revision link changed", nil)
	}
	return nil
}

func correctionGameResultLinkMatches(
	plan Plan,
	command Command,
	revision resultusecase.OfficialResultRevision,
) bool {
	return revision.ID() == command.NextResultRevisionID &&
		correctionOfficialResultRevisionIDPointersEqual(
			revision.PreviousRevisionID(), &plan.validation.authority.GameResult.ID,
		) && revision.CommandID() == command.CommandID &&
		correctionDerivedRevisionsEqual(revision.SourceProjection(), plan.projections[0].Revision()) &&
		correctionOfficialResultOutcomesEqual(revision.Outcome(), resultusecase.OfficialResultOutcome{
			GameState: command.Patch.State, GameReason: command.Patch.Reason,
			WinnerID: cloneCorrectionUUIDPointer(command.Patch.WinnerID),
		}) && revision.RecordedAt().Equal(command.RequestedAt)
}

func correctionResultProjections(
	projections []domain.ProjectionRevision,
) (domain.DerivedRevision, domain.DerivedRevision) {
	var scoreProjection, seriesProjection domain.DerivedRevision
	for _, projection := range projections {
		//nolint:exhaustive // Only the two correction source artifacts are selected here.
		switch projection.Revision().Artifact().Kind {
		case domain.ArtifactKindSeriesScore:
			scoreProjection = projection.Revision()
		case domain.ArtifactKindSeriesResult:
			seriesProjection = projection.Revision()
		default:
		}
	}
	return scoreProjection, seriesProjection
}

func correctionSeriesResultLinksMatch(
	plan Plan,
	command Command,
	scoreProjection domain.DerivedRevision,
	seriesProjection domain.DerivedRevision,
) bool {
	scoreRevision := plan.score.Revision()
	seriesRevision := plan.seriesResult.Revision()
	return scoreRevision.ID() == command.NextScoreRevisionID &&
		scoreRevision.CommandID() == command.CommandID &&
		scoreRevision.Operation() == resultusecase.SeriesScoreRevisionOperationReplaceResult &&
		correctionSeriesScoreRevisionIDPointersEqual(
			scoreRevision.PreviousRevisionID(), &plan.validation.authority.Score.ID,
		) && correctionDerivedRevisionsEqual(scoreRevision.SourceProjection(), scoreProjection) &&
		scoreRevision.RecordedAt().Equal(command.RequestedAt) &&
		seriesRevision.ID() == command.NextSeriesResultRevisionID &&
		seriesRevision.CommandID() == command.CascadeCommandID &&
		correctionOfficialResultRevisionIDPointersEqual(
			seriesRevision.PreviousRevisionID(), &plan.validation.authority.SeriesResult.ID,
		) && correctionDerivedRevisionsEqual(seriesRevision.SourceProjection(), seriesProjection) &&
		seriesRevision.Outcome().SeriesReason == domain.SeriesResultReasonOperatorCorrection &&
		seriesRevision.RecordedAt().Equal(command.RequestedAt)
}

func validateCorrectionDecisionLinks(plan Plan, command Command) error {
	decisionOffset := len(plan.decisions) - len(command.ProjectionIntents)
	if decisionOffset < 0 {
		return invalidBuiltCorrection("projection decision cardinality changed", nil)
	}
	for index, intent := range command.ProjectionIntents {
		if !correctionDecisionLinkMatches(plan.decisions[decisionOffset+index], intent, decisionOffset+index+1, command) {
			return invalidBuiltCorrection("projection decision link changed", nil)
		}
	}
	return nil
}

func correctionDecisionLinkMatches(
	decision resultprojection.RecordedProjectionDecision,
	intent ProjectionIntent,
	sequence int,
	command Command,
) bool {
	return decision.ID == intent.DecisionID && decision.Sequence == sequence &&
		decision.ProjectionRevisionID == intent.NextRevisionID &&
		decision.RecordedAt.Equal(command.RequestedAt) &&
		decision.PayloadDigest == intent.PayloadDigest && bytes.Equal(decision.Payload, intent.Payload)
}

func validateCorrectionProjectionLinks(plan Plan, command Command) error {
	for index, intent := range command.ProjectionIntents {
		revision := plan.projections[index].Revision()
		if !correctionProjectionLinkMatches(revision, intent) {
			return invalidBuiltCorrection("projection successor link changed", nil)
		}
		if index == 0 {
			continue
		}
		if !correctionSupersessionLinkMatches(plan.superseded[index-1], revision, intent) {
			return invalidBuiltCorrection("projection supersession link changed", nil)
		}
	}
	return nil
}

func correctionProjectionLinkMatches(revision domain.DerivedRevision, intent ProjectionIntent) bool {
	previous := revision.PreviousRevisionID()
	return previous != nil && *previous == intent.ExpectedRevision.ID() &&
		revision.ID() == intent.NextRevisionID &&
		revision.Artifact() == intent.ExpectedRevision.Artifact() &&
		revision.PayloadDigest() == intent.PayloadDigest
}

func correctionSupersessionLinkMatches(
	supersession ProjectionSupersession,
	revision domain.DerivedRevision,
	intent ProjectionIntent,
) bool {
	return !supersession.PreviousRevisionID.IsZero() && !supersession.SuccessorRevisionID.IsZero() &&
		supersession.ReplacementDecisionID != uuid.Nil &&
		supersession.Artifact == intent.ExpectedRevision.Artifact() &&
		supersession.PreviousRevisionID == intent.ExpectedRevision.ID() &&
		supersession.SuccessorRevisionID == revision.ID() &&
		supersession.ReplacementDecisionID == intent.DecisionID
}
