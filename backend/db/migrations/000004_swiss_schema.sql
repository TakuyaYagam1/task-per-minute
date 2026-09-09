-- +goose Up
-- +goose StatementBegin

-- Initial swiss domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: swiss_byes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.swiss_byes (
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    points_awarded smallint DEFAULT 1 NOT NULL,
    decision_evidence_id uuid NOT NULL,
    decision_algorithm_version character varying(64) NOT NULL,
    decision_inputs jsonb NOT NULL,
    decision_seed bytea NOT NULL,
    decision_result jsonb NOT NULL,
    decision_replay_digest bytea NOT NULL,
    decision_owner_id uuid NOT NULL,
    decided_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_byes_decision_check CHECK ((((decision_algorithm_version)::text = 'hmac-sha256-order-v1'::text) AND (jsonb_typeof(decision_inputs) = 'array'::text) AND (jsonb_array_length(decision_inputs) > 0) AND (octet_length(decision_seed) = 32) AND (decision_seed <> decode(repeat('00'::text, 32), 'hex'::text)) AND (jsonb_typeof(decision_result) = 'array'::text) AND (jsonb_array_length(decision_result) > 0) AND (octet_length(decision_replay_digest) = 32) AND (decision_owner_id = round_id))),
    CONSTRAINT swiss_byes_points_check CHECK ((points_awarded = 1)),
    CONSTRAINT swiss_byes_timestamps_check CHECK ((decided_at <= created_at))
);

--
-- Name: swiss_opponent_history; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.swiss_opponent_history (
    pairing_id uuid NOT NULL,
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    prior_meeting_count smallint NOT NULL,
    repeat_override_id uuid,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_opponent_history_repeat_check CHECK ((((prior_meeting_count = 0) AND (repeat_override_id IS NULL)) OR ((prior_meeting_count > 0) AND (repeat_override_id IS NOT NULL))))
);

--
-- Name: swiss_pairing_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.swiss_pairing_members (
    pairing_id uuid NOT NULL,
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    seat smallint NOT NULL,
    participant_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_pairing_members_seat_check CHECK ((seat = ANY (ARRAY[1, 2])))
);

--
-- Name: swiss_pairings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.swiss_pairings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    slot_number smallint NOT NULL,
    repeat_override_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_pairings_slot_check CHECK (((slot_number >= 1) AND (slot_number <= 8)))
);

--
-- Name: swiss_repeat_overrides; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.swiss_repeat_overrides (
    id uuid NOT NULL,
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    reason text NOT NULL,
    confirmed_at timestamp with time zone NOT NULL,
    roster_participant_ids jsonb NOT NULL,
    proposed_pairings jsonb NOT NULL,
    bye_participant_id uuid,
    previous_meetings jsonb NOT NULL,
    repeated_pairings jsonb NOT NULL,
    alternative_algorithm_version character varying(64) CONSTRAINT swiss_repeat_override_alternative_algorithm_vers_not_null NOT NULL,
    alternative_search_complete boolean CONSTRAINT swiss_repeat_override_alternative_search_complet_not_null NOT NULL,
    alternative_pairings jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_repeat_overrides_evidence_check CHECK (((jsonb_typeof(roster_participant_ids) = 'array'::text) AND (jsonb_array_length(roster_participant_ids) > 0) AND (jsonb_typeof(proposed_pairings) = 'array'::text) AND (jsonb_array_length(proposed_pairings) > 0) AND (jsonb_typeof(previous_meetings) = 'array'::text) AND (jsonb_typeof(repeated_pairings) = 'array'::text) AND (jsonb_array_length(repeated_pairings) > 0) AND ((alternative_algorithm_version)::text = 'complete-backtracking-v1'::text) AND alternative_search_complete AND (jsonb_typeof(alternative_pairings) = 'array'::text))),
    CONSTRAINT swiss_repeat_overrides_reason_check CHECK (((reason = btrim(reason)) AND (reason <> ''::text))),
    CONSTRAINT swiss_repeat_overrides_timestamps_check CHECK ((confirmed_at <= created_at))
);

