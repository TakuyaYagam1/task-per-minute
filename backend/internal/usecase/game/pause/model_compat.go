package pause

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

func pauseValidServerTime(value time.Time) bool {
	return model.PauseValidServerTime(value)
}
