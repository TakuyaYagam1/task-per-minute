-- +goose Up

-- Pause and presence evidence may already have synthetic writers. Install the
-- resume guard while both the durable decision history and live CAS rows are
-- stable. Ordinary reads remain available while concurrent writes wait.
LOCK TABLE
    arena_pauses,
    arena_resume_decisions,
    arena_reconnect_intervals,
    arena_presence_states
IN SHARE ROW EXCLUSIVE MODE;

-- A stored resume decision is evidence from one point in time. Before the
-- pause becomes resolved, lock reconnect intervals before live presence rows
-- and prove that the latest decision still describes the current CAS state.
-- The trigger name intentionally sorts before arena_pause_graph_guard.
-- +goose StatementBegin
CREATE FUNCTION arena_pause_resume_current_evidence_guard() RETURNS TRIGGER AS $$
DECLARE
    latest_decision arena_resume_decisions%ROWTYPE;
    first_live arena_presence_states%ROWTYPE;
    second_live arena_presence_states%ROWTYPE;
    open_interval_count INTEGER;
BEGIN
    IF NEW.series_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT *
    INTO latest_decision
    FROM arena_resume_decisions
    WHERE pause_id = NEW.id
    ORDER BY decision_number DESC
    LIMIT 1;

    IF latest_decision.id IS NULL
        OR latest_decision.action IS DISTINCT FROM 'resume' THEN
        RAISE EXCEPTION 'Arena pause resume requires a current resume decision'
            USING ERRCODE = 'check_violation';
    END IF;

    PERFORM 1
    FROM arena_reconnect_intervals
    WHERE pause_id = NEW.id
    ORDER BY participant_id, interval_number, id
    FOR UPDATE;

    SELECT COUNT(*)
    INTO open_interval_count
    FROM arena_reconnect_intervals
    WHERE pause_id = NEW.id AND state = 'open';

    SELECT *
    INTO first_live
    FROM arena_presence_states
    WHERE series_id = NEW.series_id
        AND participant_id = latest_decision.first_participant_id
    FOR UPDATE;

    SELECT *
    INTO second_live
    FROM arena_presence_states
    WHERE series_id = NEW.series_id
        AND participant_id = latest_decision.second_participant_id
    FOR UPDATE;

    IF open_interval_count <> 0
        OR first_live.id IS NULL
        OR second_live.id IS NULL
        OR first_live.state IS DISTINCT FROM 'connected'
        OR second_live.state IS DISTINCT FROM 'connected'
        OR latest_decision.first_live_state IS DISTINCT FROM first_live.state
        OR latest_decision.second_live_state IS DISTINCT FROM second_live.state
        OR latest_decision.first_presence_epoch IS DISTINCT FROM first_live.presence_epoch
        OR latest_decision.second_presence_epoch IS DISTINCT FROM second_live.presence_epoch
        OR latest_decision.first_presence_revision IS DISTINCT FROM first_live.revision
        OR latest_decision.second_presence_revision IS DISTINCT FROM second_live.revision THEN
        RAISE EXCEPTION 'Arena pause resume decision does not match current reconnect evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_current_resume_cas_guard
BEFORE UPDATE ON arena_pauses
FOR EACH ROW
WHEN (OLD.state = 'active' AND NEW.state = 'resumed')
EXECUTE FUNCTION arena_pause_resume_current_evidence_guard();

-- +goose Down

-- Downgrading permits a pause to resume without a decision or from stale
-- presence evidence. Production recovery must preserve this guard and use
-- roll-forward or a verified restore.
LOCK TABLE arena_pauses IN SHARE ROW EXCLUSIVE MODE;

DROP TRIGGER IF EXISTS arena_pause_current_resume_cas_guard ON arena_pauses;
DROP FUNCTION IF EXISTS arena_pause_resume_current_evidence_guard();
