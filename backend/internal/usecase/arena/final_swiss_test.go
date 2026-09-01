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

func TestFinalSwissProjection(t *testing.T) {
	t.Parallel()

	t.Run("commits final formula ties and creates only impactful Golden groups", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, true)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.False(t, projection.AdvanceDirectly())

		standings := projection.Standings()
		require.Len(t, standings, 4)
		for index, standing := range standings {
			require.Equal(t, index+1, standing.Position)
			require.Equal(t, arena.SwissPointsFinal, standing.PointsLabel)
			require.Equal(t, arena.SwissBuchholzFinal, standing.BuchholzStatus)
		}
		require.Equal(t, []uuid.UUID{fixture.participants[2], fixture.participants[1], fixture.participants[0]}, []uuid.UUID{
			standings[0].ParticipantID, standings[1].ParticipantID, standings[2].ParticipantID,
		})
		require.Equal(t, []int{2, 2, 2, 0}, []int{
			standings[0].Points, standings[1].Points, standings[2].Points, standings[3].Points,
		})
		require.Equal(t, []int{4, 4, 4, 6}, []int{
			standings[0].Buchholz, standings[1].Buchholz, standings[2].Buchholz, standings[3].Buchholz,
		})

		ties := projection.TieGroups()
		require.Len(t, ties, 1)
		require.Equal(t, 1, ties[0].PositionFrom)
		require.Equal(t, 3, ties[0].PositionTo)
		require.True(t, ties[0].Impactful)
		require.Equal(t, []uuid.UUID{fixture.participants[0], fixture.participants[1], fixture.participants[2]}, ties[0].ParticipantIDs)

		groups := projection.GoldenGroups()
		require.Len(t, groups, 1)
		require.NoError(t, groups[0].Revision.Validate())
		require.Equal(t, arena.GoldenGroupRevisionPurposeSeedOnly, groups[0].Revision.Purpose())
		state := groups[0].State
		require.Equal(t, fixture.command.GoldenGroups[0].GroupID, state.ID)
		require.Equal(t, fixture.command.RevisionID, state.SourceProjectionRevisionID)
		require.Equal(t, 1, state.PositionFrom)
		require.Equal(t, 3, state.PositionTo)
		require.False(t, state.ParticipationEstablished)
		require.Empty(t, state.Attempts)
		require.Len(t, state.Members, 3)
		require.Equal(t, domain.ArenaArtifactKindGoldenGroup, groups[0].Projection.Revision().Artifact().Kind)
		require.Equal(t, domain.ArenaRevisionDependency{
			SourceRevisionID:  fixture.command.RevisionID,
			DerivedRevisionID: fixture.command.GoldenGroups[0].RevisionID,
		}, groups[0].Dependency)

		dependencies := projection.TerminalSeriesDependencies()
		require.Len(t, dependencies, 6)
		for _, dependency := range dependencies {
			require.Equal(t, fixture.command.RevisionID, dependency.DerivedRevisionID)
		}
		require.Equal(t, domain.ArenaArtifactKindStandings, projection.Projection().Revision().Artifact().Kind)
		require.Equal(t, fixture.command.CreatedAt, projection.Projection().Revision().CreatedAt())
		require.NotEqual(t, [sha256.Size]byte{}, projection.Projection().Revision().PayloadDigest())

		standings[0].ParticipantID = uuid.Nil
		ties[0].ParticipantIDs[0] = uuid.Nil
		groups[0].State.Members[0].ParticipantID = uuid.Nil
		dependencies[0].SourceRevisionID = domain.ArenaDerivedRevisionID{}
		fixture.command.Rounds[0].LockProof.Series[0].PairingID = uuid.Nil
		require.Equal(t, fixture.participants[2], projection.Standings()[0].ParticipantID)
		require.Equal(t, fixture.participants[0], projection.TieGroups()[0].ParticipantIDs[0])
		require.Equal(t, fixture.participants[0], projection.GoldenGroups()[0].State.Members[0].ParticipantID)
		require.False(t, projection.TerminalSeriesDependencies()[0].SourceRevisionID.IsZero())
		require.NoError(t, projection.Validate())
	})

	t.Run("advances directly after the declared formula resolves every impactful position", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, false)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.True(t, projection.AdvanceDirectly())
		require.Empty(t, projection.TieGroups())
		require.Empty(t, projection.GoldenGroups())
		standings := projection.Standings()
		require.Equal(t, []uuid.UUID{
			fixture.participants[0], fixture.participants[1], fixture.participants[2], fixture.participants[3],
		}, []uuid.UUID{
			standings[0].ParticipantID, standings[1].ParticipantID,
			standings[2].ParticipantID, standings[3].ParticipantID,
		})
	})

	t.Run("records a maximal tie below Top 4 without creating Golden work", func(t *testing.T) {
		t.Parallel()

		fixture := task051LowerTieFixture(t)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		// Three six-player rounds fix the total point mass, so this realizable
		// schedule also has an upper tie. The maximal 5-6 group remains separate.
		require.False(t, projection.AdvanceDirectly())
		require.Len(t, projection.GoldenGroups(), 1)
		ties := projection.TieGroups()
		require.Len(t, ties, 2)
		require.Equal(t, 5, ties[1].PositionFrom)
		require.Equal(t, 6, ties[1].PositionTo)
		require.False(t, ties[1].Impactful)
	})

	t.Run("creates Golden work for a tie spanning positions four and five", func(t *testing.T) {
		t.Parallel()

		fixture := task051CutoffTieFixture(t)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.False(t, projection.AdvanceDirectly())
		var cutoffTie *arena.FinalSwissTieGroup
		for _, tie := range projection.TieGroups() {
			if tie.PositionFrom == 4 && tie.PositionTo == 5 {
				value := tie
				cutoffTie = &value
			}
		}
		require.NotNil(t, cutoffTie)
		require.True(t, cutoffTie.Impactful)
		var cutoffGroup *arena.FinalSwissGoldenGroup
		for _, group := range projection.GoldenGroups() {
			if group.State.PositionFrom == 4 && group.State.PositionTo == 5 {
				value := group
				cutoffGroup = &value
			}
		}
		require.NotNil(t, cutoffGroup)
	})

	t.Run("records the complete maximal set when two impactful tie groups exist", func(t *testing.T) {
		t.Parallel()

		fixture := task051TwoTieFixture(t)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.False(t, projection.AdvanceDirectly())
		ties := projection.TieGroups()
		require.Len(t, ties, 2)
		require.Equal(t, []int{1, 3}, []int{ties[0].PositionFrom, ties[1].PositionFrom})
		require.Equal(t, []int{2, 4}, []int{ties[0].PositionTo, ties[1].PositionTo})
		require.Len(t, projection.GoldenGroups(), 2)
	})

	t.Run("fails closed on incomplete stale aliased and overflowing authority", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.FinalSwissProjectionCommand)
		}{
			{name: "incomplete rounds", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds = command.Rounds[:2]
			}},
			{name: "spliced round lock proof", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].LockProof.Series[0].PairingID = uuid.Nil
			}},
			{name: "round revision differs from locked plan", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].RevisionID = task051ID(898)
			}},
			{name: "non-terminal Series", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Series.State = domain.ArenaSeriesStateActive
			}},
			{name: "stale result projection", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Series.CurrentResultRevisionID = task051OfficialPointer(900)
			}},
			{name: "spliced typed winner proof", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.Result.Outcome.WinnerID = task051UUIDPointer(
					command.Rounds[0].Series[0].Result.SecondParticipantID,
				)
			}},
			{name: "spliced typed score proof", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.Score.Score = domain.ArenaSeriesScore{
					SecondParticipantWins: 1,
				}
			}},
			{name: "forged point result", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Result.WinnerID = task051UUIDPointer(
					command.Rounds[0].Series[0].Result.SecondParticipantID,
				)
			}},
			{name: "incomplete result coverage", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series = command.Rounds[0].Series[:1]
			}},
			{name: "cross-role identity alias", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.GoldenGroups[0].GroupID = command.TournamentID
			}},
			{name: "projection identity alias", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.ProjectionID = command.ParticipantIDs[0]
			}},
			{name: "non-UTC clock", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.CreatedAt = command.CreatedAt.In(time.FixedZone("local", 60))
			}},
			{name: "duration overflow", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Result.FirstEffectiveTime = time.Duration(1<<63 - 1)
				command.Rounds[1].Series[0].Result.FirstEffectiveTime = time.Second
			}},
			{name: "revision overflow", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.RevisionNo = math.MaxInt
			}},
			{name: "extra Golden group identity", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.GoldenGroups = append(command.GoldenGroups, arena.FinalSwissGoldenGroupIdentity{
					PositionFrom: 2, PositionTo: 3,
					GroupID: task051ID(901), RevisionID: task051RevisionID(902),
				})
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := task051SwissFixture(t, true)
				test.mutate(&fixture.command)
				projection, err := arena.PlanFinalSwissProjection(fixture.command)
				require.ErrorIs(t, err, arena.ErrInvalidFinalSwissProjection)
				require.Equal(t, arena.FinalSwissProjection{}, projection)
			})
		}
	})

	t.Run("accepts exact terminal no-game evidence and freezes its audit fields", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		task051SetNoGameHead(
			t, &fixture.command.Rounds[0].Series[0], 8100,
			arena.NormalNoShowActionReopenWave, fixture.command.Rounds[0].LockProof.WaveID,
		)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.Equal(t, fixture.command.Rounds[0].LockProof.WaveID,
			fixture.command.Rounds[0].Series[0].OfficialResult.NoGame.Scope.WaveID)
		require.Equal(t, arena.SwissSeriesResultNoShow, fixture.command.Rounds[0].Series[0].Result.Label)
		require.Contains(t, projection.TerminalSeriesDependencies(), domain.ArenaRevisionDependency{
			SourceRevisionID:  fixture.command.Rounds[0].Series[0].OfficialResult.NoGame.ResultProjection.Revision().ID(),
			DerivedRevisionID: projection.Projection().Revision().ID(),
		})
		wantDigest := projection.Projection().Revision().PayloadDigest()
		fixture.command.Rounds[0].Series[0].OfficialResult.NoGame.Topology[0].SlotID = uuid.Nil
		fixture.command.Rounds[0].Series[0].Result.FirstEffectiveTime = time.Hour
		require.NoError(t, projection.Validate())
		require.Equal(t, wantDigest, projection.Projection().Revision().PayloadDigest())
	})

	t.Run("accepts terminal no-game void evidence for a cancelled Series", func(t *testing.T) {
		fixture := task051TwoTieFixture(t)
		task051SetNoGameHead(
			t, &fixture.command.Rounds[0].Series[0], 8200,
			arena.NormalNoShowActionPauseWave, fixture.command.Rounds[0].LockProof.WaveID,
		)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.Equal(t, arena.SwissSeriesResultVoid, fixture.command.Rounds[0].Series[0].Result.Label)
	})

	t.Run("rejects spliced no-game labels timing topology and current heads", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*arena.FinalSwissProjectionCommand)
		}{
			{name: "ordinary result mixed in", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.Result = command.Rounds[0].Series[1].OfficialResult.Result
			}},
			{name: "head projection", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Projection = command.Rounds[0].Series[1].Projection
			}},
			{name: "current result", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Series.CurrentResultRevisionID = task051OfficialPointer(8390)
			}},
			{name: "current score", mutate: func(command *arena.FinalSwissProjectionCommand) {
				scoreID := domain.ArenaSeriesScoreRevisionID(task051ID(8391))
				command.Rounds[0].Series[0].Series.CurrentScoreRevisionID = &scoreID
			}},
			{name: "played label", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Result.Label = arena.SwissSeriesResultPlayed
			}},
			{name: "effective time", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].Result.SecondEffectiveTime = time.Nanosecond
			}},
			{name: "accepted solve time", mutate: func(command *arena.FinalSwissProjectionCommand) {
				value := time.Duration(0)
				command.Rounds[0].Series[0].Result.FirstAcceptedSolveTime = &value
			}},
			{name: "topology", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.Topology[0].SlotPosition++
			}},
			{name: "dependency", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.ResultDependency.SourceRevisionID =
					task051RevisionID(8392)
			}},
			{name: "resolved after standings", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.ResolvedAt = command.CreatedAt.Add(time.Nanosecond)
			}},
			{name: "score recorded time", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.Score.RecordedAt =
					command.Rounds[0].Series[0].OfficialResult.NoGame.ResolvedAt.Add(-time.Nanosecond)
			}},
			{name: "result recorded time", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.Series.RecordedAt =
					command.Rounds[0].Series[0].OfficialResult.NoGame.ResolvedAt.Add(-time.Nanosecond)
			}},
			{name: "foreign round Wave", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.Scope.WaveID = task051ID(8393)
			}},
			{name: "deep Game alias", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.RevisionID = domain.ArenaDerivedRevisionID(
					command.Rounds[0].Series[0].OfficialResult.NoGame.Topology[0].GameID,
				)
			}},
			{name: "source predecessor alias", mutate: func(command *arena.FinalSwissProjectionCommand) {
				previous := command.Rounds[0].Series[0].OfficialResult.NoGame.ResultProjection.Revision().PreviousRevisionID()
				require.NotNil(t, previous)
				command.RevisionID = *previous
			}},
			{name: "official result predecessor alias", mutate: func(command *arena.FinalSwissProjectionCommand) {
				previous := command.Rounds[0].Series[0].OfficialResult.NoGame.Series.PreviousRevisionID
				require.NotNil(t, previous)
				command.RevisionID = domain.ArenaDerivedRevisionID(*previous)
			}},
			{name: "score predecessor alias", mutate: func(command *arena.FinalSwissProjectionCommand) {
				previous := command.Rounds[0].Series[0].OfficialResult.NoGame.Score.PreviousRevisionID
				require.NotNil(t, previous)
				command.RevisionID = domain.ArenaDerivedRevisionID(*previous)
			}},
			{name: "oversized no-game topology", mutate: func(command *arena.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0].OfficialResult.NoGame.Topology =
					make([]arena.RecordedNoGameAttempt, domain.ArenaMaxParticipants+1)
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				local := task051SwissFixture(t, false)
				task051SetNoGameHead(
					t, &local.command.Rounds[0].Series[0], 8300,
					arena.NormalNoShowActionReopenWave, local.command.Rounds[0].LockProof.WaveID,
				)
				command := local.command
				test.mutate(&command)
				projection, planErr := arena.PlanFinalSwissProjection(command)
				require.Equal(t, arena.FinalSwissProjection{}, projection)
				require.ErrorIs(t, planErr, arena.ErrInvalidFinalSwissProjection)
			})
		}
	})

	t.Run("exports a graph-valid predecessor edge for a standings successor", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		first, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		secondCommand := fixture.command
		secondCommand.RevisionID = task051RevisionID(8400)
		secondCommand.RevisionNo = 2
		secondCommand.Previous = &first
		secondCommand.CreatedAt = fixture.command.CreatedAt.Add(time.Minute)
		second, err := arena.PlanFinalSwissProjection(secondCommand)
		require.NoError(t, err)
		predecessor := domain.ArenaRevisionDependency{
			SourceRevisionID:  first.Projection().Revision().ID(),
			DerivedRevisionID: second.Projection().Revision().ID(),
		}
		require.Contains(t, second.Dependencies(), predecessor)
		projections := make([]domain.ArenaProjectionRevision, 0, 2+len(task051ResultHeads(fixture.command)))
		projections = append(projections, first.Projection(), second.Projection())
		for _, head := range task051ResultHeads(fixture.command) {
			projections = append(projections, head.Projection)
		}
		dependencies := append(first.Dependencies(), second.Dependencies()...)
		graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		require.True(t, graph.DependsOn(second.Projection().Revision().ID(), first.Projection().Revision().ID()))

		invalid := secondCommand
		invalid.RevisionID = first.Projection().Revision().ID()
		invalidProjection, invalidErr := arena.PlanFinalSwissProjection(invalid)
		require.Equal(t, arena.FinalSwissProjection{}, invalidProjection)
		require.ErrorIs(t, invalidErr, arena.ErrInvalidFinalSwissProjection)

		gap := secondCommand
		gap.RevisionID = task051RevisionID(8401)
		gap.RevisionNo = 3
		gapProjection, gapErr := arena.PlanFinalSwissProjection(gap)
		require.Equal(t, arena.FinalSwissProjection{}, gapProjection)
		require.ErrorIs(t, gapErr, arena.ErrInvalidFinalSwissProjection)
	})

	t.Run("keeps a long standings successor chain bounded and valid", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		current, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		payloadLimit := len(current.Projection().Payload()) + 128
		for revisionNo := 2; revisionNo <= 48; revisionNo++ {
			command := fixture.command
			command.RevisionID = task051RevisionID(12000 + revisionNo)
			command.RevisionNo = revisionNo
			command.Previous = &current
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			current, err = arena.PlanFinalSwissProjection(command)
			require.NoError(t, err)
			require.NoError(t, current.Validate())
			require.LessOrEqual(t, len(current.Projection().Payload()), payloadLimit)
		}
	})

	t.Run("rejects revision four reusing the retained revision one identity", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		first, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		current := first
		for revisionNo := 2; revisionNo <= 3; revisionNo++ {
			command := fixture.command
			command.RevisionID = task051RevisionID(12100 + revisionNo)
			command.RevisionNo = revisionNo
			command.Previous = &current
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			current, err = arena.PlanFinalSwissProjection(command)
			require.NoError(t, err)
		}
		alias := fixture.command
		alias.RevisionID = first.Projection().Revision().ID()
		alias.RevisionNo = 4
		alias.Previous = &current
		alias.CreatedAt = fixture.command.CreatedAt.Add(4 * time.Minute)
		projection, err := arena.PlanFinalSwissProjection(alias)
		require.Equal(t, arena.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, arena.ErrInvalidFinalSwissProjection)
	})

	t.Run("finalizes the full 16-player and odd-roster bye schedules", func(t *testing.T) {
		for _, size := range []int{5, 16} {
			fixture := task051SizedSwissFixture(t, size, 8500+size*100)
			projection, err := arena.PlanFinalSwissProjection(fixture.command)
			require.NoError(t, err)
			require.NoError(t, projection.Validate())
			require.Len(t, projection.Standings(), size)
			if size%2 == 1 {
				for _, round := range fixture.command.Rounds {
					require.NotNil(t, round.Bye)
				}
				digest := projection.Projection().Revision().PayloadDigest()
				fixture.command.Rounds[0].Bye.ParticipantID = uuid.Nil
				fixture.command.Rounds[0].Bye.RevisionID = uuid.Nil
				require.NoError(t, projection.Validate())
				require.Equal(t, digest, projection.Projection().Revision().PayloadDigest())
			}
		}
	})

	t.Run("rejects a bye spliced to another round participant", func(t *testing.T) {
		fixture := task051SizedSwissFixture(t, 5, 11800)
		fixture.command.Rounds[0].Bye.ParticipantID = fixture.command.Rounds[0].Series[0].Series.FirstParticipantID
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, arena.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, arena.ErrInvalidFinalSwissProjection)
	})

	t.Run("rejects oversized round Series authority before canonical cloning", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		fixture.command.Rounds[0].Series = make([]arena.FinalSwissSeriesHead, domain.ArenaMaxParticipants+1)
		projection, err := arena.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, arena.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, arena.ErrInvalidFinalSwissProjection)

		fixture = task051SwissFixture(t, false)
		fixture.command.Rounds[0].Series[0].Series.Slots = []domain.ArenaGameSlot{{
			Attempts: make([]domain.ArenaGame, domain.ArenaMaxParticipants+1),
		}}
		projection, err = arena.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, arena.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, arena.ErrInvalidFinalSwissProjection)
	})
}

