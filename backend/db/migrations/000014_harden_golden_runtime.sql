-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.golden_runtime_assignments
    ADD COLUMN plan_id uuid,
    ADD COLUMN edge_position smallint,
    ADD COLUMN ready_window_id uuid,
    ADD COLUMN ready_window_opened_at timestamp with time zone,
    ADD COLUMN ready_window_deadline timestamp with time zone;

UPDATE public.golden_runtime_assignments AS runtime
SET plan_id = edge.plan_id,
    edge_position = edge.position,
    ready_window_id = gen_random_uuid(),
    ready_window_opened_at = runtime.created_at,
    ready_window_deadline = runtime.created_at + interval '30 seconds'
FROM public.golden_exact_plan_snapshot_edges AS edge
WHERE edge.edge_id = runtime.wave_id
    AND edge.reservation_id = runtime.assignment_id
    AND edge.snapshot_id = runtime.snapshot_id
    AND edge.group_revision_id = runtime.group_revision_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.golden_runtime_assignments
        WHERE plan_id IS NULL
            OR edge_position IS NULL
            OR ready_window_id IS NULL
            OR ready_window_opened_at IS NULL
            OR ready_window_deadline IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot harden Golden runtime with assignments outside a sealed exact plan';
    END IF;
END;
$$;

ALTER TABLE public.golden_runtime_assignments
    ALTER COLUMN plan_id SET NOT NULL,
    ALTER COLUMN edge_position SET NOT NULL,
    ALTER COLUMN ready_window_id SET NOT NULL,
    ALTER COLUMN ready_window_opened_at SET NOT NULL,
    ALTER COLUMN ready_window_deadline SET NOT NULL,
    DROP CONSTRAINT golden_runtime_assignments_group_key,
    DROP CONSTRAINT golden_runtime_assignments_settlement_key,
    ADD CONSTRAINT golden_runtime_assignments_plan_edge_fk FOREIGN KEY (
        plan_id, wave_id
    ) REFERENCES public.golden_exact_plan_snapshot_edges(plan_id, edge_id) ON DELETE RESTRICT,
    ADD CONSTRAINT golden_runtime_assignments_edge_position_check CHECK (
        edge_position BETWEEN 1 AND 3
    ),
    ADD CONSTRAINT golden_runtime_assignments_ready_window_check CHECK (
        ready_window_deadline = ready_window_opened_at + interval '30 seconds'
    ),
    ADD CONSTRAINT golden_runtime_assignments_group_edge_key UNIQUE (
        group_revision_id, edge_position
    ),
    ADD CONSTRAINT golden_runtime_assignments_ready_window_key UNIQUE (ready_window_id);

