-- +goose Up
-- +goose StatementBegin

-- Wave disclosure and Wave control are distinct facts. Keep pause/resume in a
-- dedicated source namespace so every command can publish one event even when
-- the Wave revision lineage already owns a disclosure event.
CREATE TABLE public.outbox_wave_control_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    command_id uuid NOT NULL,
    action character varying(24) NOT NULL,
    wave_revision_id uuid NOT NULL,
    wave_revision bigint NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT outbox_wave_control_sources_pkey PRIMARY KEY (outbox_event_id),
    CONSTRAINT outbox_wave_control_sources_command_key UNIQUE (command_id),
    CONSTRAINT outbox_wave_control_sources_action_check CHECK (
        action IN ('pause', 'resume')
    ),
    CONSTRAINT outbox_wave_control_sources_revision_check CHECK (
        wave_revision >= 1
        AND projection_revision >= 1
        AND projection_ordinal >= 1
    )
);

ALTER TABLE ONLY public.outbox_wave_control_sources
    ADD CONSTRAINT outbox_wave_control_sources_event_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_wave_control_sources
    ADD CONSTRAINT outbox_wave_control_sources_command_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.wave_control_commands(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.outbox_wave_control_sources
    ADD CONSTRAINT outbox_wave_control_sources_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_wave_control_sources
    ADD CONSTRAINT outbox_wave_control_sources_projection_fk
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

ALTER TABLE ONLY public.outbox_wave_control_sources
    ADD CONSTRAINT outbox_wave_control_sources_target_event_fk
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

CREATE INDEX outbox_wave_control_sources_scope_idx
    ON public.outbox_wave_control_sources (
        tournament_id, roster_id, wave_id, created_at DESC, command_id
    );

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
        + (SELECT COUNT(*) FROM outbox_wave_control_sources WHERE outbox_event_id = NEW.id)
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
        FROM outbox_wave_control_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NEW.terminal
                OR NEW.topic <> 'wave.control.changed'
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
                    FROM waves AS wave
                    WHERE wave.id = source.wave_id
                        AND wave.tournament_id = source.tournament_id
                        AND wave.roster_id = source.roster_id
                        AND wave.revision_id = source.wave_revision_id
                        AND wave.revision = source.wave_revision
                )
                OR NOT EXISTS (
                    SELECT 1
                    FROM wave_control_commands AS command
                    WHERE command.command_id = source.command_id
                        AND command.tournament_id = source.tournament_id
                        AND command.roster_id = source.roster_id
                        AND command.wave_id = source.wave_id
                        AND command.action = source.action
                        AND command.source_projection_revision_id = source.projection_revision_id
                        AND command.source_projection_revision = source.projection_revision
                        AND command.resulting_wave_revision = source.wave_revision
                        AND command.executed_at = source.created_at
                        AND command.created_at = source.created_at
                )
                OR NEW.payload <> jsonb_build_object(
                    'schema', 'wave-control-changed-v1',
                    'action', source.action,
                    'wave_id', source.wave_id
                )
            )
    ) THEN
        RAISE EXCEPTION 'Wave control outbox source is not bound to its exact command'
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
        + (SELECT COUNT(*) FROM outbox_wave_control_sources WHERE outbox_event_id = NEW.outbox_event_id)
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

CREATE TRIGGER outbox_wave_control_sources_append_only
BEFORE DELETE OR UPDATE ON public.outbox_wave_control_sources
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_wave_control_source_membership
AFTER INSERT ON public.outbox_wave_control_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.outbox_wave_control_sources
    ) THEN
        RAISE EXCEPTION 'cannot remove Wave control outbox source evidence'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS outbox_wave_control_source_membership
    ON public.outbox_wave_control_sources;
DROP TRIGGER IF EXISTS outbox_wave_control_sources_append_only
    ON public.outbox_wave_control_sources;

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

DROP TABLE public.outbox_wave_control_sources;

-- +goose StatementEnd
