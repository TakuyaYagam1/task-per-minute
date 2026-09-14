-- +goose Up
-- +goose StatementBegin

-- A participant lease records the execution authority which observed the
-- socket open.  Existing v21 rows are backfilled from append-only authority
-- history before ownerless writes are forbidden.
ALTER TABLE public.participant_connection_leases
    ADD COLUMN authority_holder_id uuid,
    ADD COLUMN authority_lease_id uuid,
    ADD COLUMN authority_epoch bigint;

-- The v21 guard intentionally permits only active-to-disconnected updates.
-- Suspend it for this one deterministic provenance backfill, then restore it
-- before any application traffic can observe the migrated schema.
DROP TRIGGER participant_connection_leases_guard ON public.participant_connection_leases;

WITH inferred_authority AS (
    SELECT lease.id,
        authority.holder_id,
        authority.lease_id,
        authority.epoch
    FROM public.participant_connection_leases AS lease
    JOIN LATERAL (
        SELECT evidence.holder_id,
            evidence.lease_id,
            evidence.epoch
        FROM public.execution_authority_leases AS evidence
        WHERE evidence.tournament_id = lease.tournament_id
            AND evidence.process_kind = 'authority'::character varying
            AND evidence.renewed_at <= lease.connected_at
            AND lease.connected_at < evidence.expires_at
        ORDER BY evidence.revision DESC
        LIMIT 1
    ) AS authority ON TRUE
)
UPDATE public.participant_connection_leases AS lease
SET authority_holder_id = inferred.holder_id,
    authority_lease_id = inferred.lease_id,
    authority_epoch = inferred.epoch
FROM inferred_authority AS inferred
WHERE inferred.id = lease.id;

CREATE TRIGGER participant_connection_leases_guard
    BEFORE INSERT OR DELETE OR UPDATE ON public.participant_connection_leases
    FOR EACH ROW EXECUTE FUNCTION public.participant_connection_lease_guard();

ALTER TABLE public.participant_connection_leases
    ADD CONSTRAINT participant_connection_leases_authority_shape_check CHECK (
        (authority_holder_id IS NULL AND authority_lease_id IS NULL AND authority_epoch IS NULL)
        OR (
            authority_holder_id IS NOT NULL
            AND authority_lease_id IS NOT NULL
            AND authority_epoch IS NOT NULL
            AND authority_epoch >= 1
        )
    ),
    ADD CONSTRAINT participant_connection_leases_authority_uuid_check CHECK (
        (authority_holder_id IS NULL OR authority_holder_id <> '00000000-0000-0000-0000-000000000000'::uuid)
        AND (authority_lease_id IS NULL OR authority_lease_id <> '00000000-0000-0000-0000-000000000000'::uuid)
    ),
    ADD CONSTRAINT participant_connection_leases_authority_required_check CHECK (
        authority_holder_id IS NOT NULL
        AND authority_lease_id IS NOT NULL
        AND authority_epoch IS NOT NULL
    );

CREATE INDEX participant_connection_leases_recovery_idx
    ON public.participant_connection_leases (
        tournament_id, authority_holder_id, authority_lease_id, authority_epoch,
        roster_id, participant_id, id
    )
    WHERE state = 'active'::character varying
        AND authority_holder_id IS NOT NULL;

-- Owner identity is immutable evidence.  New application writes stamp all
-- three fields; the trigger verifies the stamp against authority history.
CREATE FUNCTION public.participant_connection_lease_authority_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.authority_holder_id IS NULL OR NEW.authority_lease_id IS NULL OR NEW.authority_epoch IS NULL THEN
            RAISE EXCEPTION 'Participant connection lease authority stamp is required'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM public.execution_authority_leases AS authority
            WHERE authority.tournament_id = NEW.tournament_id
                AND authority.holder_id = NEW.authority_holder_id
                AND authority.lease_id = NEW.authority_lease_id
                AND authority.epoch = NEW.authority_epoch
                AND authority.process_kind = 'authority'::character varying
        ) THEN
            RAISE EXCEPTION 'Participant connection lease authority stamp has no execution authority evidence'
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.authority_holder_id IS DISTINCT FROM OLD.authority_holder_id
        OR NEW.authority_lease_id IS DISTINCT FROM OLD.authority_lease_id
        OR NEW.authority_epoch IS DISTINCT FROM OLD.authority_epoch THEN
        RAISE EXCEPTION 'Participant connection lease authority stamp is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER participant_connection_leases_authority_guard
    BEFORE INSERT OR UPDATE ON public.participant_connection_leases
    FOR EACH ROW EXECUTE FUNCTION public.participant_connection_lease_authority_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER participant_connection_leases_authority_guard ON public.participant_connection_leases;
DROP FUNCTION public.participant_connection_lease_authority_guard();
DROP INDEX public.participant_connection_leases_recovery_idx;
ALTER TABLE public.participant_connection_leases
    DROP CONSTRAINT participant_connection_leases_authority_required_check,
    DROP CONSTRAINT participant_connection_leases_authority_uuid_check,
    DROP CONSTRAINT participant_connection_leases_authority_shape_check,
    DROP COLUMN authority_holder_id,
    DROP COLUMN authority_lease_id,
    DROP COLUMN authority_epoch;

-- +goose StatementEnd
