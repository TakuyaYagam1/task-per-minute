-- +goose Up
-- +goose StatementBegin

-- Deployment: this is a coordinated schema/application upgrade. Stop writers
-- before applying it, then start the application that records history versions.
-- Existing child-history writers omit that version and cannot run after Up.
-- The reservation backfill and index rebuild scan existing rows while ALTER
-- TABLE holds its transaction locks. Size these tables and budget a maintenance
-- window before deployment; this is not an online migration for large tables.
-- Goose rolls the whole transaction back on failure, so retry from version 17
-- after correcting the cause. Verify every reservation's tournament_id matches
-- assignment_plans.tournament_id, compare receipt/plan counts, and confirm that
-- private receipts have created no task_public_exposures rows.
-- Down is permitted only while the pre-upgrade representation remains lossless.
-- Once new evidence exists, retain version 18 and recover with a forward fix or
-- a separately verified backup; never delete evidence just to make Down pass.

-- A private participant receipt is not evidence that a task version was shown
-- to the public.  Public exposure is an independent, append-only fact whose
-- exact task identity is durable and reusable by every assignment boundary.
CREATE TABLE public.task_public_exposures (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    audience character varying(16) NOT NULL,
    evidence_id uuid NOT NULL,
    disclosed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT task_public_exposures_pkey PRIMARY KEY (id),
    CONSTRAINT task_public_exposures_identity_key UNIQUE (
        task_id,
        task_version,
        audience,
        evidence_id
    ),
    CONSTRAINT task_public_exposures_audience_check CHECK (
        audience IN ('public', 'spectator')
    ),
    CONSTRAINT task_public_exposures_identity_check CHECK (
        task_version >= 1
        AND task_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND evidence_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND disclosed_at <= created_at
    )
);

ALTER TABLE ONLY public.task_public_exposures
    ADD CONSTRAINT task_public_exposures_task_version_fk
    FOREIGN KEY (task_id, task_version)
    REFERENCES public.task_versions(task_id, version)
    ON DELETE RESTRICT;

CREATE FUNCTION public.task_public_exposure_immutable_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'public task exposure evidence is append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER task_public_exposures_immutable_guard
BEFORE DELETE OR UPDATE ON public.task_public_exposures
FOR EACH ROW EXECUTE FUNCTION public.task_public_exposure_immutable_guard();

CREATE FUNCTION public.task_public_exposure_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.id = '00000000-0000-0000-0000-000000000000'::uuid THEN
        RAISE EXCEPTION 'public task exposure evidence id must be non-zero'
            USING ERRCODE = 'check_violation';
    END IF;

    PERFORM 1
    FROM task_versions AS task_version
    WHERE task_version.task_id = NEW.task_id
        AND task_version.version = NEW.task_version
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'public task exposure references an unknown task version'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER task_public_exposures_insert_guard
BEFORE INSERT ON public.task_public_exposures
FOR EACH ROW EXECUTE FUNCTION public.task_public_exposure_insert_guard();

-- Reservations carry the tournament scope that was previously obtained only
-- through plan_id.  Keeping it on the row lets PostgreSQL enforce live
-- reservation exclusivity per tournament while preserving historical rows.
ALTER TABLE public.task_version_reservations
    ADD COLUMN tournament_id uuid;

ALTER TABLE public.task_version_reservations
    DISABLE TRIGGER task_version_reservation_guard;

UPDATE public.task_version_reservations AS reservation
SET tournament_id = plan.tournament_id
FROM public.assignment_plans AS plan
WHERE plan.id = reservation.plan_id;

ALTER TABLE public.task_version_reservations
    ENABLE TRIGGER task_version_reservation_guard;

ALTER TABLE public.task_version_reservations
    ALTER COLUMN tournament_id SET NOT NULL;

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_tournament_fk
    FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id)
    ON DELETE RESTRICT;

DROP INDEX IF EXISTS public.task_version_reservations_active_idx;
DROP INDEX IF EXISTS public.task_version_reservations_contingency_active_idx;

CREATE UNIQUE INDEX task_version_reservations_active_idx
    ON public.task_version_reservations USING btree (
        tournament_id,
        task_id,
        task_version
    )
    WHERE contingency_draft_branch_id IS NULL
        AND (state)::text = ANY ((ARRAY['reserved'::character varying, 'committed'::character varying])::text[]);

