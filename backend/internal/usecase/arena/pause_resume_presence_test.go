package arena_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPauseResumeSinglePresenceMatrix(t *testing.T) {
	t.Parallel()
	decidedAt := time.Date(2026, time.August, 31, 15, 0, 0, 0, time.UTC)

	t.Run("connected connected resumes in Series participant order", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		series := authority.Resume.Pause.Graph.Series[0].Execution.Series
		if err != nil || !changed || record.GameDecision.ID != command.GameDecisionID ||
			command.GameDecisionID == command.SeriesDecisionID ||
			authority.Resume.Pause.ScopeKind != arena.PauseScopeWave || authority.Resume.Pause.ScopeID != authority.Resume.Pause.Scope.WaveID ||
			authority.SeriesDecision.PauseID == command.Resume.PauseID || authority.SeriesDecision.ParentPauseID != nil ||
			authority.SeriesDecision.Depth != 0 || authority.GameDecision.Depth != 1 ||
			!authority.SeriesDecision.StartedAt.Before(authority.Resume.Pause.PausedAt) ||
			!authority.GameDecision.StartedAt.Before(authority.Resume.Pause.PausedAt) ||
			authority.GameDecision.ParentPauseID == nil || *authority.GameDecision.ParentPauseID != authority.SeriesDecision.PauseID ||
			record.GameDecision.PauseID != authority.GameDecision.PauseID ||
			record.GameDecision.DecisionNumber != authority.GameDecision.DecisionNumber+1 ||
			record.GameDecision.Action != arena.PauseResumeActionResume || !record.GameDecision.DecidedAt.Equal(decidedAt) || record.SeriesDecision == nil ||
			record.SeriesDecision.ID != command.SeriesDecisionID || record.SeriesDecision.PauseID != authority.SeriesDecision.PauseID ||
			record.SeriesDecision.DecisionNumber != authority.SeriesDecision.DecisionNumber+1 ||
			record.SeriesDecision.Action != arena.PauseResumeActionResume || !record.SeriesDecision.DecidedAt.Equal(decidedAt) ||
			record.GamePauseState != arena.PauseStateResumed || record.SeriesPauseState != arena.PauseStateResumed ||
			record.NormalPauseState != arena.PauseStateResumed || record.NormalPauseResolvedAt == nil || !record.NormalPauseResolvedAt.Equal(decidedAt) ||
			record.First.ParticipantID != series.FirstParticipantID || record.Second.ParticipantID != series.SecondParticipantID ||
			record.First.CurrentInterval != nil || record.Second.CurrentInterval != nil ||
			record.GameDecision.FirstReconnectIntervalID != nil || record.GameDecision.SecondReconnectIntervalID != nil ||
			record.SeriesDecision.FirstReconnectIntervalID != nil || record.SeriesDecision.SecondReconnectIntervalID != nil ||
			record.Graph.Tournament.State != domain.ArenaTournamentStateSwiss || record.Graph.Games[0].Game.State != domain.ArenaGameStateActive ||
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
		command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: continuationID}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		remaining := source.Deadline.Sub(*source.ClosedAt)
		current := record.First.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != arena.PauseResumeActionWaitFirst ||
			record.First.Disposition != arena.PauseResumeParticipantContinuation || record.First.SourceInterval == nil ||
			!reflect.DeepEqual(*record.First.SourceInterval, source) || current == nil || current.ID != continuationID || current.ID == source.ID ||
			current.PauseID != authority.GameDecision.PauseID || current.Number != source.Number ||
			current.ContinuationNumber != source.ContinuationNumber+1 || current.ContinuedFromID == nil || *current.ContinuedFromID != source.ID ||
			current.SuspendedByPauseID != nil ||
			current.PresenceEpoch != source.PresenceEpoch || current.State != arena.ReconnectStateOpen || current.ClosedAt != nil ||
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
		replayed, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt.Add(time.Second)}).Resume(t.Context(), command)
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
		command.SecondInterval = &arena.PauseResumeIntervalInput{ParticipantID: series.SecondParticipantID, IntervalID: uuid.New()}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		current := record.Second.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != arena.PauseResumeActionWaitSecond ||
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
		command.SecondInterval = &arena.PauseResumeIntervalInput{ParticipantID: secondID, IntervalID: uuid.New(), Window: 45 * time.Second}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		current := record.Second.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != arena.PauseResumeActionWaitSecond ||
			record.Second.Disposition != arena.PauseResumeParticipantFresh || record.Second.SourceInterval != nil || current == nil ||
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
		command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: series.FirstParticipantID, IntervalID: uuid.New(), Window: 35 * time.Second}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		current := record.First.CurrentInterval
		if err != nil || !changed || record.GameDecision.Action != arena.PauseResumeActionWaitFirst ||
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
		command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		current := record.First.CurrentInterval
		if err != nil || !changed || record.First.Disposition != arena.PauseResumeParticipantFresh || current == nil ||
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
		command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		if err != nil || !changed || record.GameDecision.Action != arena.PauseResumeActionWaitFirst {
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
		command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryFake(authority)
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		if err != nil || !changed || record.GameDecision.Action != arena.PauseResumeActionWaitFirst {
			t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
		}
	})

	t.Run("fails closed on missing stale mismatched ineligible and overflow evidence", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			preexisting [2]bool
			mutate      func(*arena.PauseResumePresenceAuthority, *arena.PauseResumePresenceCommand)
			now         time.Time
			want        error
		}{
			{name: "missing interval", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				disconnectPausePresence(&a.Resume.Presence[0], a.Resume.Pause.PausedAt.Add(time.Second), 1)
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "stale decision head", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.DecisionNumber++
			}},
			{name: "stale Series decision head", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.SeriesExpected.DecisionNumber++
			}},
			{name: "wrong child scope kind", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.GameDecision.ScopeKind = arena.PauseResumeDecisionScopeSeries
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "terminal child pause", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.GameDecision.State = arena.PauseStateResumed
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "stale child revision identity", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.CurrentRevisionID = uuid.New()
			}},
			{name: "stale child revision", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.Revision++
			}},
			{name: "Game decision ID is current revision ID", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameDecisionID = c.GameExpected.CurrentRevisionID
			}},
			{name: "Series decision ID is current revision ID", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.SeriesDecisionID = c.SeriesExpected.CurrentRevisionID
			}},
			{name: "Game decision ID is Series revision ID", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameDecisionID = c.SeriesExpected.CurrentRevisionID
			}},
			{name: "Series decision ID is Game pause ID", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.SeriesDecisionID = c.GameExpected.PauseID
			}},
			{name: "stale Game start", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.StartedAt = c.GameExpected.StartedAt.Add(time.Second)
			}},
			{name: "stale Series start", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.SeriesExpected.StartedAt = c.SeriesExpected.StartedAt.Add(time.Second)
			}},
			{name: "stale Game clock revision", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.Revision++
			}},
			{name: "stale Game clock value", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.Remaining++
			}},
			{name: "stale frozen deadline value", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.FrozenDeadlines[0].OriginalDeadline = c.FrozenDeadlines[0].OriginalDeadline.Add(time.Second)
				c.FrozenDeadlines[0].Remaining += time.Second
			}},
			{name: "Game clock belongs to another pause", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.PauseID = uuid.New()
			}},
			{name: "Game clock belongs to another Game", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.GameID = uuid.New()
			}},
			{name: "stale source reconnect revision", preexisting: [2]bool{true, false}, now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.Resume.Expected.Reconnect[0].Revision++
			}},
			{name: "source suspension missing", preexisting: [2]bool{true, false}, now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, _ *arena.PauseResumePresenceCommand) {
				participantID := a.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
				source := suspendedReconnectForParticipant(t, a.Resume.Pause, participantID)
				reconnectIntervalPointerByID(t, a.Resume.Pause.Graph.Reconnect, source.ID).SuspendedByPauseID = nil
				reconnectIntervalPointerByID(t, a.Resume.Reconnect, source.ID).SuspendedByPauseID = nil
			}},
			{name: "source suspension names another normal pause", preexisting: [2]bool{true, false}, now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, _ *arena.PauseResumePresenceCommand) {
				participantID := a.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
				source := suspendedReconnectForParticipant(t, a.Resume.Pause, participantID)
				wrong := uuid.New()
				reconnectIntervalPointerByID(t, a.Resume.Pause.Graph.Reconnect, source.ID).SuspendedByPauseID = &wrong
				reconnectIntervalPointerByID(t, a.Resume.Reconnect, source.ID).SuspendedByPauseID = &wrong
			}},
			{name: "stale Presence revision", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.Resume.Expected.Presence[0].Revision++
			}},
			{name: "stale Presence epoch", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.Resume.Expected.Presence[0].PresenceEpoch++
			}},
			{name: "stale counter revision", now: decidedAt, want: arena.ErrPauseResumePresenceConflict, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.Resume.Expected.Counters[0].Revision++
			}},
			{name: "stale counter Used", now: decidedAt, want: arena.ErrPauseResumePresenceIncomplete, mutate: func(a *arena.PauseResumePresenceAuthority, _ *arena.PauseResumePresenceCommand) {
				a.Resume.Counters[0].Used++
			}},
			{name: "normal pause substituted for child pause", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(_ *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				c.GameExpected.PauseID = c.Resume.PauseID
			}},
			{name: "old Series accidentally reparented to normal Wave pause", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.SeriesDecision.ParentPauseID = uuidPointer(c.Resume.PauseID)
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "wrong old Series Game edge", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.GameDecision.ParentPauseID = uuidPointer(c.Resume.PauseID)
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "wrong Series depth", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.SeriesDecision.Depth++
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "wrong Game depth", now: decidedAt, want: arena.ErrInvalidPauseResumePresence, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.GameDecision.Depth++
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "exhausted fresh slot", now: decidedAt, want: arena.ErrPauseResumePresenceIneligible, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				presence := &a.Resume.Presence[0]
				disconnectPausePresence(presence, a.Resume.Pause.PausedAt.Add(time.Second), 1)
				counter := reconnectCounterForParticipant(t, a.Resume.Counters, presence.ParticipantID)
				counter.Limit = counter.Used
				refreshPauseResumePresenceExpectation(a, c)
				c.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: presence.ParticipantID, IntervalID: uuid.New(), Window: time.Minute}
			}},
			{name: "fresh deadline overflow", now: time.Unix(0, math.MaxInt64-int64(30*time.Second)).UTC(), want: arena.ErrPauseResumePresenceOverflow, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				disconnectPausePresence(&a.Resume.Presence[0], a.Resume.Pause.PausedAt.Add(time.Second), 1)
				refreshPauseResumePresenceExpectation(a, c)
				c.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: a.Resume.Presence[0].ParticipantID, IntervalID: uuid.New(), Window: time.Minute}
			}},
			{name: "decision number overflow", now: decidedAt, want: arena.ErrPauseResumePresenceOverflow, mutate: func(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
				a.GameDecision.DecisionNumber = math.MaxInt64
				refreshPauseResumePresenceExpectation(a, c)
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				authority, command := pauseResumePresenceFixture(t, decidedAt, test.preexisting)
				test.mutate(&authority, &command)
				repository := newPauseResumePresenceRepositoryFake(authority)
				_, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: test.now}).Resume(t.Context(), command)
				if !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("uses locked replay lookup and stops after two conflicts", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		leaderRepository := newPauseResumePresenceRepositoryFake(authority)
		stored, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		if err != nil || !changed || stored.GameDecision.ID != command.GameDecisionID || stored.SeriesDecision == nil ||
			stored.SeriesDecision.ID != command.SeriesDecisionID {
			t.Fatalf("prepare replay: error = %v, changed = %v", err, changed)
		}
		followerRepository := newPauseResumePresenceRepositoryFake(authority)
		followerRepository.blockNextLoad()
		follower := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, followerRepository, fixedArenaClock{now: decidedAt.Add(time.Second)})
		type result struct {
			record  *arena.PauseResumePresenceRecord
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
		conflicts := newPauseResumePresenceRepositoryFake(authority)
		conflicts.conflictsRemaining = 2
		transactions := &countingArenaTransactionManager{}
		clock := &sequenceArenaClock{times: []time.Time{decidedAt, decidedAt.Add(time.Second)}}
		_, changed, err = arena.NewPauseResumePresenceUseCase(transactions, conflicts, clock).Resume(t.Context(), command)
		if !errors.Is(err, arena.ErrPauseResumePresenceConflict) || changed || conflicts.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("conflict error = %v, changed = %v, loads = %d", err, changed, conflicts.loadCount)
		}
	})

	t.Run("repository cannot mutate canonical commit evidence in place", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		beforeAuthority := clonePauseResumePresenceAuthority(authority)
		beforeCommand := clonePauseResumePresenceCommand(command)
		repository := newPauseResumePresenceRepositoryFake(authority)
		repository.mutateCommitInPlace = true
		record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
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
			mutate    func(*arena.PauseResumePresenceRecord)
		}{
			{name: "Wave members", mutate: func(record *arena.PauseResumePresenceRecord) {
				record.Graph.Wave.Wave.Members[0], record.Graph.Wave.Wave.Members[1] = record.Graph.Wave.Wave.Members[1], record.Graph.Wave.Wave.Members[0]
			}},
			{name: "Series slot category", mutate: func(record *arena.PauseResumePresenceRecord) {
				category := domain.CategoryCrypto
				if record.Graph.Series[0].Execution.Series.Slots[0].Category == category {
					category = domain.CategoryWeb
				}
				record.Graph.Series[0].Execution.Series.Slots[0].Category = category
			}},
			{name: "Game paused deadline", mutate: func(record *arena.PauseResumePresenceRecord) {
				deadline := record.DecidedAt.Add(5 * time.Minute)
				record.Graph.Games[0].Deadline = &deadline
			}},
			{name: "Draft pool order", withDraft: true, mutate: func(record *arena.PauseResumePresenceRecord) {
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
				command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
				repository := newPauseResumePresenceRepositoryFake(authority)
				repository.mutateCommitResult = test.mutate
				record, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
				if !errors.Is(err, domain.ErrInternal) || changed || record != nil {
					t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
				}
			})
		}
	})

	t.Run("sequential stale decision head cannot commit", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), authority.Resume.Pause.PausedAt.Add(time.Second), 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		command.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryFake(authority)
		stored, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt}).Resume(t.Context(), command)
		if err != nil || !changed || stored.GameDecision.ID != command.GameDecisionID {
			t.Fatalf("first decision error = %v, changed = %v, record = %+v", err, changed, stored)
		}
		stale := command
		stale.Resume.CommandID = uuid.New()
		stale.GameDecisionID = uuid.New()
		stale.SeriesDecisionID = uuid.New()
		stale.FirstInterval = &arena.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		if _, changed, err := arena.NewPauseResumePresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: decidedAt.Add(time.Second)}).Resume(t.Context(), stale); !errors.Is(err, arena.ErrPauseResumePresenceConflict) || changed || repository.writeCount() != 1 {
			t.Fatalf("stale decision error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		repository.mu.Lock()
		defer repository.mu.Unlock()
		if repository.authority.GameDecision.CurrentRevisionID != stored.GameDecision.ID ||
			repository.authority.GameDecision.Revision != authority.GameDecision.Revision+1 ||
			repository.authority.GameDecision.DecisionNumber != stored.GameDecision.DecisionNumber ||
			!reflect.DeepEqual(repository.lastGraph, stored.Graph) || repository.normalPauseState != arena.PauseStateActive {
			t.Fatalf("advanced fake head = %+v, graph revision = %d, normal state = %s", repository.authority.GameDecision,
				repository.lastGraph.Revision, repository.normalPauseState)
		}
	})
}

