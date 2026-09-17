package resumepresence

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func pauseResumeGraphEvidenceEqual(first, second PauseGraph) bool {
	return first.Scope == second.Scope && pauseGraphRevisionsEqual(PauseGraphRevisionsFrom(first), PauseGraphRevisionsFrom(second)) &&
		pauseResumeTournamentRecordEqual(first.Tournament, second.Tournament) &&
		pauseResumeWaveEqual(first.Wave, second.Wave) && pauseResumeSeriesSliceEqual(first.Series, second.Series) &&
		pauseResumeGameSliceEqual(first.Games, second.Games) && pauseResumeDraftPointerEqual(first.Draft, second.Draft) &&
		pauseResumePresenceSetEqual(first.Presence, second.Presence) &&
		pauseResumeReconnectSetEqual(first.Reconnect, second.Reconnect) && pauseResumeCounterSetEqual(first.Counters, second.Counters) &&
		pauseResumeFrozenBaselineEqual(first.FrozenDeadlines, second.FrozenDeadlines) && first.ActivePauseID == second.ActivePauseID &&
		pauseResumeTimePointerEqual(first.PausedAt, second.PausedAt) && first.DeadlinesSuppressed == second.DeadlinesSuppressed &&
		first.TerminalActionRevision == second.TerminalActionRevision
}

func pauseResumeTournamentRecordEqual(first, second TournamentRecord) bool {
	return first.ID == second.ID && first.RosterID == second.RosterID && first.Preset == second.Preset && first.State == second.State &&
		pauseResumeTournamentStatePointerEqual(first.PausedFromState, second.PausedFromState) && first.Revision == second.Revision &&
		first.RosterSize == second.RosterSize && first.CreatedAt.Equal(second.CreatedAt) && first.UpdatedAt.Equal(second.UpdatedAt) &&
		pauseResumeTimePointerEqual(first.StartedAt, second.StartedAt) && pauseResumeTimePointerEqual(first.FinishedAt, second.FinishedAt)
}

func pauseResumeWaveEqual(first, second PauseWave) bool {
	return first.Revision == second.Revision && pauseResumeDomainWaveEqual(first.Wave, second.Wave)
}

func pauseResumeDomainWaveEqual(first, second domain.Wave) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RevisionID == second.RevisionID &&
		first.State == second.State && pauseResumeComparableSliceEqual(first.Members, second.Members) &&
		pauseResumeReadyWindowPointerEqual(first.ReadyWindow, second.ReadyWindow) &&
		pauseResumeTimePointerEqual(first.StartedAt, second.StartedAt) && pauseResumeTimePointerEqual(first.PausedAt, second.PausedAt)
}

func pauseResumeReadyWindowPointerEqual(first, second *domain.ReadyWindow) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.ID == second.ID && first.WaveID == second.WaveID && first.RevisionID == second.RevisionID &&
		first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		pauseResumeTimePointerEqual(first.ConsumedAt, second.ConsumedAt)
}

func pauseResumeSeriesSliceEqual(first, second []PauseSeries) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeSeriesEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeSeriesEqual(first, second PauseSeries) bool {
	return first.Revision == second.Revision && pauseResumeUUIDPointerEqual(first.CurrentGameID, second.CurrentGameID) &&
		pauseResumeSeriesExecutionEqual(first.Execution, second.Execution)
}

func pauseResumeSeriesExecutionEqual(first, second seriesdomain.Execution) bool {
	return pauseResumeDomainSeriesEqual(first.Series, second.Series) &&
		pauseResumeComparablePointerEqual(first.ResumeState, second.ResumeState)
}

func pauseResumeDomainSeriesEqual(first, second domain.Series) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID &&
		first.FirstParticipantID == second.FirstParticipantID && first.SecondParticipantID == second.SecondParticipantID &&
		first.Format == second.Format && first.State == second.State && first.Score == second.Score &&
		pauseResumeUUIDPointerEqual(first.WinnerID, second.WinnerID) && pauseResumeGameSlotSliceEqual(first.Slots, second.Slots) &&
		pauseResumeComparablePointerEqual(first.CurrentScoreRevisionID, second.CurrentScoreRevisionID) &&
		pauseResumeComparablePointerEqual(first.CurrentResultRevisionID, second.CurrentResultRevisionID)
}

func pauseResumeGameSlotSliceEqual(first, second []domain.GameSlot) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeGameSlotEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeGameSlotEqual(first, second domain.GameSlot) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID && first.Position == second.Position &&
		first.Category == second.Category && first.ScoreBefore == second.ScoreBefore &&
		pauseResumeDomainGameSliceEqual(first.Attempts, second.Attempts)
}

func pauseResumeDomainGameSliceEqual(first, second []domain.Game) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeDomainGameEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeDomainGameEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && first.ResultReason == second.ResultReason &&
		pauseResumeUUIDPointerEqual(first.WinnerID, second.WinnerID) &&
		pauseResumeComparablePointerEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func pauseResumeGameSliceEqual(first, second []PauseGame) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !pauseResumeGameEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func pauseResumeGameEqual(first, second PauseGame) bool {
	return first.SeriesID == second.SeriesID && pauseResumeDomainGameEqual(first.Game, second.Game) &&
		first.Revision == second.Revision && pauseResumeTimePointerEqual(first.Deadline, second.Deadline) &&
		pauseResumeComparablePointerEqual(first.ResumeState, second.ResumeState)
}
