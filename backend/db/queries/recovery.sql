-- name: ListPendingRecoveryDeadlines :many
WITH active_series_wave AS (
    SELECT DISTINCT ON (wave_series.series_id)
        wave_series.series_id,
        wave_series.wave_id
    FROM wave_series
    JOIN waves AS wave
        ON wave.id = wave_series.wave_id
        AND wave.tournament_id = wave_series.tournament_id
        AND wave.roster_id = wave_series.roster_id
    WHERE wave.state = 'active'
    ORDER BY
        wave_series.series_id,
        wave.started_at DESC NULLS LAST,
        wave_series.created_at DESC,
        wave_series.wave_id DESC
),
pending_deadlines AS (
    SELECT
        1::SMALLINT AS kind_order,
        'game'::TEXT AS deadline_kind,
        game_attempt.id AS deadline_id,
        series.tournament_id,
        game_attempt.roster_id,
        active_wave.wave_id,
        game_attempt.series_id,
        game_attempt.slot_id,
        game_attempt.id AS game_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS pause_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS ready_window_revision_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS participant_id,
        game_attempt.revision AS expected_revision,
        COALESCE(
            latest_clock.exact_resumed_deadline,
            game_attempt.started_at
                + INTERVAL '180 seconds'
        )::TIMESTAMPTZ AS due_at
    FROM game_attempts AS game_attempt
    JOIN series
        ON series.id = game_attempt.series_id
        AND series.roster_id = game_attempt.roster_id
    JOIN active_series_wave AS active_wave
        ON active_wave.series_id = game_attempt.series_id
    JOIN assignments AS assignment
        ON assignment.attempt_id = game_attempt.id
        AND assignment.series_id = game_attempt.series_id
        AND assignment.roster_id = game_attempt.roster_id
        AND assignment.state = 'active'
    JOIN task_snapshots AS task_snapshot
        ON task_snapshot.id = assignment.snapshot_id
    LEFT JOIN LATERAL (
        SELECT pause_clock.resumed_at + (pause_clock.original_deadline - pause_clock.frozen_at) AS exact_resumed_deadline
        FROM pause_clocks AS pause_clock
        JOIN pauses AS clock_pause
            ON clock_pause.id = pause_clock.pause_id
            AND clock_pause.state = 'resumed'
        WHERE pause_clock.game_attempt_id = game_attempt.id
            AND pause_clock.resumed_at IS NOT NULL
            AND pause_clock.resumed_deadline IS NOT NULL
        ORDER BY
            pause_clock.resumed_at DESC,
            pause_clock.pause_id DESC
        LIMIT 1
    ) AS latest_clock ON TRUE
    WHERE game_attempt.state = 'active'
        AND game_attempt.started_at IS NOT NULL

    UNION ALL

    SELECT
        2::SMALLINT AS kind_order,
        'ready_window'::TEXT AS deadline_kind,
        ready_window.id AS deadline_id,
        wave.tournament_id,
        ready_window.roster_id,
        ready_window.wave_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS series_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS slot_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS game_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS pause_id,
        ready_window.revision_id AS ready_window_revision_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS participant_id,
        wave.revision AS expected_revision,
        ready_window.deadline::TIMESTAMPTZ AS due_at
    FROM ready_windows AS ready_window
    JOIN waves AS wave
        ON wave.id = ready_window.wave_id
        AND wave.roster_id = ready_window.roster_id
    WHERE ready_window.state = 'open'
        AND wave.state = 'ready_window_open'

    UNION ALL

    SELECT
        3::SMALLINT AS kind_order,
        'reconnect'::TEXT AS deadline_kind,
        reconnect_interval.id AS deadline_id,
        pause.tournament_id,
        reconnect_interval.roster_id,
        active_wave.wave_id,
        reconnect_interval.series_id,
        game_attempt.slot_id,
        reconnect_interval.game_attempt_id AS game_id,
        reconnect_interval.pause_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS ready_window_revision_id,
        reconnect_interval.participant_id,
        reconnect_interval.revision AS expected_revision,
        reconnect_interval.deadline_at::TIMESTAMPTZ AS due_at
    FROM reconnect_intervals AS reconnect_interval
    JOIN pauses AS pause
        ON pause.id = reconnect_interval.pause_id
        AND pause.roster_id = reconnect_interval.roster_id
        AND pause.series_id = reconnect_interval.series_id
        AND pause.game_attempt_id = reconnect_interval.game_attempt_id
    JOIN game_attempts AS game_attempt
        ON game_attempt.id = reconnect_interval.game_attempt_id
        AND game_attempt.series_id = reconnect_interval.series_id
        AND game_attempt.roster_id = reconnect_interval.roster_id
    JOIN active_series_wave AS active_wave
        ON active_wave.series_id = reconnect_interval.series_id
    WHERE reconnect_interval.state = 'open'
        AND pause.state = 'active'
        AND pause.reason = 'disconnect'
)
SELECT
    deadline_kind,
    deadline_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    game_id,
    pause_id,
    ready_window_revision_id,
    participant_id,
    expected_revision,
    due_at
FROM pending_deadlines
WHERE kind_order > sqlc.arg(after_kind)::SMALLINT
    OR (
        kind_order = sqlc.arg(after_kind)::SMALLINT
        AND deadline_id > sqlc.arg(after_id)::UUID
    )
ORDER BY kind_order, deadline_id
LIMIT sqlc.arg(batch_size);

