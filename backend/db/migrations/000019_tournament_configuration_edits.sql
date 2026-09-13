-- +goose Up
-- +goose StatementBegin

-- POST-MVP-031 keeps published configuration and execution evidence immutable.
-- An edit creates a new configuration revision and records the exact source
-- authority, unlock intent, and successor lineage in append-only tables.

CREATE TABLE public.tournament_content_configuration_heads (
    tournament_id uuid NOT NULL,
    configuration_id uuid NOT NULL,
    configuration_revision bigint NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_content_configuration_heads_pkey PRIMARY KEY (tournament_id),
    CONSTRAINT tournament_content_configuration_heads_revision_check CHECK (
        configuration_revision >= 1 AND revision >= 1
    ),
    CONSTRAINT tournament_content_configuration_heads_timestamps_check CHECK (
        updated_at >= 'epoch'::timestamptz
    ),
    CONSTRAINT tournament_content_configuration_heads_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT tournament_content_configuration_heads_configuration_fk
        FOREIGN KEY (configuration_id, tournament_id)
        REFERENCES public.tournament_content_configurations(id, tournament_id)
        ON DELETE RESTRICT
);

INSERT INTO public.tournament_content_configuration_heads (
    tournament_id, configuration_id, configuration_revision, revision, updated_at
)
SELECT DISTINCT ON (configuration.tournament_id)
    configuration.tournament_id,
    configuration.id,
    configuration.revision,
    1,
    COALESCE(configuration.published_at, configuration.created_at)
FROM public.tournament_content_configurations AS configuration
WHERE configuration.state = 'published'
ORDER BY configuration.tournament_id, configuration.revision DESC, configuration.id DESC;

CREATE OR REPLACE FUNCTION public.tournament_content_configuration_head_publish()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.state = 'draft' AND NEW.state = 'published' THEN
        INSERT INTO public.tournament_content_configuration_heads (
            tournament_id, configuration_id, configuration_revision, revision, updated_at
        )
        VALUES (
            NEW.tournament_id, NEW.id, NEW.revision, 1,
            COALESCE(NEW.published_at, NEW.created_at)
        )
        ON CONFLICT (tournament_id) DO UPDATE
        SET configuration_id = EXCLUDED.configuration_id,
            configuration_revision = EXCLUDED.configuration_revision,
            revision = public.tournament_content_configuration_heads.revision + 1,
            updated_at = EXCLUDED.updated_at
        WHERE public.tournament_content_configuration_heads.configuration_revision < EXCLUDED.configuration_revision;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tournament_content_configuration_head_publish
AFTER UPDATE OF state ON public.tournament_content_configurations
FOR EACH ROW EXECUTE FUNCTION public.tournament_content_configuration_head_publish();

-- Older rows only retained a mode and pool identity. New configuration
-- revisions persist the effective category list as part of the stage default;
-- the empty compatibility value is accepted for pre-migration rows and is
-- filled by the read adapter from the immutable pool membership.
ALTER TABLE public.tournament_content_stage_defaults
    DROP CONSTRAINT tournament_content_stage_defaults_category_mode_check,
    ADD CONSTRAINT tournament_content_stage_defaults_category_mode_check CHECK (
        category_mode IN ('random', 'admin', 'draft')
    ),
    ADD COLUMN categories jsonb DEFAULT '[]'::jsonb NOT NULL,
    ADD CONSTRAINT tournament_content_stage_defaults_categories_check CHECK (
        jsonb_typeof(categories) = 'array'
        AND jsonb_array_length(categories) <= 5
    );

-- Configuration revisions retain every original publication invariant while
-- allowing the operator-selectable category modes introduced by this edit
-- boundary. Golden remains random and the final remains a full BO3 draft.
DROP TRIGGER tournament_content_configurations_guard
ON public.tournament_content_configurations;