type pauseResumePresenceRepositoryFake struct {
	mu                                                 sync.Mutex
	authority                                          arena.PauseResumePresenceAuthority
	commands                                           map[uuid.UUID]arena.PauseResumePresenceRecord
	conflictsRemaining, loadCount, writes, commitCalls int
	gameDecisionWrites, seriesDecisionWrites           int
	mutateCommitInPlace                                bool
	mutateCommitResult                                 func(*arena.PauseResumePresenceRecord)
	lastGraph                                          arena.PauseGraph
	normalPauseState                                   arena.PauseState
	loadStarted, loadRelease                           chan struct{}
	loadOnce                                           sync.Once
}

func newPauseResumePresenceRepositoryFake(authority arena.PauseResumePresenceAuthority) *pauseResumePresenceRepositoryFake {
	return &pauseResumePresenceRepositoryFake{authority: clonePauseResumePresenceAuthority(authority), commands: make(map[uuid.UUID]arena.PauseResumePresenceRecord)}
}

func (f *pauseResumePresenceRepositoryFake) FindPauseResumePresenceCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*arena.PauseResumePresenceRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Command.Resume.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := clonePauseResumePresenceRecord(record)
	return &clone, nil
}

func (f *pauseResumePresenceRepositoryFake) LoadPauseResumePresenceAuthority(_ context.Context, _ arena.PauseGraphScope, _, _, _ uuid.UUID) (arena.PauseResumePresenceAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonePauseResumePresenceAuthority(f.authority), nil
}

