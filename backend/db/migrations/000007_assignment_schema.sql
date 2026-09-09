-- +goose Up
-- +goose StatementBegin

-- Initial assignment domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: assignment_branch_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_branch_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_state VARCHAR(16);
    source_revision_id UUID;
    plan_roster_id UUID;
    revision_roster_id UUID;
    group_category_sequence JSONB;
    committed_reservations INTEGER;
    reusable_reservations INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT kind, state, source_draft_revision_id, roster_id
        INTO plan_kind, plan_state, source_revision_id, plan_roster_id
        FROM assignment_plans
        WHERE id = NEW.plan_id
        FOR UPDATE;

        SELECT roster_id
        INTO revision_roster_id
        FROM draft_revisions
        WHERE id = NEW.draft_revision_id;

        IF plan_kind NOT IN ('exact', 'exact_draft')
            OR plan_state <> 'planned'
            OR NEW.draft_revision_id IS DISTINCT FROM source_revision_id
            OR revision_roster_id IS DISTINCT FROM plan_roster_id THEN
            RAISE EXCEPTION 'branches belong only to the current exact plan revision'
                USING ERRCODE = 'check_violation';
        END IF;

        IF plan_kind = 'exact_draft' THEN
            SELECT category_sequence
            INTO group_category_sequence
            FROM exact_draft_assignment_branches
            WHERE id = NEW.exact_draft_branch_id
                AND plan_id = NEW.plan_id
            FOR KEY SHARE;

            IF NEW.exact_draft_branch_id IS NULL
                OR NEW.exact_draft_position NOT BETWEEN 1 AND 3
                OR group_category_sequence IS NULL
                OR NEW.category_sequence IS DISTINCT FROM jsonb_build_array(
                    group_category_sequence -> (NEW.exact_draft_position - 1)
                ) THEN
                RAISE EXCEPTION 'exact draft child must bind one canonical category position'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF NEW.exact_draft_branch_id IS NOT NULL OR NEW.exact_draft_position IS NOT NULL THEN
            RAISE EXCEPTION 'ordinary exact assignment branches cannot carry a draft group'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.draft_id IS DISTINCT FROM OLD.draft_id
        OR NEW.draft_revision_id IS DISTINCT FROM OLD.draft_revision_id
        OR NEW.branch_key IS DISTINCT FROM OLD.branch_key
        OR NEW.category_sequence IS DISTINCT FROM OLD.category_sequence
        OR NEW.exact_draft_branch_id IS DISTINCT FROM OLD.exact_draft_branch_id
        OR NEW.exact_draft_position IS DISTINCT FROM OLD.exact_draft_position
        OR NEW.decision_evidence_id IS DISTINCT FROM OLD.decision_evidence_id
        OR NEW.decision_algorithm_version IS DISTINCT FROM OLD.decision_algorithm_version
        OR NEW.decision_inputs IS DISTINCT FROM OLD.decision_inputs
        OR NEW.decision_seed IS DISTINCT FROM OLD.decision_seed
        OR NEW.decision_result IS DISTINCT FROM OLD.decision_result
        OR NEW.decision_replay_digest IS DISTINCT FROM OLD.decision_replay_digest
        OR NEW.decision_owner_id IS DISTINCT FROM OLD.decision_owner_id
        OR NEW.decided_at IS DISTINCT FROM OLD.decided_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'assignment branch identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state IN ('released', 'superseded')
        OR (OLD.state = 'reserved' AND NEW.state NOT IN (
            'reserved', 'active', 'released', 'superseded'
        ))
        OR (OLD.state = 'active' AND NEW.state NOT IN ('active', 'superseded')) THEN
        RAISE EXCEPTION 'invalid assignment branch transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*)
        INTO committed_reservations
        FROM task_version_reservations
        WHERE branch_id = NEW.id AND state = 'committed';

        IF committed_reservations <> 3 THEN
            RAISE EXCEPTION 'active branch requires one committed primary and two reserves'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'released' AND OLD.state <> 'released' THEN
        SELECT COUNT(*)
        INTO reusable_reservations
        FROM task_version_reservations
        WHERE branch_id = NEW.id AND state = 'released' AND disclosed_at IS NULL;

        IF reusable_reservations <> 3 THEN
            RAISE EXCEPTION 'released branch must retain three undisclosed released reservations'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: assignment_branch_transition_evidence_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_branch_transition_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    referencing_plan_state VARCHAR(16);
BEGIN
    IF OLD.activated_at IS NOT NULL
        AND NEW.activated_at IS DISTINCT FROM OLD.activated_at THEN
        RAISE EXCEPTION 'branch activation evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.disclosed_at IS NOT NULL
        AND NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at THEN
        RAISE EXCEPTION 'branch disclosure evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'active' AND NEW.state <> 'active' THEN
        SELECT state
        INTO referencing_plan_state
        FROM assignment_plans
        WHERE active_branch_id = OLD.id
        FOR UPDATE;

        IF referencing_plan_state = 'committed' THEN
            RAISE EXCEPTION 'committed plan must retain its active branch'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

-- Exact-draft groups retain the full reachable draft branch while ordinary
-- assignment_branches retain one category position and its three task
-- reservations. Keeping both rows normalized prevents a completed draft from
-- silently discarding losing reservations or selected category order.
CREATE FUNCTION public.exact_draft_assignment_branch_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_state VARCHAR(16);
    source_draft_revision_id UUID;
    child_count INTEGER;
    active_child_count INTEGER;
    released_child_count INTEGER;
    child_source_count INTEGER;
    invalid_child_source_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT plan.kind, plan.state, plan.source_draft_revision_id
        INTO plan_kind, plan_state, source_draft_revision_id
        FROM assignment_plans AS plan
        WHERE plan.id = NEW.plan_id
        FOR UPDATE;

        IF plan_kind <> 'exact_draft'
            OR plan_state <> 'planned'
            OR NEW.state <> 'reserved'
            OR NEW.draft_revision_id IS DISTINCT FROM source_draft_revision_id
            OR jsonb_typeof(NEW.category_sequence) <> 'array'
            OR jsonb_array_length(NEW.category_sequence) <> 3 THEN
            RAISE EXCEPTION 'exact draft group requires a planned exact-draft plan and three categories'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.draft_id IS DISTINCT FROM OLD.draft_id
        OR NEW.draft_revision_id IS DISTINCT FROM OLD.draft_revision_id
        OR NEW.branch_key IS DISTINCT FROM OLD.branch_key
        OR NEW.category_sequence IS DISTINCT FROM OLD.category_sequence
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'exact draft branch identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state IN ('released', 'superseded')
        OR (OLD.state = 'reserved' AND NEW.state NOT IN ('reserved', 'active', 'released', 'superseded'))
        OR (OLD.state = 'active' AND NEW.state NOT IN ('active', 'superseded')) THEN
        RAISE EXCEPTION 'invalid exact draft branch transition'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO child_count
    FROM assignment_branches
    WHERE plan_id = NEW.plan_id
        AND exact_draft_branch_id = NEW.id;

    IF child_count <> 3 THEN
        RAISE EXCEPTION 'exact draft branch must retain one child for each category position'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*)
        INTO active_child_count
        FROM assignment_branches AS child
        JOIN task_version_reservations AS reservation
            ON reservation.plan_id = child.plan_id
            AND reservation.branch_id = child.id
            AND reservation.state = 'committed'
        WHERE child.plan_id = NEW.plan_id
            AND child.exact_draft_branch_id = NEW.id
            AND child.state = 'active';

        IF active_child_count <> 9 THEN
            RAISE EXCEPTION 'active exact draft branch requires every child reservation committed'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'released' AND OLD.state <> 'released' THEN
        SELECT COUNT(*)
        INTO released_child_count
        FROM assignment_branches AS child
        JOIN task_version_reservations AS reservation
            ON reservation.plan_id = child.plan_id
            AND reservation.branch_id = child.id
            AND reservation.state = 'released'
            AND reservation.disclosed_at IS NULL
        WHERE child.plan_id = NEW.plan_id
            AND child.exact_draft_branch_id = NEW.id
            AND child.state = 'released';

        IF released_child_count <> 9 THEN
            RAISE EXCEPTION 'released exact draft branch must release every undisclosed child reservation'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.exact_draft_assignment_child_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_tournament_id UUID;
    plan_roster_id UUID;
    child_group_id UUID;
    child_position SMALLINT;
    draft_series_id UUID;
    draft_category_revision_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'exact draft child sources are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT plan.kind,
        plan.tournament_id,
        plan.roster_id,
        child.exact_draft_branch_id,
        child.exact_draft_position,
        draft.series_id,
        draft.category_revision_id
    INTO plan_kind,
        plan_tournament_id,
        plan_roster_id,
        child_group_id,
        child_position,
        draft_series_id,
        draft_category_revision_id
    FROM assignment_branches AS child
    INNER JOIN assignment_plans AS plan ON plan.id = child.plan_id
    INNER JOIN drafts AS draft ON draft.id = child.draft_id
    WHERE child.id = NEW.child_branch_id
        AND child.plan_id = NEW.plan_id
    FOR KEY SHARE OF child, plan, draft;

    IF plan_kind <> 'exact_draft'
        OR child_group_id IS NULL
        OR child_position NOT BETWEEN 1 AND 3
        OR NEW.tournament_id IS DISTINCT FROM plan_tournament_id
        OR NEW.roster_id IS DISTINCT FROM plan_roster_id
        OR NEW.series_id IS DISTINCT FROM draft_series_id
        OR NEW.category_lock_id IS DISTINCT FROM draft_category_revision_id
        OR NEW.category_revision_id IS DISTINCT FROM draft_category_revision_id
        OR NEW.created_at IS NULL THEN
        RAISE EXCEPTION 'exact draft child source is outside its final draft authority'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.exact_draft_assignment_child_participant_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_tournament_id UUID;
    source_roster_id UUID;
    draft_first_participant_id UUID;
    draft_second_participant_id UUID;
    participant_roster_id UUID;
    participant_player_id UUID;
    reservation_tournament_id UUID;
    reservation_id UUID;
    reservation_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'exact draft child participants are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT source.tournament_id,
        source.roster_id,
        draft.first_participant_id,
        draft.second_participant_id
    INTO source_tournament_id,
        source_roster_id,
        draft_first_participant_id,
        draft_second_participant_id
    FROM exact_draft_assignment_child_sources AS source
    INNER JOIN assignment_branches AS child
        ON child.id = source.child_branch_id
        AND child.plan_id = source.plan_id
    INNER JOIN drafts AS draft ON draft.id = child.draft_id
    WHERE source.child_branch_id = NEW.child_branch_id
        AND source.plan_id = NEW.plan_id
    FOR KEY SHARE OF source, child, draft;

    SELECT participant.roster_id,
        participant.player_id,
        reservation.tournament_id,
        reservation.reservation_id,
        reservation.revision
    INTO participant_roster_id,
        participant_player_id,
        reservation_tournament_id,
        reservation_id,
        reservation_revision
    FROM participants AS participant
    INNER JOIN participant_reservations AS reservation
        ON reservation.player_id = participant.player_id
    WHERE participant.id = NEW.participant_id
    FOR KEY SHARE OF participant, reservation;

    IF source_tournament_id IS NULL
        OR NEW.tournament_id IS DISTINCT FROM source_tournament_id
        OR participant_roster_id IS DISTINCT FROM source_roster_id
        OR participant_player_id IS DISTINCT FROM NEW.player_id
        OR reservation_tournament_id IS DISTINCT FROM source_tournament_id
        OR reservation_id IS DISTINCT FROM NEW.reservation_id
        OR reservation_revision IS DISTINCT FROM NEW.reservation_revision
        OR NEW.participant_id NOT IN (
            draft_first_participant_id,
            draft_second_participant_id
        )
        OR NEW.acquired_at IS NULL
        OR NEW.updated_at IS NULL
        OR NEW.created_at IS NULL THEN
        RAISE EXCEPTION 'exact draft child participant is outside its draft authority'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.exact_draft_assignment_child_history_guard() RETURNS trigger
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

