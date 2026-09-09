package playoff_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestFinalSwissProjection(t *testing.T) {
	t.Parallel()

	t.Run("commits formula ties and freezes derived evidence", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, true)
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.False(t, projection.AdvanceDirectly())

		standings := projection.Standings()
		require.Len(t, standings, 4)
		for index, standing := range standings {
			require.Equal(t, index+1, standing.Position)
			require.Equal(t, swissusecase.PointsFinal, standing.PointsLabel)
			require.Equal(t, playoff.SwissBuchholzFinal, standing.BuchholzStatus)
		}
		require.Equal(t, []uuid.UUID{
			fixture.participants[2], fixture.participants[1], fixture.participants[0],
		}, standingParticipants(standings)[:3])
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
		require.Equal(t, fixture.participants[:3], ties[0].ParticipantIDs)

		groups := projection.GoldenGroups()
		require.Len(t, groups, 1)
		require.NoError(t, groups[0].Revision.Validate())
		require.Equal(t, fixture.command.GoldenGroups[0].GroupID, groups[0].State.ID)
		require.Equal(t, fixture.command.RevisionID, groups[0].State.SourceProjectionRevisionID)
		require.Equal(t, 1, groups[0].State.PositionFrom)
		require.Equal(t, 3, groups[0].State.PositionTo)
		require.False(t, groups[0].State.ParticipationEstablished)
		require.Empty(t, groups[0].State.Attempts)
		require.Len(t, groups[0].State.Members, 3)
		require.Equal(t, domain.ArtifactKindGoldenGroup, groups[0].Projection.Revision().Artifact().Kind)
		require.Equal(t, domain.RevisionDependency{
			SourceRevisionID:  fixture.command.RevisionID,
			DerivedRevisionID: fixture.command.GoldenGroups[0].RevisionID,
		}, groups[0].Dependency)

		dependencies := projection.TerminalSeriesDependencies()
		require.Len(t, dependencies, 6)
		for _, dependency := range dependencies {
			require.Equal(t, fixture.command.RevisionID, dependency.DerivedRevisionID)
		}
		require.Equal(t, domain.ArtifactKindStandings, projection.Projection().Revision().Artifact().Kind)
		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(projection.Projection().Payload(), &payload))
		require.Contains(t, payload, "entries")
		require.NotContains(t, payload, "standings")
		require.Equal(t, fixture.command.CreatedAt, projection.Projection().Revision().CreatedAt())
		require.NotEqual(t, [sha256.Size]byte{}, projection.Projection().Revision().PayloadDigest())

		standings[0].ParticipantID = uuid.Nil
		ties[0].ParticipantIDs[0] = uuid.Nil
		groups[0].State.Members[0].ParticipantID = uuid.Nil
		dependencies[0].SourceRevisionID = domain.DerivedRevisionID{}
		fixture.command.Rounds[0].LockProof.Series[0].PairingID = uuid.Nil
		require.Equal(t, fixture.participants[2], projection.Standings()[0].ParticipantID)
		require.Equal(t, fixture.participants[0], projection.TieGroups()[0].ParticipantIDs[0])
		require.Equal(t, fixture.participants[0], projection.GoldenGroups()[0].State.Members[0].ParticipantID)
		require.False(t, projection.TerminalSeriesDependencies()[0].SourceRevisionID.IsZero())
		require.NoError(t, projection.Validate())
	})

	t.Run("advances a fully resolved table with the canonical projection digest", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, false)
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.True(t, projection.AdvanceDirectly())
		require.Empty(t, projection.TieGroups())
		require.Empty(t, projection.GoldenGroups())
		require.Equal(t, []uuid.UUID{
			fixture.participants[0], fixture.participants[1],
			fixture.participants[2], fixture.participants[3],
		}, standingParticipants(projection.Standings()))
		require.Equal(t,
			"14610c6c43890e1feb33aae6b5c76c2e923157d99db28e4db8d78019bc6ad1cc",
			projectionDigestHex(projection.Projection()),
		)
		var payload struct {
			Rounds []map[string]json.RawMessage `json:"rounds"`
		}
		require.NoError(t, json.Unmarshal(projection.Projection().Payload(), &payload))
		require.NotEmpty(t, payload.Rounds)
		require.NotContains(t, payload.Rounds[0], "lock_proof")
		require.JSONEq(t,
			`"`+fixture.command.Rounds[0].LockProof.ProofHash+`"`,
			string(payload.Rounds[0]["lock_proof_hash"]),
		)
	})

	t.Run("separates non-impactful and cutoff tie groups", func(t *testing.T) {
		t.Parallel()

		lower := newLowerTieFixture(t)
		projection, err := playoff.PlanFinalSwissProjection(lower.command)
		require.NoError(t, err)
		require.False(t, projection.AdvanceDirectly())
		require.Len(t, projection.GoldenGroups(), 1)
		ties := projection.TieGroups()
		require.Len(t, ties, 2)
		require.Equal(t, 5, ties[1].PositionFrom)
		require.Equal(t, 6, ties[1].PositionTo)
		require.False(t, ties[1].Impactful)

		cutoff := newCutoffTieFixture(t)
		projection, err = playoff.PlanFinalSwissProjection(cutoff.command)
		require.NoError(t, err)
		var cutoffTie *playoff.FinalSwissTieGroup
		for _, tie := range projection.TieGroups() {
			if tie.PositionFrom == 4 && tie.PositionTo == 5 {
				value := tie
				cutoffTie = &value
			}
		}
		require.NotNil(t, cutoffTie)
		require.True(t, cutoffTie.Impactful)
		var cutoffGroup *playoff.FinalSwissGoldenGroup
		for _, group := range projection.GoldenGroups() {
			if group.State.PositionFrom == 4 && group.State.PositionTo == 5 {
				value := group
				cutoffGroup = &value
			}
		}
		require.NotNil(t, cutoffGroup)
	})

	t.Run("records every impactful tie group", func(t *testing.T) {
		t.Parallel()

		fixture := newTwoTieFixture(t)
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.False(t, projection.AdvanceDirectly())
		ties := projection.TieGroups()
		require.Len(t, ties, 2)
		require.Equal(t, []int{1, 3}, []int{ties[0].PositionFrom, ties[1].PositionFrom})
		require.Equal(t, []int{2, 4}, []int{ties[0].PositionTo, ties[1].PositionTo})
		require.Len(t, projection.GoldenGroups(), 2)
	})

	t.Run("fails closed on invalid command authority", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*playoff.FinalSwissProjectionCommand)
		}{
			{name: "incomplete rounds", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.Rounds = command.Rounds[:2]
			}},
			{name: "spliced round lock proof", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.Rounds[0].LockProof.Series[0].PairingID = uuid.Nil
			}},
			{name: "round revision differs from locked plan", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.Rounds[0].RevisionID = playoffID(898)
			}},
			{name: "missing terminal evidence", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.Rounds[0].Series[0] = playoff.TerminalSeriesEvidence{}
			}},
			{name: "incomplete result coverage", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.Rounds[0].Series = command.Rounds[0].Series[:1]
			}},
			{name: "cross-role identity alias", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.GoldenGroups[0].GroupID = command.TournamentID
			}},
			{name: "projection identity alias", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.ProjectionID = command.ParticipantIDs[0]
			}},
			{name: "non-UTC clock", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.CreatedAt = command.CreatedAt.In(time.FixedZone("local", 60))
			}},
			{name: "revision overflow", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.RevisionNo = math.MaxInt
			}},
			{name: "extra Golden group identity", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.GoldenGroups = append(command.GoldenGroups, playoff.FinalSwissGoldenGroupIdentity{
					PositionFrom: 2, PositionTo: 3,
					GroupID: playoffID(901), RevisionID: playoffRevisionID(902),
				})
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture := newPlayoffFixture(t, true)
				test.mutate(&fixture.command)
				projection, err := playoff.PlanFinalSwissProjection(fixture.command)
				require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
				require.Equal(t, playoff.FinalSwissProjection{}, projection)
			})
		}
	})

	t.Run("rejects accumulated duration overflow", func(t *testing.T) {
		fixture := newPlayoffFixture(t, true)
		input := terminalSeriesEvidenceInput(fixture.command.Rounds[0].Series[0])
		input.Result.FirstEffectiveTime = time.Duration(math.MaxInt64)
		evidence, err := playoff.NewTerminalSeriesEvidence(input)
		require.NoError(t, err)
		fixture.command.Rounds[0].Series[0] = evidence
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, playoff.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
	})

	t.Run("accepts immutable no-game winner and void evidence", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		fixture.command.Rounds[0].Series[0] = noGameTerminalEvidence(
			t, fixture.command.Rounds[0].Series[0], fixture.command.Rounds[0].LockProof.WaveID,
			8100, domain.NormalNoShowActionReopenWave,
		)
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.Equal(t,
			"40a29e83f65532a95eef87a73d7c3775a5a76a938a38f36e61d16b6b3b15b922",
			projectionDigestHex(projection.Projection()),
		)
		evidence := fixture.command.Rounds[0].Series[0]
		require.Equal(t, swissusecase.SeriesResultNoShow, evidence.PointResult().Label)
		require.Contains(t, projection.TerminalSeriesDependencies(), domain.RevisionDependency{
			SourceRevisionID:  evidence.Projection().Revision().ID(),
			DerivedRevisionID: projection.Projection().Revision().ID(),
		})
		wantDigest := projection.Projection().Revision().PayloadDigest()
		input := terminalSeriesEvidenceInput(evidence)
		input.OfficialResult.NoGame.Topology[0].SlotID = uuid.Nil
		input.Result.FirstEffectiveTime = time.Hour
		require.NoError(t, projection.Validate())
		require.Equal(t, wantDigest, projection.Projection().Revision().PayloadDigest())

		voidFixture := newTwoTieFixture(t)
		voidFixture.command.Rounds[0].Series[0] = noGameTerminalEvidence(
			t, voidFixture.command.Rounds[0].Series[0], voidFixture.command.Rounds[0].LockProof.WaveID,
			8200, domain.NormalNoShowActionPauseWave,
		)
		projection, err = playoff.PlanFinalSwissProjection(voidFixture.command)
		require.NoError(t, err)
		require.NoError(t, projection.Validate())
		require.Equal(t, swissusecase.SeriesResultVoid, voidFixture.command.Rounds[0].Series[0].PointResult().Label)
	})

	t.Run("rejects no-game evidence from another round", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		evidence := noGameTerminalEvidence(
			t, fixture.command.Rounds[0].Series[0], playoffID(8393),
			8300, domain.NormalNoShowActionReopenWave,
		)
		fixture.command.Rounds[0].Series[0] = evidence
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, playoff.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
	})

	t.Run("rejects no-game time and transitive identity aliases", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		fixture.command.Rounds[0].Series[0] = noGameTerminalEvidence(
			t, fixture.command.Rounds[0].Series[0], fixture.command.Rounds[0].LockProof.WaveID,
			8350, domain.NormalNoShowActionReopenWave,
		)
		recorded := fixture.command.Rounds[0].Series[0].OfficialResult().NoGame
		require.NotNil(t, recorded)
		resultPrevious := fixture.command.Rounds[0].Series[0].Projection().Revision().PreviousRevisionID()
		require.NotNil(t, resultPrevious)
		require.NotNil(t, recorded.Series.PreviousRevisionID)
		require.NotNil(t, recorded.Score.PreviousRevisionID)

		tests := []struct {
			name   string
			mutate func(*playoff.FinalSwissProjectionCommand)
		}{
			{name: "terminal evidence is newer than standings", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.CreatedAt = recorded.ResolvedAt.Add(-time.Nanosecond)
			}},
			{name: "revision aliases Game", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.RevisionID = domain.DerivedRevisionID(recorded.Topology[0].GameID)
			}},
			{name: "revision aliases projection predecessor", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.RevisionID = *resultPrevious
			}},
			{name: "revision aliases result predecessor", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.RevisionID = domain.DerivedRevisionID(*recorded.Series.PreviousRevisionID)
			}},
			{name: "revision aliases score predecessor", mutate: func(command *playoff.FinalSwissProjectionCommand) {
				command.RevisionID = domain.DerivedRevisionID(*recorded.Score.PreviousRevisionID)
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := fixture.command
				test.mutate(&command)
				projection, err := playoff.PlanFinalSwissProjection(command)
				require.Equal(t, playoff.FinalSwissProjection{}, projection)
				require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
			})
		}
	})

	t.Run("exports a graph-valid predecessor edge", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		first, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		secondCommand := fixture.command
		secondCommand.RevisionID = playoffRevisionID(8400)
		secondCommand.RevisionNo = 2
		secondCommand.PhysicalProjectionRevision = 2
		secondCommand.Previous = &first
		secondCommand.CreatedAt = fixture.command.CreatedAt.Add(time.Minute)
		second, err := playoff.PlanFinalSwissProjection(secondCommand)
		require.NoError(t, err)
		predecessor := domain.RevisionDependency{
			SourceRevisionID:  first.Projection().Revision().ID(),
			DerivedRevisionID: second.Projection().Revision().ID(),
		}
		require.Contains(t, second.Dependencies(), predecessor)
		evidences := terminalEvidence(fixture.command)
		projections := make([]domain.ProjectionRevision, 0, 2+len(evidences))
		projections = append(projections, first.Projection(), second.Projection())
		for _, evidence := range evidences {
			projections = append(projections, evidence.Projection())
		}
		graph, err := domain.NewRevisionGraph(
			projections, append(first.Dependencies(), second.Dependencies()...),
		)
		require.NoError(t, err)
		require.True(t, graph.DependsOn(second.Projection().Revision().ID(), first.Projection().Revision().ID()))

		invalid := secondCommand
		invalid.RevisionID = first.Projection().Revision().ID()
		invalidProjection, invalidErr := playoff.PlanFinalSwissProjection(invalid)
		require.Equal(t, playoff.FinalSwissProjection{}, invalidProjection)
		require.ErrorIs(t, invalidErr, playoff.ErrInvalidFinalSwissProjection)

		gap := secondCommand
		gap.RevisionID = playoffRevisionID(8401)
		gap.RevisionNo = 3
		gap.PhysicalProjectionRevision = 3
		gapProjection, gapErr := playoff.PlanFinalSwissProjection(gap)
		require.Equal(t, playoff.FinalSwissProjection{}, gapProjection)
		require.ErrorIs(t, gapErr, playoff.ErrInvalidFinalSwissProjection)
	})

	t.Run("keeps a long successor chain bounded and rejects retained identity reuse", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		first, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		current := first
		payloadLimit := len(current.Projection().Payload()) + 128
		for revisionNo := 2; revisionNo <= 48; revisionNo++ {
			command := fixture.command
			command.RevisionID = playoffRevisionID(12000 + revisionNo)
			command.RevisionNo = revisionNo
			command.PhysicalProjectionRevision = revisionNo
			command.Previous = &current
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			current, err = playoff.PlanFinalSwissProjection(command)
			require.NoError(t, err)
			require.NoError(t, current.Validate())
			require.LessOrEqual(t, len(current.Projection().Payload()), payloadLimit)
		}
		alias := fixture.command
		alias.RevisionID = first.Projection().Revision().ID()
		alias.RevisionNo = 49
		alias.PhysicalProjectionRevision = 49
		alias.Previous = &current
		alias.CreatedAt = fixture.command.CreatedAt.Add(49 * time.Minute)
		projection, err := playoff.PlanFinalSwissProjection(alias)
		require.Equal(t, playoff.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
	})

	t.Run("finalizes maximum and odd-roster bye schedules", func(t *testing.T) {
		for _, size := range []int{5, 16} {
			t.Run(playoffSizeName(size), func(t *testing.T) {
				fixture := newSizedPlayoffFixture(t, size, 8500+size*100)
				projection, err := playoff.PlanFinalSwissProjection(fixture.command)
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
			})
		}
	})

	t.Run("rejects spliced bye and oversized round authority", func(t *testing.T) {
		fixture := newSizedPlayoffFixture(t, 5, 11800)
		fixture.command.Rounds[0].Bye.ParticipantID = fixture.command.Rounds[0].Series[0].Series().FirstParticipantID
		projection, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, playoff.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)

		fixture = newPlayoffFixture(t, false)
		fixture.command.Rounds[0].Series = make([]playoff.TerminalSeriesEvidence, domain.TournamentMaxParticipants+1)
		projection, err = playoff.PlanFinalSwissProjection(fixture.command)
		require.Equal(t, playoff.FinalSwissProjection{}, projection)
		require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
	})
}