func (f *pauseResumePresenceRepositoryFake) CommitPauseResumePresence(_ context.Context, expected arena.PauseResumePresenceExpectation, record arena.PauseResumePresenceRecord) (*arena.PauseResumePresenceRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if existing, ok := f.commands[record.Command.Resume.CommandID]; ok {
		clone := clonePauseResumePresenceRecord(existing)
		return &clone, false, nil
	}
	if f.conflictsRemaining > 0 {
		f.conflictsRemaining--
		return nil, false, domain.ErrConflict
	}
	if !reflect.DeepEqual(expected, arena.PauseResumePresenceExpectationFrom(f.authority)) {
		return nil, false, domain.ErrConflict
	}
	if f.mutateCommitInPlace {
		expected.Resume.Presence[0].Revision++
		record.Graph.Tournament.CreatedAt = record.Graph.Tournament.CreatedAt.Add(time.Second)
	}
	f.writes++
	f.commands[record.Command.Resume.CommandID] = clonePauseResumePresenceRecord(record)
	f.authority.GameDecision.DecisionNumber = record.GameDecision.DecisionNumber
	f.authority.GameDecision.CurrentRevisionID = record.GameDecision.ID
	f.authority.GameDecision.Revision++
	f.authority.GameDecision.State = record.GamePauseState
	gameClock := clonePauseResumeGameClock(record.GameClock)
	f.authority.GameDecision.GameClock = &gameClock
	f.gameDecisionWrites++
	if record.SeriesDecision != nil {
		f.authority.SeriesDecision.DecisionNumber = record.SeriesDecision.DecisionNumber
		f.authority.SeriesDecision.CurrentRevisionID = record.SeriesDecision.ID
		f.authority.SeriesDecision.Revision++
		f.authority.SeriesDecision.State = record.SeriesPauseState
		f.seriesDecisionWrites++
	}
	f.lastGraph = clonePauseGraph(record.Graph)
	f.normalPauseState = record.NormalPauseState
	f.authority.Resume.Pause.State = record.NormalPauseState
	if record.NormalPauseState == arena.PauseStateResumed {
		f.authority.Resume.Pause.Graph = clonePauseGraph(record.Graph)
		f.authority.Resume.Pause.Revision++
		resolvedAt := record.DecidedAt
		f.authority.Resume.Pause.ResolvedAt = &resolvedAt
	}
	clone := clonePauseResumePresenceRecord(record)
	if f.mutateCommitResult != nil {
		f.mutateCommitResult(&clone)
	}
	return &clone, true, nil
}

