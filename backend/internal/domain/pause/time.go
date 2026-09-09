package pause

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TimeAtOrBefore(at, event time.Time) bool {
	return event.IsZero() || !at.Before(event)
}

func TimePointerAtOrBefore(at time.Time, event *time.Time) bool {
	return event == nil || TimeAtOrBefore(at, *event)
}

func AddTime(at time.Time, duration time.Duration) (time.Time, bool) {
	if duration <= 0 || !domain.IsValidServerTime(at) {
		return time.Time{}, false
	}
	nanos := at.UnixNano()
	if !time.Unix(0, nanos).UTC().Equal(at) || nanos > math.MaxInt64-int64(duration) {
		return time.Time{}, false
	}
	result := time.Unix(0, nanos+int64(duration)).UTC()
	return result, domain.IsValidServerTime(result) && result.After(at)
}
