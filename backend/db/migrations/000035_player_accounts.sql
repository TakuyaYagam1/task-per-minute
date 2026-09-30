-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

CREATE TABLE player_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id uuid UNIQUE REFERENCES players(id) ON DELETE RESTRICT,
    username varchar(50) NOT NULL,
    username_normalized varchar(50) NOT NULL UNIQUE,
    email varchar(254) NOT NULL,
    email_normalized varchar(254) NOT NULL UNIQUE,
    password_hash text NOT NULL,
    verification_token_hash bytea UNIQUE,
    verification_expires_at timestamp with time zone,
    verification_sent_at timestamp with time zone,
    email_verified_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT player_accounts_verification_state_check CHECK (
        (email_verified_at IS NULL AND player_id IS NULL
            AND verification_token_hash IS NOT NULL
            AND verification_expires_at IS NOT NULL
            AND verification_sent_at IS NOT NULL)
        OR
        (email_verified_at IS NOT NULL AND player_id IS NOT NULL
            AND verification_token_hash IS NULL
            AND verification_expires_at IS NULL
            AND verification_sent_at IS NULL)
    )
);

CREATE TABLE player_username_reservations (
    normalized_username varchar(50) PRIMARY KEY,
    legacy_count bigint NOT NULL DEFAULT 0 CHECK (legacy_count >= 0),
    account_id uuid UNIQUE REFERENCES player_accounts(id) ON DELETE RESTRICT,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

INSERT INTO player_username_reservations (normalized_username, legacy_count)
SELECT lower(username), count(*)
FROM players
GROUP BY lower(username);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM player_accounts) THEN
        RAISE EXCEPTION 'cannot roll back player accounts while account data exists';
    END IF;
END;
$$;

DROP TABLE player_username_reservations;
DROP TABLE player_accounts;

-- +goose StatementEnd
