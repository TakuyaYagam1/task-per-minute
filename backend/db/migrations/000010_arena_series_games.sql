-- +goose Up

-- Series rows carry both roster and tournament identity so every participant
-- reference remains inside the Tournament-owned Arena boundary.
CREATE UNIQUE INDEX arena_rosters_id_tournament_id_idx
    ON arena_rosters (id, tournament_id);

CREATE TABLE arena_series (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    first_participant_id UUID NOT NULL,
    second_participant_id UUID NOT NULL,
    format VARCHAR(8) NOT NULL,
    state VARCHAR(32) NOT NULL DEFAULT 'planned',
    first_participant_wins SMALLINT NOT NULL DEFAULT 0,
    second_participant_wins SMALLINT NOT NULL DEFAULT 0,
    winner_id UUID,
    current_score_revision_id UUID,
    current_result_revision_id UUID,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    CONSTRAINT arena_series_id_roster_key UNIQUE (id, roster_id),
    CONSTRAINT arena_series_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_first_participant_fk FOREIGN KEY (roster_id, first_participant_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_second_participant_fk FOREIGN KEY (roster_id, second_participant_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_winner_fk FOREIGN KEY (roster_id, winner_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_participants_check CHECK (
        first_participant_id <> second_participant_id
        AND (
            winner_id IS NULL
            OR winner_id IN (first_participant_id, second_participant_id)
        )
    ),
    CONSTRAINT arena_series_format_check CHECK (format IN ('bo1', 'bo3')),
    CONSTRAINT arena_series_state_check CHECK (
        state IN (
            'planned',
            'locked',
            'draft',
            'ready',
            'active',
            'replay_required',
            'technical_pause',
            'completed',
            'cancelled'
        )
    ),
    CONSTRAINT arena_series_score_check CHECK (
        first_participant_wins >= 0
        AND second_participant_wins >= 0
        AND (
            (
                format = 'bo1'
                AND first_participant_wins <= 1
                AND second_participant_wins <= 1
                AND NOT (
                    first_participant_wins = 1
                    AND second_participant_wins = 1
                )
            )
            OR (
                format = 'bo3'
                AND first_participant_wins <= 2
                AND second_participant_wins <= 2
                AND NOT (
                    first_participant_wins = 2
                    AND second_participant_wins = 2
                )
            )
        )
    ),
    CONSTRAINT arena_series_result_check CHECK (
        (
            state = 'completed'
            AND winner_id IS NOT NULL
            AND current_score_revision_id IS NOT NULL
            AND current_result_revision_id IS NOT NULL
            AND (
                (
                    winner_id = first_participant_id
                    AND (
                        (format = 'bo1' AND first_participant_wins = 1)
                        OR (format = 'bo3' AND first_participant_wins = 2)
                    )
                )
                OR (
                    winner_id = second_participant_id
                    AND (
                        (format = 'bo1' AND second_participant_wins = 1)
                        OR (format = 'bo3' AND second_participant_wins = 2)
                    )
                )
            )
        )
        OR (
            state = 'cancelled'
            AND current_score_revision_id IS NOT NULL
            AND current_result_revision_id IS NOT NULL
        )
        OR (
            state NOT IN ('completed', 'cancelled')
            AND winner_id IS NULL
            AND current_result_revision_id IS NULL
        )
    ),
    CONSTRAINT arena_series_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_series_timestamps_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= COALESCE(started_at, created_at))
        AND (
            (state IN ('completed', 'cancelled') AND finished_at IS NOT NULL)
            OR (state NOT IN ('completed', 'cancelled') AND finished_at IS NULL)
        )
    )
);

CREATE TABLE arena_game_slots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    slot_number SMALLINT NOT NULL,
    category VARCHAR(32) NOT NULL,
    first_participant_wins_before SMALLINT NOT NULL DEFAULT 0,
    second_participant_wins_before SMALLINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_game_slots_id_series_roster_key UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_game_slots_series_position_key UNIQUE (series_id, slot_number),
    CONSTRAINT arena_game_slots_series_fk FOREIGN KEY (series_id, roster_id)
        REFERENCES arena_series(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_game_slots_position_check CHECK (slot_number BETWEEN 1 AND 3),
    CONSTRAINT arena_game_slots_category_check CHECK (
        category IN (
            'web',
            'crypto',
            'forensics',
            'reverse',
            'pwn',
            'steganography',
            'ppc',
            'osint',
            'mobile',
            'hardware',
            'misc'
        )
    ),
    CONSTRAINT arena_game_slots_score_check CHECK (
        first_participant_wins_before BETWEEN 0 AND 2
        AND second_participant_wins_before BETWEEN 0 AND 2
        AND NOT (
            first_participant_wins_before = 2
            AND second_participant_wins_before = 2
        )
    ),
    CONSTRAINT arena_game_slots_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_game_slots_timestamps_check CHECK (updated_at >= created_at)
);

CREATE TABLE arena_game_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slot_id UUID NOT NULL,
    series_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    attempt_number INTEGER NOT NULL,
    state VARCHAR(32) NOT NULL DEFAULT 'planned',
    result_reason VARCHAR(40),
    winner_id UUID,
    result_revision_id UUID,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    CONSTRAINT arena_game_attempts_slot_number_key UNIQUE (slot_id, attempt_number),
    CONSTRAINT arena_game_attempts_slot_fk FOREIGN KEY (slot_id, series_id, roster_id)
        REFERENCES arena_game_slots(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_game_attempts_winner_fk FOREIGN KEY (roster_id, winner_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_game_attempts_number_check CHECK (attempt_number >= 1),
    CONSTRAINT arena_game_attempts_state_check CHECK (
        state IN (
            'planned',
            'ready',
            'active',
            'paused',
            'completed',
            'void',
            'cancelled',
            'superseded'
        )
    ),
    CONSTRAINT arena_game_attempts_result_check CHECK (
        (
            state IN ('planned', 'ready', 'active', 'paused')
            AND result_reason IS NULL
            AND winner_id IS NULL
            AND result_revision_id IS NULL
        )
        OR (
            state = 'completed'
            AND result_reason IS NOT NULL
            AND result_reason IN ('solved', 'surrender', 'operator_forfeit')
            AND winner_id IS NOT NULL
            AND result_revision_id IS NOT NULL
        )
        OR (
            state = 'void'
            AND result_reason IS NOT NULL
            AND result_reason IN (
                'no_solve',
                'task_failure',
                'common_platform_failure',
                'disconnect',
                'execution_epoch_break'
            )
            AND winner_id IS NULL
            AND result_revision_id IS NOT NULL
        )
        OR (
            state = 'cancelled'
            AND result_reason IS NOT NULL
            AND result_reason IN (
                'no_show',
                'series_cancelled',
                'tournament_cancelled'
            )
            AND winner_id IS NULL
            AND result_revision_id IS NOT NULL
        )
        OR (
            state = 'superseded'
            AND result_reason IS NOT NULL
            AND result_reason = 'derived_revision_superseded'
            AND winner_id IS NULL
            AND result_revision_id IS NOT NULL
        )
    ),
    CONSTRAINT arena_game_attempts_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_game_attempts_timestamps_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= COALESCE(started_at, created_at))
        AND (
            (state IN ('completed', 'void', 'cancelled', 'superseded') AND finished_at IS NOT NULL)
            OR (state NOT IN ('completed', 'void', 'cancelled', 'superseded') AND finished_at IS NULL)
        )
    )
);

