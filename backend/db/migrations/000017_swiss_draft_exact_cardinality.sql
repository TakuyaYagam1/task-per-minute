-- +goose Up
-- +goose StatementBegin

SET LOCAL check_function_bodies = false;

-- Exact draft groups use one category for BO1 and three categories for BO3.
-- The original schema encoded the BO3 cardinality directly in table checks and
-- triggers, so keep the shared tables broad and enforce the format-specific
-- count at every exact-draft lifecycle boundary below.
CREATE OR REPLACE FUNCTION public.exact_draft_required_category_count(input_draft_id uuid)
RETURNS integer
LANGUAGE sql
STABLE
AS $$
    SELECT COALESCE((
        SELECT CASE draft.format
            WHEN 'bo1' THEN 1
            WHEN 'bo3' THEN 3
            ELSE 0
        END
        FROM drafts AS draft
        WHERE draft.id = input_draft_id
    ), 0);
$$;

CREATE OR REPLACE FUNCTION public.exact_draft_required_category_count_for_revision(input_revision_id uuid)
RETURNS integer
LANGUAGE sql
STABLE
AS $$
    SELECT COALESCE((
        SELECT public.exact_draft_required_category_count(revision.draft_id)
        FROM draft_revisions AS revision
        WHERE revision.id = input_revision_id
    ), 0);
$$;

ALTER TABLE public.exact_draft_assignment_branches
    DROP CONSTRAINT IF EXISTS exact_draft_assignment_branches_category_check,
    ADD CONSTRAINT exact_draft_assignment_branches_category_check CHECK (
        jsonb_typeof(category_sequence) = 'array'
        AND jsonb_array_length(category_sequence) BETWEEN 1 AND 3
    );

ALTER TABLE public.assignment_plans
    DROP CONSTRAINT IF EXISTS assignment_plans_state_evidence_check,
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
            AND jsonb_array_length(completed_categories) BETWEEN 1 AND 3
            AND committed_at IS NOT NULL AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'superseded' AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL AND supersession_reason = btrim(supersession_reason)
            AND supersession_reason <> ''))
    );

-- Swiss drafts have no playoff-final stage row. Keep their receipt history
-- head separate so the existing final-stage FK and trigger remain strict.
CREATE TABLE public.swiss_draft_delivery_history_heads (
    draft_id uuid NOT NULL,
    series_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    revision bigint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT swiss_draft_delivery_history_heads_identity_check CHECK (
        draft_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND second_participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_participant_id <> second_participant_id
        AND revision >= 1
        AND updated_at >= created_at
    )
);

