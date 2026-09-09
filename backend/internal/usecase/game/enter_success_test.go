package game_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/google/uuid"
)

func testNormalPauseGraphEntrySuccess(t *testing.T, pausedAt time.Time) {
	t.Helper()

	t.Run("publishes one complete paused graph and reconciles an exact retry", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		repository := newNormalPauseRepositoryHarness(t, authority)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))

		record, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Enter() error = %v, changed = %v", err, changed)
		}
		if record.State != gameusecase.PauseStateActive || record.Graph.Tournament.State != domain.TournamentStateTechnicalPause ||
			record.ScopeKind != pausedomain.ScopeWave || record.ScopeID != record.Scope.WaveID ||
			record.Graph.Tournament.PausedFromState == nil || *record.Graph.Tournament.PausedFromState != domain.TournamentStateSwiss ||
			record.Graph.Wave.Wave.State != domain.WaveStatePaused || !record.Graph.DeadlinesSuppressed ||
			len(record.Graph.Series) != 1 || record.Graph.Series[0].Execution.Series.State != domain.SeriesStateTechnicalPause ||
			record.Graph.Series[0].Execution.ResumeState == nil || *record.Graph.Series[0].Execution.ResumeState != domain.SeriesStateActive ||
			len(record.Graph.Games) != 1 || record.Graph.Games[0].Game.State != domain.GameStatePaused ||
			record.Graph.Games[0].ResumeState == nil || *record.Graph.Games[0].ResumeState != domain.GameStateActive ||
			record.Graph.Games[0].Deadline != nil || len(record.Graph.FrozenDeadlines) != 1 ||
			record.Graph.FrozenDeadlines[0].Remaining != time.Minute {
			t.Fatalf("paused graph = %+v", record.Graph)
		}
		if repository.writeCount() != 1 {
			t.Fatalf("writes = %d, want 1", repository.writeCount())
		}

		record.Graph.Series[0].Revision = 99
		repository.bumpGraphRevision()
		retried, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || changed || retried.Graph.Series[0].Revision == 99 || repository.writeCount() != 1 {
			t.Fatalf("retry error = %v, changed = %v, record = %+v, writes = %d", err, changed, retried, repository.writeCount())
		}
	})

	t.Run("cancels open reconnect with explicit immutable suspension evidence", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		makeReconnectOpenBeforePause(&authority, &command, pausedAt, 40*time.Second)
		beforeAuthority := cloneNormalPauseAuthority(authority)
		beforeInterval := authority.Graph.Reconnect[0]
		beforeCounter := authority.Graph.Counters[0]
		repository := newNormalPauseRepositoryHarness(t, authority)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))

		record, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || !changed || len(record.SuspendedReconnect) != 1 ||
			!beforeInterval.OpenedAt.Before(pausedAt) ||
			record.SuspendedReconnect[0] != (gameusecase.PauseChildRevision{ID: beforeInterval.ID, Revision: beforeInterval.Revision}) {
			t.Fatalf("Enter() error = %v, changed = %v, suspension = %+v", err, changed, record.SuspendedReconnect)
		}
		closed := record.Graph.Reconnect[0]
		expectedClosed := beforeInterval
		expectedClosed.State = pausedomain.ReconnectStateCancelled
		expectedClosed.ClosedAt = &pausedAt
		expectedClosed.Revision++
		expectedClosed.UpdatedAt = pausedAt
		expectedClosed.SuspendedByPauseID = &command.PauseID
		if !reflect.DeepEqual(closed, expectedClosed) || !reflect.DeepEqual(record.Graph.Counters[0], beforeCounter) {
			t.Fatalf("closed reconnect = %+v, want %+v, counter = %+v", closed, expectedClosed, record.Graph.Counters[0])
		}
		if !reflect.DeepEqual(authority, beforeAuthority) {
			t.Fatal("Enter() mutated caller authority")
		}

		record.SuspendedReconnect[0].Revision++
		retried, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || changed || retried.SuspendedReconnect[0].Revision != beforeInterval.Revision || repository.writeCount() != 1 {
			t.Fatalf("retry error = %v, changed = %v, suspension = %+v, writes = %d", err, changed, retried.SuspendedReconnect, repository.writeCount())
		}
	})

	t.Run("repository cannot mutate canonical suspension evidence in place", func(t *testing.T) {
		t.Parallel()
		authority, command := normalPauseFixture(pausedAt)
		makeReconnectOpenBeforePause(&authority, &command, pausedAt, 40*time.Second)
		beforeAuthority := cloneNormalPauseAuthority(authority)
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.mutateCommitInPlace = true
		record, changed, err := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt)).Enter(t.Context(), command)
		if !errors.Is(err, domain.ErrInternal) || changed || record != nil || !reflect.DeepEqual(authority, beforeAuthority) {
			t.Fatalf("Enter() error = %v, changed = %v, record = %+v, authority changed = %v", err, changed, record, !reflect.DeepEqual(authority, beforeAuthority))
		}
	})

	t.Run("later normal pause suspends only the open continuation", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		oldNormalPauseID := uuid.New()
		source := authority.Graph.Reconnect[0]
		source.State = pausedomain.ReconnectStateCancelled
		source.ContinuationNumber = 0
		source.ContinuedFromID = nil
		source.SuspendedByPauseID = &oldNormalPauseID
		source.OpenedAt = pausedAt.Add(-2 * time.Minute)
		source.Deadline = pausedAt.Add(-30 * time.Second)
		sourceClosedAt := pausedAt.Add(-time.Minute)
		source.ClosedAt = &sourceClosedAt
		source.UpdatedAt = sourceClosedAt
		currentOpenedAt := pausedAt.Add(-20 * time.Second)
		current := source
		current.ID = uuid.New()
		current.State = pausedomain.ReconnectStateOpen
		current.ContinuationNumber = 1
		current.ContinuedFromID = &source.ID
		current.SuspendedByPauseID = nil
		current.OpenedAt = currentOpenedAt
		current.Deadline = currentOpenedAt.Add(source.Deadline.Sub(sourceClosedAt))
		current.ClosedAt = nil
		current.Revision = 1
		current.UpdatedAt = currentOpenedAt
		presence := &authority.Graph.Presence[0]
		presence.State = pausedomain.PresenceStateDisconnected
		presence.DisconnectedAt = &currentOpenedAt
		presence.UpdatedAt = currentOpenedAt
		source.PresenceEpoch = presence.PresenceEpoch
		current.PresenceEpoch = presence.PresenceEpoch
		authority.Graph.Reconnect = []pausedomain.PauseReconnectInterval{source, current}
		authority.Graph.Counters[0].Used = 1
		refreshNormalPauseRevisions(&authority, &command)
		repository := newNormalPauseRepositoryHarness(t, authority)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))

		record, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || !changed || len(record.SuspendedReconnect) != 1 ||
			record.SuspendedReconnect[0] != (gameusecase.PauseChildRevision{ID: current.ID, Revision: current.Revision}) {
			t.Fatalf("Enter() error = %v, changed = %v, suspension = %+v", err, changed, record.SuspendedReconnect)
		}
		if got := reconnectByID(t, record.Graph.Reconnect, source.ID); !reflect.DeepEqual(got, source) {
			t.Fatalf("predecessor changed: got %+v, want %+v", got, source)
		}
		expectedCurrent := current
		expectedCurrent.State = pausedomain.ReconnectStateCancelled
		expectedCurrent.ClosedAt = &pausedAt
		expectedCurrent.Revision++
		expectedCurrent.UpdatedAt = pausedAt
		expectedCurrent.SuspendedByPauseID = &command.PauseID
		if got := reconnectByID(t, record.Graph.Reconnect, current.ID); !reflect.DeepEqual(got, expectedCurrent) {
			t.Fatalf("current continuation = %+v, want %+v", got, expectedCurrent)
		}
	})
}
