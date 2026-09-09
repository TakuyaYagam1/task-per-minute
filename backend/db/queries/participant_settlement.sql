-- The completed Series is locked by ResultPostgres before this query runs.
-- This query locks the immutable Swiss membership and participant source rows
-- in stable order so ledger rows can be appended in the same transaction.
-- name: ListParticipantSettlementSwissSeriesLedgerSources :many
WITH source AS MATERIALIZED (
    SELECT swiss_round.id AS round_id,
        swiss_round.round_number,
        series.first_participant_id,
        series.second_participant_id,
        series.winner_id,
        first_participant.seed AS first_stable_seed,
        second_participant.seed AS second_stable_seed
    FROM series
    INNER JOIN wave_series AS membership
        ON membership.series_id = series.id
        AND membership.tournament_id = series.tournament_id
        AND membership.roster_id = series.roster_id
    INNER JOIN swiss_wave_links AS swiss_link
        ON swiss_link.wave_id = membership.wave_id
        AND swiss_link.tournament_id = membership.tournament_id
        AND swiss_link.roster_id = membership.roster_id
    INNER JOIN swiss_rounds AS swiss_round
        ON swiss_round.id = swiss_link.round_id
        AND swiss_round.roster_id = swiss_link.roster_id
    INNER JOIN participants AS first_participant
        ON first_participant.id = series.first_participant_id
        AND first_participant.roster_id = series.roster_id
    INNER JOIN participants AS second_participant
        ON second_participant.id = series.second_participant_id
        AND second_participant.roster_id = series.roster_id
    WHERE series.id = sqlc.arg(series_id)
        AND series.tournament_id = sqlc.arg(tournament_id)
        AND series.roster_id = sqlc.arg(roster_id)
        AND series.state = 'completed'
        AND series.current_result_revision_id = sqlc.arg(series_result_revision_id)
    FOR KEY SHARE OF series, membership, swiss_link, swiss_round,
        first_participant, second_participant
),
expanded AS MATERIALIZED (
    SELECT source.round_id,
        source.round_number,
        source.first_participant_id AS participant_id,
        source.second_participant_id AS opponent_id,
        CASE WHEN source.winner_id = source.first_participant_id THEN 1 ELSE 0 END AS points,
        source.first_stable_seed AS stable_seed
    FROM source
    UNION ALL
    SELECT source.round_id,
        source.round_number,
        source.second_participant_id AS participant_id,
        source.first_participant_id AS opponent_id,
        CASE WHEN source.winner_id = source.second_participant_id THEN 1 ELSE 0 END AS points,
        source.second_stable_seed AS stable_seed
    FROM source
)
SELECT round_id,
    round_number,
    participant_id,
    opponent_id,
    points,
    stable_seed
FROM expanded
ORDER BY stable_seed, participant_id;

