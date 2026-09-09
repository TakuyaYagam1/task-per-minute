package golden_test

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

func TestGoldenPermanentExclusion(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	state := stateGoldenStateFixture(t, openedAt)
	repository := stateNewGoldenStateRepository(t, state)
	participantID := state.Group.Members[0].ParticipantID
	readyCommand := goldenusecase.GoldenReadyCommand{
		Scope: state.Scope, CommandID: stateTask047ID(200),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: state.Windows[0].AttemptID, WindowID: state.Windows[0].ID,
		ExpectedState: state.Expectation(), ExpectedWindow: state.Windows[0].Expectation(),
		NextStateRevisionID: stateTask047ID(201), NextWindowRevisionID: stateTask047ID(202),
		NextReadinessRevisionID: stateTask047ID(203),
	}
	ready, changed, err := goldenusecase.NewGoldenParticipationUseCase(
		repository,
		stateNewGoldenClock(t, openedAt.Add(time.Second)),
	).AcceptReady(t.Context(), readyCommand)
	require.NoError(t, err)
	require.True(t, changed)

	window := ready.Windows[0]
	command := goldenusecase.GoldenNoShowCommand{
		Scope: ready.Scope, CommandID: stateTask047ID(210),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: ready.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: stateTask047ID(211), NextWindowRevisionID: stateTask047ID(212),
		NextMembershipRevisionID: stateTask047ID(213),
	}
	tooEarly, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		stateNewGoldenClock(t, window.Deadline.Add(-time.Nanosecond)),
	).Resolve(t.Context(), command)
	require.Nil(t, tooEarly)
	require.False(t, changed)
	require.ErrorIs(t, err, goldenusecase.ErrGoldenNoShowCutoff)
	require.Equal(t, 1, repository.commitCount())
	cutoff, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		stateNewGoldenClock(t, window.Deadline),
	).Resolve(t.Context(), command)
	require.Nil(t, cutoff)
	require.False(t, changed)
	require.ErrorIs(t, err, goldenusecase.ErrGoldenNoShowCutoff)
	require.Equal(t, 1, repository.commitCount())

	resolved, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		stateNewGoldenClock(t, window.Deadline.Add(time.Nanosecond)),
	).Resolve(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, goldenusecase.GoldenReadyWindowExpired, resolved.Windows[0].State)
	require.Len(t, resolved.NoShows, 1)
	require.Equal(t, window.Deadline.Add(time.Nanosecond), resolved.NoShows[0].ResolvedAt)
	require.Equal(t, window.ReadinessDigest, resolved.NoShows[0].ReadinessDigest)
	require.Equal(t, window.PresenceDigest, resolved.NoShows[0].PresenceDigest)
	require.Len(t, resolved.NoShows[0].ExcludedParticipantIDs, 2)
	require.Equal(t, domain.GoldenAttemptStateCancelled, resolved.Group.Attempts[0].State)
	require.Equal(t, 2, repository.commitCount(), "the whole absent set must use one CAS")
	for _, member := range resolved.Group.Members {
		require.Equal(t, member.ParticipantID != participantID, member.Excluded)
	}

	replayed, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		stateNewGoldenClock(t, window.Deadline.Add(time.Minute)),
	).Resolve(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, resolved.PayloadDigest, replayed.PayloadDigest)
	require.Equal(t, 2, repository.commitCount())

	reused := command
	reused.AttemptID = stateTask047ID(999)
	conflict, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		stateNewGoldenClock(t, window.Deadline.Add(time.Minute)),
	).Resolve(t.Context(), reused)
	require.Nil(t, conflict)
	require.False(t, changed)
	require.ErrorIs(t, err, goldenusecase.ErrGoldenCommandReuse)

	malformed := resolved.Snapshot()
	excludedID := resolved.NoShows[0].ExcludedParticipantIDs[0]
	malformed.Group.Attempts = append(malformed.Group.Attempts, domain.GoldenAttempt{
		ID: stateTask047ID(220), GroupID: malformed.Scope.GroupID,
		GroupRevisionID: malformed.Scope.GroupRevisionID, AttemptNo: 2,
		PreviousAttemptID: &malformed.Group.Attempts[0].ID,
		State:             domain.GoldenAttemptStateWaitingReady,
		ParticipantIDs:    []uuid.UUID{participantID, excludedID},
	})
	malformed.RevisionID = stateTask047ID(221)
	malformed.Revision++
	malformed.PreviousRevisionID = &resolved.RevisionID
	_, err = goldenusecase.BuildGoldenState(malformed)
	require.ErrorIs(t, err, goldenusecase.ErrInvalidGoldenState, "an excluded member must not enter a later attempt")
	require.ErrorIs(t, err, goldenusecase.ErrInvalidGoldenNoShow)

	t.Run("retains an empty terminal resolution when every member readied", func(t *testing.T) {
		local := stateGoldenStateFixture(t, openedAt.Add(time.Hour))
		localRepository := stateNewGoldenStateRepository(t, local)
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
		require.Equal(t, domain.GoldenAttemptStateWaitingReady, terminal.Group.Attempts[0].State)
		require.Equal(t, 4, localRepository.commitCount())
	})

	t.Run("requires exact no-show successor linkage", func(t *testing.T) {
		mutations := map[string]func(*goldenusecase.GoldenState){
			"state": func(value *goldenusecase.GoldenState) {
				value.NoShows[0].ResultStateRevisionID = stateTask047ID(1100)
			},
			"membership": func(value *goldenusecase.GoldenState) {
				value.NoShows[0].ResultMembershipRevisionID = stateTask047ID(1101)
			},
			"window evidence": func(value *goldenusecase.GoldenState) {
				value.NoShows[0].ExpectedWindow.ReadinessRevisionID = stateTask047ID(1102)
			},
			"state predecessor": func(value *goldenusecase.GoldenState) {
				value.PreviousRevisionID = goldenTestUUID(stateTask047ID(1103))
			},
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				forged := resolved.Snapshot()
				mutate(&forged)
				_, buildErr := goldenusecase.BuildGoldenState(forged)
				require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
			})
		}
	})

	t.Run("requires every later attempt to retain all active members", func(t *testing.T) {
		localOpenedAt := openedAt.Add(2 * time.Hour)
		local := goldenFourMemberStateFixture(t, localOpenedAt)
		localRepository := stateNewGoldenStateRepository(t, local)
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
		forged.Group.Attempts = append(forged.Group.Attempts, domain.GoldenAttempt{
			ID: stateTask047ID(1340), GroupID: forged.Scope.GroupID,
			GroupRevisionID: forged.Scope.GroupRevisionID, AttemptNo: 2,
			PreviousAttemptID: &previousAttemptID,
			State:             domain.GoldenAttemptStateWaitingReady,
			ParticipantIDs:    append([]uuid.UUID(nil), active[:2]...),
		})
		secondOpenedAt := terminal.NoShows[0].ResolvedAt.Add(time.Second)
		forged.Windows = append(forged.Windows, goldenusecase.GoldenReadyWindow{
			ID: stateTask047ID(1341), RevisionID: stateTask047ID(1342), Revision: 1,
			AttemptID: stateTask047ID(1340), AttemptNo: 2,
			OpenedAt: secondOpenedAt, Deadline: secondOpenedAt.Add(30 * time.Second),
			State:               goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: stateTask047ID(1343), ReadinessRevision: 1,
			PresenceRevisionID: stateTask047ID(1344), PresenceRevision: 1,
			PresentParticipantIDs: append([]uuid.UUID(nil), active[:2]...),
		})
		_, buildErr := goldenusecase.BuildGoldenState(forged)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenState)
	})

	t.Run("rejects every stale authority component without writing", func(t *testing.T) {
		type staleCase struct {
			name   string
			mutate func(*goldenusecase.GoldenNoShowCommand)
		}
		cases := []staleCase{
			{name: "attempt", mutate: func(value *goldenusecase.GoldenNoShowCommand) { value.AttemptID = stateTask047ID(1400) }},
			{name: "window", mutate: func(value *goldenusecase.GoldenNoShowCommand) { value.WindowID = stateTask047ID(1401) }},
			{name: "plan", mutate: func(value *goldenusecase.GoldenNoShowCommand) { value.ExpectedState.Plan.PlanID = stateTask047ID(1402) }},
			{name: "group", mutate: func(value *goldenusecase.GoldenNoShowCommand) {
				value.ExpectedState.Scope.GroupID = stateTask047ID(1403)
			}},
			{name: "source", mutate: func(value *goldenusecase.GoldenNoShowCommand) {
				value.ExpectedState.SourceProjectionPayloadDigest[0] ^= 0xff
			}},
			{name: "state", mutate: func(value *goldenusecase.GoldenNoShowCommand) { value.ExpectedState.Revision++ }},
			{name: "membership", mutate: func(value *goldenusecase.GoldenNoShowCommand) {
				value.ExpectedState.Membership.Revision++
			}},
			{name: "window revision", mutate: func(value *goldenusecase.GoldenNoShowCommand) { value.ExpectedWindow.Revision++ }},
			{name: "readiness", mutate: func(value *goldenusecase.GoldenNoShowCommand) {
				value.ExpectedWindow.ReadinessRevision++
			}},
			{name: "presence", mutate: func(value *goldenusecase.GoldenNoShowCommand) {
				value.ExpectedWindow.PresenceRevision++
			}},
		}
		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				local := stateGoldenStateFixture(t, openedAt.Add(3*time.Hour))
				localRepository := stateNewGoldenStateRepository(t, local)
				command := stateGoldenNoShowCommand(local, 1500+index*10)
				testCase.mutate(&command)
				result, changed, resolveErr := goldenusecase.NewGoldenNoShowUseCase(
					localRepository,
					stateNewGoldenClock(t, local.Windows[0].Deadline.Add(time.Nanosecond)),
				).Resolve(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, resolveErr, goldenusecase.ErrGoldenNoShowAuthorityConflict)
				require.Zero(t, localRepository.commitCount())
			})
		}
	})

	t.Run("reconciles synchronized duplicate and competing expiry commands", func(t *testing.T) {
		type resolveResult struct {
			state   *goldenusecase.GoldenState
			changed bool
			err     error
		}
		run := func(t *testing.T, commands [2]goldenusecase.GoldenNoShowCommand) [2]resolveResult {
			t.Helper()
			local := stateGoldenStateFixture(t, openedAt.Add(4*time.Hour))
			barrier := stateNewGoldenLoadBarrier(2)
			localRepository := stateNewGoldenStateRepository(t, local, withGoldenLoadBarrier(barrier))
			results := [2]resolveResult{}
			var group sync.WaitGroup
			for index := range commands {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					results[index].state, results[index].changed, results[index].err =
						goldenusecase.NewGoldenNoShowUseCase(
							localRepository,
							stateNewGoldenClock(t, local.Windows[0].Deadline.Add(time.Nanosecond)),
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

		local := stateGoldenStateFixture(t, openedAt.Add(4*time.Hour))
		first := stateGoldenNoShowCommand(local, 1700)
		identical := run(t, [2]goldenusecase.GoldenNoShowCommand{first, first})
		require.NoError(t, identical[0].err)
		require.NoError(t, identical[1].err)
		require.NotEqual(t, identical[0].changed, identical[1].changed)

		second := stateGoldenNoShowCommand(local, 1720)
		competing := run(t, [2]goldenusecase.GoldenNoShowCommand{first, second})
		successes := 0
		conflicts := 0
		for _, result := range competing {
			if result.err == nil && result.changed {
				successes++
			}
			if errors.Is(result.err, goldenusecase.ErrGoldenNoShowAuthorityConflict) {
				conflicts++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, conflicts)
	})

	t.Run("rejects overflow aliases and malformed repository results", func(t *testing.T) {
		makeOverflowState := func(t *testing.T, kind string) goldenusecase.GoldenState {
			t.Helper()
			localOpenedAt := openedAt.Add(5 * time.Hour)
			local := stateGoldenStateFixture(t, localOpenedAt)
			switch kind {
			case "state":
				localRepository := stateNewGoldenStateRepository(t, local)
				local = stateGoldenAcceptReady(
					t,
					localRepository,
					local,
					local.Group.Members[0].ParticipantID,
					localOpenedAt.Add(time.Second),
					1800,
				)
				previousStateID := stateTask047ID(1810)
				local.ReadyEvents[0].ExpectedState.RevisionID = previousStateID
				local.ReadyEvents[0].ExpectedState.Revision = math.MaxInt64 - 1
				local.PreviousRevisionID = &previousStateID
				local.Revision = math.MaxInt64
			case "window":
				previousWindowID := local.Windows[0].RevisionID
				local.Windows[0].PreviousRevisionID = &previousWindowID
				local.Windows[0].RevisionID = stateTask047ID(1820)
				local.Windows[0].Revision = math.MaxInt64
			case "membership":
				previousMembershipID := local.Membership.RevisionID
				local.Membership.PreviousRevisionID = &previousMembershipID
				local.Membership.RevisionID = stateTask047ID(1830)
				local.Membership.Revision = math.MaxInt64
			}
			built, buildErr := goldenusecase.BuildGoldenState(local)
			require.NoError(t, buildErr)
			return built
		}
		for _, kind := range []string{"state", "window", "membership"} {
			t.Run(kind, func(t *testing.T) {
				local := makeOverflowState(t, kind)
				localRepository := stateNewGoldenStateRepository(t, local)
				result, changed, resolveErr := goldenusecase.NewGoldenNoShowUseCase(
					localRepository,
					stateNewGoldenClock(t, local.Windows[0].Deadline.Add(time.Nanosecond)),
				).Resolve(t.Context(), stateGoldenNoShowCommand(local, 1840))
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, resolveErr, goldenusecase.ErrGoldenRevisionOverflow)
				require.Zero(t, localRepository.commitCount())
			})
		}

		local := stateGoldenStateFixture(t, openedAt.Add(5*time.Hour))
		aliasRepository := stateNewGoldenStateRepository(t, local)
		aliasCommand := stateGoldenNoShowCommand(local, 1860)
		aliasCommand.NextMembershipRevisionID = local.ExactPlan.Groups[0].Edges[0].ReservationID
		_, changed, aliasErr := goldenusecase.NewGoldenNoShowUseCase(
			aliasRepository,
			stateNewGoldenClock(t, local.Windows[0].Deadline.Add(time.Nanosecond)),
		).Resolve(t.Context(), aliasCommand)
		require.False(t, changed)
		require.ErrorIs(t, aliasErr, goldenusecase.ErrInvalidGoldenState)
		require.Zero(t, aliasRepository.commitCount())

		loadCause := errors.New("no-show load failed")
		_, _, loadErr := goldenusecase.NewGoldenNoShowUseCase(
			stateNewGoldenStateRepository(t, local, withGoldenLoadError(loadCause)),
			stateNewGoldenClock(t, local.Windows[0].Deadline.Add(time.Nanosecond)),
		).Resolve(t.Context(), stateGoldenNoShowCommand(local, 1880))
		require.ErrorIs(t, loadErr, loadCause)

		nilCommit := stateNewGoldenStateRepository(
			t,
			local,
			withGoldenCommitHook(func(goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
				return nil, true, nil
			}),
		)
		_, _, commitErr := goldenusecase.NewGoldenNoShowUseCase(
			nilCommit,
			stateNewGoldenClock(t, local.Windows[0].Deadline.Add(time.Nanosecond)),
		).Resolve(t.Context(), stateGoldenNoShowCommand(local, 1890))
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})

	t.Run("returns deep copies of retained no-show evidence", func(t *testing.T) {
		localOpenedAt := openedAt.Add(6 * time.Hour)
		local := stateGoldenStateFixture(t, localOpenedAt)
		localRepository := stateNewGoldenStateRepository(t, local)
		resolved := stateGoldenResolveNoShow(t, localRepository, local, localOpenedAt, 1900)
		originalExcluded := append([]uuid.UUID(nil), resolved.NoShows[0].ExcludedParticipantIDs...)
		resolved.NoShows[0].ExcludedParticipantIDs[0] = stateTask047ID(1910)
		resolved.NoShows[0].ExpectedState.Membership.PreviousRevisionID = goldenTestUUID(stateTask047ID(1911))
		reloaded, loadErr := localRepository.LoadGoldenState(t.Context(), local.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, originalExcluded, reloaded.NoShows[0].ExcludedParticipantIDs)
		require.Nil(t, reloaded.NoShows[0].ExpectedState.Membership.PreviousRevisionID)
	})
}

func goldenTestUUID(value uuid.UUID) *uuid.UUID {
	return &value
}

func stateGoldenNoShowCommand(state goldenusecase.GoldenState, base int) goldenusecase.GoldenNoShowCommand {
	window := state.Windows[len(state.Windows)-1]
	return goldenusecase.GoldenNoShowCommand{
		Scope: state.Scope, CommandID: stateTask047ID(base),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: stateTask047ID(base + 1), NextWindowRevisionID: stateTask047ID(base + 2),
		NextMembershipRevisionID: stateTask047ID(base + 3),
	}
}
