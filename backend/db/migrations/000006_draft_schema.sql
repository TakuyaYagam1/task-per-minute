-- +goose Up
-- +goose StatementBegin

-- Initial draft domain schema.
SET LOCAL check_function_bodies = false;

-- Draft participants are actor turn order. Series participants retain bracket
-- and score orientation; the two identities must contain the same exact pair.
CREATE FUNCTION public.draft_series_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM 1
    FROM series
    WHERE id = NEW.series_id AND roster_id = NEW.roster_id AND format = NEW.format
        AND (
            (first_participant_id = NEW.first_participant_id AND second_participant_id = NEW.second_participant_id)
            OR (first_participant_id = NEW.second_participant_id AND second_participant_id = NEW.first_participant_id)
        )
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'draft participants and format must match the exact Series identity'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

--
-- Name: draft_action_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.draft_action_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    draft_format VARCHAR(8);
    category_pool JSONB;
    first_actor_id UUID;
    second_actor_id UUID;
    expected_actor_id UUID;
    expected_action VARCHAR(8);
    expected_turn SMALLINT;
    final_turn SMALLINT;
    result_state VARCHAR(32);
    result_turn SMALLINT;
    result_decision_purpose VARCHAR(32);
BEGIN
    SELECT
        draft.format,
        draft.first_participant_id,
        draft.second_participant_id,
        category_revision.category_pool
    INTO draft_format, first_actor_id, second_actor_id, category_pool
    FROM drafts AS draft
    JOIN category_revisions AS category_revision
        ON category_revision.id = draft.category_revision_id
    WHERE draft.id = NEW.draft_id
    FOR UPDATE OF draft;

    SELECT COALESCE(MAX(turn_number), 0) + 1
    INTO expected_turn
    FROM draft_actions
    WHERE draft_id = NEW.draft_id;

    IF NEW.turn_number <> expected_turn THEN
        RAISE EXCEPTION 'draft actions must be contiguous and ordered'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NOT category_pool ? NEW.category THEN
        RAISE EXCEPTION 'draft category is outside the revision pool'
            USING ERRCODE = 'check_violation';
    END IF;

    IF draft_format = 'bo1' THEN
        final_turn := 2;
    ELSE
        final_turn := 4;
    END IF;

    IF NEW.turn_number IN (1, 3) THEN
        expected_actor_id := first_actor_id;
    ELSE
        expected_actor_id := second_actor_id;
    END IF;

    IF NEW.turn_number <= 2 THEN
        expected_action := 'ban';
    ELSE
        expected_action := 'pick';
    END IF;

    IF NEW.turn_number > final_turn
        OR NEW.actor_id IS DISTINCT FROM expected_actor_id
        OR NEW.action <> expected_action THEN
        RAISE EXCEPTION 'illegal draft actor, turn, or action'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state, turn_number, decision_purpose
    INTO result_state, result_turn, result_decision_purpose
    FROM draft_revisions
    WHERE id = NEW.result_revision_id AND draft_id = NEW.draft_id;

    IF NEW.turn_number = final_turn THEN
        IF result_state <> 'completed' OR result_turn <> final_turn THEN
            RAISE EXCEPTION 'final draft action must complete the draft'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF result_state <> 'active' OR result_turn <> NEW.turn_number + 1 THEN
        RAISE EXCEPTION 'draft action must advance exactly one turn'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.automatic AND result_decision_purpose IS DISTINCT FROM 'category' THEN
        RAISE EXCEPTION 'automatic draft action requires reproducible category evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: draft_immutable_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.draft_immutable_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'draft identities and evidence are immutable'
        USING ERRCODE = 'check_violation';
END;
$$;

