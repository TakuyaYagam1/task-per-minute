-- +goose Up
-- +goose StatementBegin

-- Reserve content_digest is the identity-bound immutable snapshot digest. The
-- task_versions digest is the legacy content-only digest, so it must not be
-- compared with the reserve command digest at source-load time.
CREATE OR REPLACE FUNCTION public.replay_command_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    tournament_state VARCHAR(32);
    series_state VARCHAR(32);
    series_revision BIGINT;
    wave_state VARCHAR(32);
    wave_revision_id UUID;
    game_state VARCHAR(32);
    assignment_snapshot_id UUID;
    assignment_attempt_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'replay command evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament.state, series.state, series.revision,
        wave.state, wave.revision_id, game_attempt.state,
        assignment.snapshot_id, assignment.attempt_id
    INTO tournament_state, series_state, series_revision,
        wave_state, wave_revision_id, game_state,
        assignment_snapshot_id, assignment_attempt_id
    FROM tournaments AS tournament
    JOIN series
        ON series.tournament_id = tournament.id
        AND series.id = NEW.series_id
        AND series.roster_id = NEW.roster_id
    JOIN waves AS wave
        ON wave.id = NEW.old_wave_id
        AND wave.tournament_id = tournament.id
        AND wave.roster_id = NEW.roster_id
    JOIN game_attempts AS game_attempt
        ON game_attempt.id = NEW.failed_game_id
        AND game_attempt.series_id = NEW.series_id
        AND game_attempt.roster_id = NEW.roster_id
    JOIN assignments AS assignment ON assignment.id = NEW.assignment_id
    WHERE tournament.id = NEW.tournament_id
    FOR UPDATE OF tournament, series, wave, game_attempt, assignment;

    IF tournament_state NOT IN ('swiss', 'golden', 'playoffs', 'technical_pause')
        OR wave_state <> 'completed'
        OR wave_revision_id IS DISTINCT FROM NEW.closure_revision_id
        OR game_state <> 'void'
        OR assignment_snapshot_id IS DISTINCT FROM NEW.from_snapshot_id
        OR assignment_attempt_id IS DISTINCT FROM NEW.assignment_attempt_id
        OR NEW.assignment_attempt_id IS DISTINCT FROM NEW.failed_game_id THEN
        RAISE EXCEPTION 'replay command source evidence is stale or out of scope'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_TABLE_NAME = 'replay_reserve_exhaustions' THEN
        IF series_state <> 'replay_required'
            OR series_revision <> NEW.source_series_revision THEN
            RAISE EXCEPTION 'replay exhaustion source revision is stale'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'operator_replay_reserves' THEN
        IF series_state <> 'technical_pause'
            OR series_revision <> NEW.source_series_revision
            OR NOT EXISTS (
                SELECT 1
                FROM replay_reserve_exhaustions AS exhaustion
                WHERE exhaustion.command_id = NEW.exhaustion_command_id
                    AND exhaustion.tournament_id = NEW.tournament_id
                    AND exhaustion.series_id = NEW.series_id
                    AND exhaustion.assignment_id = NEW.assignment_id
                    AND exhaustion.resulting_series_revision = NEW.source_series_revision
            ) THEN
            RAISE EXCEPTION 'operator reserve lacks current exhaustion evidence'
                USING ERRCODE = 'check_violation';
        END IF;

        PERFORM 1
        FROM replay_reserve_authorities AS authority
        JOIN assignments AS assignment
            ON assignment.id = authority.assignment_id
        JOIN replay_reserve_authority_pool_versions AS pool_version
            ON pool_version.assignment_id = authority.assignment_id
            AND pool_version.task_id = NEW.proposed_task_id
            AND pool_version.task_version = NEW.proposed_version
        JOIN task_versions AS candidate_version
            ON candidate_version.task_id = pool_version.task_id
            AND candidate_version.version = pool_version.task_version
        JOIN tasks AS candidate_task ON candidate_task.id = candidate_version.task_id
        LEFT JOIN LATERAL (
            SELECT attestation.healthy
            FROM task_version_health_attestations AS attestation
            WHERE attestation.task_id = candidate_version.task_id
                AND attestation.task_version = candidate_version.version
            ORDER BY attestation.revision DESC
            LIMIT 1
            FOR UPDATE
        ) AS health ON true
        WHERE authority.tournament_id = NEW.tournament_id
            AND authority.roster_id = NEW.roster_id
            AND authority.series_id = NEW.series_id
            AND authority.slot_id = NEW.slot_id
            AND authority.assignment_id = NEW.assignment_id
            AND authority.assignment_attempt_id = NEW.assignment_attempt_id
            AND authority.active_snapshot_id = NEW.from_snapshot_id
            AND authority.assignment_revision = NEW.expected_assignment_revision
            AND authority.pool_revision_id = NEW.expected_pool_revision_id
            AND authority.pool_revision = NEW.expected_pool_revision
            AND authority.history_revision_id = NEW.expected_history_revision_id
            AND authority.history_revision = NEW.expected_history_revision
            AND authority.artifact_revision_id = NEW.expected_artifact_revision_id
            AND authority.artifact_revision = NEW.expected_artifact_revision
            AND authority.reservation_revision_id = NEW.expected_reservation_revision_id
            AND authority.reservation_revision = NEW.expected_reservation_revision
            AND authority.category_revision_id = NEW.expected_category_revision_id
            AND authority.category_revision = NEW.expected_category_revision
            AND candidate_version.category = authority.required_category
            AND candidate_task.enabled
            AND candidate_task.deleted_at IS NULL
            AND COALESCE(health.healthy, false)
            AND NOT EXISTS (
                SELECT 1
                FROM task_delivery_receipts AS receipt
                WHERE receipt.task_id = candidate_version.task_id
                    AND receipt.task_version = candidate_version.version
            )
            AND NOT EXISTS (
                SELECT 1
                FROM task_version_reservations AS used_reservation
                WHERE used_reservation.plan_id = assignment.plan_id
                    AND used_reservation.branch_id = assignment.branch_id
                    AND used_reservation.task_id = candidate_version.task_id
                    AND used_reservation.task_version = candidate_version.version
                    AND used_reservation.state = 'committed'
            )
        FOR UPDATE OF authority, assignment, candidate_version, candidate_task;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'operator reserve authority is missing, stale, or ineligible'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'replay_replacements' THEN
        IF series_state <> 'replay_required'
            OR series_revision <> NEW.source_series_revision THEN
            RAISE EXCEPTION 'replay replacement source revision is stale'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        RAISE EXCEPTION 'unknown replay command evidence table'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- The command source is checked before the snapshot row is inserted. Keep
