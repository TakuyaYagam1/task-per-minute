-- +goose Up
-- +goose StatementBegin

-- POST-MVP-062 makes the normal assignment reserve policy an immutable part
-- of the published tournament configuration and every materialized plan and
-- assignment. Existing records were created with the historical two-reserve
-- policy. New configurations default to no reserves and the planner copies
-- the selected policy into its immutable execution evidence.
ALTER TABLE public.tournament_content_configurations
    ADD COLUMN reserve_count smallint NOT NULL DEFAULT 2,
    ADD CONSTRAINT tournament_content_configurations_reserve_count_check
        CHECK (reserve_count BETWEEN 0 AND 2);

ALTER TABLE public.tournament_content_configurations
    ALTER COLUMN reserve_count SET DEFAULT 0;

ALTER TABLE public.assignment_plans
    ADD COLUMN reserve_count smallint NOT NULL DEFAULT 2,
    ADD CONSTRAINT assignment_plans_reserve_count_check
        CHECK (reserve_count BETWEEN 0 AND 2);

ALTER TABLE public.assignments
    ADD COLUMN reserve_count smallint NOT NULL DEFAULT 2,
    ADD CONSTRAINT assignments_reserve_count_check
        CHECK (reserve_count BETWEEN 0 AND 2);

-- The historical assignment schema encoded a three-edge branch (one primary
-- plus two reserves) in table constraints and trigger predicates.  Keep the
-- columns valid for the immutable 0..2 policy, but move chain cardinality to
-- the plan-level guards below so an operator append can extend the actual
-- chain without changing the published policy.
CREATE OR REPLACE FUNCTION public.assignment_plan_reserve_count(input_plan_id uuid)
RETURNS integer
LANGUAGE sql
STABLE
AS $$
    SELECT COALESCE((SELECT reserve_count::integer FROM public.assignment_plans WHERE id = input_plan_id), 2);
$$;

ALTER TABLE public.assignment_plan_edges
    DROP CONSTRAINT IF EXISTS assignment_plan_edges_source_check;

ALTER TABLE public.replay_reserve_exhaustions
    DROP CONSTRAINT IF EXISTS replay_reserve_exhaustions_position_check,
    ADD CONSTRAINT replay_reserve_exhaustions_position_check CHECK (reserve_position >= 1);

ALTER TABLE public.operator_replay_reserves
    DROP CONSTRAINT IF EXISTS operator_replay_reserves_position_check,
    ADD CONSTRAINT operator_replay_reserves_position_check CHECK (reserve_position >= 2);

ALTER TABLE public.replay_replacements
    DROP CONSTRAINT IF EXISTS replay_replacements_position_check,
    ADD CONSTRAINT replay_replacements_position_check CHECK (reserve_position >= 2);

CREATE OR REPLACE FUNCTION public.assignment_edge_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    plan_state VARCHAR(16);
    branch_state VARCHAR(16);
    reserve_count INTEGER;
    existing_max_position INTEGER;
    locked_task_id UUID;
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'assignment selected edges are immutable proof evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT plan.state,
        branch.state,
        COALESCE(plan.reserve_count::integer, 2),
        COALESCE(MAX(edge.position)::integer, 0)
    INTO plan_state, branch_state, reserve_count, existing_max_position
    FROM assignment_plans AS plan
    JOIN assignment_branches AS branch
        ON branch.plan_id = plan.id
        AND branch.id = NEW.branch_id
    LEFT JOIN assignment_plan_edges AS edge
        ON edge.plan_id = plan.id
        AND edge.branch_id = branch.id
    WHERE plan.id = NEW.plan_id
    GROUP BY plan.state, branch.state, plan.reserve_count;

    IF NEW.operator_reserve_command_id IS NULL THEN
        IF plan_state <> 'planned'
            OR branch_state <> 'reserved'
            OR NEW.position < 1
            OR NEW.position > reserve_count + 1 THEN
            RAISE EXCEPTION 'planned edges require a reserved branch within its configured chain length'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF plan_state <> 'committed'
        OR branch_state <> 'active'
        OR existing_max_position < reserve_count + 1
        OR NEW.position <> existing_max_position + 1 THEN
        RAISE EXCEPTION 'operator reserve edges must append exactly one edge to the active chain'
            USING ERRCODE = 'check_violation';
    END IF;

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