--
-- Name: assignment_edge_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_edge_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_state VARCHAR(16);
    branch_state VARCHAR(16);
    locked_task_id UUID;
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'assignment selected edges are immutable proof evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT plan.state, branch.state
    INTO plan_state, branch_state
    FROM assignment_plans AS plan
    JOIN assignment_branches AS branch ON branch.plan_id = plan.id
    WHERE plan.id = NEW.plan_id AND branch.id = NEW.branch_id
    FOR UPDATE OF plan, branch;

    IF NEW.operator_reserve_command_id IS NULL THEN
        IF plan_state <> 'planned' OR branch_state <> 'reserved' OR NEW.position > 3 THEN
            RAISE EXCEPTION 'planned edges require a reserved draft branch and position 1..3'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF plan_state <> 'committed' OR branch_state <> 'active' OR NEW.position <= 3 THEN
        RAISE EXCEPTION 'operator reserve edges require the committed active branch after position 3'
            USING ERRCODE = 'check_violation';
    END IF;

    -- Assignment plans persist a published pool rather than a tournament stage.
    -- The normal pool is deliberately shared by Swiss, semifinal, and final.
    SELECT task.id
    INTO locked_task_id
    FROM assignment_plans AS plan
    JOIN tournament_content_configurations AS configuration
        ON configuration.tournament_id = plan.tournament_id
        AND configuration.state = 'published'
        AND plan.source_pool_revision_id IN (
            configuration.normal_pool_revision_id,
            configuration.golden_pool_revision_id
        )
    JOIN task_pool_version_memberships AS membership
        ON membership.task_pool_revision_id = plan.source_pool_revision_id
        AND membership.task_id = NEW.task_id
        AND membership.task_version = NEW.task_version
    JOIN tasks AS task ON task.id = membership.task_id
    JOIN task_versions AS task_version
        ON task_version.task_id = membership.task_id
        AND task_version.version = membership.task_version
    LEFT JOIN LATERAL (
        SELECT attestation.healthy
        FROM task_version_health_attestations AS attestation
        WHERE attestation.task_id = membership.task_id
            AND attestation.task_version = membership.task_version
        ORDER BY attestation.revision DESC
        LIMIT 1
    ) AS health ON true
    WHERE plan.id = NEW.plan_id
        AND task.enabled
        AND task.deleted_at IS NULL
        AND COALESCE(health.healthy, false)
    FOR NO KEY UPDATE OF task;

    IF locked_task_id IS NULL THEN
        RAISE EXCEPTION 'assignment edges require a healthy version in the published tournament content pool'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: assignment_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_state VARCHAR(16);
    plan_active_branch_id UUID;
    plan_active_draft_branch_id UUID;
    branch_state VARCHAR(16);
    branch_draft_group_id UUID;
    reservation_state VARCHAR(16);
    previous_attempt_id UUID;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT plan.kind,
            plan.state,
            plan.active_branch_id,
            plan.active_draft_branch_id,
            branch.state,
            branch.exact_draft_branch_id,
            reservation.state
        INTO plan_kind,
            plan_state,
            plan_active_branch_id,
            plan_active_draft_branch_id,
            branch_state,
            branch_draft_group_id,
            reservation_state
        FROM assignment_plans AS plan
        JOIN assignment_branches AS branch
            ON branch.plan_id = plan.id AND branch.id = NEW.branch_id
        JOIN task_version_reservations AS reservation
            ON reservation.plan_id = plan.id
            AND reservation.branch_id = branch.id
            AND reservation.id = NEW.reservation_id
        WHERE plan.id = NEW.plan_id;

        IF NEW.state <> 'active'
            OR plan_state <> 'committed'
            OR branch_state <> 'active'
            OR reservation_state <> 'committed'
            OR (
                plan_kind = 'exact'
                AND plan_active_branch_id IS DISTINCT FROM NEW.branch_id
            )
            OR (
                plan_kind = 'exact_draft'
                AND (
                    plan_active_draft_branch_id IS DISTINCT FROM branch_draft_group_id
                    OR branch_draft_group_id IS NULL
                )
            )
            OR plan_kind NOT IN ('exact', 'exact_draft') THEN
            RAISE EXCEPTION 'assignment requires the committed active branch and reservation'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.supersedes_assignment_id IS NOT NULL THEN
            SELECT attempt_id
            INTO previous_attempt_id
            FROM assignments
            WHERE id = NEW.supersedes_assignment_id AND state = 'superseded';

            IF previous_attempt_id IS DISTINCT FROM NEW.attempt_id THEN
                RAISE EXCEPTION 'reserve assignment must supersede the same Game attempt'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.branch_id IS DISTINCT FROM OLD.branch_id
        OR NEW.reservation_id IS DISTINCT FROM OLD.reservation_id
        OR NEW.snapshot_id IS DISTINCT FROM OLD.snapshot_id
        OR NEW.task_id IS DISTINCT FROM OLD.task_id
        OR NEW.task_version IS DISTINCT FROM OLD.task_version
        OR NEW.supersedes_assignment_id IS DISTINCT FROM OLD.supersedes_assignment_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'assignment identity and snapshot are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state <> 'active' OR NEW.state NOT IN ('active', 'completed', 'superseded') THEN
        RAISE EXCEPTION 'terminal assignment is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- Every concrete final assignment retains its own replay authority head. The
