package observability

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectionHealthSnapshotValidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot ProjectionHealthSnapshot
		valid    bool
	}{
		{
			name:     "no pending publication",
			snapshot: ProjectionHealthSnapshot{ObservedAt: now},
			valid:    true,
		},
		{
			name: "pending publication",
			snapshot: ProjectionHealthSnapshot{
				PendingCount: 1, OldestPendingAt: projectionHealthTime(now.Add(-time.Second)), ObservedAt: now,
			},
			valid: true,
		},
		{
			name:     "negative pending count",
			snapshot: ProjectionHealthSnapshot{PendingCount: -1, ObservedAt: now},
		},
		{
			name:     "pending count requires oldest timestamp",
			snapshot: ProjectionHealthSnapshot{PendingCount: 1, ObservedAt: now},
		},
		{
			name: "future oldest timestamp",
			snapshot: ProjectionHealthSnapshot{
				PendingCount: 1, OldestPendingAt: projectionHealthTime(now.Add(time.Second)), ObservedAt: now,
			},
		},
		{
			name: "empty count must not carry oldest timestamp",
			snapshot: ProjectionHealthSnapshot{
				OldestPendingAt: projectionHealthTime(now.Add(-time.Second)), ObservedAt: now,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.valid {
				require.NoError(t, test.snapshot.Validate())
				return
			}
			require.ErrorIs(t, test.snapshot.Validate(), ErrInvalidProjectionHealth)
		})
	}
}

func projectionHealthTime(value time.Time) *time.Time {
	value = value.Round(0).UTC()
	return &value
}
