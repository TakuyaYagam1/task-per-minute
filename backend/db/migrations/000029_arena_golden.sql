-- +goose Up

-- Golden is a multi-participant stage. Its attempts and evidence stay separate
-- from two-participant Series and Duel persistence.
CREATE TABLE arena_golden_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    attempt_number INTEGER NOT NULL,
    previous_attempt_id UUID,
    state VARCHAR(24) NOT NULL DEFAULT 'prepared',
    disclosed_at TIMESTAMPTZ,
    ready_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    paused_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    cancellation_reason TEXT,
    superseded_at TIMESTAMPTZ,
    supersession_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_attempts_identity_key
        UNIQUE (id, tournament_id, roster_id),
    CONSTRAINT arena_golden_attempts_number_key
        UNIQUE (tournament_id, attempt_number),
    CONSTRAINT arena_golden_attempts_previous_key UNIQUE (previous_attempt_id),
    CONSTRAINT arena_golden_attempts_roster_fk FOREIGN KEY (
        roster_id,
        tournament_id
    ) REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_attempts_previous_fk FOREIGN KEY (
        previous_attempt_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_attempts (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_attempts_number_check CHECK (
        attempt_number >= 1
        AND previous_attempt_id IS DISTINCT FROM id
        AND (
            (attempt_number = 1 AND previous_attempt_id IS NULL)
            OR (attempt_number > 1 AND previous_attempt_id IS NOT NULL)
        )
    ),
    CONSTRAINT arena_golden_attempts_state_check CHECK (
        state IN (
            'prepared',
            'ready',
            'active',
            'technical_pause',
            'completed',
            'cancelled',
            'superseded'
        )
    ),
    CONSTRAINT arena_golden_attempts_state_evidence_check CHECK (
        (
            state = 'prepared'
            AND disclosed_at IS NULL
            AND ready_at IS NULL
            AND started_at IS NULL
            AND paused_at IS NULL
            AND completed_at IS NULL
            AND cancelled_at IS NULL
            AND cancellation_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'ready'
            AND disclosed_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND started_at IS NULL
            AND paused_at IS NULL
            AND completed_at IS NULL
            AND cancelled_at IS NULL
            AND cancellation_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'active'
            AND disclosed_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND started_at IS NOT NULL
            AND paused_at IS NULL
            AND completed_at IS NULL
            AND cancelled_at IS NULL
            AND cancellation_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'technical_pause'
            AND disclosed_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND started_at IS NOT NULL
            AND paused_at IS NOT NULL
            AND completed_at IS NULL
            AND cancelled_at IS NULL
            AND cancellation_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'completed'
            AND disclosed_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND started_at IS NOT NULL
            AND paused_at IS NULL
            AND completed_at IS NOT NULL
            AND cancelled_at IS NULL
            AND cancellation_reason IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'cancelled'
            AND paused_at IS NULL
            AND completed_at IS NULL
            AND cancelled_at IS NOT NULL
            AND cancellation_reason = BTRIM(cancellation_reason)
            AND cancellation_reason <> ''
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'superseded'
            AND started_at IS NULL
            AND paused_at IS NULL
            AND completed_at IS NULL
            AND cancelled_at IS NULL
            AND cancellation_reason IS NULL
            AND superseded_at IS NOT NULL
            AND supersession_reason = BTRIM(supersession_reason)
            AND supersession_reason <> ''
            AND (ready_at IS NULL OR disclosed_at IS NOT NULL)
        )
    ),
    CONSTRAINT arena_golden_attempts_timestamps_check CHECK (
        (disclosed_at IS NULL OR disclosed_at >= created_at)
        AND (ready_at IS NULL OR ready_at >= disclosed_at)
        AND (started_at IS NULL OR started_at >= ready_at)
        AND (paused_at IS NULL OR paused_at >= started_at)
        AND (completed_at IS NULL OR completed_at >= started_at)
        AND (cancelled_at IS NULL OR cancelled_at >= created_at)
        AND (superseded_at IS NULL OR superseded_at >= created_at)
    )
);

CREATE TABLE arena_golden_memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    selection_kind VARCHAR(16) NOT NULL,
    reserve_position SMALLINT,
    selected_at TIMESTAMPTZ NOT NULL,
    ready_at TIMESTAMPTZ,
    no_show_at TIMESTAMPTZ,
    participation_established_at TIMESTAMPTZ,
    excluded_at TIMESTAMPTZ,
    exclusion_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_memberships_identity_key UNIQUE (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_golden_memberships_attempt_participant_key
        UNIQUE (attempt_id, participant_id),
    CONSTRAINT arena_golden_memberships_attempt_fk FOREIGN KEY (
        attempt_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_attempts (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_memberships_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_memberships_selection_check CHECK (
        (
            selection_kind = 'direct'
            AND reserve_position IS NULL
            AND excluded_at IS NULL
            AND exclusion_reason IS NULL
        )
        OR (
            selection_kind = 'reserve'
            AND reserve_position BETWEEN 1 AND 16
            AND excluded_at IS NULL
            AND exclusion_reason IS NULL
        )
        OR (
            selection_kind = 'excluded'
            AND reserve_position IS NULL
            AND ready_at IS NULL
            AND no_show_at IS NULL
            AND participation_established_at IS NULL
            AND excluded_at IS NOT NULL
            AND exclusion_reason = BTRIM(exclusion_reason)
            AND exclusion_reason <> ''
        )
    ),
    CONSTRAINT arena_golden_memberships_readiness_check CHECK (
        NOT (ready_at IS NOT NULL AND no_show_at IS NOT NULL)
        AND (
            participation_established_at IS NULL
            OR (
                ready_at IS NOT NULL
                AND no_show_at IS NULL
                AND selection_kind <> 'excluded'
            )
        )
    ),
    CONSTRAINT arena_golden_memberships_timestamps_check CHECK (
        selected_at <= created_at
        AND (ready_at IS NULL OR ready_at >= selected_at)
        AND (no_show_at IS NULL OR no_show_at >= selected_at)
        AND (
            participation_established_at IS NULL
            OR participation_established_at >= ready_at
        )
        AND (excluded_at IS NULL OR excluded_at >= selected_at)
    )
);

CREATE UNIQUE INDEX arena_golden_memberships_reserve_position_idx
    ON arena_golden_memberships (attempt_id, reserve_position)
    WHERE selection_kind = 'reserve';

CREATE TABLE arena_golden_ready_disconnects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    membership_id UUID NOT NULL,
    attempt_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    sequence_number INTEGER NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'open',
    disconnected_at TIMESTAMPTZ NOT NULL,
    reconnected_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_ready_disconnects_identity_key UNIQUE (
        id,
        membership_id,
        attempt_id,
        participant_id
    ),
    CONSTRAINT arena_golden_ready_disconnects_sequence_key
        UNIQUE (membership_id, sequence_number),
    CONSTRAINT arena_golden_ready_disconnects_membership_fk FOREIGN KEY (
        membership_id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_memberships (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_ready_disconnects_number_check CHECK (
        sequence_number >= 1
    ),
    CONSTRAINT arena_golden_ready_disconnects_state_check CHECK (
        (
            state = 'open'
            AND reconnected_at IS NULL
            AND expired_at IS NULL
        )
        OR (
            state = 'reconnected'
            AND reconnected_at IS NOT NULL
            AND expired_at IS NULL
        )
        OR (
            state = 'expired'
            AND reconnected_at IS NULL
            AND expired_at IS NOT NULL
        )
    ),
    CONSTRAINT arena_golden_ready_disconnects_timestamps_check CHECK (
        disconnected_at <= created_at
        AND (reconnected_at IS NULL OR reconnected_at >= disconnected_at)
        AND (expired_at IS NULL OR expired_at >= disconnected_at)
    )
);

CREATE UNIQUE INDEX arena_golden_ready_disconnects_one_open_idx
    ON arena_golden_ready_disconnects (membership_id)
    WHERE state = 'open';

CREATE TABLE arena_golden_provisional_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    membership_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    server_sequence BIGINT NOT NULL,
    idempotency_key UUID NOT NULL UNIQUE,
    provisional_position SMALLINT NOT NULL,
    elapsed_milliseconds BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL,
    rejection_reason TEXT,
    payload_digest BYTEA NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_provisional_submissions_identity_key UNIQUE (
        id,
        attempt_id,
        membership_id,
        participant_id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_golden_provisional_submissions_sequence_key
        UNIQUE (attempt_id, server_sequence),
    CONSTRAINT arena_golden_provisional_submissions_member_sequence_key
        UNIQUE (membership_id, server_sequence),
    CONSTRAINT arena_golden_provisional_submissions_membership_fk FOREIGN KEY (
        membership_id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_memberships (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_provisional_submissions_sequence_check CHECK (
        server_sequence >= 1
        AND provisional_position BETWEEN 1 AND 16
        AND elapsed_milliseconds >= 0
    ),
    CONSTRAINT arena_golden_provisional_submissions_status_check CHECK (
        (status = 'accepted' AND rejection_reason IS NULL)
        OR (
            status = 'rejected'
            AND rejection_reason = BTRIM(rejection_reason)
            AND rejection_reason <> ''
        )
    ),
    CONSTRAINT arena_golden_provisional_submissions_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT arena_golden_provisional_submissions_timestamps_check CHECK (
        submitted_at <= received_at
        AND received_at <= created_at
    )
);

CREATE TABLE arena_golden_position_commits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    membership_id UUID NOT NULL,
    participant_id UUID NOT NULL,
    provisional_submission_id UUID NOT NULL UNIQUE,
    previous_position_commit_id UUID,
    position SMALLINT NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_position_commits_identity_key UNIQUE (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_golden_position_commits_previous_identity_key UNIQUE (
        id,
        participant_id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_golden_position_commits_scope_key UNIQUE (
        id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_golden_position_commits_member_key UNIQUE (membership_id),
    CONSTRAINT arena_golden_position_commits_attempt_position_key
        UNIQUE (attempt_id, position),
    CONSTRAINT arena_golden_position_commits_membership_fk FOREIGN KEY (
        membership_id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_memberships (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_position_commits_submission_fk FOREIGN KEY (
        provisional_submission_id,
        attempt_id,
        membership_id,
        participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_provisional_submissions (
        id,
        attempt_id,
        membership_id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_position_commits_previous_fk FOREIGN KEY (
        previous_position_commit_id,
        participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_position_commits (
        id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_position_commits_position_check CHECK (
        position BETWEEN 1 AND 16
        AND previous_position_commit_id IS DISTINCT FROM id
    ),
    CONSTRAINT arena_golden_position_commits_timestamps_check CHECK (
        committed_at <= created_at
    )
);

CREATE TABLE arena_golden_reserve_promotions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    reserve_membership_id UUID NOT NULL UNIQUE,
    reserve_participant_id UUID NOT NULL,
    replaced_membership_id UUID NOT NULL UNIQUE,
    replaced_participant_id UUID NOT NULL,
    reason TEXT NOT NULL,
    promoted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_reserve_promotions_identity_key
        UNIQUE (id, attempt_id, reserve_membership_id),
    CONSTRAINT arena_golden_reserve_promotions_reserve_fk FOREIGN KEY (
        reserve_membership_id,
        attempt_id,
        reserve_participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_memberships (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_reserve_promotions_replaced_fk FOREIGN KEY (
        replaced_membership_id,
        attempt_id,
        replaced_participant_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_memberships (
        id,
        attempt_id,
        participant_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_reserve_promotions_participant_check CHECK (
        reserve_participant_id <> replaced_participant_id
    ),
    CONSTRAINT arena_golden_reserve_promotions_reason_check CHECK (
        reason = BTRIM(reason) AND reason <> ''
    ),
    CONSTRAINT arena_golden_reserve_promotions_timestamps_check CHECK (
        promoted_at <= created_at
    )
);

CREATE TABLE arena_golden_recovery_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    revision_number BIGINT NOT NULL,
    previous_revision_id UUID,
    state VARCHAR(24) NOT NULL,
    recovery_evidence JSONB NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_golden_recovery_revisions_identity_key UNIQUE (
        id,
        attempt_id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_golden_recovery_revisions_number_key
        UNIQUE (attempt_id, revision_number),
    CONSTRAINT arena_golden_recovery_revisions_previous_key
        UNIQUE (previous_revision_id),
    CONSTRAINT arena_golden_recovery_revisions_attempt_fk FOREIGN KEY (
        attempt_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_attempts (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_recovery_revisions_previous_fk FOREIGN KEY (
        previous_revision_id,
        attempt_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_golden_recovery_revisions (
        id,
        attempt_id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_golden_recovery_revisions_number_check CHECK (
        revision_number >= 1
        AND (
            (revision_number = 1 AND previous_revision_id IS NULL)
            OR (revision_number > 1 AND previous_revision_id IS NOT NULL)
        )
    ),
    CONSTRAINT arena_golden_recovery_revisions_state_check CHECK (
        state IN ('stable', 'technical_pause', 'recovering', 'resumed')
    ),
    CONSTRAINT arena_golden_recovery_revisions_evidence_check CHECK (
        jsonb_typeof(recovery_evidence) = 'object'
        AND recovery_evidence <> '{}'::JSONB
    ),
    CONSTRAINT arena_golden_recovery_revisions_timestamps_check CHECK (
        recorded_at <= created_at
    )
);

CREATE INDEX arena_golden_attempts_roster_state_idx
    ON arena_golden_attempts (roster_id, state, attempt_number);

CREATE INDEX arena_golden_memberships_participant_idx
    ON arena_golden_memberships (roster_id, participant_id, attempt_id);

CREATE INDEX arena_golden_submissions_attempt_idx
    ON arena_golden_provisional_submissions (attempt_id, server_sequence);

-- +goose StatementBegin
CREATE FUNCTION arena_golden_attempt_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_number INTEGER;
    ready_count INTEGER;
    unresolved_direct_count INTEGER;
    participant_count INTEGER;
    open_disconnect_count INTEGER;
    uncommitted_count INTEGER;
    recovery_state VARCHAR(24);
    recovery_recorded_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.previous_attempt_id IS NOT NULL THEN
            SELECT attempt_number INTO previous_number
            FROM arena_golden_attempts
            WHERE id = NEW.previous_attempt_id
            FOR KEY SHARE;

            IF previous_number <> NEW.attempt_number - 1 THEN
                RAISE EXCEPTION 'Golden attempt lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden attempts are retained stage history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
        OR NEW.previous_attempt_id IS DISTINCT FROM OLD.previous_attempt_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'Golden attempt identity and lineage are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (OLD.disclosed_at IS NOT NULL AND NEW.disclosed_at IS DISTINCT FROM OLD.disclosed_at)
        OR (OLD.ready_at IS NOT NULL AND NEW.ready_at IS DISTINCT FROM OLD.ready_at)
        OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at)
        OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
        OR (OLD.cancelled_at IS NOT NULL AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (
            OLD.cancellation_reason IS NOT NULL
            AND NEW.cancellation_reason IS DISTINCT FROM OLD.cancellation_reason
        )
        OR (
            OLD.supersession_reason IS NOT NULL
            AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason
        ) THEN
        RAISE EXCEPTION 'Golden attempt lifecycle evidence is immutable once recorded'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'prepared' AND NEW.state IN ('ready', 'cancelled', 'superseded'))
            OR (OLD.state = 'ready' AND NEW.state IN ('active', 'cancelled', 'superseded'))
            OR (OLD.state = 'active' AND NEW.state IN ('technical_pause', 'completed', 'cancelled'))
            OR (OLD.state = 'technical_pause' AND NEW.state IN ('active', 'completed', 'cancelled'))
        ) THEN
        RAISE EXCEPTION 'invalid Golden attempt lifecycle transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'active' AND NEW.state = 'technical_pause' THEN
        SELECT state, recorded_at
        INTO recovery_state, recovery_recorded_at
        FROM arena_golden_recovery_revisions
        WHERE attempt_id = NEW.id
        ORDER BY revision_number DESC
        LIMIT 1;

        IF recovery_state IS DISTINCT FROM 'technical_pause'
            OR recovery_recorded_at IS NULL
            OR recovery_recorded_at > NEW.paused_at THEN
            RAISE EXCEPTION 'Golden technical pause requires current recovery evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.state = 'technical_pause' AND NEW.state = 'active' THEN
        SELECT state, recorded_at
        INTO recovery_state, recovery_recorded_at
        FROM arena_golden_recovery_revisions
        WHERE attempt_id = NEW.id
        ORDER BY revision_number DESC
        LIMIT 1;

        IF recovery_state IS DISTINCT FROM 'resumed'
            OR recovery_recorded_at IS NULL
            OR recovery_recorded_at < OLD.paused_at THEN
            RAISE EXCEPTION 'Golden resume requires current recovery evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'ready' AND OLD.state <> 'ready' THEN
        SELECT
            COUNT(*) FILTER (WHERE ready_at IS NOT NULL),
            COUNT(*) FILTER (
                WHERE selection_kind = 'direct'
                    AND ready_at IS NULL
                    AND no_show_at IS NULL
            )
        INTO ready_count, unresolved_direct_count
        FROM arena_golden_memberships
        WHERE attempt_id = NEW.id;

        IF ready_count < 2 OR unresolved_direct_count <> 0 THEN
            RAISE EXCEPTION 'Golden readiness requires two ready members and resolved direct selections'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'active' AND OLD.state <> 'active' THEN
        SELECT COUNT(*) INTO participant_count
        FROM arena_golden_memberships
        WHERE attempt_id = NEW.id
            AND participation_established_at IS NOT NULL;

        SELECT COUNT(*) INTO open_disconnect_count
        FROM arena_golden_ready_disconnects
        WHERE attempt_id = NEW.id AND state = 'open';

        IF participant_count < 2 OR open_disconnect_count <> 0 THEN
            RAISE EXCEPTION 'Golden start requires established participants without open disconnects'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.state = 'completed' AND OLD.state <> 'completed' THEN
        SELECT
            COUNT(*) FILTER (WHERE participation_established_at IS NOT NULL),
            COUNT(*) FILTER (
                WHERE participation_established_at IS NOT NULL
                    AND NOT EXISTS (
                        SELECT 1
                        FROM arena_golden_position_commits AS position_commit
                        WHERE position_commit.membership_id = membership.id
                    )
            )
        INTO participant_count, uncommitted_count
        FROM arena_golden_memberships AS membership
        WHERE attempt_id = NEW.id;

        IF participant_count < 2 OR uncommitted_count <> 0 THEN
            RAISE EXCEPTION 'Golden completion requires every participant position commit'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_attempt_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_attempts
FOR EACH ROW EXECUTE FUNCTION arena_golden_attempt_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_golden_membership_guard() RETURNS TRIGGER AS $$
DECLARE
    attempt_state VARCHAR(24);
    promotion_count INTEGER;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden membership evidence is retained'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state INTO attempt_state
    FROM arena_golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF TG_OP = 'INSERT' THEN
        IF attempt_state <> 'prepared' THEN
            RAISE EXCEPTION 'Golden membership selection is closed after preparation'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    IF attempt_state NOT IN ('prepared', 'ready') THEN
        RAISE EXCEPTION 'Golden membership evidence is closed after start'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
        OR NEW.selection_kind IS DISTINCT FROM OLD.selection_kind
        OR NEW.reserve_position IS DISTINCT FROM OLD.reserve_position
        OR NEW.selected_at IS DISTINCT FROM OLD.selected_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.excluded_at IS DISTINCT FROM OLD.excluded_at
        OR NEW.exclusion_reason IS DISTINCT FROM OLD.exclusion_reason THEN
        RAISE EXCEPTION 'Golden selection and exclusion evidence are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (OLD.ready_at IS NOT NULL AND NEW.ready_at IS DISTINCT FROM OLD.ready_at)
        OR (OLD.no_show_at IS NOT NULL AND NEW.no_show_at IS DISTINCT FROM OLD.no_show_at)
        OR (
            OLD.participation_established_at IS NOT NULL
            AND NEW.participation_established_at IS DISTINCT FROM OLD.participation_established_at
        ) THEN
        RAISE EXCEPTION 'Golden participation evidence is irreversible'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.selection_kind = 'reserve'
        AND OLD.participation_established_at IS NULL
        AND NEW.participation_established_at IS NOT NULL THEN
        SELECT COUNT(*) INTO promotion_count
        FROM arena_golden_reserve_promotions
        WHERE reserve_membership_id = NEW.id;

        IF promotion_count <> 1 THEN
            RAISE EXCEPTION 'Golden reserve participation requires promotion evidence'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_membership_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_memberships
FOR EACH ROW EXECUTE FUNCTION arena_golden_membership_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_golden_disconnect_guard() RETURNS TRIGGER AS $$
DECLARE
    membership_ready_at TIMESTAMPTZ;
    membership_no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Golden ready disconnect evidence is retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.id IS DISTINCT FROM OLD.id
            OR NEW.membership_id IS DISTINCT FROM OLD.membership_id
            OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
            OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
            OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
            OR NEW.participant_id IS DISTINCT FROM OLD.participant_id
            OR NEW.sequence_number IS DISTINCT FROM OLD.sequence_number
            OR NEW.disconnected_at IS DISTINCT FROM OLD.disconnected_at
            OR NEW.created_at IS DISTINCT FROM OLD.created_at
            OR OLD.state <> 'open'
            OR NEW.state NOT IN ('reconnected', 'expired') THEN
            RAISE EXCEPTION 'Golden ready disconnect identity and terminal evidence are immutable'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    SELECT ready_at, no_show_at
    INTO membership_ready_at, membership_no_show_at
    FROM arena_golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM arena_golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF membership_ready_at IS NULL
        OR membership_no_show_at IS NOT NULL
        OR NEW.disconnected_at < membership_ready_at
        OR attempt_state NOT IN ('ready', 'active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden disconnect requires retained ready membership'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_disconnect_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_ready_disconnects
FOR EACH ROW EXECUTE FUNCTION arena_golden_disconnect_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_golden_submission_guard() RETURNS TRIGGER AS $$
DECLARE
    participation_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden provisional submissions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT participation_established_at INTO participation_at
    FROM arena_golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM arena_golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF participation_at IS NULL
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden provisional submission requires an active participant'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_submission_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_provisional_submissions
FOR EACH ROW EXECUTE FUNCTION arena_golden_submission_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_golden_position_commit_guard() RETURNS TRIGGER AS $$
DECLARE
    submission_status VARCHAR(16);
    submission_position SMALLINT;
    participation_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
    current_attempt_number INTEGER;
    previous_attempt_number INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position commits are immutable terminal evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT status, provisional_position
    INTO submission_status, submission_position
    FROM arena_golden_provisional_submissions
    WHERE id = NEW.provisional_submission_id
    FOR KEY SHARE;

    SELECT participation_established_at INTO participation_at
    FROM arena_golden_memberships
    WHERE id = NEW.membership_id
    FOR NO KEY UPDATE;

    SELECT state, attempt_number
    INTO attempt_state, current_attempt_number
    FROM arena_golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF submission_status <> 'accepted'
        OR submission_position <> NEW.position
        OR participation_at IS NULL
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden position commit must seal an accepted active submission'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_position_commit_id IS NOT NULL THEN
        SELECT attempt.attempt_number INTO previous_attempt_number
        FROM arena_golden_position_commits AS position_commit
        INNER JOIN arena_golden_attempts AS attempt
            ON attempt.id = position_commit.attempt_id
        WHERE position_commit.id = NEW.previous_position_commit_id
        FOR KEY SHARE OF position_commit, attempt;

        IF previous_attempt_number >= current_attempt_number THEN
            RAISE EXCEPTION 'Golden prior position must belong to an earlier attempt'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_position_commit_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_position_commits
FOR EACH ROW EXECUTE FUNCTION arena_golden_position_commit_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_golden_reserve_promotion_guard() RETURNS TRIGGER AS $$
DECLARE
    reserve_kind VARCHAR(16);
    replaced_kind VARCHAR(16);
    replaced_no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden reserve promotions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT selection_kind INTO reserve_kind
    FROM arena_golden_memberships
    WHERE id = NEW.reserve_membership_id
    FOR NO KEY UPDATE;

    SELECT selection_kind, no_show_at
    INTO replaced_kind, replaced_no_show_at
    FROM arena_golden_memberships
    WHERE id = NEW.replaced_membership_id
    FOR NO KEY UPDATE;

    SELECT state INTO attempt_state
    FROM arena_golden_attempts
    WHERE id = NEW.attempt_id
    FOR NO KEY UPDATE;

    IF reserve_kind <> 'reserve'
        OR replaced_kind <> 'direct'
        OR replaced_no_show_at IS NULL
        OR attempt_state NOT IN ('prepared', 'ready') THEN
        RAISE EXCEPTION 'Golden reserve promotion requires a direct no-show replacement'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_reserve_promotion_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_reserve_promotions
FOR EACH ROW EXECUTE FUNCTION arena_golden_reserve_promotion_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_golden_recovery_revision_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    previous_state VARCHAR(24);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden recovery revisions are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NULL THEN
        IF NEW.state NOT IN ('stable', 'technical_pause') THEN
            RAISE EXCEPTION 'initial Golden recovery state must be stable or paused'
                USING ERRCODE = 'check_violation';
        END IF;

        RETURN NEW;
    END IF;

    SELECT revision_number, state
    INTO previous_number, previous_state
    FROM arena_golden_recovery_revisions
    WHERE id = NEW.previous_revision_id
    FOR KEY SHARE;

    IF previous_number <> NEW.revision_number - 1
        OR NOT (
            (previous_state = 'stable' AND NEW.state = 'technical_pause')
            OR (previous_state = 'technical_pause' AND NEW.state IN ('recovering', 'resumed'))
            OR (previous_state = 'recovering' AND NEW.state = 'resumed')
            OR (previous_state = 'resumed' AND NEW.state = 'technical_pause')
        ) THEN
        RAISE EXCEPTION 'invalid Golden recovery revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_golden_recovery_revision_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_golden_recovery_revisions
FOR EACH ROW EXECUTE FUNCTION arena_golden_recovery_revision_guard();

-- +goose Down

DROP TABLE IF EXISTS arena_golden_recovery_revisions;
DROP TABLE IF EXISTS arena_golden_reserve_promotions;
DROP TABLE IF EXISTS arena_golden_position_commits;
DROP TABLE IF EXISTS arena_golden_provisional_submissions;
DROP TABLE IF EXISTS arena_golden_ready_disconnects;
DROP TABLE IF EXISTS arena_golden_memberships;
DROP TABLE IF EXISTS arena_golden_attempts;

DROP FUNCTION IF EXISTS arena_golden_recovery_revision_guard();
DROP FUNCTION IF EXISTS arena_golden_reserve_promotion_guard();
DROP FUNCTION IF EXISTS arena_golden_position_commit_guard();
DROP FUNCTION IF EXISTS arena_golden_submission_guard();
DROP FUNCTION IF EXISTS arena_golden_disconnect_guard();
DROP FUNCTION IF EXISTS arena_golden_membership_guard();
DROP FUNCTION IF EXISTS arena_golden_attempt_guard();
