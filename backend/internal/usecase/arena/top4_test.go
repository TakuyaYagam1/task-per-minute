package arena_test

import (
	"crypto/sha256"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestTop4Snapshot(t *testing.T) {
	t.Parallel()

	t.Run("creates one ordered revision after every impactful tie resolves", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, true)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		positions := task051GoldenPositions(
			t,
			fixture,
			[]uuid.UUID{fixture.participants[2], fixture.participants[0], fixture.participants[1]},
			3,
		)

		command := arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(702), RevisionNo: 1,
			Source:                final,
			CurrentTerminalSeries: task051ResultHeads(fixture.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(701), RevisionNo: 2,
				Positions: &positions, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		snapshot, err := arena.PlanTop4Snapshot(command)
		require.NoError(t, err)
		require.NoError(t, snapshot.Validate())

		participants := snapshot.Participants()
		require.Len(t, participants, 4)
		require.Equal(t, []uuid.UUID{
			fixture.participants[2], fixture.participants[0], fixture.participants[1], fixture.participants[3],
		}, []uuid.UUID{
			participants[0].ParticipantID, participants[1].ParticipantID,
			participants[2].ParticipantID, participants[3].ParticipantID,
		})
		for index, participant := range participants {
			require.Equal(t, index+1, participant.Seed)
		}
		require.Equal(t, domain.ArenaArtifactKindTopFour, snapshot.Projection().Revision().Artifact().Kind)
		require.Equal(t, 1, snapshot.Projection().Revision().RevisionNo())
		require.Nil(t, snapshot.Projection().Revision().PreviousRevisionID())
		require.NotEqual(t, [sha256.Size]byte{}, snapshot.Projection().Revision().PayloadDigest())

		direct := snapshot.Dependencies()
		require.Len(t, direct, 1)
		require.Equal(t, task051RevisionID(701), direct[0].SourceRevisionID)
		require.Equal(t, command.RevisionID, direct[0].DerivedRevisionID)
		qualification := snapshot.QualificationDependencies()
		require.ElementsMatch(t, []domain.ArenaRevisionDependency{
			{SourceRevisionID: fixture.command.RevisionID, DerivedRevisionID: task051RevisionID(701)},
			{SourceRevisionID: fixture.command.GoldenGroups[0].RevisionID, DerivedRevisionID: task051RevisionID(701)},
		}, qualification)
		require.Equal(t, task051RevisionID(701), snapshot.QualificationProjections()[0].Revision().ID())
		terminal := snapshot.TerminalSeriesReferences()
		require.Len(t, terminal, 6)
		for _, reference := range terminal {
			require.False(t, reference.ProjectionRevisionID.IsZero())
		}
		finalizedPrevious := snapshot.QualificationProjections()[0].Revision().PreviousRevisionID()
		require.NotNil(t, finalizedPrevious)
		require.Equal(t, fixture.command.GoldenGroups[0].RevisionID, *finalizedPrevious)

		participants[0].ParticipantID = uuid.Nil
		direct[0].SourceRevisionID = domain.ArenaDerivedRevisionID{}
		terminal[0].ProjectionRevisionID = domain.ArenaDerivedRevisionID{}
		require.Equal(t, fixture.participants[2], snapshot.Participants()[0].ParticipantID)
		require.False(t, snapshot.Dependencies()[0].SourceRevisionID.IsZero())
		require.False(t, snapshot.TerminalSeriesReferences()[0].ProjectionRevisionID.IsZero())
	})

	t.Run("uses final standings directly when Golden work never existed", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		snapshot, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(710), RevisionNo: 1,
			Source:                final,
			CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt:             fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		require.Len(t, snapshot.Participants(), 4)
		require.Equal(t, fixture.command.RevisionID, snapshot.Dependencies()[0].SourceRevisionID)
	})

	t.Run("retains terminal no-game result heads as bounded audit references", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		task051SetNoGameHead(
			t, &fixture.command.Rounds[0].Series[0], 10200,
			arena.NormalNoShowActionReopenWave, fixture.command.Rounds[0].LockProof.WaveID,
		)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		snapshot, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(10220), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		require.NoError(t, snapshot.Validate())
		references := snapshot.TerminalSeriesReferences()
		require.Contains(t, references, arena.Top4TerminalSeriesReference{
			SeriesID:                 fixture.command.Rounds[0].Series[0].Series.ID,
			OfficialResultRevisionID: fixture.command.Rounds[0].Series[0].OfficialResult.NoGame.Series.ID,
			ProjectionRevisionID:     fixture.command.Rounds[0].Series[0].Projection.Revision().ID(),
			ProjectionPayloadDigest:  fixture.command.Rounds[0].Series[0].Projection.Revision().PayloadDigest(),
		})
	})

	t.Run("requires and records the complete finalized Golden source set", func(t *testing.T) {
		t.Parallel()

		fixture := task051TwoTieFixture(t)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		first := task051GoldenPositionsForGroup(
			t, fixture, 0, []uuid.UUID{fixture.participants[1], fixture.participants[0]}, 2,
		)
		second := task051GoldenPositionsForGroup(
			t, fixture, 1, []uuid.UUID{fixture.participants[3], fixture.participants[2]}, 2,
		)
		snapshot, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(753), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{
				{RevisionID: task051RevisionID(751), RevisionNo: 2, Positions: &first, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute)},
				{RevisionID: task051RevisionID(752), RevisionNo: 2, Positions: &second, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute)},
			},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{
			fixture.participants[1], fixture.participants[0], fixture.participants[3], fixture.participants[2],
		}, []uuid.UUID{
			snapshot.Participants()[0].ParticipantID, snapshot.Participants()[1].ParticipantID,
			snapshot.Participants()[2].ParticipantID, snapshot.Participants()[3].ParticipantID,
		})
		require.Len(t, snapshot.Dependencies(), 2)
		require.Len(t, snapshot.QualificationDependencies(), 4)
		require.Len(t, snapshot.QualificationProjections(), 2)
	})

	t.Run("rejects unresolved stale duplicated and non-current evidence", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, true)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		positions := task051GoldenPositions(
			t,
			fixture,
			[]uuid.UUID{fixture.participants[0], fixture.participants[1], fixture.participants[2]},
			3,
		)

		base := arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(721), RevisionNo: 1,
			Source:                final,
			CurrentTerminalSeries: task051ResultHeads(fixture.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(720), RevisionNo: 2,
				Positions: &positions, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		tests := []struct {
			name   string
			mutate func(*arena.Top4SnapshotCommand)
		}{
			{name: "unresolved", mutate: func(command *arena.Top4SnapshotCommand) {
				command.GoldenSettlements = nil
			}},
			{name: "stale terminal head", mutate: func(command *arena.Top4SnapshotCommand) {
				command.CurrentTerminalSeries[0].Projection = command.CurrentTerminalSeries[1].Projection
			}},
			{name: "duplicated terminal head", mutate: func(command *arena.Top4SnapshotCommand) {
				command.CurrentTerminalSeries[0] = command.CurrentTerminalSeries[1]
			}},
			{name: "partial Golden ledger", mutate: func(command *arena.Top4SnapshotCommand) {
				partial := task051GoldenPositions(
					t,
					fixture,
					[]uuid.UUID{fixture.participants[0], fixture.participants[1], fixture.participants[2]},
					2,
				)
				command.GoldenSettlements[0].Positions = &partial
			}},
			{name: "seed topology used as final source", mutate: func(command *arena.Top4SnapshotCommand) {
				command.GoldenSettlements[0].RevisionID = fixture.command.GoldenGroups[0].RevisionID
			}},
			{name: "revision aliases tournament", mutate: func(command *arena.Top4SnapshotCommand) {
				command.RevisionID = domain.ArenaDerivedRevisionID(command.TournamentID)
			}},
			{name: "revision aliases terminal evidence", mutate: func(command *arena.Top4SnapshotCommand) {
				command.RevisionID = command.CurrentTerminalSeries[0].Projection.Revision().ID()
			}},
			{name: "finalized revision aliases settlement evidence", mutate: func(command *arena.Top4SnapshotCommand) {
				command.GoldenSettlements[0].RevisionID = domain.ArenaDerivedRevisionID(
					command.GoldenSettlements[0].Positions.RevisionID,
				)
			}},
			{name: "revision overflow", mutate: func(command *arena.Top4SnapshotCommand) {
				command.RevisionNo = math.MaxInt
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := base
				command.CurrentTerminalSeries = append([]arena.FinalSwissSeriesHead(nil), base.CurrentTerminalSeries...)
				command.GoldenSettlements = append([]arena.Top4GoldenSettlement(nil), base.GoldenSettlements...)
				for index := range command.GoldenSettlements {
					if base.GoldenSettlements[index].Positions != nil {
						positions := base.GoldenSettlements[index].Positions.Snapshot()
						command.GoldenSettlements[index].Positions = &positions
					}
				}
				test.mutate(&command)
				snapshot, planErr := arena.PlanTop4Snapshot(command)
				require.ErrorIs(t, planErr, arena.ErrInvalidTop4Snapshot)
				require.Equal(t, arena.Top4Snapshot{}, snapshot)
			})
		}
	})

	t.Run("accepts only a fully validated terminal Golden allocation state", func(t *testing.T) {
		fixture := task051SwissFixture(t, true)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		terminal := task051GoldenAllocationState(t, final, true)
		command := arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7690), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(7691), RevisionNo: 2,
				State: &terminal, FinalizedAt: terminal.Allocation.AllocatedAt,
			}},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		snapshot, err := arena.PlanTop4Snapshot(command)
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
				aliasCommand.RevisionID = domain.ArenaDerivedRevisionID(alias.id)
				aliased, planErr := arena.PlanTop4Snapshot(aliasCommand)
				require.Equal(t, arena.Top4Snapshot{}, aliased)
				require.ErrorIs(t, planErr, arena.ErrInvalidTop4Snapshot)
			})
		}

		terminal.Allocation.Positions[0].ParticipantID = uuid.Nil
		require.NotEqual(t, uuid.Nil, snapshot.Participants()[0].ParticipantID)
	})

	t.Run("rejects zero-participation Golden fallback without inventing Top 4 members", func(t *testing.T) {
		fixture := task051SwissFixture(t, true)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		terminal := task051GoldenAllocationState(t, final, false)
		require.Empty(t, terminal.Allocation.Positions)
		snapshot, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7692), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(7693), RevisionNo: 2,
				State: &terminal, FinalizedAt: terminal.Allocation.AllocatedAt,
			}},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		})
		require.Equal(t, arena.Top4Snapshot{}, snapshot)
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)
		require.ErrorIs(t, err, arena.ErrTop4GoldenZeroParticipation)
	})

	t.Run("rejects fabricated or stale allocation state lineage", func(t *testing.T) {
		fixture := task051SwissFixture(t, true)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		terminal := task051GoldenAllocationState(t, final, true)
		base := arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7700), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(7701), RevisionNo: 2,
				State: &terminal, FinalizedAt: terminal.Allocation.AllocatedAt,
			}},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		tests := []struct {
			name   string
			mutate func(*arena.GoldenState)
		}{
			{name: "allocation payload", mutate: func(state *arena.GoldenState) {
				state.Allocation.Positions[0].ParticipantID = fixture.participants[3]
			}},
			{name: "state digest", mutate: func(state *arena.GoldenState) {
				state.PayloadDigest = sha256.Sum256([]byte("forged"))
			}},
			{name: "allocation predecessor", mutate: func(state *arena.GoldenState) {
				state.Allocation.ExpectedState.RevisionID = task051ID(7702)
			}},
			{name: "terminal no-show", mutate: func(state *arena.GoldenState) { state.NoShows = nil }},
			{name: "topology lineage", mutate: func(state *arena.GoldenState) {
				state.Scope.GroupRevisionID = task051RevisionID(7703)
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := base
				command.CurrentTerminalSeries = task051ResultHeads(fixture.command)
				state := terminal.Snapshot()
				test.mutate(&state)
				command.GoldenSettlements = []arena.Top4GoldenSettlement{{
					RevisionID: task051RevisionID(7701), RevisionNo: 2,
					State: &state, FinalizedAt: terminal.Allocation.AllocatedAt,
				}}
				snapshot, planErr := arena.PlanTop4Snapshot(command)
				require.Equal(t, arena.Top4Snapshot{}, snapshot)
				require.ErrorIs(t, planErr, arena.ErrInvalidTop4Snapshot)
			})
		}
	})

	t.Run("exports a graph-valid predecessor edge for a Top 4 successor", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		first, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7710), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		second, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7711), RevisionNo: 2, Previous: &first,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		require.Contains(t, second.Dependencies(), domain.ArenaRevisionDependency{
			SourceRevisionID:  first.Projection().Revision().ID(),
			DerivedRevisionID: second.Projection().Revision().ID(),
		})
		graph, err := domain.NewArenaRevisionGraph(
			[]domain.ArenaProjectionRevision{final.Projection(), first.Projection(), second.Projection()},
			append(first.Dependencies(), second.Dependencies()...),
		)
		require.NoError(t, err)
		require.True(t, graph.DependsOn(second.Projection().Revision().ID(), first.Projection().Revision().ID()))
		for _, dependency := range second.Dependencies() {
			for _, reference := range second.TerminalSeriesReferences() {
				require.False(t, dependency.SourceRevisionID == reference.ProjectionRevisionID &&
					dependency.DerivedRevisionID == second.Projection().Revision().ID())
			}
		}
		gapCommand := arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7712), RevisionNo: 3, Previous: &first,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		gap, gapErr := arena.PlanTop4Snapshot(gapCommand)
		require.Equal(t, arena.Top4Snapshot{}, gap)
		require.ErrorIs(t, gapErr, arena.ErrInvalidTop4Snapshot)
	})

	t.Run("keeps a long Top 4 successor chain bounded and valid", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		current, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(13001), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		payloadLimit := len(current.Projection().Payload()) + 128
		for revisionNo := 2; revisionNo <= 48; revisionNo++ {
			command := arena.Top4SnapshotCommand{
				TournamentID: fixture.command.TournamentID,
				RevisionID:   task051RevisionID(13000 + revisionNo), RevisionNo: revisionNo,
				Previous: &current, Source: final,
				CurrentTerminalSeries: task051ResultHeads(fixture.command),
				CreatedAt:             fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute),
			}
			current, err = arena.PlanTop4Snapshot(command)
			require.NoError(t, err)
			require.NoError(t, current.Validate())
			require.LessOrEqual(t, len(current.Projection().Payload()), payloadLimit)
		}
	})

	t.Run("rejects transitive Top 4 and nested standings identity reuse", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		first, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(13101), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		second, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(13102), RevisionNo: 2, Previous: &first,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		aliased, aliasErr := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   first.Projection().Revision().ID(), RevisionNo: 3, Previous: &second,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		})
		require.Equal(t, arena.Top4Snapshot{}, aliased)
		require.ErrorIs(t, aliasErr, arena.ErrInvalidTop4Snapshot)

		firstFinal := final
		currentFinal := firstFinal
		for revisionNo := 2; revisionNo <= 4; revisionNo++ {
			command := fixture.command
			command.RevisionID = task051RevisionID(13200 + revisionNo)
			command.RevisionNo = revisionNo
			command.Previous = &currentFinal
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			currentFinal, err = arena.PlanFinalSwissProjection(command)
			require.NoError(t, err)
		}
		aliased, aliasErr = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   firstFinal.Projection().Revision().ID(), RevisionNo: 1,
			Source: currentFinal, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(5 * time.Minute),
		})
		require.Equal(t, arena.Top4Snapshot{}, aliased)
		require.ErrorIs(t, aliasErr, arena.ErrInvalidTop4Snapshot)

		tied := task051SwissFixture(t, true)
		tiedFirst, err := arena.PlanFinalSwissProjection(tied.command)
		require.NoError(t, err)
		tiedCurrent := tiedFirst
		for revisionNo := 2; revisionNo <= 4; revisionNo++ {
			command := tied.command
			command.RevisionID = task051RevisionID(13300 + revisionNo)
			for index := range command.GoldenGroups {
				command.GoldenGroups[index].GroupID = task051ID(13320 + revisionNo*10 + index*2)
				command.GoldenGroups[index].RevisionID =
					task051RevisionID(13321 + revisionNo*10 + index*2)
			}
			command.RevisionNo = revisionNo
			command.Previous = &tiedCurrent
			command.CreatedAt = tied.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			tiedCurrent, err = arena.PlanFinalSwissProjection(command)
			require.NoError(t, err)
			tied.command.GoldenGroups = command.GoldenGroups
		}
		positions := task051GoldenPositions(
			t, tied, []uuid.UUID{tied.participants[0], tied.participants[1], tied.participants[2]}, 3,
		)
		aliased, aliasErr = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   task051RevisionID(13310), RevisionNo: 1,
			Source: tiedCurrent, CurrentTerminalSeries: task051ResultHeads(tied.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: tiedFirst.Projection().Revision().ID(), RevisionNo: 2,
				Positions: &positions, FinalizedAt: tied.command.CreatedAt.Add(5 * time.Minute),
			}},
			CreatedAt: tied.command.CreatedAt.Add(6 * time.Minute),
		})
		require.Equal(t, arena.Top4Snapshot{}, aliased)
		require.ErrorIs(t, aliasErr, arena.ErrInvalidTop4Snapshot)
	})

	t.Run("rejects oversized authority before cloning nested evidence", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		heads := make([]arena.FinalSwissSeriesHead, domain.ArenaMaxParticipants*8)
		snapshot, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7720), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: heads,
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.Equal(t, arena.Top4Snapshot{}, snapshot)
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)

		heads = task051ResultHeads(fixture.command)
		heads[0].Series.Slots = []domain.ArenaGameSlot{{
			Attempts: make([]domain.ArenaGame, domain.ArenaMaxParticipants+1),
		}}
		snapshot, err = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(7728), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: heads,
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.Equal(t, arena.Top4Snapshot{}, snapshot)
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)

		tied := task051SwissFixture(t, true)
		tiedFinal, err := arena.PlanFinalSwissProjection(tied.command)
		require.NoError(t, err)
		positions := task051GoldenPositions(
			t, tied, []uuid.UUID{tied.participants[0], tied.participants[1], tied.participants[2]}, 3,
		)
		positions.Positions = make([]arena.GoldenCommittedPosition, domain.ArenaMaxParticipants+1)
		_, err = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   task051RevisionID(7721), RevisionNo: 1,
			Source: tiedFinal, CurrentTerminalSeries: task051ResultHeads(tied.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(7722), RevisionNo: 2,
				Positions: &positions, FinalizedAt: tied.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: tied.command.CreatedAt.Add(2 * time.Minute),
		})
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)

		state := task051GoldenAllocationState(t, tiedFinal, true)
		state.Windows = make([]arena.GoldenReadyWindow, domain.ArenaMaxParticipants+1)
		_, err = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   task051RevisionID(7723), RevisionNo: 1,
			Source: tiedFinal, CurrentTerminalSeries: task051ResultHeads(tied.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(7724), RevisionNo: 2,
				State: &state, FinalizedAt: tied.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: tied.command.CreatedAt.Add(2 * time.Minute),
		})
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)

		historyState := task051GoldenAllocationState(t, tiedFinal, true)
		historyState.ExactPlan.Authority.History = make(
			[]arena.ArenaTaskReceiptRef, domain.ArenaMaxParticipants*2+1,
		)
		_, err = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   task051RevisionID(7725), RevisionNo: 1,
			Source: tiedFinal, CurrentTerminalSeries: task051ResultHeads(tied.command),
			GoldenSettlements: []arena.Top4GoldenSettlement{{
				RevisionID: task051RevisionID(7726), RevisionNo: 2,
				State: &historyState, FinalizedAt: tied.command.CreatedAt.Add(time.Minute),
			}},
			CreatedAt: tied.command.CreatedAt.Add(2 * time.Minute),
		})
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)

		settlements := make([]arena.Top4GoldenSettlement, len(tiedFinal.GoldenGroups())+1)
		_, err = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   task051RevisionID(7727), RevisionNo: 1,
			Source: tiedFinal, CurrentTerminalSeries: task051ResultHeads(tied.command),
			GoldenSettlements: settlements,
			CreatedAt:         tied.command.CreatedAt.Add(2 * time.Minute),
		})
		require.ErrorIs(t, err, arena.ErrInvalidTop4Snapshot)
	})
}