-- head is derived from the committed exact-draft child source, while the pool
-- rows retain the complete immutable candidate set for a later operator
-- reserve. A deferred trigger permits the canonical transaction to create the
-- assignment, head, and pool rows in that order, but rejects partial writers
-- at commit.
CREATE FUNCTION public.replay_reserve_authority_assignment_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
BEGIN
    SELECT plan.kind
    INTO plan_kind
    FROM assignment_plans AS plan
    WHERE plan.id = NEW.plan_id;

    IF plan_kind IS NULL THEN
        RAISE EXCEPTION 'assignment plan is missing while checking replay authority'
            USING ERRCODE = 'check_violation';
    END IF;

    IF plan_kind <> 'exact_draft' THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM replay_reserve_authorities AS authority
    JOIN assignments AS assignment ON assignment.id = authority.assignment_id
    JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
    JOIN assignment_branches AS branch
        ON branch.id = assignment.branch_id
        AND branch.plan_id = plan.id
    JOIN exact_draft_assignment_child_sources AS source
        ON source.child_branch_id = branch.id
        AND source.plan_id = plan.id
    JOIN task_version_reservations AS reservation
        ON reservation.id = assignment.reservation_id
        AND reservation.plan_id = plan.id
        AND reservation.branch_id = branch.id
    JOIN game_attempts AS attempt
        ON attempt.id = assignment.attempt_id
        AND attempt.series_id = assignment.series_id
        AND attempt.roster_id = assignment.roster_id
    WHERE authority.assignment_id = NEW.id
        AND authority.tournament_id = source.tournament_id
        AND authority.roster_id = source.roster_id
        AND authority.series_id = source.series_id
        AND authority.slot_id = source.slot_id
        AND authority.assignment_attempt_id = assignment.attempt_id
        AND authority.active_snapshot_id = assignment.snapshot_id
        AND authority.required_category = branch.category_sequence ->> 0
        AND authority.assignment_revision = assignment.revision
        AND authority.pool_revision_id = source.pool_revision_id
        AND authority.pool_revision = source.pool_revision
        AND authority.history_revision_id = source.history_revision_id
        AND authority.history_revision = source.history_revision
        AND authority.artifact_revision_id = source.artifact_revision_id
        AND authority.artifact_revision = source.artifact_revision
        AND authority.reservation_revision_id = reservation.id
        AND authority.reservation_revision = reservation.revision
        AND authority.category_revision_id = source.category_revision_id
        AND authority.category_revision = source.category_revision
        AND authority.revision = 1
        AND assignment.state = 'active'
        AND plan.kind = 'exact_draft'
        AND plan.state = 'committed'
        AND branch.state = 'active'
        AND reservation.state = 'committed'
        AND attempt.slot_id = source.slot_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'active exact-draft assignment lacks its exact replay authority head'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT candidate.task_id, candidate.task_version
        FROM exact_draft_assignment_child_candidates AS candidate
        WHERE candidate.plan_id = NEW.plan_id
            AND candidate.child_branch_id = NEW.branch_id
        EXCEPT
        SELECT pool_version.task_id, pool_version.task_version
        FROM replay_reserve_authority_pool_versions AS pool_version
        WHERE pool_version.assignment_id = NEW.id
    ) OR EXISTS (
        SELECT pool_version.task_id, pool_version.task_version
        FROM replay_reserve_authority_pool_versions AS pool_version
        WHERE pool_version.assignment_id = NEW.id
        EXCEPT
        SELECT candidate.task_id, candidate.task_version
        FROM exact_draft_assignment_child_candidates AS candidate
        WHERE candidate.plan_id = NEW.plan_id
            AND candidate.child_branch_id = NEW.branch_id
    ) THEN
        RAISE EXCEPTION 'replay authority pool does not match the immutable exact child source'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: assignment_plan_commit_evidence_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_plan_commit_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.state = 'committed'
        AND (
            NEW.active_branch_id IS DISTINCT FROM OLD.active_branch_id
            OR NEW.active_draft_branch_id IS DISTINCT FROM OLD.active_draft_branch_id
            OR NEW.completion_draft_revision_id IS DISTINCT FROM OLD.completion_draft_revision_id
            OR NEW.completion_draft_revision IS DISTINCT FROM OLD.completion_draft_revision
            OR NEW.activation_command_id IS DISTINCT FROM OLD.activation_command_id
            OR NEW.completed_categories IS DISTINCT FROM OLD.completed_categories
            OR NEW.committed_at IS DISTINCT FROM OLD.committed_at
        ) THEN
        RAISE EXCEPTION 'committed plan branch evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: assignment_plan_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_plan_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_kind VARCHAR(16);
    parent_tournament_id UUID;
    parent_roster_id UUID;
    parent_roster_revision BIGINT;
    parent_pool_revision_id UUID;
    branch_count INTEGER;
    active_count INTEGER;
    incomplete_count INTEGER;
    draft_group_count INTEGER;
    active_draft_group_count INTEGER;
    active_child_count INTEGER;
    released_child_count INTEGER;
    child_source_count INTEGER;
    invalid_child_source_count INTEGER;
    expected_active_child_id UUID;
    active_category_sequence JSONB;
    completion_matches BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.kind = 'exact' THEN
            SELECT
                kind,
                tournament_id,
                roster_id,
                source_roster_revision,
                source_pool_revision_id
            INTO
                parent_kind,
                parent_tournament_id,
                parent_roster_id,
                parent_roster_revision,
                parent_pool_revision_id
            FROM assignment_plans
            WHERE id = NEW.parent_plan_id;

            IF parent_kind <> 'conservative'
                OR parent_tournament_id IS DISTINCT FROM NEW.tournament_id
                OR parent_roster_id IS DISTINCT FROM NEW.roster_id
                OR parent_roster_revision IS DISTINCT FROM NEW.source_roster_revision
                OR parent_pool_revision_id IS DISTINCT FROM NEW.source_pool_revision_id THEN
                RAISE EXCEPTION 'exact assignment plan requires its matching conservative proof'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF NEW.kind = 'exact_draft' AND NEW.parent_plan_id IS NOT NULL THEN
            RAISE EXCEPTION 'exact draft plan cannot use a generic parent plan'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.kind IS DISTINCT FROM OLD.kind
        OR NEW.parent_plan_id IS DISTINCT FROM OLD.parent_plan_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.source_roster_revision IS DISTINCT FROM OLD.source_roster_revision
        OR NEW.source_pool_revision_id IS DISTINCT FROM OLD.source_pool_revision_id
        OR NEW.source_draft_revision_id IS DISTINCT FROM OLD.source_draft_revision_id
        OR NEW.reachable_branch_count IS DISTINCT FROM OLD.reachable_branch_count
        OR NEW.constraint_graph IS DISTINCT FROM OLD.constraint_graph
        OR NEW.proof_evidence IS DISTINCT FROM OLD.proof_evidence
        OR NEW.proof_hash IS DISTINCT FROM OLD.proof_hash
        OR NEW.decision_evidence_id IS DISTINCT FROM OLD.decision_evidence_id
        OR NEW.decision_algorithm_version IS DISTINCT FROM OLD.decision_algorithm_version
        OR NEW.decision_inputs IS DISTINCT FROM OLD.decision_inputs
        OR NEW.decision_seed IS DISTINCT FROM OLD.decision_seed
        OR NEW.decision_result IS DISTINCT FROM OLD.decision_result
        OR NEW.decision_replay_digest IS DISTINCT FROM OLD.decision_replay_digest
        OR NEW.decision_owner_id IS DISTINCT FROM OLD.decision_owner_id
        OR NEW.decided_at IS DISTINCT FROM OLD.decided_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'assignment proof identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'superseded'
        OR (OLD.state = 'planned' AND NEW.state NOT IN ('planned', 'committed', 'superseded'))
        OR (OLD.state = 'committed' AND NEW.state NOT IN ('committed', 'superseded')) THEN
        RAISE EXCEPTION 'invalid assignment plan transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'committed' AND OLD.state <> 'committed' AND NEW.kind = 'exact' THEN
        SELECT COUNT(*)
        INTO branch_count
        FROM assignment_branches
        WHERE plan_id = NEW.id;

        SELECT COUNT(*)
        INTO active_count
        FROM assignment_branches
        WHERE plan_id = NEW.id AND state = 'active' AND id = NEW.active_branch_id;

        SELECT COUNT(*)
        INTO incomplete_count
        FROM assignment_branches AS branch
        WHERE branch.plan_id = NEW.id
            AND (
                (
                    branch.id = NEW.active_branch_id
                    AND branch.state <> 'active'
                )
                OR (
                    branch.id <> NEW.active_branch_id
                    AND branch.state <> 'released'
                )
                OR (
                    SELECT COUNT(*)
                    FROM assignment_plan_edges AS edge
                    WHERE edge.branch_id = branch.id
                ) <> 3
                OR (
                    SELECT COUNT(*)
                    FROM task_version_reservations AS reservation
                    JOIN task_snapshots AS snapshot
                        ON snapshot.reservation_id = reservation.id
                    WHERE reservation.branch_id = branch.id
                ) <> 3
            );

        IF branch_count <> NEW.reachable_branch_count
            OR active_count <> 1
            OR incomplete_count <> 0 THEN
            RAISE EXCEPTION 'exact plan must cover every branch with one primary and two reserves'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'committed' AND OLD.state <> 'committed' AND NEW.kind = 'exact_draft' THEN
        SELECT COUNT(*)
        INTO draft_group_count
        FROM exact_draft_assignment_branches
        WHERE plan_id = NEW.id;

        SELECT COUNT(*)
        INTO active_draft_group_count
        FROM exact_draft_assignment_branches
        WHERE plan_id = NEW.id
            AND id = NEW.active_draft_branch_id
            AND state = 'active';

        SELECT child.id
        INTO expected_active_child_id
        FROM assignment_branches AS child
        WHERE child.plan_id = NEW.id
            AND child.exact_draft_branch_id = NEW.active_draft_branch_id
            AND child.exact_draft_position = 1
            AND child.state = 'active';

        SELECT category_sequence
        INTO active_category_sequence
        FROM exact_draft_assignment_branches
        WHERE id = NEW.active_draft_branch_id
            AND plan_id = NEW.id
            AND state = 'active';

        SELECT EXISTS (
            SELECT 1
            FROM draft_revisions AS source
            INNER JOIN draft_revisions AS completion
                ON completion.draft_id = source.draft_id
            WHERE source.id = NEW.source_draft_revision_id
                AND completion.id = NEW.completion_draft_revision_id
                AND completion.revision = NEW.completion_draft_revision
                AND completion.state = 'completed'
                AND completion.selected_categories = NEW.completed_categories
                AND NOT EXISTS (
                    SELECT 1
                    FROM draft_revisions AS later
                    WHERE later.draft_id = completion.draft_id
                        AND later.revision > completion.revision
                )
        ) INTO completion_matches;

        SELECT COUNT(*)
        INTO active_child_count
        FROM assignment_branches AS child
        JOIN task_version_reservations AS reservation
            ON reservation.branch_id = child.id
            AND reservation.plan_id = child.plan_id
            AND reservation.state = 'committed'
        WHERE child.plan_id = NEW.id
            AND child.exact_draft_branch_id = NEW.active_draft_branch_id
            AND child.state = 'active'
        GROUP BY child.exact_draft_branch_id;

        SELECT COUNT(*)
        INTO released_child_count
        FROM assignment_branches AS child
        JOIN task_version_reservations AS reservation
            ON reservation.branch_id = child.id
            AND reservation.plan_id = child.plan_id
            AND reservation.state = 'released'
            AND reservation.disclosed_at IS NULL
        WHERE child.plan_id = NEW.id
            AND child.exact_draft_branch_id <> NEW.active_draft_branch_id
            AND child.state = 'released';

        SELECT COUNT(*)
        INTO branch_count
        FROM assignment_branches
        WHERE plan_id = NEW.id;

        SELECT COUNT(*)
        INTO child_source_count
        FROM exact_draft_assignment_child_sources
        WHERE plan_id = NEW.id;

        SELECT COUNT(*)
        INTO invalid_child_source_count
        FROM assignment_branches AS child
        LEFT JOIN exact_draft_assignment_child_sources AS source
            ON source.child_branch_id = child.id
                AND source.plan_id = child.plan_id
        WHERE child.plan_id = NEW.id
            AND (
                source.child_branch_id IS NULL
                OR (
                    SELECT COUNT(*)
                    FROM exact_draft_assignment_child_participants AS participant
                    WHERE participant.child_branch_id = child.id
                        AND participant.plan_id = child.plan_id
                ) <> 2
                OR (
                    SELECT COUNT(*)
                    FROM exact_draft_assignment_child_history AS history
                    WHERE history.child_branch_id = child.id
                        AND history.plan_id = child.plan_id
                        AND history.participant_id NOT IN (
                            SELECT participant.participant_id
                            FROM exact_draft_assignment_child_participants AS participant
                            WHERE participant.child_branch_id = child.id
                                AND participant.plan_id = child.plan_id
                        )
                ) <> 0
                OR (
                    SELECT COUNT(*)
                    FROM exact_draft_assignment_child_history AS history
                    WHERE history.child_branch_id = child.id
                        AND history.plan_id = child.plan_id
                ) <> (
                    SELECT COUNT(*)
                    FROM task_delivery_receipts AS receipt
                    INNER JOIN exact_draft_assignment_child_participants AS participant
                        ON participant.child_branch_id = child.id
                        AND participant.plan_id = child.plan_id
                        AND participant.participant_id = receipt.participant_id
                    WHERE receipt.task_id IS NOT NULL
                )
                OR (
                    SELECT COUNT(*)
                    FROM exact_draft_assignment_child_candidates AS candidate
                    WHERE candidate.child_branch_id = child.id
                        AND candidate.plan_id = child.plan_id
                ) <> (
                    SELECT COUNT(*)
                    FROM task_pool_version_memberships AS membership
                    WHERE membership.task_pool_revision_id = source.pool_revision_id
                )
            );

        IF draft_group_count <> NEW.reachable_branch_count
            OR active_draft_group_count <> 1
            OR expected_active_child_id IS DISTINCT FROM NEW.active_branch_id
            OR active_category_sequence IS DISTINCT FROM NEW.completed_categories
            OR completion_matches IS DISTINCT FROM true
            OR active_child_count IS DISTINCT FROM 9
            OR released_child_count IS DISTINCT FROM (draft_group_count - 1) * 9
            OR branch_count <> draft_group_count * 3
            OR child_source_count <> branch_count
            OR invalid_child_source_count <> 0 THEN
            RAISE EXCEPTION 'exact draft plan must commit one full category branch and release every losing branch'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: assignment_plan_roster_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.assignment_plan_roster_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_roster_id UUID;
BEGIN
    SELECT roster_id
    INTO plan_roster_id
    FROM assignment_plans
    WHERE id = NEW.plan_id;

    IF plan_roster_id IS DISTINCT FROM NEW.roster_id THEN
        RAISE EXCEPTION 'assignment plan and Game attempt must share one roster'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: delivery_receipt_assignment_identity_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.participant_task_instance_id(assignment_id UUID, participant_id UUID)
    RETURNS UUID
    LANGUAGE sql
    IMMUTABLE
    STRICT
    PARALLEL SAFE
    AS $$
    SELECT encode(
        substring(
            set_byte(
                set_byte(
                    digest(uuid_send($1) || uuid_send($2), 'sha1'),
                    6,
                    (get_byte(digest(uuid_send($1) || uuid_send($2), 'sha1'), 6) & 15) | 80
                ),
                8,
                (get_byte(digest(uuid_send($1) || uuid_send($2), 'sha1'), 8) & 63) | 128
            ),
            1,
            16
        ),
        'hex'
    )::uuid;
$$;

CREATE FUNCTION public.delivery_receipt_assignment_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    assignment_snapshot_id UUID;
    assignment_task_id UUID;
    assignment_task_version INTEGER;
    expected_instance_id UUID;
