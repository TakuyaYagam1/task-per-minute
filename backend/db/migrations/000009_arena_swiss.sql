-- +goose Up

-- The redundant roster/id key lets all Swiss child rows prove that a
-- participant belongs to the roster owned by the round.
CREATE UNIQUE INDEX arena_participants_roster_id_id_idx
    ON arena_participants (roster_id, id);

CREATE TABLE arena_swiss_rounds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    roster_id UUID NOT NULL REFERENCES arena_rosters(id) ON DELETE RESTRICT,
    round_number SMALLINT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    source_roster_revision BIGINT NOT NULL,
    source_history_revision BIGINT NOT NULL,
    generation_kind VARCHAR(16) NOT NULL,
    pairing_inputs JSONB NOT NULL,
    decision_evidence_id UUID UNIQUE,
    decision_algorithm_version VARCHAR(64),
    decision_seed BYTEA,
    decision_result JSONB,
    decision_replay_digest BYTEA,
    decision_owner_id UUID,
    generated_at TIMESTAMPTZ NOT NULL,
    lock_revision BIGINT,
    locked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_swiss_rounds_id_roster_key UNIQUE (id, roster_id),
    CONSTRAINT arena_swiss_rounds_roster_number_key UNIQUE (roster_id, round_number),
    CONSTRAINT arena_swiss_rounds_number_check CHECK (round_number BETWEEN 1 AND 4),
    CONSTRAINT arena_swiss_rounds_revision_check CHECK (
        revision >= 1
        AND source_roster_revision >= 1
        AND source_history_revision >= 0
    ),
    CONSTRAINT arena_swiss_rounds_generation_kind_check CHECK (
        generation_kind IN ('automatic', 'manual')
    ),
    CONSTRAINT arena_swiss_rounds_pairing_inputs_check CHECK (
        jsonb_typeof(pairing_inputs) = 'array'
        AND jsonb_array_length(pairing_inputs) > 0
    ),
    CONSTRAINT arena_swiss_rounds_generation_evidence_check CHECK (
        (
            generation_kind = 'automatic'
            AND decision_evidence_id IS NOT NULL
            AND decision_algorithm_version = 'hmac-sha256-order-v1'
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
        )
        OR (
            generation_kind = 'manual'
            AND decision_evidence_id IS NULL
            AND decision_algorithm_version IS NULL
            AND decision_seed IS NULL
            AND decision_result IS NULL
            AND decision_replay_digest IS NULL
            AND decision_owner_id IS NULL
        )
    ),
    CONSTRAINT arena_swiss_rounds_lock_check CHECK (
        (
            lock_revision IS NULL
            AND locked_at IS NULL
        )
        OR (
            lock_revision IS NOT NULL
            AND lock_revision = revision
            AND locked_at IS NOT NULL
        )
    ),
    CONSTRAINT arena_swiss_rounds_timestamps_check CHECK (
        updated_at >= created_at
        AND generated_at <= created_at
        AND (locked_at IS NULL OR locked_at >= generated_at)
    )
);

