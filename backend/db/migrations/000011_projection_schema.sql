-- +goose Up
-- +goose StatementBegin

-- Initial projection domain schema.
SET LOCAL check_function_bodies = false;

--
-- Name: projection_artifact_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.projection_artifact_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    revision_state VARCHAR(16);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'projection artifacts are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state INTO revision_state
    FROM projection_revisions
    WHERE id = NEW.produced_by_revision_id
    FOR NO KEY UPDATE;

    IF revision_state <> 'draft' THEN
        RAISE EXCEPTION 'projection artifacts can only be produced by a draft revision'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: participant_post_series_action_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.participant_post_series_action_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    result_entity_kind VARCHAR(16);
    result_entity_id UUID;
    projection_state VARCHAR(16);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Participant post-Series actions are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT entity_kind, entity_id
    INTO result_entity_kind, result_entity_id
    FROM official_result_revisions
    WHERE id = NEW.current_result_revision_id
        AND series_id = NEW.series_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    SELECT state
    INTO projection_state
    FROM projection_revisions
    WHERE id = NEW.source_projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
        AND revision_number = NEW.source_projection_revision
    FOR KEY SHARE;

    IF result_entity_kind IS DISTINCT FROM 'series'
        OR result_entity_id IS DISTINCT FROM NEW.series_id
        OR projection_state IS DISTINCT FROM 'published' THEN
        RAISE EXCEPTION 'Participant post-Series action is not bound to current published evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: projection_artifact_member_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.projection_artifact_member_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    revision_state VARCHAR(16);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'projection artifact membership is immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision.state INTO revision_state
    FROM projection_artifacts AS artifact
    INNER JOIN projection_revisions AS revision
        ON revision.id = artifact.produced_by_revision_id
    WHERE artifact.id = NEW.artifact_id
    FOR NO KEY UPDATE OF revision;

    IF revision_state <> 'draft' THEN
        RAISE EXCEPTION 'published projection membership cannot be extended'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: projection_cutoff_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.projection_cutoff_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_sequence BIGINT;
    source_tournament_id UUID;
    source_roster_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'projection cutoffs are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_cutoff_id IS NOT NULL THEN
        SELECT sequence_number INTO previous_sequence
        FROM projection_cutoffs
        WHERE id = NEW.previous_cutoff_id
        FOR KEY SHARE;

        IF previous_sequence <> NEW.sequence_number - 1 THEN
            RAISE EXCEPTION 'projection cutoff lineage must be consecutive'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.official_result_revision_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM official_result_revisions
        WHERE id = NEW.official_result_revision_id
        FOR KEY SHARE;
    ELSIF NEW.golden_position_commit_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM golden_position_commits
        WHERE id = NEW.golden_position_commit_id
        FOR KEY SHARE;
    ELSIF NEW.stage_progression_command_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM tournament_stage_progressions
        WHERE command_id = NEW.stage_progression_command_id
            AND tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
        FOR KEY SHARE;
    END IF;

    IF source_tournament_id IS NOT NULL
        AND (
            source_tournament_id <> NEW.tournament_id
            OR source_roster_id <> NEW.roster_id
        ) THEN
        RAISE EXCEPTION 'projection cutoff source is outside its roster'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: projection_dependency_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.projection_dependency_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_revision_id UUID;
    target_revision_number BIGINT;
    target_revision_state VARCHAR(16);
    source_revision_number BIGINT;
    source_tournament_id UUID;
    source_roster_id UUID;
    cycle_found BOOLEAN;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'projection dependency edges are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision.id, revision.revision_number, revision.state
    INTO target_revision_id, target_revision_number, target_revision_state
    FROM projection_artifacts AS artifact
    INNER JOIN projection_revisions AS revision
        ON revision.id = artifact.produced_by_revision_id
    WHERE artifact.id = NEW.artifact_id
    FOR NO KEY UPDATE OF revision;

    IF target_revision_state <> 'draft' THEN
        RAISE EXCEPTION 'published projection lineage cannot be extended'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.depends_on_artifact_id IS NOT NULL THEN
        IF NEW.depends_on_artifact_id = NEW.artifact_id THEN
            RAISE EXCEPTION 'projection artifact cannot depend on itself'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT revision.revision_number
        INTO source_revision_number
        FROM projection_artifacts AS artifact
        INNER JOIN projection_revisions AS revision
            ON revision.id = artifact.produced_by_revision_id
        WHERE artifact.id = NEW.depends_on_artifact_id
        FOR KEY SHARE OF artifact, revision;

        IF source_revision_number > target_revision_number THEN
            RAISE EXCEPTION 'projection cannot depend on a future revision'
                USING ERRCODE = 'check_violation';
        END IF;

        WITH RECURSIVE dependency_path(artifact_id) AS (
            SELECT NEW.depends_on_artifact_id
            UNION
            SELECT dependency.depends_on_artifact_id
            FROM projection_dependencies AS dependency
            INNER JOIN dependency_path AS path
                ON dependency.artifact_id = path.artifact_id
            WHERE dependency.depends_on_artifact_id IS NOT NULL
        )
        SELECT EXISTS (
            SELECT 1
            FROM dependency_path
            WHERE artifact_id = NEW.artifact_id
        ) INTO cycle_found;

        IF cycle_found THEN
            RAISE EXCEPTION 'projection dependency graph must remain acyclic'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.official_result_revision_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM official_result_revisions
        WHERE id = NEW.official_result_revision_id
        FOR KEY SHARE;
    ELSE
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM golden_position_commits
        WHERE id = NEW.golden_position_commit_id
        FOR KEY SHARE;
    END IF;

    IF source_tournament_id IS NOT NULL
        AND (
            source_tournament_id <> NEW.tournament_id
            OR source_roster_id <> NEW.roster_id
        ) THEN
        RAISE EXCEPTION 'projection dependency is outside its roster'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: projection_revision_artifact_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.projection_revision_artifact_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    revision_state VARCHAR(16);
    revision_number BIGINT;
    produced_revision_id UUID;
    produced_revision_number BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'projection revision artifact membership is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state, projection_revisions.revision_number
    INTO revision_state, revision_number
    FROM projection_revisions
    WHERE id = NEW.revision_id
    FOR NO KEY UPDATE;

    SELECT artifact.produced_by_revision_id, producer.revision_number
    INTO produced_revision_id, produced_revision_number
    FROM projection_artifacts AS artifact
    INNER JOIN projection_revisions AS producer
        ON producer.id = artifact.produced_by_revision_id
    WHERE artifact.id = NEW.artifact_id
    FOR KEY SHARE OF artifact, producer;

    IF revision_state <> 'draft'
        OR (
            NEW.change_kind = 'produced'
            AND produced_revision_id <> NEW.revision_id
        )
        OR (
            NEW.change_kind = 'reused'
            AND produced_revision_number >= revision_number
        ) THEN
        RAISE EXCEPTION 'invalid projection artifact revision membership'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: projection_revision_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.projection_revision_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    cutoff_sequence BIGINT;
    previous_number BIGINT;
    previous_state VARCHAR(16);
    previous_replacement_id UUID;
    replacement_previous_id UUID;
    replacement_state VARCHAR(16);
    linked_kind_count INTEGER;
    base_kind_count INTEGER;
    champion_count INTEGER;
    dependency_missing_count INTEGER;
    invalid_member_count INTEGER;
    invalid_champion_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT sequence_number INTO cutoff_sequence
        FROM projection_cutoffs
        WHERE id = NEW.cutoff_id
        FOR KEY SHARE;

        IF cutoff_sequence <> NEW.revision_number THEN
            RAISE EXCEPTION 'projection revision must use its matching cutoff sequence'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT revision_number INTO previous_number
            FROM projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR KEY SHARE;

            IF previous_number <> NEW.revision_number - 1 THEN
                RAISE EXCEPTION 'projection revision lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'projection revisions are retained'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.revision_number IS DISTINCT FROM OLD.revision_number
        OR NEW.previous_revision_id IS DISTINCT FROM OLD.previous_revision_id
        OR NEW.cutoff_id IS DISTINCT FROM OLD.cutoff_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR (OLD.published_at IS NOT NULL AND NEW.published_at IS DISTINCT FROM OLD.published_at)
        OR (OLD.superseded_at IS NOT NULL AND NEW.superseded_at IS DISTINCT FROM OLD.superseded_at)
        OR (
            OLD.superseded_by_revision_id IS NOT NULL
            AND NEW.superseded_by_revision_id IS DISTINCT FROM OLD.superseded_by_revision_id
        )
        OR (
            OLD.supersession_reason IS NOT NULL
            AND NEW.supersession_reason IS DISTINCT FROM OLD.supersession_reason
        ) THEN
        RAISE EXCEPTION 'projection revision identity and lifecycle evidence are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'draft' AND NEW.state = 'published')
            OR (OLD.state = 'published' AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid projection revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'published' THEN
        SELECT COUNT(DISTINCT artifact_kind)
        INTO linked_kind_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top_four', 'champion');

        SELECT COUNT(DISTINCT artifact_kind)
        INTO base_kind_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top_four');

        SELECT COUNT(*)
        INTO champion_count
        FROM projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind = 'champion';

        SELECT COUNT(*) INTO dependency_missing_count
        FROM projection_revision_artifacts AS revision_artifact
        WHERE revision_artifact.revision_id = NEW.id
            AND NOT EXISTS (
                SELECT 1
                FROM projection_dependencies AS dependency
                WHERE dependency.artifact_id = revision_artifact.artifact_id
            );

        SELECT COUNT(*) INTO invalid_member_count
        FROM projection_revision_artifacts AS revision_artifact
        INNER JOIN projection_artifacts AS artifact
            ON artifact.id = revision_artifact.artifact_id
        LEFT JOIN LATERAL (
            SELECT COUNT(*) AS member_count
            FROM projection_artifact_members AS artifact_member
            WHERE artifact_member.artifact_id = artifact.id
        ) AS member_evidence ON TRUE
        WHERE revision_artifact.revision_id = NEW.id
            AND (
                (
                    artifact.artifact_kind IN ('standings', 'bracket')
                    AND member_evidence.member_count < 1
                )
                OR (
                    artifact.artifact_kind = 'top_four'
                    AND member_evidence.member_count <> 4
                )
                OR (
                    artifact.artifact_kind = 'champion'
                    AND member_evidence.member_count <> 1
                )
            );

        SELECT COUNT(*)
        INTO invalid_champion_count
        FROM projection_revision_artifacts AS champion_link
        INNER JOIN projection_artifacts AS champion
            ON champion.id = champion_link.artifact_id
        WHERE champion_link.revision_id = NEW.id
            AND champion_link.artifact_kind = 'champion'
            AND NOT EXISTS (
                SELECT 1
                FROM projection_dependencies AS result_dependency
                INNER JOIN series AS final_series
                    ON final_series.id = result_dependency.official_result_series_id
                    AND final_series.tournament_id = NEW.tournament_id
                    AND final_series.roster_id = NEW.roster_id
                INNER JOIN tournament_stage_playoff_finals AS stage_final
                    ON stage_final.final_series_id = final_series.id
                    AND stage_final.tournament_id = NEW.tournament_id
                    AND stage_final.roster_id = NEW.roster_id
                INNER JOIN tournament_stage_playoff_evidence AS stage_evidence
                    ON stage_evidence.command_id = stage_final.command_id
                    AND stage_evidence.tournament_id = stage_final.tournament_id
                    AND stage_evidence.roster_id = stage_final.roster_id
                INNER JOIN official_result_heads AS final_head
                    ON final_head.entity_kind = 'series'
                    AND final_head.entity_id = final_series.id
                    AND final_head.series_id = final_series.id
                    AND final_head.roster_id = final_series.roster_id
                    AND final_head.current_revision_id = result_dependency.official_result_revision_id
                INNER JOIN official_result_revisions AS final_result
                    ON final_result.id = final_head.current_revision_id
                    AND final_result.entity_kind = final_head.entity_kind
                    AND final_result.entity_id = final_head.entity_id
                    AND final_result.series_id = final_head.series_id
                    AND final_result.roster_id = final_head.roster_id
                WHERE result_dependency.artifact_id = champion.id
                    AND result_dependency.dependency_kind = 'official_result'
                    AND final_series.state = 'completed'
                    AND final_series.current_result_revision_id = result_dependency.official_result_revision_id
                    AND final_series.winner_id IS NOT NULL
                    AND final_result.result_state = 'completed'
                    AND final_result.winner_id = final_series.winner_id
                    AND champion.payload ->> 'participant_id' = final_series.winner_id::TEXT
                    AND EXISTS (
                        SELECT 1
                        FROM projection_artifact_members AS champion_member
                        WHERE champion_member.artifact_id = champion.id
                            AND champion_member.participant_id = final_series.winner_id
                    )
                    AND EXISTS (
                        SELECT 1
                        FROM projection_dependencies AS bracket_dependency
                        INNER JOIN projection_revision_artifacts AS bracket_link
                            ON bracket_link.revision_id = champion_link.revision_id
                            AND bracket_link.artifact_kind = 'bracket'
                            AND bracket_link.artifact_id = bracket_dependency.depends_on_artifact_id
                        WHERE bracket_dependency.artifact_id = champion.id
                            AND bracket_dependency.dependency_kind = 'artifact'
                    )
                    AND (
                        SELECT COUNT(*)
                        FROM projection_dependencies AS result_count
                        WHERE result_count.artifact_id = champion.id
                            AND result_count.dependency_kind = 'official_result'
                    ) = 1
            );

        IF linked_kind_count <> base_kind_count + champion_count
            OR base_kind_count <> 3
            OR champion_count NOT IN (0, 1)
            OR dependency_missing_count <> 0
            OR invalid_member_count <> 0
            OR invalid_champion_count <> 0 THEN
            RAISE EXCEPTION 'published projection requires base artifacts and a terminal champion when present'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT state, superseded_by_revision_id
            INTO previous_state, previous_replacement_id
            FROM projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR NO KEY UPDATE;

            IF previous_state <> 'superseded'
                OR previous_replacement_id <> NEW.id THEN
                RAISE EXCEPTION 'previous projection must atomically supersede to replacement'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    ELSIF OLD.state = 'published' AND NEW.state = 'superseded' THEN
        SELECT previous_revision_id, state
        INTO replacement_previous_id, replacement_state
        FROM projection_revisions
        WHERE id = NEW.superseded_by_revision_id
        FOR NO KEY UPDATE;

        IF replacement_previous_id <> OLD.id OR replacement_state <> 'draft' THEN
            RAISE EXCEPTION 'projection supersession must target its draft successor'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: result_projection_node_guard(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.result_projection_node_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_tournament_id UUID;
    previous_roster_id UUID;
    previous_kind TEXT;
    previous_entity_id UUID;
    previous_revision_number BIGINT;
    previous_created_at TIMESTAMP WITH TIME ZONE;
    authority_tournament_id UUID;
    authority_roster_id UUID;
    authority_kind TEXT;
    authority_wave_id UUID;
    authority_stage_command_id UUID;
    stage_top4_artifact_id UUID;
    stage_bracket_artifact_id UUID;
    stage_first_semifinal_series_id UUID;
    stage_second_semifinal_series_id UUID;
    stage_progression_action TEXT;
    stage_correction_lineage BOOLEAN;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'result projection nodes are append-only evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament_id, roster_id, source_kind, wave_id, stage_command_id
    INTO authority_tournament_id, authority_roster_id, authority_kind, authority_wave_id,
        authority_stage_command_id
    FROM result_projection_node_authorities
    WHERE id = NEW.authority_id
    FOR KEY SHARE;

    IF authority_tournament_id IS NULL
        OR authority_tournament_id <> NEW.tournament_id
        OR authority_roster_id <> NEW.roster_id THEN
        RAISE EXCEPTION 'result projection node authority is outside node scope'
            USING ERRCODE = 'check_violation';
    END IF;

    IF authority_kind = 'wave_initialization' THEN
        IF NEW.artifact_kind <> 'series_score'
            OR NEW.revision_number <> 1
            OR NEW.previous_node_id IS NOT NULL
            OR NOT EXISTS (
                SELECT 1
                FROM wave_series
                WHERE wave_id = authority_wave_id
                    AND tournament_id = NEW.tournament_id
                    AND roster_id = NEW.roster_id
                    AND series_id = NEW.entity_id
            ) THEN
            RAISE EXCEPTION 'Wave provenance is valid only for its exact score genesis node'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF authority_kind = 'stage_initialization' THEN
        SELECT top4_artifact_id,
            bracket_artifact_id,
            first_semifinal_series_id,
            second_semifinal_series_id,
            progression.action,
            EXISTS (
                SELECT 1
                FROM result_correction_commits AS correction
                WHERE correction.command_id = progression.command_id
                    AND correction.tournament_id = progression.tournament_id
                    AND correction.roster_id = progression.roster_id
                    AND correction.source_projection_revision_id = progression.source_projection_revision_id
                    AND correction.source_projection_revision = progression.source_projection_revision
                    AND correction.resulting_projection_revision_id = progression.resulting_projection_revision_id
                    AND correction.resulting_projection_revision = progression.resulting_projection_revision
            )
        INTO stage_top4_artifact_id,
            stage_bracket_artifact_id,
            stage_first_semifinal_series_id,
            stage_second_semifinal_series_id,
            stage_progression_action,
            stage_correction_lineage
        FROM tournament_stage_playoff_evidence AS evidence
        INNER JOIN tournament_stage_progressions AS progression
            ON progression.command_id = evidence.command_id
            AND progression.tournament_id = evidence.tournament_id
            AND progression.roster_id = evidence.roster_id
        WHERE evidence.command_id = authority_stage_command_id
            AND evidence.tournament_id = NEW.tournament_id
            AND evidence.roster_id = NEW.roster_id
        FOR KEY SHARE OF evidence, progression;

        IF stage_top4_artifact_id IS NULL
            OR NOT (
                (NEW.artifact_kind = 'top_four' AND NEW.entity_id = NEW.tournament_id)
                OR (NEW.artifact_kind = 'bracket' AND NEW.entity_id = NEW.tournament_id)
                OR (
                    NEW.artifact_kind = 'series_score'
                    AND NEW.entity_id IN (
                        stage_first_semifinal_series_id,
                        stage_second_semifinal_series_id
                    )
                )
            )
            OR (
                NEW.artifact_kind = 'series_score'
                AND (NEW.revision_number <> 1 OR NEW.previous_node_id IS NOT NULL)
            )
            OR (
                NEW.artifact_kind IN ('top_four', 'bracket')
                AND (NEW.revision_number <> 1 OR NEW.previous_node_id IS NOT NULL)
                AND (
                    stage_progression_action IS DISTINCT FROM 'correction_start_playoffs'
                    OR stage_correction_lineage IS DISTINCT FROM TRUE
                )
            ) THEN
            RAISE EXCEPTION 'stage provenance is valid only for exact playoff publication genesis nodes'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF authority_kind IN ('normal_no_show_commit', 'operator_forfeit_commit') THEN
        IF NOT EXISTS (
            SELECT 1 FROM result_projection_node_authorities AS authority
            JOIN (
                SELECT terminal.id AS commit_id, 'normal_no_show_commit'::text AS source_kind,
                    terminal.tournament_id, terminal.roster_id, terminal.series_id,
                    terminal.series_score_revision_id AS score_id, terminal.series_result_revision_id AS result_id
                FROM normal_no_show_commits AS terminal
                UNION ALL
                SELECT terminal.id, 'operator_forfeit_commit', terminal.tournament_id, terminal.roster_id,
                    terminal.series_id, terminal.series_score_revision_id, terminal.series_result_revision_id
                FROM operator_forfeit_commits AS terminal
            ) AS terminal ON terminal.commit_id = COALESCE(authority.normal_no_show_commit_id, authority.operator_forfeit_commit_id)
                AND terminal.source_kind = authority.source_kind
                AND terminal.tournament_id = NEW.tournament_id AND terminal.roster_id = NEW.roster_id
            WHERE authority.id = NEW.authority_id AND (
                (NEW.artifact_kind = 'series_score' AND NEW.entity_id = terminal.series_id AND NEW.id = terminal.score_id
                    AND EXISTS (SELECT 1 FROM series_score_revisions AS revision WHERE revision.id = NEW.id
                        AND revision.revision_number = NEW.revision_number))
                OR (NEW.artifact_kind = 'series_result' AND NEW.entity_id = terminal.series_id AND NEW.id = terminal.result_id
                    AND EXISTS (SELECT 1 FROM official_result_revisions AS revision WHERE revision.id = NEW.id
                        AND revision.revision_number = NEW.revision_number))
                OR (authority.source_kind = 'normal_no_show_commit' AND NEW.artifact_kind = 'game_result'
                    AND EXISTS (SELECT 1 FROM normal_no_show_commit_games AS game
                        JOIN official_result_revisions AS revision ON revision.id = game.game_result_revision_id
                        WHERE game.commit_id = terminal.commit_id AND game.game_attempt_id = NEW.entity_id
                            AND game.game_result_revision_id = NEW.id AND revision.revision_number = NEW.revision_number))
            )
        ) THEN
            RAISE EXCEPTION 'terminal projection node does not match its exact commit revision'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF authority_kind NOT IN ('result_commit', 'correction_commit') THEN
        RAISE EXCEPTION 'unknown result projection node authority kind'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_node_id IS NULL THEN
        IF NEW.revision_number <> 1 THEN
            RAISE EXCEPTION 'initial result projection node must start at revision one'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    SELECT tournament_id, roster_id, artifact_kind, entity_id, revision_number, created_at
    INTO previous_tournament_id, previous_roster_id, previous_kind, previous_entity_id,
        previous_revision_number, previous_created_at
    FROM result_projection_nodes
    WHERE id = NEW.previous_node_id
    FOR KEY SHARE;

    IF previous_revision_number IS NULL
        OR NEW.tournament_id <> previous_tournament_id
        OR NEW.roster_id <> previous_roster_id
        OR NEW.artifact_kind <> previous_kind
        OR NEW.entity_id <> previous_entity_id
        OR NEW.revision_number <> previous_revision_number + 1
        OR NEW.created_at < previous_created_at THEN
        RAISE EXCEPTION 'invalid result projection node lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- Result-projection nodes are server-owned evidence for both ordinary result
-- settlement, operator correction, Wave genesis, or one exact stage
-- publication. Their provenance must name one durable source with the same
-- tournament and roster; a node is never allowed to borrow another command.
CREATE FUNCTION public.result_projection_node_authority_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_tournament_id UUID;
    source_roster_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'result projection node authorities are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.result_commit_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM result_commits
        WHERE id = NEW.result_commit_id
        FOR KEY SHARE;
    ELSIF NEW.normal_no_show_commit_id IS NOT NULL OR NEW.operator_forfeit_commit_id IS NOT NULL THEN
        SELECT terminal.tournament_id, terminal.roster_id
        INTO source_tournament_id, source_roster_id
        FROM (
            SELECT id, tournament_id, roster_id, series_id, outbox_event_id, projection_evidence_id
            FROM normal_no_show_commits WHERE id = NEW.normal_no_show_commit_id
            UNION ALL
            SELECT id, tournament_id, roster_id, series_id, outbox_event_id, projection_evidence_id
            FROM operator_forfeit_commits WHERE id = NEW.operator_forfeit_commit_id
        ) AS terminal
        JOIN outbox_events AS event ON event.id = terminal.outbox_event_id
            AND event.tournament_id = terminal.tournament_id AND event.roster_id = terminal.roster_id
            AND event.projection_revision_id = terminal.projection_evidence_id
        JOIN projection_revisions AS publication ON publication.id = event.projection_revision_id
            AND publication.tournament_id = terminal.tournament_id AND publication.roster_id = terminal.roster_id
            AND publication.revision_number = event.projection_revision AND publication.state IN ('published', 'superseded')
        JOIN outbox_result_sources AS binding ON binding.outbox_event_id = event.id
            AND binding.tournament_id = terminal.tournament_id AND binding.roster_id = terminal.roster_id
            AND binding.series_id = terminal.series_id AND binding.projection_evidence_id = terminal.projection_evidence_id;
    ELSIF NEW.correction_command_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM result_correction_commits
        WHERE command_id = NEW.correction_command_id
        FOR KEY SHARE;
    ELSIF NEW.stage_command_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM tournament_stage_playoff_evidence
        WHERE command_id = NEW.stage_command_id
            AND tournament_id = NEW.tournament_id
        FOR KEY SHARE;
    ELSE
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM waves
        WHERE id = NEW.wave_id
        FOR KEY SHARE;
    END IF;

    IF source_tournament_id IS NULL
        OR source_roster_id IS NULL
        OR source_tournament_id <> NEW.tournament_id
        OR source_roster_id <> NEW.roster_id THEN
        RAISE EXCEPTION 'result projection node authority is outside its tournament scope'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION public.result_projection_dependency_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_authority_id UUID;
    derived_authority_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'result projection dependencies are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;
    SELECT authority_id
    INTO source_authority_id
    FROM result_projection_nodes
    WHERE id = NEW.source_node_id
    FOR KEY SHARE;
    SELECT authority_id
    INTO derived_authority_id
    FROM result_projection_nodes
    WHERE id = NEW.derived_node_id
    FOR KEY SHARE;
    IF source_authority_id IS NULL
        OR derived_authority_id IS NULL
        OR source_authority_id <> NEW.authority_id
        OR derived_authority_id <> NEW.authority_id THEN
        RAISE EXCEPTION 'result projection dependency crosses provenance authority'
            USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
        WITH RECURSIVE descendants(node_id, path) AS (
            SELECT NEW.derived_node_id, ARRAY[NEW.derived_node_id]::uuid[]

            UNION ALL

            SELECT dependency.derived_node_id,
                descendants.path || dependency.derived_node_id
            FROM descendants
            INNER JOIN result_projection_dependencies AS dependency
                ON dependency.source_node_id = descendants.node_id
                AND dependency.authority_id = NEW.authority_id
            WHERE NOT dependency.derived_node_id = ANY(descendants.path)
        )
        SELECT 1
        FROM descendants
        WHERE node_id = NEW.source_node_id
    ) THEN
        RAISE EXCEPTION 'result projection dependency must remain acyclic'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

--
-- Name: final_swiss_projection_receipt_chain_guard(); Type: FUNCTION; Schema: public; Owner: -
--

-- A Final Swiss receipt is a server-owned snapshot boundary. It binds the
-- canonical logical standings identity to one published physical projection
-- revision. Later corrections publish a new physical artifact, but must carry
-- the same logical identity and name the immediately superseded receipt.
CREATE FUNCTION public.final_swiss_projection_receipt_chain_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_revision_number BIGINT;
    source_previous_revision_id UUID;
    source_revision_state VARCHAR(16);
    source_tournament_id UUID;
    source_roster_id UUID;
    source_artifact_kind VARCHAR(16);
    source_artifact_digest BYTEA;
    source_artifact_linked BOOLEAN;
    predecessor_tournament_id UUID;
    predecessor_roster_id UUID;
    predecessor_projection_id UUID;
    predecessor_receipt_revision BIGINT;
    predecessor_physical_revision_number BIGINT;
    predecessor_created_at TIMESTAMP WITH TIME ZONE;
    predecessor_state VARCHAR(16);
    predecessor_replacement_id UUID;
    bridged_playoff_receipt BOOLEAN;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'final Swiss projection receipts are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision.revision_number,
        revision.previous_revision_id,
        revision.state,
        revision.tournament_id,
        revision.roster_id,
        artifact.artifact_kind,
        artifact.payload_digest,
        EXISTS (
            SELECT 1
            FROM projection_revision_artifacts AS revision_artifact
            WHERE revision_artifact.revision_id = revision.id
                AND revision_artifact.tournament_id = revision.tournament_id
                AND revision_artifact.roster_id = revision.roster_id
                AND revision_artifact.artifact_kind = 'standings'
                AND revision_artifact.artifact_id = artifact.id
        )
    INTO source_revision_number,
        source_previous_revision_id,
        source_revision_state,
        source_tournament_id,
        source_roster_id,
        source_artifact_kind,
        source_artifact_digest,
        source_artifact_linked
    FROM projection_revisions AS revision
    INNER JOIN projection_artifacts AS artifact
        ON artifact.id = NEW.source_standings_artifact_id
        AND artifact.tournament_id = revision.tournament_id
        AND artifact.roster_id = revision.roster_id
    WHERE revision.id = NEW.projection_revision_id
    FOR KEY SHARE OF revision, artifact;

    IF source_revision_number IS NULL
        OR source_tournament_id <> NEW.tournament_id
        OR source_roster_id <> NEW.roster_id
        OR source_revision_state <> 'published'
        OR source_artifact_kind <> 'standings'
        OR source_artifact_digest IS DISTINCT FROM NEW.source_standings_payload_digest
        OR source_artifact_linked IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'Final Swiss receipt source projection is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.receipt_revision = 1 THEN
        IF NEW.previous_receipt_projection_revision_id IS NOT NULL THEN
            RAISE EXCEPTION 'initial Final Swiss receipt cannot name a predecessor'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    SELECT receipt.tournament_id,
        receipt.roster_id,
        receipt.canonical_projection_id,
        receipt.receipt_revision,
        predecessor.revision_number,
        receipt.created_at,
        predecessor.state,
        predecessor.superseded_by_revision_id
    INTO predecessor_tournament_id,
        predecessor_roster_id,
        predecessor_projection_id,
        predecessor_receipt_revision,
        predecessor_physical_revision_number,
        predecessor_created_at,
        predecessor_state,
        predecessor_replacement_id
    FROM final_swiss_projection_receipts AS receipt
    INNER JOIN projection_revisions AS predecessor
        ON predecessor.id = receipt.projection_revision_id
        AND predecessor.tournament_id = receipt.tournament_id
        AND predecessor.roster_id = receipt.roster_id
    WHERE receipt.projection_revision_id = NEW.previous_receipt_projection_revision_id
    FOR KEY SHARE OF receipt, predecessor;

    IF NEW.previous_receipt_projection_revision_id IS NULL
        OR predecessor_receipt_revision IS NULL
        OR predecessor_physical_revision_number IS NULL
        OR predecessor_tournament_id <> NEW.tournament_id
        OR predecessor_roster_id <> NEW.roster_id
        OR predecessor_projection_id <> NEW.canonical_projection_id
        OR predecessor_receipt_revision <> NEW.receipt_revision - 1
        OR source_revision_number <= predecessor_physical_revision_number
        OR predecessor_state <> 'superseded'
        OR NEW.created_at < predecessor_created_at THEN
        RAISE EXCEPTION 'previous canonical Final Swiss receipt is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    IF source_previous_revision_id = NEW.previous_receipt_projection_revision_id
        AND predecessor_replacement_id = NEW.projection_revision_id THEN
        RETURN NEW;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM tournament_stage_progressions AS progression
        INNER JOIN projection_revisions AS bridge
            ON bridge.id = progression.resulting_projection_revision_id
            AND bridge.tournament_id = progression.tournament_id
            AND bridge.roster_id = progression.roster_id
        INNER JOIN result_correction_commits AS correction
            ON correction.tournament_id = progression.tournament_id
            AND correction.roster_id = progression.roster_id
            AND correction.source_projection_revision_id = bridge.id
            AND correction.source_projection_revision = bridge.revision_number
            AND correction.resulting_projection_revision_id = NEW.projection_revision_id
            AND correction.resulting_projection_revision = source_revision_number
        WHERE progression.tournament_id = NEW.tournament_id
            AND progression.roster_id = NEW.roster_id
            AND progression.action = 'start_playoffs'
            AND progression.source_projection_revision_id = NEW.previous_receipt_projection_revision_id
            AND progression.source_projection_revision = predecessor_physical_revision_number
            AND progression.resulting_projection_revision_id = bridge.id
            AND progression.resulting_projection_revision = bridge.revision_number
            AND bridge.previous_revision_id = NEW.previous_receipt_projection_revision_id
            AND bridge.revision_number = predecessor_physical_revision_number + 1
            AND bridge.state = 'superseded'
            AND bridge.superseded_by_revision_id = NEW.projection_revision_id
            AND predecessor_replacement_id = bridge.id
            AND source_previous_revision_id = bridge.id
            AND source_revision_number = bridge.revision_number + 1
            AND progression.executed_at >= predecessor_created_at
            AND progression.executed_at <= correction.executed_at
            AND correction.executed_at <= NEW.created_at
    ) INTO bridged_playoff_receipt;

    IF bridged_playoff_receipt IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'previous canonical Final Swiss receipt is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