func task051SetNoGameHead(
	t *testing.T,
	head *arena.FinalSwissSeriesHead,
	base int,
	action arena.NormalNoShowAction,
	waveID uuid.UUID,
) {
	t.Helper()
	resolvedAt := head.Projection.Revision().CreatedAt()
	seriesID := head.Series.ID
	tournamentID := head.Series.TournamentID
	first := head.Series.FirstParticipantID
	second := head.Series.SecondParticipantID
	gameID := task051ID(base + 1)
	slotID := task051ID(base + 2)
	gameResultID := task051OfficialID(base + 3)
	scoreID := domain.ArenaSeriesScoreRevisionID(task051ID(base + 4))
	resultID := task051OfficialID(base + 5)
	previousScoreID := domain.ArenaSeriesScoreRevisionID(task051ID(base + 6))
	previousResultID := task051OfficialID(base + 7)
	gameProjection, err := domain.NewArenaProjectionRevision(
		task051RevisionID(base+8), tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGameResult, EntityID: gameID},
		1, nil, resolvedAt.Add(-3*time.Second), []byte("task051-no-game"),
	)
	require.NoError(t, err)
	scoreProjectionPrevious := task051RevisionID(base + 9)
	scoreProjection, err := domain.NewArenaProjectionRevision(
		task051RevisionID(base+10), tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesScore, EntityID: seriesID},
		2, &scoreProjectionPrevious, resolvedAt.Add(-2*time.Second), []byte("task051-no-game-score"),
	)
	require.NoError(t, err)
	resultProjectionPrevious := task051RevisionID(base + 11)
	resultProjection, err := domain.NewArenaProjectionRevision(
		task051RevisionID(base+12), tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: seriesID},
		2, &resultProjectionPrevious, resolvedAt.Add(-time.Second), []byte("task051-no-game-result"),
	)
	require.NoError(t, err)
	score := domain.ArenaSeriesScore{}
	state := domain.ArenaSeriesStateCancelled
	var winner *uuid.UUID
	var ready *uuid.UUID
	label := arena.SwissSeriesResultVoid
	if action == arena.NormalNoShowActionReopenWave {
		score.FirstParticipantWins = 1
		state = domain.ArenaSeriesStateCompleted
		winner = task051UUIDPointer(first)
		ready = task051UUIDPointer(first)
		label = arena.SwissSeriesResultNoShow
	}
	recorded := arena.RecordedNoGameResult{
		Scope: arena.NormalNoShowScope{
			TournamentID: tournamentID, WaveID: waveID,
			WindowID: task051ID(base + 14), SeriesID: seriesID,
		},
		CommandID: task051ID(base + 15), Action: action, Format: domain.ArenaSeriesFormatBO1,
		FirstParticipantID: first, SecondParticipantID: second, ReadyParticipantID: ready,
		GameResults: []arena.NormalNoShowGameRevision{{
			Ordinal: 1, ID: gameResultID, GameID: gameID,
			State: domain.ArenaGameStateCancelled, Reason: domain.ArenaGameResultReasonSeriesCancelled,
			RecordedAt: resolvedAt,
		}},
		Topology: []arena.RecordedNoGameAttempt{{
			SeriesID: seriesID, SlotID: slotID, SlotPosition: 1,
			GameID: gameID, AttemptNo: 1, ResultRevisionID: gameResultID,
		}},
		Score: arena.NormalNoShowScoreRevision{
			Ordinal: 2, ID: scoreID, SeriesID: seriesID, PreviousRevisionID: &previousScoreID,
			Score: score, GameResultRevisionIDs: []domain.ArenaOfficialResultRevisionID{gameResultID},
			RecordedAt: resolvedAt,
		},
		Series: arena.NormalNoShowSeriesRevision{
			Ordinal: 3, ID: resultID, SeriesID: seriesID, PreviousRevisionID: &previousResultID,
			State: state, WinnerID: winner, ScoreRevisionID: scoreID, RecordedAt: resolvedAt,
		},
		GameSourceRevisions: []domain.ArenaDerivedRevision{gameProjection.Revision()},
		ScoreSourceRevision: scoreProjection.Revision(), ResultSourceRevision: resultProjection.Revision(),
		GameProjections: []domain.ArenaProjectionRevision{gameProjection},
		GameDependencies: []domain.ArenaRevisionDependency{{
			SourceRevisionID:  gameProjection.Revision().ID(),
			DerivedRevisionID: scoreProjection.Revision().ID(),
		}},
		ScoreProjection: scoreProjection, ResultProjection: resultProjection,
		ResultDependency: domain.ArenaRevisionDependency{
			SourceRevisionID:  scoreProjection.Revision().ID(),
			DerivedRevisionID: resultProjection.Revision().ID(),
		},
		ResolvedAt: resolvedAt,
	}
	head.Series.State = state
	head.Series.Score = score
	head.Series.WinnerID = winner
	head.Series.Slots = []domain.ArenaGameSlot{{
		ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
		Attempts: []domain.ArenaGame{{
			ID: gameID, SlotID: slotID, AttemptNo: 1,
			State: domain.ArenaGameStateCancelled, ResultReason: domain.ArenaGameResultReasonSeriesCancelled,
			ResultRevisionID: &gameResultID,
		}},
	}}
	head.Series.CurrentScoreRevisionID = &scoreID
	head.Series.CurrentResultRevisionID = &resultID
	head.OfficialResult = arena.OfficialResultProjectionInput{NoGame: &recorded}
	head.Projection = resultProjection
	head.Result.ResultRevisionID = resultID
	head.Result.WinnerID = winner
	head.Result.Label = label
	head.Result.FirstEffectiveTime = 0
	head.Result.SecondEffectiveTime = 0
	head.Result.FirstAcceptedSolveTime = nil
	head.Result.SecondAcceptedSolveTime = nil
	require.NoError(t, head.Series.Validate())
	_, err = arena.ProjectOfficialResult(head.OfficialResult)
	require.NoError(t, err)
}

