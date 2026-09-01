package v1

import "testing"

func TestArenaPauseSchemaSource(t *testing.T) {
	schemas := loadArenaSchemaSource(t)

	requireArenaEnum(t, schemas, "ArenaWaveState", []string{
		"planned", "ready_window_open", "ready", "active", "paused", "completed",
		"ready_window_expired", "superseded",
	})
	requireArenaEnum(t, schemas, "ArenaReadyWindowState", []string{"open", "consumed", "expired", "superseded"})
	requireArenaEnum(t, schemas, "ArenaReadinessEventType", []string{"ready", "cleared"})
	requireArenaEnum(t, schemas, "ArenaPresenceState", []string{"connected", "disconnected"})
	requireArenaEnum(t, schemas, "ArenaReconnectState", []string{"open", "reconnected", "expired", "cancelled"})
	requireArenaEnum(t, schemas, "ArenaReconnectMutationKind", []string{"reconnect", "timeout", "disconnect"})
	requireArenaEnum(t, schemas, "ArenaPauseReason", []string{"operator", "disconnect", "platform", "execution_epoch"})
	requireArenaEnum(t, schemas, "ArenaPauseState", []string{"active", "resumed", "cancelled"})
	requireArenaEnum(t, schemas, "ArenaPauseDeadlineKind", []string{"ready_window", "game", "draft"})
	requireArenaEnum(t, schemas, "ArenaDraftRecoveryPolicy", []string{"shift_remaining", "fresh_on_resume"})
	requireArenaEnum(t, schemas, "ArenaDraftRecoveryReason", []string{
		"operator_pause", "epoch_mismatch", "service_restart", "correction",
	})

	requireArenaObject(t, schemas, "ArenaReadyWindow", []string{
		"id", "wave_id", "revision_id", "state", "opened_at", "deadline", "consumed_at",
	})
	requireArenaNullable(t, schemas, "ArenaReadyWindow", "consumed_at")
	requireArenaObject(t, schemas, "ArenaWave", []string{
		"id", "tournament_id", "revision_id", "revision", "state", "members", "ready_window",
		"started_at", "paused_at",
	})
	requireArenaNullableRef(t, schemas, "ArenaWave", "ready_window", "#/ArenaReadyWindow")
	requireArenaObject(t, schemas, "ArenaReadinessEvent", []string{
		"command_id", "wave_id", "window_id", "participant_id", "type", "occurred_at",
	})

	requireArenaObject(t, schemas, "ArenaPresence", []string{
		"id", "tournament_id", "roster_id", "series_id", "participant_id", "state",
		"presence_epoch", "revision", "connected_at", "disconnected_at", "updated_at",
	})
	requireArenaNullable(t, schemas, "ArenaPresence", "disconnected_at")
	requireArenaObject(t, schemas, "ArenaReconnectInterval", []string{
		"id", "pause_id", "roster_id", "series_id", "game_id", "participant_id", "presence_epoch",
		"number", "continuation_number", "continued_from_id", "suspended_by_pause_id", "state",
		"opened_at", "deadline", "closed_at", "revision", "updated_at",
	})
	requireArenaNullable(t, schemas, "ArenaReconnectInterval", "continued_from_id")
	requireArenaNullable(t, schemas, "ArenaReconnectInterval", "suspended_by_pause_id")
	requireArenaNullable(t, schemas, "ArenaReconnectInterval", "closed_at")

	requireArenaObject(t, schemas, "ArenaFrozenDeadline", []string{
		"kind", "owner_id", "original_deadline", "frozen_at", "remaining_ms",
		"resumed_at", "resumed_deadline", "revision",
	})
	requireArenaObject(t, schemas, "ArenaPauseGame", []string{
		"series_id", "game", "revision", "deadline", "resume_state",
	})
	requireArenaNullableRef(t, schemas, "ArenaPauseGame", "resume_state", "#/ArenaGameState")
	requireArenaObject(t, schemas, "ArenaPauseGraph", []string{
		"tournament_id", "roster_id", "wave", "series", "games", "draft", "presence", "reconnect",
		"counters", "frozen_deadlines", "active_pause_id", "paused_at", "deadlines_suppressed",
		"graph_revision", "terminal_action_revision",
	})
	requireArenaArrayRef(t, schemas, "ArenaPauseGraph", "games", "#/ArenaPauseGame", 0, 0)
	requireArenaArrayRef(t, schemas, "ArenaPauseGraph", "presence", "#/ArenaPresence", 0, 0)
	requireArenaArrayRef(t, schemas, "ArenaPauseGraph", "reconnect", "#/ArenaReconnectInterval", 0, 0)
	requireArenaNullable(t, schemas, "ArenaPauseGraph", "active_pause_id")
	requireArenaNullable(t, schemas, "ArenaPauseGraph", "paused_at")

	requireArenaObject(t, schemas, "ArenaPauseRecord", []string{
		"command_id", "pause_id", "actor_id", "reason", "state", "revision", "graph", "paused_at", "resolved_at",
	})
	requireArenaNullable(t, schemas, "ArenaPauseRecord", "resolved_at")
	requireArenaObject(t, schemas, "ArenaReconnectRecord", []string{
		"kind", "expected_authority_revision", "participant_id", "interval_id", "recorded_at",
		"terminal_reason", "result_revision_id", "conflict",
	})
	requireArenaNullableRef(t, schemas, "ArenaReconnectRecord", "terminal_reason", "#/ArenaGameResultReason")
	requireArenaObject(t, schemas, "ArenaReplayRequirement", []string{
		"tournament_id", "old_wave_id", "series_id", "slot_id", "assignment_id", "failed_game_id",
		"failed_result_revision_id", "terminal_reason", "closure_revision_id", "expected_authority_revision",
	})
	requireArenaObject(t, schemas, "ArenaDraftRecoveryEvidence", []string{
		"policy", "reason", "previous_state", "previous_service_epoch", "current_service_epoch",
		"previous_deadline", "recorded_at", "actor_id", "note",
	})
}
