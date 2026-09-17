package pause_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testPauseResumeSinglePresenceConflicts(t *testing.T, decidedAt time.Time) {
	t.Helper()

	t.Run("uses locked replay lookup and stops after two conflicts", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		leaderRepository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed || stored.GameDecision.ID != command.GameDecisionID || stored.SeriesDecision == nil ||
			stored.SeriesDecision.ID != command.SeriesDecisionID {
			t.Fatalf("prepare replay: error = %v, changed = %v", err, changed)
		}
		followerRepository := newPauseResumePresenceRepositoryHarness(t, authority)
		followerRepository.blockNextLoad()
		follower := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), followerRepository, newPauseClock(t, decidedAt.Add(time.Second)))
		type result struct {
			record  *gameusecase.PauseResumePresenceRecord
			changed bool
			err     error
		}
		resultCh := make(chan result, 1)
		go func() {
			record, changed, err := follower.Resume(context.Background(), command)
			resultCh <- result{record, changed, err}
		}()
		<-followerRepository.loadStarted
		followerRepository.storeCommand(*stored)
		close(followerRepository.loadRelease)
		got := <-resultCh
		if got.err != nil || got.changed || !reflect.DeepEqual(got.record, stored) || followerRepository.commitCalls != 0 {
			t.Fatalf("locked replay error = %v, changed = %v, commits = %d", got.err, got.changed, followerRepository.commitCalls)
		}
		authority, command = pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		conflicts := newPauseResumePresenceRepositoryHarness(t, authority)
		conflicts.conflictsRemaining = 2
		transactions := newCountingTransactionManager(t)
		clock := newSequenceClock(t, decidedAt, decidedAt.Add(time.Second))
		_, changed, err = gameusecase.NewPauseResumePresenceUseCase(transactions, conflicts, clock).Resume(t.Context(), command)
		if !errors.Is(err, gameusecase.ErrPauseResumePresenceConflict) || changed || conflicts.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("conflict error = %v, changed = %v, loads = %d", err, changed, conflicts.loadCount)
		}
	})

	t.Run("repository cannot mutate canonical commit evidence in place", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		beforeAuthority := clonePauseResumePresenceAuthority(authority)
		beforeCommand := clonePauseResumePresenceCommand(command)
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		repository.mutateCommitInPlace = true
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if !errors.Is(err, domain.ErrInternal) || changed || record != nil ||
			!reflect.DeepEqual(authority, beforeAuthority) || !reflect.DeepEqual(command, beforeCommand) {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v, authority changed = %v, command changed = %v", err, changed, record,
				!reflect.DeepEqual(authority, beforeAuthority), !reflect.DeepEqual(command, beforeCommand))
		}
	})

	t.Run("repository cannot return same revision graph mutations", func(t *testing.T) {
		t.Parallel()
		for _, test := range []struct {
			name      string
			withDraft bool
			mutate    func(*gameusecase.PauseResumePresenceRecord)
		}{
			{name: "Wave members", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Graph.Wave.Wave.Members[0], record.Graph.Wave.Wave.Members[1] = record.Graph.Wave.Wave.Members[1], record.Graph.Wave.Wave.Members[0]
			}},
			{name: "Series slot category", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				category := domain.CategoryCrypto
				if record.Graph.Series[0].Execution.Series.Slots[0].Category == category {
					category = domain.CategoryWeb
				}
				record.Graph.Series[0].Execution.Series.Slots[0].Category = category
			}},
			{name: "Game paused deadline", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				deadline := record.DecidedAt.Add(5 * time.Minute)
				record.Graph.Games[0].Deadline = &deadline
			}},
			{name: "Draft pool order", withDraft: true, mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Graph.Draft.Pool[0], record.Graph.Draft.Pool[1] = record.Graph.Draft.Pool[1], record.Graph.Draft.Pool[0]
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
				if test.withDraft {
					addCompletedPauseResumeDraft(t, &authority, &command, decidedAt.Add(-10*time.Minute))
				}
				firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
				disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), authority.Resume.Pause.PausedAt.Add(time.Second), 1)
				refreshPauseResumePresenceExpectation(&authority, &command)
				command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
				repository := newPauseResumePresenceRepositoryHarness(t, authority)
				repository.mutateCommitResult = test.mutate
				record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
				if !errors.Is(err, domain.ErrInternal) || changed || record != nil {
					t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
				}
			})
		}
	})
}