func task051SizedSwissFixture(t *testing.T, size, base int) task051Swiss {
	t.Helper()
	participants := make([]uuid.UUID, size)
	seeds := make([]arena.SwissParticipantSeed, size)
	for index := range size {
		participants[index] = task051ID(base + 10 + index)
		seeds[index] = arena.SwissParticipantSeed{ParticipantID: participants[index], Seed: index + 1}
	}
	createdAt := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	roundCount, err := domain.ArenaPresetV1.SwissRounds(size)
	require.NoError(t, err)
	rotation := append([]uuid.UUID(nil), participants...)
	if size%2 == 1 {
		rotation = append(rotation, uuid.Nil)
	}
	rounds := make([]arena.FinalSwissRound, roundCount)
	seriesNumber := 0
	for roundIndex := range roundCount {
		roundID := task051ID(base + 100 + roundIndex)
		rounds[roundIndex] = arena.FinalSwissRound{
			RoundID: roundID, RoundNumber: roundIndex + 1,
			RevisionID: task051ID(base + 120 + roundIndex),
		}
		for pairIndex := 0; pairIndex < len(rotation)/2; pairIndex++ {
			first := rotation[pairIndex]
			second := rotation[len(rotation)-1-pairIndex]
			if first == uuid.Nil || second == uuid.Nil {
				byeParticipant := first
				if byeParticipant == uuid.Nil {
					byeParticipant = second
				}
				rounds[roundIndex].Bye = &arena.SwissByePointResult{
					RoundID: roundID, RoundNumber: roundIndex + 1,
					ParticipantID: byeParticipant, RevisionID: task051ID(base + 140 + roundIndex),
				}
				continue
			}
			seriesNumber++
			rounds[roundIndex].Series = append(rounds[roundIndex].Series,
				task051BuildSeriesHead(t, task051ID(base), roundID, roundIndex+1,
					first, second, first, base+200+seriesNumber*20, createdAt),
			)
		}
		last := rotation[len(rotation)-1]
		copy(rotation[2:], rotation[1:len(rotation)-1])
		rotation[1] = last
	}
	command := arena.FinalSwissProjectionCommand{
		TournamentID: task051ID(base), Preset: domain.ArenaPresetV1,
		ProjectionID: task051ID(base + 1), RevisionID: task051RevisionID(base + 2), RevisionNo: 1,
		ParticipantIDs: participants, Seeds: seeds, Rounds: rounds, CreatedAt: createdAt,
	}
	task051AttachRoundProofs(t, &command, base+4000)
	ledgerInput := arena.SwissPointLedgerInput{ParticipantIDs: participants}
	for _, round := range rounds {
		for _, head := range round.Series {
			ledgerInput.Series = append(ledgerInput.Series, head.Result)
		}
		if round.Bye != nil {
			ledgerInput.Byes = append(ledgerInput.Byes, *round.Bye)
		}
	}
	ledger, err := arena.BuildSwissPointLedger(ledgerInput)
	require.NoError(t, err)
	standings, err := arena.OrderSwissNormalStandings(arena.SwissNormalOrderingInput{
		Ledger: ledger, Seeds: seeds, SwissComplete: true,
	})
	require.NoError(t, err)
	source, err := arena.NewGoldenStandingsProjection(
		command.TournamentID, command.ProjectionID, command.RevisionID, 1, nil, true, standings,
	)
	require.NoError(t, err)
	partition, err := arena.PartitionGoldenTies(source)
	require.NoError(t, err)
	for index, seed := range partition.GoldenGroups() {
		command.GoldenGroups = append(command.GoldenGroups, arena.FinalSwissGoldenGroupIdentity{
			PositionFrom: seed.PositionFrom, PositionTo: seed.PositionTo,
			GroupID: task051ID(base + 2000 + index), RevisionID: task051RevisionID(base + 2020 + index),
		})
	}
	return task051Swiss{command: command, participants: participants}
}

