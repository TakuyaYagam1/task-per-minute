package golden_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func continuationGoldenPlanStandings(points []int) []swissusecase.NormalStanding {
	standings := make([]swissusecase.NormalStanding, len(points))
	for index, value := range points {
		standings[index] = swissusecase.NormalStanding{
			ParticipantID: continuationGoldenPlanID(100 + index), Position: index + 1,
			Points: value, PointsLabel: swissusecase.PointsFinal, Seed: index + 1,
		}
	}
	return standings
}

func continuationMustGoldenPlanProjection(
	tb testing.TB,
	tournamentID uuid.UUID,
	projectionID uuid.UUID,
	revisionID domain.DerivedRevisionID,
	revisionNo int,
	standings []swissusecase.NormalStanding,
) goldenplan.StandingsProjection {
	tb.Helper()
	source, err := goldenplan.NewStandingsProjection(
		tournamentID, projectionID, revisionID, revisionNo, nil, true, standings,
	)
	require.NoError(tb, err)
	return source
}

func continuationGoldenPlanExact(tb testing.TB) (goldenplan.Authority, goldenplan.Command) {
	tb.Helper()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tournamentID := continuationGoldenPlanID(400)
	source := continuationMustGoldenPlanProjection(
		tb, tournamentID, continuationGoldenPlanID(401), continuationGoldenPlanRevisionID(402), 1,
		continuationGoldenPlanStandings([]int{10, 10, 9, 9, 9, 7}),
	)
	partition, err := goldenplan.PartitionTies(source)
	require.NoError(tb, err)
	seeds := partition.Groups()
	groups := make([]goldenplan.GroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := goldenplan.GroupRevisionCommand{
			TournamentID: tournamentID, GroupID: continuationGoldenPlanID(410 + index),
			RevisionID: continuationGoldenPlanRevisionID(420 + index), RevisionNo: 1,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
		}
		revision, buildErr := goldenplan.BuildGroupRevision(command, source, seed, nil, nil)
		require.NoError(tb, buildErr)
		members := revision.Members()
		active := make([]uuid.UUID, len(members))
		for memberIndex, member := range members {
			active[memberIndex] = member.ParticipantID
		}
		groups[index] = goldenplan.GroupAuthority{Revision: revision, ActiveParticipantIDs: active}
	}

	poolID := continuationGoldenPlanID(430)
	candidates := make([]goldenplan.TaskVersion, 6)
	versions := make([]domain.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := continuationGoldenPlanTask(500 + index)
		version := 2
		versions[index] = domain.TaskVersionRef{TaskID: task.ID, Version: version}
		candidates[index] = goldenplan.TaskVersion{
			PoolRevisionID: poolID, Version: version, Task: task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: version, PoolRevisionID: poolID,
				PoolKind: domain.AssignmentTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: goldenplan.TaskArtifactDigest(task, version),
		}
	}

	participants := make([]goldenplan.ParticipantReservation, 0, 5)
	for _, group := range groups {
		for _, participantID := range group.ActiveParticipantIDs {
			participants = append(participants, goldenplan.ParticipantReservation{
				ParticipantID: participantID, PlayerID: continuationGoldenPlanID(1000 + len(participants)),
				Reservation: domain.ParticipantReservation{
					PlayerID: continuationGoldenPlanID(1000 + len(participants)), ReservationID: continuationGoldenPlanID(1100 + len(participants)),
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
	authority, err := goldenplan.BuildAuthority(goldenplan.Authority{
		Scope: goldenplan.Scope{TournamentID: tournamentID, PlanSetID: continuationGoldenPlanID(440)},
		Revisions: goldenplan.Revisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         continuationGoldenPlanID(441), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: continuationGoldenPlanID(442), HistoryRevision: 1,
			TaskHealthRevisionID: continuationGoldenPlanID(443), TaskHealthRevision: 1,
			ArtifactRevisionID: continuationGoldenPlanID(444), ArtifactRevision: 1,
			ReservationRevisionID: continuationGoldenPlanID(445), ReservationRevision: 1,
			MembershipRevisionID: continuationGoldenPlanID(446), MembershipRevision: 1,
		},
		Source: source, Groups: groups,
		Pool:    domain.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindGolden, Versions: versions},
		History: history, Candidates: candidates,
		ParticipantReservations: participants,
	})
	require.NoError(tb, err)

	groupCommands := make([]goldenplan.GroupCommand, len(groups))
	for groupIndex, group := range groups {
		groupCommands[groupIndex].GroupID = group.Revision.GroupID()
		groupCommands[groupIndex].GroupRevisionID = group.Revision.RevisionID()
		for edgeIndex := range groupCommands[groupIndex].EdgeIDs {
			base := 1200 + groupIndex*100 + edgeIndex*10
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = continuationGoldenPlanID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = continuationGoldenPlanID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = continuationGoldenPlanID(base + 3)
		}
	}
	command := goldenplan.Command{
		Scope: authority.Scope, PlanID: continuationGoldenPlanID(700), PlanRevisionID: continuationGoldenPlanID(701),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}
	return authority, command
}

func continuationGoldenPlanTask(value int) domain.Task {
	httpsURL := "https://tasks.example.test/golden"
	return domain.Task{
		ID: continuationGoldenPlanID(value), Title: "Golden challenge", Description: "Solve the Golden challenge.",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyMedium,
		TimeLimit: 300, Flag: "FLAG{golden}", Hints: []string{"Inspect the request."},
		TaskURL: &httpsURL,
	}
}

func continuationGoldenPlanID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + continuationGoldenPlanSuffix(value))
}

func continuationGoldenPlanRevisionID(value int) domain.DerivedRevisionID {
	return domain.DerivedRevisionID(continuationGoldenPlanID(value))
}

func continuationGoldenPlanSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func continuationGoldenStateFixture(t *testing.T, openedAt time.Time) goldenstate.GoldenState {
	t.Helper()

	authority, command := continuationGoldenPlanExact(t)
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
	attemptID := continuationGoldenWaveFixtureID(10)
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
	state, err := goldenstate.BuildGoldenState(goldenstate.GoldenState{
		Scope: goldenstate.GoldenStateScope{
			TournamentID: topology.TournamentID(), GroupID: topology.GroupID(),
			GroupRevisionID: topology.RevisionID(),
		},
		Topology: topology, ExactPlan: exactPlan, Group: group,
		Membership: goldenstate.GoldenMembershipRevision{
			RevisionID: continuationGoldenWaveFixtureID(22), Revision: 1,
		},
		RevisionID: continuationGoldenWaveFixtureID(30), Revision: 1,
		Windows: []goldenstate.GoldenReadyWindow{{
			ID: continuationGoldenWaveFixtureID(40), RevisionID: continuationGoldenWaveFixtureID(41), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second),
			State:               goldenstate.GoldenReadyWindowOpen,
			ReadinessRevisionID: continuationGoldenWaveFixtureID(42), ReadinessRevision: 1,
			PresenceRevisionID: continuationGoldenWaveFixtureID(43), PresenceRevision: 1,
			PresentParticipantIDs: participantIDs,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, state.Validate())
	return state
}