DROP TRIGGER IF EXISTS assignment_edge_guard ON public.assignment_plan_edges;
CREATE TRIGGER assignment_edge_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.assignment_plan_edges
FOR EACH ROW EXECUTE FUNCTION public.assignment_edge_guard();

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
    required_categories INTEGER;
    expected_reservations INTEGER;
    committed_reservations INTEGER;
    reusable_reservations INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT kind, state, source_draft_revision_id, roster_id,
            COALESCE(reserve_count::integer, 2)
        INTO plan_kind, plan_state, source_revision_id, plan_roster_id,
            expected_reservations
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
                RAISE EXCEPTION 'draft-free exact assignment branches require canonical categories'
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

            SELECT public.exact_draft_required_category_count(draft.id)
            INTO required_categories
            FROM drafts AS draft
            WHERE draft.id = NEW.draft_id;

            IF NEW.exact_draft_branch_id IS NULL
                OR NEW.exact_draft_position NOT BETWEEN 1 AND COALESCE(required_categories, 0)
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

    SELECT COALESCE(reserve_count::integer, 2) + 1
    INTO expected_reservations
    FROM assignment_plans
    WHERE id = NEW.plan_id;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*)
        INTO committed_reservations
        FROM task_version_reservations
        WHERE branch_id = NEW.id AND state = 'committed';

        IF committed_reservations <> expected_reservations THEN
            RAISE EXCEPTION 'active branch requires its configured committed chain'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'released' AND OLD.state <> 'released' THEN
        SELECT COUNT(*)
        INTO reusable_reservations
        FROM task_version_reservations
        WHERE branch_id = NEW.id AND state = 'released' AND disclosed_at IS NULL;

        IF reusable_reservations <> expected_reservations THEN
            RAISE EXCEPTION 'released branch must retain its configured undisclosed chain'
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
    expected_reservations INTEGER;
    child_count INTEGER;
    active_child_count INTEGER;
    released_child_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT plan.kind, plan.state, plan.source_draft_revision_id,
            public.exact_draft_required_category_count_for_revision(plan.source_draft_revision_id),
            COALESCE(plan.reserve_count::integer, 2) + 1
        INTO plan_kind, plan_state, source_draft_revision_id, required_categories,
            expected_reservations
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

    SELECT public.exact_draft_required_category_count(draft.id),
        COALESCE(plan.reserve_count::integer, 2) + 1
    INTO required_categories, expected_reservations
    FROM drafts AS draft
    JOIN assignment_plans AS plan ON plan.id = NEW.plan_id
    WHERE draft.id = NEW.draft_id;

    SELECT COUNT(*)
    INTO child_count
    FROM assignment_branches
    WHERE plan_id = NEW.plan_id AND exact_draft_branch_id = NEW.id;

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

        IF active_child_count <> required_categories * expected_reservations THEN
            RAISE EXCEPTION 'active exact draft branch requires every configured child reservation committed'
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

        IF released_child_count <> required_categories * expected_reservations THEN
            RAISE EXCEPTION 'released exact draft branch must release every configured child reservation'
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

