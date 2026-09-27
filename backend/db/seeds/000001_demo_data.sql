-- +goose Up
-- This seed is deliberately separate from db/migrations. It is opt-in and
-- requires an empty application database. Goose wraps the whole file in one
-- transaction and records it in goose_seed_version, not goose_db_version.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';
LOCK TABLE public.players, public.tasks, public.tournaments IN SHARE ROW EXCLUSIVE MODE;
COMMENT ON TABLE public.goose_seed_version IS 'task-per-minute:demo-seed:v1';

-- +goose StatementBegin
DO $seed$
BEGIN
    IF EXISTS (SELECT 1 FROM public.tasks)
        OR EXISTS (SELECT 1 FROM public.tournaments)
        OR EXISTS (SELECT 1 FROM public.players) THEN
        RAISE EXCEPTION 'demo seed requires an empty application database; use a separate database';
    END IF;
END;
$seed$;
-- +goose StatementEnd

-- The catalog has enough distinct versions for four Swiss rounds, reserves,
-- replays, Golden and a BO3 final. All puzzles are local text exercises.
-- Categories are labels for demonstrating the catalog, not external services.
-- Each row publishes the current catalog after creating its task version.
-- Separate statements keep later tasks out of that publication until their
-- versions exist; a bulk INSERT makes all heads visible to the first trigger.
-- +goose StatementBegin
DO $tasks$
DECLARE
    puzzle record;
BEGIN
    FOR puzzle IN
        SELECT category.name, category.position, pool.kind, pool.offset_value, sample.number
        FROM (
            VALUES ('crypto', 1), ('reverse', 2), ('web', 3), ('forensics', 4), ('pwn', 5),
                ('steganography', 6), ('ppc', 7), ('osint', 8), ('mobile', 9), ('hardware', 10), ('misc', 11)
        ) AS category(name, position)
        CROSS JOIN (VALUES ('normal', 0), ('golden', 1000)) AS pool(kind, offset_value)
        CROSS JOIN LATERAL generate_series(
            1, CASE WHEN category.position > 5 THEN 2 WHEN pool.kind = 'golden' THEN 16 ELSE 40 END
        ) AS sample(number)
        ORDER BY category.position, pool.offset_value, sample.number
    LOOP
        INSERT INTO public.tasks (
            title, description, category, difficulty, time_limit, flag,
            hint_1, hint_2, hint_3, kind
        ) VALUES (
            format('Демо: %s / %s / %s', puzzle.name, puzzle.kind, puzzle.number),
            format(
                'Учебное задание для знакомства с интерфейсом. Вычислите %s + %s. '
                'Отправьте ответ в формате demo{число}. Внешние сервисы и файлы не нужны.',
                puzzle.number * 7, puzzle.position * 13 + puzzle.offset_value
            ),
            puzzle.name,
            (ARRAY['easy', 'medium', 'hard'])[1 + (puzzle.number - 1) % 3],
            (ARRAY[120, 180, 300])[1 + (puzzle.number - 1) % 3],
            format('demo{%s}', puzzle.number * 7 + puzzle.position * 13 + puzzle.offset_value),
            'Нужно сложить два числа из условия.',
            'Используйте десятичную запись без пробелов.',
            'Обрамите полученное число строкой demo{...}.',
            puzzle.kind
        );
    END LOOP;
END;
$tasks$;
-- +goose StatementEnd

-- Task triggers own task_versions and the publication history. Never insert a
-- second copy of a task version or turn off evidence guards in a seed.
INSERT INTO public.task_version_health_attestations (
    task_id, task_version, revision, healthy, source
)
SELECT id, current_version, 1, true, 'content_validation'
FROM public.tasks;

CREATE TEMP TABLE demo_tournaments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    roster_id uuid NOT NULL DEFAULT gen_random_uuid(),
    slug text NOT NULL,
    name text NOT NULL,
    state text NOT NULL,
    size integer NOT NULL,
    player_count integer NOT NULL,
    showcase boolean NOT NULL DEFAULT false
) ON COMMIT DROP;

