-- +goose Up
-- +goose StatementBegin
CREATE TABLE public.golden_runtime_assignments (
    attempt_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    assignment_id uuid NOT NULL,
    snapshot_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    title character varying(255) NOT NULL,
    category character varying(50) NOT NULL,
    difficulty character varying(10) NOT NULL,
    time_limit_seconds integer NOT NULL DEFAULT 180,
    source_digest bytea NOT NULL,
    started_at timestamp with time zone,
    deadline timestamp with time zone,
    settlement_revision_id uuid,
    finalized_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT golden_runtime_assignments_pkey PRIMARY KEY (attempt_id),
    CONSTRAINT golden_runtime_assignments_scope_key UNIQUE (
        attempt_id, tournament_id, roster_id, group_revision_id
    ),
    CONSTRAINT golden_runtime_assignments_group_key UNIQUE (group_revision_id),
    CONSTRAINT golden_runtime_assignments_wave_key UNIQUE (wave_id),
    CONSTRAINT golden_runtime_assignments_assignment_key UNIQUE (assignment_id),
    CONSTRAINT golden_runtime_assignments_snapshot_key UNIQUE (snapshot_id),
    CONSTRAINT golden_runtime_assignments_settlement_key UNIQUE (settlement_revision_id),
    CONSTRAINT golden_runtime_assignments_task_check CHECK (
        task_version >= 1
        AND title = btrim(title)
        AND title <> ''
        AND time_limit_seconds = 180
        AND octet_length(source_digest) = 32
        AND source_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT golden_runtime_assignments_time_check CHECK (
        (started_at IS NULL AND deadline IS NULL)
        OR (
            started_at IS NOT NULL
            AND deadline = started_at + interval '180 seconds'
        )
    ),
    CONSTRAINT golden_runtime_assignments_settlement_check CHECK (
        (settlement_revision_id IS NULL AND finalized_at IS NULL)
        OR (
            settlement_revision_id IS NOT NULL
            AND settlement_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
            AND finalized_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finalized_at >= started_at
        )
    ),
    CONSTRAINT golden_runtime_assignments_attempt_fk FOREIGN KEY (
        attempt_id, tournament_id, roster_id
    ) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT golden_runtime_assignments_group_fk FOREIGN KEY (
        group_revision_id, tournament_id, roster_id
    ) REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT golden_runtime_assignments_task_fk FOREIGN KEY (
        task_id, task_version
    ) REFERENCES public.task_versions(task_id, version) ON DELETE RESTRICT
);

ALTER TABLE public.golden_attempt_submission_revisions
    DROP CONSTRAINT golden_attempt_submission_revisions_attempt_fk,
    ADD CONSTRAINT golden_attempt_submission_revisions_attempt_fk FOREIGN KEY (
        attempt_id, tournament_id, roster_id
    ) REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE public.golden_position_ledger_attempts
    DROP CONSTRAINT golden_position_ledger_attempts_authority_fk,
    ADD CONSTRAINT golden_position_ledger_attempts_authority_fk FOREIGN KEY (
        attempt_id, tournament_id, roster_id, group_revision_id
    ) REFERENCES public.golden_attempt_stage_groups(
        attempt_id, tournament_id, roster_id, group_revision_id
    ) ON DELETE RESTRICT;

CREATE INDEX golden_runtime_assignments_tournament_idx
    ON public.golden_runtime_assignments (tournament_id, roster_id, group_revision_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM golden_runtime_assignments) THEN
        RAISE EXCEPTION 'cannot remove Golden runtime while assignments exist';
    END IF;
END;
$$;

ALTER TABLE public.golden_position_ledger_attempts
    DROP CONSTRAINT golden_position_ledger_attempts_authority_fk,
    ADD CONSTRAINT golden_position_ledger_attempts_authority_fk FOREIGN KEY (
        attempt_id, tournament_id, roster_id, group_revision_id
    ) REFERENCES public.golden_attempt_authorities(
        attempt_id, tournament_id, roster_id, group_revision_id
    ) ON DELETE RESTRICT;

ALTER TABLE public.golden_attempt_submission_revisions
    DROP CONSTRAINT golden_attempt_submission_revisions_attempt_fk,
    ADD CONSTRAINT golden_attempt_submission_revisions_attempt_fk FOREIGN KEY (
        attempt_id, tournament_id, roster_id
    ) REFERENCES public.golden_attempt_authorities(
        attempt_id, tournament_id, roster_id
    ) ON DELETE RESTRICT;

DROP TABLE public.golden_runtime_assignments;
-- +goose StatementEnd
