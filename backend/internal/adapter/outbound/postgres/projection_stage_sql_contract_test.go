package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStagePlayoffEvidenceUsesNormalizedProjectionAndGoldenBindings(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.tournament_stage_playoff_evidence",
		"CREATE TABLE public.tournament_stage_playoff_golden_settlements",
		"CREATE TABLE public.tournament_stage_playoff_semifinals",
		"tournament_stage_progressions_source_projection_fk",
		"tournament_stage_progressions_resulting_projection_fk",
		"tournament_stage_playoff_evidence_progression_fk",
		"tournament_stage_playoff_evidence_source_projection_fk",
		"tournament_stage_playoff_evidence_published_projection_fk",
		"tournament_stage_playoff_evidence_top4_artifact_fk",
		"tournament_stage_playoff_evidence_bracket_artifact_fk",
		"tournament_stage_playoff_golden_settlements_group_revision_fk",
		"tournament_stage_playoff_golden_settlements_attempt_group_fk",
		"tournament_stage_playoff_semifinals_series_fk",
		"CREATE FUNCTION public.tournament_stage_playoff_evidence_guard()",
		"CREATE FUNCTION public.validate_tournament_stage_playoff_evidence()",
		"Top4 artifact must contain exactly four normalized members",
		"playoff semifinals do not match the canonical strength ordering",
		"Golden playoff settlements do not cover every terminal group participant",
		"projection_dependencies",
		"golden_position_commit_id",
	} {
		require.Contains(t, string(schema), fragment)
	}
}

func TestStagePlayoffEvidenceAcceptsOnlyExactSupersededSourceLineage(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	// A replacement projection atomically supersedes its source. The source is
	// therefore valid only when its exact replacement is the published result;
	// a source superseded by another revision must fail closed.
	require.Contains(t, string(schema), "source_state = 'superseded'")
	require.Contains(t, string(schema), "source_replacement_revision_id = NEW.published_projection_revision_id")
	require.Contains(t, string(schema), "published_state IS DISTINCT FROM 'published'")
	require.NotContains(t, string(schema), "OR source_state IS DISTINCT FROM 'published'")
}

func TestCorrectionStagePlayoffEvidenceFailsClosedWithoutExactActionAndSeal(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"progression_action NOT IN ('start_playoffs', 'correction_start_playoffs')",
		"expected_golden_count = 0",
		"progression_action IS DISTINCT FROM 'correction_start_playoffs'",
		"golden_correction_stage_tombstones AS stage_tombstone",
		"golden_correction_tombstone_seals AS seal",
		"NOT EXISTS (\n                    SELECT 1\n                    FROM golden_correction_group_tombstones",
		"attempt.state <> 'cancelled'",
		"tombstone.prior_state IN ('planned', 'waiting_ready')",
		"Golden playoff rollback lacks sealed unstarted group evidence",
	} {
		require.Contains(t, string(schema), fragment)
	}
}

func TestCorrectionStageProjectionNodesRequireExactImmediateLineage(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"stage_progression_action IS DISTINCT FROM 'correction_start_playoffs'",
		"stage_correction_lineage IS DISTINCT FROM TRUE",
		"NEW.artifact_kind <> previous_kind",
		"NEW.entity_id <> previous_entity_id",
		"NEW.revision_number <> previous_revision_number + 1",
		"invalid result projection node lineage",
	} {
		require.Contains(t, string(schema), fragment)
	}
}

func TestProjectionSchemaUsesCanonicalTopFourArtifactKind(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	require.Contains(t, string(schema), "artifact_kind IN ('standings', 'bracket', 'top_four')")
	require.Contains(t, string(schema), "artifact.artifact_kind = 'top_four'")
	require.Contains(t, string(schema), "artifact_kind = 'top_four'")
	require.NotContains(t, string(schema), "'top4'")
}

func TestStageProgressionCutoffUsesExactDurableCommandSource(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "projection.sql"))
	require.NoError(t, err)
	source, err := os.ReadFile("projection.go")
	require.NoError(t, err)

	combined := string(schema) + "\n" + string(queries) + "\n" + string(source)
	for _, fragment := range []string{
		"stage_progression_command_id uuid",
		"source_kind)::text = 'stage_progression'::text",
		"projection_cutoffs_stage_progression_fk",
		"FOREIGN KEY (stage_progression_command_id, tournament_id, roster_id)",
		"REFERENCES public.tournament_stage_progressions(command_id, tournament_id, roster_id)",
		"DEFERRABLE INITIALLY DEFERRED",
		"tournament_stage_progressions_scope_key",
		"stage_progression_command_id,",
		"sqlc.arg(stage_progression_command_id)",
		"projectionSourceStageProgression",
		"StageProgressionCommandID",
	} {
		require.Contains(t, combined, fragment)
	}

	getCutoffStart := strings.Index(string(queries), "-- name: GetProjectionCutoffByID :one")
	getCutoffEnd := strings.Index(string(queries)[getCutoffStart:], "-- name:")
	if getCutoffEnd > 0 {
		getCutoffEnd += getCutoffStart
	} else {
		getCutoffEnd = len(queries)
	}
	require.Contains(t, string(queries)[getCutoffStart:getCutoffEnd], "stage_progression_command_id")
}

func TestProjectionSchemaRetainsExactFinalStageHeadsAndContinuationGraphs(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.tournament_stage_playoff_final_advancements",
		"CREATE TABLE public.tournament_stage_playoff_finals",
		"CREATE TABLE public.tournament_stage_playoff_final_initializations",
		"CREATE TABLE public.tournament_stage_playoff_final_progressions",
		"tournament_stage_playoff_final_advancements_score_fk",
		"tournament_stage_playoff_final_advancements_result_fk",
		"tournament_stage_playoff_final_initializations_draft_revision_fk",
		"tournament_stage_playoff_final_progressions_source_score_fk",
		"tournament_stage_playoff_final_progressions_source_result_fk",
		"CREATE FUNCTION public.tournament_stage_playoff_final_advancement_guard()",
		"CREATE FUNCTION public.tournament_stage_playoff_final_initialization_guard()",
		"CREATE FUNCTION public.tournament_stage_playoff_final_progression_guard()",
		"final advancement is not bound to the current terminal semifinal heads",
		"authoritative completed-draft Game 1 graph",
		"game_state IS DISTINCT FROM 'planned'",
		"wave_state IS DISTINCT FROM 'planned'",
		"planned next Game graph",
	} {
		require.Contains(t, string(schema), fragment)
	}
}