func task051CutoffTieFixture(t *testing.T) task051Swiss {
	t.Helper()
	const base = 12000
	fixture := task051SizedSwissFixture(t, 5, base)
	winners := []uuid.UUID{
		fixture.participants[4], fixture.participants[2], fixture.participants[4],
		fixture.participants[2], fixture.participants[0], fixture.participants[2],
	}
	seriesIndex := 0
	for roundIndex := range fixture.command.Rounds {
		round := &fixture.command.Rounds[roundIndex]
		for index, head := range round.Series {
			seriesIndex++
			round.Series[index] = task051BuildSeriesHead(
				t, fixture.command.TournamentID, round.RoundID, round.RoundNumber,
				head.Series.FirstParticipantID, head.Series.SecondParticipantID,
				winners[seriesIndex-1], base+400+seriesIndex*20, fixture.command.CreatedAt,
			)
		}
	}
	ledgerInput := arena.SwissPointLedgerInput{ParticipantIDs: fixture.participants}
	for _, round := range fixture.command.Rounds {
		for _, head := range round.Series {
			ledgerInput.Series = append(ledgerInput.Series, head.Result)
		}
		ledgerInput.Byes = append(ledgerInput.Byes, *round.Bye)
	}
	ledger, err := arena.BuildSwissPointLedger(ledgerInput)
	require.NoError(t, err)
	standings, err := arena.OrderSwissNormalStandings(arena.SwissNormalOrderingInput{
		Ledger: ledger, Seeds: fixture.command.Seeds, SwissComplete: true,
	})
	require.NoError(t, err)
	source, err := arena.NewGoldenStandingsProjection(
		fixture.command.TournamentID, fixture.command.ProjectionID, fixture.command.RevisionID,
		fixture.command.RevisionNo, nil, true, standings,
	)
	require.NoError(t, err)
	partition, err := arena.PartitionGoldenTies(source)
	require.NoError(t, err)
	groups := partition.GoldenGroups()
	require.NotEmpty(t, groups)
	fixture.command.GoldenGroups = make([]arena.FinalSwissGoldenGroupIdentity, len(groups))
	for index, group := range groups {
		fixture.command.GoldenGroups[index] = arena.FinalSwissGoldenGroupIdentity{
			PositionFrom: group.PositionFrom, PositionTo: group.PositionTo,
			GroupID:    task051ID(base + 3000 + index*2),
			RevisionID: task051RevisionID(base + 3001 + index*2),
		}
	}
	task051AttachRoundProofs(t, &fixture.command, base+5000)
	return fixture
}

