-- name: LockArenaResultAttempt :one
SELECT attempt.id,
    attempt.slot_id,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at
FROM arena_game_attempts AS attempt
INNER JOIN arena_series AS series ON series.id = attempt.series_id
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
FOR UPDATE OF attempt;

-- name: LockArenaResultSeries :one
SELECT series.id,
    series.tournament_id,
    series.roster_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.winner_id,
    series.current_score_revision_id,
    series.current_result_revision_id,
    series.revision,
    series.created_at,
    series.updated_at,
    series.started_at,
    series.finished_at,
    score_head.current_revision_id AS score_head_revision_id,
    score_head.revision AS score_head_revision
FROM arena_series AS series
INNER JOIN arena_series_score_heads AS score_head
    ON score_head.series_id = series.id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
FOR UPDATE OF series, score_head;

-- name: GetArenaSubmissionEventByIdempotencyKey :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    assignment_id,
    participant_id,
    server_sequence,
    idempotency_key,
    status,
    decision_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at
FROM arena_submission_events
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: CreateArenaSubmissionEvent :one
INSERT INTO arena_submission_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    assignment_id,
    participant_id,
    server_sequence,
    idempotency_key,
    status,
    decision_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(attempt_id),
    sqlc.arg(assignment_id),
    sqlc.arg(participant_id),
    sqlc.arg(server_sequence),
    sqlc.arg(idempotency_key),
    sqlc.arg(status),
    sqlc.narg(decision_reason)::TEXT,
    sqlc.arg(payload_digest),
    sqlc.arg(submitted_at),
    sqlc.arg(received_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    assignment_id,
    participant_id,
    server_sequence,
    idempotency_key,
    status,
    decision_reason,
    payload_digest,
    submitted_at,
    received_at,
    created_at;

-- name: CreateArenaResultEvent :one
INSERT INTO arena_result_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    submission_event_id,
    server_sequence,
    idempotency_key,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(attempt_id),
    sqlc.arg(submission_event_id),
    sqlc.arg(server_sequence),
    sqlc.arg(idempotency_key),
    sqlc.arg(result_state),
    sqlc.arg(result_reason),
    sqlc.arg(winner_id),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    submission_event_id,
    server_sequence,
    idempotency_key,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at;

-- name: CreateArenaOfficialResultRevision :one
INSERT INTO arena_official_result_revisions (
    id,
    tournament_id,
    roster_id,
    entity_kind,
    entity_id,
    series_id,
    game_attempt_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    result_state,
    result_reason,
    winner_id,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(entity_kind),
    sqlc.arg(entity_id),
    sqlc.arg(series_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(result_event_id),
    sqlc.arg(previous_revision_id),
    sqlc.arg(revision_number),
    sqlc.arg(result_state),
    sqlc.arg(result_reason),
    sqlc.arg(winner_id),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    entity_kind,
    entity_id,
    series_id,
    game_attempt_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    result_state,
    result_reason,
    winner_id,
    created_at;

-- name: CreateArenaSeriesScoreRevision :one
INSERT INTO arena_series_score_revisions (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    first_participant_wins,
    second_participant_wins,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(previous_revision_id),
    sqlc.arg(revision_number),
    sqlc.arg(first_participant_wins),
    sqlc.arg(second_participant_wins),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    first_participant_wins,
    second_participant_wins,
    created_at;

-- name: CreateArenaAuditEvent :one
INSERT INTO arena_audit_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(actor_kind),
    sqlc.arg(actor_id),
    sqlc.arg(action),
    sqlc.arg(payload),
    sqlc.arg(occurred_at),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at;

-- name: CreateArenaResultProjectionEvidence :one
INSERT INTO arena_result_projection_evidence (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    artifact_kinds,
    payload_digest,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(artifact_kinds),
    sqlc.arg(payload_digest),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    artifact_kinds,
    payload_digest,
    created_at;

-- name: CreateArenaOutboxEvent :one
INSERT INTO arena_outbox_events (
    id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    idempotency_key,
    topic,
    payload,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(result_event_id),
    sqlc.arg(idempotency_key),
    sqlc.arg(topic),
    sqlc.arg(payload),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    idempotency_key,
    topic,
    payload,
    created_at,
    published_at;

-- name: CreateArenaResultCommit :one
INSERT INTO arena_result_commits (
    id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at
)
VALUES (
    sqlc.arg(id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(attempt_id),
    sqlc.arg(result_event_id),
    sqlc.arg(game_result_revision_id),
    sqlc.arg(series_score_revision_id),
    sqlc.arg(series_result_revision_id),
    sqlc.arg(audit_event_id),
    sqlc.arg(outbox_event_id),
    sqlc.arg(projection_evidence_id),
    sqlc.arg(idempotency_key),
    sqlc.arg(created_at)
)
RETURNING id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at;

-- name: CreateArenaOfficialResultHead :one
INSERT INTO arena_official_result_heads (
    entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at
)
VALUES (
    sqlc.arg(entity_kind),
    sqlc.arg(entity_id),
    sqlc.arg(series_id),
    sqlc.arg(roster_id),
    sqlc.arg(game_attempt_id),
    sqlc.arg(current_revision_id),
    1,
    sqlc.arg(updated_at)
)
RETURNING entity_kind,
    entity_id,
    series_id,
    roster_id,
    game_attempt_id,
    current_revision_id,
    revision,
    updated_at;

-- name: AdvanceArenaSeriesScoreHeadCAS :one
UPDATE arena_series_score_heads
SET current_revision_id = sqlc.arg(current_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE series_id = sqlc.arg(series_id)
    AND roster_id = sqlc.arg(roster_id)
    AND current_revision_id = sqlc.arg(expected_revision_id)
    AND revision = sqlc.arg(expected_revision)
RETURNING series_id,
    roster_id,
    current_revision_id,
    revision,
    updated_at;

-- name: SettleArenaGameAttemptCAS :one
UPDATE arena_game_attempts AS attempt
SET state = sqlc.arg(result_state),
    result_reason = sqlc.arg(result_reason),
    winner_id = sqlc.arg(winner_id),
    result_revision_id = sqlc.arg(result_revision_id),
    revision = attempt.revision + 1,
    updated_at = sqlc.arg(settled_at)::TIMESTAMPTZ,
    finished_at = sqlc.arg(settled_at)
FROM arena_series AS series
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id)
    AND series.id = attempt.series_id
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND attempt.revision = sqlc.arg(expected_revision)
    AND attempt.state = sqlc.arg(expected_state)
RETURNING attempt.id,
    attempt.slot_id,
    attempt.series_id,
    attempt.roster_id,
    attempt.attempt_number,
    attempt.state,
    attempt.result_reason,
    attempt.winner_id,
    attempt.result_revision_id,
    attempt.revision,
    attempt.created_at,
    attempt.updated_at,
    attempt.started_at,
    attempt.finished_at;

-- name: SettleArenaSeriesCAS :one
UPDATE arena_series
SET state = sqlc.arg(next_state),
    first_participant_wins = sqlc.arg(first_participant_wins),
    second_participant_wins = sqlc.arg(second_participant_wins),
    winner_id = sqlc.arg(winner_id),
    current_score_revision_id = sqlc.arg(score_revision_id),
    current_result_revision_id = sqlc.arg(result_revision_id),
    revision = revision + 1,
    updated_at = sqlc.arg(settled_at)::TIMESTAMPTZ,
    finished_at = CASE
        WHEN sqlc.arg(next_state)::VARCHAR IN ('completed', 'cancelled')
            THEN sqlc.arg(settled_at)::TIMESTAMPTZ
        ELSE NULL
    END
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND revision = sqlc.arg(expected_revision)
    AND state = sqlc.arg(expected_state)
    AND current_score_revision_id = sqlc.arg(expected_score_revision_id)
RETURNING id,
    tournament_id,
    roster_id,
    first_participant_id,
    second_participant_id,
    format,
    state,
    first_participant_wins,
    second_participant_wins,
    winner_id,
    current_score_revision_id,
    current_result_revision_id,
    revision,
    created_at,
    updated_at,
    started_at,
    finished_at;

-- name: GetArenaResultCommitByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at
FROM arena_result_commits
WHERE id = sqlc.arg(id);

-- name: GetArenaResultCommitByIdempotencyKey :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    result_event_id,
    game_result_revision_id,
    series_score_revision_id,
    series_result_revision_id,
    audit_event_id,
    outbox_event_id,
    projection_evidence_id,
    idempotency_key,
    created_at
FROM arena_result_commits
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: GetCurrentArenaResultCommitForAttempt :one
SELECT result_commit.id,
    result_commit.tournament_id,
    result_commit.roster_id,
    result_commit.series_id,
    result_commit.attempt_id,
    result_commit.result_event_id,
    result_commit.game_result_revision_id,
    result_commit.series_score_revision_id,
    result_commit.series_result_revision_id,
    result_commit.audit_event_id,
    result_commit.outbox_event_id,
    result_commit.projection_evidence_id,
    result_commit.idempotency_key,
    result_commit.created_at
FROM arena_official_result_heads AS result_head
INNER JOIN arena_result_commits AS result_commit
    ON result_commit.game_result_revision_id = result_head.current_revision_id
WHERE result_head.entity_kind = 'game_attempt'
    AND result_head.entity_id = sqlc.arg(attempt_id)
    AND result_commit.tournament_id = sqlc.arg(tournament_id)
    AND result_commit.roster_id = sqlc.arg(roster_id)
    AND result_commit.series_id = sqlc.arg(series_id);

-- name: GetArenaResultEventByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    attempt_id,
    submission_event_id,
    server_sequence,
    idempotency_key,
    result_state,
    result_reason,
    winner_id,
    occurred_at,
    created_at
FROM arena_result_events
WHERE id = sqlc.arg(id);

-- name: GetArenaOfficialResultRevisionByID :one
SELECT id,
    tournament_id,
    roster_id,
    entity_kind,
    entity_id,
    series_id,
    game_attempt_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    result_state,
    result_reason,
    winner_id,
    created_at
FROM arena_official_result_revisions
WHERE id = sqlc.arg(id);

-- name: GetArenaSeriesScoreRevisionByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    previous_revision_id,
    revision_number,
    first_participant_wins,
    second_participant_wins,
    created_at
FROM arena_series_score_revisions
WHERE id = sqlc.arg(id);

-- name: GetArenaAuditEventByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    actor_kind,
    actor_id,
    action,
    payload,
    occurred_at,
    created_at
FROM arena_audit_events
WHERE id = sqlc.arg(id);

-- name: GetArenaOutboxEventByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    idempotency_key,
    topic,
    payload,
    created_at,
    published_at
FROM arena_outbox_events
WHERE id = sqlc.arg(id);

-- name: GetArenaResultProjectionEvidenceByID :one
SELECT id,
    tournament_id,
    roster_id,
    series_id,
    result_event_id,
    artifact_kinds,
    payload_digest,
    created_at
FROM arena_result_projection_evidence
WHERE id = sqlc.arg(id);

-- name: ListArenaResultHistory :many
SELECT result_commit.id AS commit_id,
    result_commit.tournament_id,
    result_commit.roster_id,
    result_commit.series_id,
    result_commit.attempt_id,
    result_event.id AS result_event_id,
    result_event.server_sequence,
    result_event.result_state,
    result_event.result_reason,
    result_event.winner_id,
    game_revision.id AS game_revision_id,
    game_revision.revision_number AS game_revision_number,
    score_revision.id AS score_revision_id,
    score_revision.revision_number AS score_revision_number,
    score_revision.first_participant_wins,
    score_revision.second_participant_wins,
    result_commit.series_result_revision_id,
    result_commit.created_at
FROM arena_result_commits AS result_commit
INNER JOIN arena_result_events AS result_event
    ON result_event.id = result_commit.result_event_id
INNER JOIN arena_official_result_revisions AS game_revision
    ON game_revision.id = result_commit.game_result_revision_id
INNER JOIN arena_series_score_revisions AS score_revision
    ON score_revision.id = result_commit.series_score_revision_id
WHERE result_commit.tournament_id = sqlc.arg(tournament_id)
    AND result_commit.roster_id = sqlc.arg(roster_id)
    AND result_commit.series_id = sqlc.arg(series_id)
    AND result_commit.attempt_id = sqlc.arg(attempt_id)
ORDER BY game_revision.revision_number;