BEGIN
    SELECT snapshot_id, task_id, task_version
    INTO assignment_snapshot_id, assignment_task_id, assignment_task_version
    FROM assignments
    WHERE id = NEW.assignment_id;

    expected_instance_id := public.participant_task_instance_id(
        NEW.assignment_id,
        NEW.participant_id
    );

    IF NEW.snapshot_id IS DISTINCT FROM assignment_snapshot_id
        OR NEW.task_id IS DISTINCT FROM assignment_task_id
        OR NEW.task_version IS DISTINCT FROM assignment_task_version
        OR NEW.instance_id IS DISTINCT FROM expected_instance_id THEN
        RAISE EXCEPTION 'delivery receipt must match assignment and deterministic instance identity'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: delivery_receipt_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.delivery_receipt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    first_participant_id UUID;
    second_participant_id UUID;
    disclosed_at TIMESTAMPTZ;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'task delivery receipts are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    -- Exact final-draft planning locks its two participant rows before it
    -- snapshots receipt history. Taking the same row lock here makes receipt
    -- append and planning serializable: either the receipt is visible to the
    -- plan or it waits until that plan has committed.
    PERFORM 1
    FROM participants AS participant
    WHERE participant.id = NEW.participant_id
        AND participant.roster_id = NEW.roster_id
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'delivery receipt participant is outside its roster'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT series.first_participant_id, series.second_participant_id, reservation.disclosed_at
    INTO first_participant_id, second_participant_id, disclosed_at
    FROM assignments AS assignment
    JOIN series AS series ON series.id = assignment.series_id
    JOIN task_version_reservations AS reservation
        ON reservation.id = assignment.reservation_id
    WHERE assignment.id = NEW.assignment_id AND assignment.state = 'active';

    IF NEW.participant_id NOT IN (first_participant_id, second_participant_id)
        OR disclosed_at IS NULL
        OR disclosed_at > NEW.delivered_at THEN
        RAISE EXCEPTION 'receipt requires committed disclosure to a Series participant'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: reservation_commit_evidence_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.reservation_commit_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.committed_at IS NOT NULL
        AND NEW.committed_at IS DISTINCT FROM OLD.committed_at THEN
        RAISE EXCEPTION 'reservation commit evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: task_snapshot_immutable_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.task_snapshot_immutable_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'task snapshots are immutable official evidence'
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: task_version_reservation_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.task_version_reservation_guard() RETURNS trigger
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

-- Replay command evidence is append-only. Source rows are locked and checked
-- before a command is accepted; target rows are checked again at commit by a
-- deferred constraint trigger.
CREATE FUNCTION public.replay_command_source_guard() RETURNS trigger
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

CREATE FUNCTION public.replay_command_target_guard() RETURNS trigger
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

