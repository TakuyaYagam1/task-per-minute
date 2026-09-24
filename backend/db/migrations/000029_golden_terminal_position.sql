-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- A deadline placement is not an accepted submission or a no-show. Readers
-- must understand both provenances before terminal evidence can be written.
CREATE TABLE public.golden_terminal_position_evidence (
    id uuid PRIMARY KEY,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    runtime_revision bigint NOT NULL,
    deadline timestamp with time zone NOT NULL,
    position smallint NOT NULL,
    payload_digest bytea NOT NULL,
    recorded_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_terminal_position_evidence_group_key UNIQUE (group_revision_id),
    CONSTRAINT golden_terminal_position_evidence_attempt_key UNIQUE (attempt_id),
    CONSTRAINT golden_terminal_position_evidence_identity_key UNIQUE (
        id, attempt_id, membership_id, participant_id, tournament_id, roster_id, position
    ),
    CONSTRAINT golden_terminal_position_evidence_runtime_fk FOREIGN KEY (
        attempt_id, tournament_id, roster_id, group_revision_id
    ) REFERENCES public.golden_runtime_assignments (
        attempt_id, tournament_id, roster_id, group_revision_id
    ) ON DELETE RESTRICT,
    CONSTRAINT golden_terminal_position_evidence_membership_fk FOREIGN KEY (
        membership_id, attempt_id, participant_id, tournament_id, roster_id
    ) REFERENCES public.golden_memberships (
        id, attempt_id, participant_id, tournament_id, roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT golden_terminal_position_evidence_values_check CHECK (
        id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND runtime_revision >= 1
        AND position BETWEEN 1 AND 16
        AND octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
        AND recorded_at >= deadline
        AND created_at = recorded_at
    )
);

ALTER TABLE public.golden_position_commits
    ADD COLUMN terminal_evidence_id uuid,
    ALTER COLUMN provisional_submission_id DROP NOT NULL,
    ADD CONSTRAINT golden_position_commits_provenance_check CHECK (
        (provisional_submission_id IS NOT NULL) <> (terminal_evidence_id IS NOT NULL)
    ),
    ADD CONSTRAINT golden_position_commits_terminal_key UNIQUE (terminal_evidence_id),
    ADD CONSTRAINT golden_position_commits_terminal_fk FOREIGN KEY (
        terminal_evidence_id, attempt_id, membership_id, participant_id, tournament_id, roster_id, position
    ) REFERENCES public.golden_terminal_position_evidence (
        id, attempt_id, membership_id, participant_id, tournament_id, roster_id, position
    ) ON DELETE RESTRICT;

CREATE FUNCTION public.golden_terminal_position_evidence_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_revision BIGINT;
    attempt_state TEXT;
    member_row golden_memberships%ROWTYPE;
    runtime_row golden_runtime_assignments%ROWTYPE;
    unresolved_count INTEGER;
    unresolved_participant UUID;
    position_from SMALLINT;
    position_to SMALLINT;
    committed_count INTEGER;
    distinct_positions INTEGER;
    first_position SMALLINT;
    last_position SMALLINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden terminal position evidence is immutable'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    -- Match the production command/recovery lock order. The head serializes
    -- group-wide decisions, including submissions and successor creation.
    SELECT head.revision INTO current_revision
    FROM golden_runtime_heads AS head
    WHERE head.tournament_id = NEW.tournament_id AND head.roster_id = NEW.roster_id
    FOR UPDATE;
    IF NOT FOUND OR current_revision IS DISTINCT FROM NEW.runtime_revision THEN
        RAISE EXCEPTION 'Golden terminal position requires the current runtime head'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    SELECT attempt.state INTO attempt_state
    FROM golden_attempts AS attempt
    WHERE attempt.id = NEW.attempt_id
        AND attempt.tournament_id = NEW.tournament_id AND attempt.roster_id = NEW.roster_id
    FOR UPDATE;
    IF NOT FOUND OR attempt_state IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'Golden terminal position requires an active attempt'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    PERFORM 1 FROM golden_memberships AS membership
    WHERE membership.attempt_id = NEW.attempt_id
        AND membership.tournament_id = NEW.tournament_id AND membership.roster_id = NEW.roster_id
    ORDER BY membership.id
    FOR UPDATE;
    SELECT * INTO member_row FROM golden_memberships
    WHERE id = NEW.membership_id AND attempt_id = NEW.attempt_id
        AND tournament_id = NEW.tournament_id AND roster_id = NEW.roster_id
        AND participant_id = NEW.participant_id;
    IF NOT FOUND OR member_row.participation_established_at IS NULL
        OR member_row.no_show_at IS NOT NULL OR member_row.excluded_at IS NOT NULL
        OR member_row.selection_kind = 'excluded' THEN
        RAISE EXCEPTION 'Golden terminal position requires exact participating membership'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    SELECT * INTO runtime_row FROM golden_runtime_assignments
    WHERE attempt_id = NEW.attempt_id AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id AND group_revision_id = NEW.group_revision_id
    FOR UPDATE;
    IF NOT FOUND OR runtime_row.settlement_revision_id IS NOT NULL
        OR runtime_row.finalized_at IS NOT NULL OR runtime_row.started_at IS NULL
        OR runtime_row.deadline IS NULL OR NEW.deadline IS DISTINCT FROM runtime_row.deadline
        OR runtime_row.deadline IS DISTINCT FROM runtime_row.started_at + interval '180 seconds'
        OR NEW.recorded_at < runtime_row.deadline
        OR EXISTS (
            SELECT 1 FROM golden_runtime_assignments AS successor
            WHERE successor.tournament_id = NEW.tournament_id AND successor.roster_id = NEW.roster_id
                AND successor.group_revision_id = NEW.group_revision_id
                AND successor.edge_position > runtime_row.edge_position
        ) THEN
        RAISE EXCEPTION 'Golden terminal position requires the exact elapsed unfinalized deadline'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM golden_provisional_submissions AS submission
        JOIN golden_memberships AS membership ON membership.id = submission.membership_id
        JOIN golden_position_commits AS committed ON committed.provisional_submission_id = submission.id
        WHERE submission.attempt_id = NEW.attempt_id
            AND submission.tournament_id = NEW.tournament_id AND submission.roster_id = NEW.roster_id
            AND submission.status = 'accepted'
            AND membership.participation_established_at IS NOT NULL
            AND membership.no_show_at IS NULL AND membership.excluded_at IS NULL
            AND membership.selection_kind <> 'excluded'
            AND submission.received_at >= runtime_row.started_at
            AND submission.received_at < runtime_row.deadline
    ) THEN
        RAISE EXCEPTION 'Golden terminal position requires a genuine accepted solve in this attempt'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    SELECT COUNT(*), (array_agg(source.participant_id ORDER BY source.position))[1]
    INTO unresolved_count, unresolved_participant
    FROM golden_exact_plan_snapshot_members AS source
    JOIN golden_exact_plan_snapshot_seals AS seal ON seal.plan_id = source.plan_id
    WHERE source.plan_id = runtime_row.plan_id
        AND source.tournament_id = NEW.tournament_id AND source.roster_id = NEW.roster_id
        AND source.group_revision_id = NEW.group_revision_id
        AND NOT EXISTS (
            SELECT 1 FROM golden_attempt_stage_groups AS attempt_group
            JOIN golden_position_commits AS committed ON committed.attempt_id = attempt_group.attempt_id
            WHERE attempt_group.tournament_id = NEW.tournament_id AND attempt_group.roster_id = NEW.roster_id
                AND attempt_group.group_revision_id = NEW.group_revision_id
                AND committed.participant_id = source.participant_id
        )
        AND NOT EXISTS (
            SELECT 1 FROM golden_attempt_stage_groups AS attempt_group
            JOIN golden_memberships AS membership ON membership.attempt_id = attempt_group.attempt_id
            WHERE attempt_group.tournament_id = NEW.tournament_id AND attempt_group.roster_id = NEW.roster_id
                AND attempt_group.group_revision_id = NEW.group_revision_id
                AND membership.participant_id = source.participant_id AND membership.no_show_at IS NOT NULL
        );
    IF unresolved_count <> 1 OR unresolved_participant IS DISTINCT FROM NEW.participant_id THEN
        RAISE EXCEPTION 'Golden terminal position requires the sole unresolved group participant'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    SELECT group_revision.position_from, group_revision.position_to
    INTO position_from, position_to
    FROM golden_group_revisions AS group_revision
    JOIN golden_attempt_stage_groups AS attempt_group
        ON attempt_group.group_revision_id = group_revision.revision_id
        AND attempt_group.tournament_id = group_revision.tournament_id
        AND attempt_group.roster_id = group_revision.roster_id
    WHERE attempt_group.attempt_id = NEW.attempt_id
        AND group_revision.revision_id = NEW.group_revision_id
        AND group_revision.tournament_id = NEW.tournament_id AND group_revision.roster_id = NEW.roster_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Golden terminal position requires exact stage group authority'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;

    SELECT COUNT(*), COUNT(DISTINCT committed.position), MIN(committed.position), MAX(committed.position)
    INTO committed_count, distinct_positions, first_position, last_position
    FROM golden_attempt_stage_groups AS attempt_group
    JOIN golden_position_commits AS committed ON committed.attempt_id = attempt_group.attempt_id
    WHERE attempt_group.tournament_id = NEW.tournament_id AND attempt_group.roster_id = NEW.roster_id
        AND attempt_group.group_revision_id = NEW.group_revision_id;
    IF committed_count = 0 OR distinct_positions <> committed_count
        OR first_position IS DISTINCT FROM position_from
        OR last_position IS DISTINCT FROM position_from + committed_count - 1
        OR NEW.position IS DISTINCT FROM position_from + committed_count OR NEW.position > position_to THEN
        RAISE EXCEPTION 'Golden terminal position must follow the contiguous committed group prefix'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_evidence_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER golden_terminal_position_evidence_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.golden_terminal_position_evidence