--
-- Name: swiss_rounds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.swiss_rounds (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    roster_id uuid NOT NULL,
    round_number smallint NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    source_roster_revision bigint NOT NULL,
    source_history_revision bigint NOT NULL,
    generation_kind character varying(16) NOT NULL,
    pairing_inputs jsonb NOT NULL,
    decision_evidence_id uuid,
    decision_algorithm_version character varying(64),
    decision_seed bytea,
    decision_result jsonb,
    decision_replay_digest bytea,
    decision_owner_id uuid,
    generated_at timestamp with time zone NOT NULL,
    lock_revision bigint,
    locked_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_rounds_generation_evidence_check CHECK (((((generation_kind)::text = 'automatic'::text) AND (decision_evidence_id IS NOT NULL) AND ((decision_algorithm_version)::text = 'hmac-sha256-order-v1'::text) AND (decision_seed IS NOT NULL) AND (octet_length(decision_seed) = 32) AND (decision_seed <> decode(repeat('00'::text, 32), 'hex'::text)) AND (decision_result IS NOT NULL) AND (jsonb_typeof(decision_result) = 'array'::text) AND (jsonb_array_length(decision_result) > 0) AND (decision_replay_digest IS NOT NULL) AND (octet_length(decision_replay_digest) = 32) AND (decision_owner_id IS NOT NULL) AND (decision_owner_id = id)) OR (((generation_kind)::text = 'manual'::text) AND (decision_evidence_id IS NULL) AND (decision_algorithm_version IS NULL) AND (decision_seed IS NULL) AND (decision_result IS NULL) AND (decision_replay_digest IS NULL) AND (decision_owner_id IS NULL)))),
    CONSTRAINT swiss_rounds_generation_kind_check CHECK (((generation_kind)::text = ANY ((ARRAY['automatic'::character varying, 'manual'::character varying])::text[]))),
    CONSTRAINT swiss_rounds_lock_check CHECK ((((lock_revision IS NULL) AND (locked_at IS NULL)) OR ((lock_revision IS NOT NULL) AND (lock_revision = revision) AND (locked_at IS NOT NULL)))),
    CONSTRAINT swiss_rounds_number_check CHECK (((round_number >= 1) AND (round_number <= 4))),
    CONSTRAINT swiss_rounds_pairing_inputs_check CHECK (((jsonb_typeof(pairing_inputs) = 'array'::text) AND (jsonb_array_length(pairing_inputs) > 0))),
    CONSTRAINT swiss_rounds_revision_check CHECK (((revision >= 1) AND (source_roster_revision >= 1) AND (source_history_revision >= 0))),
    CONSTRAINT swiss_rounds_timestamps_check CHECK (((updated_at >= created_at) AND (generated_at <= created_at) AND ((locked_at IS NULL) OR (locked_at >= generated_at))))
);

-- Durable operator command ledger for one locked Swiss pairing decision.
CREATE TABLE public.swiss_pairing_commands (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    round_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    round_number smallint NOT NULL,
    pairing_mode character varying(16) NOT NULL,
    category_mode character varying(16) NOT NULL,
    categories jsonb NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    source_tournament_revision bigint NOT NULL,
    source_roster_revision bigint NOT NULL,
    source_history_revision bigint NOT NULL,
    request_digest bytea NOT NULL,
    result_document jsonb NOT NULL,
    executed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_pairing_commands_categories_check CHECK (
        jsonb_typeof(categories) = 'array'
        AND jsonb_array_length(categories) > 0
    ),
    CONSTRAINT swiss_pairing_commands_digest_check CHECK (octet_length(request_digest) = 32),
    CONSTRAINT swiss_pairing_commands_mode_check CHECK (
        pairing_mode IN ('automatic', 'manual')
        AND category_mode IN ('random', 'admin', 'draft')
    ),
    CONSTRAINT swiss_pairing_commands_result_check CHECK (
        jsonb_typeof(result_document) = 'object'
        AND result_document <> '{}'::jsonb
    ),
    CONSTRAINT swiss_pairing_commands_revision_check CHECK (
        round_number BETWEEN 1 AND 4
        AND source_projection_revision >= 1
        AND source_tournament_revision >= 1
        AND source_roster_revision >= 1
        AND source_history_revision >= 0
    ),
    CONSTRAINT swiss_pairing_commands_timestamps_check CHECK (executed_at <= created_at)
);

ALTER TABLE ONLY public.swiss_pairing_commands
    ADD CONSTRAINT swiss_pairing_commands_pkey PRIMARY KEY (command_id);

ALTER TABLE ONLY public.swiss_pairing_commands
    ADD CONSTRAINT swiss_pairing_commands_round_key UNIQUE (round_id);

ALTER TABLE ONLY public.swiss_pairing_commands
    ADD CONSTRAINT swiss_pairing_commands_roster_fk
    FOREIGN KEY (roster_id, tournament_id)
    REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: swiss_byes swiss_byes_decision_evidence_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_byes
    ADD CONSTRAINT swiss_byes_decision_evidence_id_key UNIQUE (decision_evidence_id);

--
-- Name: swiss_byes swiss_byes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_byes
    ADD CONSTRAINT swiss_byes_pkey PRIMARY KEY (round_id);

--
-- Name: swiss_byes swiss_byes_roster_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_byes
    ADD CONSTRAINT swiss_byes_roster_participant_key UNIQUE (roster_id, participant_id);

--
-- Name: swiss_opponent_history swiss_opponent_history_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_opponent_history
    ADD CONSTRAINT swiss_opponent_history_pkey PRIMARY KEY (pairing_id);

--
-- Name: swiss_pairing_members swiss_pairing_members_pair_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairing_members
    ADD CONSTRAINT swiss_pairing_members_pair_participant_key UNIQUE (pairing_id, participant_id);

--
-- Name: swiss_pairing_members swiss_pairing_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairing_members
    ADD CONSTRAINT swiss_pairing_members_pkey PRIMARY KEY (pairing_id, seat);

--
-- Name: swiss_pairing_members swiss_pairing_members_round_participant_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairing_members
    ADD CONSTRAINT swiss_pairing_members_round_participant_key UNIQUE (round_id, participant_id);

