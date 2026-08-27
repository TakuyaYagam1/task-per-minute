-- +goose Up

-- Keep validation and guard installation atomic so a committed plan cannot
-- change its active proof branch while the new guards are being installed.
LOCK TABLE
    arena_assignment_plans,
    arena_assignment_branches,
    arena_task_version_reservations
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_assignment_plans AS plan
        LEFT JOIN arena_assignment_branches AS branch
            ON branch.id = plan.active_branch_id
            AND branch.plan_id = plan.id
        WHERE plan.state = 'committed'
            AND (branch.id IS NULL OR branch.state <> 'active')
    ) THEN
        RAISE EXCEPTION 'existing committed Arena plan does not reference its active branch'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_plan_commit_evidence_guard() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.state = 'committed'
        AND (
            NEW.active_branch_id IS DISTINCT FROM OLD.active_branch_id
            OR NEW.committed_at IS DISTINCT FROM OLD.committed_at
        ) THEN
        RAISE EXCEPTION 'committed Arena plan branch evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_plan_commit_evidence_guard
BEFORE UPDATE ON arena_assignment_plans
FOR EACH ROW EXECUTE FUNCTION arena_assignment_plan_commit_evidence_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_branch_transition_evidence_guard() RETURNS TRIGGER AS $$
DECLARE
    referencing_plan_state VARCHAR(16);
BEGIN
    IF OLD.activated_at IS NOT NULL
        AND NEW.activated_at IS DISTINCT FROM OLD.activated_at THEN
        RAISE EXCEPTION 'Arena branch activation evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.disclosed_at IS NOT NULL
        AND NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at THEN
        RAISE EXCEPTION 'Arena branch disclosure evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'active' AND NEW.state <> 'active' THEN
        SELECT state
        INTO referencing_plan_state
        FROM arena_assignment_plans
        WHERE active_branch_id = OLD.id
        FOR UPDATE;

        IF referencing_plan_state = 'committed' THEN
            RAISE EXCEPTION 'committed Arena plan must retain its active branch'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_branch_transition_evidence_guard
BEFORE UPDATE ON arena_assignment_branches
FOR EACH ROW EXECUTE FUNCTION arena_assignment_branch_transition_evidence_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_reservation_commit_evidence_guard() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.committed_at IS NOT NULL
        AND NEW.committed_at IS DISTINCT FROM OLD.committed_at THEN
        RAISE EXCEPTION 'Arena reservation commit evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_reservation_commit_evidence_guard
BEFORE UPDATE ON arena_task_version_reservations
FOR EACH ROW EXECUTE FUNCTION arena_reservation_commit_evidence_guard();

-- +goose Down

DROP TRIGGER IF EXISTS arena_reservation_commit_evidence_guard
    ON arena_task_version_reservations;
DROP FUNCTION IF EXISTS arena_reservation_commit_evidence_guard();
DROP TRIGGER IF EXISTS arena_assignment_branch_transition_evidence_guard
    ON arena_assignment_branches;
DROP FUNCTION IF EXISTS arena_assignment_branch_transition_evidence_guard();
DROP TRIGGER IF EXISTS arena_assignment_plan_commit_evidence_guard
    ON arena_assignment_plans;
DROP FUNCTION IF EXISTS arena_assignment_plan_commit_evidence_guard();
