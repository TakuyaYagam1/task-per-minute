-- +goose Up
-- +goose StatementBegin

-- The runtime head is the single optimistic-concurrency fence for one Golden
-- tournament. runtime_revision is the authoritative Golden projection fence;
-- it is deliberately separate from the generic published standings projection.
CREATE TABLE public.golden_runtime_heads (
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_runtime_heads_pkey PRIMARY KEY (tournament_id),
    CONSTRAINT golden_runtime_heads_revision_check CHECK (revision >= 1),
    CONSTRAINT golden_runtime_heads_source_check CHECK (source_projection_revision >= 1),
    CONSTRAINT golden_runtime_heads_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT golden_runtime_heads_projection_fk FOREIGN KEY (
        source_projection_revision_id, tournament_id, roster_id, source_projection_revision
    ) REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
        ON DELETE RESTRICT
);

-- Existing Golden assignments predate the runtime fence. Every runtime
-- tournament must have one roster and one exact source projection across all
-- of its materialized Golden groups. Refuse the migration rather than choose
-- an authority when legacy rows disagree.
DO $$
BEGIN
    IF EXISTS (
        WITH runtime_groups AS (
            SELECT DISTINCT runtime.tournament_id,
                runtime.roster_id,
                runtime.group_revision_id
            FROM public.golden_runtime_assignments AS runtime
        ),
        runtime_scope AS (
            SELECT runtime.tournament_id,
                runtime.roster_id AS runtime_roster_id,
                group_revision.roster_id AS group_roster_id
            FROM public.golden_runtime_assignments AS runtime
            INNER JOIN public.golden_group_revisions AS group_revision
                ON group_revision.revision_id = runtime.group_revision_id
                AND group_revision.tournament_id = runtime.tournament_id
        ),
        group_scope AS (
            SELECT runtime_group.tournament_id,
                group_revision.roster_id,
                group_revision.source_projection_revision_id,
                group_revision.source_projection_revision,
                projection.state AS projection_state
            FROM runtime_groups AS runtime_group
            INNER JOIN public.golden_group_revisions AS group_revision
                ON group_revision.revision_id = runtime_group.group_revision_id
                AND group_revision.tournament_id = runtime_group.tournament_id
                AND group_revision.roster_id = runtime_group.roster_id
            LEFT JOIN public.projection_revisions AS projection
                ON projection.id = group_revision.source_projection_revision_id
                AND projection.tournament_id = group_revision.tournament_id
                AND projection.roster_id = group_revision.roster_id
                AND projection.revision_number = group_revision.source_projection_revision
        ),
        inconsistent AS (
            SELECT tournament_id
            FROM runtime_scope
            GROUP BY tournament_id
            HAVING COUNT(*) FILTER (
                WHERE runtime_roster_id IS DISTINCT FROM group_roster_id
            ) > 0
                OR COUNT(DISTINCT runtime_roster_id) <> 1
                OR COUNT(DISTINCT group_roster_id) <> 1
            UNION
            SELECT tournament_id
            FROM group_scope
            GROUP BY tournament_id
            HAVING COUNT(DISTINCT roster_id) <> 1
                OR COUNT(DISTINCT source_projection_revision_id) <> 1
                OR COUNT(DISTINCT source_projection_revision) <> 1
                OR COUNT(*) FILTER (
                    WHERE projection_state IS NULL
                        OR projection_state NOT IN ('published', 'superseded')
                ) > 0
        )
        SELECT 1
        FROM inconsistent
    ) THEN
        RAISE EXCEPTION 'cannot backfill Golden runtime heads with inconsistent source authorities'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

-- Legacy runtime state has no command chain, but its first wire-visible Golden
-- projection fence is revision one so it is immediately safe to mutate.
INSERT INTO public.golden_runtime_heads (
    tournament_id,
    roster_id,
    revision,
    source_projection_revision_id,
    source_projection_revision,
    updated_at
)
SELECT runtime_group.tournament_id,
    (array_agg(group_scope.roster_id ORDER BY group_scope.roster_id))[1],
    1,
    (array_agg(group_scope.source_projection_revision_id ORDER BY group_scope.source_projection_revision_id))[1],
    MAX(group_scope.source_projection_revision),
    now()
FROM (
    SELECT DISTINCT runtime.tournament_id,
        runtime.roster_id,
        runtime.group_revision_id
    FROM public.golden_runtime_assignments AS runtime
) AS runtime_group
INNER JOIN public.golden_group_revisions AS group_scope
    ON group_scope.revision_id = runtime_group.group_revision_id
    AND group_scope.tournament_id = runtime_group.tournament_id
    AND group_scope.roster_id = runtime_group.roster_id
GROUP BY runtime_group.tournament_id;

