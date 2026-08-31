-- +goose Up

-- Continuation segments expand reconnect history without changing the slot
-- semantics used by existing writers. Every retained row starts as a root;
-- old inserts can omit the new columns and keep charging the next root slot.
LOCK TABLE
    arena_rosters,
    arena_waves,
    arena_wave_members,
    arena_pauses,
    arena_pause_presence_snapshots,
    arena_presence_states,
    arena_reconnect_slot_counters,
    arena_reconnect_intervals,
    arena_resume_decisions
IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE arena_reconnect_intervals
    ADD COLUMN continuation_number INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN continued_from_id UUID,
    ADD COLUMN suspended_by_pause_id UUID;

COMMENT ON COLUMN arena_reconnect_intervals.continuation_number IS
    'Zero for a slot-charging root and positive for resumed segments of that root';
COMMENT ON COLUMN arena_reconnect_intervals.continued_from_id IS
    'Immediate predecessor segment; null only for a root segment';
COMMENT ON COLUMN arena_reconnect_intervals.suspended_by_pause_id IS
    'Normal Wave pause that atomically cancelled this segment';

ALTER TABLE arena_reconnect_intervals
    DROP CONSTRAINT arena_reconnect_intervals_number_key,
    ADD CONSTRAINT arena_reconnect_intervals_segment_key UNIQUE (
        pause_id,
        participant_id,
        interval_number,
        continuation_number
    ),
    ADD CONSTRAINT arena_reconnect_intervals_continued_from_fk
        FOREIGN KEY (continued_from_id)
        REFERENCES arena_reconnect_intervals(id) ON DELETE RESTRICT,
    ADD CONSTRAINT arena_reconnect_intervals_suspended_by_pause_fk
        FOREIGN KEY (suspended_by_pause_id)
        REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    ADD CONSTRAINT arena_reconnect_intervals_lineage_check CHECK (
        continuation_number >= 0
        AND continued_from_id IS DISTINCT FROM id
        AND (
            (continuation_number = 0 AND continued_from_id IS NULL)
            OR (continuation_number > 0 AND continued_from_id IS NOT NULL)
        )
    ),
    ADD CONSTRAINT arena_reconnect_intervals_suspension_check CHECK (
        suspended_by_pause_id IS NULL OR state = 'cancelled'
    );

-- A disconnect epoch may charge at most one fresh root slot. Version 30 did
-- not encode this as a durable key, so fail closed before creating the index
-- instead of choosing between ambiguous retained roots.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals
        WHERE continuation_number = 0
        GROUP BY pause_id, participant_id, presence_epoch
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION
            'Arena retained reconnect roots reuse a presence epoch'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX arena_reconnect_intervals_root_presence_epoch_key
    ON arena_reconnect_intervals (pause_id, participant_id, presence_epoch)
    WHERE continuation_number = 0;

CREATE UNIQUE INDEX arena_reconnect_intervals_continued_from_key
    ON arena_reconnect_intervals (continued_from_id)
    WHERE continued_from_id IS NOT NULL;

-- Version 30 cannot describe suspension provenance. Refuse retained Wave
-- boundaries that would require guessing it instead of silently accepting an
-- inconsistent starting point for the new invariant.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_pauses AS normal_pause
        JOIN arena_pause_presence_snapshots AS snapshot
            ON snapshot.pause_id = normal_pause.id
        JOIN arena_reconnect_intervals AS reconnect_interval
            ON reconnect_interval.roster_id = snapshot.roster_id
            AND reconnect_interval.series_id = snapshot.series_id
            AND reconnect_interval.participant_id = snapshot.participant_id
        WHERE normal_pause.scope_kind = 'wave'
            AND normal_pause.parent_pause_id IS NULL
            AND reconnect_interval.opened_at < normal_pause.started_at
            AND normal_pause.started_at < reconnect_interval.deadline_at
            AND (
                reconnect_interval.closed_at IS NULL
                OR reconnect_interval.closed_at >= normal_pause.started_at
            )
            AND NOT (
                reconnect_interval.state = 'cancelled'
                AND reconnect_interval.closed_at
                    IS NOT DISTINCT FROM normal_pause.started_at
                AND reconnect_interval.suspended_by_pause_id
                    IS NOT DISTINCT FROM normal_pause.id
                AND reconnect_interval.updated_at
                    IS NOT DISTINCT FROM normal_pause.started_at
            )
    ) THEN
        RAISE EXCEPTION
            'Arena retained normal Wave pause lacks atomic reconnect suspension'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- A normal Wave pause freezes the exact membership represented by its
