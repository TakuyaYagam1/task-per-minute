-- +goose Up
-- +goose StatementBegin

-- exact_normal is the standalone assignment plan used by executable Swiss and
-- playoff series. exact_golden retains the same draft-free authority boundary
-- for Golden tie groups while allowing one reserved branch per group. Their
-- source authority is captured once so retries never rebuild mutable history.
ALTER TABLE public.assignment_plans
    DROP CONSTRAINT IF EXISTS assignment_plans_decision_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_kind_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_proof_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_source_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_state_evidence_check;

ALTER TABLE public.assignment_plans
    ADD CONSTRAINT assignment_plans_decision_check CHECK (
        ((kind IN ('conservative', 'exact_draft') AND decision_evidence_id IS NULL
            AND decision_algorithm_version IS NULL AND decision_inputs IS NULL
            AND decision_seed IS NULL AND decision_result IS NULL
            AND decision_replay_digest IS NULL AND decision_owner_id IS NULL
            AND decided_at IS NULL)
        OR (kind IN ('exact', 'exact_normal', 'exact_golden') AND decision_evidence_id IS NOT NULL
            AND decision_algorithm_version = 'hmac-sha256-order-v1'
            AND decision_inputs IS NOT NULL AND jsonb_typeof(decision_inputs) = 'array'
            AND jsonb_array_length(decision_inputs) > 0
            AND decision_seed IS NOT NULL AND octet_length(decision_seed) = 32
            AND decision_seed <> decode(repeat('00', 32), 'hex')
            AND decision_result IS NOT NULL AND jsonb_typeof(decision_result) = 'array'
            AND jsonb_array_length(decision_result) > 0
            AND decision_replay_digest IS NOT NULL AND octet_length(decision_replay_digest) = 32
            AND decision_owner_id IS NOT NULL AND decision_owner_id = id
            AND decided_at IS NOT NULL AND decided_at <= created_at))
    ),
    ADD CONSTRAINT assignment_plans_kind_check CHECK (
        kind IN ('conservative', 'exact', 'exact_draft', 'exact_normal', 'exact_golden')
    ),
    ADD CONSTRAINT assignment_plans_proof_check CHECK (
        jsonb_typeof(constraint_graph) = 'object' AND constraint_graph <> '{}'
        AND jsonb_typeof(proof_evidence) = 'object' AND proof_evidence <> '{}'
        AND (kind NOT IN ('exact_draft', 'exact_normal', 'exact_golden')
            OR (proof_hash IS NOT NULL AND proof_hash = btrim(proof_hash)
                AND char_length(proof_hash) = 64))
    ),
    ADD CONSTRAINT assignment_plans_source_check CHECK (
        source_roster_revision >= 1
        AND ((kind = 'conservative' AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NULL AND reachable_branch_count = 0)
            OR (kind = 'exact' AND parent_plan_id IS NOT NULL
                AND source_draft_revision_id IS NOT NULL AND reachable_branch_count >= 1)
            OR (kind = 'exact_draft' AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NOT NULL AND reachable_branch_count >= 1)
            OR (kind = 'exact_normal' AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NULL AND reachable_branch_count = 1)
            OR (kind = 'exact_golden' AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NULL AND reachable_branch_count >= 1))
    ),
    ADD CONSTRAINT assignment_plans_state_evidence_check CHECK (
        ((state = 'planned' AND active_branch_id IS NULL AND active_draft_branch_id IS NULL
            AND completion_draft_revision_id IS NULL AND completion_draft_revision IS NULL
            AND activation_command_id IS NULL AND completed_categories IS NULL
            AND committed_at IS NULL AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'committed' AND kind IN ('exact', 'exact_normal')
            AND active_branch_id IS NOT NULL AND active_draft_branch_id IS NULL
            AND completion_draft_revision_id IS NULL AND completion_draft_revision IS NULL
            AND activation_command_id IS NULL AND completed_categories IS NULL
            AND committed_at IS NOT NULL AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'committed' AND kind = 'exact_draft'
            AND active_branch_id IS NOT NULL AND active_draft_branch_id IS NOT NULL
            AND completion_draft_revision_id IS NOT NULL AND completion_draft_revision >= 1
            AND activation_command_id IS NOT NULL AND jsonb_typeof(completed_categories) = 'array'
            AND jsonb_array_length(completed_categories) = 3 AND committed_at IS NOT NULL
            AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'superseded' AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL AND supersession_reason = btrim(supersession_reason)
            AND supersession_reason <> ''))
    );

-- Initial tournament creation publishes a canonical empty projection.  Keep
-- the JSON collection shape strict, but permit the empty form; the
-- projection revision guard below limits that form to source_kind=initial.
ALTER TABLE public.projection_artifacts
    DROP CONSTRAINT IF EXISTS projection_artifacts_payload_check,
    ADD CONSTRAINT projection_artifacts_payload_check CHECK (
        (
            artifact_kind = 'standings'
            AND payload::jsonb ? 'entries'
            AND jsonb_typeof(payload::jsonb -> 'entries') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'entries') >= 0
            AND NOT payload::jsonb ? 'standings'
        ) OR (
            artifact_kind = 'bracket'
            AND payload::jsonb ? 'rounds'
            AND jsonb_typeof(payload::jsonb -> 'rounds') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'rounds') >= 0
            AND NOT payload::jsonb ? 'bracket'
            AND NOT payload::jsonb ? 'semifinals'
        ) OR (
            artifact_kind = 'top_four'
            AND payload::jsonb ? 'participants'
            AND jsonb_typeof(payload::jsonb -> 'participants') = 'array'
            AND (
                jsonb_array_length(payload::jsonb -> 'participants') = 0
                OR jsonb_array_length(payload::jsonb -> 'participants') = 4
            )
        ) OR (
            artifact_kind = 'champion'
            AND payload::jsonb ? 'participant_id'
            AND jsonb_typeof(payload::jsonb -> 'participant_id') = 'string'
            AND btrim(payload ->> 'participant_id') <> ''
        )
    );

