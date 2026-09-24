package pause_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testPauseResumeStoredRecords(t *testing.T, resumedAt time.Time) {
	t.Helper()

	t.Run("rejects structurally valid stored resume records with wrong transitions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.PauseResumeRecord)
		}{
			{name: "Tournament time was not updated", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Tournament.UpdatedAt = record.ResumedAt.Add(-time.Second)
			}},
			{name: "Tournament restored to another origin", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Tournament.State = domain.TournamentStatePlayoffs
			}},
			{name: "Wave state changed without revision", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Wave.Revision = record.Expected.WaveRevision
			}},
			{name: "Series state changed without revision", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Series[0].Revision = record.Expected.Series[0].Revision
			}},
			{name: "Game state changed without revision", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Games[0].Revision = record.Expected.Games[0].Revision
			}},
			{name: "Tournament scope differs from record", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Scope.Authority.HolderID = uuid.New()
			}},
			{name: "same-revision Presence is disconnected", mutate: func(record *gameusecase.PauseResumeRecord) {
				disconnectedAt := record.ResumedAt
				record.Graph.Presence[0].State = pausedomain.PresenceStateDisconnected
				record.Graph.Presence[0].DisconnectedAt = &disconnectedAt
				record.Graph.Presence[0].UpdatedAt = disconnectedAt
			}},
			{name: "same-revision Reconnect is open", mutate: func(record *gameusecase.PauseResumeRecord) {
				record.Graph.Reconnect[0].State = pausedomain.ReconnectStateOpen
				record.Graph.Reconnect[0].ClosedAt = nil
			}},
			{name: "frozen deadline shifted at another time", mutate: func(record *gameusecase.PauseResumeRecord) {
				shiftedAt := record.ResumedAt.Add(-time.Second)
				shiftedDeadline := shiftedAt.Add(record.Graph.FrozenDeadlines[0].Remaining)
				record.Graph.FrozenDeadlines[0].ResumedAt = &shiftedAt
				record.Graph.FrozenDeadlines[0].ResumedDeadline = &shiftedDeadline
				record.Graph.Games[0].Deadline = &shiftedDeadline
			}},
			{name: "Wave contains future durable evidence", mutate: func(record *gameusecase.PauseResumeRecord) {
				startedAt := record.ResumedAt.Add(time.Second)
				record.Graph.Wave.Wave.StartedAt = &startedAt
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pauseResumeFixture(t, resumedAt)
				leaderRepository := newPauseResumeRepositoryHarness(t, authority)
				leader := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
				stored, changed, err := leader.Resume(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("prepare stored resume: error = %v, changed = %v", err, changed)
				}
				test.mutate(stored)
				repository := newPauseResumeRepositoryHarness(t, authority)
				repository.storeCommand(*stored)
				useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt.Add(time.Second)))
				if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed {
					t.Fatalf("Resume() error = %v, changed = %v", err, changed)
				}
			})
		}
	})

	t.Run("rejects a stored Draft resume without its exact revision lineage", func(t *testing.T) {
		t.Parallel()

		authority, command := draftPauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryHarness(t, authority)
		leader := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored Draft resume: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Revision = stored.Expected.Draft.Revision
		stored.Graph.Draft.RevisionID = stored.Expected.Draft.RevisionID
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt.Add(time.Second)))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed {
			t.Fatalf("Resume() error = %v, changed = %v", err, changed)
		}

		authority, command = draftPauseResumeFixture(t, resumedAt)
		leaderRepository = newPauseResumeRepositoryHarness(t, authority)
		leader = gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
		stored, changed, err = leader.Resume(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.Transition == nil {
			t.Fatalf("prepare stored Draft transition: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Transition.Reason = "wrong reason"
		repository = newPauseResumeRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase = gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt.Add(time.Second)))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed {
			t.Fatalf("transition replay error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects an adopted Game deadline that diverges from its source clock", func(t *testing.T) {
		t.Parallel()

		authority, command := sourcePauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryHarness(t, authority)
		leader := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored source resume: error = %v, changed = %v", err, changed)
		}
		wrongDeadline := stored.Graph.Games[0].Deadline.Add(time.Nanosecond)
		stored.Graph.Games[0].Deadline = &wrongDeadline
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt.Add(time.Second)))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects changed lineage for a no-op Draft resume", func(t *testing.T) {
		t.Parallel()

		pausedAt := resumedAt.Add(-2 * time.Minute)
		pauseAuthority, pauseCommand := completedDraftNormalPauseFixture(t, pausedAt)
		authority, command := pauseResumeFromNormalAuthority(t, pauseAuthority, pauseCommand, pausedAt)
		leaderRepository := newPauseResumeRepositoryHarness(t, authority)
		leader := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.State != draftusecase.ExecutionStateCompleted {
			t.Fatalf("prepare stored completed Draft: error = %v, changed = %v", err, changed)
		}
		if stored.Expected.DraftPreviousRevisionID != stored.Graph.Draft.PreviousRevisionID {
			t.Fatalf("stored lineage = %s, expected = %s", stored.Graph.Draft.PreviousRevisionID, stored.Expected.DraftPreviousRevisionID)
		}
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt.Add(time.Second)))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})
}
