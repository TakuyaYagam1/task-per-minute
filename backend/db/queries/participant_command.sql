-- Membership is checked without locks before acquiring the result scope prefix.
-- The locked command authority below revalidates the same identity afterward.
-- name: GetParticipantCommandRoster :one
SELECT roster.id
FROM rosters AS roster
JOIN participants AS participant ON participant.roster_id = roster.id
WHERE roster.tournament_id = sqlc.arg(tournament_id)
    AND participant.player_id = sqlc.arg(player_id);

-- name: LockParticipantCommandAuthority :one
SELECT roster.id AS roster_id,
    participant.id AS participant_id,
    tournament.state AS tournament_state,
    projection.id AS projection_revision_id,
    projection.revision_number AS projection_revision
FROM tournaments AS tournament
JOIN rosters AS roster ON roster.tournament_id = tournament.id
JOIN participants AS participant
    ON participant.roster_id = roster.id
    AND participant.player_id = sqlc.arg(player_id)
JOIN projection_revisions AS projection
    ON projection.tournament_id = tournament.id
    AND projection.roster_id = roster.id
    AND projection.state = 'published'
WHERE tournament.id = sqlc.arg(tournament_id)
ORDER BY projection.revision_number DESC
LIMIT 1
FOR UPDATE OF tournament, roster, participant, projection;

-- name: LockParticipantReadyAuthority :one
SELECT wave.revision_id AS wave_revision_id,
    ready_window.id AS ready_window_id,
    ready_window.revision_id AS ready_window_revision_id
FROM waves AS wave
JOIN wave_members AS member
    ON member.wave_id = wave.id
    AND member.roster_id = wave.roster_id
    AND member.participant_id = sqlc.arg(participant_id)
JOIN LATERAL (
    SELECT candidate.id, candidate.revision_id
    FROM ready_windows AS candidate
    WHERE candidate.wave_id = wave.id
        AND candidate.roster_id = wave.roster_id
    ORDER BY candidate.created_at DESC, candidate.id DESC
    LIMIT 1
    FOR UPDATE OF candidate
) AS ready_window ON TRUE
WHERE wave.id = sqlc.arg(wave_id)
    AND wave.tournament_id = sqlc.arg(tournament_id)
    AND wave.roster_id = sqlc.arg(roster_id)
FOR UPDATE OF wave, member;

-- name: LockParticipantDraftAuthority :one
SELECT draft.id AS draft_id,
    revision.id AS revision_id,
    revision.revision,
    revision.service_epoch
FROM series AS series
JOIN drafts AS draft
    ON draft.series_id = series.id
    AND draft.roster_id = series.roster_id
JOIN LATERAL (
    SELECT current_revision.id,
        current_revision.revision,
        current_revision.service_epoch
    FROM draft_revisions AS current_revision
    WHERE current_revision.draft_id = draft.id
    ORDER BY current_revision.revision DESC
    LIMIT 1
    FOR UPDATE OF current_revision
) AS revision ON TRUE
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (
        series.first_participant_id,
        series.second_participant_id
    )
FOR UPDATE OF series, draft;

-- name: LockParticipantSubmissionAuthority :one
SELECT wave_series.wave_id,
    attempt.slot_id,
    assignment.id AS assignment_id
FROM series AS series
JOIN game_attempts AS attempt
    ON attempt.id = sqlc.arg(game_id)
    AND attempt.series_id = series.id
    AND attempt.roster_id = series.roster_id
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = series.roster_id
    AND assignment.state = 'active'
JOIN task_delivery_receipts AS receipt
    ON receipt.assignment_id = assignment.id
    AND receipt.attempt_id = assignment.attempt_id
    AND receipt.roster_id = assignment.roster_id
    AND receipt.participant_id = sqlc.arg(participant_id)
JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.tournament_id = series.tournament_id
    AND wave_series.roster_id = series.roster_id
JOIN waves AS wave
    ON wave.id = wave_series.wave_id
    AND wave.state <> 'superseded'
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (
        series.first_participant_id,
        series.second_participant_id
    )
ORDER BY wave.created_at DESC, wave.id DESC
LIMIT 1
FOR UPDATE OF series, attempt, assignment;

-- This immutable scope lock is intentionally usable after Game completion.
-- It establishes authorization for an exact command replay before fresh-command
-- active-assignment checks and before projection revision comparison.
-- name: LockParticipantSubmissionReplayScope :one
SELECT wave_series.wave_id,
    attempt.slot_id,
    assignment.id AS assignment_id
