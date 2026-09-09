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
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func TestNormalNoShowResolution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)

	t.Run("one ready participant wins and every unstarted Game is cancelled", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, true, false)
		repository := newNormalNoShowRepository(t, authority)
		usecase := gameusecase.NoShowNewUseCase(repository.repository, noShowNewGameClock(t, now))

		resolution, changed, err := usecase.Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, resolution.Validate())
		require.Equal(t, domain.NormalNoShowActionReopenWave, resolution.Action)
		require.Equal(t, domain.WaveStateReadyWindowExpired, resolution.Wave.State)
		require.False(t, memberReady(resolution.Wave, authority.Series.Series.FirstParticipantID))
		require.Equal(t, domain.SeriesStateCompleted, resolution.Series.Series.State)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolution.Series.Series.WinnerID)
		require.Equal(t, domain.SeriesScore{FirstParticipantWins: 1}, resolution.Series.Series.Score)
		require.Len(t, resolution.GameRevisions, 1)
		require.Equal(t, 1, resolution.GameRevisions[0].Ordinal)
		require.Equal(t, domain.GameStateCancelled, resolution.GameRevisions[0].State)
		require.Equal(t, domain.GameResultReasonSeriesCancelled, resolution.GameRevisions[0].Reason)
		require.Equal(t, 2, resolution.ScoreRevision.Ordinal)
		require.Equal(t, 3, resolution.SeriesRevision.Ordinal)
		attempt := resolution.Series.Series.Slots[0].Attempts[0]
		require.Equal(t, domain.GameStateCancelled, attempt.State)
		require.Equal(t, domain.GameResultReasonSeriesCancelled, attempt.ResultReason)
		require.Nil(t, attempt.WinnerID)

		repeated, changed, err := usecase.Resolve(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, resolution.CommandID, repeated.CommandID)
		require.Equal(t, 1, repository.commitCount())
	})

	t.Run("two absent participants never get an automatic winner", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, false, false)
		repository := newNormalNoShowRepository(t, authority)
		resolution, changed, err := gameusecase.NoShowNewUseCase(
			repository.repository,
			noShowNewGameClock(t, now),
		).Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.NormalNoShowActionPauseWave, resolution.Action)
		require.Equal(t, domain.SeriesStateCancelled, resolution.Series.Series.State)
		require.Nil(t, resolution.Series.Series.WinnerID)
		require.Equal(t, domain.SeriesScore{}, resolution.Series.Series.Score)
		require.Nil(t, resolution.SeriesRevision.WinnerID)
	})

	t.Run("later BO3 readiness preserves prior score for the ready winner", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, false, true)
		authority.Series.Series.Format = domain.SeriesFormatBO3
		authority.Series.Series.State = domain.SeriesStateActive
		authority.Series.Series.Score = domain.SeriesScore{FirstParticipantWins: 1}
		command.ExpectedSeriesState = domain.SeriesStateActive
		repository := newNormalNoShowRepository(t, authority)

		resolution, changed, err := gameusecase.NoShowNewUseCase(
			repository.repository,
			noShowNewGameClock(t, now),
		).Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.SeriesScore{
			FirstParticipantWins: 1, SecondParticipantWins: 2,
		}, resolution.Series.Series.Score)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolution.Series.Series.WinnerID)
	})

	t.Run("replay readiness may finish only through the ready state", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, true, false)
		authority.Series.Series.State = domain.SeriesStateReplayRequired
		command.ExpectedSeriesState = domain.SeriesStateReplayRequired
		repository := newNormalNoShowRepository(t, authority)

		resolution, changed, err := gameusecase.NoShowNewUseCase(
			repository.repository,
			noShowNewGameClock(t, now),
		).Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.SeriesStateCompleted, resolution.Series.Series.State)
	})

	t.Run("cutoff and evidence fail closed", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, true, false)
		atCutoff := *authority.Wave.ReadyWindow
		cutoffRepository := newNormalNoShowRepository(t, authority)
		resolution, changed, err := gameusecase.NoShowNewUseCase(
			cutoffRepository.repository,
			noShowNewGameClock(t, atCutoff.Deadline),
		).Resolve(t.Context(), command)
		require.Nil(t, resolution)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrNormalNoShowCutoff)
		require.Equal(t, 0, cutoffRepository.commitCount())

		invalidEvidence := command
		invalidEvidence.GameResultRevisionIDs = nil
		evidenceRepository := newNormalNoShowRepository(t, authority)
		resolution, changed, err = gameusecase.NoShowNewUseCase(
			evidenceRepository.repository,
			noShowNewGameClock(t, now),
		).Resolve(t.Context(), invalidEvidence)
		require.Nil(t, resolution)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrInvalidNormalNoShow)
		require.Equal(t, 0, evidenceRepository.commitCount())

		bothReady, bothReadyCommand := normalNoShowFixture(t, now, true, true)
		bothReadyRepository := newNormalNoShowRepository(t, bothReady)
		resolution, changed, err = gameusecase.NoShowNewUseCase(
			bothReadyRepository.repository,
			noShowNewGameClock(t, now),
		).Resolve(t.Context(), bothReadyCommand)
		require.Nil(t, resolution)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrNormalNoShowNotRequired)
		require.Equal(t, 0, bothReadyRepository.commitCount())
	})
}

