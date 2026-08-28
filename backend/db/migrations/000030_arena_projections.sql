-- +goose Up

CREATE TABLE arena_projection_cutoffs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    sequence_number BIGINT NOT NULL,
    previous_cutoff_id UUID,
    source_kind VARCHAR(24) NOT NULL,
    official_result_revision_id UUID REFERENCES arena_official_result_revisions(id)
        ON DELETE RESTRICT,
    golden_position_commit_id UUID REFERENCES arena_golden_position_commits(id)
        ON DELETE RESTRICT,
    reason TEXT NOT NULL,
    cutoff_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_projection_cutoffs_identity_key
        UNIQUE (id, tournament_id, roster_id),
    CONSTRAINT arena_projection_cutoffs_sequence_key
        UNIQUE (tournament_id, sequence_number),
    CONSTRAINT arena_projection_cutoffs_previous_key UNIQUE (previous_cutoff_id),
    CONSTRAINT arena_projection_cutoffs_roster_fk FOREIGN KEY (
        roster_id,
        tournament_id
    ) REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_cutoffs_previous_fk FOREIGN KEY (
        previous_cutoff_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_cutoffs (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_cutoffs_sequence_check CHECK (
        sequence_number >= 1
        AND (
            (sequence_number = 1 AND previous_cutoff_id IS NULL)
            OR (sequence_number > 1 AND previous_cutoff_id IS NOT NULL)
        )
    ),
    CONSTRAINT arena_projection_cutoffs_source_check CHECK (
        (
            source_kind = 'initial'
            AND official_result_revision_id IS NULL
            AND golden_position_commit_id IS NULL
        )
        OR (
            source_kind = 'official_result'
            AND official_result_revision_id IS NOT NULL
            AND golden_position_commit_id IS NULL
        )
        OR (
            source_kind = 'golden_position'
            AND official_result_revision_id IS NULL
            AND golden_position_commit_id IS NOT NULL
        )
        OR (
            source_kind = 'operator_rebuild'
            AND official_result_revision_id IS NULL
            AND golden_position_commit_id IS NULL
        )
    ),
    CONSTRAINT arena_projection_cutoffs_reason_check CHECK (
        reason = BTRIM(reason) AND reason <> ''
    ),
    CONSTRAINT arena_projection_cutoffs_timestamps_check CHECK (
        cutoff_at <= created_at
    )
);

CREATE TABLE arena_projection_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    revision_number BIGINT NOT NULL,
    previous_revision_id UUID,
    cutoff_id UUID NOT NULL UNIQUE,
    state VARCHAR(16) NOT NULL DEFAULT 'draft',
    published_at TIMESTAMPTZ,
    superseded_by_revision_id UUID,
    superseded_at TIMESTAMPTZ,
    supersession_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_projection_revisions_identity_key
        UNIQUE (id, tournament_id, roster_id),
    CONSTRAINT arena_projection_revisions_number_key
        UNIQUE (tournament_id, revision_number),
    CONSTRAINT arena_projection_revisions_previous_key UNIQUE (previous_revision_id),
    CONSTRAINT arena_projection_revisions_roster_fk FOREIGN KEY (
        roster_id,
        tournament_id
    ) REFERENCES arena_rosters(id, tournament_id) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_revisions_previous_fk FOREIGN KEY (
        previous_revision_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_revisions (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_revisions_cutoff_fk FOREIGN KEY (
        cutoff_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_cutoffs (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_revisions_number_check CHECK (
        revision_number >= 1
        AND (
            (revision_number = 1 AND previous_revision_id IS NULL)
            OR (revision_number > 1 AND previous_revision_id IS NOT NULL)
        )
    ),
    CONSTRAINT arena_projection_revisions_state_check CHECK (
        (
            state = 'draft'
            AND published_at IS NULL
            AND superseded_by_revision_id IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'published'
            AND published_at IS NOT NULL
            AND superseded_by_revision_id IS NULL
            AND superseded_at IS NULL
            AND supersession_reason IS NULL
        )
        OR (
            state = 'superseded'
            AND published_at IS NOT NULL
            AND superseded_by_revision_id IS NOT NULL
            AND superseded_at IS NOT NULL
            AND supersession_reason = BTRIM(supersession_reason)
            AND supersession_reason <> ''
        )
    ),
    CONSTRAINT arena_projection_revisions_timestamps_check CHECK (
        (published_at IS NULL OR published_at >= created_at)
        AND (superseded_at IS NULL OR superseded_at >= published_at)
    )
);

ALTER TABLE arena_projection_revisions
    ADD CONSTRAINT arena_projection_revisions_superseded_by_fk FOREIGN KEY (
        superseded_by_revision_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_revisions (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT;

CREATE UNIQUE INDEX arena_projection_revisions_one_published_idx
    ON arena_projection_revisions (tournament_id)
    WHERE state = 'published';

CREATE TABLE arena_projection_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    produced_by_revision_id UUID NOT NULL,
    artifact_kind VARCHAR(16) NOT NULL,
    artifact_key VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL,
    payload_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_projection_artifacts_identity_key UNIQUE (
        id,
        tournament_id,
        roster_id,
        artifact_kind
    ),
    CONSTRAINT arena_projection_artifacts_scope_key UNIQUE (
        id,
        tournament_id,
        roster_id
    ),
    CONSTRAINT arena_projection_artifacts_revision_kind_key
        UNIQUE (produced_by_revision_id, artifact_kind, artifact_key),
    CONSTRAINT arena_projection_artifacts_revision_fk FOREIGN KEY (
        produced_by_revision_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_revisions (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_artifacts_key_check CHECK (
        artifact_key = BTRIM(artifact_key) AND artifact_key <> ''
    ),
    CONSTRAINT arena_projection_artifacts_payload_check CHECK (
        (
            artifact_kind = 'standings'
            AND jsonb_typeof(payload -> 'entries') = 'array'
            AND jsonb_array_length(payload -> 'entries') > 0
        )
        OR (
            artifact_kind = 'bracket'
            AND jsonb_typeof(payload -> 'rounds') = 'array'
            AND jsonb_array_length(payload -> 'rounds') > 0
        )
        OR (
            artifact_kind = 'top4'
            AND jsonb_typeof(payload -> 'participants') = 'array'
            AND jsonb_array_length(payload -> 'participants') = 4
        )
        OR (
            artifact_kind = 'champion'
            AND jsonb_typeof(payload -> 'participant_id') = 'string'
            AND BTRIM(payload ->> 'participant_id') <> ''
        )
    ),
    CONSTRAINT arena_projection_artifacts_digest_check CHECK (
        octet_length(payload_digest) = 32
        AND payload_digest <> decode(repeat('00', 32), 'hex')
    )
);

CREATE TABLE arena_projection_revision_artifacts (
    revision_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    artifact_kind VARCHAR(16) NOT NULL,
    artifact_id UUID NOT NULL,
    change_kind VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (revision_id, artifact_kind),
    CONSTRAINT arena_projection_revision_artifacts_revision_artifact_key
        UNIQUE (revision_id, artifact_id),
    CONSTRAINT arena_projection_revision_artifacts_revision_fk FOREIGN KEY (
        revision_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_revisions (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_revision_artifacts_artifact_fk FOREIGN KEY (
        artifact_id,
        tournament_id,
        roster_id,
        artifact_kind
    ) REFERENCES arena_projection_artifacts (
        id,
        tournament_id,
        roster_id,
        artifact_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_revision_artifacts_change_check CHECK (
        change_kind IN ('produced', 'reused')
    )
);

CREATE TABLE arena_projection_artifact_members (
    artifact_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    artifact_kind VARCHAR(16) NOT NULL,
    participant_id UUID NOT NULL,
    position INTEGER NOT NULL,
    score NUMERIC(12, 3),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (artifact_id, participant_id),
    CONSTRAINT arena_projection_artifact_members_position_key
        UNIQUE (artifact_id, position),
    CONSTRAINT arena_projection_artifact_members_artifact_fk FOREIGN KEY (
        artifact_id,
        tournament_id,
        roster_id,
        artifact_kind
    ) REFERENCES arena_projection_artifacts (
        id,
        tournament_id,
        roster_id,
        artifact_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_artifact_members_participant_fk FOREIGN KEY (
        roster_id,
        participant_id
    ) REFERENCES arena_participants(roster_id, id) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_artifact_members_position_check CHECK (
        position >= 1
        AND (
            (artifact_kind = 'standings' AND score IS NOT NULL AND score >= 0)
            OR (artifact_kind = 'bracket' AND score IS NULL)
            OR (artifact_kind = 'top4' AND position <= 4 AND score IS NULL)
            OR (artifact_kind = 'champion' AND position = 1 AND score IS NULL)
        )
    )
);

CREATE TABLE arena_projection_dependencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL,
    tournament_id UUID NOT NULL,
    roster_id UUID NOT NULL,
    dependency_kind VARCHAR(24) NOT NULL,
    depends_on_artifact_id UUID,
    official_result_revision_id UUID,
    official_result_series_id UUID,
    golden_position_commit_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT arena_projection_dependencies_artifact_fk FOREIGN KEY (
        artifact_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_artifacts (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_dependencies_parent_artifact_fk FOREIGN KEY (
        depends_on_artifact_id,
        tournament_id,
        roster_id
    ) REFERENCES arena_projection_artifacts (
        id,
        tournament_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_dependencies_result_fk FOREIGN KEY (
        official_result_revision_id,
        official_result_series_id,
        roster_id
    ) REFERENCES arena_official_result_revisions (
        id,
        series_id,
        roster_id
    ) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_dependencies_golden_fk FOREIGN KEY (
        golden_position_commit_id
    ) REFERENCES arena_golden_position_commits(id) ON DELETE RESTRICT,
    CONSTRAINT arena_projection_dependencies_shape_check CHECK (
        (
            dependency_kind = 'artifact'
            AND depends_on_artifact_id IS NOT NULL
            AND official_result_revision_id IS NULL
            AND official_result_series_id IS NULL
            AND golden_position_commit_id IS NULL
        )
        OR (
            dependency_kind = 'official_result'
            AND depends_on_artifact_id IS NULL
            AND official_result_revision_id IS NOT NULL
            AND official_result_series_id IS NOT NULL
            AND golden_position_commit_id IS NULL
        )
        OR (
            dependency_kind = 'golden_position'
            AND depends_on_artifact_id IS NULL
            AND official_result_revision_id IS NULL
            AND official_result_series_id IS NULL
            AND golden_position_commit_id IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX arena_projection_dependencies_artifact_idx
    ON arena_projection_dependencies (artifact_id, depends_on_artifact_id)
    WHERE depends_on_artifact_id IS NOT NULL;

CREATE UNIQUE INDEX arena_projection_dependencies_result_idx
    ON arena_projection_dependencies (artifact_id, official_result_revision_id)
    WHERE official_result_revision_id IS NOT NULL;

CREATE UNIQUE INDEX arena_projection_dependencies_golden_idx
    ON arena_projection_dependencies (artifact_id, golden_position_commit_id)
    WHERE golden_position_commit_id IS NOT NULL;

CREATE INDEX arena_projection_artifacts_roster_kind_idx
    ON arena_projection_artifacts (roster_id, artifact_kind, created_at);

CREATE INDEX arena_projection_dependencies_source_artifact_idx
    ON arena_projection_dependencies (depends_on_artifact_id)
    WHERE depends_on_artifact_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION arena_projection_cutoff_guard() RETURNS TRIGGER AS $$
DECLARE
    previous_sequence BIGINT;
    source_tournament_id UUID;
    source_roster_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Arena projection cutoffs are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.previous_cutoff_id IS NOT NULL THEN
        SELECT sequence_number INTO previous_sequence
        FROM arena_projection_cutoffs
        WHERE id = NEW.previous_cutoff_id
        FOR KEY SHARE;

        IF previous_sequence <> NEW.sequence_number - 1 THEN
            RAISE EXCEPTION 'Arena projection cutoff lineage must be consecutive'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF NEW.official_result_revision_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM arena_official_result_revisions
        WHERE id = NEW.official_result_revision_id
        FOR KEY SHARE;
    ELSIF NEW.golden_position_commit_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM arena_golden_position_commits
        WHERE id = NEW.golden_position_commit_id
        FOR KEY SHARE;
    END IF;

    IF source_tournament_id IS NOT NULL
        AND (
            source_tournament_id <> NEW.tournament_id
            OR source_roster_id <> NEW.roster_id
        ) THEN
        RAISE EXCEPTION 'Arena projection cutoff source is outside its roster'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_projection_cutoff_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_projection_cutoffs
FOR EACH ROW EXECUTE FUNCTION arena_projection_cutoff_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_projection_revision_guard() RETURNS TRIGGER AS $$
DECLARE
    cutoff_sequence BIGINT;
    previous_number BIGINT;
    previous_state VARCHAR(16);
    previous_replacement_id UUID;
    replacement_previous_id UUID;
    replacement_state VARCHAR(16);
    linked_kind_count INTEGER;
    dependency_missing_count INTEGER;
    invalid_member_count INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT sequence_number INTO cutoff_sequence
        FROM arena_projection_cutoffs
        WHERE id = NEW.cutoff_id
        FOR KEY SHARE;

        IF cutoff_sequence <> NEW.revision_number THEN
            RAISE EXCEPTION 'Arena projection revision must use its matching cutoff sequence'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT revision_number INTO previous_number
            FROM arena_projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR KEY SHARE;

            IF previous_number <> NEW.revision_number - 1 THEN
                RAISE EXCEPTION 'Arena projection revision lineage must be consecutive'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;

        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Arena projection revisions are retained'
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
        RAISE EXCEPTION 'Arena projection revision identity and lifecycle evidence are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state
        AND NOT (
            (OLD.state = 'draft' AND NEW.state = 'published')
            OR (OLD.state = 'published' AND NEW.state = 'superseded')
        ) THEN
        RAISE EXCEPTION 'invalid Arena projection revision transition'
            USING ERRCODE = 'check_violation';
    END IF;

    IF OLD.state = 'draft' AND NEW.state = 'published' THEN
        SELECT COUNT(DISTINCT artifact_kind)
        INTO linked_kind_count
        FROM arena_projection_revision_artifacts
        WHERE revision_id = NEW.id
            AND artifact_kind IN ('standings', 'bracket', 'top4', 'champion');

        SELECT COUNT(*) INTO dependency_missing_count
        FROM arena_projection_revision_artifacts AS revision_artifact
        WHERE revision_artifact.revision_id = NEW.id
            AND NOT EXISTS (
                SELECT 1
                FROM arena_projection_dependencies AS dependency
                WHERE dependency.artifact_id = revision_artifact.artifact_id
            );

        SELECT COUNT(*) INTO invalid_member_count
        FROM arena_projection_revision_artifacts AS revision_artifact
        INNER JOIN arena_projection_artifacts AS artifact
            ON artifact.id = revision_artifact.artifact_id
        LEFT JOIN LATERAL (
            SELECT COUNT(*) AS member_count
            FROM arena_projection_artifact_members AS artifact_member
            WHERE artifact_member.artifact_id = artifact.id
        ) AS member_evidence ON TRUE
        WHERE revision_artifact.revision_id = NEW.id
            AND (
                (
                    artifact.artifact_kind IN ('standings', 'bracket')
                    AND member_evidence.member_count < 1
                )
                OR (
                    artifact.artifact_kind = 'top4'
                    AND member_evidence.member_count <> 4
                )
                OR (
                    artifact.artifact_kind = 'champion'
                    AND member_evidence.member_count <> 1
                )
            );

        IF linked_kind_count <> 4
            OR dependency_missing_count <> 0
            OR invalid_member_count <> 0 THEN
            RAISE EXCEPTION 'published Arena projection requires four artifacts with lineage'
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.previous_revision_id IS NOT NULL THEN
            SELECT state, superseded_by_revision_id
            INTO previous_state, previous_replacement_id
            FROM arena_projection_revisions
            WHERE id = NEW.previous_revision_id
            FOR NO KEY UPDATE;

            IF previous_state <> 'superseded'
                OR previous_replacement_id <> NEW.id THEN
                RAISE EXCEPTION 'previous Arena projection must atomically supersede to replacement'
                    USING ERRCODE = 'check_violation';
            END IF;
        END IF;
    ELSIF OLD.state = 'published' AND NEW.state = 'superseded' THEN
        SELECT previous_revision_id, state
        INTO replacement_previous_id, replacement_state
        FROM arena_projection_revisions
        WHERE id = NEW.superseded_by_revision_id
        FOR NO KEY UPDATE;

        IF replacement_previous_id <> OLD.id OR replacement_state <> 'draft' THEN
            RAISE EXCEPTION 'Arena projection supersession must target its draft successor'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_projection_revision_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_projection_revisions
FOR EACH ROW EXECUTE FUNCTION arena_projection_revision_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_projection_artifact_guard() RETURNS TRIGGER AS $$
DECLARE
    revision_state VARCHAR(16);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Arena projection artifacts are immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state INTO revision_state
    FROM arena_projection_revisions
    WHERE id = NEW.produced_by_revision_id
    FOR NO KEY UPDATE;

    IF revision_state <> 'draft' THEN
        RAISE EXCEPTION 'Arena projection artifacts can only be produced by a draft revision'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_projection_artifact_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_projection_artifacts
FOR EACH ROW EXECUTE FUNCTION arena_projection_artifact_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_projection_revision_artifact_guard() RETURNS TRIGGER AS $$
DECLARE
    revision_state VARCHAR(16);
    revision_number BIGINT;
    produced_revision_id UUID;
    produced_revision_number BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Arena projection revision artifact membership is immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT state, arena_projection_revisions.revision_number
    INTO revision_state, revision_number
    FROM arena_projection_revisions
    WHERE id = NEW.revision_id
    FOR NO KEY UPDATE;

    SELECT artifact.produced_by_revision_id, producer.revision_number
    INTO produced_revision_id, produced_revision_number
    FROM arena_projection_artifacts AS artifact
    INNER JOIN arena_projection_revisions AS producer
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
        RAISE EXCEPTION 'invalid Arena projection artifact revision membership'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_projection_revision_artifact_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_projection_revision_artifacts
FOR EACH ROW EXECUTE FUNCTION arena_projection_revision_artifact_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_projection_artifact_member_guard() RETURNS TRIGGER AS $$
DECLARE
    revision_state VARCHAR(16);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Arena projection artifact membership is immutable evidence'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision.state INTO revision_state
    FROM arena_projection_artifacts AS artifact
    INNER JOIN arena_projection_revisions AS revision
        ON revision.id = artifact.produced_by_revision_id
    WHERE artifact.id = NEW.artifact_id
    FOR NO KEY UPDATE OF revision;

    IF revision_state <> 'draft' THEN
        RAISE EXCEPTION 'published Arena projection membership cannot be extended'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_projection_artifact_member_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_projection_artifact_members
FOR EACH ROW EXECUTE FUNCTION arena_projection_artifact_member_guard();

-- +goose StatementBegin
CREATE FUNCTION arena_projection_dependency_guard() RETURNS TRIGGER AS $$
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
        RAISE EXCEPTION 'Arena projection dependency edges are immutable'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT revision.id, revision.revision_number, revision.state
    INTO target_revision_id, target_revision_number, target_revision_state
    FROM arena_projection_artifacts AS artifact
    INNER JOIN arena_projection_revisions AS revision
        ON revision.id = artifact.produced_by_revision_id
    WHERE artifact.id = NEW.artifact_id
    FOR NO KEY UPDATE OF revision;

    IF target_revision_state <> 'draft' THEN
        RAISE EXCEPTION 'published Arena projection lineage cannot be extended'
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.depends_on_artifact_id IS NOT NULL THEN
        IF NEW.depends_on_artifact_id = NEW.artifact_id THEN
            RAISE EXCEPTION 'Arena projection artifact cannot depend on itself'
                USING ERRCODE = 'check_violation';
        END IF;

        SELECT revision.revision_number
        INTO source_revision_number
        FROM arena_projection_artifacts AS artifact
        INNER JOIN arena_projection_revisions AS revision
            ON revision.id = artifact.produced_by_revision_id
        WHERE artifact.id = NEW.depends_on_artifact_id
        FOR KEY SHARE OF artifact, revision;

        IF source_revision_number > target_revision_number THEN
            RAISE EXCEPTION 'Arena projection cannot depend on a future revision'
                USING ERRCODE = 'check_violation';
        END IF;

        WITH RECURSIVE dependency_path(artifact_id) AS (
            SELECT NEW.depends_on_artifact_id
            UNION
            SELECT dependency.depends_on_artifact_id
            FROM arena_projection_dependencies AS dependency
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
            RAISE EXCEPTION 'Arena projection dependency graph must remain acyclic'
                USING ERRCODE = 'check_violation';
        END IF;
    ELSIF NEW.official_result_revision_id IS NOT NULL THEN
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM arena_official_result_revisions
        WHERE id = NEW.official_result_revision_id
        FOR KEY SHARE;
    ELSE
        SELECT tournament_id, roster_id
        INTO source_tournament_id, source_roster_id
        FROM arena_golden_position_commits
        WHERE id = NEW.golden_position_commit_id
        FOR KEY SHARE;
    END IF;

    IF source_tournament_id IS NOT NULL
        AND (
            source_tournament_id <> NEW.tournament_id
            OR source_roster_id <> NEW.roster_id
        ) THEN
        RAISE EXCEPTION 'Arena projection dependency is outside its roster'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER arena_projection_dependency_guard
BEFORE INSERT OR UPDATE OR DELETE ON arena_projection_dependencies
FOR EACH ROW EXECUTE FUNCTION arena_projection_dependency_guard();

-- +goose Down

DROP TABLE IF EXISTS arena_projection_dependencies;
DROP TABLE IF EXISTS arena_projection_artifact_members;
DROP TABLE IF EXISTS arena_projection_revision_artifacts;
DROP TABLE IF EXISTS arena_projection_artifacts;
DROP TABLE IF EXISTS arena_projection_revisions;
DROP TABLE IF EXISTS arena_projection_cutoffs;

DROP FUNCTION IF EXISTS arena_projection_dependency_guard();
DROP FUNCTION IF EXISTS arena_projection_artifact_member_guard();
DROP FUNCTION IF EXISTS arena_projection_revision_artifact_guard();
DROP FUNCTION IF EXISTS arena_projection_artifact_guard();
DROP FUNCTION IF EXISTS arena_projection_revision_guard();
DROP FUNCTION IF EXISTS arena_projection_cutoff_guard();