--
-- Name: validate_final_swiss_projection_receipt(); Type: FUNCTION; Schema: public; Owner: -
--

-- The root cannot become durable without the full normalized read set. This
-- runs deferred because the immutable child rows are inserted in the same
-- outer publication transaction.
CREATE FUNCTION public.validate_final_swiss_projection_receipt() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    expected_participants INTEGER;
    retained_participants INTEGER;
    expected_rounds INTEGER;
    retained_rounds INTEGER;
    expected_series INTEGER;
    retained_series INTEGER;
    expected_games INTEGER;
    retained_games INTEGER;
    expected_ledger INTEGER;
    retained_ledger INTEGER;
BEGIN
    SELECT COUNT(*)
    INTO expected_participants
    FROM participants
    WHERE roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO retained_participants
    FROM final_swiss_projection_receipt_participants
    WHERE projection_revision_id = NEW.projection_revision_id;

    SELECT COUNT(*)
    INTO expected_rounds
    FROM swiss_round_lock_proofs
    WHERE tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO retained_rounds
    FROM final_swiss_projection_receipt_rounds
    WHERE projection_revision_id = NEW.projection_revision_id;

    SELECT COUNT(*)
    INTO expected_series
    FROM swiss_round_lock_proof_series AS proof_series
    INNER JOIN swiss_round_lock_proofs AS proof
        ON proof.round_id = proof_series.round_id
        AND proof.roster_id = proof_series.roster_id
    WHERE proof.tournament_id = NEW.tournament_id
        AND proof.roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO retained_series
    FROM final_swiss_projection_receipt_series
    WHERE projection_revision_id = NEW.projection_revision_id;

    -- Only Game attempts with a durable terminal result head belong in the
    -- receipt. Pre-start forfeits have no Game child by design, while played
    -- and normal no-show paths must retain every exact terminal Game.
    SELECT COUNT(*)
    INTO expected_games
    FROM game_attempts AS attempt
    INNER JOIN swiss_round_lock_proof_series AS proof_series
        ON proof_series.series_id = attempt.series_id
        AND proof_series.roster_id = attempt.roster_id
    INNER JOIN swiss_round_lock_proofs AS proof
        ON proof.round_id = proof_series.round_id
        AND proof.roster_id = proof_series.roster_id
    WHERE proof.tournament_id = NEW.tournament_id
        AND proof.roster_id = NEW.roster_id
        AND attempt.result_revision_id IS NOT NULL;

    SELECT COUNT(*)
    INTO retained_games
    FROM final_swiss_projection_receipt_games
    WHERE projection_revision_id = NEW.projection_revision_id;

    SELECT COUNT(*)
    INTO expected_ledger
    FROM swiss_point_ledger_entries
    WHERE tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id;

    SELECT COUNT(*)
    INTO retained_ledger
    FROM final_swiss_projection_receipt_ledger_entries
    WHERE projection_revision_id = NEW.projection_revision_id;

    IF expected_participants < 1
        OR retained_participants <> expected_participants
        OR expected_rounds < 1
        OR retained_rounds <> expected_rounds
        OR expected_series < 1
        OR retained_series <> expected_series
        OR retained_games <> expected_games
        OR expected_ledger < 1
        OR retained_ledger <> expected_ledger THEN
        RAISE EXCEPTION 'Final Swiss receipt normalized evidence is incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM final_swiss_projection_receipt_participants AS receipt_participant
        INNER JOIN participants AS participant
            ON participant.id = receipt_participant.participant_id
            AND participant.roster_id = receipt_participant.roster_id
        WHERE receipt_participant.projection_revision_id = NEW.projection_revision_id
            AND receipt_participant.stable_seed <> participant.seed
    )
    OR EXISTS (
        SELECT 1
        FROM final_swiss_projection_receipt_rounds AS receipt_round
        INNER JOIN swiss_rounds AS swiss_round
            ON swiss_round.id = receipt_round.round_id
            AND swiss_round.roster_id = receipt_round.roster_id
        WHERE receipt_round.projection_revision_id = NEW.projection_revision_id
            AND receipt_round.round_number <> swiss_round.round_number
    )
    OR EXISTS (
        SELECT 1
        FROM final_swiss_projection_receipt_series AS receipt_series
        INNER JOIN series
            ON series.id = receipt_series.series_id
            AND series.tournament_id = receipt_series.tournament_id
            AND series.roster_id = receipt_series.roster_id
        INNER JOIN series_score_heads AS score_head
            ON score_head.series_id = receipt_series.series_id
            AND score_head.roster_id = receipt_series.roster_id
        INNER JOIN official_result_heads AS series_head
            ON series_head.entity_kind = 'series'
            AND series_head.entity_id = receipt_series.series_id
            AND series_head.series_id = receipt_series.series_id
            AND series_head.roster_id = receipt_series.roster_id
        WHERE receipt_series.projection_revision_id = NEW.projection_revision_id
            AND (
                series.current_result_revision_id IS DISTINCT FROM receipt_series.series_result_revision_id
                OR series.current_score_revision_id IS DISTINCT FROM receipt_series.score_revision_id
                OR score_head.current_revision_id IS DISTINCT FROM receipt_series.score_revision_id
                OR series_head.current_revision_id IS DISTINCT FROM receipt_series.series_result_revision_id
            )
    )
    OR EXISTS (
        SELECT 1
        FROM final_swiss_projection_receipt_games AS receipt_game
        INNER JOIN game_attempts AS attempt
            ON attempt.id = receipt_game.game_attempt_id
            AND attempt.series_id = receipt_game.series_id
            AND attempt.roster_id = receipt_game.roster_id
        INNER JOIN official_result_heads AS game_head
            ON game_head.entity_kind = 'game_attempt'
            AND game_head.entity_id = receipt_game.game_attempt_id
            AND game_head.series_id = receipt_game.series_id
            AND game_head.roster_id = receipt_game.roster_id
        WHERE receipt_game.projection_revision_id = NEW.projection_revision_id
            AND (
                attempt.result_revision_id IS DISTINCT FROM receipt_game.game_result_revision_id
                OR game_head.current_revision_id IS DISTINCT FROM receipt_game.game_result_revision_id
            )
    ) THEN
        RAISE EXCEPTION 'Final Swiss receipt normalized identity is stale or incomplete'
            USING ERRCODE = 'check_violation';
    END IF;

    -- Global UUID existence is not sufficient: each retained logical node
    -- must represent this exact scoped immutable result/score revision.
    IF EXISTS (
        SELECT 1
        FROM (
            SELECT tournament_id, roster_id, series_id, 'series_result'::text AS kind,
                series_id AS entity_id, series_result_revision_id AS source_id,
                series_result_node_id AS node_id
            FROM final_swiss_projection_receipt_series
            WHERE projection_revision_id = NEW.projection_revision_id
            UNION ALL
            SELECT tournament_id, roster_id, series_id, 'series_score', series_id,
                score_revision_id, score_node_id
            FROM final_swiss_projection_receipt_series
            WHERE projection_revision_id = NEW.projection_revision_id
            UNION ALL
            SELECT tournament_id, roster_id, series_id, 'game_result', game_attempt_id,
                game_result_revision_id, game_result_node_id
            FROM final_swiss_projection_receipt_games
            WHERE projection_revision_id = NEW.projection_revision_id
        ) AS retained
        WHERE NOT EXISTS (
            SELECT 1
            FROM result_projection_nodes AS node
            LEFT JOIN correction_projection_bindings AS binding
                ON binding.tournament_id = retained.tournament_id
                AND binding.roster_id = retained.roster_id
                AND binding.artifact_kind = retained.kind
                AND binding.entity_id = retained.entity_id
                AND binding.source_id = retained.source_id
            WHERE node.id = retained.node_id
                AND node.id = COALESCE(binding.node_id, retained.source_id)
                AND node.tournament_id = retained.tournament_id
                AND node.roster_id = retained.roster_id
                AND node.artifact_kind = retained.kind
                AND node.entity_id = retained.entity_id
                AND (
                    (retained.kind = 'series_score' AND EXISTS (
                        SELECT 1 FROM series_score_revisions AS revision
                        WHERE revision.id = retained.source_id
                            AND revision.tournament_id = retained.tournament_id
                            AND revision.roster_id = retained.roster_id
                            AND revision.series_id = retained.series_id
                            AND revision.revision_number = node.revision_number
                    ))
                    OR (retained.kind <> 'series_score' AND EXISTS (
                        SELECT 1 FROM official_result_revisions AS revision
                        WHERE revision.id = retained.source_id
                            AND revision.tournament_id = retained.tournament_id
                            AND revision.roster_id = retained.roster_id
                            AND revision.series_id = retained.series_id
                            AND revision.entity_id = retained.entity_id
                            AND revision.entity_kind = CASE retained.kind WHEN 'game_result' THEN 'game_attempt' ELSE 'series' END
                            AND revision.revision_number = node.revision_number
                    ))
                )
        )
    ) OR EXISTS (
        SELECT 1 FROM final_swiss_projection_receipt_series AS retained
        WHERE retained.projection_revision_id = NEW.projection_revision_id
            AND (
                (retained.terminal_source = 'normal_no_show' AND NOT EXISTS (
                    SELECT 1 FROM normal_no_show_commits AS terminal
                    JOIN swiss_round_lock_proofs AS proof ON proof.round_id = retained.round_id
                        AND proof.tournament_id = retained.tournament_id AND proof.roster_id = retained.roster_id
                    WHERE terminal.id = retained.normal_no_show_commit_id
                        AND terminal.tournament_id = retained.tournament_id AND terminal.roster_id = retained.roster_id
                        AND terminal.series_id = retained.series_id AND terminal.wave_id = proof.wave_id
                        AND terminal.series_score_revision_id = retained.score_revision_id
                        AND terminal.series_result_revision_id = retained.series_result_revision_id
                        AND NOT EXISTS (
                            SELECT 1 FROM final_swiss_projection_receipt_games AS game
                            WHERE game.projection_revision_id = retained.projection_revision_id AND game.series_id = retained.series_id
                                AND NOT EXISTS (
                                    SELECT 1 FROM normal_no_show_commit_games AS child
                                    WHERE child.commit_id = terminal.id AND child.game_attempt_id = game.game_attempt_id
                                        AND child.game_result_revision_id = game.game_result_revision_id
                                )
                        )
                ))
                OR (retained.terminal_source = 'pre_start_forfeit' AND NOT EXISTS (
                    SELECT 1 FROM operator_forfeit_commits AS terminal
                    WHERE terminal.id = retained.operator_forfeit_commit_id
                        AND terminal.tournament_id = retained.tournament_id AND terminal.roster_id = retained.roster_id
                        AND terminal.series_id = retained.series_id
                        AND terminal.series_score_revision_id = retained.score_revision_id
                        AND terminal.series_result_revision_id = retained.series_result_revision_id
                ))
            )
    ) THEN
        RAISE EXCEPTION 'Final Swiss receipt logical node or terminal commit binding is invalid'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: projection_artifact_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projection_artifact_members (
    artifact_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(16) NOT NULL,
    participant_id uuid NOT NULL,
    "position" integer NOT NULL,
    score numeric(12,3),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projection_artifact_members_position_check CHECK ((("position" >= 1) AND ((((artifact_kind)::text = 'standings'::text) AND (score IS NOT NULL) AND (score >= (0)::numeric)) OR (((artifact_kind)::text = 'bracket'::text) AND (score IS NULL)) OR (((artifact_kind)::text = 'top_four'::text) AND ("position" <= 4) AND (score IS NULL)) OR (((artifact_kind)::text = 'champion'::text) AND ("position" = 1) AND (score IS NULL)))))
);

--
-- Name: projection_artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projection_artifacts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    produced_by_revision_id uuid NOT NULL,
    artifact_kind character varying(16) NOT NULL,
    artifact_key character varying(128) NOT NULL,
    payload json NOT NULL,
    payload_digest bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projection_artifacts_digest_check CHECK (((octet_length(payload_digest) = 32) AND (payload_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT projection_artifacts_key_check CHECK ((((artifact_key)::text = btrim((artifact_key)::text)) AND ((artifact_key)::text <> ''::text))),
    CONSTRAINT projection_artifacts_payload_check CHECK (
        (
            artifact_kind = 'standings'
            AND payload::jsonb ? 'entries'
            AND jsonb_typeof(payload::jsonb -> 'entries') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'entries') > 0
            AND NOT payload::jsonb ? 'standings'
        ) OR (
            artifact_kind = 'bracket'
            AND payload::jsonb ? 'rounds'
            AND jsonb_typeof(payload::jsonb -> 'rounds') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'rounds') > 0
            AND NOT payload::jsonb ? 'bracket'
            AND NOT payload::jsonb ? 'semifinals'
        ) OR (
            artifact_kind = 'top_four'
            AND payload::jsonb ? 'participants'
            AND jsonb_typeof(payload::jsonb -> 'participants') = 'array'
            AND jsonb_array_length(payload::jsonb -> 'participants') = 4
        ) OR (
            artifact_kind = 'champion'
            AND payload::jsonb ? 'participant_id'
            AND jsonb_typeof(payload::jsonb -> 'participant_id') = 'string'
            AND btrim(payload ->> 'participant_id') <> ''
        )
    )
);

--
-- Name: projection_cutoffs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projection_cutoffs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    sequence_number bigint NOT NULL,
    previous_cutoff_id uuid,
    source_kind character varying(24) NOT NULL,
    official_result_revision_id uuid,
    golden_position_commit_id uuid,
    stage_progression_command_id uuid,
    reason text NOT NULL,
    cutoff_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projection_cutoffs_reason_check CHECK (((reason = btrim(reason)) AND (reason <> ''::text))),
    CONSTRAINT projection_cutoffs_sequence_check CHECK (((sequence_number >= 1) AND (((sequence_number = 1) AND (previous_cutoff_id IS NULL)) OR ((sequence_number > 1) AND (previous_cutoff_id IS NOT NULL))))),
    CONSTRAINT projection_cutoffs_source_check CHECK (((((source_kind)::text = 'initial'::text) AND (official_result_revision_id IS NULL) AND (golden_position_commit_id IS NULL) AND (stage_progression_command_id IS NULL)) OR (((source_kind)::text = 'official_result'::text) AND (official_result_revision_id IS NOT NULL) AND (golden_position_commit_id IS NULL) AND (stage_progression_command_id IS NULL)) OR (((source_kind)::text = 'golden_position'::text) AND (official_result_revision_id IS NULL) AND (golden_position_commit_id IS NOT NULL) AND (stage_progression_command_id IS NULL)) OR (((source_kind)::text = 'operator_rebuild'::text) AND (official_result_revision_id IS NULL) AND (golden_position_commit_id IS NULL) AND (stage_progression_command_id IS NULL)) OR (((source_kind)::text = 'stage_progression'::text) AND (official_result_revision_id IS NULL) AND (golden_position_commit_id IS NULL) AND (stage_progression_command_id IS NOT NULL)))),
    CONSTRAINT projection_cutoffs_timestamps_check CHECK ((cutoff_at <= created_at))
);

