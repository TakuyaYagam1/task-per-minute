package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaExecutionSchemaSource(t *testing.T) {
	schemas := loadArenaSchemaSource(t)

	requireArenaEnum(t, schemas, "ArenaSeriesFormat", []string{"bo1", "bo3"})
	requireArenaEnum(t, schemas, "ArenaSeriesState", []string{
		"planned", "locked", "draft", "ready", "active", "replay_required",
		"technical_pause", "completed", "cancelled",
	})
	requireArenaEnum(t, schemas, "ArenaGameState", []string{
		"planned", "ready", "active", "paused", "completed", "void", "cancelled", "superseded",
	})
	requireArenaEnum(t, schemas, "ArenaGameResultReason", []string{
		"solved", "surrender", "operator_forfeit", "no_solve", "task_failure",
		"common_platform_failure", "disconnect", "execution_epoch_break", "no_show",
		"series_cancelled", "tournament_cancelled", "derived_revision_superseded",
	})
	requireArenaEnum(t, schemas, "ArenaSeriesResultReason", []string{
		"score_complete", "operator_correction", "series_cancelled", "tournament_cancelled",
	})
	requireArenaEnum(t, schemas, "ArenaCategoryMode", []string{"random", "admin", "draft"})
	requireArenaEnum(t, schemas, "ArenaDraftState", []string{"active", "completed"})
	requireArenaEnum(t, schemas, "ArenaDraftActionType", []string{"ban", "pick"})
	requireArenaEnum(t, schemas, "ArenaTaskKind", []string{"normal", "golden"})
	requireArenaEnum(t, schemas, "ArenaResultActorKind", []string{"server", "operator"})
	requireArenaEnum(t, schemas, "ArenaOfficialResultSubjectKind", []string{"game", "series"})
	requireArenaEnum(t, schemas, "ArenaSeriesScoreRevisionOperation", []string{
		"initialize", "append_attempt", "replace_result",
	})

	requireArenaObject(t, schemas, "ArenaGame", []string{
		"id", "slot_id", "attempt_no", "state", "result_reason", "winner_id", "result_revision_id",
	})
	requireArenaMinimum(t, schemas, "ArenaGame", "attempt_no", 1)
	requireArenaRef(t, schemas, "ArenaGame", "state", "#/ArenaGameState")
	requireArenaNullableRef(t, schemas, "ArenaGame", "result_reason", "#/ArenaGameResultReason")
	requireArenaNullable(t, schemas, "ArenaGame", "winner_id")
	requireArenaNullable(t, schemas, "ArenaGame", "result_revision_id")

	requireArenaObject(t, schemas, "ArenaGameSlot", []string{
		"id", "series_id", "position", "category", "score_before", "attempts",
	})
	requireArenaArrayRef(t, schemas, "ArenaGameSlot", "attempts", "#/ArenaGame", 0, 0)
	requireArenaObject(t, schemas, "ArenaSeries", []string{
		"id", "tournament_id", "first_participant_id", "second_participant_id", "format", "state",
		"score", "winner_id", "slots", "current_score_revision_id", "current_result_revision_id",
	})
	requireArenaRef(t, schemas, "ArenaSeries", "format", "#/ArenaSeriesFormat")
	requireArenaRef(t, schemas, "ArenaSeries", "state", "#/ArenaSeriesState")
	requireArenaNullable(t, schemas, "ArenaSeries", "winner_id")
	requireArenaNullable(t, schemas, "ArenaSeries", "current_score_revision_id")
	requireArenaNullable(t, schemas, "ArenaSeries", "current_result_revision_id")

	requireArenaObject(t, schemas, "ArenaDraftTurn", []string{"number", "actor_id", "action", "deadline"})
	requireArenaObject(t, schemas, "ArenaDraftAction", []string{
		"turn", "actor_id", "action", "category", "occurred_at", "turn_deadline",
	})
	requireArenaObject(t, schemas, "ArenaDraft", []string{
		"id", "series_id", "format", "first_participant_id", "second_participant_id", "pool",
		"state", "turn", "turn_deadline", "actions", "selected_categories",
	})
	requireArenaNullable(t, schemas, "ArenaDraft", "turn_deadline")

	requireArenaObject(t, schemas, "ArenaTaskSnapshot", []string{
		"snapshot_id", "task_id", "version", "kind", "title", "description", "category",
		"difficulty", "time_limit", "hints",
	})
	require.NotContains(t, requireArenaSchema(t, schemas, "ArenaTaskSnapshot").Properties, "flag")
	requireArenaObject(t, schemas, "ArenaDeliveryReceipt", []string{
		"id", "assignment_id", "attempt_id", "participant_id", "snapshot_id", "task_id", "delivered_at",
	})
	requireArenaObject(t, schemas, "ArenaAssignment", []string{
		"id", "attempt_id", "participant_ids", "active_snapshot", "undisclosed_reserve_count", "receipts",
	})
	require.NotContains(t, requireArenaSchema(t, schemas, "ArenaAssignment").Properties, "reserve_snapshots")

	requireArenaObject(t, schemas, "ArenaSubmissionRequest", []string{"command_id", "participant_id", "submitted_flag"})
	requireArenaObject(t, schemas, "ArenaSubmissionRecord", []string{
		"command_id", "participant_id", "sequence", "committed_at", "correct", "snapshot_id", "task_id", "content_digest",
	})
	require.NotContains(t, requireArenaSchema(t, schemas, "ArenaSubmissionRecord").Properties, "submitted_flag")

	requireArenaObject(t, schemas, "ArenaOfficialResultRevision", []string{
		"id", "previous_revision_id", "ordinal", "command_id", "subject_kind", "tournament_id", "series_id",
		"game_id", "actor_kind", "actor_id", "game_state", "game_reason", "series_state",
		"series_reason", "winner_id", "score_revision_id", "source_projection_revision_id", "recorded_at",
	})
	requireArenaNullable(t, schemas, "ArenaOfficialResultRevision", "previous_revision_id")
	requireArenaObject(t, schemas, "ArenaSeriesScoreRevision", []string{
		"id", "previous_revision_id", "ordinal", "operation", "command_id", "actor_kind", "actor_id",
		"tournament_id", "series_id",
		"first_participant_id", "second_participant_id", "format", "score", "attempts",
		"source_projection_revision_id", "recorded_at",
	})
	requireArenaNullable(t, schemas, "ArenaSeriesScoreRevision", "previous_revision_id")
}
