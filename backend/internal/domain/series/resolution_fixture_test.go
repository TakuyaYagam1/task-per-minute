package series_test

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	game "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func seriesExecutionFixture(
	format domain.SeriesFormat,
	seriesState domain.SeriesState,
	gameState domain.GameState,
) game.Execution {
	seriesID := seriesFixtureID(1)
	firstParticipantID := seriesFixtureID(2)
	secondParticipantID := seriesFixtureID(3)
	scoreRevisionID := domain.SeriesScoreRevisionID(seriesFixtureID(4))
	series := domain.Series{
		ID: seriesID, TournamentID: seriesFixtureID(5),
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: format, State: seriesState, CurrentScoreRevisionID: &scoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: seriesFixtureID(6), SeriesID: seriesID, Position: 1,
			Category: domain.CategoryWeb, ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: seriesFixtureID(7), SlotID: seriesFixtureID(6), AttemptNo: 1, State: gameState,
			}},
		}},
	}
	if gameState == domain.GameStateVoid {
		resultRevisionID := domain.OfficialResultRevisionID(seriesFixtureID(8))
		series.Slots[0].Attempts[0].ResultReason = domain.GameResultReasonNoSolve
		series.Slots[0].Attempts[0].ResultRevisionID = &resultRevisionID
	}
	return game.Execution{Series: series}
}

func completeCurrentGame(
	execution game.Execution,
	winnerID uuid.UUID,
	reason domain.GameResultReason,
	resultRevisionID domain.OfficialResultRevisionID,
) game.Execution {
	cloned := cloneSeriesExecution(execution)
	slotIndex := len(cloned.Series.Slots) - 1
	gameIndex := len(cloned.Series.Slots[slotIndex].Attempts) - 1
	current := &cloned.Series.Slots[slotIndex].Attempts[gameIndex]
	current.State = domain.GameStateCompleted
	current.ResultReason = reason
	current.WinnerID = &winnerID
	current.ResultRevisionID = &resultRevisionID
	return cloned
}

func cloneSeriesExecution(execution game.Execution) game.Execution {
	cloned := execution
	if execution.ResumeState != nil {
		resumeState := *execution.ResumeState
		cloned.ResumeState = &resumeState
	}
	cloned.Series.WinnerID = cloneUUIDPointer(execution.Series.WinnerID)
	cloned.Series.CurrentScoreRevisionID = cloneScoreRevisionPointer(
		execution.Series.CurrentScoreRevisionID,
	)
	cloned.Series.CurrentResultRevisionID = cloneResultRevisionPointer(
		execution.Series.CurrentResultRevisionID,
	)
	cloned.Series.Slots = make([]domain.GameSlot, len(execution.Series.Slots))
	for slotIndex, slot := range execution.Series.Slots {
		cloned.Series.Slots[slotIndex] = slot
		cloned.Series.Slots[slotIndex].Attempts = make([]domain.Game, len(slot.Attempts))
		for gameIndex, current := range slot.Attempts {
			cloned.Series.Slots[slotIndex].Attempts[gameIndex] = current
			clonedCurrent := &cloned.Series.Slots[slotIndex].Attempts[gameIndex]
			clonedCurrent.WinnerID = cloneUUIDPointer(current.WinnerID)
			clonedCurrent.ResultRevisionID = cloneResultRevisionPointer(current.ResultRevisionID)
		}
	}
	return cloned
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneScoreRevisionPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneResultRevisionPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func seriesFixtureID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "task-039-%d", value))
}
