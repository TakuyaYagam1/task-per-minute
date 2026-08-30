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

func TestNormalNoShowResolution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)

	t.Run("one ready participant wins and every unstarted Game is cancelled", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, true, false)
		repository := &normalNoShowRepositoryFake{authority: authority}
		usecase := arena.NewNormalNoShowUseCase(repository, fixedArenaClock{now: now})

		resolution, changed, err := usecase.Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, resolution.Validate())
		require.Equal(t, arena.NormalNoShowActionReopenWave, resolution.Action)
		require.Equal(t, domain.ArenaWaveStateReadyWindowExpired, resolution.Wave.State)
		require.False(t, task036MemberReady(resolution.Wave, authority.Series.Series.FirstParticipantID))
		require.Equal(t, domain.ArenaSeriesStateCompleted, resolution.Series.Series.State)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolution.Series.Series.WinnerID)
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 1}, resolution.Series.Series.Score)
		require.Len(t, resolution.GameRevisions, 1)
		require.Equal(t, 1, resolution.GameRevisions[0].Ordinal)
		require.Equal(t, domain.ArenaGameStateCancelled, resolution.GameRevisions[0].State)
		require.Equal(t, domain.ArenaGameResultReasonSeriesCancelled, resolution.GameRevisions[0].Reason)
		require.Equal(t, 2, resolution.ScoreRevision.Ordinal)
		require.Equal(t, 3, resolution.SeriesRevision.Ordinal)
		attempt := resolution.Series.Series.Slots[0].Attempts[0]
		require.Equal(t, domain.ArenaGameStateCancelled, attempt.State)
		require.Equal(t, domain.ArenaGameResultReasonSeriesCancelled, attempt.ResultReason)
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
		repository := &normalNoShowRepositoryFake{authority: authority}
		resolution, changed, err := arena.NewNormalNoShowUseCase(
			repository,
			fixedArenaClock{now: now},
		).Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, arena.NormalNoShowActionPauseWave, resolution.Action)
		require.Equal(t, domain.ArenaSeriesStateCancelled, resolution.Series.Series.State)
		require.Nil(t, resolution.Series.Series.WinnerID)
		require.Equal(t, domain.ArenaSeriesScore{}, resolution.Series.Series.Score)
		require.Nil(t, resolution.SeriesRevision.WinnerID)
	})

	t.Run("later BO3 readiness preserves prior score for the ready winner", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, false, true)
		authority.Series.Series.Format = domain.ArenaSeriesFormatBO3
		authority.Series.Series.State = domain.ArenaSeriesStateActive
		authority.Series.Series.Score = domain.ArenaSeriesScore{FirstParticipantWins: 1}
		command.ExpectedSeriesState = domain.ArenaSeriesStateActive
		repository := &normalNoShowRepositoryFake{authority: authority}

		resolution, changed, err := arena.NewNormalNoShowUseCase(
			repository,
			fixedArenaClock{now: now},
		).Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaSeriesScore{
			FirstParticipantWins: 1, SecondParticipantWins: 2,
		}, resolution.Series.Series.Score)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolution.Series.Series.WinnerID)
	})

	t.Run("replay readiness may finish only through the ready state", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, true, false)
		authority.Series.Series.State = domain.ArenaSeriesStateReplayRequired
		command.ExpectedSeriesState = domain.ArenaSeriesStateReplayRequired
		repository := &normalNoShowRepositoryFake{authority: authority}

		resolution, changed, err := arena.NewNormalNoShowUseCase(
			repository,
			fixedArenaClock{now: now},
		).Resolve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaSeriesStateCompleted, resolution.Series.Series.State)
	})

	t.Run("cutoff and evidence fail closed", func(t *testing.T) {
		t.Parallel()

		authority, command := normalNoShowFixture(t, now, true, false)
		atCutoff := *authority.Wave.ReadyWindow
		cutoffRepository := &normalNoShowRepositoryFake{authority: authority}
		resolution, changed, err := arena.NewNormalNoShowUseCase(
			cutoffRepository,
			fixedArenaClock{now: atCutoff.Deadline},
		).Resolve(t.Context(), command)
		require.Nil(t, resolution)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrNormalNoShowCutoff)
		require.Equal(t, 0, cutoffRepository.commitCount())

		invalidEvidence := command
		invalidEvidence.GameResultRevisionIDs = nil
		evidenceRepository := &normalNoShowRepositoryFake{authority: authority}
		resolution, changed, err = arena.NewNormalNoShowUseCase(
			evidenceRepository,
			fixedArenaClock{now: now},
		).Resolve(t.Context(), invalidEvidence)
		require.Nil(t, resolution)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidNormalNoShow)
		require.Equal(t, 0, evidenceRepository.commitCount())

		bothReady, bothReadyCommand := normalNoShowFixture(t, now, true, true)
		bothReadyRepository := &normalNoShowRepositoryFake{authority: bothReady}
		resolution, changed, err = arena.NewNormalNoShowUseCase(
			bothReadyRepository,
			fixedArenaClock{now: now},
		).Resolve(t.Context(), bothReadyCommand)
		require.Nil(t, resolution)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrNormalNoShowNotRequired)
		require.Equal(t, 0, bothReadyRepository.commitCount())
	})
}

type normalNoShowRepositoryFake struct {
	mu        sync.Mutex
	authority arena.NormalNoShowAuthority
	commits   int
}

