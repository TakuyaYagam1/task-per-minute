package playoff_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestStrengthMatchedSemifinals(t *testing.T) {
	t.Parallel()

	t.Run("locks BO1 seed 1v2 and 3v4 with single-elimination paths", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(5201), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)

		command := playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(5202), RevisionNo: 1,
			Top4:      top4,
			SeriesIDs: [2]uuid.UUID{semifinalID(1), semifinalID(2)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		bracket, err := playoff.PlanStrengthMatchedSemifinals(command)
		require.NoError(t, err)
		require.NoError(t, bracket.Validate())
		require.True(t, bracket.Locked())
		require.False(t, bracket.HasLowerBracket())
		require.Equal(t, command.CreatedAt, bracket.LockedAt())
		require.Equal(t, domain.ArtifactKindBracket, bracket.Projection().Revision().Artifact().Kind)
		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(bracket.Projection().Payload(), &payload))
		require.Contains(t, payload, "rounds")
		require.NotContains(t, payload, "semifinals")
		require.Equal(t, []domain.RevisionDependency{{
			SourceRevisionID:  playoffRevisionID(5201),
			DerivedRevisionID: playoffRevisionID(5202),
		}}, bracket.Dependencies())
		require.Equal(t,
			"dab42d8e91e5b3155f34cc367f3e2ff15eaa0fafa4fa0f31d97f4065f4e9612f",
			projectionDigestHex(bracket.Projection()),
		)

		participants := top4.Participants()
		matches := bracket.Semifinals()
		require.Len(t, matches, 2)
		require.Equal(t, participants[0].ParticipantID, matches[0].Series.FirstParticipantID)
		require.Equal(t, participants[1].ParticipantID, matches[0].Series.SecondParticipantID)
		require.Equal(t, participants[2].ParticipantID, matches[1].Series.FirstParticipantID)
		require.Equal(t, participants[3].ParticipantID, matches[1].Series.SecondParticipantID)
		for index, match := range matches {
			require.Equal(t, index+1, match.Position)
			require.Equal(t, domain.SeriesFormatBO1, match.Series.Format)
			require.Equal(t, domain.SeriesStateLocked, match.Series.State)
			require.Equal(t, playoff.SemifinalWinnerToFinal, match.WinnerPath)
			require.Equal(t, playoff.SemifinalLoserEliminated, match.LoserPath)
			require.Empty(t, match.Series.Slots)
			require.Nil(t, match.Series.WinnerID)
			require.Nil(t, match.Series.CurrentScoreRevisionID)
			require.Nil(t, match.Series.CurrentResultRevisionID)
		}

		matches[0].Series.FirstParticipantID = uuid.Nil
		require.Equal(t, participants[0].ParticipantID, bracket.Semifinals()[0].Series.FirstParticipantID)
	})

	t.Run("rejects duplicate Series aliases stale lineage and overflow", func(t *testing.T) {
		t.Parallel()

		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(810), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)

		base := playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(811), RevisionNo: 1,
			Top4:      top4,
			SeriesIDs: [2]uuid.UUID{playoffID(812), playoffID(813)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		}
		tests := []struct {
			name   string
			mutate func(*playoff.SemifinalBracketCommand)
		}{
			{name: "duplicate Series", mutate: func(command *playoff.SemifinalBracketCommand) {
				command.SeriesIDs[1] = command.SeriesIDs[0]
			}},
			{name: "Series aliases participant", mutate: func(command *playoff.SemifinalBracketCommand) {
				command.SeriesIDs[0] = command.Top4.Participants()[0].ParticipantID
			}},
			{name: "Series aliases terminal evidence", mutate: func(command *playoff.SemifinalBracketCommand) {
				command.SeriesIDs[0] = command.Top4.TerminalSeriesReferences()[0].ProjectionRevisionID.UUID()
			}},
			{name: "bracket predates Top 4", mutate: func(command *playoff.SemifinalBracketCommand) {
				command.CreatedAt = command.Top4.Projection().Revision().CreatedAt().Add(-time.Nanosecond)
			}},
			{name: "wrong tournament", mutate: func(command *playoff.SemifinalBracketCommand) {
				command.TournamentID = playoffID(999)
			}},
			{name: "revision overflow", mutate: func(command *playoff.SemifinalBracketCommand) {
				command.RevisionNo = math.MaxInt
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command := base
				test.mutate(&command)
				bracket, planErr := playoff.PlanStrengthMatchedSemifinals(command)
				require.ErrorIs(t, planErr, playoff.ErrInvalidSemifinalBracket)
				require.Equal(t, playoff.SemifinalBracket{}, bracket)
			})
		}
	})

	t.Run("exports predecessor lineage and freezes a successor", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(8600), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		first, err := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(8601), RevisionNo: 1, Top4: top4,
			SeriesIDs: [2]uuid.UUID{playoffID(8602), playoffID(8603)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		command := playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(8604), RevisionNo: 2, Previous: &first, Top4: top4,
			SeriesIDs: [2]uuid.UUID{playoffID(8602), playoffID(8603)},
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		}
		second, err := playoff.PlanStrengthMatchedSemifinals(command)
		require.NoError(t, err)
		require.True(t, second.Locked())
		require.Equal(t, first.LockedAt(), second.LockedAt())
		require.Equal(t, first.Semifinals(), second.Semifinals())
		require.Contains(t, second.Dependencies(), domain.RevisionDependency{
			SourceRevisionID: first.Projection().Revision().ID(), DerivedRevisionID: second.Projection().Revision().ID(),
		})
		graph, err := domain.NewRevisionGraph(
			[]domain.ProjectionRevision{top4.Projection(), first.Projection(), second.Projection()},
			append(first.Dependencies(), second.Dependencies()...),
		)
		require.NoError(t, err)
		require.True(t, graph.DependsOn(second.Projection().Revision().ID(), first.Projection().Revision().ID()))

		for name, mutate := range map[string]func(*playoff.SemifinalBracketCommand){
			"changed Series identities": func(value *playoff.SemifinalBracketCommand) {
				value.SeriesIDs = [2]uuid.UUID{playoffID(8605), playoffID(8606)}
			},
			"reordered Series identities": func(value *playoff.SemifinalBracketCommand) {
				value.SeriesIDs = [2]uuid.UUID{playoffID(8603), playoffID(8602)}
			},
			"revision aliases Series": func(value *playoff.SemifinalBracketCommand) {
				value.RevisionID = domain.DerivedRevisionID(first.Semifinals()[0].Series.ID)
			},
		} {
			t.Run(name, func(t *testing.T) {
				invalidCommand := command
				mutate(&invalidCommand)
				invalid, invalidErr := playoff.PlanStrengthMatchedSemifinals(invalidCommand)
				require.Equal(t, playoff.SemifinalBracket{}, invalid)
				require.ErrorIs(t, invalidErr, playoff.ErrInvalidSemifinalBracket)
			})
		}

		frozen, err := playoff.PlanStrengthMatchedSemifinals(command)
		require.NoError(t, err)
		command.SeriesIDs[0] = uuid.Nil
		command.Previous = nil
		require.NoError(t, frozen.Validate())
		require.Equal(t, playoffID(8602), frozen.Semifinals()[0].Series.ID)
	})

	t.Run("keeps a long successor chain bounded and locked", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14000), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		current, err := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14001), RevisionNo: 1, Top4: top4,
			SeriesIDs: [2]uuid.UUID{playoffID(14050), playoffID(14051)},
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		lockedAt := current.LockedAt()
		payloadLimit := len(current.Projection().Payload()) + 128
		for revisionNo := 2; revisionNo <= 48; revisionNo++ {
			current, err = playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
				TournamentID: fixture.command.TournamentID,
				RevisionID:   playoffRevisionID(14100 + revisionNo), RevisionNo: revisionNo,
				Previous: &current, Top4: top4,
				SeriesIDs: [2]uuid.UUID{playoffID(14050), playoffID(14051)},
				CreatedAt: fixture.command.CreatedAt.Add(time.Duration(revisionNo+1) * time.Minute),
			})
			require.NoError(t, err)
			require.NoError(t, current.Validate())
			require.Equal(t, lockedAt, current.LockedAt())
			require.LessOrEqual(t, len(current.Projection().Payload()), payloadLimit)
		}
	})

	t.Run("rejects transitive bracket and nested Top 4 identity reuse", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		firstTop4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14200), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		currentTop4 := firstTop4
		for revisionNo := 2; revisionNo <= 3; revisionNo++ {
			currentTop4, err = playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
				TournamentID: fixture.command.TournamentID,
				RevisionID:   playoffRevisionID(14200 + revisionNo), RevisionNo: revisionNo,
				Previous: &currentTop4, Source: finalSwiss,
				CurrentTerminalSeries: terminalEvidence(fixture.command),
				CreatedAt:             fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute),
			})
			require.NoError(t, err)
		}

		for name, command := range map[string]playoff.SemifinalBracketCommand{
			"Series reuses retained Top 4 revision": {
				TournamentID: fixture.command.TournamentID,
				RevisionID:   playoffRevisionID(14210), RevisionNo: 1, Top4: currentTop4,
				SeriesIDs: [2]uuid.UUID{firstTop4.Projection().Revision().ID().UUID(), playoffID(14211)},
				CreatedAt: fixture.command.CreatedAt.Add(4 * time.Minute),
			},
			"bracket reuses retained Top 4 revision": {
				TournamentID: fixture.command.TournamentID,
				RevisionID:   firstTop4.Projection().Revision().ID(), RevisionNo: 1, Top4: currentTop4,
				SeriesIDs: [2]uuid.UUID{playoffID(14212), playoffID(14213)},
				CreatedAt: fixture.command.CreatedAt.Add(4 * time.Minute),
			},
		} {
			t.Run(name, func(t *testing.T) {
				bracket, planErr := playoff.PlanStrengthMatchedSemifinals(command)
				require.Equal(t, playoff.SemifinalBracket{}, bracket)
				require.ErrorIs(t, planErr, playoff.ErrInvalidSemifinalBracket)
			})
		}

		firstFinal := finalSwiss
		currentFinal := firstFinal
		for revisionNo := 2; revisionNo <= 4; revisionNo++ {
			command := fixture.command
			command.RevisionID = playoffRevisionID(14300 + revisionNo)
			command.RevisionNo = revisionNo
			command.PhysicalProjectionRevision = revisionNo
			command.Previous = &currentFinal
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			currentFinal, err = playoff.PlanFinalSwissProjection(command)
			require.NoError(t, err)
		}
		nestedTop4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14310), RevisionNo: 1,
			Source: currentFinal, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(5 * time.Minute),
		})
		require.NoError(t, err)
		initial, initialErr := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14311), RevisionNo: 1, Top4: nestedTop4,
			SeriesIDs: [2]uuid.UUID{firstFinal.Projection().Revision().ID().UUID(), playoffID(14312)},
			CreatedAt: fixture.command.CreatedAt.Add(6 * time.Minute),
		})
		require.Equal(t, playoff.SemifinalBracket{}, initial)
		require.ErrorIs(t, initialErr, playoff.ErrInvalidSemifinalBracket)

		first, err := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14220), RevisionNo: 1, Top4: currentTop4,
			SeriesIDs: [2]uuid.UUID{playoffID(14221), playoffID(14222)},
			CreatedAt: fixture.command.CreatedAt.Add(4 * time.Minute),
		})
		require.NoError(t, err)
		second, err := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(14223), RevisionNo: 2, Previous: &first, Top4: currentTop4,
			SeriesIDs: [2]uuid.UUID{playoffID(14221), playoffID(14222)},
			CreatedAt: fixture.command.CreatedAt.Add(5 * time.Minute),
		})
		require.NoError(t, err)
		aliased, aliasErr := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   first.Projection().Revision().ID(), RevisionNo: 3, Previous: &second,
			Top4: currentTop4, SeriesIDs: [2]uuid.UUID{playoffID(14221), playoffID(14222)},
			CreatedAt: fixture.command.CreatedAt.Add(6 * time.Minute),
		})
		require.Equal(t, playoff.SemifinalBracket{}, aliased)
		require.ErrorIs(t, aliasErr, playoff.ErrInvalidSemifinalBracket)
	})
}
