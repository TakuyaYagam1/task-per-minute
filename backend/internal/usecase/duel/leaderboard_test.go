package duel

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

func TestBumpLeaderboard_IncrementsWinner(t *testing.T) {
	t.Parallel()

	board := &leaderboardStoreSpy{}

	bumpLeaderboard(t.Context(), board, nil, uuid.New(), "alice")

	require.Equal(t, []string{"alice"}, board.snapshot())
}

func TestBumpLeaderboard_SkipsMissingInputs(t *testing.T) {
	t.Parallel()

	board := &leaderboardStoreSpy{}

	bumpLeaderboard(t.Context(), nil, nil, uuid.New(), "alice")
	bumpLeaderboard(t.Context(), board, nil, uuid.New(), "")

	require.Empty(t, board.snapshot())
}

func TestBumpLeaderboard_LogsIncrementError(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(&logs),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, log.Close())
	})

	duelID := uuid.New()
	board := &leaderboardStoreSpy{err: errors.New("redis down")}

	bumpLeaderboard(t.Context(), board, log, duelID, "alice")

	require.Equal(t, []string{"alice"}, board.snapshot())
	require.Contains(t, logs.String(), "leaderboard increment win failed")
	require.Contains(t, logs.String(), duelID.String())
	require.Contains(t, logs.String(), "alice")
	require.Contains(t, logs.String(), "redis down")
}

type leaderboardStoreSpy struct {
	mu    sync.Mutex
	users []string
	err   error
}

func (s *leaderboardStoreSpy) IncrementWin(_ context.Context, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, username)
	return s.err
}

func (s *leaderboardStoreSpy) WinScores(context.Context) ([]usecase.LeaderboardScore, error) {
	return nil, nil
}

func (s *leaderboardStoreSpy) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.users))
	copy(out, s.users)
	return out
}
