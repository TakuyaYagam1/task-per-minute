package duel

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTimerRegistryExpireSkipsPausedEntry(t *testing.T) {
	t.Parallel()

	duelID := uuid.New()
	called := false
	entry := &timerEntry{
		paused: true,
		onExpire: func() {
			called = true
		},
	}

	(&TimerRegistry{}).expire(duelID, entry)

	require.False(t, entry.done)
	require.True(t, entry.paused)
	require.False(t, called)
}
