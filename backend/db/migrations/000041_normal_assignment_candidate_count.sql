-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- Migration 27 made reserves configurable. The source evidence needs at least
-- one candidate; assignment-plan guards still enforce reserve_count + 1 edges.
ALTER TABLE public.exact_normal_assignment_sources
    DROP CONSTRAINT exact_normal_assignment_sources_check,
    ADD CONSTRAINT exact_normal_assignment_sources_check CHECK (
        tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_lock_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_revision_id = category_lock_id
        AND series_revision >= 1 AND pool_revision >= 1 AND history_revision >= 1
        AND roster_revision >= 1 AND artifact_revision >= 1 AND category_revision >= 1
        AND jsonb_typeof(pool) = 'object'
        AND jsonb_typeof(participant_ids) = 'array'
        AND jsonb_array_length(participant_ids) = 2
        AND jsonb_typeof(participant_reservations) = 'array'
        AND jsonb_array_length(participant_reservations) = 2
        AND jsonb_typeof(history) = 'array'
        AND jsonb_typeof(candidates) = 'array'
        AND jsonb_array_length(candidates) >= 1
        AND octet_length(graph_digest) = 32
        AND graph_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(artifact_digest) = 32
        AND artifact_digest <> decode(repeat('00', 32), 'hex')
        AND proof_hash = btrim(proof_hash) AND char_length(proof_hash) = 64
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- This validation deliberately refuses rollback when new evidence has fewer
-- than three candidates. Keep the migration and roll forward in that case;
-- never delete immutable assignment evidence to force a downgrade.
ALTER TABLE public.exact_normal_assignment_sources
    DROP CONSTRAINT exact_normal_assignment_sources_check,
    ADD CONSTRAINT exact_normal_assignment_sources_check CHECK (
        tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_lock_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_revision_id = category_lock_id
        AND series_revision >= 1 AND pool_revision >= 1 AND history_revision >= 1
        AND roster_revision >= 1 AND artifact_revision >= 1 AND category_revision >= 1
        AND jsonb_typeof(pool) = 'object'
        AND jsonb_typeof(participant_ids) = 'array'
        AND jsonb_array_length(participant_ids) = 2
        AND jsonb_typeof(participant_reservations) = 'array'
        AND jsonb_array_length(participant_reservations) = 2
        AND jsonb_typeof(history) = 'array'
        AND jsonb_typeof(candidates) = 'array'
        AND jsonb_array_length(candidates) >= 3
        AND octet_length(graph_digest) = 32
        AND graph_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(artifact_digest) = 32
        AND artifact_digest <> decode(repeat('00', 32), 'hex')
        AND proof_hash = btrim(proof_hash) AND char_length(proof_hash) = 64
    );
-- +goose StatementEnd
