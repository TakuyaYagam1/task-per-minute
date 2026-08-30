package arena_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestAtomicWaveStart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 0, 20, 0, time.UTC)

	t.Run("persists one start and derives every deadline", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		repository := &waveStartRepositoryFake{authority: authority}
		usecase := arena.NewWaveStartUseCase(repository, fixedArenaClock{now: now})

		record, changed, err := usecase.Start(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, record.Validate())
		require.Equal(t, now, record.StartedAt)
		require.Equal(t, domain.ArenaWaveStateActive, record.Wave.State)
		require.Equal(t, domain.ArenaReadyWindowStateConsumed, record.Wave.ReadyWindow.State)
		require.Equal(t, now, *record.Wave.ReadyWindow.ConsumedAt)
		require.Len(t, record.Games, 2)
		for index, game := range record.Games {
			require.Equal(t, now, game.StartedAt)
			require.Equal(t, now.Add(time.Duration(authority.Games[index].DeadlineSeconds)*time.Second), game.Deadline)
			require.True(t, game.DeliveryEnabled)
			require.Equal(t, domain.ArenaSeriesStateActive, game.Series.Series.State)
			attempt := game.Series.Series.Slots[0].Attempts[0]
			require.Equal(t, domain.ArenaGameStateActive, attempt.State)
		}

		repeated, changed, err := usecase.Start(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, record.StartedAt, repeated.StartedAt)
		require.Equal(t, 1, repository.commitCount())
	})

	t.Run("stale readiness and planning commit nothing", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		authority.Wave.Members[0].Ready = false
		authority.Wave.State = domain.ArenaWaveStateReadyWindowOpen
		readinessRepository := &waveStartRepositoryFake{authority: authority}
		record, changed, err := arena.NewWaveStartUseCase(
			readinessRepository,
			fixedArenaClock{now: now},
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrWaveStartAuthorityConflict)
		require.Equal(t, 0, readinessRepository.commitCount())

		authority, command = waveStartFixture(t, now)
		authority.Revisions.PlanRevision++
		planRepository := &waveStartRepositoryFake{authority: authority}
		record, changed, err = arena.NewWaveStartUseCase(
			planRepository,
			fixedArenaClock{now: now},
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrWaveStartAuthorityConflict)
		require.Equal(t, 0, planRepository.commitCount())
	})

	t.Run("repository failure cannot expose delivery", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		commitFailure := errors.New("commit failed")
		repository := &waveStartRepositoryFake{authority: authority, commitErr: commitFailure}

		record, changed, err := arena.NewWaveStartUseCase(
			repository,
			fixedArenaClock{now: now},
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, commitFailure)
		require.Nil(t, repository.current())
	})

	t.Run("concurrent start commands have one winner", func(t *testing.T) {
		t.Parallel()

		authority, first := waveStartFixture(t, now)
		second := first
		second.CommandID = task036ID(799)
		repository := &waveStartRepositoryFake{authority: authority}
		commands := []arena.StartWaveCommand{first, second}
		results := make(chan error, len(commands))
		var group sync.WaitGroup
		for _, command := range commands {
			group.Add(1)
			go func() {
				defer group.Done()
				_, _, err := arena.NewWaveStartUseCase(
					repository,
					fixedArenaClock{now: now},
				).Start(context.Background(), command)
				results <- err
			}()
		}
		group.Wait()
		close(results)

		var success, conflict int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, arena.ErrWaveStartConflict):
				conflict++
			default:
				t.Fatalf("unexpected start result: %v", err)
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, conflict)
	})
}

type waveStartRepositoryFake struct {
	mu        sync.Mutex
	authority arena.WaveStartAuthority
	commits   int
	commitErr error
}

func (r *waveStartRepositoryFake) LoadWaveStartAuthority(
	_ context.Context,
	_ arena.WaveStartScope,
) (arena.WaveStartAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneWaveStartAuthority(r.authority), nil
}

func (r *waveStartRepositoryFake) CommitWaveStart(
	_ context.Context,
	record arena.WaveStartRecord,
) (*arena.WaveStartRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	if r.commitErr != nil {
		return nil, false, r.commitErr
	}
	if record.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := cloneWaveStartRecord(record)
	r.authority.Revision++
	r.authority.Wave = cloneTask036Wave(record.Wave)
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *waveStartRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *waveStartRepositoryFake) current() *arena.WaveStartRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.authority.Current == nil {
		return nil
	}
	current := cloneWaveStartRecord(*r.authority.Current)
	return &current
}