func task051AttachRoundProofs(
	t *testing.T,
	command *arena.FinalSwissProjectionCommand,
	base int,
) {
	t.Helper()
	for roundIndex := range command.Rounds {
		round := &command.Rounds[roundIndex]
		proofBase := base + roundIndex*100
		series := make([]arena.SwissLockedSeries, len(round.Series))
		for index, head := range round.Series {
			series[index] = arena.SwissLockedSeries{
				SeriesID: head.Series.ID, PairingID: task051ID(proofBase + 20 + index),
				FirstParticipantID:  head.Series.FirstParticipantID,
				SecondParticipantID: head.Series.SecondParticipantID,
			}
		}
		byeParticipantID := uuid.Nil
		if round.Bye != nil {
			byeParticipantID = round.Bye.ParticipantID
		}
		planRevisionID := task051ID(proofBase + 5)
		proof, err := arena.NewSwissRoundLockProof(arena.SwissRoundLockProofInput{
			TournamentID: command.TournamentID, RosterID: task051ID(proofBase + 1),
			RoundID: round.RoundID, Preset: command.Preset, RoundNumber: round.RoundNumber,
			SourceRevisionID: task051ID(proofBase + 2), CategoryRevisionID: task051ID(proofBase + 3),
			PoolRevisionID: task051ID(proofBase + 4), PlanRevisionID: planRevisionID,
			PreflightRevisionID: task051ID(proofBase + 6), WaveID: task051ID(proofBase + 7),
			WaveRevisionID: domain.ArenaWaveRevisionID(task051ID(proofBase + 8)),
			Revisions: arena.SwissRoundPlanRevisions{
				Round: 1, Source: 1, Category: 1, Pool: 1,
				History: 1, Reservation: 1, Membership: 1,
			},
			RosterParticipantIDs: command.ParticipantIDs,
			Series:               series, ByeParticipantID: byeParticipantID,
		})
		require.NoError(t, err)
		round.RevisionID = planRevisionID
		round.LockProof = proof
	}
}