-- A standalone branch has no draft identity.  Existing exact and exact_draft
-- branches continue to be checked by their existing triggers and constraints.
ALTER TABLE public.assignment_branches
    ALTER COLUMN draft_id DROP NOT NULL,
    ALTER COLUMN draft_revision_id DROP NOT NULL;

CREATE TABLE public.exact_normal_assignment_history_heads (
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    revision_id uuid NOT NULL DEFAULT gen_random_uuid(),
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (tournament_id, roster_id, series_id, slot_id),
    UNIQUE (revision_id),
    CONSTRAINT exact_normal_assignment_history_heads_check CHECK (
        tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND revision >= 1 AND updated_at >= created_at
    )
);

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

DROP TRIGGER IF EXISTS exact_normal_assignment_history_head_bump ON public.task_delivery_receipts;
CREATE TRIGGER exact_normal_assignment_history_head_bump
    AFTER INSERT ON public.task_delivery_receipts
    FOR EACH ROW EXECUTE FUNCTION public.exact_normal_assignment_history_head_bump();

CREATE TABLE public.exact_normal_assignment_sources (
    plan_id uuid PRIMARY KEY REFERENCES public.assignment_plans(id) ON DELETE RESTRICT,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    slot_id uuid NOT NULL,
    category_lock_id uuid NOT NULL,
    category character varying(32) NOT NULL,
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
    pool jsonb NOT NULL,
    participant_ids jsonb NOT NULL,
    participant_reservations jsonb NOT NULL,
    history jsonb NOT NULL,
    candidates jsonb NOT NULL,
    graph_digest bytea NOT NULL,
    artifact_digest bytea NOT NULL,
    proof_hash character varying(64) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT exact_normal_assignment_sources_identity_key UNIQUE
        (tournament_id, roster_id, series_id, slot_id, category_lock_id),
    CONSTRAINT exact_normal_assignment_sources_check CHECK (
        tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_lock_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_revision_id = category_lock_id
        AND series_revision >= 1 AND pool_revision >= 1 AND history_revision >= 1
        AND roster_revision >= 1 AND artifact_revision >= 1 AND category_revision >= 1
        AND jsonb_typeof(pool) = 'object'
        AND jsonb_typeof(participant_ids) = 'array'
        AND jsonb_array_length(participant_ids) = 2
        AND jsonb_typeof(participant_reservations) = 'array'
        AND jsonb_array_length(participant_reservations) = 2
        AND jsonb_typeof(history) = 'array'
        AND jsonb_typeof(candidates) = 'array'
        AND jsonb_array_length(candidates) >= 3
        AND octet_length(graph_digest) = 32
        AND graph_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(artifact_digest) = 32
        AND artifact_digest <> decode(repeat('00', 32), 'hex')
        AND proof_hash = btrim(proof_hash) AND char_length(proof_hash) = 64
    )
);

CREATE INDEX exact_normal_assignment_sources_plan_idx
    ON public.exact_normal_assignment_sources (plan_id, created_at);

-- Extend the existing guards without weakening exact and exact_draft paths.
CREATE OR REPLACE FUNCTION public.assignment_branch_guard() RETURNS trigger
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

        IF plan_kind IN ('exact_normal', 'exact_golden') THEN
            IF plan_state <> 'planned'
                OR NEW.draft_id IS NOT NULL
                OR NEW.draft_revision_id IS NOT NULL
                OR NEW.exact_draft_branch_id IS NOT NULL
                OR NEW.exact_draft_position IS NOT NULL
                OR NEW.decision_evidence_id IS NOT NULL
                OR jsonb_typeof(NEW.category_sequence) <> 'array'
                OR (plan_kind = 'exact_normal' AND jsonb_array_length(NEW.category_sequence) <> 1)
                OR (plan_kind = 'exact_golden'
                    AND jsonb_array_length(NEW.category_sequence) NOT BETWEEN 1 AND 3) THEN
                RAISE EXCEPTION 'draft-free exact branches require canonical categories and no draft authority'
                    USING ERRCODE = 'check_violation';
            END IF;
            RETURN NEW;
        END IF;

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
        OR (OLD.state = 'reserved' AND NEW.state NOT IN ('reserved', 'active', 'released', 'superseded'))
        OR (OLD.state = 'active' AND NEW.state NOT IN ('active', 'superseded')) THEN
        RAISE EXCEPTION 'invalid assignment branch transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*) INTO committed_reservations
        FROM task_version_reservations
        WHERE branch_id = NEW.id AND state = 'committed';
        IF committed_reservations <> 3 THEN
            RAISE EXCEPTION 'active branch requires one committed primary and two reserves'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'released' AND OLD.state <> 'released' THEN
        SELECT COUNT(*) INTO reusable_reservations
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

