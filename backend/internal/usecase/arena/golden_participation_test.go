package arena_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenParticipationEstablished(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 1, 9, 0, 5, 0, time.UTC)
	state := goldenStateFixture(t, now.Add(-5*time.Second))
	repository := &goldenStateRepositoryFake{state: state}
	usecase := arena.NewGoldenParticipationUseCase(repository, fixedArenaClock{now: now})
	participantID := state.Group.Members[0].ParticipantID
	window := state.Windows[0]
	command := arena.GoldenReadyCommand{
		Scope: state.Scope, CommandID: task047ID(100),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState:           state.Expectation(),
		ExpectedWindow:          window.Expectation(),
		NextStateRevisionID:     task047ID(101),
		NextWindowRevisionID:    task047ID(102),
		NextReadinessRevisionID: task047ID(103),
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
	require.Equal(t, arena.GoldenReadyEventAccepted, ready.ReadyEvents[0].Type)
	require.Equal(t, 1, repository.commitCount())

	disconnect := arena.GoldenDisconnectCommand{
		Scope: ready.Scope, CommandID: task047ID(110), ParticipantID: participantID,
		AttemptID: ready.Windows[0].AttemptID, WindowID: ready.Windows[0].ID,
		ExpectedState:           ready.Expectation(),
		ExpectedWindow:          ready.Windows[0].Expectation(),
		NextStateRevisionID:     task047ID(111),
		NextWindowRevisionID:    task047ID(112),
		NextReadinessRevisionID: task047ID(113),
		NextPresenceRevisionID:  task047ID(114),
	}
	cleared, changed, err := usecase.ClearOnDisconnect(t.Context(), disconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, cleared.Group.ParticipationEstablished)
	require.Empty(t, cleared.Windows[0].ReadyParticipantIDs)

	restarted := arena.NewGoldenParticipationUseCase(repository, fixedArenaClock{now: now})
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
	require.ErrorIs(t, err, arena.ErrGoldenCommandReuse)
	require.Equal(t, 2, repository.commitCount())

	replayed.Windows[0].ReadyParticipantIDs = append(replayed.Windows[0].ReadyParticipantIDs, task047ID(999))
	reloaded, loadErr := repository.LoadGoldenState(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	require.Empty(t, reloaded.Windows[0].ReadyParticipantIDs, "returned snapshots must not alias storage")

	t.Run("rejects state synthesized outside retained ready evidence", func(t *testing.T) {
		synthesized := state.Snapshot()
		synthesized.Group.ParticipationEstablished = true
		_, buildErr := arena.BuildGoldenState(synthesized)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)

		clearedEvidence := ready.Snapshot()
		clearedEvidence.Group.ParticipationEstablished = false
		_, buildErr = arena.BuildGoldenState(clearedEvidence)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
	})

	t.Run("binds topology interval plan and retained revision chain", func(t *testing.T) {
		wrongInterval := state.Snapshot()
		wrongInterval.Group.PositionFrom++
		wrongInterval.Group.PositionTo++
		_, buildErr := arena.BuildGoldenState(wrongInterval)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)

		wrongPlan := state.Snapshot()
		wrongPlan.Plan.PlanID = task047ID(980)
		_, buildErr = arena.BuildGoldenState(wrongPlan)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)

		wrongChain := ready.Snapshot()
		wrongChain.ReadyEvents[0].ResultPresenceRevisionID = task047ID(981)
		_, buildErr = arena.BuildGoldenState(wrongChain)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
	})

	t.Run("retains explicit no-op receipts without changing window evidence", func(t *testing.T) {
		local := goldenStateFixture(t, now.Add(-5*time.Second))
		localRepository := &goldenStateRepositoryFake{state: local}
		localParticipantID := local.Group.Members[0].ParticipantID
		first := goldenAcceptReady(t, localRepository, local, localParticipantID, now, 150)
		beforeWindow := first.Windows[0].Expectation()
		duplicate := arena.GoldenReadyCommand{
			Scope: first.Scope, CommandID: task047ID(160),
			ActorParticipantID: localParticipantID, ParticipantID: localParticipantID,
			AttemptID: first.Windows[0].AttemptID, WindowID: first.Windows[0].ID,
			ExpectedState: first.Expectation(), ExpectedWindow: beforeWindow,
			NextStateRevisionID: task047ID(161), NextWindowRevisionID: task047ID(162),
			NextReadinessRevisionID: task047ID(163),
		}
		noOpReady, duplicateChanged, duplicateErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now.Add(time.Second)},
		).AcceptReady(t.Context(), duplicate)
		require.NoError(t, duplicateErr)
		require.True(t, duplicateChanged, "the durable receipt advances state")
		require.Equal(t, beforeWindow, noOpReady.Windows[0].Expectation())
		require.Equal(t, arena.GoldenReadyEventAlreadyReady, noOpReady.ReadyEvents[1].Type)

		disconnected, disconnectChanged, disconnectErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now.Add(2 * time.Second)},
		).ClearOnDisconnect(t.Context(), arena.GoldenDisconnectCommand{
			Scope: noOpReady.Scope, CommandID: task047ID(170), ParticipantID: localParticipantID,
			AttemptID: noOpReady.Windows[0].AttemptID, WindowID: noOpReady.Windows[0].ID,
			ExpectedState: noOpReady.Expectation(), ExpectedWindow: noOpReady.Windows[0].Expectation(),
			NextStateRevisionID: task047ID(171), NextWindowRevisionID: task047ID(172),
			NextReadinessRevisionID: task047ID(173), NextPresenceRevisionID: task047ID(174),
		})
		require.NoError(t, disconnectErr)
		require.True(t, disconnectChanged)
		beforeAbsent := disconnected.Windows[0].Expectation()
		absent := arena.GoldenDisconnectCommand{
			Scope: disconnected.Scope, CommandID: task047ID(180), ParticipantID: localParticipantID,
			AttemptID: disconnected.Windows[0].AttemptID, WindowID: disconnected.Windows[0].ID,
			ExpectedState: disconnected.Expectation(), ExpectedWindow: beforeAbsent,
			NextStateRevisionID: task047ID(181), NextWindowRevisionID: task047ID(182),
			NextReadinessRevisionID: task047ID(183), NextPresenceRevisionID: task047ID(184),
		}
		noOpAbsent, absentChanged, absentErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now.Add(3 * time.Second)},
		).ClearOnDisconnect(t.Context(), absent)
		require.NoError(t, absentErr)
		require.True(t, absentChanged)
		require.Equal(t, beforeAbsent, noOpAbsent.Windows[0].Expectation())
		require.Equal(t, arena.GoldenReadyEventAlreadyAbsent, noOpAbsent.ReadyEvents[3].Type)

		replayedNoOp, replayChanged, replayErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now.Add(4 * time.Second)},
		).AcceptReady(t.Context(), duplicate)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, noOpAbsent.PayloadDigest, replayedNoOp.PayloadDigest)

		reusedNoOp := duplicate
		reusedNoOp.NextWindowRevisionID = task047ID(185)
		conflict, reusedChanged, reusedErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now.Add(5 * time.Second)},
		).AcceptReady(t.Context(), reusedNoOp)
		require.Nil(t, conflict)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, arena.ErrGoldenCommandReuse)
		require.Equal(t, 4, localRepository.commitCount())
	})

	t.Run("requires final ready transition to link current revisions", func(t *testing.T) {
		mutations := map[string]func(*arena.GoldenState){
			"state":     func(value *arena.GoldenState) { value.ReadyEvents[0].ResultStateRevisionID = task047ID(190) },
			"window":    func(value *arena.GoldenState) { value.ReadyEvents[0].ResultWindowRevisionID = task047ID(191) },
			"readiness": func(value *arena.GoldenState) { value.ReadyEvents[0].ResultReadinessRevisionID = task047ID(192) },
			"presence":  func(value *arena.GoldenState) { value.ReadyEvents[0].ResultPresenceRevisionID = task047ID(193) },
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				tampered := ready.Snapshot()
				mutate(&tampered)
				_, buildErr := arena.BuildGoldenState(tampered)
				require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
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
				local := goldenStateFixture(t, openedAt)
				repository := &goldenStateRepositoryFake{state: local}
				participantID := local.Group.Members[0].ParticipantID
				result, changed, applyErr := arena.NewGoldenParticipationUseCase(
					repository,
					fixedArenaClock{now: testCase.at},
				).AcceptReady(t.Context(), arena.GoldenReadyCommand{
					Scope: local.Scope, CommandID: task047ID(800 + index*10),
					ActorParticipantID: participantID, ParticipantID: participantID,
					AttemptID: local.Windows[0].AttemptID, WindowID: local.Windows[0].ID,
					ExpectedState: local.Expectation(), ExpectedWindow: local.Windows[0].Expectation(),
					NextStateRevisionID:     task047ID(801 + index*10),
					NextWindowRevisionID:    task047ID(802 + index*10),
					NextReadinessRevisionID: task047ID(803 + index*10),
				})
				if testCase.wantError {
					require.Nil(t, result)
					require.False(t, changed)
					require.ErrorIs(t, applyErr, arena.ErrGoldenReadyWindowClosed)
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
		forged.ReadyEvents[0].ParticipantID = task047ID(950)
		_, buildErr := arena.BuildGoldenState(forged)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
	})

	t.Run("rejects every stale authority component without writing", func(t *testing.T) {
		type staleCase struct {
			name      string
			mutate    func(*arena.GoldenReadyCommand)
			wantError error
		}
		cases := []staleCase{
			{name: "actor", mutate: func(value *arena.GoldenReadyCommand) {
				value.ActorParticipantID = state.Group.Members[1].ParticipantID
			}, wantError: domain.ErrArenaAssignmentParticipant},
			{name: "member", mutate: func(value *arena.GoldenReadyCommand) {
				value.ParticipantID = task047ID(2000)
				value.ActorParticipantID = value.ParticipantID
			}, wantError: domain.ErrArenaAssignmentParticipant},
			{name: "attempt", mutate: func(value *arena.GoldenReadyCommand) {
				value.AttemptID = task047ID(2001)
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "window", mutate: func(value *arena.GoldenReadyCommand) {
				value.WindowID = task047ID(2002)
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "plan", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedState.Plan.PlanID = task047ID(2003)
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "group", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedState.Scope.GroupID = task047ID(2004)
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "source", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedState.SourceProjectionPayloadDigest[0] ^= 0xff
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "state", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedState.Revision++
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "membership", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedState.Membership.Revision++
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "readiness", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedWindow.ReadinessRevision++
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
			{name: "presence", mutate: func(value *arena.GoldenReadyCommand) {
				value.ExpectedWindow.PresenceRevision++
			}, wantError: arena.ErrGoldenParticipationAuthorityConflict},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				local := goldenStateFixture(t, now.Add(-5*time.Second))
				localRepository := &goldenStateRepositoryFake{state: local}
				command := goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2100+index*10)
				testCase.mutate(&command)
				result, changed, applyErr := arena.NewGoldenParticipationUseCase(
					localRepository,
					fixedArenaClock{now: now},
				).AcceptReady(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, applyErr, testCase.wantError)
				require.Zero(t, localRepository.commitCount())
			})
		}
	})

	t.Run("compares and copies semantic predecessor pointers", func(t *testing.T) {
		local := goldenStateFixture(t, now.Add(-5*time.Second))
		previousMembershipID := local.Membership.RevisionID
		local.Membership.PreviousRevisionID = &previousMembershipID
		local.Membership.RevisionID = task047ID(2300)
		local.Membership.Revision = 2
		local, err = arena.BuildGoldenState(local)
		require.NoError(t, err)
		localRepository := &goldenStateRepositoryFake{state: local}
		command := goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2310)
		result, changed, applyErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), command)
		require.NoError(t, applyErr)
		require.True(t, changed)
		require.NotNil(t, result.ReadyEvents[0].ExpectedState.Membership.PreviousRevisionID)
		*result.ReadyEvents[0].ExpectedState.Membership.PreviousRevisionID = task047ID(2320)
		reloaded, loadErr := localRepository.LoadGoldenState(t.Context(), local.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, previousMembershipID, *reloaded.ReadyEvents[0].ExpectedState.Membership.PreviousRevisionID)
	})

	t.Run("reconciles synchronized identical and competing first ready commands", func(t *testing.T) {
		type readyResult struct {
			state   *arena.GoldenState
			changed bool
			err     error
		}
		run := func(t *testing.T, commands [2]arena.GoldenReadyCommand) [2]readyResult {
			t.Helper()
			local := goldenStateFixture(t, now.Add(-5*time.Second))
			barrier := newGoldenLoadBarrier(2)
			localRepository := &goldenStateRepositoryFake{state: local, barrier: barrier}
			results := [2]readyResult{}
			var group sync.WaitGroup
			for index := range commands {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					results[index].state, results[index].changed, results[index].err =
						arena.NewGoldenParticipationUseCase(
							localRepository,
							fixedArenaClock{now: now},
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

		local := goldenStateFixture(t, now.Add(-5*time.Second))
		first := goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2400)
		identical := run(t, [2]arena.GoldenReadyCommand{first, first})
		require.NoError(t, identical[0].err)
		require.NoError(t, identical[1].err)
		require.NotEqual(t, identical[0].changed, identical[1].changed)

		second := goldenReadyCommand(local, local.Group.Members[1].ParticipantID, 2420)
		competing := run(t, [2]arena.GoldenReadyCommand{first, second})
		successes := 0
		conflicts := 0
		for _, result := range competing {
			if result.err == nil && result.changed {
				successes++
			}
			if errors.Is(result.err, arena.ErrGoldenParticipationAuthorityConflict) {
				conflicts++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, conflicts)
	})

	t.Run("surfaces repository failures and rejects malformed commit results", func(t *testing.T) {
		local := goldenStateFixture(t, now.Add(-5*time.Second))
		participantID := local.Group.Members[0].ParticipantID
		command := goldenReadyCommand(local, participantID, 2500)
		loadCause := errors.New("load failed")
		_, _, loadErr := arena.NewGoldenParticipationUseCase(
			&goldenStateRepositoryFake{state: local, loadErr: loadCause},
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, loadErr, loadCause)

		commitCause := errors.New("commit failed")
		_, _, commitErr := arena.NewGoldenParticipationUseCase(
			&goldenStateRepositoryFake{state: local, commitErr: commitCause},
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, commitErr, commitCause)

		malformed := local.Snapshot()
		malformed.PayloadDigest[0] ^= 0xff
		_, _, malformedLoadErr := arena.NewGoldenParticipationUseCase(
			&goldenStateRepositoryFake{state: malformed},
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, malformedLoadErr, domain.ErrInternal)

		nilCommit := &goldenStateRepositoryFake{
			state: local,
			commitHook: func(arena.GoldenStateCommit) (*arena.GoldenState, bool, error) {
				return nil, true, nil
			},
		}
		_, _, malformedCommitErr := arena.NewGoldenParticipationUseCase(
			nilCommit,
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), command)
		require.ErrorIs(t, malformedCommitErr, domain.ErrInternal)
	})

	t.Run("fails closed on revision overflow and identity aliases", func(t *testing.T) {
		local := goldenStateFixture(t, now.Add(-5*time.Second))
		previousWindowID := local.Windows[0].RevisionID
		previousReadinessID := local.Windows[0].ReadinessRevisionID
		local.Windows[0].PreviousRevisionID = &previousWindowID
		local.Windows[0].RevisionID = task047ID(2600)
		local.Windows[0].Revision = math.MaxInt64
		local.Windows[0].ReadinessPreviousRevisionID = &previousReadinessID
		local.Windows[0].ReadinessRevisionID = task047ID(2601)
		local.Windows[0].ReadinessRevision = math.MaxInt64
		local, err = arena.BuildGoldenState(local)
		require.NoError(t, err)
		localRepository := &goldenStateRepositoryFake{state: local}
		_, changed, overflowErr := arena.NewGoldenParticipationUseCase(
			localRepository,
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), goldenReadyCommand(local, local.Group.Members[0].ParticipantID, 2610))
		require.False(t, changed)
		require.ErrorIs(t, overflowErr, arena.ErrGoldenRevisionOverflow)
		require.Zero(t, localRepository.commitCount())

		aliased := goldenStateFixture(t, now.Add(-5*time.Second))
		aliasRepository := &goldenStateRepositoryFake{state: aliased}
		aliasCommand := goldenReadyCommand(aliased, aliased.Group.Members[0].ParticipantID, 2630)
		aliasCommand.NextStateRevisionID = aliased.ExactPlan.Groups[0].Edges[0].ID
		_, changed, aliasErr := arena.NewGoldenParticipationUseCase(
			aliasRepository,
			fixedArenaClock{now: now},
		).AcceptReady(t.Context(), aliasCommand)
		require.False(t, changed)
		require.ErrorIs(t, aliasErr, arena.ErrInvalidGoldenState)
		require.Zero(t, aliasRepository.commitCount())

		for name, mutate := range map[string]func(*arena.GoldenState){
			"membership": func(value *arena.GoldenState) { value.RevisionID = value.Membership.RevisionID },
			"window":     func(value *arena.GoldenState) { value.RevisionID = value.Windows[0].RevisionID },
		} {
			t.Run(name, func(t *testing.T) {
				forged := goldenStateFixture(t, now.Add(-5*time.Second))
				mutate(&forged)
				_, buildErr := arena.BuildGoldenState(forged)
				require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
			})
		}
	})
}

