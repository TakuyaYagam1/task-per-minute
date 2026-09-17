-- +goose Up
-- +goose StatementBegin

-- A live disconnect/reconnect mutation is durable graph evidence, not merely
-- an in-process notification.  Keep the source separate from the generic
-- event so the deferred source guard can prove the event was bound to the
-- immutable reconnect receipt and the exact game revision.
CREATE TABLE public.outbox_reconnect_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    series_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    game_revision bigint NOT NULL,
    command_id uuid NOT NULL,
    mutation_kind character varying(16) NOT NULL,
    expected_authority_revision bigint NOT NULL,
    result_authority_revision bigint NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT outbox_reconnect_sources_pkey PRIMARY KEY (outbox_event_id),
    CONSTRAINT outbox_reconnect_sources_command_key UNIQUE (command_id),
    CONSTRAINT outbox_reconnect_sources_uuid_check CHECK (
        outbox_event_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND wave_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND game_attempt_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND command_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT outbox_reconnect_sources_mutation_check CHECK (
        mutation_kind = btrim(mutation_kind)
        AND mutation_kind IN ('disconnect'::character varying, 'reconnect'::character varying)
    ),
    CONSTRAINT outbox_reconnect_sources_revision_check CHECK (
        game_revision >= 1
        AND expected_authority_revision >= 1
        AND result_authority_revision >= expected_authority_revision
        AND projection_revision >= 1
        AND projection_ordinal >= 1
    )
);

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_event_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_receipt_fk
    FOREIGN KEY (command_id)
    REFERENCES public.reconnect_command_receipts(command_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_series_fk
    FOREIGN KEY (series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_game_fk
    FOREIGN KEY (game_attempt_id)
    REFERENCES public.game_attempts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_projection_fk
    FOREIGN KEY (
        projection_revision_id,
        tournament_id,
        roster_id,
        projection_revision
    )
    REFERENCES public.projection_revisions(
        id,
        tournament_id,
        roster_id,
        revision_number
    ) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.outbox_reconnect_sources
    ADD CONSTRAINT outbox_reconnect_sources_target_event_fk
    FOREIGN KEY (
        outbox_event_id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    )
    REFERENCES public.outbox_events(
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    ) ON DELETE RESTRICT;

CREATE INDEX outbox_reconnect_sources_scope_idx
    ON public.outbox_reconnect_sources (
        tournament_id, roster_id, wave_id, created_at DESC, command_id
    );

-- Add the reconnect namespace to the existing deferred source-count guard.
-- The previous source checks remain intentionally unchanged: every event must
-- still have exactly one source and an exact published projection target.
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
        + (SELECT COUNT(*) FROM outbox_reconnect_sources WHERE outbox_event_id = NEW.id)
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

    IF EXISTS (
        SELECT 1
        FROM outbox_golden_runtime_sources AS source
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
        SELECT 1
        FROM outbox_reconnect_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NEW.terminal
                OR NEW.topic <> 'game.reconnect.changed'
                OR NEW.audience <> 'all'
                OR NEW.principal_id IS NOT NULL
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
                OR NOT EXISTS (
                    SELECT 1
                    FROM reconnect_command_receipts AS receipt
                    WHERE receipt.command_id = source.command_id
                        AND receipt.tournament_id = source.tournament_id
                        AND receipt.roster_id = source.roster_id
                        AND receipt.wave_id = source.wave_id
                        AND receipt.mutation_kind = source.mutation_kind
                        AND receipt.expected_authority_revision = source.expected_authority_revision
                        AND receipt.result_authority_revision = source.result_authority_revision
                        AND receipt.recorded_at = source.created_at
                )
                OR NOT EXISTS (
                    SELECT 1
                    FROM game_attempts AS game
                    WHERE game.id = source.game_attempt_id
                        AND game.series_id = source.series_id
                        AND game.roster_id = source.roster_id
                        AND game.revision = source.game_revision
                )
                OR NEW.payload <> jsonb_build_object(
                    'schema', 'game-reconnect-changed-v1',
                    'game_id', source.game_attempt_id,
                    'game_revision', source.game_revision,
                    'mutation_kind', source.mutation_kind
                )
            )
    ) THEN
        RAISE EXCEPTION 'reconnect outbox source is not an exact live game change binding'
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
        + (SELECT COUNT(*) FROM outbox_golden_runtime_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_reconnect_sources WHERE outbox_event_id = NEW.outbox_event_id)
    INTO source_count;
    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER outbox_reconnect_sources_append_only
BEFORE DELETE OR UPDATE ON public.outbox_reconnect_sources
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_reconnect_source_membership
AFTER INSERT ON public.outbox_reconnect_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.outbox_reconnect_sources) THEN
        RAISE EXCEPTION 'cannot remove reconnect outbox source evidence'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS outbox_reconnect_source_membership ON public.outbox_reconnect_sources;
DROP TRIGGER IF EXISTS outbox_reconnect_sources_append_only ON public.outbox_reconnect_sources;
DROP TABLE public.outbox_reconnect_sources;

-- Restore the pre-reconnect source namespace.  Down migrations are not used
-- in production, but keeping the guard definitions reversible avoids leaving
-- an invalid source-count contract in a disposable database.
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
                NOT NEW.terminal OR NEW.topic <> 'tournament.cancelled'
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
                NOT NEW.terminal OR NEW.topic <> 'tournament.champion.published'
                OR NEW.audience <> 'all' OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
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
        SELECT 1
        FROM outbox_golden_runtime_sources AS source
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

-- +goose StatementEnd
