package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaGoldenCorrectionSchemaSource(t *testing.T) {
	schemas := loadArenaSchemaSource(t)

	requireArenaEnum(t, schemas, "ArenaGoldenAttemptState", []string{
		"planned", "waiting_ready", "active", "completed", "void", "cancelled",
	})
	requireArenaEnum(t, schemas, "ArenaGoldenAttemptTerminalReason", []string{"all_solved", "deadline"})
	requireArenaEnum(t, schemas, "ArenaGoldenGroupRevisionPurpose", []string{"golden_seed_only"})
	requireArenaEnum(t, schemas, "ArenaGoldenGroupRevisionEffect", []string{"preserve_swiss_points_and_defer_rank"})
	requireArenaEnum(t, schemas, "ArenaCorrectionReason", []string{
		"scorekeeping_error", "verified_submission", "operator_ruling",
	})
	requireArenaEnum(t, schemas, "ArenaCorrectionField", []string{"winner", "result_reason", "solve_metadata"})
	requireArenaEnum(t, schemas, "ArenaAuditEntityKind", []string{"game_attempt", "series"})

	requireArenaObject(t, schemas, "ArenaGoldenAttempt", []string{
		"id", "group_id", "group_revision_id", "attempt_no", "previous_attempt_id", "state",
		"participant_ids", "retained_attempt_id", "retained_at", "started_at", "finished_at",
	})
	requireArenaNullable(t, schemas, "ArenaGoldenAttempt", "retained_attempt_id")
	requireArenaNullable(t, schemas, "ArenaGoldenAttempt", "retained_at")
	requireArenaObject(t, schemas, "ArenaGoldenGroupRevision", []string{
		"purpose", "effect", "tournament_id", "group_id", "revision_id", "revision_no",
		"previous_revision_id", "source_projection_revision_id", "source_projection_revision_no",
		"source_projection_payload_digest", "position_from", "position_to", "members", "payload_digest", "proof_hash",
	})
	requireArenaObject(t, schemas, "ArenaGoldenGroup", []string{
		"id", "tournament_id", "revision_id", "source_projection_revision_id", "position_from",
		"position_to", "participation_established", "members", "attempts",
	})

	requireArenaObject(t, schemas, "ArenaGoldenProvisionalOrder", []string{
		"attempt_id", "attempt_no", "submission_revision_id", "submission_revision", "entries", "payload_digest",
	})
	requireArenaArrayRef(t, schemas, "ArenaGoldenProvisionalOrder", "entries", "#/ArenaGoldenProvisionalOrderEntry", 0, 0)
	requireArenaObject(t, schemas, "ArenaGoldenCommittedOrder", []string{
		"revision_id", "revision", "previous_revision_id", "position_from", "position_to",
		"positions", "attempts", "payload_digest",
	})
	requireArenaNullable(t, schemas, "ArenaGoldenCommittedOrder", "previous_revision_id")

	requireArenaObject(t, schemas, "ArenaCorrectionUnlockIntent", []string{
		"reservation_id", "tournament_id", "owner_id", "source_revision_id", "expected_revision",
		"expected_used", "expected_disclosed", "evidence_digest", "binding_digest",
	})
	requireArenaObject(t, schemas, "ArenaCorrectionProjectionIntent", []string{
		"expected_revision", "next_revision_id", "decision_id", "payload_digest",
	})
	requireArenaObject(t, schemas, "ArenaCorrectionRequest", []string{
		"tournament_id", "series_id", "game_id", "command_id", "operator_id", "confirmed",
		"reason", "explanation", "requested_at", "fields", "patch", "projection_intents", "unlock_intents",
	})
	requireArenaObject(t, schemas, "ArenaCorrectionProjectionSupersession", []string{
		"artifact_kind", "artifact_id", "previous_revision_id", "successor_revision_id",
		"previous_decision_id", "replacement_decision_id",
	})
	requireArenaObject(t, schemas, "ArenaCorrectionEvidence", []string{
		"command_id", "tournament_id", "series_id", "game_id", "operator_id", "reason", "fields",
		"requested_at", "validation_digest", "supersessions", "unlock_intents",
	})
	require.True(t, requireArenaProperty(t, schemas, "ArenaCorrectionEvidence", "validation_digest").ReadOnly)

	requireArenaObject(t, schemas, "ArenaAuditCursor", []string{"occurred_at", "audit_event_id", "revision_id"})
	requireArenaObject(t, schemas, "ArenaAuditFilter", []string{"tournament_id"})
	requireArenaObject(t, schemas, "ArenaAuditEvent", []string{
		"audit_event_id", "tournament_id", "roster_id", "series_id", "result_event_id", "actor_kind",
		"actor_id", "event_type", "redacted_payload", "occurred_at", "created_at", "result_state",
		"result_reason", "winner_id", "official_result_revision_id", "entity_kind", "entity_id",
		"revision_number", "is_current", "is_superseded",
	})
	requireArenaObject(t, schemas, "ArenaAuditPage", []string{"events", "next_cursor"})
	requireArenaNullableRef(t, schemas, "ArenaAuditPage", "next_cursor", "#/ArenaAuditCursor")

	requireArenaObject(t, schemas, "ArenaIncidentBundle", []string{
		"tournament_id", "projection_revision", "generated_at", "canonical_content",
		"canonical_content_type", "canonical_content_encoding", "canonical_content_length", "sha256",
	})
}
