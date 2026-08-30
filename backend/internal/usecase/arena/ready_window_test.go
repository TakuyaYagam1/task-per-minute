package arena_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestOpenReadyWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, command := readyWindowFixture()
	repository := &readyWindowRepositoryFake{authority: authority}
	usecase := arena.NewReadyWindowUseCase(repository, fixedArenaClock{now: now})

	record, changed, err := usecase.Open(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, now, record.OpenedAt)
	require.Equal(t, now.Add(30*time.Second), record.Deadline)
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, record.Wave.State)
	require.Equal(t, command.WindowID, record.Wave.ReadyWindow.ID)
	require.Equal(t, command.WindowRevisionID, record.Wave.ReadyWindow.RevisionID)
	require.Equal(t, command.ExpectedRevisions, record.Revisions)
	require.Equal(t, 1, repository.commitCount())

	repeated, changed, err := usecase.Open(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, record.CommandID, repeated.CommandID)
	require.Equal(t, record.OpenedAt, repeated.OpenedAt)
	require.Equal(t, record.Deadline, repeated.Deadline)
	require.Equal(t, 1, repository.commitCount())

	mutations := map[string]func(*arena.ReadyWindowAuthority){
		"wave revision": func(value *arena.ReadyWindowAuthority) {
			value.Revisions.WaveRevision++
		},
		"plan revision": func(value *arena.ReadyWindowAuthority) {
			value.Revisions.PlanRevision++
		},
		"projection revision": func(value *arena.ReadyWindowAuthority) {
			value.Revisions.ProjectionRevision++
		},
		"artifact revision": func(value *arena.ReadyWindowAuthority) {
			value.Revisions.ArtifactRevision++
		},
	}
	for name, mutate := range mutations {
		t.Run("rejects stale "+name, func(t *testing.T) {
			t.Parallel()

			staleAuthority, staleCommand := readyWindowFixture()
			mutate(&staleAuthority)
			staleRepository := &readyWindowRepositoryFake{authority: staleAuthority}
			opened, staleChanged, staleErr := arena.NewReadyWindowUseCase(
				staleRepository,
				fixedArenaClock{now: now},
			).Open(t.Context(), staleCommand)
			require.Nil(t, opened)
			require.False(t, staleChanged)
			require.ErrorIs(t, staleErr, arena.ErrReadyWindowAuthorityConflict)
			require.Equal(t, 0, staleRepository.commitCount())
		})
	}

	t.Run("rejects superseded Wave and reused identities", func(t *testing.T) {
		t.Parallel()

		superseded, supersededCommand := readyWindowFixture()
		openedAt := now.Add(-time.Minute)
		deadline := openedAt.Add(30 * time.Second)
		require.NoError(t, superseded.Wave.OpenReadyWindow(
			task036ID(901),
			domain.ArenaReadyWindowRevisionID(task036ID(902)),
			openedAt,
			deadline,
		))
		superseded.Wave.State = domain.ArenaWaveStateSuperseded
		superseded.Wave.ReadyWindow.State = domain.ArenaReadyWindowStateSuperseded
		repository := &readyWindowRepositoryFake{authority: superseded}

		opened, staleChanged, staleErr := arena.NewReadyWindowUseCase(
			repository,
			fixedArenaClock{now: now},
		).Open(t.Context(), supersededCommand)
		require.Nil(t, opened)
		require.False(t, staleChanged)
		require.ErrorIs(t, staleErr, arena.ErrReadyWindowAuthorityConflict)
		require.Equal(t, 0, repository.commitCount())
	})
}

type readyWindowRepositoryFake struct {
	mu        sync.Mutex
	authority arena.ReadyWindowAuthority
	commits   int
}

func (r *readyWindowRepositoryFake) LoadReadyWindowAuthority(
	_ context.Context,
	_ arena.ReadyWindowScope,
) (arena.ReadyWindowAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneReadyWindowAuthority(r.authority), nil
}

func (r *readyWindowRepositoryFake) CommitReadyWindow(
	_ context.Context,
	record arena.ReadyWindowRecord,
) (*arena.ReadyWindowRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	if record.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := cloneReadyWindowRecord(record)
	r.authority.Revision++
	r.authority.Wave = cloneTask036Wave(record.Wave)
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *readyWindowRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func readyWindowFixture() (arena.ReadyWindowAuthority, arena.OpenReadyWindowCommand) {
	scope := arena.ReadyWindowScope{
		TournamentID: task036ID(1),
		WaveID:       task036ID(2),
	}
	revisions := arena.ReadyWindowSourceRevisions{
		WaveRevisionID:       domain.ArenaWaveRevisionID(task036ID(3)),
		WaveRevision:         5,
		PlanRevisionID:       task036ID(4),
		PlanRevision:         7,
		ProjectionRevisionID: task036ID(5),
		ProjectionRevision:   11,
		ArtifactRevisionID:   task036ID(6),
		ArtifactRevision:     13,
	}
	wave := domain.ArenaWave{
		ID: scope.WaveID, TournamentID: scope.TournamentID,
		RevisionID: revisions.WaveRevisionID, State: domain.ArenaWaveStatePlanned,
		Members: []domain.ArenaWaveMember{
			{ParticipantID: task036ID(20)},
			{ParticipantID: task036ID(21)},
		},
	}
	return arena.ReadyWindowAuthority{
			Scope: scope, Revision: 3, Revisions: revisions, Wave: wave,
		}, arena.OpenReadyWindowCommand{
			Scope: scope, CommandID: task036ID(30), WindowID: task036ID(31),
			WindowRevisionID:  domain.ArenaReadyWindowRevisionID(task036ID(32)),
			ExpectedRevisions: revisions,
		}
}

func cloneReadyWindowAuthority(value arena.ReadyWindowAuthority) arena.ReadyWindowAuthority {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	if value.Current != nil {
		current := cloneReadyWindowRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneReadyWindowRecord(value arena.ReadyWindowRecord) arena.ReadyWindowRecord {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	return clone
}

func cloneTask036Wave(value domain.ArenaWave) domain.ArenaWave {
	clone := value
	clone.Members = append([]domain.ArenaWaveMember(nil), value.Members...)
	if value.ReadyWindow != nil {
		window := *value.ReadyWindow
		if value.ReadyWindow.ConsumedAt != nil {
			consumedAt := *value.ReadyWindow.ConsumedAt
			window.ConsumedAt = &consumedAt
		}
		clone.ReadyWindow = &window
	}
	if value.StartedAt != nil {
		startedAt := *value.StartedAt
		clone.StartedAt = &startedAt
	}
	if value.PausedAt != nil {
		pausedAt := *value.PausedAt
		clone.PausedAt = &pausedAt
	}
	return clone
}

func task036ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("36000000-0000-0000-0000-%012d", value))
}

var _ arena.ReadyWindowRepository = (*readyWindowRepositoryFake)(nil)
