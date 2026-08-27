-- +goose Up

LOCK TABLE arena_series, arena_game_attempts IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE arena_presence_states (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    state VARCHAR(16) NOT NULL,
    presence_epoch BIGINT NOT NULL DEFAULT 1,
    revision BIGINT NOT NULL DEFAULT 1,
    connected_at TIMESTAMPTZ NOT NULL,
    disconnected_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_presence_states_series_participant_key
        UNIQUE (series_id, participant_id),
    CONSTRAINT arena_presence_states_identity_key
        UNIQUE (id, series_id, roster_id, participant_id),
    CONSTRAINT arena_presence_states_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_presence_states_series_fk FOREIGN KEY (series_id, roster_id)
        REFERENCES arena_series(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_presence_states_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_presence_states_state_check CHECK (
        (
            state = 'connected'
            AND disconnected_at IS NULL
        )
        OR (
            state = 'disconnected'
            AND disconnected_at IS NOT NULL
            AND disconnected_at >= connected_at
        )
    ),
    CONSTRAINT arena_presence_states_revision_check CHECK (
        presence_epoch >= 1 AND revision >= 1
    ),
    CONSTRAINT arena_presence_states_timestamp_check CHECK (
        updated_at >= connected_at
        AND (disconnected_at IS NULL OR updated_at >= disconnected_at)
    )
);

CREATE TABLE arena_pauses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    scope_kind VARCHAR(16) NOT NULL,
    scope_id UUID NOT NULL,
    wave_id UUID,
    series_id UUID,
    game_attempt_id UUID,
    parent_pause_id UUID REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    depth SMALLINT NOT NULL DEFAULT 0,
    reason VARCHAR(32) NOT NULL,
    paused_from_state VARCHAR(32) NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    current_revision_id UUID NOT NULL UNIQUE,
    revision BIGINT NOT NULL DEFAULT 1,
    started_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_pauses_identity_key UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_pauses_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_pauses_wave_fk FOREIGN KEY (wave_id, roster_id)
        REFERENCES arena_waves(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_pauses_series_fk FOREIGN KEY (series_id, roster_id)
        REFERENCES arena_series(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_pauses_attempt_fk FOREIGN KEY (
        game_attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_game_attempts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_pauses_scope_check CHECK (
        (
            scope_kind = 'tournament'
            AND scope_id = tournament_id
            AND wave_id IS NULL
            AND series_id IS NULL
            AND game_attempt_id IS NULL
        )
        OR (
            scope_kind = 'wave'
            AND scope_id = wave_id
            AND wave_id IS NOT NULL
            AND series_id IS NULL
            AND game_attempt_id IS NULL
        )
        OR (
            scope_kind = 'series'
            AND scope_id = series_id
            AND wave_id IS NULL
            AND series_id IS NOT NULL
            AND game_attempt_id IS NULL
        )
        OR (
            scope_kind = 'game_attempt'
            AND scope_id = game_attempt_id
            AND wave_id IS NULL
            AND series_id IS NOT NULL
            AND game_attempt_id IS NOT NULL
        )
    ),
    CONSTRAINT arena_pauses_depth_check CHECK (depth BETWEEN 0 AND 3),
    CONSTRAINT arena_pauses_reason_check CHECK (
        reason IN ('operator', 'disconnect', 'platform', 'execution_epoch')
    ),
    CONSTRAINT arena_pauses_origin_check CHECK (
        paused_from_state = BTRIM(paused_from_state) AND paused_from_state <> ''
    ),
    CONSTRAINT arena_pauses_state_check CHECK (
        (state = 'active' AND resolved_at IS NULL)
        OR (state IN ('resumed', 'cancelled') AND resolved_at IS NOT NULL)
    ),
    CONSTRAINT arena_pauses_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_pauses_timestamps_check CHECK (
        started_at <= created_at
        AND updated_at >= created_at
        AND (resolved_at IS NULL OR resolved_at >= started_at)
    )
);

CREATE UNIQUE INDEX arena_pauses_one_active_scope_idx
    ON arena_pauses (scope_kind, scope_id)
    WHERE state = 'active';

CREATE INDEX arena_pauses_parent_state_idx
    ON arena_pauses (parent_pause_id, state, depth, created_at);

CREATE TABLE arena_pause_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pause_id UUID NOT NULL REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    previous_revision_id UUID,
    revision_number BIGINT NOT NULL,
    state VARCHAR(16) NOT NULL,
    transition_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_pause_revisions_identity_key UNIQUE (id, pause_id),
    CONSTRAINT arena_pause_revisions_number_key UNIQUE (pause_id, revision_number),
    CONSTRAINT arena_pause_revisions_previous_fk FOREIGN KEY (
        previous_revision_id,
        pause_id
    ) REFERENCES arena_pause_revisions(id, pause_id) ON DELETE RESTRICT,
    CONSTRAINT arena_pause_revisions_number_check CHECK (
        revision_number >= 1
        AND (
            (revision_number = 1 AND previous_revision_id IS NULL)
            OR (revision_number > 1 AND previous_revision_id IS NOT NULL)
        )
    ),
    CONSTRAINT arena_pause_revisions_state_check CHECK (
        (
            state = 'active'
            AND transition_reason IS NULL
        )
        OR (
            state IN ('resumed', 'cancelled')
            AND transition_reason = BTRIM(transition_reason)
            AND transition_reason <> ''
        )
    )
);

ALTER TABLE arena_pauses
    ADD CONSTRAINT arena_pauses_current_revision_fk FOREIGN KEY (
        current_revision_id,
        id
    ) REFERENCES arena_pause_revisions(id, pause_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE arena_pause_presence_snapshots (
    pause_id UUID NOT NULL REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    presence_state VARCHAR(16) NOT NULL,
    presence_epoch BIGINT NOT NULL,
    presence_revision BIGINT NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pause_id, participant_id),
    CONSTRAINT arena_pause_presence_snapshots_identity_key
        UNIQUE (pause_id, participant_id, presence_epoch),
    CONSTRAINT arena_pause_presence_snapshots_live_presence_fk FOREIGN KEY (
        series_id,
        participant_id
    ) REFERENCES arena_presence_states (
        series_id,
        participant_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_pause_presence_snapshots_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_pause_presence_snapshots_state_check CHECK (
        presence_state IN ('connected', 'disconnected')
    ),
    CONSTRAINT arena_pause_presence_snapshots_revision_check CHECK (
        presence_epoch >= 1 AND presence_revision >= 1
    ),
    CONSTRAINT arena_pause_presence_snapshots_timestamps_check CHECK (
        captured_at <= created_at
    )
);

CREATE TABLE arena_reconnect_slot_counters (
    pause_id UUID NOT NULL REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    slot_limit SMALLINT NOT NULL,
    slots_used SMALLINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pause_id, participant_id),
    CONSTRAINT arena_reconnect_slot_counters_snapshot_fk FOREIGN KEY (
        pause_id,
        participant_id
    ) REFERENCES arena_pause_presence_snapshots (
        pause_id,
        participant_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_reconnect_slot_counters_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_reconnect_slot_counters_value_check CHECK (
        slot_limit BETWEEN 1 AND 10
        AND slots_used BETWEEN 0 AND slot_limit
        AND revision >= 1
    ),
    CONSTRAINT arena_reconnect_slot_counters_timestamps_check CHECK (
        updated_at >= created_at
    )
);

CREATE TABLE arena_reconnect_intervals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pause_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    game_attempt_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    presence_epoch BIGINT NOT NULL,
    interval_number SMALLINT NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'open',
    opened_at TIMESTAMPTZ NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_reconnect_intervals_identity_key
        UNIQUE (id, pause_id, participant_id),
    CONSTRAINT arena_reconnect_intervals_number_key
        UNIQUE (pause_id, participant_id, interval_number),
    CONSTRAINT arena_reconnect_intervals_counter_fk FOREIGN KEY (
        pause_id,
        participant_id
    ) REFERENCES arena_reconnect_slot_counters (
        pause_id,
        participant_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_reconnect_intervals_pause_fk FOREIGN KEY (
        pause_id,
        series_id,
        roster_id
    ) REFERENCES arena_pauses(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_reconnect_intervals_attempt_fk FOREIGN KEY (
        game_attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_game_attempts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_reconnect_intervals_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_reconnect_intervals_state_check CHECK (
        (state = 'open' AND closed_at IS NULL)
        OR (
            state IN ('reconnected', 'expired', 'cancelled')
            AND closed_at IS NOT NULL
        )
    ),
    CONSTRAINT arena_reconnect_intervals_revision_check CHECK (
        presence_epoch >= 1 AND interval_number >= 1 AND revision >= 1
    ),
    CONSTRAINT arena_reconnect_intervals_timestamps_check CHECK (
        deadline_at > opened_at
        AND created_at >= opened_at
        AND updated_at >= created_at
        AND (closed_at IS NULL OR closed_at >= opened_at)
    )
);

CREATE UNIQUE INDEX arena_reconnect_intervals_one_open_idx
    ON arena_reconnect_intervals (pause_id, participant_id)
    WHERE state = 'open';

CREATE INDEX arena_reconnect_intervals_deadline_idx
    ON arena_reconnect_intervals (deadline_at, id)
    WHERE state = 'open';

CREATE TABLE arena_pause_clocks (
    pause_id UUID PRIMARY KEY REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    game_attempt_id UUID NOT NULL,
    original_deadline TIMESTAMPTZ NOT NULL,
    frozen_at TIMESTAMPTZ NOT NULL,
    frozen_remaining_ms BIGINT NOT NULL,
    resumed_at TIMESTAMPTZ,
    resumed_deadline TIMESTAMPTZ,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_pause_clocks_attempt_fk FOREIGN KEY (game_attempt_id)
        REFERENCES arena_game_attempts(id) ON DELETE RESTRICT,
    CONSTRAINT arena_pause_clocks_frozen_check CHECK (
        original_deadline > frozen_at
        AND frozen_remaining_ms > 0
        AND frozen_remaining_ms = FLOOR(
            EXTRACT(EPOCH FROM (original_deadline - frozen_at)) * 1000
        )::BIGINT
    ),
    CONSTRAINT arena_pause_clocks_resume_check CHECK (
        (
            resumed_at IS NULL
            AND resumed_deadline IS NULL
        )
        OR (
            resumed_at IS NOT NULL
            AND resumed_deadline = resumed_at
                + (frozen_remaining_ms * INTERVAL '1 millisecond')
        )
    ),
    CONSTRAINT arena_pause_clocks_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_pause_clocks_timestamps_check CHECK (
        created_at >= frozen_at
        AND updated_at >= created_at
        AND (resumed_at IS NULL OR resumed_at >= frozen_at)
    )
);

CREATE TABLE arena_resume_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pause_id UUID NOT NULL REFERENCES arena_pauses(id) ON DELETE RESTRICT,
    decision_number BIGINT NOT NULL,
    first_participant_id UUID NOT NULL,
    second_participant_id UUID NOT NULL,
    first_pre_pause_state VARCHAR(16) NOT NULL,
    second_pre_pause_state VARCHAR(16) NOT NULL,
    first_live_state VARCHAR(16) NOT NULL,
    second_live_state VARCHAR(16) NOT NULL,
    first_presence_epoch BIGINT NOT NULL,
    second_presence_epoch BIGINT NOT NULL,
    first_presence_revision BIGINT NOT NULL,
    second_presence_revision BIGINT NOT NULL,
    first_reconnect_interval_id UUID,
    second_reconnect_interval_id UUID,
    action VARCHAR(16) NOT NULL,
    decided_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_resume_decisions_number_key UNIQUE (pause_id, decision_number),
    CONSTRAINT arena_resume_decisions_first_interval_fk FOREIGN KEY (
        first_reconnect_interval_id,
        pause_id,
        first_participant_id
    ) REFERENCES arena_reconnect_intervals (
        id,
        pause_id,
        participant_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_resume_decisions_second_interval_fk FOREIGN KEY (
        second_reconnect_interval_id,
        pause_id,
        second_participant_id
    ) REFERENCES arena_reconnect_intervals (
        id,
        pause_id,
        participant_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_resume_decisions_participants_check CHECK (
        first_participant_id <> second_participant_id
    ),
    CONSTRAINT arena_resume_decisions_state_check CHECK (
        first_pre_pause_state IN ('connected', 'disconnected')
        AND second_pre_pause_state IN ('connected', 'disconnected')
        AND first_live_state IN ('connected', 'disconnected')
        AND second_live_state IN ('connected', 'disconnected')
    ),
    CONSTRAINT arena_resume_decisions_revision_check CHECK (
        decision_number >= 1
        AND first_presence_epoch >= 1
        AND second_presence_epoch >= 1
        AND first_presence_revision >= 1
        AND second_presence_revision >= 1
    ),
    CONSTRAINT arena_resume_decisions_interval_check CHECK (
        (
            first_live_state = 'connected'
            AND first_reconnect_interval_id IS NULL
        )
        OR (
            first_live_state = 'disconnected'
            AND first_reconnect_interval_id IS NOT NULL
        )
    ),
    CONSTRAINT arena_resume_decisions_second_interval_check CHECK (
        (
            second_live_state = 'connected'
            AND second_reconnect_interval_id IS NULL
        )
        OR (
            second_live_state = 'disconnected'
            AND second_reconnect_interval_id IS NOT NULL
        )
    ),
    CONSTRAINT arena_resume_decisions_matrix_check CHECK (
        (
            first_live_state = 'connected'
            AND second_live_state = 'connected'
            AND action = 'resume'
        )
        OR (
            first_live_state = 'disconnected'
            AND second_live_state = 'connected'
            AND action = 'wait_first'
        )
        OR (
            first_live_state = 'connected'
            AND second_live_state = 'disconnected'
            AND action = 'wait_second'
        )
        OR (
            first_live_state = 'disconnected'
            AND second_live_state = 'disconnected'
            AND action = 'wait_both'
        )
    ),
    CONSTRAINT arena_resume_decisions_timestamps_check CHECK (
        decided_at <= created_at
    )
);

-- +goose StatementBegin
CREATE FUNCTION arena_presence_state_guard() RETURNS TRIGGER AS $$
DECLARE
    series_first UUID;
    series_second UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena live presence is durable recovery state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT first_participant_id, second_participant_id
        INTO series_first, series_second
        FROM arena_series
        WHERE id = NEW.series_id;

        IF NEW.participant_id NOT IN (series_first, series_second) THEN
            RAISE EXCEPTION 'Arena presence participant is outside the Series'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.state IS NOT DISTINCT FROM OLD.state
        OR NEW.presence_epoch <> OLD.presence_epoch + 1
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena presence CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'connected'
        AND NEW.connected_at <= OLD.connected_at THEN
        RAISE EXCEPTION 'Arena reconnect must advance connected_at'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'disconnected'
        AND NEW.connected_at IS DISTINCT FROM OLD.connected_at THEN
        RAISE EXCEPTION 'Arena disconnect cannot rewrite connection start'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_presence_state_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_presence_states
FOR EACH ROW EXECUTE FUNCTION arena_presence_state_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_pause_graph_guard() RETURNS TRIGGER AS $$
DECLARE
    parent_row arena_pauses%ROWTYPE;
    active_descendants INTEGER;
    latest_action VARCHAR(16);
    clock_resumed_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena pauses are durable recovery state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        IF NEW.parent_pause_id IS NULL THEN
            IF NEW.depth <> 0 THEN
                RAISE EXCEPTION 'root Arena pause depth must be zero'
                    USING ERRCODE = 'check_violation';
            END IF;
            RETURN NEW;
        END IF;

        SELECT *
        INTO parent_row
        FROM arena_pauses
        WHERE id = NEW.parent_pause_id
        FOR UPDATE;

        IF parent_row.state <> 'active'
            OR NEW.tournament_id IS DISTINCT FROM parent_row.tournament_id
            OR NEW.roster_id IS DISTINCT FROM parent_row.roster_id
            OR NEW.depth <> parent_row.depth + 1
            OR NOT (
                (parent_row.scope_kind = 'tournament' AND NEW.scope_kind IN ('wave', 'series'))
                OR (parent_row.scope_kind = 'wave' AND NEW.scope_kind = 'series')
                OR (parent_row.scope_kind = 'series' AND NEW.scope_kind = 'game_attempt')
            ) THEN
            RAISE EXCEPTION 'invalid Arena child pause graph edge'
                USING ERRCODE = 'check_violation';
        END IF;

        IF parent_row.series_id IS NOT NULL
            AND NEW.series_id IS DISTINCT FROM parent_row.series_id THEN
            RAISE EXCEPTION 'Arena child pause must retain Series identity'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.scope_kind IS DISTINCT FROM OLD.scope_kind
        OR NEW.scope_id IS DISTINCT FROM OLD.scope_id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.parent_pause_id IS DISTINCT FROM OLD.parent_pause_id
        OR NEW.depth IS DISTINCT FROM OLD.depth
        OR NEW.reason IS DISTINCT FROM OLD.reason
        OR NEW.paused_from_state IS DISTINCT FROM OLD.paused_from_state
        OR NEW.started_at IS DISTINCT FROM OLD.started_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.state <> 'active'
        OR NEW.state NOT IN ('resumed', 'cancelled')
        OR NEW.revision <> OLD.revision + 1
        OR NEW.current_revision_id IS NOT DISTINCT FROM OLD.current_revision_id
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena pause CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'resumed' THEN
        WITH RECURSIVE descendants AS (
            SELECT id, state
            FROM arena_pauses
            WHERE parent_pause_id = NEW.id
            UNION ALL
            SELECT child.id, child.state
            FROM arena_pauses AS child
            JOIN descendants AS parent ON child.parent_pause_id = parent.id
        )
        SELECT COUNT(*)
        INTO active_descendants
        FROM descendants
        WHERE state = 'active';

        IF active_descendants <> 0 THEN
            RAISE EXCEPTION 'Arena parent pause cannot resume with active descendants'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.series_id IS NOT NULL THEN
            SELECT action
            INTO latest_action
            FROM arena_resume_decisions
            WHERE pause_id = NEW.id
            ORDER BY decision_number DESC
            LIMIT 1;

            IF latest_action <> 'resume' THEN
                RAISE EXCEPTION 'Arena pause resume requires a complete resume decision'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        IF NEW.scope_kind = 'game_attempt' THEN
            SELECT resumed_at
            INTO clock_resumed_at
            FROM arena_pause_clocks
            WHERE pause_id = NEW.id;

            IF clock_resumed_at IS NULL THEN
                RAISE EXCEPTION 'Arena Game resume requires restored frozen time'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_graph_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_pauses
FOR EACH ROW EXECUTE FUNCTION arena_pause_graph_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_pause_revision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
BEGIN
    PERFORM 1 FROM arena_pauses WHERE id = NEW.pause_id FOR UPDATE;

    IF NEW.previous_revision_id IS NULL THEN
        IF NEW.revision_number <> 1 OR NEW.state <> 'active' THEN
            RAISE EXCEPTION 'Arena pause lineage must start active at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        SELECT revision_number
        INTO previous_number
        FROM arena_pause_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena pause revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_revision_insert_guard
BEFORE INSERT ON arena_pause_revisions
FOR EACH ROW EXECUTE FUNCTION arena_pause_revision_insert_guard();

CREATE TRIGGER arena_pause_revisions_append_only
BEFORE UPDATE OR DELETE ON arena_pause_revisions
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_pause_revision() RETURNS TRIGGER AS $$
DECLARE
    target_pause_id UUID;
    pause_row arena_pauses%ROWTYPE;
    revision_row arena_pause_revisions%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'arena_pauses' THEN
        target_pause_id := NEW.id;
    ELSE
        target_pause_id := NEW.pause_id;
    END IF;

    SELECT * INTO pause_row FROM arena_pauses WHERE id = target_pause_id;
    SELECT *
    INTO revision_row
    FROM arena_pause_revisions
    WHERE id = pause_row.current_revision_id;

    IF revision_row.id IS NULL
        OR revision_row.pause_id IS DISTINCT FROM pause_row.id
        OR revision_row.revision_number IS DISTINCT FROM pause_row.revision
        OR revision_row.state IS DISTINCT FROM pause_row.state
        OR (
            TG_TABLE_NAME = 'arena_pause_revisions'
            AND revision_row.id IS DISTINCT FROM NEW.id
        ) THEN
        RAISE EXCEPTION 'Arena pause current revision is inconsistent'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_pauses_revision_consistency
AFTER INSERT OR UPDATE ON arena_pauses
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_pause_revision();

CREATE CONSTRAINT TRIGGER arena_pause_revisions_pause_consistency
AFTER INSERT ON arena_pause_revisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_pause_revision();

-- +goose StatementBegin
CREATE FUNCTION arena_pause_presence_snapshot_guard() RETURNS TRIGGER AS $$
DECLARE
    pause_series_id UUID;
    pause_roster_id UUID;
    pause_tournament_id UUID;
    live_tournament_id UUID;
    live_roster_id UUID;
    live_state VARCHAR(16);
    live_epoch BIGINT;
    live_revision BIGINT;
BEGIN
    SELECT series_id, roster_id, tournament_id
    INTO pause_series_id, pause_roster_id, pause_tournament_id
    FROM arena_pauses
    WHERE id = NEW.pause_id;

    IF pause_roster_id IS DISTINCT FROM NEW.roster_id
        OR (
            pause_series_id IS NOT NULL
            AND pause_series_id IS DISTINCT FROM NEW.series_id
        ) THEN
        RAISE EXCEPTION 'Arena pause snapshot crosses roster boundary'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament_id, roster_id, state, presence_epoch, revision
    INTO live_tournament_id, live_roster_id, live_state, live_epoch, live_revision
    FROM arena_presence_states
    WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id;

    IF live_tournament_id IS DISTINCT FROM pause_tournament_id
        OR live_roster_id IS DISTINCT FROM pause_roster_id
        OR live_state IS DISTINCT FROM NEW.presence_state
        OR live_epoch IS DISTINCT FROM NEW.presence_epoch
        OR live_revision IS DISTINCT FROM NEW.presence_revision THEN
        RAISE EXCEPTION 'Arena pre-pause snapshot must capture current presence CAS'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_presence_snapshot_guard
BEFORE INSERT ON arena_pause_presence_snapshots
FOR EACH ROW EXECUTE FUNCTION arena_pause_presence_snapshot_guard();

CREATE TRIGGER arena_pause_presence_snapshots_append_only
BEFORE UPDATE OR DELETE ON arena_pause_presence_snapshots
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_pause_presence_snapshot() RETURNS TRIGGER AS $$
DECLARE
    target_pause_id UUID;
    pause_row arena_pauses%ROWTYPE;
    expected_count INTEGER;
    actual_count INTEGER;
    invalid_count INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'arena_pauses' THEN
        target_pause_id := NEW.id;
    ELSE
        target_pause_id := NEW.pause_id;
    END IF;

    SELECT * INTO pause_row FROM arena_pauses WHERE id = target_pause_id;

    IF pause_row.scope_kind IN ('series', 'game_attempt') THEN
        expected_count := 2;
        SELECT COUNT(*)
        INTO invalid_count
        FROM arena_pause_presence_snapshots AS snapshot
        JOIN arena_series AS series ON series.id = pause_row.series_id
        WHERE snapshot.pause_id = target_pause_id
            AND snapshot.participant_id NOT IN (
                series.first_participant_id,
                series.second_participant_id
            );
    ELSIF pause_row.scope_kind = 'wave' THEN
        SELECT COUNT(*) INTO expected_count
        FROM arena_wave_members
        WHERE wave_id = pause_row.wave_id;

        SELECT COUNT(*) INTO invalid_count
        FROM arena_pause_presence_snapshots AS snapshot
        WHERE snapshot.pause_id = target_pause_id
            AND NOT EXISTS (
                SELECT 1
                FROM arena_wave_members AS member
                WHERE member.wave_id = pause_row.wave_id
                    AND member.participant_id = snapshot.participant_id
            );
    ELSE
        SELECT COUNT(*) INTO expected_count
        FROM arena_participants
        WHERE roster_id = pause_row.roster_id AND attendance <> 'withdrawn';

        SELECT COUNT(*) INTO invalid_count
        FROM arena_pause_presence_snapshots AS snapshot
        WHERE snapshot.pause_id = target_pause_id
            AND NOT EXISTS (
                SELECT 1
                FROM arena_participants AS participant
                WHERE participant.roster_id = pause_row.roster_id
                    AND participant.id = snapshot.participant_id
                    AND participant.attendance <> 'withdrawn'
            );
    END IF;

    SELECT COUNT(*) INTO actual_count
    FROM arena_pause_presence_snapshots
    WHERE pause_id = target_pause_id;

    IF expected_count < 1 OR actual_count <> expected_count OR invalid_count <> 0 THEN
        RAISE EXCEPTION 'Arena pause requires a complete pre-pause presence snapshot'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_pauses_presence_snapshot_consistency
AFTER INSERT OR UPDATE ON arena_pauses
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_pause_presence_snapshot();

CREATE CONSTRAINT TRIGGER arena_pause_snapshots_pause_consistency
AFTER INSERT ON arena_pause_presence_snapshots
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_pause_presence_snapshot();

-- +goose StatementBegin
CREATE FUNCTION arena_reconnect_slot_counter_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect slot counters are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.slot_limit IS DISTINCT FROM OLD.slot_limit
        OR NEW.slots_used <> OLD.slots_used + 1
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena reconnect slot CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_reconnect_slot_counter_guard
BEFORE UPDATE OR DELETE ON arena_reconnect_slot_counters
FOR EACH ROW EXECUTE FUNCTION arena_reconnect_slot_counter_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_reconnect_interval_guard() RETURNS TRIGGER AS $$
DECLARE
    live_state VARCHAR(16);
    live_epoch BIGINT;
    counter_slots SMALLINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena reconnect intervals are durable history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT state, presence_epoch
        INTO live_state, live_epoch
        FROM arena_presence_states
        WHERE series_id = NEW.series_id AND participant_id = NEW.participant_id;

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

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_reconnect_interval_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_reconnect_intervals
FOR EACH ROW EXECUTE FUNCTION arena_reconnect_interval_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_reconnect_slot_count() RETURNS TRIGGER AS $$
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

CREATE CONSTRAINT TRIGGER arena_reconnect_counters_interval_consistency
AFTER INSERT OR UPDATE ON arena_reconnect_slot_counters
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_reconnect_slot_count();

CREATE CONSTRAINT TRIGGER arena_reconnect_intervals_counter_consistency
AFTER INSERT OR UPDATE ON arena_reconnect_intervals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_reconnect_slot_count();

-- +goose StatementBegin
CREATE FUNCTION arena_pause_clock_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena frozen clock evidence is durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
        OR NEW.original_deadline IS DISTINCT FROM OLD.original_deadline
        OR NEW.frozen_at IS DISTINCT FROM OLD.frozen_at
        OR NEW.frozen_remaining_ms IS DISTINCT FROM OLD.frozen_remaining_ms
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.resumed_at IS NOT NULL
        OR NEW.resumed_at IS NULL
        OR NEW.resumed_deadline IS NULL
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid Arena frozen clock CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_pause_clock_guard
BEFORE UPDATE OR DELETE ON arena_pause_clocks
FOR EACH ROW EXECUTE FUNCTION arena_pause_clock_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_game_pause_clock() RETURNS TRIGGER AS $$
DECLARE
    target_pause_id UUID;
    pause_row arena_pauses%ROWTYPE;
    clock_row arena_pause_clocks%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'arena_pauses' THEN
        IF NEW.scope_kind <> 'game_attempt' THEN
            RETURN NULL;
        END IF;
        target_pause_id := NEW.id;
    ELSE
        target_pause_id := NEW.pause_id;
    END IF;

    SELECT * INTO pause_row FROM arena_pauses WHERE id = target_pause_id;
    SELECT * INTO clock_row FROM arena_pause_clocks WHERE pause_id = target_pause_id;

    IF clock_row.pause_id IS NULL
        OR clock_row.game_attempt_id IS DISTINCT FROM pause_row.game_attempt_id
        OR clock_row.frozen_at IS DISTINCT FROM pause_row.started_at
        OR (pause_row.state = 'active' AND clock_row.resumed_at IS NOT NULL)
        OR (pause_row.state = 'resumed' AND clock_row.resumed_at IS NULL) THEN
        RAISE EXCEPTION 'Arena Game pause requires complete frozen clock evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_pauses_clock_consistency
AFTER INSERT OR UPDATE ON arena_pauses
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_game_pause_clock();

CREATE CONSTRAINT TRIGGER arena_pause_clocks_pause_consistency
AFTER INSERT OR UPDATE ON arena_pause_clocks
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_game_pause_clock();

-- +goose StatementBegin
CREATE FUNCTION arena_resume_decision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    pause_row arena_pauses%ROWTYPE;
    series_first UUID;
    series_second UUID;
    first_snapshot_state VARCHAR(16);
    second_snapshot_state VARCHAR(16);
    first_live arena_presence_states%ROWTYPE;
    second_live arena_presence_states%ROWTYPE;
    first_interval_state VARCHAR(16);
    second_interval_state VARCHAR(16);
    previous_number BIGINT;
BEGIN
    SELECT *
    INTO pause_row
    FROM arena_pauses
    WHERE id = NEW.pause_id
    FOR UPDATE;

    IF pause_row.state <> 'active' OR pause_row.series_id IS NULL THEN
        RAISE EXCEPTION 'Arena resume decision requires an active Series pause'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT first_participant_id, second_participant_id
    INTO series_first, series_second
    FROM arena_series
    WHERE id = pause_row.series_id;

    SELECT presence_state
    INTO first_snapshot_state
    FROM arena_pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.first_participant_id;

    SELECT presence_state
    INTO second_snapshot_state
    FROM arena_pause_presence_snapshots
    WHERE pause_id = NEW.pause_id AND participant_id = NEW.second_participant_id;

    SELECT *
    INTO first_live
    FROM arena_presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.first_participant_id;

    SELECT *
    INTO second_live
    FROM arena_presence_states
    WHERE series_id = pause_row.series_id
        AND participant_id = NEW.second_participant_id;

    SELECT state
    INTO first_interval_state
    FROM arena_reconnect_intervals
    WHERE id = NEW.first_reconnect_interval_id;

    SELECT state
    INTO second_interval_state
    FROM arena_reconnect_intervals
    WHERE id = NEW.second_reconnect_interval_id;

    SELECT MAX(decision_number)
    INTO previous_number
    FROM arena_resume_decisions
    WHERE pause_id = NEW.pause_id;

    IF NEW.first_participant_id IS DISTINCT FROM series_first
        OR NEW.second_participant_id IS DISTINCT FROM series_second
        OR NEW.first_pre_pause_state IS DISTINCT FROM first_snapshot_state
        OR NEW.second_pre_pause_state IS DISTINCT FROM second_snapshot_state
        OR NEW.first_live_state IS DISTINCT FROM first_live.state
        OR NEW.second_live_state IS DISTINCT FROM second_live.state
        OR NEW.first_presence_epoch IS DISTINCT FROM first_live.presence_epoch
        OR NEW.second_presence_epoch IS DISTINCT FROM second_live.presence_epoch
        OR NEW.first_presence_revision IS DISTINCT FROM first_live.revision
        OR NEW.second_presence_revision IS DISTINCT FROM second_live.revision
        OR NEW.decision_number <> COALESCE(previous_number, 0) + 1
        OR (
            NEW.first_reconnect_interval_id IS NOT NULL
            AND first_interval_state <> 'open'
        )
        OR (
            NEW.second_reconnect_interval_id IS NOT NULL
            AND second_interval_state <> 'open'
        ) THEN
        RAISE EXCEPTION 'Arena resume decision does not match durable presence evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_resume_decision_insert_guard
BEFORE INSERT ON arena_resume_decisions
FOR EACH ROW EXECUTE FUNCTION arena_resume_decision_insert_guard();

CREATE TRIGGER arena_resume_decisions_append_only
BEFORE UPDATE OR DELETE ON arena_resume_decisions
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

-- Existing Game identity remains immutable. This second trigger requires every
-- mutable Game transition to participate in an explicit revision CAS.
-- +goose StatementBegin
CREATE FUNCTION arena_game_attempt_revision_guard() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM OLD.state
        OR NEW.result_reason IS DISTINCT FROM OLD.result_reason
        OR NEW.winner_id IS DISTINCT FROM OLD.winner_id
        OR NEW.result_revision_id IS DISTINCT FROM OLD.result_revision_id
        OR NEW.started_at IS DISTINCT FROM OLD.started_at
        OR NEW.finished_at IS DISTINCT FROM OLD.finished_at
        OR NEW.updated_at IS DISTINCT FROM OLD.updated_at THEN
        IF NEW.revision <> OLD.revision + 1 OR NEW.updated_at <= OLD.updated_at THEN
            RAISE EXCEPTION 'invalid Arena Game revision CAS transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision IS DISTINCT FROM OLD.revision THEN
        RAISE EXCEPTION 'Arena Game revision cannot advance without state evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_game_attempt_revision_guard
BEFORE UPDATE ON arena_game_attempts
FOR EACH ROW EXECUTE FUNCTION arena_game_attempt_revision_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_game_pause_state() RETURNS TRIGGER AS $$
DECLARE
    target_attempt_id UUID;
    game_state VARCHAR(32);
    pause_state VARCHAR(16);
BEGIN
    IF TG_TABLE_NAME = 'arena_pauses' THEN
        IF NEW.scope_kind <> 'game_attempt' THEN
            RETURN NULL;
        END IF;
        target_attempt_id := NEW.game_attempt_id;
    ELSE
        target_attempt_id := NEW.id;
    END IF;

    SELECT state
    INTO game_state
    FROM arena_game_attempts
    WHERE id = target_attempt_id;

    SELECT state
    INTO pause_state
    FROM arena_pauses
    WHERE scope_kind = 'game_attempt' AND game_attempt_id = target_attempt_id
    ORDER BY created_at DESC, id DESC
    LIMIT 1;

    IF pause_state = 'active' AND game_state <> 'paused' THEN
        RAISE EXCEPTION 'active Arena Game pause requires paused Game CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF pause_state = 'resumed' AND game_state = 'paused' THEN
        RAISE EXCEPTION 'resumed Arena Game pause requires restored Game CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_pauses_game_state_consistency
AFTER INSERT OR UPDATE ON arena_pauses
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_game_pause_state();

CREATE CONSTRAINT TRIGGER arena_game_attempts_pause_consistency
AFTER INSERT OR UPDATE ON arena_game_attempts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_game_pause_state();

-- +goose Down

DROP TRIGGER IF EXISTS arena_game_attempts_pause_consistency
    ON arena_game_attempts;
DROP TRIGGER IF EXISTS arena_pauses_game_state_consistency ON arena_pauses;
DROP FUNCTION IF EXISTS arena_validate_game_pause_state();
DROP TRIGGER IF EXISTS arena_game_attempt_revision_guard ON arena_game_attempts;
DROP FUNCTION IF EXISTS arena_game_attempt_revision_guard();
DROP TABLE IF EXISTS arena_resume_decisions;
DROP FUNCTION IF EXISTS arena_resume_decision_insert_guard();
DROP TRIGGER IF EXISTS arena_pause_clocks_pause_consistency
    ON arena_pause_clocks;
DROP TRIGGER IF EXISTS arena_pauses_clock_consistency ON arena_pauses;
DROP FUNCTION IF EXISTS arena_validate_game_pause_clock();
DROP TABLE IF EXISTS arena_pause_clocks;
DROP FUNCTION IF EXISTS arena_pause_clock_guard();
DROP TABLE IF EXISTS arena_reconnect_intervals;
DROP TABLE IF EXISTS arena_reconnect_slot_counters;
DROP FUNCTION IF EXISTS arena_validate_reconnect_slot_count();
DROP FUNCTION IF EXISTS arena_reconnect_interval_guard();
DROP FUNCTION IF EXISTS arena_reconnect_slot_counter_guard();
DROP TABLE IF EXISTS arena_pause_presence_snapshots;
DROP FUNCTION IF EXISTS arena_validate_pause_presence_snapshot();
DROP FUNCTION IF EXISTS arena_pause_presence_snapshot_guard();
DROP TRIGGER IF EXISTS arena_pause_revisions_pause_consistency
    ON arena_pause_revisions;
DROP TRIGGER IF EXISTS arena_pauses_revision_consistency ON arena_pauses;
DROP FUNCTION IF EXISTS arena_validate_pause_revision();
ALTER TABLE arena_pauses
    DROP CONSTRAINT IF EXISTS arena_pauses_current_revision_fk;
DROP TABLE IF EXISTS arena_pause_revisions;
DROP TABLE IF EXISTS arena_pauses;
DROP FUNCTION IF EXISTS arena_pause_revision_insert_guard();
DROP FUNCTION IF EXISTS arena_pause_graph_guard();
DROP TABLE IF EXISTS arena_presence_states;
DROP FUNCTION IF EXISTS arena_presence_state_guard();
