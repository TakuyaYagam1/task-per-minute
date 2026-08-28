-- +goose Up

-- Arena execution tables are not consumed by the current application yet.
-- Keep writes stable while result heads and their deferred contracts are added.
LOCK TABLE arena_series, arena_game_attempts IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_game_attempts
        WHERE state IN ('completed', 'void', 'cancelled', 'superseded')
    ) OR EXISTS (
        SELECT 1
        FROM arena_series
        WHERE state <> 'planned'
    ) THEN
        RAISE EXCEPTION 'Arena result migration requires unstarted Arena execution rows'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Audit payloads are public operator evidence. Reject raw flag-shaped fields at
-- every nesting level; submission rows retain only a one-way payload digest.
-- +goose StatementBegin
CREATE FUNCTION arena_audit_payload_has_flag(value JSONB) RETURNS BOOLEAN AS $$
DECLARE
    item JSONB;
    item_key TEXT;
BEGIN
    IF jsonb_typeof(value) = 'object' THEN
        FOR item_key, item IN SELECT * FROM jsonb_each(value)
        LOOP
            IF LOWER(item_key) IN ('flag', 'submitted_flag', 'expected_flag')
                OR arena_audit_payload_has_flag(item) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    ELSIF jsonb_typeof(value) = 'array' THEN
        FOR item IN SELECT * FROM jsonb_array_elements(value)
        LOOP
            IF arena_audit_payload_has_flag(item) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    END IF;

    RETURN FALSE;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