func task051BuildSeriesHead(
	t *testing.T,
	tournamentID, roundID uuid.UUID,
	round int,
	first, second, winner uuid.UUID,
	base int,
	createdAt time.Time,
) arena.FinalSwissSeriesHead {
	t.Helper()
	seriesID := task051ID(base)
	resultID := task051OfficialID(base + 1)
	scoreID := domain.ArenaSeriesScoreRevisionID(task051ID(base + 2))
	previousScoreID := domain.ArenaSeriesScoreRevisionID(task051ID(base + 3))
	resultProjection, err := domain.NewArenaProjectionRevision(
		task051RevisionID(base+4), tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: seriesID},
		1, nil, createdAt.Add(-time.Minute), []byte("task051-sized-result"),
	)
	require.NoError(t, err)
	scoreProjection, err := domain.NewArenaProjectionRevision(
		task051RevisionID(base+5), tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesScore, EntityID: seriesID},
		1, nil, createdAt.Add(-time.Minute-time.Second), []byte("task051-sized-score"),
	)
	require.NoError(t, err)
	score := domain.ArenaSeriesScore{}
	if winner == first {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	series := domain.ArenaSeries{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: first, SecondParticipantID: second,
		Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateCompleted,
		Score: score, WinnerID: task051UUIDPointer(winner),
		CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
	}
	attempt := arena.SeriesScoreAttemptReference{
		SlotID: task051ID(base + 6), SlotPosition: 1,
		GameID: task051ID(base + 7), AttemptNo: 1,
		State: domain.ArenaGameStateCompleted, WinnerID: task051UUIDPointer(winner),
		Reason:                      domain.ArenaGameResultReasonSolved,
		CurrentGameResultRevisionID: task051OfficialID(base + 8),
	}
	scoreHead := arena.SeriesScoreRevisionHead{
		Scope: arena.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:    scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2,
		Operation: arena.SeriesScoreRevisionOperationAppendAttempt,
		CommandID: task051ID(base + 9), Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
		CommandAttempt: &attempt, FirstParticipantID: first, SecondParticipantID: second,
		Format: domain.ArenaSeriesFormatBO1, Score: score,
		Attempts:         []arena.SeriesScoreAttemptReference{attempt},
		SourceProjection: scoreProjection.Revision(), RecordedAt: createdAt.Add(-30 * time.Second),
	}
	official := arena.OfficialResultRevisionHead{
		Scope: arena.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, Kind: arena.OfficialResultSubjectSeries,
		},
		ID: resultID, Ordinal: 1, CommandID: task051ID(base + 10),
		Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
		Outcome: arena.OfficialResultOutcome{
			SeriesState:  domain.ArenaSeriesStateCompleted,
			SeriesReason: arena.ArenaSeriesResultReasonScoreComplete,
			WinnerID:     task051UUIDPointer(winner), ScoreRevisionID: &scoreID,
		},
		SourceProjection: resultProjection.Revision(), RecordedAt: createdAt.Add(-20 * time.Second),
	}
	return arena.FinalSwissSeriesHead{
		Series: series,
		OfficialResult: arena.OfficialResultProjectionInput{
			Result: official, ResultProjection: resultProjection,
			Score: &scoreHead, ScoreProjection: &scoreProjection,
		},
		Projection: resultProjection,
		Result: arena.SwissSeriesPointResult{
			RoundID: roundID, RoundNumber: round, SeriesID: seriesID,
			ResultRevisionID: resultID, FirstParticipantID: first, SecondParticipantID: second,
			WinnerID: task051UUIDPointer(winner), Label: arena.SwissSeriesResultPlayed,
		},
	}
}

func task051TwoTieFixture(t *testing.T) task051Swiss {
	t.Helper()

	fixture := task051SwissFixture(t, true)
	task051CancelSeriesHead(t, &fixture.command.Rounds[0].Series[0])
	task051CancelSeriesHead(t, &fixture.command.Rounds[0].Series[1])
	task051SetSeriesWinner(t, &fixture.command.Rounds[1].Series[0], fixture.participants[0])
	fixture.command.GoldenGroups = []arena.FinalSwissGoldenGroupIdentity{
		{PositionFrom: 1, PositionTo: 2, GroupID: task051ID(22), RevisionID: task051RevisionID(23)},
		{PositionFrom: 3, PositionTo: 4, GroupID: task051ID(24), RevisionID: task051RevisionID(25)},
	}
	return fixture
}

