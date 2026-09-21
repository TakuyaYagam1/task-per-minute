package playoff_test

import (
	"crypto/sha256"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestTop4Snapshot(t *testing.T) {
	t.Parallel()

	t.Run("orders every resolved Golden position and freezes evidence", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, true)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		positions := newGoldenPositionEvidence(
			t, fixture,
			[]uuid.UUID{fixture.participants[2], fixture.participants[0], fixture.participants[1]},
		)
		command := playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(702), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{{
				RevisionID: playoffRevisionID(701), RevisionNo: 2,
				Positions: &positions, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		snapshot, err := playoff.PlanTop4Snapshot(command)
		require.NoError(t, err)
		require.NoError(t, snapshot.Validate())

		participants := snapshot.Participants()
		require.Equal(t, []uuid.UUID{
			fixture.participants[2], fixture.participants[0],
			fixture.participants[1], fixture.participants[3],
		}, top4Participants(participants))
		for index, participant := range participants {
			require.Equal(t, index+1, participant.Seed)
		}
		require.Equal(t, domain.ArtifactKindTopFour, snapshot.Projection().Revision().Artifact().Kind)
		require.Equal(t, 1, snapshot.Projection().Revision().RevisionNo())
		require.Nil(t, snapshot.Projection().Revision().PreviousRevisionID())
		require.NotEqual(t, [sha256.Size]byte{}, snapshot.Projection().Revision().PayloadDigest())

		direct := snapshot.Dependencies()
		require.Equal(t, []domain.RevisionDependency{{
			SourceRevisionID: playoffRevisionID(701), DerivedRevisionID: command.RevisionID,
		}}, direct)
		require.ElementsMatch(t, []domain.RevisionDependency{
			{SourceRevisionID: fixture.command.RevisionID, DerivedRevisionID: playoffRevisionID(701)},
			{SourceRevisionID: fixture.command.GoldenGroups[0].RevisionID, DerivedRevisionID: playoffRevisionID(701)},
		}, snapshot.QualificationDependencies())
		require.Equal(t, playoffRevisionID(701), snapshot.QualificationProjections()[0].Revision().ID())
		require.Len(t, snapshot.TerminalSeriesReferences(), 6)
		for _, reference := range snapshot.TerminalSeriesReferences() {
			require.False(t, reference.ProjectionRevisionID.IsZero())
		}
		finalizedPrevious := snapshot.QualificationProjections()[0].Revision().PreviousRevisionID()
		require.NotNil(t, finalizedPrevious)
		require.Equal(t, fixture.command.GoldenGroups[0].RevisionID, *finalizedPrevious)

		participants[0].ParticipantID = uuid.Nil
		direct[0].SourceRevisionID = domain.DerivedRevisionID{}
		references := snapshot.TerminalSeriesReferences()
		references[0].ProjectionRevisionID = domain.DerivedRevisionID{}
		positionCopies := positions.Positions()
		positionCopies[0].ParticipantID = uuid.Nil
		require.Equal(t, fixture.participants[2], snapshot.Participants()[0].ParticipantID)
		require.False(t, snapshot.Dependencies()[0].SourceRevisionID.IsZero())
		require.False(t, snapshot.TerminalSeriesReferences()[0].ProjectionRevisionID.IsZero())
		require.NotEqual(t, uuid.Nil, positions.Positions()[0].ParticipantID)
		require.NoError(t, snapshot.Validate())
	})

	t.Run("uses final standings directly with the canonical projection digest", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		snapshot, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(710), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		require.NoError(t, snapshot.Validate())
		require.Equal(t, []uuid.UUID{
			fixture.participants[0], fixture.participants[1],
			fixture.participants[2], fixture.participants[3],
		}, top4Participants(snapshot.Participants()))
		require.Equal(t, fixture.command.RevisionID, snapshot.Dependencies()[0].SourceRevisionID)
		require.Equal(t,
			"f3ebe21b2dcebeb638f6cb6a0b860ab01f7a9569904de36481c36103eb075f67",
			projectionDigestHex(snapshot.Projection()),
		)
	})

	t.Run("retains terminal no-game heads as bounded audit references", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		fixture.command.Rounds[0].Series[0] = noGameTerminalEvidence(
			t, fixture.command.Rounds[0].Series[0], fixture.command.Rounds[0].LockProof.WaveID,
			10200, domain.NormalNoShowActionReopenWave,
		)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		snapshot, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(10220), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		require.NoError(t, snapshot.Validate())
		evidence := fixture.command.Rounds[0].Series[0]
		recorded := evidence.OfficialResult().NoGame
		require.NotNil(t, recorded)
		require.Contains(t, snapshot.TerminalSeriesReferences(), playoff.Top4TerminalSeriesReference{
			SeriesID:                 evidence.Series().ID,
			OfficialResultRevisionID: recorded.Series.ID,
			ProjectionRevisionID:     evidence.Projection().Revision().ID(),
			ProjectionPayloadDigest:  evidence.Projection().Revision().PayloadDigest(),
		})
	})

	t.Run("records the complete finalized Golden source set", func(t *testing.T) {
		t.Parallel()

		fixture := newTwoTieFixture(t)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		first := newGoldenPositionEvidenceForGroup(
			t, fixture, 0, []uuid.UUID{fixture.participants[1], fixture.participants[0]}, 2,
		)
		second := newGoldenPositionEvidenceForGroup(
			t, fixture, 1, []uuid.UUID{fixture.participants[3], fixture.participants[2]}, 2,
		)
		snapshot, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(753), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{
				{RevisionID: playoffRevisionID(751), RevisionNo: 2, Positions: &first, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute)},
				{RevisionID: playoffRevisionID(752), RevisionNo: 2, Positions: &second, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute)},
			},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{
			fixture.participants[1], fixture.participants[0],
			fixture.participants[3], fixture.participants[2],
		}, top4Participants(snapshot.Participants()))
		require.Len(t, snapshot.Dependencies(), 2)
		require.Len(t, snapshot.QualificationDependencies(), 4)
		require.Len(t, snapshot.QualificationProjections(), 2)
	})

	t.Run("rejects unresolved stale duplicated and non-current evidence", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, true)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		positions := newGoldenPositionEvidence(
			t, fixture,
			[]uuid.UUID{fixture.participants[0], fixture.participants[1], fixture.participants[2]},
		)
		base := playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(721), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{{
				RevisionID: playoffRevisionID(720), RevisionNo: 2,
				Positions: &positions, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		tests := []struct {
			name   string
			mutate func(*playoff.Top4SnapshotCommand)
		}{
			{name: "unresolved", mutate: func(command *playoff.Top4SnapshotCommand) {
				command.GoldenSettlements = nil
			}},
			{name: "duplicated terminal head", mutate: func(command *playoff.Top4SnapshotCommand) {
				command.CurrentTerminalSeries[0] = command.CurrentTerminalSeries[1]
			}},
			{name: "partial Golden ledger", mutate: func(command *playoff.Top4SnapshotCommand) {
				partial := newGoldenPositionEvidenceForGroup(
					t, fixture, 0,
					[]uuid.UUID{fixture.participants[0], fixture.participants[1], fixture.participants[2]}, 2,
				)
				command.GoldenSettlements[0].Positions = &partial
			}},
			{name: "seed topology used as final source", mutate: func(command *playoff.Top4SnapshotCommand) {
				command.GoldenSettlements[0].RevisionID = fixture.command.GoldenGroups[0].RevisionID
			}},
			{name: "revision aliases tournament", mutate: func(command *playoff.Top4SnapshotCommand) {
				command.RevisionID = domain.DerivedRevisionID(command.TournamentID)
			}},
			{name: "revision aliases terminal evidence", mutate: func(command *playoff.Top4SnapshotCommand) {
				command.RevisionID = command.CurrentTerminalSeries[0].Projection().Revision().ID()
			}},
			{name: "finalized revision aliases settlement evidence", mutate: func(command *playoff.Top4SnapshotCommand) {
				revisions := command.GoldenSettlements[0].Positions.RevisionIDs()
				command.GoldenSettlements[0].RevisionID = domain.DerivedRevisionID(revisions[len(revisions)-1])
			}},
			{name: "revision overflow", mutate: func(command *playoff.Top4SnapshotCommand) {
				command.RevisionNo = math.MaxInt
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneTop4Command(base)
				test.mutate(&command)
				snapshot, planErr := playoff.PlanTop4Snapshot(command)
				require.ErrorIs(t, planErr, playoff.ErrInvalidTop4Snapshot)
				require.Equal(t, playoff.Top4Snapshot{}, snapshot)
			})
		}
	})

	t.Run("accepts only a fully validated terminal Golden allocation state", func(t *testing.T) {
		fixture := newPlayoffFixture(t, true)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		terminal := newTop4GoldenAllocationState(t, finalSwiss, true)
		command := playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7690), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{{
				RevisionID: playoffRevisionID(7691), RevisionNo: 2,
				State: &terminal, FinalizedAt: terminal.Allocation.AllocatedAt,
			}},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		snapshot, err := playoff.PlanTop4Snapshot(command)
		require.NoError(t, err)
		require.NoError(t, snapshot.Validate())
		require.Equal(t, terminal.Allocation.Positions[0].ParticipantID, snapshot.Participants()[0].ParticipantID)

		for _, alias := range []struct {
			name string
			id   uuid.UUID
		}{
			{name: "expected state", id: terminal.Allocation.ExpectedState.RevisionID},
			{name: "plan history", id: terminal.ExactPlan.Expected.Revisions.HistoryRevisionID},
			{name: "membership predecessor", id: terminal.Allocation.ExpectedState.Membership.RevisionID},
		} {
			t.Run("revision aliases "+alias.name, func(t *testing.T) {
				aliasCommand := command
				aliasCommand.RevisionID = domain.DerivedRevisionID(alias.id)
				aliased, planErr := playoff.PlanTop4Snapshot(aliasCommand)
				require.Equal(t, playoff.Top4Snapshot{}, aliased)
				require.ErrorIs(t, planErr, playoff.ErrInvalidTop4Snapshot)
			})
		}

		terminal.Allocation.Positions[0].ParticipantID = uuid.Nil
		require.NotEqual(t, uuid.Nil, snapshot.Participants()[0].ParticipantID)
	})

	t.Run("rejects zero-participation Golden fallback", func(t *testing.T) {
		fixture := newPlayoffFixture(t, true)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		terminal := newTop4GoldenAllocationState(t, finalSwiss, false)
		require.Empty(t, terminal.Allocation.Positions)
		snapshot, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7692), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{{
				RevisionID: playoffRevisionID(7693), RevisionNo: 2,
				State: &terminal, FinalizedAt: terminal.Allocation.AllocatedAt,
			}},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		})
		require.Equal(t, playoff.Top4Snapshot{}, snapshot)
		require.ErrorIs(t, err, playoff.ErrInvalidTop4Snapshot)
		require.ErrorIs(t, err, playoff.ErrTop4GoldenZeroParticipation)
	})

	t.Run("rejects fabricated or stale allocation state lineage", func(t *testing.T) {
		fixture := newPlayoffFixture(t, true)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		terminal := newTop4GoldenAllocationState(t, finalSwiss, true)
		base := playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7700), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{{
				RevisionID: playoffRevisionID(7701), RevisionNo: 2,
				State: &terminal, FinalizedAt: terminal.Allocation.AllocatedAt,
			}},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		tests := []struct {
			name   string
			mutate func(*goldenstate.GoldenState)
		}{
			{name: "allocation payload", mutate: func(state *goldenstate.GoldenState) {
				state.Allocation.Positions[0].ParticipantID = fixture.participants[3]
			}},
			{name: "state digest", mutate: func(state *goldenstate.GoldenState) {
				state.PayloadDigest = sha256.Sum256([]byte("forged"))
			}},
			{name: "allocation predecessor", mutate: func(state *goldenstate.GoldenState) {
				state.Allocation.ExpectedState.RevisionID = playoffID(7702)
			}},
			{name: "terminal no-show", mutate: func(state *goldenstate.GoldenState) {
				state.NoShows = nil
			}},
			{name: "topology lineage", mutate: func(state *goldenstate.GoldenState) {
				state.Scope.GroupRevisionID = playoffRevisionID(7703)
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := cloneTop4Command(base)
				state := terminal.Snapshot()
				test.mutate(&state)
				command.GoldenSettlements[0].State = &state
				snapshot, planErr := playoff.PlanTop4Snapshot(command)
				require.Equal(t, playoff.Top4Snapshot{}, snapshot)
				require.ErrorIs(t, planErr, playoff.ErrInvalidTop4Snapshot)
			})
		}
	})

	testTop4SnapshotLineage(t)
}

func cloneTop4Command(command playoff.Top4SnapshotCommand) playoff.Top4SnapshotCommand {
	command.CurrentTerminalSeries = append([]playoff.TerminalSeriesEvidence(nil), command.CurrentTerminalSeries...)
	command.GoldenSettlements = append([]playoff.Top4GoldenSettlement(nil), command.GoldenSettlements...)
	for index := range command.GoldenSettlements {
		if command.GoldenSettlements[index].Positions != nil {
			positions := command.GoldenSettlements[index].Positions.Snapshot()
			command.GoldenSettlements[index].Positions = &positions
		}
		if command.GoldenSettlements[index].State != nil {
			state := command.GoldenSettlements[index].State.Snapshot()
			command.GoldenSettlements[index].State = &state
		}
	}
	return command
}

func top4Participants(input []playoff.Top4Participant) []uuid.UUID {
	participants := make([]uuid.UUID, len(input))
	for index, participant := range input {
		participants[index] = participant.ParticipantID
	}
	return participants
}
