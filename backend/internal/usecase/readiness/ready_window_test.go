package readiness_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	readinessmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness/mocks"
)

func TestOpenReadyWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	authority, command := readyWindowFixture()
	state, repository := newReadyWindowRepository(t, authority, 2, 1)
	usecase := readiness.NewReadyWindowUseCase(repository, newFixedClock(t, now, 2))

	record, changed, err := usecase.Open(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, now, record.OpenedAt)
	require.Equal(t, now.Add(30*time.Second), record.Deadline)
	require.Equal(t, domain.WaveStateReadyWindowOpen, record.Wave.State)
	require.Equal(t, command.WindowID, record.Wave.ReadyWindow.ID)
	require.Equal(t, command.WindowRevisionID, record.Wave.ReadyWindow.RevisionID)
	require.Equal(t, command.ExpectedRevisions, record.Revisions)
	require.Equal(t, 1, state.commitCount())

	repeated, changed, err := usecase.Open(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, record.CommandID, repeated.CommandID)
	require.Equal(t, record.OpenedAt, repeated.OpenedAt)
	require.Equal(t, record.Deadline, repeated.Deadline)
	require.Equal(t, 1, state.commitCount())

	mutations := map[string]func(*readiness.ReadyWindowAuthority){
		"wave revision": func(value *readiness.ReadyWindowAuthority) {
			value.Revisions.WaveRevision++
		},
		"projection revision": func(value *readiness.ReadyWindowAuthority) {
			value.Revisions.ProjectionRevision++
		},
		"artifact revision": func(value *readiness.ReadyWindowAuthority) {
			value.Revisions.ArtifactRevision++
		},
	}
	for name, mutate := range mutations {
		t.Run("rejects stale "+name, func(t *testing.T) {
			t.Parallel()

			staleAuthority, staleCommand := readyWindowFixture()
			mutate(&staleAuthority)
			state, staleRepository := newReadyWindowRepository(t, staleAuthority, 1, 0)
			opened, staleChanged, staleErr := readiness.NewReadyWindowUseCase(
				staleRepository,
				newFixedClock(t, now, 1),
			).Open(t.Context(), staleCommand)
			require.Nil(t, opened)
			require.False(t, staleChanged)
			require.ErrorIs(t, staleErr, readiness.ErrReadyWindowAuthorityConflict)
			require.Equal(t, 0, state.commitCount())
		})
	}

	t.Run("rejects superseded Wave and reused identities", func(t *testing.T) {
		t.Parallel()

		superseded, supersededCommand := readyWindowFixture()
		openedAt := now.Add(-time.Minute)
		deadline := openedAt.Add(30 * time.Second)
		require.NoError(t, superseded.Wave.OpenReadyWindow(
			task036ID(901),
			domain.ReadyWindowRevisionID(task036ID(902)),
			openedAt,
			deadline,
		))
		superseded.Wave.State = domain.WaveStateSuperseded
		superseded.Wave.ReadyWindow.State = domain.ReadyWindowStateSuperseded
		state, repository := newReadyWindowRepository(t, superseded, 1, 0)

		opened, staleChanged, staleErr := readiness.NewReadyWindowUseCase(
			repository,
			newFixedClock(t, now, 1),
		).Open(t.Context(), supersededCommand)
		require.Nil(t, opened)
		require.False(t, staleChanged)
		require.ErrorIs(t, staleErr, readiness.ErrReadyWindowAuthorityConflict)
		require.Equal(t, 0, state.commitCount())
	})
}

type readyWindowRepositoryState struct {
	mu        sync.Mutex
	authority readiness.ReadyWindowAuthority
	commits   int
}

func newReadyWindowRepository(
	t *testing.T,
	authority readiness.ReadyWindowAuthority,
	loadCalls int,
	commitCalls int,
) (*readyWindowRepositoryState, *readinessmocks.MockReadyWindowRepository) {
	t.Helper()
	state := &readyWindowRepositoryState{authority: authority}
	repository := readinessmocks.NewMockReadyWindowRepository(t)
	repository.EXPECT().
		LoadReadyWindowAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.load).
		Times(loadCalls)
	if commitCalls > 0 {
		repository.EXPECT().
			CommitReadyWindow(mock.Anything, mock.Anything).
			RunAndReturn(state.commit).
			Times(commitCalls)
	}
	return state, repository
}

func (s *readyWindowRepositoryState) load(
	_ context.Context,
	_ readiness.ReadyWindowScope,
) (readiness.ReadyWindowAuthority, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneReadyWindowAuthority(s.authority), nil
}

func (s *readyWindowRepositoryState) commit(
	_ context.Context,
	record readiness.ReadyWindowRecord,
) (*readiness.ReadyWindowRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits++
	if record.ExpectedAuthorityRevision != s.authority.Revision || s.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := cloneReadyWindowRecord(record)
	s.authority.Revision++
	s.authority.Wave = cloneTask036Wave(record.Wave)
	s.authority.Current = &stored
	return &stored, true, nil
}

func (s *readyWindowRepositoryState) commitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits
}

func readyWindowFixture() (readiness.ReadyWindowAuthority, readiness.OpenReadyWindowCommand) {
	scope := readiness.ReadyWindowScope{
		TournamentID: task036ID(1),
		WaveID:       task036ID(2),
	}
	revisions := domain.ReadyWindowSourceRevisions{
		WaveRevisionID:       domain.WaveRevisionID(task036ID(3)),
		WaveRevision:         5,
		ProjectionRevisionID: task036ID(5),
		ProjectionRevision:   11,
		ArtifactRevisionID:   task036ID(6),
		ArtifactRevision:     13,
	}
	wave := domain.Wave{
		ID: scope.WaveID, TournamentID: scope.TournamentID,
		RevisionID: revisions.WaveRevisionID, State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: task036ID(20)},
			{ParticipantID: task036ID(21)},
		},
	}
	return readiness.ReadyWindowAuthority{
			Scope: scope, Revision: 3, Revisions: revisions, Wave: wave,
		}, readiness.OpenReadyWindowCommand{
			Scope: scope, CommandID: task036ID(30), WindowID: task036ID(31),
			WindowRevisionID:  domain.ReadyWindowRevisionID(task036ID(32)),
			ExpectedRevisions: revisions,
		}
}

func cloneReadyWindowAuthority(value readiness.ReadyWindowAuthority) readiness.ReadyWindowAuthority {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	if value.Current != nil {
		current := cloneReadyWindowRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneReadyWindowRecord(value readiness.ReadyWindowRecord) readiness.ReadyWindowRecord {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	return clone
}
