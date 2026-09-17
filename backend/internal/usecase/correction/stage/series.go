package stage

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func findCorrectionSeriesGame(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return domain.Game{}, false
}
