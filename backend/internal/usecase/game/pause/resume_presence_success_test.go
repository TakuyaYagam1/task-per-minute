package pause_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testPauseResumeSinglePresenceSuccess(t *testing.T, decidedAt time.Time) {
	t.Helper()

	t.Run("connected connected resumes in Series participant order", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		series := authority.Resume.Pause.Graph.Series[0].Execution.Series
		if err != nil || !changed || record.GameDecision.ID != command.GameDecisionID ||
			command.GameDecisionID == command.SeriesDecisionID ||
			authority.Resume.Pause.ScopeKind != pausedomain.ScopeWave || authority.Resume.Pause.ScopeID != authority.Resume.Pause.Scope.WaveID ||
			authority.SeriesDecision.PauseID == command.Resume.PauseID || authority.SeriesDecision.ParentPauseID != nil ||
			authority.SeriesDecision.Depth != 0 || authority.GameDecision.Depth != 1 ||
			!authority.SeriesDecision.StartedAt.Before(authority.Resume.Pause.PausedAt) ||
			!authority.GameDecision.StartedAt.Before(authority.Resume.Pause.PausedAt) ||
			authority.GameDecision.ParentPauseID == nil || *authority.GameDecision.ParentPauseID != authority.SeriesDecision.PauseID ||
			record.GameDecision.PauseID != authority.GameDecision.PauseID ||
			record.GameDecision.DecisionNumber != authority.GameDecision.DecisionNumber+1 ||
			record.GameDecision.Action != gameusecase.PauseResumeActionResume || !record.GameDecision.DecidedAt.Equal(decidedAt) || record.SeriesDecision == nil ||
			record.SeriesDecision.ID != command.SeriesDecisionID || record.SeriesDecision.PauseID != authority.SeriesDecision.PauseID ||
			record.SeriesDecision.DecisionNumber != authority.SeriesDecision.DecisionNumber+1 ||
			record.SeriesDecision.Action != gameusecase.PauseResumeActionResume || !record.SeriesDecision.DecidedAt.Equal(decidedAt) ||
			record.GamePauseState != gameusecase.PauseStateResumed || record.SeriesPauseState != gameusecase.PauseStateResumed ||
			record.NormalPauseState != gameusecase.PauseStateResumed || record.NormalPauseResolvedAt == nil || !record.NormalPauseResolvedAt.Equal(decidedAt) ||
			record.First.ParticipantID != series.FirstParticipantID || record.Second.ParticipantID != series.SecondParticipantID ||
			record.First.CurrentInterval != nil || record.Second.CurrentInterval != nil ||
			record.GameDecision.FirstReconnectIntervalID != nil || record.GameDecision.SecondReconnectIntervalID != nil ||
			record.SeriesDecision.FirstReconnectIntervalID != nil || record.SeriesDecision.SecondReconnectIntervalID != nil ||
			record.Graph.Tournament.State != domain.TournamentStateSwiss || record.Graph.Games[0].Game.State != domain.GameStateActive ||
			repository.gameDecisionWriteCount() != 1 || repository.seriesDecisionWriteCount() != 1 {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
		expectedClock := *authority.GameDecision.GameClock
		expectedClock.ResumedAt = &decidedAt
		shiftedDeadline := decidedAt.Add(expectedClock.Remaining)
		expectedClock.ResumedDeadline = &shiftedDeadline
		expectedClock.Revision++
		if !reflect.DeepEqual(record.GameClock, expectedClock) {
			t.Fatalf("restored Game clock = %+v, want %+v", record.GameClock, expectedClock)
		}
	})

	t.Run("preexisting first absence appends an immutable continuation", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{true, false})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		source := suspendedReconnectForParticipant(t, authority.Resume.Pause, firstID)
		counter := *reconnectCounterForParticipant(t, authority.Resume.Counters, firstID)
		continuationID := uuid.New()
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: continuationID}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		remaining := source.Deadline.Sub(*source.ClosedAt)
		current := record.First.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitFirst ||
			record.First.Disposition != gameusecase.PauseResumeParticipantContinuation || record.First.SourceInterval == nil ||
			!reflect.DeepEqual(*record.First.SourceInterval, source) || current == nil || current.ID != continuationID || current.ID == source.ID ||
			current.PauseID != authority.GameDecision.PauseID || current.Number != source.Number ||
			current.ContinuationNumber != source.ContinuationNumber+1 || current.ContinuedFromID == nil || *current.ContinuedFromID != source.ID ||
			current.SuspendedByPauseID != nil ||
			current.PresenceEpoch != source.PresenceEpoch || current.State != pausedomain.ReconnectStateOpen || current.ClosedAt != nil ||
			!current.OpenedAt.Equal(decidedAt) || !current.Deadline.Equal(decidedAt.Add(remaining)) || !reflect.DeepEqual(record.First.Counter, counter) {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
		assertCurrentReconnectIdentity(t, current, authority, firstID, decidedAt)
		assertNormalSuspendedSource(t, source, command.Resume.PauseID, authority.Resume.Pause.PausedAt)
		assertContinuationReconnectIdentity(t, source, *current)
		assertDecisionReconnectIntervals(t, record.GameDecision, current, nil)
		assertReconnectWaitRecord(t, authority.Resume.Pause.Graph, *record)
		assertOnlyGameDecisionWrite(t, repository)
		if got := reconnectByID(t, record.Graph.Reconnect, source.ID); !reflect.DeepEqual(got, source) {
			t.Fatalf("source changed: got %+v, want %+v", got, source)
		}
		*record.First.CurrentInterval.ContinuedFromID = uuid.New()
		record.Command.FrozenDeadlines[0].Remaining++
		replayed, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), command)
		if err != nil || changed || !replayed.First.CurrentInterval.Deadline.Equal(decidedAt.Add(remaining)) ||
			replayed.First.CurrentInterval.ContinuedFromID == nil || *replayed.First.CurrentInterval.ContinuedFromID != source.ID ||
			replayed.Command.FrozenDeadlines[0].Remaining != command.FrozenDeadlines[0].Remaining || repository.writeCount() != 1 {
			t.Fatalf("replay error = %v, changed = %v, record = %+v", err, changed, replayed)
		}
	})

	t.Run("preexisting second absence appends wait second continuation", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{false, true})
		series := authority.Resume.Pause.Graph.Series[0].Execution.Series
		source := suspendedReconnectForParticipant(t, authority.Resume.Pause, series.SecondParticipantID)
		before := *reconnectCounterForParticipant(t, authority.Resume.Counters, series.SecondParticipantID)
		command.SecondInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: series.SecondParticipantID, IntervalID: uuid.New()}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		current := record.Second.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitSecond ||
			record.First.ParticipantID != series.FirstParticipantID || record.Second.ParticipantID != series.SecondParticipantID ||
			record.First.CurrentInterval != nil || record.Second.SourceInterval == nil || !reflect.DeepEqual(*record.Second.SourceInterval, source) ||
			current == nil || current.PauseID != authority.GameDecision.PauseID ||
			current.Number != source.Number || current.ContinuationNumber != source.ContinuationNumber+1 ||
			current.ContinuedFromID == nil || *current.ContinuedFromID != source.ID ||
			current.SuspendedByPauseID != nil ||
			!current.Deadline.Equal(decidedAt.Add(source.Deadline.Sub(*source.ClosedAt))) ||
			!reflect.DeepEqual(record.Second.Counter, before) || !reflect.DeepEqual(reconnectByID(t, record.Graph.Reconnect, source.ID), source) {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
		assertCurrentReconnectIdentity(t, current, authority, series.SecondParticipantID, decidedAt)
		assertNormalSuspendedSource(t, source, command.Resume.PauseID, authority.Resume.Pause.PausedAt)
		assertContinuationReconnectIdentity(t, source, *current)
		assertDecisionReconnectIntervals(t, record.GameDecision, nil, current)
		assertReconnectWaitRecord(t, authority.Resume.Pause.Graph, *record)
		assertOnlyGameDecisionWrite(t, repository)
	})

	t.Run("new second absence consumes one fresh root slot", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		secondID := authority.Resume.Pause.Graph.Series[0].Execution.Series.SecondParticipantID
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, secondID), authority.Resume.Pause.PausedAt.Add(20*time.Second), 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		before := *reconnectCounterForParticipant(t, authority.Resume.Counters, secondID)
		command.SecondInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: secondID, IntervalID: uuid.New(), Window: 45 * time.Second}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		current := record.Second.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitSecond ||
			record.Second.Disposition != gameusecase.PauseResumeParticipantFresh || record.Second.SourceInterval != nil || current == nil ||
			current.PauseID != authority.GameDecision.PauseID || current.Number != before.Used+1 || current.ContinuationNumber != 0 ||
			current.ContinuedFromID != nil || current.SuspendedByPauseID != nil || !current.OpenedAt.Equal(decidedAt) ||
			!current.Deadline.Equal(decidedAt.Add(45*time.Second)) || record.Second.Counter.Used != before.Used+1 ||
			record.Second.Counter.Revision != before.Revision+1 {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
		assertCurrentReconnectIdentity(t, current, authority, secondID, decidedAt)
		assertDecisionReconnectIntervals(t, record.GameDecision, nil, current)
		assertReconnectWaitRecord(t, authority.Resume.Pause.Graph, *record)
		assertOnlyGameDecisionWrite(t, repository)
	})

	t.Run("new first absence opens wait first fresh root", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		series := authority.Resume.Pause.Graph.Series[0].Execution.Series
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, series.FirstParticipantID), authority.Resume.Pause.PausedAt.Add(15*time.Second), 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		before := *reconnectCounterForParticipant(t, authority.Resume.Counters, series.FirstParticipantID)
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: series.FirstParticipantID, IntervalID: uuid.New(), Window: 35 * time.Second}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		current := record.First.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitFirst ||
			record.First.ParticipantID != series.FirstParticipantID || record.Second.ParticipantID != series.SecondParticipantID ||
			record.Second.CurrentInterval != nil || current == nil || current.PauseID != authority.GameDecision.PauseID ||
			current.Number != before.Used+1 || current.ContinuationNumber != 0 || current.ContinuedFromID != nil ||
			current.SuspendedByPauseID != nil || !current.OpenedAt.Equal(decidedAt) || !current.Deadline.Equal(decidedAt.Add(35*time.Second)) ||
			record.First.Counter.Used != before.Used+1 || record.First.Counter.Revision != before.Revision+1 {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
		assertCurrentReconnectIdentity(t, current, authority, series.FirstParticipantID, decidedAt)
		assertDecisionReconnectIntervals(t, record.GameDecision, current, nil)
		assertReconnectWaitRecord(t, authority.Resume.Pause.Graph, *record)
		assertOnlyGameDecisionWrite(t, repository)
	})

	t.Run("return then redisconnect uses a fresh root instead of source lineage", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{true, false})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		source := suspendedReconnectForParticipant(t, authority.Resume.Pause, firstID)
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), authority.Resume.Pause.PausedAt.Add(30*time.Second), 2)
		refreshPauseResumePresenceExpectation(&authority, &command)
		before := *reconnectCounterForParticipant(t, authority.Resume.Counters, firstID)
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		current := record.First.CurrentInterval
		if err != nil || !changed || record.First.Disposition != gameusecase.PauseResumeParticipantFresh || current == nil ||
			current.Number != before.Used+1 || current.ContinuationNumber != 0 || current.ContinuedFromID != nil ||
			current.SuspendedByPauseID != nil || record.First.Counter.Used != before.Used+1 ||
			!reflect.DeepEqual(reconnectByID(t, record.Graph.Reconnect, source.ID), source) {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
		assertCurrentReconnectIdentity(t, current, authority, firstID, decidedAt)
		assertDecisionReconnectIntervals(t, record.GameDecision, current, nil)
		assertReconnectWaitRecord(t, authority.Resume.Pause.Graph, *record)
		assertOnlyGameDecisionWrite(t, repository)
	})

	t.Run("equal Presence instants in another location share CAS identity", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		target := authority.Resume.Pause.PausedAt.Add(15 * time.Second)
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), target, 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		baseline := presenceForParticipant(t, command.Presence, firstID)
		location := time.FixedZone("test", 9*60*60)
		baseline.ConnectedAt = baseline.ConnectedAt.In(location)
		baseline.UpdatedAt = baseline.UpdatedAt.In(location)
		disconnectedAt := baseline.DisconnectedAt.In(location)
		baseline.DisconnectedAt = &disconnectedAt
		live := presenceForParticipant(t, authority.Resume.Presence, firstID)
		if !live.UpdatedAt.Equal(baseline.UpdatedAt) || reflect.DeepEqual(live.UpdatedAt, baseline.UpdatedAt) {
			t.Fatalf("fixture lacks distinct location representation: live = %v, baseline = %v", live.UpdatedAt, baseline.UpdatedAt)
		}
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitFirst {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
	})

	t.Run("equal Presence instants with monotonic command data share CAS identity", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		target := authority.Resume.Pause.PausedAt.Add(15 * time.Second)
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), target, 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		baseline := presenceForParticipant(t, command.Presence, firstID)
		monotonic := time.Now()
		monotonic = monotonic.Add(target.Sub(monotonic))
		baseline.UpdatedAt = monotonic
		baseline.DisconnectedAt = &monotonic
		live := presenceForParticipant(t, authority.Resume.Presence, firstID)
		if !live.UpdatedAt.Equal(baseline.UpdatedAt) || reflect.DeepEqual(live.UpdatedAt, baseline.UpdatedAt) {
			t.Fatalf("fixture lacks distinct monotonic representation: live = %v, baseline = %v", live.UpdatedAt, baseline.UpdatedAt)
		}
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitFirst {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
	})
}