func (f *pauseResumePresenceRepositoryFake) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}
func (f *pauseResumePresenceRepositoryFake) storeCommand(record arena.PauseResumePresenceRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.Command.Resume.CommandID] = clonePauseResumePresenceRecord(record)
}
func (f *pauseResumePresenceRepositoryFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *pauseResumePresenceRepositoryFake) gameDecisionWriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gameDecisionWrites
}

func (f *pauseResumePresenceRepositoryFake) seriesDecisionWriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seriesDecisionWrites
}

func pauseResumePresenceFixture(t *testing.T, decidedAt time.Time, preexisting [2]bool) (arena.PauseResumePresenceAuthority, arena.PauseResumePresenceCommand) {
	t.Helper()
	pausedAt := decidedAt.Add(-2 * time.Minute)
	authority, pauseCommand := normalPauseFixture(pausedAt)
	series := authority.Graph.Series[0].Execution.Series
	participants := [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}
	childPauseID := authority.Graph.Counters[0].PauseID
	authority.Graph.Counters = []arena.PauseReconnectCounter{
		{PauseID: childPauseID, RosterID: authority.Scope.RosterID, ParticipantID: participants[0], Limit: 3, Revision: 1},
		{PauseID: childPauseID, RosterID: authority.Scope.RosterID, ParticipantID: participants[1], Limit: 3, Revision: 1},
	}
	authority.Graph.Reconnect = nil
	for index, absent := range preexisting {
		if !absent {
			continue
		}
		disconnectedAt := pausedAt.Add(time.Duration(-30-index*10) * time.Second)
		presence := presenceForParticipant(t, authority.Graph.Presence, participants[index])
		presence.State, presence.DisconnectedAt, presence.UpdatedAt = arena.PresenceStateDisconnected, &disconnectedAt, disconnectedAt
		authority.Graph.Counters[index].Used, authority.Graph.Counters[index].Revision = 1, 2
		authority.Graph.Reconnect = append(authority.Graph.Reconnect, arena.PauseReconnectInterval{
			ID: uuid.New(), PauseID: childPauseID, RosterID: authority.Scope.RosterID, SeriesID: series.ID,
			GameID: authority.Graph.Games[0].Game.ID, ParticipantID: participants[index], PresenceEpoch: presence.PresenceEpoch,
			Number: 1, ContinuationNumber: 0, State: arena.ReconnectStateOpen, OpenedAt: disconnectedAt,
			Deadline: pausedAt.Add(time.Duration(40+index*25) * time.Second), Revision: 1, UpdatedAt: disconnectedAt,
		})
	}
	refreshNormalPauseRevisions(&authority, &pauseCommand)
	repository := newNormalPauseRepositoryFake(authority)
	pause, changed, err := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt}).Enter(t.Context(), pauseCommand)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	resume := pauseResumeAuthorityFromRecord(*pause)
	seriesPauseID := uuid.New()
	seriesStartedAt := pausedAt.Add(-2 * time.Minute)
	seriesDecision := arena.PauseResumeDecisionAuthority{
		PauseID: seriesPauseID, ScopeKind: arena.PauseResumeDecisionScopeSeries,
		CurrentRevisionID: uuid.New(), State: arena.PauseStateActive, Revision: 1,
		SeriesID: series.ID, Depth: 0, DecisionNumber: 0, StartedAt: seriesStartedAt,
	}
	gameStartedAt := pausedAt.Add(-time.Minute)
	originalDeadline := pausedAt.Add(time.Minute)
	gameClock := arena.PauseResumeGameClock{
		PauseID: childPauseID, GameID: authority.Graph.Games[0].Game.ID,
		OriginalDeadline: originalDeadline, FrozenAt: gameStartedAt, Remaining: originalDeadline.Sub(gameStartedAt), Revision: 1,
	}
	gameDecision := arena.PauseResumeDecisionAuthority{
		PauseID: childPauseID, ScopeKind: arena.PauseResumeDecisionScopeGameAttempt,
		CurrentRevisionID: uuid.New(), State: arena.PauseStateActive, Revision: 1,
		SeriesID: series.ID, GameID: authority.Graph.Games[0].Game.ID, ParentPauseID: uuidPointer(seriesPauseID), Depth: 1,
		DecisionNumber: 0, StartedAt: gameStartedAt, GameClock: &gameClock,
	}
	result := arena.PauseResumePresenceAuthority{Resume: resume, SeriesDecision: seriesDecision, GameDecision: gameDecision}
	presenceExpected := arena.PauseResumePresenceExpectationFrom(result)
	command := arena.PauseResumePresenceCommand{
		Resume:           arena.PauseResumeCommand{Scope: pause.Scope, PauseID: pause.PauseID, CommandID: uuid.New(), ActorID: uuid.New(), Expected: arena.PauseResumeExpectationFrom(resume)},
		SeriesDecisionID: uuid.New(), GameDecisionID: uuid.New(),
		SeriesExpected:  arena.PauseResumeDecisionExpectationFrom(seriesDecision),
		GameExpected:    arena.PauseResumeDecisionExpectationFrom(gameDecision),
		Presence:        presenceExpected.Presence,
		Reconnect:       presenceExpected.Reconnect,
		Counters:        presenceExpected.Counters,
		FrozenDeadlines: presenceExpected.FrozenDeadlines,
	}
	return result, command
}

