-- +goose Up
-- +goose StatementBegin

-- Initial task content authority schema.
SET LOCAL check_function_bodies = false;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE FUNCTION public.task_content_digest(
    content_title character varying,
    content_description text,
    content_category character varying,
    content_difficulty character varying,
    content_time_limit integer,
    content_flag character varying,
    content_hint_1 text,
    content_hint_2 text,
    content_hint_3 text,
    content_task_url text,
    content_source_file_url text
) RETURNS bytea
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
    AS $$
    SELECT digest(
        convert_to(
            jsonb_build_object(
                'category', content_category,
                'description', content_description,
                'difficulty', content_difficulty,
                'flag', content_flag,
                'hints', jsonb_build_array(content_hint_1, content_hint_2, content_hint_3),
                'source_file_url', content_source_file_url,
                'task_url', content_task_url,
                'time_limit', content_time_limit,
                'title', content_title
            )::text,
            'UTF8'
        ),
        'sha256'
    );
$$;

CREATE TABLE public.tasks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    title character varying(255) NOT NULL,
    description text NOT NULL,
    category character varying(50) NOT NULL,
    difficulty character varying(10) NOT NULL,
    time_limit integer NOT NULL,
    flag character varying(255) NOT NULL,
    hint_1 text,
    hint_2 text,
    hint_3 text,
    task_url text,
    source_file_url text,
    kind character varying(16) DEFAULT 'normal'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    current_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT tasks_category_check CHECK (
        (category)::text = ANY (
            ARRAY[
                'web'::character varying,
                'crypto'::character varying,
                'forensics'::character varying,
                'reverse'::character varying,
                'pwn'::character varying,
                'steganography'::character varying,
                'ppc'::character varying,
                'osint'::character varying,
                'mobile'::character varying,
                'hardware'::character varying,
                'misc'::character varying
            ]::text[]
        )
    ),
    CONSTRAINT tasks_content_check CHECK (
        btrim((title)::text) <> ''::text
        AND btrim(description) <> ''::text
        AND btrim((flag)::text) <> ''::text
        AND time_limit > 0
    ),
    CONSTRAINT tasks_current_version_check CHECK (current_version >= 1),
    CONSTRAINT tasks_difficulty_check CHECK (
        (difficulty)::text = ANY (
            ARRAY['easy'::character varying, 'medium'::character varying, 'hard'::character varying]::text[]
        )
    ),
    CONSTRAINT tasks_kind_check CHECK (
        (kind)::text = ANY (ARRAY['normal'::character varying, 'golden'::character varying]::text[])
    ),
    CONSTRAINT tasks_timestamps_check CHECK (
        updated_at >= created_at
        AND (deleted_at IS NULL OR deleted_at >= created_at)
        AND (deleted_at IS NULL OR NOT enabled)
    )
);

CREATE TABLE public.task_versions (
    task_id uuid NOT NULL,
    version integer NOT NULL,
    title character varying(255) NOT NULL,
    description text NOT NULL,
    category character varying(50) NOT NULL,
    difficulty character varying(10) NOT NULL,
    time_limit integer NOT NULL,
    flag character varying(255) NOT NULL,
    hint_1 text,
    hint_2 text,
    hint_3 text,
    task_url text,
    source_file_url text,
    content_digest bytea GENERATED ALWAYS AS (
        public.task_content_digest(
            title,
            description,
            category,
            difficulty,
            time_limit,
            flag,
            hint_1,
            hint_2,
            hint_3,
            task_url,
            source_file_url
        )
    ) STORED,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_versions_category_check CHECK (
        (category)::text = ANY (
            ARRAY[
                'web'::character varying,
                'crypto'::character varying,
                'forensics'::character varying,
                'reverse'::character varying,
                'pwn'::character varying,
                'steganography'::character varying,
                'ppc'::character varying,
                'osint'::character varying,
                'mobile'::character varying,
                'hardware'::character varying,
                'misc'::character varying
            ]::text[]
        )
    ),
    CONSTRAINT task_versions_content_check CHECK (
        btrim((title)::text) <> ''::text
        AND btrim(description) <> ''::text
        AND btrim((flag)::text) <> ''::text
        AND time_limit > 0
        AND octet_length(content_digest) = 32
    ),
    CONSTRAINT task_versions_difficulty_check CHECK (
        (difficulty)::text = ANY (
            ARRAY['easy'::character varying, 'medium'::character varying, 'hard'::character varying]::text[]
        )
    ),
    CONSTRAINT task_versions_pkey PRIMARY KEY (task_id, version),
    CONSTRAINT task_versions_version_check CHECK (version >= 1)
);