CREATE TABLE public.golden_runtime_commands (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    actor_kind character varying(16) NOT NULL,
    actor_id uuid,
    command_scope character varying(32) NOT NULL,
    command_kind character varying(32) NOT NULL,
    attempt_id uuid,
    participant_id uuid,
    expected_runtime_revision bigint NOT NULL,
    expected_ready_window_id uuid,
    command_digest bytea NOT NULL,
    resulting_runtime_revision bigint NOT NULL,
    result_kind character varying(16) NOT NULL,
    result_payload jsonb NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT golden_runtime_commands_pkey PRIMARY KEY (command_id),
    CONSTRAINT golden_runtime_commands_identity_key UNIQUE (command_id, tournament_id),
    CONSTRAINT golden_runtime_commands_actor_check CHECK (
        (actor_kind = 'server' AND actor_id IS NULL AND command_scope = 'recovery')
        OR (actor_kind = 'operator' AND actor_id IS NOT NULL AND command_scope = 'operator')
        OR (actor_kind = 'participant' AND actor_id IS NOT NULL AND command_scope = 'participant')
    ),
    CONSTRAINT golden_runtime_commands_kind_check CHECK (
        command_scope IN ('operator', 'participant', 'recovery')
        AND command_kind IN (
            'open', 'start', 'ready', 'submit', 'connect',
            'ready_timeout', 'no_show', 'deadline_timeout', 'reserve_creation',
            'technical_pause', 'recovery_replay', 'recovery_resume',
            'completion', 'advancement'
        )
        AND (
            (command_scope = 'operator' AND command_kind IN ('open', 'start'))
            OR (command_scope = 'participant' AND command_kind IN ('ready', 'submit', 'connect'))
            OR (
                command_scope = 'recovery'
                AND actor_kind = 'server'
                AND actor_id IS NULL
                AND command_kind IN (
                    'ready_timeout', 'no_show', 'deadline_timeout', 'reserve_creation',
                    'technical_pause', 'recovery_replay', 'recovery_resume',
                    'completion', 'advancement'
                )
            )
        )
    ),
    CONSTRAINT golden_runtime_commands_revision_check CHECK (
        expected_runtime_revision >= 0
        AND resulting_runtime_revision = expected_runtime_revision + 1
    ),
    CONSTRAINT golden_runtime_commands_result_check CHECK (
        result_kind IN ('operator', 'participant', 'connection', 'recovery')
        AND (
            (command_scope = 'operator' AND result_kind = 'operator')
            OR (command_scope = 'participant' AND result_kind IN ('participant', 'connection'))
            OR (command_scope = 'recovery' AND result_kind = 'recovery')
        )
        AND jsonb_typeof(result_payload) = 'object'
        AND result_payload <> '{}'::jsonb
    ),
    CONSTRAINT golden_runtime_commands_digest_check CHECK (
        octet_length(command_digest) = 32
        AND command_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT golden_runtime_commands_identity_check CHECK (
        command_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND (attempt_id IS NULL OR attempt_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (participant_id IS NULL OR participant_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (expected_ready_window_id IS NULL OR expected_ready_window_id <> '00000000-0000-0000-0000-000000000000'::uuid)
    ),
    CONSTRAINT golden_runtime_commands_roster_fk FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT golden_runtime_commands_attempt_fk FOREIGN KEY (attempt_id, tournament_id, roster_id)
        REFERENCES public.golden_attempts(id, tournament_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT golden_runtime_commands_participant_fk FOREIGN KEY (participant_id)
        REFERENCES public.participants(id) ON DELETE RESTRICT
);

CREATE INDEX golden_runtime_commands_tournament_idx
    ON public.golden_runtime_commands (tournament_id, created_at, command_id);

CREATE TABLE public.outbox_golden_runtime_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    command_id uuid NOT NULL,
    runtime_revision bigint NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT outbox_golden_runtime_sources_pkey PRIMARY KEY (outbox_event_id),
    CONSTRAINT outbox_golden_runtime_sources_command_key UNIQUE (command_id),
    CONSTRAINT outbox_golden_runtime_sources_revision_key UNIQUE (tournament_id, runtime_revision),
    CONSTRAINT outbox_golden_runtime_sources_revision_check CHECK (
        runtime_revision >= 1
        AND projection_revision >= 1
        AND projection_ordinal >= 1
    ),
    CONSTRAINT outbox_golden_runtime_sources_event_fk FOREIGN KEY (outbox_event_id)
        REFERENCES public.outbox_events(id) ON DELETE RESTRICT,
    CONSTRAINT outbox_golden_runtime_sources_event_identity_fk FOREIGN KEY (
        outbox_event_id, tournament_id, roster_id, projection_revision_id,
        projection_revision, projection_ordinal
    ) REFERENCES public.outbox_events(
        id, tournament_id, roster_id, projection_revision_id,
        projection_revision, projection_ordinal
    ) ON DELETE RESTRICT,
    CONSTRAINT outbox_golden_runtime_sources_command_fk FOREIGN KEY (command_id)
        REFERENCES public.golden_runtime_commands(command_id) ON DELETE RESTRICT,
    CONSTRAINT outbox_golden_runtime_sources_projection_fk FOREIGN KEY (
        projection_revision_id, tournament_id, roster_id, projection_revision
    ) REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
        ON DELETE RESTRICT
);

CREATE INDEX outbox_golden_runtime_sources_tournament_idx
    ON public.outbox_golden_runtime_sources (tournament_id, runtime_revision, outbox_event_id);

-- Extend the existing audit ledger with a Golden source. Existing result and
-- cancellation rows retain their original source constraints and FKs. The
-- command identity is retained in the immutable payload and command table so
-- the existing result-facing sqlc AuditEvent shape remains compatible.
ALTER TABLE public.audit_events
    DROP CONSTRAINT audit_events_actor_check,
    DROP CONSTRAINT audit_events_source_check,
    ADD CONSTRAINT audit_events_actor_check CHECK (
        (actor_kind = 'server' AND actor_id IS NULL)
        OR (actor_kind IN ('operator', 'participant') AND actor_id IS NOT NULL)
    ),
    ADD CONSTRAINT audit_events_source_check CHECK (
        (
            action = 'tournament.cancelled'
            AND series_id IS NULL
            AND result_event_id IS NULL
        )
        OR (
            action LIKE 'golden.runtime.%'
            AND series_id IS NULL
            AND result_event_id IS NULL
        )
        OR (
            action <> 'tournament.cancelled'
            AND action NOT LIKE 'golden.runtime.%'
            AND series_id IS NOT NULL
            AND result_event_id IS NOT NULL
        )
    );

-- The original trigger requires exactly one source namespace. Include Golden
-- runtime in that count and validate its immutable event binding as well.
CREATE OR REPLACE FUNCTION public.validate_outbox_event_source() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_count INTEGER;
    target_projection_revision BIGINT;
    target_projection_state VARCHAR(16);
BEGIN
    SELECT
        (SELECT COUNT(*) FROM outbox_result_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_tournament_cancellation_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_wave_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_champion_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_stage_projection_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_golden_runtime_sources WHERE outbox_event_id = NEW.id)
    INTO source_count;

    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, state
    INTO target_projection_revision, target_projection_state
    FROM projection_revisions
    WHERE id = NEW.projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR SHARE;

    IF target_projection_revision IS DISTINCT FROM NEW.projection_revision
        OR target_projection_state NOT IN ('published', 'superseded') THEN
        RAISE EXCEPTION 'outbox event target is not an exact published projection'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1 FROM outbox_result_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
                OR NEW.terminal
            )
    ) THEN
        RAISE EXCEPTION 'result outbox source is not an exact initial event binding'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1 FROM outbox_tournament_cancellation_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NOT NEW.terminal OR NEW.topic <> 'tournament.cancelled' OR NEW.audience <> 'all'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'tournament cancellation outbox source is malformed'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1 FROM outbox_wave_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NEW.terminal OR NEW.topic <> 'wave.disclosed'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'wave outbox source is not bound to its published projection lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1 FROM outbox_champion_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NOT NEW.terminal OR NEW.topic <> 'tournament.champion.published' OR NEW.audience <> 'all'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'champion outbox source is not an exact terminal event binding'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1 FROM outbox_golden_runtime_sources AS source
        JOIN golden_runtime_commands AS command ON command.command_id = source.command_id
        WHERE source.outbox_event_id = NEW.id
            AND (
                NEW.terminal
                OR NEW.topic <> 'golden.runtime'
                OR NEW.audience <> 'all'
                OR NEW.principal_id IS NOT NULL
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
                OR source.command_id IS DISTINCT FROM NEW.idempotency_key
                OR command.occurred_at IS DISTINCT FROM NEW.created_at
                OR command.created_at IS DISTINCT FROM NEW.created_at
                OR command.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR command.roster_id IS DISTINCT FROM NEW.roster_id
                OR command.resulting_runtime_revision IS DISTINCT FROM source.runtime_revision
                OR NEW.payload IS DISTINCT FROM jsonb_build_object(
                    'schema', 'golden-runtime-event-v1',
                    'command_id', command.command_id,
                    'command_kind', command.command_kind,
                    'runtime_revision', command.resulting_runtime_revision,
                    'source', 'golden-runtime'
                )
            )
    ) THEN
        RAISE EXCEPTION 'Golden runtime outbox source is malformed'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1 FROM outbox_stage_projection_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND NOT NEW.terminal
    ) THEN
        NULL;
    END IF;

    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.validate_outbox_source_membership() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_count INTEGER;
