package arena_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestOldWaveClosure(t *testing.T) {
	t.Parallel()

	t.Run("closes after every child is terminal or routed", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 20, 0, 0, time.UTC)
		authority, command := task040OldWaveClosureFixture(t, now)
		repository := &oldWaveCloseRepositoryFake{authority: authority}
		usecase := arena.NewOldWaveCloseUseCase(repository, fixedArenaClock{now: now})

		closure, changed, err := usecase.Close(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, closure.Validate())
		require.Equal(t, domain.ArenaWaveStateCompleted, closure.Wave.State)
		require.Equal(t, command.ClosedWaveRevisionID, closure.Wave.RevisionID)
		require.Len(t, closure.Children, 2)
		require.Equal(t, 1, repository.writeCount())

		repeated, changed, err := usecase.Close(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, closure, repeated)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("does not wait for replacement capacity", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 25, 0, 0, time.UTC)
		authority, command := task040OldWaveClosureFixture(t, now)
		for index := range authority.Children {
			authority.Children[index].State = domain.ArenaGameStateVoid
			authority.Children[index].RouteID = task040ID(100 + index)
		}
		repository := &oldWaveCloseRepositoryFake{authority: authority}

		closure, changed, err := arena.NewOldWaveCloseUseCase(
			repository,
			fixedArenaClock{now: now},
		).Close(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaWaveStateCompleted, closure.Wave.State)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("blocks an unresolved child", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 30, 0, 0, time.UTC)
		authority, command := task040OldWaveClosureFixture(t, now)
		authority.Children[1].State = domain.ArenaGameStateActive
		repository := &oldWaveCloseRepositoryFake{authority: authority}

		closure, changed, err := arena.NewOldWaveCloseUseCase(
			repository,
			fixedArenaClock{now: now},
		).Close(t.Context(), command)
		require.Nil(t, closure)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrOldWaveClosureBlocked)
		require.Equal(t, 0, repository.writeCount())
	})
}

type oldWaveCloseRepositoryFake struct {
	mu        sync.Mutex
	authority arena.OldWaveCloseAuthority
	writes    int
}

func (r *oldWaveCloseRepositoryFake) LoadOldWaveCloseAuthority(
	_ context.Context,
	_ arena.OldWaveScope,
) (arena.OldWaveCloseAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority, nil
}

func (r *oldWaveCloseRepositoryFake) CommitOldWaveClosure(
	_ context.Context,
	closure arena.OldWaveClosure,
) (*arena.OldWaveClosure, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if closure.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := closure
	r.authority.Revision++
	r.authority.Wave = stored.Wave
	r.authority.Children = append([]arena.OldWaveChild(nil), stored.Children...)
	r.authority.Current = &stored
	r.writes++
	return &stored, true, nil
}

func (r *oldWaveCloseRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func task040OldWaveClosureFixture(
	t *testing.T,
	now time.Time,
) (arena.OldWaveCloseAuthority, arena.OldWaveCloseCommand) {
	t.Helper()

	failedAuthority, failedCommand := task040FailedAttemptFixture(t, now)
	failedRepository := &failedAttemptRepositoryFake{authority: failedAuthority}
	failed, changed, err := arena.NewFailedAttemptUseCase(
		failedRepository,
		fixedArenaClock{now: now},
	).Terminalize(t.Context(), failedCommand)
	require.NoError(t, err)
	require.True(t, changed)

	scope := arena.OldWaveScope{
		TournamentID: failed.Scope.TournamentID,
		WaveID:       failed.Scope.WaveID,
	}
	authority := arena.OldWaveCloseAuthority{
		Scope: scope, Revision: 5, Wave: failedAuthority.Wave,
		Children: []arena.OldWaveChild{
			{
				SeriesID: failed.Scope.SeriesID, SlotID: failed.Scope.SlotID,
				GameID: failed.Scope.GameID, State: failed.Game.State,
				RouteID: failed.WaveRoute.ID,
			},
			{
				SeriesID: task040ID(31), SlotID: task040ID(32), GameID: task040ID(33),
				State: domain.ArenaGameStateCompleted,
			},
		},
	}
	command := arena.OldWaveCloseCommand{
		Scope: scope, CommandID: task040ID(34),
		ExpectedWaveRevisionID: failedAuthority.Wave.RevisionID,
		ClosedWaveRevisionID:   domain.ArenaWaveRevisionID(task040ID(35)),
	}
	return authority, command
}

var _ arena.OldWaveCloseRepository = (*oldWaveCloseRepositoryFake)(nil)