INSERT INTO demo_tournaments (slug, name, state, size, player_count, showcase)
VALUES
    ('demo-draft', 'Демо: настройка турнира', 'draft', 8, 0, false),
    ('demo-registration', 'Демо: открытая регистрация', 'registration', 8, 4, false),
    ('demo-play', 'Демо: сыграть с друзьями', 'registration', 4, 0, false),
    ('demo-start', 'Демо: готов к запуску', 'roster_locked', 8, 8, false),
    ('demo-swiss', 'Демо: первый раунд Swiss', 'swiss', 8, 8, false),
    ('demo-results', 'Демо: результаты и сетка (витрина)', 'completed', 8, 8, true);

INSERT INTO public.tournaments (
    id, name, public_id, state, planned_roster_size, content_revision,
    created_at, updated_at, started_at, finished_at
)
SELECT
    id, name, slug, state, size,
    (SELECT max(revision) FROM public.task_pool_publications),
    now() - INTERVAL '2 days', now(),
    CASE WHEN showcase OR state = 'swiss' THEN now() - INTERVAL '1 day' END,
    CASE WHEN showcase THEN now() - INTERVAL '12 hours' END
FROM demo_tournaments;

INSERT INTO public.rosters (
    id, tournament_id, revision, created_at, updated_at, locked_at, execution_started_at
)
SELECT
    roster_id, id, player_count + 1, now() - INTERVAL '2 days', now(),
    CASE WHEN state IN ('roster_locked', 'swiss', 'completed') THEN now() - INTERVAL '1 day' END,
    CASE WHEN showcase OR state = 'swiss' THEN now() - INTERVAL '1 day' END
FROM demo_tournaments;

CREATE TEMP TABLE demo_players (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    participant_id uuid NOT NULL DEFAULT gen_random_uuid(),
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    seed integer NOT NULL,
    username text NOT NULL,
    attendance text NOT NULL,
    showcase boolean NOT NULL
) ON COMMIT DROP;

INSERT INTO demo_players (tournament_id, roster_id, seed, username, attendance, showcase)
SELECT
    tournament.id, tournament.roster_id, seat.number,
    format('%s-%s', tournament.slug, seat.number),
    CASE WHEN tournament.state = 'registration' THEN 'registered' ELSE 'checked_in' END,
    tournament.showcase
FROM demo_tournaments AS tournament
CROSS JOIN LATERAL generate_series(1, tournament.player_count) AS seat(number);

-- No sessions, passwords or admin identities are created by the seed.
INSERT INTO public.players (id, username, created_at)
SELECT id, username, now() - INTERVAL '2 days' FROM demo_players;

INSERT INTO public.participants (id, roster_id, player_id, seed, attendance, created_at, updated_at)
SELECT participant_id, roster_id, id, seed, attendance, now() - INTERVAL '2 days', now()
FROM demo_players;

INSERT INTO public.participant_reservations (player_id, tournament_id)
SELECT id, tournament_id FROM demo_players WHERE NOT showcase;

-- Global leaderboard overrides are explicitly synthetic. They are separate
-- from tournament standings and have matching administrative audit records.
INSERT INTO public.player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
SELECT id, 9 - seed, 15000 + seed * 3500 FROM demo_players WHERE showcase;

INSERT INTO public.admin_player_audit_events (
    actor_subject, actor_jti, action, player_id, before_state, after_state
)
SELECT
    'demo-seed', 'demo-seed', 'update', player.id,
    jsonb_build_object('username', player.username, 'wins', 0, 'average_solve_time_ms', 0),
    jsonb_build_object('username', player.username, 'wins', score.wins,
        'average_solve_time_ms', score.average_solve_time_ms)
FROM demo_players AS player
JOIN public.player_leaderboard_overrides AS score ON score.player_id = player.id;

-- Publish a complete configuration for each tournament. The same validated
-- immutable content publication is safe to share across different rosters.
-- +goose StatementBegin
DO $config$
DECLARE
    tournament record;
    selected_publication uuid;
    normal_pool_id uuid;
    golden_pool_id uuid;
    configuration_id uuid;
    bo1_pool_id uuid;
    bo3_pool_id uuid;