FROM series AS series
JOIN game_attempts AS attempt
    ON attempt.id = sqlc.arg(game_id)
    AND attempt.series_id = series.id
    AND attempt.roster_id = series.roster_id
JOIN assignments AS assignment
    ON assignment.attempt_id = attempt.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = series.roster_id
JOIN task_delivery_receipts AS receipt
    ON receipt.assignment_id = assignment.id
    AND receipt.attempt_id = assignment.attempt_id
    AND receipt.roster_id = assignment.roster_id
    AND receipt.participant_id = sqlc.arg(participant_id)
JOIN wave_series
    ON wave_series.series_id = series.id
    AND wave_series.tournament_id = series.tournament_id
    AND wave_series.roster_id = series.roster_id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (
        series.first_participant_id,
        series.second_participant_id
    )
ORDER BY wave_series.created_at DESC, wave_series.wave_id DESC, assignment.id
LIMIT 1
FOR UPDATE OF series, attempt, assignment;

-- An exact submission replay is visible only after the caller has locked and
-- authenticated this participant's tournament scope. The caller compares the
-- returned intent_digest with the shared submission intent contract before it
-- permits stale projection replay handling.
-- name: FindParticipantSubmissionReplay :one
SELECT submission.id,
    submission.tournament_id,
    submission.roster_id,
    submission.series_id,
    submission.attempt_id,
    submission.assignment_id,
    submission.participant_id,
    submission.server_sequence,
    submission.idempotency_key,
    submission.status,
    submission.decision_reason,
    submission.payload_digest,
    submission.intent_digest,
    submission.submitted_at,
    submission.received_at,
    submission.created_at,
    snapshot.id AS snapshot_id,
    snapshot.task_id,
    result_commit.id AS result_commit_id,
    projection.id AS projection_revision_id,
    COALESCE(projection.revision_number, 0)::BIGINT AS projection_revision,
    COALESCE(projection.state, '')::TEXT AS projection_state,
    projection.published_at AS projection_published_at
FROM submission_events AS submission
INNER JOIN assignments AS assignment
    ON assignment.id = submission.assignment_id
    AND assignment.roster_id = submission.roster_id
    AND assignment.series_id = submission.series_id
    AND assignment.attempt_id = submission.attempt_id
INNER JOIN task_snapshots AS snapshot
    ON snapshot.id = assignment.snapshot_id
    AND snapshot.task_id = assignment.task_id
    AND snapshot.task_version = assignment.task_version
LEFT JOIN result_commits AS result_commit
    ON result_commit.idempotency_key = submission.idempotency_key
    AND result_commit.tournament_id = submission.tournament_id
    AND result_commit.roster_id = submission.roster_id
    AND result_commit.series_id = submission.series_id
    AND result_commit.attempt_id = submission.attempt_id
LEFT JOIN outbox_result_sources AS outbox_source
    ON outbox_source.outbox_event_id = result_commit.outbox_event_id
    AND outbox_source.result_event_id = result_commit.result_event_id
    AND outbox_source.projection_evidence_id = result_commit.projection_evidence_id
LEFT JOIN outbox_events AS outbox_event
    ON outbox_event.id = outbox_source.outbox_event_id
    AND outbox_event.tournament_id = submission.tournament_id
    AND outbox_event.roster_id = submission.roster_id
LEFT JOIN projection_revisions AS projection
    ON projection.id = outbox_event.projection_revision_id
    AND projection.tournament_id = submission.tournament_id
    AND projection.roster_id = submission.roster_id
WHERE submission.idempotency_key = sqlc.arg(command_id)
    AND submission.tournament_id = sqlc.arg(tournament_id)
    AND submission.roster_id = sqlc.arg(roster_id)
    AND submission.series_id = sqlc.arg(series_id)
    AND submission.attempt_id = sqlc.arg(attempt_id)
    AND submission.assignment_id = sqlc.arg(assignment_id)
    AND submission.participant_id = sqlc.arg(participant_id)
FOR KEY SHARE OF submission;

-- name: LockParticipantSurrenderAuthority :one
SELECT slot.id AS slot_id,
    attempt.id AS game_id,
    attempt.attempt_number,
    attempt.state