CREATE OR REPLACE FUNCTION public.assignment_plan_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    parent_kind VARCHAR(16);
    parent_tournament_id UUID;
    parent_roster_id UUID;
    parent_roster_revision BIGINT;
    parent_pool_revision_id UUID;
    expected_reservations INTEGER;
    branch_count INTEGER;
    active_count INTEGER;
    incomplete_count INTEGER;
    required_categories INTEGER;
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
            SELECT kind, tournament_id, roster_id, source_roster_revision, source_pool_revision_id
            INTO parent_kind, parent_tournament_id, parent_roster_id, parent_roster_revision, parent_pool_revision_id
            FROM assignment_plans WHERE id = NEW.parent_plan_id;
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

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
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
        OR NEW.reserve_count IS DISTINCT FROM OLD.reserve_count
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

    expected_reservations := COALESCE(NEW.reserve_count::integer, 2) + 1;

    IF NEW.state = 'committed' AND OLD.state <> 'committed'
        AND NEW.kind IN ('exact', 'exact_normal', 'exact_golden') THEN
        SELECT COUNT(*) INTO branch_count
        FROM assignment_branches WHERE plan_id = NEW.id;
        SELECT COUNT(*) INTO active_count
        FROM assignment_branches
        WHERE plan_id = NEW.id AND id = NEW.active_branch_id AND state = 'active';
        SELECT COUNT(*) INTO incomplete_count
        FROM assignment_branches AS branch
        WHERE branch.plan_id = NEW.id
            AND (
                (branch.id = NEW.active_branch_id AND branch.state <> 'active')
                OR (branch.id <> NEW.active_branch_id AND branch.state <> 'released')
                OR (SELECT COUNT(*) FROM assignment_plan_edges AS edge WHERE edge.branch_id = branch.id) <> expected_reservations
                OR (SELECT COUNT(*) FROM task_version_reservations AS reservation WHERE reservation.branch_id = branch.id) <> expected_reservations
            );
        IF branch_count <> NEW.reachable_branch_count OR active_count <> 1 OR incomplete_count <> 0 THEN
            RAISE EXCEPTION 'exact plan must cover every branch with its configured assignment chain'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'committed' AND OLD.state <> 'committed' AND NEW.kind = 'exact_draft' THEN
        SELECT public.exact_draft_required_category_count_for_revision(NEW.source_draft_revision_id)
        INTO required_categories;
        SELECT COUNT(*) INTO draft_group_count
        FROM exact_draft_assignment_branches WHERE plan_id = NEW.id;
        SELECT COUNT(*) INTO active_draft_group_count
        FROM exact_draft_assignment_branches
        WHERE plan_id = NEW.id AND id = NEW.active_draft_branch_id AND state = 'active';
        SELECT child.id INTO expected_active_child_id
        FROM assignment_branches AS child
        WHERE child.plan_id = NEW.id
            AND child.exact_draft_branch_id = NEW.active_draft_branch_id
            AND child.exact_draft_position = 1
            AND child.state = 'active';
        SELECT category_sequence INTO active_category_sequence
        FROM exact_draft_assignment_branches
        WHERE id = NEW.active_draft_branch_id AND plan_id = NEW.id AND state = 'active';
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
        SELECT COUNT(*) INTO active_child_count
        FROM assignment_branches AS child
        JOIN task_version_reservations AS reservation
            ON reservation.branch_id = child.id AND reservation.plan_id = child.plan_id
            AND reservation.state = 'committed'
        WHERE child.plan_id = NEW.id AND child.exact_draft_branch_id = NEW.active_draft_branch_id
            AND child.state = 'active';
        SELECT COUNT(*) INTO released_child_count
        FROM assignment_branches AS child
        JOIN task_version_reservations AS reservation
            ON reservation.branch_id = child.id AND reservation.plan_id = child.plan_id
            AND reservation.state = 'released' AND reservation.disclosed_at IS NULL
        WHERE child.plan_id = NEW.id AND child.exact_draft_branch_id <> NEW.active_draft_branch_id
            AND child.state = 'released';
        SELECT COUNT(*) INTO branch_count
        FROM assignment_branches WHERE plan_id = NEW.id;
        SELECT COUNT(*) INTO child_source_count
        FROM exact_draft_assignment_child_sources
        WHERE plan_id = NEW.id;
        SELECT COUNT(*) INTO invalid_child_source_count
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
            OR active_child_count IS DISTINCT FROM required_categories * expected_reservations
            OR released_child_count IS DISTINCT FROM (draft_group_count - 1) * required_categories * expected_reservations
            OR branch_count <> draft_group_count * required_categories
            OR child_source_count <> branch_count
            OR invalid_child_source_count <> 0 THEN
            RAISE EXCEPTION 'exact draft plan must commit one full configured category chain and retain canonical child sources'
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

