-- +goose Up

-- This key lets a draft prove that its actors and format are the exact Series
-- identities from which the draft was created.
CREATE UNIQUE INDEX arena_series_draft_identity_idx
    ON arena_series (
        id,
        roster_id,
        first_participant_id,
        second_participant_id,
        format
    );

CREATE TABLE arena_category_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    revision BIGINT NOT NULL,
    source_pool_revision_id UUID NOT NULL,
    mode VARCHAR(16) NOT NULL,
    category_pool JSONB NOT NULL,
    selected_categories JSONB NOT NULL DEFAULT '[]'::JSONB,
    selector_actor_id UUID,
    selection_reason TEXT,
    decision_evidence_id UUID UNIQUE,
    decision_algorithm_version VARCHAR(64),
    decision_inputs JSONB,
    decision_seed BYTEA,
    decision_result JSONB,
    decision_replay_digest BYTEA,
    decision_owner_id UUID,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_category_revisions_id_series_roster_key
        UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_category_revisions_series_revision_key
        UNIQUE (series_id, revision),
    CONSTRAINT arena_category_revisions_series_fk FOREIGN KEY (series_id, roster_id)
        REFERENCES arena_series(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_category_revisions_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_category_revisions_mode_check CHECK (
        mode IN ('random', 'admin', 'draft')
    ),
    CONSTRAINT arena_category_revisions_pool_check CHECK (
        jsonb_typeof(category_pool) = 'array'
        AND jsonb_array_length(category_pool) IN (3, 5)
        AND jsonb_typeof(selected_categories) = 'array'
        AND jsonb_array_length(selected_categories) <= 3
    ),
    CONSTRAINT arena_category_revisions_selection_check CHECK (
        (
            mode = 'random'
            AND jsonb_array_length(selected_categories) > 0
            AND selector_actor_id IS NULL
            AND selection_reason IS NULL
            AND decision_evidence_id IS NOT NULL
            AND decision_algorithm_version IS NOT NULL
            AND decision_algorithm_version = 'hmac-sha256-order-v1'
            AND decision_inputs IS NOT NULL
            AND jsonb_typeof(decision_inputs) = 'array'
            AND jsonb_array_length(decision_inputs) > 0
            AND decision_seed IS NOT NULL
            AND octet_length(decision_seed) = 32
            AND decision_seed <> decode(repeat('00', 32), 'hex')
            AND decision_result IS NOT NULL
            AND jsonb_typeof(decision_result) = 'array'
            AND jsonb_array_length(decision_result) > 0
            AND decision_replay_digest IS NOT NULL
            AND octet_length(decision_replay_digest) = 32
            AND decision_owner_id IS NOT NULL
            AND decision_owner_id = id
            AND decided_at IS NOT NULL
        )
        OR (
            mode = 'admin'
            AND jsonb_array_length(selected_categories) > 0
            AND selector_actor_id IS NOT NULL
            AND selection_reason IS NOT NULL
            AND selection_reason = BTRIM(selection_reason)
            AND selection_reason <> ''
            AND decision_evidence_id IS NULL
            AND decision_algorithm_version IS NULL
            AND decision_inputs IS NULL
            AND decision_seed IS NULL
            AND decision_result IS NULL
            AND decision_replay_digest IS NULL
            AND decision_owner_id IS NULL
            AND decided_at IS NOT NULL
        )
        OR (
            mode = 'draft'
            AND selected_categories = '[]'::JSONB
            AND selector_actor_id IS NULL
            AND selection_reason IS NULL
            AND decision_evidence_id IS NULL
            AND decision_algorithm_version IS NULL
            AND decision_inputs IS NULL
            AND decision_seed IS NULL
            AND decision_result IS NULL
            AND decision_replay_digest IS NULL
            AND decision_owner_id IS NULL
            AND decided_at IS NULL
        )
    ),
    CONSTRAINT arena_category_revisions_decided_at_check CHECK (
        decided_at IS NULL OR decided_at <= created_at
    )
);

