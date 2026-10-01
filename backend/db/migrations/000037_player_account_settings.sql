-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE player_accounts
    ADD COLUMN pending_email varchar(254),
    ADD COLUMN pending_email_normalized varchar(254),
    ADD COLUMN email_change_code_hash text,
    ADD COLUMN email_change_expires_at timestamp with time zone,
    ADD COLUMN email_change_last_sent_at timestamp with time zone,
    ADD COLUMN email_change_send_window_started_at timestamp with time zone,
    ADD COLUMN email_change_send_count integer NOT NULL DEFAULT 0,
    ADD COLUMN email_change_attempt_window_started_at timestamp with time zone,
    ADD COLUMN email_change_attempt_count integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT player_accounts_email_change_state_check CHECK (
        (pending_email IS NULL
            AND pending_email_normalized IS NULL
            AND email_change_code_hash IS NULL
            AND email_change_expires_at IS NULL)
        OR
        (pending_email IS NOT NULL
            AND pending_email_normalized IS NOT NULL
            AND email_change_code_hash IS NOT NULL
            AND email_change_expires_at IS NOT NULL
            AND email_verified_at IS NOT NULL
            AND pending_email_normalized <> email_normalized
            AND lower(pending_email) = pending_email_normalized)
    ),
    ADD CONSTRAINT player_accounts_email_change_send_count_check
        CHECK (email_change_send_count BETWEEN 0 AND 5),
    ADD CONSTRAINT player_accounts_email_change_attempt_count_check
        CHECK (email_change_attempt_count BETWEEN 0 AND 5),
    ADD CONSTRAINT player_accounts_email_change_send_window_check CHECK (
        (email_change_send_window_started_at IS NULL AND email_change_send_count = 0)
        OR (email_change_send_window_started_at IS NOT NULL AND email_change_send_count BETWEEN 1 AND 5)
    ),
    ADD CONSTRAINT player_accounts_email_change_attempt_window_check CHECK (
        (email_change_attempt_window_started_at IS NULL AND email_change_attempt_count = 0)
        OR (email_change_attempt_window_started_at IS NOT NULL AND email_change_attempt_count BETWEEN 0 AND 5)
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM player_accounts
        WHERE pending_email IS NOT NULL
            OR email_change_last_sent_at IS NOT NULL
            OR email_change_send_count <> 0
            OR email_change_attempt_count <> 0
    ) THEN
        RAISE EXCEPTION 'cannot roll back player account settings after an email-change flow has started';
    END IF;
END;
$$;

ALTER TABLE player_accounts
    DROP CONSTRAINT player_accounts_email_change_attempt_window_check,
    DROP CONSTRAINT player_accounts_email_change_send_window_check,
    DROP CONSTRAINT player_accounts_email_change_attempt_count_check,
    DROP CONSTRAINT player_accounts_email_change_send_count_check,
    DROP CONSTRAINT player_accounts_email_change_state_check,
    DROP COLUMN email_change_attempt_count,
    DROP COLUMN email_change_attempt_window_started_at,
    DROP COLUMN email_change_send_count,
    DROP COLUMN email_change_send_window_started_at,
    DROP COLUMN email_change_last_sent_at,
    DROP COLUMN email_change_expires_at,
    DROP COLUMN email_change_code_hash,
    DROP COLUMN pending_email_normalized,
    DROP COLUMN pending_email;

-- +goose StatementEnd
