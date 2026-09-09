package attendance_test

import (
	"testing"
	"time"

	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance/mocks"
)

func attendanceNewFixedTournamentClock(t *testing.T, now time.Time, calls int) *tournamentmocks.MockAttendanceClock {
	t.Helper()
	clock := tournamentmocks.NewMockAttendanceClock(t)
	if calls > 0 {
		clock.EXPECT().Now().Return(now).Times(calls)
	}
	return clock
}