--
-- Name: draft_revision_action_consistency(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.draft_revision_action_consistency() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_turn SMALLINT;
    action_count INTEGER;
BEGIN
    IF NEW.previous_revision_id IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT turn_number
    INTO previous_turn
    FROM draft_revisions
    WHERE id = NEW.previous_revision_id;

    IF NEW.turn_number > previous_turn OR NEW.state = 'completed' THEN
        SELECT COUNT(*)
        INTO action_count
        FROM draft_actions
        WHERE result_revision_id = NEW.id;

        IF action_count <> 1 THEN
            RAISE EXCEPTION 'turn-changing draft revision requires one ordered action'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: draft_revision_insert_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.draft_revision_insert_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_id UUID;
    previous_number BIGINT;
    previous_state VARCHAR(32);
    previous_turn SMALLINT;
    first_actor_id UUID;
    second_actor_id UUID;
BEGIN
    PERFORM 1
    FROM drafts
    WHERE id = NEW.draft_id
    FOR UPDATE;

    SELECT id, revision, state, turn_number
    INTO previous_id, previous_number, previous_state, previous_turn
    FROM draft_revisions
    WHERE draft_id = NEW.draft_id
    ORDER BY revision DESC
    LIMIT 1;

    SELECT first_participant_id, second_participant_id
    INTO first_actor_id, second_actor_id
    FROM drafts
    WHERE id = NEW.draft_id;

    IF NEW.current_actor_id IS NOT NULL
        AND NEW.current_actor_id NOT IN (first_actor_id, second_actor_id) THEN
        RAISE EXCEPTION 'draft actor must belong to the Series'
            USING ERRCODE = 'check_violation';
    END IF;

    IF previous_id IS NULL THEN
        IF NEW.revision <> 1
            OR NEW.previous_revision_id IS NOT NULL
            OR NEW.state <> 'active'
            OR NEW.turn_number <> 1
            OR NEW.decision_purpose IS DISTINCT FROM 'draft_order' THEN
            RAISE EXCEPTION 'first draft revision must establish turn one and reproducible actor order'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.revision <> previous_number + 1
        OR NEW.previous_revision_id IS DISTINCT FROM previous_id THEN
        RAISE EXCEPTION 'draft revision CAS is stale'
            USING ERRCODE = 'serialization_failure';
    END IF;

    IF previous_state IN ('completed', 'superseded') THEN
        RAISE EXCEPTION 'terminal draft evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.turn_number < previous_turn OR NEW.turn_number > previous_turn + 1 THEN
        RAISE EXCEPTION 'draft turn revision is not contiguous'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (previous_state = 'active' AND NEW.state NOT IN (
        'active', 'paused', 'recovery_required', 'completed', 'superseded'
    ))
        OR (previous_state = 'paused' AND NEW.state NOT IN (
            'active', 'recovery_required', 'superseded'
        ))
        OR (previous_state = 'recovery_required' AND NEW.state NOT IN (
            'active', 'superseded'
        )) THEN
        RAISE EXCEPTION 'invalid draft revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: draft_revision_turn_identity_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.draft_revision_turn_identity_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    draft_format VARCHAR(8);
    first_actor_id UUID;
    second_actor_id UUID;
    expected_actor_id UUID;
    expected_action VARCHAR(8);
    final_turn SMALLINT;
BEGIN
    IF NEW.current_actor_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT format, first_participant_id, second_participant_id
    INTO draft_format, first_actor_id, second_actor_id
    FROM drafts
    WHERE id = NEW.draft_id;

    IF draft_format = 'bo1' THEN
        final_turn := 2;
    ELSE
        final_turn := 4;
    END IF;

    IF NEW.turn_number IN (1, 3) THEN
        expected_actor_id := first_actor_id;
    ELSE
        expected_actor_id := second_actor_id;
    END IF;

    IF NEW.turn_number <= 2 THEN
        expected_action := 'ban';
    ELSE
        expected_action := 'pick';
    END IF;

    IF NEW.turn_number > final_turn
        OR NEW.current_actor_id IS DISTINCT FROM expected_actor_id
        OR NEW.current_action IS DISTINCT FROM expected_action THEN
        RAISE EXCEPTION 'draft revision actor and action must match its turn'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: category_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.category_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision bigint NOT NULL,
    source_pool_revision_id uuid NOT NULL,
    mode character varying(16) NOT NULL,
    category_pool jsonb NOT NULL,
    selected_categories jsonb DEFAULT '[]'::jsonb NOT NULL,
    selector_actor_id uuid,
    selection_reason text,
    decision_evidence_id uuid,
    decision_algorithm_version character varying(64),
    decision_inputs jsonb,
    decision_seed bytea,
    decision_result jsonb,
    decision_replay_digest bytea,
    decision_owner_id uuid,
    decided_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT category_revisions_decided_at_check CHECK (((decided_at IS NULL) OR (decided_at <= created_at))),
    CONSTRAINT category_revisions_mode_check CHECK (((mode)::text = ANY ((ARRAY['random'::character varying, 'admin'::character varying, 'draft'::character varying])::text[]))),
    CONSTRAINT category_revisions_pool_check CHECK (((jsonb_typeof(category_pool) = 'array'::text) AND (jsonb_array_length(category_pool) = ANY (ARRAY[3, 5])) AND (jsonb_typeof(selected_categories) = 'array'::text) AND (jsonb_array_length(selected_categories) <= 3))),
    CONSTRAINT category_revisions_revision_check CHECK ((revision >= 1)),
    CONSTRAINT category_revisions_selection_check CHECK (((((mode)::text = 'random'::text) AND (jsonb_array_length(selected_categories) > 0) AND (selector_actor_id IS NULL) AND (selection_reason IS NULL) AND (decision_evidence_id IS NOT NULL) AND (decision_algorithm_version IS NOT NULL) AND ((decision_algorithm_version)::text = 'hmac-sha256-order-v1'::text) AND (decision_inputs IS NOT NULL) AND (jsonb_typeof(decision_inputs) = 'array'::text) AND (jsonb_array_length(decision_inputs) > 0) AND (decision_seed IS NOT NULL) AND (octet_length(decision_seed) = 32) AND (decision_seed <> decode(repeat('00'::text, 32), 'hex'::text)) AND (decision_result IS NOT NULL) AND (jsonb_typeof(decision_result) = 'array'::text) AND (jsonb_array_length(decision_result) > 0) AND (decision_replay_digest IS NOT NULL) AND (octet_length(decision_replay_digest) = 32) AND (decision_owner_id IS NOT NULL) AND (decision_owner_id = id) AND (decided_at IS NOT NULL)) OR (((mode)::text = 'admin'::text) AND (jsonb_array_length(selected_categories) > 0) AND (selector_actor_id IS NOT NULL) AND (selection_reason IS NOT NULL) AND (selection_reason = btrim(selection_reason)) AND (selection_reason <> ''::text) AND (decision_evidence_id IS NULL) AND (decision_algorithm_version IS NULL) AND (decision_inputs IS NULL) AND (decision_seed IS NULL) AND (decision_result IS NULL) AND (decision_replay_digest IS NULL) AND (decision_owner_id IS NULL) AND (decided_at IS NOT NULL)) OR (((mode)::text = 'draft'::text) AND (selected_categories = '[]'::jsonb) AND (selector_actor_id IS NULL) AND (selection_reason IS NULL) AND (decision_evidence_id IS NULL) AND (decision_algorithm_version IS NULL) AND (decision_inputs IS NULL) AND (decision_seed IS NULL) AND (decision_result IS NULL) AND (decision_replay_digest IS NULL) AND (decision_owner_id IS NULL) AND (decided_at IS NULL))))
);

