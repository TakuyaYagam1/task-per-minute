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
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func attemptGoldenPlanStandings(points []int) []swissusecase.NormalStanding {
	standings := make([]swissusecase.NormalStanding, len(points))
	for index, value := range points {
		standings[index] = swissusecase.NormalStanding{
			ParticipantID: attemptGoldenPlanID(100 + index), Position: index + 1,
			Points: value, PointsLabel: swissusecase.PointsFinal, Seed: index + 1,
		}
	}
	return standings
}

func attemptMustGoldenPlanProjection(
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

func attemptGoldenPlanExact(tb testing.TB) (goldenusecase.Authority, goldenusecase.Command) {
	tb.Helper()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tournamentID := attemptGoldenPlanID(400)
	source := attemptMustGoldenPlanProjection(
		tb, tournamentID, attemptGoldenPlanID(401), attemptGoldenPlanRevisionID(402), 1,
		attemptGoldenPlanStandings([]int{10, 10, 9, 9, 9, 7}),
	)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(tb, err)
	seeds := partition.Groups()
	groups := make([]goldenusecase.GroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := goldenusecase.GroupRevisionCommand{
			TournamentID: tournamentID, GroupID: attemptGoldenPlanID(410 + index),
			RevisionID: attemptGoldenPlanRevisionID(420 + index), RevisionNo: 1,
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

	poolID := attemptGoldenPlanID(430)
	candidates := make([]goldenusecase.TaskVersion, 6)
	versions := make([]domain.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := attemptGoldenPlanTask(500 + index)
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
				ParticipantID: participantID, PlayerID: attemptGoldenPlanID(1000 + len(participants)),
				Reservation: domain.ParticipantReservation{
					PlayerID: attemptGoldenPlanID(1000 + len(participants)), ReservationID: attemptGoldenPlanID(1100 + len(participants)),
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
		Scope: goldenusecase.Scope{TournamentID: tournamentID, PlanSetID: attemptGoldenPlanID(440)},
		Revisions: goldenusecase.Revisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         attemptGoldenPlanID(441), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: attemptGoldenPlanID(442), HistoryRevision: 1,
			TaskHealthRevisionID: attemptGoldenPlanID(443), TaskHealthRevision: 1,
			ArtifactRevisionID: attemptGoldenPlanID(444), ArtifactRevision: 1,
			ReservationRevisionID: attemptGoldenPlanID(445), ReservationRevision: 1,
			MembershipRevisionID: attemptGoldenPlanID(446), MembershipRevision: 1,
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
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = attemptGoldenPlanID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = attemptGoldenPlanID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = attemptGoldenPlanID(base + 3)
		}
	}
	command := goldenusecase.Command{
		Scope: authority.Scope, PlanID: attemptGoldenPlanID(700), PlanRevisionID: attemptGoldenPlanID(701),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}
	return authority, command
}

func attemptGoldenPlanTask(value int) domain.Task {
	httpsURL := "https://tasks.example.test/golden"
	return domain.Task{
		ID: attemptGoldenPlanID(value), Title: "Golden challenge", Description: "Solve the Golden challenge.",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyMedium,
		TimeLimit: 300, Flag: "FLAG{golden}", Hints: []string{"Inspect the request."},
		TaskURL: &httpsURL,
	}
}

func attemptGoldenPlanID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + attemptGoldenPlanSuffix(value))
}

func attemptGoldenPlanRevisionID(value int) domain.DerivedRevisionID {
	return domain.DerivedRevisionID(attemptGoldenPlanID(value))
}

func attemptGoldenPlanSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func attemptGoldenStateFixture(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()

	authority, command := attemptGoldenPlanExact(t)
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
	attemptID := attemptGoldenWaveFixtureID(10)
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
			RevisionID: attemptGoldenWaveFixtureID(22), Revision: 1,
		},
		RevisionID: attemptGoldenWaveFixtureID(30), Revision: 1,
		Windows: []goldenusecase.GoldenReadyWindow{{
			ID: attemptGoldenWaveFixtureID(40), RevisionID: attemptGoldenWaveFixtureID(41), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second),
			State:               goldenusecase.GoldenReadyWindowOpen,
			ReadinessRevisionID: attemptGoldenWaveFixtureID(42), ReadinessRevision: 1,
			PresenceRevisionID: attemptGoldenWaveFixtureID(43), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	return state
}

type attemptStartedWaveFixtureState struct {
	mu        sync.Mutex
	state     goldenusecase.GoldenState
	execution *goldenusecase.GoldenWaveExecution
	replays   map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay
}

func attemptNewStartedGoldenFixture(
	t *testing.T,
	startedAt time.Time,
) (goldenusecase.GoldenState, goldenusecase.GoldenWaveExecution) {
	t.Helper()

	openedAt := startedAt.Add(-20 * time.Second)
	state := attemptGoldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [sha256.Size]byte{}
	state.PayloadDigest = [sha256.Size]byte{}
	state, err := goldenusecase.BuildGoldenState(state)
	require.NoError(t, err)

	fixture := &attemptStartedWaveFixtureState{
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
			AssignmentID:  attemptGoldenWaveFixtureID(20020 + index),
		}
	}
	openCommand := goldenusecase.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: attemptGoldenWaveFixtureID(20000), ExpectedState: state.Expectation(),
		AttemptID: attemptGoldenWaveFixtureID(20001), WaveID: attemptGoldenWaveFixtureID(20002),
		WaveRevisionID:       domain.WaveRevisionID(attemptGoldenWaveFixtureID(20003)),
		WindowID:             attemptGoldenWaveFixtureID(20004),
		WaveWindowRevisionID: domain.ReadyWindowRevisionID(attemptGoldenWaveFixtureID(20005)),
		WindowRevisionID:     attemptGoldenWaveFixtureID(20006), ReadinessRevisionID: attemptGoldenWaveFixtureID(20007),
		PresenceRevisionID: attemptGoldenWaveFixtureID(20008), MembershipID: attemptGoldenWaveFixtureID(20009),
		MembershipRevisionID: attemptGoldenWaveFixtureID(20010), AssignmentID: attemptGoldenWaveFixtureID(20011),
		AssignmentRevisionID: attemptGoldenWaveFixtureID(20012), ExecutionRevisionID: attemptGoldenWaveFixtureID(20013),
		PrivateAssignments: privateAssignments,
	}
	opened, changed, err := goldenusecase.NewGoldenReadyWindowUseCase(
		repository,
		attemptNewWaveFixtureClock(t, openedAt),
	).Open(t.Context(), openCommand)
	require.NoError(t, err)
	require.True(t, changed)

	current := *opened
	for index, participantID := range current.Membership.ParticipantIDs {
		readyCommand := goldenusecase.GoldenMarkReadyCommand{
			Scope: current.Scope, CommandID: attemptGoldenWaveFixtureID(20100 + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
			ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
			NextExecutionRevisionID: attemptGoldenWaveFixtureID(20101 + index*10),
			NextWindowRevisionID:    attemptGoldenWaveFixtureID(20102 + index*10),
			NextReadinessRevisionID: attemptGoldenWaveFixtureID(20103 + index*10),
		}
		ready, readyChanged, readyErr := goldenusecase.NewGoldenReadinessUseCase(
			repository,
			attemptNewWaveFixtureClock(t, openedAt.Add(time.Duration(index+1)*time.Second)),
		).MarkReady(t.Context(), readyCommand)
		require.NoError(t, readyErr)
		require.True(t, readyChanged)
		current = *ready
	}

	authority := authoritydomain.Lease{
		TournamentID: state.Scope.TournamentID,
		HolderID:     attemptGoldenWaveFixtureID(28001), LeaseID: attemptGoldenWaveFixtureID(28002),
		Epoch: 2, ProcessKind: authoritydomain.ProcessAuthority, Revision: 2,
		CommandID:  attemptGoldenWaveFixtureID(28003),
		Previous:   &authoritydomain.Stamp{LeaseID: attemptGoldenWaveFixtureID(28004), Epoch: 1},
		AcquiredAt: startedAt.Add(-time.Minute), RenewedAt: startedAt.Add(-time.Second),
		ExpiresAt: startedAt.Add(time.Minute),
	}
	started, startChanged, err := goldenusecase.NewGoldenStartUseCase(
		repository,
		attemptNewWaveFixtureClock(t, startedAt),
	).Start(t.Context(), goldenusecase.GoldenStartCommand{
		Scope: current.Scope, CommandID: attemptGoldenWaveFixtureID(20200),
		AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
		ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
		NextExecutionRevisionID: attemptGoldenWaveFixtureID(20201), NextWindowRevisionID: attemptGoldenWaveFixtureID(20202),
		Authority: authority,
	})
	require.NoError(t, err)
	require.True(t, startChanged)
	return state, *started
}

func attemptNewWaveFixtureClock(t *testing.T, now time.Time) *goldenmocks.MockWaveClock {
	t.Helper()
	clock := goldenmocks.NewMockWaveClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func attemptTask049StartedExecution(t *testing.T, startedAt time.Time) goldenusecase.GoldenWaveExecution {
	t.Helper()
	_, execution := attemptNewStartedGoldenFixture(t, startedAt)
	return execution
}

func attemptGoldenWaveFixtureID(value int) uuid.UUID {
	return uuid.MustParse("48000000-0000-0000-0000-" + attemptGoldenWaveFixtureSuffix(value))
}

func attemptGoldenWaveFixtureSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func attemptTask049ID(value int) uuid.UUID {
	return uuid.MustParse("49000000-0000-0000-0000-" + attemptTask049Suffix(value))
}

func attemptTask049Suffix(value int) string { return attemptGoldenWaveFixtureSuffix(value) }
