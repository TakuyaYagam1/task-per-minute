package reconnect_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func TestReconnectRoundTripUsesPostgresTimestampPrecision(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 20, 18, 0, 0, 123456000, time.UTC)
	authority := task045Authority(base, true, false)
	command := reconnect.ReconnectCommand{
		Scope:         authority.Scope,
		CommandID:     task045ID(1200),
		ParticipantID: authority.Series.FirstParticipantID,
		IntervalID:    authority.Reconnect[0].ID,
		Settlement:    task045SettlementIDs(1201),
	}
	repository := newTask045RepositoryHarness(t, authority)
	// PostgreSQL timestamptz stores microseconds. Simulate the precision loss
	// on the adapter's read-after-write result so the usecase must create a
	// record that can make a lossless database round trip.
	repository.mutateCommitted = func(record *reconnect.ReconnectRecord) {
		record.RecordedAt = record.RecordedAt.Truncate(time.Microsecond)
		for index := range record.ReconnectAuthority.Presence {
			presence := &record.ReconnectAuthority.Presence[index]
			presence.ConnectedAt = presence.ConnectedAt.Truncate(time.Microsecond)
			presence.UpdatedAt = presence.UpdatedAt.Truncate(time.Microsecond)
			if presence.DisconnectedAt != nil {
				value := presence.DisconnectedAt.Truncate(time.Microsecond)
				presence.DisconnectedAt = &value
			}
		}
		for index := range record.ReconnectAuthority.Reconnect {
			interval := &record.ReconnectAuthority.Reconnect[index]
			interval.OpenedAt = interval.OpenedAt.Truncate(time.Microsecond)
			interval.Deadline = interval.Deadline.Truncate(time.Microsecond)
			interval.UpdatedAt = interval.UpdatedAt.Truncate(time.Microsecond)
			if interval.ClosedAt != nil {
				value := interval.ClosedAt.Truncate(time.Microsecond)
				interval.ClosedAt = &value
			}
		}
		if record.ReconnectAuthority.GameClock.ResumedAt != nil {
			value := record.ReconnectAuthority.GameClock.ResumedAt.Truncate(time.Microsecond)
			record.ReconnectAuthority.GameClock.ResumedAt = &value
		}
		if record.ReconnectAuthority.GameClock.ResumedDeadline != nil {
			value := record.ReconnectAuthority.GameClock.ResumedDeadline.Truncate(time.Microsecond)
			record.ReconnectAuthority.GameClock.ResumedDeadline = &value
		}
	}

	clockAt := base.Add(789 * time.Nanosecond)
	record, changed, err := reconnect.ReconnectNewUseCase(repository, newReconnectClock(t, clockAt)).Reconnect(t.Context(), command)

	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, record)
	require.Equal(t, base, record.RecordedAt)
	require.Equal(t, base, task045Presence(t, record.ReconnectAuthority, command.ParticipantID).ConnectedAt)
	require.Equal(t, base, task045Presence(t, record.ReconnectAuthority, command.ParticipantID).UpdatedAt)
	interval := task045Interval(t, record.ReconnectAuthority, command.IntervalID)
	require.NotNil(t, interval.ClosedAt)
	require.Equal(t, base, *interval.ClosedAt)
	require.Equal(t, base, interval.UpdatedAt)
	require.NotNil(t, record.ReconnectAuthority.GameClock.ResumedAt)
	require.Equal(t, base, *record.ReconnectAuthority.GameClock.ResumedAt)
}