--
-- Name: draft_actions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.draft_actions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    draft_id uuid NOT NULL,
    result_revision_id uuid NOT NULL,
    command_id uuid NOT NULL,
    turn_number smallint NOT NULL,
    actor_id uuid NOT NULL,
    action character varying(8) NOT NULL,
    category character varying(32) NOT NULL,
    scheduled_deadline timestamp with time zone NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    automatic boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT draft_actions_action_check CHECK (((action)::text = ANY ((ARRAY['ban'::character varying, 'pick'::character varying])::text[]))),
    CONSTRAINT draft_actions_category_check CHECK (((category)::text = ANY ((ARRAY['web'::character varying, 'crypto'::character varying, 'forensics'::character varying, 'reverse'::character varying, 'pwn'::character varying, 'steganography'::character varying, 'ppc'::character varying, 'osint'::character varying, 'mobile'::character varying, 'hardware'::character varying, 'misc'::character varying])::text[]))),
    CONSTRAINT draft_actions_deadline_check CHECK (((occurred_at <= scheduled_deadline) AND (occurred_at <= created_at))),
    CONSTRAINT draft_actions_turn_check CHECK (((turn_number >= 1) AND (turn_number <= 4)))
);

--
-- Name: draft_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.draft_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    draft_id uuid NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision bigint NOT NULL,
    previous_revision_id uuid,
    command_id uuid NOT NULL,
    service_epoch uuid NOT NULL,
    state character varying(32) NOT NULL,
    turn_number smallint NOT NULL,
    current_actor_id uuid,
    current_action character varying(8),
    absolute_deadline timestamp with time zone,
    paused_remaining_ms integer,
    recovery_reason character varying(32),
    recovery_evidence jsonb,
    selected_categories jsonb DEFAULT '[]'::jsonb NOT NULL,
    decision_evidence_id uuid,
    decision_purpose character varying(32),
    decision_algorithm_version character varying(64),
    decision_inputs jsonb,
    decision_seed bytea,
    decision_result jsonb,
    decision_replay_digest bytea,
    decision_owner_id uuid,
    decided_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT draft_revisions_current_action_check CHECK (((current_action IS NULL) OR ((current_action)::text = ANY ((ARRAY['ban'::character varying, 'pick'::character varying])::text[])))),
    CONSTRAINT draft_revisions_decision_check CHECK ((((decision_evidence_id IS NULL) AND (decision_purpose IS NULL) AND (decision_algorithm_version IS NULL) AND (decision_inputs IS NULL) AND (decision_seed IS NULL) AND (decision_result IS NULL) AND (decision_replay_digest IS NULL) AND (decision_owner_id IS NULL) AND (decided_at IS NULL)) OR ((decision_evidence_id IS NOT NULL) AND (decision_purpose IS NOT NULL) AND ((decision_purpose)::text = ANY ((ARRAY['category'::character varying, 'draft_order'::character varying])::text[])) AND (decision_algorithm_version IS NOT NULL) AND ((decision_algorithm_version)::text = 'hmac-sha256-order-v1'::text) AND (decision_inputs IS NOT NULL) AND (jsonb_typeof(decision_inputs) = 'array'::text) AND (jsonb_array_length(decision_inputs) > 0) AND (decision_seed IS NOT NULL) AND (octet_length(decision_seed) = 32) AND (decision_seed <> decode(repeat('00'::text, 32), 'hex'::text)) AND (decision_result IS NOT NULL) AND (jsonb_typeof(decision_result) = 'array'::text) AND (jsonb_array_length(decision_result) > 0) AND (decision_replay_digest IS NOT NULL) AND (octet_length(decision_replay_digest) = 32) AND (decision_owner_id IS NOT NULL) AND (decision_owner_id = draft_id) AND (decided_at IS NOT NULL) AND (decided_at <= created_at)))),
    CONSTRAINT draft_revisions_recovery_check CHECK (((recovery_evidence IS NULL) OR (jsonb_typeof(recovery_evidence) = 'object'::text))),
    CONSTRAINT draft_revisions_revision_check CHECK ((revision >= 1)),
    CONSTRAINT draft_revisions_selected_check CHECK (((jsonb_typeof(selected_categories) = 'array'::text) AND (jsonb_array_length(selected_categories) <= 3))),
    CONSTRAINT draft_revisions_state_check CHECK (((state)::text = ANY ((ARRAY['active'::character varying, 'paused'::character varying, 'recovery_required'::character varying, 'completed'::character varying, 'superseded'::character varying])::text[]))),
    CONSTRAINT draft_revisions_state_evidence_check CHECK (((((state)::text = 'active'::text) AND (current_actor_id IS NOT NULL) AND (current_action IS NOT NULL) AND (absolute_deadline IS NOT NULL) AND (paused_remaining_ms IS NULL) AND (recovery_reason IS NULL) AND (recovery_evidence IS NULL) AND (selected_categories = '[]'::jsonb)) OR (((state)::text = 'paused'::text) AND (current_actor_id IS NOT NULL) AND (current_action IS NOT NULL) AND (absolute_deadline IS NULL) AND ((paused_remaining_ms >= 1) AND (paused_remaining_ms <= 15000)) AND (recovery_reason IS NOT NULL) AND ((recovery_reason)::text = 'operator_pause'::text) AND (recovery_evidence IS NOT NULL) AND (selected_categories = '[]'::jsonb)) OR (((state)::text = 'recovery_required'::text) AND (current_actor_id IS NOT NULL) AND (current_action IS NOT NULL) AND (absolute_deadline IS NULL) AND (paused_remaining_ms IS NULL) AND (recovery_reason IS NOT NULL) AND ((recovery_reason)::text = ANY ((ARRAY['epoch_mismatch'::character varying, 'service_restart'::character varying])::text[])) AND (recovery_evidence IS NOT NULL) AND (selected_categories = '[]'::jsonb)) OR (((state)::text = 'completed'::text) AND (current_actor_id IS NULL) AND (current_action IS NULL) AND (absolute_deadline IS NULL) AND (paused_remaining_ms IS NULL) AND (recovery_reason IS NULL) AND (recovery_evidence IS NULL) AND (jsonb_array_length(selected_categories) > 0)) OR (((state)::text = 'superseded'::text) AND (current_actor_id IS NULL) AND (current_action IS NULL) AND (absolute_deadline IS NULL) AND (paused_remaining_ms IS NULL) AND (recovery_reason IS NOT NULL) AND ((recovery_reason)::text = 'correction'::text) AND (recovery_evidence IS NOT NULL) AND (selected_categories = '[]'::jsonb)))),
    CONSTRAINT draft_revisions_turn_check CHECK (((turn_number >= 1) AND (turn_number <= 4)))
);

