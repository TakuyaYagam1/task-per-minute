package settlement

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func validCommittedConcurrentWinner(
	committed *SettlementRecord,
	proposed SettlementRecord,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return concurrentWinnerSettlementsEqual(*committed, proposed)
}

func concurrentWinnerSettlementsEqual(
	first SettlementRecord,
	second SettlementRecord,
) bool {
	return first.Scope == second.Scope &&
		first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.WinningSubmission == second.WinningSubmission &&
		first.EffectiveSolveTime == second.EffectiveSolveTime &&
		settlementSettlementGamesEqual(first.Game, second.Game) &&
		gameResultRevisionsEqual(first.SettlementGameResultRevision, second.SettlementGameResultRevision) &&
		seriesdomain.ScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		seriesdomain.ExecutionsEqual(
			seriesdomain.Execution{Series: first.Series},
			seriesdomain.Execution{Series: second.Series},
		) &&
		first.Evidence == second.Evidence && first.SettledAt.Equal(second.SettledAt)
}

func gameResultRevisionsEqual(
	first SettlementGameResultRevision,
	second SettlementGameResultRevision,
) bool {
	return first.ID == second.ID && first.GameID == second.GameID &&
		settlementOfficialResultRevisionPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.WinnerID == second.WinnerID && first.Reason == second.Reason &&
		first.WinningSubmissionSequence == second.WinningSubmissionSequence &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func startedParticipant(started gamedomain.Started, participantID uuid.UUID) bool {
	return started.ParticipantIDs[0] == participantID || started.ParticipantIDs[1] == participantID
}

func cloneConcurrentWinnerSettlement(
	settlement SettlementRecord,
) SettlementRecord {
	cloned := settlement
	cloned.Game = settlementCloneGame(settlement.Game)
	cloned.SettlementGameResultRevision.PreviousRevisionID = settlementCloneOfficialResultRevisionIDPointer(
		settlement.SettlementGameResultRevision.PreviousRevisionID,
	)
	cloned.ScoreRevision.PreviousRevisionID = settlementCloneSeriesScoreRevisionIDPointer(
		settlement.ScoreRevision.PreviousRevisionID,
	)
	cloned.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		settlement.ScoreRevision.GameResultRevisionIDs...,
	)
	cloned.Series = seriesdomain.CloneExecution(seriesdomain.Execution{Series: settlement.Series}).Series
	return cloned
}