CREATE OR REPLACE FUNCTION public.assignment_guard() RETURNS trigger
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
        SELECT plan.kind, plan.state, plan.active_branch_id, plan.active_draft_branch_id,
            branch.state, branch.exact_draft_branch_id, reservation.state
        INTO plan_kind, plan_state, plan_active_branch_id, plan_active_draft_branch_id,
            branch_state, branch_draft_group_id, reservation_state
        FROM assignment_plans AS plan
        JOIN assignment_branches AS branch ON branch.plan_id = plan.id AND branch.id = NEW.branch_id
        JOIN task_version_reservations AS reservation
            ON reservation.plan_id = plan.id AND reservation.branch_id = branch.id
            AND reservation.id = NEW.reservation_id
        WHERE plan.id = NEW.plan_id;

        IF NEW.state <> 'active'
            OR plan_state <> 'committed'
            OR branch_state <> 'active'
            OR reservation_state <> 'committed'
            OR (plan_kind IN ('exact', 'exact_normal')
                AND plan_active_branch_id IS DISTINCT FROM NEW.branch_id)
            OR (plan_kind = 'exact_draft'
                AND (plan_active_draft_branch_id IS DISTINCT FROM branch_draft_group_id
                    OR branch_draft_group_id IS NULL))
            OR plan_kind NOT IN ('exact', 'exact_draft', 'exact_normal') THEN
            RAISE EXCEPTION 'assignment requires the committed active branch and reservation'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.supersedes_assignment_id IS NOT NULL THEN
            SELECT attempt_id INTO previous_attempt_id
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

CREATE OR REPLACE FUNCTION public.exact_normal_assignment_plan_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    branch_count INTEGER;
    active_count INTEGER;
    edge_count INTEGER;
    reservation_count INTEGER;
    snapshot_count INTEGER;
    source_count INTEGER;
BEGIN
    IF NEW.kind <> 'exact_normal' THEN
        RETURN NEW;
    END IF;

    IF NEW.parent_plan_id IS NOT NULL OR NEW.source_draft_revision_id IS NOT NULL
        OR NEW.reachable_branch_count <> 1 THEN
        RAISE EXCEPTION 'exact normal plan cannot reference draft or parent authority'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'committed' AND (TG_OP = 'INSERT' OR OLD.state <> 'committed') THEN
        SELECT COUNT(*) INTO branch_count
        FROM assignment_branches
        WHERE plan_id = NEW.id;
        SELECT COUNT(*) INTO active_count
        FROM assignment_branches
        WHERE plan_id = NEW.id AND id = NEW.active_branch_id AND state = 'active'
            AND draft_id IS NULL AND draft_revision_id IS NULL
            AND exact_draft_branch_id IS NULL AND exact_draft_position IS NULL
            AND jsonb_typeof(category_sequence) = 'array'
            AND jsonb_array_length(category_sequence) = 1;
        SELECT COUNT(*) INTO edge_count
        FROM assignment_plan_edges AS edge
        WHERE edge.plan_id = NEW.id AND edge.branch_id = NEW.active_branch_id;
        SELECT COUNT(*) INTO reservation_count
        FROM task_version_reservations AS reservation
        WHERE reservation.plan_id = NEW.id AND reservation.branch_id = NEW.active_branch_id
            AND reservation.state = 'committed';
        SELECT COUNT(*) INTO snapshot_count
        FROM task_version_reservations AS reservation
        JOIN task_snapshots AS snapshot ON snapshot.reservation_id = reservation.id
        WHERE reservation.plan_id = NEW.id AND reservation.branch_id = NEW.active_branch_id;
        SELECT COUNT(*) INTO source_count
        FROM exact_normal_assignment_sources AS source
        WHERE source.plan_id = NEW.id;
        IF branch_count <> 1 OR active_count <> 1 OR edge_count <> 3
            OR reservation_count <> 3 OR snapshot_count <> 3 OR source_count <> 1 THEN
            RAISE EXCEPTION 'exact normal plan requires one complete committed branch and source snapshot'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS exact_normal_assignment_plan_guard ON public.assignment_plans;
CREATE TRIGGER exact_normal_assignment_plan_guard
    BEFORE INSERT OR UPDATE ON public.assignment_plans
    FOR EACH ROW EXECUTE FUNCTION public.exact_normal_assignment_plan_guard();

CREATE OR REPLACE FUNCTION public.exact_normal_assignment_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_tournament_id UUID;
    plan_roster_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'exact normal assignment source is immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;
    SELECT kind, tournament_id, roster_id
    INTO plan_kind, plan_tournament_id, plan_roster_id
    FROM assignment_plans
    WHERE id = NEW.plan_id
    FOR KEY SHARE;
    IF plan_kind <> 'exact_normal'
        OR plan_tournament_id IS DISTINCT FROM NEW.tournament_id
        OR plan_roster_id IS DISTINCT FROM NEW.roster_id
        OR NEW.category_lock_id IS DISTINCT FROM NEW.category_revision_id THEN
        RAISE EXCEPTION 'exact normal source is outside its assignment authority'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS exact_normal_assignment_source_guard ON public.exact_normal_assignment_sources;