--
-- Name: drafts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drafts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    series_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    category_revision_id uuid NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    format character varying(8) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT drafts_format_check CHECK (((format)::text = ANY ((ARRAY['bo1'::character varying, 'bo3'::character varying])::text[]))),
    CONSTRAINT drafts_participants_check CHECK ((first_participant_id <> second_participant_id))
);

--
-- Name: category_revisions category_revisions_decision_evidence_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_revisions
    ADD CONSTRAINT category_revisions_decision_evidence_id_key UNIQUE (decision_evidence_id);

--
-- Name: category_revisions category_revisions_id_series_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_revisions
    ADD CONSTRAINT category_revisions_id_series_roster_key UNIQUE (id, series_id, roster_id);

--
-- Name: category_revisions category_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_revisions
    ADD CONSTRAINT category_revisions_pkey PRIMARY KEY (id);

--
-- Name: category_revisions category_revisions_series_revision_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_revisions
    ADD CONSTRAINT category_revisions_series_revision_key UNIQUE (series_id, revision);

--
-- Name: draft_actions draft_actions_draft_category_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_actions
    ADD CONSTRAINT draft_actions_draft_category_key UNIQUE (draft_id, category);

--
-- Name: draft_actions draft_actions_draft_command_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_actions
    ADD CONSTRAINT draft_actions_draft_command_key UNIQUE (draft_id, command_id);

