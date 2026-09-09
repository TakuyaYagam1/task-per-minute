package golden_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func waveGoldenPlanStandings(points []int) []swissusecase.NormalStanding {
	standings := make([]swissusecase.NormalStanding, len(points))
	for index, value := range points {
		standings[index] = swissusecase.NormalStanding{
			ParticipantID: waveGoldenPlanID(100 + index), Position: index + 1,
			Points: value, PointsLabel: swissusecase.PointsFinal, Seed: index + 1,
		}
	}
	return standings
}

func waveMustGoldenPlanProjection(
	tb testing.TB,
	tournamentID uuid.UUID,
	projectionID uuid.UUID,
	revisionID domain.DerivedRevisionID,
	revisionNo int,
	standings []swissusecase.NormalStanding,
) goldenusecase.StandingsProjection {
	tb.Helper()
	source, err := goldenusecase.NewStandingsProjection(
		tournamentID, projectionID, revisionID, revisionNo, nil, true, standings,
	)
	require.NoError(tb, err)
	return source
}

func waveGoldenPlanExact(tb testing.TB) (goldenusecase.Authority, goldenusecase.Command) {
	tb.Helper()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tournamentID := waveGoldenPlanID(400)
	source := waveMustGoldenPlanProjection(
		tb, tournamentID, waveGoldenPlanID(401), waveGoldenPlanRevisionID(402), 1,
		waveGoldenPlanStandings([]int{10, 10, 9, 9, 9, 7}),
	)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(tb, err)
	seeds := partition.Groups()
	groups := make([]goldenusecase.GroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := goldenusecase.GroupRevisionCommand{
			TournamentID: tournamentID, GroupID: waveGoldenPlanID(410 + index),
			RevisionID: waveGoldenPlanRevisionID(420 + index), RevisionNo: 1,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
		}
		revision, buildErr := goldenusecase.BuildGroupRevision(command, source, seed, nil, nil)
		require.NoError(tb, buildErr)
		members := revision.Members()
		active := make([]uuid.UUID, len(members))
		for memberIndex, member := range members {
			active[memberIndex] = member.ParticipantID
		}
		groups[index] = goldenusecase.GroupAuthority{Revision: revision, ActiveParticipantIDs: active}
	}

	poolID := waveGoldenPlanID(430)
	candidates := make([]goldenusecase.TaskVersion, 6)
	versions := make([]domain.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := waveGoldenPlanTask(500 + index)
		version := 2
		versions[index] = domain.TaskVersionRef{TaskID: task.ID, Version: version}
		candidates[index] = goldenusecase.TaskVersion{
			PoolRevisionID: poolID, Version: version, Task: task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: version, PoolRevisionID: poolID,
				PoolKind: domain.AssignmentTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: goldenusecase.TaskArtifactDigest(task, version),
		}
	}

	participants := make([]goldenusecase.ParticipantReservation, 0, 5)
	for _, group := range groups {
		for _, participantID := range group.ActiveParticipantIDs {
			participants = append(participants, goldenusecase.ParticipantReservation{
				ParticipantID: participantID, PlayerID: waveGoldenPlanID(1000 + len(participants)),
				Reservation: domain.ParticipantReservation{
					PlayerID: waveGoldenPlanID(1000 + len(participants)), ReservationID: waveGoldenPlanID(1100 + len(participants)),
					TournamentID: tournamentID,
					Revision:     1, AcquiredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
				},
			})
		}
	}

	history := make([]assignmentusecase.TaskReceiptRef, 0, 3)
	for index := 3; index < 6; index++ {
		history = append(history, assignmentusecase.TaskReceiptRef{
			ParticipantID: groups[1].ActiveParticipantIDs[0],
			TaskID:        versions[index].TaskID, Version: 1,
		})
	}
	authority, err := goldenusecase.BuildAuthority(goldenusecase.Authority{
		Scope: goldenusecase.Scope{TournamentID: tournamentID, PlanSetID: waveGoldenPlanID(440)},
		Revisions: goldenusecase.Revisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         waveGoldenPlanID(441), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: waveGoldenPlanID(442), HistoryRevision: 1,
			TaskHealthRevisionID: waveGoldenPlanID(443), TaskHealthRevision: 1,
			ArtifactRevisionID: waveGoldenPlanID(444), ArtifactRevision: 1,
			ReservationRevisionID: waveGoldenPlanID(445), ReservationRevision: 1,
			MembershipRevisionID: waveGoldenPlanID(446), MembershipRevision: 1,
		},
		Source: source, Groups: groups,
		Pool:    domain.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindGolden, Versions: versions},
		History: history, Candidates: candidates,
		ParticipantReservations: participants,
	})
	require.NoError(tb, err)

	groupCommands := make([]goldenusecase.GroupCommand, len(groups))
	for groupIndex, group := range groups {
		groupCommands[groupIndex].GroupID = group.Revision.GroupID()
		groupCommands[groupIndex].GroupRevisionID = group.Revision.RevisionID()
		for edgeIndex := range groupCommands[groupIndex].EdgeIDs {
			base := 1200 + groupIndex*100 + edgeIndex*10
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = waveGoldenPlanID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = waveGoldenPlanID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = waveGoldenPlanID(base + 3)
		}
	}
	command := goldenusecase.Command{
		Scope: authority.Scope, PlanID: waveGoldenPlanID(700), PlanRevisionID: waveGoldenPlanID(701),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}
	return authority, command
}