type normalNoShowRepositoryState struct {
	mu        sync.Mutex
	authority gameusecase.NoShowAuthority
	commits   int
}

type normalNoShowRepositoryHarness struct {
	repository *gamemocks.MockNoShowRepository
	state      *normalNoShowRepositoryState
}

func newNormalNoShowRepository(
	t *testing.T,
	authority gameusecase.NoShowAuthority,
) *normalNoShowRepositoryHarness {
	t.Helper()

	state := &normalNoShowRepositoryState{authority: cloneNormalNoShowAuthority(authority)}
	repository := gamemocks.NewMockNoShowRepository(t)
	repository.EXPECT().LoadNormalNoShowAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, domain.NormalNoShowScope) (gameusecase.NoShowAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return cloneNormalNoShowAuthority(state.authority), nil
		}).Maybe()
	repository.EXPECT().CommitNormalNoShow(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			resolution gameusecase.NoShowResolution,
		) (*gameusecase.NoShowResolution, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if resolution.ExpectedAuthorityRevision != state.authority.Revision ||
				state.authority.Current != nil {
				return nil, false, domain.ErrConflict
			}
			state.commits++
			stored := cloneNormalNoShowResolution(resolution)
			state.authority.Revision++
			state.authority.Wave = cloneWave(resolution.Wave)
			state.authority.Series = cloneSeriesExecution(resolution.Series)
			state.authority.CurrentOrdinal = resolution.SeriesRevision.Ordinal
			state.authority.Current = &stored
			return &stored, true, nil
		}).Maybe()
	return &normalNoShowRepositoryHarness{repository: repository, state: state}
}

func (h *normalNoShowRepositoryHarness) commitCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.commits
}