CREATE TABLE arena_drafts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL UNIQUE,
    roster_id UUID NOT NULL,
    category_revision_id UUID NOT NULL,
    first_participant_id UUID NOT NULL,
    second_participant_id UUID NOT NULL,
    format VARCHAR(8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_drafts_id_series_roster_key UNIQUE (id, series_id, roster_id),
    CONSTRAINT arena_drafts_series_identity_fk FOREIGN KEY (
        series_id,
        roster_id,
        first_participant_id,
        second_participant_id,
        format
    ) REFERENCES arena_series (
        id,
        roster_id,
        first_participant_id,
        second_participant_id,
        format
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_drafts_category_revision_fk FOREIGN KEY (
        category_revision_id,
        series_id,
        roster_id
    ) REFERENCES arena_category_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_drafts_participants_check CHECK (
        first_participant_id <> second_participant_id
    ),
    CONSTRAINT arena_drafts_format_check CHECK (format IN ('bo1', 'bo3'))
);

CREATE TABLE arena_draft_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id UUID NOT NULL,
    series_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    revision BIGINT NOT NULL,
    previous_revision_id UUID,
    command_id UUID NOT NULL,
    service_epoch UUID NOT NULL,
    state VARCHAR(32) NOT NULL,
    turn_number SMALLINT NOT NULL,
    current_actor_id UUID,
    current_action VARCHAR(8),
    absolute_deadline TIMESTAMPTZ,
    paused_remaining_ms INTEGER,
    recovery_reason VARCHAR(32),
    recovery_evidence JSONB,
    selected_categories JSONB NOT NULL DEFAULT '[]'::JSONB,
    decision_evidence_id UUID UNIQUE,
    decision_purpose VARCHAR(32),
    decision_algorithm_version VARCHAR(64),
    decision_inputs JSONB,
    decision_seed BYTEA,
    decision_result JSONB,
    decision_replay_digest BYTEA,
    decision_owner_id UUID,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_draft_revisions_id_draft_key UNIQUE (id, draft_id),
    CONSTRAINT arena_draft_revisions_id_draft_command_key
        UNIQUE (id, draft_id, command_id),
    CONSTRAINT arena_draft_revisions_draft_revision_key UNIQUE (draft_id, revision),
    CONSTRAINT arena_draft_revisions_draft_command_key UNIQUE (draft_id, command_id),
    CONSTRAINT arena_draft_revisions_draft_fk FOREIGN KEY (draft_id, series_id, roster_id)
        REFERENCES arena_drafts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_draft_revisions_previous_fk FOREIGN KEY (previous_revision_id, draft_id)
        REFERENCES arena_draft_revisions(id, draft_id) ON DELETE RESTRICT,
    CONSTRAINT arena_draft_revisions_actor_fk FOREIGN KEY (roster_id, current_actor_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_draft_revisions_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_draft_revisions_turn_check CHECK (turn_number BETWEEN 1 AND 4),
    CONSTRAINT arena_draft_revisions_state_check CHECK (
        state IN ('active', 'paused', 'recovery_required', 'completed', 'superseded')
    ),
    CONSTRAINT arena_draft_revisions_current_action_check CHECK (
        current_action IS NULL OR current_action IN ('ban', 'pick')
    ),
    CONSTRAINT arena_draft_revisions_selected_check CHECK (
        jsonb_typeof(selected_categories) = 'array'
        AND jsonb_array_length(selected_categories) <= 3
    ),
    CONSTRAINT arena_draft_revisions_recovery_check CHECK (
        recovery_evidence IS NULL OR jsonb_typeof(recovery_evidence) = 'object'
    ),
    CONSTRAINT arena_draft_revisions_state_evidence_check CHECK (
        (
            state = 'active'
            AND current_actor_id IS NOT NULL
            AND current_action IS NOT NULL
            AND absolute_deadline IS NOT NULL
            AND paused_remaining_ms IS NULL
            AND recovery_reason IS NULL
            AND recovery_evidence IS NULL
            AND selected_categories = '[]'::JSONB
        )
        OR (
            state = 'paused'
            AND current_actor_id IS NOT NULL
            AND current_action IS NOT NULL
            AND absolute_deadline IS NULL
            AND paused_remaining_ms BETWEEN 1 AND 15000
            AND recovery_reason IS NOT NULL
            AND recovery_reason = 'operator_pause'
            AND recovery_evidence IS NOT NULL
            AND selected_categories = '[]'::JSONB
        )
        OR (
            state = 'recovery_required'
            AND current_actor_id IS NOT NULL
            AND current_action IS NOT NULL
            AND absolute_deadline IS NULL
            AND paused_remaining_ms IS NULL
            AND recovery_reason IS NOT NULL
            AND recovery_reason IN ('epoch_mismatch', 'service_restart')
            AND recovery_evidence IS NOT NULL
            AND selected_categories = '[]'::JSONB
        )
        OR (
            state = 'completed'
            AND current_actor_id IS NULL
            AND current_action IS NULL
            AND absolute_deadline IS NULL
            AND paused_remaining_ms IS NULL
            AND recovery_reason IS NULL
            AND recovery_evidence IS NULL
            AND jsonb_array_length(selected_categories) > 0
        )
        OR (
            state = 'superseded'
            AND current_actor_id IS NULL
            AND current_action IS NULL
            AND absolute_deadline IS NULL
            AND paused_remaining_ms IS NULL
            AND recovery_reason IS NOT NULL
            AND recovery_reason = 'correction'
            AND recovery_evidence IS NOT NULL
            AND selected_categories = '[]'::JSONB
        )
    ),
    CONSTRAINT arena_draft_revisions_decision_check CHECK (
        (
            decision_evidence_id IS NULL
            AND decision_purpose IS NULL
            AND decision_algorithm_version IS NULL
            AND decision_inputs IS NULL
            AND decision_seed IS NULL
            AND decision_result IS NULL
            AND decision_replay_digest IS NULL
            AND decision_owner_id IS NULL
            AND decided_at IS NULL
        )
        OR (
            decision_evidence_id IS NOT NULL
            AND decision_purpose IS NOT NULL
            AND decision_purpose IN ('category', 'draft_order')
            AND decision_algorithm_version IS NOT NULL
            AND decision_algorithm_version = 'hmac-sha256-order-v1'
            AND decision_inputs IS NOT NULL
            AND jsonb_typeof(decision_inputs) = 'array'
            AND jsonb_array_length(decision_inputs) > 0
            AND decision_seed IS NOT NULL
            AND octet_length(decision_seed) = 32
            AND decision_seed <> decode(repeat('00', 32), 'hex')
            AND decision_result IS NOT NULL
            AND jsonb_typeof(decision_result) = 'array'
            AND jsonb_array_length(decision_result) > 0
            AND decision_replay_digest IS NOT NULL
            AND octet_length(decision_replay_digest) = 32
            AND decision_owner_id IS NOT NULL
            AND decision_owner_id = draft_id
            AND decided_at IS NOT NULL
            AND decided_at <= created_at
        )
    )
);

CREATE TABLE arena_draft_actions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id UUID NOT NULL,
    result_revision_id UUID NOT NULL,
    command_id UUID NOT NULL,
    turn_number SMALLINT NOT NULL,
    actor_id UUID NOT NULL,
    action VARCHAR(8) NOT NULL,
    category VARCHAR(32) NOT NULL,
    scheduled_deadline TIMESTAMPTZ NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    automatic BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_draft_actions_draft_turn_key UNIQUE (draft_id, turn_number),
    CONSTRAINT arena_draft_actions_draft_category_key UNIQUE (draft_id, category),
    CONSTRAINT arena_draft_actions_draft_command_key UNIQUE (draft_id, command_id),
    CONSTRAINT arena_draft_actions_revision_fk FOREIGN KEY (
        result_revision_id,
        draft_id,
        command_id
    ) REFERENCES arena_draft_revisions (
        id,
        draft_id,
        command_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_draft_actions_turn_check CHECK (turn_number BETWEEN 1 AND 4),
    CONSTRAINT arena_draft_actions_action_check CHECK (action IN ('ban', 'pick')),
    CONSTRAINT arena_draft_actions_category_check CHECK (
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
    CONSTRAINT arena_draft_actions_deadline_check CHECK (
        occurred_at <= scheduled_deadline
        AND occurred_at <= created_at
    )
);

CREATE INDEX arena_draft_revisions_draft_created_idx
    ON arena_draft_revisions (draft_id, revision DESC, created_at);

CREATE INDEX arena_draft_actions_draft_created_idx
    ON arena_draft_actions (draft_id, turn_number, created_at);

-- +goose StatementBegin
CREATE FUNCTION arena_draft_revision_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_id UUID;
    previous_number BIGINT;
    previous_state VARCHAR(32);
    previous_turn SMALLINT;
    first_actor_id UUID;
    second_actor_id UUID;
BEGIN
    PERFORM 1
    FROM arena_drafts
    WHERE id = NEW.draft_id
    FOR UPDATE;

    SELECT id, revision, state, turn_number
    INTO previous_id, previous_number, previous_state, previous_turn
    FROM arena_draft_revisions
    WHERE draft_id = NEW.draft_id
    ORDER BY revision DESC
    LIMIT 1;

    SELECT first_participant_id, second_participant_id
    INTO first_actor_id, second_actor_id
    FROM arena_drafts
    WHERE id = NEW.draft_id;

    IF NEW.current_actor_id IS NOT NULL
        AND NEW.current_actor_id NOT IN (first_actor_id, second_actor_id) THEN
        RAISE EXCEPTION 'Arena draft actor must belong to the Series'
            USING ERRCODE = 'check_violation';
    END IF;

    IF previous_id IS NULL THEN
        IF NEW.revision <> 1
            OR NEW.previous_revision_id IS NOT NULL
            OR NEW.state <> 'active'
            OR NEW.turn_number <> 1
            OR NEW.decision_purpose IS DISTINCT FROM 'draft_order' THEN
            RAISE EXCEPTION 'first Arena draft revision must establish turn one and reproducible actor order'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.revision <> previous_number + 1
        OR NEW.previous_revision_id IS DISTINCT FROM previous_id THEN
        RAISE EXCEPTION 'Arena draft revision CAS is stale'
            USING ERRCODE = 'serialization_failure';
    END IF;

    IF previous_state IN ('completed', 'superseded') THEN
        RAISE EXCEPTION 'terminal Arena draft evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.turn_number < previous_turn OR NEW.turn_number > previous_turn + 1 THEN
        RAISE EXCEPTION 'Arena draft turn revision is not contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (previous_state = 'active' AND NEW.state NOT IN (
        'active', 'paused', 'recovery_required', 'completed', 'superseded'
    ))
        OR (previous_state = 'paused' AND NEW.state NOT IN (
            'active', 'recovery_required', 'superseded'
        ))
        OR (previous_state = 'recovery_required' AND NEW.state NOT IN (
            'active', 'superseded'
        )) THEN
        RAISE EXCEPTION 'invalid Arena draft revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_draft_revision_insert_guard
