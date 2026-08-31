package arena_test

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenDirectFallback(t *testing.T) {
	t.Parallel()

	t.Run("allocates a sole active member without a solo attempt", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
		state := goldenStateFixture(t, openedAt)
		repository := &goldenStateRepositoryFake{state: state}
		participantID := state.Group.Members[0].ParticipantID
		resolved := goldenReadyAndResolve(t, repository, state, []uuid.UUID{participantID}, openedAt, 300)
		attemptCount := len(resolved.Group.Attempts)
		command := arena.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: task047ID(330), AllocationID: task047ID(331),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: task047ID(332),
		}
		before := repository.commitCount()
		premature, prematureChanged, prematureErr := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: resolved.NoShows[len(resolved.NoShows)-1].ResolvedAt.Add(-time.Nanosecond)},
		).Allocate(t.Context(), command)
		require.Nil(t, premature)
		require.False(t, prematureChanged)
		require.ErrorIs(t, prematureErr, arena.ErrGoldenFallbackAuthorityConflict)
		require.Equal(t, before, repository.commitCount())

		allocated, changed, err := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: resolved.NoShows[len(resolved.NoShows)-1].ResolvedAt},
		).Allocate(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, allocated.Allocation)
		require.Len(t, allocated.Allocation.Positions, len(resolved.Group.Members))
		require.Equal(t, resolved.Group.PositionFrom, allocated.Allocation.Positions[0].Position)
		require.Equal(t, participantID, allocated.Allocation.Positions[0].ParticipantID)
		require.Equal(t, arena.GoldenPositionDirect, allocated.Allocation.Positions[0].Kind)
		require.Len(t, allocated.Group.Attempts, attemptCount, "direct allocation must not create a solo attempt")
		excludedSeeds := make([]arena.GoldenGroupMemberSeed, 0, len(resolved.Group.Members)-1)
		for _, member := range resolved.Topology.Members() {
			if member.ParticipantID != participantID {
				excludedSeeds = append(excludedSeeds, member)
			}
		}
		orderedExcluded, orderErr := arena.OrderGoldenFallbackMembers(excludedSeeds)
		require.NoError(t, orderErr)
		expectedParticipants := append([]uuid.UUID{participantID}, orderedExcluded...)
		for index, position := range allocated.Allocation.Positions {
			require.Equal(t, expectedParticipants[index], position.ParticipantID)
			if index > 0 {
				require.Equal(t, arena.GoldenPositionNoShowFallback, position.Kind)
			}
		}

		replayed, changed, err := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(2 * time.Minute)},
		).Allocate(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, allocated.PayloadDigest, replayed.PayloadDigest)
		reused := command
		reused.AllocationID = task047ID(339)
		conflict, reusedChanged, reusedErr := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(2*time.Minute + time.Nanosecond)},
		).Allocate(t.Context(), reused)
		require.Nil(t, conflict)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, arena.ErrGoldenCommandReuse)

		secondCommand := arena.GoldenFallbackCommand{
			Scope: allocated.Scope, CommandID: task047ID(340), AllocationID: task047ID(341),
			ExpectedState: allocated.Expectation(), NextStateRevisionID: task047ID(342),
		}
		secondAllocation, secondChanged, secondErr := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(3 * time.Minute)},
		).Allocate(t.Context(), secondCommand)
		require.Nil(t, secondAllocation)
		require.False(t, secondChanged)
		require.ErrorIs(t, secondErr, arena.ErrGoldenCommandReuse)
		replayed.Allocation.Positions[0].ParticipantID = task047ID(338)
		reloaded, loadErr := repository.LoadGoldenState(t.Context(), resolved.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, participantID, reloaded.Allocation.Positions[0].ParticipantID)
	})

	t.Run("uses frozen normal ordering rather than UUID order", func(t *testing.T) {
		ordered, err := arena.OrderGoldenFallbackMembers([]arena.GoldenGroupMemberSeed{
			{ParticipantID: task047ID(401), Points: 7, Buchholz: 2, EffectiveTime: 3 * time.Minute, Seed: 3},
			{ParticipantID: task047ID(402), Points: 7, Buchholz: 5, EffectiveTime: 9 * time.Minute, Seed: 2},
			{ParticipantID: task047ID(403), Points: 7, Buchholz: 5, EffectiveTime: time.Minute, Seed: 1},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{task047ID(403), task047ID(402), task047ID(401)}, ordered)

		headToHead, err := arena.OrderGoldenFallbackMembers([]arena.GoldenGroupMemberSeed{
			{ParticipantID: task047ID(404), Points: 7, Buchholz: 5, HeadToHeadApplied: true, HeadToHeadPoints: 1, EffectiveTime: time.Minute, Seed: 1},
			{ParticipantID: task047ID(405), Points: 7, Buchholz: 5, HeadToHeadApplied: true, HeadToHeadPoints: 2, EffectiveTime: 2 * time.Minute, Seed: 2},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{task047ID(405), task047ID(404)}, headToHead)

		notApplicable, err := arena.OrderGoldenFallbackMembers([]arena.GoldenGroupMemberSeed{
			{ParticipantID: task047ID(406), Points: 7, Buchholz: 5, HeadToHeadApplied: true, HeadToHeadPoints: 9, EffectiveTime: 2 * time.Minute, Seed: 1},
			{ParticipantID: task047ID(407), Points: 7, Buchholz: 5, HeadToHeadApplied: false, EffectiveTime: time.Minute, Seed: 2},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{task047ID(407), task047ID(406)}, notApplicable)

		seedTie, err := arena.OrderGoldenFallbackMembers([]arena.GoldenGroupMemberSeed{
			{ParticipantID: task047ID(408), Points: 7, Buchholz: 5, EffectiveTime: time.Minute, Seed: 4},
			{ParticipantID: task047ID(409), Points: 7, Buchholz: 5, EffectiveTime: time.Minute, Seed: 3},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{task047ID(409), task047ID(408)}, seedTie)
	})

	t.Run("records terminal zero positions when participation never existed", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		state := goldenStateFixture(t, openedAt)
		repository := &goldenStateRepositoryFake{state: state}
		resolved := goldenReadyAndResolve(t, repository, state, nil, openedAt, 500)
		require.False(t, resolved.Group.ParticipationEstablished)

		allocated, changed, err := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Minute)},
		).Allocate(t.Context(), arena.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: task047ID(530), AllocationID: task047ID(531),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: task047ID(532),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, allocated.Allocation)
		require.Empty(t, allocated.Allocation.Positions)
	})

	t.Run("fills every position when established participation loses all active members", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
		state := goldenStateFixture(t, openedAt)
		repository := &goldenStateRepositoryFake{state: state}
		participantID := state.Group.Members[0].ParticipantID
		ready := goldenAcceptReady(t, repository, state, participantID, openedAt.Add(time.Second), 600)
		cleared, changed, err := arena.NewGoldenParticipationUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(2 * time.Second)},
		).ClearOnDisconnect(t.Context(), arena.GoldenDisconnectCommand{
			Scope: ready.Scope, CommandID: task047ID(610), ParticipantID: participantID,
			AttemptID: ready.Windows[0].AttemptID, WindowID: ready.Windows[0].ID,
			ExpectedState: ready.Expectation(), ExpectedWindow: ready.Windows[0].Expectation(),
			NextStateRevisionID: task047ID(611), NextWindowRevisionID: task047ID(612),
			NextReadinessRevisionID: task047ID(613), NextPresenceRevisionID: task047ID(614),
		})
		require.NoError(t, err)
		require.True(t, changed)
		resolved := goldenResolveNoShow(t, repository, *cleared, openedAt, 620)
		require.True(t, resolved.Group.ParticipationEstablished)
		require.Empty(t, resolved.ActiveParticipantIDs())
		attemptCount := len(resolved.Group.Attempts)

		allocated, changed, err := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Minute)},
		).Allocate(t.Context(), arena.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: task047ID(630), AllocationID: task047ID(631),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: task047ID(632),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.Len(t, allocated.Allocation.Positions, len(resolved.Group.Members))
		ordered, orderErr := arena.OrderGoldenFallbackMembers(resolved.Topology.Members())
		require.NoError(t, orderErr)
		for index, position := range allocated.Allocation.Positions {
			require.Equal(t, resolved.Group.PositionFrom+index, position.Position)
			require.Equal(t, ordered[index], position.ParticipantID)
			require.Equal(t, arena.GoldenPositionNoShowFallback, position.Kind)
		}
		require.Len(t, allocated.Group.Attempts, attemptCount)
	})

	t.Run("does not allocate while two or more active members remain", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
		state := goldenStateFixture(t, openedAt)
		repository := &goldenStateRepositoryFake{state: state}
		readyIDs := []uuid.UUID{state.Group.Members[0].ParticipantID, state.Group.Members[1].ParticipantID}
		resolved := goldenReadyAndResolve(t, repository, state, readyIDs, openedAt, 700)
		require.Len(t, resolved.ActiveParticipantIDs(), 2)
		before := repository.commitCount()

		allocated, changed, err := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Minute)},
		).Allocate(t.Context(), arena.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: task047ID(730), AllocationID: task047ID(731),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: task047ID(732),
		})
		require.Nil(t, allocated)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrGoldenFallbackNotRequired)
		require.Equal(t, before, repository.commitCount())
	})

	t.Run("rejects allocation without terminal expiry evidence", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
		initial := goldenStateFixture(t, openedAt)
		initial.Windows = nil
		initial, err := arena.BuildGoldenState(initial)
		require.NoError(t, err)
		repository := &goldenStateRepositoryFake{state: initial}
		allocated, changed, allocateErr := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Hour)},
		).Allocate(t.Context(), arena.GoldenFallbackCommand{
			Scope: initial.Scope, CommandID: task047ID(1200), AllocationID: task047ID(1201),
			ExpectedState: initial.Expectation(), NextStateRevisionID: task047ID(1202),
		})
		require.Nil(t, allocated)
		require.False(t, changed)
		require.ErrorIs(t, allocateErr, arena.ErrGoldenFallbackAuthorityConflict)
		require.Zero(t, repository.commitCount())
	})

	t.Run("rejects an open nonterminal window without persisting empty allocation", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 15, 30, 0, 0, time.UTC)
		initial := goldenStateFixture(t, openedAt)
		repository := &goldenStateRepositoryFake{state: initial}
		allocated, changed, allocateErr := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Hour)},
		).Allocate(t.Context(), goldenFallbackCommand(initial, 1210))
		require.Nil(t, allocated)
		require.False(t, changed)
		require.ErrorIs(t, allocateErr, arena.ErrGoldenFallbackAuthorityConflict)
		require.Zero(t, repository.commitCount())
	})

	t.Run("enforces allocation causality at the terminal resolution nanosecond", func(t *testing.T) {
		for index, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			name := map[time.Duration]string{-time.Nanosecond: "before", 0: "equal", time.Nanosecond: "after"}[offset]
			t.Run(name, func(t *testing.T) {
				openedAt := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Hour)
				initial := goldenStateFixture(t, openedAt)
				setupRepository := &goldenStateRepositoryFake{state: initial}
				participantID := initial.Group.Members[0].ParticipantID
				terminal := goldenReadyAndResolve(
					t,
					setupRepository,
					initial,
					[]uuid.UUID{participantID},
					openedAt,
					1220+index*20,
				)
				repository := &goldenStateRepositoryFake{state: terminal}
				result, changed, allocateErr := arena.NewGoldenFallbackUseCase(
					repository,
					fixedArenaClock{now: terminal.NoShows[0].ResolvedAt.Add(offset)},
				).Allocate(t.Context(), goldenFallbackCommand(terminal, 1280+index*10))
				if offset < 0 {
					require.Nil(t, result)
					require.False(t, changed)
					require.ErrorIs(t, allocateErr, arena.ErrGoldenFallbackAuthorityConflict)
					require.Zero(t, repository.commitCount())
					return
				}
				require.NoError(t, allocateErr)
				require.True(t, changed)
			})
		}
	})

	t.Run("rejects stale authority and historical identity aliases without writing", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
		initial := goldenStateFixture(t, openedAt)
		setupRepository := &goldenStateRepositoryFake{state: initial}
		terminal := goldenReadyAndResolve(
			t,
			setupRepository,
			initial,
			[]uuid.UUID{initial.Group.Members[0].ParticipantID},
			openedAt,
			1320,
		)
		type staleCase struct {
			name   string
			mutate func(*arena.GoldenFallbackCommand)
		}
		cases := []staleCase{
			{name: "state", mutate: func(value *arena.GoldenFallbackCommand) { value.ExpectedState.Revision++ }},
			{name: "membership", mutate: func(value *arena.GoldenFallbackCommand) {
				value.ExpectedState.Membership.Revision++
			}},
			{name: "plan", mutate: func(value *arena.GoldenFallbackCommand) {
				value.ExpectedState.Plan.PlanID = task047ID(1360)
			}},
			{name: "group", mutate: func(value *arena.GoldenFallbackCommand) {
				value.ExpectedState.Scope.GroupID = task047ID(1361)
			}},
			{name: "source", mutate: func(value *arena.GoldenFallbackCommand) {
				value.ExpectedState.SourceProjectionPayloadDigest[0] ^= 0xff
			}},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				repository := &goldenStateRepositoryFake{state: terminal}
				command := goldenFallbackCommand(terminal, 1370+index*10)
				testCase.mutate(&command)
				result, changed, allocateErr := arena.NewGoldenFallbackUseCase(
					repository,
					fixedArenaClock{now: terminal.NoShows[0].ResolvedAt},
				).Allocate(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, allocateErr, arena.ErrGoldenFallbackAuthorityConflict)
				require.Zero(t, repository.commitCount())
			})
		}

		aliases := map[string]uuid.UUID{
			"historical state": terminal.ReadyEvents[0].ExpectedState.RevisionID,
			"nested plan edge": terminal.ExactPlan.Groups[0].Edges[0].ID,
			"nested snapshot":  terminal.ExactPlan.Groups[0].Edges[0].Snapshot.SnapshotID,
		}
		for name, alias := range aliases {
			t.Run(name, func(t *testing.T) {
				repository := &goldenStateRepositoryFake{state: terminal}
				command := goldenFallbackCommand(terminal, 1430)
				command.AllocationID = alias
				result, changed, allocateErr := arena.NewGoldenFallbackUseCase(
					repository,
					fixedArenaClock{now: terminal.NoShows[0].ResolvedAt},
				).Allocate(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, allocateErr, arena.ErrInvalidGoldenState)
				require.Zero(t, repository.commitCount())
			})
		}
	})

	t.Run("reconciles synchronized duplicate and competing allocations", func(t *testing.T) {
		type allocateResult struct {
			state   *arena.GoldenState
			changed bool
			err     error
		}
		openedAt := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
		initial := goldenStateFixture(t, openedAt)
		setupRepository := &goldenStateRepositoryFake{state: initial}
		terminal := goldenReadyAndResolve(
			t,
			setupRepository,
			initial,
			[]uuid.UUID{initial.Group.Members[0].ParticipantID},
			openedAt,
			1500,
		)
		run := func(t *testing.T, commands [2]arena.GoldenFallbackCommand) [2]allocateResult {
			t.Helper()
			barrier := newGoldenLoadBarrier(2)
			repository := &goldenStateRepositoryFake{state: terminal, barrier: barrier}
			results := [2]allocateResult{}
			var group sync.WaitGroup
			for index := range commands {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					results[index].state, results[index].changed, results[index].err =
						arena.NewGoldenFallbackUseCase(
							repository,
							fixedArenaClock{now: terminal.NoShows[0].ResolvedAt},
						).Allocate(t.Context(), commands[index])
				}(index)
			}
			for range 2 {
				<-barrier.arrived
			}
			close(barrier.release)
			group.Wait()
			require.Equal(t, 1, repository.commitCount())
			return results
		}

		first := goldenFallbackCommand(terminal, 1540)
		identical := run(t, [2]arena.GoldenFallbackCommand{first, first})
		require.NoError(t, identical[0].err)
		require.NoError(t, identical[1].err)
		require.NotEqual(t, identical[0].changed, identical[1].changed)

		second := goldenFallbackCommand(terminal, 1560)
		competing := run(t, [2]arena.GoldenFallbackCommand{first, second})
		successes := 0
		reuses := 0
		for _, result := range competing {
			if result.err == nil && result.changed {
				successes++
			}
			if errors.Is(result.err, arena.ErrGoldenCommandReuse) {
				reuses++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, reuses)
	})

	t.Run("rejects overflow and malformed repository results", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
		initial := goldenStateFixture(t, openedAt)
		setupRepository := &goldenStateRepositoryFake{state: initial}
		terminal := goldenReadyAndResolve(
			t,
			setupRepository,
			initial,
			[]uuid.UUID{initial.Group.Members[0].ParticipantID},
			openedAt,
			1600,
		)
		firstExpectedStateID := task047ID(1640)
		terminal.ReadyEvents[0].ExpectedState.RevisionID = firstExpectedStateID
		terminal.ReadyEvents[0].ExpectedState.Revision = math.MaxInt64 - 2
		terminal.NoShows[0].ExpectedState.Revision = math.MaxInt64 - 1
		terminal.PreviousRevisionID = goldenTestUUID(terminal.NoShows[0].ExpectedState.RevisionID)
		terminal.Revision = math.MaxInt64
		terminal, err := arena.BuildGoldenState(terminal)
		require.NoError(t, err)
		repository := &goldenStateRepositoryFake{state: terminal}
		result, changed, overflowErr := arena.NewGoldenFallbackUseCase(
			repository,
			fixedArenaClock{now: terminal.NoShows[0].ResolvedAt},
		).Allocate(t.Context(), goldenFallbackCommand(terminal, 1650))
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, overflowErr, arena.ErrGoldenRevisionOverflow)
		require.Zero(t, repository.commitCount())

		validInitial := goldenStateFixture(t, openedAt.Add(time.Hour))
		validSetupRepository := &goldenStateRepositoryFake{state: validInitial}
		validTerminal := goldenReadyAndResolve(
			t,
			validSetupRepository,
			validInitial,
			[]uuid.UUID{validInitial.Group.Members[0].ParticipantID},
			openedAt.Add(time.Hour),
			1670,
		)
		loadCause := errors.New("fallback load failed")
		_, _, loadErr := arena.NewGoldenFallbackUseCase(
			&goldenStateRepositoryFake{state: validTerminal, loadErr: loadCause},
			fixedArenaClock{now: validTerminal.NoShows[0].ResolvedAt},
		).Allocate(t.Context(), goldenFallbackCommand(validTerminal, 1710))
		require.ErrorIs(t, loadErr, loadCause)

		nilCommit := &goldenStateRepositoryFake{
			state: validTerminal,
			commitHook: func(arena.GoldenStateCommit) (*arena.GoldenState, bool, error) {
				return nil, true, nil
			},
		}
		_, _, commitErr := arena.NewGoldenFallbackUseCase(
			nilCommit,
			fixedArenaClock{now: validTerminal.NoShows[0].ResolvedAt},
		).Allocate(t.Context(), goldenFallbackCommand(validTerminal, 1730))
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})
}