CREATE FUNCTION public.tournament_content_configuration_edit_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    previous_revision bigint;
    locked_task record;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tournament content configurations are retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'INSERT' THEN
        IF NEW.state <> 'draft' OR NEW.published_at IS NOT NULL THEN
            RAISE EXCEPTION 'tournament content configuration must begin as draft'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT MAX(revision)
        INTO previous_revision
        FROM public.tournament_content_configurations
        WHERE tournament_id = NEW.tournament_id;

        IF NEW.revision <> COALESCE(previous_revision, 0) + 1 THEN
            RAISE EXCEPTION 'tournament content configuration revisions must be consecutive'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.revision IS DISTINCT FROM OLD.revision
        OR NEW.pool_publication_id IS DISTINCT FROM OLD.pool_publication_id
        OR NEW.normal_pool_revision_id IS DISTINCT FROM OLD.normal_pool_revision_id
        OR NEW.golden_pool_revision_id IS DISTINCT FROM OLD.golden_pool_revision_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'tournament content configuration identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state <> 'draft' OR NEW.state <> 'published' OR NEW.published_at IS NULL THEN
        RAISE EXCEPTION 'tournament content configuration must publish once'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM public.task_pool_revisions AS pool
        WHERE pool.id = NEW.normal_pool_revision_id
            AND pool.publication_id = NEW.pool_publication_id
            AND pool.kind = 'normal'
    ) OR NOT EXISTS (
        SELECT 1
        FROM public.task_pool_revisions AS pool
        WHERE pool.id = NEW.golden_pool_revision_id
            AND pool.publication_id = NEW.pool_publication_id
            AND pool.kind = 'golden'
    ) THEN
        RAISE EXCEPTION 'tournament content pools must belong to one publication'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM public.task_pool_version_memberships
        WHERE task_pool_revision_id = NEW.normal_pool_revision_id
    ) OR NOT EXISTS (
        SELECT 1
        FROM public.task_pool_version_memberships
        WHERE task_pool_revision_id = NEW.golden_pool_revision_id
    ) THEN
        RAISE EXCEPTION 'tournament content pools require task versions'
            USING ERRCODE = 'check_violation';
    END IF;

    FOR locked_task IN
        SELECT task.id, task.enabled, task.deleted_at
        FROM public.task_pool_version_memberships AS membership
        JOIN public.tasks AS task ON task.id = membership.task_id
        WHERE membership.task_pool_revision_id IN (
            NEW.normal_pool_revision_id,
            NEW.golden_pool_revision_id
        )
        ORDER BY task.id
        FOR NO KEY UPDATE OF task
    LOOP
        IF NOT locked_task.enabled OR locked_task.deleted_at IS NOT NULL THEN
            RAISE EXCEPTION 'tournament content cannot publish disabled task versions'
                USING ERRCODE = 'check_violation';
        END IF;
    END LOOP;

    IF EXISTS (
        SELECT 1
        FROM public.task_pool_version_memberships AS membership
        LEFT JOIN LATERAL (
            SELECT attestation.healthy
            FROM public.task_version_health_attestations AS attestation
            WHERE attestation.task_id = membership.task_id
                AND attestation.task_version = membership.task_version
            ORDER BY attestation.revision DESC
            LIMIT 1
        ) AS health ON true
        WHERE membership.task_pool_revision_id IN (
            NEW.normal_pool_revision_id,
            NEW.golden_pool_revision_id
        )
            AND NOT COALESCE(health.healthy, false)
    ) THEN
        RAISE EXCEPTION 'tournament content cannot publish unvalidated task versions'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (
        SELECT COUNT(*)
        FROM public.tournament_category_pool_revisions
        WHERE configuration_id = NEW.id
    ) <> 2 OR EXISTS (
        SELECT required.format
        FROM (VALUES ('bo1'::varchar, 3), ('bo3'::varchar, 5)) AS required(format, category_count)
        LEFT JOIN public.tournament_category_pool_revisions AS pool
            ON pool.configuration_id = NEW.id
            AND pool.format = required.format
        LEFT JOIN public.tournament_category_pool_memberships AS membership
            ON membership.category_pool_revision_id = pool.id
        GROUP BY required.format, required.category_count
        HAVING COUNT(pool.id) = 0 OR COUNT(membership.category) <> required.category_count
    ) THEN
        RAISE EXCEPTION 'tournament content requires exact BO1 and BO3 category pools'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (
        SELECT COUNT(*)
        FROM public.tournament_content_stage_defaults
        WHERE configuration_id = NEW.id
    ) <> 4 OR EXISTS (
        SELECT required.stage
        FROM (
            VALUES
                ('swiss'::varchar, 'bo1'::varchar, 'normal'::varchar),
                ('golden'::varchar, 'bo1'::varchar, 'golden'::varchar),
                ('semifinal'::varchar, 'bo1'::varchar, 'normal'::varchar),
                ('final'::varchar, 'bo3'::varchar, 'normal'::varchar)
        ) AS required(stage, format, task_pool_kind)
        LEFT JOIN public.tournament_content_stage_defaults AS stage_default
            ON stage_default.configuration_id = NEW.id
            AND stage_default.stage = required.stage
        LEFT JOIN public.tournament_category_pool_revisions AS category_pool
            ON category_pool.id = stage_default.category_pool_revision_id
            AND category_pool.configuration_id = NEW.id
        WHERE stage_default.stage IS NULL
            OR stage_default.format <> required.format
            OR stage_default.task_pool_kind <> required.task_pool_kind
            OR category_pool.format <> required.format
            OR (required.stage = 'golden' AND stage_default.category_mode <> 'random')
            OR (required.stage = 'final' AND stage_default.category_mode <> 'draft')
            OR (required.stage IN ('swiss', 'semifinal')
                AND stage_default.category_mode NOT IN ('random', 'admin', 'draft'))
    ) OR EXISTS (
        SELECT 1
        FROM public.tournament_content_stage_defaults AS stage_default
        JOIN public.tournament_category_pool_revisions AS category_pool
            ON category_pool.id = stage_default.category_pool_revision_id
        WHERE stage_default.configuration_id = NEW.id
            AND (
                jsonb_array_length(stage_default.categories) <>
                    CASE stage_default.category_mode
                        WHEN 'draft' THEN (
                            SELECT COUNT(*)
                            FROM public.tournament_category_pool_memberships AS membership
                            WHERE membership.category_pool_revision_id = category_pool.id
                        )
                        ELSE 1
                    END
                OR (
                    SELECT COUNT(DISTINCT category.value)
                    FROM jsonb_array_elements_text(stage_default.categories) AS category(value)
                ) <> jsonb_array_length(stage_default.categories)
                OR EXISTS (
                    SELECT 1
                    FROM jsonb_array_elements_text(stage_default.categories) AS category(value)
                    WHERE NOT EXISTS (
                        SELECT 1
                        FROM public.tournament_category_pool_memberships AS membership
                        WHERE membership.category_pool_revision_id = category_pool.id
                            AND membership.category = category.value
                    )
                )
            )
    ) THEN
        RAISE EXCEPTION 'tournament content stage defaults are invalid'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER tournament_content_configurations_guard