func (r *normalNoShowRepositoryFake) LoadNormalNoShowAuthority(
	_ context.Context,
	_ arena.NormalNoShowScope,
) (arena.NormalNoShowAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneNormalNoShowAuthority(r.authority), nil
}

func (r *normalNoShowRepositoryFake) CommitNormalNoShow(
	_ context.Context,
	resolution arena.NormalNoShowResolution,
) (*arena.NormalNoShowResolution, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if resolution.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	r.commits++
	stored := cloneNormalNoShowResolution(resolution)
	r.authority.Revision++
	r.authority.Wave = cloneTask036Wave(resolution.Wave)
	r.authority.Series = cloneTask036SeriesExecution(resolution.Series)
	r.authority.CurrentOrdinal = resolution.SeriesRevision.Ordinal
	r.authority.Current = &stored
	return &stored, true, nil
}

func (r *normalNoShowRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func normalNoShowFixture(
	t *testing.T,
	now time.Time,
	firstReady bool,
	secondReady bool,
) (arena.NormalNoShowAuthority, arena.NormalNoShowCommand) {
	t.Helper()

	windowAuthority, openCommand := readyWindowFixture()
	wave := cloneTask036Wave(windowAuthority.Wave)
	openedAt := now.Add(-31 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		openCommand.WindowID,
		openCommand.WindowRevisionID,
		openedAt,
		now.Add(-time.Second),
	))
	if firstReady {
		_, err := wave.MarkReady(openCommand.WindowID, wave.Members[0].ParticipantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	if secondReady {
		_, err := wave.MarkReady(openCommand.WindowID, wave.Members[1].ParticipantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	seriesID := task036ID(200)
	slotID := task036ID(201)
	gameID := task036ID(202)
	series := arena.SeriesExecution{Series: domain.ArenaSeries{
		ID: seriesID, TournamentID: wave.TournamentID,
		FirstParticipantID:  wave.Members[0].ParticipantID,
		SecondParticipantID: wave.Members[1].ParticipantID,
		Format:              domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateReady,
		Slots: []domain.ArenaGameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStatePlanned,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	scope := arena.NormalNoShowScope{
		TournamentID: wave.TournamentID, WaveID: wave.ID,
		WindowID: openCommand.WindowID, SeriesID: seriesID,
	}
	authority := arena.NormalNoShowAuthority{
		Scope: scope, Revision: 4, Wave: wave, Series: series, CurrentOrdinal: 0,
	}
	command := arena.NormalNoShowCommand{
		Scope: scope, CommandID: task036ID(210),
		ExpectedWaveRevisionID:   wave.RevisionID,
		ExpectedWindowRevisionID: openCommand.WindowRevisionID,
		ExpectedSeriesState:      series.Series.State,
		GameResultRevisionIDs: []domain.ArenaOfficialResultRevisionID{
			domain.ArenaOfficialResultRevisionID(task036ID(211)),
		},
		ScoreRevisionID:        domain.ArenaSeriesScoreRevisionID(task036ID(212)),
		SeriesResultRevisionID: domain.ArenaOfficialResultRevisionID(task036ID(213)),
	}
	return authority, command
}

func cloneNormalNoShowAuthority(value arena.NormalNoShowAuthority) arena.NormalNoShowAuthority {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	clone.Series = cloneTask036SeriesExecution(value.Series)
	if value.Current != nil {
		current := cloneNormalNoShowResolution(*value.Current)
		clone.Current = &current
	}
	return clone
}

func cloneNormalNoShowResolution(value arena.NormalNoShowResolution) arena.NormalNoShowResolution {
	clone := value
	clone.Wave = cloneTask036Wave(value.Wave)
	clone.Series = cloneTask036SeriesExecution(value.Series)
	clone.GameRevisions = append([]arena.NormalNoShowGameRevision(nil), value.GameRevisions...)
	clone.SeriesRevision.WinnerID = cloneTask036UUIDPointer(value.SeriesRevision.WinnerID)
	return clone
}

func cloneTask036SeriesExecution(value arena.SeriesExecution) arena.SeriesExecution {
	clone := value
	clone.Series = value.Series
	clone.Series.WinnerID = cloneTask036UUIDPointer(value.Series.WinnerID)
	if value.Series.CurrentScoreRevisionID != nil {
		revisionID := *value.Series.CurrentScoreRevisionID
		clone.Series.CurrentScoreRevisionID = &revisionID
	}
	if value.Series.CurrentResultRevisionID != nil {
		revisionID := *value.Series.CurrentResultRevisionID
		clone.Series.CurrentResultRevisionID = &revisionID
	}
	clone.Series.Slots = make([]domain.ArenaGameSlot, len(value.Series.Slots))
	for index, slot := range value.Series.Slots {
		clone.Series.Slots[index] = slot
		clone.Series.Slots[index].Attempts = make([]domain.ArenaGame, len(slot.Attempts))
		for attemptIndex, attempt := range slot.Attempts {
			clone.Series.Slots[index].Attempts[attemptIndex] = attempt
			clone.Series.Slots[index].Attempts[attemptIndex].WinnerID = cloneTask036UUIDPointer(attempt.WinnerID)
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

func cloneTask036UUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

var _ arena.NormalNoShowRepository = (*normalNoShowRepositoryFake)(nil)
