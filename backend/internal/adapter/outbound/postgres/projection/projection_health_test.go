package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectionHealthSnapshotMapsBoundedAggregate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 40, 0, 0, time.UTC)
	tests := []struct {
		name       string
		count      int64
		oldest     time.Time
		oldestOK   bool
		observedAt time.Time
		valid      bool
	}{
		{name: "empty aggregate", observedAt: now, valid: true},
		{
			name: "pending aggregate", count: 3, oldest: now.Add(-time.Minute), oldestOK: true, observedAt: now, valid: true,
		},
		{
			name: "missing oldest aggregate", count: 1, observedAt: now,
		},
		{
			name: "future aggregate", count: 1, oldest: now.Add(time.Second), oldestOK: true, observedAt: now,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			snapshot := projectionHealthSnapshot(
				test.count,
				test.oldest,
				test.oldestOK,
				test.observedAt,
			)
			if test.valid {
				require.NoError(t, snapshot.Validate())
				return
			}
			require.Error(t, snapshot.Validate())
		})
	}
}
