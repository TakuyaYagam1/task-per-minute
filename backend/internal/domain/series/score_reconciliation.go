package series

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func reconcileSeriesScoreProgression(
	current Execution,
	command ScoreProgressionCommand,
) (ScoreProgression, error) {
	series := current.Series
	if series.Score != command.ScoreRevision.ScoreAfter ||
		command.ScoreRevision.SeriesID != series.ID ||
		command.ScoreRevision.FirstParticipantID != series.FirstParticipantID ||
		command.ScoreRevision.SecondParticipantID != series.SecondParticipantID ||
		command.ScoreRevision.Format != series.Format ||
		!seriesContainsCompletedGame(series, command.Game) {
		return ScoreProgression{}, seriesScoreProgressionError("duplicate result does not match current Series")
	}
	winnerID := series.Score.Winner(
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
	)
	if err := validateReconciledSeriesRoute(current, command, winnerID); err != nil {
		return ScoreProgression{}, err
	}
	progression := ScoreProgression{
		Series: current, ScoreRevision: cloneSettlementScoreRevision(command.ScoreRevision),
	}
	if winnerID == nil {
		nextWave, err := reconciledNextSeriesWave(series, *command.Next)
		if err != nil {
			return ScoreProgression{}, err
		}
		progression.NextWave = &nextWave
	}
	if err := progression.Validate(); err != nil {
		return ScoreProgression{}, err
	}
	return cloneSeriesScoreProgression(progression), nil
}

func validateReconciledSeriesRoute(
	current Execution,
	command ScoreProgressionCommand,
	winnerID *uuid.UUID,
) error {
	if winnerID != nil {
		if command.Next != nil || command.TerminalResultRevisionID == nil ||
			current.Series.State != domain.SeriesStateCompleted || current.Series.WinnerID == nil ||
			*current.Series.WinnerID != *winnerID || current.Series.CurrentResultRevisionID == nil ||
			*current.Series.CurrentResultRevisionID != *command.TerminalResultRevisionID {
			return seriesScoreProgressionError("duplicate terminal result does not match current Series")
		}
		return validateProgressionIdentities(command, true)
	}
	if command.Next == nil || command.TerminalResultRevisionID != nil ||
		current.Series.State != domain.SeriesStateActive || current.Series.WinnerID != nil ||
		current.Series.CurrentResultRevisionID != nil {
		return seriesScoreProgressionError("duplicate continuing result does not match current Series")
	}
	return validateProgressionIdentities(command, false)
}

func reconciledNextSeriesWave(
	series domain.Series,
	next NextGameWave,
) (domain.Wave, error) {
	if len(series.Slots) < 2 {
		return domain.Wave{}, seriesScoreProgressionError("duplicate result has no next Game slot")
	}
	slot := series.Slots[len(series.Slots)-1]
	if slot.ID != next.SlotID || slot.Category != next.Category ||
		slot.ScoreBefore != series.Score || len(slot.Attempts) != 1 ||
		slot.Attempts[0].ID != next.GameID || slot.Attempts[0].State != domain.GameStatePlanned {
		return domain.Wave{}, seriesScoreProgressionError("duplicate result has a different next Game slot")
	}
	series.Slots = series.Slots[:len(series.Slots)-1]
	return appendNextSeriesGameWave(&series, next)
}