CREATE UNIQUE INDEX task_version_reservations_contingency_active_idx
    ON public.task_version_reservations USING btree (
        tournament_id,
        task_id,
        task_version,
        contingency_draft_branch_id
    )
    WHERE contingency_draft_branch_id IS NOT NULL
        AND (state)::text = ANY ((ARRAY['reserved'::character varying, 'committed'::character varying])::text[]);

ALTER TABLE public.task_delivery_receipts
    DROP CONSTRAINT task_delivery_receipts_participant_task_key,
    ADD CONSTRAINT task_delivery_receipts_participant_task_version_key
    UNIQUE (participant_id, task_id, task_version);

-- The reservation trigger is the last database boundary before a task version
-- becomes usable.  It remains the authority for identity, transition and
-- contention checks, with tournament scoped locking and exposure fencing.
CREATE OR REPLACE FUNCTION public.task_version_reservation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    branch_state VARCHAR(16);
    plan_state VARCHAR(16);
    plan_tournament_id UUID;
    operator_reserve_command_id UUID;
    branch_contingency_draft_id UUID;
    reservation_conflict BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT branch.state,
            plan.state,
            plan.tournament_id,
            edge.operator_reserve_command_id,
            branch.exact_draft_branch_id
        INTO branch_state,
            plan_state,
            plan_tournament_id,
            operator_reserve_command_id,
            branch_contingency_draft_id
        FROM assignment_branches AS branch
        JOIN assignment_plans AS plan ON plan.id = branch.plan_id
        JOIN assignment_plan_edges AS edge
            ON edge.id = NEW.edge_id
            AND edge.plan_id = NEW.plan_id
            AND edge.branch_id = NEW.branch_id
            AND edge.task_id = NEW.task_id
            AND edge.task_version = NEW.task_version
        WHERE branch.id = NEW.branch_id AND branch.plan_id = NEW.plan_id
        FOR UPDATE OF branch, plan, edge;

        IF plan_tournament_id IS NULL THEN
            RAISE EXCEPTION 'task-version reservation plan is missing'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.tournament_id IS NULL THEN
            NEW.tournament_id := plan_tournament_id;
        ELSIF NEW.tournament_id IS DISTINCT FROM plan_tournament_id THEN
            RAISE EXCEPTION 'task-version reservation tournament is outside its plan'
                USING ERRCODE = 'check_violation';
        END IF;

        IF operator_reserve_command_id IS NULL THEN
            IF NEW.state <> 'reserved' OR branch_state <> 'reserved' OR plan_state <> 'planned' THEN
                RAISE EXCEPTION 'planned task versions require an uncommitted branch'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF NEW.state <> 'committed'
            OR NEW.committed_at IS NULL
            OR branch_state <> 'active'
            OR plan_state <> 'committed' THEN
            RAISE EXCEPTION 'operator reserve requires a committed reservation on the active branch'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.contingency_draft_branch_id IS DISTINCT FROM branch_contingency_draft_id THEN
            RAISE EXCEPTION 'reservation contingency group must match its assignment branch'
                USING ERRCODE = 'check_violation';
        END IF;

        PERFORM pg_advisory_xact_lock(
            hashtextextended(
                NEW.tournament_id::TEXT || ':' || NEW.task_id::TEXT || ':' || NEW.task_version::TEXT,
                0
            )
        );

        PERFORM 1
        FROM task_versions AS task_version
        WHERE task_version.task_id = NEW.task_id
            AND task_version.version = NEW.task_version
        FOR UPDATE;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'task-version reservation references an unknown task version'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM task_public_exposures AS exposure
            WHERE exposure.task_id = NEW.task_id
                AND exposure.task_version = NEW.task_version
        ) THEN
            RAISE EXCEPTION 'publicly exposed task versions cannot be reserved'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT EXISTS (
            SELECT 1
            FROM task_version_reservations AS existing
            WHERE existing.tournament_id = NEW.tournament_id
                AND existing.task_id = NEW.task_id
                AND existing.task_version = NEW.task_version
                AND existing.state IN ('reserved', 'committed')
                AND (
                    NEW.contingency_draft_branch_id IS NULL
                    OR existing.contingency_draft_branch_id IS NULL
                    OR existing.plan_id <> NEW.plan_id
                    OR existing.state = 'committed'
                )
        ) INTO reservation_conflict;

        IF reservation_conflict THEN
            RAISE EXCEPTION 'task version conflicts with a non-contingent or committed reservation'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.edge_id IS DISTINCT FROM OLD.edge_id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.branch_id IS DISTINCT FROM OLD.branch_id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.task_id IS DISTINCT FROM OLD.task_id
        OR NEW.task_version IS DISTINCT FROM OLD.task_version
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'task-version reservation identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT plan.tournament_id
    INTO plan_tournament_id
    FROM assignment_plans AS plan
    WHERE plan.id = NEW.plan_id
    FOR KEY SHARE;

    IF plan_tournament_id IS NULL
        OR NEW.tournament_id IS DISTINCT FROM plan_tournament_id THEN
        RAISE EXCEPTION 'task-version reservation tournament is outside its plan'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'task-version reservation update must advance revision exactly once'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.contingency_draft_branch_id IS DISTINCT FROM OLD.contingency_draft_branch_id THEN
        RAISE EXCEPTION 'task-version reservation contingency group is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended(
            NEW.tournament_id::TEXT || ':' || NEW.task_id::TEXT || ':' || NEW.task_version::TEXT,
            0
        )
    );

    IF NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at THEN
        IF OLD.state <> 'committed'
            OR OLD.disclosed_at IS NOT NULL
            OR NEW.disclosed_at IS NULL THEN
            RAISE EXCEPTION 'reservation disclosure is monotonic and committed-only'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF OLD.state IN ('released', 'superseded')
        OR (OLD.state = 'reserved' AND NEW.state NOT IN (
            'reserved', 'committed', 'released', 'superseded'
        ))
        OR (OLD.state = 'committed' AND NEW.state NOT IN (
            'committed', 'superseded'
        )) THEN
        RAISE EXCEPTION 'invalid task-version reservation transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'released' AND NEW.disclosed_at IS NOT NULL THEN
        RAISE EXCEPTION 'a disclosed task version cannot be released for reuse'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'committed' AND OLD.state <> 'committed' THEN
        PERFORM 1
        FROM task_versions AS task_version
        WHERE task_version.task_id = NEW.task_id
            AND task_version.version = NEW.task_version
        FOR UPDATE;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'task-version reservation references an unknown task version'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM task_public_exposures AS exposure
            WHERE exposure.task_id = NEW.task_id
                AND exposure.task_version = NEW.task_version
        ) THEN
            RAISE EXCEPTION 'publicly exposed task versions cannot be committed'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT EXISTS (
            SELECT 1
            FROM task_version_reservations AS existing
            WHERE existing.tournament_id = NEW.tournament_id
                AND existing.task_id = NEW.task_id
                AND existing.task_version = NEW.task_version
                AND existing.id <> NEW.id
                AND existing.state IN ('reserved', 'committed')
        ) INTO reservation_conflict;

        IF reservation_conflict THEN
            RAISE EXCEPTION 'committed task version cannot retain another live reservation'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

