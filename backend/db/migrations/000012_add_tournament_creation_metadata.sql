-- +goose Up
-- +goose StatementBegin
ALTER TABLE tournaments
    ADD COLUMN name character varying(120),
    ADD COLUMN public_id character varying(64),
    ADD COLUMN planned_roster_size integer,
    ADD COLUMN content_revision bigint;

CREATE FUNCTION public.current_task_pool_publication_revision() RETURNS bigint
    LANGUAGE sql STABLE
    SET search_path = ''
    AS $$
        SELECT COALESCE(MAX(publication.revision), 1)
        FROM public.task_pool_publications AS publication
    $$;

UPDATE tournaments
SET name = 'Tournament',
    public_id = id::text,
    planned_roster_size = 4,
    content_revision = 1;

UPDATE tournaments AS tournament
SET content_revision = publication.revision
FROM tournament_content_configurations AS configuration
INNER JOIN task_pool_publications AS publication
    ON publication.id = configuration.pool_publication_id
WHERE configuration.tournament_id = tournament.id;

ALTER TABLE tournaments
    ALTER COLUMN name SET DEFAULT 'Tournament',
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN public_id SET DEFAULT gen_random_uuid()::text,
    ALTER COLUMN public_id SET NOT NULL,
    ALTER COLUMN planned_roster_size SET DEFAULT 4,
    ALTER COLUMN planned_roster_size SET NOT NULL,
    ALTER COLUMN content_revision SET DEFAULT public.current_task_pool_publication_revision(),
    ALTER COLUMN content_revision SET NOT NULL,
    ADD CONSTRAINT tournaments_name_check CHECK (
        name = btrim(name) AND name <> '' AND char_length(name) <= 120
    ),
    ADD CONSTRAINT tournaments_public_id_check CHECK (
        public_id ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(public_id) <= 64
    ),
    ADD CONSTRAINT tournaments_planned_roster_size_check CHECK (
        planned_roster_size BETWEEN 4 AND 16
    ),
    ADD CONSTRAINT tournaments_content_revision_check CHECK (content_revision >= 1),
    ADD CONSTRAINT tournaments_public_id_unique UNIQUE (public_id);

ALTER TABLE tournament_create_command_receipts
    DROP CONSTRAINT tournament_create_command_receipts_result_check,
    ADD CONSTRAINT tournament_create_command_receipts_result_check CHECK (
        result_preset = 'tournament_v1' AND
        result_state = 'draft' AND
        result_revision = 1 AND
        result_roster_size = 0 AND
        result_updated_at = result_created_at AND
        result_changed AND
        result_created_at <= created_at AND (
            result_schema_version = 1 AND
            result_document = jsonb_build_object(
                'schema_version', result_schema_version,
                'tournament_id', tournament_id,
                'roster_id', roster_id,
                'preset', result_preset,
                'state', result_state,
                'revision', result_revision,
                'roster_size', result_roster_size,
                'created_at', result_created_at,
                'updated_at', result_updated_at,
                'changed', result_changed
            ) OR
            result_schema_version = 2 AND
            jsonb_typeof(result_document) = 'object' AND
            result_document ?& ARRAY[
                'schema_version', 'tournament_id', 'roster_id', 'preset', 'state',
                'revision', 'roster_size', 'name', 'public_id', 'planned_roster_size',
                'content_revision', 'created_at', 'updated_at', 'changed'
            ] AND
            result_document - ARRAY[
                'schema_version', 'tournament_id', 'roster_id', 'preset', 'state',
                'revision', 'roster_size', 'name', 'public_id', 'planned_roster_size',
                'content_revision', 'created_at', 'updated_at', 'changed'
            ]::text[] = '{}'::jsonb AND
            result_document->>'schema_version' = '2' AND
            result_document->>'tournament_id' = tournament_id::text AND
            result_document->>'roster_id' = roster_id::text AND
            result_document->>'preset' = result_preset AND
            result_document->>'state' = result_state AND
            (result_document->>'revision')::bigint = result_revision AND
            (result_document->>'roster_size')::integer = result_roster_size AND
            result_document->>'name' = btrim(result_document->>'name') AND
            result_document->>'name' <> '' AND
            char_length(result_document->>'name') <= 120 AND
            result_document->>'public_id' ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND
            char_length(result_document->>'public_id') <= 64 AND
            (result_document->>'planned_roster_size')::integer BETWEEN 4 AND 16 AND
            (result_document->>'content_revision')::bigint >= 1 AND
            (result_document->>'created_at')::timestamptz = result_created_at AND
            (result_document->>'updated_at')::timestamptz = result_updated_at AND
            (result_document->>'changed')::boolean
        )
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM tournament_create_command_receipts
        WHERE result_schema_version = 2
    ) THEN
        RAISE EXCEPTION 'cannot remove tournament creation metadata while v2 receipts exist';
    END IF;
END;
$$;

ALTER TABLE tournament_create_command_receipts
    DROP CONSTRAINT tournament_create_command_receipts_result_check,
    ADD CONSTRAINT tournament_create_command_receipts_result_check CHECK (
        result_schema_version = 1 AND
        result_preset = 'tournament_v1' AND
        result_state = 'draft' AND
        result_revision = 1 AND
        result_roster_size = 0 AND
        result_updated_at = result_created_at AND
        result_changed AND
        result_created_at <= created_at AND
        result_document = jsonb_build_object(
            'schema_version', result_schema_version,
            'tournament_id', tournament_id,
            'roster_id', roster_id,
            'preset', result_preset,
            'state', result_state,
            'revision', result_revision,
            'roster_size', result_roster_size,
            'created_at', result_created_at,
            'updated_at', result_updated_at,
            'changed', result_changed
        )
    );

ALTER TABLE tournaments
    DROP CONSTRAINT tournaments_public_id_unique,
    DROP CONSTRAINT tournaments_content_revision_check,
    DROP CONSTRAINT tournaments_planned_roster_size_check,
    DROP CONSTRAINT tournaments_public_id_check,
    DROP CONSTRAINT tournaments_name_check,
    DROP COLUMN content_revision,
    DROP COLUMN planned_roster_size,
    DROP COLUMN public_id,
    DROP COLUMN name;

DROP FUNCTION public.current_task_pool_publication_revision();

-- +goose StatementEnd