FOR EACH ROW EXECUTE FUNCTION public.golden_terminal_position_evidence_guard();

CREATE OR REPLACE FUNCTION public.golden_position_commit_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    submission_status VARCHAR(16);
    submission_position SMALLINT;
    participation_at TIMESTAMPTZ;
    no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
    current_attempt_number INTEGER;
    previous_attempt_number INTEGER;
    evidence_row golden_terminal_position_evidence%ROWTYPE;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position commits are immutable terminal evidence'
            USING ERRCODE = 'check_violation';
    END IF;
    IF (NEW.provisional_submission_id IS NULL) = (NEW.terminal_evidence_id IS NULL) THEN
        RAISE EXCEPTION 'Golden position commit requires exactly one provenance'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_position_commits_provenance_check';
    END IF;

    IF NEW.terminal_evidence_id IS NOT NULL THEN
        SELECT * INTO evidence_row FROM golden_terminal_position_evidence
        WHERE id = NEW.terminal_evidence_id AND attempt_id = NEW.attempt_id
            AND membership_id = NEW.membership_id AND participant_id = NEW.participant_id
            AND tournament_id = NEW.tournament_id AND roster_id = NEW.roster_id AND position = NEW.position;
        IF NOT FOUND OR NEW.committed_at IS DISTINCT FROM evidence_row.recorded_at
            OR NEW.created_at IS DISTINCT FROM evidence_row.created_at OR NEW.previous_position_commit_id IS NOT NULL THEN
            RAISE EXCEPTION 'Golden terminal commit must match exact evidence identity and timestamps'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_position_commits_terminal_fk';
        END IF;
        PERFORM 1 FROM golden_runtime_heads
        WHERE tournament_id = NEW.tournament_id AND roster_id = NEW.roster_id
            AND revision = evidence_row.runtime_revision
        FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'Golden terminal commit requires its captured runtime head'
                USING ERRCODE = 'check_violation';
        END IF;
        PERFORM 1 FROM golden_attempts
        WHERE id = NEW.attempt_id AND tournament_id = NEW.tournament_id AND roster_id = NEW.roster_id AND state = 'active'
        FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'Golden terminal commit requires an active attempt'
                USING ERRCODE = 'check_violation';
        END IF;
        PERFORM 1 FROM golden_memberships AS membership
        WHERE membership.id = NEW.membership_id AND membership.participation_established_at IS NOT NULL
            AND membership.no_show_at IS NULL AND membership.excluded_at IS NULL AND membership.selection_kind <> 'excluded'
        FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'Golden terminal commit requires participating membership'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    SELECT status, provisional_position INTO submission_status, submission_position
    FROM golden_provisional_submissions WHERE id = NEW.provisional_submission_id FOR KEY SHARE;
    SELECT membership.participation_established_at, membership.no_show_at INTO participation_at, no_show_at
    FROM golden_memberships AS membership WHERE membership.id = NEW.membership_id FOR NO KEY UPDATE;
    SELECT state, attempt_number INTO attempt_state, current_attempt_number
    FROM golden_attempts WHERE id = NEW.attempt_id FOR NO KEY UPDATE;
    IF submission_status IS DISTINCT FROM 'accepted' OR submission_position IS DISTINCT FROM NEW.position
        OR (participation_at IS NULL AND no_show_at IS NULL)
        OR attempt_state IS NULL OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden position commit must seal an accepted active submission'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.previous_position_commit_id IS NOT NULL THEN
        SELECT attempt.attempt_number INTO previous_attempt_number
        FROM golden_position_commits AS position_commit
        INNER JOIN golden_attempts AS attempt ON attempt.id = position_commit.attempt_id
        WHERE position_commit.id = NEW.previous_position_commit_id
        FOR KEY SHARE OF position_commit, attempt;
        IF previous_attempt_number >= current_attempt_number THEN
            RAISE EXCEPTION 'Golden prior position must belong to an earlier attempt'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

