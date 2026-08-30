package arena_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestReadyCommandAndPreStartDisconnect(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 0, 10, 0, time.UTC)
	authority := readinessFixture(t, now)
	repository := &readinessRepositoryFake{authority: authority}
	usecase := arena.NewReadinessUseCase(repository, fixedArenaClock{now: now})
	firstParticipant := authority.Wave.Members[0].ParticipantID
	secondParticipant := authority.Wave.Members[1].ParticipantID

	firstCommand := arena.ReadyCommand{
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
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, record.Wave.State)
	require.Len(t, record.Events, 1)
	require.Equal(t, arena.ReadinessEventReady, record.Events[0].Type)

	repeated, changed, err := usecase.MarkReady(t.Context(), firstCommand)
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, task036MemberReady(repeated.Wave, firstParticipant))
	require.Equal(t, 1, repository.commitCount())

	wrongOwner := firstCommand
	wrongOwner.CommandID = task036ID(101)
	wrongOwner.ActorParticipantID = secondParticipant
	denied, changed, err := usecase.MarkReady(t.Context(), wrongOwner)
	require.Nil(t, denied)
	require.False(t, changed)
	require.ErrorIs(t, err, domain.ErrArenaAssignmentParticipant)

	secondCommand := firstCommand
	secondCommand.CommandID = task036ID(102)
	secondCommand.ActorParticipantID = secondParticipant
	secondCommand.ParticipantID = secondParticipant
	record, changed, err = usecase.MarkReady(t.Context(), secondCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaWaveStateReady, record.Wave.State)

	disconnect := arena.DisconnectReadinessCommand{
		Scope: authority.Scope, CommandID: task036ID(103), ParticipantID: firstParticipant,
		ExpectedWaveRevisionID:   authority.Wave.RevisionID,
		ExpectedWindowRevisionID: authority.Wave.ReadyWindow.RevisionID,
	}
	record, changed, err = usecase.ClearOnDisconnect(t.Context(), disconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, task036MemberReady(record.Wave, firstParticipant))
	require.True(t, task036MemberReady(record.Wave, secondParticipant))
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, record.Wave.State)
	require.Equal(t, arena.ReadinessEventCleared, record.Events[len(record.Events)-1].Type)

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
	require.Equal(t, domain.ArenaWaveStateReady, record.Wave.State)

	t.Run("rejects a stale window", func(t *testing.T) {
		t.Parallel()

		staleAuthority := readinessFixture(t, now)
		staleRepository := &readinessRepositoryFake{authority: staleAuthority}
		staleCommand := firstCommand
		staleCommand.Scope = staleAuthority.Scope
		staleCommand.CommandID = task036ID(110)
		staleCommand.ActorParticipantID = staleAuthority.Wave.Members[0].ParticipantID
		staleCommand.ParticipantID = staleAuthority.Wave.Members[0].ParticipantID
		staleCommand.ExpectedWaveRevisionID = staleAuthority.Wave.RevisionID
		staleCommand.ExpectedWindowRevisionID = domain.ArenaReadyWindowRevisionID(task036ID(999))

		stale, staleChanged, staleErr := arena.NewReadinessUseCase(
			staleRepository,
			fixedArenaClock{now: now},
		).MarkReady(t.Context(), staleCommand)
		require.Nil(t, stale)
		require.False(t, staleChanged)
		require.ErrorIs(t, staleErr, arena.ErrReadinessAuthorityConflict)
		require.Equal(t, 0, staleRepository.commitCount())
	})

	t.Run("serializes concurrent commands for one participant", func(t *testing.T) {
		t.Parallel()

		concurrentAuthority := readinessFixture(t, now)
		concurrentRepository := &readinessRepositoryFake{authority: concurrentAuthority}
		commands := []arena.ReadyCommand{
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
				_, commandChanged, commandErr := arena.NewReadinessUseCase(
					concurrentRepository,
					fixedArenaClock{now: now},
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

type readinessRepositoryFake struct {
	mu        sync.Mutex
	authority arena.ReadinessAuthority
	commits   int
}

func (r *readinessRepositoryFake) LoadReadinessAuthority(
	_ context.Context,
	_ arena.ReadinessScope,
) (arena.ReadinessAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneReadinessAuthority(r.authority), nil
}

func (r *readinessRepositoryFake) CommitReadiness(
	_ context.Context,
	commit arena.ReadinessCommit,
) (*arena.ReadinessRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if commit.ExpectedRevision != r.authority.Revision {
		return nil, false, domain.ErrConflict
	}
	r.commits++
	r.authority.Revision++
	r.authority.Wave = cloneTask036Wave(commit.Wave)
	r.authority.Events = append(r.authority.Events, commit.Event)
	record := arena.ReadinessRecord{
		Scope: r.authority.Scope, Revision: r.authority.Revision,
		Wave:   cloneTask036Wave(r.authority.Wave),
		Events: append([]arena.ReadinessEvent(nil), r.authority.Events...),
	}
	return &record, true, nil
}

func (r *readinessRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func readinessFixture(t *testing.T, now time.Time) arena.ReadinessAuthority {
	t.Helper()

	readyWindowAuthority, command := readyWindowFixture()
	wave := cloneTask036Wave(readyWindowAuthority.Wave)
	require.NoError(t, wave.OpenReadyWindow(
		command.WindowID,
		command.WindowRevisionID,
		now.Add(-10*time.Second),
		now.Add(20*time.Second),
	))
	return arena.ReadinessAuthority{
		Scope:    arena.ReadinessScope{WaveID: wave.ID, WindowID: command.WindowID},
		Revision: 1,
		Wave:     wave,
	}
}

func cloneReadinessAuthority(value arena.ReadinessAuthority) arena.ReadinessAuthority {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	clone.Events = append([]arena.ReadinessEvent(nil), value.Events...)
	return clone
}

func task036MemberReady(wave domain.ArenaWave, participantID uuid.UUID) bool {
	for _, member := range wave.Members {
		if member.ParticipantID == participantID {
			return member.Ready
		}
	}
	return false
}

var _ arena.ReadinessRepository = (*readinessRepositoryFake)(nil)
