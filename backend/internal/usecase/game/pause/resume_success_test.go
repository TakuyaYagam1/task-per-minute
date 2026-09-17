package pause_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testPauseResumeSuccess(t *testing.T, resumedAt time.Time) {
	t.Helper()

	t.Run("restores the graph and shifts each frozen deadline once", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		liveAt := resumedAt.Add(-30 * time.Second)
		authority.Presence[0].PresenceEpoch++
		authority.Presence[0].Revision++
		authority.Presence[0].ConnectedAt = liveAt
		authority.Presence[0].UpdatedAt = liveAt
		command.Expected = gameusecase.PauseResumeExpectationFrom(authority)
		repository := newPauseResumeRepositoryHarness(t, authority)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))

		record, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Resume() error = %v, changed = %v", err, changed)
		}
		if record.State != gameusecase.PauseStateResumed || record.Graph.Tournament.State != domain.TournamentStateSwiss ||
			record.Graph.Tournament.PausedFromState != nil || record.Graph.Wave.Wave.State != domain.WaveStateActive ||
			record.Graph.Series[0].Execution.Series.State != domain.SeriesStateActive || record.Graph.Series[0].Execution.ResumeState != nil ||
			record.Graph.Games[0].Game.State != domain.GameStateActive || record.Graph.Games[0].ResumeState != nil ||
			record.Graph.Games[0].Deadline == nil || !record.Graph.Games[0].Deadline.Equal(resumedAt.Add(time.Minute)) ||
			record.Graph.DeadlinesSuppressed || len(record.Graph.FrozenDeadlines) != 1 ||
			record.Graph.FrozenDeadlines[0].ResumedAt == nil || !record.Graph.FrozenDeadlines[0].ResumedAt.Equal(resumedAt) ||
			!reflect.DeepEqual(record.Graph.Presence, authority.Presence) || !reflect.DeepEqual(record.Graph.Reconnect, authority.Reconnect) ||
			!reflect.DeepEqual(record.Graph.Counters, authority.Counters) || record.Graph.TerminalActionRevision != authority.TerminalActionRevision {
			t.Fatalf("resumed record = %+v", record)
		}
		deadline := *record.Graph.Games[0].Deadline
		record.Graph.Games[0].Deadline = nil
		retried, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || changed || retried.Graph.Games[0].Deadline == nil || !retried.Graph.Games[0].Deadline.Equal(deadline) || repository.writeCount() != 1 {
			t.Fatalf("retry error = %v, changed = %v, record = %+v, writes = %d", err, changed, retried, repository.writeCount())
		}
	})

	t.Run("shifts an open ready-window deadline exactly once", func(t *testing.T) {
		t.Parallel()

		authority, command := readyWindowPauseResumeFixture(t, resumedAt)
		remaining := authority.FrozenDeadlines[0].Remaining
		repository := newPauseResumeRepositoryHarness(t, authority)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
		record, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || !changed || record.Graph.Wave.Wave.ReadyWindow == nil ||
			!record.Graph.Wave.Wave.ReadyWindow.Deadline.Equal(resumedAt.Add(remaining)) ||
			len(record.Graph.FrozenDeadlines) != 1 || record.Graph.FrozenDeadlines[0].Kind != gameusecase.PauseDeadlineReadyWindow {
			t.Fatalf("Resume() error = %v, changed = %v, graph = %+v", err, changed, record)
		}
	})

	t.Run("restores an active Draft and fences its revision", func(t *testing.T) {
		t.Parallel()

		authority, command := draftPauseResumeFixture(t, resumedAt)
		remaining := authority.FrozenDeadlines[0].Remaining
		repository := newPauseResumeRepositoryHarness(t, authority)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
		record, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || !changed || record.Graph.Draft == nil || record.Graph.Draft.State != draftusecase.ExecutionStateActive ||
			record.Graph.Draft.AbsoluteDeadline == nil || !record.Graph.Draft.AbsoluteDeadline.Equal(resumedAt.Add(remaining)) ||
			record.Graph.Draft.RevisionID != command.DraftResultRevisionID ||
			len(record.Graph.FrozenDeadlines) != 1 || record.Graph.FrozenDeadlines[0].Kind != gameusecase.PauseDeadlineDraft {
			t.Fatalf("Resume() error = %v, changed = %v, graph = %+v", err, changed, record)
		}

		authority, command = draftPauseResumeFixture(t, resumedAt)
		invalidIdentity := command
		invalidIdentity.DraftResultRevisionID = command.CommandID
		invalidRepository := newPauseResumeRepositoryHarness(t, authority)
		invalidUseCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), invalidRepository, newPauseClock(t, resumedAt))
		if _, changed, err := invalidUseCase.Resume(t.Context(), invalidIdentity); !errors.Is(err, gameusecase.ErrInvalidPauseResume) || changed {
			t.Fatalf("invalid Draft result identity error = %v, changed = %v", err, changed)
		}
		invalidDraftIdentity := command
		invalidDraftIdentity.DraftResultRevisionID = authority.Pause.Graph.Draft.ID
		invalidRepository = newPauseResumeRepositoryHarness(t, authority)
		invalidUseCase = gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), invalidRepository, newPauseClock(t, resumedAt))
		if _, changed, err := invalidUseCase.Resume(t.Context(), invalidDraftIdentity); !errors.Is(err, gameusecase.ErrInvalidPauseResume) || changed {
			t.Fatalf("Draft-owned result identity error = %v, changed = %v", err, changed)
		}
		command.Expected.Draft.Revision++
		repository = newPauseResumeRepositoryHarness(t, authority)
		useCase = gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("stale Draft Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}

		cyclePauseAuthority, cyclePauseCommand := draftRevisionTwoNormalPauseFixture(t, resumedAt.Add(-2*time.Minute))
		cycleAuthority, cycleCommand := pauseResumeFromNormalAuthority(t, cyclePauseAuthority, cyclePauseCommand, resumedAt.Add(-2*time.Minute))
		if cycleCommand.Expected.DraftPreviousRevisionID != cycleAuthority.Pause.Graph.Draft.PreviousRevisionID {
			t.Fatalf("expected previous Draft revision = %s, want %s", cycleCommand.Expected.DraftPreviousRevisionID, cycleAuthority.Pause.Graph.Draft.PreviousRevisionID)
		}
		cycleIDs := []uuid.UUID{
			cycleAuthority.Pause.Graph.Draft.ID,
			cycleAuthority.Pause.Graph.Draft.RevisionID,
			cycleAuthority.Pause.Graph.Draft.PreviousRevisionID,
		}
		for _, cycleID := range cycleIDs {
			candidate := cycleCommand
			candidate.DraftResultRevisionID = cycleID
			repository := newPauseResumeRepositoryHarness(t, cycleAuthority)
			useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
			if _, changed, err := useCase.Resume(t.Context(), candidate); !errors.Is(err, gameusecase.ErrInvalidPauseResume) || changed || repository.writeCount() != 0 {
				t.Fatalf("cycle identity %s error = %v, changed = %v, writes = %d", cycleID, err, changed, repository.writeCount())
			}
		}
		stalePrevious := cycleCommand
		stalePrevious.Expected.DraftPreviousRevisionID = uuid.New()
		repository = newPauseResumeRepositoryHarness(t, cycleAuthority)
		useCase = gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
		if _, changed, err := useCase.Resume(t.Context(), stalePrevious); !errors.Is(err, gameusecase.ErrPauseResumeConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("stale previous Draft revision error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})
}