--
-- Name: swiss_pairings swiss_pairings_id_round_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairings
    ADD CONSTRAINT swiss_pairings_id_round_roster_key UNIQUE (id, round_id, roster_id);

--
-- Name: swiss_pairings swiss_pairings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairings
    ADD CONSTRAINT swiss_pairings_pkey PRIMARY KEY (id);

--
-- Name: swiss_pairings swiss_pairings_round_slot_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairings
    ADD CONSTRAINT swiss_pairings_round_slot_key UNIQUE (round_id, slot_number);

--
-- Name: swiss_repeat_overrides swiss_repeat_overrides_id_round_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_repeat_overrides
    ADD CONSTRAINT swiss_repeat_overrides_id_round_key UNIQUE (id, round_id);

--
-- Name: swiss_repeat_overrides swiss_repeat_overrides_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_repeat_overrides
    ADD CONSTRAINT swiss_repeat_overrides_pkey PRIMARY KEY (id);

--
-- Name: swiss_repeat_overrides swiss_repeat_overrides_round_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_repeat_overrides
    ADD CONSTRAINT swiss_repeat_overrides_round_key UNIQUE (round_id);

--
-- Name: swiss_rounds swiss_rounds_decision_evidence_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_rounds
    ADD CONSTRAINT swiss_rounds_decision_evidence_id_key UNIQUE (decision_evidence_id);

--
-- Name: swiss_rounds swiss_rounds_id_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_rounds
    ADD CONSTRAINT swiss_rounds_id_roster_key UNIQUE (id, roster_id);

ALTER TABLE ONLY public.swiss_pairing_commands
    ADD CONSTRAINT swiss_pairing_commands_round_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

--
-- Name: swiss_rounds swiss_rounds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_rounds
    ADD CONSTRAINT swiss_rounds_pkey PRIMARY KEY (id);

--
-- Name: swiss_rounds swiss_rounds_roster_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_rounds
    ADD CONSTRAINT swiss_rounds_roster_number_key UNIQUE (roster_id, round_number);

--
-- Name: swiss_opponent_history_roster_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX swiss_opponent_history_roster_idx ON public.swiss_opponent_history USING btree (roster_id, recorded_at, pairing_id);

--
-- Name: swiss_byes swiss_byes_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_byes
    ADD CONSTRAINT swiss_byes_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: swiss_byes swiss_byes_round_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_byes
    ADD CONSTRAINT swiss_byes_round_fk FOREIGN KEY (round_id, roster_id) REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

--
-- Name: swiss_opponent_history swiss_opponent_history_override_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_opponent_history
    ADD CONSTRAINT swiss_opponent_history_override_fk FOREIGN KEY (repeat_override_id, round_id) REFERENCES public.swiss_repeat_overrides(id, round_id) ON DELETE RESTRICT;

--
-- Name: swiss_opponent_history swiss_opponent_history_pairing_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_opponent_history
    ADD CONSTRAINT swiss_opponent_history_pairing_fk FOREIGN KEY (pairing_id, round_id, roster_id) REFERENCES public.swiss_pairings(id, round_id, roster_id) ON DELETE RESTRICT;

--
-- Name: swiss_pairing_members swiss_pairing_members_pairing_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairing_members
    ADD CONSTRAINT swiss_pairing_members_pairing_fk FOREIGN KEY (pairing_id, round_id, roster_id) REFERENCES public.swiss_pairings(id, round_id, roster_id) ON DELETE RESTRICT;

--
-- Name: swiss_pairing_members swiss_pairing_members_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairing_members
    ADD CONSTRAINT swiss_pairing_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: swiss_pairings swiss_pairings_override_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairings
    ADD CONSTRAINT swiss_pairings_override_fk FOREIGN KEY (repeat_override_id, round_id) REFERENCES public.swiss_repeat_overrides(id, round_id) ON DELETE RESTRICT;

--
-- Name: swiss_pairings swiss_pairings_round_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_pairings
    ADD CONSTRAINT swiss_pairings_round_fk FOREIGN KEY (round_id, roster_id) REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