BEFORE INSERT ON arena_draft_revisions
FOR EACH ROW EXECUTE FUNCTION arena_draft_revision_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_draft_action_insert_guard() RETURNS TRIGGER AS $$
DECLARE
    draft_format VARCHAR(8);
    category_pool JSONB;
    first_actor_id UUID;
    second_actor_id UUID;
    expected_actor_id UUID;
    expected_action VARCHAR(8);
    expected_turn SMALLINT;
    final_turn SMALLINT;
    result_state VARCHAR(32);
    result_turn SMALLINT;
    result_decision_purpose VARCHAR(32);
BEGIN
    SELECT
        draft.format,
        draft.first_participant_id,
        draft.second_participant_id,
        category_revision.category_pool
    INTO draft_format, first_actor_id, second_actor_id, category_pool
    FROM arena_drafts AS draft
    JOIN arena_category_revisions AS category_revision
        ON category_revision.id = draft.category_revision_id
    WHERE draft.id = NEW.draft_id
    FOR UPDATE OF draft;

    SELECT COALESCE(MAX(turn_number), 0) + 1
    INTO expected_turn
    FROM arena_draft_actions
    WHERE draft_id = NEW.draft_id;

    IF NEW.turn_number <> expected_turn THEN
        RAISE EXCEPTION 'Arena draft actions must be contiguous and ordered'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT category_pool ? NEW.category THEN
        RAISE EXCEPTION 'Arena draft category is outside the revision pool'
            USING ERRCODE = 'check_violation';
    END IF;

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
        OR NEW.actor_id IS DISTINCT FROM expected_actor_id
        OR NEW.action <> expected_action THEN
        RAISE EXCEPTION 'illegal Arena draft actor, turn, or action'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state, turn_number, decision_purpose
    INTO result_state, result_turn, result_decision_purpose
    FROM arena_draft_revisions
    WHERE id = NEW.result_revision_id AND draft_id = NEW.draft_id;

    IF NEW.turn_number = final_turn THEN
        IF result_state <> 'completed' OR result_turn <> final_turn THEN
            RAISE EXCEPTION 'final Arena draft action must complete the draft'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF result_state <> 'active' OR result_turn <> NEW.turn_number + 1 THEN
        RAISE EXCEPTION 'Arena draft action must advance exactly one turn'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.automatic AND result_decision_purpose IS DISTINCT FROM 'category' THEN
        RAISE EXCEPTION 'automatic Arena draft action requires reproducible category evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_draft_action_insert_guard