-- These guards complement the existing graph authority checks. Each one takes
-- the task-version lock and then reads exposure in a separate statement, so a
-- public disclosure cannot race a stale write.
CREATE FUNCTION public.task_public_exposure_assignment_edge_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM task_versions AS task_version
    WHERE task_version.task_id = NEW.task_id
        AND task_version.version = NEW.task_version
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'assignment edge references an unknown task version'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = NEW.task_id
            AND exposure.task_version = NEW.task_version
    ) THEN
        RAISE EXCEPTION 'publicly exposed task versions cannot enter an assignment edge'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER assignment_edge_public_exposure_guard
BEFORE INSERT ON public.assignment_plan_edges
FOR EACH ROW EXECUTE FUNCTION public.task_public_exposure_assignment_edge_guard();

CREATE FUNCTION public.task_public_exposure_delivery_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM task_versions AS task_version
    WHERE task_version.task_id = NEW.task_id
        AND task_version.version = NEW.task_version
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'delivery receipt references an unknown task version'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = NEW.task_id
            AND exposure.task_version = NEW.task_version
    ) THEN
        RAISE EXCEPTION 'publicly exposed task versions cannot be delivered privately'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER delivery_receipt_public_exposure_guard
BEFORE INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.task_public_exposure_delivery_guard();

