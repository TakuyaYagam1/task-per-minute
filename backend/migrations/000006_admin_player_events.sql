-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION notify_admin_players_changed()
RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify(
        'admin_players_changed',
        json_build_object('table', TG_TABLE_NAME, 'op', TG_OP)::text
    );
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER players_admin_players_changed
    AFTER INSERT OR UPDATE OR DELETE ON players
    FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_players_changed();

CREATE TRIGGER player_leaderboard_overrides_admin_players_changed
    AFTER INSERT OR UPDATE OR DELETE ON player_leaderboard_overrides
    FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_players_changed();

CREATE TRIGGER duels_admin_players_changed
    AFTER INSERT OR UPDATE OR DELETE ON duels
    FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_players_changed();

CREATE TRIGGER duel_player_tasks_admin_players_changed
    AFTER INSERT OR UPDATE OR DELETE ON duel_player_tasks
    FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_players_changed();

-- +goose Down

DROP TRIGGER IF EXISTS duel_player_tasks_admin_players_changed ON duel_player_tasks;
DROP TRIGGER IF EXISTS duels_admin_players_changed ON duels;
DROP TRIGGER IF EXISTS player_leaderboard_overrides_admin_players_changed ON player_leaderboard_overrides;
DROP TRIGGER IF EXISTS players_admin_players_changed ON players;
DROP FUNCTION IF EXISTS notify_admin_players_changed();
