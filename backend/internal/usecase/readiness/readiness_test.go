package readiness_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	readinessmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness/mocks"
)

func TestReadyCommandAndPreStartDisconnect(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 0, 10, 0, time.UTC)
	authority := readinessFixture(t, now)
	state, repository := newReadinessRepository(t, authority, 6, 4)
	usecase := readiness.NewReadinessUseCase(repository, newFixedClock(t, now, 6))
	firstParticipant := authority.Wave.Members[0].ParticipantID
	secondParticipant := authority.Wave.Members[1].ParticipantID

	firstCommand := readiness.ReadyCommand{
		Scope: authority.Scope, CommandID: task036ID(100),
		ActorParticipantID: firstParticipant, ParticipantID: firstParticipant,
		ExpectedWaveRevisionID:   authority.Wave.RevisionID,
		ExpectedWindowRevisionID: authority.Wave.ReadyWindow.RevisionID,
	}
	record, changed, err := usecase.MarkReady(t.Context(), firstCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, task036MemberReady(record.Wave, firstParticipant))
	require.False(t, task036MemberReady(record.Wave, secondParticipant))
	require.Equal(t, domain.WaveStateReadyWindowOpen, record.Wave.State)
	require.Len(t, record.Events, 1)
	require.Equal(t, readiness.ReadinessEventReady, record.Events[0].Type)

	repeated, changed, err := usecase.MarkReady(t.Context(), firstCommand)
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, task036MemberReady(repeated.Wave, firstParticipant))
	require.Equal(t, 1, state.commitCount())

	wrongOwner := firstCommand
	wrongOwner.CommandID = task036ID(101)
	wrongOwner.ActorParticipantID = secondParticipant
	denied, changed, err := usecase.MarkReady(t.Context(), wrongOwner)
	require.Nil(t, denied)
	require.False(t, changed)
	require.ErrorIs(t, err, domain.ErrAssignmentParticipant)

	secondCommand := firstCommand
	secondCommand.CommandID = task036ID(102)
	secondCommand.ActorParticipantID = secondParticipant
	secondCommand.ParticipantID = secondParticipant
	record, changed, err = usecase.MarkReady(t.Context(), secondCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateReady, record.Wave.State)

	disconnect := readiness.DisconnectReadinessCommand{
		Scope: authority.Scope, CommandID: task036ID(103), ParticipantID: firstParticipant,
		ExpectedWaveRevisionID:   authority.Wave.RevisionID,
		ExpectedWindowRevisionID: authority.Wave.ReadyWindow.RevisionID,
	}
	record, changed, err = usecase.ClearOnDisconnect(t.Context(), disconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, task036MemberReady(record.Wave, firstParticipant))
	require.True(t, task036MemberReady(record.Wave, secondParticipant))
	require.Equal(t, domain.WaveStateReadyWindowOpen, record.Wave.State)
	require.Equal(t, readiness.ReadinessEventCleared, record.Events[len(record.Events)-1].Type)

	repeated, changed, err = usecase.MarkReady(t.Context(), firstCommand)
	require.NoError(t, err)
	require.False(t, changed)
	require.False(t, task036MemberReady(repeated.Wave, firstParticipant), "an old command must not restore readiness")

	freshCommand := firstCommand
	freshCommand.CommandID = task036ID(104)
	record, changed, err = usecase.MarkReady(t.Context(), freshCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, task036MemberReady(record.Wave, firstParticipant))
	require.Equal(t, domain.WaveStateReady, record.Wave.State)

	t.Run("rejects a stale window", func(t *testing.T) {
		t.Parallel()

		staleAuthority := readinessFixture(t, now)
		state, staleRepository := newReadinessRepository(t, staleAuthority, 1, 0)
		staleCommand := firstCommand
		staleCommand.Scope = staleAuthority.Scope
		staleCommand.CommandID = task036ID(110)
		staleCommand.ActorParticipantID = staleAuthority.Wave.Members[0].ParticipantID
		staleCommand.ParticipantID = staleAuthority.Wave.Members[0].ParticipantID
		staleCommand.ExpectedWaveRevisionID = staleAuthority.Wave.RevisionID
		staleCommand.ExpectedWindowRevisionID = domain.ReadyWindowRevisionID(task036ID(999))

		stale, staleChanged, staleErr := readiness.NewReadinessUseCase(
			staleRepository,
			newFixedClock(t, now, 1),
		).MarkReady(t.Context(), staleCommand)
		require.Nil(t, stale)
		require.False(t, staleChanged)
		require.ErrorIs(t, staleErr, readiness.ErrReadinessAuthorityConflict)
		require.Equal(t, 0, state.commitCount())
	})

	t.Run("serializes concurrent commands for one participant", func(t *testing.T) {
		t.Parallel()

		concurrentAuthority := readinessFixture(t, now)
		_, concurrentRepository := newConcurrentReadinessRepository(t, concurrentAuthority)
		clock := newFixedClock(t, now, 2)
		commands := []readiness.ReadyCommand{
			{
				Scope: concurrentAuthority.Scope, CommandID: task036ID(120),
				ActorParticipantID:       concurrentAuthority.Wave.Members[0].ParticipantID,
				ParticipantID:            concurrentAuthority.Wave.Members[0].ParticipantID,
				ExpectedWaveRevisionID:   concurrentAuthority.Wave.RevisionID,
				ExpectedWindowRevisionID: concurrentAuthority.Wave.ReadyWindow.RevisionID,
			},
			{
				Scope: concurrentAuthority.Scope, CommandID: task036ID(121),
				ActorParticipantID:       concurrentAuthority.Wave.Members[0].ParticipantID,
				ParticipantID:            concurrentAuthority.Wave.Members[0].ParticipantID,
				ExpectedWaveRevisionID:   concurrentAuthority.Wave.RevisionID,
				ExpectedWindowRevisionID: concurrentAuthority.Wave.ReadyWindow.RevisionID,
			},
		}
		results := make(chan bool, len(commands))
		errorsChannel := make(chan error, len(commands))
		var group sync.WaitGroup
		for _, command := range commands {
			group.Add(1)
			go func() {
				defer group.Done()
				_, commandChanged, commandErr := readiness.NewReadinessUseCase(
					concurrentRepository,
					clock,
				).MarkReady(context.Background(), command)
				results <- commandChanged
				errorsChannel <- commandErr
			}()
		}
		group.Wait()
		close(results)
		close(errorsChannel)

		var changedCount int
		for commandErr := range errorsChannel {
			require.NoError(t, commandErr)
		}
		for commandChanged := range results {
			if commandChanged {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
	})
}

type readinessRepositoryState struct {
	mu        sync.Mutex
	authority readiness.ReadinessAuthority
	commits   int
}

func newReadinessRepository(
	t *testing.T,
	authority readiness.ReadinessAuthority,
	loadCalls int,
	commitCalls int,
) (*readinessRepositoryState, *readinessmocks.MockReadinessRepository) {
	t.Helper()
	state := &readinessRepositoryState{authority: authority}
	repository := readinessmocks.NewMockReadinessRepository(t)
	repository.EXPECT().
		LoadReadinessAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.load).
		Times(loadCalls)
	if commitCalls > 0 {
		repository.EXPECT().
			CommitReadiness(mock.Anything, mock.Anything).
			RunAndReturn(state.commit).
			Times(commitCalls)
	}
	return state, repository
}