-- presence snapshots. Validate retained version-30 history while membership
-- writers are excluded; installing only future-row triggers would otherwise
-- preserve an ambiguous coverage boundary.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_pauses AS normal_pause
        WHERE normal_pause.scope_kind = 'wave'
            AND normal_pause.parent_pause_id IS NULL
            AND (
                EXISTS (
                    SELECT 1
                    FROM arena_wave_members AS member
                    WHERE member.wave_id = normal_pause.wave_id
                        AND member.roster_id = normal_pause.roster_id
                        AND NOT EXISTS (
                            SELECT 1
                            FROM arena_pause_presence_snapshots AS snapshot
                            JOIN arena_presence_states AS presence
                                ON presence.series_id = snapshot.series_id
                                AND presence.roster_id = snapshot.roster_id
                                AND presence.participant_id = snapshot.participant_id
                            WHERE snapshot.pause_id = normal_pause.id
                                AND snapshot.roster_id = member.roster_id
                                AND snapshot.participant_id = member.participant_id
                        )
                )
                OR EXISTS (
                    SELECT 1
                    FROM arena_pause_presence_snapshots AS snapshot
                    WHERE snapshot.pause_id = normal_pause.id
                        AND NOT EXISTS (
                            SELECT 1
                            FROM arena_wave_members AS member
                            WHERE member.wave_id = normal_pause.wave_id
                                AND member.roster_id = snapshot.roster_id
                                AND member.participant_id = snapshot.participant_id
                        )
                )
            )
    ) THEN
        RAISE EXCEPTION
            'Arena retained normal Wave pause lacks exact membership snapshot coverage'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Membership changes serialize on their target Wave without upgrading the
-- roster lock already held by concurrent Wave creation. Normal Wave entry
-- takes the same Wave lock after its roster topology fence. A change remains
-- legal before the first normal pause, then becomes retained history together
-- with the pause snapshots.
-- +goose StatementBegin
CREATE FUNCTION arena_wave_member_normal_pause_guard()
RETURNS TRIGGER AS $$
DECLARE
    old_wave_id UUID;
    new_wave_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_wave_id := OLD.wave_id;
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_wave_id := NEW.wave_id;
    END IF;

    PERFORM 1
    FROM arena_waves
    WHERE id = ANY(
        ARRAY_REMOVE(ARRAY[old_wave_id, new_wave_id], NULL)
    )
    ORDER BY id
    FOR UPDATE;

    IF EXISTS (
        SELECT 1
        FROM arena_pauses
        WHERE scope_kind = 'wave'
            AND parent_pause_id IS NULL
            AND wave_id = ANY(
                ARRAY_REMOVE(ARRAY[old_wave_id, new_wave_id], NULL)
            )
    ) THEN
        RAISE EXCEPTION
            'Arena Wave membership is immutable after normal pause history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_wave_members_normal_pause_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_wave_members
FOR EACH ROW EXECUTE FUNCTION arena_wave_member_normal_pause_guard();

