-- +goose Up

CREATE TABLE arena_waves (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    revision_id UUID NOT NULL UNIQUE,
    revision BIGINT NOT NULL DEFAULT 1,
    state VARCHAR(32) NOT NULL DEFAULT 'planned',
    replaces_wave_id UUID UNIQUE REFERENCES arena_waves(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    paused_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    CONSTRAINT arena_waves_id_roster_key UNIQUE (id, roster_id),
    CONSTRAINT arena_waves_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_waves_replacement_check CHECK (replaces_wave_id IS DISTINCT FROM id),
    CONSTRAINT arena_waves_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_waves_state_check CHECK (
        state IN (
            'planned',
            'ready_window_open',
            'ready',
            'active',
            'paused',
            'completed',
            'ready_window_expired',
            'superseded'
        )
    ),
    CONSTRAINT arena_waves_state_evidence_check CHECK (
        (
            state IN ('planned', 'ready_window_open', 'ready', 'ready_window_expired')
            AND started_at IS NULL
            AND paused_at IS NULL
            AND closed_at IS NULL
        )
        OR (
            state = 'active'
            AND started_at IS NOT NULL
            AND paused_at IS NULL
            AND closed_at IS NULL
        )
        OR (
            state = 'paused'
            AND started_at IS NOT NULL
            AND paused_at IS NOT NULL
            AND closed_at IS NULL
        )
        OR (
            state = 'completed'
            AND started_at IS NOT NULL
            AND paused_at IS NULL
            AND closed_at IS NOT NULL
        )
        OR (
            state = 'superseded'
            AND paused_at IS NULL
            AND closed_at IS NOT NULL
        )
    ),
    CONSTRAINT arena_waves_timestamps_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (paused_at IS NULL OR paused_at >= started_at)
        AND (closed_at IS NULL OR closed_at >= COALESCE(started_at, created_at))
    )
);