-- Existing solver bindings retain their contract. A terminal binding must
-- carry the independent evidence digest, never a solver's payload digest.
CREATE FUNCTION public.golden_terminal_position_binding_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    evidence_row golden_terminal_position_evidence%ROWTYPE;
BEGIN
    SELECT evidence.* INTO evidence_row
    FROM golden_position_commits AS committed
    JOIN golden_terminal_position_evidence AS evidence ON evidence.id = committed.terminal_evidence_id
    WHERE committed.id = NEW.position_commit_id;
    IF FOUND AND (
        NEW.evidence_digest IS DISTINCT FROM evidence_row.payload_digest
        OR NEW.position IS DISTINCT FROM evidence_row.position
        OR NEW.attempt_id IS DISTINCT FROM evidence_row.attempt_id
        OR NEW.participant_id IS DISTINCT FROM evidence_row.participant_id
        OR NEW.tournament_id IS DISTINCT FROM evidence_row.tournament_id
        OR NEW.roster_id IS DISTINCT FROM evidence_row.roster_id
    ) THEN
        RAISE EXCEPTION 'Golden terminal ledger binding must match exact evidence'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_binding_guard';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER golden_terminal_position_binding_guard
BEFORE INSERT ON public.golden_position_ledger_commit_bindings
FOR EACH ROW EXECUTE FUNCTION public.golden_terminal_position_binding_guard();