CREATE TABLE public.task_version_health_attestations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    revision bigint NOT NULL,
    healthy boolean NOT NULL,
    source character varying(32) NOT NULL,
    attested_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_version_health_attestations_pkey PRIMARY KEY (id),
    CONSTRAINT task_version_health_attestations_revision_key UNIQUE (task_id, task_version, revision),
    CONSTRAINT task_version_health_attestations_revision_check CHECK (revision >= 1),
    CONSTRAINT task_version_health_attestations_source_check CHECK (
        (source)::text = ANY (ARRAY['content_validation'::character varying, 'probe'::character varying]::text[])
    ),
    CONSTRAINT task_version_health_attestations_version_check CHECK (task_version >= 1)
);

CREATE TABLE public.task_pool_publication_lock (
    singleton boolean PRIMARY KEY DEFAULT true,
    CONSTRAINT task_pool_publication_lock_singleton_check CHECK (singleton)
);

CREATE TABLE public.task_pool_publications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    revision bigint NOT NULL,
    published_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_pool_publications_pkey PRIMARY KEY (id),
    CONSTRAINT task_pool_publications_revision_check CHECK (revision >= 1),
    CONSTRAINT task_pool_publications_revision_key UNIQUE (revision)
);

CREATE TABLE public.task_pool_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    publication_id uuid NOT NULL,
    kind character varying(16) NOT NULL,
    revision bigint NOT NULL,
    published_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_pool_revisions_kind_check CHECK (
        (kind)::text = ANY (ARRAY['normal'::character varying, 'golden'::character varying]::text[])
    ),
    CONSTRAINT task_pool_revisions_pkey PRIMARY KEY (id),
    CONSTRAINT task_pool_revisions_publication_kind_key UNIQUE (publication_id, kind),
    CONSTRAINT task_pool_revisions_revision_check CHECK (revision >= 1)
);

CREATE TABLE public.task_pool_version_memberships (
    task_pool_revision_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_version integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_pool_version_memberships_pkey PRIMARY KEY (task_pool_revision_id, task_id, task_version),
    CONSTRAINT task_pool_version_memberships_version_check CHECK (task_version >= 1)
);

CREATE TABLE public.tournament_content_configurations (
    id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    revision bigint NOT NULL,
    state character varying(16) DEFAULT 'draft'::character varying NOT NULL,
    pool_publication_id uuid NOT NULL,
    normal_pool_revision_id uuid NOT NULL,
    golden_pool_revision_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    published_at timestamp with time zone,
    CONSTRAINT tournament_content_configurations_pkey PRIMARY KEY (id),
    CONSTRAINT tournament_content_configurations_revision_check CHECK (revision >= 1),
    CONSTRAINT tournament_content_configurations_state_check CHECK (
        (state)::text = ANY (ARRAY['draft'::character varying, 'published'::character varying]::text[])
    ),
    CONSTRAINT tournament_content_configurations_state_evidence_check CHECK (
        ((state)::text = 'draft'::text AND published_at IS NULL)
        OR ((state)::text = 'published'::text AND published_at IS NOT NULL)
    ),
    CONSTRAINT tournament_content_configurations_tournament_revision_key UNIQUE (tournament_id, revision)
);

CREATE TABLE public.tournament_category_pool_revisions (
    id uuid NOT NULL,
    configuration_id uuid NOT NULL,
    format character varying(16) NOT NULL,
    revision bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_category_pool_revisions_format_check CHECK (
        (format)::text = ANY (ARRAY['bo1'::character varying, 'bo3'::character varying]::text[])
    ),
    CONSTRAINT tournament_category_pool_revisions_pkey PRIMARY KEY (id),
    CONSTRAINT tournament_category_pool_revisions_configuration_format_key UNIQUE (configuration_id, format),
    CONSTRAINT tournament_category_pool_revisions_revision_check CHECK (revision >= 1)
);

