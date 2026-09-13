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

func TestUnlockedTaskHints(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	schedule := domain.BuildHintSchedule(startedAt, 40)
	hints := []string{" first ", "", " third "}

	tests := []struct {
		name string
		now  time.Time
		want []string
	}{
		{name: "before first boundary", now: startedAt.Add(9 * time.Second), want: []string{}},
		{name: "at first boundary", now: startedAt.Add(10 * time.Second), want: []string{"first"}},
		{name: "at second boundary skips empty slot", now: startedAt.Add(20 * time.Second), want: []string{"first"}},
		{name: "at third boundary", now: startedAt.Add(30 * time.Second), want: []string{"first", "third"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := domain.UnlockedTaskHints(hints, schedule, tt.now)

			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestUnlockedTaskHintsRejectsMalformedTiming(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	schedule := domain.BuildHintSchedule(startedAt, 40)

	_, ok := domain.UnlockedTaskHints([]string{"one"}, schedule[:2], startedAt)
	require.False(t, ok)

	_, ok = domain.UnlockedTaskHints([]string{"one"}, schedule, time.Time{})
	require.False(t, ok)

	malformed := append([]domain.HintScheduleEntry(nil), schedule...)
	malformed[0].Index = 2
	_, ok = domain.UnlockedTaskHints([]string{"one"}, malformed, startedAt)
	require.False(t, ok)
}