BEFORE DELETE OR INSERT OR UPDATE ON public.tournament_content_configurations
FOR EACH ROW EXECUTE FUNCTION public.tournament_content_configuration_edit_guard();

CREATE TABLE public.tournament_configuration_edit_commands (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(24) NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    source_cutoff_id uuid NOT NULL,
    source_cutoff_sequence bigint NOT NULL,
    source_configuration_id uuid NOT NULL,
    source_configuration_revision bigint NOT NULL,
    result_configuration_id uuid NOT NULL,
    result_configuration_revision bigint NOT NULL,
    source_tournament_revision bigint NOT NULL,
    result_tournament_revision bigint NOT NULL,
    source_series_id uuid,
    source_series_revision bigint,
    result_series_id uuid,
    result_series_revision bigint,
    source_round_id uuid,
    source_round_revision bigint,
    result_round_revision bigint,
    request_digest bytea NOT NULL,
    intent_document jsonb NOT NULL,
    result_document jsonb NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_configuration_edit_commands_pkey PRIMARY KEY (command_id),
    CONSTRAINT tournament_configuration_edit_commands_action_check CHECK (
        action IN ('defaults', 'series', 'swiss_round')
    ),
    CONSTRAINT tournament_configuration_edit_commands_digest_check CHECK (
        octet_length(request_digest) = 32
        AND request_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT tournament_configuration_edit_commands_json_check CHECK (
        jsonb_typeof(intent_document) = 'object'
        AND intent_document <> '{}'::jsonb
        AND jsonb_typeof(result_document) = 'object'
        AND result_document <> '{}'::jsonb
    ),
    CONSTRAINT tournament_configuration_edit_commands_revision_check CHECK (
        source_projection_revision >= 1
        AND source_cutoff_sequence >= 1
        AND source_configuration_revision >= 1
        AND result_configuration_revision >= 1
        AND source_tournament_revision >= 1
        AND result_tournament_revision >= source_tournament_revision
        AND (source_series_revision IS NULL OR source_series_revision >= 1)
        AND (result_series_revision IS NULL OR result_series_revision >= 1)
        AND (source_round_revision IS NULL OR source_round_revision >= 1)
        AND (result_round_revision IS NULL OR result_round_revision >= 1)
    ),
    CONSTRAINT tournament_configuration_edit_commands_scope_check CHECK (
        (action = 'defaults' AND source_series_id IS NULL AND result_series_id IS NULL
            AND source_round_id IS NULL AND source_series_revision IS NULL
            AND result_series_revision IS NULL AND source_round_revision IS NULL
            AND result_round_revision IS NULL)
        OR (action = 'series' AND source_series_id IS NOT NULL AND result_series_id IS NOT NULL
            AND source_series_revision IS NOT NULL AND result_series_revision IS NOT NULL
            AND source_round_id IS NULL AND source_round_revision IS NULL
            AND result_round_revision IS NULL)
        OR (action = 'swiss_round' AND source_round_id IS NOT NULL
            AND source_round_revision IS NOT NULL AND result_round_revision IS NOT NULL
            AND source_series_id IS NULL AND result_series_id IS NULL
            AND source_series_revision IS NULL AND result_series_revision IS NULL)
    ),
    CONSTRAINT tournament_configuration_edit_commands_timestamps_check CHECK (
        occurred_at <= created_at
    ),
    CONSTRAINT tournament_configuration_edit_commands_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_commands_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_commands_source_projection_fk
        FOREIGN KEY (source_projection_revision_id, tournament_id, roster_id, source_projection_revision)
        REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
        ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_commands_source_cutoff_fk
        FOREIGN KEY (source_cutoff_id, tournament_id, roster_id)
        REFERENCES public.projection_cutoffs(id, tournament_id, roster_id)
        ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_commands_source_configuration_fk
        FOREIGN KEY (source_configuration_id, tournament_id)
        REFERENCES public.tournament_content_configurations(id, tournament_id)
        ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_commands_result_configuration_fk
        FOREIGN KEY (result_configuration_id, tournament_id)
        REFERENCES public.tournament_content_configurations(id, tournament_id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX tournament_configuration_edit_commands_digest_key
ON public.tournament_configuration_edit_commands (tournament_id, request_digest);

CREATE TABLE public.tournament_configuration_edit_unlock_intents (
    intent_id uuid NOT NULL,
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(24) NOT NULL,
    artifact_id uuid NOT NULL,
    expected_revision bigint NOT NULL,
    reservation_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    invalidate_proof boolean DEFAULT false NOT NULL,
    invalidate_readiness boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_configuration_edit_unlock_intents_pkey PRIMARY KEY (intent_id),
    CONSTRAINT tournament_configuration_edit_unlock_intents_kind_check CHECK (
        artifact_kind IN ('series', 'swiss_round', 'wave')
    ),
    CONSTRAINT tournament_configuration_edit_unlock_intents_revision_check CHECK (
        expected_revision >= 1
    ),
    CONSTRAINT tournament_configuration_edit_unlock_intents_reservations_check CHECK (
        array_position(reservation_ids, NULL::uuid) IS NULL
    ),
    CONSTRAINT tournament_configuration_edit_unlock_intents_command_fk
        FOREIGN KEY (command_id) REFERENCES public.tournament_configuration_edit_commands(command_id)
        ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_unlock_intents_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_unlock_intents_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX tournament_configuration_edit_unlock_intents_scope_key
ON public.tournament_configuration_edit_unlock_intents (command_id, artifact_kind, artifact_id);

CREATE TABLE public.tournament_configuration_edit_artifacts (
    lineage_id uuid NOT NULL,
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(32) NOT NULL,
    source_artifact_id uuid NOT NULL,
    source_artifact_revision bigint,
    successor_artifact_id uuid,
    successor_artifact_revision bigint,
    proof_hash bytea,
    lineage_document jsonb NOT NULL,
    superseded_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_configuration_edit_artifacts_pkey PRIMARY KEY (lineage_id),
    CONSTRAINT tournament_configuration_edit_artifacts_kind_check CHECK (
        artifact_kind IN (
            'configuration', 'series', 'swiss_round', 'wave', 'category_revision',
            'assignment_plan', 'assignment', 'reservation', 'readiness', 'proof'
        )
    ),
    CONSTRAINT tournament_configuration_edit_artifacts_revision_check CHECK (
        (source_artifact_revision IS NULL OR source_artifact_revision >= 1)
        AND (successor_artifact_revision IS NULL OR successor_artifact_revision >= 1)
    ),
    CONSTRAINT tournament_configuration_edit_artifacts_hash_check CHECK (
        proof_hash IS NULL OR octet_length(proof_hash) = 32
    ),
    CONSTRAINT tournament_configuration_edit_artifacts_json_check CHECK (
        jsonb_typeof(lineage_document) = 'object'
        AND lineage_document <> '{}'::jsonb
    ),
    CONSTRAINT tournament_configuration_edit_artifacts_timestamps_check CHECK (
        superseded_at <= created_at
    ),
    CONSTRAINT tournament_configuration_edit_artifacts_command_fk
        FOREIGN KEY (command_id) REFERENCES public.tournament_configuration_edit_commands(command_id)
        ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_artifacts_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_artifacts_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX tournament_configuration_edit_artifacts_source_key
ON public.tournament_configuration_edit_artifacts (command_id, artifact_kind, source_artifact_id);

CREATE TABLE public.tournament_configuration_edit_invalidations (
    invalidation_id uuid NOT NULL,
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(24) NOT NULL,
    artifact_id uuid NOT NULL,
    artifact_revision bigint,
    reason text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_configuration_edit_invalidations_pkey PRIMARY KEY (invalidation_id),
    CONSTRAINT tournament_configuration_edit_invalidations_kind_check CHECK (
        artifact_kind IN ('proof', 'readiness', 'ready_window')
    ),
    CONSTRAINT tournament_configuration_edit_invalidations_revision_check CHECK (
        artifact_revision IS NULL OR artifact_revision >= 1
    ),
    CONSTRAINT tournament_configuration_edit_invalidations_reason_check CHECK (
        reason = btrim(reason) AND reason <> ''
    ),
    CONSTRAINT tournament_configuration_edit_invalidations_command_fk
        FOREIGN KEY (command_id) REFERENCES public.tournament_configuration_edit_commands(command_id)
        ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_invalidations_tournament_fk
        FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT,
    CONSTRAINT tournament_configuration_edit_invalidations_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX tournament_configuration_edit_invalidations_scope_key
ON public.tournament_configuration_edit_invalidations (command_id, artifact_kind, artifact_id);

CREATE OR REPLACE FUNCTION public.tournament_configuration_edit_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'tournament configuration edit evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tournament_configuration_edit_commands_append_only
BEFORE UPDATE OR DELETE ON public.tournament_configuration_edit_commands
FOR EACH ROW EXECUTE FUNCTION public.tournament_configuration_edit_append_only();

CREATE TRIGGER tournament_configuration_edit_unlock_intents_append_only
BEFORE UPDATE OR DELETE ON public.tournament_configuration_edit_unlock_intents
FOR EACH ROW EXECUTE FUNCTION public.tournament_configuration_edit_append_only();

CREATE TRIGGER tournament_configuration_edit_artifacts_append_only
BEFORE UPDATE OR DELETE ON public.tournament_configuration_edit_artifacts
FOR EACH ROW EXECUTE FUNCTION public.tournament_configuration_edit_append_only();

CREATE TRIGGER tournament_configuration_edit_invalidations_append_only
BEFORE UPDATE OR DELETE ON public.tournament_configuration_edit_invalidations
FOR EACH ROW EXECUTE FUNCTION public.tournament_configuration_edit_append_only();

ALTER TABLE public.series
    ADD COLUMN supersedes_series_id uuid,
    ADD COLUMN superseded_by_series_id uuid,
    ADD COLUMN superseded_at timestamp with time zone,
    ADD COLUMN supersession_reason text,
    ADD COLUMN content_configuration_id uuid,
    ADD COLUMN content_configuration_revision bigint,
    ADD COLUMN category_mode character varying(16),
    ADD COLUMN effective_categories jsonb;

ALTER TABLE public.series
    DROP CONSTRAINT series_state_check,
    DROP CONSTRAINT series_result_check,
    DROP CONSTRAINT series_timestamps_check,
    ADD CONSTRAINT series_state_check CHECK (
        state IN ('planned', 'locked', 'draft', 'ready', 'active', 'replay_required',
            'technical_pause', 'completed', 'cancelled', 'superseded')
    ),
    ADD CONSTRAINT series_result_check CHECK (
        (state = 'completed' AND winner_id IS NOT NULL
            AND current_score_revision_id IS NOT NULL
            AND current_result_revision_id IS NOT NULL
            AND ((winner_id = first_participant_id AND ((format = 'bo1' AND first_participant_wins = 1)
                OR (format = 'bo3' AND first_participant_wins = 2)))
                OR (winner_id = second_participant_id AND ((format = 'bo1' AND second_participant_wins = 1)
                OR (format = 'bo3' AND second_participant_wins = 2)))))
        OR (state = 'cancelled' AND current_score_revision_id IS NOT NULL
            AND current_result_revision_id IS NOT NULL)
        OR (state = 'superseded' AND winner_id IS NULL
            AND current_result_revision_id IS NULL AND started_at IS NULL
            AND finished_at IS NULL AND superseded_at IS NOT NULL)
        OR (state NOT IN ('completed', 'cancelled', 'superseded')
            AND winner_id IS NULL AND current_result_revision_id IS NULL)
    ),
    ADD CONSTRAINT series_timestamps_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= COALESCE(started_at, created_at))
        AND (superseded_at IS NULL OR superseded_at >= created_at)
        AND (
            (state IN ('completed', 'cancelled') AND finished_at IS NOT NULL AND superseded_at IS NULL)
            OR (state = 'superseded' AND finished_at IS NULL AND superseded_at IS NOT NULL)
            OR (state NOT IN ('completed', 'cancelled', 'superseded')
                AND finished_at IS NULL AND superseded_at IS NULL)
        )
    ),
    ADD CONSTRAINT series_configuration_binding_check CHECK (
        (content_configuration_id IS NULL) = (content_configuration_revision IS NULL)
        AND (content_configuration_revision IS NULL OR content_configuration_revision >= 1)
        AND (category_mode IS NULL) = (effective_categories IS NULL)
        AND (category_mode IS NULL OR category_mode IN ('random', 'admin', 'draft'))
        AND (effective_categories IS NULL
            OR (jsonb_typeof(effective_categories) = 'array'
                AND jsonb_array_length(effective_categories) > 0))
    );

ALTER TABLE public.series
    ADD CONSTRAINT series_supersedes_fk
        FOREIGN KEY (supersedes_series_id, tournament_id, roster_id)
        REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT,
    ADD CONSTRAINT series_superseded_by_fk
        FOREIGN KEY (superseded_by_series_id, tournament_id, roster_id)
        REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT,
    ADD CONSTRAINT series_content_configuration_fk
        FOREIGN KEY (content_configuration_id, tournament_id)
        REFERENCES public.tournament_content_configurations(id, tournament_id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX series_supersedes_source_key
ON public.series (supersedes_series_id)
WHERE supersedes_series_id IS NOT NULL;

CREATE UNIQUE INDEX series_superseded_target_key
ON public.series (superseded_by_series_id)
WHERE superseded_by_series_id IS NOT NULL;

CREATE INDEX series_active_configuration_idx
ON public.series (tournament_id, roster_id, state, revision)
WHERE state <> 'superseded';

CREATE OR REPLACE FUNCTION public.series_configuration_edit_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_state VARCHAR(32);
    source_started_at TIMESTAMPTZ;
    source_revision BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.state = 'superseded' THEN
            RAISE EXCEPTION 'a replacement Series cannot begin superseded'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.supersedes_series_id IS NOT NULL THEN
            SELECT state, started_at, revision
            INTO source_state, source_started_at, source_revision
            FROM public.series
            WHERE id = NEW.supersedes_series_id
                AND tournament_id = NEW.tournament_id
                AND roster_id = NEW.roster_id
            FOR KEY SHARE;
            IF source_state IS NULL OR source_state NOT IN ('planned', 'locked', 'draft', 'ready')
                OR source_started_at IS NOT NULL OR source_revision < 1 THEN
                RAISE EXCEPTION 'Series replacement source is started or unavailable'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.state = 'superseded' THEN
        RAISE EXCEPTION 'superseded Series is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.superseded_by_series_id IS DISTINCT FROM OLD.superseded_by_series_id THEN
        IF OLD.state NOT IN ('planned', 'locked', 'draft', 'ready')
            OR NEW.state <> 'superseded'
            OR NEW.revision <> OLD.revision + 1
            OR OLD.started_at IS NOT NULL
            OR NEW.started_at IS NOT NULL
            OR NEW.superseded_by_series_id IS NULL
            OR NEW.superseded_at IS NULL
            OR NEW.supersession_reason IS NULL
            OR btrim(NEW.supersession_reason) = '' THEN
            RAISE EXCEPTION 'invalid unstarted Series supersession'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF OLD.content_configuration_id IS NOT NULL
        AND (NEW.content_configuration_id IS DISTINCT FROM OLD.content_configuration_id
            OR NEW.content_configuration_revision IS DISTINCT FROM OLD.content_configuration_revision
            OR NEW.category_mode IS DISTINCT FROM OLD.category_mode
            OR NEW.effective_categories IS DISTINCT FROM OLD.effective_categories) THEN
        RAISE EXCEPTION 'Series content authority is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER series_configuration_edit_guard
BEFORE INSERT OR UPDATE ON public.series
FOR EACH ROW EXECUTE FUNCTION public.series_configuration_edit_guard();

ALTER TABLE public.swiss_rounds
    ADD COLUMN tournament_id uuid,
    ADD COLUMN content_configuration_id uuid,
    ADD COLUMN content_configuration_revision bigint,
    ADD COLUMN category_mode character varying(16),
    ADD COLUMN effective_categories jsonb;

UPDATE public.swiss_rounds AS round
SET tournament_id = roster.tournament_id
FROM public.rosters AS roster
WHERE roster.id = round.roster_id;

ALTER TABLE public.swiss_rounds
    ALTER COLUMN tournament_id SET NOT NULL,
    ADD CONSTRAINT swiss_rounds_id_roster_tournament_key UNIQUE (id, roster_id, tournament_id),
    ADD CONSTRAINT swiss_rounds_tournament_roster_fk
        FOREIGN KEY (roster_id, tournament_id)
        REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT,
    ADD CONSTRAINT swiss_rounds_configuration_binding_check CHECK (
        (content_configuration_id IS NULL) = (content_configuration_revision IS NULL)
        AND (content_configuration_revision IS NULL OR content_configuration_revision >= 1)
        AND (category_mode IS NULL) = (effective_categories IS NULL)
        AND (category_mode IS NULL OR category_mode IN ('random', 'admin', 'draft'))
        AND (effective_categories IS NULL
            OR (jsonb_typeof(effective_categories) = 'array'
                AND jsonb_array_length(effective_categories) > 0))
    ),
    ADD CONSTRAINT swiss_rounds_content_configuration_fk
        FOREIGN KEY (content_configuration_id, tournament_id)
        REFERENCES public.tournament_content_configurations(id, tournament_id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION public.swiss_round_configuration_edit_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    configuration_revision BIGINT;
BEGIN
    IF NEW.content_configuration_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT revision INTO configuration_revision
    FROM public.tournament_content_configurations
    WHERE id = NEW.content_configuration_id
        AND tournament_id = NEW.tournament_id
        AND state = 'published';

    IF configuration_revision IS NULL
        OR configuration_revision <> NEW.content_configuration_revision THEN
        RAISE EXCEPTION 'Swiss round content configuration revision is stale'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER swiss_round_configuration_edit_guard
BEFORE INSERT OR UPDATE ON public.swiss_rounds
FOR EACH ROW EXECUTE FUNCTION public.swiss_round_configuration_edit_guard();

CREATE OR REPLACE FUNCTION public.series_content_configuration_revision_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    configuration_revision BIGINT;
BEGIN
    IF NEW.content_configuration_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT revision INTO configuration_revision
    FROM public.tournament_content_configurations
    WHERE id = NEW.content_configuration_id
        AND tournament_id = NEW.tournament_id
        AND state = 'published';

    IF configuration_revision IS NULL
        OR configuration_revision <> NEW.content_configuration_revision THEN
        RAISE EXCEPTION 'Series content configuration revision is stale'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER series_content_configuration_revision_guard
BEFORE INSERT OR UPDATE ON public.series
FOR EACH ROW EXECUTE FUNCTION public.series_content_configuration_revision_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.tournament_configuration_edit_commands)
        OR EXISTS (SELECT 1 FROM public.tournament_configuration_edit_unlock_intents)
        OR EXISTS (SELECT 1 FROM public.tournament_configuration_edit_artifacts)
        OR EXISTS (SELECT 1 FROM public.tournament_configuration_edit_invalidations) THEN
        RAISE EXCEPTION 'cannot roll back configuration edit evidence after use';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS series_content_configuration_revision_guard ON public.series;
DROP FUNCTION IF EXISTS public.series_content_configuration_revision_guard();
DROP TRIGGER IF EXISTS series_configuration_edit_guard ON public.series;
DROP FUNCTION IF EXISTS public.series_configuration_edit_guard();
DROP TRIGGER IF EXISTS swiss_round_configuration_edit_guard ON public.swiss_rounds;
DROP FUNCTION IF EXISTS public.swiss_round_configuration_edit_guard();
DROP TRIGGER IF EXISTS tournament_content_configuration_head_publish ON public.tournament_content_configurations;
DROP FUNCTION IF EXISTS public.tournament_content_configuration_head_publish();
DROP TRIGGER IF EXISTS tournament_content_configurations_guard ON public.tournament_content_configurations;
DROP FUNCTION IF EXISTS public.tournament_content_configuration_edit_guard();

CREATE TRIGGER tournament_content_configurations_guard
BEFORE DELETE OR INSERT OR UPDATE ON public.tournament_content_configurations
FOR EACH ROW EXECUTE FUNCTION public.tournament_content_configuration_guard();

DROP TRIGGER IF EXISTS tournament_configuration_edit_invalidations_append_only ON public.tournament_configuration_edit_invalidations;
DROP TRIGGER IF EXISTS tournament_configuration_edit_artifacts_append_only ON public.tournament_configuration_edit_artifacts;
DROP TRIGGER IF EXISTS tournament_configuration_edit_unlock_intents_append_only ON public.tournament_configuration_edit_unlock_intents;
DROP TRIGGER IF EXISTS tournament_configuration_edit_commands_append_only ON public.tournament_configuration_edit_commands;
DROP FUNCTION IF EXISTS public.tournament_configuration_edit_append_only();

DROP TABLE public.tournament_configuration_edit_invalidations;
DROP TABLE public.tournament_configuration_edit_artifacts;
DROP TABLE public.tournament_configuration_edit_unlock_intents;
DROP TABLE public.tournament_configuration_edit_commands;
DROP TABLE public.tournament_content_configuration_heads;

ALTER TABLE public.tournament_content_stage_defaults
    DROP CONSTRAINT tournament_content_stage_defaults_categories_check,
    DROP COLUMN categories,
    DROP CONSTRAINT tournament_content_stage_defaults_category_mode_check,
    ADD CONSTRAINT tournament_content_stage_defaults_category_mode_check CHECK (
        category_mode IN ('random', 'draft')
    );

ALTER TABLE public.swiss_rounds
    DROP CONSTRAINT swiss_rounds_content_configuration_fk,
    DROP CONSTRAINT swiss_rounds_configuration_binding_check,
    DROP CONSTRAINT swiss_rounds_tournament_roster_fk,
    DROP CONSTRAINT swiss_rounds_id_roster_tournament_key,
    DROP COLUMN effective_categories,
    DROP COLUMN category_mode,
    DROP COLUMN content_configuration_revision,
    DROP COLUMN content_configuration_id,
    DROP COLUMN tournament_id;

ALTER TABLE public.series
    DROP CONSTRAINT series_content_configuration_fk,
    DROP CONSTRAINT series_superseded_by_fk,
    DROP CONSTRAINT series_supersedes_fk,
    DROP CONSTRAINT series_configuration_binding_check,
    DROP CONSTRAINT series_timestamps_check,
    DROP CONSTRAINT series_result_check,
    DROP CONSTRAINT series_state_check,
    DROP COLUMN effective_categories,
    DROP COLUMN category_mode,
    DROP COLUMN content_configuration_revision,
    DROP COLUMN content_configuration_id,
    DROP COLUMN supersession_reason,
    DROP COLUMN superseded_at,
    DROP COLUMN superseded_by_series_id,
    DROP COLUMN supersedes_series_id;

DROP INDEX IF EXISTS public.series_active_configuration_idx;
DROP INDEX IF EXISTS public.series_superseded_target_key;
DROP INDEX IF EXISTS public.series_supersedes_source_key;

-- Restore the original checks for a lossless rollback before this migration
-- has been used. Historical rows were never modified by the migration.
ALTER TABLE public.series
    ADD CONSTRAINT series_state_check CHECK (
        state IN ('planned', 'locked', 'draft', 'ready', 'active', 'replay_required',
            'technical_pause', 'completed', 'cancelled')
    ),
    ADD CONSTRAINT series_result_check CHECK (
        (state = 'completed' AND winner_id IS NOT NULL
            AND current_score_revision_id IS NOT NULL AND current_result_revision_id IS NOT NULL)
        OR (state = 'cancelled' AND current_score_revision_id IS NOT NULL
            AND current_result_revision_id IS NOT NULL)
        OR (state NOT IN ('completed', 'cancelled')
            AND winner_id IS NULL AND current_result_revision_id IS NULL)
    ),
    ADD CONSTRAINT series_timestamps_check CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= COALESCE(started_at, created_at))
        AND ((state IN ('completed', 'cancelled') AND finished_at IS NOT NULL)
            OR (state NOT IN ('completed', 'cancelled') AND finished_at IS NULL))
    );

-- +goose StatementEnd
