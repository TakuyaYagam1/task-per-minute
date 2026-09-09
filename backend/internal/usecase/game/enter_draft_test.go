package game_test

import (
	"errors"
	"testing"
	"time"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/google/uuid"
)

func testNormalPauseGraphEntryDraft(t *testing.T, pausedAt time.Time) {
	t.Helper()

	t.Run("rejects a stored Draft pause without its exact revision transition", func(t *testing.T) {
		t.Parallel()

		authority, command := draftNormalPauseFixture(t, pausedAt)
		leaderRepository := newNormalPauseRepositoryHarness(t, authority)
		leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored Draft pause: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Revision = stored.Expected.Draft.Revision
		stored.Graph.Draft.RevisionID = stored.Expected.Draft.RevisionID
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed {
			t.Fatalf("Enter() error = %v, changed = %v", err, changed)
		}

		authority, command = draftNormalPauseFixture(t, pausedAt)
		leaderRepository = newNormalPauseRepositoryHarness(t, authority)
		leader = gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
		stored, changed, err = leader.Enter(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.Transition == nil {
			t.Fatalf("prepare stored Draft transition: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Transition.ActorID = uuid.New()
		repository = newNormalPauseRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase = gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed {
			t.Fatalf("transition replay error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects changed lineage for a no-op Draft pause", func(t *testing.T) {
		t.Parallel()

		authority, command := completedDraftNormalPauseFixture(t, pausedAt)
		leaderRepository := newNormalPauseRepositoryHarness(t, authority)
		leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.State != draftusecase.ExecutionStateCompleted {
			t.Fatalf("prepare stored completed Draft: error = %v, changed = %v", err, changed)
		}
		if stored.Expected.DraftPreviousRevisionID != stored.Graph.Draft.PreviousRevisionID {
			t.Fatalf("stored lineage = %s, expected = %s", stored.Graph.Draft.PreviousRevisionID, stored.Expected.DraftPreviousRevisionID)
		}
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("fences an active Draft revision", func(t *testing.T) {
		t.Parallel()

		authority, command := draftNormalPauseFixture(t, pausedAt)
		invalidIdentity := command
		invalidIdentity.DraftResultRevisionID = command.CommandID
		invalidRepository := newNormalPauseRepositoryHarness(t, authority)
		invalidUseCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), invalidRepository, newPauseClock(t, pausedAt))
		if _, changed, err := invalidUseCase.Enter(t.Context(), invalidIdentity); !errors.Is(err, gameusecase.ErrInvalidNormalPauseGraph) || changed {
			t.Fatalf("invalid Draft result identity error = %v, changed = %v", err, changed)
		}
		invalidDraftIdentity := command
		invalidDraftIdentity.DraftResultRevisionID = authority.Graph.Draft.ID
		invalidRepository = newNormalPauseRepositoryHarness(t, authority)
		invalidUseCase = gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), invalidRepository, newPauseClock(t, pausedAt))
		if _, changed, err := invalidUseCase.Enter(t.Context(), invalidDraftIdentity); !errors.Is(err, gameusecase.ErrInvalidNormalPauseGraph) || changed {
			t.Fatalf("Draft-owned result identity error = %v, changed = %v", err, changed)
		}
		validRepository := newNormalPauseRepositoryHarness(t, authority)
		validUseCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), validRepository, newPauseClock(t, pausedAt))
		record, changed, err := validUseCase.Enter(t.Context(), command)
		if err != nil || !changed || record.Graph.Draft == nil || record.Graph.Draft.State != draftusecase.ExecutionStatePaused ||
			record.Graph.Draft.RevisionID != command.DraftResultRevisionID {
			t.Fatalf("valid Draft pause error = %v, changed = %v, graph = %+v", err, changed, record)
		}
		command.Expected.Draft.Revision++
		command.Expected.DraftPreviousRevisionID = uuid.New()
		repository := newNormalPauseRepositoryHarness(t, authority)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseGraphConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}

		cycleAuthority, cycleCommand := draftRevisionTwoNormalPauseFixture(t, pausedAt)
		if cycleCommand.Expected.DraftPreviousRevisionID != cycleAuthority.Graph.Draft.PreviousRevisionID {
			t.Fatalf("expected previous Draft revision = %s, want %s", cycleCommand.Expected.DraftPreviousRevisionID, cycleAuthority.Graph.Draft.PreviousRevisionID)
		}
		cycleIDs := []uuid.UUID{
			cycleAuthority.Graph.Draft.ID,
			cycleAuthority.Graph.Draft.RevisionID,
			cycleAuthority.Graph.Draft.PreviousRevisionID,
		}
		for _, cycleID := range cycleIDs {
			candidate := cycleCommand
			candidate.DraftResultRevisionID = cycleID
			repository := newNormalPauseRepositoryHarness(t, cycleAuthority)
			useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
			if _, changed, err := useCase.Enter(t.Context(), candidate); !errors.Is(err, gameusecase.ErrInvalidNormalPauseGraph) || changed || repository.writeCount() != 0 {
				t.Fatalf("cycle identity %s error = %v, changed = %v, writes = %d", cycleID, err, changed, repository.writeCount())
			}
		}
		stalePrevious := cycleCommand
		stalePrevious.Expected.DraftPreviousRevisionID = uuid.New()
		repository = newNormalPauseRepositoryHarness(t, cycleAuthority)
		useCase = gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if _, changed, err := useCase.Enter(t.Context(), stalePrevious); !errors.Is(err, gameusecase.ErrNormalPauseGraphConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("stale previous Draft revision error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}

		futureAuthority, futureCommand := draftNormalPauseFixture(t, pausedAt)
		futureAuthority.Graph.Draft.FirstActorDecision.DecidedAt = pausedAt.Add(time.Second)
		refreshNormalPauseRevisions(&futureAuthority, &futureCommand)
		futureCommand.DraftResultRevisionID = uuid.New()
		repository = newNormalPauseRepositoryHarness(t, futureAuthority)
		useCase = gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if _, changed, err := useCase.Enter(t.Context(), futureCommand); !errors.Is(err, gameusecase.ErrInvalidNormalPauseGraph) || changed || repository.writeCount() != 0 {
			t.Fatalf("future Draft evidence error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})
}