FROM series AS series
JOIN LATERAL (
    SELECT candidate.id, candidate.slot_number
    FROM game_slots AS candidate
    WHERE candidate.series_id = series.id
        AND candidate.roster_id = series.roster_id
    ORDER BY candidate.slot_number DESC, candidate.id DESC
    LIMIT 1
) AS slot ON TRUE
JOIN LATERAL (
    SELECT candidate.id, candidate.attempt_number, candidate.state
    FROM game_attempts AS candidate
    WHERE candidate.slot_id = slot.id
        AND candidate.series_id = series.id
        AND candidate.roster_id = series.roster_id
    ORDER BY candidate.attempt_number DESC, candidate.id DESC
    LIMIT 1
    FOR UPDATE OF candidate
) AS attempt ON TRUE
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (
        series.first_participant_id,
        series.second_participant_id
    )
FOR UPDATE OF series;

-- name: LockParticipantPostSeriesAuthority :one
SELECT series.state,
    series.current_result_revision_id
FROM series AS series
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND sqlc.arg(participant_id) IN (
        series.first_participant_id,
        series.second_participant_id
    )
FOR UPDATE OF series;

-- name: ListParticipantReadinessEvents :many
SELECT command_id,
    wave_id,
    ready_window_id,
    roster_id,
    participant_id,
    readiness_revision,
    event_type,
    occurred_at,
    created_at
FROM readiness_events
WHERE wave_id = sqlc.arg(wave_id)
    AND ready_window_id = sqlc.arg(ready_window_id)
ORDER BY readiness_revision, command_id;

-- name: LockParticipantReadinessState :one
SELECT wave.tournament_id,
    wave.roster_id,
    wave.revision_id AS wave_revision_id,
    wave.revision AS wave_revision,
    wave.state AS wave_state,
    ready_window.revision_id AS ready_window_revision_id,
    ready_window.state AS ready_window_state,
    readiness.revision AS participant_readiness_revision,
    readiness.ready AS participant_ready
FROM waves AS wave
JOIN ready_windows AS ready_window
    ON ready_window.id = sqlc.arg(ready_window_id)
    AND ready_window.wave_id = wave.id
    AND ready_window.roster_id = wave.roster_id
JOIN wave_readiness AS readiness
    ON readiness.wave_id = wave.id
    AND readiness.roster_id = wave.roster_id
    AND readiness.ready_window_id = ready_window.id
    AND readiness.participant_id = sqlc.arg(participant_id)
WHERE wave.id = sqlc.arg(wave_id)
FOR UPDATE OF wave, ready_window, readiness;

-- name: SetParticipantReadinessHead :one
UPDATE wave_readiness
SET ready = sqlc.arg(ready),
    ready_at = CASE
        WHEN sqlc.arg(ready)::BOOLEAN THEN sqlc.arg(occurred_at)::TIMESTAMPTZ
        ELSE NULL
    END,
    revision = revision + 1,
    updated_at = sqlc.arg(occurred_at)
WHERE wave_id = sqlc.arg(wave_id)
    AND ready_window_id = sqlc.arg(ready_window_id)
    AND participant_id = sqlc.arg(participant_id)
    AND revision = sqlc.arg(expected_revision)
RETURNING ready_window_id,
    wave_id,
    roster_id,
    participant_id,
    ready,
    revision,
    ready_at,
    created_at,
    updated_at;

-- name: SetParticipantWaveReadiness :one
UPDATE waves
SET state = sqlc.arg(next_state),
    revision = revision + 1,
    updated_at = sqlc.arg(occurred_at)
WHERE id = sqlc.arg(wave_id)
    AND revision_id = sqlc.arg(expected_wave_revision_id)
    AND revision = sqlc.arg(expected_revision)
    AND state IN ('ready_window_open', 'ready')
RETURNING id,
    tournament_id,
    roster_id,
    revision_id,
    revision,
    state,
    replaces_wave_id,
    created_at,
    updated_at,
    started_at,
    paused_at,
    closed_at;

-- name: CreateParticipantReadinessEvent :one
INSERT INTO readiness_events (
    command_id,
    wave_id,
    ready_window_id,
    roster_id,
    participant_id,
    readiness_revision,
    event_type,
    occurred_at,
    created_at
)
VALUES (
    sqlc.arg(command_id),
    sqlc.arg(wave_id),
    sqlc.arg(ready_window_id),
    sqlc.arg(roster_id),
    sqlc.arg(participant_id),
    sqlc.arg(readiness_revision),
    sqlc.arg(event_type),
    sqlc.arg(occurred_at),
    sqlc.arg(occurred_at)
)
RETURNING command_id,
    wave_id,
    ready_window_id,
    roster_id,
    participant_id,
    readiness_revision,
    event_type,
    occurred_at,
    created_at;

