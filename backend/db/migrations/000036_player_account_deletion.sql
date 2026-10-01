-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

CREATE TABLE player_session_tombstones (
    session_token_hash bytea PRIMARY KEY,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT player_session_tombstones_hash_length_check
        CHECK (octet_length(session_token_hash) = 32)
);

CREATE INDEX player_session_tombstones_expires_at_idx
    ON player_session_tombstones (expires_at);

-- Deleted players remain as historical records, but their account credentials
-- and username reservations must no longer block registration.
DELETE FROM player_username_reservations AS reservation
USING player_accounts AS account,
      players AS player
WHERE reservation.account_id = account.id
    AND account.player_id = player.id
    AND player.deleted_at IS NOT NULL;

DELETE FROM player_accounts AS account
USING players AS player
WHERE account.player_id = player.id
    AND player.deleted_at IS NOT NULL;

DELETE FROM player_leaderboard_overrides AS override
USING players AS player
WHERE override.player_id = player.id
    AND player.deleted_at IS NOT NULL;

-- Older soft-deletes decremented this count but left an empty reservation row.
DELETE FROM player_username_reservations
WHERE legacy_count = 0
    AND account_id IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $$
BEGIN
    RAISE EXCEPTION 'migration 000036 is irreversible: deleted player credentials and username reservations cannot be restored';
END;
$$;

-- +goose StatementEnd
