-- +goose Up

CREATE TABLE admin_player_audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_subject TEXT NOT NULL CHECK (btrim(actor_subject) <> ''),
    actor_jti TEXT NOT NULL CHECK (btrim(actor_jti) <> ''),
    action TEXT NOT NULL CHECK (action IN ('update', 'delete')),
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    before_state JSONB NOT NULL,
    after_state JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX admin_player_audit_events_player_created_idx
    ON admin_player_audit_events (player_id, created_at DESC);

CREATE INDEX admin_player_audit_events_created_idx
    ON admin_player_audit_events (created_at DESC);

-- +goose Down

DROP INDEX IF EXISTS admin_player_audit_events_created_idx;
DROP INDEX IF EXISTS admin_player_audit_events_player_created_idx;
DROP TABLE IF EXISTS admin_player_audit_events;
