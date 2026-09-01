package arena_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestStrengthMatchedSemifinals(t *testing.T) {
	t.Parallel()

	t.Run("locks BO1 seed 1v2 and 3v4 with single-elimination paths", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(801), RevisionNo: 1,
			Source:                final,
			CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt:             fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)

		command := arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(802), RevisionNo: 1,
			Top4:      top4,
			SeriesIDs: [2]uuid.UUID{task051ID(803), task051ID(804)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		bracket, err := arena.PlanStrengthMatchedSemifinals(command)
		require.NoError(t, err)
		require.NoError(t, bracket.Validate())
		require.True(t, bracket.Locked())
		require.False(t, bracket.HasLowerBracket())
		require.Equal(t, command.CreatedAt, bracket.LockedAt())
		require.Equal(t, domain.ArenaArtifactKindBracket, bracket.Projection().Revision().Artifact().Kind)
		require.Equal(t, []domain.ArenaRevisionDependency{{
			SourceRevisionID:  task051RevisionID(801),
			DerivedRevisionID: task051RevisionID(802),
		}}, bracket.Dependencies())

		participants := top4.Participants()
		matches := bracket.Semifinals()
		require.Len(t, matches, 2)
		require.Equal(t, participants[0].ParticipantID, matches[0].Series.FirstParticipantID)
		require.Equal(t, participants[1].ParticipantID, matches[0].Series.SecondParticipantID)
		require.Equal(t, participants[2].ParticipantID, matches[1].Series.FirstParticipantID)
		require.Equal(t, participants[3].ParticipantID, matches[1].Series.SecondParticipantID)
		for index, match := range matches {
			require.Equal(t, index+1, match.Position)
			require.Equal(t, domain.ArenaSeriesFormatBO1, match.Series.Format)
			require.Equal(t, domain.ArenaSeriesStateLocked, match.Series.State)
			require.Equal(t, arena.SemifinalWinnerToFinal, match.WinnerPath)
			require.Equal(t, arena.SemifinalLoserEliminated, match.LoserPath)
			require.Empty(t, match.Series.Slots)
			require.Nil(t, match.Series.WinnerID)
			require.Nil(t, match.Series.CurrentScoreRevisionID)
			require.Nil(t, match.Series.CurrentResultRevisionID)
		}

		matches[0].Series.FirstParticipantID = uuid.Nil
		require.Equal(t, participants[0].ParticipantID, bracket.Semifinals()[0].Series.FirstParticipantID)
	})

	t.Run("rejects conventional pairing duplicate Series and stale lineage", func(t *testing.T) {
		t.Parallel()

		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(810), RevisionNo: 1,
			Source:                final,
			CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt:             fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)

		base := arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(811), RevisionNo: 1,
			Top4:      top4,
			SeriesIDs: [2]uuid.UUID{task051ID(812), task051ID(813)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		tests := []struct {
			name   string
			mutate func(*arena.SemifinalBracketCommand)
		}{
			{name: "duplicate Series", mutate: func(command *arena.SemifinalBracketCommand) {
				command.SeriesIDs[1] = command.SeriesIDs[0]
			}},
			{name: "Series aliases participant", mutate: func(command *arena.SemifinalBracketCommand) {
				command.SeriesIDs[0] = command.Top4.Participants()[0].ParticipantID
			}},
			{name: "Series aliases terminal evidence", mutate: func(command *arena.SemifinalBracketCommand) {
				command.SeriesIDs[0] = command.Top4.TerminalSeriesReferences()[0].ProjectionRevisionID.UUID()
			}},
			{name: "bracket predates Top 4", mutate: func(command *arena.SemifinalBracketCommand) {
				command.CreatedAt = command.Top4.Projection().Revision().CreatedAt().Add(-time.Nanosecond)
			}},
			{name: "wrong tournament", mutate: func(command *arena.SemifinalBracketCommand) {
				command.TournamentID = task051ID(999)
			}},
			{name: "revision overflow", mutate: func(command *arena.SemifinalBracketCommand) {
				command.RevisionNo = math.MaxInt
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := base
				test.mutate(&command)
				bracket, planErr := arena.PlanStrengthMatchedSemifinals(command)
				require.ErrorIs(t, planErr, arena.ErrInvalidSemifinalBracket)
				require.Equal(t, arena.SemifinalBracket{}, bracket)
			})
		}
	})

	t.Run("exports predecessor lineage and keeps a successor locked", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(8600), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		first, err := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(8601), RevisionNo: 1, Top4: top4,
			SeriesIDs: [2]uuid.UUID{task051ID(8602), task051ID(8603)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		command := arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(8604), RevisionNo: 2, Previous: &first, Top4: top4,
			SeriesIDs: [2]uuid.UUID{task051ID(8602), task051ID(8603)},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		second, err := arena.PlanStrengthMatchedSemifinals(command)
		require.NoError(t, err)
		require.True(t, second.Locked())
		require.Equal(t, first.LockedAt(), second.LockedAt())
		require.Equal(t, first.Semifinals(), second.Semifinals())
		require.Contains(t, second.Dependencies(), domain.ArenaRevisionDependency{
			SourceRevisionID:  first.Projection().Revision().ID(),
			DerivedRevisionID: second.Projection().Revision().ID(),
		})
		graph, err := domain.NewArenaRevisionGraph(
			[]domain.ArenaProjectionRevision{top4.Projection(), first.Projection(), second.Projection()},
			append(first.Dependencies(), second.Dependencies()...),
		)
		require.NoError(t, err)
		require.True(t, graph.DependsOn(second.Projection().Revision().ID(), first.Projection().Revision().ID()))

		command.SeriesIDs = [2]uuid.UUID{task051ID(8605), task051ID(8606)}
		invalid, invalidErr := arena.PlanStrengthMatchedSemifinals(command)
		require.Equal(t, arena.SemifinalBracket{}, invalid)
		require.ErrorIs(t, invalidErr, arena.ErrInvalidSemifinalBracket)

		command.SeriesIDs = [2]uuid.UUID{task051ID(8603), task051ID(8602)}
		invalid, invalidErr = arena.PlanStrengthMatchedSemifinals(command)
		require.Equal(t, arena.SemifinalBracket{}, invalid)
		require.ErrorIs(t, invalidErr, arena.ErrInvalidSemifinalBracket)

		command.SeriesIDs = [2]uuid.UUID{task051ID(8602), task051ID(8603)}
		command.RevisionID = domain.ArenaDerivedRevisionID(first.Semifinals()[0].Series.ID)
		invalid, invalidErr = arena.PlanStrengthMatchedSemifinals(command)
		require.Equal(t, arena.SemifinalBracket{}, invalid)
		require.ErrorIs(t, invalidErr, arena.ErrInvalidSemifinalBracket)

		command.RevisionID = task051RevisionID(8604)
		command.SeriesIDs = [2]uuid.UUID{task051ID(8602), task051ID(8603)}
		frozen, freezeErr := arena.PlanStrengthMatchedSemifinals(command)
		require.NoError(t, freezeErr)
		command.SeriesIDs[0] = uuid.Nil
		command.Previous = nil
		require.NoError(t, frozen.Validate())
		require.Equal(t, task051ID(8602), frozen.Semifinals()[0].Series.ID)
	})

	t.Run("keeps a long bracket successor chain bounded and locked", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14000), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		current, err := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14001), RevisionNo: 1, Top4: top4,
			SeriesIDs: [2]uuid.UUID{task051ID(14050), task051ID(14051)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		lockedAt := current.LockedAt()
		payloadLimit := len(current.Projection().Payload()) + 128
		for revisionNo := 2; revisionNo <= 48; revisionNo++ {
			command := arena.SemifinalBracketCommand{
				TournamentID: fixture.command.TournamentID,
				RevisionID:   task051RevisionID(14100 + revisionNo), RevisionNo: revisionNo,
				Previous: &current, Top4: top4,
				SeriesIDs: [2]uuid.UUID{task051ID(14050), task051ID(14051)},
				CreatedAt: fixture.command.CreatedAt.Add(time.Duration(revisionNo+1) * time.Minute),
			}
			current, err = arena.PlanStrengthMatchedSemifinals(command)
			require.NoError(t, err)
			require.NoError(t, current.Validate())
			require.Equal(t, lockedAt, current.LockedAt())
			require.LessOrEqual(t, len(current.Projection().Payload()), payloadLimit)
		}
	})

	t.Run("rejects transitive bracket and nested Top 4 identity reuse", func(t *testing.T) {
		fixture := task051SwissFixture(t, false)
		final, err := arena.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		firstTop4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14200), RevisionNo: 1,
			Source: final, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		currentTop4 := firstTop4
		for revisionNo := 2; revisionNo <= 3; revisionNo++ {
			currentTop4, err = arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
				TournamentID: fixture.command.TournamentID,
				RevisionID:   task051RevisionID(14200 + revisionNo), RevisionNo: revisionNo,
				Previous: &currentTop4, Source: final,
				CurrentTerminalSeries: task051ResultHeads(fixture.command),
				CreatedAt:             fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute),
			})
			require.NoError(t, err)
		}
		initial, initialErr := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14210), RevisionNo: 1, Top4: currentTop4,
			SeriesIDs: [2]uuid.UUID{
				firstTop4.Projection().Revision().ID().UUID(), task051ID(14211),
			},
			CreatedAt: fixture.command.CreatedAt.Add(4 * time.Minute),
		})
		require.Equal(t, arena.SemifinalBracket{}, initial)
		require.ErrorIs(t, initialErr, arena.ErrInvalidSemifinalBracket)

		initial, initialErr = arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   firstTop4.Projection().Revision().ID(), RevisionNo: 1, Top4: currentTop4,
			SeriesIDs: [2]uuid.UUID{task051ID(14212), task051ID(14213)},
			CreatedAt: fixture.command.CreatedAt.Add(4 * time.Minute),
		})
		require.Equal(t, arena.SemifinalBracket{}, initial)
		require.ErrorIs(t, initialErr, arena.ErrInvalidSemifinalBracket)

		firstFinal := final
		currentFinal := firstFinal
		for revisionNo := 2; revisionNo <= 4; revisionNo++ {
			command := fixture.command
			command.RevisionID = task051RevisionID(14300 + revisionNo)
			command.RevisionNo = revisionNo
			command.Previous = &currentFinal
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			currentFinal, err = arena.PlanFinalSwissProjection(command)
			require.NoError(t, err)
		}
		nestedTop4, err := arena.PlanTop4Snapshot(arena.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14310), RevisionNo: 1,
			Source: currentFinal, CurrentTerminalSeries: task051ResultHeads(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(5 * time.Minute),
		})
		require.NoError(t, err)
		initial, initialErr = arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14311), RevisionNo: 1, Top4: nestedTop4,
			SeriesIDs: [2]uuid.UUID{
				firstFinal.Projection().Revision().ID().UUID(), task051ID(14312),
			},
			CreatedAt: fixture.command.CreatedAt.Add(6 * time.Minute),
		})
		require.Equal(t, arena.SemifinalBracket{}, initial)
		require.ErrorIs(t, initialErr, arena.ErrInvalidSemifinalBracket)

		first, err := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14220), RevisionNo: 1, Top4: currentTop4,
			SeriesIDs: [2]uuid.UUID{task051ID(14221), task051ID(14222)},
			CreatedAt: fixture.command.CreatedAt.Add(4 * time.Minute),
		})
		require.NoError(t, err)
		second, err := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   task051RevisionID(14223), RevisionNo: 2, Previous: &first, Top4: currentTop4,
			SeriesIDs: [2]uuid.UUID{task051ID(14221), task051ID(14222)},
			CreatedAt: fixture.command.CreatedAt.Add(5 * time.Minute),
		})
		require.NoError(t, err)
		aliased, aliasErr := arena.PlanStrengthMatchedSemifinals(arena.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   first.Projection().Revision().ID(), RevisionNo: 3, Previous: &second,
			Top4: currentTop4, SeriesIDs: [2]uuid.UUID{task051ID(14221), task051ID(14222)},
			CreatedAt: fixture.command.CreatedAt.Add(6 * time.Minute),
		})
		require.Equal(t, arena.SemifinalBracket{}, aliased)
		require.ErrorIs(t, aliasErr, arena.ErrInvalidSemifinalBracket)
	})
}
