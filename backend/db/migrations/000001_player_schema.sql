-- +goose Up
-- +goose StatementBegin

-- Initial player domain schema.
SET LOCAL check_function_bodies = false;

-- Goose owns this metadata table; sqlc only models application tables.
DO $lineage$
BEGIN
    COMMENT ON TABLE public.goose_db_version IS 'task-per-minute:domain-schema:v1';
END;
$lineage$;

--
-- Name: notify_admin_players_changed(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.notify_admin_players_changed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM pg_notify(
        'admin_players_changed',
        json_build_object('table', TG_TABLE_NAME, 'op', TG_OP)::text
    );
    RETURN NULL;
END;
$$;

--
-- Name: admin_player_audit_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.admin_player_audit_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    actor_subject text NOT NULL,
    actor_jti text NOT NULL,
    action text NOT NULL,
    player_id uuid NOT NULL,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT admin_player_audit_events_action_check CHECK ((action = ANY (ARRAY['update'::text, 'delete'::text]))),
    CONSTRAINT admin_player_audit_events_actor_jti_check CHECK ((btrim(actor_jti) <> ''::text)),
    CONSTRAINT admin_player_audit_events_actor_subject_check CHECK ((btrim(actor_subject) <> ''::text))
);

--
-- Name: player_leaderboard_overrides; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.player_leaderboard_overrides (
    player_id uuid NOT NULL,
    wins integer NOT NULL,
    average_solve_time_ms bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT player_leaderboard_overrides_average_solve_time_ms_check CHECK ((average_solve_time_ms >= 0)),
    CONSTRAINT player_leaderboard_overrides_check CHECK ((((wins = 0) AND (average_solve_time_ms = 0)) OR ((wins > 0) AND (average_solve_time_ms > 0)))),
    CONSTRAINT player_leaderboard_overrides_wins_check CHECK ((wins >= 0))
);

--
-- Name: players; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.players (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    username character varying(50) NOT NULL,
    session_token uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    session_expires_at timestamp with time zone
);

--
-- Name: admin_player_audit_events admin_player_audit_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.admin_player_audit_events
    ADD CONSTRAINT admin_player_audit_events_pkey PRIMARY KEY (id);

--
-- Name: player_leaderboard_overrides player_leaderboard_overrides_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_leaderboard_overrides
    ADD CONSTRAINT player_leaderboard_overrides_pkey PRIMARY KEY (player_id);

--
-- Name: players players_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.players
    ADD CONSTRAINT players_pkey PRIMARY KEY (id);

--
-- Name: players players_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.players
    ADD CONSTRAINT players_username_key UNIQUE (username);

--
-- Name: admin_player_audit_events_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX admin_player_audit_events_created_idx ON public.admin_player_audit_events USING btree (created_at DESC);

--
-- Name: admin_player_audit_events_player_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX admin_player_audit_events_player_created_idx ON public.admin_player_audit_events USING btree (player_id, created_at DESC);

--
-- Name: players_deleted_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX players_deleted_at_idx ON public.players USING btree (deleted_at);

--
-- Name: players_session_expires_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX players_session_expires_at_idx ON public.players USING btree (session_expires_at) WHERE (session_expires_at IS NOT NULL);

--
-- Name: players_session_token_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX players_session_token_idx ON public.players USING btree (session_token) WHERE (session_token IS NOT NULL);

--
-- Name: player_leaderboard_overrides player_leaderboard_overrides_admin_players_changed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER player_leaderboard_overrides_admin_players_changed AFTER INSERT OR DELETE OR UPDATE ON public.player_leaderboard_overrides FOR EACH STATEMENT EXECUTE FUNCTION public.notify_admin_players_changed();

--
-- Name: players players_admin_players_changed; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER players_admin_players_changed AFTER INSERT OR DELETE OR UPDATE ON public.players FOR EACH STATEMENT EXECUTE FUNCTION public.notify_admin_players_changed();

--
-- Name: admin_player_audit_events admin_player_audit_events_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.admin_player_audit_events
    ADD CONSTRAINT admin_player_audit_events_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id) ON DELETE CASCADE;

--
-- Name: player_leaderboard_overrides player_leaderboard_overrides_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_leaderboard_overrides
    ADD CONSTRAINT player_leaderboard_overrides_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id) ON DELETE CASCADE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DO $lineage$
BEGIN
    COMMENT ON TABLE public.goose_db_version IS NULL;
END;
$lineage$;

DROP TABLE IF EXISTS
    public.admin_player_audit_events,
    public.player_leaderboard_overrides,
    public.players;

DROP FUNCTION IF EXISTS
    public.notify_admin_players_changed();

-- +goose StatementEnd
