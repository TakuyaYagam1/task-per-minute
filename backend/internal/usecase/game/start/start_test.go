package start_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
)

func TestAtomicWaveStart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 0, 20, 0, time.UTC)

	t.Run("persists one start and derives every deadline", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		harness := newWaveStartRepositoryHarness(t, authority)
		usecase := gameusecase.NewStartUseCase(harness.repository, waveNewGameClock(t, now))

		record, changed, err := usecase.Start(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, record.Validate())
		require.Equal(t, now, record.StartedAt)
		require.Equal(t, domain.WaveStateActive, record.Wave.State)
		require.Equal(t, domain.ReadyWindowStateConsumed, record.Wave.ReadyWindow.State)
		require.Equal(t, now, *record.Wave.ReadyWindow.ConsumedAt)
		require.Len(t, record.Games, 2)
		for index, startedGame := range record.Games {
			require.Equal(t, now, startedGame.StartedAt)
			require.Equal(
				t,
				now.Add(time.Duration(authority.Games[index].DeadlineSeconds)*time.Second),
				startedGame.Deadline,
			)
			require.True(t, startedGame.DeliveryEnabled)
			require.Equal(t, domain.SeriesStateActive, startedGame.Series.Series.State)
			attempt := startedGame.Series.Series.Slots[0].Attempts[0]
			require.Equal(t, domain.GameStateActive, attempt.State)
		}

		repeated, changed, err := usecase.Start(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, record.StartedAt, repeated.StartedAt)
		require.Equal(t, 1, harness.commitCount())

		reused := command
		reused.ActorID = waveStartID(531)
		_, changed, err = usecase.Start(t.Context(), reused)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrWaveStartConflict)
		require.Equal(t, 1, harness.commitCount())

		reused = command
		reused.RequestDigest[0] ^= 0xff
		_, changed, err = usecase.Start(t.Context(), reused)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrWaveStartConflict)
		require.Equal(t, 1, harness.commitCount())
	})

	t.Run("permits exactly one explicit Swiss bye without a Game", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		byeParticipantID := waveStartID(514)
		authority.Wave.Members = append(authority.Wave.Members, domain.WaveMember{
			ParticipantID: byeParticipantID,
			Ready:         true,
		})
		authority.ReadinessRevisions[byeParticipantID] = 1
		authority.ByeParticipantID = &byeParticipantID
		harness := newWaveStartRepositoryHarness(t, authority)

		record, changed, err := gameusecase.NewStartUseCase(
			harness.repository,
			waveNewGameClock(t, now),
		).Start(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, byeParticipantID, *record.ByeParticipantID)
		require.Len(t, record.Games, 2)
		require.NoError(t, record.Validate())

		replayed, changed, err := gameusecase.NewStartUseCase(
			harness.repository,
			waveNewGameClock(t, now),
		).Start(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, byeParticipantID, *replayed.ByeParticipantID)
	})

	for _, testCase := range []struct {
		name   string
		mutate func(*gameusecase.StartAuthority)
	}{
		{
			name: "missing explicit bye",
			mutate: func(authority *gameusecase.StartAuthority) {
				participantID := waveStartID(514)
				authority.Wave.Members = append(authority.Wave.Members, domain.WaveMember{ParticipantID: participantID, Ready: true})
				authority.ReadinessRevisions[participantID] = 1
			},
		},
		{
			name: "bye also has a Game",
			mutate: func(authority *gameusecase.StartAuthority) {
				participantID := authority.Games[0].ParticipantIDs[0]
				authority.ByeParticipantID = &participantID
			},
		},
		{
			name: "bye is outside the Wave",
			mutate: func(authority *gameusecase.StartAuthority) {
				participantID := waveStartID(599)
				authority.ByeParticipantID = &participantID
			},
		},
	} {
		t.Run("rejects "+testCase.name, func(t *testing.T) {
			t.Parallel()

			authority, command := waveStartFixture(t, now)
			testCase.mutate(&authority)
			harness := newWaveStartRepositoryHarness(t, authority)
			record, changed, err := gameusecase.NewStartUseCase(
				harness.repository,
				waveNewGameClock(t, now),
			).Start(t.Context(), command)
			require.Nil(t, record)
			require.False(t, changed)
			require.ErrorIs(t, err, gameusecase.ErrInvalidWaveStart)
			require.Equal(t, 0, harness.commitCount())
		})
	}

	t.Run("stale readiness and planning commit nothing", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		authority.Wave.Members[0].Ready = false
		authority.Wave.State = domain.WaveStateReadyWindowOpen
		readinessHarness := newWaveStartRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewStartUseCase(
			readinessHarness.repository,
			waveNewGameClock(t, now),
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrWaveStartAuthorityConflict)
		require.Equal(t, 0, readinessHarness.commitCount())

		authority, command = waveStartFixture(t, now)
		command.ExpectedProjectionRevision--
		projectionHarness := newWaveStartRepositoryHarness(t, authority)
		record, changed, err = gameusecase.NewStartUseCase(
			projectionHarness.repository,
			waveNewGameClock(t, now),
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrWaveStartAuthorityConflict)
		require.Equal(t, 0, projectionHarness.commitCount())
	})

	t.Run("rejects a task deadline outside the server policy", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		authority.Games[0].DeadlineSeconds = 179
		harness := newWaveStartRepositoryHarness(t, authority)

		record, changed, err := gameusecase.NewStartUseCase(
			harness.repository,
			waveNewGameClock(t, now),
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrInvalidWaveStart)
		require.Equal(t, 0, harness.commitCount())
	})

	t.Run("repository failure cannot expose delivery", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		commitFailure := errors.New("commit failed")
		harness := newWaveStartRepositoryHarness(t, authority)
		harness.setCommitError(commitFailure)

		record, changed, err := gameusecase.NewStartUseCase(
			harness.repository,
			waveNewGameClock(t, now),
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, commitFailure)
		require.Nil(t, harness.current())
	})

	t.Run("uses PostgreSQL time instead of the process clock", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		harness := newWaveStartRepositoryHarness(t, authority)
		serverNow := now.Add(2 * time.Second)
		harness.setServerTime(serverNow)
		processNow := serverNow.Add(24 * time.Hour)

		record, changed, err := gameusecase.NewStartUseCase(
			harness.repository,
			waveNewGameClock(t, processNow),
		).Start(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, serverNow, record.StartedAt)
		require.NotEqual(t, processNow, record.StartedAt)
		for _, startedGame := range record.Games {
			require.Equal(t, serverNow, startedGame.StartedAt)
		}
	})

	t.Run("fails closed when PostgreSQL time cannot be read", func(t *testing.T) {
		t.Parallel()

		authority, command := waveStartFixture(t, now)
		harness := newWaveStartRepositoryHarness(t, authority)
		timeFailure := errors.New("database time unavailable")
		harness.setServerTimeError(timeFailure)

		record, changed, err := gameusecase.NewStartUseCase(
			harness.repository,
			waveNewGameClock(t, now.Add(24*time.Hour)),
		).Start(t.Context(), command)
		require.Nil(t, record)
		require.False(t, changed)
		require.ErrorIs(t, err, timeFailure)
		require.Equal(t, 0, harness.commitCount())
	})

	t.Run("concurrent start commands have one winner", func(t *testing.T) {
		t.Parallel()

		authority, first := waveStartFixture(t, now)
		second := first
		second.CommandID = waveStartID(799)
		harness := newWaveStartRepositoryHarness(t, authority)
		clock := waveNewGameClock(t, now)
		commands := []gameusecase.StartCommand{first, second}
		results := make(chan error, len(commands))
		var group sync.WaitGroup
		for _, command := range commands {
			group.Add(1)
			go func() {
				defer group.Done()
				_, _, err := gameusecase.NewStartUseCase(
					harness.repository,
					clock,
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
			case errors.Is(err, gameusecase.ErrWaveStartConflict):
				conflict++
			default:
				t.Fatalf("unexpected start result: %v", err)
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, conflict)
	})
}

type waveStartRepositoryState struct {
	mu              sync.Mutex
	authority       gameusecase.StartAuthority
	serverTime      time.Time
	timeErr         error
	commits         int
	commitConflicts int
	commitErr       error
	loadErr         error
}

type waveStartRepositoryHarness struct {
	repository *gamemocks.MockStartRepository
	state      *waveStartRepositoryState
}

func newWaveStartRepositoryHarness(
	t *testing.T,
	authority gameusecase.StartAuthority,
) *waveStartRepositoryHarness {
	t.Helper()

	state := &waveStartRepositoryState{
		authority:  cloneWaveStartAuthority(authority),
		serverTime: waveStartAuthorityTime(t, authority),
	}
	repository := gamemocks.NewMockStartRepository(t)
	repository.EXPECT().LoadWaveStartAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, gameusecase.StartScope) (gameusecase.StartAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.loadErr != nil {
				return gameusecase.StartAuthority{}, state.loadErr
			}
			return cloneWaveStartAuthority(state.authority), nil
		}).Maybe()
	repository.EXPECT().ReadWaveStartTime(mock.Anything).
		RunAndReturn(func(context.Context) (time.Time, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return state.serverTime, state.timeErr
		}).Maybe()
	repository.EXPECT().CommitWaveStart(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			record gameusecase.StartRecord,
		) (*gameusecase.StartRecord, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.commits++
			if state.commitConflicts > 0 {
				state.commitConflicts--
				return nil, false, domain.ErrConflict
			}
			if state.commitErr != nil {
				return nil, false, state.commitErr
			}
			if record.ExpectedWaveRevision != state.authority.WaveRevision ||
				state.authority.Current != nil {
				return nil, false, domain.ErrConflict
			}
			stored := cloneWaveStartRecord(record)
			state.authority.WaveRevision++
			state.authority.Wave = cloneWaveStartWave(record.Wave)
			state.authority.Current = &stored
			result := cloneWaveStartRecord(stored)
			return &result, true, nil
		}).Maybe()
	return &waveStartRepositoryHarness{repository: repository, state: state}
}