CREATE OR REPLACE FUNCTION public.exact_normal_assignment_plan_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    expected_reservations INTEGER;
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
        expected_reservations := COALESCE(NEW.reserve_count::integer, 2) + 1;
        SELECT COUNT(*) INTO branch_count FROM assignment_branches WHERE plan_id = NEW.id;
        SELECT COUNT(*) INTO active_count
        FROM assignment_branches
        WHERE plan_id = NEW.id AND id = NEW.active_branch_id AND state = 'active'
            AND draft_id IS NULL AND draft_revision_id IS NULL
            AND exact_draft_branch_id IS NULL AND exact_draft_position IS NULL
            AND jsonb_typeof(category_sequence) = 'array'
            AND jsonb_array_length(category_sequence) = 1;
        SELECT COUNT(*) INTO edge_count
        FROM assignment_plan_edges WHERE plan_id = NEW.id AND branch_id = NEW.active_branch_id;
        SELECT COUNT(*) INTO reservation_count
        FROM task_version_reservations
        WHERE plan_id = NEW.id AND branch_id = NEW.active_branch_id AND state = 'committed';
        SELECT COUNT(*) INTO snapshot_count
        FROM task_version_reservations AS reservation
        JOIN task_snapshots AS snapshot ON snapshot.reservation_id = reservation.id
        WHERE reservation.plan_id = NEW.id AND reservation.branch_id = NEW.active_branch_id;
        SELECT COUNT(*) INTO source_count
        FROM exact_normal_assignment_sources WHERE plan_id = NEW.id;
        IF branch_count <> 1 OR active_count <> 1 OR edge_count <> expected_reservations
            OR reservation_count <> expected_reservations
            OR snapshot_count <> expected_reservations OR source_count <> 1 THEN
            RAISE EXCEPTION 'exact normal plan requires one complete configured committed branch'
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
            OR series_revision <> NEW.resulting_series_revision
            OR NOT EXISTS (
                SELECT 1
                FROM assignments AS assignment
                JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
                WHERE assignment.id = NEW.assignment_id
                    AND NEW.reserve_position = COALESCE(plan.reserve_count::integer, 2) + 1
            ) THEN
            RAISE EXCEPTION 'replay exhaustion did not commit its paused Series at the active chain tail'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF TG_TABLE_NAME = 'operator_replay_reserves' THEN
        SELECT COUNT(*)
        INTO target_count
        FROM assignment_plan_edges AS edge
        JOIN task_version_reservations AS reservation
            ON reservation.edge_id = edge.id AND reservation.id = NEW.reservation_id
        JOIN task_snapshots AS snapshot
            ON snapshot.reservation_id = reservation.id AND snapshot.id = NEW.proposed_snapshot_id
        JOIN assignments AS assignment ON assignment.id = NEW.assignment_id
        JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
        WHERE edge.id = NEW.edge_id
            AND edge.operator_reserve_command_id = NEW.command_id
            AND edge.position = NEW.reserve_position
            AND edge.task_id = NEW.proposed_task_id
            AND edge.task_version = NEW.proposed_version
            AND reservation.state = 'committed'
            AND NEW.reserve_position > COALESCE(plan.reserve_count::integer, 2) + 1;

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
            ON membership.wave_id = wave.id AND membership.series_id = NEW.series_id
        JOIN game_attempts AS game_attempt
            ON game_attempt.id = NEW.replacement_game_id AND game_attempt.series_id = NEW.series_id
        JOIN assignments AS assignment
            ON assignment.id = NEW.replacement_assignment_attempt_id AND assignment.attempt_id = game_attempt.id
        JOIN task_version_reservations AS reservation
            ON reservation.id = assignment.reservation_id AND reservation.state = 'committed'
        JOIN assignment_plan_edges AS edge
            ON edge.id = reservation.edge_id
            AND edge.plan_id = assignment.plan_id
            AND edge.branch_id = assignment.branch_id
            AND edge.task_id = assignment.task_id
            AND edge.task_version = assignment.task_version
        JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
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
                (edge.operator_reserve_command_id IS NULL
                    AND edge.position BETWEEN 1 AND COALESCE(plan.reserve_count::integer, 2) + 1)
                OR (edge.operator_reserve_command_id IS NOT NULL
                    AND edge.position > COALESCE(plan.reserve_count::integer, 2) + 1
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
                    ))
            )
            AND (
                SELECT COUNT(*)
                FROM wave_members AS member
                JOIN wave_readiness AS readiness
                    ON readiness.wave_id = member.wave_id AND readiness.participant_id = member.participant_id
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

CREATE OR REPLACE FUNCTION public.validate_golden_exact_plan_snapshot() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    expected_chain_length INTEGER;
    incomplete_group_count INTEGER;
    candidate_count INTEGER;
    expected_group_count INTEGER;
    observed_group_count INTEGER;
    invalid_group_count INTEGER;
    missing_edge_reservation_count INTEGER;
    missing_member_reservation_count INTEGER;
BEGIN
    SELECT COALESCE((
        SELECT plan.reserve_count::integer + 1
        FROM assignment_plans AS plan
        WHERE plan.id = NEW.plan_id
            AND plan.tournament_id = NEW.tournament_id
            AND plan.roster_id = NEW.roster_id
            AND plan.kind = 'exact_golden'
    ), 3)
    INTO expected_chain_length;

    IF expected_chain_length IS NULL OR expected_chain_length NOT BETWEEN 1 AND 3 THEN
        RAISE EXCEPTION 'Golden exact plan snapshot has an invalid reserve count'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO incomplete_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    WHERE plan_group.plan_id = NEW.plan_id
        AND (
            (SELECT COUNT(*)
             FROM golden_exact_plan_snapshot_members AS member
             WHERE member.plan_id = plan_group.plan_id
                 AND member.group_revision_id = plan_group.group_revision_id) < 2
            OR
            (SELECT COUNT(*)
             FROM golden_exact_plan_snapshot_edges AS edge
             WHERE edge.plan_id = plan_group.plan_id
                 AND edge.group_revision_id = plan_group.group_revision_id) <> expected_chain_length
        );

    SELECT COUNT(*)
    INTO candidate_count
    FROM golden_exact_plan_snapshot_candidates AS candidate
    WHERE candidate.plan_id = NEW.plan_id;

    SELECT COUNT(*)
    INTO expected_group_count
    FROM golden_group_revisions AS group_revision
    WHERE group_revision.tournament_id = NEW.tournament_id
        AND group_revision.roster_id = NEW.roster_id
        AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
        AND group_revision.source_projection_revision = NEW.source_projection_revision;

    SELECT COUNT(*)
    INTO observed_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    WHERE plan_group.plan_id = NEW.plan_id
        AND plan_group.tournament_id = NEW.tournament_id
        AND plan_group.roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO invalid_group_count
    FROM golden_exact_plan_snapshot_groups AS plan_group
    INNER JOIN golden_group_revisions AS group_revision
        ON group_revision.revision_id = plan_group.group_revision_id
        AND group_revision.tournament_id = plan_group.tournament_id
        AND group_revision.roster_id = plan_group.roster_id
    INNER JOIN tournament_stage_tie_groups AS tie_group
        ON tie_group.command_id = group_revision.stage_progression_command_id
        AND tie_group.tournament_id = group_revision.tournament_id
        AND tie_group.roster_id = group_revision.roster_id
        AND tie_group.group_id = group_revision.group_id
        AND tie_group.group_revision_id = group_revision.revision_id
    WHERE plan_group.plan_id = NEW.plan_id
        AND (
            plan_group.source_projection_revision_id <> NEW.source_projection_revision_id
            OR plan_group.source_projection_revision <> NEW.source_projection_revision
            OR group_revision.source_projection_revision_id <> NEW.source_projection_revision_id
            OR group_revision.source_projection_revision <> NEW.source_projection_revision
            OR plan_group.position_from <> tie_group.position_from
            OR plan_group.position_to <> tie_group.position_to
            OR plan_group.definition_digest IS DISTINCT FROM group_revision.definition_digest
            OR (
                SELECT COUNT(*)
                FROM golden_exact_plan_snapshot_members AS member
                WHERE member.plan_id = plan_group.plan_id
                    AND member.group_revision_id = plan_group.group_revision_id
            ) <> plan_group.position_to - plan_group.position_from + 1
            OR EXISTS (
                SELECT 1
                FROM generate_series(
                    plan_group.position_from::integer,
                    plan_group.position_to::integer
                ) AS expected(position)
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM golden_exact_plan_snapshot_members AS member
                    WHERE member.plan_id = plan_group.plan_id
                        AND member.group_revision_id = plan_group.group_revision_id
                        AND member.position = expected.position
                )
            )
            OR EXISTS (
                SELECT 1
                FROM golden_exact_plan_snapshot_members AS member
                FULL OUTER JOIN tournament_stage_tie_group_members AS source_member
                    ON source_member.command_id = tie_group.command_id
                    AND source_member.tournament_id = tie_group.tournament_id
                    AND source_member.group_id = tie_group.group_id
                    AND source_member.participant_id = member.participant_id
                    AND source_member.standing_position = member.position
                WHERE member.plan_id = plan_group.plan_id
                    AND member.group_revision_id = plan_group.group_revision_id
                    AND source_member.participant_id IS NULL
            )
            OR EXISTS (
                SELECT 1
                FROM tournament_stage_tie_group_members AS source_member
                WHERE source_member.command_id = tie_group.command_id
                    AND source_member.tournament_id = tie_group.tournament_id
                    AND source_member.group_id = tie_group.group_id
                    AND NOT EXISTS (
                        SELECT 1
                        FROM golden_exact_plan_snapshot_members AS member
                        WHERE member.plan_id = plan_group.plan_id
                            AND member.group_revision_id = plan_group.group_revision_id
                            AND member.participant_id = source_member.participant_id
                            AND member.position = source_member.standing_position
                    )
            )
            OR (
                SELECT COUNT(*)
                FROM golden_exact_plan_snapshot_edges AS edge
                WHERE edge.plan_id = plan_group.plan_id
                    AND edge.group_revision_id = plan_group.group_revision_id
            ) <> expected_chain_length
            OR EXISTS (
                SELECT 1
                FROM generate_series(1, expected_chain_length) AS expected(position)
                WHERE NOT EXISTS (
                    SELECT 1
                    FROM golden_exact_plan_snapshot_edges AS edge
                    WHERE edge.plan_id = plan_group.plan_id
                        AND edge.group_revision_id = plan_group.group_revision_id
                        AND edge.position = expected.position
                )
            )
        );

    SELECT COUNT(*)
    INTO missing_edge_reservation_count
    FROM golden_exact_plan_snapshot_edges AS edge
    WHERE edge.plan_id = NEW.plan_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_exact_plan_snapshot_reservations AS reservation
            WHERE reservation.plan_id = edge.plan_id
                AND reservation.tournament_id = edge.tournament_id
                AND reservation.roster_id = edge.roster_id
                AND reservation.reservation_id = edge.reservation_id
                AND reservation.task_id = edge.task_id
                AND reservation.task_version = edge.task_version
        );

    SELECT COUNT(*)
    INTO missing_member_reservation_count
    FROM golden_exact_plan_snapshot_members AS member
    WHERE member.plan_id = NEW.plan_id
        AND NOT EXISTS (
            SELECT 1
            FROM golden_exact_plan_snapshot_participant_reservations AS reservation
            WHERE reservation.plan_id = member.plan_id
                AND reservation.tournament_id = member.tournament_id
                AND reservation.roster_id = member.roster_id
                AND reservation.participant_id = member.participant_id
        );

    IF NOT EXISTS (
        SELECT 1
        FROM golden_exact_plan_snapshot_seals AS seal
        WHERE seal.plan_id = NEW.plan_id
            AND seal.tournament_id = NEW.tournament_id
            AND seal.roster_id = NEW.roster_id
    ) OR incomplete_group_count <> 0
        OR expected_group_count < 1
        OR observed_group_count <> expected_group_count
        OR invalid_group_count <> 0
        OR missing_edge_reservation_count <> 0
        OR missing_member_reservation_count <> 0
        OR candidate_count < expected_group_count * expected_chain_length THEN
        RAISE EXCEPTION 'Golden exact plan snapshot is missing canonical authority children'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE public.assignments
    DROP CONSTRAINT IF EXISTS assignments_reserve_count_check,
    DROP COLUMN IF EXISTS reserve_count;

ALTER TABLE public.assignment_plans
    DROP CONSTRAINT IF EXISTS assignment_plans_reserve_count_check,
    DROP COLUMN IF EXISTS reserve_count;

ALTER TABLE public.tournament_content_configurations
    DROP CONSTRAINT IF EXISTS tournament_content_configurations_reserve_count_check,
    DROP COLUMN IF EXISTS reserve_count;

DROP FUNCTION IF EXISTS public.assignment_plan_reserve_count(uuid);

-- +goose StatementEnd