-- Golden runtime assignments are a separate delivery boundary and do not
-- create task_delivery_receipts. Fence their insert directly as well.
CREATE FUNCTION public.golden_runtime_assignment_public_exposure_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM task_versions AS task_version
    WHERE task_version.task_id = NEW.task_id
        AND task_version.version = NEW.task_version
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'Golden runtime assignment references an unknown task version'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = NEW.task_id
            AND exposure.task_version = NEW.task_version
    ) THEN
        RAISE EXCEPTION 'publicly exposed task versions cannot enter Golden runtime'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER golden_runtime_assignment_public_exposure_guard
BEFORE INSERT ON public.golden_runtime_assignments
FOR EACH ROW EXECUTE FUNCTION public.golden_runtime_assignment_public_exposure_guard();

-- Operator replay reserve selection has its own source table and can happen
-- after the sealed candidate snapshot. Fence that late candidate directly.
CREATE FUNCTION public.operator_replay_reserve_public_exposure_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM task_versions AS task_version
    WHERE task_version.task_id = NEW.proposed_task_id
        AND task_version.version = NEW.proposed_version
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'operator replay reserve references an unknown task version'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM task_public_exposures AS exposure
        WHERE exposure.task_id = NEW.proposed_task_id
            AND exposure.task_version = NEW.proposed_version
    ) THEN
        RAISE EXCEPTION 'publicly exposed task versions cannot enter replay reserve'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER operator_replay_reserve_public_exposure_guard
BEFORE INSERT ON public.operator_replay_reserves
FOR EACH ROW EXECUTE FUNCTION public.operator_replay_reserve_public_exposure_guard();

-- Replay authority must use the same tournament, roster, participant and
-- task-version scope as normal planning.  Private receipts from another
-- participant or tournament do not consume this candidate, and public
-- exposure is an independent hard exclusion.
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
        JOIN series AS target_series
            ON target_series.id = authority.series_id
            AND target_series.tournament_id = authority.tournament_id
            AND target_series.roster_id = authority.roster_id
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
                FROM task_public_exposures AS exposure
                WHERE exposure.task_id = candidate_version.task_id
                    AND exposure.task_version = candidate_version.version
            )
            AND NOT EXISTS (
                SELECT 1
                FROM task_delivery_receipts AS receipt
                JOIN assignments AS receipt_assignment
                    ON receipt_assignment.id = receipt.assignment_id
                JOIN series AS receipt_series
                    ON receipt_series.id = receipt_assignment.series_id
                    AND receipt_series.roster_id = receipt_assignment.roster_id
                WHERE receipt.task_id = candidate_version.task_id
                    AND receipt.task_version = candidate_version.version
                    AND receipt_series.tournament_id = authority.tournament_id
                    AND receipt_series.roster_id = authority.roster_id
                    AND receipt.participant_id IN (
                        target_series.first_participant_id,
                        target_series.second_participant_id
                    )
            )
            AND NOT EXISTS (
                SELECT 1
                FROM task_version_reservations AS used_reservation
                WHERE used_reservation.tournament_id = authority.tournament_id
                    AND used_reservation.plan_id = assignment.plan_id
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

-- Version zero is retained only for rows written before task-version evidence
-- was added to child history.  New writes must bind the exact receipt version.
ALTER TABLE public.exact_draft_assignment_child_history
    ADD COLUMN task_version integer NOT NULL DEFAULT 0;

ALTER TABLE public.exact_draft_assignment_child_history
    DROP CONSTRAINT exact_draft_assignment_child_history_pkey,
    ADD CONSTRAINT exact_draft_assignment_child_history_pkey PRIMARY KEY (
        child_branch_id,
        participant_id,
        task_id,
        task_version
    ),
    ADD CONSTRAINT exact_draft_assignment_child_history_version_check CHECK (task_version >= 0);

CREATE OR REPLACE FUNCTION public.exact_draft_assignment_child_history_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'exact draft child history is immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.task_version < 1 THEN
        RAISE EXCEPTION 'new exact draft child history requires a positive task version'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM task_delivery_receipts AS receipt
        WHERE receipt.participant_id = NEW.participant_id
            AND receipt.task_id = NEW.task_id
            AND receipt.task_version = NEW.task_version
    ) THEN
        RAISE EXCEPTION 'exact draft child history must match an exact delivered task version'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- A receipt for one participant advances every exact-normal history head for
