package game

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func replayReserveExhaustionsEqual(first, second ReplayReserveExhaustion) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.ClosureRevisionID == second.ClosureRevisionID &&
		first.FailedAttemptCommandID == second.FailedAttemptCommandID &&
		first.AssignmentAttemptID == second.AssignmentAttemptID && first.GameID == second.GameID &&
		first.ActiveSnapshotID == second.ActiveSnapshotID &&
		first.ReservePosition == second.ReservePosition && first.Category == second.Category &&
		replaySeriesExecutionsEqual(first.PreviousSeries, second.PreviousSeries) &&
		replaySeriesExecutionsEqual(first.Series, second.Series) &&
		replayWavesEqual(first.OldWave, second.OldWave)
}

func replaySeriesExecutionsEqual(first, second seriesdomain.Execution) bool {
	if first.ResumeState == nil || second.ResumeState == nil {
		if first.ResumeState != second.ResumeState {
			return false
		}
	} else if *first.ResumeState != *second.ResumeState {
		return false
	}
	if !replaySeriesHeadersEqual(first.Series, second.Series) ||
		len(first.Series.Slots) != len(second.Series.Slots) {
		return false
	}
	for index := range first.Series.Slots {
		if !replayGameSlotsEqual(first.Series.Slots[index], second.Series.Slots[index]) {
			return false
		}
	}
	return true
}

func replaySeriesHeadersEqual(first, second domain.Series) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID && first.Format == second.Format &&
		first.State == second.State && first.Score == second.Score &&
		replayUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		replaySeriesScoreRevisionPointersEqual(
			first.CurrentScoreRevisionID,
			second.CurrentScoreRevisionID,
		) && replayResultRevisionPointersEqual(
		first.CurrentResultRevisionID,
		second.CurrentResultRevisionID,
	)
}

func replaySeriesScoreRevisionPointersEqual(
	first *domain.SeriesScoreRevisionID,
	second *domain.SeriesScoreRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func cloneReplayReserveExhaustion(record ReplayReserveExhaustion) ReplayReserveExhaustion {
	clone := record
	clone.PreviousSeries = cloneSeriesExecution(record.PreviousSeries)
	clone.Series = cloneSeriesExecution(record.Series)
	clone.OldWave = replayCloneWaveExecution(record.OldWave)
	return clone
}
