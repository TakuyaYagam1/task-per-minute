package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaSwissSchemaSource(t *testing.T) {
	schemas := loadArenaSchemaSource(t)

	requireArenaEnum(t, schemas, "ArenaArtifactKind", []string{
		"game_result", "series_score", "series_result", "standings", "golden_group",
		"top_four", "bracket", "champion",
	})
	requireArenaEnum(t, schemas, "ArenaSwissPointsLabel", []string{"provisional", "final"})
	requireArenaEnum(t, schemas, "ArenaSwissBuchholzStatus", []string{"provisional", "final"})
	requireArenaEnum(t, schemas, "ArenaSemifinalWinnerPath", []string{"final"})
	requireArenaEnum(t, schemas, "ArenaSemifinalLoserPath", []string{"eliminated"})

	requireArenaObject(t, schemas, "ArenaProjectionRevision", []string{
		"id", "tournament_id", "artifact_kind", "artifact_id", "revision_no",
		"payload_digest", "created_at",
	})
	requireArenaMinimum(t, schemas, "ArenaProjectionRevision", "revision_no", 1)
	requireArenaRef(t, schemas, "ArenaProjectionRevision", "artifact_kind", "#/ArenaArtifactKind")
	requireArenaNullable(t, schemas, "ArenaProjectionRevision", "previous_revision_id")
	require.True(t, requireArenaProperty(t, schemas, "ArenaProjectionRevision", "id").ReadOnly)
	require.True(t, requireArenaProperty(t, schemas, "ArenaProjectionRevision", "payload_digest").ReadOnly)
	requireArenaObject(t, schemas, "ArenaProjectionDependency", []string{
		"source_revision_id", "derived_revision_id",
	})

	requireArenaObject(t, schemas, "ArenaSwissPairingEvidence", []string{
		"id", "purpose", "algorithm_version", "normalized_inputs", "result", "replay_digest", "owner_id", "decided_at",
	})
	require.NotContains(t, requireArenaSchema(t, schemas, "ArenaSwissPairingEvidence").Properties, "seed")
	requireArenaObject(t, schemas, "ArenaSwissPairing", []string{
		"id", "round_id", "first_participant_id", "second_participant_id", "evidence_id",
	})
	requireArenaObject(t, schemas, "ArenaSwissBye", []string{
		"id", "round_id", "participant_id", "points_awarded", "revision_id", "evidence_id",
	})
	requireArenaObject(t, schemas, "ArenaSwissRound", []string{
		"id", "tournament_id", "round_number", "revision", "roster_participant_ids",
		"pairings", "standings", "locked", "created_at", "updated_at",
	})
	requireArenaMinimum(t, schemas, "ArenaSwissRound", "round_number", 1)
	requireArenaMinimum(t, schemas, "ArenaSwissRound", "revision", 1)
	requireArenaUUIDArray(t, schemas, "ArenaSwissRound", "roster_participant_ids", 4, 16)
	requireArenaArrayRef(t, schemas, "ArenaSwissRound", "pairings", "#/ArenaSwissPairing", 1, 8)
	requireArenaArrayRef(t, schemas, "ArenaSwissRound", "standings", "#/ArenaSwissStanding", 4, 16)

	requireArenaObject(t, schemas, "ArenaSwissStanding", []string{
		"participant_id", "position", "points", "points_label", "buchholz", "buchholz_status",
		"head_to_head_points", "head_to_head_applied", "effective_time_ms", "stable_seed",
	})
	requireArenaMinimum(t, schemas, "ArenaSwissStanding", "position", 1)
	requireArenaMinimum(t, schemas, "ArenaSwissStanding", "stable_seed", 1)
	requireArenaRef(t, schemas, "ArenaSwissStanding", "points_label", "#/ArenaSwissPointsLabel")
	requireArenaRef(t, schemas, "ArenaSwissStanding", "buchholz_status", "#/ArenaSwissBuchholzStatus")

	requireArenaObject(t, schemas, "ArenaGoldenSeed", []string{
		"group_id", "revision_id", "source_projection_revision_id", "position_from", "position_to", "participant_ids",
	})
	requireArenaUUIDArray(t, schemas, "ArenaGoldenSeed", "participant_ids", 2, 16)
	requireArenaObject(t, schemas, "ArenaSwissStandingsProjection", []string{
		"revision", "participant_ids", "standings", "golden_seeds", "dependencies",
	})
	requireArenaUUIDArray(t, schemas, "ArenaSwissStandingsProjection", "participant_ids", 4, 16)
	requireArenaArrayRef(t, schemas, "ArenaSwissStandingsProjection", "standings", "#/ArenaSwissStanding", 4, 16)
	requireArenaArrayRef(t, schemas, "ArenaSwissStandingsProjection", "golden_seeds", "#/ArenaGoldenSeed", 0, 0)

	requireArenaObject(t, schemas, "ArenaTop4Participant", []string{"seed", "participant_id"})
	requireArenaMinimum(t, schemas, "ArenaTop4Participant", "seed", 1)
	requireArenaObject(t, schemas, "ArenaTop4Projection", []string{
		"revision", "source_standings_revision_id", "participants", "dependencies",
	})
	requireArenaArrayRef(t, schemas, "ArenaTop4Projection", "participants", "#/ArenaTop4Participant", 4, 4)

	requireArenaObject(t, schemas, "ArenaSemifinalMatch", []string{
		"position", "series_id", "first_participant_id", "second_participant_id", "winner_path", "loser_path",
	})
	requireArenaRef(t, schemas, "ArenaSemifinalMatch", "winner_path", "#/ArenaSemifinalWinnerPath")
	requireArenaRef(t, schemas, "ArenaSemifinalMatch", "loser_path", "#/ArenaSemifinalLoserPath")
	requireArenaObject(t, schemas, "ArenaSemifinalAdvancement", []string{
		"position", "series_id", "winner_id", "loser_id", "score_revision_id", "result_revision_id",
	})
	requireArenaObject(t, schemas, "ArenaElimination", []string{
		"participant_id", "series_id", "result_revision_id", "eliminated_at_stage",
	})
	requireArenaObject(t, schemas, "ArenaSemifinalBracketProjection", []string{
		"revision", "top_four_revision_id", "semifinals", "advancements", "eliminations", "dependencies", "locked_at",
	})
	requireArenaArrayRef(t, schemas, "ArenaSemifinalBracketProjection", "semifinals", "#/ArenaSemifinalMatch", 2, 2)

	requireArenaObject(t, schemas, "ArenaFinalProjection", []string{
		"revision", "bracket_revision_id", "series_id", "participant_ids", "score_revision_id",
		"result_revision_id", "winner_id", "loser_id", "dependencies",
	})
	requireArenaUUIDArray(t, schemas, "ArenaFinalProjection", "participant_ids", 2, 2)
	requireArenaObject(t, schemas, "ArenaChampionProjection", []string{
		"revision", "final_revision_id", "series_id", "champion_id", "runner_up_id",
		"score_revision_id", "result_revision_id", "dependencies",
	})

	for schemaName, propertyNames := range map[string][]string{
		"ArenaSwissStandingsProjection":   {"golden_seeds", "dependencies"},
		"ArenaTop4Projection":             {"dependencies"},
		"ArenaSemifinalBracketProjection": {"advancements", "eliminations", "dependencies"},
		"ArenaFinalProjection":            {"dependencies"},
		"ArenaChampionProjection":         {"dependencies"},
	} {
		for _, propertyName := range propertyNames {
			require.Nil(t, requireArenaProperty(t, schemas, schemaName, propertyName).MinItems,
				schemaName+"."+propertyName+" must allow an empty array")
		}
	}
}