-- graph validation there, then verify the immutable snapshot contents and its
-- identity-bound digest in the deferred target guard below.
CREATE OR REPLACE FUNCTION public.replay_command_target_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    series_state VARCHAR(32);
    series_revision BIGINT;
    target_count INTEGER;
BEGIN
    SELECT state, revision
    INTO series_state, series_revision
    FROM series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id;

    IF TG_TABLE_NAME = 'replay_reserve_exhaustions' THEN
        IF series_state <> 'technical_pause'
            OR series_revision <> NEW.resulting_series_revision THEN
            RAISE EXCEPTION 'replay exhaustion did not commit its paused Series'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'operator_replay_reserves' THEN
        SELECT COUNT(*)
        INTO target_count
        FROM assignment_plan_edges AS edge
        JOIN task_version_reservations AS reservation
            ON reservation.edge_id = edge.id
            AND reservation.id = NEW.reservation_id
        JOIN task_snapshots AS snapshot
            ON snapshot.reservation_id = reservation.id
            AND snapshot.id = NEW.proposed_snapshot_id
        JOIN task_versions AS candidate_version
            ON candidate_version.task_id = snapshot.task_id
            AND candidate_version.version = snapshot.task_version
        WHERE edge.id = NEW.edge_id
            AND edge.operator_reserve_command_id = NEW.command_id
            AND edge.position = NEW.reserve_position
            AND edge.task_id = NEW.proposed_task_id
            AND edge.task_version = NEW.proposed_version
            AND reservation.state = 'committed'
            AND snapshot.task_id = NEW.proposed_task_id
            AND snapshot.task_version = NEW.proposed_version
            AND snapshot.kind = 'normal'
            AND snapshot.title IS NOT DISTINCT FROM candidate_version.title
            AND snapshot.description IS NOT DISTINCT FROM candidate_version.description
            AND snapshot.category IS NOT DISTINCT FROM candidate_version.category
            AND snapshot.difficulty IS NOT DISTINCT FROM candidate_version.difficulty
            AND snapshot.time_limit IS NOT DISTINCT FROM candidate_version.time_limit
            AND snapshot.flag IS NOT DISTINCT FROM candidate_version.flag
            AND snapshot.hints IS NOT DISTINCT FROM jsonb_build_array(
                btrim(COALESCE(candidate_version.hint_1, '')),
                btrim(COALESCE(candidate_version.hint_2, '')),
                btrim(COALESCE(candidate_version.hint_3, ''))
            )
            AND snapshot.task_url IS NOT DISTINCT FROM candidate_version.task_url
            AND snapshot.source_file_url IS NOT DISTINCT FROM candidate_version.source_file_url
            AND snapshot.content_digest IS NOT DISTINCT FROM NEW.content_digest;

        IF target_count <> 1
            OR series_state <> 'replay_required'
            OR series_revision <> NEW.resulting_series_revision THEN
            RAISE EXCEPTION 'operator reserve target graph is incomplete'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'replay_replacements' THEN
        SELECT COUNT(*)
        INTO target_count
        FROM waves AS wave
        JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
        JOIN wave_series AS membership
            ON membership.wave_id = wave.id
            AND membership.series_id = NEW.series_id
        JOIN game_attempts AS game_attempt
            ON game_attempt.id = NEW.replacement_game_id
            AND game_attempt.series_id = NEW.series_id
        JOIN assignments AS assignment
            ON assignment.id = NEW.replacement_assignment_attempt_id
            AND assignment.attempt_id = game_attempt.id
        JOIN task_version_reservations AS reservation
            ON reservation.id = assignment.reservation_id
            AND reservation.state = 'committed'
        JOIN assignment_plan_edges AS edge
            ON edge.id = reservation.edge_id
            AND edge.plan_id = assignment.plan_id
            AND edge.branch_id = assignment.branch_id
            AND edge.task_id = assignment.task_id
            AND edge.task_version = assignment.task_version
        WHERE wave.id = NEW.replacement_wave_id
            AND wave.revision_id = NEW.replacement_wave_revision_id
            AND wave.state = 'ready_window_open'
            AND ready_window.id = NEW.ready_window_id
            AND ready_window.revision_id = NEW.ready_window_revision_id
            AND ready_window.state = 'open'
            AND game_attempt.state = 'planned'
            AND assignment.snapshot_id = NEW.snapshot_id
            AND assignment.state = 'active'
            AND edge.position = NEW.reserve_position
            AND (
                (NEW.reserve_position <= 3 AND edge.operator_reserve_command_id IS NULL)
                OR (
                    NEW.reserve_position = 4
                    AND EXISTS (
                        SELECT 1
                        FROM operator_replay_reserves AS operator_reserve
                        WHERE operator_reserve.command_id = edge.operator_reserve_command_id
                            AND operator_reserve.tournament_id = NEW.tournament_id
                            AND operator_reserve.old_wave_id = NEW.old_wave_id
                            AND operator_reserve.series_id = NEW.series_id
                            AND operator_reserve.slot_id = NEW.slot_id
                            AND operator_reserve.assignment_id = NEW.assignment_id
                            AND operator_reserve.assignment_attempt_id = NEW.assignment_attempt_id
                            AND operator_reserve.failed_game_id = NEW.failed_game_id
                            AND operator_reserve.proposed_snapshot_id = NEW.snapshot_id
                    )
                )
            )
            AND (
                SELECT COUNT(*)
                FROM wave_members AS member
                JOIN wave_readiness AS readiness
                    ON readiness.wave_id = member.wave_id
                    AND readiness.participant_id = member.participant_id
                WHERE member.wave_id = wave.id
                    AND readiness.ready_window_id = ready_window.id
                    AND NOT readiness.ready
            ) = 2;

        IF target_count <> 1
            OR series_state <> 'ready'
            OR series_revision <> NEW.resulting_series_revision THEN
            RAISE EXCEPTION 'replay replacement target graph is incomplete'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION public.replay_command_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    tournament_state VARCHAR(32);
    series_state VARCHAR(32);
    series_revision BIGINT;
    wave_state VARCHAR(32);
    wave_revision_id UUID;
    game_state VARCHAR(32);
    assignment_snapshot_id UUID;
    assignment_attempt_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'replay command evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament.state, series.state, series.revision,
        wave.state, wave.revision_id, game_attempt.state,
        assignment.snapshot_id, assignment.attempt_id
    INTO tournament_state, series_state, series_revision,
        wave_state, wave_revision_id, game_state,
        assignment_snapshot_id, assignment_attempt_id
    FROM tournaments AS tournament
    JOIN series
        ON series.tournament_id = tournament.id
        AND series.id = NEW.series_id
        AND series.roster_id = NEW.roster_id
    JOIN waves AS wave
        ON wave.id = NEW.old_wave_id
        AND wave.tournament_id = tournament.id
        AND wave.roster_id = NEW.roster_id
    JOIN game_attempts AS game_attempt
        ON game_attempt.id = NEW.failed_game_id
        AND game_attempt.series_id = NEW.series_id
        AND game_attempt.roster_id = NEW.roster_id
    JOIN assignments AS assignment ON assignment.id = NEW.assignment_id
    WHERE tournament.id = NEW.tournament_id
    FOR UPDATE OF tournament, series, wave, game_attempt, assignment;

    IF tournament_state NOT IN ('swiss', 'golden', 'playoffs', 'technical_pause')
        OR wave_state <> 'completed'
        OR wave_revision_id IS DISTINCT FROM NEW.closure_revision_id
        OR game_state <> 'void'
        OR assignment_snapshot_id IS DISTINCT FROM NEW.from_snapshot_id
        OR assignment_attempt_id IS DISTINCT FROM NEW.assignment_attempt_id
        OR NEW.assignment_attempt_id IS DISTINCT FROM NEW.failed_game_id THEN
        RAISE EXCEPTION 'replay command source evidence is stale or out of scope'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_TABLE_NAME = 'replay_reserve_exhaustions' THEN
        IF series_state <> 'replay_required'
            OR series_revision <> NEW.source_series_revision THEN
            RAISE EXCEPTION 'replay exhaustion source revision is stale'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'operator_replay_reserves' THEN
        IF series_state <> 'technical_pause'
            OR series_revision <> NEW.source_series_revision
            OR NOT EXISTS (
                SELECT 1
                FROM replay_reserve_exhaustions AS exhaustion
                WHERE exhaustion.command_id = NEW.exhaustion_command_id
                    AND exhaustion.tournament_id = NEW.tournament_id
                    AND exhaustion.series_id = NEW.series_id
                    AND exhaustion.assignment_id = NEW.assignment_id
                    AND exhaustion.resulting_series_revision = NEW.source_series_revision
            ) THEN
            RAISE EXCEPTION 'operator reserve lacks current exhaustion evidence'
                USING ERRCODE = 'check_violation';
        END IF;

        PERFORM 1
        FROM replay_reserve_authorities AS authority
        JOIN assignments AS assignment
            ON assignment.id = authority.assignment_id
        JOIN replay_reserve_authority_pool_versions AS pool_version
            ON pool_version.assignment_id = authority.assignment_id
            AND pool_version.task_id = NEW.proposed_task_id
            AND pool_version.task_version = NEW.proposed_version
        JOIN task_versions AS candidate_version
            ON candidate_version.task_id = pool_version.task_id
            AND candidate_version.version = pool_version.task_version
        JOIN tasks AS candidate_task ON candidate_task.id = candidate_version.task_id
        LEFT JOIN LATERAL (
            SELECT attestation.healthy
            FROM task_version_health_attestations AS attestation
            WHERE attestation.task_id = candidate_version.task_id
                AND attestation.task_version = candidate_version.version
            ORDER BY attestation.revision DESC
            LIMIT 1
            FOR UPDATE
        ) AS health ON true
        WHERE authority.tournament_id = NEW.tournament_id
            AND authority.roster_id = NEW.roster_id
            AND authority.series_id = NEW.series_id
            AND authority.slot_id = NEW.slot_id
            AND authority.assignment_id = NEW.assignment_id
            AND authority.assignment_attempt_id = NEW.assignment_attempt_id
            AND authority.active_snapshot_id = NEW.from_snapshot_id
            AND authority.assignment_revision = NEW.expected_assignment_revision
            AND authority.pool_revision_id = NEW.expected_pool_revision_id
            AND authority.pool_revision = NEW.expected_pool_revision
            AND authority.history_revision_id = NEW.expected_history_revision_id
            AND authority.history_revision = NEW.expected_history_revision
            AND authority.artifact_revision_id = NEW.expected_artifact_revision_id
            AND authority.artifact_revision = NEW.expected_artifact_revision
            AND authority.reservation_revision_id = NEW.expected_reservation_revision_id
            AND authority.reservation_revision = NEW.expected_reservation_revision
            AND authority.category_revision_id = NEW.expected_category_revision_id
            AND authority.category_revision = NEW.expected_category_revision
            AND candidate_version.category = authority.required_category
            AND candidate_version.content_digest = NEW.content_digest
            AND candidate_task.enabled
            AND candidate_task.deleted_at IS NULL
            AND COALESCE(health.healthy, false)
            AND NOT EXISTS (
                SELECT 1
                FROM task_delivery_receipts AS receipt
                WHERE receipt.task_id = candidate_version.task_id
                    AND receipt.task_version = candidate_version.version
            )
            AND NOT EXISTS (
                SELECT 1
                FROM task_version_reservations AS used_reservation
                WHERE used_reservation.plan_id = assignment.plan_id
                    AND used_reservation.branch_id = assignment.branch_id
                    AND used_reservation.task_id = candidate_version.task_id
                    AND used_reservation.task_version = candidate_version.version
                    AND used_reservation.state = 'committed'
            )
        FOR UPDATE OF authority, assignment, candidate_version, candidate_task;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'operator reserve authority is missing, stale, or ineligible'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'replay_replacements' THEN
        IF series_state <> 'replay_required'
            OR series_revision <> NEW.source_series_revision THEN
            RAISE EXCEPTION 'replay replacement source revision is stale'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        RAISE EXCEPTION 'unknown replay command evidence table'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.replay_command_target_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    series_state VARCHAR(32);
    series_revision BIGINT;
    target_count INTEGER;