-- that participant's target series in the same tournament and roster.  This
-- preserves the complete cross-series history used by later planning.
CREATE OR REPLACE FUNCTION public.exact_normal_assignment_history_head_bump() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE exact_normal_assignment_history_heads AS head
    SET revision = head.revision + 1,
        revision_id = gen_random_uuid(),
        updated_at = GREATEST(head.updated_at, NEW.created_at)
    FROM assignments AS receipt_assignment
    INNER JOIN series AS receipt_series
        ON receipt_series.id = receipt_assignment.series_id
        AND receipt_series.roster_id = receipt_assignment.roster_id
    INNER JOIN series AS target_series
        ON target_series.tournament_id = receipt_series.tournament_id
        AND target_series.roster_id = receipt_series.roster_id
        AND NEW.participant_id IN (
            target_series.first_participant_id,
            target_series.second_participant_id
        )
    INNER JOIN game_slots AS target_slot
        ON target_slot.series_id = target_series.id
        AND target_slot.roster_id = target_series.roster_id
    WHERE receipt_assignment.id = NEW.assignment_id
        AND head.tournament_id = receipt_series.tournament_id
        AND head.roster_id = receipt_series.roster_id
        AND head.series_id = target_series.id
        AND target_slot.id = head.slot_id;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS exact_normal_assignment_history_head_bump ON public.task_delivery_receipts;
CREATE TRIGGER exact_normal_assignment_history_head_bump
AFTER INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.exact_normal_assignment_history_head_bump();

-- Keep Swiss history heads scoped to the receipt's authoritative tournament.
CREATE OR REPLACE FUNCTION public.swiss_draft_delivery_history_head_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Swiss draft delivery history heads follow immutable receipts'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE swiss_draft_delivery_history_heads AS head
    SET revision_id = gen_random_uuid(),
        revision = head.revision + 1,
        updated_at = GREATEST(head.updated_at, NEW.created_at)
    FROM assignments AS receipt_assignment
    INNER JOIN series AS receipt_series
        ON receipt_series.id = receipt_assignment.series_id
        AND receipt_series.roster_id = receipt_assignment.roster_id
    WHERE receipt_assignment.id = NEW.assignment_id
        AND head.tournament_id = receipt_series.tournament_id
        AND head.roster_id = receipt_series.roster_id
        AND NEW.participant_id IN (head.first_participant_id, head.second_participant_id);

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS swiss_draft_delivery_history_head_guard ON public.task_delivery_receipts;
CREATE TRIGGER swiss_draft_delivery_history_head_guard
AFTER INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.swiss_draft_delivery_history_head_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Freeze the affected evidence before checking whether the old representation
-- is lossless. A concurrent writer must commit before these checks, not between
-- the checks and the removal of its table or version column.
LOCK TABLE public.task_public_exposures,
    public.task_delivery_receipts,
    public.task_version_reservations,
    public.exact_draft_assignment_child_history
    IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.task_public_exposures) THEN
        RAISE EXCEPTION 'cannot roll back task exposure evidence while rows exist';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.exact_draft_assignment_child_history
        WHERE task_version > 0
    ) THEN
        RAISE EXCEPTION 'cannot roll back positive exact draft child history versions';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.task_delivery_receipts
        GROUP BY participant_id, task_id
        HAVING COUNT(DISTINCT task_version) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back task delivery version identity while multiple versions exist';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.task_version_reservations
        WHERE state IN ('reserved', 'committed')
        GROUP BY task_id, task_version
        HAVING COUNT(DISTINCT tournament_id) > 1
    ) THEN
        RAISE EXCEPTION 'cannot roll back tournament scoped reservations while cross-tournament rows exist';
    END IF;
END;
$$;