-- A captured decision must not consume the group's unique terminal slot
-- without its position. Check at commit so the normal head advance and
-- finalization may follow the evidence and position inserts in this transaction.
CREATE FUNCTION public.validate_golden_terminal_position_commit() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM golden_position_commits AS committed
        WHERE committed.terminal_evidence_id = NEW.id
            AND committed.attempt_id = NEW.attempt_id AND committed.membership_id = NEW.membership_id
            AND committed.participant_id = NEW.participant_id AND committed.tournament_id = NEW.tournament_id
            AND committed.roster_id = NEW.roster_id AND committed.position = NEW.position
            AND committed.provisional_submission_id IS NULL
            AND committed.committed_at = NEW.recorded_at AND committed.created_at = NEW.created_at
    ) THEN
        RAISE EXCEPTION 'Golden terminal evidence requires its exact position commit in the same transaction'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'golden_terminal_position_commit_required';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER golden_terminal_position_commit_required
AFTER INSERT ON public.golden_terminal_position_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_golden_terminal_position_commit();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE public.golden_terminal_position_evidence IN ACCESS EXCLUSIVE MODE;
LOCK TABLE public.golden_position_commits IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.golden_terminal_position_evidence) THEN
        RAISE EXCEPTION 'migration 000029 cannot roll back terminal position evidence; keep version 29 and use a forward migration';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION public.golden_position_commit_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    submission_status VARCHAR(16);
    submission_position SMALLINT;
    participation_at TIMESTAMPTZ;
    no_show_at TIMESTAMPTZ;
    attempt_state VARCHAR(24);
    current_attempt_number INTEGER;
    previous_attempt_number INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Golden position commits are immutable terminal evidence'
            USING ERRCODE = 'check_violation';
    END IF;
    SELECT status, provisional_position INTO submission_status, submission_position
    FROM golden_provisional_submissions WHERE id = NEW.provisional_submission_id FOR KEY SHARE;
    SELECT membership.participation_established_at, membership.no_show_at INTO participation_at, no_show_at
    FROM golden_memberships AS membership WHERE membership.id = NEW.membership_id FOR NO KEY UPDATE;
    SELECT state, attempt_number INTO attempt_state, current_attempt_number
    FROM golden_attempts WHERE id = NEW.attempt_id FOR NO KEY UPDATE;
    IF submission_status <> 'accepted' OR submission_position <> NEW.position
        OR (participation_at IS NULL AND no_show_at IS NULL)
        OR attempt_state NOT IN ('active', 'technical_pause') THEN
        RAISE EXCEPTION 'Golden position commit must seal an accepted active submission'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.previous_position_commit_id IS NOT NULL THEN
        SELECT attempt.attempt_number INTO previous_attempt_number
        FROM golden_position_commits AS position_commit
        INNER JOIN golden_attempts AS attempt ON attempt.id = position_commit.attempt_id
        WHERE position_commit.id = NEW.previous_position_commit_id
        FOR KEY SHARE OF position_commit, attempt;
        IF previous_attempt_number >= current_attempt_number THEN
            RAISE EXCEPTION 'Golden prior position must belong to an earlier attempt'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE public.golden_position_commits
    ALTER COLUMN provisional_submission_id SET NOT NULL,
    DROP CONSTRAINT golden_position_commits_terminal_fk,
    DROP CONSTRAINT golden_position_commits_terminal_key,
    DROP CONSTRAINT golden_position_commits_provenance_check,
    DROP COLUMN terminal_evidence_id;
DROP TRIGGER golden_terminal_position_binding_guard ON public.golden_position_ledger_commit_bindings;
DROP FUNCTION public.golden_terminal_position_binding_guard();
DROP TRIGGER golden_terminal_position_commit_required ON public.golden_terminal_position_evidence;
DROP FUNCTION public.validate_golden_terminal_position_commit();
DROP TRIGGER golden_terminal_position_evidence_guard ON public.golden_terminal_position_evidence;
DROP FUNCTION public.golden_terminal_position_evidence_guard();
DROP TABLE public.golden_terminal_position_evidence;
-- +goose StatementEnd