ALTER TABLE ONLY public.swiss_draft_delivery_history_heads
    ADD CONSTRAINT swiss_draft_delivery_history_heads_pkey PRIMARY KEY (draft_id),
    ADD CONSTRAINT swiss_draft_delivery_history_heads_revision_key UNIQUE (revision_id),
    ADD CONSTRAINT swiss_draft_delivery_history_heads_draft_fk
        FOREIGN KEY (draft_id, series_id, roster_id) REFERENCES public.drafts(id, series_id, roster_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT swiss_draft_delivery_history_heads_series_fk
        FOREIGN KEY (series_id, tournament_id, roster_id)
        REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT,
    ADD CONSTRAINT swiss_draft_delivery_history_heads_first_participant_fk
        FOREIGN KEY (roster_id, first_participant_id)
        REFERENCES public.participants(roster_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT swiss_draft_delivery_history_heads_second_participant_fk
        FOREIGN KEY (roster_id, second_participant_id)
        REFERENCES public.participants(roster_id, id)
        ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION public.swiss_draft_history_scope_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (
        NEW.draft_id IS DISTINCT FROM OLD.draft_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.first_participant_id IS DISTINCT FROM OLD.first_participant_id
        OR NEW.second_participant_id IS DISTINCT FROM OLD.second_participant_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    ) THEN
        RAISE EXCEPTION 'Swiss draft history identity is immutable' USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM drafts AS draft
        INNER JOIN series AS target ON target.id = draft.series_id AND target.roster_id = draft.roster_id
        INNER JOIN wave_series AS member ON member.series_id = target.id AND member.roster_id = target.roster_id
        INNER JOIN swiss_wave_links AS link ON link.wave_id = member.wave_id AND link.roster_id = target.roster_id
        WHERE draft.id = NEW.draft_id AND draft.series_id = NEW.series_id AND draft.roster_id = NEW.roster_id
            AND draft.format = 'bo1' AND target.tournament_id = NEW.tournament_id
            AND draft.first_participant_id = NEW.first_participant_id
            AND draft.second_participant_id = NEW.second_participant_id
    ) THEN
        RAISE EXCEPTION 'Swiss draft history must match its Series and participants' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER swiss_draft_history_scope_guard
BEFORE INSERT OR UPDATE ON public.swiss_draft_delivery_history_heads
FOR EACH ROW EXECUTE FUNCTION public.swiss_draft_history_scope_guard();

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

DROP TRIGGER IF EXISTS swiss_draft_delivery_history_head_guard ON public.task_delivery_receipts;
CREATE TRIGGER swiss_draft_delivery_history_head_guard
AFTER INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.swiss_draft_delivery_history_head_guard();

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
                OR NEW.exact_draft_position IS NULL
                OR NEW.exact_draft_position < 1
                OR group_category_sequence IS NULL
                OR NEW.exact_draft_position > jsonb_array_length(group_category_sequence)
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

DROP TRIGGER IF EXISTS assignment_branch_guard ON public.assignment_branches;
CREATE TRIGGER assignment_branch_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.assignment_branches
FOR EACH ROW EXECUTE FUNCTION public.assignment_branch_guard();

CREATE OR REPLACE FUNCTION public.exact_draft_assignment_branch_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_state VARCHAR(16);
    source_draft_revision_id UUID;
    required_categories INTEGER;
    child_count INTEGER;
    active_child_count INTEGER;
    released_child_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT plan.kind,
            plan.state,
            plan.source_draft_revision_id,
            public.exact_draft_required_category_count_for_revision(plan.source_draft_revision_id)
        INTO plan_kind, plan_state, source_draft_revision_id, required_categories
        FROM assignment_plans AS plan
        WHERE plan.id = NEW.plan_id
        FOR UPDATE;

        IF plan_kind <> 'exact_draft'
            OR plan_state <> 'planned'
            OR required_categories NOT IN (1, 3)
            OR NEW.state <> 'reserved'
            OR NEW.draft_revision_id IS DISTINCT FROM source_draft_revision_id
            OR jsonb_typeof(NEW.category_sequence) <> 'array'
            OR jsonb_array_length(NEW.category_sequence) <> required_categories THEN
            RAISE EXCEPTION 'exact draft group requires its draft format category count'
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

    SELECT public.exact_draft_required_category_count(draft.id)
    INTO required_categories
    FROM drafts AS draft
    WHERE draft.id = NEW.draft_id;

    SELECT COUNT(*)
    INTO child_count
    FROM assignment_branches
    WHERE plan_id = NEW.plan_id
        AND exact_draft_branch_id = NEW.id;

    IF required_categories NOT IN (1, 3) OR child_count <> required_categories THEN
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

        IF active_child_count <> required_categories * 3 THEN
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

        IF released_child_count <> required_categories * 3 THEN
            RAISE EXCEPTION 'released exact draft branch must release every undisclosed child reservation'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS exact_draft_assignment_branch_guard ON public.exact_draft_assignment_branches;
CREATE TRIGGER exact_draft_assignment_branch_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.exact_draft_assignment_branches
FOR EACH ROW EXECUTE FUNCTION public.exact_draft_assignment_branch_guard();

CREATE OR REPLACE FUNCTION public.exact_draft_assignment_child_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_tournament_id UUID;
    plan_roster_id UUID;
    child_group_id UUID;
    child_position SMALLINT;
    draft_id UUID;
    draft_series_id UUID;
    draft_category_revision_id UUID;
    required_categories INTEGER;
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
        draft.id,
        draft.series_id,
        draft.category_revision_id,
        public.exact_draft_required_category_count(draft.id)
    INTO plan_kind,
        plan_tournament_id,
        plan_roster_id,
        child_group_id,
        child_position,
        draft_id,
        draft_series_id,
        draft_category_revision_id,
        required_categories
    FROM assignment_branches AS child
    INNER JOIN assignment_plans AS plan ON plan.id = child.plan_id
    INNER JOIN drafts AS draft ON draft.id = child.draft_id
    WHERE child.id = NEW.child_branch_id
        AND child.plan_id = NEW.plan_id
    FOR KEY SHARE OF child, plan, draft;

    IF plan_kind <> 'exact_draft'
        OR child_group_id IS NULL
        OR child_position IS NULL
        OR required_categories NOT IN (1, 3)
        OR child_position < 1
        OR child_position > required_categories
        OR NEW.tournament_id IS DISTINCT FROM plan_tournament_id
        OR NEW.roster_id IS DISTINCT FROM plan_roster_id
        OR NEW.series_id IS DISTINCT FROM draft_series_id
        OR NEW.category_lock_id IS DISTINCT FROM draft_category_revision_id
        OR NEW.category_revision_id IS DISTINCT FROM draft_category_revision_id
        OR NEW.created_at IS NULL THEN
        RAISE EXCEPTION 'exact draft child source is outside its draft authority'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS exact_draft_assignment_child_source_guard ON public.exact_draft_assignment_child_sources;
CREATE TRIGGER exact_draft_assignment_child_source_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.exact_draft_assignment_child_sources
FOR EACH ROW EXECUTE FUNCTION public.exact_draft_assignment_child_source_guard();

CREATE OR REPLACE FUNCTION public.assignment_plan_guard() RETURNS trigger
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
    required_categories INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.kind = 'exact' THEN
            SELECT kind,
                tournament_id,
                roster_id,
                source_roster_revision,
                source_pool_revision_id
            INTO parent_kind,
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
                (branch.id = NEW.active_branch_id AND branch.state <> 'active')
                OR (branch.id <> NEW.active_branch_id AND branch.state <> 'released')
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
        SELECT public.exact_draft_required_category_count_for_revision(NEW.source_draft_revision_id)
        INTO required_categories;

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

        IF required_categories NOT IN (1, 3)
            OR draft_group_count <> NEW.reachable_branch_count
            OR active_draft_group_count <> 1
            OR expected_active_child_id IS DISTINCT FROM NEW.active_branch_id
            OR active_category_sequence IS DISTINCT FROM NEW.completed_categories
            OR completion_matches IS DISTINCT FROM true
            OR active_child_count IS DISTINCT FROM required_categories * 3
            OR released_child_count IS DISTINCT FROM (draft_group_count - 1) * required_categories * 3
            OR branch_count <> draft_group_count * required_categories
            OR child_source_count <> branch_count
            OR invalid_child_source_count <> 0 THEN
            RAISE EXCEPTION 'exact draft plan must commit one full category branch and release every losing branch'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS assignment_plan_guard ON public.assignment_plans;
CREATE TRIGGER assignment_plan_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.assignment_plans
FOR EACH ROW EXECUTE FUNCTION public.assignment_plan_guard();

-- The Swiss lock proof binds the selected category to the exact completed
-- draft revision while preserving the original graph and reservation checks.
CREATE OR REPLACE FUNCTION public.validate_swiss_round_lock_proof() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    proof_round_id UUID;
    proof_tournament_id UUID;
    proof_roster_id UUID;
    proof_round_number SMALLINT;
    proof_round_revision BIGINT;
    proof_source_projection_revision_id UUID;
    proof_source_projection_revision BIGINT;
    proof_roster_revision BIGINT;
    proof_history_revision BIGINT;
    proof_preflight_revision_id UUID;
    proof_normal_pool_revision_id UUID;
    proof_normal_pool_revision BIGINT;
    proof_wave_id UUID;
    proof_wave_revision_id UUID;
    proof_wave_revision BIGINT;
    proof_locked_at TIMESTAMPTZ;
    proof_bye_participant_id UUID;
    proof_mode TEXT;
    proof_terminal_command_id UUID;
    terminal_authority_valid BOOLEAN;
    stored_round_number SMALLINT;
    stored_round_revision BIGINT;
    stored_lock_revision BIGINT;
    member_count INTEGER;
    series_count INTEGER;
    member_coverage_count INTEGER;
    pairing_mismatch_count INTEGER;
BEGIN
    proof_round_id := NEW.round_id;

    SELECT proof.tournament_id,
        proof.roster_id,
        proof.round_number,
        proof.round_revision,
        proof.source_projection_revision_id,
        proof.source_projection_revision,
        proof.roster_revision,
        proof.history_revision,
        proof.preflight_revision_id,
        proof.normal_pool_revision_id,
        proof.normal_pool_revision,
        proof.wave_id,
        proof.wave_revision_id,
        proof.wave_revision,
        proof.locked_at,
        proof.bye_participant_id,
        proof.proof_mode,
        proof.terminal_command_id
    INTO proof_tournament_id,
        proof_roster_id,
        proof_round_number,
        proof_round_revision,
        proof_source_projection_revision_id,
        proof_source_projection_revision,
        proof_roster_revision,
        proof_history_revision,
        proof_preflight_revision_id,
        proof_normal_pool_revision_id,
        proof_normal_pool_revision,
        proof_wave_id,
        proof_wave_revision_id,
        proof_wave_revision,
        proof_locked_at,
        proof_bye_participant_id,
        proof_mode,
        proof_terminal_command_id
    FROM swiss_round_lock_proofs AS proof
    WHERE proof.round_id = proof_round_id
    FOR KEY SHARE;

    IF proof_roster_id IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT swiss_round.round_number,
        swiss_round.revision,
        swiss_round.lock_revision
    INTO stored_round_number,
        stored_round_revision,
        stored_lock_revision
    FROM swiss_rounds AS swiss_round
    WHERE swiss_round.id = proof_round_id
        AND swiss_round.roster_id = proof_roster_id
    FOR KEY SHARE;

    IF stored_round_number IS NULL
        OR stored_round_number <> proof_round_number
        OR stored_round_revision <> proof_round_revision
        OR stored_lock_revision <> proof_round_revision THEN
        RAISE EXCEPTION 'Swiss round lock proof no longer matches locked round'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO member_count
    FROM swiss_round_lock_proof_members AS member
    WHERE member.round_id = proof_round_id
        AND member.roster_id = proof_roster_id;

    SELECT COUNT(*)
    INTO series_count
    FROM swiss_round_lock_proof_series AS proof_series
    WHERE proof_series.round_id = proof_round_id
        AND proof_series.roster_id = proof_roster_id;

    IF member_count < 4
        OR member_count > 16
        OR (proof_bye_participant_id IS NULL AND member_count % 2 <> 0)
        OR (proof_bye_participant_id IS NOT NULL AND member_count % 2 <> 1)
        OR series_count <> (member_count - CASE WHEN proof_bye_participant_id IS NULL THEN 0 ELSE 1 END) / 2 THEN
        RAISE EXCEPTION 'Swiss round lock proof has incomplete membership or series coverage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO member_coverage_count
    FROM (
        SELECT proof_series.first_participant_id AS participant_id
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
        UNION ALL
        SELECT proof_series.second_participant_id
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
        UNION ALL
        SELECT proof_bye_participant_id
        WHERE proof_bye_participant_id IS NOT NULL
    ) AS covered;

    IF member_coverage_count <> member_count
        OR EXISTS (
            SELECT 1
            FROM swiss_round_lock_proof_members AS member
            LEFT JOIN (
                SELECT proof_series.first_participant_id AS participant_id
                FROM swiss_round_lock_proof_series AS proof_series
                WHERE proof_series.round_id = proof_round_id
                    AND proof_series.roster_id = proof_roster_id
                UNION ALL
                SELECT proof_series.second_participant_id
                FROM swiss_round_lock_proof_series AS proof_series
                WHERE proof_series.round_id = proof_round_id
                    AND proof_series.roster_id = proof_roster_id
                UNION ALL
                SELECT proof_bye_participant_id
                WHERE proof_bye_participant_id IS NOT NULL
            ) AS covered ON covered.participant_id = member.participant_id
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
            GROUP BY member.participant_id
            HAVING COUNT(covered.participant_id) <> 1
        ) THEN
        RAISE EXCEPTION 'Swiss round lock proof membership does not exactly match series seats'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO pairing_mismatch_count
    FROM swiss_round_lock_proof_series AS proof_series
    LEFT JOIN swiss_pairing_members AS first_member
        ON first_member.pairing_id = proof_series.pairing_id
        AND first_member.round_id = proof_series.round_id
        AND first_member.roster_id = proof_series.roster_id
        AND first_member.seat = 1
    LEFT JOIN swiss_pairing_members AS second_member
        ON second_member.pairing_id = proof_series.pairing_id
        AND second_member.round_id = proof_series.round_id
        AND second_member.roster_id = proof_series.roster_id
        AND second_member.seat = 2
    WHERE proof_series.round_id = proof_round_id
        AND proof_series.roster_id = proof_roster_id
        AND (
            first_member.participant_id IS DISTINCT FROM proof_series.first_participant_id
            OR second_member.participant_id IS DISTINCT FROM proof_series.second_participant_id
        );

    IF pairing_mismatch_count <> 0 THEN
        RAISE EXCEPTION 'Swiss round lock proof pairing differs from persisted pairing'
            USING ERRCODE = 'check_violation';
    END IF;

    terminal_authority_valid := FALSE;
    IF proof_mode = 'pre_start_forfeit' THEN
        SELECT EXISTS (
            SELECT 1 FROM operator_forfeit_commits AS terminal
            JOIN projection_revisions AS publication ON publication.id = terminal.projection_evidence_id
                AND publication.tournament_id = proof_tournament_id AND publication.roster_id = proof_roster_id
                AND publication.previous_revision_id = proof_source_projection_revision_id
                AND publication.state IN ('published', 'superseded')
            JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id AND member.series_id = terminal.series_id
            WHERE terminal.command_id = proof_terminal_command_id
                AND terminal.tournament_id = proof_tournament_id AND terminal.roster_id = proof_roster_id
                AND terminal.source_projection_revision_id = proof_source_projection_revision_id
                AND terminal.source_projection_revision = proof_source_projection_revision
                AND terminal.resolved_at = proof_locked_at
        ) INTO terminal_authority_valid;
    ELSIF proof_mode = 'normal_no_show' THEN
        SELECT EXISTS (
            SELECT 1 FROM normal_no_show_commits AS terminal
            JOIN official_result_revisions AS result ON result.id = terminal.series_result_revision_id
                AND result.source_projection_revision_id = proof_source_projection_revision_id
                AND result.source_projection_revision = proof_source_projection_revision
            JOIN projection_revisions AS publication ON publication.id = terminal.projection_evidence_id
                AND publication.tournament_id = proof_tournament_id AND publication.roster_id = proof_roster_id
                AND publication.previous_revision_id = proof_source_projection_revision_id
                AND publication.state IN ('published', 'superseded')
            JOIN ready_windows AS ready_window ON ready_window.id = terminal.ready_window_id
                AND ready_window.wave_id = proof_wave_id AND ready_window.revision_id = terminal.ready_window_revision_id
                AND ready_window.state = 'expired'
            JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id AND member.series_id = terminal.series_id
            WHERE terminal.command_id = proof_terminal_command_id
                AND terminal.tournament_id = proof_tournament_id AND terminal.roster_id = proof_roster_id
                AND terminal.wave_id = proof_wave_id AND terminal.expected_wave_revision = proof_wave_revision
                AND terminal.resolved_at = proof_locked_at
        ) INTO terminal_authority_valid;
    END IF;

    -- All sources below are intentionally normalized tables. The proof may
    -- not stand in for a stale projection, preflight, pool, Wave, category,
    -- assignment plan, or reservation. These tables are introduced by later
    -- fresh-schema migrations, so this deferred trigger is the integrity
    -- boundary rather than forward foreign keys in migration 000004.
    IF NOT EXISTS (
        SELECT 1
        FROM rosters AS roster
        JOIN tournaments AS tournament ON tournament.id = roster.tournament_id
        JOIN swiss_wave_links AS swiss_link
            ON swiss_link.round_id = proof_round_id
            AND swiss_link.roster_id = proof_roster_id
            AND swiss_link.tournament_id = proof_tournament_id
            AND swiss_link.wave_id = proof_wave_id
        JOIN waves AS wave
            ON wave.id = swiss_link.wave_id
            AND wave.tournament_id = proof_tournament_id
            AND wave.roster_id = proof_roster_id
        JOIN projection_revisions AS source_projection
            ON source_projection.id = proof_source_projection_revision_id
            AND source_projection.tournament_id = proof_tournament_id
            AND source_projection.roster_id = proof_roster_id
            AND source_projection.revision_number = proof_source_projection_revision
            AND (source_projection.state = 'published'
                OR (terminal_authority_valid AND source_projection.state = 'superseded'))
        JOIN tournament_roster_operations AS lock_operation
            ON lock_operation.tournament_id = proof_tournament_id
            AND lock_operation.roster_id = proof_roster_id
            AND lock_operation.action = 'lock'
            AND lock_operation.preflight_revision_id = proof_preflight_revision_id
            AND lock_operation.resulting_roster_revision = proof_roster_revision
        JOIN tournament_roster_operations AS preflight_operation
            ON preflight_operation.command_id = proof_preflight_revision_id
            AND preflight_operation.tournament_id = proof_tournament_id
            AND preflight_operation.roster_id = proof_roster_id
            AND preflight_operation.action = 'preflight'
        JOIN LATERAL (
            SELECT configuration.normal_pool_revision_id,
                normal_pool.revision AS normal_pool_revision
            FROM tournament_content_configurations AS configuration
            JOIN task_pool_revisions AS normal_pool
                ON normal_pool.id = configuration.normal_pool_revision_id
                AND normal_pool.kind = 'normal'
            WHERE configuration.tournament_id = proof_tournament_id
                AND configuration.state = 'published'
            ORDER BY configuration.revision DESC, configuration.id DESC
            LIMIT 1
        ) AS current_content ON TRUE
        WHERE roster.id = proof_roster_id
            AND roster.revision = proof_roster_revision
            AND tournament.id = proof_tournament_id
            AND wave.revision_id = proof_wave_revision_id
            AND (
                (proof_mode = 'wave_start' AND wave.revision = proof_wave_revision + 1
                    AND wave.state = 'active' AND wave.started_at = proof_locked_at)
                OR (terminal_authority_valid AND wave.started_at IS NULL AND wave.paused_at IS NULL
                    AND wave.closed_at IS NULL AND (
                        (proof_mode = 'pre_start_forfeit' AND wave.revision = proof_wave_revision
                            AND wave.state IN ('planned', 'ready_window_open', 'ready'))
                        OR (proof_mode = 'normal_no_show' AND wave.revision = proof_wave_revision + 1
                            AND wave.state = 'ready_window_expired' AND wave.updated_at = proof_locked_at)
                    ))
            )
            AND current_content.normal_pool_revision_id = proof_normal_pool_revision_id
            AND current_content.normal_pool_revision = proof_normal_pool_revision
            AND EXISTS (
                SELECT 1
                FROM swiss_rounds AS swiss_round
                WHERE swiss_round.id = proof_round_id
                    AND swiss_round.roster_id = proof_roster_id
                    AND swiss_round.revision = proof_round_revision
                    AND swiss_round.source_history_revision = proof_history_revision
                    AND swiss_round.lock_revision = proof_round_revision
                    AND swiss_round.locked_at = proof_locked_at
            )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof source authority is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        (
            SELECT member.participant_id
            FROM swiss_round_lock_proof_members AS member
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
            EXCEPT
            SELECT member.participant_id
            FROM wave_members AS member
            WHERE member.wave_id = proof_wave_id
                AND member.roster_id = proof_roster_id
        )
        UNION ALL
        (
            SELECT member.participant_id
            FROM wave_members AS member
            WHERE member.wave_id = proof_wave_id
                AND member.roster_id = proof_roster_id
            EXCEPT
            SELECT member.participant_id
            FROM swiss_round_lock_proof_members AS member
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
        )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof membership differs from persisted Wave membership'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        (
            SELECT proof_series.series_id
            FROM swiss_round_lock_proof_series AS proof_series
            WHERE proof_series.round_id = proof_round_id
                AND proof_series.roster_id = proof_roster_id
            EXCEPT
            SELECT membership.series_id
            FROM wave_series AS membership
            WHERE membership.wave_id = proof_wave_id
                AND membership.tournament_id = proof_tournament_id
                AND membership.roster_id = proof_roster_id
        )
        UNION ALL
        (
            SELECT membership.series_id
            FROM wave_series AS membership
            WHERE membership.wave_id = proof_wave_id
                AND membership.tournament_id = proof_tournament_id
                AND membership.roster_id = proof_roster_id
            EXCEPT
            SELECT proof_series.series_id
            FROM swiss_round_lock_proof_series AS proof_series
            WHERE proof_series.round_id = proof_round_id
                AND proof_series.roster_id = proof_roster_id
        )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof Series differ from persisted Wave membership'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM swiss_round_lock_proof_series AS proof_series
        LEFT JOIN series AS series
            ON series.id = proof_series.series_id
            AND series.tournament_id = proof_tournament_id
            AND series.roster_id = proof_roster_id
        LEFT JOIN category_revisions AS category_revision
            ON category_revision.id = proof_series.category_revision_id
            AND category_revision.series_id = proof_series.series_id
            AND category_revision.roster_id = proof_roster_id
            AND category_revision.revision = proof_series.category_revision
            AND category_revision.source_pool_revision_id = proof_normal_pool_revision_id
        LEFT JOIN assignments AS assignment
            ON assignment.id = proof_series.assignment_id
            AND assignment.series_id = proof_series.series_id
            AND assignment.roster_id = proof_roster_id
            AND assignment.revision = proof_series.assignment_revision
            AND assignment.state = 'active'
        LEFT JOIN assignment_plans AS assignment_plan
            ON assignment_plan.id = proof_series.assignment_plan_id
            AND assignment_plan.revision_id = proof_series.assignment_plan_revision_id
            AND assignment_plan.roster_id = proof_roster_id
            AND assignment_plan.state = 'committed'
            AND assignment_plan.active_branch_id = assignment.branch_id
        LEFT JOIN task_version_reservations AS reservation
            ON reservation.id = proof_series.reservation_id
            AND reservation.id = assignment.reservation_id
            AND reservation.plan_id = assignment_plan.id
            AND reservation.branch_id = assignment.branch_id
            AND reservation.state = 'committed'
            AND (
                (proof_mode = 'wave_start' AND reservation.revision = proof_series.reservation_revision + 1
                    AND reservation.disclosed_at = proof_locked_at)
                OR (terminal_authority_valid AND reservation.revision = proof_series.reservation_revision
                    AND reservation.disclosed_at IS NULL)
            )
        LEFT JOIN game_attempts AS attempt
            ON attempt.id = assignment.attempt_id
            AND attempt.series_id = proof_series.series_id
            AND attempt.roster_id = proof_roster_id
            AND ((proof_mode = 'wave_start' AND attempt.state = 'active')
                OR (terminal_authority_valid AND attempt.state IN ('planned', 'ready', 'cancelled') AND attempt.started_at IS NULL))
        LEFT JOIN game_slots AS slot
            ON slot.id = attempt.slot_id
            AND slot.series_id = proof_series.series_id
            AND slot.roster_id = proof_roster_id
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
            AND (
                series.id IS NULL
                OR series.format <> 'bo1'
                OR (proof_mode = 'wave_start' AND series.state <> 'active')
                OR (proof_mode <> 'wave_start' AND (NOT terminal_authority_valid OR series.started_at IS NOT NULL
                    OR series.state NOT IN ('planned', 'ready', 'completed', 'cancelled')))
                OR series.first_participant_id <> proof_series.first_participant_id
                OR series.second_participant_id <> proof_series.second_participant_id
                OR category_revision.id IS NULL
                OR assignment.id IS NULL
                OR assignment_plan.id IS NULL
                OR reservation.id IS NULL
                OR attempt.id IS NULL
                OR slot.id IS NULL
                OR NOT (
                    (category_revision.mode IN ('random', 'admin')
                        AND category_revision.selected_categories @> jsonb_build_array(slot.category))
                    OR (category_revision.mode = 'draft' AND assignment_plan.kind = 'exact_draft' AND EXISTS (
                        SELECT 1
                        FROM drafts AS draft
                        JOIN draft_revisions AS completed
                            ON completed.draft_id = draft.id
                            AND completed.series_id = draft.series_id
                            AND completed.roster_id = draft.roster_id
                            AND completed.id = assignment_plan.completion_draft_revision_id
                            AND completed.revision = assignment_plan.completion_draft_revision
                            AND completed.state = 'completed'
                        JOIN draft_revisions AS source
                            ON source.id = assignment_plan.source_draft_revision_id
                            AND source.draft_id = draft.id
                        WHERE draft.category_revision_id = category_revision.id
                            AND draft.series_id = proof_series.series_id
                            AND draft.roster_id = proof_roster_id
                            AND draft.format = 'bo1'
                            AND completed.selected_categories = assignment_plan.completed_categories
                            AND completed.selected_categories @> jsonb_build_array(slot.category)
                    ))
                )
                OR (SELECT COUNT(*) FROM game_slots AS current_slot
                    WHERE current_slot.series_id = proof_series.series_id
                        AND current_slot.roster_id = proof_roster_id) <> 1
                OR EXISTS (
                    SELECT 1
                    FROM game_attempts AS newer_attempt
                    WHERE newer_attempt.slot_id = attempt.slot_id
                        AND newer_attempt.series_id = proof_series.series_id
                        AND newer_attempt.roster_id = proof_roster_id
                        AND newer_attempt.attempt_number > attempt.attempt_number
                )
            )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof Series binding is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.swiss_draft_delivery_history_heads) THEN
        RAISE EXCEPTION 'cannot roll back Swiss draft cardinality while history heads exist';
    END IF;