-- Restore the migration-007 reservation authority before removing its scope
-- column. This keeps the old trigger valid for a clean down/up cycle.
CREATE OR REPLACE FUNCTION public.task_version_reservation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    branch_state VARCHAR(16);
    plan_state VARCHAR(16);
    operator_reserve_command_id UUID;
    branch_contingency_draft_id UUID;
    reservation_conflict BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT branch.state,
            plan.state,
            edge.operator_reserve_command_id,
            branch.exact_draft_branch_id
        INTO branch_state,
            plan_state,
            operator_reserve_command_id,
            branch_contingency_draft_id
        FROM assignment_branches AS branch
        JOIN assignment_plans AS plan ON plan.id = branch.plan_id
        JOIN assignment_plan_edges AS edge
            ON edge.id = NEW.edge_id
            AND edge.plan_id = NEW.plan_id
            AND edge.branch_id = NEW.branch_id
            AND edge.task_id = NEW.task_id
            AND edge.task_version = NEW.task_version
        WHERE branch.id = NEW.branch_id AND branch.plan_id = NEW.plan_id
        FOR UPDATE OF branch, plan, edge;

        IF operator_reserve_command_id IS NULL THEN
            IF NEW.state <> 'reserved' OR branch_state <> 'reserved' OR plan_state <> 'planned' THEN
                RAISE EXCEPTION 'planned task versions require an uncommitted branch'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF NEW.state <> 'committed'
            OR NEW.committed_at IS NULL
            OR branch_state <> 'active'
            OR plan_state <> 'committed' THEN
            RAISE EXCEPTION 'operator reserve requires a committed reservation on the active branch'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.contingency_draft_branch_id IS DISTINCT FROM branch_contingency_draft_id THEN
            RAISE EXCEPTION 'reservation contingency group must match its assignment branch'
                USING ERRCODE = 'check_violation';
        END IF;

        PERFORM pg_advisory_xact_lock(
            hashtextextended(NEW.task_id::TEXT || ':' || NEW.task_version::TEXT, 0)
        );

        SELECT EXISTS (
            SELECT 1
            FROM task_version_reservations AS existing
            WHERE existing.task_id = NEW.task_id
                AND existing.task_version = NEW.task_version
                AND existing.state IN ('reserved', 'committed')
                AND (
                    NEW.contingency_draft_branch_id IS NULL
                    OR existing.contingency_draft_branch_id IS NULL
                    OR existing.plan_id <> NEW.plan_id
                    OR existing.state = 'committed'
                )
        ) INTO reservation_conflict;

        IF reservation_conflict THEN
            RAISE EXCEPTION 'task version conflicts with a non-contingent or committed reservation'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.edge_id IS DISTINCT FROM OLD.edge_id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.branch_id IS DISTINCT FROM OLD.branch_id
        OR NEW.task_id IS DISTINCT FROM OLD.task_id
        OR NEW.task_version IS DISTINCT FROM OLD.task_version
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'task-version reservation identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'task-version reservation update must advance revision exactly once'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.contingency_draft_branch_id IS DISTINCT FROM OLD.contingency_draft_branch_id THEN
        RAISE EXCEPTION 'task-version reservation contingency group is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.task_id::TEXT || ':' || NEW.task_version::TEXT, 0)
    );

    IF NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at THEN
        IF OLD.state <> 'committed'
            OR OLD.disclosed_at IS NOT NULL
            OR NEW.disclosed_at IS NULL THEN
            RAISE EXCEPTION 'reservation disclosure is monotonic and committed-only'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF OLD.state IN ('released', 'superseded')
        OR (OLD.state = 'reserved' AND NEW.state NOT IN (
            'reserved', 'committed', 'released', 'superseded'
        ))
        OR (OLD.state = 'committed' AND NEW.state NOT IN (
            'committed', 'superseded'
        )) THEN
        RAISE EXCEPTION 'invalid task-version reservation transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'released' AND NEW.disclosed_at IS NOT NULL THEN
        RAISE EXCEPTION 'a disclosed task version cannot be released for reuse'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'committed' AND OLD.state <> 'committed' THEN
        SELECT EXISTS (
            SELECT 1
            FROM task_version_reservations AS existing
            WHERE existing.task_id = NEW.task_id
                AND existing.task_version = NEW.task_version
                AND existing.id <> NEW.id
                AND existing.state IN ('reserved', 'committed')
        ) INTO reservation_conflict;

        IF reservation_conflict THEN
            RAISE EXCEPTION 'committed task version cannot retain another live reservation'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

