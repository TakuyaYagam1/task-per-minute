package game_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func forfeitNewGameClock(t *testing.T, now time.Time) *gamemocks.MockForfeitClock {
	t.Helper()

	clock := gamemocks.NewMockForfeitClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func forfeitID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-" + fmt.Sprintf("%012d", value))
}

func seriesExecutionFixture(
	format domain.SeriesFormat,
	seriesState domain.SeriesState,
	gameState domain.GameState,
) seriesdomain.Execution {
	seriesID := forfeitID(1)
	firstParticipantID := forfeitID(2)
	secondParticipantID := forfeitID(3)
	scoreRevisionID := domain.SeriesScoreRevisionID(forfeitID(4))
	series := domain.Series{
		ID: seriesID, TournamentID: forfeitID(5),
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: format, State: seriesState, CurrentScoreRevisionID: &scoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: forfeitID(6), SeriesID: seriesID, Position: 1,
			Category: domain.CategoryWeb, ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: forfeitID(7), SlotID: forfeitID(6), AttemptNo: 1, State: gameState,
			}},
		}},
	}
	if gameState == domain.GameStateVoid {
		resultRevisionID := domain.OfficialResultRevisionID(forfeitID(8))
		series.Slots[0].Attempts[0].ResultReason = domain.GameResultReasonNoSolve
		series.Slots[0].Attempts[0].ResultRevisionID = &resultRevisionID
	}
	return seriesdomain.Execution{Series: series}
}

func forfeitCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
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