--
-- Name: projection_dependencies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projection_dependencies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    artifact_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    dependency_kind character varying(24) NOT NULL,
    depends_on_artifact_id uuid,
    official_result_revision_id uuid,
    official_result_series_id uuid,
    golden_position_commit_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projection_dependencies_shape_check CHECK (((((dependency_kind)::text = 'artifact'::text) AND (depends_on_artifact_id IS NOT NULL) AND (official_result_revision_id IS NULL) AND (official_result_series_id IS NULL) AND (golden_position_commit_id IS NULL)) OR (((dependency_kind)::text = 'official_result'::text) AND (depends_on_artifact_id IS NULL) AND (official_result_revision_id IS NOT NULL) AND (official_result_series_id IS NOT NULL) AND (golden_position_commit_id IS NULL)) OR (((dependency_kind)::text = 'golden_position'::text) AND (depends_on_artifact_id IS NULL) AND (official_result_revision_id IS NULL) AND (official_result_series_id IS NULL) AND (golden_position_commit_id IS NOT NULL))))
);

--
-- Name: projection_revision_artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projection_revision_artifacts (
    revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(16) NOT NULL,
    artifact_id uuid NOT NULL,
    change_kind character varying(16) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projection_revision_artifacts_change_check CHECK (((change_kind)::text = ANY ((ARRAY['produced'::character varying, 'reused'::character varying])::text[])))
);

--
-- Name: projection_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projection_revisions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_revision_id uuid,
    cutoff_id uuid NOT NULL,
    state character varying(16) DEFAULT 'draft'::character varying NOT NULL,
    published_at timestamp with time zone,
    superseded_by_revision_id uuid,
    superseded_at timestamp with time zone,
    supersession_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projection_revisions_number_check CHECK (((revision_number >= 1) AND (((revision_number = 1) AND (previous_revision_id IS NULL)) OR ((revision_number > 1) AND (previous_revision_id IS NOT NULL))))),
    CONSTRAINT projection_revisions_state_check CHECK (((((state)::text = 'draft'::text) AND (published_at IS NULL) AND (superseded_by_revision_id IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'published'::text) AND (published_at IS NOT NULL) AND (superseded_by_revision_id IS NULL) AND (superseded_at IS NULL) AND (supersession_reason IS NULL)) OR (((state)::text = 'superseded'::text) AND (published_at IS NOT NULL) AND (superseded_by_revision_id IS NOT NULL) AND (superseded_at IS NOT NULL) AND (supersession_reason = btrim(supersession_reason)) AND (supersession_reason <> ''::text)))),
    CONSTRAINT projection_revisions_timestamps_check CHECK ((((published_at IS NULL) OR (published_at >= created_at)) AND ((superseded_at IS NULL) OR (superseded_at >= published_at))))
);

-- Reject secrets and identity-bearing task data from public realtime payloads.
CREATE FUNCTION public.outbox_payload_has_private_field(value jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $$
DECLARE
    item JSONB;
    item_key TEXT;
BEGIN
    IF jsonb_typeof(value) = 'object' THEN
        FOR item_key, item IN SELECT * FROM jsonb_each(value)
        LOOP
            IF LOWER(item_key) IN (
                'flag',
                'submitted_flag',
                'expected_flag',
                'secret',
                'token',
                'session_token',
                'task',
                'task_snapshot',
                'assignment',
                'participant_id',
                'operator_id',
                'source_url',
                'presigned_url',
                'payload_digest'
            ) OR public.outbox_payload_has_private_field(item) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    ELSIF jsonb_typeof(value) = 'array' THEN
        FOR item IN SELECT * FROM jsonb_array_elements(value)
        LOOP
            IF public.outbox_payload_has_private_field(item) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    END IF;

    RETURN FALSE;
END;
$$;

-- Cross-domain realtime delivery core. Source-specific evidence is held only
-- in the normalized binding tables below.
CREATE TABLE public.outbox_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    sequence bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    terminal boolean DEFAULT false NOT NULL,
    idempotency_key uuid NOT NULL,
    audience character varying(16) DEFAULT 'all'::character varying NOT NULL,
    principal_id uuid,
    topic character varying(128) NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    claimed_by uuid,
    claim_token uuid,
    claimed_until timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    last_error character varying(96),
    published_at timestamp with time zone,
    CONSTRAINT outbox_events_attempt_check CHECK (attempt_count >= 0),
    CONSTRAINT outbox_events_audience_check CHECK (
        (audience IN ('all', 'public') AND principal_id IS NULL)
        OR (audience IN ('participant', 'operator') AND principal_id IS NOT NULL)
    ),
    CONSTRAINT outbox_events_claim_check CHECK (
        (claimed_by IS NULL AND claim_token IS NULL AND claimed_until IS NULL)
        OR (claimed_by IS NOT NULL AND claim_token IS NOT NULL AND claimed_until IS NOT NULL AND claimed_until > available_at)
    ),
    CONSTRAINT outbox_events_error_check CHECK (last_error IS NULL OR (last_error = btrim(last_error) AND last_error <> '')),
    CONSTRAINT outbox_events_projection_binding_check CHECK (
        projection_revision >= 1
        AND projection_ordinal >= 1
    ),
    CONSTRAINT outbox_events_payload_check CHECK (
        jsonb_typeof(payload) = 'object'
        AND payload <> '{}'::jsonb
        AND (audience NOT IN ('all', 'public') OR NOT public.outbox_payload_has_private_field(payload))
    ),
    CONSTRAINT outbox_events_publish_check CHECK (published_at IS NULL OR published_at >= created_at),
    CONSTRAINT outbox_events_sequence_check CHECK (sequence >= 1),
    CONSTRAINT outbox_events_timestamps_check CHECK (created_at <= available_at),
    CONSTRAINT outbox_events_topic_check CHECK (topic = btrim(topic) AND topic <> '')
);

-- Sequence allocation is deliberately separate from the delivery rows.  A
-- row-level increment remains correct under READ COMMITTED when concurrent
-- producers append events for the same Tournament.
CREATE TABLE public.tournament_outbox_cursors (
    tournament_id uuid NOT NULL,
    next_sequence bigint NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_outbox_cursors_next_sequence_check CHECK (next_sequence >= 1)
);

-- Projection ordinals are allocated when a draft projection receives its
-- first bound outbox batch.  The row is also the replay guard for a later
-- terminal publication of that projection.
CREATE TABLE public.projection_outbox_cursors (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    next_ordinal integer NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT projection_outbox_cursors_next_ordinal_check CHECK (
        next_ordinal >= 1
        AND next_ordinal <= 32768
    )
);

CREATE TABLE public.outbox_result_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    result_event_id uuid NOT NULL,
    projection_evidence_id uuid NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT outbox_result_sources_projection_check CHECK (
        projection_revision >= 1
        AND projection_ordinal >= 1
    )
);

CREATE TABLE public.outbox_tournament_cancellation_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    cancellation_command_id uuid NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT outbox_tournament_cancellation_sources_projection_check CHECK (
        projection_revision >= 1
        AND projection_ordinal >= 1
    )
);

CREATE TABLE public.outbox_wave_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    wave_id uuid NOT NULL,
    wave_revision_id uuid NOT NULL,
    wave_revision bigint NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT outbox_wave_sources_revision_check CHECK (
        wave_revision >= 1
        AND projection_revision >= 1
        AND projection_ordinal >= 1
    )
);

CREATE TABLE public.outbox_champion_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    final_series_id uuid NOT NULL,
    final_result_revision_id uuid NOT NULL,
    champion_artifact_id uuid NOT NULL,
    artifact_kind character varying(16) DEFAULT 'champion'::character varying NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT outbox_champion_sources_projection_check CHECK (
        projection_revision >= 1
        AND projection_ordinal >= 1
        AND artifact_kind = 'champion'
    )
);

CREATE TABLE public.outbox_stage_projection_sources (
    outbox_event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    stage_command_id uuid NOT NULL,
    projection_revision_id uuid NOT NULL,
    projection_revision bigint NOT NULL,
    projection_ordinal smallint NOT NULL,
    swiss_receipt_projection_revision_id uuid NOT NULL,
    top4_artifact_id uuid NOT NULL,
    top4_kind character varying(16) DEFAULT 'top_four' NOT NULL,
    bracket_artifact_id uuid NOT NULL,
    bracket_kind character varying(16) DEFAULT 'bracket' NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT outbox_stage_projection_sources_identity_check CHECK (
        projection_revision >= 1 AND projection_ordinal >= 1
        AND top4_kind = 'top_four' AND bracket_kind = 'bracket'
        AND top4_artifact_id <> bracket_artifact_id
    )
);

CREATE TABLE public.realtime_subscribers (
    id uuid NOT NULL,
    instance_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    connection_generation bigint NOT NULL,
    tournament_id uuid NOT NULL,
    role character varying(16) NOT NULL,
    principal_id uuid,
    initial_sequence bigint NOT NULL,
    last_acknowledged_sequence bigint NOT NULL,
    snapshot_sequence bigint NOT NULL,
    connected_at timestamp with time zone NOT NULL,
    closed_at timestamp with time zone,
    close_reason character varying(64),
    CONSTRAINT realtime_subscribers_identity_check CHECK (
        id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND instance_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND connection_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT realtime_subscribers_close_check CHECK (
        (closed_at IS NULL AND close_reason IS NULL)
        OR (closed_at IS NOT NULL AND closed_at >= connected_at AND close_reason = btrim(close_reason) AND close_reason <> '')
    ),
    CONSTRAINT realtime_subscribers_connection_generation_check CHECK (connection_generation >= 1),
    CONSTRAINT realtime_subscribers_cursor_check CHECK (
        initial_sequence >= 0
        AND last_acknowledged_sequence >= 0
        AND snapshot_sequence >= last_acknowledged_sequence
    ),
    CONSTRAINT realtime_subscribers_role_check CHECK (
        (role = 'public' AND principal_id IS NULL)
        OR (role IN ('participant', 'operator') AND principal_id IS NOT NULL)
    )
);

CREATE TABLE public.realtime_delivery_receipts (
    subscriber_id uuid NOT NULL,
    event_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    sequence bigint NOT NULL,
    role character varying(16) NOT NULL,
    principal_id uuid,
    enqueued_at timestamp with time zone NOT NULL,
    available_at timestamp with time zone NOT NULL,
    claimed_by uuid,
    claim_token uuid,
    claimed_until timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    write_acknowledged_at timestamp with time zone,
    terminal_at timestamp with time zone,
    outcome character varying(24),
    last_error character varying(96),
    CONSTRAINT realtime_delivery_receipts_attempt_check CHECK (attempt_count >= 0),
    CONSTRAINT realtime_delivery_receipts_claim_check CHECK (
        (claimed_by IS NULL AND claim_token IS NULL AND claimed_until IS NULL)
        OR (claimed_by IS NOT NULL AND claim_token IS NOT NULL AND claimed_until IS NOT NULL AND claimed_until > available_at)
    ),
    CONSTRAINT realtime_delivery_receipts_error_check CHECK (last_error IS NULL OR (last_error = btrim(last_error) AND last_error <> '')),
    CONSTRAINT realtime_delivery_receipts_role_check CHECK (
        (role = 'public' AND principal_id IS NULL)
        OR (role IN ('participant', 'operator') AND principal_id IS NOT NULL)
    ),
    CONSTRAINT realtime_delivery_receipts_state_check CHECK (
        (terminal_at IS NULL AND write_acknowledged_at IS NULL AND outcome IS NULL)
        OR (
            terminal_at IS NOT NULL
            AND terminal_at >= enqueued_at
            AND ((outcome = 'written' AND write_acknowledged_at IS NOT NULL AND write_acknowledged_at >= enqueued_at)
                OR (outcome = 'disconnected' AND write_acknowledged_at IS NULL))
        )
    ),
    CONSTRAINT realtime_delivery_receipts_timestamps_check CHECK (enqueued_at <= available_at)
);

CREATE FUNCTION public.outbox_event_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'outbox events are durable retained evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.roster_id IS DISTINCT FROM OLD.roster_id
        OR NEW.projection_revision_id IS DISTINCT FROM OLD.projection_revision_id
        OR NEW.projection_revision IS DISTINCT FROM OLD.projection_revision
        OR NEW.sequence IS DISTINCT FROM OLD.sequence
        OR NEW.projection_ordinal IS DISTINCT FROM OLD.projection_ordinal
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.audience IS DISTINCT FROM OLD.audience
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.topic IS DISTINCT FROM OLD.topic
        OR NEW.payload IS DISTINCT FROM OLD.payload
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.published_at IS NOT NULL THEN
        RAISE EXCEPTION 'invalid outbox evidence transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.terminal IS DISTINCT FROM OLD.terminal THEN
        IF OLD.terminal
            OR NOT NEW.terminal
            OR OLD.claim_token IS NOT NULL
            OR NEW.claim_token IS NOT NULL
            OR NEW.claimed_by IS DISTINCT FROM OLD.claimed_by
            OR NEW.claimed_until IS DISTINCT FROM OLD.claimed_until
            OR NEW.attempt_count IS DISTINCT FROM OLD.attempt_count
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error
            OR NEW.published_at IS NOT NULL THEN
            RAISE EXCEPTION 'invalid outbox terminal promotion'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.published_at IS NOT NULL THEN
        IF OLD.claim_token IS NULL
            OR NEW.claimed_by IS NOT NULL
            OR NEW.claim_token IS NOT NULL
            OR NEW.claimed_until IS NOT NULL
            OR NEW.attempt_count <> OLD.attempt_count
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error THEN
            RAISE EXCEPTION 'invalid outbox acknowledgement transition'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.claim_token IS NULL AND NEW.claim_token IS NOT NULL THEN
        IF NEW.claimed_by IS NULL
            OR NEW.claimed_until IS NULL
            OR NEW.attempt_count <> OLD.attempt_count + 1
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error THEN
            RAISE EXCEPTION 'invalid outbox claim transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.claim_token IS NOT NULL AND NEW.claim_token IS NULL THEN
        IF NEW.claimed_by IS NOT NULL
            OR NEW.claimed_until IS NOT NULL
            OR NEW.attempt_count <> OLD.attempt_count
            OR NEW.available_at < OLD.available_at THEN
            RAISE EXCEPTION 'invalid outbox release transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.claim_token IS NOT NULL AND NEW.claim_token IS NOT NULL THEN
        IF (
            NEW.claimed_by IS NOT DISTINCT FROM OLD.claimed_by
            AND NEW.claim_token IS NOT DISTINCT FROM OLD.claim_token
            AND NEW.claimed_until >= OLD.claimed_until
            AND NEW.attempt_count = OLD.attempt_count
        ) IS NOT TRUE
            AND (
                NEW.claimed_until > OLD.claimed_until
                AND NEW.attempt_count = OLD.attempt_count + 1
            ) IS NOT TRUE
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error THEN
            RAISE EXCEPTION 'invalid outbox lease transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'invalid unclaimed outbox transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_outbox_event_source() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_count INTEGER;
    target_projection_revision BIGINT;
    target_projection_state VARCHAR(16);
BEGIN
    SELECT
        (SELECT COUNT(*) FROM outbox_result_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_tournament_cancellation_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_wave_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_champion_sources WHERE outbox_event_id = NEW.id)
        + (SELECT COUNT(*) FROM outbox_stage_projection_sources WHERE outbox_event_id = NEW.id)
    INTO source_count;

    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number, state
    INTO target_projection_revision, target_projection_state
    FROM projection_revisions
    WHERE id = NEW.projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR SHARE;

    IF target_projection_revision IS DISTINCT FROM NEW.projection_revision
        OR target_projection_state NOT IN ('published', 'superseded') THEN
        RAISE EXCEPTION 'outbox event target is not an exact published projection'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_result_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
                OR NEW.terminal
            )
    ) THEN
        RAISE EXCEPTION 'result outbox source is not an exact initial event binding'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_tournament_cancellation_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NOT NEW.terminal
                OR NEW.topic <> 'tournament.cancelled'
                OR NEW.audience <> 'all'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'tournament cancellation outbox source is malformed'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_wave_sources AS source
        WHERE source.outbox_event_id = NEW.id
        AND (
                NEW.terminal
                OR NEW.topic <> 'wave.disclosed'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'wave outbox source is not bound to its published projection lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM outbox_champion_sources AS source
        WHERE source.outbox_event_id = NEW.id
            AND (
                NOT NEW.terminal
                OR NEW.topic <> 'tournament.champion.published'
                OR NEW.audience <> 'all'
                OR source.tournament_id IS DISTINCT FROM NEW.tournament_id
                OR source.roster_id IS DISTINCT FROM NEW.roster_id
                OR source.created_at IS DISTINCT FROM NEW.created_at
                OR source.projection_revision_id IS DISTINCT FROM NEW.projection_revision_id
                OR source.projection_revision IS DISTINCT FROM NEW.projection_revision
                OR source.projection_ordinal IS DISTINCT FROM NEW.projection_ordinal
            )
    ) THEN
        RAISE EXCEPTION 'champion outbox source is not an exact terminal event binding'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

CREATE FUNCTION public.outbox_stage_projection_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM tournament_stage_progressions AS stage
        JOIN tournament_stage_playoff_evidence AS evidence
            ON evidence.command_id = stage.command_id
            AND evidence.tournament_id = stage.tournament_id
            AND evidence.roster_id = stage.roster_id
        JOIN final_swiss_projection_receipts AS receipt
            ON receipt.projection_revision_id = stage.source_projection_revision_id
            AND receipt.tournament_id = stage.tournament_id
            AND receipt.roster_id = stage.roster_id
        JOIN projection_revisions AS revision
            ON revision.id = evidence.published_projection_revision_id
        JOIN projection_artifacts AS top4 ON top4.id = evidence.top4_artifact_id
        JOIN projection_artifacts AS bracket ON bracket.id = evidence.bracket_artifact_id
        JOIN outbox_events AS event ON event.id = NEW.outbox_event_id
        WHERE stage.command_id = NEW.stage_command_id
            AND stage.tournament_id = NEW.tournament_id
            AND stage.roster_id = NEW.roster_id
            AND stage.action = 'start_playoffs'
            AND stage.resulting_projection_revision_id = NEW.projection_revision_id
            AND stage.resulting_projection_revision = NEW.projection_revision
            AND evidence.source_projection_revision_id = receipt.projection_revision_id
            AND evidence.published_projection_revision_id = NEW.projection_revision_id
            AND evidence.published_projection_revision = NEW.projection_revision
            AND receipt.projection_revision_id = NEW.swiss_receipt_projection_revision_id
            AND evidence.top4_artifact_id = NEW.top4_artifact_id
            AND evidence.bracket_artifact_id = NEW.bracket_artifact_id
            AND top4.produced_by_revision_id = NEW.projection_revision_id
            AND bracket.produced_by_revision_id = NEW.projection_revision_id
            AND stage.executed_at = NEW.created_at
            AND event.created_at = NEW.created_at
            AND revision.state IN ('published', 'superseded')
            AND event.topic = 'tournament.playoffs.published'
            AND event.audience = 'all' AND event.principal_id IS NULL AND event.terminal
            AND event.payload = jsonb_build_object(
                'schema', 'playoffs-publication-v1',
                'stage_command_id', NEW.stage_command_id,
                'projection_revision_id', NEW.projection_revision_id,
                'projection_revision', NEW.projection_revision,
                'top4_artifact_id', NEW.top4_artifact_id,
                'top4_digest', encode(top4.payload_digest, 'hex'),
                'bracket_artifact_id', NEW.bracket_artifact_id,
                'bracket_digest', encode(bracket.payload_digest, 'hex'))
    ) THEN
        RAISE EXCEPTION 'stage outbox source is not an exact published playoff receipt'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

