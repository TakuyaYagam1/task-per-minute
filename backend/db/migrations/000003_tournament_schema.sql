-- +goose Up
-- +goose StatementBegin

-- Initial tournament domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: tournament_cancellation_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.tournament_cancellation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'Tournament cancellations are append-only evidence'
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: tournament_lifecycle_command_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.tournament_lifecycle_command_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'Tournament lifecycle commands are append-only evidence'
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: tournament_roster_operation_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.tournament_roster_operation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'Tournament roster operations are append-only evidence'
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: participants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.participants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    roster_id uuid NOT NULL,
    player_id uuid NOT NULL,
    seed integer NOT NULL,
    attendance character varying(20) DEFAULT 'invited'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT participants_attendance_check CHECK (((attendance)::text = ANY ((ARRAY['invited'::character varying, 'registered'::character varying, 'checked_in'::character varying, 'withdrawn'::character varying])::text[]))),
    CONSTRAINT participants_seed_check CHECK ((seed >= 1)),
    CONSTRAINT participants_timestamps_check CHECK ((updated_at >= created_at))
);

--
-- Name: rosters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.rosters (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    locked_at timestamp with time zone,
    execution_started_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT rosters_lock_check CHECK (((execution_started_at IS NULL) OR (locked_at IS NOT NULL))),
    CONSTRAINT rosters_revision_check CHECK ((revision >= 1)),
    CONSTRAINT rosters_timestamps_check CHECK (((updated_at >= created_at) AND ((locked_at IS NULL) OR (locked_at >= created_at)) AND ((execution_started_at IS NULL) OR (execution_started_at >= locked_at))))
);