BEGIN
    SELECT id INTO STRICT selected_publication
    FROM public.task_pool_publications ORDER BY revision DESC LIMIT 1;
    SELECT id INTO STRICT normal_pool_id FROM public.task_pool_revisions
    WHERE task_pool_revisions.publication_id = selected_publication AND kind = 'normal';
    SELECT id INTO STRICT golden_pool_id FROM public.task_pool_revisions
    WHERE task_pool_revisions.publication_id = selected_publication AND kind = 'golden';

    FOR tournament IN SELECT id FROM demo_tournaments ORDER BY slug LOOP
        configuration_id := gen_random_uuid();
        bo1_pool_id := gen_random_uuid();
        bo3_pool_id := gen_random_uuid();
        INSERT INTO public.tournament_content_configurations (
            id, tournament_id, revision, pool_publication_id, normal_pool_revision_id,
            golden_pool_revision_id, reserve_count
        ) VALUES (configuration_id, tournament.id, 1, selected_publication, normal_pool_id, golden_pool_id, 2);

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
    END LOOP;
END;
$config$;
-- +goose StatementEnd

-- A manually prepared first round is intentionally left unlocked. The real
-- operator command owns wave creation, ready windows and execution authority.
CREATE TEMP TABLE demo_pairs ON COMMIT DROP AS
SELECT
    gen_random_uuid() AS id, first.roster_id, (first.seed + 1) / 2 AS slot,
    first.participant_id AS first_id, second.participant_id AS second_id
FROM demo_players AS first
JOIN demo_players AS second ON second.roster_id = first.roster_id AND second.seed = first.seed + 1
JOIN demo_tournaments AS tournament ON tournament.id = first.tournament_id
WHERE tournament.state = 'swiss' AND first.seed % 2 = 1;

INSERT INTO public.swiss_rounds (
    tournament_id, roster_id, round_number, source_roster_revision, source_history_revision,
    generation_kind, pairing_inputs, generated_at
)
SELECT
    roster.tournament_id, roster.id, 1, roster.revision, 0, 'manual',
    jsonb_agg(
        least(pair.first_id, pair.second_id)::text || ':' || greatest(pair.first_id, pair.second_id)::text
        ORDER BY least(pair.first_id, pair.second_id), greatest(pair.first_id, pair.second_id)
    ), now()
FROM demo_pairs AS pair
JOIN public.rosters AS roster ON roster.id = pair.roster_id
GROUP BY roster.id, roster.tournament_id, roster.revision;

INSERT INTO public.swiss_pairings (id, round_id, roster_id, slot_number)
SELECT pair.id AS pairing_id, round.id AS round_id, pair.roster_id, pair.slot
FROM demo_pairs AS pair
JOIN public.swiss_rounds AS round ON round.roster_id = pair.roster_id;

INSERT INTO public.swiss_pairing_members (pairing_id, round_id, roster_id, seat, participant_id)
SELECT pair.id AS pairing_id, round.id AS round_id, pair.roster_id, member.seat, member.participant_id
FROM demo_pairs AS pair
JOIN public.swiss_rounds AS round ON round.roster_id = pair.roster_id
CROSS JOIN LATERAL (VALUES (1, pair.first_id), (2, pair.second_id)) AS member(seat, participant_id);

-- Public pages require published initial projections even before the first
-- game. The result showcase gets a separate, explicitly synthetic snapshot.
-- It has no fabricated official result journal, sessions or execution leases.
-- +goose StatementBegin
DO $projections$
DECLARE
    tournament record;
    cutoff_id uuid;
    revision_id uuid;
    prior_cutoff uuid;
    prior_revision uuid;
    artifact_id uuid;
    artifact_kind text;
    document jsonb;
    participants uuid[];
    standings jsonb;
    bracket jsonb;
    snapshot_number integer;