func TestFinalSwissAcceptsRestoredNoGameSources(t *testing.T) {
	fixture := newPlayoffFixture(t, false)
	for index := range fixture.command.Rounds[0].Series {
		evidence := noGameTerminalEvidence(t, fixture.command.Rounds[0].Series[index], fixture.command.Rounds[0].LockProof.WaveID, 8500+index*100, domain.NormalNoShowActionReopenWave)
		input := terminalSeriesEvidenceInput(evidence)
		recorded := *input.OfficialResult.NoGame
		recorded.Scope.WindowID = playoffID(8700)
		recorded.Series.PreviousRevisionID = nil
		bind := func(id uuid.UUID, original domain.ProjectionRevision, ordinal int, previous *domain.DerivedRevisionID) domain.ProjectionRevision {
			node, err := domain.NewProjectionRevision(domain.DerivedRevisionID(id), recorded.Scope.TournamentID, original.Revision().Artifact(), ordinal, previous, recorded.ResolvedAt, original.Payload())
			require.NoError(t, err)
			return node
		}
		previous := domain.DerivedRevisionID(recorded.Score.PreviousRevisionID.UUID())
		recorded.ScoreProjection = bind(recorded.Score.ID.UUID(), recorded.ScoreProjection, 2, &previous)
		recorded.ScoreSourceRevision = recorded.ScoreProjection.Revision()
		recorded.ResultProjection = bind(recorded.Series.ID.UUID(), recorded.ResultProjection, 1, nil)
		recorded.ResultSourceRevision = recorded.ResultProjection.Revision()
		recorded.ResultDependency = domain.RevisionDependency{SourceRevisionID: recorded.ScoreSourceRevision.ID(), DerivedRevisionID: recorded.ResultSourceRevision.ID()}
		for game := range recorded.GameResults {
			recorded.GameProjections[game] = bind(recorded.GameResults[game].ID.UUID(), recorded.GameProjections[game], 1, nil)
			recorded.GameSourceRevisions[game] = recorded.GameProjections[game].Revision()
			recorded.GameDependencies[game] = domain.RevisionDependency{SourceRevisionID: recorded.GameSourceRevisions[game].ID(), DerivedRevisionID: recorded.ScoreSourceRevision.ID()}
		}
		restored, err := resultprojection.RestoreSQLNoGameResult(recorded)
		require.NoError(t, err)
		input.Projection, input.OfficialResult.NoGame = restored.ResultProjection, &restored
		fixture.command.Rounds[0].Series[index], err = playoff.NewTerminalSeriesEvidence(input)
		require.NoError(t, err)
	}
	planned, err := playoff.PlanFinalSwissProjection(fixture.command)
	require.NoError(t, err)
	require.NoError(t, planned.Validate())
	top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{TournamentID: fixture.command.TournamentID, RevisionID: playoffRevisionID(8800), RevisionNo: 1, Source: planned, CurrentTerminalSeries: terminalEvidence(fixture.command), CreatedAt: fixture.command.CreatedAt.Add(time.Second)})
	require.NoError(t, err)
	require.NoError(t, top4.Validate())
	for _, identity := range []uuid.UUID{fixture.command.Rounds[0].Series[0].OfficialResult().NoGame.Scope.WindowID,
		fixture.command.Rounds[0].Series[0].OfficialResult().NoGame.Score.ID.UUID()} {
		forged := fixture.command
		forged.RevisionID = domain.DerivedRevisionID(identity)
		_, err := playoff.PlanFinalSwissProjection(forged)
		require.Error(t, err)
	}
}