func waveStartFixture(
	t *testing.T,
	now time.Time,
) (arena.WaveStartAuthority, arena.StartWaveCommand) {
	t.Helper()

	tournamentID := task036ID(500)
	waveID := task036ID(501)
	waveRevisionID := domain.ArenaWaveRevisionID(task036ID(502))
	windowID := task036ID(503)
	windowRevisionID := domain.ArenaReadyWindowRevisionID(task036ID(504))
	participants := []uuid.UUID{
		task036ID(510), task036ID(511), task036ID(512), task036ID(513),
	}
	wave := domain.ArenaWave{
		ID: waveID, TournamentID: tournamentID, RevisionID: waveRevisionID,
		State: domain.ArenaWaveStatePlanned,
		Members: []domain.ArenaWaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
			{ParticipantID: participants[2]}, {ParticipantID: participants[3]},
		},
	}
	openedAt := now.Add(-10 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(windowID, windowRevisionID, openedAt, now.Add(20*time.Second)))
	for _, participantID := range participants {
		_, err := wave.MarkReady(windowID, participantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	revisions := arena.ReadyWindowSourceRevisions{
		WaveRevisionID: waveRevisionID, WaveRevision: 2,
		PlanRevisionID: task036ID(520), PlanRevision: 3,
		ProjectionRevisionID: task036ID(521), ProjectionRevision: 4,
		ArtifactRevisionID: task036ID(522), ArtifactRevision: 5,
	}
	scope := arena.WaveStartScope{TournamentID: tournamentID, WaveID: waveID, WindowID: windowID}
	games := []arena.WaveStartGameAuthority{
		waveStartGame(t, tournamentID, participants[0], participants[1], 600, 90),
		waveStartGame(t, tournamentID, participants[2], participants[3], 620, 120),
	}
	authority := arena.WaveStartAuthority{
		Scope: scope, Revision: 7, Revisions: revisions, Wave: wave, Games: games,
	}
	command := arena.StartWaveCommand{
		Scope: scope, CommandID: task036ID(530), ExpectedRevisions: revisions,
	}
	return authority, command
}

func waveStartGame(
	t *testing.T,
	tournamentID uuid.UUID,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	base int,
	deadlineSeconds int,
) arena.WaveStartGameAuthority {
	t.Helper()

	seriesID := task036ID(base)
	slotID := task036ID(base + 1)
	gameID := task036ID(base + 2)
	series := arena.SeriesExecution{Series: domain.ArenaSeries{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateReady,
		Slots: []domain.ArenaGameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStatePlanned,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	return arena.WaveStartGameAuthority{
		Scope: arena.GameScope{
			TournamentID: tournamentID, SeriesID: seriesID, SlotID: slotID, GameID: gameID,
		},
		ParticipantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID},
		Series:         series,
		AssignmentID:   task036ID(base + 3), AssignmentRevision: 2,
		PlanRevisionID: task036ID(base + 4), SnapshotID: task036ID(base + 5),
		ContentDigest: sha256.Sum256([]byte{byte(base)}), DeadlineSeconds: deadlineSeconds,
	}
}

func cloneWaveStartAuthority(value arena.WaveStartAuthority) arena.WaveStartAuthority {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	clone.Games = make([]arena.WaveStartGameAuthority, len(value.Games))
	for index, game := range value.Games {
		clone.Games[index] = game
		clone.Games[index].Series = cloneTask036SeriesExecution(game.Series)
	}
	if value.Current != nil {
		current := cloneWaveStartRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneWaveStartRecord(value arena.WaveStartRecord) arena.WaveStartRecord {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	clone.Games = make([]arena.StartedWaveGame, len(value.Games))
	for index, game := range value.Games {
		clone.Games[index] = game
		clone.Games[index].Series = cloneTask036SeriesExecution(game.Series)
	}
	return clone
}

var _ arena.WaveStartRepository = (*waveStartRepositoryFake)(nil)