BEGIN
    FOR tournament IN SELECT id, roster_id, showcase FROM demo_tournaments ORDER BY slug LOOP
        SELECT array_agg(participant_id ORDER BY seed) INTO participants
        FROM demo_players WHERE tournament_id = tournament.id;
        prior_cutoff := NULL;
        prior_revision := NULL;
        FOR snapshot_number IN 1..(CASE WHEN tournament.showcase THEN 2 ELSE 1 END) LOOP
            cutoff_id := gen_random_uuid();
            revision_id := gen_random_uuid();
            INSERT INTO public.projection_cutoffs (
                id, tournament_id, roster_id, sequence_number, previous_cutoff_id,
                source_kind, reason, cutoff_at
            ) VALUES (
                cutoff_id, tournament.id, tournament.roster_id, snapshot_number, prior_cutoff,
                CASE WHEN snapshot_number = 1 THEN 'initial' ELSE 'operator_rebuild' END,
                CASE WHEN snapshot_number = 1 THEN 'tournament_created' ELSE 'demo display snapshot; synthetic results' END,
                now()
            );
            INSERT INTO public.projection_revisions (
                id, tournament_id, roster_id, revision_number, previous_revision_id, cutoff_id
            ) VALUES (revision_id, tournament.id, tournament.roster_id, snapshot_number, prior_revision, cutoff_id);

            standings := '[]';
            bracket := '[]';
            IF snapshot_number = 2 THEN
                SELECT jsonb_agg(jsonb_build_object(
                    'participant_id', player.participant_id, 'position', player.seed,
                    'points', (9 - player.seed) / 2, 'wins', (9 - player.seed) / 2,
                    'losses', 4 - (9 - player.seed) / 2, 'bye_count', 0,
                    'buchholz', 12 - player.seed, 'effective_time', (30 + player.seed * 10)::bigint * 1000000000
                ) ORDER BY player.seed) INTO standings
                FROM demo_players AS player WHERE player.tournament_id = tournament.id;
                bracket := jsonb_build_array(
                    jsonb_build_object('stage', 'semifinal', 'position', 1, 'state', 'completed',
                        'first_participant_id', participants[1], 'second_participant_id', participants[4],
                        'first_wins', 1, 'second_wins', 0),
                    jsonb_build_object('stage', 'semifinal', 'position', 2, 'state', 'completed',
                        'first_participant_id', participants[2], 'second_participant_id', participants[3],
                        'first_wins', 0, 'second_wins', 1),
                    jsonb_build_object('stage', 'final', 'position', 1, 'state', 'completed',
                        'first_participant_id', participants[1], 'second_participant_id', participants[3],
                        'first_wins', 2, 'second_wins', 1)
                );
            END IF;
            FOREACH artifact_kind IN ARRAY ARRAY['standings', 'bracket', 'top_four'] LOOP
                artifact_id := gen_random_uuid();
                document := CASE artifact_kind
                    WHEN 'standings' THEN jsonb_build_object('entries', standings, 'tie_groups', '[]'::jsonb)
                    WHEN 'bracket' THEN jsonb_build_object('rounds', bracket)
                    ELSE jsonb_build_object('participants',
                        CASE WHEN snapshot_number = 2 THEN to_jsonb(participants[1:4]) ELSE '[]'::jsonb END)
                END;
                INSERT INTO public.projection_artifacts (
                    id, tournament_id, roster_id, produced_by_revision_id, artifact_kind,
                    artifact_key, payload, payload_digest
                ) VALUES (
                    artifact_id, tournament.id, tournament.roster_id, revision_id, artifact_kind,
                    artifact_kind, document::json, sha256(convert_to(document::text, 'UTF8'))
                );
                INSERT INTO public.projection_revision_artifacts (
                    revision_id, tournament_id, roster_id, artifact_kind, artifact_id, change_kind
                ) VALUES (revision_id, tournament.id, tournament.roster_id, artifact_kind, artifact_id, 'produced');
                IF snapshot_number = 2 THEN
                    INSERT INTO public.projection_artifact_members (
                        artifact_id, tournament_id, roster_id, artifact_kind, participant_id, position, score
                    ) SELECT
                        artifact_id, tournament.id, tournament.roster_id, artifact_kind,
                        player.participant_id, player.seed,
                        CASE WHEN artifact_kind = 'standings' THEN (9 - player.seed) / 2 END
                    FROM demo_players AS player
                    WHERE player.tournament_id = tournament.id
                        AND (artifact_kind = 'standings' OR player.seed <= 4);
                END IF;
            END LOOP;
            IF prior_revision IS NOT NULL THEN
                UPDATE public.projection_revisions
                SET state = 'superseded', superseded_by_revision_id = revision_id,
                    superseded_at = now(), supersession_reason = 'demo display snapshot'
                WHERE id = prior_revision;
            END IF;
            UPDATE public.projection_revisions SET state = 'published', published_at = now()
            WHERE id = revision_id;
            prior_cutoff := cutoff_id;
            prior_revision := revision_id;
        END LOOP;
    END LOOP;
END;
$projections$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $rollback$
BEGIN
    RAISE EXCEPTION 'demo seed is retained history; restore a pre-seed backup or recreate the disposable database';
END;
$rollback$;
-- +goose StatementEnd