-- The roster row is the stable topology fence for normal Wave entry. Its
-- UPDATE lock conflicts with the FK KEY SHARE held by concurrent pause
-- creation. The Wave row serializes membership, then existing Game pauses and
-- intervals are locked in their established order; reconnect writers can keep
-- their version-30 Game-first sequence without a participant fence.
-- +goose StatementBegin
CREATE FUNCTION arena_normal_wave_reconnect_lock()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM 'active'
        OR NEW.scope_kind IS DISTINCT FROM 'wave'
        OR NEW.parent_pause_id IS NOT NULL THEN
        RETURN NEW;
    END IF;

    PERFORM 1
    FROM arena_rosters
    WHERE id = NEW.roster_id
        AND tournament_id = NEW.tournament_id
    FOR UPDATE;

    PERFORM 1
    FROM arena_waves
    WHERE id = NEW.wave_id
        AND roster_id = NEW.roster_id
    FOR UPDATE;

    PERFORM 1
    FROM arena_pauses AS game_pause
    JOIN arena_series AS series ON series.id = game_pause.series_id
    WHERE game_pause.state = 'active'
        AND game_pause.scope_kind = 'game_attempt'
        AND game_pause.roster_id = NEW.roster_id
        AND EXISTS (
            SELECT 1
            FROM arena_wave_members AS member
            WHERE member.wave_id = NEW.wave_id
                AND member.roster_id = NEW.roster_id
                AND member.participant_id IN (
                    series.first_participant_id,
                    series.second_participant_id
                )
        )
    ORDER BY game_pause.id
    FOR UPDATE OF game_pause;

    PERFORM 1
    FROM arena_reconnect_intervals AS reconnect_interval
    JOIN arena_pauses AS game_pause
        ON game_pause.id = reconnect_interval.pause_id
    JOIN arena_wave_members AS member
        ON member.wave_id = NEW.wave_id
        AND member.roster_id = NEW.roster_id
        AND member.participant_id = reconnect_interval.participant_id
    WHERE game_pause.state = 'active'
        AND game_pause.scope_kind = 'game_attempt'
        AND game_pause.roster_id = NEW.roster_id
        AND reconnect_interval.state = 'open'
        AND reconnect_interval.opened_at < NEW.started_at
        AND NEW.started_at < reconnect_interval.deadline_at
    ORDER BY
        reconnect_interval.pause_id,
        reconnect_interval.participant_id,
        reconnect_interval.interval_number,
        reconnect_interval.continuation_number,
        reconnect_interval.id
    FOR UPDATE OF reconnect_interval;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_wave_reconnect_lock
BEFORE INSERT ON arena_pauses
FOR EACH ROW EXECUTE FUNCTION arena_normal_wave_reconnect_lock();

-- The roster fence prevents write skew, while these deferred checks observe
-- the final state of both transactions. A Wave pause must atomically suspend
-- every covered segment crossing its start; a later interval cannot backdate
-- across an already committed covering Wave pause.
-- +goose StatementBegin
CREATE FUNCTION arena_validate_normal_wave_reconnect_suspension()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_TABLE_NAME = 'arena_pauses' THEN
        IF NEW.state IS DISTINCT FROM 'active'
            OR NEW.scope_kind IS DISTINCT FROM 'wave'
            OR NEW.parent_pause_id IS NOT NULL THEN
            RETURN NULL;
        END IF;

        IF EXISTS (
            SELECT 1
            FROM arena_wave_members AS member
            WHERE member.wave_id = NEW.wave_id
                AND member.roster_id = NEW.roster_id
                AND NOT EXISTS (
                    SELECT 1
                    FROM arena_pause_presence_snapshots AS snapshot
                    JOIN arena_presence_states AS presence
                        ON presence.series_id = snapshot.series_id
                        AND presence.roster_id = snapshot.roster_id
                        AND presence.participant_id = snapshot.participant_id
                    WHERE snapshot.pause_id = NEW.id
                        AND snapshot.roster_id = member.roster_id
                        AND snapshot.participant_id = member.participant_id
                )
        ) OR EXISTS (
            SELECT 1
            FROM arena_pause_presence_snapshots AS snapshot
            WHERE snapshot.pause_id = NEW.id
                AND NOT EXISTS (
                    SELECT 1
                    FROM arena_wave_members AS member
                    WHERE member.wave_id = NEW.wave_id
                        AND member.roster_id = snapshot.roster_id
                        AND member.participant_id = snapshot.participant_id
                )
        ) THEN
            RAISE EXCEPTION
                'Arena normal Wave pause requires exact membership snapshot coverage'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM arena_pause_presence_snapshots AS snapshot
            JOIN arena_reconnect_intervals AS reconnect_interval
                ON reconnect_interval.roster_id = snapshot.roster_id
                AND reconnect_interval.series_id = snapshot.series_id
                AND reconnect_interval.participant_id = snapshot.participant_id
            WHERE snapshot.pause_id = NEW.id
                AND reconnect_interval.opened_at < NEW.started_at
                AND NEW.started_at < reconnect_interval.deadline_at
                AND (
                    reconnect_interval.closed_at IS NULL
                    OR reconnect_interval.closed_at >= NEW.started_at
                )
                AND NOT (
                    reconnect_interval.state = 'cancelled'
                    AND reconnect_interval.closed_at
                        IS NOT DISTINCT FROM NEW.started_at
                    AND reconnect_interval.suspended_by_pause_id
                        IS NOT DISTINCT FROM NEW.id
                    AND reconnect_interval.updated_at
                        IS NOT DISTINCT FROM NEW.started_at
                )
        ) THEN
            RAISE EXCEPTION
                'Arena normal Wave pause requires atomic reconnect suspension'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NULL;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals AS current_interval
        CROSS JOIN arena_pauses AS normal_pause
        JOIN arena_pause_presence_snapshots AS snapshot
            ON snapshot.pause_id = normal_pause.id
            AND snapshot.roster_id = current_interval.roster_id
            AND snapshot.series_id = current_interval.series_id
            AND snapshot.participant_id = current_interval.participant_id
        WHERE normal_pause.scope_kind = 'wave'
            AND normal_pause.parent_pause_id IS NULL
            AND normal_pause.roster_id = current_interval.roster_id
            AND current_interval.id = NEW.id
            AND current_interval.opened_at < normal_pause.started_at
            AND normal_pause.started_at < current_interval.deadline_at
            AND (
                current_interval.closed_at IS NULL
                OR current_interval.closed_at >= normal_pause.started_at
            )
            AND NOT (
                current_interval.state = 'cancelled'
                AND current_interval.closed_at
                    IS NOT DISTINCT FROM normal_pause.started_at
                AND current_interval.suspended_by_pause_id
                    IS NOT DISTINCT FROM normal_pause.id
                AND current_interval.updated_at
                    IS NOT DISTINCT FROM normal_pause.started_at
            )
    ) THEN
        RAISE EXCEPTION
            'Arena reconnect interval cannot backdate across normal Wave pause history'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_normal_wave_reconnect_suspension