CREATE UNIQUE INDEX arena_game_attempts_one_non_terminal_idx
    ON arena_game_attempts (slot_id)
    WHERE state IN ('planned', 'ready', 'active', 'paused');

CREATE INDEX arena_game_attempts_slot_created_idx
    ON arena_game_attempts (slot_id, attempt_number, created_at);

-- +goose StatementBegin
CREATE FUNCTION arena_game_slot_identity_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena Game slots are stable Series history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.slot_number IS DISTINCT FROM OLD.slot_number
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena Game slot identity and position are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_game_slot_identity_guard
BEFORE UPDATE OR DELETE ON arena_game_slots
FOR EACH ROW EXECUTE FUNCTION arena_game_slot_identity_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_game_attempt_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_attempt_number INTEGER;
    previous_state VARCHAR(32);
BEGIN
    PERFORM 1
    FROM arena_game_slots
    WHERE id = NEW.slot_id
    FOR UPDATE;

    SELECT attempt_number, state
    INTO previous_attempt_number, previous_state
    FROM arena_game_attempts
    WHERE slot_id = NEW.slot_id
    ORDER BY attempt_number DESC
    LIMIT 1;

    IF previous_attempt_number IS NULL THEN
        IF NEW.attempt_number <> 1 THEN
            RAISE EXCEPTION 'first Arena Game attempt number must be 1'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.attempt_number <> previous_attempt_number + 1 OR previous_state <> 'void' THEN
        RAISE EXCEPTION 'Arena Game attempts must form a contiguous void-replay chain'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_game_attempt_insert_guard
BEFORE INSERT ON arena_game_attempts
FOR EACH ROW EXECUTE FUNCTION arena_game_attempt_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_game_attempt_identity_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena Game attempts are immutable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.slot_id IS DISTINCT FROM OLD.slot_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena Game attempt identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_game_attempt_identity_guard
BEFORE UPDATE OR DELETE ON arena_game_attempts
FOR EACH ROW EXECUTE FUNCTION arena_game_attempt_identity_guard();

-- +goose Down

DROP TABLE IF EXISTS arena_game_attempts;
DROP FUNCTION IF EXISTS arena_game_attempt_identity_guard();
DROP FUNCTION IF EXISTS arena_game_attempt_insert_guard();
DROP TABLE IF EXISTS arena_game_slots;
DROP FUNCTION IF EXISTS arena_game_slot_identity_guard();
DROP TABLE IF EXISTS arena_series;
DROP INDEX IF EXISTS arena_rosters_id_tournament_id_idx;