func newConcurrentReadinessRepository(
	t *testing.T,
	authority readiness.ReadinessAuthority,
) (*readinessRepositoryState, *readinessmocks.MockReadinessRepository) {
	t.Helper()
	state := &readinessRepositoryState{authority: authority}
	repository := readinessmocks.NewMockReadinessRepository(t)
	repository.EXPECT().
		LoadReadinessAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.load).
		Maybe()
	repository.EXPECT().
		CommitReadiness(mock.Anything, mock.Anything).
		RunAndReturn(state.commit).
		Maybe()
	return state, repository
}

func (s *readinessRepositoryState) load(
	_ context.Context,
	_ readiness.ReadinessScope,
) (readiness.ReadinessAuthority, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneReadinessAuthority(s.authority), nil
}

func (s *readinessRepositoryState) commit(
	_ context.Context,
	commit readiness.ReadinessCommit,
) (*readiness.ReadinessRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if commit.ExpectedRevision != s.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	s.commits++
	s.authority.Revision++
	s.authority.Wave = cloneTask036Wave(commit.Wave)
	s.authority.Events = append(s.authority.Events, commit.Event)
	record := readiness.ReadinessRecord{
		Scope: s.authority.Scope, Revision: s.authority.Revision,
		Wave:   cloneTask036Wave(s.authority.Wave),
		Events: append([]readiness.ReadinessEvent(nil), s.authority.Events...),
	}
	return &record, true, nil
}

func (s *readinessRepositoryState) commitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits
}

func readinessFixture(t *testing.T, now time.Time) readiness.ReadinessAuthority {
	t.Helper()

	readyWindowAuthority, command := readyWindowFixture()
	wave := cloneTask036Wave(readyWindowAuthority.Wave)
	require.NoError(t, wave.OpenReadyWindow(
		command.WindowID,
		command.WindowRevisionID,
		now.Add(-10*time.Second),
		now.Add(20*time.Second),
	))
	return readiness.ReadinessAuthority{
		Scope:    readiness.ReadinessScope{WaveID: wave.ID, WindowID: command.WindowID},
		Revision: 1,
		Wave:     wave,
	}
}

func cloneReadinessAuthority(value readiness.ReadinessAuthority) readiness.ReadinessAuthority {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	clone.Events = append([]readiness.ReadinessEvent(nil), value.Events...)
	return clone
}

func task036MemberReady(wave domain.Wave, participantID uuid.UUID) bool {
	for _, member := range wave.Members {
		if member.ParticipantID == participantID {
			return member.Ready
		}
	}
	return false
}