func waveStartAuthorityTime(tb testing.TB, authority gameusecase.StartAuthority) time.Time {
	tb.Helper()
	require.NotNil(tb, authority.Wave.ReadyWindow)
	return authority.Wave.ReadyWindow.OpenedAt.Add(10 * time.Second)
}

func (h *waveStartRepositoryHarness) commitCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.commits
}

func (h *waveStartRepositoryHarness) current() *gameusecase.StartRecord {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.state.authority.Current == nil {
		return nil
	}
	current := cloneWaveStartRecord(*h.state.authority.Current)
	return &current
}

func (h *waveStartRepositoryHarness) setCommitError(err error) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.commitErr = err
}

func (h *waveStartRepositoryHarness) setServerTime(value time.Time) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.serverTime = value
	h.state.timeErr = nil
}

func (h *waveStartRepositoryHarness) setServerTimeError(err error) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.timeErr = err
}

func waveStartFixture(
	t *testing.T,
	now time.Time,
) (gameusecase.StartAuthority, gameusecase.StartCommand) {
	t.Helper()

	tournamentID := waveStartID(500)
	waveID := waveStartID(501)
	waveRevisionID := domain.WaveRevisionID(waveStartID(502))
	windowID := waveStartID(503)
	windowRevisionID := domain.ReadyWindowRevisionID(waveStartID(504))
	participants := []uuid.UUID{
		waveStartID(510), waveStartID(511), waveStartID(512), waveStartID(513),
	}
	plannedWave := domain.Wave{
		ID: waveID, TournamentID: tournamentID, RevisionID: waveRevisionID,
		State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
			{ParticipantID: participants[2]}, {ParticipantID: participants[3]},
		},
	}
	openedAt := now.Add(-10 * time.Second)
	require.NoError(t, plannedWave.OpenReadyWindow(windowID, windowRevisionID, openedAt, now.Add(20*time.Second)))
	for _, participantID := range participants {
		_, err := plannedWave.MarkReady(windowID, participantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	revisions := domain.ReadyWindowSourceRevisions{
		WaveRevisionID: waveRevisionID, WaveRevision: 2,
		ProjectionRevisionID: waveStartID(521), ProjectionRevision: 4,
		ArtifactRevisionID: waveStartID(522), ArtifactRevision: 5,
	}
	scope := gameusecase.StartScope{TournamentID: tournamentID, WaveID: waveID, WindowID: windowID}
	games := []gameusecase.GameAuthority{
		waveStartGame(t, tournamentID, participants[0], participants[1], 600, 180),
		waveStartGame(t, tournamentID, participants[2], participants[3], 620, 180),
	}
	authority := gameusecase.StartAuthority{
		Scope: scope, WaveRevision: 7, Revisions: revisions,
		ReadinessRevisions: map[uuid.UUID]int64{
			participants[0]: 1, participants[1]: 1, participants[2]: 1, participants[3]: 1,
		},
		Wave: plannedWave, Games: games,
	}
	command := gameusecase.StartCommand{
		Scope: scope, CommandID: waveStartID(530), ActorID: waveStartID(529), ExpectedRevisions: revisions,
		ExecutionAuthority: authoritydomain.Identity{
			TournamentID: tournamentID, HolderID: waveStartID(523), LeaseID: waveStartID(524),
			Epoch: 1, ProcessKind: authoritydomain.ProcessAuthority,
		},
		ExpectedProjectionRevision: revisions.ProjectionRevision,
		RequestDigest:              [sha256.Size]byte{1},
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
) gameusecase.GameAuthority {
	t.Helper()

	seriesID := waveStartID(base)
	slotID := waveStartID(base + 1)
	gameID := waveStartID(base + 2)
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateReady,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStatePlanned,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	return gameusecase.GameAuthority{
		Scope: gamedomain.Scope{
			TournamentID: tournamentID, SeriesID: seriesID, SlotID: slotID, GameID: gameID,
		},
		ParticipantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID},
		Series:         series,
		AssignmentID:   waveStartID(base + 3), AssignmentRevision: 2,
		PlanRevisionID: waveStartID(base + 4), SnapshotID: waveStartID(base + 5),
		ContentDigest: sha256.Sum256([]byte{byte(base)}), DeadlineSeconds: deadlineSeconds,
	}
}

func cloneWaveStartAuthority(value gameusecase.StartAuthority) gameusecase.StartAuthority {
	clone := value
	clone.ByeParticipantID = cloneWaveStartParticipantID(value.ByeParticipantID)
	clone.Wave = cloneWaveStartWave(value.Wave)
	clone.ReadinessRevisions = cloneWaveStartReadiness(value.ReadinessRevisions)
	clone.Games = make([]gameusecase.GameAuthority, len(value.Games))
	for index, authorityGame := range value.Games {
		clone.Games[index] = authorityGame
		clone.Games[index].Series = cloneWaveStartSeriesExecution(authorityGame.Series)
	}
	if value.Current != nil {
		current := cloneWaveStartRecord(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneWaveStartRecord(value gameusecase.StartRecord) gameusecase.StartRecord {
	clone := value
	clone.ByeParticipantID = cloneWaveStartParticipantID(value.ByeParticipantID)
	clone.Wave = cloneWaveStartWave(value.Wave)
	clone.ReadinessRevisions = cloneWaveStartReadiness(value.ReadinessRevisions)
	clone.Games = make([]gamedomain.Started, len(value.Games))
	for index, startedGame := range value.Games {
		clone.Games[index] = startedGame
		clone.Games[index].Series = cloneWaveStartSeriesExecution(startedGame.Series)
	}
	return clone
}

func waveNewGameClock(t *testing.T, now time.Time) *gamemocks.MockWaveClock {
	t.Helper()

	clock := gamemocks.NewMockWaveClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func cloneWaveStartParticipantID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneWaveStartReadiness(source map[uuid.UUID]int64) map[uuid.UUID]int64 {
	if source == nil {
		return nil
	}
	clone := make(map[uuid.UUID]int64, len(source))
	for participantID, revision := range source {
		clone[participantID] = revision
	}
	return clone
}

func cloneWaveStartWave(value domain.Wave) domain.Wave {
	clone := value
	clone.Members = append([]domain.WaveMember(nil), value.Members...)
	if value.ReadyWindow != nil {
		window := *value.ReadyWindow
		window.ConsumedAt = cloneWaveStartTime(value.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = cloneWaveStartTime(value.StartedAt)
	clone.PausedAt = cloneWaveStartTime(value.PausedAt)
	return clone
}

func cloneWaveStartSeriesExecution(value seriesdomain.Execution) seriesdomain.Execution {
	clone := value
	clone.ResumeState = cloneWaveStartSeriesState(value.ResumeState)
	clone.Series = value.Series
	clone.Series.WinnerID = cloneWaveStartUUID(value.Series.WinnerID)
	clone.Series.CurrentScoreRevisionID = cloneWaveStartScoreRevision(value.Series.CurrentScoreRevisionID)
	clone.Series.CurrentResultRevisionID = cloneWaveStartResultRevision(value.Series.CurrentResultRevisionID)
	clone.Series.Slots = make([]domain.GameSlot, len(value.Series.Slots))
	for slotIndex, slot := range value.Series.Slots {
		clone.Series.Slots[slotIndex] = slot
		clone.Series.Slots[slotIndex].Attempts = make([]domain.Game, len(slot.Attempts))
		for attemptIndex, attempt := range slot.Attempts {
			clone.Series.Slots[slotIndex].Attempts[attemptIndex] = attempt
			clone.Series.Slots[slotIndex].Attempts[attemptIndex].WinnerID = cloneWaveStartUUID(attempt.WinnerID)
			clone.Series.Slots[slotIndex].Attempts[attemptIndex].ResultRevisionID =
				cloneWaveStartResultRevision(attempt.ResultRevisionID)
		}
	}
	return clone
}

func cloneWaveStartTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneWaveStartUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneWaveStartSeriesState(value *domain.SeriesState) *domain.SeriesState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneWaveStartScoreRevision(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneWaveStartResultRevision(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func waveStartID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "wave-start-%d", value))
}