--
-- Name: draft_actions draft_actions_draft_turn_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_actions
    ADD CONSTRAINT draft_actions_draft_turn_key UNIQUE (draft_id, turn_number);

--
-- Name: draft_actions draft_actions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_actions
    ADD CONSTRAINT draft_actions_pkey PRIMARY KEY (id);

--
-- Name: draft_revisions draft_revisions_decision_evidence_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_decision_evidence_id_key UNIQUE (decision_evidence_id);

--
-- Name: draft_revisions draft_revisions_draft_command_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_draft_command_key UNIQUE (draft_id, command_id);

--
-- Name: draft_revisions draft_revisions_draft_revision_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_draft_revision_key UNIQUE (draft_id, revision);

--
-- Name: draft_revisions draft_revisions_id_draft_command_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_id_draft_command_key UNIQUE (id, draft_id, command_id);

--
-- Name: draft_revisions draft_revisions_id_draft_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_id_draft_key UNIQUE (id, draft_id);

--
-- Name: draft_revisions draft_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_pkey PRIMARY KEY (id);

--
-- Name: drafts drafts_id_series_roster_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drafts
    ADD CONSTRAINT drafts_id_series_roster_key UNIQUE (id, series_id, roster_id);

--
-- Name: drafts drafts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drafts
    ADD CONSTRAINT drafts_pkey PRIMARY KEY (id);

--
-- Name: drafts drafts_series_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drafts
    ADD CONSTRAINT drafts_series_id_key UNIQUE (series_id);

