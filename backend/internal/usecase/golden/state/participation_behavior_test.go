package state_test

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenParticipationEstablished(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 1, 9, 0, 5, 0, time.UTC)
	state := stateGoldenStateFixture(t, now.Add(-5*time.Second))
	repository := stateNewGoldenStateRepository(t, state)
	usecase := goldenusecase.NewGoldenParticipationUseCase(repository, stateNewGoldenClock(t, now))
	participantID := state.Group.Members[0].ParticipantID
	window := state.Windows[0]
	command := goldenusecase.GoldenReadyCommand{
		Scope: state.Scope, CommandID: stateTask047ID(100),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState:           state.Expectation(),
		ExpectedWindow:          window.Expectation(),
		NextStateRevisionID:     stateTask047ID(101),
		NextWindowRevisionID:    stateTask047ID(102),
		NextReadinessRevisionID: stateTask047ID(103),
	}

	ready, changed, err := usecase.AcceptReady(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, ready.Group.ParticipationEstablished)
	require.Equal(t, int64(2), ready.Revision)
	require.Equal(t, state.RevisionID, *ready.PreviousRevisionID)
	require.NotEqual(t, state.PayloadDigest, ready.PayloadDigest)
	require.Equal(t, []uuid.UUID{participantID}, ready.Windows[0].ReadyParticipantIDs)
	require.Len(t, ready.ReadyEvents, 1)
	require.Equal(t, goldenusecase.GoldenReadyEventAccepted, ready.ReadyEvents[0].Type)
	require.Equal(t, 1, repository.commitCount())

	disconnect := goldenusecase.GoldenDisconnectCommand{
		Scope: ready.Scope, CommandID: stateTask047ID(110), ParticipantID: participantID,
		AttemptID: ready.Windows[0].AttemptID, WindowID: ready.Windows[0].ID,
		ExpectedState:           ready.Expectation(),
		ExpectedWindow:          ready.Windows[0].Expectation(),
		NextStateRevisionID:     stateTask047ID(111),
		NextWindowRevisionID:    stateTask047ID(112),
		NextReadinessRevisionID: stateTask047ID(113),
		NextPresenceRevisionID:  stateTask047ID(114),
	}
	cleared, changed, err := usecase.ClearOnDisconnect(t.Context(), disconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, cleared.Group.ParticipationEstablished)
	require.Empty(t, cleared.Windows[0].ReadyParticipantIDs)

	restarted := goldenusecase.NewGoldenParticipationUseCase(repository, stateNewGoldenClock(t, now))
	replayed, changed, err := restarted.AcceptReady(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, replayed.Group.ParticipationEstablished)
	require.Empty(t, replayed.Windows[0].ReadyParticipantIDs)
	require.Equal(t, 2, repository.commitCount(), "replay after authority drift must not write")

	reused := command
	reused.ParticipantID = state.Group.Members[1].ParticipantID
	reused.ActorParticipantID = reused.ParticipantID
	conflict, changed, err := restarted.AcceptReady(t.Context(), reused)
	require.Nil(t, conflict)
	require.False(t, changed)
	require.ErrorIs(t, err, goldenusecase.ErrGoldenCommandReuse)
	require.Equal(t, 2, repository.commitCount())

	replayed.Windows[0].ReadyParticipantIDs = append(replayed.Windows[0].ReadyParticipantIDs, stateTask047ID(999))
	reloaded, loadErr := repository.LoadGoldenState(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	require.Empty(t, reloaded.Windows[0].ReadyParticipantIDs, "returned snapshots must not alias storage")

	t.Run("rejects state synthesized outside retained ready evidence", func(t *testing.T) {
		synthesized := state.Snapshot()
		synthesized.Group.ParticipationEstablished = true
		_, buildErr := goldenusecase.BuildGoldenState(synthesized)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)

		clearedEvidence := ready.Snapshot()
		clearedEvidence.Group.ParticipationEstablished = false
		_, buildErr = goldenusecase.BuildGoldenState(clearedEvidence)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
	})

	t.Run("binds topology interval plan and retained revision chain", func(t *testing.T) {
		wrongInterval := state.Snapshot()
		wrongInterval.Group.PositionFrom++
		wrongInterval.Group.PositionTo++
		_, buildErr := goldenusecase.BuildGoldenState(wrongInterval)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)

		wrongPlan := state.Snapshot()
		wrongPlan.Plan.PlanID = stateTask047ID(980)
		_, buildErr = goldenusecase.BuildGoldenState(wrongPlan)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)

		wrongChain := ready.Snapshot()
		wrongChain.ReadyEvents[0].ResultPresenceRevisionID = stateTask047ID(981)
		_, buildErr = goldenusecase.BuildGoldenState(wrongChain)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
	})

	t.Run("retains explicit no-op receipts without changing window evidence", func(t *testing.T) {
		local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
		localRepository := stateNewGoldenStateRepository(t, local)
		localParticipantID := local.Group.Members[0].ParticipantID
		first := stateGoldenAcceptReady(t, localRepository, local, localParticipantID, now, 150)
		beforeWindow := first.Windows[0].Expectation()
		duplicate := goldenusecase.GoldenReadyCommand{
			Scope: first.Scope, CommandID: stateTask047ID(160),
			ActorParticipantID: localParticipantID, ParticipantID: localParticipantID,
			AttemptID: first.Windows[0].AttemptID, WindowID: first.Windows[0].ID,
			ExpectedState: first.Expectation(), ExpectedWindow: beforeWindow,
			NextStateRevisionID: stateTask047ID(161), NextWindowRevisionID: stateTask047ID(162),
			NextReadinessRevisionID: stateTask047ID(163),
		}
		noOpReady, duplicateChanged, duplicateErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now.Add(time.Second)),
		).AcceptReady(t.Context(), duplicate)
		require.NoError(t, duplicateErr)
		require.True(t, duplicateChanged, "the durable receipt advances state")
		require.Equal(t, beforeWindow, noOpReady.Windows[0].Expectation())
		require.Equal(t, goldenusecase.GoldenReadyEventAlreadyReady, noOpReady.ReadyEvents[1].Type)

		disconnected, disconnectChanged, disconnectErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now.Add(2*time.Second)),
		).ClearOnDisconnect(t.Context(), goldenusecase.GoldenDisconnectCommand{
			Scope: noOpReady.Scope, CommandID: stateTask047ID(170), ParticipantID: localParticipantID,
			AttemptID: noOpReady.Windows[0].AttemptID, WindowID: noOpReady.Windows[0].ID,
			ExpectedState: noOpReady.Expectation(), ExpectedWindow: noOpReady.Windows[0].Expectation(),
			NextStateRevisionID: stateTask047ID(171), NextWindowRevisionID: stateTask047ID(172),
			NextReadinessRevisionID: stateTask047ID(173), NextPresenceRevisionID: stateTask047ID(174),
		})
		require.NoError(t, disconnectErr)
		require.True(t, disconnectChanged)
		beforeAbsent := disconnected.Windows[0].Expectation()
		absent := goldenusecase.GoldenDisconnectCommand{
			Scope: disconnected.Scope, CommandID: stateTask047ID(180), ParticipantID: localParticipantID,
			AttemptID: disconnected.Windows[0].AttemptID, WindowID: disconnected.Windows[0].ID,
			ExpectedState: disconnected.Expectation(), ExpectedWindow: beforeAbsent,
			NextStateRevisionID: stateTask047ID(181), NextWindowRevisionID: stateTask047ID(182),
			NextReadinessRevisionID: stateTask047ID(183), NextPresenceRevisionID: stateTask047ID(184),
		}
		noOpAbsent, absentChanged, absentErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now.Add(3*time.Second)),
		).ClearOnDisconnect(t.Context(), absent)
		require.NoError(t, absentErr)
		require.True(t, absentChanged)
		require.Equal(t, beforeAbsent, noOpAbsent.Windows[0].Expectation())
		require.Equal(t, goldenusecase.GoldenReadyEventAlreadyAbsent, noOpAbsent.ReadyEvents[3].Type)

		replayedNoOp, replayChanged, replayErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now.Add(4*time.Second)),
		).AcceptReady(t.Context(), duplicate)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, noOpAbsent.PayloadDigest, replayedNoOp.PayloadDigest)

		reusedNoOp := duplicate
		reusedNoOp.NextWindowRevisionID = stateTask047ID(185)
		conflict, reusedChanged, reusedErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now.Add(5*time.Second)),
		).AcceptReady(t.Context(), reusedNoOp)
		require.Nil(t, conflict)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenCommandReuse)
		require.Equal(t, 4, localRepository.commitCount())
	})

	t.Run("requires final ready transition to link current revisions", func(t *testing.T) {
		mutations := map[string]func(*goldenusecase.GoldenState){
			"state": func(value *goldenusecase.GoldenState) {
				value.ReadyEvents[0].ResultStateRevisionID = stateTask047ID(190)
			},
			"window": func(value *goldenusecase.GoldenState) {
				value.ReadyEvents[0].ResultWindowRevisionID = stateTask047ID(191)
			},
			"readiness": func(value *goldenusecase.GoldenState) {
				value.ReadyEvents[0].ResultReadinessRevisionID = stateTask047ID(192)
			},
			"presence": func(value *goldenusecase.GoldenState) {
				value.ReadyEvents[0].ResultPresenceRevisionID = stateTask047ID(193)
			},
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				tampered := ready.Snapshot()
				mutate(&tampered)
				_, buildErr := goldenusecase.BuildGoldenState(tampered)
				require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
			})
		}
	})

	t.Run("enforces the inclusive ready interval", func(t *testing.T) {
		openedAt := now.Add(-5 * time.Second)
		cases := []struct {
			name      string
			at        time.Time
			wantError bool
		}{
			{name: "before open", at: openedAt.Add(-time.Nanosecond), wantError: true},
			{name: "at open", at: openedAt},
			{name: "at deadline", at: openedAt.Add(30 * time.Second)},
			{name: "after deadline", at: openedAt.Add(30*time.Second + time.Nanosecond), wantError: true},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				local := stateGoldenStateFixture(t, openedAt)
				repository := stateNewGoldenStateRepository(t, local)
				participantID := local.Group.Members[0].ParticipantID
				result, changed, applyErr := goldenusecase.NewGoldenParticipationUseCase(
					repository,
					stateNewGoldenClock(t, testCase.at),
				).AcceptReady(t.Context(), goldenusecase.GoldenReadyCommand{
					Scope: local.Scope, CommandID: stateTask047ID(800 + index*10),
					ActorParticipantID: participantID, ParticipantID: participantID,
					AttemptID: local.Windows[0].AttemptID, WindowID: local.Windows[0].ID,
					ExpectedState: local.Expectation(), ExpectedWindow: local.Windows[0].Expectation(),
					NextStateRevisionID:     stateTask047ID(801 + index*10),
					NextWindowRevisionID:    stateTask047ID(802 + index*10),
					NextReadinessRevisionID: stateTask047ID(803 + index*10),
				})
				if testCase.wantError {
					require.Nil(t, result)
					require.False(t, changed)
					require.ErrorIs(t, applyErr, goldenusecase.ErrGoldenReadyWindowClosed)
					require.Zero(t, repository.commitCount())
					return
				}
				require.NoError(t, applyErr)
				require.True(t, changed)
			})
		}
	})

	t.Run("rejects foreign retained participants", func(t *testing.T) {
		forged := ready.Snapshot()
		forged.ReadyEvents[0].ParticipantID = stateTask047ID(950)
		_, buildErr := goldenusecase.BuildGoldenState(forged)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
	})

	t.Run("rejects every stale authority component without writing", func(t *testing.T) {
		type staleCase struct {
			name      string
			mutate    func(*goldenusecase.GoldenReadyCommand)
			wantError error
		}
		cases := []staleCase{
			{name: "actor", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ActorParticipantID = state.Group.Members[1].ParticipantID
			}, wantError: domain.ErrAssignmentParticipant},
			{name: "member", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ParticipantID = stateTask047ID(2000)
				value.ActorParticipantID = value.ParticipantID
			}, wantError: domain.ErrAssignmentParticipant},
			{name: "attempt", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.AttemptID = stateTask047ID(2001)
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "window", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.WindowID = stateTask047ID(2002)
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "plan", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedState.Plan.PlanID = stateTask047ID(2003)
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "group", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedState.Scope.GroupID = stateTask047ID(2004)
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "source", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedState.SourceProjectionPayloadDigest[0] ^= 0xff
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "state", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedState.Revision++
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "membership", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedState.Membership.Revision++
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "readiness", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedWindow.ReadinessRevision++
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
			{name: "presence", mutate: func(value *goldenusecase.GoldenReadyCommand) {
				value.ExpectedWindow.PresenceRevision++
			}, wantError: goldenusecase.ErrGoldenParticipationAuthorityConflict},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
				localRepository := stateNewGoldenStateRepository(t, local)
				command := goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2100+index*10)
				testCase.mutate(&command)
				result, changed, applyErr := goldenusecase.NewGoldenParticipationUseCase(
					localRepository,
					stateNewGoldenClock(t, now),
				).AcceptReady(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, applyErr, testCase.wantError)
				require.Zero(t, localRepository.commitCount())
			})
		}
	})

	t.Run("compares and copies semantic predecessor pointers", func(t *testing.T) {
		local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
		previousMembershipID := local.Membership.RevisionID
		local.Membership.PreviousRevisionID = &previousMembershipID
		local.Membership.RevisionID = stateTask047ID(2300)
		local.Membership.Revision = 2
		local, err = goldenusecase.BuildGoldenState(local)
		require.NoError(t, err)
		localRepository := stateNewGoldenStateRepository(t, local)
		command := goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2310)
		result, changed, applyErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), command)
		require.NoError(t, applyErr)
		require.True(t, changed)
		require.NotNil(t, result.ReadyEvents[0].ExpectedState.Membership.PreviousRevisionID)
		*result.ReadyEvents[0].ExpectedState.Membership.PreviousRevisionID = stateTask047ID(2320)
		reloaded, loadErr := localRepository.LoadGoldenState(t.Context(), local.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, previousMembershipID, *reloaded.ReadyEvents[0].ExpectedState.Membership.PreviousRevisionID)
	})

	t.Run("reconciles synchronized identical and competing first ready commands", func(t *testing.T) {
		type readyResult struct {
			state   *goldenusecase.GoldenState
			changed bool
			err     error
		}
		run := func(t *testing.T, commands [2]goldenusecase.GoldenReadyCommand) [2]readyResult {
			t.Helper()
			local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
			barrier := stateNewGoldenLoadBarrier(2)
			localRepository := stateNewGoldenStateRepository(t, local, withGoldenLoadBarrier(barrier))
			results := [2]readyResult{}
			var group sync.WaitGroup
			for index := range commands {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					results[index].state, results[index].changed, results[index].err =
						goldenusecase.NewGoldenParticipationUseCase(
							localRepository,
							stateNewGoldenClock(t, now),
						).AcceptReady(t.Context(), commands[index])
				}(index)
			}
			for range 2 {
				<-barrier.arrived
			}
			close(barrier.release)
			group.Wait()
			require.Equal(t, 1, localRepository.commitCount())
			return results
		}

		local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
		first := goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2400)
		identical := run(t, [2]goldenusecase.GoldenReadyCommand{first, first})
		require.NoError(t, identical[0].err)
		require.NoError(t, identical[1].err)
		require.NotEqual(t, identical[0].changed, identical[1].changed)

		second := goldenReadyCommand(local, local.Group.Members[1].ParticipantID, 2420)
		competing := run(t, [2]goldenusecase.GoldenReadyCommand{first, second})
		successes := 0
		conflicts := 0
		for _, result := range competing {
			if result.err == nil && result.changed {
				successes++
			}
			if errors.Is(result.err, goldenusecase.ErrGoldenParticipationAuthorityConflict) {
				conflicts++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, conflicts)
	})

	t.Run("surfaces repository failures and rejects malformed commit results", func(t *testing.T) {
		local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
		participantID := local.Group.Members[0].ParticipantID
		command := goldenReadyCommand(local, participantID, 2500)
		loadCause := errors.New("load failed")
		_, _, loadErr := goldenusecase.NewGoldenParticipationUseCase(
			stateNewGoldenStateRepository(t, local, withGoldenLoadError(loadCause)),
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, loadErr, loadCause)

		commitCause := errors.New("commit failed")
		_, _, commitErr := goldenusecase.NewGoldenParticipationUseCase(
			stateNewGoldenStateRepository(t, local, withGoldenCommitError(commitCause)),
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, commitErr, commitCause)

		malformed := local.Snapshot()
		malformed.PayloadDigest[0] ^= 0xff
		_, _, malformedLoadErr := goldenusecase.NewGoldenParticipationUseCase(
			stateNewGoldenStateRepository(t, malformed),
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, malformedLoadErr, domain.ErrInternal)

		nilCommit := stateNewGoldenStateRepository(
			t,
			local,
			withGoldenCommitHook(func(goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
				return nil, true, nil
			}),
		)
		_, _, malformedCommitErr := goldenusecase.NewGoldenParticipationUseCase(
			nilCommit,
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, malformedCommitErr, domain.ErrInternal)
	})

	t.Run("fails closed on revision overflow and identity aliases", func(t *testing.T) {
		local := stateGoldenStateFixture(t, now.Add(-5*time.Second))
		previousWindowID := local.Windows[0].RevisionID
		previousReadinessID := local.Windows[0].ReadinessRevisionID
		local.Windows[0].PreviousRevisionID = &previousWindowID
		local.Windows[0].RevisionID = stateTask047ID(2600)
		local.Windows[0].Revision = math.MaxInt64
		local.Windows[0].ReadinessPreviousRevisionID = &previousReadinessID
		local.Windows[0].ReadinessRevisionID = stateTask047ID(2601)
		local.Windows[0].ReadinessRevision = math.MaxInt64
		local, err = goldenusecase.BuildGoldenState(local)
		require.NoError(t, err)
		localRepository := stateNewGoldenStateRepository(t, local)
		_, changed, overflowErr := goldenusecase.NewGoldenParticipationUseCase(
			localRepository,
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2610))
		require.False(t, changed)
		require.ErrorIs(t, overflowErr, goldenusecase.ErrGoldenRevisionOverflow)
		require.Zero(t, localRepository.commitCount())

		aliased := stateGoldenStateFixture(t, now.Add(-5*time.Second))
		aliasRepository := stateNewGoldenStateRepository(t, aliased)
		aliasCommand := goldenReadyCommand(aliased, aliased.Group.Members[0].ParticipantID, 2630)
		aliasCommand.NextStateRevisionID = aliased.ExactPlan.Groups[0].Edges[0].ID
		_, changed, aliasErr := goldenusecase.NewGoldenParticipationUseCase(
			aliasRepository,
			stateNewGoldenClock(t, now),
		).AcceptReady(t.Context(), aliasCommand)
		require.False(t, changed)
		require.ErrorIs(t, aliasErr, goldenusecase.ErrInvalidGoldenState)
		require.Zero(t, aliasRepository.commitCount())

		for name, mutate := range map[string]func(*goldenusecase.GoldenState){
			"membership": func(value *goldenusecase.GoldenState) { value.RevisionID = value.Membership.RevisionID },
			"window":     func(value *goldenusecase.GoldenState) { value.RevisionID = value.Windows[0].RevisionID },
		} {
			t.Run(name, func(t *testing.T) {
				forged := stateGoldenStateFixture(t, now.Add(-5*time.Second))
				mutate(&forged)
				_, buildErr := goldenusecase.BuildGoldenState(forged)
				require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
			})
		}
	})
}

type goldenStateRepositoryState struct {
	mu         sync.Mutex
	state      goldenusecase.GoldenState
	commits    int
	loadErr    error
	commitErr  error
	commitHook func(goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error)
	barrier    *stateGoldenLoadBarrier
}