BEGIN
    PERFORM 1 FROM outbox_events WHERE id = NEW.outbox_event_id FOR UPDATE;
    SELECT
        (SELECT COUNT(*) FROM outbox_result_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_tournament_cancellation_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_wave_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_champion_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_stage_projection_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_golden_runtime_sources WHERE outbox_event_id = NEW.outbox_event_id)
    INTO source_count;
    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER outbox_golden_runtime_sources_append_only
    BEFORE DELETE OR UPDATE ON public.outbox_golden_runtime_sources
    FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_golden_runtime_source_membership
    AFTER INSERT ON public.outbox_golden_runtime_sources
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

CREATE INDEX golden_runtime_heads_revision_idx
    ON public.golden_runtime_heads (tournament_id, revision);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS outbox_golden_runtime_source_membership
    ON public.outbox_golden_runtime_sources;
DROP TRIGGER IF EXISTS outbox_golden_runtime_sources_append_only
    ON public.outbox_golden_runtime_sources;

-- A down migration must not erase retained Golden runtime receipts, audit
-- rows, or outbox lineage. The source namespace remains part of the evidence
-- contract until every dependent record is gone.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.outbox_golden_runtime_sources)
        OR EXISTS (SELECT 1 FROM public.golden_runtime_commands)
        OR EXISTS (SELECT 1 FROM public.golden_runtime_heads)
        OR EXISTS (
            SELECT 1
            FROM public.audit_events
            WHERE action LIKE 'golden.runtime.%'
        ) THEN
        RAISE EXCEPTION 'cannot remove Golden runtime fencing while durable runtime evidence exists'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

