-- +goose Up

-- Assignment rows retain the exact Game attempt scope without depending on a
-- mutable task row for official task evidence.
CREATE UNIQUE INDEX arena_game_attempts_assignment_identity_idx
    ON arena_game_attempts (id, series_id, roster_id);

CREATE TABLE arena_assignment_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    kind VARCHAR(16) NOT NULL,
    parent_plan_id UUID REFERENCES arena_assignment_plans(id) ON DELETE RESTRICT,
    revision_id UUID NOT NULL UNIQUE,
    source_roster_revision BIGINT NOT NULL,
    source_pool_revision_id UUID NOT NULL,
    source_draft_revision_id UUID REFERENCES arena_draft_revisions(id) ON DELETE RESTRICT,
    reachable_branch_count INTEGER NOT NULL DEFAULT 0,
    constraint_graph JSONB NOT NULL,
    proof_evidence JSONB NOT NULL,
    decision_evidence_id UUID UNIQUE,
    decision_algorithm_version VARCHAR(64),
    decision_inputs JSONB,
    decision_seed BYTEA,
    decision_result JSONB,
    decision_replay_digest BYTEA,
    decision_owner_id UUID,
    decided_at TIMESTAMPTZ,
    state VARCHAR(16) NOT NULL DEFAULT 'planned',
    active_branch_id UUID,
    committed_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,
    supersession_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_assignment_plans_id_roster_key UNIQUE (id, roster_id),
    CONSTRAINT arena_assignment_plans_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_assignment_plans_kind_check CHECK (
        kind IN ('conservative', 'exact')
    ),
    CONSTRAINT arena_assignment_plans_source_check CHECK (
        source_roster_revision >= 1
        AND (
            (
                kind = 'conservative'
                AND parent_plan_id IS NULL
                AND source_draft_revision_id IS NULL
                AND reachable_branch_count = 0
            )
            OR (
                kind = 'exact'
                AND parent_plan_id IS NOT NULL
                AND source_draft_revision_id IS NOT NULL
                AND reachable_branch_count >= 1
            )
        )
    ),
    CONSTRAINT arena_assignment_plans_proof_check CHECK (
        jsonb_typeof(constraint_graph) = 'object'
        AND constraint_graph <> '{}'::JSONB
        AND jsonb_typeof(proof_evidence) = 'object'
        AND proof_evidence <> '{}'::JSONB
    ),
    CONSTRAINT arena_assignment_plans_decision_check CHECK (
        (
            kind = 'conservative'
            AND decision_evidence_id IS NULL
            AND decision_algorithm_version IS NULL
            AND decision_inputs IS NULL
            AND decision_seed IS NULL
            AND decision_result IS NULL
            AND decision_replay_digest IS NULL
            AND decision_owner_id IS NULL
            AND decided_at IS NULL
        )
        OR (
            kind = 'exact'
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
            AND decided_at <= created_at
        )
    ),
    CONSTRAINT arena_assignment_plans_state_check CHECK (
        state IN ('planned', 'committed', 'superseded')
    ),
    CONSTRAINT arena_assignment_plans_state_evidence_check CHECK (
        (
            state = 'planned'
            AND active_branch_id IS NULL
            AND committed_at IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'committed'
            AND kind = 'exact'
            AND active_branch_id IS NOT NULL
            AND committed_at IS NOT NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'superseded'
            AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL
            AND supersession_reason = BTRIM(supersession_reason)
            AND supersession_reason <> ''
        )
    ),
    CONSTRAINT arena_assignment_plans_timestamps_check CHECK (
        (committed_at IS NULL OR committed_at >= created_at)
        AND (superseded_at IS NULL OR superseded_at >= COALESCE(committed_at, created_at))
    )
);

