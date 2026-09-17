package state_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type stateGoldenStateRepositoryHarness struct {
	*goldenmocks.MockStateRepository

	state *goldenStateRepositoryState
}

type goldenStateRepositoryOption func(*goldenStateRepositoryState)

func stateNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockStateClock {
	t.Helper()

	clock := goldenmocks.NewMockStateClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func stateNewGoldenStateRepository(
	t *testing.T,
	initial goldenusecase.GoldenState,
	options ...goldenStateRepositoryOption,
) *stateGoldenStateRepositoryHarness {
	t.Helper()

	state := &goldenStateRepositoryState{state: initial.Snapshot()}
	for _, option := range options {
		option(state)
	}
	repository := &stateGoldenStateRepositoryHarness{
		MockStateRepository: goldenmocks.NewMockStateRepository(t),
		state:               state,
	}
	repository.EXPECT().LoadGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			state.mu.Lock()
			current := state.state.Snapshot()
			loadErr := state.loadErr
			barrier := state.barrier
			state.mu.Unlock()
			if loadErr != nil {
				return goldenusecase.GoldenState{}, loadErr
			}
			if barrier != nil {
				barrier.wait()
			}
			return current, nil
		},
	).Maybe()
	repository.EXPECT().CommitGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, commit goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.commitErr != nil {
				return nil, false, state.commitErr
			}
			if state.commitHook != nil {
				return state.commitHook(commit)
			}
			if !commit.Expected.Equal(state.state.Expectation()) {
				return nil, false, domain.ErrConflict
			}
			state.commits++
			state.state = commit.Next.Snapshot()
			result := state.state.Snapshot()
			return &result, true, nil
		},
	).Maybe()
	return repository
}

func withGoldenLoadError(err error) goldenStateRepositoryOption {
	return func(state *goldenStateRepositoryState) {
		state.loadErr = err
	}
}

func withGoldenCommitError(err error) goldenStateRepositoryOption {
	return func(state *goldenStateRepositoryState) {
		state.commitErr = err
	}
}

func withGoldenCommitHook(
	hook func(goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error),
) goldenStateRepositoryOption {
	return func(state *goldenStateRepositoryState) {
		state.commitHook = hook
	}
}

func withGoldenLoadBarrier(barrier *stateGoldenLoadBarrier) goldenStateRepositoryOption {
	return func(state *goldenStateRepositoryState) {
		state.barrier = barrier
	}
}

type stateGoldenLoadBarrier struct {
	limit   int32
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
}

func stateNewGoldenLoadBarrier(limit int) *stateGoldenLoadBarrier {
	return &stateGoldenLoadBarrier{
		limit: int32(limit), arrived: make(chan struct{}, limit), release: make(chan struct{}),
	}
}

func (b *stateGoldenLoadBarrier) wait() {
	if b.calls.Add(1) > b.limit {
		return
	}
	b.arrived <- struct{}{}
	<-b.release
}

func (r *stateGoldenStateRepositoryHarness) commitCount() int {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.commits
}