BEGIN
    SELECT state, revision
    INTO series_state, series_revision
    FROM series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id;

    IF TG_TABLE_NAME = 'replay_reserve_exhaustions' THEN
        IF series_state <> 'technical_pause'
            OR series_revision <> NEW.resulting_series_revision THEN
            RAISE EXCEPTION 'replay exhaustion did not commit its paused Series'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'operator_replay_reserves' THEN
        SELECT COUNT(*)
        INTO target_count
        FROM assignment_plan_edges AS edge
        JOIN task_version_reservations AS reservation
            ON reservation.edge_id = edge.id
            AND reservation.id = NEW.reservation_id
        JOIN task_snapshots AS snapshot
            ON snapshot.reservation_id = reservation.id
            AND snapshot.id = NEW.proposed_snapshot_id
        WHERE edge.id = NEW.edge_id
            AND edge.operator_reserve_command_id = NEW.command_id
            AND edge.position = NEW.reserve_position
            AND edge.task_id = NEW.proposed_task_id
            AND edge.task_version = NEW.proposed_version
            AND reservation.state = 'committed';

        IF target_count <> 1
            OR series_state <> 'replay_required'
            OR series_revision <> NEW.resulting_series_revision THEN
            RAISE EXCEPTION 'operator reserve target graph is incomplete'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'replay_replacements' THEN
        SELECT COUNT(*)
        INTO target_count
        FROM waves AS wave
        JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
        JOIN wave_series AS membership
            ON membership.wave_id = wave.id
            AND membership.series_id = NEW.series_id
        JOIN game_attempts AS game_attempt
            ON game_attempt.id = NEW.replacement_game_id
            AND game_attempt.series_id = NEW.series_id
        JOIN assignments AS assignment
            ON assignment.id = NEW.replacement_assignment_attempt_id
            AND assignment.attempt_id = game_attempt.id
        JOIN task_version_reservations AS reservation
            ON reservation.id = assignment.reservation_id
            AND reservation.state = 'committed'
        JOIN assignment_plan_edges AS edge
            ON edge.id = reservation.edge_id
            AND edge.plan_id = assignment.plan_id
            AND edge.branch_id = assignment.branch_id
            AND edge.task_id = assignment.task_id
            AND edge.task_version = assignment.task_version
        WHERE wave.id = NEW.replacement_wave_id
            AND wave.revision_id = NEW.replacement_wave_revision_id
            AND wave.state = 'ready_window_open'
            AND ready_window.id = NEW.ready_window_id
            AND ready_window.revision_id = NEW.ready_window_revision_id
            AND ready_window.state = 'open'
            AND game_attempt.state = 'planned'
            AND assignment.snapshot_id = NEW.snapshot_id
            AND assignment.state = 'active'
            AND edge.position = NEW.reserve_position
            AND (
                (NEW.reserve_position <= 3 AND edge.operator_reserve_command_id IS NULL)
                OR (
                    NEW.reserve_position = 4
                    AND EXISTS (
                        SELECT 1
                        FROM operator_replay_reserves AS operator_reserve
                        WHERE operator_reserve.command_id = edge.operator_reserve_command_id
                            AND operator_reserve.tournament_id = NEW.tournament_id
                            AND operator_reserve.old_wave_id = NEW.old_wave_id
                            AND operator_reserve.series_id = NEW.series_id
                            AND operator_reserve.slot_id = NEW.slot_id
                            AND operator_reserve.assignment_id = NEW.assignment_id
                            AND operator_reserve.assignment_attempt_id = NEW.assignment_attempt_id
                            AND operator_reserve.failed_game_id = NEW.failed_game_id
                            AND operator_reserve.proposed_snapshot_id = NEW.snapshot_id
                    )
                )
            )
            AND (
                SELECT COUNT(*)
                FROM wave_members AS member
                JOIN wave_readiness AS readiness
                    ON readiness.wave_id = member.wave_id
                    AND readiness.participant_id = member.participant_id
                WHERE member.wave_id = wave.id
                    AND readiness.ready_window_id = ready_window.id
                    AND NOT readiness.ready
            ) = 2;

        IF target_count <> 1
            OR series_state <> 'ready'
            OR series_revision <> NEW.resulting_series_revision THEN
            RAISE EXCEPTION 'replay replacement target graph is incomplete'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

-- +goose StatementEnd
