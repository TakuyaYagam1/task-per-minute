package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestBuildHintSchedule(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	got := domain.BuildHintSchedule(startedAt, 40)

	require.Equal(t, []domain.HintScheduleEntry{
		{Index: 1, UnlockAt: startedAt.Add(10 * time.Second)},
		{Index: 2, UnlockAt: startedAt.Add(20 * time.Second)},
		{Index: 3, UnlockAt: startedAt.Add(30 * time.Second)},
	}, got)
}