type goldenStateRepositoryFake struct {
	mu         sync.Mutex
	state      arena.GoldenState
	commits    int
	loadErr    error
	commitErr  error
	commitHook func(arena.GoldenStateCommit) (*arena.GoldenState, bool, error)
	barrier    *goldenLoadBarrier
}

func (r *goldenStateRepositoryFake) LoadGoldenState(
	_ context.Context,
	_ arena.GoldenStateScope,
) (arena.GoldenState, error) {
	r.mu.Lock()
	state := r.state.Snapshot()
	loadErr := r.loadErr
	barrier := r.barrier
	r.mu.Unlock()
	if loadErr != nil {
		return arena.GoldenState{}, loadErr
	}
	if barrier != nil {
		barrier.wait()
	}
	return state, nil
}

func (r *goldenStateRepositoryFake) CommitGoldenState(
	_ context.Context,
	commit arena.GoldenStateCommit,
) (*arena.GoldenState, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commitErr != nil {
		return nil, false, r.commitErr
	}
	if r.commitHook != nil {
		return r.commitHook(commit)
	}
	if !commit.Expected.Equal(r.state.Expectation()) {
		return nil, false, domain.ErrConflict
	}
	r.commits++
	r.state = commit.Next.Snapshot()
	result := r.state.Snapshot()
	return &result, true, nil
}

