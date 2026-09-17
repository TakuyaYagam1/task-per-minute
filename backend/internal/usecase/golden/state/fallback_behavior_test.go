package state_test

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenDirectFallback(t *testing.T) {
	t.Parallel()

	t.Run("allocates a sole active member without a solo attempt", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
		state := stateGoldenStateFixture(t, openedAt)
		repository := stateNewGoldenStateRepository(t, state)
		participantID := state.Group.Members[0].ParticipantID
		resolved := goldenReadyAndResolve(t, repository, state, []uuid.UUID{participantID}, openedAt, 300)
		attemptCount := len(resolved.Group.Attempts)
		command := goldenusecase.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: stateTask047ID(330), AllocationID: stateTask047ID(331),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: stateTask047ID(332),
		}
		before := repository.commitCount()
		premature, prematureChanged, prematureErr := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, resolved.NoShows[len(resolved.NoShows)-1].ResolvedAt.Add(-time.Nanosecond)),
		).Allocate(t.Context(), command)
		require.Nil(t, premature)
		require.False(t, prematureChanged)
		require.ErrorIs(t, prematureErr, goldenusecase.ErrGoldenFallbackAuthorityConflict)
		require.Equal(t, before, repository.commitCount())

		allocated, changed, err := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, resolved.NoShows[len(resolved.NoShows)-1].ResolvedAt),
		).Allocate(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, allocated.Allocation)
		require.Len(t, allocated.Allocation.Positions, len(resolved.Group.Members))
		require.Equal(t, resolved.Group.PositionFrom, allocated.Allocation.Positions[0].Position)
		require.Equal(t, participantID, allocated.Allocation.Positions[0].ParticipantID)
		require.Equal(t, goldenusecase.GoldenPositionDirect, allocated.Allocation.Positions[0].Kind)
		require.Len(t, allocated.Group.Attempts, attemptCount, "direct allocation must not create a solo attempt")
		excludedSeeds := make([]goldenplan.GroupMemberSeed, 0, len(resolved.Group.Members)-1)
		for _, member := range resolved.Topology.Members() {
			if member.ParticipantID != participantID {
				excludedSeeds = append(excludedSeeds, member)
			}
		}
		orderedExcluded, orderErr := goldenusecase.OrderGoldenFallbackMembers(excludedSeeds)
		require.NoError(t, orderErr)
		expectedParticipants := append([]uuid.UUID{participantID}, orderedExcluded...)
		for index, position := range allocated.Allocation.Positions {
			require.Equal(t, expectedParticipants[index], position.ParticipantID)
			if index > 0 {
				require.Equal(t, goldenusecase.GoldenPositionNoShowFallback, position.Kind)
			}
		}

		replayed, changed, err := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(2*time.Minute)),
		).Allocate(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, allocated.PayloadDigest, replayed.PayloadDigest)
		reused := command
		reused.AllocationID = stateTask047ID(339)
		conflict, reusedChanged, reusedErr := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(2*time.Minute+time.Nanosecond)),
		).Allocate(t.Context(), reused)
		require.Nil(t, conflict)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenCommandReuse)

		secondCommand := goldenusecase.GoldenFallbackCommand{
			Scope: allocated.Scope, CommandID: stateTask047ID(340), AllocationID: stateTask047ID(341),
			ExpectedState: allocated.Expectation(), NextStateRevisionID: stateTask047ID(342),
		}
		secondAllocation, secondChanged, secondErr := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(3*time.Minute)),
		).Allocate(t.Context(), secondCommand)
		require.Nil(t, secondAllocation)
		require.False(t, secondChanged)
		require.ErrorIs(t, secondErr, goldenusecase.ErrGoldenCommandReuse)
		replayed.Allocation.Positions[0].ParticipantID = stateTask047ID(338)
		reloaded, loadErr := repository.LoadGoldenState(t.Context(), resolved.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, participantID, reloaded.Allocation.Positions[0].ParticipantID)
	})

	t.Run("uses frozen normal ordering rather than UUID order", func(t *testing.T) {
		ordered, err := goldenusecase.OrderGoldenFallbackMembers([]goldenplan.GroupMemberSeed{
			{ParticipantID: stateTask047ID(401), Points: 7, Buchholz: 2, EffectiveTime: 3 * time.Minute, Seed: 3},
			{ParticipantID: stateTask047ID(402), Points: 7, Buchholz: 5, EffectiveTime: 9 * time.Minute, Seed: 2},
			{ParticipantID: stateTask047ID(403), Points: 7, Buchholz: 5, EffectiveTime: time.Minute, Seed: 1},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{stateTask047ID(403), stateTask047ID(402), stateTask047ID(401)}, ordered)

		headToHead, err := goldenusecase.OrderGoldenFallbackMembers([]goldenplan.GroupMemberSeed{
			{ParticipantID: stateTask047ID(404), Points: 7, Buchholz: 5, HeadToHeadApplied: true, HeadToHeadPoints: 1, EffectiveTime: time.Minute, Seed: 1},
			{ParticipantID: stateTask047ID(405), Points: 7, Buchholz: 5, HeadToHeadApplied: true, HeadToHeadPoints: 2, EffectiveTime: 2 * time.Minute, Seed: 2},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{stateTask047ID(405), stateTask047ID(404)}, headToHead)

		notApplicable, err := goldenusecase.OrderGoldenFallbackMembers([]goldenplan.GroupMemberSeed{
			{ParticipantID: stateTask047ID(406), Points: 7, Buchholz: 5, HeadToHeadApplied: true, HeadToHeadPoints: 9, EffectiveTime: 2 * time.Minute, Seed: 1},
			{ParticipantID: stateTask047ID(407), Points: 7, Buchholz: 5, HeadToHeadApplied: false, EffectiveTime: time.Minute, Seed: 2},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{stateTask047ID(407), stateTask047ID(406)}, notApplicable)

		seedTie, err := goldenusecase.OrderGoldenFallbackMembers([]goldenplan.GroupMemberSeed{
			{ParticipantID: stateTask047ID(408), Points: 7, Buchholz: 5, EffectiveTime: time.Minute, Seed: 4},
			{ParticipantID: stateTask047ID(409), Points: 7, Buchholz: 5, EffectiveTime: time.Minute, Seed: 3},
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{stateTask047ID(409), stateTask047ID(408)}, seedTie)
	})

	t.Run("records terminal zero positions when participation never existed", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		state := stateGoldenStateFixture(t, openedAt)
		repository := stateNewGoldenStateRepository(t, state)
		resolved := goldenReadyAndResolve(t, repository, state, nil, openedAt, 500)
		require.False(t, resolved.Group.ParticipationEstablished)

		allocated, changed, err := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(time.Minute)),
		).Allocate(t.Context(), goldenusecase.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: stateTask047ID(530), AllocationID: stateTask047ID(531),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: stateTask047ID(532),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, allocated.Allocation)
		require.Empty(t, allocated.Allocation.Positions)
	})

	t.Run("fills every position when established participation loses all active members", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
		state := stateGoldenStateFixture(t, openedAt)
		repository := stateNewGoldenStateRepository(t, state)
		participantID := state.Group.Members[0].ParticipantID
		ready := stateGoldenAcceptReady(t, repository, state, participantID, openedAt.Add(time.Second), 600)
		cleared, changed, err := goldenusecase.NewGoldenParticipationUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(2*time.Second)),
		).ClearOnDisconnect(t.Context(), goldenusecase.GoldenDisconnectCommand{
			Scope: ready.Scope, CommandID: stateTask047ID(610), ParticipantID: participantID,
			AttemptID: ready.Windows[0].AttemptID, WindowID: ready.Windows[0].ID,
			ExpectedState: ready.Expectation(), ExpectedWindow: ready.Windows[0].Expectation(),
			NextStateRevisionID: stateTask047ID(611), NextWindowRevisionID: stateTask047ID(612),
			NextReadinessRevisionID: stateTask047ID(613), NextPresenceRevisionID: stateTask047ID(614),
		})
		require.NoError(t, err)
		require.True(t, changed)
		resolved := stateGoldenResolveNoShow(t, repository, *cleared, openedAt, 620)
		require.True(t, resolved.Group.ParticipationEstablished)
		require.Empty(t, resolved.ActiveParticipantIDs())
		attemptCount := len(resolved.Group.Attempts)

		allocated, changed, err := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(time.Minute)),
		).Allocate(t.Context(), goldenusecase.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: stateTask047ID(630), AllocationID: stateTask047ID(631),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: stateTask047ID(632),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.Len(t, allocated.Allocation.Positions, len(resolved.Group.Members))
		ordered, orderErr := goldenusecase.OrderGoldenFallbackMembers(resolved.Topology.Members())
		require.NoError(t, orderErr)
		for index, position := range allocated.Allocation.Positions {
			require.Equal(t, resolved.Group.PositionFrom+index, position.Position)
			require.Equal(t, ordered[index], position.ParticipantID)
			require.Equal(t, goldenusecase.GoldenPositionNoShowFallback, position.Kind)
		}
		require.Len(t, allocated.Group.Attempts, attemptCount)
	})

	t.Run("does not allocate while two or more active members remain", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
		state := stateGoldenStateFixture(t, openedAt)
		repository := stateNewGoldenStateRepository(t, state)
		readyIDs := []uuid.UUID{state.Group.Members[0].ParticipantID, state.Group.Members[1].ParticipantID}
		resolved := goldenReadyAndResolve(t, repository, state, readyIDs, openedAt, 700)
		require.Len(t, resolved.ActiveParticipantIDs(), 2)
		before := repository.commitCount()

		allocated, changed, err := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(time.Minute)),
		).Allocate(t.Context(), goldenusecase.GoldenFallbackCommand{
			Scope: resolved.Scope, CommandID: stateTask047ID(730), AllocationID: stateTask047ID(731),
			ExpectedState: resolved.Expectation(), NextStateRevisionID: stateTask047ID(732),
		})
		require.Nil(t, allocated)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrGoldenFallbackNotRequired)
		require.Equal(t, before, repository.commitCount())
	})

	t.Run("rejects allocation without terminal expiry evidence", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
		initial := stateGoldenStateFixture(t, openedAt)
		initial.Windows = nil
		initial, err := goldenusecase.BuildGoldenState(initial)
		require.NoError(t, err)
		repository := stateNewGoldenStateRepository(t, initial)
		allocated, changed, allocateErr := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(time.Hour)),
		).Allocate(t.Context(), goldenusecase.GoldenFallbackCommand{
			Scope: initial.Scope, CommandID: stateTask047ID(1200), AllocationID: stateTask047ID(1201),
			ExpectedState: initial.Expectation(), NextStateRevisionID: stateTask047ID(1202),
		})
		require.Nil(t, allocated)
		require.False(t, changed)
		require.ErrorIs(t, allocateErr, goldenusecase.ErrGoldenFallbackAuthorityConflict)
		require.Zero(t, repository.commitCount())
	})

	t.Run("rejects an open nonterminal window without persisting empty allocation", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 1, 15, 30, 0, 0, time.UTC)
		initial := stateGoldenStateFixture(t, openedAt)
		repository := stateNewGoldenStateRepository(t, initial)
		allocated, changed, allocateErr := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, openedAt.Add(time.Hour)),
		).Allocate(t.Context(), goldenFallbackCommand(initial, 1210))
		require.Nil(t, allocated)
		require.False(t, changed)
		require.ErrorIs(t, allocateErr, goldenusecase.ErrGoldenFallbackAuthorityConflict)
		require.Zero(t, repository.commitCount())
	})

	t.Run("enforces allocation causality at the terminal resolution nanosecond", func(t *testing.T) {
		for index, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			name := map[time.Duration]string{-time.Nanosecond: "before", 0: "equal", time.Nanosecond: "after"}[offset]
			t.Run(name, func(t *testing.T) {
				openedAt := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Hour)
				initial := stateGoldenStateFixture(t, openedAt)
				setupRepository := stateNewGoldenStateRepository(t, initial)
				participantID := initial.Group.Members[0].ParticipantID
				terminal := goldenReadyAndResolve(
					t,
					setupRepository,
					initial,
					[]uuid.UUID{participantID},
					openedAt,
					1220+index*20,
				)
				repository := stateNewGoldenStateRepository(t, terminal)
				result, changed, allocateErr := goldenusecase.NewGoldenFallbackUseCase(
					repository,
					stateNewGoldenClock(t, terminal.NoShows[0].ResolvedAt.Add(offset)),
				).Allocate(t.Context(), goldenFallbackCommand(terminal, 1280+index*10))
				if offset < 0 {
					require.Nil(t, result)
					require.False(t, changed)
					require.ErrorIs(t, allocateErr, goldenusecase.ErrGoldenFallbackAuthorityConflict)
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
		initial := stateGoldenStateFixture(t, openedAt)
		setupRepository := stateNewGoldenStateRepository(t, initial)
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
			mutate func(*goldenusecase.GoldenFallbackCommand)
		}
		cases := []staleCase{
			{name: "state", mutate: func(value *goldenusecase.GoldenFallbackCommand) { value.ExpectedState.Revision++ }},
			{name: "membership", mutate: func(value *goldenusecase.GoldenFallbackCommand) {
				value.ExpectedState.Membership.Revision++
			}},
			{name: "plan", mutate: func(value *goldenusecase.GoldenFallbackCommand) {
				value.ExpectedState.Plan.PlanID = stateTask047ID(1360)
			}},
			{name: "group", mutate: func(value *goldenusecase.GoldenFallbackCommand) {
				value.ExpectedState.Scope.GroupID = stateTask047ID(1361)
			}},
			{name: "source", mutate: func(value *goldenusecase.GoldenFallbackCommand) {
				value.ExpectedState.SourceProjectionPayloadDigest[0] ^= 0xff
			}},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				repository := stateNewGoldenStateRepository(t, terminal)
				command := goldenFallbackCommand(terminal, 1370+index*10)
				testCase.mutate(&command)
				result, changed, allocateErr := goldenusecase.NewGoldenFallbackUseCase(
					repository,
					stateNewGoldenClock(t, terminal.NoShows[0].ResolvedAt),
				).Allocate(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, allocateErr, goldenusecase.ErrGoldenFallbackAuthorityConflict)
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
				repository := stateNewGoldenStateRepository(t, terminal)
				command := goldenFallbackCommand(terminal, 1430)
				command.AllocationID = alias
				result, changed, allocateErr := goldenusecase.NewGoldenFallbackUseCase(
					repository,
					stateNewGoldenClock(t, terminal.NoShows[0].ResolvedAt),
				).Allocate(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, allocateErr, goldenusecase.ErrInvalidGoldenState)
				require.Zero(t, repository.commitCount())
			})
		}
	})

	t.Run("reconciles synchronized duplicate and competing allocations", func(t *testing.T) {
		type allocateResult struct {
			state   *goldenusecase.GoldenState
			changed bool
			err     error
		}
		openedAt := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
		initial := stateGoldenStateFixture(t, openedAt)
		setupRepository := stateNewGoldenStateRepository(t, initial)
		terminal := goldenReadyAndResolve(
			t,
			setupRepository,
			initial,
			[]uuid.UUID{initial.Group.Members[0].ParticipantID},
			openedAt,
			1500,
		)
		run := func(t *testing.T, commands [2]goldenusecase.GoldenFallbackCommand) [2]allocateResult {
			t.Helper()
			barrier := stateNewGoldenLoadBarrier(2)
			repository := stateNewGoldenStateRepository(t, terminal, withGoldenLoadBarrier(barrier))
			results := [2]allocateResult{}
			var group sync.WaitGroup
			for index := range commands {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					results[index].state, results[index].changed, results[index].err =
						goldenusecase.NewGoldenFallbackUseCase(
							repository,
							stateNewGoldenClock(t, terminal.NoShows[0].ResolvedAt),
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
		identical := run(t, [2]goldenusecase.GoldenFallbackCommand{first, first})
		require.NoError(t, identical[0].err)
		require.NoError(t, identical[1].err)
		require.NotEqual(t, identical[0].changed, identical[1].changed)

		second := goldenFallbackCommand(terminal, 1560)
		competing := run(t, [2]goldenusecase.GoldenFallbackCommand{first, second})
		successes := 0
		reuses := 0
		for _, result := range competing {
			if result.err == nil && result.changed {
				successes++
			}
			if errors.Is(result.err, goldenusecase.ErrGoldenCommandReuse) {
				reuses++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, reuses)
	})

	t.Run("rejects overflow and malformed repository results", func(t *testing.T) {
		openedAt := time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC)
		initial := stateGoldenStateFixture(t, openedAt)
		setupRepository := stateNewGoldenStateRepository(t, initial)
		terminal := goldenReadyAndResolve(
			t,
			setupRepository,
			initial,
			[]uuid.UUID{initial.Group.Members[0].ParticipantID},
			openedAt,
			1600,
		)
		firstExpectedStateID := stateTask047ID(1640)
		terminal.ReadyEvents[0].ExpectedState.RevisionID = firstExpectedStateID
		terminal.ReadyEvents[0].ExpectedState.Revision = math.MaxInt64 - 2
		terminal.NoShows[0].ExpectedState.Revision = math.MaxInt64 - 1
		terminal.PreviousRevisionID = goldenTestUUID(terminal.NoShows[0].ExpectedState.RevisionID)
		terminal.Revision = math.MaxInt64
		terminal, err := goldenusecase.BuildGoldenState(terminal)
		require.NoError(t, err)
		repository := stateNewGoldenStateRepository(t, terminal)
		result, changed, overflowErr := goldenusecase.NewGoldenFallbackUseCase(
			repository,
			stateNewGoldenClock(t, terminal.NoShows[0].ResolvedAt),
		).Allocate(t.Context(), goldenFallbackCommand(terminal, 1650))
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, overflowErr, goldenusecase.ErrGoldenRevisionOverflow)
		require.Zero(t, repository.commitCount())

		validInitial := stateGoldenStateFixture(t, openedAt.Add(time.Hour))
		validSetupRepository := stateNewGoldenStateRepository(t, validInitial)
		validTerminal := goldenReadyAndResolve(
			t,
			validSetupRepository,
			validInitial,
			[]uuid.UUID{validInitial.Group.Members[0].ParticipantID},
			openedAt.Add(time.Hour),
			1670,
		)
		loadCause := errors.New("fallback load failed")
		_, _, loadErr := goldenusecase.NewGoldenFallbackUseCase(
			stateNewGoldenStateRepository(t, validTerminal, withGoldenLoadError(loadCause)),
			stateNewGoldenClock(t, validTerminal.NoShows[0].ResolvedAt),
		).Allocate(t.Context(), goldenFallbackCommand(validTerminal, 1710))
		require.ErrorIs(t, loadErr, loadCause)

		nilCommit := stateNewGoldenStateRepository(
			t,
			validTerminal,
			withGoldenCommitHook(func(goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
				return nil, true, nil
			}),
		)
		_, _, commitErr := goldenusecase.NewGoldenFallbackUseCase(
			nilCommit,
			stateNewGoldenClock(t, validTerminal.NoShows[0].ResolvedAt),
		).Allocate(t.Context(), goldenFallbackCommand(validTerminal, 1730))
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})
}