--
-- Name: draft_actions_draft_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX draft_actions_draft_created_idx ON public.draft_actions USING btree (draft_id, turn_number, created_at);

--
-- Name: draft_revisions_draft_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX draft_revisions_draft_created_idx ON public.draft_revisions USING btree (draft_id, revision DESC, created_at);

--
-- Name: category_revisions category_revision_immutable_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER category_revision_immutable_guard BEFORE DELETE OR UPDATE ON public.category_revisions FOR EACH ROW EXECUTE FUNCTION public.draft_immutable_guard();

--
-- Name: draft_actions draft_action_immutable_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER draft_action_immutable_guard BEFORE DELETE OR UPDATE ON public.draft_actions FOR EACH ROW EXECUTE FUNCTION public.draft_immutable_guard();

--
-- Name: draft_actions draft_action_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER draft_action_insert_guard BEFORE INSERT ON public.draft_actions FOR EACH ROW EXECUTE FUNCTION public.draft_action_insert_guard();

--
-- Name: drafts draft_identity_immutable_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER draft_identity_immutable_guard BEFORE DELETE OR UPDATE ON public.drafts FOR EACH ROW EXECUTE FUNCTION public.draft_immutable_guard();

CREATE TRIGGER draft_series_identity_guard BEFORE INSERT OR UPDATE ON public.drafts
FOR EACH ROW EXECUTE FUNCTION public.draft_series_identity_guard();

--
-- Name: draft_revisions draft_revision_action_consistency; Type: TRIGGER; Schema: public; Owner: -
--

CREATE CONSTRAINT TRIGGER draft_revision_action_consistency AFTER INSERT ON public.draft_revisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.draft_revision_action_consistency();

--
-- Name: draft_revisions draft_revision_immutable_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER draft_revision_immutable_guard BEFORE DELETE OR UPDATE ON public.draft_revisions FOR EACH ROW EXECUTE FUNCTION public.draft_immutable_guard();

--
-- Name: draft_revisions draft_revision_insert_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER draft_revision_insert_guard BEFORE INSERT ON public.draft_revisions FOR EACH ROW EXECUTE FUNCTION public.draft_revision_insert_guard();

--
-- Name: draft_revisions draft_revision_turn_identity_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER draft_revision_turn_identity_guard BEFORE INSERT ON public.draft_revisions FOR EACH ROW EXECUTE FUNCTION public.draft_revision_turn_identity_guard();

--
-- Name: category_revisions category_revisions_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_revisions
    ADD CONSTRAINT category_revisions_series_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

--
-- Name: draft_actions draft_actions_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_actions
    ADD CONSTRAINT draft_actions_revision_fk FOREIGN KEY (result_revision_id, draft_id, command_id) REFERENCES public.draft_revisions(id, draft_id, command_id) ON DELETE RESTRICT;

--
-- Name: draft_revisions draft_revisions_actor_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_actor_fk FOREIGN KEY (roster_id, current_actor_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: draft_revisions draft_revisions_draft_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_draft_fk FOREIGN KEY (draft_id, series_id, roster_id) REFERENCES public.drafts(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: draft_revisions draft_revisions_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.draft_revisions
    ADD CONSTRAINT draft_revisions_previous_fk FOREIGN KEY (previous_revision_id, draft_id) REFERENCES public.draft_revisions(id, draft_id) ON DELETE RESTRICT;

--
-- Name: drafts drafts_category_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drafts
    ADD CONSTRAINT drafts_category_revision_fk FOREIGN KEY (category_revision_id, series_id, roster_id) REFERENCES public.category_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: drafts drafts_series_identity_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drafts
    ADD CONSTRAINT drafts_series_identity_fk FOREIGN KEY (series_id, roster_id) REFERENCES public.series(id, roster_id) ON DELETE RESTRICT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS
    public.draft_actions,
    public.draft_revisions,
    public.drafts,
    public.category_revisions;

DROP FUNCTION IF EXISTS
    public.draft_series_identity_guard(),
    public.draft_revision_turn_identity_guard(),
    public.draft_revision_insert_guard(),
    public.draft_revision_action_consistency(),
    public.draft_immutable_guard(),
    public.draft_action_insert_guard();

-- +goose StatementEnd