func addCompletedPauseResumeDraft(
	t *testing.T,
	authority *arena.PauseResumePresenceAuthority,
	command *arena.PauseResumePresenceCommand,
	startedAt time.Time,
) {
	t.Helper()
	series := &authority.Resume.Pause.Graph.Series[0].Execution.Series
	revision := task029CategoryRevision(t, arena.ArenaStageSwiss, true, uuid.New(), startedAt.Add(-time.Minute))
	revision.SeriesID = series.ID
	initial, err := arena.StartDraftExecution(arena.DraftExecutionStartCommand{
		CategoryRevision: revision, DraftID: uuid.New(), InitialRevisionID: uuid.New(), DecisionEvidenceID: uuid.New(),
		ParticipantIDs: [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}, ServiceEpoch: uuid.New(),
		CommandID: uuid.New(), StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("start completed Draft fixture: %v", err)
	}
	draft := completeTask030Draft(t, initial, startedAt)
	series.FirstParticipantID = draft.FirstParticipantID
	series.SecondParticipantID = draft.SecondParticipantID
	authority.Resume.Pause.Graph.Draft = &draft
	authority.Resume.Pause.Expected.Draft = &arena.DraftRevisionExpectation{
		RevisionID: draft.RevisionID, Revision: draft.Revision, ServiceEpoch: draft.ServiceEpoch,
	}
	authority.Resume.Pause.Expected.DraftPreviousRevisionID = draft.PreviousRevisionID
	authority.Resume.Pause.DraftResultRevisionID = uuid.New()
	command.Resume.DraftResultRevisionID = uuid.New()
	refreshPauseResumePresenceExpectation(authority, command)
}

func refreshPauseResumePresenceExpectation(a *arena.PauseResumePresenceAuthority, c *arena.PauseResumePresenceCommand) {
	c.Resume.Expected = arena.PauseResumeExpectationFrom(a.Resume)
	c.SeriesExpected = arena.PauseResumeDecisionExpectationFrom(a.SeriesDecision)
	c.GameExpected = arena.PauseResumeDecisionExpectationFrom(a.GameDecision)
	expected := arena.PauseResumePresenceExpectationFrom(*a)
	c.Presence = expected.Presence
	c.Reconnect = expected.Reconnect
	c.Counters = expected.Counters
	c.FrozenDeadlines = expected.FrozenDeadlines
}

func disconnectPausePresence(p *arena.PausePresence, at time.Time, delta int64) {
	p.State = arena.PresenceStateDisconnected
	p.PresenceEpoch += delta
	p.Revision += delta
	if delta > 1 {
		p.ConnectedAt = at.Add(-time.Second)
	}
	p.DisconnectedAt, p.UpdatedAt = &at, at
}

func presenceForParticipant(tb testing.TB, values []arena.PausePresence, id uuid.UUID) *arena.PausePresence {
	tb.Helper()
	for i := range values {
		if values[i].ParticipantID == id {
			return &values[i]
		}
	}
	tb.Fatalf("Presence %s not found", id)
	return nil
}
func reconnectCounterForParticipant(tb testing.TB, values []arena.PauseReconnectCounter, id uuid.UUID) *arena.PauseReconnectCounter {
	tb.Helper()
	for i := range values {
		if values[i].ParticipantID == id {
			return &values[i]
		}
	}
	tb.Fatalf("counter %s not found", id)
	return nil
}
func reconnectByID(tb testing.TB, values []arena.PauseReconnectInterval, id uuid.UUID) arena.PauseReconnectInterval {
	tb.Helper()
	for _, value := range values {
		if value.ID == id {
			return value
		}
	}
	tb.Fatalf("Reconnect %s not found", id)
	return arena.PauseReconnectInterval{}
}
func suspendedReconnectForParticipant(tb testing.TB, pause arena.NormalPauseRecord, id uuid.UUID) arena.PauseReconnectInterval {
	tb.Helper()
	for _, item := range pause.SuspendedReconnect {
		value := reconnectByID(tb, pause.Graph.Reconnect, item.ID)
		if value.ParticipantID == id {
			return value
		}
	}
	tb.Fatalf("suspended Reconnect %s not found", id)
	return arena.PauseReconnectInterval{}
}

func assertCurrentReconnectIdentity(tb testing.TB, current *arena.PauseReconnectInterval, authority arena.PauseResumePresenceAuthority, participantID uuid.UUID, decidedAt time.Time) {
	tb.Helper()
	series := authority.Resume.Pause.Graph.Series[0].Execution.Series
	if current == nil || current.RosterID != authority.Resume.Pause.Scope.RosterID || current.SeriesID != series.ID ||
		current.GameID != authority.GameDecision.GameID || current.ParticipantID != participantID || current.Revision != 1 ||
		!current.UpdatedAt.Equal(decidedAt) {
		tb.Fatalf("current Reconnect identity = %+v", current)
	}
}

func assertContinuationReconnectIdentity(tb testing.TB, source, current arena.PauseReconnectInterval) {
	tb.Helper()
	if current.PauseID != source.PauseID || current.RosterID != source.RosterID || current.SeriesID != source.SeriesID ||
		current.GameID != source.GameID || current.ParticipantID != source.ParticipantID ||
		current.PresenceEpoch != source.PresenceEpoch || current.Number != source.Number {
		tb.Fatalf("continuation identity = %+v, source = %+v", current, source)
	}
}

func assertNormalSuspendedSource(tb testing.TB, source arena.PauseReconnectInterval, normalPauseID uuid.UUID, pausedAt time.Time) {
	tb.Helper()
	if source.State != arena.ReconnectStateCancelled || source.ClosedAt == nil || !source.ClosedAt.Equal(pausedAt) ||
		source.SuspendedByPauseID == nil || *source.SuspendedByPauseID != normalPauseID || !source.OpenedAt.Before(pausedAt) {
		tb.Fatalf("normal-pause source suspension = %+v", source)
	}
}

func assertDecisionReconnectIntervals(tb testing.TB, decision arena.PauseResumeDecisionRecord, first, second *arena.PauseReconnectInterval) {
	tb.Helper()
	var firstID, secondID *uuid.UUID
	if first != nil {
		firstID = uuidPointer(first.ID)
	}
	if second != nil {
		secondID = uuidPointer(second.ID)
	}
	if !reflect.DeepEqual(decision.FirstReconnectIntervalID, firstID) || !reflect.DeepEqual(decision.SecondReconnectIntervalID, secondID) {
		tb.Fatalf("decision Reconnect refs = %v, %v, want %v, %v", decision.FirstReconnectIntervalID, decision.SecondReconnectIntervalID, firstID, secondID)
	}
}

func uuidPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func assertReconnectWaitRecord(tb testing.TB, paused arena.PauseGraph, record arena.PauseResumePresenceRecord) {
	tb.Helper()
	got := record.Graph
	if got.Tournament.State != domain.ArenaTournamentStateTechnicalPause || got.Wave.Wave.State != domain.ArenaWaveStatePaused ||
		got.Series[0].Execution.Series.State != domain.ArenaSeriesStateTechnicalPause || got.Games[0].Game.State != domain.ArenaGameStatePaused ||
		!got.DeadlinesSuppressed || got.ActivePauseID != paused.ActivePauseID || got.FrozenDeadlines[0].ResumedAt != nil || got.FrozenDeadlines[0].ResumedDeadline != nil {
		tb.Fatalf("wait resumed parent clocks: %+v", got)
	}
	if record.GameDecision.ID != record.Command.GameDecisionID || record.GameDecision.PauseID != record.Command.GameExpected.PauseID ||
		record.GameDecision.DecisionNumber != record.Command.GameExpected.DecisionNumber+1 ||
		!record.GameDecision.DecidedAt.Equal(record.DecidedAt) ||
		record.Command.GameExpected.GameClock == nil || !reflect.DeepEqual(record.GameClock, *record.Command.GameExpected.GameClock) ||
		record.SeriesDecision != nil || record.GamePauseState != arena.PauseStateActive ||
		record.SeriesPauseState != arena.PauseStateActive || record.NormalPauseState != arena.PauseStateActive ||
		record.NormalPauseResolvedAt != nil {
		tb.Fatalf("wait decision or pause chain = %+v", record)
	}
}

func assertOnlyGameDecisionWrite(tb testing.TB, repository *pauseResumePresenceRepositoryFake) {
	tb.Helper()
	if repository.gameDecisionWriteCount() != 1 || repository.seriesDecisionWriteCount() != 0 {
		tb.Fatalf("decision writes: Game = %d, Series = %d", repository.gameDecisionWriteCount(), repository.seriesDecisionWriteCount())
	}
}

func clonePauseResumePresenceAuthority(value arena.PauseResumePresenceAuthority) arena.PauseResumePresenceAuthority {
	value.Resume = clonePauseResumeAuthority(value.Resume)
	value.SeriesDecision = clonePauseResumeDecisionAuthority(value.SeriesDecision)
	value.GameDecision = clonePauseResumeDecisionAuthority(value.GameDecision)
	return value
}

func clonePauseResumeDecisionAuthority(value arena.PauseResumeDecisionAuthority) arena.PauseResumeDecisionAuthority {
	if value.ParentPauseID != nil {
		id := *value.ParentPauseID
		value.ParentPauseID = &id
	}
	if value.GameClock != nil {
		clock := clonePauseResumeGameClock(*value.GameClock)
		value.GameClock = &clock
	}
	return value
}

func clonePauseResumeGameClock(value arena.PauseResumeGameClock) arena.PauseResumeGameClock {
	if value.ResumedAt != nil {
		at := *value.ResumedAt
		value.ResumedAt = &at
	}
	if value.ResumedDeadline != nil {
		deadline := *value.ResumedDeadline
		value.ResumedDeadline = &deadline
	}
	return value
}

func clonePauseResumePresenceRecord(value arena.PauseResumePresenceRecord) arena.PauseResumePresenceRecord {
	value.Command = clonePauseResumePresenceCommand(value.Command)
	value.Graph = clonePauseGraph(value.Graph)
	value.GameClock = clonePauseResumeGameClock(value.GameClock)
	value.First = clonePauseResumeParticipantResolution(value.First)
	value.Second = clonePauseResumeParticipantResolution(value.Second)
	value.GameDecision = clonePauseResumeDecisionRecord(value.GameDecision)
	if value.SeriesDecision != nil {
		decision := clonePauseResumeDecisionRecord(*value.SeriesDecision)
		value.SeriesDecision = &decision
	}
	return value
}

func clonePauseResumePresenceCommand(value arena.PauseResumePresenceCommand) arena.PauseResumePresenceCommand {
	value.Resume.Expected = clonePauseResumeExpectationExact(value.Resume.Expected)
	value.SeriesExpected = clonePauseResumeDecisionExpectation(value.SeriesExpected)
	value.GameExpected = clonePauseResumeDecisionExpectation(value.GameExpected)
	value.Presence = clonePauseGraph(arena.PauseGraph{Presence: value.Presence}).Presence
	value.Reconnect = clonePauseGraph(arena.PauseGraph{Reconnect: value.Reconnect}).Reconnect
	value.Counters = cloneTestSlice(value.Counters)
	value.FrozenDeadlines = clonePauseGraph(arena.PauseGraph{FrozenDeadlines: value.FrozenDeadlines}).FrozenDeadlines
	value.FirstInterval = clonePauseResumeIntervalInput(value.FirstInterval)
	value.SecondInterval = clonePauseResumeIntervalInput(value.SecondInterval)
	return value
}

func clonePauseResumeExpectationExact(value arena.PauseResumeExpectation) arena.PauseResumeExpectation {
	value.Games = cloneTestSlice(value.Games)
	value.Series = cloneTestSlice(value.Series)
	value.Presence = cloneTestSlice(value.Presence)
	value.Reconnect = cloneTestSlice(value.Reconnect)
	value.Counters = cloneTestSlice(value.Counters)
	value.FrozenDeadlines = cloneTestSlice(value.FrozenDeadlines)
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	return value
}

func clonePauseResumeDecisionExpectation(value arena.PauseResumeDecisionExpectation) arena.PauseResumeDecisionExpectation {
	if value.ParentPauseID != nil {
		id := *value.ParentPauseID
		value.ParentPauseID = &id
	}
	if value.GameClock != nil {
		clock := clonePauseResumeGameClock(*value.GameClock)
		value.GameClock = &clock
	}
	return value
}

func clonePauseResumeDecisionRecord(value arena.PauseResumeDecisionRecord) arena.PauseResumeDecisionRecord {
	if value.FirstReconnectIntervalID != nil {
		id := *value.FirstReconnectIntervalID
		value.FirstReconnectIntervalID = &id
	}
	if value.SecondReconnectIntervalID != nil {
		id := *value.SecondReconnectIntervalID
		value.SecondReconnectIntervalID = &id
	}
	return value
}

func clonePauseResumeIntervalInput(value *arena.PauseResumeIntervalInput) *arena.PauseResumeIntervalInput {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePauseResumeParticipantResolution(value arena.PauseResumeParticipantResolution) arena.PauseResumeParticipantResolution {
	if value.SourceInterval != nil {
		item := clonePauseGraph(arena.PauseGraph{Reconnect: []arena.PauseReconnectInterval{*value.SourceInterval}}).Reconnect[0]
		value.SourceInterval = &item
	}
	if value.CurrentInterval != nil {
		item := clonePauseGraph(arena.PauseGraph{Reconnect: []arena.PauseReconnectInterval{*value.CurrentInterval}}).Reconnect[0]
		value.CurrentInterval = &item
	}
	return value
}

var _ arena.PauseResumePresenceRepository = (*pauseResumePresenceRepositoryFake)(nil)