CREATE OR REPLACE FUNCTION public.golden_attempt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number INTEGER;
    ready_count INTEGER;
    unresolved_direct_count INTEGER;
    membership_count INTEGER;
    no_show_count INTEGER;
    participant_count INTEGER;
    open_disconnect_count INTEGER;
    uncommitted_count INTEGER;
    recovery_state VARCHAR(24);
    recovery_recorded_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.previous_attempt_id IS NOT NULL THEN
            SELECT attempt_number INTO previous_number
            FROM golden_attempts
            WHERE id = NEW.previous_attempt_id
            FOR KEY SHARE;

            IF previous_number <> NEW.attempt_number - 1 THEN
                RAISE EXCEPTION 'Golden attempt lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden attempts are retained stage history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
        OR NEW.previous_attempt_id IS DISTINCT FROM OLD.previous_attempt_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Golden attempt identity and lineage are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (OLD.disclosed_at IS NOT NULL AND NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at)
        OR (OLD.ready_at IS NOT NULL AND NEW.ready_at IS DISTINCT FROM OLD.ready_at)
        OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at)
        OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
        OR (OLD.cancelled_at IS NOT NULL AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (OLD.cancellation_reason IS NOT NULL AND NEW.cancellation_reason IS DISTINCT FROM OLD.cancellation_reason)
        OR (OLD.supersession_reason IS NOT NULL AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason) THEN
        RAISE EXCEPTION 'Golden attempt lifecycle evidence is immutable once recorded'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'prepared' AND NEW.state IN ('ready', 'cancelled', 'superseded'))
            OR (OLD.state = 'ready' AND NEW.state IN ('active', 'cancelled', 'superseded'))
            OR (OLD.state = 'active' AND NEW.state IN ('technical_pause', 'completed', 'cancelled'))
            OR (OLD.state = 'technical_pause' AND NEW.state IN ('active', 'completed', 'cancelled'))
        ) THEN
        RAISE EXCEPTION 'invalid Golden attempt lifecycle transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'active' AND NEW.state = 'technical_pause' THEN
        SELECT state, recorded_at
        INTO recovery_state, recovery_recorded_at
        FROM golden_recovery_revisions
        WHERE attempt_id = NEW.id
        ORDER BY revision_number DESC
        LIMIT 1;

        IF recovery_state IS DISTINCT FROM 'technical_pause'
            OR recovery_recorded_at IS NULL
            OR recovery_recorded_at > NEW.paused_at THEN
            RAISE EXCEPTION 'Golden technical pause requires current recovery evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.state = 'technical_pause' AND NEW.state = 'active' THEN
        SELECT state, recorded_at
        INTO recovery_state, recovery_recorded_at
        FROM golden_recovery_revisions
        WHERE attempt_id = NEW.id
        ORDER BY revision_number DESC
        LIMIT 1;

        IF recovery_state IS DISTINCT FROM 'resumed'
            OR recovery_recorded_at IS NULL
            OR recovery_recorded_at < OLD.paused_at THEN
            RAISE EXCEPTION 'Golden resume requires current recovery evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'ready' AND OLD.state <> 'ready' THEN
        SELECT
            COUNT(*) FILTER (WHERE ready_at IS NOT NULL),
            COUNT(*) FILTER (WHERE selection_kind = 'direct' AND ready_at IS NULL AND no_show_at IS NULL),
            COUNT(*),
            COUNT(*) FILTER (WHERE no_show_at IS NOT NULL)
        INTO ready_count, unresolved_direct_count, membership_count, no_show_count
        FROM golden_memberships
        WHERE attempt_id = NEW.id;

        IF unresolved_direct_count <> 0
            OR (ready_count < 1 AND (membership_count = 0 OR no_show_count <> membership_count)) THEN
            RAISE EXCEPTION 'Golden readiness requires ready members or a fully resolved no-show group'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT
            COUNT(*) FILTER (WHERE participation_established_at IS NOT NULL),
            COUNT(*),
            COUNT(*) FILTER (WHERE no_show_at IS NOT NULL)
        INTO participant_count, membership_count, no_show_count
        FROM golden_memberships
        WHERE attempt_id = NEW.id;

        SELECT COUNT(*) INTO open_disconnect_count
        FROM golden_ready_disconnects
        WHERE attempt_id = NEW.id AND state = 'open';

        IF open_disconnect_count <> 0
            OR (participant_count < 1 AND (membership_count = 0 OR no_show_count <> membership_count)) THEN
            RAISE EXCEPTION 'Golden start requires participants or a fully resolved no-show group without open disconnects'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'completed' AND OLD.state <> 'completed' THEN
        SELECT
            COUNT(*) FILTER (WHERE participation_established_at IS NOT NULL),
            COUNT(*),
            COUNT(*) FILTER (WHERE no_show_at IS NOT NULL),
            COUNT(*) FILTER (
                WHERE participation_established_at IS NOT NULL
                    AND NOT EXISTS (
                        SELECT 1
                        FROM golden_position_commits AS position_commit
                        WHERE position_commit.membership_id = membership.id
                    )
                    AND NOT EXISTS (
                        SELECT 1
                        FROM golden_attempts AS successor
                        INNER JOIN golden_memberships AS successor_membership
                            ON successor_membership.attempt_id = successor.id
                            AND successor_membership.participant_id = membership.participant_id
                        INNER JOIN golden_runtime_assignments AS current_runtime
                            ON current_runtime.attempt_id = NEW.id
                        INNER JOIN golden_runtime_assignments AS successor_runtime
                            ON successor_runtime.attempt_id = successor.id
                            AND successor_runtime.group_revision_id = current_runtime.group_revision_id
                            AND successor_runtime.edge_position = current_runtime.edge_position + 1
                    )
            )
        INTO participant_count, membership_count, no_show_count, uncommitted_count
        FROM golden_memberships AS membership
        WHERE attempt_id = NEW.id;

        IF uncommitted_count <> 0
            OR (
                participant_count = 0
                AND (
                    membership_count = 0
                    OR no_show_count <> membership_count
                    OR EXISTS (
                        SELECT 1
                        FROM golden_memberships AS no_show_membership
                        WHERE no_show_membership.attempt_id = NEW.id
                            AND NOT EXISTS (
                                SELECT 1
                                FROM golden_position_commits AS position_commit
                                WHERE position_commit.membership_id = no_show_membership.id
                            )
                    )
                )
            ) THEN
            RAISE EXCEPTION 'Golden completion requires committed participants or exact successor evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.golden_position_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    submission_status VARCHAR(16);
    submission_position SMALLINT;
    participation_at TIMESTAMPTZ;
    no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
    current_attempt_number INTEGER;
    previous_attempt_number INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position commits are immutable terminal evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT status, provisional_position
    INTO submission_status, submission_position
    FROM golden_provisional_submissions
    WHERE id = NEW.provisional_submission_id
    FOR KEY SHARE;

    SELECT participation_established_at, golden_memberships.no_show_at
    INTO participation_at, no_show_at
    FROM golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state, attempt_number
    INTO attempt_state, current_attempt_number
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF submission_status <> 'accepted'
        OR submission_position <> NEW.position
        OR (participation_at IS NULL AND no_show_at IS NULL)
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden position commit must seal an accepted active submission'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_position_commit_id IS NOT NULL THEN
        SELECT attempt.attempt_number INTO previous_attempt_number
        FROM golden_position_commits AS position_commit
        INNER JOIN golden_attempts AS attempt ON attempt.id = position_commit.attempt_id
        WHERE position_commit.id = NEW.previous_position_commit_id
        FOR KEY SHARE OF position_commit, attempt;

        IF previous_attempt_number >= current_attempt_number THEN
            RAISE EXCEPTION 'Golden prior position must belong to an earlier attempt'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.golden_submission_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    participation_at TIMESTAMPTZ;
    no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden provisional submissions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT participation_established_at, golden_memberships.no_show_at
    INTO participation_at, no_show_at
    FROM golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF (participation_at IS NULL AND no_show_at IS NULL)
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden provisional submission requires participant or no-show evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