-- name: GetPendingRecoveryDeadline :one
WITH active_series_wave AS (
    SELECT DISTINCT ON (wave_series.series_id)
        wave_series.series_id,
        wave_series.wave_id
    FROM wave_series
    JOIN waves AS wave
        ON wave.id = wave_series.wave_id
        AND wave.tournament_id = wave_series.tournament_id
        AND wave.roster_id = wave_series.roster_id
    WHERE wave.state = 'active'
    ORDER BY
        wave_series.series_id,
        wave.started_at DESC NULLS LAST,
        wave_series.created_at DESC,
        wave_series.wave_id DESC
),
pending_deadlines AS (
    SELECT
        'game'::TEXT AS deadline_kind,
        game_attempt.id AS deadline_id,
        series.tournament_id,
        game_attempt.roster_id,
        active_wave.wave_id,
        game_attempt.series_id,
        game_attempt.slot_id,
        game_attempt.id AS game_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS pause_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS ready_window_revision_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS participant_id,
        game_attempt.revision AS expected_revision,
        COALESCE(
            latest_clock.exact_resumed_deadline,
            game_attempt.started_at
                + INTERVAL '180 seconds'
        )::TIMESTAMPTZ AS due_at
    FROM game_attempts AS game_attempt
    JOIN series
        ON series.id = game_attempt.series_id
        AND series.roster_id = game_attempt.roster_id
    JOIN active_series_wave AS active_wave
        ON active_wave.series_id = game_attempt.series_id
    JOIN assignments AS assignment
        ON assignment.attempt_id = game_attempt.id
        AND assignment.series_id = game_attempt.series_id
        AND assignment.roster_id = game_attempt.roster_id
        AND assignment.state = 'active'
    JOIN task_snapshots AS task_snapshot
        ON task_snapshot.id = assignment.snapshot_id
    LEFT JOIN LATERAL (
        SELECT pause_clock.resumed_at + (pause_clock.original_deadline - pause_clock.frozen_at) AS exact_resumed_deadline
        FROM pause_clocks AS pause_clock
        JOIN pauses AS clock_pause
            ON clock_pause.id = pause_clock.pause_id
            AND clock_pause.state = 'resumed'
        WHERE pause_clock.game_attempt_id = game_attempt.id
            AND pause_clock.resumed_at IS NOT NULL
            AND pause_clock.resumed_deadline IS NOT NULL
        ORDER BY
            pause_clock.resumed_at DESC,
            pause_clock.pause_id DESC
        LIMIT 1
    ) AS latest_clock ON TRUE
    WHERE game_attempt.state = 'active'
        AND game_attempt.started_at IS NOT NULL
        AND sqlc.arg(deadline_kind)::TEXT = 'game'
        AND game_attempt.id = sqlc.arg(deadline_id)::UUID

    UNION ALL

    SELECT
        'ready_window'::TEXT AS deadline_kind,
        ready_window.id AS deadline_id,
        wave.tournament_id,
        ready_window.roster_id,
        ready_window.wave_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS series_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS slot_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS game_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS pause_id,
        ready_window.revision_id AS ready_window_revision_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS participant_id,
        wave.revision AS expected_revision,
        ready_window.deadline::TIMESTAMPTZ AS due_at
    FROM ready_windows AS ready_window
    JOIN waves AS wave
        ON wave.id = ready_window.wave_id
        AND wave.roster_id = ready_window.roster_id
    WHERE ready_window.state = 'open'
        AND wave.state = 'ready_window_open'
        AND sqlc.arg(deadline_kind)::TEXT = 'ready_window'
        AND ready_window.id = sqlc.arg(deadline_id)::UUID

    UNION ALL

    SELECT
        'reconnect'::TEXT AS deadline_kind,
        reconnect_interval.id AS deadline_id,
        pause.tournament_id,
        reconnect_interval.roster_id,
        active_wave.wave_id,
        reconnect_interval.series_id,
        game_attempt.slot_id,
        reconnect_interval.game_attempt_id AS game_id,
        reconnect_interval.pause_id,
        '00000000-0000-0000-0000-000000000000'::UUID AS ready_window_revision_id,
        reconnect_interval.participant_id,
        reconnect_interval.revision AS expected_revision,
        reconnect_interval.deadline_at::TIMESTAMPTZ AS due_at
    FROM reconnect_intervals AS reconnect_interval
    JOIN pauses AS pause
        ON pause.id = reconnect_interval.pause_id
        AND pause.roster_id = reconnect_interval.roster_id
        AND pause.series_id = reconnect_interval.series_id
        AND pause.game_attempt_id = reconnect_interval.game_attempt_id
    JOIN game_attempts AS game_attempt
        ON game_attempt.id = reconnect_interval.game_attempt_id
        AND game_attempt.series_id = reconnect_interval.series_id
        AND game_attempt.roster_id = reconnect_interval.roster_id
    JOIN active_series_wave AS active_wave
        ON active_wave.series_id = reconnect_interval.series_id
    WHERE reconnect_interval.state = 'open'
        AND pause.state = 'active'
        AND pause.reason = 'disconnect'
        AND sqlc.arg(deadline_kind)::TEXT = 'reconnect'
        AND reconnect_interval.id = sqlc.arg(deadline_id)::UUID
)
SELECT
    deadline_kind,
    deadline_id,
    tournament_id,
    roster_id,
    wave_id,
    series_id,
    slot_id,
    game_id,
    pause_id,
    ready_window_revision_id,
    participant_id,
    expected_revision,
    due_at
FROM pending_deadlines;
