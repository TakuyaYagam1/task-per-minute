package reconnect_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func TestReconnectSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	authority := task045Authority(now, true, false)
	interval := authority.Reconnect[0]
	command := reconnectusecase.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(30),
		ParticipantID: authority.Series.FirstParticipantID, IntervalID: interval.ID,
		Settlement: task045SettlementIDs(31)}
	repository := newTask045RepositoryHarness(t, authority)
	useCase := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now))

	reconnected, changed, err := useCase.Reconnect(t.Context(), command)
	if err != nil || !changed {
		t.Fatalf("Reconnect() error = %v, changed = %v, want success", err, changed)
	}
	if reconnected.ExpectedAuthorityRevision != authority.Revision || reconnected.ReconnectAuthority.Revision != authority.Revision+1 {
		t.Fatalf("authority revisions = (%d, %d), want (%d, %d)", reconnected.ExpectedAuthorityRevision, reconnected.ReconnectAuthority.Revision, authority.Revision, authority.Revision+1)
	}
	gotPresence := task045Presence(t, reconnected.ReconnectAuthority, command.ParticipantID)
	if gotPresence.State != pause.PresenceStateConnected || gotPresence.PresenceEpoch != 3 || gotPresence.Revision != 3 ||
		gotPresence.DisconnectedAt != nil || !gotPresence.ConnectedAt.Equal(now) || !gotPresence.UpdatedAt.Equal(now) {
		t.Fatalf("reconnected Presence = %+v", gotPresence)
	}
	gotInterval := task045Interval(t, reconnected.ReconnectAuthority, interval.ID)
	if gotInterval.State != pause.ReconnectStateReconnected || gotInterval.Revision != interval.Revision+1 ||
		gotInterval.ClosedAt == nil || !gotInterval.ClosedAt.Equal(now) || !gotInterval.UpdatedAt.Equal(now) {
		t.Fatalf("closed reconnect interval = %+v", gotInterval)
	}
	if reconnected.ReconnectAuthority.Game.State != domain.GameStateActive || reconnected.ReconnectAuthority.GameRevision != authority.GameRevision+1 {
		t.Fatalf("Game = %+v, revision = %d", reconnected.ReconnectAuthority.Game, reconnected.ReconnectAuthority.GameRevision)
	}
	if reconnected.ReconnectAuthority.SeriesRevision != authority.SeriesRevision+1 ||
		reconnected.ReconnectAuthority.Series.Slots[0].Attempts[0] != reconnected.ReconnectAuthority.Game {
		t.Fatalf("Series did not mirror resumed Game: Series revision %d, Game %+v, slot %+v", reconnected.ReconnectAuthority.SeriesRevision, reconnected.ReconnectAuthority.Game, reconnected.ReconnectAuthority.Series.Slots[0].Attempts[0])
	}
	wantDeadline := now.Add(authority.GameClock.Remaining)
	clock := reconnected.ReconnectAuthority.GameClock
	if clock.ResumedAt == nil || !clock.ResumedAt.Equal(now) || clock.ResumedDeadline == nil ||
		!clock.ResumedDeadline.Equal(wantDeadline) || clock.Revision != authority.GameClock.Revision+1 {
		t.Fatalf("resumed Game clock = %+v, want deadline %v", clock, wantDeadline)
	}
	if got := task045Counter(t, reconnected.ReconnectAuthority, command.ParticipantID); got != authority.Counters[0] {
		t.Fatalf("counter changed on reconnect: got %+v want %+v", got, authority.Counters[0])
	}
	if got := task045Presence(t, reconnected.ReconnectAuthority, authority.Series.SecondParticipantID); !task045PresenceEqual(got, authority.Presence[1]) {
		t.Fatalf("opponent Presence changed: got %+v want %+v", got, authority.Presence[1])
	}
	if reconnected.GameResultRevision != nil || reconnected.ScoreRevision != nil || reconnected.Evidence != nil {
		t.Fatal("timely reconnect wrote terminal evidence")
	}

	writes := repository.writeCount()
	replayed, changed, err := useCase.Reconnect(t.Context(), command)
	if err != nil || changed || repository.writeCount() != writes {
		t.Fatalf("Reconnect(replay) error = %v, changed = %v, writes = %d, want no write", err, changed, repository.writeCount())
	}
	replayed.ReconnectAuthority.Presence[0].State = pause.PresenceStateDisconnected
	replayed.ReconnectAuthority.Reconnect[0].State = pause.ReconnectStateExpired
	stored := repository.snapshot()
	if task045Presence(t, stored, command.ParticipantID).State != pause.PresenceStateConnected || task045Interval(t, stored, interval.ID).State != pause.ReconnectStateReconnected {
		t.Fatal("mutating returned receipt changed repository authority")
	}

	reused := command
	reused.ParticipantID = authority.Series.SecondParticipantID
	_, changed, err = useCase.Reconnect(t.Context(), reused)
	if !errors.Is(err, reconnectusecase.ErrCommandReuse) || changed || repository.writeCount() != writes {
		t.Fatalf("Reconnect(command reuse) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
	}

	t.Run("deadline_is_exclusive", func(t *testing.T) {
		boundary := task045Authority(now, true, false)
		boundaryNow := boundary.Reconnect[0].Deadline
		repo := newTask045RepositoryHarness(t, boundary)
		uc := reconnectusecase.ReconnectNewUseCase(repo, newReconnectClock(t, boundaryNow))
		_, changed, err := uc.Reconnect(t.Context(), reconnectusecase.ReconnectCommand{Scope: boundary.Scope, CommandID: task045ID(38),
			ParticipantID: boundary.Series.FirstParticipantID, IntervalID: boundary.Reconnect[0].ID, Settlement: task045SettlementIDs(39)})
		if !errors.Is(err, reconnectusecase.ErrDeadline) || changed || repo.writeCount() != 0 {
			t.Fatalf("Reconnect(at deadline) error = %v, changed = %v, writes = %d", err, changed, repo.writeCount())
		}
	})

	t.Run("both_open_resume_only_after_second_reconnect", func(t *testing.T) {
		both := task045Authority(now, true, true)
		repository := newTask045RepositoryHarness(t, both)
		first, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
			Scope: both.Scope, CommandID: task045ID(320), ParticipantID: both.Series.FirstParticipantID,
			IntervalID: both.Reconnect[0].ID, Settlement: task045SettlementIDs(321),
		})
		if err != nil || !changed || first.ReconnectAuthority.Game.State != domain.GameStatePaused ||
			first.ReconnectAuthority.GameClock.ResumedDeadline != nil || first.ReconnectAuthority.GameRevision != both.GameRevision ||
			first.ReconnectAuthority.SeriesRevision != both.SeriesRevision {
			t.Fatalf("first of both reconnects resumed early: error = %v, record = %+v", err, first)
		}
		secondAt := now.Add(time.Second)
		second, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, secondAt)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
			Scope: both.Scope, CommandID: task045ID(330), ParticipantID: both.Series.SecondParticipantID,
			IntervalID: both.Reconnect[1].ID, Settlement: task045SettlementIDs(331),
		})
		wantResumedDeadline := secondAt.Add(both.GameClock.Remaining)
		if err != nil || !changed || second.ReconnectAuthority.Game.State != domain.GameStateActive ||
			second.ReconnectAuthority.GameClock.ResumedDeadline == nil || !second.ReconnectAuthority.GameClock.ResumedDeadline.Equal(wantResumedDeadline) ||
			second.ReconnectAuthority.GameClock.ResumedDeadline.Equal(both.GameClock.OriginalDeadline) ||
			second.ReconnectAuthority.GameClock.Revision != both.GameClock.Revision+1 {
			t.Fatalf("second reconnect did not resume exact Remaining: error = %v, clock = %+v", err, second.ReconnectAuthority.GameClock)
		}
	})

	t.Run("repository_owned_memory_is_not_returned", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		command := reconnectusecase.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(340),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(341)}
		repository := newTask045RepositoryHarness(t, authority)
		record, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Reconnect(aliasing fake) error = %v, changed = %v", err, changed)
		}
		repository.mutateReturned()
		if task045Presence(t, record.ReconnectAuthority, command.ParticipantID).State != pause.PresenceStateConnected ||
			task045Interval(t, record.ReconnectAuthority, command.IntervalID).State != pause.ReconnectStateReconnected {
			t.Fatal("use case returned repository-owned nested memory")
		}
	})

	t.Run("bad_commit_and_bad_receipt_are_internal_errors", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		command := reconnectusecase.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(350),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(351)}
		t.Run("bad_commit", func(t *testing.T) {
			repository := newTask045RepositoryHarness(t, authority)
			repository.mutateCommitted = func(record *reconnectusecase.ReconnectRecord) {
				record.RecordedAt = record.RecordedAt.Add(time.Nanosecond)
			}
			_, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), command)
			if !errors.Is(err, domain.ErrInternal) || changed {
				t.Fatalf("Reconnect(bad commit) error = %v, changed = %v", err, changed)
			}
		})
		t.Run("bad_receipt", func(t *testing.T) {
			repository := newTask045RepositoryHarness(t, authority)
			useCase := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now))
			if _, changed, err := useCase.Reconnect(t.Context(), command); err != nil || !changed {
				t.Fatalf("Reconnect(seed receipt) error = %v, changed = %v", err, changed)
			}
			repository.corruptReceipt(command.CommandID)
			_, changed, err := useCase.Reconnect(t.Context(), command)
			if !errors.Is(err, domain.ErrInternal) || changed {
				t.Fatalf("Reconnect(bad receipt) error = %v, changed = %v", err, changed)
			}
		})
	})

	t.Run("mutation_timestamps_must_advance", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		at := authority.Reconnect[0].UpdatedAt
		repository := newTask045RepositoryHarness(t, authority)
		_, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, at)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(380), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(381),
		})
		if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || changed || repository.writeCount() != 0 {
			t.Fatalf("Reconnect(same timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("durable_history_invariants_reject_without_write", func(t *testing.T) {
		for _, test := range []struct {
			name string
			make func() reconnectusecase.ReconnectAuthority
		}{
			{name: "counter_without_root", make: func() reconnectusecase.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				authority.Counters[0].Used = 1
				return authority
			}},
			{name: "duplicate_cycle_segment", make: func() reconnectusecase.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				duplicate := authority.Reconnect[0]
				duplicate.ID = task045ID(390)
				authority.Reconnect = append(authority.Reconnect, duplicate)
				return authority
			}},
			{name: "multiple_open_intervals", make: func() reconnectusecase.ReconnectAuthority {
				authority := task045Authority(now, true, false)
				second := authority.Reconnect[0]
				second.ID = task045ID(391)
				second.Number = 2
				authority.Reconnect = append(authority.Reconnect, second)
				authority.Counters[0].Used = 2
				authority.Counters[0].Revision = 3
				return authority
			}},
			{name: "open_epoch_does_not_match_presence", make: func() reconnectusecase.ReconnectAuthority {
				authority := task045Authority(now, true, false)
				authority.Reconnect[0].PresenceEpoch--
				return authority
			}},
			{name: "dangling_continuation", make: func() reconnectusecase.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				dangling := task045ID(392)
				openedAt, closedAt := now.Add(-40*time.Second), now.Add(-30*time.Second)
				authority.Reconnect = append(authority.Reconnect, pause.PauseReconnectInterval{
					ID: task045ID(393), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID,
					GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID, PresenceEpoch: 2, Number: 1,
					ContinuationNumber: 1, ContinuedFromID: &dangling, State: pause.ReconnectStateReconnected,
					OpenedAt: openedAt, Deadline: now.Add(time.Minute), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt,
				})
				return authority
			}},
			{name: "cyclic_continuation", make: func() reconnectusecase.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				firstID, secondID := task045ID(394), task045ID(395)
				openedAt, closedAt := now.Add(-40*time.Second), now.Add(-30*time.Second)
				suspended := task045ID(396)
				authority.Reconnect = append(authority.Reconnect,
					pause.PauseReconnectInterval{ID: firstID, PauseID: authority.PauseID, RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID,
						GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID, PresenceEpoch: 2, Number: 1,
						ContinuationNumber: 1, ContinuedFromID: &secondID, SuspendedByPauseID: &suspended,
						State: pause.ReconnectStateCancelled, OpenedAt: openedAt, Deadline: now.Add(time.Minute), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt},
					pause.PauseReconnectInterval{ID: secondID, PauseID: authority.PauseID, RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID,
						GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID, PresenceEpoch: 2, Number: 1,
						ContinuationNumber: 2, ContinuedFromID: &firstID, SuspendedByPauseID: &suspended,
						State: pause.ReconnectStateCancelled, OpenedAt: openedAt, Deadline: now.Add(time.Minute), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt})
				return authority
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := test.make()
				intervalID := task045ID(396)
				if len(authority.Reconnect) > 0 {
					intervalID = authority.Reconnect[0].ID
				}
				repository := newTask045RepositoryHarness(t, authority)
				_, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(397), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: intervalID, Settlement: task045SettlementIDs(398),
				})
				if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
					t.Fatalf("Reconnect(%s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})

	t.Run("persistent_conflict_is_bounded_to_two_attempts", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		repository := newTask045RepositoryHarness(t, authority)
		repository.alwaysConflict = true
		command := reconnectusecase.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(360),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(361)}
		_, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), command)
		if !errors.Is(err, reconnectusecase.ErrConflict) || changed || repository.commitCount() != 2 || repository.writeCount() != 0 {
			t.Fatalf("Reconnect(persistent conflict) error = %v, changed = %v, commits = %d, writes = %d", err, changed, repository.commitCount(), repository.writeCount())
		}
	})

	t.Run("expired_current_continuation_is_not_hidden_by_cancelled_predecessor", func(t *testing.T) {
		authority := task045Authority(now, true, true)
		root := &authority.Reconnect[0]
		closedAt := now
		root.State = pause.ReconnectStateCancelled
		root.ClosedAt = &closedAt
		root.UpdatedAt = closedAt
		root.Revision++
		suspendedBy := task045ID(370)
		root.SuspendedByPauseID = &suspendedBy
		rootID := root.ID
		continuationOpened := now.Add(time.Second)
		continuationDeadline := now.Add(21 * time.Second)
		authority.Reconnect[1].Deadline = now.Add(40 * time.Second)
		authority.Reconnect = append(authority.Reconnect, pause.PauseReconnectInterval{
			ID: task045ID(371), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID,
			SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID,
			PresenceEpoch: authority.Presence[0].PresenceEpoch, Number: 1, ContinuationNumber: 1, ContinuedFromID: &rootID,
			State: pause.ReconnectStateExpired, OpenedAt: continuationOpened, Deadline: continuationDeadline,
			ClosedAt: &continuationDeadline, Revision: 2, UpdatedAt: continuationDeadline,
		})
		reconnectAt := now.Add(25 * time.Second)
		repository := newTask045RepositoryHarness(t, authority)
		terminal, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, reconnectAt)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(372), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(373),
		})
		if err != nil || !changed || terminal.GameResultRevision == nil ||
			terminal.GameResultRevision.WinnerID != authority.Series.SecondParticipantID {
			t.Fatalf("Reconnect(current continuation expired) error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
	})

	t.Run("current_epoch_expiry_cannot_cross_replacement_game", func(t *testing.T) {
		authority := task045Authority(now, true, true)
		prior := task045ReplaceCurrentGame(&authority, task045ID(960), now)
		authority.Game.State = domain.GameStatePaused
		authority.Series.Slots[0].Attempts[1] = authority.Game
		authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
		authority.GameClock.Remaining = 40 * time.Second
		authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
		authority.GameClock.Revision = 2
		first := &authority.Reconnect[0]
		second := &authority.Reconnect[1]
		first.GameID = authority.Game.ID
		second.GameID = prior.ID
		second.State = pause.ReconnectStateExpired
		second.Deadline = now.Add(-time.Second)
		second.ClosedAt = &second.Deadline
		second.UpdatedAt = second.Deadline
		second.Revision = 2
		repository := newTask045RepositoryHarness(t, authority)
		_, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(961), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: first.ID, Settlement: task045SettlementIDs(962),
		})
		if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || !strings.Contains(err.Error(), "current reconnect interval belongs to predecessor Game") ||
			changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
			t.Fatalf("Reconnect(cross-Game current expiry) error = %v, changed = %v, writes = %d, commits = %d", err, changed, repository.writeCount(), repository.commitCount())
		}
	})
}
