package correction

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func findCorrectionSeriesGame(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	game, found := findCorrectionSeriesGamePointer(&series, gameID)
	if !found {
		return domain.Game{}, false
	}
	return *game, true
}

func findCorrectionSeriesGamePointer(series *domain.Series, gameID uuid.UUID) (*domain.Game, bool) {
	if series == nil {
		return nil, false
	}
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			game := &series.Slots[slotIndex].Attempts[gameIndex]
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return nil, false
}