CREATE TABLE arena_swiss_repeat_overrides (
    id UUID PRIMARY KEY,
    round_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    reason TEXT NOT NULL,
    confirmed_at TIMESTAMPTZ NOT NULL,
    roster_participant_ids JSONB NOT NULL,
    proposed_pairings JSONB NOT NULL,
    bye_participant_id UUID,
    previous_meetings JSONB NOT NULL,
    repeated_pairings JSONB NOT NULL,
    alternative_algorithm_version VARCHAR(64) NOT NULL,
    alternative_search_complete BOOLEAN NOT NULL,
    alternative_pairings JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_swiss_repeat_overrides_id_round_key UNIQUE (id, round_id),
    CONSTRAINT arena_swiss_repeat_overrides_round_key UNIQUE (round_id),
    CONSTRAINT arena_swiss_repeat_overrides_round_fk FOREIGN KEY (round_id, roster_id)
        REFERENCES arena_swiss_rounds(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_repeat_overrides_bye_fk FOREIGN KEY (roster_id, bye_participant_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_repeat_overrides_reason_check CHECK (
        reason = BTRIM(reason) AND reason <> ''
    ),
    CONSTRAINT arena_swiss_repeat_overrides_evidence_check CHECK (
        jsonb_typeof(roster_participant_ids) = 'array'
        AND jsonb_array_length(roster_participant_ids) > 0
        AND jsonb_typeof(proposed_pairings) = 'array'
        AND jsonb_array_length(proposed_pairings) > 0
        AND jsonb_typeof(previous_meetings) = 'array'
        AND jsonb_typeof(repeated_pairings) = 'array'
        AND jsonb_array_length(repeated_pairings) > 0
        AND alternative_algorithm_version = 'complete-backtracking-v1'
        AND alternative_search_complete
        AND jsonb_typeof(alternative_pairings) = 'array'
    ),
    CONSTRAINT arena_swiss_repeat_overrides_timestamps_check CHECK (
        confirmed_at <= created_at
    )
);

CREATE TABLE arena_swiss_pairings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    round_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    slot_number SMALLINT NOT NULL,
    repeat_override_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_swiss_pairings_id_round_roster_key UNIQUE (id, round_id, roster_id),
    CONSTRAINT arena_swiss_pairings_round_slot_key UNIQUE (round_id, slot_number),
    CONSTRAINT arena_swiss_pairings_round_fk FOREIGN KEY (round_id, roster_id)
        REFERENCES arena_swiss_rounds(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_pairings_override_fk FOREIGN KEY (repeat_override_id, round_id)
        REFERENCES arena_swiss_repeat_overrides(id, round_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_pairings_slot_check CHECK (slot_number BETWEEN 1 AND 8)
);

CREATE TABLE arena_swiss_pairing_members (
    pairing_id UUID NOT NULL,
    round_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    seat SMALLINT NOT NULL,
    participant_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pairing_id, seat),
    CONSTRAINT arena_swiss_pairing_members_pair_participant_key UNIQUE (pairing_id, participant_id),
    CONSTRAINT arena_swiss_pairing_members_round_participant_key UNIQUE (round_id, participant_id),
    CONSTRAINT arena_swiss_pairing_members_pairing_fk FOREIGN KEY (pairing_id, round_id, roster_id)
        REFERENCES arena_swiss_pairings(id, round_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_pairing_members_participant_fk FOREIGN KEY (roster_id, participant_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_pairing_members_seat_check CHECK (seat IN (1, 2))
);

CREATE TABLE arena_swiss_opponent_history (
    pairing_id UUID PRIMARY KEY,
    round_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    prior_meeting_count SMALLINT NOT NULL,
    repeat_override_id UUID,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_swiss_opponent_history_pairing_fk FOREIGN KEY (pairing_id, round_id, roster_id)
        REFERENCES arena_swiss_pairings(id, round_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_opponent_history_override_fk FOREIGN KEY (repeat_override_id, round_id)
        REFERENCES arena_swiss_repeat_overrides(id, round_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_opponent_history_repeat_check CHECK (
        (
            prior_meeting_count = 0
            AND repeat_override_id IS NULL
        )
        OR (
            prior_meeting_count > 0
            AND repeat_override_id IS NOT NULL
        )
    )
);

CREATE TABLE arena_swiss_byes (
    round_id UUID PRIMARY KEY,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    points_awarded SMALLINT NOT NULL DEFAULT 1,
    decision_evidence_id UUID NOT NULL UNIQUE,
    decision_algorithm_version VARCHAR(64) NOT NULL,
    decision_inputs JSONB NOT NULL,
    decision_seed BYTEA NOT NULL,
    decision_result JSONB NOT NULL,
    decision_replay_digest BYTEA NOT NULL,
    decision_owner_id UUID NOT NULL,
    decided_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_swiss_byes_roster_participant_key UNIQUE (roster_id, participant_id),
    CONSTRAINT arena_swiss_byes_round_fk FOREIGN KEY (round_id, roster_id)
        REFERENCES arena_swiss_rounds(id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_byes_participant_fk FOREIGN KEY (roster_id, participant_id)
        REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_swiss_byes_points_check CHECK (points_awarded = 1),
    CONSTRAINT arena_swiss_byes_decision_check CHECK (
        decision_algorithm_version = 'hmac-sha256-order-v1'
        AND jsonb_typeof(decision_inputs) = 'array'
        AND jsonb_array_length(decision_inputs) > 0
        AND octet_length(decision_seed) = 32
        AND decision_seed <> decode(repeat('00', 32), 'hex')
        AND jsonb_typeof(decision_result) = 'array'
        AND jsonb_array_length(decision_result) > 0
        AND octet_length(decision_replay_digest) = 32
        AND decision_owner_id = round_id
    ),
    CONSTRAINT arena_swiss_byes_timestamps_check CHECK (decided_at <= created_at)
);

CREATE INDEX arena_swiss_pairings_round_idx
    ON arena_swiss_pairings (round_id, slot_number);

CREATE INDEX arena_swiss_opponent_history_roster_idx
    ON arena_swiss_opponent_history (roster_id, recorded_at, pairing_id);

-- +goose Down

DROP INDEX IF EXISTS arena_swiss_opponent_history_roster_idx;
DROP INDEX IF EXISTS arena_swiss_pairings_round_idx;
DROP TABLE IF EXISTS arena_swiss_byes;
DROP TABLE IF EXISTS arena_swiss_opponent_history;
DROP TABLE IF EXISTS arena_swiss_pairing_members;
DROP TABLE IF EXISTS arena_swiss_pairings;
DROP TABLE IF EXISTS arena_swiss_repeat_overrides;
DROP TABLE IF EXISTS arena_swiss_rounds;
DROP INDEX IF EXISTS arena_participants_roster_id_id_idx;