func task051SetSeriesWinner(t *testing.T, head *arena.FinalSwissSeriesHead, winner uuid.UUID) {
	t.Helper()

	score := domain.ArenaSeriesScore{}
	if winner == head.Series.FirstParticipantID {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	head.Series.State = domain.ArenaSeriesStateCompleted
	head.Series.Score = score
	head.Series.WinnerID = task051UUIDPointer(winner)
	head.OfficialResult.Score.Score = score
	head.OfficialResult.Score.CommandAttempt.WinnerID = task051UUIDPointer(winner)
	head.OfficialResult.Score.Attempts[0].WinnerID = task051UUIDPointer(winner)
	head.OfficialResult.Result.Outcome.SeriesState = domain.ArenaSeriesStateCompleted
	head.OfficialResult.Result.Outcome.SeriesReason = arena.ArenaSeriesResultReasonScoreComplete
	head.OfficialResult.Result.Outcome.WinnerID = task051UUIDPointer(winner)
	head.Result.WinnerID = task051UUIDPointer(winner)
	head.Result.Label = arena.SwissSeriesResultPlayed
	require.NoError(t, head.Series.Validate())
	require.NoError(t, head.OfficialResult.Score.Validate())
	require.NoError(t, head.OfficialResult.Result.Validate())
}

func task051CancelSeriesHead(t *testing.T, head *arena.FinalSwissSeriesHead) {
	t.Helper()

	head.Series.State = domain.ArenaSeriesStateCancelled
	head.Series.Score = domain.ArenaSeriesScore{}
	head.Series.WinnerID = nil
	head.OfficialResult.Score.Ordinal = 1
	head.OfficialResult.Score.PreviousRevisionID = nil
	head.OfficialResult.Score.Operation = arena.SeriesScoreRevisionOperationInitialize
	head.OfficialResult.Score.CommandAttempt = nil
	head.OfficialResult.Score.Score = domain.ArenaSeriesScore{}
	head.OfficialResult.Score.Attempts = nil
	head.OfficialResult.Result.Outcome.SeriesState = domain.ArenaSeriesStateCancelled
	head.OfficialResult.Result.Outcome.SeriesReason = arena.ArenaSeriesResultReasonSeriesCancelled
	head.OfficialResult.Result.Outcome.WinnerID = nil
	head.Result.WinnerID = nil
	head.Result.Label = arena.SwissSeriesResultVoid
	require.NoError(t, head.Series.Validate())
	require.NoError(t, head.OfficialResult.Score.Validate())
	require.NoError(t, head.OfficialResult.Result.Validate())
}

type task051Swiss struct {
	command      arena.FinalSwissProjectionCommand
	participants []uuid.UUID
}

func task051SwissFixture(t *testing.T, tied bool) task051Swiss {
	t.Helper()

	participants := []uuid.UUID{task051ID(101), task051ID(102), task051ID(103), task051ID(104)}
	createdAt := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	seeds := []arena.SwissParticipantSeed{
		{ParticipantID: participants[0], Seed: 4},
		{ParticipantID: participants[1], Seed: 3},
		{ParticipantID: participants[2], Seed: 2},
		{ParticipantID: participants[3], Seed: 1},
	}

	series := func(round, number int, first, second, winner uuid.UUID, firstTime, secondTime time.Duration) arena.FinalSwissSeriesHead {
		t.Helper()
		seriesID := task051ID(200 + number)
		resultID := task051OfficialID(300 + number)
		scoreID := domain.ArenaSeriesScoreRevisionID(task051ID(330 + number))
		previousScoreID := domain.ArenaSeriesScoreRevisionID(task051ID(340 + number))
		projectionID := task051RevisionID(400 + number)
		resultProjection, err := domain.NewArenaProjectionRevision(
			projectionID,
			task051ID(1),
			domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: seriesID},
			1,
			nil,
			createdAt.Add(-time.Duration(20-number)*time.Minute),
			[]byte("terminal-series-"+seriesID.String()),
		)
		require.NoError(t, err)
		scoreProjection, err := domain.NewArenaProjectionRevision(
			task051RevisionID(430+number),
			task051ID(1),
			domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesScore, EntityID: seriesID},
			1,
			nil,
			resultProjection.Revision().CreatedAt().Add(-2*time.Second),
			[]byte("terminal-score-"+seriesID.String()),
		)
		require.NoError(t, err)
		score := domain.ArenaSeriesScore{}
		if winner == first {
			score.FirstParticipantWins = 1
		} else {
			score.SecondParticipantWins = 1
		}
		seriesState := domain.ArenaSeries{
			ID: seriesID, TournamentID: task051ID(1),
			FirstParticipantID: first, SecondParticipantID: second,
			Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateCompleted,
			Score: score, WinnerID: task051UUIDPointer(winner),
			CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
		}
		attempt := arena.SeriesScoreAttemptReference{
			SlotID: task051ID(470 + number), SlotPosition: 1,
			GameID: task051ID(480 + number), AttemptNo: 1,
			State: domain.ArenaGameStateCompleted, WinnerID: task051UUIDPointer(winner),
			Reason:                      domain.ArenaGameResultReasonSolved,
			CurrentGameResultRevisionID: task051OfficialID(490 + number),
		}
		scoreHead := arena.SeriesScoreRevisionHead{
			Scope: arena.SeriesScoreRevisionScope{TournamentID: task051ID(1), SeriesID: seriesID},
			ID:    scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2,
			Operation: arena.SeriesScoreRevisionOperationAppendAttempt,
			CommandID: task051ID(370 + number), Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
			CommandAttempt:     &attempt,
			FirstParticipantID: first, SecondParticipantID: second,
			Format: domain.ArenaSeriesFormatBO1, Score: score,
			Attempts:         []arena.SeriesScoreAttemptReference{attempt},
			SourceProjection: scoreProjection.Revision(),
			RecordedAt:       scoreProjection.Revision().CreatedAt().Add(time.Second),
		}
		official := arena.OfficialResultRevisionHead{
			Scope: arena.OfficialResultScope{
				TournamentID: task051ID(1), SeriesID: seriesID, Kind: arena.OfficialResultSubjectSeries,
			},
			ID: resultID, Ordinal: 1, CommandID: task051ID(360 + number),
			Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
			Outcome: arena.OfficialResultOutcome{
				SeriesState:  domain.ArenaSeriesStateCompleted,
				SeriesReason: arena.ArenaSeriesResultReasonScoreComplete,
				WinnerID:     task051UUIDPointer(winner), ScoreRevisionID: &scoreID,
			},
			SourceProjection: resultProjection.Revision(),
			RecordedAt:       resultProjection.Revision().CreatedAt(),
		}
		require.NoError(t, seriesState.Validate())
		require.NoError(t, scoreHead.Validate())
		require.NoError(t, official.Validate())
		return arena.FinalSwissSeriesHead{
			Series: seriesState,
			OfficialResult: arena.OfficialResultProjectionInput{
				Result: official, ResultProjection: resultProjection,
				Score: &scoreHead, ScoreProjection: &scoreProjection,
			},
			Projection: resultProjection,
			Result: arena.SwissSeriesPointResult{
				RoundID: task051ID(500 + round), RoundNumber: round,
				SeriesID: seriesID, ResultRevisionID: resultID,
				FirstParticipantID: first, SecondParticipantID: second,
				WinnerID: task051UUIDPointer(winner), Label: arena.SwissSeriesResultPlayed,
				FirstEffectiveTime: firstTime, SecondEffectiveTime: secondTime,
			},
		}
	}

	times := map[uuid.UUID]time.Duration{
		participants[0]: time.Second,
		participants[1]: time.Second,
		participants[2]: time.Second,
		participants[3]: time.Second,
	}
	if !tied {
		times[participants[1]] = 2 * time.Second
		times[participants[2]] = 3 * time.Second
		times[participants[3]] = 4 * time.Second
	}
	roundTwoWinner := participants[2]
	if !tied {
		roundTwoWinner = participants[0]
	}

	rounds := []arena.FinalSwissRound{
		{
			RoundID: task051ID(501), RoundNumber: 1, RevisionID: task051ID(601),
			Series: []arena.FinalSwissSeriesHead{
				series(1, 1, participants[0], participants[1], participants[0], times[participants[0]], times[participants[1]]),
				series(1, 2, participants[2], participants[3], participants[2], times[participants[2]], times[participants[3]]),
			},
		},
		{
			RoundID: task051ID(502), RoundNumber: 2, RevisionID: task051ID(602),
			Series: []arena.FinalSwissSeriesHead{
				series(2, 3, participants[0], participants[2], roundTwoWinner, times[participants[0]], times[participants[2]]),
				series(2, 4, participants[1], participants[3], participants[1], times[participants[1]], times[participants[3]]),
			},
		},
		{
			RoundID: task051ID(503), RoundNumber: 3, RevisionID: task051ID(603),
			Series: []arena.FinalSwissSeriesHead{
				series(3, 5, participants[0], participants[3], participants[0], times[participants[0]], times[participants[3]]),
				series(3, 6, participants[1], participants[2], participants[1], times[participants[1]], times[participants[2]]),
			},
		},
	}
	command := arena.FinalSwissProjectionCommand{
		TournamentID:   task051ID(1),
		Preset:         domain.ArenaPresetV1,
		ProjectionID:   task051ID(9),
		RevisionID:     task051RevisionID(10),
		RevisionNo:     1,
		ParticipantIDs: participants,
		Seeds:          seeds,
		Rounds:         rounds,
		CreatedAt:      createdAt,
	}
	if tied {
		command.GoldenGroups = []arena.FinalSwissGoldenGroupIdentity{{
			PositionFrom: 1, PositionTo: 3,
			GroupID: task051ID(20), RevisionID: task051RevisionID(21),
		}}
	}
	task051AttachRoundProofs(t, &command, 20000)
	return task051Swiss{command: command, participants: participants}
}

