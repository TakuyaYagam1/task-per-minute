package result

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}
