package plan

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func buildAtomicCorrection(validation Validation) (Plan, error) {
	projections, byPrevious, err := buildCorrectionProjectionSuccessors(validation)
	if err != nil {
		return Plan{}, err
	}
	series, references, err := buildCorrectionSeries(validation)
	if err != nil {
		return Plan{}, err
	}
	gameResult, score, seriesResult, err := buildCorrectionRevisionPlans(
		validation, series, references, byPrevious,
	)
	if err != nil {
		return Plan{}, err
	}
	dag, err := buildCorrectionDAG(validation, projections, byPrevious, gameResult, score, seriesResult)
	if err != nil {
		return Plan{}, err
	}
	decisions, previousDecisions, err := buildCorrectionDecisions(validation, byPrevious)
	if err != nil {
		return Plan{}, err
	}
	rebuild, err := resultprojection.RebuildOfficialProjections(resultprojection.ProjectionRebuildInput{DAG: dag, Decisions: decisions})
	if err != nil {
		return Plan{}, invalidBuiltCorrection("rebuild corrected projections", err)
	}
	return Plan{
		validation: validation, cutoff: buildCorrectionCutoffCondition(validation),
		audit:      buildCorrectionAuditRecord(validation),
		gameResult: gameResult, score: score, seriesResult: seriesResult,
		series: series, solve: SolveTransition{
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

func buildCorrectionAuditRecord(validation Validation) AuditRecord {
	command := validation.command
	return AuditRecord{
		TournamentID: command.TournamentID, SeriesID: command.SeriesID, GameID: command.GameID,
		CommandID: command.CommandID, CascadeCommandID: command.CascadeCommandID,
		OperatorID: command.OperatorID, Confirmed: command.Confirmed, Reason: command.Reason,
		Explanation: command.Explanation, RequestedAt: command.RequestedAt,
		Fields:           append([]Field(nil), command.Fields...),
		ValidationDigest: validation.bindingDigest,
	}
}

func buildCorrectionCutoffCondition(validation Validation) CutoffCondition {
	affected := make([]domain.DerivedRevision, 0, len(validation.cutoff.Descendants())+1)
	affected = append(affected, validation.target)
	affected = append(affected, validation.cutoff.Descendants()...)
	return CutoffCondition{
		tournamentID:               validation.command.TournamentID,
		expectedTournamentState:    validation.authority.TournamentState,
		expectedTournamentRevision: validation.authority.TournamentRevision,
		target:                     validation.target,
		affected:                   affected,
		observedEventsDigest:       correctionCutoffEventSetDigest(validation.authority.CutoffEvents),
	}
}

func buildCorrectionProjectionSuccessors(
	validation Validation,
) ([]domain.ProjectionRevision, map[domain.DerivedRevisionID]domain.ProjectionRevision, error) {
	intents := validation.command.ProjectionIntents
	if len(intents) > maxCorrectionDAGProjections-len(validation.snapshot.Projections) {
		return nil, nil, rejectCorrection(
			RejectionMalformed, ErrInvalid, "correction exceeds projection bounds",
		)
	}
	payloadBytes := 0
	for _, projection := range validation.snapshot.Projections {
		size := len(projection.Payload())
		if size > maxCorrectionDAGPayloadBytes-payloadBytes {
			return nil, nil, rejectCorrection(
				RejectionMalformed, ErrInvalid, "correction exceeds DAG payload bounds",
			)
		}
		payloadBytes += size
	}
	for _, intent := range intents {
		if len(intent.Payload) > maxCorrectionDAGPayloadBytes-payloadBytes {
			return nil, nil, rejectCorrection(
				RejectionMalformed, ErrInvalid, "correction exceeds DAG payload bounds",
			)
		}
		payloadBytes += len(intent.Payload)
	}
	projections := make([]domain.ProjectionRevision, len(intents))
	byPrevious := make(map[domain.DerivedRevisionID]domain.ProjectionRevision, len(intents))
	for index, intent := range intents {
		previous := intent.ExpectedRevision.ID()
		projection, err := domain.NewProjectionRevision(
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
	validation Validation,
) (domain.Series, []resultusecase.SeriesScoreAttemptReference, error) {
	series := cloneCorrectionSeries(validation.authority.Series)
	game, found := findCorrectionSeriesGamePointer(&series, validation.command.GameID)
	if !found {
		return domain.Series{}, nil, rejectCorrection(
			RejectionStale, ErrInvalid, "corrected Game disappeared",
		)
	}
	game.State = validation.command.Patch.State
	game.ResultReason = validation.command.Patch.Reason
	game.WinnerID = cloneCorrectionUUIDPointer(validation.command.Patch.WinnerID)
	gameResultID := validation.command.NextResultRevisionID
	game.ResultRevisionID = &gameResultID
	references, err := correctionSeriesScoreAttemptReferencesFromSeries(series)
	if err != nil {
		return domain.Series{}, nil, invalidBuiltCorrection("derive corrected score attempts", err)
	}
	score, err := correctionScoreFromAttemptReferences(
		references, series.FirstParticipantID, series.SecondParticipantID, series.Format,
	)
	if err != nil {
		return domain.Series{}, nil, invalidBuiltCorrection("derive corrected score", err)
	}
	winner := score.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format)
	if series.State == domain.SeriesStateCompleted && winner == nil {
		return domain.Series{}, nil, rejectCorrection(
			RejectionTerminal, ErrInvalid, "corrected Series is not terminal",
		)
	}
	series.Score = score
	if series.State == domain.SeriesStateCompleted {
		series.WinnerID = winner
	} else {
		series.WinnerID = nil
	}
	scoreID := validation.command.NextScoreRevisionID
	resultID := validation.command.NextSeriesResultRevisionID
	series.CurrentScoreRevisionID = &scoreID
	series.CurrentResultRevisionID = &resultID
	if err := series.Validate(); err != nil {
		return domain.Series{}, nil, invalidBuiltCorrection("invalid corrected Series", err)
	}
	return series, references, nil
}

func buildCorrectionRevisionPlans(
	validation Validation,
	series domain.Series,
	references []resultusecase.SeriesScoreAttemptReference,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
) (resultusecase.OfficialResultRevisionPlan, resultusecase.SeriesScoreRevisionPlan, resultusecase.OfficialResultRevisionPlan, error) {
	command := validation.command
	authority := validation.authority
	actorID := command.OperatorID
	actor := domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: &actorID}
	gameProjection := byPrevious[command.Expected.TargetProjection.ID()].Revision()
	scoreProjection := byPrevious[command.Expected.ScoreProjection.ID()].Revision()
	seriesProjection := byPrevious[command.Expected.SeriesProjection.ID()].Revision()
	gameCurrentID := authority.GameResult.ID
	gamePlan, err := resultusecase.PlanOfficialResultRevision(resultusecase.OfficialResultRevisionCommand{
		Scope: authority.GameResult.Scope, CommandID: command.CommandID,
		RevisionID: command.NextResultRevisionID, Actor: actor,
		ExpectedCurrentRevisionID: &gameCurrentID, ExpectedSourceProjection: gameProjection,
		Outcome: resultusecase.OfficialResultOutcome{
			GameState: command.Patch.State, GameReason: command.Patch.Reason,
			WinnerID: cloneCorrectionUUIDPointer(command.Patch.WinnerID),
		},
	}, resultusecase.OfficialResultRevisionAuthority{
		Scope: authority.GameResult.Scope, PersistedSeries: authority.Series,
		ProjectedSeries: series, SourceProjection: gameProjection,
		CurrentHead: &authority.GameResult, SeriesRevision: authority.SeriesRevision,
		AttemptRevision: authority.AttemptRevision,
	}, command.RequestedAt)
	if err != nil {
		return resultusecase.OfficialResultRevisionPlan{}, resultusecase.SeriesScoreRevisionPlan{}, resultusecase.OfficialResultRevisionPlan{},
			invalidBuiltCorrection("plan Game result successor", err)
	}
	var commandAttempt *resultusecase.SeriesScoreAttemptReference
	for index := range references {
		if references[index].GameID == command.GameID {
			attempt := cloneCorrectionSeriesScoreAttemptReference(references[index])
			commandAttempt = &attempt
			break
		}
	}
	if commandAttempt == nil {
		return resultusecase.OfficialResultRevisionPlan{}, resultusecase.SeriesScoreRevisionPlan{}, resultusecase.OfficialResultRevisionPlan{},
			invalidBuiltCorrection("corrected score attempt is missing", nil)
	}
	scoreCurrentID := authority.Score.ID
	scorePlan, err := resultusecase.PlanSeriesScoreRevision(resultusecase.SeriesScoreRevisionCommand{
		Scope: authority.Score.Scope, Operation: resultusecase.SeriesScoreRevisionOperationReplaceResult,
		CommandID: command.CommandID, RevisionID: command.NextScoreRevisionID, Actor: actor,
		ExpectedCurrentRevisionID: &scoreCurrentID, ExpectedSourceProjection: scoreProjection,
		Attempt: commandAttempt,
	}, resultusecase.SeriesScoreRevisionAuthority{
		Scope: authority.Score.Scope, PersistedSeries: authority.Series,
		ProjectedSeries: series, SourceProjection: scoreProjection,
		CurrentHead: &authority.Score, SeriesRevision: authority.SeriesRevision,
		AttemptRevision: authority.AttemptRevision,
	}, command.RequestedAt)
	if err != nil {
		return resultusecase.OfficialResultRevisionPlan{}, resultusecase.SeriesScoreRevisionPlan{}, resultusecase.OfficialResultRevisionPlan{},
			invalidBuiltCorrection("plan score successor", err)
	}
	seriesCurrentID := authority.SeriesResult.ID
	seriesPlan, err := resultusecase.PlanOfficialResultRevision(resultusecase.OfficialResultRevisionCommand{
		Scope: authority.SeriesResult.Scope, CommandID: command.CascadeCommandID,
		RevisionID: command.NextSeriesResultRevisionID, Actor: actor,
		ExpectedCurrentRevisionID: &seriesCurrentID, ExpectedSourceProjection: seriesProjection,
		Outcome: resultusecase.OfficialResultOutcome{
			SeriesState:     series.State,
			SeriesReason:    domain.SeriesResultReasonOperatorCorrection,
			WinnerID:        cloneCorrectionUUIDPointer(series.WinnerID),
			ScoreRevisionID: cloneCorrectionSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID),
		},
	}, resultusecase.OfficialResultRevisionAuthority{
		Scope: authority.SeriesResult.Scope, PersistedSeries: authority.Series,
		ProjectedSeries: series, ProjectedSeriesReason: domain.SeriesResultReasonOperatorCorrection,
		SourceProjection: seriesProjection, CurrentHead: &authority.SeriesResult,
		SeriesRevision: authority.SeriesRevision,
	}, command.RequestedAt)
	if err != nil {
		return resultusecase.OfficialResultRevisionPlan{}, resultusecase.SeriesScoreRevisionPlan{}, resultusecase.OfficialResultRevisionPlan{},
			invalidBuiltCorrection("plan Series result successor", err)
	}
	return gamePlan, scorePlan, seriesPlan, nil
}

func buildCorrectionDAG(
	validation Validation,
	projections []domain.ProjectionRevision,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
	gameResult resultusecase.OfficialResultRevisionPlan,
	score resultusecase.SeriesScoreRevisionPlan,
	seriesResult resultusecase.OfficialResultRevisionPlan,
) (resultprojection.RevisionDAG, error) {
	snapshot := validation.snapshot
	artifacts := correctionDAGArtifacts(snapshot, projections)
	projectionCount := len(snapshot.Projections) + len(projections)
	dependencyCount := correctionDAGDependencyCount(snapshot, projections, byPrevious, artifacts)
	if projectionCount > maxCorrectionDAGProjections || dependencyCount > maxCorrectionDAGDependencies {
		return resultprojection.RevisionDAG{}, rejectCorrection(
			RejectionMalformed, ErrInvalid, "correction exceeds DAG bounds",
		)
	}
	graphProjections := make([]domain.ProjectionRevision, 0, projectionCount)
	graphProjections = append(graphProjections, snapshot.Projections...)
	graphProjections = append(graphProjections, projections...)
	dependencies := correctionDAGDependencies(snapshot, byPrevious, artifacts)
	graph, err := domain.NewRevisionGraph(graphProjections, dependencies)
	if err != nil {
		return resultprojection.RevisionDAG{}, invalidBuiltCorrection("build corrected graph", err)
	}
	inputs, err := correctionDAGInputs(validation, byPrevious, gameResult, score, seriesResult)
	if err != nil {
		return resultprojection.RevisionDAG{}, err
	}
	dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{Graph: graph, Results: inputs})
	if err != nil {
		return resultprojection.RevisionDAG{}, invalidBuiltCorrection("build corrected revision DAG", err)
	}
	return dag, nil
}

func correctionDAGArtifacts(
	snapshot resultprojection.RevisionDAGSnapshot,
	projections []domain.ProjectionRevision,
) map[domain.DerivedRevisionID]domain.ArtifactRef {
	artifacts := make(map[domain.DerivedRevisionID]domain.ArtifactRef, len(snapshot.Projections)+len(projections))
	for _, projection := range snapshot.Projections {
		artifacts[projection.Revision().ID()] = projection.Revision().Artifact()
	}
	for _, projection := range projections {
		artifacts[projection.Revision().ID()] = projection.Revision().Artifact()
	}
	return artifacts
}

func correctionDAGDependencyCount(
	snapshot resultprojection.RevisionDAGSnapshot,
	projections []domain.ProjectionRevision,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
	artifacts map[domain.DerivedRevisionID]domain.ArtifactRef,
) int {
	count := len(snapshot.Dependencies) + len(projections)
	for _, dependency := range snapshot.Dependencies {
		if _, affected := byPrevious[dependency.DerivedRevisionID]; affected &&
			artifacts[dependency.SourceRevisionID] != artifacts[dependency.DerivedRevisionID] {
			count++
		}
	}
	return count
}

func correctionDAGDependencies(
	snapshot resultprojection.RevisionDAGSnapshot,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
	artifacts map[domain.DerivedRevisionID]domain.ArtifactRef,
) []domain.RevisionDependency {
	dependencies := append([]domain.RevisionDependency(nil), snapshot.Dependencies...)
	for previousID, projection := range byPrevious {
		dependencies = append(dependencies, domain.RevisionDependency{
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
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID: sourceID, DerivedRevisionID: derived.Revision().ID(),
		})
	}
	return dependencies
}

func correctionDAGInputs(
	validation Validation,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
	gameResult resultusecase.OfficialResultRevisionPlan,
	score resultusecase.SeriesScoreRevisionPlan,
	seriesResult resultusecase.OfficialResultRevisionPlan,
) ([]resultprojection.OfficialResultProjectionInput, error) {
	inputs := validation.authority.DAG.Inputs()
	if len(inputs) == 0 {
		return nil, invalidBuiltCorrection("read revision DAG inputs", nil)
	}
	for index := range inputs {
		input := inputs[index]
		switch input.Result.Scope {
		case validation.authority.GameResult.Scope:
			input = resultprojection.OfficialResultProjectionInput{
				TerminalSource:   resultprojection.TerminalResultSourcePlayed,
				Result:           gameResult.Revision().Head(),
				ResultProjection: byPrevious[validation.command.Expected.TargetProjection.ID()],
			}
		case validation.authority.SeriesResult.Scope:
			scoreHead := score.Revision().Head()
			scoreProjection := byPrevious[validation.command.Expected.ScoreProjection.ID()]
			input = resultprojection.OfficialResultProjectionInput{
				TerminalSource:   resultprojection.TerminalResultSourcePlayed,
				Result:           seriesResult.Revision().Head(),
				ResultProjection: byPrevious[validation.command.Expected.SeriesProjection.ID()],
				Score:            &scoreHead, ScoreProjection: &scoreProjection,
			}
		}
		inputs[index] = input
	}
	return inputs, nil
}

func buildCorrectionDecisions(
	validation Validation,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
) ([]resultprojection.RecordedProjectionDecision, map[domain.DerivedRevisionID]uuid.UUID, error) {
	old := canonicalCorrectionDecisions(validation.authority.Decisions)
	affected := make(map[domain.DerivedRevisionID]struct{}, len(byPrevious))
	for id := range byPrevious {
		affected[id] = struct{}{}
	}
	firstAffected := len(old)
	previous := make(map[domain.DerivedRevisionID]uuid.UUID)
	for index, decision := range old {
		if _, isAffected := affected[decision.ProjectionRevisionID]; isAffected {
			previous[decision.ProjectionRevisionID] = decision.ID
			if firstAffected == len(old) {
				firstAffected = index
			}
		} else if firstAffected != len(old) {
			return nil, nil, rejectCorrection(
				RejectionIncomplete, ErrInvalid,
				"affected projection decisions are not a causal suffix",
			)
		}
	}
	if len(validation.command.ProjectionIntents) > maxCorrectionProjectionDecisions-firstAffected {
		return nil, nil, rejectCorrection(
			RejectionMalformed, ErrInvalid, "correction exceeds decision bounds",
		)
	}
	decisions := cloneCorrectionRecordedProjectionDecisions(old[:firstAffected])
	for index, intent := range validation.command.ProjectionIntents {
		successor := byPrevious[intent.ExpectedRevision.ID()].Revision()
		decisions = append(decisions, resultprojection.RecordedProjectionDecision{
			ID: intent.DecisionID, Sequence: firstAffected + index + 1,
			ProjectionRevisionID: successor.ID(), RecordedAt: validation.command.RequestedAt,
			Payload: append([]byte(nil), intent.Payload...), PayloadDigest: intent.PayloadDigest,
		})
	}
	return decisions, previous, nil
}

func buildCorrectionSupersessions(
	validation Validation,
	byPrevious map[domain.DerivedRevisionID]domain.ProjectionRevision,
	previousDecisions map[domain.DerivedRevisionID]uuid.UUID,
) []ProjectionSupersession {
	intents := validation.command.ProjectionIntents[1:]
	result := make([]ProjectionSupersession, len(intents))
	for index, intent := range intents {
		descendant := intent.ExpectedRevision
		var previousDecision *uuid.UUID
		if id, exists := previousDecisions[descendant.ID()]; exists {
			value := id
			previousDecision = &value
		}
		result[index] = ProjectionSupersession{
			Artifact: descendant.Artifact(), PreviousRevisionID: descendant.ID(),
			SuccessorRevisionID:   byPrevious[descendant.ID()].Revision().ID(),
			PreviousDecisionID:    previousDecision,
			ReplacementDecisionID: intent.DecisionID,
		}
	}
	return result
}

func buildCorrectionReadinessTransition(
	validation Validation,
) ReadinessTransition {
	expected := cloneCorrectionReadiness(validation.authority.Readiness)
	if correctionReadinessIsEmpty(expected) {
		return ReadinessTransition{}
	}
	next := cloneCorrectionReadiness(expected)
	next.RevisionID = validation.command.NextReadinessRevisionID
	next.Revision++
	next.State = ReadinessClosed
	return ReadinessTransition{
		Expected: expected, Next: next, ClosedAt: validation.command.RequestedAt,
	}
}

func buildCorrectionReleases(validation Validation) []ReservationRelease {
	releases := make([]ReservationRelease, len(validation.unlocks))
	for index, intent := range validation.unlocks {
		releases[index] = ReservationRelease{
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