END;
$$;

-- Restore the original category authority before removing BO1 draft support.
CREATE OR REPLACE FUNCTION public.validate_swiss_round_lock_proof() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    proof_round_id UUID;
    proof_tournament_id UUID;
    proof_roster_id UUID;
    proof_round_number SMALLINT;
    proof_round_revision BIGINT;
    proof_source_projection_revision_id UUID;
    proof_source_projection_revision BIGINT;
    proof_roster_revision BIGINT;
    proof_history_revision BIGINT;
    proof_preflight_revision_id UUID;
    proof_normal_pool_revision_id UUID;
    proof_normal_pool_revision BIGINT;
    proof_wave_id UUID;
    proof_wave_revision_id UUID;
    proof_wave_revision BIGINT;
    proof_locked_at TIMESTAMPTZ;
    proof_bye_participant_id UUID;
    proof_mode TEXT;
    proof_terminal_command_id UUID;
    terminal_authority_valid BOOLEAN;
    stored_round_number SMALLINT;
    stored_round_revision BIGINT;
    stored_lock_revision BIGINT;
    member_count INTEGER;
    series_count INTEGER;
    member_coverage_count INTEGER;
    pairing_mismatch_count INTEGER;
BEGIN
    proof_round_id := NEW.round_id;

    SELECT proof.tournament_id,
        proof.roster_id,
        proof.round_number,
        proof.round_revision,
        proof.source_projection_revision_id,
        proof.source_projection_revision,
        proof.roster_revision,
        proof.history_revision,
        proof.preflight_revision_id,
        proof.normal_pool_revision_id,
        proof.normal_pool_revision,
        proof.wave_id,
        proof.wave_revision_id,
        proof.wave_revision,
        proof.locked_at,
        proof.bye_participant_id,
        proof.proof_mode,
        proof.terminal_command_id
    INTO proof_tournament_id,
        proof_roster_id,
        proof_round_number,
        proof_round_revision,
        proof_source_projection_revision_id,
        proof_source_projection_revision,
        proof_roster_revision,
        proof_history_revision,
        proof_preflight_revision_id,
        proof_normal_pool_revision_id,
        proof_normal_pool_revision,
        proof_wave_id,
        proof_wave_revision_id,
        proof_wave_revision,
        proof_locked_at,
        proof_bye_participant_id,
        proof_mode,
        proof_terminal_command_id
    FROM swiss_round_lock_proofs AS proof
    WHERE proof.round_id = proof_round_id
    FOR KEY SHARE;

    IF proof_roster_id IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT swiss_round.round_number,
        swiss_round.revision,
        swiss_round.lock_revision
    INTO stored_round_number,
        stored_round_revision,
        stored_lock_revision
    FROM swiss_rounds AS swiss_round
    WHERE swiss_round.id = proof_round_id
        AND swiss_round.roster_id = proof_roster_id
    FOR KEY SHARE;

    IF stored_round_number IS NULL
        OR stored_round_number <> proof_round_number
        OR stored_round_revision <> proof_round_revision
        OR stored_lock_revision <> proof_round_revision THEN
        RAISE EXCEPTION 'Swiss round lock proof no longer matches locked round'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO member_count
    FROM swiss_round_lock_proof_members AS member
    WHERE member.round_id = proof_round_id
        AND member.roster_id = proof_roster_id;

    SELECT COUNT(*)
    INTO series_count
    FROM swiss_round_lock_proof_series AS proof_series
    WHERE proof_series.round_id = proof_round_id
        AND proof_series.roster_id = proof_roster_id;

    IF member_count < 4
        OR member_count > 16
        OR (proof_bye_participant_id IS NULL AND member_count % 2 <> 0)
        OR (proof_bye_participant_id IS NOT NULL AND member_count % 2 <> 1)
        OR series_count <> (member_count - CASE WHEN proof_bye_participant_id IS NULL THEN 0 ELSE 1 END) / 2 THEN
        RAISE EXCEPTION 'Swiss round lock proof has incomplete membership or series coverage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO member_coverage_count
    FROM (
        SELECT proof_series.first_participant_id AS participant_id
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
        UNION ALL
        SELECT proof_series.second_participant_id
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
        UNION ALL
        SELECT proof_bye_participant_id
        WHERE proof_bye_participant_id IS NOT NULL
    ) AS covered;

    IF member_coverage_count <> member_count
        OR EXISTS (
            SELECT 1
            FROM swiss_round_lock_proof_members AS member
            LEFT JOIN (
                SELECT proof_series.first_participant_id AS participant_id
                FROM swiss_round_lock_proof_series AS proof_series
                WHERE proof_series.round_id = proof_round_id
                    AND proof_series.roster_id = proof_roster_id
                UNION ALL
                SELECT proof_series.second_participant_id
                FROM swiss_round_lock_proof_series AS proof_series
                WHERE proof_series.round_id = proof_round_id
                    AND proof_series.roster_id = proof_roster_id
                UNION ALL
                SELECT proof_bye_participant_id
                WHERE proof_bye_participant_id IS NOT NULL
            ) AS covered ON covered.participant_id = member.participant_id
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
            GROUP BY member.participant_id
            HAVING COUNT(covered.participant_id) <> 1
        ) THEN
        RAISE EXCEPTION 'Swiss round lock proof membership does not exactly match series seats'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO pairing_mismatch_count
    FROM swiss_round_lock_proof_series AS proof_series
    LEFT JOIN swiss_pairing_members AS first_member
        ON first_member.pairing_id = proof_series.pairing_id
        AND first_member.round_id = proof_series.round_id
        AND first_member.roster_id = proof_series.roster_id
        AND first_member.seat = 1
    LEFT JOIN swiss_pairing_members AS second_member
        ON second_member.pairing_id = proof_series.pairing_id
        AND second_member.round_id = proof_series.round_id
        AND second_member.roster_id = proof_series.roster_id
        AND second_member.seat = 2
    WHERE proof_series.round_id = proof_round_id
        AND proof_series.roster_id = proof_roster_id
        AND (
            first_member.participant_id IS DISTINCT FROM proof_series.first_participant_id
            OR second_member.participant_id IS DISTINCT FROM proof_series.second_participant_id
        );

    IF pairing_mismatch_count <> 0 THEN
        RAISE EXCEPTION 'Swiss round lock proof pairing differs from persisted pairing'
            USING ERRCODE = 'check_violation';
    END IF;

    terminal_authority_valid := FALSE;
    IF proof_mode = 'pre_start_forfeit' THEN
        SELECT EXISTS (
            SELECT 1 FROM operator_forfeit_commits AS terminal
            JOIN projection_revisions AS publication ON publication.id = terminal.projection_evidence_id
                AND publication.tournament_id = proof_tournament_id AND publication.roster_id = proof_roster_id
                AND publication.previous_revision_id = proof_source_projection_revision_id
                AND publication.state IN ('published', 'superseded')
            JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id AND member.series_id = terminal.series_id
            WHERE terminal.command_id = proof_terminal_command_id
                AND terminal.tournament_id = proof_tournament_id AND terminal.roster_id = proof_roster_id
                AND terminal.source_projection_revision_id = proof_source_projection_revision_id
                AND terminal.source_projection_revision = proof_source_projection_revision
                AND terminal.resolved_at = proof_locked_at
        ) INTO terminal_authority_valid;
    ELSIF proof_mode = 'normal_no_show' THEN
        SELECT EXISTS (
            SELECT 1 FROM normal_no_show_commits AS terminal
            JOIN official_result_revisions AS result ON result.id = terminal.series_result_revision_id
                AND result.source_projection_revision_id = proof_source_projection_revision_id
                AND result.source_projection_revision = proof_source_projection_revision
            JOIN projection_revisions AS publication ON publication.id = terminal.projection_evidence_id
                AND publication.tournament_id = proof_tournament_id AND publication.roster_id = proof_roster_id
                AND publication.previous_revision_id = proof_source_projection_revision_id
                AND publication.state IN ('published', 'superseded')
            JOIN ready_windows AS ready_window ON ready_window.id = terminal.ready_window_id
                AND ready_window.wave_id = proof_wave_id AND ready_window.revision_id = terminal.ready_window_revision_id
                AND ready_window.state = 'expired'
            JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id AND member.series_id = terminal.series_id
            WHERE terminal.command_id = proof_terminal_command_id
                AND terminal.tournament_id = proof_tournament_id AND terminal.roster_id = proof_roster_id
                AND terminal.wave_id = proof_wave_id AND terminal.expected_wave_revision = proof_wave_revision
                AND terminal.resolved_at = proof_locked_at
        ) INTO terminal_authority_valid;
    END IF;

    -- All sources below are intentionally normalized tables. The proof may
    -- not stand in for a stale projection, preflight, pool, Wave, category,
    -- assignment plan, or reservation. These tables are introduced by later
    -- fresh-schema migrations, so this deferred trigger is the integrity
    -- boundary rather than forward foreign keys in migration 000004.
    IF NOT EXISTS (
        SELECT 1
        FROM rosters AS roster
        JOIN tournaments AS tournament ON tournament.id = roster.tournament_id
        JOIN swiss_wave_links AS swiss_link
            ON swiss_link.round_id = proof_round_id
            AND swiss_link.roster_id = proof_roster_id
            AND swiss_link.tournament_id = proof_tournament_id
            AND swiss_link.wave_id = proof_wave_id
        JOIN waves AS wave
            ON wave.id = swiss_link.wave_id
            AND wave.tournament_id = proof_tournament_id
            AND wave.roster_id = proof_roster_id
        JOIN projection_revisions AS source_projection
            ON source_projection.id = proof_source_projection_revision_id
            AND source_projection.tournament_id = proof_tournament_id
            AND source_projection.roster_id = proof_roster_id
            AND source_projection.revision_number = proof_source_projection_revision
            AND (source_projection.state = 'published'
                OR (terminal_authority_valid AND source_projection.state = 'superseded'))
        JOIN tournament_roster_operations AS lock_operation
            ON lock_operation.tournament_id = proof_tournament_id
            AND lock_operation.roster_id = proof_roster_id
            AND lock_operation.action = 'lock'
            AND lock_operation.preflight_revision_id = proof_preflight_revision_id
            AND lock_operation.resulting_roster_revision = proof_roster_revision
        JOIN tournament_roster_operations AS preflight_operation
            ON preflight_operation.command_id = proof_preflight_revision_id
            AND preflight_operation.tournament_id = proof_tournament_id
            AND preflight_operation.roster_id = proof_roster_id
            AND preflight_operation.action = 'preflight'
        JOIN LATERAL (
            SELECT configuration.normal_pool_revision_id,
                normal_pool.revision AS normal_pool_revision
            FROM tournament_content_configurations AS configuration
            JOIN task_pool_revisions AS normal_pool
                ON normal_pool.id = configuration.normal_pool_revision_id
                AND normal_pool.kind = 'normal'
            WHERE configuration.tournament_id = proof_tournament_id
                AND configuration.state = 'published'
            ORDER BY configuration.revision DESC, configuration.id DESC
            LIMIT 1
        ) AS current_content ON TRUE
        WHERE roster.id = proof_roster_id
            AND roster.revision = proof_roster_revision
            AND tournament.id = proof_tournament_id
            AND wave.revision_id = proof_wave_revision_id
            AND (
                (proof_mode = 'wave_start' AND wave.revision = proof_wave_revision + 1
                    AND wave.state = 'active' AND wave.started_at = proof_locked_at)
                OR (terminal_authority_valid AND wave.started_at IS NULL AND wave.paused_at IS NULL
                    AND wave.closed_at IS NULL AND (
                        (proof_mode = 'pre_start_forfeit' AND wave.revision = proof_wave_revision
                            AND wave.state IN ('planned', 'ready_window_open', 'ready'))
                        OR (proof_mode = 'normal_no_show' AND wave.revision = proof_wave_revision + 1
                            AND wave.state = 'ready_window_expired' AND wave.updated_at = proof_locked_at)
                    ))
            )
            AND current_content.normal_pool_revision_id = proof_normal_pool_revision_id
            AND current_content.normal_pool_revision = proof_normal_pool_revision
            AND EXISTS (
                SELECT 1
                FROM swiss_rounds AS swiss_round
                WHERE swiss_round.id = proof_round_id
                    AND swiss_round.roster_id = proof_roster_id
                    AND swiss_round.revision = proof_round_revision
                    AND swiss_round.source_history_revision = proof_history_revision
                    AND swiss_round.lock_revision = proof_round_revision
                    AND swiss_round.locked_at = proof_locked_at
            )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof source authority is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        (
            SELECT member.participant_id
            FROM swiss_round_lock_proof_members AS member
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
            EXCEPT
            SELECT member.participant_id
            FROM wave_members AS member
            WHERE member.wave_id = proof_wave_id
                AND member.roster_id = proof_roster_id
        )
        UNION ALL
        (
            SELECT member.participant_id
            FROM wave_members AS member
            WHERE member.wave_id = proof_wave_id
                AND member.roster_id = proof_roster_id
            EXCEPT
            SELECT member.participant_id
            FROM swiss_round_lock_proof_members AS member
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
        )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof membership differs from persisted Wave membership'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        (
            SELECT proof_series.series_id
            FROM swiss_round_lock_proof_series AS proof_series
            WHERE proof_series.round_id = proof_round_id
                AND proof_series.roster_id = proof_roster_id
            EXCEPT
            SELECT membership.series_id
            FROM wave_series AS membership
            WHERE membership.wave_id = proof_wave_id
                AND membership.tournament_id = proof_tournament_id
                AND membership.roster_id = proof_roster_id
        )
        UNION ALL
        (
            SELECT membership.series_id
            FROM wave_series AS membership
            WHERE membership.wave_id = proof_wave_id
                AND membership.tournament_id = proof_tournament_id
                AND membership.roster_id = proof_roster_id
            EXCEPT
            SELECT proof_series.series_id
            FROM swiss_round_lock_proof_series AS proof_series
            WHERE proof_series.round_id = proof_round_id
                AND proof_series.roster_id = proof_roster_id
        )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof Series differ from persisted Wave membership'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM swiss_round_lock_proof_series AS proof_series
        LEFT JOIN series AS series
            ON series.id = proof_series.series_id
            AND series.tournament_id = proof_tournament_id
            AND series.roster_id = proof_roster_id
        LEFT JOIN category_revisions AS category_revision
            ON category_revision.id = proof_series.category_revision_id
            AND category_revision.series_id = proof_series.series_id
            AND category_revision.roster_id = proof_roster_id
            AND category_revision.revision = proof_series.category_revision
            AND category_revision.source_pool_revision_id = proof_normal_pool_revision_id
        LEFT JOIN assignments AS assignment
            ON assignment.id = proof_series.assignment_id
            AND assignment.series_id = proof_series.series_id
            AND assignment.roster_id = proof_roster_id
            AND assignment.revision = proof_series.assignment_revision
            AND assignment.state = 'active'
        LEFT JOIN assignment_plans AS assignment_plan
            ON assignment_plan.id = proof_series.assignment_plan_id
            AND assignment_plan.revision_id = proof_series.assignment_plan_revision_id
            AND assignment_plan.roster_id = proof_roster_id
            AND assignment_plan.state = 'committed'
            AND assignment_plan.active_branch_id = assignment.branch_id
        LEFT JOIN task_version_reservations AS reservation
            ON reservation.id = proof_series.reservation_id
            AND reservation.id = assignment.reservation_id
            AND reservation.plan_id = assignment_plan.id
            AND reservation.branch_id = assignment.branch_id
            AND reservation.state = 'committed'
            AND (
                (proof_mode = 'wave_start' AND reservation.revision = proof_series.reservation_revision + 1
                    AND reservation.disclosed_at = proof_locked_at)
                OR (terminal_authority_valid AND reservation.revision = proof_series.reservation_revision
                    AND reservation.disclosed_at IS NULL)
            )
        LEFT JOIN game_attempts AS attempt
            ON attempt.id = assignment.attempt_id
            AND attempt.series_id = proof_series.series_id
            AND attempt.roster_id = proof_roster_id
            AND ((proof_mode = 'wave_start' AND attempt.state = 'active')
                OR (terminal_authority_valid AND attempt.state IN ('planned', 'ready', 'cancelled') AND attempt.started_at IS NULL))
        LEFT JOIN game_slots AS slot
            ON slot.id = attempt.slot_id
            AND slot.series_id = proof_series.series_id
            AND slot.roster_id = proof_roster_id
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
            AND (
                series.id IS NULL
                OR series.format <> 'bo1'
                OR (proof_mode = 'wave_start' AND series.state <> 'active')
                OR (proof_mode <> 'wave_start' AND (NOT terminal_authority_valid OR series.started_at IS NOT NULL
                    OR series.state NOT IN ('planned', 'ready', 'completed', 'cancelled')))
                OR series.first_participant_id <> proof_series.first_participant_id
                OR series.second_participant_id <> proof_series.second_participant_id
                OR category_revision.id IS NULL
                OR assignment.id IS NULL
                OR assignment_plan.id IS NULL
                OR reservation.id IS NULL
                OR attempt.id IS NULL
                OR slot.id IS NULL
                OR NOT category_revision.selected_categories @> jsonb_build_array(slot.category)
                OR (SELECT COUNT(*) FROM game_slots AS current_slot
                    WHERE current_slot.series_id = proof_series.series_id
                        AND current_slot.roster_id = proof_roster_id) <> 1
                OR EXISTS (
                    SELECT 1
                    FROM game_attempts AS newer_attempt
                    WHERE newer_attempt.slot_id = attempt.slot_id
                        AND newer_attempt.series_id = proof_series.series_id
                        AND newer_attempt.roster_id = proof_roster_id
                        AND newer_attempt.attempt_number > attempt.attempt_number
                )
            )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof Series binding is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

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

