-- +goose Up
-- Add one registration to an existing demo database. Goose applies this once;
-- existing tournaments, players and task publications remain unchanged.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

-- +goose StatementBegin
DO $practice$
DECLARE
    practice_tournament_id uuid := gen_random_uuid();
    practice_roster_id uuid := gen_random_uuid();
    practice_player_id uuid;
    selected_publication uuid;
    selected_revision bigint;
    normal_pool_id uuid;
    golden_pool_id uuid;
    configuration_id uuid := gen_random_uuid();
    bo1_pool_id uuid := gen_random_uuid();
    bo3_pool_id uuid := gen_random_uuid();
    cutoff_id uuid := gen_random_uuid();
    projection_id uuid := gen_random_uuid();
    artifact_id uuid;
    artifact_kind text;
    document jsonb;
    seat record;
BEGIN
    IF EXISTS (SELECT 1 FROM public.tournaments WHERE public_id = 'demo-practice') THEN
        RAISE EXCEPTION 'demo-practice already exists; resolve the name collision before applying this seed';
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.players
        WHERE username IN ('DemoAlex', 'DemoMaria', 'DemoNikita')
    ) THEN
        RAISE EXCEPTION 'demo practice player names are already in use; existing players will not be reused';
    END IF;

    -- Upload the intended tasks before applying this migration. A tournament
    -- keeps the immutable publication selected at creation, including its pools.
    SELECT id, revision INTO STRICT selected_publication, selected_revision
    FROM public.task_pool_publications ORDER BY revision DESC LIMIT 1;
    SELECT id INTO STRICT normal_pool_id FROM public.task_pool_revisions
    WHERE publication_id = selected_publication AND kind = 'normal';
    SELECT id INTO STRICT golden_pool_id FROM public.task_pool_revisions
    WHERE publication_id = selected_publication AND kind = 'golden';

    INSERT INTO public.tournaments (
        id, name, public_id, state, planned_roster_size, content_revision
    ) VALUES (
        practice_tournament_id, 'Демо: пробный турнир', 'demo-practice',
        'registration', 4, selected_revision
    );
    INSERT INTO public.rosters (id, tournament_id, revision)
    VALUES (practice_roster_id, practice_tournament_id, 4);

    -- These are registered participants, without sessions or automated actions.
    -- The fourth place is left for a player joining through the public page.
    FOR seat IN
        SELECT * FROM (VALUES (1, 'DemoAlex'), (2, 'DemoMaria'), (3, 'DemoNikita'))
            AS seats(position, username)
    LOOP
        practice_player_id := gen_random_uuid();
        INSERT INTO public.players (id, username) VALUES (practice_player_id, seat.username);
        INSERT INTO public.participants (roster_id, player_id, seed, attendance)
        VALUES (practice_roster_id, practice_player_id, seat.position, 'registered');
        INSERT INTO public.participant_reservations (player_id, tournament_id)
        VALUES (practice_player_id, practice_tournament_id);
    END LOOP;

    INSERT INTO public.tournament_content_configurations (
        id, tournament_id, revision, pool_publication_id, normal_pool_revision_id,
        golden_pool_revision_id, reserve_count
    ) VALUES (
        configuration_id, practice_tournament_id, 1, selected_publication,
        normal_pool_id, golden_pool_id, 2
    );
    INSERT INTO public.tournament_category_pool_revisions (id, configuration_id, format, revision)
    VALUES (bo1_pool_id, configuration_id, 'bo1', 1), (bo3_pool_id, configuration_id, 'bo3', 1);
    INSERT INTO public.tournament_category_pool_memberships (category_pool_revision_id, category)
    SELECT bo1_pool_id, category FROM unnest(ARRAY['crypto', 'reverse', 'web']) AS category;
    INSERT INTO public.tournament_category_pool_memberships (category_pool_revision_id, category)
    SELECT bo3_pool_id, category FROM unnest(ARRAY['crypto', 'forensics', 'pwn', 'reverse', 'web']) AS category;
    INSERT INTO public.tournament_content_stage_defaults (
        configuration_id, stage, format, category_mode, category_pool_revision_id, task_pool_kind, categories
    ) VALUES
        (configuration_id, 'swiss', 'bo1', 'random', bo1_pool_id, 'normal', '["crypto"]'),
        (configuration_id, 'golden', 'bo1', 'random', bo1_pool_id, 'golden', '["crypto"]'),
        (configuration_id, 'semifinal', 'bo1', 'draft', bo1_pool_id, 'normal', '["crypto","reverse","web"]'),
        (configuration_id, 'final', 'bo3', 'draft', bo3_pool_id, 'normal', '["crypto","forensics","pwn","reverse","web"]');
    UPDATE public.tournament_content_configurations
    SET state = 'published', published_at = now() WHERE id = configuration_id;

    -- Publish the initial empty standings and bracket so the public page opens
    -- through the normal projection API before any matches have been played.
    INSERT INTO public.projection_cutoffs (
        id, tournament_id, roster_id, sequence_number, source_kind, reason, cutoff_at
    ) VALUES (
        cutoff_id, practice_tournament_id, practice_roster_id, 1, 'initial', 'tournament_created', now()
    );
    INSERT INTO public.projection_revisions (
        id, tournament_id, roster_id, revision_number, cutoff_id
    ) VALUES (projection_id, practice_tournament_id, practice_roster_id, 1, cutoff_id);

    FOREACH artifact_kind IN ARRAY ARRAY['standings', 'bracket', 'top_four'] LOOP
        artifact_id := gen_random_uuid();
        document := CASE artifact_kind
            WHEN 'standings' THEN jsonb_build_object('entries', '[]'::jsonb, 'tie_groups', '[]'::jsonb)
            WHEN 'bracket' THEN jsonb_build_object('rounds', '[]'::jsonb)
            ELSE jsonb_build_object('participants', '[]'::jsonb)
        END;
        INSERT INTO public.projection_artifacts (
            id, tournament_id, roster_id, produced_by_revision_id, artifact_kind,
            artifact_key, payload, payload_digest
        ) VALUES (
            artifact_id, practice_tournament_id, practice_roster_id, projection_id, artifact_kind,
            artifact_kind, document::json, sha256(convert_to(document::text, 'UTF8'))
        );
        INSERT INTO public.projection_revision_artifacts (
            revision_id, tournament_id, roster_id, artifact_kind, artifact_id, change_kind
        ) VALUES (projection_id, practice_tournament_id, practice_roster_id, artifact_kind, artifact_id, 'produced');
    END LOOP;
    UPDATE public.projection_revisions SET state = 'published', published_at = now()
    WHERE id = projection_id;
END;
$practice$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $rollback$
BEGIN
    RAISE EXCEPTION 'demo seed is retained history; restore a pre-seed backup or recreate the disposable database';
END;
$rollback$;
-- +goose StatementEnd