CREATE TABLE arena_assignment_branches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL,
    draft_id UUID NOT NULL,
    draft_revision_id UUID NOT NULL,
    branch_key VARCHAR(128) NOT NULL,
    category_sequence JSONB NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'reserved',
    disclosed_at TIMESTAMPTZ,
    activated_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    release_reason TEXT,
    superseded_at TIMESTAMPTZ,
    supersession_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_assignment_branches_id_plan_key UNIQUE (id, plan_id),
    CONSTRAINT arena_assignment_branches_plan_branch_key UNIQUE (plan_id, branch_key),
    CONSTRAINT arena_assignment_branches_plan_fk FOREIGN KEY (plan_id)
        REFERENCES arena_assignment_plans(id) ON DELETE RESTRICT,
    CONSTRAINT arena_assignment_branches_draft_revision_fk FOREIGN KEY (
        draft_revision_id,
        draft_id
    ) REFERENCES arena_draft_revisions(id, draft_id) ON DELETE RESTRICT,
    CONSTRAINT arena_assignment_branches_key_check CHECK (
        branch_key = BTRIM(branch_key) AND branch_key <> ''
    ),
    CONSTRAINT arena_assignment_branches_category_check CHECK (
        jsonb_typeof(category_sequence) = 'array'
        AND jsonb_array_length(category_sequence) BETWEEN 1 AND 3
    ),
    CONSTRAINT arena_assignment_branches_state_check CHECK (
        state IN ('reserved', 'active', 'released', 'superseded')
    ),
    CONSTRAINT arena_assignment_branches_state_evidence_check CHECK (
        (
            state = 'reserved'
            AND disclosed_at IS NULL
            AND activated_at IS NULL
            AND released_at IS NULL
            AND release_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'active'
            AND activated_at IS NOT NULL
            AND released_at IS NULL
            AND release_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'released'
            AND disclosed_at IS NULL
            AND activated_at IS NULL
            AND released_at IS NOT NULL
            AND release_reason IS NOT NULL
            AND release_reason = BTRIM(release_reason)
            AND release_reason <> ''
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'superseded'
            AND released_at IS NULL
            AND release_reason IS NULL
            AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL
            AND supersession_reason = BTRIM(supersession_reason)
            AND supersession_reason <> ''
        )
    ),
    CONSTRAINT arena_assignment_branches_timestamps_check CHECK (
        (disclosed_at IS NULL OR disclosed_at >= created_at)
        AND (activated_at IS NULL OR activated_at >= created_at)
        AND (released_at IS NULL OR released_at >= created_at)
        AND (superseded_at IS NULL OR superseded_at >= COALESCE(activated_at, created_at))
    )
);

ALTER TABLE arena_assignment_plans
    ADD CONSTRAINT arena_assignment_plans_active_branch_fk FOREIGN KEY (
        active_branch_id,
        id
    ) REFERENCES arena_assignment_branches(id, plan_id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX arena_assignment_branches_one_active_idx
    ON arena_assignment_branches (plan_id)
    WHERE state = 'active';

CREATE TABLE arena_assignment_plan_edges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL,
    branch_id UUID NOT NULL,
    position SMALLINT NOT NULL,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    task_version INTEGER NOT NULL,
    selection_evidence JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_assignment_plan_edges_identity_key
        UNIQUE (id, plan_id, branch_id, task_id, task_version),
    CONSTRAINT arena_assignment_plan_edges_branch_position_key
        UNIQUE (branch_id, position),
    CONSTRAINT arena_assignment_plan_edges_plan_version_key
        UNIQUE (plan_id, task_id, task_version),
    CONSTRAINT arena_assignment_plan_edges_branch_fk FOREIGN KEY (branch_id, plan_id)
        REFERENCES arena_assignment_branches(id, plan_id) ON DELETE RESTRICT,
    CONSTRAINT arena_assignment_plan_edges_position_check CHECK (position BETWEEN 1 AND 3),
    CONSTRAINT arena_assignment_plan_edges_version_check CHECK (task_version >= 1),
    CONSTRAINT arena_assignment_plan_edges_evidence_check CHECK (
        jsonb_typeof(selection_evidence) = 'object'
        AND selection_evidence <> '{}'::JSONB
    )
);