DO $$
DECLARE
    current_definition TEXT;
    hardened_definition TEXT;
BEGIN
    current_definition := pg_get_functiondef(
        'public.validate_tournament_stage_playoff_evidence()'::regprocedure
    );
    hardened_definition := replace(
        current_definition,
        'AND membership.participation_established_at IS NOT NULL',
        'AND (membership.participation_established_at IS NOT NULL OR membership.no_show_at IS NOT NULL)'
    );
    IF hardened_definition = current_definition THEN
        RAISE EXCEPTION 'cannot harden playoff Golden no-show evidence validation';
    END IF;
    EXECUTE hardened_definition;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    current_definition TEXT;
    restored_definition TEXT;
BEGIN
    current_definition := pg_get_functiondef(
        'public.validate_tournament_stage_playoff_evidence()'::regprocedure
    );
    restored_definition := replace(
        current_definition,
        'AND (membership.participation_established_at IS NOT NULL OR membership.no_show_at IS NOT NULL)',
        'AND membership.participation_established_at IS NOT NULL'
    );
    IF restored_definition = current_definition THEN
        RAISE EXCEPTION 'cannot restore playoff Golden evidence validation';
    END IF;
    EXECUTE restored_definition;
END;
$$;

CREATE OR REPLACE FUNCTION public.golden_submission_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    participation_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden provisional submissions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;
    SELECT participation_established_at INTO participation_at
    FROM golden_memberships WHERE id = NEW.membership_id FOR NO KEY UPDATE;
    SELECT state INTO attempt_state FROM golden_attempts
    WHERE id = NEW.attempt_id FOR NO KEY UPDATE;
    IF participation_at IS NULL OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden provisional submission requires an active participant'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.golden_position_commit_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    submission_status VARCHAR(16);
    submission_position SMALLINT;
    participation_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
    current_attempt_number INTEGER;
    previous_attempt_number INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position commits are immutable terminal evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT status, provisional_position INTO submission_status, submission_position
    FROM golden_provisional_submissions WHERE id = NEW.provisional_submission_id FOR KEY SHARE;
    SELECT participation_established_at INTO participation_at
    FROM golden_memberships WHERE id = NEW.membership_id FOR NO KEY UPDATE;
    SELECT state, attempt_number INTO attempt_state, current_attempt_number
    FROM golden_attempts WHERE id = NEW.attempt_id FOR NO KEY UPDATE;

    IF submission_status <> 'accepted'
        OR submission_position <> NEW.position
        OR participation_at IS NULL
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden position commit must seal an accepted active submission'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_position_commit_id IS NOT NULL THEN
        SELECT attempt.attempt_number INTO previous_attempt_number
        FROM golden_position_commits AS position_commit
        INNER JOIN golden_attempts AS attempt ON attempt.id = position_commit.attempt_id
        WHERE position_commit.id = NEW.previous_position_commit_id
        FOR KEY SHARE OF position_commit, attempt;
        IF previous_attempt_number >= current_attempt_number THEN
            RAISE EXCEPTION 'Golden prior position must belong to an earlier attempt'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.golden_attempt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_number INTEGER;
    ready_count INTEGER;
    unresolved_direct_count INTEGER;
    participant_count INTEGER;
    open_disconnect_count INTEGER;
    uncommitted_count INTEGER;
    recovery_state VARCHAR(24);
    recovery_recorded_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.previous_attempt_id IS NOT NULL THEN
            SELECT attempt_number INTO previous_number FROM golden_attempts
            WHERE id = NEW.previous_attempt_id FOR KEY SHARE;
            IF previous_number <> NEW.attempt_number - 1 THEN
                RAISE EXCEPTION 'Golden attempt lineage must be consecutive' USING ERRCODE = 'check_violation';
            END IF;
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden attempts are retained stage history' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
        OR NEW.previous_attempt_id IS DISTINCT FROM OLD.previous_attempt_id OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Golden attempt identity and lineage are immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF (OLD.disclosed_at IS NOT NULL AND NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at)
        OR (OLD.ready_at IS NOT NULL AND NEW.ready_at IS DISTINCT FROM OLD.ready_at)
        OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at)
        OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
        OR (OLD.cancelled_at IS NOT NULL AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (OLD.cancellation_reason IS NOT NULL AND NEW.cancellation_reason IS DISTINCT FROM OLD.cancellation_reason)
        OR (OLD.supersession_reason IS NOT NULL AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason) THEN
        RAISE EXCEPTION 'Golden attempt lifecycle evidence is immutable once recorded' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state AND NOT (
        (OLD.state = 'prepared' AND NEW.state IN ('ready', 'cancelled', 'superseded'))
        OR (OLD.state = 'ready' AND NEW.state IN ('active', 'cancelled', 'superseded'))
        OR (OLD.state = 'active' AND NEW.state IN ('technical_pause', 'completed', 'cancelled'))
        OR (OLD.state = 'technical_pause' AND NEW.state IN ('active', 'completed', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid Golden attempt lifecycle transition' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.state = 'active' AND NEW.state = 'technical_pause' THEN
        SELECT state, recorded_at INTO recovery_state, recovery_recorded_at
        FROM golden_recovery_revisions WHERE attempt_id = NEW.id ORDER BY revision_number DESC LIMIT 1;
        IF recovery_state IS DISTINCT FROM 'technical_pause' OR recovery_recorded_at IS NULL OR recovery_recorded_at > NEW.paused_at THEN
            RAISE EXCEPTION 'Golden technical pause requires current recovery evidence' USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.state = 'technical_pause' AND NEW.state = 'active' THEN
        SELECT state, recorded_at INTO recovery_state, recovery_recorded_at
        FROM golden_recovery_revisions WHERE attempt_id = NEW.id ORDER BY revision_number DESC LIMIT 1;
        IF recovery_state IS DISTINCT FROM 'resumed' OR recovery_recorded_at IS NULL OR recovery_recorded_at < OLD.paused_at THEN
            RAISE EXCEPTION 'Golden resume requires current recovery evidence' USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    IF NEW.state = 'ready' AND OLD.state <> 'ready' THEN
        SELECT COUNT(*) FILTER (WHERE ready_at IS NOT NULL),
            COUNT(*) FILTER (WHERE selection_kind = 'direct' AND ready_at IS NULL AND no_show_at IS NULL)
        INTO ready_count, unresolved_direct_count FROM golden_memberships WHERE attempt_id = NEW.id;
        IF ready_count < 2 OR unresolved_direct_count <> 0 THEN
            RAISE EXCEPTION 'Golden readiness requires two ready members and resolved direct selections' USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*) INTO participant_count FROM golden_memberships
        WHERE attempt_id = NEW.id AND participation_established_at IS NOT NULL;
        SELECT COUNT(*) INTO open_disconnect_count FROM golden_ready_disconnects
        WHERE attempt_id = NEW.id AND state = 'open';
        IF participant_count < 2 OR open_disconnect_count <> 0 THEN
            RAISE EXCEPTION 'Golden start requires established participants without open disconnects' USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    IF NEW.state = 'completed' AND OLD.state <> 'completed' THEN
        SELECT COUNT(*) FILTER (WHERE participation_established_at IS NOT NULL),
            COUNT(*) FILTER (WHERE participation_established_at IS NOT NULL AND NOT EXISTS (
                SELECT 1 FROM golden_position_commits AS position_commit WHERE position_commit.membership_id = membership.id
            ))
        INTO participant_count, uncommitted_count FROM golden_memberships AS membership WHERE attempt_id = NEW.id;
        IF participant_count < 2 OR uncommitted_count <> 0 THEN
            RAISE EXCEPTION 'Golden completion requires every participant position commit' USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.golden_runtime_assignments
        GROUP BY group_revision_id
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot remove Golden continuation evidence while a group has multiple attempts';
    END IF;
END;
$$;

ALTER TABLE public.golden_runtime_assignments
    DROP CONSTRAINT golden_runtime_assignments_ready_window_key,
    DROP CONSTRAINT golden_runtime_assignments_group_edge_key,
    DROP CONSTRAINT golden_runtime_assignments_ready_window_check,
    DROP CONSTRAINT golden_runtime_assignments_edge_position_check,
    DROP CONSTRAINT golden_runtime_assignments_plan_edge_fk,
    ADD CONSTRAINT golden_runtime_assignments_group_key UNIQUE (group_revision_id),
    ADD CONSTRAINT golden_runtime_assignments_settlement_key UNIQUE (settlement_revision_id),
    DROP COLUMN ready_window_deadline,
    DROP COLUMN ready_window_opened_at,
    DROP COLUMN ready_window_id,
    DROP COLUMN edge_position,
    DROP COLUMN plan_id;
-- +goose StatementEnd
