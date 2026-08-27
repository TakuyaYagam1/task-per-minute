-- +goose Up

-- Result commits are durable and may already have multiple historical
-- revisions. Freeze every current-head surface while retained latest commits
-- are checked and the deferred current-head contract is installed atomically.
LOCK TABLE
    arena_game_attempts,
    arena_series,
    arena_official_result_revisions,
    arena_official_result_heads,
    arena_series_score_revisions,
    arena_series_score_heads,
    arena_result_commits
IN SHARE ROW EXCLUSIVE MODE;

-- Historical commits may legitimately point at superseded revisions. The
-- latest committed revision for each lineage must be the durable current head
-- and entity pointer, however, or the retained ledger already has an orphaned
-- commit that cannot be repaired safely by guessing intent.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM arena_result_commits AS result_commit
        JOIN arena_official_result_revisions AS revision
            ON revision.id = result_commit.game_result_revision_id
        JOIN arena_game_attempts AS attempt
            ON attempt.id = result_commit.attempt_id
        LEFT JOIN arena_official_result_heads AS head
            ON head.entity_kind = 'game_attempt'
            AND head.entity_id = result_commit.attempt_id
        WHERE NOT EXISTS (
            SELECT 1
            FROM arena_official_result_revisions AS later_revision
            WHERE later_revision.entity_kind = revision.entity_kind
                AND later_revision.entity_id = revision.entity_id
                AND later_revision.revision_number > revision.revision_number
        )
            AND (
                attempt.result_revision_id
                    IS DISTINCT FROM result_commit.game_result_revision_id
                OR head.current_revision_id
                    IS DISTINCT FROM result_commit.game_result_revision_id
            )
    ) OR EXISTS (
        SELECT 1
        FROM arena_result_commits AS result_commit
        JOIN arena_series_score_revisions AS revision
            ON revision.id = result_commit.series_score_revision_id
        JOIN arena_series AS series
            ON series.id = result_commit.series_id
        LEFT JOIN arena_series_score_heads AS head
            ON head.series_id = result_commit.series_id
        WHERE NOT EXISTS (
            SELECT 1
            FROM arena_series_score_revisions AS later_revision
            WHERE later_revision.series_id = revision.series_id
                AND later_revision.revision_number > revision.revision_number
        )
            AND (
                series.current_score_revision_id
                    IS DISTINCT FROM result_commit.series_score_revision_id
                OR head.current_revision_id
                    IS DISTINCT FROM result_commit.series_score_revision_id
                OR (
                    series.state IN ('completed', 'cancelled')
                    AND result_commit.series_result_revision_id IS NULL
                )
            )
    ) OR EXISTS (
        SELECT 1
        FROM arena_result_commits AS result_commit
        JOIN arena_official_result_revisions AS revision
            ON revision.id = result_commit.series_result_revision_id
        JOIN arena_series AS series
            ON series.id = result_commit.series_id
        LEFT JOIN arena_official_result_heads AS head
            ON head.entity_kind = 'series'
            AND head.entity_id = result_commit.series_id
        WHERE NOT EXISTS (
            SELECT 1
            FROM arena_official_result_revisions AS later_revision
            WHERE later_revision.entity_kind = revision.entity_kind
                AND later_revision.entity_id = revision.entity_id
                AND later_revision.revision_number > revision.revision_number
        )
            AND (
                series.current_result_revision_id
                    IS DISTINCT FROM result_commit.series_result_revision_id
                OR head.current_revision_id
                    IS DISTINCT FROM result_commit.series_result_revision_id
            )
    ) THEN
        RAISE EXCEPTION 'Arena result commit has orphaned current revision evidence'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;
-- +goose StatementEnd

-- A result transaction can insert evidence before it advances the mutable
-- heads, so validate at transaction end. Later correction commits may
-- supersede these revisions normally; each new commit must first become the
-- complete current view of its Game and Series.
-- +goose StatementBegin
CREATE FUNCTION arena_validate_result_commit_current_heads() RETURNS TRIGGER AS $$
DECLARE
    attempt_revision_id UUID;
    game_head_revision_id UUID;
    series_score_revision_id UUID;
    score_head_revision_id UUID;
    series_result_revision_id UUID;
    result_head_revision_id UUID;
BEGIN
    SELECT result_revision_id
    INTO attempt_revision_id
    FROM arena_game_attempts
    WHERE id = NEW.attempt_id;

    SELECT current_revision_id
    INTO game_head_revision_id
    FROM arena_official_result_heads
    WHERE entity_kind = 'game_attempt'
        AND entity_id = NEW.attempt_id;

    SELECT current_score_revision_id, current_result_revision_id
    INTO series_score_revision_id, series_result_revision_id
    FROM arena_series
    WHERE id = NEW.series_id;

    SELECT current_revision_id
    INTO score_head_revision_id
    FROM arena_series_score_heads
    WHERE series_id = NEW.series_id;

    SELECT current_revision_id
    INTO result_head_revision_id
    FROM arena_official_result_heads
    WHERE entity_kind = 'series'
        AND entity_id = NEW.series_id;

    IF attempt_revision_id IS DISTINCT FROM NEW.game_result_revision_id
        OR game_head_revision_id IS DISTINCT FROM NEW.game_result_revision_id
        OR series_score_revision_id IS DISTINCT FROM NEW.series_score_revision_id
        OR score_head_revision_id IS DISTINCT FROM NEW.series_score_revision_id
        OR series_result_revision_id
            IS DISTINCT FROM NEW.series_result_revision_id
        OR result_head_revision_id
            IS DISTINCT FROM NEW.series_result_revision_id THEN
        RAISE EXCEPTION 'Arena result commit revisions must be current heads'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER arena_result_commits_current_heads_consistency
AFTER INSERT ON arena_result_commits
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION arena_validate_result_commit_current_heads();

-- +goose Down

-- Downgrading reopens the orphan-commit gap. Production recovery must keep
-- the deferred current-head contract and use roll-forward or a verified
-- restore.
LOCK TABLE arena_result_commits IN SHARE ROW EXCLUSIVE MODE;

DROP TRIGGER IF EXISTS arena_result_commits_current_heads_consistency
    ON arena_result_commits;
DROP FUNCTION IF EXISTS arena_validate_result_commit_current_heads();