CREATE TABLE arena_task_version_reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    edge_id UUID NOT NULL UNIQUE,
    plan_id UUID NOT NULL,
    branch_id UUID NOT NULL,
    task_id UUID NOT NULL,
    task_version INTEGER NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'reserved',
    disclosed_at TIMESTAMPTZ,
    committed_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    release_reason TEXT,
    superseded_at TIMESTAMPTZ,
    supersession_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_task_version_reservations_identity_key
        UNIQUE (id, plan_id, branch_id, task_id, task_version),
    CONSTRAINT arena_task_version_reservations_task_key
        UNIQUE (id, task_id, task_version),
    CONSTRAINT arena_task_version_reservations_edge_fk FOREIGN KEY (
        edge_id,
        plan_id,
        branch_id,
        task_id,
        task_version
    ) REFERENCES arena_assignment_plan_edges (
        id,
        plan_id,
        branch_id,
        task_id,
        task_version
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_task_version_reservations_version_check CHECK (task_version >= 1),
    CONSTRAINT arena_task_version_reservations_state_check CHECK (
        state IN ('reserved', 'committed', 'released', 'superseded')
    ),
    CONSTRAINT arena_task_version_reservations_state_evidence_check CHECK (
        (
            state = 'reserved'
            AND disclosed_at IS NULL
            AND committed_at IS NULL
            AND released_at IS NULL
            AND release_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'committed'
            AND committed_at IS NOT NULL
            AND released_at IS NULL
            AND release_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'released'
            AND disclosed_at IS NULL
            AND committed_at IS NULL
            AND released_at IS NOT NULL
            AND release_reason IS NOT NULL
            AND release_reason = BTRIM(release_reason)
            AND release_reason <> ''
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'superseded'
            AND released_at IS NULL
            AND release_reason IS NULL
            AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL
            AND supersession_reason = BTRIM(supersession_reason)
            AND supersession_reason <> ''
        )
    ),
    CONSTRAINT arena_task_version_reservations_timestamps_check CHECK (
        (disclosed_at IS NULL OR disclosed_at >= created_at)
        AND (committed_at IS NULL OR committed_at >= created_at)
        AND (released_at IS NULL OR released_at >= created_at)
        AND (superseded_at IS NULL OR superseded_at >= COALESCE(committed_at, created_at))
    )
);

CREATE UNIQUE INDEX arena_task_version_reservations_active_idx
    ON arena_task_version_reservations (task_id, task_version)
    WHERE state IN ('reserved', 'committed');