-- Source insertion must recheck exclusivity even after the event's initial
-- transaction. Serialize on the immutable event before counting all namespaces.
CREATE FUNCTION public.validate_outbox_source_membership() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_count INTEGER;
BEGIN
    PERFORM 1 FROM outbox_events WHERE id = NEW.outbox_event_id FOR UPDATE;
    SELECT
        (SELECT COUNT(*) FROM outbox_result_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_tournament_cancellation_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_wave_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_champion_sources WHERE outbox_event_id = NEW.outbox_event_id)
        + (SELECT COUNT(*) FROM outbox_stage_projection_sources WHERE outbox_event_id = NEW.outbox_event_id)
    INTO source_count;
    IF source_count <> 1 THEN
        RAISE EXCEPTION 'outbox event must have exactly one normalized source binding'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION public.outbox_wave_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    stored_revision_id UUID;
    stored_revision BIGINT;
    stored_projection_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'outbox wave sources are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_id, revision
    INTO stored_revision_id, stored_revision
    FROM waves
    WHERE id = NEW.wave_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF stored_revision_id IS DISTINCT FROM NEW.wave_revision_id
        OR stored_revision IS DISTINCT FROM NEW.wave_revision THEN
        RAISE EXCEPTION 'outbox wave source is not bound to the exact current Wave revision'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number
    INTO stored_projection_revision
    FROM projection_revisions
    WHERE id = NEW.projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
        AND state = 'published'
    FOR SHARE;

    IF stored_projection_revision IS DISTINCT FROM NEW.projection_revision THEN
        RAISE EXCEPTION 'outbox wave source is not bound to the exact current published projection'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.outbox_tournament_cancellation_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    cancellation_tournament_id UUID;
    cancellation_roster_id UUID;
    cancellation_outbox_event_id UUID;
    stored_projection_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'outbox cancellation sources are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT tournament_id, roster_id, outbox_event_id
    INTO cancellation_tournament_id, cancellation_roster_id, cancellation_outbox_event_id
    FROM tournament_cancellations
    WHERE command_id = NEW.cancellation_command_id
    FOR KEY SHARE;

    IF cancellation_tournament_id IS DISTINCT FROM NEW.tournament_id
        OR cancellation_roster_id IS DISTINCT FROM NEW.roster_id
        OR cancellation_outbox_event_id IS DISTINCT FROM NEW.outbox_event_id THEN
        RAISE EXCEPTION 'outbox cancellation source does not match cancellation evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision_number
    INTO stored_projection_revision
    FROM projection_revisions
    WHERE id = NEW.projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
        AND state = 'published'
    FOR SHARE;

    IF stored_projection_revision IS DISTINCT FROM NEW.projection_revision THEN
        RAISE EXCEPTION 'outbox cancellation source is not bound to the exact current published projection'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.outbox_champion_source_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    stored_series_state VARCHAR(16);
    stored_series_format VARCHAR(8);
    stored_series_winner_id UUID;
    stored_result_state VARCHAR(16);
    stored_result_winner_id UUID;
    stored_artifact_kind VARCHAR(16);
    stored_artifact_revision_id UUID;
    stored_artifact_winner_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'outbox champion sources are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT series.state,
        series.format,
        series.winner_id,
        result_revision.result_state,
        result_revision.winner_id
    INTO stored_series_state,
        stored_series_format,
        stored_series_winner_id,
        stored_result_state,
        stored_result_winner_id
    FROM series
    INNER JOIN tournament_stage_playoff_finals AS stage_final
        ON stage_final.final_series_id = series.id
        AND stage_final.tournament_id = series.tournament_id
        AND stage_final.roster_id = series.roster_id
    INNER JOIN tournament_stage_playoff_evidence AS stage_evidence
        ON stage_evidence.command_id = stage_final.command_id
        AND stage_evidence.tournament_id = stage_final.tournament_id
        AND stage_evidence.roster_id = stage_final.roster_id
    INNER JOIN official_result_revisions AS result_revision
        ON result_revision.id = NEW.final_result_revision_id
        AND result_revision.entity_kind = 'series'
        AND result_revision.entity_id = NEW.final_series_id
        AND result_revision.series_id = NEW.final_series_id
        AND result_revision.roster_id = NEW.roster_id
    WHERE series.id = NEW.final_series_id
        AND series.tournament_id = NEW.tournament_id
        AND series.roster_id = NEW.roster_id
        AND series.current_result_revision_id = NEW.final_result_revision_id
    FOR SHARE OF series, stage_final, stage_evidence, result_revision;

    IF stored_series_state IS DISTINCT FROM 'completed'
        OR stored_series_format IS DISTINCT FROM 'bo3'
        OR stored_series_winner_id IS NULL
        OR stored_result_state IS DISTINCT FROM 'completed'
        OR stored_result_winner_id IS DISTINCT FROM stored_series_winner_id THEN
        RAISE EXCEPTION 'champion outbox source is not bound to the exact terminal final result'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT artifact.artifact_kind,
        artifact.produced_by_revision_id,
        (artifact.payload ->> 'participant_id')::UUID
    INTO stored_artifact_kind,
        stored_artifact_revision_id,
        stored_artifact_winner_id
    FROM projection_artifacts AS artifact
    WHERE artifact.id = NEW.champion_artifact_id
        AND artifact.tournament_id = NEW.tournament_id
        AND artifact.roster_id = NEW.roster_id
    FOR SHARE;

    IF stored_artifact_kind IS DISTINCT FROM 'champion'
        OR stored_artifact_revision_id IS DISTINCT FROM NEW.projection_revision_id
        OR stored_artifact_winner_id IS DISTINCT FROM stored_series_winner_id THEN
        RAISE EXCEPTION 'champion outbox source is not bound to the exact champion projection artifact'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.realtime_delivery_receipt_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.terminal_at IS NULL
            OR OLD.claimed_by IS NOT NULL
            OR OLD.claim_token IS NOT NULL
            OR OLD.claimed_until IS NOT NULL
            OR NOT EXISTS (
                SELECT 1
                FROM realtime_subscribers AS subscriber
                WHERE subscriber.id = OLD.subscriber_id
                    AND subscriber.tournament_id = OLD.tournament_id
                    AND subscriber.closed_at IS NOT NULL
            ) THEN
            RAISE EXCEPTION 'only terminal receipts for closed subscribers may be deleted'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN OLD;
    END IF;

    IF NEW.subscriber_id IS DISTINCT FROM OLD.subscriber_id
        OR NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.sequence IS DISTINCT FROM OLD.sequence
        OR NEW.role IS DISTINCT FROM OLD.role
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.enqueued_at IS DISTINCT FROM OLD.enqueued_at
        OR OLD.terminal_at IS NOT NULL
        OR NEW.attempt_count < OLD.attempt_count THEN
        RAISE EXCEPTION 'invalid realtime delivery receipt transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.claim_token IS NULL AND NEW.claim_token IS NOT NULL THEN
        IF NEW.claimed_by IS NULL
            OR NEW.claimed_until IS NULL
            OR NEW.attempt_count <> OLD.attempt_count + 1
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error
            OR NEW.terminal_at IS NOT NULL
            OR NEW.write_acknowledged_at IS NOT NULL
            OR NEW.outcome IS NOT NULL THEN
            RAISE EXCEPTION 'invalid realtime delivery claim transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.claim_token IS NOT NULL AND NEW.claim_token IS NULL THEN
        IF NEW.claimed_by IS NOT NULL
            OR NEW.claimed_until IS NOT NULL
            OR NEW.attempt_count <> OLD.attempt_count THEN
            RAISE EXCEPTION 'invalid realtime delivery release transition'
                USING ERRCODE = 'check_violation';
        END IF;
        IF NEW.terminal_at IS NULL THEN
            IF NEW.write_acknowledged_at IS NOT NULL
                OR NEW.outcome IS NOT NULL
                OR NEW.available_at < OLD.available_at
                OR NEW.last_error IS NULL THEN
                RAISE EXCEPTION 'invalid realtime delivery retry transition'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF NEW.outcome = 'written' THEN
            IF NEW.write_acknowledged_at IS NULL
                OR NEW.available_at IS DISTINCT FROM OLD.available_at
                OR NEW.last_error IS DISTINCT FROM OLD.last_error THEN
                RAISE EXCEPTION 'invalid realtime delivery acknowledgement transition'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF NEW.outcome = 'disconnected' THEN
            IF NEW.write_acknowledged_at IS NOT NULL
                OR NEW.available_at IS DISTINCT FROM OLD.available_at
                OR NEW.last_error IS DISTINCT FROM OLD.last_error THEN
                RAISE EXCEPTION 'invalid realtime delivery disconnect transition'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSE
            RAISE EXCEPTION 'invalid realtime delivery terminal outcome'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF OLD.claim_token IS NOT NULL AND NEW.claim_token IS NOT NULL THEN
        IF NEW.claimed_until <= OLD.claimed_until
            OR NEW.attempt_count <> OLD.attempt_count + 1
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error
            OR NEW.terminal_at IS NOT NULL
            OR NEW.write_acknowledged_at IS NOT NULL
            OR NEW.outcome IS NOT NULL THEN
            RAISE EXCEPTION 'invalid realtime delivery lease transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.terminal_at IS NOT NULL AND NEW.outcome = 'disconnected' THEN
        IF NEW.claimed_by IS NOT NULL
            OR NEW.claimed_until IS NOT NULL
            OR NEW.write_acknowledged_at IS NOT NULL
            OR NEW.attempt_count <> OLD.attempt_count
            OR NEW.available_at IS DISTINCT FROM OLD.available_at
            OR NEW.last_error IS DISTINCT FROM OLD.last_error THEN
            RAISE EXCEPTION 'invalid unclaimed realtime delivery disconnect transition'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'invalid unclaimed realtime delivery transition'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.realtime_subscriber_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.closed_at IS NULL
            OR EXISTS (
                SELECT 1
                FROM realtime_delivery_receipts AS receipt
                WHERE receipt.subscriber_id = OLD.id
                    AND receipt.tournament_id = OLD.tournament_id
            ) THEN
            RAISE EXCEPTION 'only closed subscribers without retained receipts may be deleted'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN OLD;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tournament_id IS DISTINCT FROM OLD.tournament_id
        OR NEW.role IS DISTINCT FROM OLD.role
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.initial_sequence IS DISTINCT FROM OLD.initial_sequence
        OR NEW.connected_at IS DISTINCT FROM OLD.connected_at
        OR NEW.last_acknowledged_sequence < OLD.last_acknowledged_sequence
        OR NEW.snapshot_sequence < OLD.snapshot_sequence
        OR NEW.snapshot_sequence < NEW.last_acknowledged_sequence
        OR NEW.connection_generation < OLD.connection_generation THEN
        RAISE EXCEPTION 'invalid realtime subscriber transition'
            USING ERRCODE = 'check_violation';
    END IF;

    -- A resume atomically replaces the current socket fence. It deliberately
    -- keeps the durable acknowledgement cursor unchanged, so a reconnect
    -- cannot manufacture a successful write acknowledgement.
    IF NEW.connection_id IS DISTINCT FROM OLD.connection_id
        AND NEW.connection_generation = OLD.connection_generation + 1
        AND NEW.last_acknowledged_sequence = OLD.last_acknowledged_sequence
        AND NEW.snapshot_sequence >= OLD.snapshot_sequence
        AND NEW.closed_at IS NOT DISTINCT FROM OLD.closed_at
        AND NEW.close_reason IS NOT DISTINCT FROM OLD.close_reason THEN
        RETURN NEW;
    END IF;

    IF NEW.last_acknowledged_sequence > OLD.last_acknowledged_sequence
        AND NEW.instance_id IS NOT DISTINCT FROM OLD.instance_id
        AND NEW.connection_id IS NOT DISTINCT FROM OLD.connection_id
        AND NEW.connection_generation = OLD.connection_generation
        AND NEW.snapshot_sequence = GREATEST(
            OLD.snapshot_sequence,
            NEW.last_acknowledged_sequence
        )
        AND NEW.closed_at IS NOT DISTINCT FROM OLD.closed_at
        AND NEW.close_reason IS NOT DISTINCT FROM OLD.close_reason
        AND OLD.closed_at IS NULL THEN
        RETURN NEW;
    END IF;

    IF OLD.closed_at IS NULL
        AND NEW.closed_at IS NOT NULL
        AND NEW.instance_id IS NOT DISTINCT FROM OLD.instance_id
        AND NEW.connection_id IS NOT DISTINCT FROM OLD.connection_id
        AND NEW.connection_generation = OLD.connection_generation
        AND NEW.snapshot_sequence = OLD.snapshot_sequence
        AND NEW.last_acknowledged_sequence = OLD.last_acknowledged_sequence THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'invalid realtime subscriber transition'
        USING ERRCODE = 'check_violation';

END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_evidence_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'stage playoff evidence is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_golden_settlement_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_projection_revision_id UUID;
    source_projection_revision BIGINT;
    source_tournament_state TEXT;
    group_source_revision_id UUID;
    group_source_revision BIGINT;
    attempt_state TEXT;
    committed_attempt_id UUID;
    committed_participant_id UUID;
    committed_position SMALLINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'stage playoff Golden settlements are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT progression.source_projection_revision_id,
        progression.source_projection_revision,
        progression.source_tournament_state
    INTO source_projection_revision_id,
        source_projection_revision,
        source_tournament_state
    FROM tournament_stage_playoff_evidence AS evidence
    INNER JOIN tournament_stage_progressions AS progression
        ON progression.command_id = evidence.command_id
        AND progression.tournament_id = evidence.tournament_id
    WHERE evidence.command_id = NEW.command_id
        AND evidence.tournament_id = NEW.tournament_id
    FOR KEY SHARE OF evidence, progression;

    IF source_tournament_state IS DISTINCT FROM 'golden' THEN
        RAISE EXCEPTION 'Golden settlement requires a Golden source stage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT group_revision.source_projection_revision_id,
        group_revision.source_projection_revision
    INTO group_source_revision_id, group_source_revision
    FROM golden_group_revisions AS group_revision
    WHERE group_revision.revision_id = NEW.group_revision_id
        AND group_revision.tournament_id = NEW.tournament_id
        AND group_revision.roster_id = NEW.roster_id
    FOR KEY SHARE;

    SELECT attempt.state,
        position_commit.attempt_id,
        position_commit.participant_id,
        position_commit.position
    INTO attempt_state,
        committed_attempt_id,
        committed_participant_id,
        committed_position
    FROM golden_position_commits AS position_commit
    INNER JOIN golden_attempts AS attempt
        ON attempt.id = position_commit.attempt_id
        AND attempt.tournament_id = position_commit.tournament_id
        AND attempt.roster_id = position_commit.roster_id
    INNER JOIN golden_attempt_stage_groups AS attempt_group
        ON attempt_group.attempt_id = position_commit.attempt_id
        AND attempt_group.tournament_id = position_commit.tournament_id
        AND attempt_group.roster_id = position_commit.roster_id
        AND attempt_group.group_revision_id = NEW.group_revision_id
    WHERE position_commit.id = NEW.position_commit_id
        AND position_commit.tournament_id = NEW.tournament_id
        AND position_commit.roster_id = NEW.roster_id
    FOR KEY SHARE OF position_commit, attempt, attempt_group;

    IF group_source_revision_id IS DISTINCT FROM source_projection_revision_id
        OR group_source_revision IS DISTINCT FROM source_projection_revision
        OR attempt_state IS DISTINCT FROM 'completed'
        OR committed_attempt_id IS DISTINCT FROM NEW.attempt_id
        OR committed_participant_id IS DISTINCT FROM NEW.participant_id
        OR committed_position IS DISTINCT FROM NEW.position THEN
        RAISE EXCEPTION 'Golden settlement is not exact terminal group evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_semifinal_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    evidence_bracket_artifact_id UUID;
    series_state TEXT;
    series_format TEXT;
    series_started_at TIMESTAMPTZ;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'stage playoff semifinals are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT bracket_artifact_id
    INTO evidence_bracket_artifact_id
    FROM tournament_stage_playoff_evidence
    WHERE command_id = NEW.command_id
        AND tournament_id = NEW.tournament_id
    FOR KEY SHARE;

    SELECT state, format, started_at
    INTO series_state, series_format, series_started_at
    FROM series
    WHERE id = NEW.series_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF evidence_bracket_artifact_id IS DISTINCT FROM NEW.bracket_artifact_id
        OR series_state IS DISTINCT FROM 'locked'
        OR series_format IS DISTINCT FROM 'bo1'
        OR series_started_at IS NOT NULL THEN
        RAISE EXCEPTION 'playoff semifinal is not a locked unstarted BO1 bound to its bracket'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_final_advancement_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    series_state TEXT;
    series_format TEXT;
    series_winner_id UUID;
    first_participant_id UUID;
    second_participant_id UUID;
    score_head_revision_id UUID;
    result_head_revision_id UUID;
    result_state TEXT;
    result_kind TEXT;
    result_winner_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'final semifinal advancements are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT semifinal.state,
        semifinal.format,
        semifinal.winner_id,
        semifinal.first_participant_id,
        semifinal.second_participant_id,
        score_head.current_revision_id,
        result_head.current_revision_id,
        result_revision.result_state,
        result_revision.entity_kind,
        result_revision.winner_id
    INTO series_state,
        series_format,
        series_winner_id,
        first_participant_id,
        second_participant_id,
        score_head_revision_id,
        result_head_revision_id,
        result_state,
        result_kind,
        result_winner_id
    FROM series AS semifinal
    INNER JOIN series_score_heads AS score_head
        ON score_head.series_id = semifinal.id
        AND score_head.roster_id = semifinal.roster_id
    INNER JOIN official_result_heads AS result_head
        ON result_head.entity_kind = 'series'
        AND result_head.entity_id = semifinal.id
        AND result_head.series_id = semifinal.id
        AND result_head.roster_id = semifinal.roster_id
    INNER JOIN official_result_revisions AS result_revision
        ON result_revision.id = result_head.current_revision_id
        AND result_revision.series_id = semifinal.id
        AND result_revision.roster_id = semifinal.roster_id
    WHERE semifinal.id = NEW.semifinal_series_id
        AND semifinal.tournament_id = NEW.tournament_id
        AND semifinal.roster_id = NEW.roster_id
    FOR KEY SHARE OF semifinal, score_head, result_head, result_revision;

    IF series_state IS DISTINCT FROM 'completed'
        OR series_format IS DISTINCT FROM 'bo1'
        OR score_head_revision_id IS DISTINCT FROM NEW.score_revision_id
        OR result_head_revision_id IS DISTINCT FROM NEW.result_revision_id
        OR result_state IS DISTINCT FROM 'completed'
        OR result_kind IS DISTINCT FROM 'series'
        OR series_winner_id IS DISTINCT FROM NEW.winner_id
        OR result_winner_id IS DISTINCT FROM NEW.winner_id
        OR NEW.loser_id IS DISTINCT FROM (CASE
            WHEN NEW.winner_id = first_participant_id THEN second_participant_id
            WHEN NEW.winner_id = second_participant_id THEN first_participant_id
            ELSE NULL
        END) THEN
        RAISE EXCEPTION 'final advancement is not bound to the current terminal semifinal heads'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_final_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    advancement_count INTEGER;
    expected_first_participant_id UUID;
    expected_second_participant_id UUID;
    series_state TEXT;
    series_format TEXT;
    series_first_participant_id UUID;
    series_second_participant_id UUID;
    category_mode TEXT;
    category_selected JSONB;
    draft_category_revision_id UUID;
    draft_first_participant_id UUID;
    draft_second_participant_id UUID;
    draft_format TEXT;
    initial_draft_state TEXT;
    initial_draft_revision BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'final stage records are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*),
        (array_agg(advancement.winner_id) FILTER (WHERE advancement.position = 1))[1],
        (array_agg(advancement.winner_id) FILTER (WHERE advancement.position = 2))[1]
    INTO advancement_count, expected_first_participant_id, expected_second_participant_id
    FROM tournament_stage_playoff_final_advancements AS advancement
    WHERE advancement.command_id = NEW.command_id
        AND advancement.tournament_id = NEW.tournament_id
        AND advancement.roster_id = NEW.roster_id;

    SELECT final_series.state,
        final_series.format,
        final_series.first_participant_id,
        final_series.second_participant_id,
        category.mode,
        category.selected_categories,
        draft.category_revision_id,
        draft.first_participant_id,
        draft.second_participant_id,
        draft.format,
        initial_revision.state,
        initial_revision.revision
    INTO series_state,
        series_format,
        series_first_participant_id,
        series_second_participant_id,
        category_mode,
        category_selected,
        draft_category_revision_id,
        draft_first_participant_id,
        draft_second_participant_id,
        draft_format,
        initial_draft_state,
        initial_draft_revision
    FROM series AS final_series
    INNER JOIN category_revisions AS category
        ON category.id = NEW.category_revision_id
        AND category.series_id = final_series.id
        AND category.roster_id = final_series.roster_id
    INNER JOIN drafts AS draft
        ON draft.id = NEW.draft_id
        AND draft.series_id = final_series.id
        AND draft.roster_id = final_series.roster_id
    INNER JOIN draft_revisions AS initial_revision
        ON initial_revision.id = NEW.draft_initial_revision_id
        AND initial_revision.draft_id = draft.id
    WHERE final_series.id = NEW.final_series_id
        AND final_series.tournament_id = NEW.tournament_id
        AND final_series.roster_id = NEW.roster_id
    FOR KEY SHARE OF final_series, category, draft, initial_revision;

    IF advancement_count <> 2
        OR NEW.first_participant_id IS DISTINCT FROM expected_first_participant_id
        OR NEW.second_participant_id IS DISTINCT FROM expected_second_participant_id
        OR series_state IS DISTINCT FROM 'planned'
        OR series_format IS DISTINCT FROM 'bo3'
        OR series_first_participant_id IS DISTINCT FROM NEW.first_participant_id
        OR series_second_participant_id IS DISTINCT FROM NEW.second_participant_id
        OR category_mode IS DISTINCT FROM 'draft'
        OR category_selected IS DISTINCT FROM '[]'::JSONB
        OR draft_category_revision_id IS DISTINCT FROM NEW.category_revision_id
        OR NOT (
            (draft_first_participant_id IS NOT DISTINCT FROM NEW.first_participant_id
                AND draft_second_participant_id IS NOT DISTINCT FROM NEW.second_participant_id)
            OR (draft_first_participant_id IS NOT DISTINCT FROM NEW.second_participant_id
                AND draft_second_participant_id IS NOT DISTINCT FROM NEW.first_participant_id)
        )
        OR draft_format IS DISTINCT FROM 'bo3'
        OR initial_draft_state IS DISTINCT FROM 'active'
        OR initial_draft_revision IS DISTINCT FROM 1 THEN
        RAISE EXCEPTION 'final stage is not the exact planned BO3 and active draft from semifinal evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