CREATE TABLE arena_submission_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    attempt_id UUID NOT NULL,
    assignment_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    server_sequence BIGINT NOT NULL,
    idempotency_key UUID NOT NULL UNIQUE,
    status VARCHAR(16) NOT NULL,
    decision_reason TEXT,
    payload_digest BYTEA NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_submission_events_identity_key
        UNIQUE (id, attempt_id, series_id, roster_id),
    CONSTRAINT arena_submission_events_sequence_key
        UNIQUE (attempt_id, server_sequence),
    CONSTRAINT arena_submission_events_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_submission_events_attempt_fk FOREIGN KEY (
        attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_game_attempts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_submission_events_assignment_fk FOREIGN KEY (
        assignment_id,
        attempt_id,
        roster_id
    ) REFERENCES arena_assignments(id, attempt_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_submission_events_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_submission_events_sequence_check CHECK (server_sequence >= 1),
    CONSTRAINT arena_submission_events_status_check CHECK (
        status IN ('received', 'accepted', 'rejected')
    ),
    CONSTRAINT arena_submission_events_decision_check CHECK (
        (status IN ('received', 'accepted') AND decision_reason IS NULL)
        OR (
            status = 'rejected'
            AND decision_reason = BTRIM(decision_reason)
            AND decision_reason <> ''
        )
    ),
    CONSTRAINT arena_submission_events_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT arena_submission_events_timestamps_check CHECK (
        submitted_at <= received_at
        AND received_at <= created_at
    )
);

CREATE TABLE arena_result_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    attempt_id UUID NOT NULL,
    submission_event_id UUID,
    server_sequence BIGINT NOT NULL,
    idempotency_key UUID NOT NULL UNIQUE,
    result_state VARCHAR(16) NOT NULL,
    result_reason VARCHAR(40) NOT NULL,
    winner_id UUID,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_result_events_identity_key
        UNIQUE (id, attempt_id, series_id, roster_id),
    CONSTRAINT arena_result_events_series_identity_key
        UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_result_events_sequence_key
        UNIQUE (attempt_id, server_sequence),
    CONSTRAINT arena_result_events_submission_key UNIQUE (submission_event_id),
    CONSTRAINT arena_result_events_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_events_attempt_fk FOREIGN KEY (
        attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_game_attempts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_events_submission_fk FOREIGN KEY (
        submission_event_id,
        attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_submission_events (
        id,
        attempt_id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_result_events_winner_fk FOREIGN KEY (roster_id, winner_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_events_sequence_check CHECK (server_sequence >= 1),
    CONSTRAINT arena_result_events_result_check CHECK (
        (
            result_state = 'completed'
            AND result_reason IN ('solved', 'surrender', 'operator_forfeit')
            AND winner_id IS NOT NULL
            AND (
                result_reason <> 'solved'
                OR submission_event_id IS NOT NULL
            )
        )
        OR (
            result_state = 'void'
            AND result_reason IN (
                'no_solve',
                'task_failure',
                'common_platform_failure',
                'disconnect',
                'execution_epoch_break'
            )
            AND winner_id IS NULL
        )
        OR (
            result_state = 'cancelled'
            AND result_reason IN (
                'no_show',
                'series_cancelled',
                'tournament_cancelled'
            )
            AND winner_id IS NULL
        )
        OR (
            result_state = 'superseded'
            AND result_reason = 'derived_revision_superseded'
            AND winner_id IS NULL
        )
    ),
    CONSTRAINT arena_result_events_timestamps_check CHECK (occurred_at <= created_at)
);

CREATE TABLE arena_official_result_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    entity_kind VARCHAR(16) NOT NULL,
    entity_id UUID NOT NULL,
    series_id UUID NOT NULL,
    game_attempt_id UUID,
    result_event_id UUID NOT NULL,
    previous_revision_id UUID,
    revision_number BIGINT NOT NULL,
    result_state VARCHAR(16) NOT NULL,
    result_reason VARCHAR(40) NOT NULL,
    winner_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_official_result_revisions_entity_key
        UNIQUE (id, entity_kind, entity_id),
    CONSTRAINT arena_official_result_revisions_head_identity_key
        UNIQUE (id, entity_kind, entity_id, series_id, roster_id),
    CONSTRAINT arena_official_result_revisions_series_key
        UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_official_result_revisions_number_key
        UNIQUE (entity_kind, entity_id, revision_number),
    CONSTRAINT arena_official_result_revisions_roster_fk FOREIGN KEY (
        roster_id,
        tournament_id
    ) REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_official_result_revisions_series_fk FOREIGN KEY (
        series_id,
        roster_id
    ) REFERENCES arena_series(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_official_result_revisions_attempt_fk FOREIGN KEY (
        game_attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_game_attempts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_official_result_revisions_event_fk FOREIGN KEY (
        result_event_id,
        series_id,
        roster_id
    ) REFERENCES arena_result_events(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_official_result_revisions_previous_fk FOREIGN KEY (
        previous_revision_id,
        entity_kind,
        entity_id
    ) REFERENCES arena_official_result_revisions (
        id,
        entity_kind,
        entity_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_official_result_revisions_entity_check CHECK (
        (
            entity_kind = 'game_attempt'
            AND game_attempt_id IS NOT NULL
            AND entity_id = game_attempt_id
        )
        OR (
            entity_kind = 'series'
            AND game_attempt_id IS NULL
            AND entity_id = series_id
        )
    ),
    CONSTRAINT arena_official_result_revisions_number_check CHECK (
        revision_number >= 1
        AND (
            (revision_number = 1 AND previous_revision_id IS NULL)
            OR (revision_number > 1 AND previous_revision_id IS NOT NULL)
        )
    ),
    CONSTRAINT arena_official_result_revisions_result_check CHECK (
        (
            entity_kind = 'game_attempt'
            AND result_state IN ('completed', 'void', 'cancelled', 'superseded')
        )
        OR (
            entity_kind = 'series'
            AND result_state IN ('completed', 'cancelled')
            AND result_reason IN (
                'score_complete',
                'operator_correction',
                'series_cancelled',
                'tournament_cancelled'
            )
        )
    )
);

CREATE TABLE arena_official_result_heads (
    entity_kind VARCHAR(16) NOT NULL,
    entity_id UUID NOT NULL,
    series_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    game_attempt_id UUID,
    current_revision_id UUID NOT NULL UNIQUE,
    revision BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entity_kind, entity_id),
    CONSTRAINT arena_official_result_heads_revision_fk FOREIGN KEY (
        current_revision_id,
        entity_kind,
        entity_id,
        series_id,
        roster_id
    ) REFERENCES arena_official_result_revisions (
        id,
        entity_kind,
        entity_id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT arena_official_result_heads_entity_check CHECK (
        (
            entity_kind = 'game_attempt'
            AND game_attempt_id IS NOT NULL
            AND entity_id = game_attempt_id
        )
        OR (
            entity_kind = 'series'
            AND game_attempt_id IS NULL
            AND entity_id = series_id
        )
    ),
    CONSTRAINT arena_official_result_heads_revision_check CHECK (revision >= 1)
);

CREATE TABLE arena_series_score_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    result_event_id UUID,
    previous_revision_id UUID,
    revision_number BIGINT NOT NULL,
    first_participant_wins SMALLINT NOT NULL,
    second_participant_wins SMALLINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_series_score_revisions_identity_key
        UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_series_score_revisions_number_key
        UNIQUE (series_id, revision_number),
    CONSTRAINT arena_series_score_revisions_roster_fk FOREIGN KEY (
        roster_id,
        tournament_id
    ) REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_score_revisions_series_fk FOREIGN KEY (
        series_id,
        roster_id
    ) REFERENCES arena_series(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_score_revisions_event_fk FOREIGN KEY (
        result_event_id,
        series_id,
        roster_id
    ) REFERENCES arena_result_events(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_series_score_revisions_previous_fk FOREIGN KEY (
        previous_revision_id,
        series_id,
        roster_id
    ) REFERENCES arena_series_score_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_series_score_revisions_number_check CHECK (
        revision_number >= 1
        AND (
            (
                revision_number = 1
                AND previous_revision_id IS NULL
                AND result_event_id IS NULL
                AND first_participant_wins = 0
                AND second_participant_wins = 0
            )
            OR (
                revision_number > 1
                AND previous_revision_id IS NOT NULL
                AND result_event_id IS NOT NULL
            )
        )
    ),
    CONSTRAINT arena_series_score_revisions_score_check CHECK (
        first_participant_wins BETWEEN 0 AND 2
        AND second_participant_wins BETWEEN 0 AND 2
        AND NOT (
            first_participant_wins = 2
            AND second_participant_wins = 2
        )
    )
);

CREATE TABLE arena_series_score_heads (
    series_id UUID PRIMARY KEY,
    roster_id UUID NOT NULL,
    current_revision_id UUID NOT NULL UNIQUE,
    revision BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_series_score_heads_revision_fk FOREIGN KEY (
        current_revision_id,
        series_id,
        roster_id
    ) REFERENCES arena_series_score_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT arena_series_score_heads_revision_check CHECK (revision >= 1)
);

CREATE TABLE arena_audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    result_event_id UUID NOT NULL UNIQUE,
    actor_kind VARCHAR(16) NOT NULL,
    actor_id UUID,
    action VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_audit_events_identity_key UNIQUE (id, result_event_id),
    CONSTRAINT arena_audit_events_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_audit_events_event_fk FOREIGN KEY (
        result_event_id,
        series_id,
        roster_id
    ) REFERENCES arena_result_events(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_audit_events_actor_check CHECK (
        (actor_kind = 'server' AND actor_id IS NULL)
        OR (actor_kind = 'operator' AND actor_id IS NOT NULL)
    ),
    CONSTRAINT arena_audit_events_action_check CHECK (
        action = BTRIM(action) AND action <> ''
    ),
    CONSTRAINT arena_audit_events_payload_check CHECK (
        jsonb_typeof(payload) = 'object'
        AND payload <> '{}'::JSONB
        AND NOT arena_audit_payload_has_flag(payload)
    ),
    CONSTRAINT arena_audit_events_timestamps_check CHECK (occurred_at <= created_at)
);

CREATE TABLE arena_result_projection_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    result_event_id UUID NOT NULL UNIQUE,
    artifact_kinds JSONB NOT NULL,
    payload_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_result_projection_evidence_identity_key
        UNIQUE (id, result_event_id),
    CONSTRAINT arena_result_projection_evidence_roster_fk FOREIGN KEY (
        roster_id,
        tournament_id
    ) REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_projection_evidence_event_fk FOREIGN KEY (
        result_event_id,
        series_id,
        roster_id
    ) REFERENCES arena_result_events(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_projection_evidence_artifacts_check CHECK (
        jsonb_typeof(artifact_kinds) = 'array'
        AND jsonb_array_length(artifact_kinds) > 0
    ),
    CONSTRAINT arena_result_projection_evidence_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE arena_outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    result_event_id UUID NOT NULL UNIQUE,
    idempotency_key UUID NOT NULL UNIQUE,
    topic VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    CONSTRAINT arena_outbox_events_identity_key UNIQUE (id, result_event_id),
    CONSTRAINT arena_outbox_events_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_outbox_events_event_fk FOREIGN KEY (
        result_event_id,
        series_id,
        roster_id
    ) REFERENCES arena_result_events(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_outbox_events_topic_check CHECK (
        topic = BTRIM(topic) AND topic <> ''
    ),
    CONSTRAINT arena_outbox_events_payload_check CHECK (
        jsonb_typeof(payload) = 'object' AND payload <> '{}'::JSONB
    ),
    CONSTRAINT arena_outbox_events_publish_check CHECK (
        published_at IS NULL OR published_at >= created_at
    )
);

CREATE INDEX arena_outbox_events_unpublished_idx
    ON arena_outbox_events (created_at, id)
    WHERE published_at IS NULL;

CREATE TABLE arena_result_commits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    series_id UUID NOT NULL,
    attempt_id UUID NOT NULL,
    result_event_id UUID NOT NULL UNIQUE,
    game_result_revision_id UUID NOT NULL UNIQUE,
    series_score_revision_id UUID NOT NULL UNIQUE,
    series_result_revision_id UUID UNIQUE,
    audit_event_id UUID NOT NULL UNIQUE,
    outbox_event_id UUID NOT NULL UNIQUE,
    projection_evidence_id UUID NOT NULL UNIQUE,
    idempotency_key UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_result_commits_event_fk FOREIGN KEY (
        result_event_id,
        attempt_id,
        series_id,
        roster_id
    ) REFERENCES arena_result_events (
        id,
        attempt_id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_result_commits_game_revision_fk FOREIGN KEY (
        game_result_revision_id,
        series_id,
        roster_id
    ) REFERENCES arena_official_result_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_result_commits_score_revision_fk FOREIGN KEY (
        series_score_revision_id,
        series_id,
        roster_id
    ) REFERENCES arena_series_score_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_result_commits_series_revision_fk FOREIGN KEY (
        series_result_revision_id,
        series_id,
        roster_id
    ) REFERENCES arena_official_result_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_result_commits_audit_fk FOREIGN KEY (
        audit_event_id,
        result_event_id
    ) REFERENCES arena_audit_events(id, result_event_id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_commits_outbox_fk FOREIGN KEY (
        outbox_event_id,
        result_event_id
    ) REFERENCES arena_outbox_events(id, result_event_id) ON DELETE RESTRICT,
    CONSTRAINT arena_result_commits_projection_fk FOREIGN KEY (
        projection_evidence_id,
        result_event_id
    ) REFERENCES arena_result_projection_evidence (
        id,
        result_event_id
    ) ON DELETE RESTRICT
);

-- +goose StatementBegin
CREATE FUNCTION arena_append_only_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% is append-only Arena evidence', TG_TABLE_NAME
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_submission_events_append_only
BEFORE UPDATE OR DELETE ON arena_submission_events
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

CREATE TRIGGER arena_result_events_append_only
BEFORE UPDATE OR DELETE ON arena_result_events
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

CREATE TRIGGER arena_official_result_revisions_append_only
BEFORE UPDATE OR DELETE ON arena_official_result_revisions
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

CREATE TRIGGER arena_series_score_revisions_append_only
BEFORE UPDATE OR DELETE ON arena_series_score_revisions
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

CREATE TRIGGER arena_audit_events_append_only
BEFORE UPDATE OR DELETE ON arena_audit_events
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

CREATE TRIGGER arena_result_projection_evidence_append_only
BEFORE UPDATE OR DELETE ON arena_result_projection_evidence
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

CREATE TRIGGER arena_result_commits_append_only
BEFORE UPDATE OR DELETE ON arena_result_commits
FOR EACH ROW EXECUTE FUNCTION arena_append_only_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_submission_event_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
BEGIN
    PERFORM 1
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT MAX(server_sequence)
    INTO previous_sequence
    FROM arena_submission_events
    WHERE attempt_id = NEW.attempt_id;

    IF NEW.server_sequence <> COALESCE(previous_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'Arena submission sequence must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_submission_event_insert_guard
BEFORE INSERT ON arena_submission_events
FOR EACH ROW EXECUTE FUNCTION arena_submission_event_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_result_event_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
    submission_status VARCHAR(16);
BEGIN
    PERFORM 1
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id
    FOR UPDATE;

    SELECT MAX(server_sequence)
    INTO previous_sequence
    FROM arena_result_events
    WHERE attempt_id = NEW.attempt_id;

    IF NEW.server_sequence <> COALESCE(previous_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'Arena result event sequence must be contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.submission_event_id IS NOT NULL THEN
        SELECT status
        INTO submission_status
        FROM arena_submission_events
        WHERE id = NEW.submission_event_id;

        IF submission_status <> 'accepted' THEN
            RAISE EXCEPTION 'Arena result requires an accepted submission event'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_result_event_insert_guard
BEFORE INSERT ON arena_result_events
FOR EACH ROW EXECUTE FUNCTION arena_result_event_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_official_result_revision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    event_state VARCHAR(16);
    event_reason VARCHAR(40);
    event_winner UUID;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_result_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed Arena result event cannot gain another revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.entity_kind = 'game_attempt' THEN
        PERFORM 1
        FROM arena_game_attempts
        WHERE id = NEW.game_attempt_id
        FOR UPDATE;
    ELSE
        PERFORM 1
        FROM arena_series
        WHERE id = NEW.series_id
        FOR UPDATE;
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM arena_official_result_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena official-result revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Arena official-result lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT result_state, result_reason, winner_id
    INTO event_state, event_reason, event_winner
    FROM arena_result_events
    WHERE id = NEW.result_event_id;

    IF NEW.entity_kind = 'game_attempt'
        AND (
            NEW.result_state IS DISTINCT FROM event_state
            OR NEW.result_reason IS DISTINCT FROM event_reason
            OR NEW.winner_id IS DISTINCT FROM event_winner
        ) THEN
        RAISE EXCEPTION 'Arena Game result revision must retain server event evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_official_result_revision_insert_guard
BEFORE INSERT ON arena_official_result_revisions
FOR EACH ROW EXECUTE FUNCTION arena_official_result_revision_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_series_score_revision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    series_format VARCHAR(8);
BEGIN
    IF NEW.result_event_id IS NOT NULL AND EXISTS (
        SELECT 1
        FROM arena_result_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed Arena result event cannot gain another score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT format
    INTO series_format
    FROM arena_series
    WHERE id = NEW.series_id
    FOR UPDATE;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM arena_series_score_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena Series-score revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Arena Series-score lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (series_format = 'bo1' AND (
        NEW.first_participant_wins > 1
        OR NEW.second_participant_wins > 1
    )) THEN
        RAISE EXCEPTION 'BO1 score revision exceeds one win'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_series_score_revision_insert_guard
BEFORE INSERT ON arena_series_score_revisions
FOR EACH ROW EXECUTE FUNCTION arena_series_score_revision_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_official_result_head_guard() RETURNS TRIGGER AS $$
DECLARE
    current_number BIGINT;
    previous_revision UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena official-result heads are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, previous_revision_id
    INTO current_number, previous_revision
    FROM arena_official_result_revisions
    WHERE id = NEW.current_revision_id;

    IF TG_OP = 'INSERT' THEN
        IF NEW.revision <> 1 OR current_number <> 1 THEN
            RAISE EXCEPTION 'Arena official-result head must start at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        IF NEW.entity_kind IS DISTINCT FROM OLD.entity_kind
            OR NEW.entity_id IS DISTINCT FROM OLD.entity_id
            OR NEW.series_id IS DISTINCT FROM OLD.series_id
            OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
            OR NEW.game_attempt_id IS DISTINCT FROM OLD.game_attempt_id
            OR NEW.revision <> OLD.revision + 1
            OR current_number <> NEW.revision
            OR previous_revision IS DISTINCT FROM OLD.current_revision_id
            OR NEW.updated_at <= OLD.updated_at THEN
            RAISE EXCEPTION 'invalid Arena official-result head CAS transition'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_official_result_head_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_official_result_heads
FOR EACH ROW EXECUTE FUNCTION arena_official_result_head_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_series_score_head_guard() RETURNS TRIGGER AS $$
DECLARE
    current_number BIGINT;
    previous_revision UUID;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena Series-score heads are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, previous_revision_id
    INTO current_number, previous_revision
    FROM arena_series_score_revisions
    WHERE id = NEW.current_revision_id;

    IF TG_OP = 'INSERT' THEN
        IF NEW.revision <> 1 OR current_number <> 1 THEN
            RAISE EXCEPTION 'Arena Series-score head must start at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSE
        IF NEW.series_id IS DISTINCT FROM OLD.series_id
            OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
            OR NEW.revision <> OLD.revision + 1
            OR current_number <> NEW.revision
            OR previous_revision IS DISTINCT FROM OLD.current_revision_id
            OR NEW.updated_at <= OLD.updated_at THEN
            RAISE EXCEPTION 'invalid Arena Series-score head CAS transition'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_series_score_head_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_series_score_heads
FOR EACH ROW EXECUTE FUNCTION arena_series_score_head_guard();

-- Outbox identity and payload are immutable. Publication is a one-way marker;
-- retained rows can never be deleted after delivery.
-- +goose StatementBegin
CREATE FUNCTION arena_outbox_event_guard() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena outbox events are durable retained evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.result_event_id IS DISTINCT FROM OLD.result_event_id
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.topic IS DISTINCT FROM OLD.topic
        OR NEW.payload IS DISTINCT FROM OLD.payload
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.published_at IS NOT NULL
        OR NEW.published_at IS NULL THEN
        RAISE EXCEPTION 'invalid Arena outbox publication transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_outbox_event_guard
BEFORE UPDATE OR DELETE ON arena_outbox_events
FOR EACH ROW EXECUTE FUNCTION arena_outbox_event_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_result_commit_guard() RETURNS TRIGGER AS $$
DECLARE
    linkage_is_consistent BOOLEAN;
    official_revision_count INTEGER;
    score_revision_count INTEGER;
BEGIN
    SELECT (
        game_revision.entity_kind = 'game_attempt'
        AND game_revision.entity_id = NEW.attempt_id
        AND game_revision.result_event_id = NEW.result_event_id
        AND score_revision.result_event_id = NEW.result_event_id
        AND audit_event.tournament_id = NEW.tournament_id
        AND outbox_event.tournament_id = NEW.tournament_id
        AND projection.tournament_id = NEW.tournament_id
        AND (
            NEW.series_result_revision_id IS NULL
            OR (
                series_revision.entity_kind = 'series'
                AND series_revision.entity_id = NEW.series_id
                AND series_revision.result_event_id = NEW.result_event_id
            )
        )
    )
    INTO linkage_is_consistent
    FROM arena_official_result_revisions AS game_revision
    JOIN arena_series_score_revisions AS score_revision
        ON score_revision.id = NEW.series_score_revision_id
    JOIN arena_audit_events AS audit_event
        ON audit_event.id = NEW.audit_event_id
    JOIN arena_outbox_events AS outbox_event
        ON outbox_event.id = NEW.outbox_event_id
    JOIN arena_result_projection_evidence AS projection
        ON projection.id = NEW.projection_evidence_id
    LEFT JOIN arena_official_result_revisions AS series_revision
        ON series_revision.id = NEW.series_result_revision_id
    WHERE game_revision.id = NEW.game_result_revision_id;

    IF linkage_is_consistent IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'Arena result commit evidence is not one atomic lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO official_revision_count
    FROM arena_official_result_revisions
    WHERE result_event_id = NEW.result_event_id;

    SELECT COUNT(*)
    INTO score_revision_count
    FROM arena_series_score_revisions
    WHERE result_event_id = NEW.result_event_id;

    IF official_revision_count <> 1 + (
        CASE
            WHEN NEW.series_result_revision_id IS NOT NULL THEN 1
            ELSE 0
        END
    )
        OR score_revision_count <> 1 THEN
        RAISE EXCEPTION 'Arena result commit must exhaustively link event revisions'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_result_commit_guard
BEFORE INSERT ON arena_result_commits
FOR EACH ROW EXECUTE FUNCTION arena_result_commit_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_result_event_commit() RETURNS TRIGGER AS $$
DECLARE
    target_result_event_id UUID;
    commit_count INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'arena_result_events' THEN
        target_result_event_id := NEW.id;
    ELSE
        target_result_event_id := NEW.result_event_id;
    END IF;

    SELECT COUNT(*)
    INTO commit_count
    FROM arena_result_commits
    WHERE result_event_id = target_result_event_id;

    IF commit_count <> 1 THEN
        RAISE EXCEPTION 'Arena result event requires exactly one atomic evidence commit'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_result_events_commit_consistency
AFTER INSERT ON arena_result_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_result_event_commit();

CREATE CONSTRAINT TRIGGER arena_result_commits_event_consistency
AFTER INSERT ON arena_result_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_result_event_commit();

ALTER TABLE arena_game_attempts
    ADD CONSTRAINT arena_game_attempts_result_revision_fk FOREIGN KEY (
        result_revision_id
    ) REFERENCES arena_official_result_revisions(id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE arena_series
    ADD CONSTRAINT arena_series_result_revision_fk FOREIGN KEY (
        current_result_revision_id
    ) REFERENCES arena_official_result_revisions(id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT arena_series_score_revision_fk FOREIGN KEY (
        current_score_revision_id
    ) REFERENCES arena_series_score_revisions(id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

-- +goose StatementBegin
CREATE FUNCTION arena_validate_game_result_head() RETURNS TRIGGER AS $$
DECLARE
    target_attempt_id UUID;
    attempt_row arena_game_attempts%ROWTYPE;
    head_revision_id UUID;
    revision_row arena_official_result_revisions%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'arena_game_attempts' THEN
        target_attempt_id := NEW.id;
    ELSIF NEW.entity_kind = 'game_attempt' THEN
        target_attempt_id := NEW.entity_id;
    ELSE
        RETURN NULL;
    END IF;

    SELECT *
    INTO attempt_row
    FROM arena_game_attempts
    WHERE id = target_attempt_id;

    SELECT current_revision_id
    INTO head_revision_id
    FROM arena_official_result_heads
    WHERE entity_kind = 'game_attempt' AND entity_id = target_attempt_id;

    IF attempt_row.state IN ('completed', 'void', 'cancelled', 'superseded') THEN
        IF head_revision_id IS NULL
            OR attempt_row.result_revision_id IS DISTINCT FROM head_revision_id THEN
            RAISE EXCEPTION 'terminal Arena Game requires exactly one current result head'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO revision_row
        FROM arena_official_result_revisions
        WHERE id = head_revision_id;

        IF revision_row.series_id IS DISTINCT FROM attempt_row.series_id
            OR revision_row.roster_id IS DISTINCT FROM attempt_row.roster_id
            OR revision_row.result_state IS DISTINCT FROM attempt_row.state
            OR revision_row.result_reason IS DISTINCT FROM attempt_row.result_reason
            OR revision_row.winner_id IS DISTINCT FROM attempt_row.winner_id THEN
            RAISE EXCEPTION 'Arena Game terminal state differs from current result revision'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF head_revision_id IS NOT NULL OR attempt_row.result_revision_id IS NOT NULL THEN
        RAISE EXCEPTION 'non-terminal Arena Game cannot have a current result head'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_game_attempts_result_head_consistency
AFTER INSERT OR UPDATE ON arena_game_attempts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_game_result_head();

CREATE CONSTRAINT TRIGGER arena_game_result_heads_attempt_consistency
AFTER INSERT OR UPDATE ON arena_official_result_heads
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_game_result_head();

-- +goose StatementBegin
CREATE FUNCTION arena_validate_series_revision_heads() RETURNS TRIGGER AS $$
DECLARE
    target_series_id UUID;
    series_row arena_series%ROWTYPE;
    score_revision_id UUID;
    result_revision_id UUID;
    score_row arena_series_score_revisions%ROWTYPE;
    result_row arena_official_result_revisions%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'arena_series' THEN
        target_series_id := NEW.id;
    ELSIF TG_TABLE_NAME = 'arena_series_score_heads' THEN
        target_series_id := NEW.series_id;
    ELSIF NEW.entity_kind = 'series' THEN
        target_series_id := NEW.entity_id;
    ELSE
        RETURN NULL;
    END IF;

    SELECT *
    INTO series_row
    FROM arena_series
    WHERE id = target_series_id;

    SELECT current_revision_id
    INTO score_revision_id
    FROM arena_series_score_heads
    WHERE series_id = target_series_id;

    SELECT current_revision_id
    INTO result_revision_id
    FROM arena_official_result_heads
    WHERE entity_kind = 'series' AND entity_id = target_series_id;

    IF series_row.state = 'planned' THEN
        IF score_revision_id IS NOT NULL
            OR result_revision_id IS NOT NULL
            OR series_row.current_score_revision_id IS NOT NULL
            OR series_row.current_result_revision_id IS NOT NULL THEN
            RAISE EXCEPTION 'planned Arena Series cannot have revision heads'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NULL;
    END IF;

    IF score_revision_id IS NULL
        OR series_row.current_score_revision_id IS DISTINCT FROM score_revision_id THEN
        RAISE EXCEPTION 'locked Arena Series requires exactly one current score head'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT *
    INTO score_row
    FROM arena_series_score_revisions
    WHERE id = score_revision_id;

    IF score_row.roster_id IS DISTINCT FROM series_row.roster_id
        OR score_row.first_participant_wins IS DISTINCT FROM series_row.first_participant_wins
        OR score_row.second_participant_wins IS DISTINCT FROM series_row.second_participant_wins THEN
        RAISE EXCEPTION 'Arena Series score differs from current score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF series_row.state IN ('completed', 'cancelled') THEN
        IF result_revision_id IS NULL
            OR series_row.current_result_revision_id IS DISTINCT FROM result_revision_id THEN
            RAISE EXCEPTION 'terminal Arena Series requires exactly one current result head'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT *
        INTO result_row
        FROM arena_official_result_revisions
        WHERE id = result_revision_id;

        IF result_row.roster_id IS DISTINCT FROM series_row.roster_id
            OR result_row.result_state IS DISTINCT FROM series_row.state
            OR result_row.winner_id IS DISTINCT FROM series_row.winner_id THEN
            RAISE EXCEPTION 'Arena Series terminal state differs from current result revision'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF result_revision_id IS NOT NULL OR series_row.current_result_revision_id IS NOT NULL THEN
        RAISE EXCEPTION 'non-terminal Arena Series cannot have a current result head'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_series_revision_head_consistency
AFTER INSERT OR UPDATE ON arena_series
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_series_revision_heads();

CREATE CONSTRAINT TRIGGER arena_series_score_heads_series_consistency
AFTER INSERT OR UPDATE ON arena_series_score_heads
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_series_revision_heads();

CREATE CONSTRAINT TRIGGER arena_series_result_heads_series_consistency
AFTER INSERT OR UPDATE ON arena_official_result_heads
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_series_revision_heads();

-- +goose Down

DROP TRIGGER IF EXISTS arena_series_result_heads_series_consistency
    ON arena_official_result_heads;
DROP TRIGGER IF EXISTS arena_series_score_heads_series_consistency
    ON arena_series_score_heads;
DROP TRIGGER IF EXISTS arena_series_revision_head_consistency ON arena_series;
DROP FUNCTION IF EXISTS arena_validate_series_revision_heads();
DROP TRIGGER IF EXISTS arena_game_result_heads_attempt_consistency
    ON arena_official_result_heads;
DROP TRIGGER IF EXISTS arena_game_attempts_result_head_consistency
    ON arena_game_attempts;
DROP FUNCTION IF EXISTS arena_validate_game_result_head();
ALTER TABLE arena_series
    DROP CONSTRAINT IF EXISTS arena_series_score_revision_fk,
    DROP CONSTRAINT IF EXISTS arena_series_result_revision_fk;
ALTER TABLE arena_game_attempts
    DROP CONSTRAINT IF EXISTS arena_game_attempts_result_revision_fk;
DROP TRIGGER IF EXISTS arena_result_commits_event_consistency
    ON arena_result_commits;
DROP TRIGGER IF EXISTS arena_result_events_commit_consistency
    ON arena_result_events;
DROP FUNCTION IF EXISTS arena_validate_result_event_commit();
DROP TABLE IF EXISTS arena_result_commits;
DROP TABLE IF EXISTS arena_outbox_events;
DROP FUNCTION IF EXISTS arena_outbox_event_guard();
DROP INDEX IF EXISTS arena_outbox_events_unpublished_idx;
DROP TABLE IF EXISTS arena_result_projection_evidence;
DROP TABLE IF EXISTS arena_audit_events;
DROP TABLE IF EXISTS arena_series_score_heads;
DROP TABLE IF EXISTS arena_series_score_revisions;
DROP TABLE IF EXISTS arena_official_result_heads;
DROP TABLE IF EXISTS arena_official_result_revisions;
DROP TABLE IF EXISTS arena_result_events;
DROP TABLE IF EXISTS arena_submission_events;
DROP FUNCTION IF EXISTS arena_result_commit_guard();
DROP FUNCTION IF EXISTS arena_series_score_head_guard();
DROP FUNCTION IF EXISTS arena_official_result_head_guard();
DROP FUNCTION IF EXISTS arena_series_score_revision_insert_guard();
DROP FUNCTION IF EXISTS arena_official_result_revision_insert_guard();
DROP FUNCTION IF EXISTS arena_result_event_insert_guard();
DROP FUNCTION IF EXISTS arena_submission_event_insert_guard();
DROP FUNCTION IF EXISTS arena_append_only_guard();
DROP FUNCTION IF EXISTS arena_audit_payload_has_flag(JSONB);