func task051GoldenAllocationState(t *testing.T, final arena.FinalSwissProjection, participate bool) arena.GoldenState {
	t.Helper()
	groups := final.GoldenGroups()
	require.Len(t, groups, 1)
	group := groups[0]
	members := group.Revision.Members()
	active := make([]uuid.UUID, len(members))
	groupMembers := make([]domain.ArenaGoldenMember, len(members))
	for index, member := range members {
		active[index] = member.ParticipantID
		groupMembers[index] = domain.ArenaGoldenMember{ParticipantID: member.ParticipantID}
	}
	poolID := task051ID(7800)
	versions := make([]arena.TaskVersionRef, domain.ArenaAssignmentReserveCount+1)
	candidates := make([]arena.GoldenExactTaskVersion, len(versions))
	for index := range versions {
		task := goldenTask(8000 + index)
		versions[index] = arena.TaskVersionRef{TaskID: task.ID, Version: 2}
		candidates[index] = arena.GoldenExactTaskVersion{
			PoolRevisionID: poolID, Version: 2, Task: task,
			Health: arena.TaskVersionHealth{
				TaskID: task.ID, Version: 2, PoolRevisionID: poolID,
				PoolKind: domain.ArenaTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: arena.GoldenTaskArtifactDigest(task, 2),
		}
	}
	reservedAt := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	reservations := make([]arena.GoldenParticipantReservation, len(active))
	for index, participantID := range active {
		playerID := task051ID(7820 + index)
		reservations[index] = arena.GoldenParticipantReservation{
			ParticipantID: participantID, PlayerID: playerID,
			Reservation: domain.ParticipantReservation{
				PlayerID: playerID, ReservationID: task051ID(7840 + index),
				OwnerKind: domain.ParticipantReservationOwnerArena,
				OwnerID:   group.State.TournamentID, Revision: 1,
				AcquiredAt: reservedAt, UpdatedAt: reservedAt,
			},
		}
	}
	authority, err := arena.BuildGoldenExactPlanAuthority(arena.GoldenExactPlanAuthority{
		Scope: arena.GoldenExactPlanScope{TournamentID: group.State.TournamentID, PlanSetID: task051ID(7860)},
		Revisions: arena.GoldenExactPlanRevisions{
			SourceProjectionRevisionID: final.GoldenSource().RevisionID,
			GroupSetRevisionID:         task051ID(7861), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: task051ID(7862), HistoryRevision: 1,
			TaskHealthRevisionID: task051ID(7863), TaskHealthRevision: 1,
			ArtifactRevisionID: task051ID(7864), ArtifactRevision: 1,
			ReservationRevisionID: task051ID(7865), ReservationRevision: 1,
			MembershipRevisionID: task051ID(7866), MembershipRevision: 1,
		},
		Source:     final.GoldenSource(),
		Groups:     []arena.GoldenPlanGroupAuthority{{Revision: group.Revision, ActiveParticipantIDs: active}},
		Pool:       arena.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.ArenaTaskKindGolden, Versions: versions},
		Candidates: candidates, ParticipantReservations: reservations,
	})
	require.NoError(t, err)
	groupCommand := arena.GoldenExactGroupCommand{
		GroupID: group.State.ID, GroupRevisionID: group.State.RevisionID,
	}
	for index := range groupCommand.EdgeIDs {
		groupCommand.EdgeIDs[index] = task051ID(7880 + index*3)
		groupCommand.ReservationIDs[index] = task051ID(7881 + index*3)
		groupCommand.SnapshotIDs[index] = task051ID(7882 + index*3)
	}
	plan, err := arena.BuildGoldenExactPlan(arena.GoldenExactPlanCommand{
		Scope: authority.Scope, PlanID: task051ID(7900), PlanRevisionID: task051ID(7901),
		Expected: authority.Expectation(), GroupCommands: []arena.GoldenExactGroupCommand{groupCommand},
		CreatedAt: reservedAt.Add(time.Hour),
	}, authority)
	require.NoError(t, err)
	attemptID := task051ID(7910)
	openedAt := reservedAt.Add(time.Hour + time.Second)
	state, err := arena.BuildGoldenState(arena.GoldenState{
		Scope: arena.GoldenStateScope{
			TournamentID: group.State.TournamentID, GroupID: group.State.ID,
			GroupRevisionID: group.State.RevisionID,
		},
		Topology: group.Revision, ExactPlan: plan,
		Group: domain.ArenaGoldenGroupState{
			ID: group.State.ID, TournamentID: group.State.TournamentID,
			RevisionID:                 group.State.RevisionID,
			SourceProjectionRevisionID: group.State.SourceProjectionRevisionID,
			PositionFrom:               group.State.PositionFrom, PositionTo: group.State.PositionTo,
			Members: groupMembers,
			Attempts: []domain.ArenaGoldenAttempt{{
				ID: attemptID, GroupID: group.State.ID, GroupRevisionID: group.State.RevisionID,
				AttemptNo: 1, State: domain.ArenaGoldenAttemptStateWaitingReady,
				ParticipantIDs: active,
			}},
		},
		Membership: arena.GoldenMembershipRevision{RevisionID: task051ID(7911), Revision: 1},
		RevisionID: task051ID(7912), Revision: 1,
		Windows: []arena.GoldenReadyWindow{{
			ID: task051ID(7913), RevisionID: task051ID(7914), Revision: 1,
			AttemptID: attemptID, AttemptNo: 1, OpenedAt: openedAt,
			Deadline: openedAt.Add(30 * time.Second), State: arena.GoldenReadyWindowOpen,
			ReadinessRevisionID: task051ID(7915), ReadinessRevision: 1,
			PresenceRevisionID: task051ID(7916), PresenceRevision: 1,
			PresentParticipantIDs: active,
		}},
	})
	require.NoError(t, err)
	repository := &goldenStateRepositoryFake{state: state}
	ready := []uuid.UUID(nil)
	if participate {
		ready = []uuid.UUID{active[0]}
	}
	resolved := goldenReadyAndResolve(t, repository, state, ready, openedAt, 7920)
	allocated, changed, err := arena.NewGoldenFallbackUseCase(
		repository, fixedArenaClock{now: resolved.NoShows[len(resolved.NoShows)-1].ResolvedAt},
	).Allocate(t.Context(), arena.GoldenFallbackCommand{
		Scope: resolved.Scope, CommandID: task051ID(7950), AllocationID: task051ID(7951),
		ExpectedState: resolved.Expectation(), NextStateRevisionID: task051ID(7952),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, allocated.Validate())
	return *allocated
}

func task051GoldenPositions(
	t *testing.T,
	fixture task051Swiss,
	ordered []uuid.UUID,
	count int,
) arena.GoldenPositionLedger {
	t.Helper()

	return task051GoldenPositionsForGroup(t, fixture, 0, ordered, count)
}

func task051GoldenPositionsForGroup(
	t *testing.T,
	fixture task051Swiss,
	groupIndex int,
	ordered []uuid.UUID,
	count int,
) arena.GoldenPositionLedger {
	t.Helper()
	require.GreaterOrEqual(t, len(ordered), count)
	require.Less(t, groupIndex, len(fixture.command.GoldenGroups))
	group := fixture.command.GoldenGroups[groupIndex]
	idOffset := groupIndex * 100
	scope := arena.GoldenStateScope{
		TournamentID:    fixture.command.TournamentID,
		GroupID:         group.GroupID,
		GroupRevisionID: group.RevisionID,
	}
	attemptID := task051ID(730 + idOffset)
	submissionScope := arena.GoldenSubmissionScope{
		State:        scope,
		AttemptID:    attemptID,
		WaveID:       task051ID(731 + idOffset),
		AssignmentID: task051ID(732 + idOffset),
		SnapshotID:   task051ID(733 + idOffset),
		TaskID:       task051ID(734 + idOffset),
	}
	ordering := arena.GoldenAttemptOrderingEvidence{
		AttemptID: attemptID,
		AttemptNo: 1,
		SubmissionHead: arena.GoldenSubmissionLedgerExpectation{
			Scope:            submissionScope,
			RevisionID:       task051ID(735 + idOffset),
			Revision:         int64(count + 1),
			NextSubmissionID: uint64(count + 1),
			PayloadDigest:    sha256.Sum256([]byte("task051-submissions")),
		},
		Order: make([]arena.GoldenPositionOrderEntry, count),
	}
	positions := make([]arena.GoldenCommittedPosition, count)
	for index := range count {
		digest := sha256.Sum256([]byte{byte(index + 1)})
		ordering.Order[index] = arena.GoldenPositionOrderEntry{
			SubmissionID:   uint64(index + 1),
			ParticipantID:  ordered[index],
			CommittedAt:    fixture.command.CreatedAt.Add(time.Duration(index+1) * time.Second),
			EvidenceDigest: digest,
		}
		positions[index] = arena.GoldenCommittedPosition{
			Position:       group.PositionFrom + index,
			ParticipantID:  ordered[index],
			AttemptID:      attemptID,
			AttemptNo:      1,
			SubmissionID:   uint64(index + 1),
			EvidenceDigest: digest,
			CommitID:       task051ID(736 + idOffset),
		}
	}
	task049SealOrdering(t, &ordering)
	previousRevisionID := task051ID(737 + idOffset)
	ledger := arena.GoldenPositionLedger{
		Scope:              scope,
		RevisionID:         task051ID(738 + idOffset),
		Revision:           2,
		PreviousRevisionID: &previousRevisionID,
		RevisionIDs:        []uuid.UUID{previousRevisionID, task051ID(738 + idOffset)},
		PositionFrom:       group.PositionFrom,
		PositionTo:         group.PositionTo,
		Positions:          positions,
		Attempts:           []arena.GoldenAttemptOrderingEvidence{ordering},
	}
	task049SealPositionLedger(t, &ledger)
	require.NoError(t, ledger.Validate())
	return ledger
}
