-- +goose Up

-- Keep validation and guard installation atomic so no concurrent write can
-- enter between the existing-data checks and the new trigger coverage.
LOCK TABLE
    arena_draft_revisions,
    arena_assignments,
    arena_task_delivery_receipts
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_draft_revisions AS revision
        JOIN arena_drafts AS draft ON draft.id = revision.draft_id
        WHERE revision.current_actor_id IS NOT NULL
            AND (
                revision.turn_number > CASE WHEN draft.format = 'bo1' THEN 2 ELSE 4 END
                OR revision.current_actor_id IS DISTINCT FROM CASE
                    WHEN revision.turn_number IN (1, 3) THEN draft.first_participant_id
                    ELSE draft.second_participant_id
                END
                OR revision.current_action IS DISTINCT FROM CASE
                    WHEN revision.turn_number <= 2 THEN 'ban'
                    ELSE 'pick'
                END
            )
    ) THEN
        RAISE EXCEPTION 'existing Arena draft revision has an invalid turn identity'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM arena_assignments AS assignment
        JOIN arena_assignment_plans AS plan ON plan.id = assignment.plan_id
        WHERE assignment.roster_id IS DISTINCT FROM plan.roster_id
    ) THEN
        RAISE EXCEPTION 'existing Arena assignment crosses roster proof scope'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM arena_task_delivery_receipts AS receipt
        JOIN arena_assignments AS assignment ON assignment.id = receipt.assignment_id
        WHERE receipt.snapshot_id IS DISTINCT FROM assignment.snapshot_id
            OR receipt.task_id IS DISTINCT FROM assignment.task_id
            OR receipt.task_version IS DISTINCT FROM assignment.task_version
    ) THEN
        RAISE EXCEPTION 'existing Arena delivery receipt does not match its assignment snapshot'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION arena_draft_revision_turn_identity_guard() RETURNS TRIGGER AS $$
DECLARE
    draft_format VARCHAR(8);
    first_actor_id UUID;
    second_actor_id UUID;
    expected_actor_id UUID;
    expected_action VARCHAR(8);
    final_turn SMALLINT;
BEGIN
    IF NEW.current_actor_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT format, first_participant_id, second_participant_id
    INTO draft_format, first_actor_id, second_actor_id
    FROM arena_drafts
    WHERE id = NEW.draft_id;

    IF draft_format = 'bo1' THEN
        final_turn := 2;
    ELSE
        final_turn := 4;
    END IF;

    IF NEW.turn_number IN (1, 3) THEN
        expected_actor_id := first_actor_id;
    ELSE
        expected_actor_id := second_actor_id;
    END IF;

    IF NEW.turn_number <= 2 THEN
        expected_action := 'ban';
    ELSE
        expected_action := 'pick';
    END IF;

    IF NEW.turn_number > final_turn
        OR NEW.current_actor_id IS DISTINCT FROM expected_actor_id
        OR NEW.current_action IS DISTINCT FROM expected_action THEN
        RAISE EXCEPTION 'Arena draft revision actor and action must match its turn'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_draft_revision_turn_identity_guard
BEFORE INSERT ON arena_draft_revisions
FOR EACH ROW EXECUTE FUNCTION arena_draft_revision_turn_identity_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_plan_roster_guard() RETURNS TRIGGER AS $$
DECLARE
    plan_roster_id UUID;
BEGIN
    SELECT roster_id
    INTO plan_roster_id
    FROM arena_assignment_plans
    WHERE id = NEW.plan_id;

    IF plan_roster_id IS DISTINCT FROM NEW.roster_id THEN
        RAISE EXCEPTION 'Arena assignment plan and Game attempt must share one roster'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_plan_roster_guard
BEFORE INSERT ON arena_assignments
FOR EACH ROW EXECUTE FUNCTION arena_assignment_plan_roster_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_delivery_receipt_assignment_identity_guard() RETURNS TRIGGER AS $$
DECLARE
    assignment_snapshot_id UUID;
    assignment_task_id UUID;
    assignment_task_version INTEGER;
BEGIN
    SELECT snapshot_id, task_id, task_version
    INTO assignment_snapshot_id, assignment_task_id, assignment_task_version
    FROM arena_assignments
    WHERE id = NEW.assignment_id;

    IF NEW.snapshot_id IS DISTINCT FROM assignment_snapshot_id
        OR NEW.task_id IS DISTINCT FROM assignment_task_id
        OR NEW.task_version IS DISTINCT FROM assignment_task_version THEN
        RAISE EXCEPTION 'Arena delivery receipt must match its assignment snapshot identity'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_delivery_receipt_assignment_identity_guard
BEFORE INSERT ON arena_task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION arena_delivery_receipt_assignment_identity_guard();

-- +goose Down

DROP TRIGGER IF EXISTS arena_delivery_receipt_assignment_identity_guard
    ON arena_task_delivery_receipts;
DROP FUNCTION IF EXISTS arena_delivery_receipt_assignment_identity_guard();
DROP TRIGGER IF EXISTS arena_assignment_plan_roster_guard ON arena_assignments;
DROP FUNCTION IF EXISTS arena_assignment_plan_roster_guard();
DROP TRIGGER IF EXISTS arena_draft_revision_turn_identity_guard ON arena_draft_revisions;
DROP FUNCTION IF EXISTS arena_draft_revision_turn_identity_guard();
