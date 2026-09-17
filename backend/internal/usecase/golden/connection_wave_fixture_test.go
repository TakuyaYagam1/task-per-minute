package golden_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave/mocks"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func connectionGoldenPlanStandings(points []int) []swissusecase.NormalStanding {
	standings := make([]swissusecase.NormalStanding, len(points))
	for index, value := range points {
		standings[index] = swissusecase.NormalStanding{
			ParticipantID: connectionGoldenPlanID(100 + index), Position: index + 1,
			Points: value, PointsLabel: swissusecase.PointsFinal, Seed: index + 1,
		}
	}
	return standings
}

func connectionMustGoldenPlanProjection(
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

func connectionGoldenPlanExact(tb testing.TB) (goldenusecase.Authority, goldenusecase.Command) {
	tb.Helper()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tournamentID := connectionGoldenPlanID(400)
	source := connectionMustGoldenPlanProjection(
		tb, tournamentID, connectionGoldenPlanID(401), connectionGoldenPlanRevisionID(402), 1,
		connectionGoldenPlanStandings([]int{10, 10, 9, 9, 9, 7}),
	)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(tb, err)
	seeds := partition.Groups()
	groups := make([]goldenusecase.GroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := goldenusecase.GroupRevisionCommand{
			TournamentID: tournamentID, GroupID: connectionGoldenPlanID(410 + index),
			RevisionID: connectionGoldenPlanRevisionID(420 + index), RevisionNo: 1,
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

	poolID := connectionGoldenPlanID(430)
	candidates := make([]goldenusecase.TaskVersion, 6)
	versions := make([]domain.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := connectionGoldenPlanTask(500 + index)
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
				ParticipantID: participantID, PlayerID: connectionGoldenPlanID(1000 + len(participants)),
				Reservation: domain.ParticipantReservation{
					PlayerID: connectionGoldenPlanID(1000 + len(participants)), ReservationID: connectionGoldenPlanID(1100 + len(participants)),
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
		Scope: goldenusecase.Scope{TournamentID: tournamentID, PlanSetID: connectionGoldenPlanID(440)},
		Revisions: goldenusecase.Revisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         connectionGoldenPlanID(441), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: connectionGoldenPlanID(442), HistoryRevision: 1,
			TaskHealthRevisionID: connectionGoldenPlanID(443), TaskHealthRevision: 1,
			ArtifactRevisionID: connectionGoldenPlanID(444), ArtifactRevision: 1,
			ReservationRevisionID: connectionGoldenPlanID(445), ReservationRevision: 1,
			MembershipRevisionID: connectionGoldenPlanID(446), MembershipRevision: 1,
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
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = connectionGoldenPlanID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = connectionGoldenPlanID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = connectionGoldenPlanID(base + 3)
		}
	}
	command := goldenusecase.Command{
		Scope: authority.Scope, PlanID: connectionGoldenPlanID(700), PlanRevisionID: connectionGoldenPlanID(701),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}
	return authority, command
}

func connectionGoldenPlanTask(value int) domain.Task {
	httpsURL := "https://tasks.example.test/golden"
	return domain.Task{
		ID: connectionGoldenPlanID(value), Title: "Golden challenge", Description: "Solve the Golden challenge.",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyMedium,
		TimeLimit: 300, Flag: "FLAG{golden}", Hints: []string{"Inspect the request."},
		TaskURL: &httpsURL,
	}
}

func connectionGoldenPlanID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + connectionGoldenPlanSuffix(value))
}

func connectionGoldenPlanRevisionID(value int) domain.DerivedRevisionID {
	return domain.DerivedRevisionID(connectionGoldenPlanID(value))
}

func connectionGoldenPlanSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func connectionGoldenStateFixture(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()

	authority, command := connectionGoldenPlanExact(t)
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
	attemptID := connectionGoldenWaveFixtureID(10)
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
			RevisionID: connectionGoldenWaveFixtureID(22), Revision: 1,
		},
		RevisionID: connectionGoldenWaveFixtureID(30), Revision: 1,
		Windows: []goldenusecase.GoldenReadyWindow{{
			ID: connectionGoldenWaveFixtureID(40), RevisionID: connectionGoldenWaveFixtureID(41), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second),
			State:               goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: connectionGoldenWaveFixtureID(42), ReadinessRevision: 1,
			PresenceRevisionID: connectionGoldenWaveFixtureID(43), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	return state
}

type connectionStartedWaveFixtureState struct {
	mu        sync.Mutex
	state     goldenusecase.GoldenState
	execution *goldenusecase.GoldenWaveExecution
	replays   map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay
}

func connectionNewStartedGoldenFixture(
	t *testing.T,
	startedAt time.Time,
) (goldenusecase.GoldenState, goldenusecase.GoldenWaveExecution) {
	t.Helper()

	openedAt := startedAt.Add(-20 * time.Second)
	state := connectionGoldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [sha256.Size]byte{}
	state.PayloadDigest = [sha256.Size]byte{}
	state, err := goldenusecase.BuildGoldenState(state)
	require.NoError(t, err)

	fixture := &connectionStartedWaveFixtureState{
		state:   state.Snapshot(),
		replays: make(map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay),
	}
	repository := goldenmocks.NewMockWaveRepository(t)
	repository.EXPECT().
		LoadGoldenState(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			return fixture.state.Snapshot(), nil
		}).
		Maybe()
	repository.EXPECT().
		FindGoldenWaveCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, commandID uuid.UUID) (*goldenusecase.GoldenWaveCommandReplay, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			replay, found := fixture.replays[commandID]
			if !found {
				return nil, nil
			}
			clone := replay
			clone.Execution = replay.Execution.Snapshot()
			return &clone, nil
		}).
		Maybe()
	repository.EXPECT().
		LoadGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, goldenusecase.GoldenStateScope) (*goldenusecase.GoldenWaveExecution, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.execution == nil {
				return nil, nil
			}
			clone := fixture.execution.Snapshot()
			return &clone, nil
		}).
		Maybe()
	repository.EXPECT().
		CommitGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, commit goldenusecase.GoldenWaveExecutionCommit) (*goldenusecase.GoldenWaveExecution, bool, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if !fixture.state.Expectation().Equal(commit.ExpectedState) {
				return nil, false, domain.ErrConflict
			}
			if commit.ExpectedExecution == nil {
				if fixture.execution != nil {
					return nil, false, domain.ErrConflict
				}
			} else if fixture.execution == nil || !fixture.execution.Expectation().Equal(*commit.ExpectedExecution) {
				return nil, false, domain.ErrConflict
			}
			next := commit.Next.Snapshot()
			fixture.execution = &next
			receipt := next.Receipts[len(next.Receipts)-1]
			fixture.replays[receipt.CommandID] = goldenusecase.GoldenWaveCommandReplay{
				Receipt: receipt, Execution: next.Snapshot(),
			}
			result := next.Snapshot()
			return &result, true, nil
		}).
		Maybe()

	participants := state.ActiveParticipantIDs()
	privateAssignments := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(participants))
	for index, participantID := range participants {
		privateAssignments[index] = goldenusecase.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  connectionGoldenWaveFixtureID(20020 + index),
		}
	}
	openCommand := goldenusecase.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: connectionGoldenWaveFixtureID(20000), ExpectedState: state.Expectation(),
		AttemptID: connectionGoldenWaveFixtureID(20001), WaveID: connectionGoldenWaveFixtureID(20002),
		WaveRevisionID:       domain.WaveRevisionID(connectionGoldenWaveFixtureID(20003)),
		WindowID:             connectionGoldenWaveFixtureID(20004),
		WaveWindowRevisionID: domain.ReadyWindowRevisionID(connectionGoldenWaveFixtureID(20005)),
		WindowRevisionID:     connectionGoldenWaveFixtureID(20006), ReadinessRevisionID: connectionGoldenWaveFixtureID(20007),
		PresenceRevisionID: connectionGoldenWaveFixtureID(20008), MembershipID: connectionGoldenWaveFixtureID(20009),
		MembershipRevisionID: connectionGoldenWaveFixtureID(20010), AssignmentID: connectionGoldenWaveFixtureID(20011),
		AssignmentRevisionID: connectionGoldenWaveFixtureID(20012), ExecutionRevisionID: connectionGoldenWaveFixtureID(20013),
		PrivateAssignments: privateAssignments,
	}
	opened, changed, err := goldenusecase.NewGoldenReadyWindowUseCase(
		repository,
		connectionNewWaveFixtureClock(t, openedAt),
	).Open(t.Context(), openCommand)
	require.NoError(t, err)
	require.True(t, changed)

	current := *opened
	for index, participantID := range current.Membership.ParticipantIDs {
		readyCommand := goldenusecase.GoldenMarkReadyCommand{
			Scope: current.Scope, CommandID: connectionGoldenWaveFixtureID(20100 + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
			ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
			NextExecutionRevisionID: connectionGoldenWaveFixtureID(20101 + index*10),
			NextWindowRevisionID:    connectionGoldenWaveFixtureID(20102 + index*10),
			NextReadinessRevisionID: connectionGoldenWaveFixtureID(20103 + index*10),
		}
		ready, readyChanged, readyErr := goldenusecase.NewGoldenReadinessUseCase(
			repository,
			connectionNewWaveFixtureClock(t, openedAt.Add(time.Duration(index+1)*time.Second)),
		).MarkReady(t.Context(), readyCommand)
		require.NoError(t, readyErr)
		require.True(t, readyChanged)
		current = *ready
	}

	authority := authoritydomain.Lease{
		TournamentID: state.Scope.TournamentID,
		HolderID:     connectionGoldenWaveFixtureID(28001), LeaseID: connectionGoldenWaveFixtureID(28002),
		Epoch: 2, ProcessKind: authoritydomain.ProcessAuthority, Revision: 2,
		CommandID:  connectionGoldenWaveFixtureID(28003),
		Previous:   &authoritydomain.Stamp{LeaseID: connectionGoldenWaveFixtureID(28004), Epoch: 1},
		AcquiredAt: startedAt.Add(-time.Minute), RenewedAt: startedAt.Add(-time.Second),
		ExpiresAt: startedAt.Add(time.Minute),
	}
	started, startChanged, err := goldenusecase.NewGoldenStartUseCase(
		repository,
		connectionNewWaveFixtureClock(t, startedAt),
	).Start(t.Context(), goldenusecase.GoldenStartCommand{
		Scope: current.Scope, CommandID: connectionGoldenWaveFixtureID(20200),
		AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
		ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
		NextExecutionRevisionID: connectionGoldenWaveFixtureID(20201), NextWindowRevisionID: connectionGoldenWaveFixtureID(20202),
		Authority: authority,
	})
	require.NoError(t, err)
	require.True(t, startChanged)
	return state, *started
}

func connectionNewWaveFixtureClock(t *testing.T, now time.Time) *goldenmocks.MockWaveClock {
	t.Helper()
	clock := goldenmocks.NewMockWaveClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func connectionTask049StartedExecution(t *testing.T, startedAt time.Time) goldenusecase.GoldenWaveExecution {
	t.Helper()
	_, execution := connectionNewStartedGoldenFixture(t, startedAt)
	return execution
}

func connectionGoldenWaveFixtureID(value int) uuid.UUID {
	return uuid.MustParse("48000000-0000-0000-0000-" + connectionGoldenWaveFixtureSuffix(value))
}

func connectionGoldenWaveFixtureSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func connectionTask049ID(value int) uuid.UUID {
	return uuid.MustParse("49000000-0000-0000-0000-" + connectionTask049Suffix(value))
}

func connectionTask049Suffix(value int) string { return connectionGoldenWaveFixtureSuffix(value) }
