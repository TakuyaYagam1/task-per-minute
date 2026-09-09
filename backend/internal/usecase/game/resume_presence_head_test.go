package game_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/google/uuid"
)

func testPauseResumeSinglePresenceHead(t *testing.T, decidedAt time.Time) {
	t.Helper()

	t.Run("sequential stale decision head cannot commit", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), authority.Resume.Pause.PausedAt.Add(time.Second), 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed || stored.GameDecision.ID != command.GameDecisionID {
			t.Fatalf("first decision error = %v, changed = %v, record = %+v", err, changed, stored)
		}
		stale := command
		stale.Resume.CommandID = uuid.New()
		stale.GameDecisionID = uuid.New()
		stale.SeriesDecisionID = uuid.New()
		stale.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		if _, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), stale); !errors.Is(err, gameusecase.ErrPauseResumePresenceConflict) || changed || repository.writeCount() != 1 {
			t.Fatalf("stale decision error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		repository.mu.Lock()
		defer repository.mu.Unlock()
		if repository.authority.GameDecision.CurrentRevisionID != stored.GameDecision.ID ||
			repository.authority.GameDecision.Revision != authority.GameDecision.Revision+1 ||
			repository.authority.GameDecision.DecisionNumber != stored.GameDecision.DecisionNumber ||
			!reflect.DeepEqual(repository.lastGraph, stored.Graph) || repository.normalPauseState != gameusecase.PauseStateActive {
			t.Fatalf("advanced repository head = %+v, graph revision = %d, normal state = %s", repository.authority.GameDecision,
				repository.lastGraph.Revision, repository.normalPauseState)
		}
	})
}
