INSERT INTO players (
    id, username, session_token, status, created_at, deleted_at, session_expires_at
)
VALUES
    (
        '00000000-0000-4000-8000-000000000101',
        'migration_legacy_active_one',
        '00000000-0000-4000-8000-000000000201',
        'in_duel',
        '2025-01-15 10:00:00+00',
        NULL,
        '2025-01-16 10:00:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000102',
        'migration_legacy_active_two',
        '00000000-0000-4000-8000-000000000202',
        'in_duel',
        '2025-01-15 10:00:01+00',
        NULL,
        '2025-01-16 10:00:01+00'
    ),
    (
        '00000000-0000-4000-8000-000000000103',
        'migration_legacy_finished_one',
        NULL,
        'idle',
        '2025-01-15 08:00:00+00',
        NULL,
        NULL
    ),
    (
        '00000000-0000-4000-8000-000000000104',
        'migration_legacy_finished_two',
        NULL,
        'idle',
        '2025-01-15 08:00:01+00',
        NULL,
        NULL
    );

INSERT INTO tasks (
    id, title, description, category, difficulty, time_limit,
    flag, hint_1, hint_2, hint_3, task_url, source_file_url, created_at
)
VALUES
    (
        '00000000-0000-4000-8000-000000000301',
        'Legacy active task one',
        'Preserved active casual task',
        'web',
        'easy',
        300,
        'FLAG{legacy_active_one}',
        'active hint one',
        NULL,
        NULL,
        'https://tasks.example/legacy-active-one',
        NULL,
        '2025-01-15 09:30:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000302',
        'Legacy active task two',
        'Preserved unsolved active casual task',
        'crypto',
        'medium',
        420,
        'FLAG{legacy_active_two}',
        NULL,
        NULL,
        NULL,
        NULL,
        'https://files.example/legacy-active-two.zip',
        '2025-01-15 09:31:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000303',
        'Legacy finished task one',
        'Preserved solved finished casual task',
        'forensics',
        'hard',
        600,
        'FLAG{legacy_finished_one}',
        'finished hint one',
        'finished hint two',
        NULL,
        NULL,
        'https://files.example/legacy-finished-one.zip',
        '2025-01-15 07:30:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000304',
        'Legacy finished task two',
        'Preserved unsolved finished casual task',
        'reverse',
        'medium',
        480,
        'FLAG{legacy_finished_two}',
        NULL,
        NULL,
        NULL,
        NULL,
        NULL,
        '2025-01-15 07:31:00+00'
    );

INSERT INTO duels (
    id, player1_id, player2_id, status, winner_id,
    deadline, started_at, finished_at
)
VALUES
    (
        '00000000-0000-4000-8000-000000000401',
        '00000000-0000-4000-8000-000000000101',
        '00000000-0000-4000-8000-000000000102',
        'active',
        NULL,
        '2025-01-15 11:10:00+00',
        '2025-01-15 11:00:00+00',
        NULL
    ),
    (
        '00000000-0000-4000-8000-000000000402',
        '00000000-0000-4000-8000-000000000103',
        '00000000-0000-4000-8000-000000000104',
        'finished',
        '00000000-0000-4000-8000-000000000103',
        '2025-01-15 09:10:00+00',
        '2025-01-15 09:00:00+00',
        '2025-01-15 09:05:00+00'
    );

INSERT INTO duel_player_tasks (
    duel_id, player_id, task_id, solved, solved_at
)
VALUES
    (
        '00000000-0000-4000-8000-000000000401',
        '00000000-0000-4000-8000-000000000101',
        '00000000-0000-4000-8000-000000000301',
        TRUE,
        '2025-01-15 11:02:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000401',
        '00000000-0000-4000-8000-000000000102',
        '00000000-0000-4000-8000-000000000302',
        FALSE,
        NULL
    ),
    (
        '00000000-0000-4000-8000-000000000402',
        '00000000-0000-4000-8000-000000000103',
        '00000000-0000-4000-8000-000000000303',
        TRUE,
        '2025-01-15 09:04:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000402',
        '00000000-0000-4000-8000-000000000104',
        '00000000-0000-4000-8000-000000000304',
        FALSE,
        NULL
    );

INSERT INTO player_task_history (player_id, task_id, solved_at)
VALUES
    (
        '00000000-0000-4000-8000-000000000101',
        '00000000-0000-4000-8000-000000000301',
        '2025-01-15 11:02:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000103',
        '00000000-0000-4000-8000-000000000303',
        '2025-01-15 09:04:00+00'
    );

INSERT INTO player_leaderboard_overrides (
    player_id, wins, average_solve_time_ms, updated_at
)
VALUES
    (
        '00000000-0000-4000-8000-000000000101',
        2,
        120000,
        '2025-01-15 11:03:00+00'
    ),
    (
        '00000000-0000-4000-8000-000000000103',
        4,
        90000,
        '2025-01-15 09:06:00+00'
    );