CREATE TABLE arena_wave_members (
    wave_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (wave_id, participant_id),
    CONSTRAINT arena_wave_members_wave_roster_participant_key
        UNIQUE (wave_id, roster_id, participant_id),
    CONSTRAINT arena_wave_members_wave_fk FOREIGN KEY (wave_id, roster_id)
        REFERENCES arena_waves(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_wave_members_participant_fk FOREIGN KEY (roster_id, participant_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT
);

CREATE TABLE arena_ready_windows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wave_id UUID NOT NULL UNIQUE,
    roster_id UUID NOT NULL,
    revision_id UUID NOT NULL UNIQUE,
    state VARCHAR(16) NOT NULL DEFAULT 'open',
    opened_at TIMESTAMPTZ NOT NULL,
    deadline TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_ready_windows_id_wave_roster_key UNIQUE (id, wave_id, roster_id),
    CONSTRAINT arena_ready_windows_wave_fk FOREIGN KEY (wave_id, roster_id)
        REFERENCES arena_waves(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_ready_windows_state_check CHECK (
        state IN ('open', 'consumed', 'expired', 'superseded')
    ),
    CONSTRAINT arena_ready_windows_interval_check CHECK (
        deadline > opened_at
        AND created_at >= opened_at
    ),
    CONSTRAINT arena_ready_windows_consumption_check CHECK (
        (
            state = 'consumed'
            AND consumed_at IS NOT NULL
            AND consumed_at BETWEEN opened_at AND deadline
        )
        OR (
            state <> 'consumed'
            AND consumed_at IS NULL
        )
    )
);

CREATE TABLE arena_wave_readiness (
    ready_window_id UUID NOT NULL,
    wave_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    ready_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (ready_window_id, participant_id),
    CONSTRAINT arena_wave_readiness_window_fk FOREIGN KEY (ready_window_id, wave_id, roster_id)
        REFERENCES arena_ready_windows(id, wave_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_wave_readiness_member_fk FOREIGN KEY (wave_id, roster_id, participant_id)
        REFERENCES arena_wave_members(wave_id, roster_id, participant_id) ON DELETE RESTRICT
);

CREATE INDEX arena_waves_tournament_state_idx
    ON arena_waves (tournament_id, state, created_at);

CREATE INDEX arena_wave_members_participant_idx
    ON arena_wave_members (roster_id, participant_id, wave_id);

-- +goose StatementBegin
CREATE FUNCTION arena_wave_identity_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena Waves are retained execution history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.replaces_wave_id IS DISTINCT FROM OLD.replaces_wave_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena Wave identity and lineage are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at THEN
        RAISE EXCEPTION 'Arena Wave start is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.closed_at IS NOT NULL AND NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN
        RAISE EXCEPTION 'Arena Wave closure is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_wave_identity_guard
BEFORE UPDATE OR DELETE ON arena_waves
FOR EACH ROW EXECUTE FUNCTION arena_wave_identity_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_ready_window_lifecycle_guard() RETURNS TRIGGER AS $$
DECLARE
    member_count INTEGER;
    ready_count INTEGER;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena ready windows are retained execution history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline IS DISTINCT FROM OLD.deadline
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena ready-window identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'open' AND NEW.state IN ('consumed', 'expired', 'superseded'))
            OR (OLD.state IN ('consumed', 'expired') AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid Arena ready-window transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'consumed' AND OLD.state <> 'consumed' THEN
        SELECT COUNT(*) INTO member_count
        FROM arena_wave_members
        WHERE wave_id = NEW.wave_id;

        SELECT COUNT(*) INTO ready_count
        FROM arena_wave_readiness
        WHERE ready_window_id = NEW.id;

        IF member_count < 2 OR ready_count <> member_count THEN
            RAISE EXCEPTION 'all Arena Wave members must be ready before start'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_ready_window_lifecycle_guard
BEFORE UPDATE OR DELETE ON arena_ready_windows
FOR EACH ROW EXECUTE FUNCTION arena_ready_window_lifecycle_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_wave_readiness_guard() RETURNS TRIGGER AS $$
DECLARE
    window_state VARCHAR(16);
    window_opened_at TIMESTAMPTZ;
    window_deadline TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'Arena Wave readiness evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state, opened_at, deadline
    INTO window_state, window_opened_at, window_deadline
    FROM arena_ready_windows
    WHERE id = NEW.ready_window_id
    FOR UPDATE;

    IF window_state <> 'open'
        OR NEW.ready_at < window_opened_at
        OR NEW.ready_at > window_deadline THEN
        RAISE EXCEPTION 'Arena Wave readiness is outside the current open window'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_wave_readiness_guard
BEFORE INSERT OR UPDATE ON arena_wave_readiness
FOR EACH ROW EXECUTE FUNCTION arena_wave_readiness_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_clear_closed_window_readiness() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.state IN ('expired', 'superseded') AND NEW.state IS DISTINCT FROM OLD.state THEN
        DELETE FROM arena_wave_readiness
        WHERE ready_window_id = NEW.id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_clear_closed_window_readiness
AFTER UPDATE OF state ON arena_ready_windows
FOR EACH ROW EXECUTE FUNCTION arena_clear_closed_window_readiness();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_wave_window() RETURNS TRIGGER AS $$
DECLARE
    target_wave_id UUID;
    wave_tournament_id UUID;
    wave_roster_id UUID;
    wave_replaces_id UUID;
    wave_state VARCHAR(32);
    wave_started_at TIMESTAMPTZ;
    window_state VARCHAR(16);
    window_consumed_at TIMESTAMPTZ;
    member_count INTEGER;
    replaced_tournament_id UUID;
    replaced_roster_id UUID;
    replaced_state VARCHAR(32);
BEGIN
    IF TG_TABLE_NAME = 'arena_waves' THEN
        target_wave_id := NEW.id;
    ELSE
        target_wave_id := NEW.wave_id;
    END IF;

    SELECT
        wave.tournament_id,
        wave.roster_id,
        wave.replaces_wave_id,
        wave.state,
        wave.started_at,
        ready_window.state,
        ready_window.consumed_at
    INTO
        wave_tournament_id,
        wave_roster_id,
        wave_replaces_id,
        wave_state,
        wave_started_at,
        window_state,
        window_consumed_at
    FROM arena_waves AS wave
    LEFT JOIN arena_ready_windows AS ready_window ON ready_window.wave_id = wave.id
    WHERE wave.id = target_wave_id;

    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT COUNT(*) INTO member_count
    FROM arena_wave_members
    WHERE wave_id = target_wave_id;

    IF wave_state = 'planned' THEN
        IF window_state IS NOT NULL THEN
            RAISE EXCEPTION 'planned Arena Wave cannot have a ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state IN ('ready_window_open', 'ready') THEN
        IF window_state <> 'open' OR member_count < 2 THEN
            RAISE EXCEPTION 'open Arena Wave requires members and an open ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state IN ('active', 'paused', 'completed') THEN
        IF window_state <> 'consumed'
            OR window_consumed_at IS DISTINCT FROM wave_started_at
            OR member_count < 2 THEN
            RAISE EXCEPTION 'started Arena Wave requires one shared consumed ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state = 'ready_window_expired' THEN
        IF window_state <> 'expired' OR member_count < 2 THEN
            RAISE EXCEPTION 'expired Arena Wave requires its expired ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF wave_state = 'superseded' THEN
        IF window_state <> 'superseded' OR member_count < 2 THEN
            RAISE EXCEPTION 'superseded Arena Wave requires its superseded ready window'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF wave_replaces_id IS NOT NULL THEN
        SELECT tournament_id, roster_id, state
        INTO replaced_tournament_id, replaced_roster_id, replaced_state
        FROM arena_waves
        WHERE id = wave_replaces_id;

        IF replaced_state <> 'superseded'
            OR replaced_tournament_id IS DISTINCT FROM wave_tournament_id
            OR replaced_roster_id IS DISTINCT FROM wave_roster_id THEN
            RAISE EXCEPTION 'Arena Wave replacement must continue one superseded lineage'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_waves_window_consistency
AFTER INSERT OR UPDATE ON arena_waves
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_wave_window();

CREATE CONSTRAINT TRIGGER arena_ready_windows_wave_consistency
AFTER INSERT OR UPDATE ON arena_ready_windows
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_wave_window();

-- +goose Down

DROP TABLE IF EXISTS arena_wave_readiness;
DROP TABLE IF EXISTS arena_ready_windows;
DROP TABLE IF EXISTS arena_wave_members;
DROP TABLE IF EXISTS arena_waves;
DROP FUNCTION IF EXISTS arena_validate_wave_window();
DROP FUNCTION IF EXISTS arena_clear_closed_window_readiness();
DROP FUNCTION IF EXISTS arena_wave_readiness_guard();
DROP FUNCTION IF EXISTS arena_ready_window_lifecycle_guard();
DROP FUNCTION IF EXISTS arena_wave_identity_guard();
