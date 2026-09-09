package game_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func TestWaveClosure(t *testing.T) {
	t.Parallel()

	t.Run("closes after every child is terminal or routed", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 20, 0, 0, time.UTC)
		authority, command := waveClosureFixture(t, now)
		harness := newCloseRepositoryHarness(t, authority)
		usecase := gameusecase.NewCloseUseCase(harness.repository, waveNewGameClock(t, now))

		closure, changed, err := usecase.Close(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, closure.Validate())
		require.Equal(t, domain.WaveStateCompleted, closure.Wave.State)
		require.Equal(t, command.ClosedWaveRevisionID, closure.Wave.RevisionID)
		require.Len(t, closure.Children, 2)
		require.Equal(t, 1, harness.writeCount())

		repeated, changed, err := usecase.Close(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, closure, repeated)
		require.Equal(t, 1, harness.writeCount())
	})

	t.Run("does not wait for replacement capacity", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 25, 0, 0, time.UTC)
		authority, command := waveClosureFixture(t, now)
		for index := range authority.Children {
			authority.Children[index].State = domain.GameStateVoid
			authority.Children[index].RouteID = waveClosureID(100 + index)
		}
		harness := newCloseRepositoryHarness(t, authority)

		closure, changed, err := gameusecase.NewCloseUseCase(
			harness.repository,
			waveNewGameClock(t, now),
		).Close(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.WaveStateCompleted, closure.Wave.State)
		require.Equal(t, 1, harness.writeCount())
	})

	t.Run("blocks an unresolved child", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 30, 0, 0, time.UTC)
		authority, command := waveClosureFixture(t, now)
		authority.Children[1].State = domain.GameStateActive
		harness := newCloseRepositoryHarness(t, authority)

		closure, changed, err := gameusecase.NewCloseUseCase(
			harness.repository,
			waveNewGameClock(t, now),
		).Close(t.Context(), command)
		require.Nil(t, closure)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrClosureBlocked)
		require.Equal(t, 0, harness.writeCount())
	})
}

type closeRepositoryState struct {
	mu        sync.Mutex
	authority gameusecase.CloseAuthority
	writes    int
}

type closeRepositoryHarness struct {
	repository *gamemocks.MockCloseRepository
	state      *closeRepositoryState
}

func newCloseRepositoryHarness(
	t *testing.T,
	authority gameusecase.CloseAuthority,
) *closeRepositoryHarness {
	t.Helper()

	state := &closeRepositoryState{authority: authority}
	repository := gamemocks.NewMockCloseRepository(t)
	repository.EXPECT().LoadCloseAuthority(mock.Anything, authority.Scope).
		RunAndReturn(func(context.Context, gameusecase.CloseScope) (gameusecase.CloseAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return state.authority, nil
		}).Maybe()
	repository.EXPECT().CommitClosure(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			closure gameusecase.Closure,
		) (*gameusecase.Closure, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if closure.ExpectedAuthorityRevision != state.authority.Revision ||
				state.authority.Current != nil {
				return nil, false, domain.ErrConflict
			}
			stored := closure
			state.authority.Revision++
			state.authority.Wave = stored.Wave
			state.authority.Children = append([]gameusecase.CloseChild(nil), stored.Children...)
			state.authority.Current = &stored
			state.writes++
			return &stored, true, nil
		}).Maybe()
	return &closeRepositoryHarness{repository: repository, state: state}
}

func (h *closeRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func waveClosureFixture(
	t *testing.T,
	now time.Time,
) (gameusecase.CloseAuthority, gameusecase.CloseCommand) {
	t.Helper()

	tournamentID := waveClosureID(1)
	waveID := waveClosureID(2)
	seriesID := waveClosureID(3)
	slotID := waveClosureID(4)
	gameID := waveClosureID(5)
	scope := gameusecase.CloseScope{
		TournamentID: tournamentID,
		WaveID:       waveID,
	}
	authority := gameusecase.CloseAuthority{
		Scope: scope, Revision: 5,
		Wave: waveActiveWaveFixture(
			t, tournamentID, waveID,
			[2]uuid.UUID{waveClosureID(6), waveClosureID(7)}, now,
		),
		Children: []gameusecase.CloseChild{
			{
				SeriesID: seriesID, SlotID: slotID,
				GameID: gameID, State: domain.GameStateVoid,
				RouteID: waveClosureID(15),
			},
			{
				SeriesID: waveClosureID(31), SlotID: waveClosureID(32), GameID: waveClosureID(33),
				State: domain.GameStateCompleted,
			},
		},
	}
	command := gameusecase.CloseCommand{
		Scope: scope, CommandID: waveClosureID(34),
		ExpectedWaveRevisionID: authority.Wave.RevisionID,
		ClosedWaveRevisionID:   domain.WaveRevisionID(waveClosureID(35)),
	}
	return authority, command
}

func waveActiveWaveFixture(
	t *testing.T,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	participants [2]uuid.UUID,
	now time.Time,
) domain.Wave {
	t.Helper()

	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID,
		RevisionID: domain.WaveRevisionID(waveClosureID(19)),
		State:      domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
		},
	}
	openedAt := now.Add(-20 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		waveClosureID(20),
		domain.ReadyWindowRevisionID(waveClosureID(21)),
		openedAt,
		now.Add(10*time.Second),
	))
	for _, participantID := range participants {
		_, err := wave.MarkReady(wave.ReadyWindow.ID, participantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	_, err := wave.Start(wave.ReadyWindow.ID, now.Add(-5*time.Second))
	require.NoError(t, err)
	require.NoError(t, wave.Validate())
	return wave
}

func waveClosureID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "task-040-%d", value))
}