-- Restore the canonical pre-000015 source validators before dropping the
-- Golden source table. Otherwise an executable rollback would leave trigger
-- functions referring to a relation that no longer exists.
CREATE OR REPLACE FUNCTION public.validate_outbox_event_source() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_count INTEGER;
    target_projection_revision BIGINT;
    target_projection_state VARCHAR(16);
BEGIN
    SELECT
        (SELECT COUNT(*) FROM outbox_result_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_tournament_cancellation_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_wave_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_champion_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_stage_projection_sources WHERE outbox_event_id = NEW.id)
    INTO source_count;

    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, state
    INTO target_projection_revision, target_projection_state
    FROM projection_revisions
    WHERE id = NEW.projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR SHARE;

    IF target_projection_revision IS DISTINCT FROM NEW.projection_revision
        OR target_projection_state NOT IN ('published', 'superseded') THEN
        RAISE EXCEPTION 'outbox event target is not an exact published projection'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_result_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
                OR NEW.terminal
            )
    ) THEN
        RAISE EXCEPTION 'result outbox source is not an exact initial event binding'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_tournament_cancellation_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NOT NEW.terminal
                OR NEW.topic <> 'tournament.cancelled'
                OR NEW.audience <> 'all'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'tournament cancellation outbox source is malformed'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_wave_sources AS source
        WHERE source.outbox_event_id = NEW.id
        AND (
                NEW.terminal
                OR NEW.topic <> 'wave.disclosed'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'wave outbox source is not bound to its published projection lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_champion_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NOT NEW.terminal
                OR NEW.topic <> 'tournament.champion.published'
                OR NEW.audience <> 'all'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'champion outbox source is not an exact terminal event binding'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION public.validate_outbox_source_membership() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_count INTEGER;
BEGIN
    PERFORM 1 FROM outbox_events WHERE id = NEW.outbox_event_id FOR UPDATE;
    SELECT
        (SELECT COUNT(*) FROM outbox_result_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_tournament_cancellation_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_wave_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_champion_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_stage_projection_sources WHERE outbox_event_id = NEW.outbox_event_id)
    INTO source_count;
    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

ALTER TABLE public.audit_events
    DROP CONSTRAINT IF EXISTS audit_events_actor_check,
    DROP CONSTRAINT IF EXISTS audit_events_source_check,
    ADD CONSTRAINT audit_events_actor_check CHECK (
        (actor_kind = 'server' AND actor_id IS NULL)
        OR (actor_kind = 'operator' AND actor_id IS NOT NULL)
    ),
    ADD CONSTRAINT audit_events_source_check CHECK (
        (action = 'tournament.cancelled' AND series_id IS NULL AND result_event_id IS NULL)
        OR (action <> 'tournament.cancelled' AND series_id IS NOT NULL AND result_event_id IS NOT NULL)
    );

DROP TABLE IF EXISTS public.outbox_golden_runtime_sources;
DROP TABLE IF EXISTS public.golden_runtime_commands;
DROP TABLE IF EXISTS public.golden_runtime_heads;

-- +goose StatementEnd