func normalNoShowFixture(
	t *testing.T,
	now time.Time,
	firstReady bool,
	secondReady bool,
) (gameusecase.NoShowAuthority, gameusecase.NoShowCommand) {
	t.Helper()

	wave := domain.Wave{
		ID:           noShowFixtureID(2),
		TournamentID: noShowFixtureID(1),
		RevisionID:   domain.WaveRevisionID(noShowFixtureID(3)),
		State:        domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: noShowFixtureID(20)},
			{ParticipantID: noShowFixtureID(21)},
		},
	}
	windowID := noShowFixtureID(31)
	windowRevisionID := domain.ReadyWindowRevisionID(noShowFixtureID(32))
	openedAt := now.Add(-31 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		windowID,
		windowRevisionID,
		openedAt,
		now.Add(-time.Second),
	))
	if firstReady {
		_, err := wave.MarkReady(windowID, wave.Members[0].ParticipantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	if secondReady {
		_, err := wave.MarkReady(windowID, wave.Members[1].ParticipantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	seriesID := noShowFixtureID(200)
	slotID := noShowFixtureID(201)
	gameID := noShowFixtureID(202)
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: wave.TournamentID,
		FirstParticipantID:  wave.Members[0].ParticipantID,
		SecondParticipantID: wave.Members[1].ParticipantID,
		Format:              domain.SeriesFormatBO1, State: domain.SeriesStateReady,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStatePlanned,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	scope := domain.NormalNoShowScope{
		TournamentID: wave.TournamentID, WaveID: wave.ID,
		WindowID: windowID, SeriesID: seriesID,
	}
	authority := gameusecase.NoShowAuthority{
		Scope: scope, Revision: 4, Wave: wave, Series: series, CurrentOrdinal: 0,
	}
	command := gameusecase.NoShowCommand{
		Scope: scope, CommandID: noShowFixtureID(210),
		ExpectedWaveRevisionID:   wave.RevisionID,
		ExpectedWindowRevisionID: windowRevisionID,
		ExpectedSeriesState:      series.Series.State,
		GameResultRevisionIDs: []domain.OfficialResultRevisionID{
			domain.OfficialResultRevisionID(noShowFixtureID(211)),
		},
		ScoreRevisionID:        domain.SeriesScoreRevisionID(noShowFixtureID(212)),
		SeriesResultRevisionID: domain.OfficialResultRevisionID(noShowFixtureID(213)),
	}
	return authority, command
}

func noShowFixtureID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("36000000-0000-0000-0000-%012d", value))
}

func memberReady(wave domain.Wave, participantID uuid.UUID) bool {
	for _, member := range wave.Members {
		if member.ParticipantID == participantID {
			return member.Ready
		}
	}
	return false
}

func cloneWave(value domain.Wave) domain.Wave {
	clone := value
	clone.Members = append([]domain.WaveMember(nil), value.Members...)
	if value.ReadyWindow != nil {
		window := *value.ReadyWindow
		window.ConsumedAt = cloneTimePointer(value.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = cloneTimePointer(value.StartedAt)
	clone.PausedAt = cloneTimePointer(value.PausedAt)
	return clone
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneNormalNoShowAuthority(value gameusecase.NoShowAuthority) gameusecase.NoShowAuthority {
	clone := value
	clone.Wave = cloneWave(value.Wave)
	clone.Series = cloneSeriesExecution(value.Series)
	if value.Current != nil {
		current := cloneNormalNoShowResolution(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneNormalNoShowResolution(value gameusecase.NoShowResolution) gameusecase.NoShowResolution {
	clone := value
	clone.Wave = cloneWave(value.Wave)
	clone.Series = cloneSeriesExecution(value.Series)
	clone.GameRevisions = append([]domain.NormalNoShowGameRevision(nil), value.GameRevisions...)
	clone.SeriesRevision.WinnerID = noShowCloneUUIDPointer(value.SeriesRevision.WinnerID)
	return clone
}

func cloneSeriesExecution(value seriesdomain.Execution) seriesdomain.Execution {
	clone := value
	clone.Series = value.Series
	clone.Series.WinnerID = noShowCloneUUIDPointer(value.Series.WinnerID)
	if value.Series.CurrentScoreRevisionID != nil {
		revisionID := *value.Series.CurrentScoreRevisionID
		clone.Series.CurrentScoreRevisionID = &revisionID
	}
	if value.Series.CurrentResultRevisionID != nil {
		revisionID := *value.Series.CurrentResultRevisionID
		clone.Series.CurrentResultRevisionID = &revisionID
	}
	clone.Series.Slots = make([]domain.GameSlot, len(value.Series.Slots))
	for index, slot := range value.Series.Slots {
		clone.Series.Slots[index] = slot
		clone.Series.Slots[index].Attempts = make([]domain.Game, len(slot.Attempts))
		for attemptIndex, attempt := range slot.Attempts {
			clone.Series.Slots[index].Attempts[attemptIndex] = attempt
			clone.Series.Slots[index].Attempts[attemptIndex].WinnerID = noShowCloneUUIDPointer(attempt.WinnerID)
			if attempt.ResultRevisionID != nil {
				revisionID := *attempt.ResultRevisionID
				clone.Series.Slots[index].Attempts[attemptIndex].ResultRevisionID = &revisionID
			}
		}
	}
	if value.ResumeState != nil {
		resumeState := *value.ResumeState
		clone.ResumeState = &resumeState
	}
	return clone
}

func noShowCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
