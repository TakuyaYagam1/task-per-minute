-- +goose Up
-- +goose StatementBegin

-- Ready-window deadlines are otherwise immutable. This clock retains the
-- original deadline and permits exactly one guarded shift while its normal
-- Wave pause is active.
CREATE TABLE public.ready_window_pause_clocks (
    pause_id uuid NOT NULL,
    ready_window_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    original_deadline timestamp with time zone NOT NULL,
    frozen_at timestamp with time zone NOT NULL,
    frozen_remaining interval NOT NULL,
    resumed_at timestamp with time zone,
    resumed_deadline timestamp with time zone,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT ready_window_pause_clocks_pkey PRIMARY KEY (pause_id),
    CONSTRAINT ready_window_pause_clocks_pause_fk
        FOREIGN KEY (pause_id) REFERENCES public.pauses(id) ON DELETE RESTRICT,
    CONSTRAINT ready_window_pause_clocks_window_fk
        FOREIGN KEY (ready_window_id, wave_id, roster_id)
        REFERENCES public.ready_windows(id, wave_id, roster_id) ON DELETE RESTRICT,
    CONSTRAINT ready_window_pause_clocks_frozen_check CHECK (
        original_deadline > frozen_at
        AND frozen_remaining > INTERVAL '0 seconds'
        AND frozen_remaining = original_deadline - frozen_at
    ),
    CONSTRAINT ready_window_pause_clocks_resume_check CHECK (
        (resumed_at IS NULL AND resumed_deadline IS NULL)
        OR (
            resumed_at IS NOT NULL
            AND resumed_deadline = resumed_at + frozen_remaining
        )
    ),
    CONSTRAINT ready_window_pause_clocks_revision_check CHECK (revision >= 1),
    CONSTRAINT ready_window_pause_clocks_timestamps_check CHECK (
        created_at >= frozen_at
        AND updated_at >= created_at
        AND (resumed_at IS NULL OR resumed_at >= frozen_at)
    )
);

CREATE INDEX ready_window_pause_clocks_window_idx
ON public.ready_window_pause_clocks (ready_window_id, resumed_at DESC, pause_id DESC);

CREATE FUNCTION public.ready_window_pause_clock_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ready-window frozen clocks are durable CAS state'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.pause_id IS DISTINCT FROM OLD.pause_id
        OR NEW.ready_window_id IS DISTINCT FROM OLD.ready_window_id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.original_deadline IS DISTINCT FROM OLD.original_deadline
        OR NEW.frozen_at IS DISTINCT FROM OLD.frozen_at
        OR NEW.frozen_remaining IS DISTINCT FROM OLD.frozen_remaining
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.resumed_at IS NOT NULL
        OR NEW.resumed_at IS NULL
        OR NEW.resumed_deadline IS NULL
        OR NEW.revision <> OLD.revision + 1
        OR NEW.updated_at <= OLD.updated_at THEN
        RAISE EXCEPTION 'invalid ready-window frozen clock CAS transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER ready_window_pause_clock_guard
BEFORE DELETE OR UPDATE ON public.ready_window_pause_clocks
FOR EACH ROW EXECUTE FUNCTION public.ready_window_pause_clock_guard();

CREATE FUNCTION public.validate_ready_window_pause_clock() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    pause_row public.pauses%ROWTYPE;
    ready_window_row public.ready_windows%ROWTYPE;
BEGIN
    SELECT * INTO pause_row FROM public.pauses WHERE id = NEW.pause_id;
    SELECT * INTO ready_window_row FROM public.ready_windows WHERE id = NEW.ready_window_id;

    IF pause_row.scope_kind <> 'wave'
        OR pause_row.parent_pause_id IS NOT NULL
        OR pause_row.wave_id IS DISTINCT FROM NEW.wave_id
        OR pause_row.roster_id IS DISTINCT FROM NEW.roster_id
        OR pause_row.started_at IS DISTINCT FROM NEW.frozen_at
        OR ready_window_row.wave_id IS DISTINCT FROM NEW.wave_id
        OR ready_window_row.roster_id IS DISTINCT FROM NEW.roster_id
        OR (
            NEW.resumed_at IS NULL
            AND ready_window_row.deadline IS DISTINCT FROM NEW.original_deadline
        )
        OR (
            NEW.resumed_at IS NOT NULL
            AND ready_window_row.deadline IS DISTINCT FROM NEW.resumed_deadline
        ) THEN
        RAISE EXCEPTION 'ready-window pause clock does not match durable Wave evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER ready_window_pause_clock_consistency
