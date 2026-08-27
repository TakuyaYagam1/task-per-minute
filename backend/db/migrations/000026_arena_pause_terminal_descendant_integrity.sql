-- +goose Up

-- Pause rows are durable graph authority. Freeze the graph while retained
-- terminal parents are checked and the missing terminal transition guard is
-- installed atomically. Ordinary reads remain available.
LOCK TABLE arena_pauses IN SHARE ROW EXCLUSIVE MODE;

-- Resume already rejects active descendants. Cancellation did not, so an
-- older writer could have left a terminal parent above live recovery state.
-- Stop the migration instead of silently accepting or rewriting that graph.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        WITH RECURSIVE pause_descendants AS (
            SELECT
                parent.id AS terminal_parent_id,
                child.id,
                child.state
            FROM arena_pauses AS parent
            JOIN arena_pauses AS child ON child.parent_pause_id = parent.id
            WHERE parent.state IN ('resumed', 'cancelled')

            UNION ALL

            SELECT
                descendant.terminal_parent_id,
                child.id,
                child.state
            FROM pause_descendants AS descendant
            JOIN arena_pauses AS child ON child.parent_pause_id = descendant.id
        )
        SELECT 1
        FROM pause_descendants
        WHERE state = 'active'
    ) THEN
        RAISE EXCEPTION 'terminal Arena pause retains an active descendant'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Updating the parent row serializes concurrent direct-child inserts because
-- the existing graph guard locks that same parent before accepting an edge.
-- Descendant state is monotonic, so observing no active descendant cannot be
-- invalidated by a terminal descendant returning to active.
-- +goose StatementBegin
CREATE FUNCTION arena_pause_terminal_descendant_guard() RETURNS TRIGGER AS $$
BEGIN
    IF EXISTS (
        WITH RECURSIVE pause_descendants AS (
            SELECT id, state
            FROM arena_pauses
            WHERE parent_pause_id = NEW.id

            UNION ALL

            SELECT child.id, child.state
            FROM arena_pauses AS child
            JOIN pause_descendants AS parent ON child.parent_pause_id = parent.id
        )
        SELECT 1
        FROM pause_descendants
        WHERE state = 'active'
    ) THEN
        RAISE EXCEPTION 'Arena pause cannot terminate with active descendants'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_terminal_descendant_guard
BEFORE UPDATE ON arena_pauses
FOR EACH ROW
WHEN (OLD.state = 'active' AND NEW.state IN ('resumed', 'cancelled'))
EXECUTE FUNCTION arena_pause_terminal_descendant_guard();

-- +goose Down

-- Downgrading permits a parent pause to be cancelled while child recovery
-- state remains active. Production recovery must keep the corrected graph
-- guard and use roll-forward or a verified restore.
LOCK TABLE arena_pauses IN SHARE ROW EXCLUSIVE MODE;

DROP TRIGGER IF EXISTS arena_pause_terminal_descendant_guard ON arena_pauses;
DROP FUNCTION IF EXISTS arena_pause_terminal_descendant_guard();