-- name: GetParticipantSeriesExecution :many
SELECT series.id AS series_id,
    series.tournament_id,
    series.roster_id,
    series.first_participant_id,
    series.second_participant_id,
    series.format,
    series.state AS series_state,
    series.first_participant_wins,
    series.second_participant_wins,
    series.winner_id AS series_winner_id,
    series.current_score_revision_id,
    series.current_result_revision_id,
    series.revision AS series_revision,
    score_head.revision AS score_revision,
    COALESCE(series_result.revision_number, 0)::BIGINT AS series_result_revision,
    COALESCE(active_pause.paused_from_state, '')::TEXT AS series_resume_state,
    projection.revision_number AS projection_revision,
    slot.id AS slot_id,
    slot.slot_number,
    slot.category,
    slot.first_participant_wins_before,
    slot.second_participant_wins_before,
    attempt.id AS attempt_id,
    attempt.attempt_number,
    attempt.state AS attempt_state,
    attempt.result_reason AS attempt_result_reason,
    attempt.winner_id AS attempt_winner_id,
    attempt.result_revision_id AS attempt_result_revision_id,
    attempt.revision AS attempt_revision,
    attempt.started_at AS attempt_started_at
FROM series AS series
JOIN series_score_heads AS score_head
    ON score_head.series_id = series.id
    AND score_head.roster_id = series.roster_id
LEFT JOIN official_result_revisions AS series_result
    ON series_result.id = series.current_result_revision_id
    AND series_result.entity_kind = 'series'
LEFT JOIN LATERAL (
    SELECT pause.paused_from_state
    FROM pauses AS pause
    WHERE pause.tournament_id = series.tournament_id
        AND pause.roster_id = series.roster_id
        AND pause.series_id = series.id
        AND pause.state = 'active'
    ORDER BY pause.started_at DESC, pause.id DESC
    LIMIT 1
) AS active_pause ON TRUE
JOIN projection_revisions AS projection
    ON projection.tournament_id = series.tournament_id
    AND projection.roster_id = series.roster_id
    AND projection.state = 'published'
JOIN game_slots AS slot
    ON slot.series_id = series.id
    AND slot.roster_id = series.roster_id
JOIN game_attempts AS attempt
    ON attempt.slot_id = slot.id
    AND attempt.series_id = series.id
    AND attempt.roster_id = series.roster_id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
ORDER BY slot.slot_number, attempt.attempt_number;

-- name: GetParticipantSeriesRoster :one
SELECT roster_id
FROM series
WHERE id = sqlc.arg(series_id)
    AND tournament_id = sqlc.arg(tournament_id);

-- name: GetParticipantSubmissionBinding :one
SELECT assignment.revision AS assignment_revision,
    plan.revision_id AS plan_revision_id,
    snapshot.id AS snapshot_id,
    snapshot.task_id,
    snapshot.task_version,
    snapshot.kind,
    snapshot.title,
    snapshot.description,
    snapshot.category,
    snapshot.difficulty,
    snapshot.time_limit,
    snapshot.flag,
    snapshot.hints,
    snapshot.task_url,
    snapshot.source_file_url,
    snapshot.content_digest,
    attempt.revision AS attempt_revision,
    attempt.state AS attempt_state,
    attempt.started_at,
    EXISTS (
        SELECT 1
        FROM pauses AS pause
        WHERE pause.tournament_id = series.tournament_id
            AND pause.roster_id = series.roster_id
            AND (
                pause.scope_kind = 'tournament'
                OR pause.wave_id = sqlc.arg(wave_id)
                OR pause.series_id = series.id
                OR pause.game_attempt_id = attempt.id
            )
            AND pause.state = 'active'
    )::BOOLEAN AS paused
FROM series AS series
JOIN game_attempts AS attempt
    ON attempt.id = sqlc.arg(game_id)
    AND attempt.slot_id = sqlc.arg(slot_id)
    AND attempt.series_id = series.id
    AND attempt.roster_id = series.roster_id
JOIN assignments AS assignment
    ON assignment.id = sqlc.arg(assignment_id)
    AND assignment.attempt_id = attempt.id
    AND assignment.series_id = series.id
    AND assignment.roster_id = series.roster_id
    AND assignment.state = 'active'
JOIN assignment_plans AS plan
    ON plan.id = assignment.plan_id
    AND plan.roster_id = assignment.roster_id