-- A participant settlement may expose an active Swiss round only at the
-- instant its final Series becomes terminal. The eligibility CTE is stricter
-- than the completed-wave reader: it locks the Wave graph, requires every
-- current Game and Series head to be terminal, and requires exact immutable
-- ledger coverage for every Series and optional bye. Partial active Waves are
-- deliberately excluded and retain their previous standings artifact.
-- name: ListParticipantSettlementSwissProjectionLedger :many
WITH active_wave AS MATERIALIZED (
    SELECT wave.id AS wave_id,
        wave.revision_id AS round_revision_id,
        swiss_round.id AS round_id,
        swiss_round.round_number,
        swiss_link.bye_participant_id,
        swiss_link.bye_revision_id
    FROM waves AS wave
    INNER JOIN swiss_wave_links AS swiss_link
        ON swiss_link.wave_id = wave.id
        AND swiss_link.tournament_id = wave.tournament_id
        AND swiss_link.roster_id = wave.roster_id
    INNER JOIN swiss_rounds AS swiss_round
        ON swiss_round.id = swiss_link.round_id
        AND swiss_round.roster_id = swiss_link.roster_id
    WHERE wave.id = sqlc.arg(wave_id)
        AND wave.tournament_id = sqlc.arg(tournament_id)
        AND wave.roster_id = sqlc.arg(roster_id)
        AND wave.state = 'active'
    FOR UPDATE OF wave, swiss_link, swiss_round
),
active_series AS MATERIALIZED (
    SELECT membership.series_id,
        series.first_participant_id,
        series.second_participant_id,
        series.state,
        series.current_result_revision_id
    FROM active_wave
    INNER JOIN wave_series AS membership
        ON membership.wave_id = active_wave.wave_id
        AND membership.tournament_id = sqlc.arg(tournament_id)
        AND membership.roster_id = sqlc.arg(roster_id)
    INNER JOIN series
        ON series.id = membership.series_id
        AND series.tournament_id = membership.tournament_id
        AND series.roster_id = membership.roster_id
    ORDER BY membership.series_id
    FOR UPDATE OF membership, series
),
active_slots AS MATERIALIZED (
    SELECT active_series.series_id,
        slot.id AS slot_id
    FROM active_series
    INNER JOIN game_slots AS slot
        ON slot.series_id = active_series.series_id
        AND slot.roster_id = sqlc.arg(roster_id)
    ORDER BY active_series.series_id, slot.slot_number, slot.id
    FOR UPDATE OF slot
),
active_games AS MATERIALIZED (
    SELECT active_slots.series_id,
        active_slots.slot_id,
        attempt.id AS game_id,
        attempt.state
    FROM active_slots
    INNER JOIN LATERAL (
        SELECT candidate.id,
            candidate.state
        FROM game_attempts AS candidate
        WHERE candidate.slot_id = active_slots.slot_id
            AND candidate.series_id = active_slots.series_id
            AND candidate.roster_id = sqlc.arg(roster_id)
        ORDER BY candidate.attempt_number DESC, candidate.id DESC
        LIMIT 1
        FOR UPDATE OF candidate
    ) AS attempt ON TRUE
),
active_ledger AS MATERIALIZED (
    SELECT entry.id,
        entry.tournament_id,
        entry.roster_id,
        entry.round_id,
        active_wave.round_revision_id,
        entry.round_number,
        entry.source_kind,
        entry.source_series_id,
        entry.series_result_revision_id,
        entry.bye_revision_id,
        entry.result_label,
        entry.participant_id,
        entry.opponent_id,
        entry.points,
        entry.effective_time_ns,
        entry.accepted_solve_time_ns,
        entry.stable_seed,
        entry.created_at
    FROM active_wave
    INNER JOIN swiss_point_ledger_entries AS entry
        ON entry.tournament_id = sqlc.arg(tournament_id)
        AND entry.roster_id = sqlc.arg(roster_id)
        AND entry.round_id = active_wave.round_id
    ORDER BY entry.source_kind,
        entry.source_series_id,
        entry.bye_revision_id,
        entry.participant_id
    FOR UPDATE OF entry
),
eligible_active_wave AS MATERIALIZED (
    SELECT active_wave.*
    FROM active_wave
    WHERE EXISTS (SELECT 1 FROM active_series)
        AND NOT EXISTS (
            SELECT 1
            FROM active_series
            WHERE state NOT IN ('completed', 'cancelled')
                OR current_result_revision_id IS NULL
        )
        AND NOT EXISTS (
            SELECT 1
            FROM active_series AS current_series
            WHERE NOT EXISTS (
                SELECT 1
                FROM active_games
                WHERE active_games.series_id = current_series.series_id
            )
        )
        AND NOT EXISTS (
            SELECT 1
            FROM active_slots
            LEFT JOIN active_games
                ON active_games.slot_id = active_slots.slot_id
            WHERE active_games.game_id IS NULL
                OR active_games.state NOT IN ('completed', 'void', 'cancelled', 'superseded')
        )
        AND NOT EXISTS (
            SELECT 1
            FROM active_ledger
            WHERE NOT (
                source_kind = 'series'
                AND EXISTS (
                    SELECT 1
                    FROM active_series
                    WHERE active_series.series_id = active_ledger.source_series_id
                        AND active_series.current_result_revision_id = active_ledger.series_result_revision_id
                )
            )
                AND NOT (
                    source_kind = 'bye'
                    AND active_ledger.participant_id = active_wave.bye_participant_id
                    AND active_ledger.bye_revision_id = active_wave.bye_revision_id
                )
        )
        AND NOT EXISTS (
            SELECT 1
            FROM active_series AS current_series
            WHERE (SELECT COUNT(*)
                    FROM active_ledger
                    WHERE source_kind = 'series'
                        AND source_series_id = current_series.series_id
                        AND series_result_revision_id = current_series.current_result_revision_id) <> 2
                OR NOT EXISTS (
                    SELECT 1
                    FROM active_ledger
                    WHERE source_kind = 'series'
                        AND source_series_id = current_series.series_id
                        AND series_result_revision_id = current_series.current_result_revision_id
                        AND participant_id = current_series.first_participant_id
                )
                OR NOT EXISTS (
                    SELECT 1
                    FROM active_ledger
                    WHERE source_kind = 'series'
                        AND source_series_id = current_series.series_id
                        AND series_result_revision_id = current_series.current_result_revision_id
                        AND participant_id = current_series.second_participant_id
                )
        )
        AND (
            active_wave.bye_participant_id IS NULL
            AND active_wave.bye_revision_id IS NULL
            OR active_wave.bye_participant_id IS NOT NULL
                AND active_wave.bye_revision_id IS NOT NULL
                AND (SELECT COUNT(*)
                     FROM active_ledger
                     WHERE source_kind = 'bye'
                        AND participant_id = active_wave.bye_participant_id
                        AND bye_revision_id = active_wave.bye_revision_id) = 1
        )
),
completed_series_ledger AS MATERIALIZED (
    SELECT entry.id,
        entry.tournament_id,
        entry.roster_id,
        entry.round_id,
        wave.revision_id AS round_revision_id,
        entry.round_number,
        entry.source_kind,
        entry.source_series_id,
        entry.series_result_revision_id,
        entry.bye_revision_id,
        entry.result_label,
        entry.participant_id,
        entry.opponent_id,
        entry.points,
        entry.effective_time_ns,
        entry.accepted_solve_time_ns,
        entry.stable_seed,
        entry.created_at
    FROM swiss_point_ledger_entries AS entry
    INNER JOIN swiss_rounds AS swiss_round
        ON swiss_round.id = entry.round_id
        AND swiss_round.roster_id = entry.roster_id
    INNER JOIN swiss_wave_links AS swiss_link
        ON swiss_link.round_id = swiss_round.id
        AND swiss_link.tournament_id = entry.tournament_id
        AND swiss_link.roster_id = entry.roster_id
    INNER JOIN waves AS wave
        ON wave.id = swiss_link.wave_id
        AND wave.tournament_id = entry.tournament_id
        AND wave.roster_id = entry.roster_id
    INNER JOIN series AS source_series
        ON source_series.id = entry.source_series_id
        AND source_series.tournament_id = entry.tournament_id
        AND source_series.roster_id = entry.roster_id
    WHERE entry.tournament_id = sqlc.arg(tournament_id)
        AND entry.roster_id = sqlc.arg(roster_id)
        AND entry.source_kind = 'series'
        AND wave.state = 'completed'
        AND source_series.state IN ('completed', 'cancelled')
        AND source_series.current_result_revision_id = entry.series_result_revision_id
    FOR UPDATE OF entry, swiss_round, swiss_link, wave, source_series
),
completed_bye_ledger AS MATERIALIZED (
    SELECT entry.id,
        entry.tournament_id,
        entry.roster_id,
        entry.round_id,
        wave.revision_id AS round_revision_id,
        entry.round_number,
        entry.source_kind,
        entry.source_series_id,
        entry.series_result_revision_id,
        entry.bye_revision_id,
        entry.result_label,
        entry.participant_id,
        entry.opponent_id,
        entry.points,
        entry.effective_time_ns,
        entry.accepted_solve_time_ns,
        entry.stable_seed,
        entry.created_at
    FROM swiss_point_ledger_entries AS entry
    INNER JOIN swiss_rounds AS swiss_round
        ON swiss_round.id = entry.round_id
        AND swiss_round.roster_id = entry.roster_id
    INNER JOIN swiss_wave_links AS swiss_link
        ON swiss_link.round_id = swiss_round.id
        AND swiss_link.tournament_id = entry.tournament_id
        AND swiss_link.roster_id = entry.roster_id
    INNER JOIN waves AS wave
        ON wave.id = swiss_link.wave_id
        AND wave.tournament_id = entry.tournament_id
        AND wave.roster_id = entry.roster_id
    WHERE entry.tournament_id = sqlc.arg(tournament_id)
        AND entry.roster_id = sqlc.arg(roster_id)
        AND entry.source_kind = 'bye'
        AND wave.state = 'completed'
        AND swiss_link.bye_participant_id = entry.participant_id
        AND swiss_link.bye_revision_id = entry.bye_revision_id
    FOR UPDATE OF entry, swiss_round, swiss_link, wave
),
canonical_ledger AS MATERIALIZED (
    SELECT *
    FROM completed_series_ledger
    UNION ALL
    SELECT *
    FROM completed_bye_ledger
    UNION ALL
    SELECT active_ledger.*
    FROM active_ledger
    INNER JOIN eligible_active_wave ON TRUE
)
SELECT round_id,
    round_revision_id,
    round_number,
    source_kind,
    source_series_id,
    series_result_revision_id,
    bye_revision_id,
    result_label,
    participant_id,
    opponent_id,
    points,
    effective_time_ns,
    accepted_solve_time_ns,
    stable_seed
FROM canonical_ledger
ORDER BY round_number,
    round_id,
    source_kind,
    source_series_id,
    bye_revision_id,
    participant_id;
