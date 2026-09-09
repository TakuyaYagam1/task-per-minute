package game

import (
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func reconcileForfeitResolution(
	resolution ForfeitResolution,
	request forfeitRequest,
) (*ForfeitResolution, error) {
	if resolution.Validate() != nil {
		return nil, domain.ErrInternal
	}
	if resolution.CommandID != request.commandID {
		return nil, ErrForfeitAuthorityConflict
	}
	if resolution.Source != request.source || resolution.Scope != request.scope ||
		resolution.ActorID != request.actorID ||
		resolution.ForfeitingParticipantID != request.forfeitingParticipantID ||
		resolution.ScoreRevision.ID != request.revisions.ScoreRevisionID ||
		resolution.SeriesRevision.ID != request.revisions.SeriesResultRevisionID ||
		resolution.Evidence.AuditEventID != request.revisions.AuditEventID ||
		resolution.Evidence.OutboxEventID != request.revisions.OutboxEventID ||
		resolution.Evidence.ProjectionRevisionID != request.revisions.ProjectionRevisionID ||
		!forfeitGameExpectationsEqual(resolution.ExpectedGame, request.expectedGame) ||
		!forfeitOfficialResultRevisionPointersEqual(
			forfeitGameRevisionIDPointer(resolution.GameRevision),
			request.revisions.GameResultRevisionID,
		) || !operatorForfeitEvidencePointersEqual(resolution.OperatorEvidence, request.operatorEvidence) {
		return nil, ErrForfeitCommandReuse
	}
	clone := cloneForfeitResolution(resolution)
	return &clone, nil
}

func validCommittedForfeitResolution(
	committed *ForfeitResolution,
	proposed ForfeitResolution,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope ||
		committed.CommandID != proposed.CommandID {
		return false
	}
	return !changed || forfeitResolutionsEqual(*committed, proposed)
}

func forfeitResolutionsEqual(first, second ForfeitResolution) bool {
	return forfeitResolutionHeadersEqual(first, second) &&
		forfeitSeriesHeadsEqual(first.Series, second.Series) &&
		forfeitGamesEqual(first.Game, second.Game) &&
		forfeitGameRevisionsEqual(first.GameRevision, second.GameRevision) &&
		seriesdomain.ScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		forfeitSeriesRevisionsEqual(first.SeriesRevision, second.SeriesRevision) &&
		operatorForfeitEvidencePointersEqual(first.OperatorEvidence, second.OperatorEvidence) &&
		first.Evidence == second.Evidence && first.ResolvedAt.Equal(second.ResolvedAt)
}

func forfeitResolutionHeadersEqual(first, second ForfeitResolution) bool {
	return first.Source == second.Source && first.Reason == second.Reason && first.Scope == second.Scope &&
		first.CommandID == second.CommandID && first.ActorID == second.ActorID &&
		first.ForfeitingParticipantID == second.ForfeitingParticipantID &&
		forfeitGameExpectationsEqual(first.ExpectedGame, second.ExpectedGame) &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision
}

func forfeitSeriesHeadsEqual(first, second seriesdomain.Execution) bool {
	return seriesdomain.ExecutionsEqual(first, second)
}

func forfeitGamesEqual(first, second *domain.Game) bool {
	if first == nil || second == nil {
		return first == second
	}
	return forfeitSettlementGamesEqual(*first, *second)
}

func forfeitGameRevisionsEqual(first, second *GameRevision) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func forfeitSeriesRevisionsEqual(first, second SeriesRevision) bool {
	return first.Ordinal == second.Ordinal && first.ID == second.ID && first.SeriesID == second.SeriesID &&
		forfeitOfficialResultRevisionPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.State == second.State && forfeitUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		first.ScoreRevisionID == second.ScoreRevisionID && first.Reason == second.Reason &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func operatorForfeitEvidencePointersEqual(
	first *OperatorEvidence,
	second *OperatorEvidence,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Confirmed == second.Confirmed && first.Basis == second.Basis &&
		first.Reason == second.Reason && first.RuleID == second.RuleID &&
		slices.Equal(first.EvidenceIDs, second.EvidenceIDs)
}

func forfeitGameRevisionIDPointer(
	revision *GameRevision,
) *domain.OfficialResultRevisionID {
	if revision == nil {
		return nil
	}
	result := revision.ID
	return &result
}

func forfeitCompletedGameMatchesExpectation(
	game domain.Game,
	expected *GameExpectation,
) bool {
	if expected == nil {
		return false
	}
	return game.ID == expected.GameID && game.SlotID == expected.SlotID &&
		game.AttemptNo == expected.AttemptNo && liveForfeitGameState(expected.State)
}

func forfeitGameExpectationsEqual(first, second *GameExpectation) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func cloneForfeitResolution(resolution ForfeitResolution) ForfeitResolution {
	clone := resolution
	clone.ExpectedGame = cloneForfeitGameExpectation(resolution.ExpectedGame)
	clone.Series = seriesdomain.CloneExecution(resolution.Series)
	if resolution.Game != nil {
		game := forfeitCloneGame(*resolution.Game)
		clone.Game = &game
	}
	if resolution.GameRevision != nil {
		gameRevision := *resolution.GameRevision
		clone.GameRevision = &gameRevision
	}
	clone.ScoreRevision = cloneScoreRevision(resolution.ScoreRevision)
	clone.SeriesRevision.PreviousRevisionID = forfeitCloneOfficialResultRevisionIDPointer(
		resolution.SeriesRevision.PreviousRevisionID,
	)
	clone.SeriesRevision.WinnerID = forfeitCloneUUIDPointer(resolution.SeriesRevision.WinnerID)
	clone.OperatorEvidence = cloneOperatorForfeitEvidencePointer(resolution.OperatorEvidence)
	return clone
}

func cloneOperatorForfeitEvidence(evidence OperatorEvidence) OperatorEvidence {
	clone := evidence
	clone.EvidenceIDs = append([]uuid.UUID(nil), evidence.EvidenceIDs...)
	return clone
}

func cloneOperatorForfeitEvidencePointer(
	evidence *OperatorEvidence,
) *OperatorEvidence {
	if evidence == nil {
		return nil
	}
	clone := cloneOperatorForfeitEvidence(*evidence)
	return &clone
}

func cloneForfeitGameExpectation(
	expected *GameExpectation,
) *GameExpectation {
	if expected == nil {
		return nil
	}
	clone := *expected
	return &clone
}