CREATE TABLE public.tournament_category_pool_memberships (
    category_pool_revision_id uuid NOT NULL,
    category character varying(50) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_category_pool_memberships_category_check CHECK (
        (category)::text = ANY (
            ARRAY[
                'web'::character varying,
                'crypto'::character varying,
                'forensics'::character varying,
                'reverse'::character varying,
                'pwn'::character varying,
                'steganography'::character varying,
                'ppc'::character varying,
                'osint'::character varying,
                'mobile'::character varying,
                'hardware'::character varying,
                'misc'::character varying
            ]::text[]
        )
    ),
    CONSTRAINT tournament_category_pool_memberships_pkey PRIMARY KEY (category_pool_revision_id, category)
);

CREATE TABLE public.tournament_content_stage_defaults (
    configuration_id uuid NOT NULL,
    stage character varying(16) NOT NULL,
    format character varying(16) NOT NULL,
    category_mode character varying(16) NOT NULL,
    category_pool_revision_id uuid NOT NULL,
    task_pool_kind character varying(16) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_content_stage_defaults_category_mode_check CHECK (
        (category_mode)::text = ANY (ARRAY['random'::character varying, 'draft'::character varying]::text[])
    ),
    CONSTRAINT tournament_content_stage_defaults_format_check CHECK (
        (format)::text = ANY (ARRAY['bo1'::character varying, 'bo3'::character varying]::text[])
    ),
    CONSTRAINT tournament_content_stage_defaults_pkey PRIMARY KEY (configuration_id, stage),
    CONSTRAINT tournament_content_stage_defaults_stage_check CHECK (
        (stage)::text = ANY (
            ARRAY[
                'swiss'::character varying,
                'golden'::character varying,
                'semifinal'::character varying,
                'final'::character varying
            ]::text[]
        )
    ),
    CONSTRAINT tournament_content_stage_defaults_task_pool_kind_check CHECK (
        (task_pool_kind)::text = ANY (ARRAY['normal'::character varying, 'golden'::character varying]::text[])
    )
);

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.task_versions
    ADD CONSTRAINT task_versions_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_version_health_attestations
    ADD CONSTRAINT task_version_health_attestations_task_version_fkey FOREIGN KEY (task_id, task_version) REFERENCES public.task_versions(task_id, version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_pool_revisions
    ADD CONSTRAINT task_pool_revisions_publication_id_fkey FOREIGN KEY (publication_id) REFERENCES public.task_pool_publications(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_pool_version_memberships
    ADD CONSTRAINT task_pool_version_memberships_pool_revision_id_fkey FOREIGN KEY (task_pool_revision_id) REFERENCES public.task_pool_revisions(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.task_pool_version_memberships
    ADD CONSTRAINT task_pool_version_memberships_task_version_fkey FOREIGN KEY (task_id, task_version) REFERENCES public.task_versions(task_id, version) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_content_configurations
    ADD CONSTRAINT tournament_content_configurations_golden_pool_revision_id_fkey FOREIGN KEY (golden_pool_revision_id) REFERENCES public.task_pool_revisions(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_content_configurations
    ADD CONSTRAINT tournament_content_configurations_normal_pool_revision_id_fkey FOREIGN KEY (normal_pool_revision_id) REFERENCES public.task_pool_revisions(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_content_configurations
    ADD CONSTRAINT tournament_content_configurations_pool_publication_id_fkey FOREIGN KEY (pool_publication_id) REFERENCES public.task_pool_publications(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_category_pool_revisions
    ADD CONSTRAINT tournament_category_pool_revisions_configuration_id_fkey FOREIGN KEY (configuration_id) REFERENCES public.tournament_content_configurations(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_category_pool_memberships
    ADD CONSTRAINT tournament_category_pool_memberships_revision_id_fkey FOREIGN KEY (category_pool_revision_id) REFERENCES public.tournament_category_pool_revisions(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_content_stage_defaults
    ADD CONSTRAINT tournament_content_stage_defaults_category_pool_revision_id_fkey FOREIGN KEY (category_pool_revision_id) REFERENCES public.tournament_category_pool_revisions(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_content_stage_defaults
    ADD CONSTRAINT tournament_content_stage_defaults_configuration_id_fkey FOREIGN KEY (configuration_id) REFERENCES public.tournament_content_configurations(id) ON DELETE RESTRICT;

CREATE INDEX tasks_difficulty_idx ON public.tasks USING btree (difficulty) WHERE (deleted_at IS NULL);

CREATE INDEX tasks_enabled_kind_idx ON public.tasks USING btree (kind, id) WHERE (enabled AND deleted_at IS NULL);

CREATE INDEX task_pool_version_memberships_task_version_idx ON public.task_pool_version_memberships USING btree (task_id, task_version);

CREATE INDEX task_version_health_attestations_current_idx ON public.task_version_health_attestations USING btree (task_id, task_version, revision DESC);

CREATE INDEX tournament_content_configurations_current_idx ON public.tournament_content_configurations USING btree (tournament_id, revision DESC) WHERE ((state)::text = 'published'::text);

CREATE FUNCTION public.immutable_content_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION '% is immutable content evidence', TG_TABLE_NAME
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE FUNCTION public.task_head_version_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    content_changed boolean;
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.current_version := 1;
        NEW.updated_at := NEW.created_at;
        RETURN NEW;
    END IF;

    IF OLD.deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'deleted tasks cannot change'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.current_version IS DISTINCT FROM OLD.current_version THEN
        RAISE EXCEPTION 'task version is server owned'
            USING ERRCODE = 'check_violation';
    END IF;

    content_changed := ROW(
        NEW.title,
        NEW.description,
        NEW.category,
        NEW.difficulty,
        NEW.time_limit,
        NEW.flag,
        NEW.hint_1,
        NEW.hint_2,
        NEW.hint_3,
        NEW.task_url,
        NEW.source_file_url,
        NEW.kind,
        NEW.enabled,
        NEW.deleted_at
    ) IS DISTINCT FROM ROW(
        OLD.title,
        OLD.description,
        OLD.category,
        OLD.difficulty,
        OLD.time_limit,
        OLD.flag,
        OLD.hint_1,
        OLD.hint_2,
        OLD.hint_3,
        OLD.task_url,
        OLD.source_file_url,
        OLD.kind,
        OLD.enabled,
        OLD.deleted_at
    );

    IF NEW.deleted_at IS NOT NULL AND (OLD.deleted_at IS NOT NULL OR NEW.enabled) THEN
        RAISE EXCEPTION 'task deletion must disable the task'
            USING ERRCODE = 'check_violation';
    END IF;

    IF content_changed THEN
        NEW.current_version := OLD.current_version + 1;
        NEW.updated_at := clock_timestamp();
    ELSIF NEW.updated_at IS DISTINCT FROM OLD.updated_at THEN
        RAISE EXCEPTION 'task update timestamp is server owned'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.task_version_health_attestation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    next_revision bigint;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'task health attestations are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    PERFORM 1
    FROM task_versions AS task_version
    WHERE task_version.task_id = NEW.task_id
        AND task_version.version = NEW.task_version
    FOR UPDATE;

    SELECT COALESCE(MAX(revision), 0) + 1
    INTO next_revision
    FROM task_version_health_attestations
    WHERE task_id = NEW.task_id
        AND task_version = NEW.task_version;

    IF NEW.revision <> next_revision THEN
        RAISE EXCEPTION 'task health attestations must be consecutive'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.publish_task_pool_heads() RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    publication_id uuid;
    publication_revision bigint;
    normal_pool_id uuid;
    golden_pool_id uuid;
BEGIN
    PERFORM 1
    FROM task_pool_publication_lock
    WHERE singleton
    FOR UPDATE;

    SELECT COALESCE(MAX(revision), 0) + 1
    INTO publication_revision
    FROM task_pool_publications;

    INSERT INTO task_pool_publications (revision)
    VALUES (publication_revision)
    RETURNING id INTO publication_id;

    INSERT INTO task_pool_revisions (publication_id, kind, revision)
    VALUES (publication_id, 'normal', publication_revision)
    RETURNING id INTO normal_pool_id;

    INSERT INTO task_pool_revisions (publication_id, kind, revision)
    VALUES (publication_id, 'golden', publication_revision)
    RETURNING id INTO golden_pool_id;

    INSERT INTO task_pool_version_memberships (task_pool_revision_id, task_id, task_version)
    SELECT
        CASE task.kind
            WHEN 'normal' THEN normal_pool_id
            WHEN 'golden' THEN golden_pool_id
        END,
        task.id,
        task.current_version
    FROM tasks AS task
    WHERE task.enabled
        AND task.deleted_at IS NULL;

    RETURN;
END;
$$;

CREATE FUNCTION public.append_task_version() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.current_version = OLD.current_version THEN
        RETURN NULL;
    END IF;

    INSERT INTO task_versions (
        task_id,
        version,
        title,
        description,
        category,
        difficulty,
        time_limit,
        flag,
        hint_1,
        hint_2,
        hint_3,
        task_url,
        source_file_url,
        created_at
    )
    VALUES (
        NEW.id,
        NEW.current_version,
        NEW.title,
        NEW.description,
        NEW.category,
        NEW.difficulty,
        NEW.time_limit,
        NEW.flag,
        NEW.hint_1,
        NEW.hint_2,
        NEW.hint_3,
        NEW.task_url,
        NEW.source_file_url,
        NEW.updated_at
    );

    PERFORM public.publish_task_pool_heads();
    RETURN NULL;
END;
$$;

CREATE FUNCTION public.tournament_content_draft_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    new_configuration_state character varying(16);
    old_configuration_state character varying(16);
BEGIN
    IF TG_OP IN ('DELETE', 'UPDATE') THEN
        SELECT state
        INTO old_configuration_state
        FROM tournament_content_configurations
        WHERE id = OLD.configuration_id
        FOR KEY SHARE;
    END IF;

    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        SELECT state
        INTO new_configuration_state
        FROM tournament_content_configurations
        WHERE id = NEW.configuration_id
        FOR KEY SHARE;
    END IF;

    IF TG_OP = 'UPDATE' AND NEW.configuration_id IS DISTINCT FROM OLD.configuration_id THEN
        RAISE EXCEPTION 'published tournament content cannot move to another configuration'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (
        TG_OP IN ('DELETE', 'UPDATE')
        AND old_configuration_state IS DISTINCT FROM 'draft'
    ) OR (
        TG_OP IN ('INSERT', 'UPDATE')
        AND new_configuration_state IS DISTINCT FROM 'draft'
    ) THEN
        RAISE EXCEPTION 'tournament content revisions change only while draft'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_category_membership_draft_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    new_configuration_state character varying(16);
    old_configuration_state character varying(16);
BEGIN
    IF TG_OP IN ('DELETE', 'UPDATE') THEN
        SELECT configuration.state
        INTO old_configuration_state
        FROM tournament_category_pool_revisions AS category_pool
        JOIN tournament_content_configurations AS configuration ON configuration.id = category_pool.configuration_id
        WHERE category_pool.id = OLD.category_pool_revision_id
        FOR KEY SHARE OF configuration;
    END IF;

    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        SELECT configuration.state
        INTO new_configuration_state
        FROM tournament_category_pool_revisions AS category_pool
        JOIN tournament_content_configurations AS configuration ON configuration.id = category_pool.configuration_id
        WHERE category_pool.id = NEW.category_pool_revision_id
        FOR KEY SHARE OF configuration;
    END IF;

    IF TG_OP = 'UPDATE' AND NEW.category_pool_revision_id IS DISTINCT FROM OLD.category_pool_revision_id THEN
        RAISE EXCEPTION 'published tournament category memberships cannot move'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (
        TG_OP IN ('DELETE', 'UPDATE')
        AND old_configuration_state IS DISTINCT FROM 'draft'
    ) OR (
        TG_OP IN ('INSERT', 'UPDATE')
        AND new_configuration_state IS DISTINCT FROM 'draft'
    ) THEN
        RAISE EXCEPTION 'tournament category memberships change only while draft'
            USING ERRCODE = 'check_violation';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_content_configuration_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_revision bigint;
    category_pool_count integer;
    category_count integer;
    stage_default_count integer;
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
        FROM tournament_content_configurations
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
        FROM task_pool_revisions AS pool
        WHERE pool.id = NEW.normal_pool_revision_id
            AND pool.publication_id = NEW.pool_publication_id
            AND pool.kind = 'normal'
    ) OR NOT EXISTS (
        SELECT 1
        FROM task_pool_revisions AS pool
        WHERE pool.id = NEW.golden_pool_revision_id
            AND pool.publication_id = NEW.pool_publication_id
            AND pool.kind = 'golden'
    ) THEN
        RAISE EXCEPTION 'tournament content pools must belong to one publication'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM task_pool_version_memberships AS membership
        WHERE membership.task_pool_revision_id = NEW.normal_pool_revision_id
    ) OR NOT EXISTS (
        SELECT 1
        FROM task_pool_version_memberships AS membership
        WHERE membership.task_pool_revision_id = NEW.golden_pool_revision_id
    ) THEN
        RAISE EXCEPTION 'tournament content pools require task versions'
            USING ERRCODE = 'check_violation';
    END IF;

    FOR locked_task IN
        SELECT task.id, task.enabled, task.deleted_at
        FROM task_pool_version_memberships AS membership
        JOIN tasks AS task ON task.id = membership.task_id
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
        FROM task_pool_version_memberships AS membership
        LEFT JOIN LATERAL (
            SELECT attestation.healthy
            FROM task_version_health_attestations AS attestation
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

    SELECT COUNT(*)
    INTO category_pool_count
    FROM tournament_category_pool_revisions
    WHERE configuration_id = NEW.id;

    IF category_pool_count <> 2
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_category_pool_revisions
            WHERE configuration_id = NEW.id
                AND format = 'bo1'
        )
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_category_pool_revisions
            WHERE configuration_id = NEW.id
                AND format = 'bo3'
        ) THEN
        RAISE EXCEPTION 'tournament content requires BO1 and BO3 category pools'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO category_count
    FROM tournament_category_pool_memberships AS membership
    JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = membership.category_pool_revision_id
    WHERE category_pool.configuration_id = NEW.id
        AND category_pool.format = 'bo1';

    IF category_count <> 3 THEN
        RAISE EXCEPTION 'BO1 category pool must contain three categories'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO category_count
    FROM tournament_category_pool_memberships AS membership
    JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = membership.category_pool_revision_id
    WHERE category_pool.configuration_id = NEW.id
        AND category_pool.format = 'bo3';

    IF category_count <> 5 THEN
        RAISE EXCEPTION 'BO3 category pool must contain five categories'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO stage_default_count
    FROM tournament_content_stage_defaults
    WHERE configuration_id = NEW.id;

    IF stage_default_count <> 4
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_content_stage_defaults AS stage_default
            JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = stage_default.category_pool_revision_id
            WHERE stage_default.configuration_id = NEW.id
                AND stage_default.stage = 'swiss'
                AND stage_default.format = 'bo1'
                AND stage_default.category_mode = 'random'
                AND stage_default.task_pool_kind = 'normal'
                AND category_pool.configuration_id = NEW.id
                AND category_pool.format = stage_default.format
        )
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_content_stage_defaults AS stage_default
            JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = stage_default.category_pool_revision_id
            WHERE stage_default.configuration_id = NEW.id
                AND stage_default.stage = 'golden'
                AND stage_default.format = 'bo1'
                AND stage_default.category_mode = 'random'
                AND stage_default.task_pool_kind = 'golden'
                AND category_pool.configuration_id = NEW.id
                AND category_pool.format = stage_default.format
        )
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_content_stage_defaults AS stage_default
            JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = stage_default.category_pool_revision_id
            WHERE stage_default.configuration_id = NEW.id
                AND stage_default.stage = 'semifinal'
                AND stage_default.format = 'bo1'
                AND stage_default.category_mode = 'draft'
                AND stage_default.task_pool_kind = 'normal'
                AND category_pool.configuration_id = NEW.id
                AND category_pool.format = stage_default.format
        )
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_content_stage_defaults AS stage_default
            JOIN tournament_category_pool_revisions AS category_pool ON category_pool.id = stage_default.category_pool_revision_id
            WHERE stage_default.configuration_id = NEW.id
                AND stage_default.stage = 'final'
                AND stage_default.format = 'bo3'
                AND stage_default.category_mode = 'draft'
                AND stage_default.task_pool_kind = 'normal'
                AND category_pool.configuration_id = NEW.id
                AND category_pool.format = stage_default.format
        ) THEN
        RAISE EXCEPTION 'tournament content stage defaults do not match the preset'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER task_versions_immutable_guard
    BEFORE DELETE OR UPDATE ON public.task_versions
    FOR EACH ROW EXECUTE FUNCTION public.immutable_content_evidence_guard();

CREATE TRIGGER task_version_health_attestations_immutable_guard
    BEFORE DELETE OR UPDATE ON public.task_version_health_attestations
    FOR EACH ROW EXECUTE FUNCTION public.task_version_health_attestation_guard();

CREATE TRIGGER task_pool_publications_immutable_guard
    BEFORE DELETE OR UPDATE ON public.task_pool_publications
    FOR EACH ROW EXECUTE FUNCTION public.immutable_content_evidence_guard();

CREATE TRIGGER task_pool_revisions_immutable_guard
    BEFORE DELETE OR UPDATE ON public.task_pool_revisions
    FOR EACH ROW EXECUTE FUNCTION public.immutable_content_evidence_guard();

CREATE TRIGGER task_pool_version_memberships_immutable_guard
    BEFORE DELETE OR UPDATE ON public.task_pool_version_memberships
    FOR EACH ROW EXECUTE FUNCTION public.immutable_content_evidence_guard();

CREATE TRIGGER task_head_version_guard
    BEFORE INSERT OR UPDATE ON public.tasks
    FOR EACH ROW EXECUTE FUNCTION public.task_head_version_guard();

CREATE TRIGGER task_head_append_version
    AFTER INSERT OR UPDATE ON public.tasks
    FOR EACH ROW EXECUTE FUNCTION public.append_task_version();

CREATE TRIGGER tournament_content_configurations_guard
    BEFORE DELETE OR INSERT OR UPDATE ON public.tournament_content_configurations
    FOR EACH ROW EXECUTE FUNCTION public.tournament_content_configuration_guard();

CREATE TRIGGER tournament_category_pool_revisions_draft_guard
    BEFORE DELETE OR INSERT OR UPDATE ON public.tournament_category_pool_revisions
    FOR EACH ROW EXECUTE FUNCTION public.tournament_content_draft_guard();

CREATE TRIGGER tournament_category_pool_memberships_draft_guard
    BEFORE DELETE OR INSERT OR UPDATE ON public.tournament_category_pool_memberships
    FOR EACH ROW EXECUTE FUNCTION public.tournament_category_membership_draft_guard();

CREATE TRIGGER tournament_content_stage_defaults_draft_guard
    BEFORE DELETE OR INSERT OR UPDATE ON public.tournament_content_stage_defaults
    FOR EACH ROW EXECUTE FUNCTION public.tournament_content_draft_guard();

INSERT INTO public.task_pool_publication_lock (singleton)
VALUES (true);

WITH publication AS (
    INSERT INTO public.task_pool_publications (revision)
    VALUES (1)
    RETURNING id, revision
)
INSERT INTO public.task_pool_revisions (publication_id, kind, revision)
SELECT publication.id, pool.kind, publication.revision
FROM publication
CROSS JOIN (VALUES ('normal'::character varying), ('golden'::character varying)) AS pool(kind);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS
    public.tournament_content_stage_defaults,
    public.tournament_category_pool_memberships,
    public.tournament_category_pool_revisions,
    public.tournament_content_configurations,
    public.task_pool_version_memberships,
    public.task_pool_revisions,
    public.task_pool_publications,
    public.task_pool_publication_lock,
    public.task_version_health_attestations,
    public.task_versions,
    public.tasks;

DROP FUNCTION IF EXISTS
    public.tournament_content_configuration_guard(),
    public.tournament_category_membership_draft_guard(),
    public.tournament_content_draft_guard(),
    public.append_task_version(),
    public.publish_task_pool_heads(),
    public.task_head_version_guard(),
    public.task_version_health_attestation_guard(),
    public.immutable_content_evidence_guard(),
    public.task_content_digest(
        character varying,
        text,
        character varying,
        character varying,
        integer,
        character varying,
        text,
        text,
        text,
        text,
        text
    );

-- +goose StatementEnd