func TestFinalSwissProjectionKeepsPhysicalProjectionCounterSeparate(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)
	firstCommand := fixture.command
	firstCommand.PhysicalProjectionRevision = 7
	first, err := playoff.PlanFinalSwissProjection(firstCommand)
	require.NoError(t, err)
	require.Equal(t, 1, first.Projection().Revision().RevisionNo())
	require.Equal(t, 7, first.PhysicalProjectionRevision())

	secondCommand := fixture.command
	secondCommand.RevisionID = playoffRevisionID(25001)
	secondCommand.RevisionNo = 2
	secondCommand.PhysicalProjectionRevision = 11
	secondCommand.Previous = &first
	secondCommand.CreatedAt = fixture.command.CreatedAt.Add(time.Minute)
	second, err := playoff.PlanFinalSwissProjection(secondCommand)
	require.NoError(t, err)
	require.Equal(t, 2, second.Projection().Revision().RevisionNo())
	require.Equal(t, 11, second.PhysicalProjectionRevision())
}

func TestFinalSwissProjectionRejectsSwappedLogicalAndPhysicalCounters(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)
	firstCommand := fixture.command
	firstCommand.PhysicalProjectionRevision = 7
	first, err := playoff.PlanFinalSwissProjection(firstCommand)
	require.NoError(t, err)

	conflated := fixture.command
	conflated.RevisionID = playoffRevisionID(25002)
	conflated.RevisionNo = 7
	conflated.PhysicalProjectionRevision = 2
	conflated.Previous = &first
	conflated.CreatedAt = fixture.command.CreatedAt.Add(time.Minute)
	projection, planErr := playoff.PlanFinalSwissProjection(conflated)

	require.Equal(t, playoff.FinalSwissProjection{}, projection)
	require.ErrorIs(t, planErr, playoff.ErrInvalidFinalSwissProjection)
}

func standingParticipants(standings []playoff.FinalSwissStanding) []uuid.UUID {
	participants := make([]uuid.UUID, len(standings))
	for index, standing := range standings {
		participants[index] = standing.ParticipantID
	}
	return participants
}

func projectionDigestHex(projection domain.ProjectionRevision) string {
	digest := projection.Revision().PayloadDigest()
	return hex.EncodeToString(digest[:])
}

func playoffSizeName(size int) string {
	if size == 5 {
		return "five participants"
	}
	return "sixteen participants"
}