-- Delivery history is a mutable eligibility source for an exact final-draft
-- plan. Its head is deliberately separate from the draft aggregate: a draft
-- revision does not describe receipts appended by either finalist.
CREATE FUNCTION public.final_draft_delivery_history_head_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'final draft delivery history heads follow immutable receipts'
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE final_draft_delivery_history_heads AS head
    SET revision_id = gen_random_uuid(),
        revision = head.revision + 1,
        updated_at = clock_timestamp()
    FROM tournament_stage_playoff_finals AS stage
    WHERE head.draft_id = stage.draft_id
        AND head.tournament_id = stage.tournament_id
        AND head.roster_id = NEW.roster_id
        AND NEW.participant_id IN (stage.first_participant_id, stage.second_participant_id);

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_final_initialization_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    draft_state TEXT;
    selected_categories JSONB;
    series_state TEXT;
    score_head_revision_id UUID;
    slot_number SMALLINT;
    slot_category TEXT;
    game_state TEXT;
    wave_state TEXT;
    wave_revision_id UUID;
    assignment_state TEXT;
    assignment_attempt_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'final initializations are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT completed_revision.state,
        completed_revision.selected_categories,
        final_series.state,
        score_head.current_revision_id,
        slot.slot_number,
        slot.category,
        game.state,
        wave.state,
        wave.revision_id,
        assignment.state,
        assignment.attempt_id
    INTO draft_state,
        selected_categories,
        series_state,
        score_head_revision_id,
        slot_number,
        slot_category,
        game_state,
        wave_state,
        wave_revision_id,
        assignment_state,
        assignment_attempt_id
    FROM draft_revisions AS completed_revision
    INNER JOIN series AS final_series
        ON final_series.id = NEW.final_series_id
        AND final_series.tournament_id = NEW.tournament_id
        AND final_series.roster_id = NEW.roster_id
    INNER JOIN series_score_heads AS score_head
        ON score_head.series_id = final_series.id
        AND score_head.roster_id = final_series.roster_id
    INNER JOIN game_slots AS slot
        ON slot.id = NEW.first_slot_id
        AND slot.series_id = final_series.id
        AND slot.roster_id = final_series.roster_id
    INNER JOIN game_attempts AS game
        ON game.id = NEW.first_game_id
        AND game.slot_id = slot.id
        AND game.series_id = final_series.id
        AND game.roster_id = final_series.roster_id
    INNER JOIN waves AS wave
        ON wave.id = NEW.first_wave_id
        AND wave.tournament_id = NEW.tournament_id
        AND wave.roster_id = NEW.roster_id
    INNER JOIN assignments AS assignment
        ON assignment.id = NEW.first_assignment_id
        AND assignment.attempt_id = game.id
        AND assignment.roster_id = NEW.roster_id
    WHERE completed_revision.id = NEW.completed_draft_revision_id
        AND completed_revision.draft_id = NEW.draft_id
    FOR KEY SHARE OF completed_revision, final_series, score_head, slot, game, wave, assignment;

    IF draft_state IS DISTINCT FROM 'completed'
        OR jsonb_array_length(selected_categories) <> 3
        OR series_state IS DISTINCT FROM 'active'
        OR score_head_revision_id IS DISTINCT FROM NEW.initial_score_revision_id
        OR slot_number IS DISTINCT FROM 1
        OR slot_category IS DISTINCT FROM (selected_categories ->> 0)
        OR game_state IS DISTINCT FROM 'planned'
        OR wave_state IS DISTINCT FROM 'planned'
        OR wave_revision_id IS DISTINCT FROM NEW.first_wave_revision_id
        OR assignment_state IS DISTINCT FROM 'active'
        OR assignment_attempt_id IS DISTINCT FROM NEW.first_game_id THEN
        RAISE EXCEPTION 'final initialization is not the authoritative completed-draft Game 1 graph'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.tournament_stage_playoff_final_progression_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    series_state TEXT;
    score_head_revision_id UUID;
    source_result_head_revision_id UUID;
    result_kind TEXT;
    result_state TEXT;
    source_game_state TEXT;
    source_game_slot_number SMALLINT;
    slot_number SMALLINT;
    game_state TEXT;
    wave_state TEXT;
    wave_revision_id UUID;
    assignment_state TEXT;
    assignment_attempt_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'final progressions are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT final_series.state,
        score_head.current_revision_id,
        result_head.current_revision_id,
        source_result.entity_kind,
        source_result.result_state,
        source_game.state,
        source_slot.slot_number,
        slot.slot_number,
        game.state,
        wave.state,
        wave.revision_id,
        assignment.state,
        assignment.attempt_id
    INTO series_state,
        score_head_revision_id,
        source_result_head_revision_id,
        result_kind,
        result_state,
        source_game_state,
        source_game_slot_number,
        slot_number,
        game_state,
        wave_state,
        wave_revision_id,
        assignment_state,
        assignment_attempt_id
    FROM series AS final_series
    INNER JOIN series_score_heads AS score_head
        ON score_head.series_id = final_series.id
        AND score_head.roster_id = final_series.roster_id
    INNER JOIN official_result_revisions AS source_result
        ON source_result.id = NEW.source_game_result_revision_id
        AND source_result.series_id = final_series.id
        AND source_result.roster_id = final_series.roster_id
    INNER JOIN official_result_heads AS result_head
        ON result_head.entity_kind = 'game_attempt'
        AND result_head.entity_id = source_result.entity_id
        AND result_head.series_id = final_series.id
        AND result_head.roster_id = final_series.roster_id
    INNER JOIN game_attempts AS source_game
        ON source_game.id = source_result.entity_id
        AND source_game.series_id = final_series.id
        AND source_game.roster_id = final_series.roster_id
    INNER JOIN game_slots AS source_slot
        ON source_slot.id = source_game.slot_id
        AND source_slot.series_id = final_series.id
        AND source_slot.roster_id = final_series.roster_id
    INNER JOIN game_slots AS slot
        ON slot.id = NEW.next_slot_id
        AND slot.series_id = final_series.id
        AND slot.roster_id = final_series.roster_id
    INNER JOIN game_attempts AS game
        ON game.id = NEW.next_game_id
        AND game.slot_id = slot.id
        AND game.series_id = final_series.id
        AND game.roster_id = final_series.roster_id
    INNER JOIN waves AS wave
        ON wave.id = NEW.next_wave_id
        AND wave.tournament_id = NEW.tournament_id
        AND wave.roster_id = NEW.roster_id
    INNER JOIN assignments AS assignment
        ON assignment.id = NEW.next_assignment_id
        AND assignment.attempt_id = game.id
        AND assignment.roster_id = NEW.roster_id
    WHERE final_series.id = NEW.final_series_id
        AND final_series.tournament_id = NEW.tournament_id
        AND final_series.roster_id = NEW.roster_id
    FOR KEY SHARE OF final_series, score_head, result_head, source_result, source_game, source_slot, slot, game, wave, assignment;

    IF series_state NOT IN ('active', 'ready')
        OR score_head_revision_id IS DISTINCT FROM NEW.source_score_revision_id
        OR source_result_head_revision_id IS DISTINCT FROM NEW.source_game_result_revision_id
        OR result_kind IS DISTINCT FROM 'game_attempt'
        OR result_state IS DISTINCT FROM 'completed'
        OR source_game_state IS DISTINCT FROM 'completed'
        OR source_game_slot_number IS DISTINCT FROM NEW.next_position - 1
        OR slot_number IS DISTINCT FROM NEW.next_position
        OR game_state IS DISTINCT FROM 'planned'
        OR wave_state IS DISTINCT FROM 'planned'
        OR wave_revision_id IS DISTINCT FROM NEW.next_wave_revision_id
        OR assignment_state IS DISTINCT FROM 'active'
        OR assignment_attempt_id IS DISTINCT FROM NEW.next_game_id THEN
        RAISE EXCEPTION 'final continuation is not the exact current result and planned next Game graph'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE FUNCTION public.validate_tournament_stage_playoff_evidence() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    progression_action TEXT;
    progression_source_state TEXT;
    progression_source_revision_id UUID;
    progression_source_revision BIGINT;
    progression_resulting_revision_id UUID;
    progression_resulting_revision BIGINT;
    source_state TEXT;
    source_replacement_revision_id UUID;
    published_state TEXT;
    top4_kind TEXT;
    bracket_kind TEXT;
    top4_member_count INTEGER;
    semifinal_count INTEGER;
    invalid_semifinal_count INTEGER;
    expected_golden_count INTEGER;
    missing_golden_count INTEGER;
    unexpected_golden_count INTEGER;
    missing_dependency_count INTEGER;
    stage_authority_id UUID;
    top4_node_authority_id UUID;
    bracket_node_authority_id UUID;
    top4_node_kind TEXT;
    bracket_node_kind TEXT;
    top4_node_entity_id UUID;
    bracket_node_entity_id UUID;
    top4_node_digest BYTEA;
    bracket_node_digest BYTEA;
    top4_artifact_digest BYTEA;
    bracket_artifact_digest BYTEA;
    stage_node_count INTEGER;
    stage_semifinal_score_count INTEGER;
    stage_dependency_count INTEGER;
    required_stage_dependency_count INTEGER;
    top_one UUID;
    top_two UUID;
    top_three UUID;
    top_four UUID;
BEGIN
    SELECT progression.action,
        progression.source_tournament_state,
        progression.source_projection_revision_id,
        progression.source_projection_revision,
        progression.resulting_projection_revision_id,
        progression.resulting_projection_revision
    INTO progression_action,
        progression_source_state,
        progression_source_revision_id,
        progression_source_revision,
        progression_resulting_revision_id,
        progression_resulting_revision
    FROM tournament_stage_progressions AS progression
    WHERE progression.command_id = NEW.command_id
        AND progression.tournament_id = NEW.tournament_id
    FOR KEY SHARE;

    SELECT state, superseded_by_revision_id
    INTO source_state, source_replacement_revision_id
    FROM projection_revisions
    WHERE id = NEW.source_projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
        AND revision_number = NEW.source_projection_revision
    FOR KEY SHARE;

    SELECT state INTO published_state
    FROM projection_revisions
    WHERE id = NEW.published_projection_revision_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
        AND revision_number = NEW.published_projection_revision
    FOR KEY SHARE;

    SELECT artifact_kind, payload_digest INTO top4_kind, top4_artifact_digest
    FROM projection_artifacts
    WHERE id = NEW.top4_artifact_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    SELECT artifact_kind, payload_digest INTO bracket_kind, bracket_artifact_digest
    FROM projection_artifacts
    WHERE id = NEW.bracket_artifact_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
    FOR KEY SHARE;

    IF progression_action NOT IN ('start_playoffs', 'correction_start_playoffs')
        OR progression_source_revision_id IS DISTINCT FROM NEW.source_projection_revision_id
        OR progression_source_revision IS DISTINCT FROM NEW.source_projection_revision
        OR progression_resulting_revision_id IS DISTINCT FROM NEW.published_projection_revision_id
        OR progression_resulting_revision IS DISTINCT FROM NEW.published_projection_revision
        OR NOT (
            source_state = 'published'
            OR (
                source_state = 'superseded'
                AND source_replacement_revision_id = NEW.published_projection_revision_id
            )
        )
        OR published_state IS DISTINCT FROM 'published'
        OR top4_kind IS DISTINCT FROM 'top_four'
        OR bracket_kind IS DISTINCT FROM 'bracket'
        OR NOT EXISTS (
            SELECT 1
            FROM projection_revision_artifacts
            WHERE revision_id = NEW.published_projection_revision_id
                AND artifact_kind = 'top_four'
                AND artifact_id = NEW.top4_artifact_id
        )
        OR NOT EXISTS (
            SELECT 1
            FROM projection_revision_artifacts
            WHERE revision_id = NEW.published_projection_revision_id
                AND artifact_kind = 'bracket'
                AND artifact_id = NEW.bracket_artifact_id
        ) THEN
        RAISE EXCEPTION 'stage playoff evidence is not bound to exact published projection lineage'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT authority.id,
        top4_node.authority_id,
        bracket_node.authority_id,
        top4_node.artifact_kind,
        bracket_node.artifact_kind,
        top4_node.entity_id,
        bracket_node.entity_id,
        top4_node.payload_digest,
        bracket_node.payload_digest
    INTO stage_authority_id,
        top4_node_authority_id,
        bracket_node_authority_id,
        top4_node_kind,
        bracket_node_kind,
        top4_node_entity_id,
        bracket_node_entity_id,
        top4_node_digest,
        bracket_node_digest
    FROM result_projection_node_authorities AS authority
    INNER JOIN result_projection_nodes AS top4_node
        ON top4_node.id = NEW.top4_node_id
    INNER JOIN result_projection_nodes AS bracket_node
        ON bracket_node.id = NEW.bracket_node_id
    WHERE authority.stage_command_id = NEW.command_id
        AND authority.tournament_id = NEW.tournament_id
        AND authority.roster_id = NEW.roster_id
        AND authority.source_kind = 'stage_initialization'
    FOR KEY SHARE OF authority, top4_node, bracket_node;

    IF stage_authority_id IS NULL
        OR top4_node_authority_id IS DISTINCT FROM stage_authority_id
        OR bracket_node_authority_id IS DISTINCT FROM stage_authority_id
        OR top4_node_kind IS DISTINCT FROM 'top_four'
        OR bracket_node_kind IS DISTINCT FROM 'bracket'
        OR top4_node_entity_id IS DISTINCT FROM NEW.tournament_id
        OR bracket_node_entity_id IS DISTINCT FROM NEW.tournament_id
        OR top4_node_digest IS DISTINCT FROM top4_artifact_digest
        OR bracket_node_digest IS DISTINCT FROM bracket_artifact_digest THEN
        RAISE EXCEPTION 'stage playoff evidence does not bind exact logical artifact authority'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*),
        COUNT(*) FILTER (
            WHERE node.artifact_kind = 'series_score'
                AND node.entity_id IN (
                    NEW.first_semifinal_series_id,
                    NEW.second_semifinal_series_id
                )
        )
    INTO stage_node_count, stage_semifinal_score_count
    FROM result_projection_nodes AS node
    WHERE node.authority_id = stage_authority_id;

    SELECT COUNT(*)
    INTO stage_dependency_count
    FROM result_projection_dependencies
    WHERE authority_id = stage_authority_id;

    SELECT COUNT(*)
    INTO required_stage_dependency_count
    FROM result_projection_dependencies
    WHERE authority_id = stage_authority_id
        AND (
            (source_node_id = NEW.top4_node_id AND derived_node_id = NEW.bracket_node_id)
            OR (
                source_node_id = NEW.bracket_node_id
                AND derived_node_id IN (
                    SELECT id
                    FROM result_projection_nodes
                    WHERE authority_id = stage_authority_id
                        AND artifact_kind = 'series_score'
                        AND entity_id IN (
                            NEW.first_semifinal_series_id,
                            NEW.second_semifinal_series_id
                        )
                )
            )
        );

    IF stage_node_count <> 4
        OR stage_semifinal_score_count <> 2
        OR stage_dependency_count <> 3
        OR required_stage_dependency_count <> 3 THEN
        RAISE EXCEPTION 'stage playoff provenance does not cover exact publication and semifinal genesis graph'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*) INTO top4_member_count
    FROM projection_artifact_members
    WHERE artifact_id = NEW.top4_artifact_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id
        AND artifact_kind = 'top_four';

    SELECT participant_id INTO top_one
    FROM projection_artifact_members
    WHERE artifact_id = NEW.top4_artifact_id
        AND position = 1;

    SELECT participant_id INTO top_two
    FROM projection_artifact_members
    WHERE artifact_id = NEW.top4_artifact_id
        AND position = 2;

    SELECT participant_id INTO top_three
    FROM projection_artifact_members
    WHERE artifact_id = NEW.top4_artifact_id
        AND position = 3;

    SELECT participant_id INTO top_four
    FROM projection_artifact_members
    WHERE artifact_id = NEW.top4_artifact_id
        AND position = 4;

    IF top4_member_count <> 4
        OR top_one IS NULL
        OR top_two IS NULL
        OR top_three IS NULL
        OR top_four IS NULL THEN
        RAISE EXCEPTION 'Top4 artifact must contain exactly four normalized members'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT COUNT(*) INTO semifinal_count
    FROM tournament_stage_playoff_semifinals
    WHERE command_id = NEW.command_id
        AND tournament_id = NEW.tournament_id
        AND roster_id = NEW.roster_id;

    SELECT COUNT(*) INTO invalid_semifinal_count
    FROM tournament_stage_playoff_semifinals AS semifinal
    INNER JOIN series
        ON series.id = semifinal.series_id
        AND series.tournament_id = semifinal.tournament_id
        AND series.roster_id = semifinal.roster_id
    WHERE semifinal.command_id = NEW.command_id
        AND semifinal.tournament_id = NEW.tournament_id
        AND semifinal.roster_id = NEW.roster_id
        AND (
            semifinal.bracket_artifact_id <> NEW.bracket_artifact_id
            OR series.state <> 'locked'
            OR series.format <> 'bo1'
            OR series.started_at IS NOT NULL
            OR (
                semifinal.position = 1
                AND (series.first_participant_id <> top_one OR series.second_participant_id <> top_two)
            )
            OR (
                semifinal.position = 2
                AND (series.first_participant_id <> top_three OR series.second_participant_id <> top_four)
            )
        );

    IF semifinal_count <> 2
        OR invalid_semifinal_count <> 0
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_stage_playoff_semifinals
            WHERE command_id = NEW.command_id
                AND tournament_id = NEW.tournament_id
                AND position = 1
                AND series_id = NEW.first_semifinal_series_id
        )
        OR NOT EXISTS (
            SELECT 1
            FROM tournament_stage_playoff_semifinals
            WHERE command_id = NEW.command_id
                AND tournament_id = NEW.tournament_id
                AND position = 2
                AND series_id = NEW.second_semifinal_series_id
        ) THEN
        RAISE EXCEPTION 'playoff semifinals do not match the canonical strength ordering'
            USING ERRCODE = 'check_violation';
    END IF;

    IF progression_source_state = 'swiss' THEN
        IF EXISTS (
            SELECT 1
            FROM tournament_stage_playoff_golden_settlements
            WHERE command_id = NEW.command_id
                AND tournament_id = NEW.tournament_id
        ) THEN
            RAISE EXCEPTION 'Swiss playoff progression cannot contain Golden settlements'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NULL;
    END IF;

    IF progression_source_state IS DISTINCT FROM 'golden' THEN
        RAISE EXCEPTION 'playoff progression has an invalid source stage'
            USING ERRCODE = 'check_violation';
    END IF;

    WITH expected AS (
        SELECT group_revision.revision_id AS group_revision_id,
            position_commit.attempt_id,
            position_commit.id AS position_commit_id,
            position_commit.participant_id,
            position_commit.position
        FROM golden_group_revisions AS group_revision
        INNER JOIN golden_attempt_stage_groups AS attempt_group
            ON attempt_group.group_revision_id = group_revision.revision_id
            AND attempt_group.tournament_id = group_revision.tournament_id
            AND attempt_group.roster_id = group_revision.roster_id
        INNER JOIN golden_attempts AS attempt
            ON attempt.id = attempt_group.attempt_id
            AND attempt.tournament_id = attempt_group.tournament_id
            AND attempt.roster_id = attempt_group.roster_id
            AND attempt.state = 'completed'
        INNER JOIN golden_position_commits AS position_commit
            ON position_commit.attempt_id = attempt.id
            AND position_commit.tournament_id = attempt.tournament_id
            AND position_commit.roster_id = attempt.roster_id
        INNER JOIN golden_memberships AS membership
            ON membership.id = position_commit.membership_id
            AND membership.attempt_id = position_commit.attempt_id
            AND membership.participant_id = position_commit.participant_id
            AND membership.participation_established_at IS NOT NULL
        WHERE group_revision.tournament_id = NEW.tournament_id
            AND group_revision.roster_id = NEW.roster_id
            AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
            AND group_revision.source_projection_revision = NEW.source_projection_revision
    ), settlements AS (
        SELECT group_revision_id,
            attempt_id,
            position_commit_id,
            participant_id,
            position
        FROM tournament_stage_playoff_golden_settlements
        WHERE command_id = NEW.command_id
            AND tournament_id = NEW.tournament_id
            AND roster_id = NEW.roster_id
    )
    SELECT
        (SELECT COUNT(*) FROM expected),
        (SELECT COUNT(*) FROM expected AS item WHERE NOT EXISTS (
            SELECT 1 FROM settlements
            WHERE settlements.group_revision_id = item.group_revision_id
                AND settlements.attempt_id = item.attempt_id
                AND settlements.position_commit_id = item.position_commit_id
                AND settlements.participant_id = item.participant_id
                AND settlements.position = item.position
        )),
        (SELECT COUNT(*) FROM settlements AS item WHERE NOT EXISTS (
            SELECT 1 FROM expected
            WHERE expected.group_revision_id = item.group_revision_id
                AND expected.attempt_id = item.attempt_id
                AND expected.position_commit_id = item.position_commit_id
                AND expected.participant_id = item.participant_id
                AND expected.position = item.position
        )),
        (SELECT COUNT(*) FROM settlements AS item WHERE NOT EXISTS (
            SELECT 1
            FROM projection_dependencies
            WHERE artifact_id = NEW.top4_artifact_id
                AND dependency_kind = 'golden_position'
                AND golden_position_commit_id = item.position_commit_id
        ))
    INTO expected_golden_count,
        missing_golden_count,
        unexpected_golden_count,
        missing_dependency_count;

    IF missing_golden_count <> 0
        OR unexpected_golden_count <> 0
        OR missing_dependency_count <> 0 THEN
        RAISE EXCEPTION 'Golden playoff settlements do not cover every terminal group participant'
            USING ERRCODE = 'check_violation';
    END IF;

    IF expected_golden_count = 0 AND (
        progression_action IS DISTINCT FROM 'correction_start_playoffs'
        OR NOT EXISTS (
            SELECT 1
            FROM golden_correction_stage_tombstones AS stage_tombstone
            INNER JOIN golden_correction_tombstone_seals AS seal
                ON seal.command_id = stage_tombstone.command_id
                AND seal.tournament_id = stage_tombstone.tournament_id
                AND seal.roster_id = stage_tombstone.roster_id
            WHERE stage_tombstone.command_id = NEW.command_id
                AND stage_tombstone.tournament_id = NEW.tournament_id
                AND stage_tombstone.roster_id = NEW.roster_id
                AND stage_tombstone.resulting_tournament_state = 'playoffs'
                AND stage_tombstone.source_projection_revision_id = NEW.source_projection_revision_id
                AND stage_tombstone.source_projection_revision = NEW.source_projection_revision
                AND stage_tombstone.resulting_projection_revision_id = NEW.published_projection_revision_id
                AND stage_tombstone.resulting_projection_revision = NEW.published_projection_revision
        )
        OR NOT EXISTS (
            SELECT 1
            FROM golden_group_revisions AS group_revision
            WHERE group_revision.tournament_id = NEW.tournament_id
                AND group_revision.roster_id = NEW.roster_id
                AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
                AND group_revision.source_projection_revision = NEW.source_projection_revision
        )
        OR EXISTS (
            SELECT 1
            FROM golden_group_revisions AS group_revision
            WHERE group_revision.tournament_id = NEW.tournament_id
                AND group_revision.roster_id = NEW.roster_id
                AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
                AND group_revision.source_projection_revision = NEW.source_projection_revision
                AND NOT EXISTS (
                    SELECT 1
                    FROM golden_correction_group_tombstones AS tombstone
                    WHERE tombstone.command_id = NEW.command_id
                        AND tombstone.tournament_id = group_revision.tournament_id
                        AND tombstone.roster_id = group_revision.roster_id
                        AND tombstone.group_id = group_revision.group_id
                        AND tombstone.group_revision_id = group_revision.revision_id
                )
        )
        OR EXISTS (
            SELECT 1
            FROM golden_group_revisions AS group_revision
            INNER JOIN golden_state_revisions AS state_revision
                ON state_revision.group_revision_id = group_revision.revision_id
                AND state_revision.tournament_id = group_revision.tournament_id
                AND state_revision.roster_id = group_revision.roster_id
            WHERE group_revision.tournament_id = NEW.tournament_id
                AND group_revision.roster_id = NEW.roster_id
                AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
                AND group_revision.source_projection_revision = NEW.source_projection_revision
                AND NOT EXISTS (
                    SELECT 1
                    FROM golden_correction_state_tombstones AS tombstone
                    WHERE tombstone.command_id = NEW.command_id
                        AND tombstone.tournament_id = state_revision.tournament_id
                        AND tombstone.roster_id = state_revision.roster_id
                        AND tombstone.group_revision_id = state_revision.group_revision_id
                        AND tombstone.state_revision_id = state_revision.revision_id
                )
        )
        OR EXISTS (
            SELECT 1
            FROM golden_group_revisions AS group_revision
            INNER JOIN golden_attempt_stage_groups AS attempt_group
                ON attempt_group.group_revision_id = group_revision.revision_id
                AND attempt_group.tournament_id = group_revision.tournament_id
                AND attempt_group.roster_id = group_revision.roster_id
            INNER JOIN golden_attempts AS attempt
                ON attempt.id = attempt_group.attempt_id
                AND attempt.tournament_id = attempt_group.tournament_id
                AND attempt.roster_id = attempt_group.roster_id
            WHERE group_revision.tournament_id = NEW.tournament_id
                AND group_revision.roster_id = NEW.roster_id
                AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
                AND group_revision.source_projection_revision = NEW.source_projection_revision
                AND (
                    attempt.state <> 'cancelled'
                    OR NOT EXISTS (
                        SELECT 1
                        FROM golden_correction_attempt_tombstones AS tombstone
                        WHERE tombstone.command_id = NEW.command_id
                            AND tombstone.tournament_id = attempt.tournament_id
                            AND tombstone.roster_id = attempt.roster_id
                            AND tombstone.group_revision_id = group_revision.revision_id
                            AND tombstone.attempt_id = attempt.id
                            AND tombstone.prior_state IN ('planned', 'waiting_ready')
                    )
                )
        )
        OR EXISTS (
            SELECT 1
            FROM golden_group_revisions AS group_revision
            INNER JOIN golden_attempt_stage_groups AS attempt_group
                ON attempt_group.group_revision_id = group_revision.revision_id
                AND attempt_group.tournament_id = group_revision.tournament_id
                AND attempt_group.roster_id = group_revision.roster_id
            INNER JOIN golden_position_commits AS position_commit
                ON position_commit.attempt_id = attempt_group.attempt_id
                AND position_commit.tournament_id = attempt_group.tournament_id
                AND position_commit.roster_id = attempt_group.roster_id
            WHERE group_revision.tournament_id = NEW.tournament_id
                AND group_revision.roster_id = NEW.roster_id
                AND group_revision.source_projection_revision_id = NEW.source_projection_revision_id
                AND group_revision.source_projection_revision = NEW.source_projection_revision
        )
    ) THEN
        RAISE EXCEPTION 'Golden playoff rollback lacks sealed unstarted group evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NULL;
END;
$$;

--
-- Name: result_projection_nodes; Type: TABLE; Schema: public; Owner: -
--