CREATE TABLE arena_task_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id UUID NOT NULL UNIQUE,
    task_id UUID NOT NULL,
    task_version INTEGER NOT NULL,
    kind VARCHAR(16) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    category VARCHAR(32) NOT NULL,
    difficulty VARCHAR(16) NOT NULL,
    time_limit INTEGER NOT NULL,
    flag VARCHAR(255) NOT NULL,
    hints JSONB NOT NULL DEFAULT '[]'::JSONB,
    task_url TEXT,
    source_file_url TEXT,
    content_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_task_snapshots_identity_key
        UNIQUE (id, reservation_id, task_id, task_version),
    CONSTRAINT arena_task_snapshots_task_identity_key UNIQUE (id, task_id, task_version),
    CONSTRAINT arena_task_snapshots_reservation_fk FOREIGN KEY (
        reservation_id,
        task_id,
        task_version
    ) REFERENCES arena_task_version_reservations (
        id,
        task_id,
        task_version
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_task_snapshots_version_check CHECK (task_version >= 1),
    CONSTRAINT arena_task_snapshots_kind_check CHECK (kind IN ('normal', 'golden')),
    CONSTRAINT arena_task_snapshots_content_check CHECK (
        BTRIM(title) <> ''
        AND BTRIM(description) <> ''
        AND BTRIM(flag) <> ''
        AND time_limit > 0
        AND jsonb_typeof(hints) = 'array'
        AND octet_length(content_digest) = 32
        AND content_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT arena_task_snapshots_category_check CHECK (
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
    CONSTRAINT arena_task_snapshots_difficulty_check CHECK (
        difficulty IN ('easy', 'medium', 'hard')
    )
);

CREATE TABLE arena_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL,
    series_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    plan_id UUID NOT NULL,
    branch_id UUID NOT NULL,
    reservation_id UUID NOT NULL,
    snapshot_id UUID NOT NULL,
    task_id UUID NOT NULL,
    task_version INTEGER NOT NULL,
    supersedes_assignment_id UUID UNIQUE REFERENCES arena_assignments(id) ON DELETE RESTRICT,
    state VARCHAR(16) NOT NULL DEFAULT 'active',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,
    supersession_reason TEXT,
    CONSTRAINT arena_assignments_id_attempt_roster_key UNIQUE (id, attempt_id, roster_id),
    CONSTRAINT arena_assignments_attempt_fk FOREIGN KEY (attempt_id, series_id, roster_id)
        REFERENCES arena_game_attempts(id, series_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_assignments_branch_fk FOREIGN KEY (branch_id, plan_id)
        REFERENCES arena_assignment_branches(id, plan_id) ON DELETE RESTRICT,
    CONSTRAINT arena_assignments_reservation_fk FOREIGN KEY (
        reservation_id,
        plan_id,
        branch_id,
        task_id,
        task_version
    ) REFERENCES arena_task_version_reservations (
        id,
        plan_id,
        branch_id,
        task_id,
        task_version
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_assignments_snapshot_fk FOREIGN KEY (
        snapshot_id,
        reservation_id,
        task_id,
        task_version
    ) REFERENCES arena_task_snapshots (
        id,
        reservation_id,
        task_id,
        task_version
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_assignments_state_check CHECK (
        state IN ('active', 'completed', 'superseded')
    ),
    CONSTRAINT arena_assignments_revision_check CHECK (revision >= 1),
    CONSTRAINT arena_assignments_state_evidence_check CHECK (
        (
            state = 'active'
            AND completed_at IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'completed'
            AND completed_at IS NOT NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'superseded'
            AND completed_at IS NULL
            AND superseded_at IS NOT NULL
            AND supersession_reason IS NOT NULL
            AND supersession_reason = BTRIM(supersession_reason)
            AND supersession_reason <> ''
        )
    ),
    CONSTRAINT arena_assignments_timestamps_check CHECK (
        updated_at >= created_at
        AND (completed_at IS NULL OR completed_at >= created_at)
        AND (superseded_at IS NULL OR superseded_at >= created_at)
    )
);

CREATE UNIQUE INDEX arena_assignments_one_active_attempt_idx
    ON arena_assignments (attempt_id)
    WHERE state = 'active';

CREATE TABLE arena_task_delivery_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id UUID NOT NULL,
    attempt_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    snapshot_id UUID NOT NULL,
    task_id UUID NOT NULL,
    task_version INTEGER NOT NULL,
    delivered_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_task_delivery_receipts_assignment_participant_key
        UNIQUE (assignment_id, participant_id),
    CONSTRAINT arena_task_delivery_receipts_participant_task_key
        UNIQUE (participant_id, task_id),
    CONSTRAINT arena_task_delivery_receipts_assignment_fk FOREIGN KEY (
        assignment_id,
        attempt_id,
        roster_id
    ) REFERENCES arena_assignments(id, attempt_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT arena_task_delivery_receipts_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_task_delivery_receipts_snapshot_fk FOREIGN KEY (
        snapshot_id,
        task_id,
        task_version
    ) REFERENCES arena_task_snapshots(id, task_id, task_version) ON DELETE RESTRICT,
    CONSTRAINT arena_task_delivery_receipts_timestamps_check CHECK (
        delivered_at <= created_at
    )
);

CREATE INDEX arena_assignment_plans_roster_state_idx
    ON arena_assignment_plans (roster_id, state, created_at);

CREATE INDEX arena_assignment_branches_plan_state_idx
    ON arena_assignment_branches (plan_id, state, created_at);

CREATE INDEX arena_task_delivery_receipts_participant_created_idx
    ON arena_task_delivery_receipts (participant_id, delivered_at, id);

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_plan_guard() RETURNS TRIGGER AS $$
DECLARE
    parent_kind VARCHAR(16);
    parent_tournament_id UUID;
    parent_roster_id UUID;
    parent_roster_revision BIGINT;
    parent_pool_revision_id UUID;
    branch_count INTEGER;
    active_count INTEGER;
    incomplete_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.kind = 'exact' THEN
            SELECT
                kind,
                tournament_id,
                roster_id,
                source_roster_revision,
                source_pool_revision_id
            INTO
                parent_kind,
                parent_tournament_id,
                parent_roster_id,
                parent_roster_revision,
                parent_pool_revision_id
            FROM arena_assignment_plans
            WHERE id = NEW.parent_plan_id;

            IF parent_kind <> 'conservative'
                OR parent_tournament_id IS DISTINCT FROM NEW.tournament_id
                OR parent_roster_id IS DISTINCT FROM NEW.roster_id
                OR parent_roster_revision IS DISTINCT FROM NEW.source_roster_revision
                OR parent_pool_revision_id IS DISTINCT FROM NEW.source_pool_revision_id THEN
                RAISE EXCEPTION 'exact Arena assignment plan requires its matching conservative proof'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.kind IS DISTINCT FROM OLD.kind
        OR NEW.parent_plan_id IS DISTINCT FROM OLD.parent_plan_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.source_roster_revision IS DISTINCT FROM OLD.source_roster_revision
        OR NEW.source_pool_revision_id IS DISTINCT FROM OLD.source_pool_revision_id
        OR NEW.source_draft_revision_id IS DISTINCT FROM OLD.source_draft_revision_id
        OR NEW.reachable_branch_count IS DISTINCT FROM OLD.reachable_branch_count
        OR NEW.constraint_graph IS DISTINCT FROM OLD.constraint_graph
        OR NEW.proof_evidence IS DISTINCT FROM OLD.proof_evidence
        OR NEW.decision_evidence_id IS DISTINCT FROM OLD.decision_evidence_id
        OR NEW.decision_algorithm_version IS DISTINCT FROM OLD.decision_algorithm_version
        OR NEW.decision_inputs IS DISTINCT FROM OLD.decision_inputs
        OR NEW.decision_seed IS DISTINCT FROM OLD.decision_seed
        OR NEW.decision_result IS DISTINCT FROM OLD.decision_result
        OR NEW.decision_replay_digest IS DISTINCT FROM OLD.decision_replay_digest
        OR NEW.decision_owner_id IS DISTINCT FROM OLD.decision_owner_id
        OR NEW.decided_at IS DISTINCT FROM OLD.decided_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena assignment proof identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'superseded'
        OR (OLD.state = 'planned' AND NEW.state NOT IN ('planned', 'committed', 'superseded'))
        OR (OLD.state = 'committed' AND NEW.state NOT IN ('committed', 'superseded')) THEN
        RAISE EXCEPTION 'invalid Arena assignment plan transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'committed' AND OLD.state <> 'committed' THEN
        SELECT COUNT(*)
        INTO branch_count
        FROM arena_assignment_branches
        WHERE plan_id = NEW.id;

        SELECT COUNT(*)
        INTO active_count
        FROM arena_assignment_branches
        WHERE plan_id = NEW.id AND state = 'active' AND id = NEW.active_branch_id;

        SELECT COUNT(*)
        INTO incomplete_count
        FROM arena_assignment_branches AS branch
        WHERE branch.plan_id = NEW.id
            AND (
                (
                    branch.id = NEW.active_branch_id
                    AND branch.state <> 'active'
                )
                OR (
                    branch.id <> NEW.active_branch_id
                    AND branch.state <> 'released'
                )
                OR (
                    SELECT COUNT(*)
                    FROM arena_assignment_plan_edges AS edge
                    WHERE edge.branch_id = branch.id
                ) <> 3
                OR (
                    SELECT COUNT(*)
                    FROM arena_task_version_reservations AS reservation
                    JOIN arena_task_snapshots AS snapshot
                        ON snapshot.reservation_id = reservation.id
                    WHERE reservation.branch_id = branch.id
                ) <> 3
            );

        IF branch_count <> NEW.reachable_branch_count
            OR active_count <> 1
            OR incomplete_count <> 0 THEN
            RAISE EXCEPTION 'exact Arena plan must cover every branch with one primary and two reserves'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_plan_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_assignment_plans
FOR EACH ROW EXECUTE FUNCTION arena_assignment_plan_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_branch_guard() RETURNS TRIGGER AS $$
DECLARE
    plan_kind VARCHAR(16);
    plan_state VARCHAR(16);
    source_revision_id UUID;
    plan_roster_id UUID;
    revision_roster_id UUID;
    committed_reservations INTEGER;
    reusable_reservations INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT kind, state, source_draft_revision_id, roster_id
        INTO plan_kind, plan_state, source_revision_id, plan_roster_id
        FROM arena_assignment_plans
        WHERE id = NEW.plan_id
        FOR UPDATE;

        SELECT roster_id
        INTO revision_roster_id
        FROM arena_draft_revisions
        WHERE id = NEW.draft_revision_id;

        IF plan_kind <> 'exact'
            OR plan_state <> 'planned'
            OR NEW.draft_revision_id IS DISTINCT FROM source_revision_id
            OR revision_roster_id IS DISTINCT FROM plan_roster_id THEN
            RAISE EXCEPTION 'Arena branches belong only to the current exact plan revision'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.draft_id IS DISTINCT FROM OLD.draft_id
        OR NEW.draft_revision_id IS DISTINCT FROM OLD.draft_revision_id
        OR NEW.branch_key IS DISTINCT FROM OLD.branch_key
        OR NEW.category_sequence IS DISTINCT FROM OLD.category_sequence
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena assignment branch identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state IN ('released', 'superseded')
        OR (OLD.state = 'reserved' AND NEW.state NOT IN (
            'reserved', 'active', 'released', 'superseded'
        ))
        OR (OLD.state = 'active' AND NEW.state NOT IN ('active', 'superseded')) THEN
        RAISE EXCEPTION 'invalid Arena assignment branch transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*)
        INTO committed_reservations
        FROM arena_task_version_reservations
        WHERE branch_id = NEW.id AND state = 'committed';

        IF committed_reservations <> 3 THEN
            RAISE EXCEPTION 'active Arena branch requires one committed primary and two reserves'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.state = 'released' AND OLD.state <> 'released' THEN
        SELECT COUNT(*)
        INTO reusable_reservations
        FROM arena_task_version_reservations
        WHERE branch_id = NEW.id AND state = 'released' AND disclosed_at IS NULL;

        IF reusable_reservations <> 3 THEN
            RAISE EXCEPTION 'released Arena branch must retain three undisclosed released reservations'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_branch_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_assignment_branches
FOR EACH ROW EXECUTE FUNCTION arena_assignment_branch_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_edge_guard() RETURNS TRIGGER AS $$
DECLARE
    plan_state VARCHAR(16);
    branch_state VARCHAR(16);
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'Arena assignment selected edges are immutable proof evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT plan.state, branch.state
    INTO plan_state, branch_state
    FROM arena_assignment_plans AS plan
    JOIN arena_assignment_branches AS branch ON branch.plan_id = plan.id
    WHERE plan.id = NEW.plan_id AND branch.id = NEW.branch_id
    FOR UPDATE OF plan, branch;

    IF plan_state <> 'planned' OR branch_state <> 'reserved' THEN
        RAISE EXCEPTION 'selected edges can be added only to a reserved draft branch'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_edge_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_assignment_plan_edges
FOR EACH ROW EXECUTE FUNCTION arena_assignment_edge_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_task_version_reservation_guard() RETURNS TRIGGER AS $$
DECLARE
    branch_state VARCHAR(16);
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT state
        INTO branch_state
        FROM arena_assignment_branches
        WHERE id = NEW.branch_id
        FOR UPDATE;

        IF branch_state <> 'reserved' THEN
            RAISE EXCEPTION 'task versions can be reserved only for an uncommitted branch'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.edge_id IS DISTINCT FROM OLD.edge_id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.branch_id IS DISTINCT FROM OLD.branch_id
        OR NEW.task_id IS DISTINCT FROM OLD.task_id
        OR NEW.task_version IS DISTINCT FROM OLD.task_version
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena task-version reservation identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at THEN
        IF OLD.state <> 'committed'
            OR OLD.disclosed_at IS NOT NULL
            OR NEW.disclosed_at IS NULL THEN
            RAISE EXCEPTION 'Arena reservation disclosure is monotonic and committed-only'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF OLD.state IN ('released', 'superseded')
        OR (OLD.state = 'reserved' AND NEW.state NOT IN (
            'reserved', 'committed', 'released', 'superseded'
        ))
        OR (OLD.state = 'committed' AND NEW.state NOT IN (
            'committed', 'superseded'
        )) THEN
        RAISE EXCEPTION 'invalid Arena task-version reservation transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'released' AND NEW.disclosed_at IS NOT NULL THEN
        RAISE EXCEPTION 'a disclosed Arena task version cannot be released for reuse'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_task_version_reservation_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_task_version_reservations
FOR EACH ROW EXECUTE FUNCTION arena_task_version_reservation_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_assignment_guard() RETURNS TRIGGER AS $$
DECLARE
    plan_state VARCHAR(16);
    plan_active_branch_id UUID;
    branch_state VARCHAR(16);
    reservation_state VARCHAR(16);
    previous_attempt_id UUID;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT plan.state, plan.active_branch_id, branch.state, reservation.state
        INTO plan_state, plan_active_branch_id, branch_state, reservation_state
        FROM arena_assignment_plans AS plan
        JOIN arena_assignment_branches AS branch
            ON branch.plan_id = plan.id AND branch.id = NEW.branch_id
        JOIN arena_task_version_reservations AS reservation
            ON reservation.plan_id = plan.id
            AND reservation.branch_id = branch.id
            AND reservation.id = NEW.reservation_id
        WHERE plan.id = NEW.plan_id;

        IF NEW.state <> 'active'
            OR plan_state <> 'committed'
            OR plan_active_branch_id IS DISTINCT FROM NEW.branch_id
            OR branch_state <> 'active'
            OR reservation_state <> 'committed' THEN
            RAISE EXCEPTION 'Arena assignment requires the committed active branch and reservation'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.supersedes_assignment_id IS NOT NULL THEN
            SELECT attempt_id
            INTO previous_attempt_id
            FROM arena_assignments
            WHERE id = NEW.supersedes_assignment_id AND state = 'superseded';

            IF previous_attempt_id IS DISTINCT FROM NEW.attempt_id THEN
                RAISE EXCEPTION 'Arena reserve assignment must supersede the same Game attempt'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE'
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
        OR NEW.branch_id IS DISTINCT FROM OLD.branch_id
        OR NEW.reservation_id IS DISTINCT FROM OLD.reservation_id
        OR NEW.snapshot_id IS DISTINCT FROM OLD.snapshot_id
        OR NEW.task_id IS DISTINCT FROM OLD.task_id
        OR NEW.task_version IS DISTINCT FROM OLD.task_version
        OR NEW.supersedes_assignment_id IS DISTINCT FROM OLD.supersedes_assignment_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Arena assignment identity and snapshot are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state <> 'active' OR NEW.state NOT IN ('active', 'completed', 'superseded') THEN
        RAISE EXCEPTION 'terminal Arena assignment is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_assignment_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_assignments
FOR EACH ROW EXECUTE FUNCTION arena_assignment_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_delivery_receipt_guard() RETURNS TRIGGER AS $$
DECLARE
    first_participant_id UUID;
    second_participant_id UUID;
    disclosed_at TIMESTAMPTZ;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Arena task delivery receipts are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT series.first_participant_id, series.second_participant_id, reservation.disclosed_at
    INTO first_participant_id, second_participant_id, disclosed_at
    FROM arena_assignments AS assignment
    JOIN arena_series AS series ON series.id = assignment.series_id
    JOIN arena_task_version_reservations AS reservation
        ON reservation.id = assignment.reservation_id
    WHERE assignment.id = NEW.assignment_id AND assignment.state = 'active';

    IF NEW.participant_id NOT IN (first_participant_id, second_participant_id)
        OR disclosed_at IS NULL
        OR disclosed_at > NEW.delivered_at THEN
        RAISE EXCEPTION 'Arena receipt requires committed disclosure to a Series participant'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_delivery_receipt_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION arena_delivery_receipt_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_task_snapshot_immutable_guard() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Arena task snapshots are immutable official evidence'
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_task_snapshot_immutable_guard
BEFORE UPDATE OR DELETE ON arena_task_snapshots
FOR EACH ROW EXECUTE FUNCTION arena_task_snapshot_immutable_guard();

-- +goose Down

DROP TABLE IF EXISTS arena_task_delivery_receipts;
DROP TABLE IF EXISTS arena_assignments;
DROP TABLE IF EXISTS arena_task_snapshots;
DROP TABLE IF EXISTS arena_task_version_reservations;
DROP TABLE IF EXISTS arena_assignment_plan_edges;
ALTER TABLE IF EXISTS arena_assignment_plans
    DROP CONSTRAINT IF EXISTS arena_assignment_plans_active_branch_fk;
DROP TABLE IF EXISTS arena_assignment_branches;
DROP TABLE IF EXISTS arena_assignment_plans;
DROP FUNCTION IF EXISTS arena_task_snapshot_immutable_guard();
DROP FUNCTION IF EXISTS arena_delivery_receipt_guard();
DROP FUNCTION IF EXISTS arena_assignment_guard();
DROP FUNCTION IF EXISTS arena_task_version_reservation_guard();
DROP FUNCTION IF EXISTS arena_assignment_edge_guard();
DROP FUNCTION IF EXISTS arena_assignment_branch_guard();
DROP FUNCTION IF EXISTS arena_assignment_plan_guard();
DROP INDEX IF EXISTS arena_game_attempts_assignment_identity_idx;