type goldenLoadBarrier struct {
	limit   int32
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
}

func newGoldenLoadBarrier(limit int) *goldenLoadBarrier {
	return &goldenLoadBarrier{
		limit: int32(limit), arrived: make(chan struct{}, limit), release: make(chan struct{}),
	}
}

func (b *goldenLoadBarrier) wait() {
	if b.calls.Add(1) > b.limit {
		return
	}
	b.arrived <- struct{}{}
	<-b.release
}

func (r *goldenStateRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func goldenStateFixture(t *testing.T, openedAt time.Time) arena.GoldenState {
	t.Helper()

	authority, command := goldenExactPlanFixture(t)
	exactPlan, err := arena.BuildGoldenExactPlan(command, authority)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(exactPlan.Groups), 2)
	groupPlan := exactPlan.Groups[1]
	topology := authority.Groups[1].Revision

	members := topology.Members()
	participantIDs := make([]uuid.UUID, len(members))
	groupMembers := make([]domain.ArenaGoldenMember, len(members))
	for index, member := range members {
		participantIDs[index] = member.ParticipantID
		groupMembers[index] = domain.ArenaGoldenMember{ParticipantID: member.ParticipantID}
	}
	attemptID := task047ID(10)
	group := domain.ArenaGoldenGroupState{
		ID: topology.GroupID(), TournamentID: topology.TournamentID(),
		RevisionID: topology.RevisionID(), SourceProjectionRevisionID: topology.SourceProjectionRevisionID(),
		PositionFrom: groupPlan.PositionFrom, PositionTo: groupPlan.PositionTo, Members: groupMembers,
		Attempts: []domain.ArenaGoldenAttempt{{
			ID: attemptID, GroupID: topology.GroupID(), GroupRevisionID: topology.RevisionID(),
			AttemptNo: 1, State: domain.ArenaGoldenAttemptStateWaitingReady,
			ParticipantIDs: participantIDs,
		}},
	}
	state, err := arena.BuildGoldenState(arena.GoldenState{
		Scope: arena.GoldenStateScope{
			TournamentID: topology.TournamentID(), GroupID: topology.GroupID(),
			GroupRevisionID: topology.RevisionID(),
		},
		Topology: topology, ExactPlan: exactPlan, Group: group,
		Membership: arena.GoldenMembershipRevision{
			RevisionID: task047ID(22), Revision: 1,
		},
		RevisionID: task047ID(30), Revision: 1,
		Windows: []arena.GoldenReadyWindow{{
			ID: task047ID(40), RevisionID: task047ID(41), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second),
			State:               arena.GoldenReadyWindowOpen,
			ReadinessRevisionID: task047ID(42), ReadinessRevision: 1,
			PresenceRevisionID: task047ID(43), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	return state
}

func goldenReadyCommand(
	state arena.GoldenState,
	participantID uuid.UUID,
	base int,
) arena.GoldenReadyCommand {
	window := state.Windows[len(state.Windows)-1]
	return arena.GoldenReadyCommand{
		Scope: state.Scope, CommandID: task047ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: task047ID(base + 1), NextWindowRevisionID: task047ID(base + 2),
		NextReadinessRevisionID: task047ID(base + 3),
	}
}

func goldenFourMemberStateFixture(t *testing.T, openedAt time.Time) arena.GoldenState {
	t.Helper()

	now := openedAt.Add(-time.Hour)
	tournamentID := task046ID(6000)
	source := mustGoldenStandingsProjection(
		t,
		tournamentID,
		task046ID(6001),
		task046RevisionID(6002),
		1,
		goldenStandings([]int{10, 10, 9, 9, 9, 9, 7}),
	)
	partition, err := arena.PartitionGoldenTies(source)
	require.NoError(t, err)
	seeds := partition.GoldenGroups()
	require.Len(t, seeds, 2)
	require.Len(t, seeds[1].Members, 4)

	groups := make([]arena.GoldenPlanGroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := arena.GoldenGroupRevisionCommand{
			TournamentID: tournamentID, GroupID: task046ID(6010 + index),
			RevisionID: task046RevisionID(6020 + index), RevisionNo: 1,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seed.PositionFrom,
			PositionTo:                  seed.PositionTo,
		}
		revision, buildErr := arena.BuildGoldenGroupRevision(command, source, seed, nil, nil)
		require.NoError(t, buildErr)
		members := revision.Members()
		active := make([]uuid.UUID, len(members))
		for memberIndex, member := range members {
			active[memberIndex] = member.ParticipantID
		}
		groups[index] = arena.GoldenPlanGroupAuthority{
			Revision: revision, ActiveParticipantIDs: active,
		}
	}

	poolID := task046ID(6030)
	candidates := make([]arena.GoldenExactTaskVersion, 6)
	versions := make([]arena.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := goldenTask(6040 + index)
		versions[index] = arena.TaskVersionRef{TaskID: task.ID, Version: 2}
		candidates[index] = arena.GoldenExactTaskVersion{
			PoolRevisionID: poolID,
			Version:        2,
			Task:           task,
			Health: arena.TaskVersionHealth{
				TaskID: task.ID, Version: 2, PoolRevisionID: poolID,
				PoolKind: domain.ArenaTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: arena.GoldenTaskArtifactDigest(task, 2),
		}
	}

	reservations := make([]arena.GoldenParticipantReservation, 0, 6)
	for _, group := range groups {
		for _, participantID := range group.ActiveParticipantIDs {
			index := len(reservations)
			playerID := task046ID(6100 + index)
			reservations = append(reservations, arena.GoldenParticipantReservation{
				ParticipantID: participantID,
				PlayerID:      playerID,
				Reservation: domain.ParticipantReservation{
					PlayerID: playerID, ReservationID: task046ID(6120 + index),
					OwnerKind: domain.ParticipantReservationOwnerArena, OwnerID: tournamentID,
					Revision: 1, AcquiredAt: now, UpdatedAt: now,
				},
			})
		}
	}
	authority, err := arena.BuildGoldenExactPlanAuthority(arena.GoldenExactPlanAuthority{
		Scope: arena.GoldenExactPlanScope{TournamentID: tournamentID, PlanSetID: task046ID(6200)},
		Revisions: arena.GoldenExactPlanRevisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         task046ID(6201), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: task046ID(6203), HistoryRevision: 1,
			TaskHealthRevisionID: task046ID(6204), TaskHealthRevision: 1,
			ArtifactRevisionID: task046ID(6205), ArtifactRevision: 1,
			ReservationRevisionID: task046ID(6206), ReservationRevision: 1,
			MembershipRevisionID: task046ID(6207), MembershipRevision: 1,
		},
		Source: source, Groups: groups,
		Pool:       arena.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.ArenaTaskKindGolden, Versions: versions},
		Candidates: candidates, ParticipantReservations: reservations,
	})
	require.NoError(t, err)

	groupCommands := make([]arena.GoldenExactGroupCommand, len(groups))
	for groupIndex, group := range groups {
		groupCommands[groupIndex].GroupID = group.Revision.GroupID()
		groupCommands[groupIndex].GroupRevisionID = group.Revision.RevisionID()
		for edgeIndex := range groupCommands[groupIndex].EdgeIDs {
			base := 6300 + groupIndex*100 + edgeIndex*10
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = task046ID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = task046ID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = task046ID(base + 3)
		}
	}
	exactPlan, err := arena.BuildGoldenExactPlan(arena.GoldenExactPlanCommand{
		Scope: authority.Scope, PlanID: task046ID(6500), PlanRevisionID: task046ID(6501),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}, authority)
	require.NoError(t, err)

	groupPlan := exactPlan.Groups[1]
	topology := authority.Groups[1].Revision
	members := topology.Members()
	participantIDs := make([]uuid.UUID, len(members))
	groupMembers := make([]domain.ArenaGoldenMember, len(members))
	for index, member := range members {
		participantIDs[index] = member.ParticipantID
		groupMembers[index] = domain.ArenaGoldenMember{ParticipantID: member.ParticipantID}
	}
	attemptID := task047ID(6600)
	state, err := arena.BuildGoldenState(arena.GoldenState{
		Scope: arena.GoldenStateScope{
			TournamentID: tournamentID, GroupID: topology.GroupID(),
			GroupRevisionID: topology.RevisionID(),
		},
		Topology: topology, ExactPlan: exactPlan,
		Group: domain.ArenaGoldenGroupState{
			ID: topology.GroupID(), TournamentID: tournamentID,
			RevisionID: topology.RevisionID(), SourceProjectionRevisionID: topology.SourceProjectionRevisionID(),
			PositionFrom: groupPlan.PositionFrom, PositionTo: groupPlan.PositionTo,
			Members: groupMembers,
			Attempts: []domain.ArenaGoldenAttempt{{
				ID: attemptID, GroupID: topology.GroupID(), GroupRevisionID: topology.RevisionID(),
				AttemptNo: 1, State: domain.ArenaGoldenAttemptStateWaitingReady,
				ParticipantIDs: participantIDs,
			}},
		},
		Membership: arena.GoldenMembershipRevision{RevisionID: task047ID(6601), Revision: 1},
		RevisionID: task047ID(6602), Revision: 1,
		Windows: []arena.GoldenReadyWindow{{
			ID: task047ID(6603), RevisionID: task047ID(6604), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1, OpenedAt: openedAt,
			Deadline: openedAt.Add(30 * time.Second), State: arena.GoldenReadyWindowOpen,
			ReadinessRevisionID: task047ID(6605), ReadinessRevision: 1,
			PresenceRevisionID: task047ID(6606), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	return state
}

func task047ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("47000000-0000-0000-0000-%012d", value))
}

var _ arena.GoldenStateRepository = (*goldenStateRepositoryFake)(nil)