--
-- Name: assignment_branches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.assignment_branches (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    plan_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    draft_revision_id uuid NOT NULL,
    branch_key character varying(128) NOT NULL,
    category_sequence jsonb NOT NULL,
    exact_draft_branch_id uuid,
    exact_draft_position smallint,
    decision_evidence_id uuid,
    decision_algorithm_version character varying(64),
    decision_inputs jsonb,
    decision_seed bytea,
    decision_result jsonb,
    decision_replay_digest bytea,
    decision_owner_id uuid,
    decided_at timestamp with time zone,
    state character varying(16) DEFAULT 'reserved'::character varying NOT NULL,
    disclosed_at timestamp with time zone,
    activated_at timestamp with time zone,
    released_at timestamp with time zone,
    release_reason text,
    superseded_at timestamp with time zone,
    supersession_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT assignment_branches_category_check CHECK (((jsonb_typeof(category_sequence) = 'array'::text) AND ((jsonb_array_length(category_sequence) >= 1) AND (jsonb_array_length(category_sequence) <= 3)))),
    CONSTRAINT assignment_branches_exact_draft_binding_check CHECK (((exact_draft_branch_id IS NULL) = (exact_draft_position IS NULL)) AND (exact_draft_position IS NULL OR exact_draft_position BETWEEN 1 AND 3)),
    CONSTRAINT assignment_branches_exact_draft_decision_check CHECK (((exact_draft_branch_id IS NULL) AND decision_evidence_id IS NULL AND decision_algorithm_version IS NULL AND decision_inputs IS NULL AND decision_seed IS NULL AND decision_result IS NULL AND decision_replay_digest IS NULL AND decision_owner_id IS NULL AND decided_at IS NULL) OR ((exact_draft_branch_id IS NOT NULL) AND decision_evidence_id IS NOT NULL AND decision_algorithm_version = 'hmac-sha256-order-v1' AND jsonb_typeof(decision_inputs) = 'array' AND jsonb_array_length(decision_inputs) > 0 AND octet_length(decision_seed) = 32 AND decision_seed <> decode(repeat('00', 32), 'hex') AND jsonb_typeof(decision_result) = 'array' AND jsonb_array_length(decision_result) > 0 AND octet_length(decision_replay_digest) = 32 AND decision_owner_id = plan_id AND decided_at IS NOT NULL AND decided_at <= created_at)),
    CONSTRAINT assignment_branches_key_check CHECK ((((branch_key)::text = btrim((branch_key)::text)) AND ((branch_key)::text <> ''::text))),
    CONSTRAINT assignment_branches_state_check CHECK (((state)::text = ANY ((ARRAY['reserved'::character varying, 'active'::character varying, 'released'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT assignment_branches_state_evidence_check CHECK (((((state)::text = 'reserved'::text) AND (disclosed_at IS NULL) AND (activated_at IS NULL) AND (released_at IS NULL) AND (release_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'active'::text) AND (activated_at IS NOT NULL) AND (released_at IS NULL) AND (release_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'released'::text) AND (disclosed_at IS NULL) AND (activated_at IS NULL) AND (released_at IS NOT NULL) AND (release_reason IS NOT NULL) AND (release_reason = btrim(release_reason)) AND (release_reason <> ''::text) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'superseded'::text) AND (released_at IS NULL) AND (release_reason IS NULL) AND (superseded_at IS NOT NULL) AND (supersession_reason IS NOT NULL) AND (supersession_reason = btrim(supersession_reason)) AND (supersession_reason <> ''::text)))),
    CONSTRAINT assignment_branches_timestamps_check CHECK ((((disclosed_at IS NULL) OR (disclosed_at >= created_at)) AND ((activated_at IS NULL) OR (activated_at >= created_at)) AND ((released_at IS NULL) OR (released_at >= created_at)) AND ((superseded_at IS NULL) OR (superseded_at >= COALESCE(activated_at, created_at)))))
);

-- One row records a reachable final-draft path. Its exactly three category
-- children are stored in assignment_branches and carry the normal task
-- reservation graph, so the eventual Game assignment keeps the existing FK
-- chain instead of introducing a parallel task authority.
CREATE TABLE public.exact_draft_assignment_branches (
    id uuid NOT NULL,
    plan_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    draft_revision_id uuid NOT NULL,
    branch_key character varying(128) NOT NULL,
    category_sequence jsonb NOT NULL,
    state character varying(16) DEFAULT 'reserved'::character varying NOT NULL,
    activated_at timestamp with time zone,
    released_at timestamp with time zone,
    release_reason text,
    superseded_at timestamp with time zone,
    supersession_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT exact_draft_assignment_branches_category_check CHECK (jsonb_typeof(category_sequence) = 'array' AND jsonb_array_length(category_sequence) = 3),
    CONSTRAINT exact_draft_assignment_branches_key_check CHECK (branch_key = btrim(branch_key) AND branch_key <> ''),
    CONSTRAINT exact_draft_assignment_branches_state_check CHECK (state IN ('reserved', 'active', 'released', 'superseded')),
    CONSTRAINT exact_draft_assignment_branches_state_evidence_check CHECK (((state = 'reserved') AND activated_at IS NULL AND released_at IS NULL AND release_reason IS NULL AND superseded_at IS NULL AND supersession_reason IS NULL) OR ((state = 'active') AND activated_at IS NOT NULL AND released_at IS NULL AND release_reason IS NULL AND superseded_at IS NULL AND supersession_reason IS NULL) OR ((state = 'released') AND activated_at IS NULL AND released_at IS NOT NULL AND release_reason = btrim(release_reason) AND release_reason <> '' AND superseded_at IS NULL AND supersession_reason IS NULL) OR ((state = 'superseded') AND activated_at IS NULL AND released_at IS NULL AND release_reason IS NULL AND superseded_at IS NOT NULL AND supersession_reason = btrim(supersession_reason) AND supersession_reason <> '')),
    CONSTRAINT exact_draft_assignment_branches_timestamps_check CHECK ((activated_at IS NULL OR activated_at >= created_at) AND (released_at IS NULL OR released_at >= created_at) AND (superseded_at IS NULL OR superseded_at >= created_at))
);

-- Every child remains a full exact normal-assignment proof. These source
-- rows make a retried completed draft independent from history appended by a
-- later final Game, while the existing edge, reservation, and snapshot rows
-- retain the selected task chain.
CREATE TABLE public.exact_draft_assignment_child_sources (
    child_branch_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    category_lock_id uuid NOT NULL,
    series_revision bigint NOT NULL,
    pool_revision_id uuid NOT NULL,
    pool_revision bigint NOT NULL,
    history_revision_id uuid NOT NULL,
    history_revision bigint NOT NULL,
    roster_revision bigint NOT NULL,
    artifact_revision_id uuid NOT NULL,
    artifact_revision bigint NOT NULL,
    category_revision_id uuid NOT NULL,
    category_revision bigint NOT NULL,
    graph_digest bytea NOT NULL,
    artifact_digest bytea NOT NULL,
    proof_hash character varying(64) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT exact_draft_assignment_child_sources_pkey PRIMARY KEY (child_branch_id),
    CONSTRAINT exact_draft_assignment_child_sources_identity_key UNIQUE (child_branch_id, plan_id),
    CONSTRAINT exact_draft_assignment_child_sources_scope_check CHECK (
        tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_lock_id = category_revision_id
        AND series_revision >= 1
        AND pool_revision >= 1
        AND history_revision >= 1
        AND roster_revision >= 1
        AND artifact_revision >= 1
        AND category_revision >= 1
        AND octet_length(graph_digest) = 32
        AND graph_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(artifact_digest) = 32
        AND artifact_digest <> decode(repeat('00', 32), 'hex')
        AND proof_hash = btrim(proof_hash)
        AND char_length(proof_hash) = 64
    )
);

CREATE TABLE public.exact_draft_assignment_child_participants (
    child_branch_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    player_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    reservation_revision bigint NOT NULL,
    acquired_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT exact_draft_assignment_child_participants_pkey PRIMARY KEY (child_branch_id, participant_id),
    CONSTRAINT exact_draft_assignment_child_participants_identity_key UNIQUE (
        child_branch_id,
        plan_id,
        participant_id
    ),
    CONSTRAINT exact_draft_assignment_child_participants_scope_check CHECK (
        reservation_revision >= 1
        AND updated_at >= acquired_at
        AND created_at >= acquired_at
    )
);

CREATE TABLE public.exact_draft_assignment_child_history (
    child_branch_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT exact_draft_assignment_child_history_pkey PRIMARY KEY (child_branch_id, participant_id, task_id)
);

CREATE TABLE public.exact_draft_assignment_child_candidates (
    child_branch_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    pool_revision_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT exact_draft_assignment_child_candidates_pkey PRIMARY KEY (
        child_branch_id,
        task_id,
        task_version
    ),
    CONSTRAINT exact_draft_assignment_child_candidates_version_check CHECK (task_version >= 1)
);

--
-- Name: assignment_plan_edges; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.assignment_plan_edges (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    plan_id uuid NOT NULL,
    branch_id uuid NOT NULL,
    "position" smallint NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    operator_reserve_command_id uuid,
    selection_evidence jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT assignment_plan_edges_evidence_check CHECK (((jsonb_typeof(selection_evidence) = 'object'::text) AND (selection_evidence <> '{}'::jsonb))),
    CONSTRAINT assignment_plan_edges_position_check CHECK (("position" >= 1)),
    CONSTRAINT assignment_plan_edges_source_check CHECK ((("position" <= 3) = (operator_reserve_command_id IS NULL))),
    CONSTRAINT assignment_plan_edges_version_check CHECK ((task_version >= 1))
);

--
-- Name: assignment_plans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.assignment_plans (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    kind character varying(16) NOT NULL,
    parent_plan_id uuid,
    revision_id uuid NOT NULL,
    source_roster_revision bigint NOT NULL,
    source_pool_revision_id uuid NOT NULL,
    source_draft_revision_id uuid,
    reachable_branch_count integer DEFAULT 0 NOT NULL,
    constraint_graph jsonb NOT NULL,
    proof_evidence jsonb NOT NULL,
    proof_hash character varying(64),
    decision_evidence_id uuid,
    decision_algorithm_version character varying(64),
    decision_inputs jsonb,
    decision_seed bytea,
    decision_result jsonb,
    decision_replay_digest bytea,
    decision_owner_id uuid,
    decided_at timestamp with time zone,
    state character varying(16) DEFAULT 'planned'::character varying NOT NULL,
    active_branch_id uuid,
    active_draft_branch_id uuid,
    completion_draft_revision_id uuid,
    completion_draft_revision bigint,
    activation_command_id uuid,
    completed_categories jsonb,
    committed_at timestamp with time zone,
    superseded_at timestamp with time zone,
    supersession_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT assignment_plans_decision_check CHECK (((((kind)::text IN ('conservative', 'exact_draft')) AND (decision_evidence_id IS NULL) AND (decision_algorithm_version IS NULL) AND (decision_inputs IS NULL) AND (decision_seed IS NULL) AND (decision_result IS NULL) AND (decision_replay_digest IS NULL) AND (decision_owner_id IS NULL) AND (decided_at IS NULL)) OR (((kind)::text = 'exact'::text) AND (decision_evidence_id IS NOT NULL) AND (decision_algorithm_version IS NOT NULL) AND ((decision_algorithm_version)::text = 'hmac-sha256-order-v1'::text) AND (decision_inputs IS NOT NULL) AND (jsonb_typeof(decision_inputs) = 'array'::text) AND (jsonb_array_length(decision_inputs) > 0) AND (decision_seed IS NOT NULL) AND (octet_length(decision_seed) = 32) AND (decision_seed <> decode(repeat('00'::text, 32), 'hex'::text)) AND (decision_result IS NOT NULL) AND (jsonb_typeof(decision_result) = 'array'::text) AND (jsonb_array_length(decision_result) > 0) AND (decision_replay_digest IS NOT NULL) AND (octet_length(decision_replay_digest) = 32) AND (decision_owner_id IS NOT NULL) AND (decision_owner_id = id) AND (decided_at IS NOT NULL) AND (decided_at <= created_at)))),
    CONSTRAINT assignment_plans_kind_check CHECK (((kind)::text = ANY ((ARRAY['conservative'::character varying, 'exact'::character varying, 'exact_draft'::character varying])::text[]))),
    CONSTRAINT assignment_plans_proof_check CHECK (((jsonb_typeof(constraint_graph) = 'object'::text) AND (constraint_graph <> '{}'::jsonb) AND (jsonb_typeof(proof_evidence) = 'object'::text) AND (proof_evidence <> '{}'::jsonb) AND (((kind)::text <> 'exact_draft'::text) OR ((proof_hash IS NOT NULL) AND (proof_hash = btrim(proof_hash)) AND (char_length(proof_hash) = 64))))),
    CONSTRAINT assignment_plans_source_check CHECK (((source_roster_revision >= 1) AND ((((kind)::text = 'conservative'::text) AND (parent_plan_id IS NULL) AND (source_draft_revision_id IS NULL) AND (reachable_branch_count = 0)) OR (((kind)::text = 'exact'::text) AND (parent_plan_id IS NOT NULL) AND (source_draft_revision_id IS NOT NULL) AND (reachable_branch_count >= 1)) OR (((kind)::text = 'exact_draft'::text) AND (parent_plan_id IS NULL) AND (source_draft_revision_id IS NOT NULL) AND (reachable_branch_count >= 1))))),
    CONSTRAINT assignment_plans_state_check CHECK (((state)::text = ANY ((ARRAY['planned'::character varying, 'committed'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT assignment_plans_state_evidence_check CHECK (((((state)::text = 'planned'::text) AND (active_branch_id IS NULL) AND (active_draft_branch_id IS NULL) AND (completion_draft_revision_id IS NULL) AND (completion_draft_revision IS NULL) AND (activation_command_id IS NULL) AND (completed_categories IS NULL) AND (committed_at IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'committed'::text) AND ((kind)::text = 'exact'::text) AND (active_branch_id IS NOT NULL) AND (active_draft_branch_id IS NULL) AND (completion_draft_revision_id IS NULL) AND (completion_draft_revision IS NULL) AND (activation_command_id IS NULL) AND (completed_categories IS NULL) AND (committed_at IS NOT NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'committed'::text) AND ((kind)::text = 'exact_draft'::text) AND (active_branch_id IS NOT NULL) AND (active_draft_branch_id IS NOT NULL) AND (completion_draft_revision_id IS NOT NULL) AND (completion_draft_revision >= 1) AND (activation_command_id IS NOT NULL) AND (jsonb_typeof(completed_categories) = 'array'::text) AND (jsonb_array_length(completed_categories) = 3) AND (committed_at IS NOT NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'superseded'::text) AND (superseded_at IS NOT NULL) AND (supersession_reason IS NOT NULL) AND (supersession_reason = btrim(supersession_reason)) AND (supersession_reason <> ''::text)))),
    CONSTRAINT assignment_plans_timestamps_check CHECK ((((committed_at IS NULL) OR (committed_at >= created_at)) AND ((superseded_at IS NULL) OR (superseded_at >= COALESCE(committed_at, created_at)))))
);

--
-- Name: assignments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.assignments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    attempt_id uuid NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    branch_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    snapshot_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    supersedes_assignment_id uuid,
    state character varying(16) DEFAULT 'active'::character varying NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    superseded_at timestamp with time zone,
    supersession_reason text,
    CONSTRAINT assignments_revision_check CHECK ((revision >= 1)),
    CONSTRAINT assignments_state_check CHECK (((state)::text = ANY ((ARRAY['active'::character varying, 'completed'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT assignments_state_evidence_check CHECK (((((state)::text = 'active'::text) AND (completed_at IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'completed'::text) AND (completed_at IS NOT NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'superseded'::text) AND (completed_at IS NULL) AND (superseded_at IS NOT NULL) AND (supersession_reason IS NOT NULL) AND (supersession_reason = btrim(supersession_reason)) AND (supersession_reason <> ''::text)))),
    CONSTRAINT assignments_timestamps_check CHECK (((updated_at >= created_at) AND ((completed_at IS NULL) OR (completed_at >= created_at)) AND ((superseded_at IS NULL) OR (superseded_at >= created_at))))
);

--
-- Name: task_delivery_receipts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_delivery_receipts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    assignment_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    snapshot_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    delivered_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_delivery_receipts_timestamps_check CHECK ((delivered_at <= created_at))
);

--
-- Name: task_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_snapshots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    reservation_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    kind character varying(16) NOT NULL,
    title character varying(255) NOT NULL,
    description text NOT NULL,
    category character varying(32) NOT NULL,
    difficulty character varying(16) NOT NULL,
    time_limit integer NOT NULL,
    flag character varying(255) NOT NULL,
    hints jsonb DEFAULT '[]'::jsonb NOT NULL,
    task_url text,
    source_file_url text,
    content_digest bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_snapshots_category_check CHECK (((category)::text = ANY ((ARRAY['web'::character varying, 'crypto'::character varying, 'forensics'::character varying, 'reverse'::character varying, 'pwn'::character varying, 'steganography'::character varying, 'ppc'::character varying, 'osint'::character varying, 'mobile'::character varying, 'hardware'::character varying, 'misc'::character varying])::text[]))),
    CONSTRAINT task_snapshots_content_check CHECK (((btrim((title)::text) <> ''::text) AND (btrim(description) <> ''::text) AND (btrim((flag)::text) <> ''::text) AND (time_limit > 0) AND (jsonb_typeof(hints) = 'array'::text) AND (octet_length(content_digest) = 32) AND (content_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT task_snapshots_difficulty_check CHECK (((difficulty)::text = ANY ((ARRAY['easy'::character varying, 'medium'::character varying, 'hard'::character varying])::text[]))),
    CONSTRAINT task_snapshots_kind_check CHECK (((kind)::text = ANY ((ARRAY['normal'::character varying, 'golden'::character varying])::text[]))),
    CONSTRAINT task_snapshots_version_check CHECK ((task_version >= 1))
);

--
-- Name: task_version_reservations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_version_reservations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    edge_id uuid NOT NULL,
    plan_id uuid NOT NULL,
    branch_id uuid NOT NULL,
    contingency_draft_branch_id uuid,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    state character varying(16) DEFAULT 'reserved'::character varying NOT NULL,
    disclosed_at timestamp with time zone,
    committed_at timestamp with time zone,
    released_at timestamp with time zone,
    release_reason text,
    superseded_at timestamp with time zone,
    supersession_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_version_reservations_revision_check CHECK ((revision >= 1)),
    CONSTRAINT task_version_reservations_state_check CHECK (((state)::text = ANY ((ARRAY['reserved'::character varying, 'committed'::character varying, 'released'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT task_version_reservations_state_evidence_check CHECK (((((state)::text = 'reserved'::text) AND (disclosed_at IS NULL) AND (committed_at IS NULL) AND (released_at IS NULL) AND (release_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'committed'::text) AND (committed_at IS NOT NULL) AND (released_at IS NULL) AND (release_reason IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'released'::text) AND (disclosed_at IS NULL) AND (committed_at IS NULL) AND (released_at IS NOT NULL) AND (release_reason IS NOT NULL) AND (release_reason = btrim(release_reason)) AND (release_reason <> ''::text) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'superseded'::text) AND (released_at IS NULL) AND (release_reason IS NULL) AND (superseded_at IS NOT NULL) AND (supersession_reason IS NOT NULL) AND (supersession_reason = btrim(supersession_reason)) AND (supersession_reason <> ''::text)))),
    CONSTRAINT task_version_reservations_timestamps_check CHECK ((((disclosed_at IS NULL) OR (disclosed_at >= created_at)) AND ((committed_at IS NULL) OR (committed_at >= created_at)) AND ((released_at IS NULL) OR (released_at >= created_at)) AND ((superseded_at IS NULL) OR (superseded_at >= COALESCE(committed_at, created_at))))),
    CONSTRAINT task_version_reservations_version_check CHECK ((task_version >= 1))
);

-- replay_reserve_authorities is the mutable, normalized authority head used
-- by an operator to append one fourth replay reserve. It is deliberately
-- separate from the immutable command record: a command may retain what it
-- observed, but only this locked head decides whether the observation is
-- still current.
CREATE TABLE public.replay_reserve_authorities (
    assignment_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    assignment_attempt_id uuid NOT NULL,
    active_snapshot_id uuid NOT NULL,
    required_category character varying(32) NOT NULL,
    assignment_revision bigint NOT NULL,
    pool_revision_id uuid NOT NULL,
    pool_revision bigint NOT NULL,
    history_revision_id uuid NOT NULL,
    history_revision bigint NOT NULL,
    artifact_revision_id uuid NOT NULL,
    artifact_revision bigint NOT NULL,
    reservation_revision_id uuid NOT NULL,
    reservation_revision bigint NOT NULL,
    category_revision_id uuid NOT NULL,
    category_revision bigint NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT replay_reserve_authorities_pkey PRIMARY KEY (assignment_id),
    CONSTRAINT replay_reserve_authorities_scope_key UNIQUE (tournament_id, roster_id, series_id, slot_id, assignment_id, assignment_attempt_id),
    CONSTRAINT replay_reserve_authorities_category_check CHECK ((required_category)::text = ANY ((ARRAY['web'::character varying, 'crypto'::character varying, 'forensics'::character varying, 'reverse'::character varying, 'pwn'::character varying, 'steganography'::character varying, 'ppc'::character varying, 'osint'::character varying, 'mobile'::character varying, 'hardware'::character varying, 'misc'::character varying])::text[])),
    CONSTRAINT replay_reserve_authorities_revision_check CHECK (assignment_revision >= 1 AND pool_revision >= 1 AND history_revision >= 1 AND artifact_revision >= 1 AND reservation_revision >= 1 AND category_revision >= 1 AND revision >= 1 AND updated_at >= created_at)
);

CREATE TABLE public.replay_reserve_authority_pool_versions (
    assignment_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT replay_reserve_authority_pool_versions_pkey PRIMARY KEY (assignment_id, task_id, task_version),
    CONSTRAINT replay_reserve_authority_pool_versions_version_check CHECK (task_version >= 1)
);

CREATE TABLE public.replay_reserve_exhaustions (
    command_id uuid PRIMARY KEY,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    old_wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    assignment_attempt_id uuid NOT NULL,
    failed_game_id uuid NOT NULL,
    closure_revision_id uuid NOT NULL,
    active_snapshot_id uuid NOT NULL,
    from_snapshot_id uuid NOT NULL,
    reserve_position smallint NOT NULL,
    category character varying(32) NOT NULL,
    source_series_revision bigint NOT NULL,
    resulting_series_revision bigint NOT NULL,
    request_digest bytea NOT NULL,
    authority_document jsonb NOT NULL,
    record_document jsonb NOT NULL,
    paused_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT replay_reserve_exhaustions_category_check CHECK (((category)::text = ANY ((ARRAY['web'::character varying, 'crypto'::character varying, 'forensics'::character varying, 'reverse'::character varying, 'pwn'::character varying, 'steganography'::character varying, 'ppc'::character varying, 'osint'::character varying, 'mobile'::character varying, 'hardware'::character varying, 'misc'::character varying])::text[]))),
    CONSTRAINT replay_reserve_exhaustions_digest_check CHECK ((octet_length(request_digest) = 32)),
    CONSTRAINT replay_reserve_exhaustions_document_check CHECK ((jsonb_typeof(authority_document) = 'object'::text AND authority_document <> '{}'::jsonb AND authority_document ->> 'schema_version'::text = '1'::text AND jsonb_typeof(record_document) = 'object'::text AND record_document <> '{}'::jsonb AND record_document ->> 'schema_version'::text = '1'::text)),
    CONSTRAINT replay_reserve_exhaustions_position_check CHECK ((reserve_position = 3)),
    CONSTRAINT replay_reserve_exhaustions_revision_check CHECK ((source_series_revision >= 1 AND resulting_series_revision = source_series_revision + 1)),
    CONSTRAINT replay_reserve_exhaustions_snapshot_check CHECK ((active_snapshot_id = from_snapshot_id)),
    CONSTRAINT replay_reserve_exhaustions_timestamps_check CHECK ((paused_at <= created_at))
);

CREATE TABLE public.operator_replay_reserves (
    command_id uuid PRIMARY KEY,
    exhaustion_command_id uuid NOT NULL UNIQUE,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    old_wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    assignment_attempt_id uuid NOT NULL,
    failed_game_id uuid NOT NULL,
    closure_revision_id uuid NOT NULL,
    from_snapshot_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    reason text NOT NULL,
    expected_assignment_revision bigint NOT NULL,
    expected_pool_revision_id uuid NOT NULL,
    expected_pool_revision bigint NOT NULL,
    expected_history_revision_id uuid NOT NULL,
    expected_history_revision bigint NOT NULL,
    expected_artifact_revision_id uuid NOT NULL,
    expected_artifact_revision bigint NOT NULL,
    expected_reservation_revision_id uuid NOT NULL,
    expected_reservation_revision bigint NOT NULL,
    expected_category_revision_id uuid NOT NULL,
    expected_category_revision bigint NOT NULL,
    proposed_task_id uuid NOT NULL,
    proposed_version integer NOT NULL,
    proposed_snapshot_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    edge_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    reserve_position smallint NOT NULL,
    source_series_revision bigint NOT NULL,
    resulting_series_revision bigint NOT NULL,
    request_digest bytea NOT NULL,
    evidence_digest bytea NOT NULL,
    proof_digest bytea NOT NULL,
    content_digest bytea NOT NULL,
    authority_document jsonb NOT NULL,
    record_document jsonb NOT NULL,
    promoted_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operator_replay_reserves_digest_check CHECK ((octet_length(request_digest) = 32 AND octet_length(evidence_digest) = 32 AND octet_length(proof_digest) = 32 AND octet_length(content_digest) = 32)),
    CONSTRAINT operator_replay_reserves_document_check CHECK ((jsonb_typeof(authority_document) = 'object'::text AND authority_document <> '{}'::jsonb AND authority_document ->> 'schema_version'::text = '1'::text AND jsonb_typeof(record_document) = 'object'::text AND record_document <> '{}'::jsonb AND record_document ->> 'schema_version'::text = '1'::text)),
    CONSTRAINT operator_replay_reserves_position_check CHECK ((reserve_position = 4)),
    CONSTRAINT operator_replay_reserves_reason_check CHECK ((reason = btrim(reason) AND reason <> ''::text AND char_length(reason) <= 512)),
    CONSTRAINT operator_replay_reserves_revision_check CHECK ((expected_assignment_revision >= 1 AND expected_pool_revision >= 1 AND expected_history_revision >= 1 AND expected_artifact_revision >= 1 AND expected_reservation_revision >= 1 AND expected_category_revision >= 1 AND source_series_revision >= 1 AND resulting_series_revision = source_series_revision + 1)),
    CONSTRAINT operator_replay_reserves_timestamps_check CHECK ((promoted_at <= created_at)),
    CONSTRAINT operator_replay_reserves_command_fk FOREIGN KEY (exhaustion_command_id) REFERENCES public.replay_reserve_exhaustions(command_id) ON DELETE RESTRICT
);

CREATE TABLE public.replay_replacements (
    command_id uuid PRIMARY KEY,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    old_wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    assignment_attempt_id uuid NOT NULL,
    failed_game_id uuid NOT NULL,
    closure_revision_id uuid NOT NULL,
    from_snapshot_id uuid NOT NULL,
    replacement_assignment_attempt_id uuid NOT NULL UNIQUE,
    replacement_game_id uuid NOT NULL UNIQUE,
    replacement_wave_id uuid NOT NULL UNIQUE,
    replacement_wave_revision_id uuid NOT NULL UNIQUE,
    ready_window_id uuid NOT NULL UNIQUE,
    ready_window_revision_id uuid NOT NULL UNIQUE,
    snapshot_id uuid NOT NULL,
    reserve_position smallint NOT NULL,
    source_series_revision bigint NOT NULL,
    resulting_series_revision bigint NOT NULL,
    actor_id uuid NOT NULL,
    reason text NOT NULL,
    request_digest bytea NOT NULL,
    authority_document jsonb NOT NULL,
    record_document jsonb NOT NULL,
    opened_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT replay_replacements_digest_check CHECK ((octet_length(request_digest) = 32)),
    CONSTRAINT replay_replacements_document_check CHECK ((jsonb_typeof(authority_document) = 'object'::text AND authority_document <> '{}'::jsonb AND authority_document ->> 'schema_version'::text = '1'::text AND jsonb_typeof(record_document) = 'object'::text AND record_document <> '{}'::jsonb AND record_document ->> 'schema_version'::text = '1'::text)),
    CONSTRAINT replay_replacements_position_check CHECK ((reserve_position >= 2 AND reserve_position <= 4)),
    CONSTRAINT replay_replacements_reason_check CHECK ((reason = btrim(reason) AND reason <> ''::text AND char_length(reason) <= 512)),
    CONSTRAINT replay_replacements_revision_check CHECK ((source_series_revision >= 1 AND resulting_series_revision = source_series_revision + 1)),
    CONSTRAINT replay_replacements_timestamps_check CHECK ((opened_at <= created_at)),
    CONSTRAINT replay_replacements_game_fk FOREIGN KEY (replacement_game_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT replay_replacements_wave_fk FOREIGN KEY (replacement_wave_id) REFERENCES public.waves(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT replay_replacements_window_fk FOREIGN KEY (ready_window_id) REFERENCES public.ready_windows(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED
);

--
-- Name: assignment_branches assignment_branches_id_plan_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_id_plan_key UNIQUE (id, plan_id);

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_exact_draft_position_key UNIQUE (
        exact_draft_branch_id,
        exact_draft_position
    );

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_exact_draft_decision_key UNIQUE (decision_evidence_id);

--
-- Name: assignment_branches assignment_branches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_pkey PRIMARY KEY (id);

--
-- Name: assignment_branches assignment_branches_plan_branch_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_plan_branch_key UNIQUE (plan_id, branch_key);

ALTER TABLE ONLY public.exact_draft_assignment_branches
    ADD CONSTRAINT exact_draft_assignment_branches_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.exact_draft_assignment_branches
    ADD CONSTRAINT exact_draft_assignment_branches_id_plan_key UNIQUE (id, plan_id);

ALTER TABLE ONLY public.exact_draft_assignment_branches
    ADD CONSTRAINT exact_draft_assignment_branches_plan_key UNIQUE (plan_id, branch_key);

--
-- Name: assignment_plan_edges assignment_plan_edges_branch_position_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_branch_position_key UNIQUE (branch_id, "position");

--
-- Name: assignment_plan_edges assignment_plan_edges_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_identity_key UNIQUE (id, plan_id, branch_id, task_id, task_version);

--
-- Name: assignment_plan_edges assignment_plan_edges_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_pkey PRIMARY KEY (id);

--
-- Name: assignment_plan_edges assignment_plan_edges_branch_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_branch_version_key UNIQUE (branch_id, task_id, task_version);

--
-- Name: assignment_plans assignment_plans_decision_evidence_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_decision_evidence_id_key UNIQUE (decision_evidence_id);

--
-- Name: assignment_plans assignment_plans_id_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_id_roster_key UNIQUE (id, roster_id);

--
-- Name: assignment_plans assignment_plans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_pkey PRIMARY KEY (id);

--
-- Name: assignment_plans assignment_plans_revision_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_revision_id_key UNIQUE (revision_id);

--
-- Name: assignments assignments_id_attempt_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_id_attempt_roster_key UNIQUE (id, attempt_id, roster_id);

--
-- Name: assignments assignments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_pkey PRIMARY KEY (id);

--
-- Name: assignments assignments_supersedes_assignment_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_supersedes_assignment_id_key UNIQUE (supersedes_assignment_id);

--
-- Name: task_delivery_receipts task_delivery_receipts_assignment_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_assignment_participant_key UNIQUE (assignment_id, participant_id);

--
-- Name: task_delivery_receipts task_delivery_receipts_instance_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_instance_id_key UNIQUE (instance_id);

--
-- Name: task_delivery_receipts task_delivery_receipts_participant_task_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_participant_task_key UNIQUE (participant_id, task_id);

--
-- Name: task_delivery_receipts task_delivery_receipts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_pkey PRIMARY KEY (id);

--
-- Name: task_snapshots task_snapshots_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_snapshots
    ADD CONSTRAINT task_snapshots_identity_key UNIQUE (id, reservation_id, task_id, task_version);

--
-- Name: task_snapshots task_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_snapshots
    ADD CONSTRAINT task_snapshots_pkey PRIMARY KEY (id);

--
-- Name: task_snapshots task_snapshots_reservation_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_snapshots
    ADD CONSTRAINT task_snapshots_reservation_id_key UNIQUE (reservation_id);

--
-- Name: task_snapshots task_snapshots_task_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_snapshots
    ADD CONSTRAINT task_snapshots_task_identity_key UNIQUE (id, task_id, task_version);

--
-- Name: task_version_reservations task_version_reservations_edge_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_edge_id_key UNIQUE (edge_id);

--
-- Name: task_version_reservations task_version_reservations_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_identity_key UNIQUE (id, plan_id, branch_id, task_id, task_version);

--
-- Name: task_version_reservations task_version_reservations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_pkey PRIMARY KEY (id);

--
-- Name: task_version_reservations task_version_reservations_task_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_task_key UNIQUE (id, task_id, task_version);

--
-- Name: assignment_branches_one_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX assignment_branches_one_active_idx ON public.assignment_branches USING btree (plan_id) WHERE ((state)::text = 'active'::text AND exact_draft_branch_id IS NULL);

CREATE UNIQUE INDEX exact_draft_assignment_branches_one_active_idx ON public.exact_draft_assignment_branches USING btree (plan_id) WHERE ((state)::text = 'active'::text);

--
-- Name: assignment_branches_plan_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX assignment_branches_plan_state_idx ON public.assignment_branches USING btree (plan_id, state, created_at);

CREATE INDEX exact_draft_assignment_branches_plan_state_idx ON public.exact_draft_assignment_branches USING btree (plan_id, state, created_at);

CREATE INDEX exact_draft_assignment_child_sources_plan_idx
    ON public.exact_draft_assignment_child_sources USING btree (plan_id, child_branch_id);

CREATE INDEX exact_draft_assignment_child_participants_plan_idx
    ON public.exact_draft_assignment_child_participants USING btree (plan_id, child_branch_id, participant_id);

CREATE INDEX exact_draft_assignment_child_history_plan_idx
    ON public.exact_draft_assignment_child_history USING btree (plan_id, child_branch_id, participant_id);

CREATE INDEX exact_draft_assignment_child_candidates_plan_idx
    ON public.exact_draft_assignment_child_candidates USING btree (plan_id, child_branch_id, task_id, task_version);

--
-- Name: assignment_plans_roster_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX assignment_plans_roster_state_idx ON public.assignment_plans USING btree (roster_id, state, created_at);

--
-- Name: assignments_one_active_attempt_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX assignments_one_active_attempt_idx ON public.assignments USING btree (attempt_id) WHERE ((state)::text = 'active'::text);

--
-- Name: task_delivery_receipts_participant_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_delivery_receipts_participant_created_idx ON public.task_delivery_receipts USING btree (participant_id, delivered_at, id);

--
-- Name: task_version_reservations_active_idx; Type: INDEX; Schema: public; Owner: -
--

-- Contingent final-draft branches may reserve the same immutable task version
-- because only one group can become active. Ordinary reservations and every
-- committed reservation remain globally exclusive.
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

--
-- Name: assignment_branches assignment_branch_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_branch_guard BEFORE INSERT OR DELETE OR UPDATE ON public.assignment_branches FOR EACH ROW EXECUTE FUNCTION public.assignment_branch_guard();

CREATE TRIGGER exact_draft_assignment_branch_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.exact_draft_assignment_branches
FOR EACH ROW EXECUTE FUNCTION public.exact_draft_assignment_branch_guard();

CREATE TRIGGER exact_draft_assignment_child_source_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.exact_draft_assignment_child_sources
FOR EACH ROW EXECUTE FUNCTION public.exact_draft_assignment_child_source_guard();

CREATE TRIGGER exact_draft_assignment_child_participant_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.exact_draft_assignment_child_participants
FOR EACH ROW EXECUTE FUNCTION public.exact_draft_assignment_child_participant_guard();

CREATE TRIGGER exact_draft_assignment_child_history_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.exact_draft_assignment_child_history
FOR EACH ROW EXECUTE FUNCTION public.exact_draft_assignment_child_history_guard();

--
-- Name: assignment_branches assignment_branch_transition_evidence_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_branch_transition_evidence_guard BEFORE UPDATE ON public.assignment_branches FOR EACH ROW EXECUTE FUNCTION public.assignment_branch_transition_evidence_guard();

--
-- Name: assignment_plan_edges assignment_edge_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_edge_guard BEFORE INSERT OR DELETE OR UPDATE ON public.assignment_plan_edges FOR EACH ROW EXECUTE FUNCTION public.assignment_edge_guard();

--
-- Name: assignments assignment_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_guard BEFORE INSERT OR DELETE OR UPDATE ON public.assignments FOR EACH ROW EXECUTE FUNCTION public.assignment_guard();

CREATE CONSTRAINT TRIGGER replay_reserve_authority_assignment_guard
AFTER INSERT ON public.assignments
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.replay_reserve_authority_assignment_guard();

--
-- Name: assignment_plans assignment_plan_commit_evidence_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_plan_commit_evidence_guard BEFORE UPDATE ON public.assignment_plans FOR EACH ROW EXECUTE FUNCTION public.assignment_plan_commit_evidence_guard();

--
-- Name: assignment_plans assignment_plan_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_plan_guard BEFORE INSERT OR DELETE OR UPDATE ON public.assignment_plans FOR EACH ROW EXECUTE FUNCTION public.assignment_plan_guard();

--
-- Name: assignments assignment_plan_roster_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER assignment_plan_roster_guard BEFORE INSERT ON public.assignments FOR EACH ROW EXECUTE FUNCTION public.assignment_plan_roster_guard();

--
-- Name: task_delivery_receipts delivery_receipt_assignment_identity_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER delivery_receipt_assignment_identity_guard BEFORE INSERT ON public.task_delivery_receipts FOR EACH ROW EXECUTE FUNCTION public.delivery_receipt_assignment_identity_guard();

--
-- Name: task_delivery_receipts delivery_receipt_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER delivery_receipt_guard BEFORE INSERT OR DELETE OR UPDATE ON public.task_delivery_receipts FOR EACH ROW EXECUTE FUNCTION public.delivery_receipt_guard();

--
-- Name: task_version_reservations reservation_commit_evidence_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER reservation_commit_evidence_guard BEFORE UPDATE ON public.task_version_reservations FOR EACH ROW EXECUTE FUNCTION public.reservation_commit_evidence_guard();

--
-- Name: task_snapshots task_snapshot_immutable_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER task_snapshot_immutable_guard BEFORE DELETE OR UPDATE ON public.task_snapshots FOR EACH ROW EXECUTE FUNCTION public.task_snapshot_immutable_guard();

--
-- Name: task_version_reservations task_version_reservation_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER task_version_reservation_guard BEFORE INSERT OR DELETE OR UPDATE ON public.task_version_reservations FOR EACH ROW EXECUTE FUNCTION public.task_version_reservation_guard();

CREATE TRIGGER replay_reserve_exhaustion_source_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.replay_reserve_exhaustions
FOR EACH ROW EXECUTE FUNCTION public.replay_command_source_guard();

CREATE CONSTRAINT TRIGGER replay_reserve_exhaustion_target_guard
AFTER INSERT ON public.replay_reserve_exhaustions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.replay_command_target_guard();

CREATE TRIGGER operator_replay_reserve_source_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.operator_replay_reserves
FOR EACH ROW EXECUTE FUNCTION public.replay_command_source_guard();

CREATE CONSTRAINT TRIGGER operator_replay_reserve_target_guard
AFTER INSERT ON public.operator_replay_reserves
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.replay_command_target_guard();

CREATE TRIGGER replay_replacement_source_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.replay_replacements
FOR EACH ROW EXECUTE FUNCTION public.replay_command_source_guard();

CREATE CONSTRAINT TRIGGER replay_replacement_target_guard
AFTER INSERT ON public.replay_replacements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.replay_command_target_guard();

--
-- Name: assignment_branches assignment_branches_draft_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_draft_revision_fk FOREIGN KEY (draft_revision_id, draft_id) REFERENCES public.draft_revisions(id, draft_id) ON DELETE RESTRICT;

--
-- Name: assignment_branches assignment_branches_plan_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_plan_fk FOREIGN KEY (plan_id) REFERENCES public.assignment_plans(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.assignment_branches
    ADD CONSTRAINT assignment_branches_exact_draft_group_fk
    FOREIGN KEY (exact_draft_branch_id, plan_id)
    REFERENCES public.exact_draft_assignment_branches(id, plan_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_branches
    ADD CONSTRAINT exact_draft_assignment_branches_plan_fk
    FOREIGN KEY (plan_id) REFERENCES public.assignment_plans(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_branches
    ADD CONSTRAINT exact_draft_assignment_branches_draft_revision_fk
    FOREIGN KEY (draft_revision_id, draft_id)
    REFERENCES public.draft_revisions(id, draft_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_sources
    ADD CONSTRAINT exact_draft_assignment_child_sources_branch_fk
    FOREIGN KEY (child_branch_id, plan_id)
    REFERENCES public.assignment_branches(id, plan_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_sources
    ADD CONSTRAINT exact_draft_assignment_child_sources_plan_fk
    FOREIGN KEY (plan_id)
    REFERENCES public.assignment_plans(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_sources
    ADD CONSTRAINT exact_draft_assignment_child_sources_pool_fk
    FOREIGN KEY (pool_revision_id)
    REFERENCES public.task_pool_revisions(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_participants
    ADD CONSTRAINT exact_draft_assignment_child_participants_source_fk
    FOREIGN KEY (child_branch_id, plan_id)
    REFERENCES public.exact_draft_assignment_child_sources(child_branch_id, plan_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_participants
    ADD CONSTRAINT exact_draft_assignment_child_participants_participant_fk
    FOREIGN KEY (participant_id)
    REFERENCES public.participants(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_history
    ADD CONSTRAINT exact_draft_assignment_child_history_participant_fk
    FOREIGN KEY (child_branch_id, plan_id, participant_id)
    REFERENCES public.exact_draft_assignment_child_participants(child_branch_id, plan_id, participant_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_history
    ADD CONSTRAINT exact_draft_assignment_child_history_task_fk
    FOREIGN KEY (task_id)
    REFERENCES public.tasks(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_candidates
    ADD CONSTRAINT exact_draft_assignment_child_candidates_source_fk
    FOREIGN KEY (child_branch_id, plan_id)
    REFERENCES public.exact_draft_assignment_child_sources(child_branch_id, plan_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_candidates
    ADD CONSTRAINT exact_draft_assignment_child_candidates_version_fk
    FOREIGN KEY (task_id, task_version)
    REFERENCES public.task_versions(task_id, version)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.exact_draft_assignment_child_candidates
    ADD CONSTRAINT exact_draft_assignment_child_candidates_pool_fk
    FOREIGN KEY (pool_revision_id, task_id, task_version)
    REFERENCES public.task_pool_version_memberships(task_pool_revision_id, task_id, task_version)
    ON DELETE RESTRICT;

--
-- Name: assignment_plan_edges assignment_plan_edges_branch_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_branch_fk FOREIGN KEY (branch_id, plan_id) REFERENCES public.assignment_branches(id, plan_id) ON DELETE RESTRICT;

--
-- Name: assignment_plan_edges assignment_plan_edges_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

--
-- Name: assignment_plans assignment_plans_active_branch_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_active_branch_fk FOREIGN KEY (active_branch_id, id) REFERENCES public.assignment_branches(id, plan_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_active_draft_branch_fk
    FOREIGN KEY (active_draft_branch_id, id)
    REFERENCES public.exact_draft_assignment_branches(id, plan_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_activation_command_key UNIQUE (activation_command_id);

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_completion_draft_revision_fk
    FOREIGN KEY (completion_draft_revision_id)
    REFERENCES public.draft_revisions(id)
    ON DELETE RESTRICT;

--
-- Name: assignment_plans assignment_plans_parent_plan_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_parent_plan_id_fkey FOREIGN KEY (parent_plan_id) REFERENCES public.assignment_plans(id) ON DELETE RESTRICT;

--
-- Name: assignment_plans assignment_plans_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: assignment_plans assignment_plans_source_draft_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment_plans
    ADD CONSTRAINT assignment_plans_source_draft_revision_id_fkey FOREIGN KEY (source_draft_revision_id) REFERENCES public.draft_revisions(id) ON DELETE RESTRICT;

--
-- Name: assignments assignments_attempt_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_attempt_fk FOREIGN KEY (attempt_id, series_id, roster_id) REFERENCES public.game_attempts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: assignments assignments_branch_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_branch_fk FOREIGN KEY (branch_id, plan_id) REFERENCES public.assignment_branches(id, plan_id) ON DELETE RESTRICT;

--
-- Name: assignments assignments_reservation_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_reservation_fk FOREIGN KEY (reservation_id, plan_id, branch_id, task_id, task_version) REFERENCES public.task_version_reservations(id, plan_id, branch_id, task_id, task_version) ON DELETE RESTRICT;

--
-- Name: assignments assignments_snapshot_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_snapshot_fk FOREIGN KEY (snapshot_id, reservation_id, task_id, task_version) REFERENCES public.task_snapshots(id, reservation_id, task_id, task_version) ON DELETE RESTRICT;

--
-- Name: assignments assignments_supersedes_assignment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignments
    ADD CONSTRAINT assignments_supersedes_assignment_id_fkey FOREIGN KEY (supersedes_assignment_id) REFERENCES public.assignments(id) ON DELETE RESTRICT;

--
-- Name: task_delivery_receipts task_delivery_receipts_assignment_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_assignment_fk FOREIGN KEY (assignment_id, attempt_id, roster_id) REFERENCES public.assignments(id, attempt_id, roster_id) ON DELETE RESTRICT;

--
-- Name: task_delivery_receipts task_delivery_receipts_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: task_delivery_receipts task_delivery_receipts_snapshot_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_delivery_receipts
    ADD CONSTRAINT task_delivery_receipts_snapshot_fk FOREIGN KEY (snapshot_id, task_id, task_version) REFERENCES public.task_snapshots(id, task_id, task_version) ON DELETE RESTRICT;

--
-- Name: task_snapshots task_snapshots_reservation_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_snapshots
    ADD CONSTRAINT task_snapshots_reservation_fk FOREIGN KEY (reservation_id, task_id, task_version) REFERENCES public.task_version_reservations(id, task_id, task_version) ON DELETE RESTRICT;

--
-- Name: task_version_reservations task_version_reservations_edge_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_edge_fk FOREIGN KEY (edge_id, plan_id, branch_id, task_id, task_version) REFERENCES public.assignment_plan_edges(id, plan_id, branch_id, task_id, task_version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_version_reservations
    ADD CONSTRAINT task_version_reservations_contingency_draft_branch_fk
    FOREIGN KEY (contingency_draft_branch_id, plan_id)
    REFERENCES public.exact_draft_assignment_branches(id, plan_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_edge_fk
    FOREIGN KEY (edge_id)
    REFERENCES public.assignment_plan_edges(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_reservation_fk
    FOREIGN KEY (reservation_id)
    REFERENCES public.task_version_reservations(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_snapshot_fk
    FOREIGN KEY (proposed_snapshot_id)
    REFERENCES public.task_snapshots(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_assignment_fk
    FOREIGN KEY (replacement_assignment_attempt_id)
    REFERENCES public.assignments(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.replay_reserve_authorities
    ADD CONSTRAINT replay_reserve_authorities_assignment_fk
    FOREIGN KEY (assignment_id) REFERENCES public.assignments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authorities
    ADD CONSTRAINT replay_reserve_authorities_attempt_fk
    FOREIGN KEY (assignment_attempt_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authorities
    ADD CONSTRAINT replay_reserve_authorities_snapshot_fk
    FOREIGN KEY (active_snapshot_id) REFERENCES public.task_snapshots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authorities
    ADD CONSTRAINT replay_reserve_authorities_series_fk
    FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authorities
    ADD CONSTRAINT replay_reserve_authorities_slot_fk
    FOREIGN KEY (slot_id, series_id, roster_id) REFERENCES public.game_slots(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authorities
    ADD CONSTRAINT replay_reserve_authorities_roster_fk
    FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authority_pool_versions
    ADD CONSTRAINT replay_reserve_authority_pool_versions_authority_fk
    FOREIGN KEY (assignment_id) REFERENCES public.replay_reserve_authorities(assignment_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_authority_pool_versions
    ADD CONSTRAINT replay_reserve_authority_pool_versions_task_fk
    FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.assignment_plan_edges
    ADD CONSTRAINT assignment_plan_edges_operator_reserve_fk
    FOREIGN KEY (operator_reserve_command_id)
    REFERENCES public.operator_replay_reserves(command_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_tournament_fk
    FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_wave_fk
    FOREIGN KEY (old_wave_id) REFERENCES public.waves(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_series_fk
    FOREIGN KEY (series_id) REFERENCES public.series(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_slot_fk
    FOREIGN KEY (slot_id) REFERENCES public.game_slots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_assignment_fk
    FOREIGN KEY (assignment_id) REFERENCES public.assignments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_attempt_fk
    FOREIGN KEY (assignment_attempt_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_game_fk
    FOREIGN KEY (failed_game_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_reserve_exhaustions
    ADD CONSTRAINT replay_reserve_exhaustions_snapshot_fk
    FOREIGN KEY (from_snapshot_id) REFERENCES public.task_snapshots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_tournament_fk
    FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_wave_fk
    FOREIGN KEY (old_wave_id) REFERENCES public.waves(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_series_fk
    FOREIGN KEY (series_id) REFERENCES public.series(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_slot_fk
    FOREIGN KEY (slot_id) REFERENCES public.game_slots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_assignment_fk
    FOREIGN KEY (assignment_id) REFERENCES public.assignments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_attempt_fk
    FOREIGN KEY (assignment_attempt_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_game_fk
    FOREIGN KEY (failed_game_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_from_snapshot_fk
    FOREIGN KEY (from_snapshot_id) REFERENCES public.task_snapshots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_replay_reserves
    ADD CONSTRAINT operator_replay_reserves_task_fk
    FOREIGN KEY (proposed_task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_tournament_fk
    FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_old_wave_fk
    FOREIGN KEY (old_wave_id) REFERENCES public.waves(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_series_fk
    FOREIGN KEY (series_id) REFERENCES public.series(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_slot_fk
    FOREIGN KEY (slot_id) REFERENCES public.game_slots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_root_assignment_fk
    FOREIGN KEY (assignment_id) REFERENCES public.assignments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_source_assignment_fk
    FOREIGN KEY (assignment_attempt_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_failed_game_fk
    FOREIGN KEY (failed_game_id) REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_source_snapshot_fk
    FOREIGN KEY (from_snapshot_id) REFERENCES public.task_snapshots(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.replay_replacements
    ADD CONSTRAINT replay_replacements_snapshot_fk
    FOREIGN KEY (snapshot_id) REFERENCES public.task_snapshots(id) ON DELETE RESTRICT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS
    public.task_delivery_receipts,
    public.replay_replacements,
    public.operator_replay_reserves,
    public.replay_reserve_exhaustions,
    public.replay_reserve_authority_pool_versions,
    public.replay_reserve_authorities,
    public.assignments,
    public.task_snapshots,
    public.task_version_reservations,
    public.assignment_plan_edges,
    public.exact_draft_assignment_child_history,
    public.exact_draft_assignment_child_candidates,
    public.exact_draft_assignment_child_participants,
    public.exact_draft_assignment_child_sources,
    public.assignment_branches,
    public.exact_draft_assignment_branches,
    public.assignment_plans;

DROP FUNCTION IF EXISTS
    public.replay_command_target_guard(),
    public.replay_command_source_guard(),
    public.task_version_reservation_guard(),
    public.task_snapshot_immutable_guard(),
    public.reservation_commit_evidence_guard(),
    public.delivery_receipt_guard(),
    public.delivery_receipt_assignment_identity_guard(),
    public.participant_task_instance_id(uuid, uuid),
    public.assignment_plan_roster_guard(),
    public.assignment_plan_guard(),
    public.assignment_plan_commit_evidence_guard(),
    public.replay_reserve_authority_assignment_guard(),
    public.assignment_guard(),
    public.assignment_edge_guard(),
    public.exact_draft_assignment_child_source_guard(),
    public.exact_draft_assignment_child_participant_guard(),
    public.exact_draft_assignment_child_history_guard(),
    public.exact_draft_assignment_branch_guard(),
    public.assignment_branch_transition_evidence_guard(),
    public.assignment_branch_guard();

-- +goose StatementEnd