--
-- Name: tournament_create_command_receipts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tournament_create_command_receipts (
    command_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    content_configuration_id uuid NOT NULL,
    result_schema_version smallint DEFAULT 1 NOT NULL,
    result_preset character varying(32) NOT NULL,
    result_state character varying(32) NOT NULL,
    result_revision bigint NOT NULL,
    result_roster_size integer NOT NULL,
    result_created_at timestamp with time zone NOT NULL,
    result_updated_at timestamp with time zone NOT NULL,
    result_changed boolean NOT NULL,
    result_document jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_create_command_receipts_actor_check CHECK (actor_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT tournament_create_command_receipts_command_check CHECK (command_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT tournament_create_command_receipts_digest_check CHECK ((octet_length(request_digest) = 32 AND request_digest <> decode(repeat('00'::text, 32), 'hex'::text))),
    CONSTRAINT tournament_create_command_receipts_result_check CHECK ((result_schema_version = 1 AND result_preset = 'tournament_v1'::character varying AND result_state = 'draft'::character varying AND result_revision = 1 AND result_roster_size = 0 AND result_updated_at = result_created_at AND result_changed AND result_created_at <= created_at AND result_document = jsonb_build_object('schema_version', result_schema_version, 'tournament_id', tournament_id, 'roster_id', roster_id, 'preset', result_preset, 'state', result_state, 'revision', result_revision, 'roster_size', result_roster_size, 'created_at', result_created_at, 'updated_at', result_updated_at, 'changed', result_changed)))
);

--
-- Name: tournaments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tournaments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    preset character varying(32) DEFAULT 'tournament_v1'::character varying NOT NULL,
    state character varying(32) DEFAULT 'draft'::character varying NOT NULL,
    paused_from_state character varying(32),
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    CONSTRAINT tournaments_pause_origin_check CHECK (((((state)::text = 'technical_pause'::text) AND (paused_from_state IS NOT NULL) AND ((paused_from_state)::text = ANY ((ARRAY['swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying])::text[]))) OR (((state)::text <> 'technical_pause'::text) AND (paused_from_state IS NULL)))),
    CONSTRAINT tournaments_preset_check CHECK (((preset)::text = 'tournament_v1'::text)),
    CONSTRAINT tournaments_revision_check CHECK ((revision >= 1)),
    CONSTRAINT tournaments_state_check CHECK (((state)::text = ANY ((ARRAY['draft'::character varying, 'registration'::character varying, 'roster_locked'::character varying, 'swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying, 'completed'::character varying, 'cancelled'::character varying])::text[]))),
    CONSTRAINT tournaments_timestamps_check CHECK (((updated_at >= created_at) AND ((started_at IS NULL) OR (started_at >= created_at)) AND ((finished_at IS NULL) OR (finished_at >= created_at)) AND ((((state)::text = ANY ((ARRAY['draft'::character varying, 'registration'::character varying, 'roster_locked'::character varying])::text[])) AND (started_at IS NULL) AND (finished_at IS NULL)) OR (((state)::text = ANY ((ARRAY['swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying])::text[])) AND (started_at IS NOT NULL) AND (finished_at IS NULL)) OR (((state)::text = 'completed'::text) AND (started_at IS NOT NULL) AND (finished_at IS NOT NULL)) OR (((state)::text = 'cancelled'::text) AND (finished_at IS NOT NULL)))))
);

--
-- Name: participant_reservations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.participant_reservations (
    player_id uuid NOT NULL,
    reservation_id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    acquired_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT participant_reservations_revision_check CHECK ((revision >= 1)),
    CONSTRAINT participant_reservations_timestamps_check CHECK ((updated_at >= acquired_at))
);

--
-- Name: tournament_cancellations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tournament_cancellations (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    source_revision bigint NOT NULL,
    resulting_revision bigint NOT NULL,
    source_state character varying(32) NOT NULL,
    actor_id uuid NOT NULL,
    reason text NOT NULL,
    audit_event_id uuid NOT NULL,
    outbox_event_id uuid NOT NULL,
    cancelled_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_cancellations_evidence_check CHECK ((audit_event_id <> outbox_event_id)),
    CONSTRAINT tournament_cancellations_reason_check CHECK ((reason = btrim(reason) AND reason <> ''::text AND char_length(reason) <= 512)),
    CONSTRAINT tournament_cancellations_revision_check CHECK ((source_revision >= 1 AND resulting_revision = source_revision + 1)),
    CONSTRAINT tournament_cancellations_source_state_check CHECK (((source_state)::text = ANY ((ARRAY['draft'::character varying, 'registration'::character varying, 'roster_locked'::character varying, 'swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying])::text[]))),
    CONSTRAINT tournament_cancellations_timestamps_check CHECK ((cancelled_at <= created_at))
);

--
-- Name: tournament_lifecycle_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tournament_lifecycle_commands (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(32) NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    source_tournament_revision bigint NOT NULL,
    source_tournament_state character varying(32) NOT NULL,
    resulting_tournament_revision bigint NOT NULL,
    resulting_tournament_state character varying(32) NOT NULL,
    reason text,
    pause_id uuid,
    source_pause_command_id uuid,
    execution_snapshot jsonb,
    preset character varying(32) NOT NULL,
    roster_size integer NOT NULL,
    tournament_created_at timestamp with time zone NOT NULL,
    tournament_updated_at timestamp with time zone NOT NULL,
    tournament_started_at timestamp with time zone,
    tournament_finished_at timestamp with time zone,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_lifecycle_commands_action_check CHECK (((action)::text = ANY ((ARRAY['open_registration'::character varying, 'start_swiss'::character varying, 'start_golden'::character varying, 'start_playoffs'::character varying, 'correction_start_golden'::character varying, 'correction_start_playoffs'::character varying, 'correction_refresh_golden'::character varying, 'pause'::character varying, 'resume'::character varying, 'complete'::character varying, 'cancel'::character varying])::text[]))),
    CONSTRAINT tournament_lifecycle_commands_pause_check CHECK (((action = 'pause' AND pause_id IS NOT NULL AND source_pause_command_id IS NULL AND execution_snapshot IS NOT NULL AND jsonb_typeof(execution_snapshot) = 'object') OR (action = 'resume' AND pause_id IS NOT NULL AND source_pause_command_id IS NOT NULL AND execution_snapshot IS NOT NULL AND jsonb_typeof(execution_snapshot) = 'object') OR (action NOT IN ('pause', 'resume') AND pause_id IS NULL AND source_pause_command_id IS NULL AND execution_snapshot IS NULL))),
    CONSTRAINT tournament_lifecycle_commands_reason_check CHECK ((reason IS NULL OR (reason = btrim(reason) AND reason <> ''::text AND char_length(reason) <= 512))),
    CONSTRAINT tournament_lifecycle_commands_revision_check CHECK ((source_projection_revision >= 1 AND source_tournament_revision >= 1 AND resulting_tournament_revision = source_tournament_revision + 1)),
    CONSTRAINT tournament_lifecycle_commands_roster_size_check CHECK ((roster_size >= 0 AND roster_size <= 16)),
    CONSTRAINT tournament_lifecycle_commands_state_check CHECK (((source_tournament_state)::text = ANY ((ARRAY['draft'::character varying, 'registration'::character varying, 'roster_locked'::character varying, 'swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying])::text[])) AND ((resulting_tournament_state)::text = ANY ((ARRAY['registration'::character varying, 'swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying, 'completed'::character varying, 'cancelled'::character varying])::text[]))),
    CONSTRAINT tournament_lifecycle_commands_timestamps_check CHECK ((tournament_created_at <= tournament_updated_at AND executed_at = tournament_updated_at AND executed_at <= created_at AND (tournament_started_at IS NULL OR tournament_started_at BETWEEN tournament_created_at AND tournament_updated_at) AND (tournament_finished_at IS NULL OR tournament_finished_at BETWEEN tournament_created_at AND tournament_updated_at)))
);

--
-- Name: tournament_roster_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tournament_roster_operations (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(16) NOT NULL,
    preflight_revision_id uuid,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    source_tournament_revision bigint NOT NULL,
    source_tournament_state character varying(32) NOT NULL,
    resulting_tournament_revision bigint NOT NULL,
    resulting_tournament_state character varying(32) NOT NULL,
    source_roster_revision bigint NOT NULL,
    resulting_roster_revision bigint NOT NULL,
    request_digest bytea NOT NULL,
    checked_in_player_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    result_document jsonb NOT NULL,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_roster_operations_action_check CHECK (((action)::text = ANY ((ARRAY['replace'::character varying, 'preflight'::character varying, 'lock'::character varying, 'unlock'::character varying])::text[]))),
    CONSTRAINT tournament_roster_operations_digest_check CHECK ((octet_length(request_digest) = 32 AND request_digest <> decode(repeat('00'::text, 32), 'hex'::text))),
    CONSTRAINT tournament_roster_operations_result_check CHECK ((jsonb_typeof(result_document) = 'object'::text)),
    CONSTRAINT tournament_roster_operations_revision_check CHECK ((source_projection_revision >= 1 AND source_tournament_revision >= 1 AND source_roster_revision >= 1 AND (((action = 'preflight' OR action = 'replace') AND resulting_tournament_revision = source_tournament_revision) OR ((action = 'lock' OR action = 'unlock') AND resulting_tournament_revision = source_tournament_revision + 1)) AND ((action = 'preflight' AND resulting_roster_revision = source_roster_revision) OR (action <> 'preflight' AND resulting_roster_revision = source_roster_revision + 1)))),
    CONSTRAINT tournament_roster_operations_state_check CHECK (((action = 'lock' AND source_tournament_state = 'registration' AND resulting_tournament_state = 'roster_locked') OR (action = 'unlock' AND source_tournament_state = 'roster_locked' AND resulting_tournament_state = 'registration') OR ((action = 'replace' OR action = 'preflight') AND resulting_tournament_state = source_tournament_state))),
    CONSTRAINT tournament_roster_operations_evidence_check CHECK (((action = 'lock' AND preflight_revision_id IS NOT NULL AND cardinality(checked_in_player_ids) BETWEEN 4 AND 16) OR (action = 'preflight' AND preflight_revision_id IS NULL AND cardinality(checked_in_player_ids) BETWEEN 0 AND 16) OR (action IN ('replace', 'unlock') AND preflight_revision_id IS NULL AND cardinality(checked_in_player_ids) = 0))),
    CONSTRAINT tournament_roster_operations_timestamps_check CHECK ((executed_at <= created_at))
);

--
-- Name: participants participants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participants
    ADD CONSTRAINT participants_pkey PRIMARY KEY (id);

--
-- Name: participants participants_roster_player_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participants
    ADD CONSTRAINT participants_roster_player_key UNIQUE (roster_id, player_id);

--
-- Name: participants participants_roster_seed_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participants
    ADD CONSTRAINT participants_roster_seed_key UNIQUE (roster_id, seed);

--
-- Name: rosters rosters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rosters
    ADD CONSTRAINT rosters_pkey PRIMARY KEY (id);

--
-- Name: rosters rosters_tournament_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rosters
    ADD CONSTRAINT rosters_tournament_id_key UNIQUE (tournament_id);

--
-- Name: rosters rosters_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rosters
    ADD CONSTRAINT rosters_identity_key UNIQUE (id, tournament_id);

--
-- Name: tournament_create_command_receipts tournament_create_command_receipts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_create_command_receipts
    ADD CONSTRAINT tournament_create_command_receipts_pkey PRIMARY KEY (command_id);

--
-- Name: tournament_create_command_receipts tournament_create_command_receipts_tournament_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_create_command_receipts
    ADD CONSTRAINT tournament_create_command_receipts_tournament_key UNIQUE (tournament_id);

--
-- Name: tournaments tournaments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournaments
    ADD CONSTRAINT tournaments_pkey PRIMARY KEY (id);

--
-- Name: participant_reservations participant_reservations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_reservations
    ADD CONSTRAINT participant_reservations_pkey PRIMARY KEY (player_id);

--
-- Name: participant_reservations participant_reservations_reservation_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_reservations
    ADD CONSTRAINT participant_reservations_reservation_id_key UNIQUE (reservation_id);

--
-- Name: tournament_cancellations tournament_cancellations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_cancellations
    ADD CONSTRAINT tournament_cancellations_pkey PRIMARY KEY (command_id);

--
-- Name: tournament_cancellations tournament_cancellations_tournament_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_cancellations
    ADD CONSTRAINT tournament_cancellations_tournament_key UNIQUE (tournament_id);

--
-- Name: tournament_lifecycle_commands tournament_lifecycle_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_lifecycle_commands
    ADD CONSTRAINT tournament_lifecycle_commands_pkey PRIMARY KEY (command_id);

--
-- Name: tournament_lifecycle_commands tournament_lifecycle_commands_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_lifecycle_commands
    ADD CONSTRAINT tournament_lifecycle_commands_identity_key UNIQUE (command_id, tournament_id);

--
-- Name: tournament_roster_operations tournament_roster_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_roster_operations
    ADD CONSTRAINT tournament_roster_operations_pkey PRIMARY KEY (command_id);

--
-- Name: tournament_roster_operations tournament_roster_operations_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_roster_operations
    ADD CONSTRAINT tournament_roster_operations_identity_key UNIQUE (command_id, tournament_id);

--
-- Name: tournament_roster_operations tournament_roster_operations_preflight_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_roster_operations
    ADD CONSTRAINT tournament_roster_operations_preflight_fk FOREIGN KEY (preflight_revision_id, tournament_id) REFERENCES public.tournament_roster_operations(command_id, tournament_id) ON DELETE RESTRICT;

--
-- Name: participants_roster_id_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX participants_roster_id_id_idx ON public.participants USING btree (roster_id, id);

--
-- Name: rosters_id_tournament_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX rosters_id_tournament_id_idx ON public.rosters USING btree (id, tournament_id);

--
-- Name: tournaments_single_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX tournaments_single_active_idx ON public.tournaments USING btree ((1)) WHERE ((state)::text = ANY ((ARRAY['swiss'::character varying, 'golden'::character varying, 'playoffs'::character varying, 'technical_pause'::character varying])::text[]));

--
-- Name: tournaments_state_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tournaments_state_created_idx ON public.tournaments USING btree (state, created_at DESC);

--
-- Name: participant_reservations_tournament_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX participant_reservations_tournament_idx ON public.participant_reservations USING btree (tournament_id);

--
-- Name: tournament_lifecycle_commands_tournament_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tournament_lifecycle_commands_tournament_idx ON public.tournament_lifecycle_commands USING btree (tournament_id, created_at, command_id);

--
-- Name: tournament_roster_operations_tournament_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tournament_roster_operations_tournament_idx ON public.tournament_roster_operations USING btree (tournament_id, executed_at, command_id);

--
-- Name: tournament_cancellations tournament_cancellation_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER tournament_cancellation_guard BEFORE DELETE OR UPDATE ON public.tournament_cancellations FOR EACH ROW EXECUTE FUNCTION public.tournament_cancellation_guard();

--
-- Name: tournament_lifecycle_commands tournament_lifecycle_command_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER tournament_lifecycle_command_guard BEFORE DELETE OR UPDATE ON public.tournament_lifecycle_commands FOR EACH ROW EXECUTE FUNCTION public.tournament_lifecycle_command_guard();

--
-- Name: tournament_roster_operations tournament_roster_operation_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER tournament_roster_operation_guard BEFORE DELETE OR UPDATE ON public.tournament_roster_operations FOR EACH ROW EXECUTE FUNCTION public.tournament_roster_operation_guard();

--
-- Name: participants participants_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participants
    ADD CONSTRAINT participants_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id) ON DELETE RESTRICT;

--
-- Name: participants participants_roster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participants
    ADD CONSTRAINT participants_roster_id_fkey FOREIGN KEY (roster_id) REFERENCES public.rosters(id) ON DELETE RESTRICT;

--
-- Name: rosters rosters_tournament_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rosters
    ADD CONSTRAINT rosters_tournament_id_fkey FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

--
-- Name: tournament_content_configurations tournament_content_configurations_tournament_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_content_configurations
    ADD CONSTRAINT tournament_content_configurations_tournament_id_fkey FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

--
-- Name: tournament_content_configurations tournament_content_configurations_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_content_configurations
    ADD CONSTRAINT tournament_content_configurations_identity_key UNIQUE (id, tournament_id);

--
-- Name: tournament_create_command_receipts tournament_create_command_receipts_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_create_command_receipts
    ADD CONSTRAINT tournament_create_command_receipts_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: tournament_create_command_receipts tournament_create_command_receipts_tournament_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_create_command_receipts
    ADD CONSTRAINT tournament_create_command_receipts_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

--
-- Name: tournament_create_command_receipts tournament_create_command_receipts_content_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_create_command_receipts
    ADD CONSTRAINT tournament_create_command_receipts_content_fk FOREIGN KEY (content_configuration_id, tournament_id) REFERENCES public.tournament_content_configurations(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: participant_reservations participant_reservations_tournament_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_reservations
    ADD CONSTRAINT participant_reservations_tournament_id_fkey FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

--
-- Name: participant_reservations participant_reservations_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_reservations
    ADD CONSTRAINT participant_reservations_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id) ON DELETE CASCADE;

--
-- Name: tournament_cancellations tournament_cancellations_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_cancellations
    ADD CONSTRAINT tournament_cancellations_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: tournament_cancellations tournament_cancellations_tournament_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_cancellations
    ADD CONSTRAINT tournament_cancellations_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

--
-- Name: tournament_lifecycle_commands tournament_lifecycle_commands_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_lifecycle_commands
    ADD CONSTRAINT tournament_lifecycle_commands_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: tournament_lifecycle_commands tournament_lifecycle_commands_tournament_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_lifecycle_commands
    ADD CONSTRAINT tournament_lifecycle_commands_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

--
-- Name: tournament_lifecycle_commands tournament_lifecycle_commands_source_pause_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_lifecycle_commands
    ADD CONSTRAINT tournament_lifecycle_commands_source_pause_fk FOREIGN KEY (source_pause_command_id, tournament_id) REFERENCES public.tournament_lifecycle_commands(command_id, tournament_id) ON DELETE RESTRICT;

--
-- Name: tournament_roster_operations tournament_roster_operations_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_roster_operations
    ADD CONSTRAINT tournament_roster_operations_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: tournament_roster_operations tournament_roster_operations_tournament_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_roster_operations
    ADD CONSTRAINT tournament_roster_operations_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

-- Immutable stage progression evidence is created before a lifecycle command
-- and linked to it by a deferred foreign key in the same transaction.
CREATE FUNCTION public.tournament_create_command_receipt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'Tournament create command receipts are immutable'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER tournament_create_command_receipts_immutable
    BEFORE UPDATE OR DELETE ON public.tournament_create_command_receipts
    FOR EACH ROW EXECUTE FUNCTION public.tournament_create_command_receipt_guard();

CREATE FUNCTION public.tournament_stage_progression_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'Tournament stage progressions are append-only evidence'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE FUNCTION public.tournament_stage_tie_group_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_action TEXT;
    parent_projection_id UUID;
    parent_projection_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Tournament stage tie groups are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT action,
		CASE
			WHEN action IN ('correction_start_golden', 'correction_refresh_golden')
				THEN resulting_projection_revision_id
			ELSE source_projection_revision_id
		END,
		CASE
			WHEN action IN ('correction_start_golden', 'correction_refresh_golden')
				THEN resulting_projection_revision
			ELSE source_projection_revision
		END
    INTO parent_action, parent_projection_id, parent_projection_revision
    FROM tournament_stage_progressions
    WHERE command_id = NEW.command_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF parent_action NOT IN ('start_golden', 'correction_start_golden', 'correction_refresh_golden')
        OR parent_projection_id IS DISTINCT FROM NEW.source_projection_revision_id
        OR parent_projection_revision IS DISTINCT FROM NEW.source_projection_revision THEN
        RAISE EXCEPTION 'Golden tie evidence must match its stage progression source'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_tie_group_member_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    position_from SMALLINT;
    position_to SMALLINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Tournament stage tie group members are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT group_evidence.position_from, group_evidence.position_to
    INTO position_from, position_to
    FROM tournament_stage_tie_groups AS group_evidence
    WHERE group_evidence.command_id = NEW.command_id
        AND group_evidence.tournament_id = NEW.tournament_id
        AND group_evidence.roster_id = NEW.roster_id
        AND group_evidence.group_id = NEW.group_id
    FOR KEY SHARE;

    IF NEW.standing_position < position_from OR NEW.standing_position > position_to THEN
        RAISE EXCEPTION 'Tie group member position is outside its exact interval'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_tournament_stage_progression() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    group_count INTEGER;
    incomplete_group_count INTEGER;
BEGIN
    IF NEW.action NOT IN ('start_golden', 'correction_start_golden', 'correction_refresh_golden') THEN
        RETURN NEW;
    END IF;

    SELECT COUNT(*)
    INTO group_count
    FROM tournament_stage_tie_groups AS group_evidence
    WHERE group_evidence.command_id = NEW.command_id
        AND group_evidence.tournament_id = NEW.tournament_id
        AND group_evidence.roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO incomplete_group_count
    FROM tournament_stage_tie_groups AS group_evidence
    WHERE group_evidence.command_id = NEW.command_id
        AND group_evidence.tournament_id = NEW.tournament_id
        AND group_evidence.roster_id = NEW.roster_id
        AND (
            SELECT COUNT(*)
            FROM tournament_stage_tie_group_members AS member
            WHERE member.command_id = group_evidence.command_id
                AND member.tournament_id = group_evidence.tournament_id
                AND member.roster_id = group_evidence.roster_id
                AND member.group_id = group_evidence.group_id
        ) <> group_evidence.position_to - group_evidence.position_from + 1;

    IF group_count < 1 OR incomplete_group_count <> 0 THEN
        RAISE EXCEPTION 'Golden progression requires complete exact tie evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- correction_start_golden is sealed by its lifecycle command. Deferred child
-- checks distinguish the command written by this transaction from a command
-- that was already committed, preserving build order while closing the
-- aggregate against later appends.
CREATE FUNCTION public.validate_tournament_stage_golden_child_open() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    parent_action TEXT;
    parent_transaction xid;
BEGIN
    SELECT lifecycle.action, lifecycle.xmin
    INTO parent_action, parent_transaction
    FROM tournament_lifecycle_commands AS lifecycle
    WHERE lifecycle.command_id = NEW.command_id
        AND lifecycle.tournament_id = NEW.tournament_id
        AND lifecycle.roster_id = NEW.roster_id;

    IF parent_action = 'correction_start_golden'
        AND parent_transaction IS DISTINCT FROM pg_current_xact_id()::text::xid THEN
        RAISE EXCEPTION 'committed correction_start_golden rejects late tie evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TABLE public.tournament_stage_progressions (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(32) NOT NULL,
    source_tournament_revision bigint NOT NULL,
    source_tournament_state character varying(32) NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    resulting_projection_revision_id uuid,
    resulting_projection_revision bigint,
    resulting_tournament_revision bigint NOT NULL,
    resulting_tournament_state character varying(32) NOT NULL,
    proof jsonb NOT NULL,
    proof_digest bytea NOT NULL,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_stage_progressions_action_check CHECK (
        (action = 'start_golden'
            AND source_tournament_state = 'swiss'
            AND resulting_tournament_state = 'golden'
            AND resulting_projection_revision_id IS NULL
            AND resulting_projection_revision IS NULL)
        OR (action = 'correction_start_golden'
            AND source_tournament_state = 'playoffs'
            AND resulting_tournament_state = 'golden'
            AND resulting_projection_revision_id IS NOT NULL
            AND resulting_projection_revision IS NOT NULL)
        OR (action = 'start_playoffs'
            AND source_tournament_state IN ('swiss', 'golden')
            AND resulting_tournament_state = 'playoffs'
            AND resulting_projection_revision_id IS NOT NULL
            AND resulting_projection_revision IS NOT NULL)
        OR (action = 'correction_start_playoffs'
            AND source_tournament_state = 'golden'
            AND resulting_tournament_state = 'playoffs'
            AND resulting_projection_revision_id IS NOT NULL
            AND resulting_projection_revision IS NOT NULL)
        OR (action = 'correction_refresh_golden'
            AND source_tournament_state = 'golden'
            AND resulting_tournament_state = 'golden'
            AND resulting_projection_revision_id IS NOT NULL
            AND resulting_projection_revision IS NOT NULL)
    ),
    CONSTRAINT tournament_stage_progressions_proof_check CHECK (
        jsonb_typeof(proof) = 'object'
        AND proof <> '{}'::jsonb
        AND octet_length(proof_digest) = 32
        AND proof_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT tournament_stage_progressions_revision_check CHECK (
        source_tournament_revision >= 1
        AND source_projection_revision >= 1
        AND resulting_tournament_revision = source_tournament_revision + 1
        AND (
            (resulting_projection_revision_id IS NULL AND resulting_projection_revision IS NULL)
            OR resulting_projection_revision >= source_projection_revision
        )
    ),
    CONSTRAINT tournament_stage_progressions_timestamps_check CHECK (
        executed_at <= created_at
    )
);

CREATE TABLE public.tournament_stage_tie_groups (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    position_from smallint NOT NULL,
    position_to smallint NOT NULL,
    proof jsonb NOT NULL,
    proof_digest bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_stage_tie_groups_interval_check CHECK (
        position_from >= 1
        AND position_from <= 4
        AND position_to >= position_from
        AND position_to <= 16
    ),
    CONSTRAINT tournament_stage_tie_groups_proof_check CHECK (
        jsonb_typeof(proof) = 'object'
        AND proof <> '{}'::jsonb
        AND octet_length(proof_digest) = 32
        AND proof_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT tournament_stage_tie_groups_source_check CHECK (
        source_projection_revision >= 1
    )
);

CREATE TABLE public.tournament_stage_tie_group_members (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    standing_position smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tournament_stage_tie_group_members_position_check CHECK (
        standing_position >= 1
        AND standing_position <= 16
    )
);

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_pkey PRIMARY KEY (command_id, tournament_id);

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_command_key UNIQUE (command_id);

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_tournament_fk FOREIGN KEY (tournament_id) REFERENCES public.tournaments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_lifecycle_command_fk FOREIGN KEY (command_id, tournament_id) REFERENCES public.tournament_lifecycle_commands(command_id, tournament_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_tie_groups
    ADD CONSTRAINT tournament_stage_tie_groups_pkey PRIMARY KEY (command_id, tournament_id, group_id);

ALTER TABLE ONLY public.tournament_stage_tie_groups
    ADD CONSTRAINT tournament_stage_tie_groups_group_revision_key UNIQUE (command_id, tournament_id, group_revision_id);

ALTER TABLE ONLY public.tournament_stage_tie_groups
    ADD CONSTRAINT tournament_stage_tie_groups_scope_key UNIQUE (command_id, tournament_id, roster_id, group_id);

ALTER TABLE ONLY public.tournament_stage_tie_groups
    ADD CONSTRAINT tournament_stage_tie_groups_progression_fk FOREIGN KEY (command_id, tournament_id) REFERENCES public.tournament_stage_progressions(command_id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_stage_tie_groups
    ADD CONSTRAINT tournament_stage_tie_groups_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_stage_tie_group_members
    ADD CONSTRAINT tournament_stage_tie_group_members_pkey PRIMARY KEY (command_id, tournament_id, group_id, standing_position);

ALTER TABLE ONLY public.tournament_stage_tie_group_members
    ADD CONSTRAINT tournament_stage_tie_group_members_participant_key UNIQUE (command_id, tournament_id, group_id, participant_id);

ALTER TABLE ONLY public.tournament_stage_tie_group_members
    ADD CONSTRAINT tournament_stage_tie_group_members_group_fk FOREIGN KEY (command_id, tournament_id, roster_id, group_id) REFERENCES public.tournament_stage_tie_groups(command_id, tournament_id, roster_id, group_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_stage_tie_group_members
    ADD CONSTRAINT tournament_stage_tie_group_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

CREATE INDEX tournament_stage_progressions_tournament_idx
    ON public.tournament_stage_progressions USING btree (tournament_id, created_at, command_id);

CREATE TRIGGER tournament_stage_progressions_append_only
    BEFORE DELETE OR UPDATE ON public.tournament_stage_progressions
    FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_progression_guard();

CREATE TRIGGER tournament_stage_tie_groups_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_tie_groups
    FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_tie_group_guard();

CREATE TRIGGER tournament_stage_tie_group_members_append_only
    BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_tie_group_members
    FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_tie_group_member_guard();

CREATE CONSTRAINT TRIGGER tournament_stage_progression_completeness
    AFTER INSERT ON public.tournament_stage_progressions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_tournament_stage_progression();

CREATE CONSTRAINT TRIGGER tournament_stage_tie_groups_correction_closure
    AFTER INSERT ON public.tournament_stage_tie_groups
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_tournament_stage_golden_child_open();

CREATE CONSTRAINT TRIGGER tournament_stage_tie_group_members_correction_closure
    AFTER INSERT ON public.tournament_stage_tie_group_members
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_tournament_stage_golden_child_open();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public.tournament_create_command_receipts;

ALTER TABLE ONLY public.tournament_content_configurations
    DROP CONSTRAINT IF EXISTS tournament_content_configurations_identity_key;

ALTER TABLE ONLY public.tournament_content_configurations
    DROP CONSTRAINT IF EXISTS tournament_content_configurations_tournament_id_fkey;

DROP TABLE IF EXISTS
    public.tournament_stage_tie_group_members,
    public.tournament_stage_tie_groups,
    public.tournament_stage_progressions,
    public.tournament_roster_operations,
    public.tournament_lifecycle_commands,
    public.tournament_cancellations,
    public.participant_reservations,
    public.participants,
    public.rosters,
    public.tournaments;

DROP FUNCTION IF EXISTS
    public.validate_tournament_stage_golden_child_open(),
    public.validate_tournament_stage_progression(),
    public.tournament_stage_tie_group_member_guard(),
    public.tournament_stage_tie_group_guard(),
    public.tournament_stage_progression_guard(),
    public.tournament_create_command_receipt_guard(),
    public.tournament_roster_operation_guard(),
    public.tournament_lifecycle_command_guard(),
    public.tournament_cancellation_guard();

-- +goose StatementEnd
