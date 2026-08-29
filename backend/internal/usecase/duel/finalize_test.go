package duel

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestFinalizeCasualDuelPreservesWinnerDrawAndPlayerState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		winner     bool
		wantBumps  []string
		wantWinner bool
	}{
		{name: "winner", winner: true, wantBumps: []string{"alice"}, wantWinner: true},
		{name: "draw", winner: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 8, 29, 17, 0, 0, 0, time.UTC)
			firstID := uuid.New()
			secondID := uuid.New()
			duel := &domain.Duel{
				ID: uuid.New(), Player1ID: firstID, Player2ID: secondID,
				Status: domain.DuelStatusActive, StartedAt: now.Add(-time.Minute), Deadline: now.Add(time.Minute),
			}
			duels := &finalizeDuelRepositoryFake{duel: duel}
			players := &finalizePlayerRepositoryFake{players: map[uuid.UUID]*domain.Player{
				firstID:  {ID: firstID, Username: "alice", Status: domain.PlayerStatusInDuel},
				secondID: {ID: secondID, Username: "bob", Status: domain.PlayerStatusInDuel},
			}}
			board := &finalizeBoardFake{}
			var winnerID *uuid.UUID
			if test.winner {
				winnerID = &firstID
			}

			finished, err := finalizeDuel(
				t.Context(), finalizeTransaction{}, duels, players, now, duel.ID, winnerID, board, nil,
			)
			require.NoError(t, err)
			require.Equal(t, domain.DuelStatusFinished, finished.Status)
			require.Equal(t, test.wantWinner, finished.WinnerID != nil)
			require.Equal(t, domain.PlayerStatusIdle, players.players[firstID].Status)
			require.Equal(t, domain.PlayerStatusIdle, players.players[secondID].Status)
			require.Equal(t, test.wantBumps, board.usernames)
		})
	}
}

type finalizeTransaction struct{}

func (finalizeTransaction) Do(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type finalizeDuelRepositoryFake struct {
	duel *domain.Duel
}

func (r *finalizeDuelRepositoryFake) GetByID(_ context.Context, id uuid.UUID) (*domain.Duel, error) {
	if r.duel == nil || r.duel.ID != id {
		return nil, domain.ErrDuelNotFound
	}
	cloned := *r.duel
	return &cloned, nil
}

func (r *finalizeDuelRepositoryFake) Finish(
	_ context.Context,
	id uuid.UUID,
	winnerID *uuid.UUID,
	finishedAt time.Time,
	status domain.DuelStatus,
) (*domain.Duel, error) {
	if r.duel == nil || r.duel.ID != id {
		return nil, domain.ErrDuelNotFound
	}
	if r.duel.Status != domain.DuelStatusActive {
		return nil, domain.ErrDuelFinished
	}
	r.duel.Status = status
	r.duel.WinnerID = cloneFinalizeUUID(winnerID)
	r.duel.FinishedAt = &finishedAt
	finished := *r.duel
	finished.WinnerID = cloneFinalizeUUID(r.duel.WinnerID)
	return &finished, nil
}

type finalizePlayerRepositoryFake struct {
	players map[uuid.UUID]*domain.Player
}

func (r *finalizePlayerRepositoryFake) GetByID(_ context.Context, id uuid.UUID) (*domain.Player, error) {
	player, ok := r.players[id]
	if !ok {
		return nil, domain.ErrPlayerNotFound
	}
	cloned := *player
	return &cloned, nil
}

func (r *finalizePlayerRepositoryFake) UpdateStatus(
	_ context.Context,
	id uuid.UUID,
	status domain.PlayerStatus,
) (*domain.Player, error) {
	player, ok := r.players[id]
	if !ok {
		return nil, domain.ErrPlayerNotFound
	}
	player.Status = status
	updated := *player
	return &updated, nil
}

type finalizeBoardFake struct {
	usernames []string
}

func (b *finalizeBoardFake) IncrementWin(_ context.Context, username string) error {
	b.usernames = append(b.usernames, username)
	return nil
}

func cloneFinalizeUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