CREATE TRIGGER exact_normal_assignment_source_guard
    BEFORE INSERT OR UPDATE OR DELETE ON public.exact_normal_assignment_sources
    FOR EACH ROW EXECUTE FUNCTION public.exact_normal_assignment_source_guard();

CREATE OR REPLACE FUNCTION public.projection_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    cutoff_sequence BIGINT;
    cutoff_source_kind VARCHAR(24);
    previous_number BIGINT;
    previous_state VARCHAR(16);
    previous_replacement_id UUID;
    replacement_previous_id UUID;
    replacement_state VARCHAR(16);
    linked_kind_count INTEGER;
    base_kind_count INTEGER;
    champion_count INTEGER;
    dependency_missing_count INTEGER;
    invalid_member_count INTEGER;
    invalid_champion_count INTEGER;
    dependency_count INTEGER;
    member_count INTEGER;
    invalid_payload_count INTEGER;
    playoff_stage_exists BOOLEAN;
    completed_swiss_exists BOOLEAN;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT sequence_number INTO cutoff_sequence
        FROM projection_cutoffs
        WHERE id = NEW.cutoff_id
        FOR KEY SHARE;

        IF cutoff_sequence <> NEW.revision_number THEN
            RAISE EXCEPTION 'projection revision must use its matching cutoff sequence'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT revision_number INTO previous_number
            FROM projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR KEY SHARE;

            IF previous_number <> NEW.revision_number - 1 THEN
                RAISE EXCEPTION 'projection revision lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'projection revisions are retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_number IS DISTINCT FROM OLD.revision_number
        OR NEW.previous_revision_id IS DISTINCT FROM OLD.previous_revision_id
        OR NEW.cutoff_id IS DISTINCT FROM OLD.cutoff_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (OLD.published_at IS NOT NULL AND NEW.published_at IS DISTINCT FROM OLD.published_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (
            OLD.superseded_by_revision_id IS NOT NULL
            AND NEW.superseded_by_revision_id IS DISTINCT FROM OLD.superseded_by_revision_id
        )
        OR (
            OLD.supersession_reason IS NOT NULL
            AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason
        ) THEN
        RAISE EXCEPTION 'projection revision identity and lifecycle evidence are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'draft' AND NEW.state = 'published')
            OR (OLD.state = 'published' AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid projection revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'published' THEN
        SELECT source_kind
        INTO cutoff_source_kind
        FROM projection_cutoffs
        WHERE id = NEW.cutoff_id
        FOR KEY SHARE;

        SELECT EXISTS (
            SELECT 1
            FROM tournament_stage_playoff_evidence AS stage
            WHERE stage.tournament_id = NEW.tournament_id
                AND stage.roster_id = NEW.roster_id
        ) INTO playoff_stage_exists;

        SELECT EXISTS (
            SELECT 1
            FROM waves AS wave
            INNER JOIN swiss_wave_links AS swiss_link
                ON swiss_link.wave_id = wave.id
                AND swiss_link.tournament_id = wave.tournament_id
                AND swiss_link.roster_id = wave.roster_id
            WHERE wave.tournament_id = NEW.tournament_id
                AND wave.roster_id = NEW.roster_id
                AND wave.state = 'completed'
        ) INTO completed_swiss_exists;

        SELECT COUNT(DISTINCT artifact_kind)
        INTO linked_kind_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top_four', 'champion');

        SELECT COUNT(DISTINCT artifact_kind)
        INTO base_kind_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top_four');

        SELECT COUNT(*)
        INTO champion_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind = 'champion';

        SELECT COUNT(*) INTO dependency_missing_count
        FROM projection_revision_artifacts AS revision_artifact
        INNER JOIN projection_artifacts AS artifact
            ON artifact.id = revision_artifact.artifact_id
        WHERE revision_artifact.revision_id = NEW.id
            AND NOT EXISTS (
                SELECT 1
                FROM projection_dependencies AS dependency
                WHERE dependency.artifact_id = revision_artifact.artifact_id
            )
            AND NOT (
                NOT completed_swiss_exists
                AND artifact.artifact_kind = 'standings'
                OR NOT playoff_stage_exists
                AND artifact.artifact_kind IN ('bracket', 'top_four')
            );

        SELECT COUNT(*) INTO invalid_member_count
        FROM projection_revision_artifacts AS revision_artifact
        INNER JOIN projection_artifacts AS artifact
            ON artifact.id = revision_artifact.artifact_id
        LEFT JOIN LATERAL (
            SELECT COUNT(*) AS member_count
            FROM projection_artifact_members AS artifact_member
            WHERE artifact_member.artifact_id = artifact.id
        ) AS member_evidence ON TRUE
        WHERE revision_artifact.revision_id = NEW.id
            AND (
                (
                    artifact.artifact_kind = 'standings'
                    AND completed_swiss_exists
                    AND member_evidence.member_count < 1
                )
                OR (
                    playoff_stage_exists
                    AND (
                        (
                            artifact.artifact_kind = 'bracket'
                            AND member_evidence.member_count < 1
                        )
                        OR (
                            artifact.artifact_kind = 'top_four'
                            AND member_evidence.member_count <> 4
                        )
                    )
                )
                OR (
                    artifact.artifact_kind = 'champion'
                    AND member_evidence.member_count <> 1
                )
            );

        SELECT COUNT(*)
        INTO invalid_champion_count
        FROM projection_revision_artifacts AS champion_link
        INNER JOIN projection_artifacts AS champion
            ON champion.id = champion_link.artifact_id
        WHERE champion_link.revision_id = NEW.id
            AND champion_link.artifact_kind = 'champion'
            AND NOT EXISTS (
                SELECT 1
                FROM projection_dependencies AS result_dependency
                INNER JOIN series AS final_series
                    ON final_series.id = result_dependency.official_result_series_id
                    AND final_series.tournament_id = NEW.tournament_id
                    AND final_series.roster_id = NEW.roster_id
                INNER JOIN tournament_stage_playoff_finals AS stage_final
                    ON stage_final.final_series_id = final_series.id
                    AND stage_final.tournament_id = NEW.tournament_id
                    AND stage_final.roster_id = NEW.roster_id
                INNER JOIN tournament_stage_playoff_evidence AS stage_evidence
                    ON stage_evidence.command_id = stage_final.command_id
                    AND stage_evidence.tournament_id = stage_final.tournament_id
                    AND stage_evidence.roster_id = stage_final.roster_id
                INNER JOIN official_result_heads AS final_head
                    ON final_head.entity_kind = 'series'
                    AND final_head.entity_id = final_series.id
                    AND final_head.series_id = final_series.id
                    AND final_head.roster_id = final_series.roster_id
                    AND final_head.current_revision_id = result_dependency.official_result_revision_id
                INNER JOIN official_result_revisions AS final_result
                    ON final_result.id = final_head.current_revision_id
                    AND final_result.entity_kind = final_head.entity_kind
                    AND final_result.entity_id = final_head.entity_id
                    AND final_result.series_id = final_head.series_id
                    AND final_result.roster_id = final_head.roster_id
                WHERE result_dependency.artifact_id = champion.id
                    AND result_dependency.dependency_kind = 'official_result'
                    AND final_series.state = 'completed'
                    AND final_series.current_result_revision_id = result_dependency.official_result_revision_id
                    AND final_series.winner_id IS NOT NULL
                    AND final_result.result_state = 'completed'
                    AND final_result.winner_id = final_series.winner_id
                    AND champion.payload ->> 'participant_id' = final_series.winner_id::TEXT
                    AND EXISTS (
                        SELECT 1
                        FROM projection_artifact_members AS champion_member
                        WHERE champion_member.artifact_id = champion.id
                            AND champion_member.participant_id = final_series.winner_id
                    )
                    AND EXISTS (
                        SELECT 1
                        FROM projection_dependencies AS bracket_dependency
                        INNER JOIN projection_revision_artifacts AS bracket_link
                            ON bracket_link.revision_id = champion_link.revision_id
                            AND bracket_link.artifact_kind = 'bracket'
                            AND bracket_link.artifact_id = bracket_dependency.depends_on_artifact_id
                        WHERE bracket_dependency.artifact_id = champion.id
                            AND bracket_dependency.dependency_kind = 'artifact'
                    )
                    AND (
                        SELECT COUNT(*)
                        FROM projection_dependencies AS result_count
                        WHERE result_count.artifact_id = champion.id
                            AND result_count.dependency_kind = 'official_result'
                    ) = 1
            );

        IF cutoff_source_kind = 'initial' THEN
            SELECT COUNT(*)
            INTO dependency_count
            FROM projection_revision_artifacts AS revision_artifact
            INNER JOIN projection_dependencies AS dependency
                ON dependency.artifact_id = revision_artifact.artifact_id
            WHERE revision_artifact.revision_id = NEW.id;

            SELECT COUNT(*)
            INTO member_count
            FROM projection_revision_artifacts AS revision_artifact
            INNER JOIN projection_artifact_members AS member
                ON member.artifact_id = revision_artifact.artifact_id
            WHERE revision_artifact.revision_id = NEW.id;

            IF linked_kind_count <> 3 OR base_kind_count <> 3 OR champion_count <> 0
                OR dependency_count <> 0 OR member_count <> 0 THEN
                RAISE EXCEPTION 'initial projection requires three empty base artifacts'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSE
            SELECT COUNT(*)
            INTO invalid_payload_count
            FROM projection_revision_artifacts AS revision_artifact
            INNER JOIN projection_artifacts AS artifact
                ON artifact.id = revision_artifact.artifact_id
            WHERE revision_artifact.revision_id = NEW.id
                AND (
                    (
                        artifact.artifact_kind = 'standings'
                        AND completed_swiss_exists
                        AND jsonb_array_length(artifact.payload::jsonb -> 'entries') = 0
                    )
                    OR (
                        playoff_stage_exists
                        AND (
                            (
                                artifact.artifact_kind = 'bracket'
                                AND jsonb_array_length(artifact.payload::jsonb -> 'rounds') = 0
                            )
                            OR (
                                artifact.artifact_kind = 'top_four'
                                AND jsonb_array_length(artifact.payload::jsonb -> 'participants') <> 4
                            )
                        )
                    )
                );

            IF linked_kind_count <> base_kind_count + champion_count
                OR base_kind_count <> 3
                OR champion_count NOT IN (0, 1)
                OR dependency_missing_count <> 0
                OR invalid_member_count <> 0
                OR invalid_champion_count <> 0
                OR invalid_payload_count <> 0 THEN
                RAISE EXCEPTION 'published projection requires base artifacts and a terminal champion when present'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT state, superseded_by_revision_id
            INTO previous_state, previous_replacement_id
            FROM projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR NO KEY UPDATE;

            IF previous_state <> 'superseded'
                OR previous_replacement_id <> NEW.id THEN
                RAISE EXCEPTION 'previous projection must atomically supersede to replacement'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    ELSIF OLD.state = 'published' AND NEW.state = 'superseded' THEN
        SELECT previous_revision_id, state
        INTO replacement_previous_id, replacement_state
        FROM projection_revisions
        WHERE id = NEW.superseded_by_revision_id
        FOR NO KEY UPDATE;

        IF replacement_previous_id <> OLD.id OR replacement_state <> 'draft' THEN
            RAISE EXCEPTION 'projection supersession must target its draft successor'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS projection_revision_guard ON public.projection_revisions;
CREATE TRIGGER projection_revision_guard
    BEFORE INSERT OR DELETE OR UPDATE ON public.projection_revisions
    FOR EACH ROW EXECUTE FUNCTION public.projection_revision_guard();

-- Playoff publication and executable graph materialization are atomic. The
-- immutable stage evidence proves the locked semifinal genesis; by deferred
-- validation time a fully materialized, not-yet-started Series is ready.
DO $$
DECLARE
    current_definition TEXT;
    executable_definition TEXT;
BEGIN
    current_definition := pg_get_functiondef(
        'public.validate_tournament_stage_playoff_evidence()'::regprocedure
    );
    executable_definition := replace(
        current_definition,
        'series.state <> ''locked''',
        'series.state NOT IN (''locked'', ''ready'')'
    );
    IF executable_definition = current_definition THEN
        RAISE EXCEPTION 'cannot permit ready executable playoff semifinal evidence';
    END IF;
    EXECUTE executable_definition;
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
        'series.state NOT IN (''locked'', ''ready'')',
        'series.state <> ''locked'''
    );
    IF restored_definition = current_definition THEN
        RAISE EXCEPTION 'cannot restore locked playoff semifinal evidence validation';
    END IF;
    EXECUTE restored_definition;
END;
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.assignment_plans
        WHERE kind IN ('exact_normal', 'exact_golden')
    ) THEN
        RAISE EXCEPTION 'cannot roll back 000016 while draft-free exact plans exist';
    END IF;