AFTER INSERT ON arena_pauses
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_normal_wave_reconnect_suspension();

CREATE CONSTRAINT TRIGGER arena_reconnect_interval_normal_wave_consistency
AFTER INSERT ON arena_reconnect_intervals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_normal_wave_reconnect_suspension();

-- Root count remains the durable slot-counter meaning. Continuations share a
-- root's logical interval number and therefore neither consume nor create a
-- counter revision.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_validate_reconnect_slot_count()
RETURNS TRIGGER AS $$
DECLARE
    target_pause_id UUID;
    target_participant_id UUID;
    used_slots SMALLINT;
    root_count INTEGER;
    distinct_root_count INTEGER;
    first_root_number INTEGER;
    last_root_number INTEGER;
BEGIN
    target_pause_id := NEW.pause_id;
    target_participant_id := NEW.participant_id;

    SELECT slots_used
    INTO used_slots
    FROM arena_reconnect_slot_counters
    WHERE pause_id = target_pause_id
        AND participant_id = target_participant_id;

    SELECT
        COUNT(*),
        COUNT(DISTINCT interval_number),
        MIN(interval_number),
        MAX(interval_number)
    INTO
        root_count,
        distinct_root_count,
        first_root_number,
        last_root_number
    FROM arena_reconnect_intervals
    WHERE pause_id = target_pause_id
        AND participant_id = target_participant_id
        AND continuation_number = 0;

    IF used_slots IS DISTINCT FROM root_count THEN
        RAISE EXCEPTION
            'Arena reconnect slot count differs from retained root intervals'
            USING ERRCODE = 'check_violation';
    END IF;

    IF root_count > 0 AND (
        distinct_root_count IS DISTINCT FROM root_count
        OR first_root_number IS DISTINCT FROM 1
        OR last_root_number IS DISTINCT FROM root_count
    ) THEN
        RAISE EXCEPTION
            'Arena reconnect root interval numbers must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- The interval guard owns both sides of the segment lifecycle. A normal Wave
