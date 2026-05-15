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

func TestNormalizeTaskHints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hints []string
		want  []string
		ok    bool
	}{
		{name: "missing", hints: nil, want: []string{"", "", ""}, ok: true},
		{name: "first only", hints: []string{" one "}, want: []string{"one", "", ""}, ok: true},
		{name: "third only", hints: []string{"", " ", " three "}, want: []string{"", "", "three"}, ok: true},
		{name: "too many", hints: []string{"one", "two", "three", "four"}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := domain.NormalizeTaskHints(tt.hints)

			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