BEFORE INSERT ON arena_draft_actions
FOR EACH ROW EXECUTE FUNCTION arena_draft_action_insert_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_draft_revision_action_consistency() RETURNS TRIGGER AS $$
DECLARE
    previous_turn SMALLINT;
    action_count INTEGER;
BEGIN
    IF NEW.previous_revision_id IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT turn_number
    INTO previous_turn
    FROM arena_draft_revisions
    WHERE id = NEW.previous_revision_id;

    IF NEW.turn_number > previous_turn OR NEW.state = 'completed' THEN
        SELECT COUNT(*)
        INTO action_count
        FROM arena_draft_actions
        WHERE result_revision_id = NEW.id;

        IF action_count <> 1 THEN
            RAISE EXCEPTION 'turn-changing Arena draft revision requires one ordered action'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_draft_revision_action_consistency
AFTER INSERT ON arena_draft_revisions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_draft_revision_action_consistency();

-- +goose StatementBegin
CREATE FUNCTION arena_draft_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Arena draft identities and evidence are immutable'
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_category_revision_immutable_guard
BEFORE UPDATE OR DELETE ON arena_category_revisions
FOR EACH ROW EXECUTE FUNCTION arena_draft_immutable_guard();

CREATE TRIGGER arena_draft_identity_immutable_guard
BEFORE UPDATE OR DELETE ON arena_drafts
FOR EACH ROW EXECUTE FUNCTION arena_draft_immutable_guard();

CREATE TRIGGER arena_draft_revision_immutable_guard
BEFORE UPDATE OR DELETE ON arena_draft_revisions
FOR EACH ROW EXECUTE FUNCTION arena_draft_immutable_guard();

CREATE TRIGGER arena_draft_action_immutable_guard
BEFORE UPDATE OR DELETE ON arena_draft_actions
FOR EACH ROW EXECUTE FUNCTION arena_draft_immutable_guard();

-- +goose Down

DROP TABLE IF EXISTS arena_draft_actions;
DROP TABLE IF EXISTS arena_draft_revisions;
DROP TABLE IF EXISTS arena_drafts;
DROP TABLE IF EXISTS arena_category_revisions;
DROP FUNCTION IF EXISTS arena_draft_immutable_guard();
DROP FUNCTION IF EXISTS arena_draft_revision_action_consistency();
DROP FUNCTION IF EXISTS arena_draft_action_insert_guard();
DROP FUNCTION IF EXISTS arena_draft_revision_insert_guard();
DROP INDEX IF EXISTS arena_series_draft_identity_idx;
