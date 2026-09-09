package game_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/google/uuid"
)

func TestPauseResumeBothAbsentMatrix(t *testing.T) {
	t.Parallel()
	decidedAt := time.Date(2026, time.August, 31, 16, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name        string
		preexisting [2]bool
		windows     [2]time.Duration
	}{
		{name: "P P", preexisting: [2]bool{true, true}},
		{name: "P N", preexisting: [2]bool{true, false}, windows: [2]time.Duration{0, 55 * time.Second}},
		{name: "N P", preexisting: [2]bool{false, true}, windows: [2]time.Duration{35 * time.Second, 0}},
		{name: "N N", windows: [2]time.Duration{30 * time.Second, 70 * time.Second}},
	} {
		t.Run(test.name+" keeps independent lineage counters and deadlines", func(t *testing.T) {
			t.Parallel()
			authority, command := pauseResumePresenceFixture(t, decidedAt, test.preexisting)
			series := authority.Resume.Pause.Graph.Series[0].Execution.Series
			participants := [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}
			inputs := [2]*gameusecase.PauseResumeIntervalInput{}
			for index, participantID := range participants {
				if !test.preexisting[index] {
					disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, participantID), authority.Resume.Pause.PausedAt.Add(time.Duration(10+index*10)*time.Second), 1)
				}
				inputs[index] = &gameusecase.PauseResumeIntervalInput{ParticipantID: participantID, IntervalID: uuid.New(), Window: test.windows[index]}
			}
			command.FirstInterval, command.SecondInterval = inputs[0], inputs[1]
			if test.name == "P P" {
				live := reconnectCounterForParticipant(t, authority.Resume.Counters, participants[0])
				snapshot := reconnectCounterForParticipant(t, authority.Resume.Pause.Graph.Counters, participants[0])
				live.Limit, snapshot.Limit = live.Used, snapshot.Used
			}
			refreshPauseResumePresenceExpectation(&authority, &command)
			beforeCounters := [2]pausedomain.PauseReconnectCounter{
				*reconnectCounterForParticipant(t, authority.Resume.Counters, participants[0]),
				*reconnectCounterForParticipant(t, authority.Resume.Counters, participants[1]),
			}
			if test.name == "N P" {
				reversePauseResumeEvidence(&authority)
				refreshPauseResumePresenceExpectation(&authority, &command)
			}
			repository := newPauseResumePresenceRepositoryHarness(t, authority)
			record, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
			if err != nil || !changed || record.GameDecision.Action != gameusecase.PauseResumeActionWaitBoth ||
				record.First.ParticipantID != participants[0] || record.Second.ParticipantID != participants[1] ||
				record.First.CurrentInterval == nil || record.Second.CurrentInterval == nil ||
				record.First.CurrentInterval.ID == record.Second.CurrentInterval.ID ||
				record.First.CurrentInterval.Deadline.Equal(record.Second.CurrentInterval.Deadline) {
				t.Fatalf("Resume() error = %v, changed = %v, record = %+v", err, changed, record)
			}
			assertReconnectWaitRecord(t, authority.Resume.Pause.Graph, *record)
			assertOnlyGameDecisionWrite(t, repository)
			assertDecisionReconnectIntervals(t, record.GameDecision, record.First.CurrentInterval, record.Second.CurrentInterval)
			resolutions := [2]gameusecase.PauseResumeParticipantResolution{record.First, record.Second}
			for index, resolution := range resolutions {
				current := resolution.CurrentInterval
				if current.PauseID != authority.GameDecision.PauseID || current.ParticipantID != participants[index] ||
					current.ID != inputs[index].IntervalID || current.State != pausedomain.ReconnectStateOpen || current.ClosedAt != nil ||
					!current.OpenedAt.Equal(decidedAt) {
					t.Fatalf("participant %d current interval = %+v", index, current)
				}
				assertCurrentReconnectIdentity(t, current, authority, participants[index], decidedAt)
				if test.preexisting[index] {
					source := suspendedReconnectForParticipant(t, authority.Resume.Pause, participants[index])
					remaining := source.Deadline.Sub(*source.ClosedAt)
					if resolution.Disposition != gameusecase.PauseResumeParticipantContinuation || resolution.SourceInterval == nil ||
						!reflect.DeepEqual(*resolution.SourceInterval, source) || current.Number != source.Number ||
						current.ContinuationNumber != source.ContinuationNumber+1 || current.ContinuedFromID == nil || *current.ContinuedFromID != source.ID ||
						current.SuspendedByPauseID != nil ||
						!current.Deadline.Equal(decidedAt.Add(remaining)) || !reflect.DeepEqual(resolution.Counter, beforeCounters[index]) ||
						!reflect.DeepEqual(reconnectByID(t, record.Graph.Reconnect, source.ID), source) {
						t.Fatalf("participant %d continuation = %+v, source = %+v", index, resolution, source)
					}
					assertNormalSuspendedSource(t, source, command.Resume.PauseID, authority.Resume.Pause.PausedAt)
					assertContinuationReconnectIdentity(t, source, *current)
				} else if resolution.Disposition != gameusecase.PauseResumeParticipantFresh || resolution.SourceInterval != nil ||
					current.Number != beforeCounters[index].Used+1 || current.ContinuationNumber != 0 ||
					current.ContinuedFromID != nil || current.SuspendedByPauseID != nil ||
					!current.Deadline.Equal(decidedAt.Add(test.windows[index])) ||
					resolution.Counter.Used != beforeCounters[index].Used+1 || resolution.Counter.Revision != beforeCounters[index].Revision+1 {
					t.Fatalf("participant %d fresh interval = %+v", index, resolution)
				}
			}
			revisions := gameusecase.PauseGraphRevisionsFrom(record.Graph)
			for _, resolution := range resolutions {
				if !pauseChildRevisionContains(revisions.Reconnect, resolution.CurrentInterval.ID) {
					t.Fatalf("Reconnect revisions omit current segment %s: %+v", resolution.CurrentInterval.ID, revisions.Reconnect)
				}
				if resolution.SourceInterval != nil && !pauseChildRevisionContains(revisions.Reconnect, resolution.SourceInterval.ID) {
					t.Fatalf("Reconnect revisions omit source segment %s: %+v", resolution.SourceInterval.ID, revisions.Reconnect)
				}
			}
		})
	}

	t.Run("swapped participant wiring and child Game wiring fail closed", func(t *testing.T) {
		t.Parallel()
		for _, test := range []struct {
			name   string
			mutate func(*gameusecase.PauseResumePresenceAuthority, *gameusecase.PauseResumePresenceCommand)
			want   error
		}{
			{name: "swapped interval inputs", want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				firstID := a.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
				secondID := a.Resume.Pause.Graph.Series[0].Execution.Series.SecondParticipantID
				disconnectPausePresence(presenceForParticipant(t, a.Resume.Presence, firstID), a.Resume.Pause.PausedAt.Add(time.Second), 1)
				disconnectPausePresence(presenceForParticipant(t, a.Resume.Presence, secondID), a.Resume.Pause.PausedAt.Add(2*time.Second), 1)
				refreshPauseResumePresenceExpectation(a, c)
				c.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: secondID, IntervalID: uuid.New(), Window: time.Minute}
				c.SecondInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
			}},
			{name: "wrong child Game", want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				gameID := uuid.New()
				a.GameDecision.GameID = gameID
				a.GameDecision.GameClock.GameID = gameID
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "child interval owner is normal pause", want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				for index := range a.Resume.Counters {
					a.Resume.Counters[index].PauseID = c.Resume.PauseID
				}
				refreshPauseResumePresenceExpectation(a, c)
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
				test.mutate(&authority, &command)
				repository := newPauseResumePresenceRepositoryHarness(t, authority)
				_, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
				if !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("malformed replay action and participant wiring fail closed", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare replay: error = %v, changed = %v", err, changed)
		}
		for _, mutate := range []func(*gameusecase.PauseResumePresenceRecord){
			func(record *gameusecase.PauseResumePresenceRecord) {
				record.GameDecision.Action = gameusecase.PauseResumeActionWaitFirst
			},
			func(record *gameusecase.PauseResumePresenceRecord) {
				record.First, record.Second = record.Second, record.First
			},
			func(record *gameusecase.PauseResumePresenceRecord) { record.GameDecision.DecisionNumber++ },
			func(record *gameusecase.PauseResumePresenceRecord) {
				record.GameDecision.ID, record.SeriesDecision.ID = record.SeriesDecision.ID, record.GameDecision.ID
			},
			func(record *gameusecase.PauseResumePresenceRecord) { record.GameDecision.ID = uuid.Nil },
			func(record *gameusecase.PauseResumePresenceRecord) { record.SeriesDecision.ID = uuid.Nil },
			func(record *gameusecase.PauseResumePresenceRecord) { record.SeriesDecision = nil },
			func(record *gameusecase.PauseResumePresenceRecord) { record.GameClock.GameID = uuid.New() },
			func(record *gameusecase.PauseResumePresenceRecord) {
				record.Graph.FrozenDeadlines[0].OriginalDeadline = record.Graph.FrozenDeadlines[0].OriginalDeadline.Add(time.Second)
				record.Graph.FrozenDeadlines[0].Remaining += time.Second
				shifted := record.Graph.FrozenDeadlines[0].ResumedDeadline.Add(time.Second)
				record.Graph.FrozenDeadlines[0].ResumedDeadline = &shifted
			},
		} {
			malformed := clonePauseResumePresenceRecord(*stored)
			mutate(&malformed)
			malformedRepository := newPauseResumePresenceRepositoryHarness(t, authority)
			malformedRepository.storeCommand(malformed)
			_, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), malformedRepository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), command)
			if !errors.Is(err, gameusecase.ErrPauseResumePresenceCommandReuse) || changed || malformedRepository.writeCount() != 0 {
				t.Fatalf("malformed replay error = %v, changed = %v", err, changed)
			}
		}
	})

	t.Run("stored baselines must match their revision projections", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{true, false})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New()}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare replay: error = %v, changed = %v", err, changed)
		}
		for _, test := range []struct {
			name   string
			mutate func(*gameusecase.PauseResumePresenceRecord)
		}{
			{name: "Presence", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Command.Resume.Expected.Presence[0].Revision++
			}},
			{name: "Reconnect", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Command.Resume.Expected.Reconnect[0].Revision++
			}},
			{name: "Counters", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Command.Resume.Expected.Counters[0].Revision++
			}},
			{name: "FrozenDeadlines", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Command.Resume.Expected.FrozenDeadlines[0].Revision++
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				malformed := clonePauseResumePresenceRecord(*stored)
				test.mutate(&malformed)
				malformedCommand := clonePauseResumePresenceCommand(malformed.Command)
				malformedRepository := newPauseResumePresenceRepositoryHarness(t, authority)
				malformedRepository.storeCommand(malformed)
				_, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), malformedRepository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), malformedCommand)
				if !errors.Is(err, gameusecase.ErrPauseResumePresenceCommandReuse) || changed || malformedRepository.writeCount() != 0 {
					t.Fatalf("malformed %s projection error = %v, changed = %v", test.name, err, changed)
				}
			})
		}
	})

	t.Run("malformed continuation predecessor duplicate and fork fail closed", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{true, false})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New()}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare continuation replay: error = %v, changed = %v", err, changed)
		}
		for _, test := range []struct {
			name   string
			mutate func(*gameusecase.PauseResumePresenceRecord)
		}{
			{name: "missing predecessor", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				current := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.CurrentInterval.ID)
				missing := uuid.New()
				current.ContinuedFromID = &missing
				record.First.CurrentInterval.ContinuedFromID = &missing
			}},
			{name: "duplicate continuation number", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				duplicate := *record.First.CurrentInterval
				duplicate.ID = uuid.New()
				record.Graph.Reconnect = append(record.Graph.Reconnect, duplicate)
			}},
			{name: "forked predecessor", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				fork := *record.First.CurrentInterval
				fork.ID = uuid.New()
				fork.ContinuationNumber++
				record.Graph.Reconnect = append(record.Graph.Reconnect, fork)
			}},
			{name: "predecessor is not cancelled", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				source := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.SourceInterval.ID)
				source.State = pausedomain.ReconnectStateReconnected
				record.First.SourceInterval.State = pausedomain.ReconnectStateReconnected
			}},
			{name: "predecessor suspension is missing", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				source := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.SourceInterval.ID)
				source.SuspendedByPauseID = nil
				record.First.SourceInterval.SuspendedByPauseID = nil
			}},
			{name: "predecessor suspension names another normal pause", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				source := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.SourceInterval.ID)
				wrong := uuid.New()
				source.SuspendedByPauseID = &wrong
				record.First.SourceInterval.SuspendedByPauseID = &wrong
			}},
			{name: "open continuation is prematurely suspended", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				current := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.CurrentInterval.ID)
				normalPauseID := record.Command.Resume.PauseID
				current.SuspendedByPauseID = &normalPauseID
				record.First.CurrentInterval.SuspendedByPauseID = &normalPauseID
			}},
			{name: "source and continuation deadlines shift together", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				source := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.SourceInterval.ID)
				current := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.CurrentInterval.ID)
				source.Deadline = source.Deadline.Add(time.Second)
				current.Deadline = current.Deadline.Add(time.Second)
				record.First.SourceInterval.Deadline = source.Deadline
				record.First.CurrentInterval.Deadline = current.Deadline
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				malformed := clonePauseResumePresenceRecord(*stored)
				test.mutate(&malformed)
				malformedRepository := newPauseResumePresenceRepositoryHarness(t, authority)
				malformedRepository.storeCommand(malformed)
				_, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), malformedRepository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), command)
				if !errors.Is(err, gameusecase.ErrPauseResumePresenceCommandReuse) || changed || malformedRepository.writeCount() != 0 {
					t.Fatalf("malformed replay error = %v, changed = %v", err, changed)
				}
			})
		}
	})

	t.Run("malformed wait timing and extra interval set fail closed", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), authority.Resume.Pause.PausedAt.Add(time.Second), 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare wait replay: error = %v, changed = %v", err, changed)
		}
		for _, test := range []struct {
			name   string
			mutate func(*gameusecase.PauseResumePresenceRecord)
		}{
			{name: "fresh deadline changed with graph row", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				current := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.CurrentInterval.ID)
				current.Deadline = current.Deadline.Add(time.Second)
				record.First.CurrentInterval.Deadline = record.First.CurrentInterval.Deadline.Add(time.Second)
			}},
			{name: "fresh open time changed with graph row", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				current := reconnectIntervalPointerByID(t, record.Graph.Reconnect, record.First.CurrentInterval.ID)
				current.OpenedAt = current.OpenedAt.Add(time.Second)
				current.UpdatedAt = current.UpdatedAt.Add(time.Second)
				current.Deadline = current.Deadline.Add(time.Second)
				record.First.CurrentInterval.OpenedAt = current.OpenedAt
				record.First.CurrentInterval.UpdatedAt = current.UpdatedAt
				record.First.CurrentInterval.Deadline = current.Deadline
			}},
			{name: "extra coherent root", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				extra := *record.First.CurrentInterval
				extra.ID = uuid.New()
				extra.Number++
				extra.Deadline = extra.Deadline.Add(time.Second)
				record.Graph.Reconnect = append(record.Graph.Reconnect, extra)
				counter := reconnectCounterForParticipant(t, record.Graph.Counters, firstID)
				counter.Used++
				record.First.Counter.Used++
			}},
			{name: "frozen deadline values changed without revision", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				record.Graph.FrozenDeadlines[0].OriginalDeadline = record.Graph.FrozenDeadlines[0].OriginalDeadline.Add(time.Second)
				record.Graph.FrozenDeadlines[0].Remaining += time.Second
			}},
			{name: "counter limit changes at the same revision", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				counter := reconnectCounterForParticipant(t, record.Graph.Counters, firstID)
				counter.Limit++
				record.First.Counter.Limit++
			}},
			{name: "Presence timestamp changes at the same revision", mutate: func(record *gameusecase.PauseResumePresenceRecord) {
				presence := presenceForParticipant(t, record.Graph.Presence, firstID)
				presence.UpdatedAt = presence.UpdatedAt.Add(time.Second)
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				malformed := clonePauseResumePresenceRecord(*stored)
				test.mutate(&malformed)
				malformedRepository := newPauseResumePresenceRepositoryHarness(t, authority)
				malformedRepository.storeCommand(malformed)
				_, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), malformedRepository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), command)
				if !errors.Is(err, gameusecase.ErrPauseResumePresenceCommandReuse) || changed || malformedRepository.writeCount() != 0 {
					t.Fatalf("malformed wait replay error = %v, changed = %v", err, changed)
				}
			})
		}
	})

	t.Run("equivalent decision instant timezone replays", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		disconnectPausePresence(presenceForParticipant(t, authority.Resume.Presence, firstID), authority.Resume.Pause.PausedAt.Add(time.Second), 1)
		refreshPauseResumePresenceExpectation(&authority, &command)
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New(), Window: time.Minute}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare timezone replay: error = %v, changed = %v", err, changed)
		}
		equivalent := stored.DecidedAt.In(time.FixedZone("JST", 9*60*60))
		stored.DecidedAt = equivalent
		replayRepository := newPauseResumePresenceRepositoryHarness(t, authority)
		replayRepository.storeCommand(*stored)
		replayed, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), replayRepository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), command)
		if err != nil || changed || replayed == nil || !replayed.DecidedAt.Equal(decidedAt) || replayRepository.writeCount() != 0 {
			t.Fatalf("timezone replay error = %v, changed = %v, record = %+v", err, changed, replayed)
		}
	})

	t.Run("equivalent baseline instants in another location replay", func(t *testing.T) {
		t.Parallel()
		authority, command := pauseResumePresenceFixture(t, decidedAt, [2]bool{true, false})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		command.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New()}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		stored, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt)).Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare baseline replay: error = %v, changed = %v", err, changed)
		}
		location := time.FixedZone("JST", 9*60*60)
		stored.Command.SeriesExpected.StartedAt = stored.Command.SeriesExpected.StartedAt.In(location)
		stored.Command.GameExpected.StartedAt = stored.Command.GameExpected.StartedAt.In(location)
		stored.Command.GameExpected.GameClock.OriginalDeadline = stored.Command.GameExpected.GameClock.OriginalDeadline.In(location)
		stored.Command.GameExpected.GameClock.FrozenAt = stored.Command.GameExpected.GameClock.FrozenAt.In(location)
		for index := range stored.Command.Presence {
			stored.Command.Presence[index].ConnectedAt = stored.Command.Presence[index].ConnectedAt.In(location)
			stored.Command.Presence[index].UpdatedAt = stored.Command.Presence[index].UpdatedAt.In(location)
			if stored.Command.Presence[index].DisconnectedAt != nil {
				at := stored.Command.Presence[index].DisconnectedAt.In(location)
				stored.Command.Presence[index].DisconnectedAt = &at
			}
		}
		for index := range stored.Command.Reconnect {
			stored.Command.Reconnect[index].OpenedAt = stored.Command.Reconnect[index].OpenedAt.In(location)
			stored.Command.Reconnect[index].Deadline = stored.Command.Reconnect[index].Deadline.In(location)
			stored.Command.Reconnect[index].UpdatedAt = stored.Command.Reconnect[index].UpdatedAt.In(location)
			if stored.Command.Reconnect[index].ClosedAt != nil {
				at := stored.Command.Reconnect[index].ClosedAt.In(location)
				stored.Command.Reconnect[index].ClosedAt = &at
			}
		}
		for index := range stored.Command.FrozenDeadlines {
			stored.Command.FrozenDeadlines[index].OriginalDeadline = stored.Command.FrozenDeadlines[index].OriginalDeadline.In(location)
			stored.Command.FrozenDeadlines[index].FrozenAt = stored.Command.FrozenDeadlines[index].FrozenAt.In(location)
		}
		replayRepository := newPauseResumePresenceRepositoryHarness(t, authority)
		replayRepository.storeCommand(*stored)
		replayed, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), replayRepository, newPauseClock(t, decidedAt.Add(time.Second))).Resume(t.Context(), command)
		if err != nil || changed || replayed == nil || replayRepository.writeCount() != 0 {
			t.Fatalf("baseline replay error = %v, changed = %v, record = %+v", err, changed, replayed)
		}
	})

	t.Run("same head permits one of two distinct contenders", func(t *testing.T) {
		t.Parallel()
		authority, firstCommand := pauseResumePresenceFixture(t, decidedAt, [2]bool{true, false})
		firstID := authority.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
		firstCommand.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New()}
		secondCommand := firstCommand
		secondCommand.Resume.CommandID = uuid.New()
		secondCommand.Resume.ActorID = uuid.New()
		secondCommand.GameDecisionID = uuid.New()
		secondCommand.SeriesDecisionID = uuid.New()
		secondCommand.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: firstID, IntervalID: uuid.New()}
		repository := newPauseResumePresenceRepositoryHarness(t, authority)
		useCase := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, decidedAt))
		type result struct {
			record  *gameusecase.PauseResumePresenceRecord
			changed bool
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for _, command := range []gameusecase.PauseResumePresenceCommand{firstCommand, secondCommand} {
			go func() {
				<-start
				record, changed, err := useCase.Resume(t.Context(), command)
				results <- result{record, changed, err}
			}()
		}
		close(start)
		first, second := <-results, <-results
		changedCount, conflictCount := 0, 0
		for _, got := range []result{first, second} {
			if got.changed && got.err == nil {
				changedCount++
			}
			if !got.changed && errors.Is(got.err, gameusecase.ErrPauseResumePresenceConflict) {
				conflictCount++
			}
		}
		if changedCount != 1 || conflictCount != 1 || repository.writeCount() != 1 {
			t.Fatalf("contenders = %+v, %+v, writes = %d", first, second, repository.writeCount())
		}
		winner := first
		if !winner.changed {
			winner = second
		}
		if winner.record == nil || winner.record.First.CurrentInterval == nil || winner.record.First.CurrentInterval.ContinuedFromID == nil {
			t.Fatalf("winner did not append exactly one continuation: %+v", winner)
		}
	})
}