JOIN task_snapshots AS snapshot
    ON snapshot.id = assignment.snapshot_id
    AND snapshot.task_id = assignment.task_id
    AND snapshot.task_version = assignment.task_version
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id);

-- name: ListParticipantSubmissionPresence :many
SELECT participant_id
FROM presence_states
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND state = 'connected'
ORDER BY participant_id;

-- name: ListParticipantSubmissionHistory :many
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
    intent_digest,
    submitted_at,
    received_at,
    created_at
FROM submission_events
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND attempt_id = sqlc.arg(attempt_id)
    AND assignment_id = sqlc.arg(assignment_id)
ORDER BY server_sequence;

-- name: GetParticipantSubmissionCommitMetadata :one
SELECT attempt.revision AS attempt_revision,
    attempt.state AS attempt_state,
    transaction_timestamp()::TIMESTAMPTZ AS committed_at
FROM game_attempts AS attempt
WHERE attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = sqlc.arg(series_id)
    AND attempt.roster_id = sqlc.arg(roster_id);

-- name: ListParticipantForfeitPresence :many
SELECT participant_id
FROM presence_states
WHERE tournament_id = sqlc.arg(tournament_id)
    AND roster_id = sqlc.arg(roster_id)
    AND series_id = sqlc.arg(series_id)
    AND state = 'connected'
ORDER BY participant_id;

-- name: ListParticipantGameResultRevisionIDs :many
SELECT result.current_revision_id
FROM game_slots AS slot
JOIN game_attempts AS attempt
    ON attempt.slot_id = slot.id
    AND attempt.series_id = slot.series_id
    AND attempt.roster_id = slot.roster_id
JOIN official_result_heads AS result
    ON result.entity_kind = 'game_attempt'
    AND result.entity_id = attempt.id
    AND result.game_attempt_id = attempt.id
    AND result.series_id = slot.series_id
    AND result.roster_id = slot.roster_id
WHERE slot.series_id = sqlc.arg(series_id)
    AND slot.roster_id = sqlc.arg(roster_id)
ORDER BY slot.slot_number, attempt.attempt_number;

-- name: GetParticipantSurrenderCommitMetadata :one
SELECT attempt.revision AS attempt_revision,
    attempt.state AS attempt_state,
    series.revision AS series_revision,
    series.state AS series_state
FROM series AS series
JOIN game_attempts AS attempt
    ON attempt.id = sqlc.arg(attempt_id)
    AND attempt.series_id = series.id
    AND attempt.roster_id = series.roster_id
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id);

-- name: FindParticipantPostSeriesAction :one
SELECT command_id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    current_result_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    action,
    occurred_at,
    created_at
FROM participant_post_series_actions
WHERE command_id = sqlc.arg(command_id);

-- name: CreateParticipantPostSeriesAction :one
INSERT INTO participant_post_series_actions (
    command_id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    current_result_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    action,
    occurred_at,
    created_at
)
SELECT sqlc.arg(command_id),
    sqlc.arg(tournament_id),
    sqlc.arg(roster_id),
    sqlc.arg(series_id),
    sqlc.arg(participant_id),
    sqlc.arg(current_result_revision_id),
    sqlc.arg(source_projection_revision_id),
    sqlc.arg(source_projection_revision),
    sqlc.arg(resulting_projection_revision_id),
    sqlc.arg(resulting_projection_revision),
    sqlc.arg(action),
    sqlc.arg(occurred_at),
    sqlc.arg(occurred_at)
FROM series
JOIN projection_revisions AS projection
    ON projection.tournament_id = series.tournament_id
    AND projection.roster_id = series.roster_id
    AND projection.id = sqlc.arg(source_projection_revision_id)
    AND projection.revision_number = sqlc.arg(source_projection_revision)
    AND projection.state = 'published'
WHERE series.id = sqlc.arg(series_id)
    AND series.tournament_id = sqlc.arg(tournament_id)
    AND series.roster_id = sqlc.arg(roster_id)
    AND series.state = sqlc.arg(expected_series_state)
    AND series.state IN ('completed', 'cancelled')
    AND series.current_result_revision_id = sqlc.arg(current_result_revision_id)
    AND sqlc.arg(participant_id) IN (
        series.first_participant_id,
        series.second_participant_id
    )
RETURNING command_id,
    tournament_id,
    roster_id,
    series_id,
    participant_id,
    current_result_revision_id,
    source_projection_revision_id,
    source_projection_revision,
    resulting_projection_revision_id,
    resulting_projection_revision,
    action,
    occurred_at,
    created_at;