-- Final Swiss receipts are normalized server evidence for the bounded
-- predecessor chain consumed by the playoff planner. They are distinct from
-- public artifacts: the public standings artifact can be replaced, while the
-- canonical projection identity remains stable across physical revisions.
CREATE TABLE public.final_swiss_projection_receipts (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    receipt_revision bigint NOT NULL,
    canonical_projection_id uuid NOT NULL,
    previous_receipt_projection_revision_id uuid,
    source_standings_artifact_id uuid NOT NULL,
    source_standings_payload_digest bytea NOT NULL,
    canonical_payload_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT final_swiss_projection_receipts_identity_check CHECK (
        projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND canonical_projection_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_standings_artifact_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND receipt_revision >= 1
        AND (
            (receipt_revision = 1 AND previous_receipt_projection_revision_id IS NULL)
            OR (receipt_revision > 1 AND previous_receipt_projection_revision_id IS NOT NULL)
        )
    ),
    CONSTRAINT final_swiss_projection_receipts_digest_check CHECK (
        octet_length(source_standings_payload_digest) = 32
        AND source_standings_payload_digest <> decode(repeat('00', 32), 'hex')
        AND octet_length(canonical_payload_digest) = 32
        AND canonical_payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.final_swiss_projection_receipt_participants (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    stable_seed integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT final_swiss_projection_receipt_participants_identity_check CHECK (
        projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND stable_seed >= 1
    )
);

CREATE TABLE public.final_swiss_projection_receipt_rounds (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    round_id uuid NOT NULL,
    round_number smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT final_swiss_projection_receipt_rounds_identity_check CHECK (
        projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND round_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND round_number >= 1
    )
);

CREATE TABLE public.final_swiss_projection_receipt_series (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    round_id uuid NOT NULL,
    series_id uuid NOT NULL,
    terminal_source character varying(24) NOT NULL,
    series_result_revision_id uuid NOT NULL,
    score_revision_id uuid NOT NULL,
    series_result_node_id uuid NOT NULL,
    score_node_id uuid NOT NULL,
    normal_no_show_commit_id uuid,
    operator_forfeit_commit_id uuid,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT final_swiss_projection_receipt_series_identity_check CHECK (
        projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND round_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_result_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND score_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_result_node_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND score_node_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND (
            (terminal_source = 'played' AND normal_no_show_commit_id IS NULL AND operator_forfeit_commit_id IS NULL)
            OR (terminal_source = 'normal_no_show' AND normal_no_show_commit_id IS NOT NULL AND operator_forfeit_commit_id IS NULL)
            OR (terminal_source = 'pre_start_forfeit' AND normal_no_show_commit_id IS NULL AND operator_forfeit_commit_id IS NOT NULL)
        )
    )
);

CREATE TABLE public.final_swiss_projection_receipt_games (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    game_attempt_id uuid NOT NULL,
    game_result_revision_id uuid NOT NULL,
    game_result_node_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT final_swiss_projection_receipt_games_identity_check CHECK (
        projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND game_attempt_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND game_result_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND game_result_node_id <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

CREATE TABLE public.final_swiss_projection_receipt_ledger_entries (
    projection_revision_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    ledger_entry_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT final_swiss_projection_receipt_ledger_entries_identity_check CHECK (
        projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND ledger_entry_id <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

-- Result-projection provenance is normalized before logical nodes. Exactly
-- one durable origin owns each authority: an ordinary result commit, a
-- correction command, a Wave score genesis, or a stage publication genesis.
CREATE TABLE public.result_projection_node_authorities (
    id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    source_kind character varying(24) NOT NULL,
    result_commit_id uuid,
    correction_command_id uuid,
    wave_id uuid,
    stage_command_id uuid,
    normal_no_show_commit_id uuid,
    operator_forfeit_commit_id uuid,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT result_projection_node_authorities_identity_check CHECK (
        id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND tournament_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND roster_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND num_nonnulls(result_commit_id, correction_command_id, wave_id, stage_command_id,
            normal_no_show_commit_id, operator_forfeit_commit_id) = 1
        AND (
            (source_kind = 'result_commit'
                AND result_commit_id IS NOT NULL
                AND correction_command_id IS NULL
                AND wave_id IS NULL
                AND stage_command_id IS NULL)
            OR (source_kind = 'correction_commit'
                AND result_commit_id IS NULL
                AND correction_command_id IS NOT NULL
                AND wave_id IS NULL
                AND stage_command_id IS NULL)
            OR (source_kind = 'wave_initialization'
                AND result_commit_id IS NULL
                AND correction_command_id IS NULL
                AND wave_id IS NOT NULL
                AND stage_command_id IS NULL)
            OR (source_kind = 'stage_initialization'
                AND result_commit_id IS NULL
                AND correction_command_id IS NULL
                AND wave_id IS NULL
                AND stage_command_id IS NOT NULL)
            OR (source_kind = 'normal_no_show_commit' AND normal_no_show_commit_id IS NOT NULL)
            OR (source_kind = 'operator_forfeit_commit' AND operator_forfeit_commit_id IS NOT NULL)
        )
    )
);

-- Logical result revisions retain the server-built DAG separately from the
-- public materialized artifacts. They deliberately contain no client supplied
-- artifact payloads.
CREATE TABLE public.result_projection_nodes (
    id uuid NOT NULL,
    authority_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(24) NOT NULL,
    entity_id uuid NOT NULL,
    revision_number bigint NOT NULL,
    previous_node_id uuid,
    payload json NOT NULL,
    payload_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT result_projection_nodes_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT result_projection_nodes_identity_check CHECK (
        artifact_kind IN (
            'game_result', 'series_score', 'series_result', 'standings',
            'golden_group', 'top_four', 'bracket', 'champion'
        )
        AND revision_number >= 1
        AND ((revision_number = 1 AND previous_node_id IS NULL)
            OR (revision_number > 1 AND previous_node_id IS NOT NULL))
    ),
    CONSTRAINT result_projection_nodes_payload_check CHECK (
        jsonb_typeof(payload::jsonb) = 'object'
        AND payload::jsonb <> '{}'::jsonb
        AND NOT public.audit_payload_has_flag(payload::jsonb)
    )
);

--
-- Name: result_projection_dependencies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.result_projection_dependencies (
    authority_id uuid NOT NULL,
    source_node_id uuid NOT NULL,
    derived_node_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT result_projection_dependencies_identity_check CHECK (
        source_node_id <> derived_node_id
    )
);

--
-- Name: correction_projection_decisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.correction_projection_decisions (
    id uuid NOT NULL,
    command_id uuid NOT NULL,
    sequence_number integer NOT NULL,
    projection_node_id uuid NOT NULL,
    payload json NOT NULL,
    payload_digest bytea NOT NULL,
    recorded_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT correction_projection_decisions_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT correction_projection_decisions_payload_check CHECK (
        sequence_number >= 1
        AND jsonb_typeof(payload::jsonb) = 'object'
        AND payload::jsonb <> '{}'::jsonb
        AND NOT public.audit_payload_has_flag(payload::jsonb)
        AND recorded_at <= created_at
    )
);

--
-- Name: correction_projection_bindings; Type: TABLE; Schema: public; Owner: -
--

-- Binding rows tie a logical node to the exact normalized result, score, or
-- materialized artifact state which was current when the node was recorded.
CREATE TABLE public.correction_projection_bindings (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    artifact_kind character varying(24) NOT NULL,
    entity_id uuid NOT NULL,
    source_id uuid NOT NULL,
    node_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT correction_projection_bindings_kind_check CHECK (
        artifact_kind IN (
            'game_result', 'series_score', 'series_result', 'standings',
            'golden_group', 'top_four', 'bracket', 'champion'
        )
    )
);

--
-- Name: participant_post_series_actions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.participant_post_series_actions (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    series_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    current_result_revision_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    resulting_projection_revision_id uuid NOT NULL,
    resulting_projection_revision bigint NOT NULL,
    action character varying(32) NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT participant_post_series_actions_action_check CHECK (((action)::text = ANY ((ARRAY['acknowledge_result'::character varying, 'request_next_assignment'::character varying, 'leave_lobby'::character varying])::text[]))),
    CONSTRAINT participant_post_series_actions_projection_check CHECK ((source_projection_revision >= 1 AND resulting_projection_revision_id = source_projection_revision_id AND resulting_projection_revision = source_projection_revision)),
    CONSTRAINT participant_post_series_actions_timestamps_check CHECK ((occurred_at <= created_at))
);

-- Stage progression records its transition separately. This evidence binds the
-- exact published projection and the normalized semifinal and Golden inputs
-- which made that transition safe to expose.
CREATE TABLE public.tournament_stage_playoff_evidence (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    source_projection_revision_id uuid NOT NULL,
    source_projection_revision bigint NOT NULL,
    published_projection_revision_id uuid NOT NULL,
    published_projection_revision bigint NOT NULL,
    top4_artifact_id uuid NOT NULL,
    bracket_artifact_id uuid NOT NULL,
    top4_node_id uuid NOT NULL,
    bracket_node_id uuid NOT NULL,
    first_semifinal_series_id uuid NOT NULL,
    second_semifinal_series_id uuid NOT NULL,
    proof_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_evidence_identity_check CHECK (
        source_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND published_projection_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND top4_artifact_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND bracket_artifact_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND top4_node_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND bracket_node_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_semifinal_series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND second_semifinal_series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_semifinal_series_id <> second_semifinal_series_id
        AND source_projection_revision >= 1
        AND published_projection_revision >= source_projection_revision
        AND octet_length(proof_digest) = 32
        AND proof_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE public.tournament_stage_playoff_golden_settlements (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    group_revision_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    position_commit_id uuid NOT NULL,
    participant_id uuid NOT NULL,
    position smallint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_golden_settlements_identity_check CHECK (
        group_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND attempt_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND position_commit_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND position >= 1
        AND position <= 16
    )
);

CREATE TABLE public.tournament_stage_playoff_semifinals (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    bracket_artifact_id uuid NOT NULL,
    position smallint NOT NULL,
    series_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_semifinals_identity_check CHECK (
        bracket_artifact_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND position IN (1, 2)
    )
);

-- Final-stage records are append-only evidence. They retain the exact
-- terminal semifinal heads, planned final identities, draft completion, and
-- each non-terminal BO3 continuation without overloading projection payloads.
CREATE TABLE public.tournament_stage_playoff_final_advancements (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    position smallint NOT NULL,
    semifinal_series_id uuid NOT NULL,
    winner_id uuid NOT NULL,
    loser_id uuid NOT NULL,
    score_revision_id uuid NOT NULL,
    result_revision_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_final_advancements_identity_check CHECK (
        position IN (1, 2)
        AND semifinal_series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND winner_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND loser_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND winner_id <> loser_id
        AND score_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND result_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

CREATE TABLE public.tournament_stage_playoff_finals (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    final_series_id uuid NOT NULL,
    category_revision_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    draft_initial_revision_id uuid NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_finals_identity_check CHECK (
        final_series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND category_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND draft_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND draft_initial_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND second_participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_participant_id <> second_participant_id
    )
);

-- This is the authoritative, CAS-locked receipt-history head for one final
-- draft. A new immutable revision identity is assigned whenever either
-- finalist receives a task, while child plans retain the exact head they
-- observed when their eligible-task proof was constructed.
CREATE TABLE public.final_draft_delivery_history_heads (
    draft_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    first_participant_id uuid NOT NULL,
    second_participant_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    revision bigint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT final_draft_delivery_history_heads_identity_check CHECK (
        draft_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND second_participant_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_participant_id <> second_participant_id
        AND revision >= 1
        AND updated_at >= created_at
    )
);

CREATE TABLE public.tournament_stage_playoff_final_initializations (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    final_series_id uuid NOT NULL,
    draft_id uuid NOT NULL,
    completed_draft_revision_id uuid NOT NULL,
    initial_score_revision_id uuid NOT NULL,
    first_slot_id uuid NOT NULL,
    first_game_id uuid NOT NULL,
    first_wave_id uuid NOT NULL,
    first_wave_revision_id uuid NOT NULL,
    first_assignment_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_final_initializations_identity_check CHECK (
        final_series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND draft_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND completed_draft_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND initial_score_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_game_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_wave_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_wave_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND first_assignment_id <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

CREATE TABLE public.tournament_stage_playoff_final_progressions (
    command_id uuid NOT NULL,
    tournament_id uuid NOT NULL,
    roster_id uuid NOT NULL,
    final_series_id uuid NOT NULL,
    source_score_revision_id uuid NOT NULL,
    source_game_result_revision_id uuid NOT NULL,
    next_position smallint NOT NULL,
    next_slot_id uuid NOT NULL,
    next_game_id uuid NOT NULL,
    next_wave_id uuid NOT NULL,
    next_wave_revision_id uuid NOT NULL,
    next_assignment_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT tournament_stage_playoff_final_progressions_identity_check CHECK (
        next_position IN (2, 3)
        AND final_series_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_score_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND source_game_result_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND next_slot_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND next_game_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND next_wave_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND next_wave_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND next_assignment_id <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

--
-- Name: projection_artifact_members projection_artifact_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifact_members
    ADD CONSTRAINT projection_artifact_members_pkey PRIMARY KEY (artifact_id, participant_id);

--
-- Name: projection_artifact_members projection_artifact_members_position_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifact_members
    ADD CONSTRAINT projection_artifact_members_position_key UNIQUE (artifact_id, "position");

--
-- Name: projection_artifacts projection_artifacts_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifacts
    ADD CONSTRAINT projection_artifacts_identity_key UNIQUE (id, tournament_id, roster_id, artifact_kind);

--
-- Name: projection_artifacts projection_artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifacts
    ADD CONSTRAINT projection_artifacts_pkey PRIMARY KEY (id);

--
-- Name: projection_artifacts projection_artifacts_revision_kind_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifacts
    ADD CONSTRAINT projection_artifacts_revision_kind_key UNIQUE (produced_by_revision_id, artifact_kind, artifact_key);

--
-- Name: projection_artifacts projection_artifacts_scope_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifacts
    ADD CONSTRAINT projection_artifacts_scope_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: projection_cutoffs projection_cutoffs_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_identity_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: projection_cutoffs projection_cutoffs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_pkey PRIMARY KEY (id);

--
-- Name: projection_cutoffs projection_cutoffs_previous_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_previous_key UNIQUE (previous_cutoff_id);

--
-- Name: projection_cutoffs projection_cutoffs_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_sequence_key UNIQUE (tournament_id, sequence_number);

--
-- Name: projection_dependencies projection_dependencies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_dependencies
    ADD CONSTRAINT projection_dependencies_pkey PRIMARY KEY (id);

--
-- Name: projection_revision_artifacts projection_revision_artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revision_artifacts
    ADD CONSTRAINT projection_revision_artifacts_pkey PRIMARY KEY (revision_id, artifact_kind);

--
-- Name: projection_revision_artifacts projection_revision_artifacts_revision_artifact_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revision_artifacts
    ADD CONSTRAINT projection_revision_artifacts_revision_artifact_key UNIQUE (revision_id, artifact_id);

--
-- Name: projection_revisions projection_revisions_cutoff_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_cutoff_id_key UNIQUE (cutoff_id);

--
-- Name: projection_revisions projection_revisions_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_identity_key UNIQUE (id, tournament_id, roster_id);

--
-- Name: projection_revisions projection_revisions_record_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_record_key UNIQUE (id, tournament_id, roster_id, revision_number);

ALTER TABLE ONLY public.final_swiss_projection_receipts
    ADD CONSTRAINT final_swiss_projection_receipts_pkey PRIMARY KEY (projection_revision_id);

ALTER TABLE ONLY public.final_swiss_projection_receipts
    ADD CONSTRAINT final_swiss_projection_receipts_scope_key UNIQUE (
        projection_revision_id,
        tournament_id,
        roster_id
    );

ALTER TABLE ONLY public.final_swiss_projection_receipts
    ADD CONSTRAINT final_swiss_projection_receipts_canonical_revision_key UNIQUE (
        tournament_id,
        roster_id,
        canonical_projection_id,
        receipt_revision
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_participants
    ADD CONSTRAINT final_swiss_projection_receipt_participants_pkey PRIMARY KEY (
        projection_revision_id,
        participant_id
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_participants
    ADD CONSTRAINT final_swiss_projection_receipt_participants_seed_key UNIQUE (
        projection_revision_id,
        stable_seed
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_rounds
    ADD CONSTRAINT final_swiss_projection_receipt_rounds_pkey PRIMARY KEY (
        projection_revision_id,
        round_id
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_rounds
    ADD CONSTRAINT final_swiss_projection_receipt_rounds_number_key UNIQUE (
        projection_revision_id,
        round_number
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_pkey PRIMARY KEY (
        projection_revision_id,
        series_id
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_round_key UNIQUE (
        projection_revision_id,
        round_id,
        series_id
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_games
    ADD CONSTRAINT final_swiss_projection_receipt_games_pkey PRIMARY KEY (
        projection_revision_id,
        game_attempt_id
    );

ALTER TABLE ONLY public.final_swiss_projection_receipt_ledger_entries
    ADD CONSTRAINT final_swiss_projection_receipt_ledger_entries_pkey PRIMARY KEY (
        projection_revision_id,
        ledger_entry_id
    );

--
-- Name: participant_post_series_actions participant_post_series_actions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_pkey PRIMARY KEY (command_id);

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_pkey PRIMARY KEY (command_id, tournament_id);

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_pkey PRIMARY KEY (
        command_id,
        tournament_id,
        group_revision_id,
        position
    );

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_position_commit_key UNIQUE (
        command_id,
        tournament_id,
        position_commit_id
    );

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_participant_key UNIQUE (
        command_id,
        tournament_id,
        group_revision_id,
        participant_id
    );

ALTER TABLE ONLY public.tournament_stage_playoff_semifinals
    ADD CONSTRAINT tournament_stage_playoff_semifinals_pkey PRIMARY KEY (command_id, tournament_id, position);

ALTER TABLE ONLY public.tournament_stage_playoff_semifinals
    ADD CONSTRAINT tournament_stage_playoff_semifinals_series_key UNIQUE (command_id, tournament_id, series_id);

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_source_projection_fk
    FOREIGN KEY (
        source_projection_revision_id,
        tournament_id,
        roster_id,
        source_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_resulting_projection_fk
    FOREIGN KEY (
        resulting_projection_revision_id,
        tournament_id,
        roster_id,
        resulting_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_progression_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_progressions(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_source_projection_fk
    FOREIGN KEY (
        source_projection_revision_id,
        tournament_id,
        roster_id,
        source_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_published_projection_fk
    FOREIGN KEY (
        published_projection_revision_id,
        tournament_id,
        roster_id,
        published_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_top4_artifact_fk
    FOREIGN KEY (top4_artifact_id, tournament_id, roster_id)
    REFERENCES public.projection_artifacts(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_bracket_artifact_fk
    FOREIGN KEY (bracket_artifact_id, tournament_id, roster_id)
    REFERENCES public.projection_artifacts(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_first_semifinal_fk
    FOREIGN KEY (first_semifinal_series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_second_semifinal_fk
    FOREIGN KEY (second_semifinal_series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_evidence_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_evidence(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_group_revision_fk
    FOREIGN KEY (group_revision_id, tournament_id, roster_id)
    REFERENCES public.golden_group_revisions(revision_id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_attempt_group_fk
    FOREIGN KEY (attempt_id, tournament_id, roster_id, group_revision_id)
    REFERENCES public.golden_attempt_stage_groups(attempt_id, tournament_id, roster_id, group_revision_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_position_commit_fk
    FOREIGN KEY (position_commit_id)
    REFERENCES public.golden_position_commits(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_golden_settlements
    ADD CONSTRAINT tournament_stage_playoff_golden_settlements_participant_fk
    FOREIGN KEY (roster_id, participant_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_semifinals
    ADD CONSTRAINT tournament_stage_playoff_semifinals_evidence_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_evidence(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_semifinals
    ADD CONSTRAINT tournament_stage_playoff_semifinals_bracket_artifact_fk
    FOREIGN KEY (bracket_artifact_id, tournament_id, roster_id)
    REFERENCES public.projection_artifacts(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_semifinals
    ADD CONSTRAINT tournament_stage_playoff_semifinals_series_fk
    FOREIGN KEY (series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_pkey PRIMARY KEY (
        command_id,
        tournament_id,
        position
    );

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_series_key UNIQUE (
        command_id,
        tournament_id,
        semifinal_series_id
    );

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_evidence_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_evidence(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_semifinal_fk
    FOREIGN KEY (command_id, tournament_id, position)
    REFERENCES public.tournament_stage_playoff_semifinals(command_id, tournament_id, position)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_semifinal_series_fk
    FOREIGN KEY (command_id, tournament_id, semifinal_series_id)
    REFERENCES public.tournament_stage_playoff_semifinals(command_id, tournament_id, series_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_score_fk
    FOREIGN KEY (score_revision_id, semifinal_series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_result_fk
    FOREIGN KEY (result_revision_id, semifinal_series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_winner_fk
    FOREIGN KEY (roster_id, winner_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_advancements
    ADD CONSTRAINT tournament_stage_playoff_final_advancements_loser_fk
    FOREIGN KEY (roster_id, loser_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_pkey PRIMARY KEY (command_id, tournament_id);

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_series_key UNIQUE (final_series_id);

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_draft_key UNIQUE (draft_id);

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_draft_scope_key UNIQUE (
        draft_id,
        tournament_id,
        roster_id,
        first_participant_id,
        second_participant_id
    );

ALTER TABLE ONLY public.final_draft_delivery_history_heads
    ADD CONSTRAINT final_draft_delivery_history_heads_pkey PRIMARY KEY (draft_id);

ALTER TABLE ONLY public.final_draft_delivery_history_heads
    ADD CONSTRAINT final_draft_delivery_history_heads_stage_fk
    FOREIGN KEY (
        draft_id,
        tournament_id,
        roster_id,
        first_participant_id,
        second_participant_id
    )
    REFERENCES public.tournament_stage_playoff_finals(
        draft_id,
        tournament_id,
        roster_id,
        first_participant_id,
        second_participant_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_draft_delivery_history_heads
    ADD CONSTRAINT final_draft_delivery_history_heads_first_participant_fk
    FOREIGN KEY (roster_id, first_participant_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_draft_delivery_history_heads
    ADD CONSTRAINT final_draft_delivery_history_heads_second_participant_fk
    FOREIGN KEY (roster_id, second_participant_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_evidence_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_evidence(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_series_fk
    FOREIGN KEY (final_series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_category_fk
    FOREIGN KEY (category_revision_id, final_series_id, roster_id)
    REFERENCES public.category_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_draft_fk
    FOREIGN KEY (draft_id, final_series_id, roster_id)
    REFERENCES public.drafts(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_draft_initial_revision_fk
    FOREIGN KEY (draft_initial_revision_id, draft_id)
    REFERENCES public.draft_revisions(id, draft_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_first_participant_fk
    FOREIGN KEY (roster_id, first_participant_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_finals
    ADD CONSTRAINT tournament_stage_playoff_finals_second_participant_fk
    FOREIGN KEY (roster_id, second_participant_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_pkey PRIMARY KEY (
        command_id,
        tournament_id
    );

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_stage_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_finals(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_series_fk
    FOREIGN KEY (final_series_id)
    REFERENCES public.tournament_stage_playoff_finals(final_series_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_draft_fk
    FOREIGN KEY (draft_id)
    REFERENCES public.tournament_stage_playoff_finals(draft_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_draft_revision_fk
    FOREIGN KEY (completed_draft_revision_id, draft_id)
    REFERENCES public.draft_revisions(id, draft_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_score_fk
    FOREIGN KEY (initial_score_revision_id, final_series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_slot_fk
    FOREIGN KEY (first_slot_id, final_series_id, roster_id)
    REFERENCES public.game_slots(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_game_fk
    FOREIGN KEY (first_game_id, final_series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_wave_fk
    FOREIGN KEY (first_wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_initializations
    ADD CONSTRAINT tournament_stage_playoff_final_initializations_assignment_fk
    FOREIGN KEY (first_assignment_id, first_game_id, roster_id)
    REFERENCES public.assignments(id, attempt_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_pkey PRIMARY KEY (
        command_id,
        tournament_id,
        source_score_revision_id
    );

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_position_key UNIQUE (
        command_id,
        tournament_id,
        next_position
    );

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_stage_fk
    FOREIGN KEY (command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_finals(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_series_fk
    FOREIGN KEY (final_series_id)
    REFERENCES public.tournament_stage_playoff_finals(final_series_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_source_score_fk
    FOREIGN KEY (source_score_revision_id, final_series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_source_result_fk
    FOREIGN KEY (source_game_result_revision_id, final_series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_slot_fk
    FOREIGN KEY (next_slot_id, final_series_id, roster_id)
    REFERENCES public.game_slots(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_game_fk
    FOREIGN KEY (next_game_id, final_series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_wave_fk
    FOREIGN KEY (next_wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_final_progressions
    ADD CONSTRAINT tournament_stage_playoff_final_progressions_assignment_fk
    FOREIGN KEY (next_assignment_id, next_game_id, roster_id)
    REFERENCES public.assignments(id, attempt_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

--
-- Name: outbox_events outbox_events_projection_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

-- Result-projection node identity and provenance precede lineage FKs below.
ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_no_show_key UNIQUE (normal_no_show_commit_id),
    ADD CONSTRAINT result_projection_node_authorities_forfeit_key UNIQUE (operator_forfeit_commit_id),
    ADD CONSTRAINT result_projection_node_authorities_no_show_fk FOREIGN KEY (normal_no_show_commit_id)
        REFERENCES public.normal_no_show_commits(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT result_projection_node_authorities_forfeit_fk FOREIGN KEY (operator_forfeit_commit_id)
        REFERENCES public.operator_forfeit_commits(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_result_commit_key UNIQUE (result_commit_id);

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_correction_command_key UNIQUE (correction_command_id);

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_wave_key UNIQUE (wave_id);

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_stage_key UNIQUE (stage_command_id, tournament_id);

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_result_commit_fk
    FOREIGN KEY (result_commit_id)
    REFERENCES public.result_commits(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_correction_command_fk
    FOREIGN KEY (correction_command_id)
    REFERENCES public.result_correction_commits(command_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_node_authorities
    ADD CONSTRAINT result_projection_node_authorities_stage_fk
    FOREIGN KEY (stage_command_id, tournament_id)
    REFERENCES public.tournament_stage_playoff_evidence(command_id, tournament_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_nodes
    ADD CONSTRAINT result_projection_nodes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.result_projection_nodes
    ADD CONSTRAINT result_projection_nodes_revision_key UNIQUE (
        tournament_id,
        roster_id,
        artifact_kind,
        entity_id,
        revision_number
    );

ALTER TABLE ONLY public.result_projection_nodes
    ADD CONSTRAINT result_projection_nodes_authority_fk
    FOREIGN KEY (authority_id)
    REFERENCES public.result_projection_node_authorities(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_nodes
    ADD CONSTRAINT result_projection_nodes_previous_fk
    FOREIGN KEY (previous_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_top4_node_fk
    FOREIGN KEY (top4_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_stage_playoff_evidence
    ADD CONSTRAINT tournament_stage_playoff_evidence_bracket_node_fk
    FOREIGN KEY (bracket_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_dependencies
    ADD CONSTRAINT result_projection_dependencies_authority_fk
    FOREIGN KEY (authority_id)
    REFERENCES public.result_projection_node_authorities(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.result_projection_dependencies
    ADD CONSTRAINT result_projection_dependencies_source_fk
    FOREIGN KEY (source_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.result_projection_dependencies
    ADD CONSTRAINT result_projection_dependencies_derived_fk
    FOREIGN KEY (derived_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.correction_projection_decisions
    ADD CONSTRAINT correction_projection_decisions_command_fk
    FOREIGN KEY (command_id)
    REFERENCES public.result_correction_commits(command_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.correction_projection_decisions
    ADD CONSTRAINT correction_projection_decisions_node_fk
    FOREIGN KEY (projection_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.correction_projection_bindings
    ADD CONSTRAINT correction_projection_bindings_command_fk
    FOREIGN KEY (command_id)
    REFERENCES public.result_correction_commits(command_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.correction_projection_bindings
    ADD CONSTRAINT correction_projection_bindings_node_fk
    FOREIGN KEY (node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_projection_revision_fk
    FOREIGN KEY (
        projection_revision_id,
        tournament_id,
        roster_id,
        projection_revision
    )
    REFERENCES public.projection_revisions(
        id,
        tournament_id,
        roster_id,
        revision_number
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.tournament_outbox_cursors
    ADD CONSTRAINT tournament_outbox_cursors_tournament_fk
    FOREIGN KEY (tournament_id)
    REFERENCES public.tournaments(id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.projection_outbox_cursors
    ADD CONSTRAINT projection_outbox_cursors_projection_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.projection_revisions(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_idempotency_key_key UNIQUE (idempotency_key);

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_tournament_identity_key UNIQUE (id, tournament_id);

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_delivery_identity_key UNIQUE (id, tournament_id, sequence);

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_target_identity_key UNIQUE (
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    );

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_projection_ordinal_key UNIQUE (tournament_id, projection_revision_id, projection_ordinal);

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_sequence_key UNIQUE (tournament_id, sequence);

ALTER TABLE ONLY public.tournament_outbox_cursors
    ADD CONSTRAINT tournament_outbox_cursors_pkey PRIMARY KEY (tournament_id);

ALTER TABLE ONLY public.projection_outbox_cursors
    ADD CONSTRAINT projection_outbox_cursors_pkey PRIMARY KEY (projection_revision_id);

ALTER TABLE ONLY public.projection_outbox_cursors
    ADD CONSTRAINT projection_outbox_cursors_scope_key UNIQUE (
        projection_revision_id,
        tournament_id,
        roster_id
    );

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_pkey PRIMARY KEY (outbox_event_id);

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_event_key UNIQUE (outbox_event_id, result_event_id);

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_result_key UNIQUE (result_event_id);

ALTER TABLE ONLY public.outbox_tournament_cancellation_sources
    ADD CONSTRAINT outbox_tournament_cancellation_sources_pkey PRIMARY KEY (outbox_event_id);

ALTER TABLE ONLY public.outbox_tournament_cancellation_sources
    ADD CONSTRAINT outbox_tournament_cancellation_sources_command_key UNIQUE (cancellation_command_id);

ALTER TABLE ONLY public.outbox_wave_sources
    ADD CONSTRAINT outbox_wave_sources_pkey PRIMARY KEY (outbox_event_id);

ALTER TABLE ONLY public.outbox_wave_sources
    ADD CONSTRAINT outbox_wave_sources_revision_key UNIQUE (wave_id, wave_revision_id);

ALTER TABLE ONLY public.realtime_subscribers
    ADD CONSTRAINT realtime_subscribers_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.realtime_subscribers
    ADD CONSTRAINT realtime_subscribers_identity_key UNIQUE (id, tournament_id);

ALTER TABLE ONLY public.realtime_delivery_receipts
    ADD CONSTRAINT realtime_delivery_receipts_pkey PRIMARY KEY (subscriber_id, event_id);

ALTER TABLE ONLY public.outbox_events
    ADD CONSTRAINT outbox_events_roster_fk
    FOREIGN KEY (roster_id, tournament_id)
    REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_event_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_result_fk
    FOREIGN KEY (result_event_id, series_id, roster_id)
    REFERENCES public.result_events(id, series_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_projection_fk
    FOREIGN KEY (projection_evidence_id, tournament_id, roster_id)
    REFERENCES public.result_projection_evidence(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_projection_result_fk
    FOREIGN KEY (projection_evidence_id, result_event_id)
    REFERENCES public.result_projection_evidence(id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_target_event_fk
    FOREIGN KEY (
        outbox_event_id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    )
    REFERENCES public.outbox_events(
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    ) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_result_sources
    ADD CONSTRAINT outbox_result_sources_target_projection_fk
    FOREIGN KEY (
        projection_revision_id,
        tournament_id,
        roster_id,
        projection_revision
    )
    REFERENCES public.projection_revisions(
        id,
        tournament_id,
        roster_id,
        revision_number
    ) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.outbox_tournament_cancellation_sources
    ADD CONSTRAINT outbox_tournament_cancellation_sources_event_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_tournament_cancellation_sources
    ADD CONSTRAINT outbox_tournament_cancellation_sources_command_fk
    FOREIGN KEY (cancellation_command_id)
    REFERENCES public.tournament_cancellations(command_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_tournament_cancellation_sources
    ADD CONSTRAINT outbox_tournament_cancellation_sources_projection_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id, projection_revision)
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_tournament_cancellation_sources
    ADD CONSTRAINT outbox_tournament_cancellation_sources_target_event_fk
    FOREIGN KEY (
        outbox_event_id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    )
    REFERENCES public.outbox_events(
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    ) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_wave_sources
    ADD CONSTRAINT outbox_wave_sources_event_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_wave_sources
    ADD CONSTRAINT outbox_wave_sources_wave_fk
    FOREIGN KEY (wave_id, tournament_id, roster_id)
    REFERENCES public.waves(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_wave_sources
    ADD CONSTRAINT outbox_wave_sources_projection_fk
    FOREIGN KEY (
        projection_revision_id,
        tournament_id,
        roster_id,
        projection_revision
    )
    REFERENCES public.projection_revisions(
        id,
        tournament_id,
        roster_id,
        revision_number
    ) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_wave_sources
    ADD CONSTRAINT outbox_wave_sources_target_event_fk
    FOREIGN KEY (
        outbox_event_id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    )
    REFERENCES public.outbox_events(
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    ) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_pkey PRIMARY KEY (outbox_event_id);

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_result_key UNIQUE (final_result_revision_id);

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_event_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_target_event_fk
    FOREIGN KEY (
        outbox_event_id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    )
    REFERENCES public.outbox_events(
        id,
        tournament_id,
        roster_id,
        projection_revision_id,
        projection_revision,
        projection_ordinal
    ) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_series_fk
    FOREIGN KEY (final_series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_result_fk
    FOREIGN KEY (final_result_revision_id, final_series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_artifact_fk
    FOREIGN KEY (champion_artifact_id, tournament_id, roster_id, artifact_kind)
    REFERENCES public.projection_artifacts(id, tournament_id, roster_id, artifact_kind)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.outbox_champion_sources
    ADD CONSTRAINT outbox_champion_sources_target_projection_fk
    FOREIGN KEY (
        projection_revision_id,
        tournament_id,
        roster_id,
        projection_revision
    )
    REFERENCES public.projection_revisions(
        id,
        tournament_id,
        roster_id,
        revision_number
    ) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.realtime_delivery_receipts
    ADD CONSTRAINT realtime_delivery_receipts_event_fk
    FOREIGN KEY (event_id, tournament_id, sequence)
    REFERENCES public.outbox_events(id, tournament_id, sequence) ON DELETE RESTRICT;

ALTER TABLE ONLY public.realtime_delivery_receipts
    ADD CONSTRAINT realtime_delivery_receipts_subscriber_fk
    FOREIGN KEY (subscriber_id, tournament_id)
    REFERENCES public.realtime_subscribers(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.realtime_subscribers
    ADD CONSTRAINT realtime_subscribers_tournament_fk
    FOREIGN KEY (tournament_id)
    REFERENCES public.tournaments(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.tournament_cancellations
    ADD CONSTRAINT tournament_cancellations_outbox_fk
    FOREIGN KEY (outbox_event_id, tournament_id)
    REFERENCES public.outbox_events(id, tournament_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.result_commits
    ADD CONSTRAINT result_commits_outbox_fk
    FOREIGN KEY (outbox_event_id, result_event_id)
    REFERENCES public.outbox_result_sources(outbox_event_id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.normal_no_show_commits
    ADD CONSTRAINT normal_no_show_commits_outbox_fk
    FOREIGN KEY (outbox_event_id, result_event_id)
    REFERENCES public.outbox_result_sources(outbox_event_id, result_event_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_outbox_fk
    FOREIGN KEY (outbox_event_id, result_event_id)
    REFERENCES public.outbox_result_sources(outbox_event_id, result_event_id) ON DELETE RESTRICT;

CREATE INDEX outbox_events_claim_idx
    ON public.outbox_events (available_at, tournament_id, sequence)
    WHERE published_at IS NULL;

CREATE INDEX outbox_events_claim_expiry_idx
    ON public.outbox_events (claimed_until)
    WHERE published_at IS NULL AND claimed_until IS NOT NULL;

CREATE INDEX outbox_events_resume_idx ON public.outbox_events (tournament_id, sequence);

CREATE INDEX realtime_delivery_receipts_claim_idx
    ON public.realtime_delivery_receipts (available_at, tournament_id, sequence)
    WHERE terminal_at IS NULL;

CREATE INDEX realtime_delivery_receipts_retention_idx
    ON public.realtime_delivery_receipts (terminal_at, subscriber_id, event_id)
    WHERE terminal_at IS NOT NULL;

CREATE INDEX realtime_subscribers_active_idx
    ON public.realtime_subscribers (instance_id, tournament_id, id, connection_generation)
    WHERE closed_at IS NULL;

CREATE INDEX realtime_subscribers_retention_idx
    ON public.realtime_subscribers (closed_at, id)
    WHERE closed_at IS NOT NULL;

CREATE TRIGGER outbox_event_guard
BEFORE DELETE OR UPDATE ON public.outbox_events
FOR EACH ROW EXECUTE FUNCTION public.outbox_event_guard();

CREATE CONSTRAINT TRIGGER outbox_event_source_consistency
AFTER INSERT ON public.outbox_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_event_source();

CREATE TRIGGER outbox_result_sources_append_only
BEFORE DELETE OR UPDATE ON public.outbox_result_sources
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_result_source_membership
AFTER INSERT ON public.outbox_result_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

CREATE CONSTRAINT TRIGGER outbox_cancellation_source_membership
AFTER INSERT ON public.outbox_tournament_cancellation_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

CREATE CONSTRAINT TRIGGER outbox_wave_source_membership
AFTER INSERT ON public.outbox_wave_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

CREATE CONSTRAINT TRIGGER outbox_champion_source_membership
AFTER INSERT ON public.outbox_champion_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

CREATE CONSTRAINT TRIGGER outbox_stage_projection_source_membership
AFTER INSERT ON public.outbox_stage_projection_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_outbox_source_membership();

CREATE TRIGGER outbox_stage_projection_sources_append_only
BEFORE DELETE OR UPDATE ON public.outbox_stage_projection_sources
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_stage_projection_sources_guard
AFTER INSERT ON public.outbox_stage_projection_sources DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.outbox_stage_projection_source_guard();

CREATE TRIGGER outbox_tournament_cancellation_sources_append_only
BEFORE DELETE OR UPDATE ON public.outbox_tournament_cancellation_sources
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_tournament_cancellation_sources_guard
AFTER INSERT ON public.outbox_tournament_cancellation_sources
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.outbox_tournament_cancellation_source_guard();

CREATE TRIGGER outbox_wave_sources_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.outbox_wave_sources
FOR EACH ROW EXECUTE FUNCTION public.outbox_wave_source_guard();

CREATE TRIGGER outbox_champion_sources_append_only
BEFORE DELETE OR UPDATE ON public.outbox_champion_sources
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER outbox_champion_sources_guard
AFTER INSERT ON public.outbox_champion_sources
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.outbox_champion_source_guard();

CREATE TRIGGER realtime_delivery_receipt_guard
BEFORE DELETE OR UPDATE ON public.realtime_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.realtime_delivery_receipt_guard();

CREATE TRIGGER realtime_subscriber_guard
BEFORE DELETE OR UPDATE ON public.realtime_subscribers
FOR EACH ROW EXECUTE FUNCTION public.realtime_subscriber_guard();

ALTER TABLE ONLY public.operator_forfeit_commits
    ADD CONSTRAINT operator_forfeit_commits_source_projection_fk
    FOREIGN KEY (
        source_projection_revision_id,
        tournament_id,
        roster_id,
        source_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_source_projection_fk
    FOREIGN KEY (
        source_projection_revision_id,
        tournament_id,
        roster_id,
        source_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT;

ALTER TABLE ONLY public.result_correction_commits
    ADD CONSTRAINT result_correction_commits_resulting_projection_fk
    FOREIGN KEY (
        resulting_projection_revision_id,
        tournament_id,
        roster_id,
        resulting_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT;

--
-- Name: projection_revisions projection_revisions_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_number_key UNIQUE (tournament_id, revision_number);

--
-- Name: projection_revisions projection_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_pkey PRIMARY KEY (id);

--
-- Name: result_projection_dependencies result_projection_dependencies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.result_projection_dependencies
    ADD CONSTRAINT result_projection_dependencies_pkey PRIMARY KEY (source_node_id, derived_node_id);

--
-- Name: correction_projection_decisions correction_projection_decisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.correction_projection_decisions
    ADD CONSTRAINT correction_projection_decisions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.correction_projection_decisions
    ADD CONSTRAINT correction_projection_decisions_command_sequence_key UNIQUE (command_id, sequence_number);

--
-- Name: correction_projection_bindings correction_projection_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.correction_projection_bindings
    ADD CONSTRAINT correction_projection_bindings_pkey PRIMARY KEY (
        tournament_id,
        roster_id,
        artifact_kind,
        entity_id,
        source_id
    );

--
-- Name: projection_revisions projection_revisions_previous_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_previous_key UNIQUE (previous_revision_id);

--
-- Name: projection_artifacts_roster_kind_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projection_artifacts_roster_kind_idx ON public.projection_artifacts USING btree (roster_id, artifact_kind, created_at);

CREATE INDEX result_projection_nodes_current_idx
    ON public.result_projection_nodes USING btree (
        tournament_id,
        roster_id,
        artifact_kind,
        entity_id,
        revision_number DESC
    );

CREATE INDEX correction_projection_bindings_source_idx
    ON public.correction_projection_bindings USING btree (
        tournament_id,
        roster_id,
        artifact_kind,
        entity_id,
        source_id
    );

CREATE INDEX correction_projection_decisions_node_idx
    ON public.correction_projection_decisions USING btree (projection_node_id, recorded_at);

--
-- Name: projection_dependencies_artifact_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX projection_dependencies_artifact_idx ON public.projection_dependencies USING btree (artifact_id, depends_on_artifact_id) WHERE (depends_on_artifact_id IS NOT NULL);

--
-- Name: projection_dependencies_golden_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX projection_dependencies_golden_idx ON public.projection_dependencies USING btree (artifact_id, golden_position_commit_id) WHERE (golden_position_commit_id IS NOT NULL);

--
-- Name: projection_dependencies_result_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX projection_dependencies_result_idx ON public.projection_dependencies USING btree (artifact_id, official_result_revision_id) WHERE (official_result_revision_id IS NOT NULL);

--
-- Name: projection_dependencies_source_artifact_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projection_dependencies_source_artifact_idx ON public.projection_dependencies USING btree (depends_on_artifact_id) WHERE (depends_on_artifact_id IS NOT NULL);

--
-- Name: projection_revisions_one_published_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX projection_revisions_one_published_idx ON public.projection_revisions USING btree (tournament_id) WHERE ((state)::text = 'published'::text);

--
-- Name: participant_post_series_actions_participant_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX participant_post_series_actions_participant_idx ON public.participant_post_series_actions USING btree (tournament_id, participant_id, occurred_at);

--
-- Name: projection_artifacts projection_artifact_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projection_artifact_guard BEFORE INSERT OR DELETE OR UPDATE ON public.projection_artifacts FOR EACH ROW EXECUTE FUNCTION public.projection_artifact_guard();

CREATE TRIGGER result_projection_node_authorities_append_only
BEFORE DELETE OR UPDATE ON public.result_projection_node_authorities
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE CONSTRAINT TRIGGER result_projection_node_authorities_scope
AFTER INSERT ON public.result_projection_node_authorities
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.result_projection_node_authority_guard();

CREATE TRIGGER result_projection_nodes_append_only
BEFORE DELETE OR UPDATE ON public.result_projection_nodes
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER result_projection_nodes_lineage
BEFORE INSERT ON public.result_projection_nodes
FOR EACH ROW EXECUTE FUNCTION public.result_projection_node_guard();

CREATE TRIGGER final_swiss_projection_receipts_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.final_swiss_projection_receipts
FOR EACH ROW EXECUTE FUNCTION public.final_swiss_projection_receipt_chain_guard();

CREATE CONSTRAINT TRIGGER final_swiss_projection_receipts_complete
AFTER INSERT ON public.final_swiss_projection_receipts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();

-- Every retained child carries the same receipt scope used by the aggregate
-- validator. Deferral permits child-before-root construction and rechecks
-- late inserts after the root's original validation transaction has finished.
CREATE CONSTRAINT TRIGGER final_swiss_projection_receipt_participants_complete
AFTER INSERT ON public.final_swiss_projection_receipt_participants
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();

CREATE CONSTRAINT TRIGGER final_swiss_projection_receipt_rounds_complete
AFTER INSERT ON public.final_swiss_projection_receipt_rounds
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();

CREATE CONSTRAINT TRIGGER final_swiss_projection_receipt_series_complete
AFTER INSERT ON public.final_swiss_projection_receipt_series
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();

CREATE CONSTRAINT TRIGGER final_swiss_projection_receipt_games_complete
AFTER INSERT ON public.final_swiss_projection_receipt_games
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();

CREATE CONSTRAINT TRIGGER final_swiss_projection_receipt_ledger_entries_complete
AFTER INSERT ON public.final_swiss_projection_receipt_ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_final_swiss_projection_receipt();

CREATE TRIGGER final_swiss_projection_receipt_participants_append_only
BEFORE DELETE OR UPDATE ON public.final_swiss_projection_receipt_participants
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER final_swiss_projection_receipt_rounds_append_only
BEFORE DELETE OR UPDATE ON public.final_swiss_projection_receipt_rounds
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER final_swiss_projection_receipt_series_append_only
BEFORE DELETE OR UPDATE ON public.final_swiss_projection_receipt_series
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER final_swiss_projection_receipt_games_append_only
BEFORE DELETE OR UPDATE ON public.final_swiss_projection_receipt_games
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER final_swiss_projection_receipt_ledger_entries_append_only
BEFORE DELETE OR UPDATE ON public.final_swiss_projection_receipt_ledger_entries
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER result_projection_dependencies_append_only
BEFORE DELETE OR UPDATE ON public.result_projection_dependencies
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER result_projection_dependencies_authority
BEFORE INSERT ON public.result_projection_dependencies
FOR EACH ROW EXECUTE FUNCTION public.result_projection_dependency_guard();

CREATE TRIGGER correction_projection_decisions_append_only
BEFORE DELETE OR UPDATE ON public.correction_projection_decisions
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

CREATE TRIGGER correction_projection_bindings_append_only
BEFORE DELETE OR UPDATE ON public.correction_projection_bindings
FOR EACH ROW EXECUTE FUNCTION public.append_only_guard();

--
-- Name: projection_artifact_members projection_artifact_member_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projection_artifact_member_guard BEFORE INSERT OR DELETE OR UPDATE ON public.projection_artifact_members FOR EACH ROW EXECUTE FUNCTION public.projection_artifact_member_guard();

--
-- Name: projection_cutoffs projection_cutoff_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projection_cutoff_guard BEFORE INSERT OR DELETE OR UPDATE ON public.projection_cutoffs FOR EACH ROW EXECUTE FUNCTION public.projection_cutoff_guard();

--
-- Name: projection_dependencies projection_dependency_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projection_dependency_guard BEFORE INSERT OR DELETE OR UPDATE ON public.projection_dependencies FOR EACH ROW EXECUTE FUNCTION public.projection_dependency_guard();

--
-- Name: projection_revision_artifacts projection_revision_artifact_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projection_revision_artifact_guard BEFORE INSERT OR DELETE OR UPDATE ON public.projection_revision_artifacts FOR EACH ROW EXECUTE FUNCTION public.projection_revision_artifact_guard();

--
-- Name: projection_revisions projection_revision_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER projection_revision_guard BEFORE INSERT OR DELETE OR UPDATE ON public.projection_revisions FOR EACH ROW EXECUTE FUNCTION public.projection_revision_guard();

--
-- Name: participant_post_series_actions participant_post_series_action_guard; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER participant_post_series_action_guard BEFORE INSERT OR DELETE OR UPDATE ON public.participant_post_series_actions FOR EACH ROW EXECUTE FUNCTION public.participant_post_series_action_guard();

CREATE TRIGGER tournament_stage_playoff_evidence_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_evidence
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_evidence_guard();

CREATE CONSTRAINT TRIGGER tournament_stage_playoff_evidence_consistency
AFTER INSERT ON public.tournament_stage_playoff_evidence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.validate_tournament_stage_playoff_evidence();

CREATE TRIGGER tournament_stage_playoff_golden_settlements_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_golden_settlements
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_golden_settlement_guard();

CREATE TRIGGER tournament_stage_playoff_semifinals_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_semifinals
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_semifinal_guard();

CREATE TRIGGER tournament_stage_playoff_final_advancements_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_final_advancements
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_final_advancement_guard();

CREATE TRIGGER tournament_stage_playoff_finals_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_finals
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_final_guard();

CREATE TRIGGER final_draft_delivery_history_head_guard
AFTER INSERT ON public.task_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION public.final_draft_delivery_history_head_guard();

CREATE TRIGGER tournament_stage_playoff_final_initializations_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_final_initializations
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_final_initialization_guard();

CREATE TRIGGER tournament_stage_playoff_final_progressions_guard
BEFORE INSERT OR DELETE OR UPDATE ON public.tournament_stage_playoff_final_progressions
FOR EACH ROW EXECUTE FUNCTION public.tournament_stage_playoff_final_progression_guard();

--
-- Name: projection_artifact_members projection_artifact_members_artifact_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifact_members
    ADD CONSTRAINT projection_artifact_members_artifact_fk FOREIGN KEY (artifact_id, tournament_id, roster_id, artifact_kind) REFERENCES public.projection_artifacts(id, tournament_id, roster_id, artifact_kind) ON DELETE RESTRICT;

--
-- Name: projection_artifact_members projection_artifact_members_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifact_members
    ADD CONSTRAINT projection_artifact_members_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: projection_artifacts projection_artifacts_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_artifacts
    ADD CONSTRAINT projection_artifacts_revision_fk FOREIGN KEY (produced_by_revision_id, tournament_id, roster_id) REFERENCES public.projection_revisions(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: tournament_stage_progressions tournament_stage_progressions_scope_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_stage_progressions
    ADD CONSTRAINT tournament_stage_progressions_scope_key
    UNIQUE (command_id, tournament_id, roster_id);

ALTER TABLE ONLY public.outbox_stage_projection_sources
    ADD CONSTRAINT outbox_stage_projection_sources_pkey PRIMARY KEY (outbox_event_id),
    ADD CONSTRAINT outbox_stage_projection_sources_command_key UNIQUE (stage_command_id),
    ADD CONSTRAINT outbox_stage_projection_sources_revision_key UNIQUE (projection_revision_id),
    ADD CONSTRAINT outbox_stage_projection_sources_event_fk
        FOREIGN KEY (outbox_event_id, tournament_id, roster_id, projection_revision_id, projection_revision, projection_ordinal)
        REFERENCES public.outbox_events(id, tournament_id, roster_id, projection_revision_id, projection_revision, projection_ordinal) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_revision_fk
        FOREIGN KEY (projection_revision_id, tournament_id, roster_id, projection_revision)
        REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_command_fk
        FOREIGN KEY (stage_command_id, tournament_id, roster_id)
        REFERENCES public.tournament_stage_progressions(command_id, tournament_id, roster_id) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_receipt_fk
        FOREIGN KEY (swiss_receipt_projection_revision_id, tournament_id, roster_id)
        REFERENCES public.final_swiss_projection_receipts(projection_revision_id, tournament_id, roster_id) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_top4_fk
        FOREIGN KEY (top4_artifact_id, tournament_id, roster_id, top4_kind)
        REFERENCES public.projection_artifacts(id, tournament_id, roster_id, artifact_kind) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_bracket_fk
        FOREIGN KEY (bracket_artifact_id, tournament_id, roster_id, bracket_kind)
        REFERENCES public.projection_artifacts(id, tournament_id, roster_id, artifact_kind) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_top4_membership_fk
        FOREIGN KEY (projection_revision_id, top4_artifact_id)
        REFERENCES public.projection_revision_artifacts(revision_id, artifact_id) ON DELETE RESTRICT,
    ADD CONSTRAINT outbox_stage_projection_sources_bracket_membership_fk
        FOREIGN KEY (projection_revision_id, bracket_artifact_id)
        REFERENCES public.projection_revision_artifacts(revision_id, artifact_id) ON DELETE RESTRICT;

--
-- Name: projection_cutoffs projection_cutoffs_golden_position_commit_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_golden_position_commit_id_fkey FOREIGN KEY (golden_position_commit_id) REFERENCES public.golden_position_commits(id) ON DELETE RESTRICT;

--
-- Name: projection_cutoffs projection_cutoffs_official_result_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_official_result_revision_id_fkey FOREIGN KEY (official_result_revision_id) REFERENCES public.official_result_revisions(id) ON DELETE RESTRICT;

--
-- Name: projection_cutoffs projection_cutoffs_stage_progression_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_stage_progression_fk
    FOREIGN KEY (stage_progression_command_id, tournament_id, roster_id)
    REFERENCES public.tournament_stage_progressions(command_id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

--
-- Name: projection_cutoffs projection_cutoffs_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_previous_fk FOREIGN KEY (previous_cutoff_id, tournament_id, roster_id) REFERENCES public.projection_cutoffs(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_cutoffs projection_cutoffs_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_cutoffs
    ADD CONSTRAINT projection_cutoffs_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: projection_dependencies projection_dependencies_artifact_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_dependencies
    ADD CONSTRAINT projection_dependencies_artifact_fk FOREIGN KEY (artifact_id, tournament_id, roster_id) REFERENCES public.projection_artifacts(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_dependencies projection_dependencies_golden_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_dependencies
    ADD CONSTRAINT projection_dependencies_golden_fk FOREIGN KEY (golden_position_commit_id) REFERENCES public.golden_position_commits(id) ON DELETE RESTRICT;

--
-- Name: projection_dependencies projection_dependencies_parent_artifact_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_dependencies
    ADD CONSTRAINT projection_dependencies_parent_artifact_fk FOREIGN KEY (depends_on_artifact_id, tournament_id, roster_id) REFERENCES public.projection_artifacts(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_dependencies projection_dependencies_result_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_dependencies
    ADD CONSTRAINT projection_dependencies_result_fk FOREIGN KEY (official_result_revision_id, official_result_series_id, roster_id) REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_revision_artifacts projection_revision_artifacts_artifact_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revision_artifacts
    ADD CONSTRAINT projection_revision_artifacts_artifact_fk FOREIGN KEY (artifact_id, tournament_id, roster_id, artifact_kind) REFERENCES public.projection_artifacts(id, tournament_id, roster_id, artifact_kind) ON DELETE RESTRICT;

--
-- Name: projection_revision_artifacts projection_revision_artifacts_revision_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revision_artifacts
    ADD CONSTRAINT projection_revision_artifacts_revision_fk FOREIGN KEY (revision_id, tournament_id, roster_id) REFERENCES public.projection_revisions(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_revisions projection_revisions_cutoff_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_cutoff_fk FOREIGN KEY (cutoff_id, tournament_id, roster_id) REFERENCES public.projection_cutoffs(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_revisions projection_revisions_previous_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_previous_fk FOREIGN KEY (previous_revision_id, tournament_id, roster_id) REFERENCES public.projection_revisions(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: projection_revisions projection_revisions_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: projection_revisions projection_revisions_superseded_by_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projection_revisions
    ADD CONSTRAINT projection_revisions_superseded_by_fk FOREIGN KEY (superseded_by_revision_id, tournament_id, roster_id) REFERENCES public.projection_revisions(id, tournament_id, roster_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.final_swiss_projection_receipts
    ADD CONSTRAINT final_swiss_projection_receipts_revision_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.projection_revisions(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipts
    ADD CONSTRAINT final_swiss_projection_receipts_source_artifact_fk
    FOREIGN KEY (source_standings_artifact_id, tournament_id, roster_id)
    REFERENCES public.projection_artifacts(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipts
    ADD CONSTRAINT final_swiss_projection_receipts_previous_fk
    FOREIGN KEY (
        previous_receipt_projection_revision_id,
        tournament_id,
        roster_id
    )
    REFERENCES public.final_swiss_projection_receipts(
        projection_revision_id,
        tournament_id,
        roster_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_participants
    ADD CONSTRAINT final_swiss_projection_receipt_participants_receipt_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.final_swiss_projection_receipts(
        projection_revision_id,
        tournament_id,
        roster_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_participants
    ADD CONSTRAINT final_swiss_projection_receipt_participants_participant_fk
    FOREIGN KEY (roster_id, participant_id)
    REFERENCES public.participants(roster_id, id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_rounds
    ADD CONSTRAINT final_swiss_projection_receipt_rounds_receipt_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.final_swiss_projection_receipts(
        projection_revision_id,
        tournament_id,
        roster_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_rounds
    ADD CONSTRAINT final_swiss_projection_receipt_rounds_round_fk
    FOREIGN KEY (round_id, roster_id)
    REFERENCES public.swiss_rounds(id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_receipt_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.final_swiss_projection_receipts(
        projection_revision_id,
        tournament_id,
        roster_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_round_fk
    FOREIGN KEY (projection_revision_id, round_id)
    REFERENCES public.final_swiss_projection_receipt_rounds(
        projection_revision_id,
        round_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_series_fk
    FOREIGN KEY (series_id, tournament_id, roster_id)
    REFERENCES public.series(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_result_fk
    FOREIGN KEY (series_result_revision_id, series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_score_fk
    FOREIGN KEY (score_revision_id, series_id, roster_id)
    REFERENCES public.series_score_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_result_node_fk
    FOREIGN KEY (series_result_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_score_node_fk
    FOREIGN KEY (score_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_no_show_fk
    FOREIGN KEY (normal_no_show_commit_id)
    REFERENCES public.normal_no_show_commits(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_series
    ADD CONSTRAINT final_swiss_projection_receipt_series_forfeit_fk
    FOREIGN KEY (operator_forfeit_commit_id)
    REFERENCES public.operator_forfeit_commits(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_games
    ADD CONSTRAINT final_swiss_projection_receipt_games_receipt_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.final_swiss_projection_receipts(
        projection_revision_id,
        tournament_id,
        roster_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_games
    ADD CONSTRAINT final_swiss_projection_receipt_games_series_fk
    FOREIGN KEY (projection_revision_id, series_id)
    REFERENCES public.final_swiss_projection_receipt_series(
        projection_revision_id,
        series_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_games
    ADD CONSTRAINT final_swiss_projection_receipt_games_attempt_fk
    FOREIGN KEY (game_attempt_id, series_id, roster_id)
    REFERENCES public.game_attempts(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_games
    ADD CONSTRAINT final_swiss_projection_receipt_games_result_fk
    FOREIGN KEY (game_result_revision_id, series_id, roster_id)
    REFERENCES public.official_result_revisions(id, series_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_games
    ADD CONSTRAINT final_swiss_projection_receipt_games_node_fk
    FOREIGN KEY (game_result_node_id)
    REFERENCES public.result_projection_nodes(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_ledger_entries
    ADD CONSTRAINT final_swiss_projection_receipt_ledger_entries_receipt_fk
    FOREIGN KEY (projection_revision_id, tournament_id, roster_id)
    REFERENCES public.final_swiss_projection_receipts(
        projection_revision_id,
        tournament_id,
        roster_id
    )
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.final_swiss_projection_receipt_ledger_entries
    ADD CONSTRAINT final_swiss_projection_receipt_ledger_entries_ledger_fk
    FOREIGN KEY (ledger_entry_id)
    REFERENCES public.swiss_point_ledger_entries(id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

--
-- Name: participant_post_series_actions participant_post_series_actions_participant_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_participant_fk FOREIGN KEY (roster_id, participant_id) REFERENCES public.participants(roster_id, id) ON DELETE RESTRICT;

--
-- Name: participant_post_series_actions participant_post_series_actions_result_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_result_fk FOREIGN KEY (current_result_revision_id, series_id, roster_id) REFERENCES public.official_result_revisions(id, series_id, roster_id) ON DELETE RESTRICT;

--
-- Name: participant_post_series_actions participant_post_series_actions_roster_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_roster_fk FOREIGN KEY (roster_id, tournament_id) REFERENCES public.rosters(id, tournament_id) ON DELETE RESTRICT;

--
-- Name: participant_post_series_actions participant_post_series_actions_series_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_series_fk FOREIGN KEY (series_id, tournament_id, roster_id) REFERENCES public.series(id, tournament_id, roster_id) ON DELETE RESTRICT;

--
-- Name: participant_post_series_actions participant_post_series_actions_source_projection_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_source_projection_fk FOREIGN KEY (source_projection_revision_id, tournament_id, roster_id, source_projection_revision) REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number) ON DELETE RESTRICT;

--
-- Name: participant_post_series_actions participant_post_series_actions_resulting_projection_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.participant_post_series_actions
    ADD CONSTRAINT participant_post_series_actions_resulting_projection_fk FOREIGN KEY (resulting_projection_revision_id, tournament_id, roster_id, resulting_projection_revision) REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number) ON DELETE RESTRICT;

--
-- Name: tournament_roster_operations tournament_roster_operations_source_projection_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tournament_roster_operations
    ADD CONSTRAINT tournament_roster_operations_source_projection_fk FOREIGN KEY (source_projection_revision_id, tournament_id, roster_id, source_projection_revision) REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number) ON DELETE RESTRICT;

-- Golden's normalized plan snapshot is introduced in 000010, before this
-- migration owns physical projection rows. These deferred constraints close
-- the cross-domain scope proof once both authorities exist.
ALTER TABLE ONLY public.golden_group_revisions
    ADD CONSTRAINT golden_group_revisions_source_projection_fk
    FOREIGN KEY (source_projection_revision_id, tournament_id, roster_id, source_projection_revision)
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.golden_exact_plan_snapshots
    ADD CONSTRAINT golden_exact_plan_snapshot_source_projection_fk
    FOREIGN KEY (source_projection_revision_id, tournament_id, roster_id, source_projection_revision)
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.golden_exact_plan_snapshots
    ADD CONSTRAINT golden_exact_plan_snapshot_source_standings_fk
    FOREIGN KEY (source_standings_artifact_id, tournament_id, roster_id)
    REFERENCES public.projection_artifacts(id, tournament_id, roster_id)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    ADD CONSTRAINT golden_exact_plan_snapshot_group_source_projection_fk
    FOREIGN KEY (
        source_projection_revision_id,
        tournament_id,
        roster_id,
        source_projection_revision
    )
    REFERENCES public.projection_revisions(id, tournament_id, roster_id, revision_number)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION public.validate_golden_exact_plan_snapshot_source() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    artifact_digest BYTEA;
    artifact_kind VARCHAR(16);
    source_state VARCHAR(16);
    source_previous_revision_id UUID;
BEGIN
    SELECT artifact.payload_digest,
        artifact.artifact_kind,
        revision.state,
        revision.previous_revision_id
    INTO artifact_digest,
        artifact_kind,
        source_state,
        source_previous_revision_id
    FROM projection_revisions AS revision
    INNER JOIN projection_revision_artifacts AS revision_artifact
        ON revision_artifact.revision_id = revision.id
        AND revision_artifact.tournament_id = revision.tournament_id
        AND revision_artifact.roster_id = revision.roster_id
    INNER JOIN projection_artifacts AS artifact
        ON artifact.id = revision_artifact.artifact_id
        AND artifact.tournament_id = revision_artifact.tournament_id
        AND artifact.roster_id = revision_artifact.roster_id
        AND artifact.artifact_kind = revision_artifact.artifact_kind
    WHERE revision.id = NEW.source_projection_revision_id
        AND revision.tournament_id = NEW.tournament_id
        AND revision.roster_id = NEW.roster_id
        AND revision.revision_number = NEW.source_projection_revision
        AND revision_artifact.artifact_id = NEW.source_standings_artifact_id
    FOR KEY SHARE OF revision, revision_artifact, artifact;

    IF NOT FOUND
        OR artifact_kind <> 'standings'
        OR source_state NOT IN ('published', 'superseded')
        OR source_previous_revision_id IS DISTINCT FROM NEW.source_projection_previous_revision_id
        OR artifact_digest IS DISTINCT FROM NEW.source_standings_payload_digest THEN
        RAISE EXCEPTION 'Golden exact plan source must bind the exact scoped standings artifact'
            USING ERRCODE = 'check_violation';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM golden_exact_plan_snapshot_groups AS plan_group
        INNER JOIN golden_group_revisions AS group_revision
            ON group_revision.revision_id = plan_group.group_revision_id
            AND group_revision.tournament_id = plan_group.tournament_id
            AND group_revision.roster_id = plan_group.roster_id
        WHERE plan_group.plan_id = NEW.plan_id
            AND (
                plan_group.tournament_id <> NEW.tournament_id
                OR plan_group.roster_id <> NEW.roster_id
                OR plan_group.source_projection_revision_id <> NEW.source_projection_revision_id
                OR plan_group.source_projection_revision <> NEW.source_projection_revision
                OR group_revision.source_projection_revision_id <> NEW.source_projection_revision_id
                OR group_revision.source_projection_revision <> NEW.source_projection_revision
                OR group_revision.definition_digest IS DISTINCT FROM plan_group.definition_digest
            )
    ) THEN
        RAISE EXCEPTION 'Golden exact plan group must retain the same source projection as its plan'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER golden_exact_plan_snapshot_source_guard
    AFTER INSERT ON public.golden_exact_plan_snapshots
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.validate_golden_exact_plan_snapshot_source();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS final_draft_delivery_history_head_guard
ON public.task_delivery_receipts;

DROP TRIGGER IF EXISTS golden_exact_plan_snapshot_source_guard
ON public.golden_exact_plan_snapshots;

ALTER TABLE ONLY public.golden_exact_plan_snapshots
    DROP CONSTRAINT IF EXISTS golden_exact_plan_snapshot_source_standings_fk;

ALTER TABLE ONLY public.golden_exact_plan_snapshot_groups
    DROP CONSTRAINT IF EXISTS golden_exact_plan_snapshot_group_source_projection_fk;

ALTER TABLE ONLY public.golden_exact_plan_snapshots
    DROP CONSTRAINT IF EXISTS golden_exact_plan_snapshot_source_projection_fk;

ALTER TABLE ONLY public.golden_group_revisions
    DROP CONSTRAINT IF EXISTS golden_group_revisions_source_projection_fk;

ALTER TABLE ONLY public.tournament_stage_progressions
    DROP CONSTRAINT IF EXISTS tournament_stage_progressions_resulting_projection_fk;

ALTER TABLE ONLY public.tournament_stage_progressions
    DROP CONSTRAINT IF EXISTS tournament_stage_progressions_source_projection_fk;

ALTER TABLE ONLY public.outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_projection_revision_fk;

ALTER TABLE ONLY public.tournament_cancellations
    DROP CONSTRAINT IF EXISTS tournament_cancellations_outbox_fk;

ALTER TABLE ONLY public.result_commits
    DROP CONSTRAINT IF EXISTS result_commits_outbox_fk;

ALTER TABLE ONLY public.normal_no_show_commits
    DROP CONSTRAINT IF EXISTS normal_no_show_commits_outbox_fk;

ALTER TABLE ONLY public.operator_forfeit_commits
    DROP CONSTRAINT IF EXISTS operator_forfeit_commits_outbox_fk;

ALTER TABLE ONLY public.operator_forfeit_commits
    DROP CONSTRAINT IF EXISTS operator_forfeit_commits_source_projection_fk;

ALTER TABLE ONLY public.result_correction_commits
    DROP CONSTRAINT IF EXISTS result_correction_commits_source_projection_fk;

ALTER TABLE ONLY public.result_correction_commits
    DROP CONSTRAINT IF EXISTS result_correction_commits_resulting_projection_fk;

ALTER TABLE ONLY public.tournament_roster_operations
    DROP CONSTRAINT IF EXISTS tournament_roster_operations_source_projection_fk;

DROP TABLE IF EXISTS
    public.realtime_delivery_receipts,
    public.realtime_subscribers,
    public.outbox_champion_sources,
    public.outbox_stage_projection_sources,
    public.outbox_wave_sources,
    public.outbox_tournament_cancellation_sources,
    public.outbox_result_sources,
    public.outbox_events,
    public.projection_outbox_cursors,
    public.tournament_outbox_cursors,
    public.final_swiss_projection_receipt_ledger_entries,
    public.final_swiss_projection_receipt_games,
    public.final_swiss_projection_receipt_series,
    public.final_swiss_projection_receipt_rounds,
    public.final_swiss_projection_receipt_participants,
    public.final_swiss_projection_receipts,
    public.correction_projection_bindings,
    public.correction_projection_decisions,
    public.result_projection_dependencies,
    public.result_projection_nodes,
    public.result_projection_node_authorities,
    public.final_draft_delivery_history_heads,
    public.tournament_stage_playoff_final_progressions,
    public.tournament_stage_playoff_final_initializations,
    public.tournament_stage_playoff_finals,
    public.tournament_stage_playoff_final_advancements,
    public.tournament_stage_playoff_semifinals,
    public.tournament_stage_playoff_golden_settlements,
    public.tournament_stage_playoff_evidence,
    public.participant_post_series_actions,
    public.projection_dependencies,
    public.projection_artifact_members,
    public.projection_revision_artifacts,
    public.projection_artifacts,
    public.projection_revisions,
    public.projection_cutoffs;

ALTER TABLE ONLY public.tournament_stage_progressions
    DROP CONSTRAINT IF EXISTS tournament_stage_progressions_scope_key;

DROP FUNCTION IF EXISTS
    public.validate_golden_exact_plan_snapshot_source(),
    public.realtime_subscriber_guard(),
    public.realtime_delivery_receipt_guard(),
    public.outbox_champion_source_guard(),
    public.outbox_tournament_cancellation_source_guard(),
    public.outbox_wave_source_guard(),
    public.validate_outbox_event_source(),
    public.outbox_event_guard(),
    public.validate_tournament_stage_playoff_evidence(),
    public.outbox_stage_projection_source_guard(),
    public.validate_outbox_source_membership(),
    public.tournament_stage_playoff_final_progression_guard(),
    public.tournament_stage_playoff_final_initialization_guard(),
    public.final_draft_delivery_history_head_guard(),
    public.tournament_stage_playoff_final_guard(),
    public.tournament_stage_playoff_final_advancement_guard(),
    public.tournament_stage_playoff_semifinal_guard(),
    public.tournament_stage_playoff_golden_settlement_guard(),
    public.tournament_stage_playoff_evidence_guard(),
    public.outbox_payload_has_private_field(jsonb),
    public.validate_final_swiss_projection_receipt(),
    public.final_swiss_projection_receipt_chain_guard(),
    public.result_projection_dependency_guard(),
    public.result_projection_node_authority_guard(),
    public.result_projection_node_guard(),
    public.participant_post_series_action_guard(),
    public.projection_revision_guard(),
    public.projection_revision_artifact_guard(),
    public.projection_dependency_guard(),
    public.projection_cutoff_guard(),
    public.projection_artifact_member_guard(),
    public.projection_artifact_guard();

-- +goose StatementEnd
