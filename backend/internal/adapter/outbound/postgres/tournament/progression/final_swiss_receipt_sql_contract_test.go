package progression

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFinalSwissReceiptPersistsExactCanonicalPredecessorChain(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_progression.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.final_swiss_projection_receipts",
		"CREATE TABLE public.final_swiss_projection_receipt_participants",
		"CREATE TABLE public.final_swiss_projection_receipt_rounds",
		"CREATE TABLE public.final_swiss_projection_receipt_series",
		"CREATE TABLE public.final_swiss_projection_receipt_games",
		"CREATE TABLE public.final_swiss_projection_receipt_ledger_entries",
		"previous_receipt_projection_revision_id uuid",
		"canonical_projection_id uuid NOT NULL",
		"receipt_revision bigint NOT NULL",
		"receipt_revision = 1 AND previous_receipt_projection_revision_id IS NULL",
		"predecessor_receipt_revision <> NEW.receipt_revision - 1",
		"source_revision_number <= predecessor_physical_revision_number",
		"INTO expected_games",
		"INTO retained_games",
		"retained_games <> expected_games",
		"series.current_result_revision_id IS DISTINCT FROM receipt_series.series_result_revision_id",
		"series.current_score_revision_id IS DISTINCT FROM receipt_series.score_revision_id",
		"attempt.result_revision_id IS DISTINCT FROM receipt_game.game_result_revision_id",
		"source_standings_artifact_id uuid NOT NULL",
		"final_swiss_projection_receipt_chain_guard",
		"previous canonical Final Swiss receipt is stale or incomplete",
		"-- name: LockTournamentProgressionFinalSwissReceiptChain :many",
		"projection_revision.revision_number AS physical_projection_revision",
		"artifact.payload AS source_standings_payload",
		"FOR KEY SHARE OF receipt, projection_revision, membership, artifact",
		"-- name: LockTournamentProgressionFinalSwissReceiptParticipants :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptRounds :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptSeries :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptGames :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptLedgerEntries :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptSeriesEvidence :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptScoreRevisionAttempts :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudications :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptGameEvidence :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptNormalNoShowCommits :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptOperatorForfeitCommits :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptProjectionNodes :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptProjectionDependencies :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptLedger :many",
		"WITH RECURSIVE receipt_chain",
		"FOR KEY SHARE OF receipt",
	} {
		require.Contains(t, string(schema)+"\n"+string(queries), fragment)
	}
	require.NotContains(t, string(schema), "source_revision_number <> NEW.receipt_revision")
}

func TestFinalSwissFirstReceiptMayUsePhysicalRevisionAboveOne(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_progression.sql"))
	require.NoError(t, err)

	require.Contains(t, string(schema), "FOREIGN KEY (projection_revision_id, tournament_id, roster_id)")
	require.Contains(t, string(schema), "REFERENCES public.projection_revisions(id, tournament_id, roster_id)")
	require.Contains(t, string(queries), "receipt.receipt_revision")
	require.Contains(t, string(queries), "ORDER BY receipt.receipt_revision")
	require.NotContains(t, string(schema), "source_previous_revision_id IS NOT NULL")
}

func TestCorrectionFinalSwissReceiptBridgeRequiresExactPlayoffLineage(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"progression.action = 'start_playoffs'",
		"progression.source_projection_revision_id = NEW.previous_receipt_projection_revision_id",
		"bridge.previous_revision_id = NEW.previous_receipt_projection_revision_id",
		"bridge.revision_number = predecessor_physical_revision_number + 1",
		"bridge.state = 'superseded'",
		"bridge.superseded_by_revision_id = NEW.projection_revision_id",
		"predecessor_replacement_id = bridge.id",
		"source_previous_revision_id = bridge.id",
		"source_revision_number = bridge.revision_number + 1",
		"bridged_playoff_receipt IS DISTINCT FROM TRUE",
	} {
		require.Contains(t, string(schema), fragment)
	}
}

func TestCorrectedGoldenGroupsBindResultingProjectionAuthority(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations", "000003_tournament_schema.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"WHEN action IN ('correction_start_golden', 'correction_refresh_golden')",
		"THEN resulting_projection_revision_id",
		"THEN resulting_projection_revision",
		"Golden tie evidence must match its stage progression source",
	} {
		require.Contains(t, string(schema), fragment)
	}
}

func TestResultProjectionNodeAuthorityHasExactDurableOrigin(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "result_correction.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"CREATE TABLE public.result_projection_node_authorities",
		"source_kind = 'result_commit'",
		"source_kind = 'correction_commit'",
		"source_kind = 'wave_initialization'",
		"source_kind = 'stage_initialization'",
		"stage_command_id uuid",
		"result_projection_node_authorities_result_commit_fk",
		"result_projection_node_authorities_correction_command_fk",
		"result_projection_node_authorities_wave_fk",
		"result_projection_node_authorities_stage_fk",
		"Wave provenance is valid only for its exact score genesis node",
		"stage provenance is valid only for exact playoff publication genesis nodes",
		"result projection dependency crosses provenance authority",
		"result projection dependency must remain acyclic",
		"WITH RECURSIVE descendants",
		"-- name: CreateResultProjectionNodeAuthority :exec",
		"authority_id",
	} {
		require.Contains(t, string(schema)+"\n"+string(queries), fragment)
	}
}

func TestStageProjectionProvenanceBindsExactArtifactsAndSemifinalGenesis(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations", "000011_projection_schema.sql"))
	require.NoError(t, err)
	queries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_progression.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"top4_node_id uuid NOT NULL",
		"bracket_node_id uuid NOT NULL",
		"tournament_stage_playoff_evidence_top4_node_fk",
		"tournament_stage_playoff_evidence_bracket_node_fk",
		"stage playoff evidence does not bind exact logical artifact authority",
		"stage playoff provenance does not cover exact publication and semifinal genesis graph",
		"NEW.artifact_kind = 'top_four' AND NEW.entity_id = NEW.tournament_id",
		"NEW.artifact_kind = 'bracket' AND NEW.entity_id = NEW.tournament_id",
		"CreateTournamentProgressionStageProjectionNodeAuthority",
		"CreateTournamentProgressionStageProjectionNode",
		"CreateTournamentProgressionStageProjectionDependency",
	} {
		require.Contains(t, string(schema)+"\n"+string(queries), fragment)
	}
}
