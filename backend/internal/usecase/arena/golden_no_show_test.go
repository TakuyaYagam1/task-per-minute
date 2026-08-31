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

func TestGoldenPermanentExclusion(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	state := goldenStateFixture(t, openedAt)
	repository := &goldenStateRepositoryFake{state: state}
	participantID := state.Group.Members[0].ParticipantID
	readyCommand := arena.GoldenReadyCommand{
		Scope: state.Scope, CommandID: task047ID(200),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: state.Windows[0].AttemptID, WindowID: state.Windows[0].ID,
		ExpectedState: state.Expectation(), ExpectedWindow: state.Windows[0].Expectation(),
		NextStateRevisionID: task047ID(201), NextWindowRevisionID: task047ID(202),
		NextReadinessRevisionID: task047ID(203),
	}
	ready, changed, err := arena.NewGoldenParticipationUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(time.Second)},
	).AcceptReady(t.Context(), readyCommand)
	require.NoError(t, err)
	require.True(t, changed)

	window := ready.Windows[0]
	command := arena.GoldenNoShowCommand{
		Scope: ready.Scope, CommandID: task047ID(210),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: ready.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: task047ID(211), NextWindowRevisionID: task047ID(212),
		NextMembershipRevisionID: task047ID(213),
	}
	tooEarly, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: window.Deadline.Add(-time.Nanosecond)},
	).Resolve(t.Context(), command)
	require.Nil(t, tooEarly)
	require.False(t, changed)
	require.ErrorIs(t, err, arena.ErrGoldenNoShowCutoff)
	require.Equal(t, 1, repository.commitCount())
	cutoff, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: window.Deadline},
	).Resolve(t.Context(), command)
	require.Nil(t, cutoff)
	require.False(t, changed)
	require.ErrorIs(t, err, arena.ErrGoldenNoShowCutoff)
	require.Equal(t, 1, repository.commitCount())

	resolved, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: window.Deadline.Add(time.Nanosecond)},
	).Resolve(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, arena.GoldenReadyWindowExpired, resolved.Windows[0].State)
	require.Len(t, resolved.NoShows, 1)
	require.Equal(t, window.Deadline.Add(time.Nanosecond), resolved.NoShows[0].ResolvedAt)
	require.Equal(t, window.ReadinessDigest, resolved.NoShows[0].ReadinessDigest)
	require.Equal(t, window.PresenceDigest, resolved.NoShows[0].PresenceDigest)
	require.Len(t, resolved.NoShows[0].ExcludedParticipantIDs, 2)
	require.Equal(t, domain.ArenaGoldenAttemptStateCancelled, resolved.Group.Attempts[0].State)
	require.Equal(t, 2, repository.commitCount(), "the whole absent set must use one CAS")
	for _, member := range resolved.Group.Members {
		require.Equal(t, member.ParticipantID != participantID, member.Excluded)
	}

	replayed, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: window.Deadline.Add(time.Minute)},
	).Resolve(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, resolved.PayloadDigest, replayed.PayloadDigest)
	require.Equal(t, 2, repository.commitCount())

	reused := command
	reused.AttemptID = task047ID(999)
	conflict, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: window.Deadline.Add(time.Minute)},
	).Resolve(t.Context(), reused)
	require.Nil(t, conflict)
	require.False(t, changed)
	require.ErrorIs(t, err, arena.ErrGoldenCommandReuse)

	malformed := resolved.Snapshot()
	excludedID := resolved.NoShows[0].ExcludedParticipantIDs[0]
	malformed.Group.Attempts = append(malformed.Group.Attempts, domain.ArenaGoldenAttempt{
		ID: task047ID(220), GroupID: malformed.Scope.GroupID,
		GroupRevisionID: malformed.Scope.GroupRevisionID, AttemptNo: 2,
		PreviousAttemptID: &malformed.Group.Attempts[0].ID,
		State:             domain.ArenaGoldenAttemptStateWaitingReady,
		ParticipantIDs:    []uuid.UUID{participantID, excludedID},
	})
	malformed.RevisionID = task047ID(221)
	malformed.Revision++
	malformed.PreviousRevisionID = &resolved.RevisionID
	_, err = arena.BuildGoldenState(malformed)
	require.ErrorIs(t, err, arena.ErrInvalidGoldenState, "an excluded member must not enter a later attempt")
	require.ErrorIs(t, err, arena.ErrInvalidGoldenNoShow)

	t.Run("retains an empty terminal resolution when every member readied", func(t *testing.T) {
		local := goldenStateFixture(t, openedAt.Add(time.Hour))
		localRepository := &goldenStateRepositoryFake{state: local}
		readyIDs := make([]uuid.UUID, len(local.Group.Members))
		for index, member := range local.Group.Members {
			readyIDs[index] = member.ParticipantID
		}
		terminal := goldenReadyAndResolve(
			t,
			localRepository,
			local,
			readyIDs,
			openedAt.Add(time.Hour),
			1000,
		)
		require.Len(t, terminal.NoShows, 1)
		require.Empty(t, terminal.NoShows[0].ExcludedParticipantIDs)
		require.Equal(t, domain.ArenaGoldenAttemptStateWaitingReady, terminal.Group.Attempts[0].State)
		require.Equal(t, 4, localRepository.commitCount())
	})

	t.Run("requires exact no-show successor linkage", func(t *testing.T) {
		mutations := map[string]func(*arena.GoldenState){
			"state": func(value *arena.GoldenState) {
				value.NoShows[0].ResultStateRevisionID = task047ID(1100)
			},
			"membership": func(value *arena.GoldenState) {
				value.NoShows[0].ResultMembershipRevisionID = task047ID(1101)
			},
			"window evidence": func(value *arena.GoldenState) {
				value.NoShows[0].ExpectedWindow.ReadinessRevisionID = task047ID(1102)
			},
			"state predecessor": func(value *arena.GoldenState) {
				value.PreviousRevisionID = goldenTestUUID(task047ID(1103))
			},
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				forged := resolved.Snapshot()
				mutate(&forged)
				_, buildErr := arena.BuildGoldenState(forged)
				require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
			})
		}
	})

	t.Run("requires every later attempt to retain all active members", func(t *testing.T) {
		localOpenedAt := openedAt.Add(2 * time.Hour)
		local := goldenFourMemberStateFixture(t, localOpenedAt)
		localRepository := &goldenStateRepositoryFake{state: local}
		readyIDs := []uuid.UUID{
			local.Group.Members[0].ParticipantID,
			local.Group.Members[1].ParticipantID,
			local.Group.Members[2].ParticipantID,
		}
		terminal := goldenReadyAndResolve(
			t,
			localRepository,
			local,
			readyIDs,
			localOpenedAt,
			1300,
		)
		active := terminal.ActiveParticipantIDs()
		require.Len(t, active, 3)

		forged := terminal.Snapshot()
		previousAttemptID := forged.Group.Attempts[0].ID
		forged.Group.Attempts = append(forged.Group.Attempts, domain.ArenaGoldenAttempt{
			ID: task047ID(1340), GroupID: forged.Scope.GroupID,
			GroupRevisionID: forged.Scope.GroupRevisionID, AttemptNo: 2,
			PreviousAttemptID: &previousAttemptID,
			State:             domain.ArenaGoldenAttemptStateWaitingReady,
			ParticipantIDs:    append([]uuid.UUID(nil), active[:2]...),
		})
		secondOpenedAt := terminal.NoShows[0].ResolvedAt.Add(time.Second)
		forged.Windows = append(forged.Windows, arena.GoldenReadyWindow{
			ID: task047ID(1341), RevisionID: task047ID(1342), Revision: 1,
			AttemptID: task047ID(1340), AttemptNo: 2,
			OpenedAt: secondOpenedAt, Deadline: secondOpenedAt.Add(30 * time.Second),
			State:               arena.GoldenReadyWindowOpen,
			ReadinessRevisionID: task047ID(1343), ReadinessRevision: 1,
			PresenceRevisionID: task047ID(1344), PresenceRevision: 1,
			PresentParticipantIDs: append([]uuid.UUID(nil), active[:2]...),
		})
		_, buildErr := arena.BuildGoldenState(forged)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenState)
	})

	t.Run("rejects every stale authority component without writing", func(t *testing.T) {
		type staleCase struct {
			name   string
			mutate func(*arena.GoldenNoShowCommand)
		}
		cases := []staleCase{
			{name: "attempt", mutate: func(value *arena.GoldenNoShowCommand) { value.AttemptID = task047ID(1400) }},
			{name: "window", mutate: func(value *arena.GoldenNoShowCommand) { value.WindowID = task047ID(1401) }},
			{name: "plan", mutate: func(value *arena.GoldenNoShowCommand) { value.ExpectedState.Plan.PlanID = task047ID(1402) }},
			{name: "group", mutate: func(value *arena.GoldenNoShowCommand) { value.ExpectedState.Scope.GroupID = task047ID(1403) }},
			{name: "source", mutate: func(value *arena.GoldenNoShowCommand) {
				value.ExpectedState.SourceProjectionPayloadDigest[0] ^= 0xff
			}},
			{name: "state", mutate: func(value *arena.GoldenNoShowCommand) { value.ExpectedState.Revision++ }},
			{name: "membership", mutate: func(value *arena.GoldenNoShowCommand) {
				value.ExpectedState.Membership.Revision++
			}},
			{name: "window revision", mutate: func(value *arena.GoldenNoShowCommand) { value.ExpectedWindow.Revision++ }},
			{name: "readiness", mutate: func(value *arena.GoldenNoShowCommand) {
				value.ExpectedWindow.ReadinessRevision++
			}},
			{name: "presence", mutate: func(value *arena.GoldenNoShowCommand) {
				value.ExpectedWindow.PresenceRevision++
			}},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				local := goldenStateFixture(t, openedAt.Add(3*time.Hour))
				localRepository := &goldenStateRepositoryFake{state: local}
				command := goldenNoShowCommand(local, 1500+index*10)
				testCase.mutate(&command)
				result, changed, resolveErr := arena.NewGoldenNoShowUseCase(
					localRepository,
					fixedArenaClock{now: local.Windows[0].Deadline.Add(time.Nanosecond)},
				).Resolve(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, resolveErr, arena.ErrGoldenNoShowAuthorityConflict)
				require.Zero(t, localRepository.commitCount())
			})
		}
	})

	t.Run("reconciles synchronized duplicate and competing expiry commands", func(t *testing.T) {
		type resolveResult struct {
			state   *arena.GoldenState
			changed bool
			err     error
		}
		run := func(t *testing.T, commands [2]arena.GoldenNoShowCommand) [2]resolveResult {
			t.Helper()
			local := goldenStateFixture(t, openedAt.Add(4*time.Hour))
			barrier := newGoldenLoadBarrier(2)
			localRepository := &goldenStateRepositoryFake{state: local, barrier: barrier}
			results := [2]resolveResult{}
			var group sync.WaitGroup
			for index := range commands {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					results[index].state, results[index].changed, results[index].err =
						arena.NewGoldenNoShowUseCase(
							localRepository,
							fixedArenaClock{now: local.Windows[0].Deadline.Add(time.Nanosecond)},
						).Resolve(t.Context(), commands[index])
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

		local := goldenStateFixture(t, openedAt.Add(4*time.Hour))
		first := goldenNoShowCommand(local, 1700)
		identical := run(t, [2]arena.GoldenNoShowCommand{first, first})
		require.NoError(t, identical[0].err)
		require.NoError(t, identical[1].err)
		require.NotEqual(t, identical[0].changed, identical[1].changed)

		second := goldenNoShowCommand(local, 1720)
		competing := run(t, [2]arena.GoldenNoShowCommand{first, second})
		successes := 0
		conflicts := 0
		for _, result := range competing {
			if result.err == nil && result.changed {
				successes++
			}
			if errors.Is(result.err, arena.ErrGoldenNoShowAuthorityConflict) {
				conflicts++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, conflicts)
	})

	t.Run("rejects overflow aliases and malformed repository results", func(t *testing.T) {
		makeOverflowState := func(t *testing.T, kind string) arena.GoldenState {
			t.Helper()
			localOpenedAt := openedAt.Add(5 * time.Hour)
			local := goldenStateFixture(t, localOpenedAt)
			switch kind {
			case "state":
				localRepository := &goldenStateRepositoryFake{state: local}
				local = goldenAcceptReady(
					t,
					localRepository,
					local,
					local.Group.Members[0].ParticipantID,
					localOpenedAt.Add(time.Second),
					1800,
				)
				previousStateID := task047ID(1810)
				local.ReadyEvents[0].ExpectedState.RevisionID = previousStateID
				local.ReadyEvents[0].ExpectedState.Revision = math.MaxInt64 - 1
				local.PreviousRevisionID = &previousStateID
				local.Revision = math.MaxInt64
			case "window":
				previousWindowID := local.Windows[0].RevisionID
				local.Windows[0].PreviousRevisionID = &previousWindowID
				local.Windows[0].RevisionID = task047ID(1820)
				local.Windows[0].Revision = math.MaxInt64
			case "membership":
				previousMembershipID := local.Membership.RevisionID
				local.Membership.PreviousRevisionID = &previousMembershipID
				local.Membership.RevisionID = task047ID(1830)
				local.Membership.Revision = math.MaxInt64
			}
			built, buildErr := arena.BuildGoldenState(local)
			require.NoError(t, buildErr)
			return built
		}
		for _, kind := range []string{"state", "window", "membership"} {
			t.Run(kind, func(t *testing.T) {
				local := makeOverflowState(t, kind)
				localRepository := &goldenStateRepositoryFake{state: local}
				result, changed, resolveErr := arena.NewGoldenNoShowUseCase(
					localRepository,
					fixedArenaClock{now: local.Windows[0].Deadline.Add(time.Nanosecond)},
				).Resolve(t.Context(), goldenNoShowCommand(local, 1840))
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, resolveErr, arena.ErrGoldenRevisionOverflow)
				require.Zero(t, localRepository.commitCount())
			})
		}

		local := goldenStateFixture(t, openedAt.Add(5*time.Hour))
		aliasRepository := &goldenStateRepositoryFake{state: local}
		aliasCommand := goldenNoShowCommand(local, 1860)
		aliasCommand.NextMembershipRevisionID = local.ExactPlan.Groups[0].Edges[0].ReservationID
		_, changed, aliasErr := arena.NewGoldenNoShowUseCase(
			aliasRepository,
			fixedArenaClock{now: local.Windows[0].Deadline.Add(time.Nanosecond)},
		).Resolve(t.Context(), aliasCommand)
		require.False(t, changed)
		require.ErrorIs(t, aliasErr, arena.ErrInvalidGoldenState)
		require.Zero(t, aliasRepository.commitCount())

		loadCause := errors.New("no-show load failed")
		_, _, loadErr := arena.NewGoldenNoShowUseCase(
			&goldenStateRepositoryFake{state: local, loadErr: loadCause},
			fixedArenaClock{now: local.Windows[0].Deadline.Add(time.Nanosecond)},
		).Resolve(t.Context(), goldenNoShowCommand(local, 1880))
		require.ErrorIs(t, loadErr, loadCause)

		nilCommit := &goldenStateRepositoryFake{
			state: local,
			commitHook: func(arena.GoldenStateCommit) (*arena.GoldenState, bool, error) {
				return nil, true, nil
			},
		}
		_, _, commitErr := arena.NewGoldenNoShowUseCase(
			nilCommit,
			fixedArenaClock{now: local.Windows[0].Deadline.Add(time.Nanosecond)},
		).Resolve(t.Context(), goldenNoShowCommand(local, 1890))
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})

	t.Run("returns deep copies of retained no-show evidence", func(t *testing.T) {
		localOpenedAt := openedAt.Add(6 * time.Hour)
		local := goldenStateFixture(t, localOpenedAt)
		localRepository := &goldenStateRepositoryFake{state: local}
		resolved := goldenResolveNoShow(t, localRepository, local, localOpenedAt, 1900)
		originalExcluded := append([]uuid.UUID(nil), resolved.NoShows[0].ExcludedParticipantIDs...)
		resolved.NoShows[0].ExcludedParticipantIDs[0] = task047ID(1910)
		resolved.NoShows[0].ExpectedState.Membership.PreviousRevisionID = goldenTestUUID(task047ID(1911))
		reloaded, loadErr := localRepository.LoadGoldenState(t.Context(), local.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, originalExcluded, reloaded.NoShows[0].ExcludedParticipantIDs)
		require.Nil(t, reloaded.NoShows[0].ExpectedState.Membership.PreviousRevisionID)
	})
}

func goldenTestUUID(value uuid.UUID) *uuid.UUID {
	return &value
}

func goldenNoShowCommand(state arena.GoldenState, base int) arena.GoldenNoShowCommand {
	window := state.Windows[len(state.Windows)-1]
	return arena.GoldenNoShowCommand{
		Scope: state.Scope, CommandID: task047ID(base),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: task047ID(base + 1), NextWindowRevisionID: task047ID(base + 2),
		NextMembershipRevisionID: task047ID(base + 3),
	}
}