func waveGoldenPlanTask(value int) domain.Task {
	httpsURL := "https://tasks.example.test/golden"
	return domain.Task{
		ID: waveGoldenPlanID(value), Title: "Golden challenge", Description: "Solve the Golden challenge.",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyMedium,
		TimeLimit: 300, Flag: "FLAG{golden}", Hints: []string{"Inspect the request."},
		TaskURL: &httpsURL,
	}
}

func waveGoldenPlanID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + waveGoldenPlanSuffix(value))
}

func waveGoldenPlanRevisionID(value int) domain.DerivedRevisionID {
	return domain.DerivedRevisionID(waveGoldenPlanID(value))
}

func waveGoldenPlanSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func waveGoldenStateFixture(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()

	authority, command := waveGoldenPlanExact(t)
	exactPlan, err := goldenusecase.BuildExactPlan(command, authority)
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
	attemptID := waveTask047ID(10)
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
			RevisionID: waveTask047ID(22), Revision: 1,
		},
		RevisionID: waveTask047ID(30), Revision: 1,
		Windows: []goldenusecase.GoldenReadyWindow{{
			ID: waveTask047ID(40), RevisionID: waveTask047ID(41), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second),
			State:               goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: waveTask047ID(42), ReadinessRevision: 1,
			PresenceRevisionID: waveTask047ID(43), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	return state
}

type waveGoldenStateRepositoryHarness struct {
	*goldenmocks.MockStateRepository

	mu    sync.Mutex
	state goldenusecase.GoldenState
}

func waveNewGoldenStateRepository(t *testing.T, initial goldenusecase.GoldenState) *waveGoldenStateRepositoryHarness {
	t.Helper()

	harness := &waveGoldenStateRepositoryHarness{state: initial.Snapshot()}
	repository := goldenmocks.NewMockStateRepository(t)
	repository.EXPECT().LoadGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			return harness.state.Snapshot(), nil
		},
	).Maybe()
	repository.EXPECT().CommitGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, commit goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			if !commit.Expected.Equal(harness.state.Expectation()) {
				return nil, false, domain.ErrConflict
			}
			harness.state = commit.Next.Snapshot()
			result := harness.state.Snapshot()
			return &result, true, nil
		},
	).Maybe()
	harness.MockStateRepository = repository
	return harness
}

func waveNewGoldenStateClock(t *testing.T, now time.Time) *goldenmocks.MockStateClock {
	t.Helper()

	clock := goldenmocks.NewMockStateClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func waveGoldenAcceptReady(
	t *testing.T,
	repository *waveGoldenStateRepositoryHarness,
	state goldenusecase.GoldenState,
	participantID uuid.UUID,
	now time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()
	window := state.Windows[0]
	ready, changed, err := goldenusecase.NewGoldenParticipationUseCase(
		repository,
		waveNewGoldenStateClock(t, now),
	).AcceptReady(t.Context(), goldenusecase.GoldenReadyCommand{
		Scope: state.Scope, CommandID: waveTask047ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: waveTask047ID(base + 1), NextWindowRevisionID: waveTask047ID(base + 2),
		NextReadinessRevisionID: waveTask047ID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	return *ready
}

func waveGoldenNoShowCommand(state goldenusecase.GoldenState, base int) goldenusecase.GoldenNoShowCommand {
	window := state.Windows[len(state.Windows)-1]
	return goldenusecase.GoldenNoShowCommand{
		Scope: state.Scope, CommandID: waveTask047ID(base),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: waveTask047ID(base + 1), NextWindowRevisionID: waveTask047ID(base + 2),
		NextMembershipRevisionID: waveTask047ID(base + 3),
	}
}

func waveTask047ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("47000000-0000-0000-0000-%012d", value))
}
