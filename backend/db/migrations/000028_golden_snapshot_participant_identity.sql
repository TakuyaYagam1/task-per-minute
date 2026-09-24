-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

ALTER TABLE ONLY public.participants
    ADD CONSTRAINT participants_roster_participant_player_key
    UNIQUE (roster_id, id, player_id);

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    ADD CONSTRAINT golden_plan_snapshot_participant_reservation_identity_fk
    FOREIGN KEY (roster_id, participant_id, player_id)
    REFERENCES public.participants(roster_id, id, player_id)
    ON DELETE RESTRICT
    NOT VALID;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    VALIDATE CONSTRAINT golden_plan_snapshot_participant_reservation_identity_fk;

CREATE FUNCTION public.golden_snapshot_participant_reservation_live_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM 1
    FROM public.participant_reservations AS reservation
    WHERE reservation.player_id = NEW.player_id
        AND reservation.tournament_id = NEW.tournament_id
        AND reservation.reservation_id = NEW.reservation_id
        AND reservation.revision = NEW.revision
        AND reservation.acquired_at = NEW.acquired_at
        AND reservation.updated_at = NEW.updated_at
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'Golden snapshot participant reservation must match a live reservation'
            USING ERRCODE = 'foreign_key_violation',
                CONSTRAINT = 'golden_snapshot_participant_reservation_live_guard',
                TABLE = 'golden_exact_plan_snapshot_participant_reservations';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER golden_snapshot_participant_reservation_live_guard
    BEFORE INSERT ON public.golden_exact_plan_snapshot_participant_reservations
    FOR EACH ROW
    EXECUTE FUNCTION public.golden_snapshot_participant_reservation_live_guard();

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    DROP CONSTRAINT golden_plan_snapshot_participant_reservation_lock_fk;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.golden_exact_plan_snapshot_participant_reservations
    IN ACCESS EXCLUSIVE MODE;

LOCK TABLE public.participant_reservations
    IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.golden_exact_plan_snapshot_participant_reservations AS snapshot
        WHERE NOT EXISTS (
            SELECT 1
            FROM public.participant_reservations AS reservation
            WHERE reservation.player_id = snapshot.player_id
                AND reservation.tournament_id = snapshot.tournament_id
                AND reservation.reservation_id = snapshot.reservation_id
                AND reservation.revision = snapshot.revision
                AND reservation.acquired_at = snapshot.acquired_at
                AND reservation.updated_at = snapshot.updated_at
        )
    ) THEN
        RAISE EXCEPTION 'migration 000028 cannot roll back while Golden snapshot reservation history differs from live reservations; keep version 28 and use a forward migration';
    END IF;
END;
$$;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    ADD CONSTRAINT golden_plan_snapshot_participant_reservation_lock_fk
    FOREIGN KEY (player_id)
    REFERENCES public.participant_reservations(player_id)
    ON DELETE RESTRICT
    NOT VALID;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    VALIDATE CONSTRAINT golden_plan_snapshot_participant_reservation_lock_fk;

DROP TRIGGER golden_snapshot_participant_reservation_live_guard
    ON public.golden_exact_plan_snapshot_participant_reservations;

DROP FUNCTION public.golden_snapshot_participant_reservation_live_guard();

ALTER TABLE ONLY public.golden_exact_plan_snapshot_participant_reservations
    DROP CONSTRAINT golden_plan_snapshot_participant_reservation_identity_fk;

ALTER TABLE ONLY public.participants
    DROP CONSTRAINT participants_roster_participant_player_key;

-- +goose StatementEnd