-- pause may cancel an open segment once. A continuation then reuses the same
-- Game, participant, presence epoch and logical root number with a shifted
-- deadline, while fresh roots keep the pre-existing counter CAS path.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_reconnect_interval_guard()
RETURNS TRIGGER AS $$
DECLARE
    owning_pause arena_pauses%ROWTYPE;
    suspending_pause arena_pauses%ROWTYPE;
    predecessor arena_reconnect_intervals%ROWTYPE;
    live_presence arena_presence_states%ROWTYPE;
    pause_snapshot arena_pause_presence_snapshots%ROWTYPE;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        -- Let the named lineage constraint report the two nullability axes and
        -- let the named foreign key report a missing predecessor.
        IF (
            NEW.continuation_number = 0
            AND NEW.continued_from_id IS NOT NULL
        ) OR (
            NEW.continuation_number > 0
            AND NEW.continued_from_id IS NULL
        ) THEN
            RETURN NEW;
        END IF;

        IF NEW.continuation_number = 0
            AND NEW.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION
                'Arena reconnect interval insert cannot carry suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.state = 'open' AND NEW.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION
                'Arena open reconnect continuation cannot carry suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.continuation_number > 0
            AND (
                NEW.state IS DISTINCT FROM 'open'
                OR NEW.closed_at IS NOT NULL
                OR NEW.suspended_by_pause_id IS NOT NULL
                OR NEW.revision IS DISTINCT FROM 1
                OR NEW.created_at IS DISTINCT FROM NEW.opened_at
                OR NEW.updated_at IS DISTINCT FROM NEW.opened_at
            ) THEN
            RAISE EXCEPTION
                'Arena reconnect continuation must start as a canonical open segment'
                USING ERRCODE = 'check_violation';
        END IF;

        -- The owning Game pause remains the first explicit lock for reconnect
        -- writers and is always locked before a continuation predecessor.
        SELECT *
        INTO owning_pause
        FROM arena_pauses
        WHERE id = NEW.pause_id
            AND series_id = NEW.series_id
            AND roster_id = NEW.roster_id
        FOR UPDATE;

        IF NEW.continuation_number = 0 THEN
            IF owning_pause.state IS DISTINCT FROM 'active'
                OR owning_pause.scope_kind IS DISTINCT FROM 'game_attempt'
                OR owning_pause.game_attempt_id
                    IS DISTINCT FROM NEW.game_attempt_id
                OR NEW.opened_at < owning_pause.started_at THEN
                RAISE EXCEPTION
                    'Arena reconnect interval requires its active Game pause'
                    USING ERRCODE = 'check_violation';
            END IF;

            IF EXISTS (
                SELECT 1
                FROM arena_pauses AS normal_pause
                JOIN arena_pause_presence_snapshots AS snapshot
                    ON snapshot.pause_id = normal_pause.id
                    AND snapshot.roster_id = NEW.roster_id
                    AND snapshot.series_id = NEW.series_id
                    AND snapshot.participant_id = NEW.participant_id
                WHERE normal_pause.scope_kind = 'wave'
                    AND normal_pause.parent_pause_id IS NULL
                    AND normal_pause.roster_id = NEW.roster_id
                    AND NEW.opened_at < normal_pause.started_at
                    AND normal_pause.started_at < NEW.deadline_at
                    AND (
                        NEW.closed_at IS NULL
                        OR NEW.closed_at >= normal_pause.started_at
                    )
            ) THEN
                RAISE EXCEPTION
                    'Arena reconnect interval cannot backdate across normal Wave pause history'
                    USING ERRCODE = 'check_violation';
            END IF;

            SELECT *
            INTO live_presence
            FROM arena_presence_states
            WHERE series_id = NEW.series_id
                AND participant_id = NEW.participant_id
            FOR UPDATE;

            SELECT slots_used
            INTO counter_slots
            FROM arena_reconnect_slot_counters
            WHERE pause_id = NEW.pause_id
                AND participant_id = NEW.participant_id
            FOR UPDATE;

            IF live_presence.state IS DISTINCT FROM 'disconnected'
                OR live_presence.presence_epoch
                    IS DISTINCT FROM NEW.presence_epoch
                OR NEW.interval_number IS DISTINCT FROM counter_slots + 1 THEN
                RAISE EXCEPTION
                    'Arena reconnect interval must consume the next live slot'
                    USING ERRCODE = 'check_violation';
            END IF;

            RETURN NEW;
        END IF;

        SELECT *
        INTO predecessor
        FROM arena_reconnect_intervals
        WHERE id = NEW.continued_from_id
        FOR UPDATE;

        IF NOT FOUND THEN
            RETURN NEW;
        END IF;

        IF NEW.pause_id IS DISTINCT FROM predecessor.pause_id
            OR NEW.roster_id IS DISTINCT FROM predecessor.roster_id
            OR NEW.series_id IS DISTINCT FROM predecessor.series_id
            OR NEW.game_attempt_id
                IS DISTINCT FROM predecessor.game_attempt_id
            OR NEW.participant_id
                IS DISTINCT FROM predecessor.participant_id
            OR NEW.presence_epoch
                IS DISTINCT FROM predecessor.presence_epoch
            OR NEW.interval_number
                IS DISTINCT FROM predecessor.interval_number
            OR NEW.continuation_number
                IS DISTINCT FROM predecessor.continuation_number + 1 THEN
            RAISE EXCEPTION
                'Arena reconnect continuation does not match predecessor identity'
                USING ERRCODE = 'check_violation';
        END IF;

        IF predecessor.state IS DISTINCT FROM 'cancelled'
            OR predecessor.closed_at IS NULL
            OR predecessor.suspended_by_pause_id IS NULL THEN
            RAISE EXCEPTION
                'Arena reconnect continuation requires a cancelled predecessor with suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF owning_pause.state IS DISTINCT FROM 'active'
            OR owning_pause.scope_kind IS DISTINCT FROM 'game_attempt'
            OR owning_pause.game_attempt_id
                IS DISTINCT FROM NEW.game_attempt_id
            OR NEW.opened_at < owning_pause.started_at THEN
            RAISE EXCEPTION
                'Arena reconnect interval requires its active Game pause'
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM arena_pauses AS normal_pause
            JOIN arena_pause_presence_snapshots AS snapshot
                ON snapshot.pause_id = normal_pause.id
                AND snapshot.roster_id = NEW.roster_id
                AND snapshot.series_id = NEW.series_id
                AND snapshot.participant_id = NEW.participant_id
            WHERE normal_pause.scope_kind = 'wave'
                AND normal_pause.parent_pause_id IS NULL
                AND normal_pause.roster_id = NEW.roster_id
                AND NEW.opened_at < normal_pause.started_at
                AND normal_pause.started_at < NEW.deadline_at
                AND (
                    NEW.closed_at IS NULL
                    OR NEW.closed_at >= normal_pause.started_at
                )
        ) THEN
            RAISE EXCEPTION
                'Arena reconnect interval cannot backdate across normal Wave pause history'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO suspending_pause
        FROM arena_pauses
        WHERE id = predecessor.suspended_by_pause_id
        FOR UPDATE;

        IF suspending_pause.state IS DISTINCT FROM 'active'
            OR suspending_pause.scope_kind IS DISTINCT FROM 'wave'
            OR suspending_pause.parent_pause_id IS NOT NULL
            OR suspending_pause.tournament_id
                IS DISTINCT FROM owning_pause.tournament_id
            OR suspending_pause.roster_id IS DISTINCT FROM NEW.roster_id
            OR predecessor.closed_at
                IS DISTINCT FROM suspending_pause.started_at THEN
            RAISE EXCEPTION
                'Arena reconnect continuation requires a cancelled predecessor with suspension provenance'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.opened_at <= predecessor.closed_at
            OR NEW.deadline_at IS DISTINCT FROM NEW.opened_at
                + (predecessor.deadline_at - predecessor.closed_at) THEN
            RAISE EXCEPTION
                'Arena reconnect continuation does not match suspended predecessor'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO live_presence
        FROM arena_presence_states
        WHERE series_id = NEW.series_id
            AND participant_id = NEW.participant_id
        FOR UPDATE;

        SELECT slots_used
        INTO counter_slots
        FROM arena_reconnect_slot_counters
        WHERE pause_id = NEW.pause_id
            AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_presence.state IS DISTINCT FROM 'disconnected'
            OR live_presence.presence_epoch
                IS DISTINCT FROM NEW.presence_epoch
            OR counter_slots IS NULL
            OR counter_slots < NEW.interval_number THEN
            RAISE EXCEPTION
                'Arena reconnect continuation does not match predecessor identity'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.suspended_by_pause_id IS DISTINCT FROM OLD.suspended_by_pause_id
        AND NOT (
            OLD.state = 'open'
            AND OLD.suspended_by_pause_id IS NULL
            AND NEW.state = 'cancelled'
            AND NEW.suspended_by_pause_id IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'Arena reconnect suspension provenance is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.presence_epoch IS DISTINCT FROM OLD.presence_epoch
        OR NEW.interval_number IS DISTINCT FROM OLD.interval_number
        OR NEW.continuation_number IS DISTINCT FROM OLD.continuation_number
        OR NEW.continued_from_id IS DISTINCT FROM OLD.continued_from_id
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'open'
        OR NEW.state NOT IN ('reconnected', 'expired', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena reconnect interval CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'cancelled' THEN
        IF OLD.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION 'Arena reconnect suspension provenance is immutable'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.suspended_by_pause_id IS NULL THEN
            -- Version 30 writers may still close a root or continuation
            -- generically. Without provenance the terminal row is durable but
            -- cannot become a continuation predecessor.
            RETURN NEW;
        END IF;

        SELECT *
        INTO owning_pause
        FROM arena_pauses
        WHERE id = OLD.pause_id
            AND series_id = OLD.series_id
            AND roster_id = OLD.roster_id;

        SELECT *
        INTO suspending_pause
        FROM arena_pauses
        WHERE id = NEW.suspended_by_pause_id
        FOR UPDATE;

        SELECT *
        INTO pause_snapshot
        FROM arena_pause_presence_snapshots
        WHERE pause_id = NEW.suspended_by_pause_id
            AND participant_id = OLD.participant_id;

        SELECT *
        INTO live_presence
        FROM arena_presence_states
        WHERE series_id = OLD.series_id
            AND participant_id = OLD.participant_id
        FOR UPDATE;

        IF owning_pause.state IS DISTINCT FROM 'active'
            OR owning_pause.scope_kind IS DISTINCT FROM 'game_attempt'
            OR owning_pause.game_attempt_id
                IS DISTINCT FROM OLD.game_attempt_id
            OR OLD.opened_at < owning_pause.started_at
            OR suspending_pause.state IS DISTINCT FROM 'active'
            OR suspending_pause.scope_kind IS DISTINCT FROM 'wave'
            OR suspending_pause.parent_pause_id IS NOT NULL
            OR suspending_pause.tournament_id
                IS DISTINCT FROM owning_pause.tournament_id
            OR suspending_pause.roster_id IS DISTINCT FROM OLD.roster_id
            OR suspending_pause.started_at <= OLD.opened_at
            OR suspending_pause.started_at >= OLD.deadline_at
            OR NEW.closed_at IS DISTINCT FROM suspending_pause.started_at
            OR NEW.updated_at IS DISTINCT FROM NEW.closed_at
            OR NOT EXISTS (
                SELECT 1
                FROM arena_wave_members AS member
                WHERE member.wave_id = suspending_pause.wave_id
                    AND member.roster_id = OLD.roster_id
                    AND member.participant_id = OLD.participant_id
            )
            OR pause_snapshot.pause_id IS NULL
            OR pause_snapshot.roster_id IS DISTINCT FROM OLD.roster_id
            OR pause_snapshot.series_id IS DISTINCT FROM OLD.series_id
            OR pause_snapshot.presence_state IS DISTINCT FROM 'disconnected'
            OR pause_snapshot.presence_epoch
                IS DISTINCT FROM OLD.presence_epoch
            OR live_presence.state IS DISTINCT FROM 'disconnected'
            OR live_presence.presence_epoch
                IS DISTINCT FROM OLD.presence_epoch
            OR pause_snapshot.presence_revision
                IS DISTINCT FROM live_presence.revision THEN
            RAISE EXCEPTION
                'Arena reconnect cancellation requires a covering active normal Wave pause'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        IF NEW.suspended_by_pause_id IS NOT NULL THEN
            RAISE EXCEPTION 'Arena reconnect suspension provenance is immutable'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO live_presence
        FROM arena_presence_states
        WHERE series_id = NEW.series_id
            AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_presence.state IS DISTINCT FROM 'disconnected'
            OR live_presence.presence_epoch
                IS DISTINCT FROM NEW.presence_epoch THEN
            RAISE EXCEPTION
                'Arena reconnect terminal transition has stale presence CAS'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- Continuation and suspension provenance cannot be represented by version 30.
-- Lock every touched surface and fail before DDL so Goose leaves version 31,
-- its rows, and all definitions intact when any such evidence exists.
LOCK TABLE
    arena_rosters,
    arena_waves,
    arena_wave_members,
    arena_pauses,
    arena_pause_presence_snapshots,
    arena_presence_states,
    arena_reconnect_slot_counters,
    arena_reconnect_intervals,
    arena_resume_decisions
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_reconnect_intervals
        WHERE continuation_number <> 0
            OR continued_from_id IS NOT NULL
            OR suspended_by_pause_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION
            'Arena reconnect continuation history prevents schema downgrade'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER arena_reconnect_interval_normal_wave_consistency
    ON arena_reconnect_intervals;
DROP TRIGGER arena_normal_wave_reconnect_suspension ON arena_pauses;
DROP TRIGGER arena_pause_wave_reconnect_lock ON arena_pauses;
DROP TRIGGER arena_wave_members_normal_pause_guard ON arena_wave_members;
DROP FUNCTION arena_validate_normal_wave_reconnect_suspension();
DROP FUNCTION arena_normal_wave_reconnect_lock();
DROP FUNCTION arena_wave_member_normal_pause_guard();

DROP INDEX arena_reconnect_intervals_continued_from_key;
DROP INDEX arena_reconnect_intervals_root_presence_epoch_key;

ALTER TABLE arena_reconnect_intervals
    DROP CONSTRAINT arena_reconnect_intervals_suspension_check,
    DROP CONSTRAINT arena_reconnect_intervals_lineage_check,
    DROP CONSTRAINT arena_reconnect_intervals_suspended_by_pause_fk,
    DROP CONSTRAINT arena_reconnect_intervals_continued_from_fk,
    DROP CONSTRAINT arena_reconnect_intervals_segment_key,
    ADD CONSTRAINT arena_reconnect_intervals_number_key UNIQUE (
        pause_id,
        participant_id,
        interval_number
    );

-- Restore the version-30 root-only counter meaning before removing the
-- continuation columns used by the version-31 validator.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_validate_reconnect_slot_count()
RETURNS TRIGGER AS $$
DECLARE
    target_pause_id UUID;
    target_participant_id UUID;
    used_slots SMALLINT;
    interval_count INTEGER;
BEGIN
    target_pause_id := NEW.pause_id;
    target_participant_id := NEW.participant_id;

    SELECT slots_used
    INTO used_slots
    FROM arena_reconnect_slot_counters
    WHERE pause_id = target_pause_id AND participant_id = target_participant_id;

    SELECT COUNT(*)
    INTO interval_count
    FROM arena_reconnect_intervals
    WHERE pause_id = target_pause_id AND participant_id = target_participant_id;

    IF used_slots IS DISTINCT FROM interval_count THEN
        RAISE EXCEPTION 'Arena reconnect slot count differs from retained intervals'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_reconnect_interval_guard()
RETURNS TRIGGER AS $$
DECLARE
    pause_state VARCHAR(16);
    pause_scope_kind VARCHAR(16);
    pause_game_attempt_id UUID;
    pause_started_at TIMESTAMPTZ;
    live_state VARCHAR(16);
    live_epoch BIGINT;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT state, scope_kind, game_attempt_id, started_at
        INTO pause_state, pause_scope_kind, pause_game_attempt_id, pause_started_at
        FROM arena_pauses
        WHERE id = NEW.pause_id
            AND series_id = NEW.series_id
            AND roster_id = NEW.roster_id
        FOR UPDATE;

        IF pause_state IS DISTINCT FROM 'active'
            OR pause_scope_kind IS DISTINCT FROM 'game_attempt'
            OR pause_game_attempt_id IS DISTINCT FROM NEW.game_attempt_id
            OR NEW.opened_at < pause_started_at THEN
            RAISE EXCEPTION 'Arena reconnect interval requires its active Game pause'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT state, presence_epoch
        INTO live_state, live_epoch
        FROM arena_presence_states
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        SELECT slots_used
        INTO counter_slots
        FROM arena_reconnect_slot_counters
        WHERE pause_id = NEW.pause_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_state <> 'disconnected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch
            OR NEW.interval_number <> counter_slots + 1 THEN
            RAISE EXCEPTION 'Arena reconnect interval must consume the next live slot'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.presence_epoch IS DISTINCT FROM OLD.presence_epoch
        OR NEW.interval_number IS DISTINCT FROM OLD.interval_number
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'open'
        OR NEW.state NOT IN ('reconnected', 'expired', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena reconnect interval CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IN ('reconnected', 'expired') THEN
        SELECT state, presence_epoch
        INTO live_state, live_epoch
        FROM arena_presence_states
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id
        FOR UPDATE;

        IF live_state IS DISTINCT FROM 'disconnected'
            OR live_epoch IS DISTINCT FROM NEW.presence_epoch THEN
            RAISE EXCEPTION 'Arena reconnect terminal transition has stale presence CAS'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

ALTER TABLE arena_reconnect_intervals
    DROP COLUMN suspended_by_pause_id,
    DROP COLUMN continued_from_id,
    DROP COLUMN continuation_number;