AFTER INSERT OR UPDATE ON public.ready_window_pause_clocks
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_ready_window_pause_clock();

CREATE OR REPLACE FUNCTION public.ready_window_lifecycle_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    member_count integer;
    ready_count integer;
    supported_deadline_shift boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ready windows are retained execution history'
            USING ERRCODE = 'check_violation';
    END IF;

    supported_deadline_shift := NEW.deadline IS DISTINCT FROM OLD.deadline
        AND EXISTS (
            SELECT 1
            FROM public.ready_window_pause_clocks AS clock
            JOIN public.pauses AS pause ON pause.id = clock.pause_id
            WHERE clock.ready_window_id = NEW.id
                AND clock.wave_id = NEW.wave_id
                AND clock.roster_id = NEW.roster_id
                AND clock.original_deadline = OLD.deadline
                AND clock.resumed_deadline = NEW.deadline
                AND clock.resumed_at IS NOT NULL
                AND pause.state = 'active'
                AND pause.scope_kind = 'wave'
                AND pause.parent_pause_id IS NULL
        );

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR (NEW.deadline IS DISTINCT FROM OLD.deadline AND NOT supported_deadline_shift)
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'ready-window identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'open' AND NEW.state IN ('consumed', 'expired', 'superseded'))
            OR (OLD.state IN ('consumed', 'expired') AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid ready-window transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'consumed' AND OLD.state <> 'consumed' THEN
        SELECT COUNT(*) INTO member_count
        FROM public.wave_members
        WHERE wave_id = NEW.wave_id;

        SELECT COUNT(*) INTO ready_count
        FROM public.wave_readiness
        WHERE ready_window_id = NEW.id AND ready;

        IF member_count < 2 OR ready_count <> member_count THEN
            RAISE EXCEPTION 'all Wave members must be ready before start'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

ALTER TABLE public.wave_control_commands
    DROP CONSTRAINT wave_control_commands_revision_check;

ALTER TABLE public.wave_control_commands
    ADD CONSTRAINT wave_control_commands_revision_check CHECK (
        source_projection_revision >= 1
        AND source_tournament_revision >= 1
        AND source_roster_revision >= 1
        AND source_wave_revision >= 1
        AND (
            resulting_wave_revision = source_wave_revision + 1
            OR (
                action IN ('pause', 'resume')
                AND resulting_wave_revision = source_wave_revision
            )
        )
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE public.wave_control_commands
    DROP CONSTRAINT wave_control_commands_revision_check;

ALTER TABLE public.wave_control_commands
    ADD CONSTRAINT wave_control_commands_revision_check CHECK (
        source_projection_revision >= 1
        AND source_tournament_revision >= 1
        AND source_roster_revision >= 1
        AND source_wave_revision >= 1
        AND resulting_wave_revision = source_wave_revision + 1
    );

CREATE OR REPLACE FUNCTION public.ready_window_lifecycle_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    member_count integer;
    ready_count integer;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ready windows are retained execution history'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.wave_id IS DISTINCT FROM OLD.wave_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_id IS DISTINCT FROM OLD.revision_id
        OR NEW.opened_at IS DISTINCT FROM OLD.opened_at
        OR NEW.deadline IS DISTINCT FROM OLD.deadline
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'ready-window identity is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'open' AND NEW.state IN ('consumed', 'expired', 'superseded'))
            OR (OLD.state IN ('consumed', 'expired') AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid ready-window transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state = 'consumed' AND OLD.state <> 'consumed' THEN
        SELECT COUNT(*) INTO member_count
        FROM public.wave_members
        WHERE wave_id = NEW.wave_id;

        SELECT COUNT(*) INTO ready_count
        FROM public.wave_readiness
        WHERE ready_window_id = NEW.id AND ready;

        IF member_count < 2 OR ready_count <> member_count THEN
            RAISE EXCEPTION 'all Wave members must be ready before start'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER ready_window_pause_clock_consistency
ON public.ready_window_pause_clocks;
DROP FUNCTION public.validate_ready_window_pause_clock();
DROP TRIGGER ready_window_pause_clock_guard
ON public.ready_window_pause_clocks;
DROP FUNCTION public.ready_window_pause_clock_guard();
DROP TABLE public.ready_window_pause_clocks;

-- +goose StatementEnd
