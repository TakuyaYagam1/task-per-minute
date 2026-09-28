-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DROP INDEX public.tournaments_single_active_idx;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.tournaments IN SHARE ROW EXCLUSIVE MODE;

DO $$
DECLARE
    active_count BIGINT;
BEGIN
    SELECT COUNT(*)
    INTO active_count
    FROM public.tournaments
    WHERE state IN ('swiss', 'golden', 'playoffs', 'technical_pause');

    IF active_count > 1 THEN
        RAISE EXCEPTION 'cannot restore tournaments_single_active_idx while multiple tournaments are active'
            USING ERRCODE = 'check_violation';
    END IF;
END;
$$;

CREATE UNIQUE INDEX tournaments_single_active_idx
    ON public.tournaments USING btree ((1))
    WHERE state IN ('swiss', 'golden', 'playoffs', 'technical_pause');

-- +goose StatementEnd
