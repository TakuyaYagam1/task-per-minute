package draft_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasedraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestDraftPauseAndEpochRecovery(t *testing.T) {
	t.Parallel()

	t.Run("shifts the exact remaining turn window across operator pause", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 16, 0, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryHarness(t, initial)
		pausedAt := startedAt.Add(5 * time.Second)
		pauseUseCase := usecasedraft.NewRecoveryUseCase(repository.mock, newDraftClock(t, pausedAt))
		pauseCommand := usecasedraft.PauseCommand{
			DraftID: initial.ID, ExpectedRevisionID: initial.RevisionID,
			ExpectedRevision: initial.Revision, ExpectedServiceEpoch: initial.ServiceEpoch,
			CommandID: task030ID(101), ResultRevisionID: task030ID(102),
			ActorID: task030ID(103), Reason: "operator network check",
		}
		paused, err := pauseUseCase.Pause(t.Context(), pauseCommand)
		if err != nil {
			t.Fatalf("Pause() error = %v", err)
		}
		if !paused.Changed || paused.Draft.State != usecasedraft.ExecutionStatePaused ||
			paused.Draft.PausedRemaining != 10*time.Second || paused.Draft.AbsoluteDeadline != nil ||
			paused.NextTimeout != nil || paused.Draft.Recovery == nil ||
			paused.Draft.Recovery.Policy != usecasedraft.RecoveryPolicyShiftRemaining {
			t.Fatalf("paused draft = %+v", paused)
		}

		resumedAt := startedAt.Add(time.Minute)
		resumeUseCase := usecasedraft.NewRecoveryUseCase(repository.mock, newDraftClock(t, resumedAt))
		resumeCommand := usecasedraft.ResumeCommand{
			DraftID: paused.Draft.ID, ExpectedRevisionID: paused.Draft.RevisionID,
			ExpectedRevision: paused.Draft.Revision, ExpectedServiceEpoch: paused.Draft.ServiceEpoch,
			CommandID: task030ID(104), ResultRevisionID: task030ID(105),
			ActorID: task030ID(103), Reason: "operator network check complete",
		}
		start := make(chan struct{})
		results := make(chan usecasedraft.RecoveryResult, 2)
		errorsChannel := make(chan error, 2)
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				result, resumeErr := resumeUseCase.Resume(context.Background(), resumeCommand)
				results <- result
				errorsChannel <- resumeErr
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errorsChannel)
		for resumeErr := range errorsChannel {
			if resumeErr != nil {
				t.Fatalf("Resume() error = %v", resumeErr)
			}
		}
		var resumed usecasedraft.RecoveryResult
		changed := 0
		for result := range results {
			if result.Changed {
				changed++
				resumed = result
			}
		}
		wantDeadline := resumedAt.Add(10 * time.Second)
		if changed != 1 || !resumed.Changed || resumed.Draft.State != usecasedraft.ExecutionStateActive ||
			resumed.Draft.AbsoluteDeadline == nil || !resumed.Draft.AbsoluteDeadline.Equal(wantDeadline) ||
			resumed.Draft.TurnDeadline != wantDeadline || resumed.NextTimeout == nil ||
			resumed.NextTimeout.Deadline != wantDeadline ||
			resumed.Draft.FirstActorDecision.Seed != initial.FirstActorDecision.Seed {
			t.Fatalf("resumed draft = %+v", resumed)
		}

		retried, err := resumeUseCase.Resume(t.Context(), resumeCommand)
		if err != nil || retried.Changed || retried.Draft.RevisionID != resumed.Draft.RevisionID ||
			retried.NextTimeout == nil || retried.NextTimeout.Deadline != wantDeadline {
			t.Fatalf("resume retry = %+v, error = %v", retried, err)
		}
	})

	t.Run("records recovery and resumes one fresh turn atomically", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 16, 30, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryHarness(t, initial)
		recoveredAt := initial.TurnDeadline.Add(3 * time.Second)
		newEpoch := task030ID(110)
		useCase := usecasedraft.NewRecoveryUseCase(repository.mock, newDraftClock(t, recoveredAt))
		command := usecasedraft.EpochRecoveryCommand{
			DraftID: initial.ID, ExpectedRevisionID: initial.RevisionID,
			ExpectedRevision: initial.Revision, PreviousServiceEpoch: initial.ServiceEpoch,
			CurrentServiceEpoch: newEpoch, RecoveryCommandID: task030ID(111),
			RecoveryRevisionID: task030ID(112), ResumeCommandID: task030ID(113),
			ResumeRevisionID: task030ID(114), RecoveryOwnerID: task030ID(115),
		}
		result, err := useCase.RecoverEpoch(t.Context(), command)
		if err != nil {
			t.Fatalf("RecoverEpoch() error = %v", err)
		}
		wantDeadline := recoveredAt.Add(usecasedraft.TurnDuration)
		if !result.Changed || result.Draft.State != usecasedraft.ExecutionStateActive ||
			result.Draft.Revision != 3 || result.Draft.ServiceEpoch != newEpoch ||
			result.Draft.AbsoluteDeadline == nil || !result.Draft.AbsoluteDeadline.Equal(wantDeadline) ||
			result.NextTimeout == nil || result.NextTimeout.Deadline != wantDeadline ||
			len(result.Draft.Actions) != 0 ||
			result.Draft.FirstActorDecision.Seed != initial.FirstActorDecision.Seed {
			t.Fatalf("recovered draft = %+v", result)
		}
		history := task030DraftHistory(repository)
		if len(history) != 3 || history[1].State != usecasedraft.ExecutionStateRecoveryRequired ||
			history[1].Recovery == nil ||
			history[1].Recovery.Policy != usecasedraft.RecoveryPolicyFreshOnResume ||
			history[1].Recovery.PreviousServiceEpoch != initial.ServiceEpoch ||
			history[1].Recovery.CurrentServiceEpoch != newEpoch ||
			history[1].Recovery.PreviousDeadline != initial.TurnDeadline {
			t.Fatalf("recovery history = %+v", history)
		}
		tampered := cloneDraftExecution(history[1])
		tampered.Recovery.CurrentServiceEpoch = uuid.Nil
		if err := tampered.Validate(); !errors.Is(err, usecasedraft.ErrInvalidExecution) {
			t.Fatalf("tampered recovery epoch error = %v", err)
		}

		retried, err := useCase.RecoverEpoch(t.Context(), command)
		if err != nil || retried.Changed || retried.Draft.RevisionID != result.Draft.RevisionID ||
			len(task030DraftHistory(repository)) != 3 {
			t.Fatalf("recovery retry = %+v, error = %v", retried, err)
		}
	})

	t.Run("supersedes active and paused evidence without deleting actions", func(t *testing.T) {
		t.Parallel()

		for _, state := range []usecasedraft.ExecutionState{
			usecasedraft.ExecutionStateActive,
			usecasedraft.ExecutionStatePaused,
		} {
			t.Run(string(state), func(t *testing.T) {
				t.Parallel()

				startedAt := time.Date(2026, time.August, 30, 17, 0, 0, 0, time.UTC)
				initial := task030Draft(t, startedAt)
				repository := newDraftRepositoryHarness(t, initial)
				actionUseCase := usecasedraft.NewActionUseCase(repository.mock, newDraftClock(t, startedAt.Add(time.Second)))
				action, err := actionUseCase.Apply(
					t.Context(),
					task030ActionCommand(initial, uuid.New(), domain.CategoryWeb),
				)
				if err != nil {
					t.Fatalf("prepare action: %v", err)
				}
				current := action.Draft
				if state == usecasedraft.ExecutionStatePaused {
					pauseUseCase := usecasedraft.NewRecoveryUseCase(
						repository.mock,
						newDraftClock(t, startedAt.Add(2*time.Second)),
					)
					paused, pauseErr := pauseUseCase.Pause(t.Context(), usecasedraft.PauseCommand{
						DraftID: current.ID, ExpectedRevisionID: current.RevisionID,
						ExpectedRevision: current.Revision, ExpectedServiceEpoch: current.ServiceEpoch,
						CommandID: uuid.New(), ResultRevisionID: uuid.New(), ActorID: uuid.New(),
						Reason: "operator correction review",
					})
					if pauseErr != nil {
						t.Fatalf("prepare pause: %v", pauseErr)
					}
					current = paused.Draft
				}

				supersededAt := startedAt.Add(3 * time.Second)
				useCase := usecasedraft.NewRecoveryUseCase(repository.mock, newDraftClock(t, supersededAt))
				result, err := useCase.Supersede(t.Context(), usecasedraft.SupersedeCommand{
					DraftID: current.ID, ExpectedRevisionID: current.RevisionID,
					ExpectedRevision: current.Revision, ExpectedServiceEpoch: current.ServiceEpoch,
					CommandID: uuid.New(), ResultRevisionID: uuid.New(), ActorID: uuid.New(),
					Reason: "category correction approved",
				})
				if err != nil {
					t.Fatalf("Supersede() error = %v", err)
				}
				if !result.Changed || result.Draft.State != usecasedraft.ExecutionStateSuperseded ||
					result.NextTimeout != nil || len(result.Draft.Actions) != 1 ||
					result.Draft.Actions[0].Category != domain.CategoryWeb || result.Draft.Recovery == nil ||
					result.Draft.Recovery.Reason != usecasedraft.RecoveryReasonCorrection {
					t.Fatalf("superseded draft = %+v", result)
				}
			})
		}
	})

	t.Run("rejects correction at the active cutoff", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 17, 30, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryHarness(t, initial)
		useCase := usecasedraft.NewRecoveryUseCase(repository.mock, newDraftClock(t, initial.TurnDeadline))
		result, err := useCase.Supersede(t.Context(), usecasedraft.SupersedeCommand{
			DraftID: initial.ID, ExpectedRevisionID: initial.RevisionID,
			ExpectedRevision: initial.Revision, ExpectedServiceEpoch: initial.ServiceEpoch,
			CommandID: uuid.New(), ResultRevisionID: uuid.New(), ActorID: uuid.New(),
			Reason: "late correction",
		})
		if !errors.Is(err, usecasedraft.ErrCorrectionCutoff) || result.Changed {
			t.Fatalf("Supersede() result = %+v, error = %v", result, err)
		}
	})
}

func task030DraftHistory(repository *draftRepositoryHarness) []usecasedraft.Execution {
	repository.state.mu.Lock()
	defer repository.state.mu.Unlock()
	history := make([]usecasedraft.Execution, len(repository.state.history))
	for index, revision := range repository.state.history {
		history[index] = cloneDraftExecution(revision)
	}
	return history
}