--
-- Name: swiss_repeat_overrides swiss_repeat_overrides_bye_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_repeat_overrides
    ADD CONSTRAINT swiss_repeat_overrides_bye_fk FOREIGN KEY (roster_id, bye_participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: swiss_repeat_overrides swiss_repeat_overrides_round_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_repeat_overrides
    ADD CONSTRAINT swiss_repeat_overrides_round_fk FOREIGN KEY (round_id, roster_id) REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

--
-- Name: swiss_rounds swiss_rounds_roster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.swiss_rounds
    ADD CONSTRAINT swiss_rounds_roster_id_fkey FOREIGN KEY (roster_id) REFERENCES public.rosters(id) ON DELETE RESTRICT;

-- Immutable normalized authority for a locked Swiss round. The proof is kept
-- outside pairing JSON so later progression can revalidate the exact lock.
CREATE TABLE public.swiss_round_lock_proofs (
    round_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    preset character varying(32) NOT NULL,
    round_number smallint NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    preflight_revision_id uuid NOT NULL,
    normal_pool_revision_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    wave_revision_id uuid NOT NULL,
    round_revision bigint NOT NULL,
    source_projection_revision bigint NOT NULL,
    roster_revision bigint NOT NULL,
    history_revision bigint NOT NULL,
    normal_pool_revision bigint NOT NULL,
    wave_revision bigint NOT NULL,
    bye_participant_id uuid,
    proof_hash bytea NOT NULL,
    proof_mode character varying(24) DEFAULT 'wave_start' NOT NULL,
    terminal_command_id uuid,
    locked_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_round_lock_proofs_identity_check CHECK (
        round_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND source_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND preflight_revision_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND normal_pool_revision_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND wave_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND wave_revision_id <> '00000000-0000-0000-0000-000000000000'::UUID
    ),
    CONSTRAINT swiss_round_lock_proofs_preset_check CHECK (preset = 'tournament_v1'),
    CONSTRAINT swiss_round_lock_proofs_revisions_check CHECK (
        round_number BETWEEN 1 AND 4
        AND round_revision >= 1
        AND source_projection_revision >= 1
        AND roster_revision >= 1
        AND history_revision >= 0
        AND normal_pool_revision >= 1
        AND wave_revision >= 1
    ),
    CONSTRAINT swiss_round_lock_proofs_hash_check CHECK (
        octet_length(proof_hash) = 32
        AND proof_hash <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT swiss_round_lock_proofs_mode_check CHECK (
        (proof_mode = 'wave_start' AND terminal_command_id IS NULL)
        OR (proof_mode IN ('normal_no_show', 'pre_start_forfeit')
            AND terminal_command_id IS NOT NULL
            AND terminal_command_id <> '00000000-0000-0000-0000-000000000000'::uuid)
    ),
    CONSTRAINT swiss_round_lock_proofs_timestamps_check CHECK (locked_at <= created_at)
);

CREATE TABLE public.swiss_round_lock_proof_members (
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_round_lock_proof_members_pkey PRIMARY KEY (round_id, participant_id)
);

CREATE TABLE public.swiss_round_lock_proof_series (
    round_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    pairing_id uuid NOT NULL,
    series_id uuid NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    category_revision_id uuid NOT NULL,
    category_revision bigint NOT NULL,
    assignment_id uuid NOT NULL,
    assignment_revision bigint NOT NULL,
    assignment_plan_id uuid NOT NULL,
    assignment_plan_revision_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    reservation_revision bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT swiss_round_lock_proof_series_pkey PRIMARY KEY (round_id, pairing_id),
    CONSTRAINT swiss_round_lock_proof_series_identity_check CHECK (
        pairing_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND series_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND first_participant_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND second_participant_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND category_revision_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND assignment_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND assignment_plan_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND assignment_plan_revision_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND reservation_id <> '00000000-0000-0000-0000-000000000000'::UUID
        AND first_participant_id <> second_participant_id
    ),
    CONSTRAINT swiss_round_lock_proof_series_revision_check CHECK (
        category_revision >= 1
        AND assignment_revision >= 1
        AND reservation_revision >= 1
    ),
    CONSTRAINT swiss_round_lock_proof_series_series_key UNIQUE (round_id, series_id)
);

ALTER TABLE ONLY public.swiss_round_lock_proofs
    ADD CONSTRAINT swiss_round_lock_proofs_pkey PRIMARY KEY (round_id);

ALTER TABLE ONLY public.swiss_round_lock_proofs
    ADD CONSTRAINT swiss_round_lock_proofs_round_roster_key UNIQUE (round_id, roster_id);

ALTER TABLE ONLY public.swiss_round_lock_proofs
    ADD CONSTRAINT swiss_round_lock_proofs_round_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_rounds(id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proofs
    ADD CONSTRAINT swiss_round_lock_proofs_roster_fk
    FOREIGN KEY (roster_id, tournament_id)
    REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proofs
    ADD CONSTRAINT swiss_round_lock_proofs_bye_fk
    FOREIGN KEY (roster_id, bye_participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proof_members
    ADD CONSTRAINT swiss_round_lock_proof_members_proof_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_round_lock_proofs(round_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proof_members
    ADD CONSTRAINT swiss_round_lock_proof_members_participant_fk
    FOREIGN KEY (roster_id, participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proof_series
    ADD CONSTRAINT swiss_round_lock_proof_series_proof_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_round_lock_proofs(round_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proof_series
    ADD CONSTRAINT swiss_round_lock_proof_series_pairing_fk
    FOREIGN KEY (pairing_id, round_id, roster_id)
    REFERENCES public.swiss_pairings(id, round_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proof_series
    ADD CONSTRAINT swiss_round_lock_proof_series_first_participant_fk
    FOREIGN KEY (roster_id, first_participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.swiss_round_lock_proof_series
    ADD CONSTRAINT swiss_round_lock_proof_series_second_participant_fk
    FOREIGN KEY (roster_id, second_participant_id)
    REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

CREATE FUNCTION public.swiss_round_lock_proof_append_only() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'swiss round lock proof is immutable'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE FUNCTION public.validate_swiss_round_lock_proof() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    proof_round_id UUID;
    proof_tournament_id UUID;
    proof_roster_id UUID;
    proof_round_number SMALLINT;
    proof_round_revision BIGINT;
    proof_source_projection_revision_id UUID;
    proof_source_projection_revision BIGINT;
    proof_roster_revision BIGINT;
    proof_history_revision BIGINT;
    proof_preflight_revision_id UUID;
    proof_normal_pool_revision_id UUID;
    proof_normal_pool_revision BIGINT;
    proof_wave_id UUID;
    proof_wave_revision_id UUID;
    proof_wave_revision BIGINT;
    proof_locked_at TIMESTAMPTZ;
    proof_bye_participant_id UUID;
    proof_mode TEXT;
    proof_terminal_command_id UUID;
    terminal_authority_valid BOOLEAN;
    stored_round_number SMALLINT;
    stored_round_revision BIGINT;
    stored_lock_revision BIGINT;
    member_count INTEGER;
    series_count INTEGER;
    member_coverage_count INTEGER;
    pairing_mismatch_count INTEGER;
BEGIN
    proof_round_id := NEW.round_id;

    SELECT proof.tournament_id,
        proof.roster_id,
        proof.round_number,
        proof.round_revision,
        proof.source_projection_revision_id,
        proof.source_projection_revision,
        proof.roster_revision,
        proof.history_revision,
        proof.preflight_revision_id,
        proof.normal_pool_revision_id,
        proof.normal_pool_revision,
        proof.wave_id,
        proof.wave_revision_id,
        proof.wave_revision,
        proof.locked_at,
        proof.bye_participant_id,
        proof.proof_mode,
        proof.terminal_command_id
    INTO proof_tournament_id,
        proof_roster_id,
        proof_round_number,
        proof_round_revision,
        proof_source_projection_revision_id,
        proof_source_projection_revision,
        proof_roster_revision,
        proof_history_revision,
        proof_preflight_revision_id,
        proof_normal_pool_revision_id,
        proof_normal_pool_revision,
        proof_wave_id,
        proof_wave_revision_id,
        proof_wave_revision,
        proof_locked_at,
        proof_bye_participant_id,
        proof_mode,
        proof_terminal_command_id
    FROM swiss_round_lock_proofs AS proof
    WHERE proof.round_id = proof_round_id
    FOR KEY SHARE;

    IF proof_roster_id IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT swiss_round.round_number,
        swiss_round.revision,
        swiss_round.lock_revision
    INTO stored_round_number,
        stored_round_revision,
        stored_lock_revision
    FROM swiss_rounds AS swiss_round
    WHERE swiss_round.id = proof_round_id
        AND swiss_round.roster_id = proof_roster_id
    FOR KEY SHARE;

    IF stored_round_number IS NULL
        OR stored_round_number <> proof_round_number
        OR stored_round_revision <> proof_round_revision
        OR stored_lock_revision <> proof_round_revision THEN
        RAISE EXCEPTION 'Swiss round lock proof no longer matches locked round'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO member_count
    FROM swiss_round_lock_proof_members AS member
    WHERE member.round_id = proof_round_id
        AND member.roster_id = proof_roster_id;

    SELECT COUNT(*)
    INTO series_count
    FROM swiss_round_lock_proof_series AS proof_series
    WHERE proof_series.round_id = proof_round_id
        AND proof_series.roster_id = proof_roster_id;

    IF member_count < 4
        OR member_count > 16
        OR (proof_bye_participant_id IS NULL AND member_count % 2 <> 0)
        OR (proof_bye_participant_id IS NOT NULL AND member_count % 2 <> 1)
        OR series_count <> (member_count - CASE WHEN proof_bye_participant_id IS NULL THEN 0 ELSE 1 END) / 2 THEN
        RAISE EXCEPTION 'Swiss round lock proof has incomplete membership or series coverage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO member_coverage_count
    FROM (
        SELECT proof_series.first_participant_id AS participant_id
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
        UNION ALL
        SELECT proof_series.second_participant_id
        FROM swiss_round_lock_proof_series AS proof_series
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
        UNION ALL
        SELECT proof_bye_participant_id
        WHERE proof_bye_participant_id IS NOT NULL
    ) AS covered;

    IF member_coverage_count <> member_count
        OR EXISTS (
            SELECT 1
            FROM swiss_round_lock_proof_members AS member
            LEFT JOIN (
                SELECT proof_series.first_participant_id AS participant_id
                FROM swiss_round_lock_proof_series AS proof_series
                WHERE proof_series.round_id = proof_round_id
                    AND proof_series.roster_id = proof_roster_id
                UNION ALL
                SELECT proof_series.second_participant_id
                FROM swiss_round_lock_proof_series AS proof_series
                WHERE proof_series.round_id = proof_round_id
                    AND proof_series.roster_id = proof_roster_id
                UNION ALL
                SELECT proof_bye_participant_id
                WHERE proof_bye_participant_id IS NOT NULL
            ) AS covered ON covered.participant_id = member.participant_id
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
            GROUP BY member.participant_id
            HAVING COUNT(covered.participant_id) <> 1
        ) THEN
        RAISE EXCEPTION 'Swiss round lock proof membership does not exactly match series seats'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*)
    INTO pairing_mismatch_count
    FROM swiss_round_lock_proof_series AS proof_series
    LEFT JOIN swiss_pairing_members AS first_member
        ON first_member.pairing_id = proof_series.pairing_id
        AND first_member.round_id = proof_series.round_id
        AND first_member.roster_id = proof_series.roster_id
        AND first_member.seat = 1
    LEFT JOIN swiss_pairing_members AS second_member
        ON second_member.pairing_id = proof_series.pairing_id
        AND second_member.round_id = proof_series.round_id
        AND second_member.roster_id = proof_series.roster_id
        AND second_member.seat = 2
    WHERE proof_series.round_id = proof_round_id
        AND proof_series.roster_id = proof_roster_id
        AND (
            first_member.participant_id IS DISTINCT FROM proof_series.first_participant_id
            OR second_member.participant_id IS DISTINCT FROM proof_series.second_participant_id
        );

    IF pairing_mismatch_count <> 0 THEN
        RAISE EXCEPTION 'Swiss round lock proof pairing differs from persisted pairing'
            USING ERRCODE = 'check_violation';
    END IF;

    terminal_authority_valid := FALSE;
    IF proof_mode = 'pre_start_forfeit' THEN
        SELECT EXISTS (
            SELECT 1 FROM operator_forfeit_commits AS terminal
            JOIN projection_revisions AS publication ON publication.id = terminal.projection_evidence_id
                AND publication.tournament_id = proof_tournament_id AND publication.roster_id = proof_roster_id
                AND publication.previous_revision_id = proof_source_projection_revision_id
                AND publication.state IN ('published', 'superseded')
            JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id AND member.series_id = terminal.series_id
            WHERE terminal.command_id = proof_terminal_command_id
                AND terminal.tournament_id = proof_tournament_id AND terminal.roster_id = proof_roster_id
                AND terminal.source_projection_revision_id = proof_source_projection_revision_id
                AND terminal.source_projection_revision = proof_source_projection_revision
                AND terminal.resolved_at = proof_locked_at
        ) INTO terminal_authority_valid;
    ELSIF proof_mode = 'normal_no_show' THEN
        SELECT EXISTS (
            SELECT 1 FROM normal_no_show_commits AS terminal
            JOIN official_result_revisions AS result ON result.id = terminal.series_result_revision_id
                AND result.source_projection_revision_id = proof_source_projection_revision_id
                AND result.source_projection_revision = proof_source_projection_revision
            JOIN projection_revisions AS publication ON publication.id = terminal.projection_evidence_id
                AND publication.tournament_id = proof_tournament_id AND publication.roster_id = proof_roster_id
                AND publication.previous_revision_id = proof_source_projection_revision_id
                AND publication.state IN ('published', 'superseded')
            JOIN ready_windows AS ready_window ON ready_window.id = terminal.ready_window_id
                AND ready_window.wave_id = proof_wave_id AND ready_window.revision_id = terminal.ready_window_revision_id
                AND ready_window.state = 'expired'
            JOIN swiss_round_lock_proof_series AS member ON member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id AND member.series_id = terminal.series_id
            WHERE terminal.command_id = proof_terminal_command_id
                AND terminal.tournament_id = proof_tournament_id AND terminal.roster_id = proof_roster_id
                AND terminal.wave_id = proof_wave_id AND terminal.expected_wave_revision = proof_wave_revision
                AND terminal.resolved_at = proof_locked_at
        ) INTO terminal_authority_valid;
    END IF;

    -- All sources below are intentionally normalized tables. The proof may
    -- not stand in for a stale projection, preflight, pool, Wave, category,
    -- assignment plan, or reservation. These tables are introduced by later
    -- fresh-schema migrations, so this deferred trigger is the integrity
    -- boundary rather than forward foreign keys in migration 000004.
    IF NOT EXISTS (
        SELECT 1
        FROM rosters AS roster
        JOIN tournaments AS tournament ON tournament.id = roster.tournament_id
        JOIN swiss_wave_links AS swiss_link
            ON swiss_link.round_id = proof_round_id
            AND swiss_link.roster_id = proof_roster_id
            AND swiss_link.tournament_id = proof_tournament_id
            AND swiss_link.wave_id = proof_wave_id
        JOIN waves AS wave
            ON wave.id = swiss_link.wave_id
            AND wave.tournament_id = proof_tournament_id
            AND wave.roster_id = proof_roster_id
        JOIN projection_revisions AS source_projection
            ON source_projection.id = proof_source_projection_revision_id
            AND source_projection.tournament_id = proof_tournament_id
            AND source_projection.roster_id = proof_roster_id
            AND source_projection.revision_number = proof_source_projection_revision
            AND (source_projection.state = 'published'
                OR (terminal_authority_valid AND source_projection.state = 'superseded'))
        JOIN tournament_roster_operations AS lock_operation
            ON lock_operation.tournament_id = proof_tournament_id
            AND lock_operation.roster_id = proof_roster_id
            AND lock_operation.action = 'lock'
            AND lock_operation.preflight_revision_id = proof_preflight_revision_id
            AND lock_operation.resulting_roster_revision = proof_roster_revision
        JOIN tournament_roster_operations AS preflight_operation
            ON preflight_operation.command_id = proof_preflight_revision_id
            AND preflight_operation.tournament_id = proof_tournament_id
            AND preflight_operation.roster_id = proof_roster_id
            AND preflight_operation.action = 'preflight'
        JOIN LATERAL (
            SELECT configuration.normal_pool_revision_id,
                normal_pool.revision AS normal_pool_revision
            FROM tournament_content_configurations AS configuration
            JOIN task_pool_revisions AS normal_pool
                ON normal_pool.id = configuration.normal_pool_revision_id
                AND normal_pool.kind = 'normal'
            WHERE configuration.tournament_id = proof_tournament_id
                AND configuration.state = 'published'
            ORDER BY configuration.revision DESC, configuration.id DESC
            LIMIT 1
        ) AS current_content ON TRUE
        WHERE roster.id = proof_roster_id
            AND roster.revision = proof_roster_revision
            AND tournament.id = proof_tournament_id
            AND wave.revision_id = proof_wave_revision_id
            AND (
                (proof_mode = 'wave_start' AND wave.revision = proof_wave_revision + 1
                    AND wave.state = 'active' AND wave.started_at = proof_locked_at)
                OR (terminal_authority_valid AND wave.started_at IS NULL AND wave.paused_at IS NULL
                    AND wave.closed_at IS NULL AND (
                        (proof_mode = 'pre_start_forfeit' AND wave.revision = proof_wave_revision
                            AND wave.state IN ('planned', 'ready_window_open', 'ready'))
                        OR (proof_mode = 'normal_no_show' AND wave.revision = proof_wave_revision + 1
                            AND wave.state = 'ready_window_expired' AND wave.updated_at = proof_locked_at)
                    ))
            )
            AND current_content.normal_pool_revision_id = proof_normal_pool_revision_id
            AND current_content.normal_pool_revision = proof_normal_pool_revision
            AND EXISTS (
                SELECT 1
                FROM swiss_rounds AS swiss_round
                WHERE swiss_round.id = proof_round_id
                    AND swiss_round.roster_id = proof_roster_id
                    AND swiss_round.revision = proof_round_revision
                    AND swiss_round.source_history_revision = proof_history_revision
                    AND swiss_round.lock_revision = proof_round_revision
                    AND swiss_round.locked_at = proof_locked_at
            )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof source authority is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        (
            SELECT member.participant_id
            FROM swiss_round_lock_proof_members AS member
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
            EXCEPT
            SELECT member.participant_id
            FROM wave_members AS member
            WHERE member.wave_id = proof_wave_id
                AND member.roster_id = proof_roster_id
        )
        UNION ALL
        (
            SELECT member.participant_id
            FROM wave_members AS member
            WHERE member.wave_id = proof_wave_id
                AND member.roster_id = proof_roster_id
            EXCEPT
            SELECT member.participant_id
            FROM swiss_round_lock_proof_members AS member
            WHERE member.round_id = proof_round_id
                AND member.roster_id = proof_roster_id
        )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof membership differs from persisted Wave membership'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        (
            SELECT proof_series.series_id
            FROM swiss_round_lock_proof_series AS proof_series
            WHERE proof_series.round_id = proof_round_id
                AND proof_series.roster_id = proof_roster_id
            EXCEPT
            SELECT membership.series_id
            FROM wave_series AS membership
            WHERE membership.wave_id = proof_wave_id
                AND membership.tournament_id = proof_tournament_id
                AND membership.roster_id = proof_roster_id
        )
        UNION ALL
        (
            SELECT membership.series_id
            FROM wave_series AS membership
            WHERE membership.wave_id = proof_wave_id
                AND membership.tournament_id = proof_tournament_id
                AND membership.roster_id = proof_roster_id
            EXCEPT
            SELECT proof_series.series_id
            FROM swiss_round_lock_proof_series AS proof_series
            WHERE proof_series.round_id = proof_round_id
                AND proof_series.roster_id = proof_roster_id
        )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof Series differ from persisted Wave membership'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM swiss_round_lock_proof_series AS proof_series
        LEFT JOIN series AS series
            ON series.id = proof_series.series_id
            AND series.tournament_id = proof_tournament_id
            AND series.roster_id = proof_roster_id
        LEFT JOIN category_revisions AS category_revision
            ON category_revision.id = proof_series.category_revision_id
            AND category_revision.series_id = proof_series.series_id
            AND category_revision.roster_id = proof_roster_id
            AND category_revision.revision = proof_series.category_revision
            AND category_revision.source_pool_revision_id = proof_normal_pool_revision_id
        LEFT JOIN assignments AS assignment
            ON assignment.id = proof_series.assignment_id
            AND assignment.series_id = proof_series.series_id
            AND assignment.roster_id = proof_roster_id
            AND assignment.revision = proof_series.assignment_revision
            AND assignment.state = 'active'
        LEFT JOIN assignment_plans AS assignment_plan
            ON assignment_plan.id = proof_series.assignment_plan_id
            AND assignment_plan.revision_id = proof_series.assignment_plan_revision_id
            AND assignment_plan.roster_id = proof_roster_id
            AND assignment_plan.state = 'committed'
            AND assignment_plan.active_branch_id = assignment.branch_id
        LEFT JOIN task_version_reservations AS reservation
            ON reservation.id = proof_series.reservation_id
            AND reservation.id = assignment.reservation_id
            AND reservation.plan_id = assignment_plan.id
            AND reservation.branch_id = assignment.branch_id
            AND reservation.state = 'committed'
            AND (
                (proof_mode = 'wave_start' AND reservation.revision = proof_series.reservation_revision + 1
                    AND reservation.disclosed_at = proof_locked_at)
                OR (terminal_authority_valid AND reservation.revision = proof_series.reservation_revision
                    AND reservation.disclosed_at IS NULL)
            )
        LEFT JOIN game_attempts AS attempt
            ON attempt.id = assignment.attempt_id
            AND attempt.series_id = proof_series.series_id
            AND attempt.roster_id = proof_roster_id
            AND ((proof_mode = 'wave_start' AND attempt.state = 'active')
                OR (terminal_authority_valid AND attempt.state IN ('planned', 'ready', 'cancelled') AND attempt.started_at IS NULL))
        LEFT JOIN game_slots AS slot
            ON slot.id = attempt.slot_id
            AND slot.series_id = proof_series.series_id
            AND slot.roster_id = proof_roster_id
        WHERE proof_series.round_id = proof_round_id
            AND proof_series.roster_id = proof_roster_id
            AND (
                series.id IS NULL
                OR series.format <> 'bo1'
                OR (proof_mode = 'wave_start' AND series.state <> 'active')
                OR (proof_mode <> 'wave_start' AND (NOT terminal_authority_valid OR series.started_at IS NOT NULL
                    OR series.state NOT IN ('planned', 'ready', 'completed', 'cancelled')))
                OR series.first_participant_id <> proof_series.first_participant_id
                OR series.second_participant_id <> proof_series.second_participant_id
                OR category_revision.id IS NULL
                OR assignment.id IS NULL
                OR assignment_plan.id IS NULL
                OR reservation.id IS NULL
                OR attempt.id IS NULL
                OR slot.id IS NULL
                OR NOT category_revision.selected_categories @> jsonb_build_array(slot.category)
                OR (SELECT COUNT(*) FROM game_slots AS current_slot
                    WHERE current_slot.series_id = proof_series.series_id
                        AND current_slot.roster_id = proof_roster_id) <> 1
                OR EXISTS (
                    SELECT 1
                    FROM game_attempts AS newer_attempt
                    WHERE newer_attempt.slot_id = attempt.slot_id
                        AND newer_attempt.series_id = proof_series.series_id
                        AND newer_attempt.roster_id = proof_roster_id
                        AND newer_attempt.attempt_number > attempt.attempt_number
                )
            )
    ) THEN
        RAISE EXCEPTION 'Swiss round lock proof Series binding is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER swiss_round_lock_proof_validate_root
    AFTER INSERT ON public.swiss_round_lock_proofs
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION public.validate_swiss_round_lock_proof();

CREATE CONSTRAINT TRIGGER swiss_round_lock_proof_validate_members
    AFTER INSERT ON public.swiss_round_lock_proof_members
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION public.validate_swiss_round_lock_proof();

CREATE CONSTRAINT TRIGGER swiss_round_lock_proof_validate_series
    AFTER INSERT ON public.swiss_round_lock_proof_series
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION public.validate_swiss_round_lock_proof();

CREATE TRIGGER swiss_round_lock_proof_root_immutable
    BEFORE UPDATE OR DELETE ON public.swiss_round_lock_proofs
    FOR EACH ROW
    EXECUTE FUNCTION public.swiss_round_lock_proof_append_only();

CREATE TRIGGER swiss_round_lock_proof_members_immutable
    BEFORE UPDATE OR DELETE ON public.swiss_round_lock_proof_members
    FOR EACH ROW
    EXECUTE FUNCTION public.swiss_round_lock_proof_append_only();

CREATE TRIGGER swiss_round_lock_proof_series_immutable
    BEFORE UPDATE OR DELETE ON public.swiss_round_lock_proof_series
    FOR EACH ROW
    EXECUTE FUNCTION public.swiss_round_lock_proof_append_only();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS
    public.swiss_round_lock_proof_series,
    public.swiss_round_lock_proof_members,
    public.swiss_round_lock_proofs,
    public.swiss_pairing_commands,
    public.swiss_byes,
    public.swiss_opponent_history,
    public.swiss_pairing_members,
    public.swiss_pairings,
    public.swiss_repeat_overrides,
    public.swiss_rounds;

DROP FUNCTION IF EXISTS public.validate_swiss_round_lock_proof();
DROP FUNCTION IF EXISTS public.swiss_round_lock_proof_append_only();

-- +goose StatementEnd
