package playoff

import "time"

func validPlayoffTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Equal(value.Round(0))
}