END;
$$;

ALTER TABLE public.projection_artifacts
    DROP CONSTRAINT IF EXISTS projection_artifacts_payload_check,
    ADD CONSTRAINT projection_artifacts_payload_check CHECK (
        (
            artifact_kind = 'standings'
            AND payload::jsonb ? 'entries'
            AND jsonb_typeof(payload::jsonb -> 'entries') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'entries') > 0
            AND NOT payload::jsonb ? 'standings'
        ) OR (
            artifact_kind = 'bracket'
            AND payload::jsonb ? 'rounds'
            AND jsonb_typeof(payload::jsonb -> 'rounds') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'rounds') > 0
            AND NOT payload::jsonb ? 'bracket'
            AND NOT payload::jsonb ? 'semifinals'
        ) OR (
            artifact_kind = 'top_four'
            AND payload::jsonb ? 'participants'
            AND jsonb_typeof(payload::jsonb -> 'participants') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'participants') = 4
        ) OR (
            artifact_kind = 'champion'
            AND payload::jsonb ? 'participant_id'
            AND jsonb_typeof(payload::jsonb -> 'participant_id') = 'string'
            AND btrim(payload ->> 'participant_id') <> ''
        )
    );

DROP TRIGGER IF EXISTS projection_revision_guard ON public.projection_revisions;
CREATE OR REPLACE FUNCTION public.projection_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    cutoff_sequence BIGINT;
    previous_number BIGINT;
    previous_state VARCHAR(16);
    previous_replacement_id UUID;
    replacement_previous_id UUID;
    replacement_state VARCHAR(16);
    linked_kind_count INTEGER;
    base_kind_count INTEGER;
    champion_count INTEGER;
    dependency_missing_count INTEGER;
    invalid_member_count INTEGER;
    invalid_champion_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT sequence_number INTO cutoff_sequence
        FROM projection_cutoffs
        WHERE id = NEW.cutoff_id
        FOR KEY SHARE;

        IF cutoff_sequence <> NEW.revision_number THEN
            RAISE EXCEPTION 'projection revision must use its matching cutoff sequence'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT revision_number INTO previous_number
            FROM projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR KEY SHARE;

            IF previous_number <> NEW.revision_number - 1 THEN
                RAISE EXCEPTION 'projection revision lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'projection revisions are retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_number IS DISTINCT FROM OLD.revision_number
        OR NEW.previous_revision_id IS DISTINCT FROM OLD.previous_revision_id
        OR NEW.cutoff_id IS DISTINCT FROM OLD.cutoff_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (OLD.published_at IS NOT NULL AND NEW.published_at IS DISTINCT FROM OLD.published_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (
            OLD.superseded_by_revision_id IS NOT NULL
            AND NEW.superseded_by_revision_id IS DISTINCT FROM OLD.superseded_by_revision_id
        )
        OR (
            OLD.supersession_reason IS NOT NULL
            AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason
        ) THEN
        RAISE EXCEPTION 'projection revision identity and lifecycle evidence are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'draft' AND NEW.state = 'published')
            OR (OLD.state = 'published' AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid projection revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'published' THEN
        SELECT COUNT(DISTINCT artifact_kind)
        INTO linked_kind_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top_four', 'champion');

        SELECT COUNT(DISTINCT artifact_kind)
        INTO base_kind_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top_four');

        SELECT COUNT(*)
        INTO champion_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind = 'champion';

        SELECT COUNT(*) INTO dependency_missing_count
        FROM projection_revision_artifacts AS revision_artifact
        WHERE revision_artifact.revision_id = NEW.id
            AND NOT EXISTS (
                SELECT 1
                FROM projection_dependencies AS dependency
                WHERE dependency.artifact_id = revision_artifact.artifact_id
            );

        SELECT COUNT(*) INTO invalid_member_count
        FROM projection_revision_artifacts AS revision_artifact
        INNER JOIN projection_artifacts AS artifact
            ON artifact.id = revision_artifact.artifact_id
        LEFT JOIN LATERAL (
            SELECT COUNT(*) AS member_count
            FROM projection_artifact_members AS artifact_member
            WHERE artifact_member.artifact_id = artifact.id
        ) AS member_evidence ON TRUE
        WHERE revision_artifact.revision_id = NEW.id
            AND (
                (
                    artifact.artifact_kind IN ('standings', 'bracket')
                    AND member_evidence.member_count < 1
                )
                OR (
                    artifact.artifact_kind = 'top_four'
                    AND member_evidence.member_count <> 4
                )
                OR (
                    artifact.artifact_kind = 'champion'
                    AND member_evidence.member_count <> 1
                )
            );

        SELECT COUNT(*)
        INTO invalid_champion_count
        FROM projection_revision_artifacts AS champion_link
        INNER JOIN projection_artifacts AS champion
            ON champion.id = champion_link.artifact_id
        WHERE champion_link.revision_id = NEW.id
            AND champion_link.artifact_kind = 'champion'
            AND NOT EXISTS (
                SELECT 1
                FROM projection_dependencies AS result_dependency
                INNER JOIN series AS final_series
                    ON final_series.id = result_dependency.official_result_series_id
                    AND final_series.tournament_id = NEW.tournament_id
                    AND final_series.roster_id = NEW.roster_id
                INNER JOIN tournament_stage_playoff_finals AS stage_final
                    ON stage_final.final_series_id = final_series.id
                    AND stage_final.tournament_id = NEW.tournament_id
                    AND stage_final.roster_id = NEW.roster_id
                INNER JOIN tournament_stage_playoff_evidence AS stage_evidence
                    ON stage_evidence.command_id = stage_final.command_id
                    AND stage_evidence.tournament_id = stage_final.tournament_id
                    AND stage_evidence.roster_id = stage_final.roster_id
                INNER JOIN official_result_heads AS final_head
                    ON final_head.entity_kind = 'series'
                    AND final_head.entity_id = final_series.id
                    AND final_head.series_id = final_series.id
                    AND final_head.roster_id = final_series.roster_id
                    AND final_head.current_revision_id = result_dependency.official_result_revision_id
                INNER JOIN official_result_revisions AS final_result
                    ON final_result.id = final_head.current_revision_id
                    AND final_result.entity_kind = final_head.entity_kind
                    AND final_result.entity_id = final_head.entity_id
                    AND final_result.series_id = final_head.series_id
                    AND final_result.roster_id = final_head.roster_id
                WHERE result_dependency.artifact_id = champion.id
                    AND result_dependency.dependency_kind = 'official_result'
                    AND final_series.state = 'completed'
                    AND final_series.current_result_revision_id = result_dependency.official_result_revision_id
                    AND final_series.winner_id IS NOT NULL
                    AND final_result.result_state = 'completed'
                    AND final_result.winner_id = final_series.winner_id
                    AND champion.payload ->> 'participant_id' = final_series.winner_id::TEXT
                    AND EXISTS (
                        SELECT 1
                        FROM projection_artifact_members AS champion_member
                        WHERE champion_member.artifact_id = champion.id
                            AND champion_member.participant_id = final_series.winner_id
                    )
                    AND EXISTS (
                        SELECT 1
                        FROM projection_dependencies AS bracket_dependency
                        INNER JOIN projection_revision_artifacts AS bracket_link
                            ON bracket_link.revision_id = champion_link.revision_id
                            AND bracket_link.artifact_kind = 'bracket'
                            AND bracket_link.artifact_id = bracket_dependency.depends_on_artifact_id
                        WHERE bracket_dependency.artifact_id = champion.id
                            AND bracket_dependency.dependency_kind = 'artifact'
                    )
                    AND (
                        SELECT COUNT(*)
                        FROM projection_dependencies AS result_count
                        WHERE result_count.artifact_id = champion.id
                            AND result_count.dependency_kind = 'official_result'
                    ) = 1
            );

        IF linked_kind_count <> base_kind_count + champion_count
            OR base_kind_count <> 3
            OR champion_count NOT IN (0, 1)
            OR dependency_missing_count <> 0
            OR invalid_member_count <> 0
            OR invalid_champion_count <> 0 THEN
            RAISE EXCEPTION 'published projection requires base artifacts and a terminal champion when present'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT state, superseded_by_revision_id
            INTO previous_state, previous_replacement_id
            FROM projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR NO KEY UPDATE;

            IF previous_state <> 'superseded'
                OR previous_replacement_id <> NEW.id THEN
                RAISE EXCEPTION 'previous projection must atomically supersede to replacement'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    ELSIF OLD.state = 'published' AND NEW.state = 'superseded' THEN
        SELECT previous_revision_id, state
        INTO replacement_previous_id, replacement_state
        FROM projection_revisions
        WHERE id = NEW.superseded_by_revision_id
        FOR NO KEY UPDATE;

        IF replacement_previous_id <> OLD.id OR replacement_state <> 'draft' THEN
            RAISE EXCEPTION 'projection supersession must target its draft successor'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
CREATE TRIGGER projection_revision_guard
    BEFORE INSERT OR DELETE OR UPDATE ON public.projection_revisions
    FOR EACH ROW EXECUTE FUNCTION public.projection_revision_guard();

DROP TABLE IF EXISTS public.exact_normal_assignment_sources;
DROP TABLE IF EXISTS public.exact_normal_assignment_history_heads;
ALTER TABLE public.assignment_branches
    ALTER COLUMN draft_id SET NOT NULL,
    ALTER COLUMN draft_revision_id SET NOT NULL;
ALTER TABLE public.assignment_plans
    DROP CONSTRAINT IF EXISTS assignment_plans_decision_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_kind_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_proof_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_source_check,
    DROP CONSTRAINT IF EXISTS assignment_plans_state_evidence_check,
    ADD CONSTRAINT assignment_plans_decision_check CHECK (
        ((kind IN ('conservative', 'exact_draft') AND decision_evidence_id IS NULL
            AND decision_algorithm_version IS NULL AND decision_inputs IS NULL
            AND decision_seed IS NULL AND decision_result IS NULL
            AND decision_replay_digest IS NULL AND decision_owner_id IS NULL
            AND decided_at IS NULL)
        OR (kind = 'exact' AND decision_evidence_id IS NOT NULL
            AND decision_algorithm_version = 'hmac-sha256-order-v1'
            AND decision_inputs IS NOT NULL AND jsonb_typeof(decision_inputs) = 'array'
            AND jsonb_array_length(decision_inputs) > 0
            AND decision_seed IS NOT NULL AND octet_length(decision_seed) = 32
            AND decision_seed <> decode(repeat('00', 32), 'hex')
            AND decision_result IS NOT NULL AND jsonb_typeof(decision_result) = 'array'
            AND jsonb_array_length(decision_result) > 0
            AND decision_replay_digest IS NOT NULL AND octet_length(decision_replay_digest) = 32
            AND decision_owner_id IS NOT NULL AND decision_owner_id = id
            AND decided_at IS NOT NULL AND decided_at <= created_at))
    ),
    ADD CONSTRAINT assignment_plans_kind_check CHECK (kind IN ('conservative', 'exact', 'exact_draft')),
    ADD CONSTRAINT assignment_plans_proof_check CHECK (
        jsonb_typeof(constraint_graph) = 'object' AND constraint_graph <> '{}'
        AND jsonb_typeof(proof_evidence) = 'object' AND proof_evidence <> '{}'
        AND (kind <> 'exact_draft' OR (proof_hash IS NOT NULL AND proof_hash = btrim(proof_hash)
            AND char_length(proof_hash) = 64))
    ),
    ADD CONSTRAINT assignment_plans_source_check CHECK (
        source_roster_revision >= 1
        AND ((kind = 'conservative' AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NULL AND reachable_branch_count = 0)
            OR (kind = 'exact' AND parent_plan_id IS NOT NULL
                AND source_draft_revision_id IS NOT NULL AND reachable_branch_count >= 1)
            OR (kind = 'exact_draft' AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NOT NULL AND reachable_branch_count >= 1))
    ),
    ADD CONSTRAINT assignment_plans_state_evidence_check CHECK (
        ((state = 'planned' AND active_branch_id IS NULL AND active_draft_branch_id IS NULL
            AND completion_draft_revision_id IS NULL AND completion_draft_revision IS NULL
            AND activation_command_id IS NULL AND completed_categories IS NULL
            AND committed_at IS NULL AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'committed' AND kind = 'exact' AND active_branch_id IS NOT NULL
            AND active_draft_branch_id IS NULL AND completion_draft_revision_id IS NULL
            AND completion_draft_revision IS NULL AND activation_command_id IS NULL
            AND completed_categories IS NULL AND committed_at IS NOT NULL
            AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'committed' AND kind = 'exact_draft' AND active_branch_id IS NOT NULL
            AND active_draft_branch_id IS NOT NULL AND completion_draft_revision_id IS NOT NULL
            AND completion_draft_revision >= 1 AND activation_command_id IS NOT NULL
            AND jsonb_typeof(completed_categories) = 'array'
            AND jsonb_array_length(completed_categories) = 3 AND committed_at IS NOT NULL
            AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'superseded' AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL AND supersession_reason = btrim(supersession_reason)
            AND supersession_reason <> ''))
    );
-- +goose StatementEnd