-- Restore the baseline child-history, exact-normal and Swiss receipt guards.
CREATE OR REPLACE FUNCTION public.exact_draft_assignment_child_history_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'exact draft child history is immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM task_delivery_receipts AS receipt
        WHERE receipt.participant_id = NEW.participant_id
            AND receipt.task_id = NEW.task_id
    ) THEN
        RAISE EXCEPTION 'exact draft child history must match a delivered task'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.exact_normal_assignment_history_head_bump() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE exact_normal_assignment_history_heads AS head
    SET revision = head.revision + 1,
        revision_id = gen_random_uuid(),
        updated_at = GREATEST(head.updated_at, NEW.created_at)
    FROM assignments AS assignment
    INNER JOIN series AS series
        ON series.id = assignment.series_id
        AND series.roster_id = assignment.roster_id
    INNER JOIN game_attempts AS attempt
        ON attempt.id = assignment.attempt_id
        AND attempt.series_id = assignment.series_id
        AND attempt.roster_id = assignment.roster_id
    WHERE assignment.id = NEW.assignment_id
        AND head.tournament_id = series.tournament_id
        AND head.roster_id = assignment.roster_id
        AND head.series_id = assignment.series_id
        AND head.slot_id = attempt.slot_id;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.swiss_draft_delivery_history_head_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Swiss draft delivery history heads follow immutable receipts'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE swiss_draft_delivery_history_heads AS head
    SET revision_id = gen_random_uuid(),
        revision = head.revision + 1,
        updated_at = GREATEST(head.updated_at, NEW.created_at)
    WHERE head.roster_id = NEW.roster_id
        AND NEW.participant_id IN (head.first_participant_id, head.second_participant_id);

    RETURN NEW;
END;
$$;

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

DROP TRIGGER IF EXISTS task_public_exposures_insert_guard ON public.task_public_exposures;
DROP FUNCTION IF EXISTS public.task_public_exposure_insert_guard();
DROP TRIGGER IF EXISTS task_public_exposures_immutable_guard ON public.task_public_exposures;
DROP FUNCTION IF EXISTS public.task_public_exposure_immutable_guard();
DROP TRIGGER IF EXISTS assignment_edge_public_exposure_guard ON public.assignment_plan_edges;
DROP FUNCTION IF EXISTS public.task_public_exposure_assignment_edge_guard();
DROP TRIGGER IF EXISTS delivery_receipt_public_exposure_guard ON public.task_delivery_receipts;
DROP FUNCTION IF EXISTS public.task_public_exposure_delivery_guard();
DROP TRIGGER IF EXISTS golden_runtime_assignment_public_exposure_guard ON public.golden_runtime_assignments;
DROP FUNCTION IF EXISTS public.golden_runtime_assignment_public_exposure_guard();
DROP TRIGGER IF EXISTS operator_replay_reserve_public_exposure_guard ON public.operator_replay_reserves;
DROP FUNCTION IF EXISTS public.operator_replay_reserve_public_exposure_guard();
DROP TABLE IF EXISTS public.task_public_exposures;

DROP TRIGGER IF EXISTS exact_normal_assignment_history_head_bump ON public.task_delivery_receipts;
CREATE TRIGGER exact_normal_assignment_history_head_bump
AFTER INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.exact_normal_assignment_history_head_bump();

DROP TRIGGER IF EXISTS swiss_draft_delivery_history_head_guard ON public.task_delivery_receipts;
CREATE TRIGGER swiss_draft_delivery_history_head_guard
AFTER INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.swiss_draft_delivery_history_head_guard();

ALTER TABLE public.exact_draft_assignment_child_history
    DROP CONSTRAINT exact_draft_assignment_child_history_version_check,
    DROP CONSTRAINT exact_draft_assignment_child_history_pkey,
    DROP COLUMN task_version,
    ADD CONSTRAINT exact_draft_assignment_child_history_pkey PRIMARY KEY (
        child_branch_id,
        participant_id,
        task_id
    );

ALTER TABLE public.task_delivery_receipts
    DROP CONSTRAINT task_delivery_receipts_participant_task_version_key,
    ADD CONSTRAINT task_delivery_receipts_participant_task_key
    UNIQUE (participant_id, task_id);

DROP INDEX IF EXISTS public.task_version_reservations_active_idx;
DROP INDEX IF EXISTS public.task_version_reservations_contingency_active_idx;

ALTER TABLE public.task_version_reservations
    DROP CONSTRAINT task_version_reservations_tournament_fk,
    DROP COLUMN tournament_id;

CREATE UNIQUE INDEX task_version_reservations_active_idx
    ON public.task_version_reservations USING btree (task_id, task_version)
    WHERE contingency_draft_branch_id IS NULL
        AND (state)::text = ANY ((ARRAY['reserved'::character varying, 'committed'::character varying])::text[]);

CREATE UNIQUE INDEX task_version_reservations_contingency_active_idx
    ON public.task_version_reservations USING btree (
        task_id,
        task_version,
        contingency_draft_branch_id
    )
    WHERE contingency_draft_branch_id IS NOT NULL
        AND (state)::text = ANY ((ARRAY['reserved'::character varying, 'committed'::character varying])::text[]);

-- +goose StatementEnd