func stateGoldenStateFixture(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()

	authority, command := stateGoldenPlanExact(t)
	exactPlan, err := goldenplan.BuildExactPlan(command, authority)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(exactPlan.Groups), 2)
	groupPlan := exactPlan.Groups[1]
	topology := authority.Groups[1].Revision

	members := topology.Members()
	participantIDs := make([]uuid.UUID, len(members))
	groupMembers := make([]domain.GoldenMember, len(members))
	for index, member := range members {
		participantIDs[index] = member.ParticipantID
		groupMembers[index] = domain.GoldenMember{ParticipantID: member.ParticipantID}
	}
	attemptID := stateTask047ID(10)
	group := domain.GoldenGroupState{
		ID: topology.GroupID(), TournamentID: topology.TournamentID(),
		RevisionID: topology.RevisionID(), SourceProjectionRevisionID: topology.SourceProjectionRevisionID(),
		PositionFrom: groupPlan.PositionFrom, PositionTo: groupPlan.PositionTo, Members: groupMembers,
		Attempts: []domain.GoldenAttempt{{
			ID: attemptID, GroupID: topology.GroupID(), GroupRevisionID: topology.RevisionID(),
			AttemptNo: 1, State: domain.GoldenAttemptStateWaitingReady,
			ParticipantIDs: participantIDs,
		}},
	}
	state, err := goldenusecase.BuildGoldenState(goldenusecase.GoldenState{
		Scope: goldenusecase.GoldenStateScope{
			TournamentID: topology.TournamentID(), GroupID: topology.GroupID(),
			GroupRevisionID: topology.RevisionID(),
		},
		Topology: topology, ExactPlan: exactPlan, Group: group,
		Membership: goldenusecase.GoldenMembershipRevision{
			RevisionID: stateTask047ID(22), Revision: 1,
		},
		RevisionID: stateTask047ID(30), Revision: 1,
		Windows: []goldenusecase.GoldenReadyWindow{{
			ID: stateTask047ID(40), RevisionID: stateTask047ID(41), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second),
			State:               goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: stateTask047ID(42), ReadinessRevision: 1,
			PresenceRevisionID: stateTask047ID(43), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	return state
}

func goldenReadyCommand(
	state goldenusecase.GoldenState,
	participantID uuid.UUID,
	base int,
) goldenusecase.GoldenReadyCommand {
	window := state.Windows[len(state.Windows)-1]
	return goldenusecase.GoldenReadyCommand{
		Scope: state.Scope, CommandID: stateTask047ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: stateTask047ID(base + 1), NextWindowRevisionID: stateTask047ID(base + 2),
		NextReadinessRevisionID: stateTask047ID(base + 3),
	}
}

func goldenFourMemberStateFixture(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()

	now := openedAt.Add(-time.Hour)
	tournamentID := stateGoldenPlanID(6000)
	source := stateMustGoldenPlanProjection(
		t,
		tournamentID,
		stateGoldenPlanID(6001),
		stateGoldenPlanRevisionID(6002),
		1,
		stateGoldenPlanStandings([]int{10, 10, 9, 9, 9, 9, 7}),
	)
	partition, err := goldenplan.PartitionTies(source)
	require.NoError(t, err)
	seeds := partition.Groups()
	require.Len(t, seeds, 2)
	require.Len(t, seeds[1].Members, 4)

	groups := make([]goldenplan.GroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := goldenplan.GroupRevisionCommand{
			TournamentID: tournamentID, GroupID: stateGoldenPlanID(6010 + index),
			RevisionID: stateGoldenPlanRevisionID(6020 + index), RevisionNo: 1,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seed.PositionFrom,
			PositionTo:                  seed.PositionTo,
		}
		revision, buildErr := goldenplan.BuildGroupRevision(command, source, seed, nil, nil)
		require.NoError(t, buildErr)
		members := revision.Members()
		active := make([]uuid.UUID, len(members))
		for memberIndex, member := range members {
			active[memberIndex] = member.ParticipantID
		}
		groups[index] = goldenplan.GroupAuthority{
			Revision: revision, ActiveParticipantIDs: active,
		}
	}

	poolID := stateGoldenPlanID(6030)
	candidates := make([]goldenplan.TaskVersion, 6)
	versions := make([]domain.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := stateGoldenPlanTask(6040 + index)
		versions[index] = domain.TaskVersionRef{TaskID: task.ID, Version: 2}
		candidates[index] = goldenplan.TaskVersion{
			PoolRevisionID: poolID,
			Version:        2,
			Task:           task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: 2, PoolRevisionID: poolID,
				PoolKind: domain.AssignmentTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: goldenplan.TaskArtifactDigest(task, 2),
		}
	}

	reservations := make([]goldenplan.ParticipantReservation, 0, 6)
	for _, group := range groups {
		for _, participantID := range group.ActiveParticipantIDs {
			index := len(reservations)
			playerID := stateGoldenPlanID(6100 + index)
			reservations = append(reservations, goldenplan.ParticipantReservation{
				ParticipantID: participantID,
				PlayerID:      playerID,
				Reservation: domain.ParticipantReservation{
					PlayerID: playerID, ReservationID: stateGoldenPlanID(6120 + index),
					TournamentID: tournamentID,
					Revision:     1, AcquiredAt: now, UpdatedAt: now,
				},
			})
		}
	}
	authority, err := goldenplan.BuildAuthority(goldenplan.Authority{
		Scope: goldenplan.Scope{TournamentID: tournamentID, PlanSetID: stateGoldenPlanID(6200)},
		Revisions: goldenplan.Revisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         stateGoldenPlanID(6201), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: stateGoldenPlanID(6203), HistoryRevision: 1,
			TaskHealthRevisionID: stateGoldenPlanID(6204), TaskHealthRevision: 1,
			ArtifactRevisionID: stateGoldenPlanID(6205), ArtifactRevision: 1,
			ReservationRevisionID: stateGoldenPlanID(6206), ReservationRevision: 1,
			MembershipRevisionID: stateGoldenPlanID(6207), MembershipRevision: 1,
		},
		Source: source, Groups: groups,
		Pool:       domain.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindGolden, Versions: versions},
		Candidates: candidates, ParticipantReservations: reservations,
	})
	require.NoError(t, err)

	groupCommands := make([]goldenplan.GroupCommand, len(groups))
	for groupIndex, group := range groups {
		groupCommands[groupIndex].GroupID = group.Revision.GroupID()
		groupCommands[groupIndex].GroupRevisionID = group.Revision.RevisionID()
		for edgeIndex := range groupCommands[groupIndex].EdgeIDs {
			base := 6300 + groupIndex*100 + edgeIndex*10
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = stateGoldenPlanID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = stateGoldenPlanID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = stateGoldenPlanID(base + 3)
		}
	}
	exactPlan, err := goldenplan.BuildExactPlan(goldenplan.Command{
		Scope: authority.Scope, PlanID: stateGoldenPlanID(6500), PlanRevisionID: stateGoldenPlanID(6501),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}, authority)
	require.NoError(t, err)

	groupPlan := exactPlan.Groups[1]
	topology := authority.Groups[1].Revision
	members := topology.Members()
	participantIDs := make([]uuid.UUID, len(members))
	groupMembers := make([]domain.GoldenMember, len(members))
	for index, member := range members {
		participantIDs[index] = member.ParticipantID
		groupMembers[index] = domain.GoldenMember{ParticipantID: member.ParticipantID}
	}
	attemptID := stateTask047ID(6600)
	state, err := goldenusecase.BuildGoldenState(goldenusecase.GoldenState{
		Scope: goldenusecase.GoldenStateScope{
			TournamentID: tournamentID, GroupID: topology.GroupID(),
			GroupRevisionID: topology.RevisionID(),
		},
		Topology: topology, ExactPlan: exactPlan,
		Group: domain.GoldenGroupState{
			ID: topology.GroupID(), TournamentID: tournamentID,
			RevisionID: topology.RevisionID(), SourceProjectionRevisionID: topology.SourceProjectionRevisionID(),
			PositionFrom: groupPlan.PositionFrom, PositionTo: groupPlan.PositionTo,
			Members: groupMembers,
			Attempts: []domain.GoldenAttempt{{
				ID: attemptID, GroupID: topology.GroupID(), GroupRevisionID: topology.RevisionID(),
				AttemptNo: 1, State: domain.GoldenAttemptStateWaitingReady,
				ParticipantIDs: participantIDs,
			}},
		},
		Membership: goldenusecase.GoldenMembershipRevision{RevisionID: stateTask047ID(6601), Revision: 1},
		RevisionID: stateTask047ID(6602), Revision: 1,
		Windows: []goldenusecase.GoldenReadyWindow{{
			ID: stateTask047ID(6603), RevisionID: stateTask047ID(6604), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1, OpenedAt: openedAt,
			Deadline: openedAt.Add(30 * time.Second), State: goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: stateTask047ID(6605), ReadinessRevision: 1,
			PresenceRevisionID: stateTask047ID(6606), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	return state
}

func stateTask047ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("47000000-0000-0000-0000-%012d", value))
}

var _ goldenusecase.StateRepository = (*stateGoldenStateRepositoryHarness)(nil)