func task051LowerTieFixture(t *testing.T) task051Swiss {
	t.Helper()

	participants := []uuid.UUID{
		task051ID(111), task051ID(112), task051ID(113),
		task051ID(114), task051ID(115), task051ID(116),
	}
	createdAt := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	seeds := make([]arena.SwissParticipantSeed, len(participants))
	for index, participantID := range participants {
		seeds[index] = arena.SwissParticipantSeed{ParticipantID: participantID, Seed: len(participants) - index}
	}
	series := func(round, number int, first, second, winner uuid.UUID, firstTime, secondTime time.Duration) arena.FinalSwissSeriesHead {
		fixture := task051SwissFixture(t, false)
		head := fixture.command.Rounds[(round-1)%len(fixture.command.Rounds)].Series[0]
		seriesID := task051ID(1200 + number)
		resultID := task051OfficialID(1300 + number)
		scoreID := domain.ArenaSeriesScoreRevisionID(task051ID(1400 + number))
		projectionID := task051RevisionID(1500 + number)
		projection, err := domain.NewArenaProjectionRevision(
			projectionID, task051ID(2),
			domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: seriesID},
			1, nil, createdAt.Add(-time.Duration(20-number)*time.Minute),
			[]byte("lower-terminal-"+seriesID.String()),
		)
		require.NoError(t, err)
		scoreProjection, err := domain.NewArenaProjectionRevision(
			task051RevisionID(1550+number), task051ID(2),
			domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesScore, EntityID: seriesID},
			1, nil, projection.Revision().CreatedAt().Add(-2*time.Second),
			[]byte("lower-score-"+seriesID.String()),
		)
		require.NoError(t, err)
		score := domain.ArenaSeriesScore{}
		if winner == first {
			score.FirstParticipantWins = 1
		} else {
			score.SecondParticipantWins = 1
		}
		head.Series = domain.ArenaSeries{
			ID: seriesID, TournamentID: task051ID(2), FirstParticipantID: first, SecondParticipantID: second,
			Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateCompleted,
			Score: score, WinnerID: task051UUIDPointer(winner),
			CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
		}
		attempt := arena.SeriesScoreAttemptReference{
			SlotID: task051ID(1610 + number), SlotPosition: 1,
			GameID: task051ID(1630 + number), AttemptNo: 1,
			State: domain.ArenaGameStateCompleted, WinnerID: task051UUIDPointer(winner),
			Reason:                      domain.ArenaGameResultReasonSolved,
			CurrentGameResultRevisionID: task051OfficialID(1650 + number),
		}
		previousScoreID := domain.ArenaSeriesScoreRevisionID(task051ID(1420 + number))
		scoreHead := arena.SeriesScoreRevisionHead{
			Scope: arena.SeriesScoreRevisionScope{TournamentID: task051ID(2), SeriesID: seriesID},
			ID:    scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2,
			Operation: arena.SeriesScoreRevisionOperationAppendAttempt,
			CommandID: task051ID(1590 + number), Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
			CommandAttempt:     &attempt,
			FirstParticipantID: first, SecondParticipantID: second,
			Format: domain.ArenaSeriesFormatBO1, Score: score,
			Attempts:         []arena.SeriesScoreAttemptReference{attempt},
			SourceProjection: scoreProjection.Revision(),
			RecordedAt:       scoreProjection.Revision().CreatedAt().Add(time.Second),
		}
		official := arena.OfficialResultRevisionHead{
			Scope: arena.OfficialResultScope{TournamentID: task051ID(2), SeriesID: seriesID, Kind: arena.OfficialResultSubjectSeries},
			ID:    resultID, Ordinal: 1, CommandID: task051ID(1600 + number),
			Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
			Outcome: arena.OfficialResultOutcome{
				SeriesState:  domain.ArenaSeriesStateCompleted,
				SeriesReason: arena.ArenaSeriesResultReasonScoreComplete,
				WinnerID:     task051UUIDPointer(winner), ScoreRevisionID: &scoreID,
			},
			SourceProjection: projection.Revision(), RecordedAt: projection.Revision().CreatedAt(),
		}
		require.NoError(t, scoreHead.Validate())
		require.NoError(t, official.Validate())
		head.OfficialResult = arena.OfficialResultProjectionInput{
			Result: official, ResultProjection: projection,
			Score: &scoreHead, ScoreProjection: &scoreProjection,
		}
		head.Projection = projection
		head.Result = arena.SwissSeriesPointResult{
			RoundID: task051ID(1700 + round), RoundNumber: round,
			SeriesID: seriesID, ResultRevisionID: resultID,
			FirstParticipantID: first, SecondParticipantID: second,
			WinnerID: task051UUIDPointer(winner), Label: arena.SwissSeriesResultPlayed,
			FirstEffectiveTime: firstTime, SecondEffectiveTime: secondTime,
		}
		return head
	}
	fast := map[uuid.UUID]time.Duration{
		participants[0]: time.Second,
		participants[1]: 2 * time.Second,
		participants[2]: 3 * time.Second,
		participants[3]: 4 * time.Second,
		participants[4]: 5 * time.Second,
		participants[5]: 5 * time.Second,
	}
	rounds := []arena.FinalSwissRound{
		{RoundID: task051ID(1701), RoundNumber: 1, RevisionID: task051ID(1801), Series: []arena.FinalSwissSeriesHead{
			series(1, 1, participants[0], participants[5], participants[0], fast[participants[0]], fast[participants[5]]),
			series(1, 2, participants[1], participants[4], participants[1], fast[participants[1]], fast[participants[4]]),
			series(1, 3, participants[2], participants[3], participants[3], fast[participants[2]], fast[participants[3]]),
		}},
		{RoundID: task051ID(1702), RoundNumber: 2, RevisionID: task051ID(1802), Series: []arena.FinalSwissSeriesHead{
			series(2, 4, participants[0], participants[4], participants[0], fast[participants[0]], fast[participants[4]]),
			series(2, 5, participants[5], participants[2], participants[2], fast[participants[5]], fast[participants[2]]),
			series(2, 6, participants[1], participants[3], participants[1], fast[participants[1]], fast[participants[3]]),
		}},
		{RoundID: task051ID(1703), RoundNumber: 3, RevisionID: task051ID(1803), Series: []arena.FinalSwissSeriesHead{
			series(3, 7, participants[0], participants[3], participants[0], fast[participants[0]], fast[participants[3]]),
			series(3, 8, participants[4], participants[2], participants[2], fast[participants[4]], fast[participants[2]]),
			series(3, 9, participants[5], participants[1], participants[1], fast[participants[5]], fast[participants[1]]),
		}},
	}
	fixture := task051Swiss{
		participants: participants,
		command: arena.FinalSwissProjectionCommand{
			TournamentID: task051ID(2), Preset: domain.ArenaPresetV1,
			ProjectionID: task051ID(1899),
			RevisionID:   task051RevisionID(1900), RevisionNo: 1,
			ParticipantIDs: participants, Seeds: seeds, Rounds: rounds,
			GoldenGroups: []arena.FinalSwissGoldenGroupIdentity{{
				PositionFrom: 1, PositionTo: 2,
				GroupID: task051ID(1910), RevisionID: task051RevisionID(1911),
			}},
			CreatedAt: createdAt,
		},
	}
	task051AttachRoundProofs(t, &fixture.command, 23000)
	return fixture
}

func task051ResultHeads(command arena.FinalSwissProjectionCommand) []arena.FinalSwissSeriesHead {
	heads := make([]arena.FinalSwissSeriesHead, 0)
	for _, round := range command.Rounds {
		heads = append(heads, round.Series...)
	}
	return heads
}

func task051UUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func task051ID(value int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + task051Suffix(value))
}

func task051RevisionID(value int) domain.ArenaDerivedRevisionID {
	return domain.ArenaDerivedRevisionID(task051ID(value))
}

func task051OfficialID(value int) domain.ArenaOfficialResultRevisionID {
	return domain.ArenaOfficialResultRevisionID(task051ID(value))
}

func task051OfficialPointer(value int) *domain.ArenaOfficialResultRevisionID {
	id := task051OfficialID(value)
	return &id
}

func task051Suffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}