func goldenFallbackCommand(state arena.GoldenState, base int) arena.GoldenFallbackCommand {
	return arena.GoldenFallbackCommand{
		Scope: state.Scope, CommandID: task047ID(base), AllocationID: task047ID(base + 1),
		ExpectedState: state.Expectation(), NextStateRevisionID: task047ID(base + 2),
	}
}

func goldenReadyAndResolve(
	t *testing.T,
	repository *goldenStateRepositoryFake,
	state arena.GoldenState,
	readyIDs []uuid.UUID,
	openedAt time.Time,
	base int,
) arena.GoldenState {
	t.Helper()
	current := state
	for index, participantID := range readyIDs {
		current = goldenAcceptReady(t, repository, current, participantID, openedAt.Add(time.Duration(index+1)*time.Second), base+index*10)
	}
	return goldenResolveNoShow(t, repository, current, openedAt, base+len(readyIDs)*10)
}

func goldenAcceptReady(
	t *testing.T,
	repository *goldenStateRepositoryFake,
	state arena.GoldenState,
	participantID uuid.UUID,
	now time.Time,
	base int,
) arena.GoldenState {
	t.Helper()
	window := state.Windows[0]
	ready, changed, err := arena.NewGoldenParticipationUseCase(
		repository,
		fixedArenaClock{now: now},
	).AcceptReady(t.Context(), arena.GoldenReadyCommand{
		Scope: state.Scope, CommandID: task047ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: task047ID(base + 1), NextWindowRevisionID: task047ID(base + 2),
		NextReadinessRevisionID: task047ID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	return *ready
}

func goldenResolveNoShow(
	t *testing.T,
	repository *goldenStateRepositoryFake,
	state arena.GoldenState,
	openedAt time.Time,
	base int,
) arena.GoldenState {
	t.Helper()
	window := state.Windows[0]
	resolved, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: window.Deadline.Add(time.Nanosecond)},
	).Resolve(t.Context(), arena.GoldenNoShowCommand{
		Scope: state.Scope, CommandID: task047ID(base), AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: task047ID(base + 1), NextWindowRevisionID: task047ID(base + 2),
		NextMembershipRevisionID: task047ID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, resolved.NoShows[0].ResolvedAt.After(openedAt))
	return *resolved
}
