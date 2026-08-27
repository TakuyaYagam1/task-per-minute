-- +goose Up

-- Result revisions are durable and may already be used by synthetic writers.
-- Freeze every table involved in the commit seal while retained lineages are
-- checked and the insert guards are replaced atomically.
LOCK TABLE
    arena_game_attempts,
    arena_series,
    arena_official_result_revisions,
    arena_series_score_revisions,
    arena_result_commits
IN SHARE ROW EXCLUSIVE MODE;

-- A result commit must remain exhaustive after it is inserted. Older guards
-- checked the seal before taking the entity lock, so a concurrent revision
-- insert could pass the check, wait for the commit, and append stale evidence.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_result_commits AS result_commit
        WHERE (
            SELECT COUNT(*)
            FROM arena_official_result_revisions AS revision
            WHERE revision.result_event_id = result_commit.result_event_id
        ) <> 1 + CASE
            WHEN result_commit.series_result_revision_id IS NOT NULL THEN 1
            ELSE 0
        END
        OR (
            SELECT COUNT(*)
            FROM arena_series_score_revisions AS score_revision
            WHERE score_revision.result_event_id = result_commit.result_event_id
        ) <> 1
    ) THEN
        RAISE EXCEPTION 'Arena committed result has unsealed revision evidence'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Lock the owning Game or Series before checking the commit seal. The result
-- transaction already takes the same locks while creating its revisions, so a
-- waiter observes the committed seal after the lock is granted.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_official_result_revision_insert_guard()
RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    event_state VARCHAR(16);
    event_reason VARCHAR(40);
    event_winner UUID;
BEGIN
    IF NEW.entity_kind = 'game_attempt' THEN
        PERFORM 1
        FROM arena_game_attempts
        WHERE id = NEW.game_attempt_id
        FOR UPDATE;
    ELSE
        PERFORM 1
        FROM arena_series
        WHERE id = NEW.series_id
        FOR UPDATE;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM arena_result_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed Arena result event cannot gain another revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM arena_official_result_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena official-result revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Arena official-result lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT result_state, result_reason, winner_id
    INTO event_state, event_reason, event_winner
    FROM arena_result_events
    WHERE id = NEW.result_event_id;

    IF NEW.entity_kind = 'game_attempt'
        AND (
            NEW.result_state IS DISTINCT FROM event_state
            OR NEW.result_reason IS DISTINCT FROM event_reason
            OR NEW.winner_id IS DISTINCT FROM event_winner
        ) THEN
        RAISE EXCEPTION 'Arena Game result revision must retain server event evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_series_score_revision_insert_guard()
RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    series_format VARCHAR(8);
BEGIN
    SELECT format
    INTO series_format
    FROM arena_series
    WHERE id = NEW.series_id
    FOR UPDATE;

    IF NEW.result_event_id IS NOT NULL AND EXISTS (
        SELECT 1
        FROM arena_result_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed Arena result event cannot gain another score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM arena_series_score_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena Series-score revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Arena Series-score lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (series_format = 'bo1' AND (
        NEW.first_participant_wins > 1
        OR NEW.second_participant_wins > 1
    )) THEN
        RAISE EXCEPTION 'BO1 score revision exceeds one win'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- Downgrading reopens the stale-read race. Production recovery must preserve
-- the corrected lock order and use roll-forward or a verified restore.
LOCK TABLE
    arena_game_attempts,
    arena_series,
    arena_official_result_revisions,
    arena_series_score_revisions,
    arena_result_commits
IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_official_result_revision_insert_guard()
RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    event_state VARCHAR(16);
    event_reason VARCHAR(40);
    event_winner UUID;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_result_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed Arena result event cannot gain another revision'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.entity_kind = 'game_attempt' THEN
        PERFORM 1
        FROM arena_game_attempts
        WHERE id = NEW.game_attempt_id
        FOR UPDATE;
    ELSE
        PERFORM 1
        FROM arena_series
        WHERE id = NEW.series_id
        FOR UPDATE;
    END IF;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM arena_official_result_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena official-result revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Arena official-result lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT result_state, result_reason, winner_id
    INTO event_state, event_reason, event_winner
    FROM arena_result_events
    WHERE id = NEW.result_event_id;

    IF NEW.entity_kind = 'game_attempt'
        AND (
            NEW.result_state IS DISTINCT FROM event_state
            OR NEW.result_reason IS DISTINCT FROM event_reason
            OR NEW.winner_id IS DISTINCT FROM event_winner
        ) THEN
        RAISE EXCEPTION 'Arena Game result revision must retain server event evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION arena_series_score_revision_insert_guard()
RETURNS TRIGGER AS $$
DECLARE
    previous_number BIGINT;
    series_format VARCHAR(8);
BEGIN
    IF NEW.result_event_id IS NOT NULL AND EXISTS (
        SELECT 1
        FROM arena_result_commits
        WHERE result_event_id = NEW.result_event_id
    ) THEN
        RAISE EXCEPTION 'committed Arena result event cannot gain another score revision'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT format
    INTO series_format
    FROM arena_series
    WHERE id = NEW.series_id
    FOR UPDATE;

    IF NEW.previous_revision_id IS NOT NULL THEN
        SELECT revision_number
        INTO previous_number
        FROM arena_series_score_revisions
        WHERE id = NEW.previous_revision_id;

        IF previous_number IS NULL OR NEW.revision_number <> previous_number + 1 THEN
            RAISE EXCEPTION 'Arena Series-score revisions must be contiguous'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.revision_number <> 1 THEN
        RAISE EXCEPTION 'Arena Series-score lineage must start at revision one'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (series_format = 'bo1' AND (
        NEW.first_participant_wins > 1
        OR NEW.second_participant_wins > 1
    )) THEN
        RAISE EXCEPTION 'BO1 score revision exceeds one win'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