CREATE OR REPLACE FUNCTION public.exact_draft_assignment_branch_guard() RETURNS trigger
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

CREATE OR REPLACE FUNCTION public.exact_draft_assignment_child_source_guard() RETURNS trigger
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

CREATE OR REPLACE FUNCTION public.assignment_plan_guard() RETURNS trigger
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

DROP TRIGGER IF EXISTS swiss_draft_delivery_history_head_guard ON public.task_delivery_receipts;
DROP TABLE IF EXISTS public.swiss_draft_delivery_history_heads;
DROP FUNCTION IF EXISTS public.swiss_draft_delivery_history_head_guard();
DROP FUNCTION IF EXISTS public.swiss_draft_history_scope_guard();
DROP FUNCTION IF EXISTS public.exact_draft_required_category_count_for_revision(uuid);
DROP FUNCTION IF EXISTS public.exact_draft_required_category_count(uuid);

ALTER TABLE public.exact_draft_assignment_branches
    DROP CONSTRAINT IF EXISTS exact_draft_assignment_branches_category_check,
    ADD CONSTRAINT exact_draft_assignment_branches_category_check CHECK (
        jsonb_typeof(category_sequence) = 'array'
        AND jsonb_array_length(category_sequence) = 3
    );

ALTER TABLE public.assignment_plans
    DROP CONSTRAINT IF EXISTS assignment_plans_state_evidence_check,
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
            AND jsonb_array_length(completed_categories) = 3
            AND committed_at IS NOT NULL AND superseded_at IS NULL AND supersession_reason IS NULL)
        OR (state = 'superseded' AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL AND supersession_reason = btrim(supersession_reason)
            AND supersession_reason <> ''))
    );

-- +goose StatementEnd
