//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/goldenseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type goldenTerminalProof struct {
	id, tournamentID, rosterID, groupID, attemptID, membershipID, participantID uuid.UUID
	revision                                                                    int64
	position                                                                    int16
	deadline, recordedAt, createdAt                                             time.Time
	digest                                                                      []byte
}

func TestGoldenTerminalMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "golden_terminal_upgrade")
	dir := bootstrap.ResolveMigrationsDir("db/migrations")
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, dir, 28))
	previousPool := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previousPool })

	proof := createGoldenTerminalFixture(ctx, t, pool, true, false)
	before := readGoldenTerminalLegacyRows(ctx, t, pool, proof.attemptID)
	require.NoError(t, goose.UpToContext(ctx, database, dir, 29))
	require.Equal(t, before, readGoldenTerminalLegacyRows(ctx, t, pool, proof.attemptID),
		"28 -> 29 must preserve every existing accepted row and runtime fact")

	for _, testCase := range []struct {
		name    string
		edit    func(*goldenTerminalProof)
		message string
	}{
		{"premature", func(p *goldenTerminalProof) { p.recordedAt = p.deadline.Add(-time.Microsecond) }, "exact elapsed unfinalized deadline"},
		{"wrong deadline", func(p *goldenTerminalProof) { p.deadline = p.deadline.Add(-time.Microsecond) }, "exact elapsed unfinalized deadline"},
		{"stale head", func(p *goldenTerminalProof) { p.revision-- }, "current runtime head"},
		{"fake participant", func(p *goldenTerminalProof) { p.participantID = uuid.New() }, "exact participating membership"},
		{"fake membership", func(p *goldenTerminalProof) { p.membershipID = uuid.New() }, "exact participating membership"},
		{"fake group", func(p *goldenTerminalProof) { p.groupID = uuid.New() }, "exact elapsed unfinalized deadline"},
		{"wrong position", func(p *goldenTerminalProof) { p.position++ }, "contiguous committed group prefix"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			invalid := proof
			testCase.edit(&invalid)
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			err = insertGoldenTerminalProof(ctx, tx, invalid)
			requireGoldenTerminalConstraint(t, err, "golden_terminal_position_evidence_guard")
			require.ErrorContains(t, err, testCase.message)
		})
	}
	for _, testCase := range []struct {
		name string
		edit func(*goldenTerminalProof)
	}{
		{"zero digest", func(p *goldenTerminalProof) { p.digest = make([]byte, 32) }},
		{"short digest", func(p *goldenTerminalProof) { p.digest = []byte{1} }},
		{"two timestamps", func(p *goldenTerminalProof) { p.createdAt = p.recordedAt.Add(time.Microsecond) }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			invalid := proof
			testCase.edit(&invalid)
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			requireGoldenTerminalConstraint(t, insertGoldenTerminalProof(ctx, tx, invalid), "golden_terminal_position_evidence_values_check")
		})
	}

	t.Run("orphan is rejected at commit", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertGoldenTerminalProof(ctx, tx, proof))
		requireGoldenTerminalConstraint(t, tx.Commit(ctx), "golden_terminal_position_commit_required")
		var count int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM golden_terminal_position_evidence`).Scan(&count))
		require.Zero(t, count, "failed commit must not consume the group's terminal slot")
	})

	t.Run("head lock rejects a concurrently stale capture", func(t *testing.T) {
		assertGoldenTerminalHeadFence(ctx, t, pool, proof)
		proof.revision++
	})

	t.Run("commit binds provenance and timestamp", func(t *testing.T) {
		for _, change := range []string{"timestamp", "position", "both provenances", "neither provenance"} {
			t.Run(change, func(t *testing.T) {
				tx, err := pool.Begin(ctx)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback(ctx) }()
				require.NoError(t, insertGoldenTerminalProof(ctx, tx, proof))
				invalid := proof
				var submissionID, evidenceID any
				evidenceID = proof.id
				constraint := "golden_position_commits_terminal_fk"
				switch change {
				case "timestamp":
					invalid.recordedAt = invalid.recordedAt.Add(time.Microsecond)
				case "position":
					invalid.position++
				case "both provenances":
					submissionID = uuid.New()
					constraint = "golden_position_commits_provenance_check"
				case "neither provenance":
					evidenceID = nil
					constraint = "golden_position_commits_provenance_check"
				}
				requireGoldenTerminalConstraint(t, insertGoldenTerminalCommit(ctx, tx, invalid, submissionID, evidenceID), constraint)
			})
		}
	})

	t.Run("proof commit ledger and finalization are atomic", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertGoldenTerminalProof(ctx, tx, proof))
		require.NoError(t, insertGoldenTerminalCommit(ctx, tx, proof, nil, proof.id))
		ledgerID := sealGoldenTerminalLedger(ctx, t, tx, proof)
		_, err = tx.Exec(ctx, `UPDATE golden_attempts SET state = 'completed', completed_at = $2 WHERE id = $1`, proof.attemptID, proof.recordedAt)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `UPDATE golden_runtime_assignments SET settlement_revision_id = $2, finalized_at = $3 WHERE attempt_id = $1`, proof.attemptID, ledgerID, proof.recordedAt)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `UPDATE golden_runtime_heads SET revision = revision + 1, updated_at = $2 WHERE tournament_id = $1`, proof.tournamentID, proof.recordedAt)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx), "deferred evidence check must allow legitimate finalization and head advance")

		var submissions, revisions, commits, terminalCommits int
		require.NoError(t, pool.QueryRow(ctx, `SELECT
			(SELECT COUNT(*) FROM golden_provisional_submissions WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM golden_attempt_submission_revisions WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM golden_position_commits WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM golden_position_commits WHERE attempt_id = $1 AND provisional_submission_id IS NULL AND terminal_evidence_id = $2)`, proof.attemptID, proof.id,
		).Scan(&submissions, &revisions, &commits, &terminalCommits))
		require.Equal(t, 1, submissions)
		require.Equal(t, 1, revisions, "terminal placement must retain the real submission watermark")
		require.Equal(t, 2, commits)
		require.Equal(t, 1, terminalCommits)
	})

	t.Run("immutable replay and failed Down", func(t *testing.T) {
		var replayID uuid.UUID
		var replayDigest []byte
		require.NoError(t, pool.QueryRow(ctx, `SELECT id, payload_digest FROM golden_terminal_position_evidence WHERE tournament_id = $1 AND roster_id = $2 AND group_revision_id = $3`, proof.tournamentID, proof.rosterID, proof.groupID).Scan(&replayID, &replayDigest))
		require.Equal(t, proof.id, replayID)
		require.Equal(t, proof.digest, replayDigest)
		for _, statement := range []string{
			`UPDATE golden_terminal_position_evidence SET payload_digest = payload_digest WHERE id = $1`,
			`DELETE FROM golden_terminal_position_evidence WHERE id = $1`,
		} {
			_, err := pool.Exec(ctx, statement, proof.id)
			requireGoldenTerminalConstraint(t, err, "golden_terminal_position_evidence_guard")
		}
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		requireGoldenTerminalConstraint(t, insertGoldenTerminalProof(ctx, tx, proof), "golden_terminal_position_evidence_guard")
		require.NoError(t, tx.Rollback(ctx))
		legacy := readGoldenTerminalLegacyRows(ctx, t, pool, proof.attemptID)
		err = goose.DownContext(ctx, database, dir)
		require.ErrorContains(t, err, "keep version 29 and use a forward migration")
		version, err := goose.GetDBVersionContext(ctx, database)
		require.NoError(t, err)
		require.EqualValues(t, 29, version)
		require.Equal(t, legacy, readGoldenTerminalLegacyRows(ctx, t, pool, proof.attemptID))
		var count int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM golden_terminal_position_evidence WHERE id = $1`, proof.id).Scan(&count))
		require.Equal(t, 1, count)
	})

	for _, noShow := range []bool{false, true} {
		name := "no accepted solve"
		if noShow {
			name = "no-show submission is not a genuine solve"
		}
		t.Run(name, func(t *testing.T) {
			invalid := createGoldenTerminalFixture(ctx, t, pool, noShow, noShow)
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			err = insertGoldenTerminalProof(ctx, tx, invalid)
			requireGoldenTerminalConstraint(t, err, "golden_terminal_position_evidence_guard")
			require.ErrorContains(t, err, "genuine accepted solve")
		})
	}
}

func TestGoldenTerminalMigrationEmptyRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()
	_, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "golden_terminal_empty")
	dir := bootstrap.ResolveMigrationsDir("db/migrations")
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, dir, 29))
	require.NoError(t, goose.DownContext(ctx, database, dir))
	version, err := goose.GetDBVersionContext(ctx, database)
	require.NoError(t, err)
	require.EqualValues(t, 28, version)
	var nullable string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_name = 'golden_position_commits' AND column_name = 'provisional_submission_id'`).Scan(&nullable))
	require.Equal(t, "NO", nullable)
	require.NoError(t, goose.UpToContext(ctx, database, dir, 29))
}

func TestGoldenTerminalMigrationRejectsMultipleUnresolved(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "golden_terminal_multiple")
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, database, bootstrap.ResolveMigrationsDir("db/migrations")))
	previousPool := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previousPool })

	// Reuse the real three-way Swiss tie fixture; no snapshot, membership or
	// result guards are disabled to manufacture a larger unresolved group.
	swiss := prepareGoldenThreeMemberSwiss(t)
	sourceID, sourceRevision := currentPublishedProjection(ctx, t, swiss.tournamentID, swiss.rosterID)
	require.NoError(t, publishSwissGolden(ctx, swiss, tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: swiss.tournamentID, RosterID: swiss.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: sourceRevision, Action: tournamentprogression.ActionStartGolden,
	}))
	ensureGoldenRuntimeTestCapacity(ctx, t, swiss.tournamentID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	planID := createGoldenRuntimeTestPlan(ctx, t, swiss.tournamentID, swiss.rosterID, sourceID, sourceRevision, now)
	fixture := goldenExactPlanAuthorityFixture{
		golden:             goldenMigrationFixture{tournamentID: swiss.tournamentID, rosterID: swiss.rosterID, createdAt: now},
		sourceProjectionID: sourceID, sourceProjectionRevision: sourceRevision,
	}
	require.NoError(t, pool.QueryRow(ctx, `SELECT revision_id FROM golden_group_revisions WHERE tournament_id = $1 AND position_to - position_from = 2`, swiss.tournamentID).Scan(&fixture.groupRevisionID))
	rows, err := pool.Query(ctx, `SELECT participant_id FROM golden_exact_plan_snapshot_members WHERE plan_id = $1 AND group_revision_id = $2 ORDER BY position`, planID, fixture.groupRevisionID)
	require.NoError(t, err)
	fixture.golden.participantIDs, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	require.NoError(t, err)
	require.Len(t, fixture.golden.participantIDs, 3)
	proof := createGoldenTerminalRuntime(ctx, t, pool, fixture, planID, true, false)
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = insertGoldenTerminalProof(ctx, tx, proof)
	requireGoldenTerminalConstraint(t, err, "golden_terminal_position_evidence_guard")
	require.ErrorContains(t, err, "sole unresolved group participant")
}

func createGoldenTerminalFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool, accepted, noShow bool) goldenTerminalProof {
	t.Helper()
	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	planID := createSealedGoldenExactPlan(ctx, t, fixture)
	return createGoldenTerminalRuntime(ctx, t, pool, fixture, planID, accepted, noShow)
}

func createGoldenTerminalRuntime(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fixture goldenExactPlanAuthorityFixture, planID uuid.UUID, accepted, noShow bool) goldenTerminalProof {
	t.Helper()
	startedAt := fixture.golden.createdAt.Add(2 * time.Minute)
	var positionFrom int16
	require.NoError(t, pool.QueryRow(ctx, `SELECT position_from FROM golden_group_revisions WHERE revision_id = $1`, fixture.groupRevisionID).Scan(&positionFrom))
	proof := goldenTerminalProof{
		id: uuid.New(), tournamentID: fixture.golden.tournamentID, rosterID: fixture.golden.rosterID,
		groupID: fixture.groupRevisionID, participantID: fixture.golden.participantIDs[len(fixture.golden.participantIDs)-1],
		revision: 7, position: positionFrom + 1, deadline: startedAt.Add(180 * time.Second), digest: goldenAuthorityDigest(91),
	}
	proof.recordedAt, proof.createdAt = proof.deadline, proof.deadline
	var previousID uuid.NullUUID
	var attemptNumber int
	require.NoError(t, pool.QueryRow(ctx, `SELECT
		(SELECT id FROM golden_attempts WHERE tournament_id = $1 ORDER BY attempt_number DESC LIMIT 1),
		COALESCE((SELECT MAX(attempt_number) FROM golden_attempts WHERE tournament_id = $1), 0) + 1`, proof.tournamentID,
	).Scan(&previousID, &attemptNumber))
	var err error
	proof.attemptID, err = goldenseed.CreateAttempt(ctx, pool, goldenseed.AttemptInput{
		TournamentID: proof.tournamentID, RosterID: proof.rosterID, AttemptNumber: attemptNumber,
		PreviousAttemptID: previousID, CreatedAt: startedAt.Add(-time.Second),
	})
	require.NoError(t, err)
	members := make([]uuid.UUID, len(fixture.golden.participantIDs))
	for index, participantID := range fixture.golden.participantIDs {
		var readyAt, noShowAt any
		if noShow && index == 0 {
			noShowAt = startedAt
		} else {
			readyAt = startedAt
		}
		members[index] = uuid.New()
		_, err = pool.Exec(ctx, `INSERT INTO golden_memberships (
			id, attempt_id, tournament_id, roster_id, participant_id, selection_kind,
			selected_at, ready_at, no_show_at, created_at
		) VALUES ($1, $2, $3, $4, $5, 'direct', $6, $7, $8, $6)`, members[index], proof.attemptID,
			proof.tournamentID, proof.rosterID, participantID, startedAt.Add(-time.Second), readyAt, noShowAt)
		require.NoError(t, err)
		if readyAt != nil {
			_, err = pool.Exec(ctx, `UPDATE golden_memberships SET participation_established_at = $2 WHERE id = $1`, members[index], startedAt)
			require.NoError(t, err)
		}
	}
	proof.membershipID = members[len(members)-1]
	_, err = pool.Exec(ctx, `UPDATE golden_attempts SET state = 'ready', disclosed_at = $2, ready_at = $2 WHERE id = $1`, proof.attemptID, startedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE golden_attempts SET state = 'active', started_at = $2 WHERE id = $1`, proof.attemptID, startedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO golden_attempt_stage_groups (attempt_id, tournament_id, roster_id, group_revision_id, bound_at) VALUES ($1, $2, $3, $4, $5)`, proof.attemptID, proof.tournamentID, proof.rosterID, proof.groupID, startedAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO golden_runtime_assignments (
		attempt_id, tournament_id, roster_id, group_revision_id, wave_id, assignment_id, snapshot_id,
		task_id, task_version, title, category, difficulty, source_digest, started_at, deadline, created_at,
		plan_id, edge_position, ready_window_id, ready_window_opened_at, ready_window_deadline
	) SELECT $1, edge.tournament_id, edge.roster_id, edge.group_revision_id, edge.edge_id,
		edge.reservation_id, edge.snapshot_id, edge.task_id, edge.task_version,
		snapshot.title, snapshot.category, snapshot.difficulty, edge.content_digest, $3, $4, $3,
		edge.plan_id, edge.position, $5, $3::timestamptz - interval '30 seconds', $3
	FROM golden_exact_plan_snapshot_edges AS edge
	JOIN task_snapshots AS snapshot ON snapshot.id = edge.snapshot_id
	WHERE edge.plan_id = $2 AND edge.position = 1 AND edge.group_revision_id = $6`, proof.attemptID, planID, startedAt, proof.deadline, uuid.New(), proof.groupID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO golden_runtime_heads (tournament_id, roster_id, revision, source_projection_revision_id, source_projection_revision, updated_at) VALUES ($1, $2, $3, $4, $5, $6)`, proof.tournamentID, proof.rosterID, proof.revision, fixture.sourceProjectionID, fixture.sourceProjectionRevision, startedAt)
	require.NoError(t, err)
	if accepted {
		submissionID, submitErr := goldenseed.CreateSubmission(ctx, pool, goldenseed.SubmissionInput{
			AttemptID: proof.attemptID, TournamentID: proof.tournamentID, RosterID: proof.rosterID,
			MembershipID: members[0], ParticipantID: fixture.golden.participantIDs[0], ServerSequence: 1,
			Position: int(positionFrom), Status: "accepted", CreatedAt: startedAt.Add(time.Second),
		})
		require.NoError(t, submitErr)
		_, err = goldenseed.CreatePositionCommit(ctx, pool, goldenseed.PositionCommitInput{
			AttemptID: proof.attemptID, TournamentID: proof.tournamentID, RosterID: proof.rosterID,
			MembershipID: members[0], ParticipantID: fixture.golden.participantIDs[0], SubmissionID: submissionID,
			Position: int(positionFrom), CreatedAt: startedAt.Add(time.Second),
		})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO golden_attempt_submission_revisions (
			revision_id, attempt_id, tournament_id, roster_id, membership_id, participant_id, revision_number,
			provisional_submission_id, payload_digest, committed_at, created_at
		) SELECT $1, attempt_id, tournament_id, roster_id, membership_id, participant_id, 1, id, payload_digest, received_at, created_at
		FROM golden_provisional_submissions WHERE id = $2`, uuid.New(), submissionID)
		require.NoError(t, err)
	}
	return proof
}

func insertGoldenTerminalProof(ctx context.Context, tx pgx.Tx, p goldenTerminalProof) error {
	_, err := tx.Exec(ctx, `INSERT INTO golden_terminal_position_evidence (
		id, tournament_id, roster_id, group_revision_id, attempt_id, membership_id, participant_id,
		runtime_revision, deadline, position, payload_digest, recorded_at, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		p.id, p.tournamentID, p.rosterID, p.groupID, p.attemptID, p.membershipID, p.participantID,
		p.revision, p.deadline, p.position, p.digest, p.recordedAt, p.createdAt)
	return err
}

func insertGoldenTerminalCommit(ctx context.Context, tx pgx.Tx, p goldenTerminalProof, submissionID, evidenceID any) error {
	_, err := tx.Exec(ctx, `INSERT INTO golden_position_commits (
		id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
		provisional_submission_id, terminal_evidence_id, position, committed_at, created_at
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, uuid.New(), p.attemptID, p.tournamentID,
		p.rosterID, p.membershipID, p.participantID, submissionID, evidenceID, p.position, p.recordedAt, p.createdAt)
	return err
}

func sealGoldenTerminalLedger(ctx context.Context, t *testing.T, tx pgx.Tx, p goldenTerminalProof) uuid.UUID {
	t.Helper()
	ledgerID := uuid.New()
	_, err := tx.Exec(ctx, `INSERT INTO golden_position_ledger_revisions (
		revision_id, tournament_id, roster_id, group_revision_id, revision_number, payload_digest, finalized_at, created_at
	) VALUES ($1, $2, $3, $4, 1, $5, $6, $6)`, ledgerID, p.tournamentID, p.rosterID, p.groupID, p.digest, p.recordedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO golden_position_ledger_attempts (
		ledger_revision_id, attempt_id, submission_revision_id, tournament_id, roster_id, group_revision_id, attempt_number, order_count, created_at
	) SELECT $1, $2, submission.revision_id, $3, $4, $5, attempt.attempt_number, 2, $6
	FROM golden_attempt_submission_revisions AS submission
	JOIN golden_attempts AS attempt ON attempt.id = submission.attempt_id
	WHERE submission.attempt_id = $2 ORDER BY submission.revision_number DESC LIMIT 1`, ledgerID, p.attemptID, p.tournamentID, p.rosterID, p.groupID, p.recordedAt)
	require.NoError(t, err)
	for _, change := range []string{"digest", "position"} {
		_, err = tx.Exec(ctx, `SAVEPOINT invalid_terminal_binding`)
		require.NoError(t, err)
		digest, position := p.digest, p.position
		if change == "digest" {
			digest = goldenAuthorityDigest(92)
		} else {
			position++
		}
		_, err = tx.Exec(ctx, `INSERT INTO golden_position_ledger_commit_bindings (
			ledger_revision_id, attempt_id, position_commit_id, tournament_id, roster_id, participant_id, position, evidence_digest, created_at
		) SELECT $1, attempt_id, id, tournament_id, roster_id, participant_id, $3, $4, $5
		FROM golden_position_commits WHERE terminal_evidence_id = $2`, ledgerID, p.id, position, digest, p.recordedAt)
		requireGoldenTerminalConstraint(t, err, "golden_terminal_position_binding_guard")
		_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_terminal_binding`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `RELEASE SAVEPOINT invalid_terminal_binding`)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO golden_position_ledger_commit_bindings (
		ledger_revision_id, attempt_id, position_commit_id, tournament_id, roster_id, participant_id, position, evidence_digest, created_at
	) SELECT $1, committed.attempt_id, committed.id, committed.tournament_id, committed.roster_id, committed.participant_id,
		committed.position, COALESCE(evidence.payload_digest, submission.payload_digest), $3
	FROM golden_position_commits AS committed
	LEFT JOIN golden_terminal_position_evidence AS evidence ON evidence.id = committed.terminal_evidence_id
	LEFT JOIN golden_provisional_submissions AS submission ON submission.id = committed.provisional_submission_id
	WHERE committed.attempt_id = $2`, ledgerID, p.attemptID, p.recordedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO golden_position_ledger_revision_seals (ledger_revision_id, tournament_id, roster_id, payload_digest, sealed_at) VALUES ($1, $2, $3, $4, $5)`, ledgerID, p.tournamentID, p.rosterID, p.digest, p.recordedAt)
	require.NoError(t, err)
	return ledgerID
}

func requireGoldenTerminalConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var databaseError *pgconn.PgError
	require.ErrorAs(t, err, &databaseError)
	require.Equal(t, "23514", databaseError.Code)
	require.Equal(t, constraint, databaseError.ConstraintName)
}

func assertGoldenTerminalHeadFence(ctx context.Context, t *testing.T, pool *pgxpool.Pool, proof goldenTerminalProof) {
	t.Helper()
	blocker, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback(ctx) }()
	var blockerPID int32
	require.NoError(t, blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
	_, err = blocker.Exec(ctx, `UPDATE golden_runtime_heads SET revision = revision + 1 WHERE tournament_id = $1`, proof.tournamentID)
	require.NoError(t, err)

	workerCtx, stopWorker := context.WithTimeout(ctx, 5*time.Second)
	defer stopWorker()
	worker, err := pool.Begin(workerCtx)
	require.NoError(t, err)
	defer func() {
		cleanupCtx, stopCleanup := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stopCleanup()
		_ = worker.Rollback(cleanupCtx)
	}()
	var workerPID int32
	require.NoError(t, worker.QueryRow(workerCtx, `SELECT pg_backend_pid()`).Scan(&workerPID))
	result := make(chan error, 1)
	joined := false
	defer func() {
		cleanupCtx, stopCleanup := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = blocker.Rollback(cleanupCtx)
		stopCleanup()
		stopWorker()
		if !joined {
			select {
			case <-result:
			case <-time.After(2 * time.Second):
				t.Error("terminal capture worker did not stop")
			}
		}
	}()
	go func() { result <- insertGoldenTerminalProof(workerCtx, worker, proof) }()

	// Observe the server's lock wait, not a scheduler-dependent sleep. The
	// only blocker is the runtime head, before attempt/member locks are taken.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waitCtx, stopWait := context.WithTimeout(ctx, 3*time.Second)
	defer stopWait()
	for {
		var blocked bool
		require.NoError(t, pool.QueryRow(waitCtx, `SELECT EXISTS (
			SELECT 1 FROM pg_locks WHERE pid = $1 AND NOT granted
		) AND $2::integer = ANY(pg_blocking_pids($1))`, workerPID, blockerPID).Scan(&blocked))
		if blocked {
			break
		}
		select {
		case err = <-result:
			joined = true
			t.Fatalf("terminal capture finished without waiting for the runtime head: %v", err)
		case <-waitCtx.Done():
			t.Fatal("terminal capture did not wait on the runtime head")
		case <-ticker.C:
		}
	}
	require.NoError(t, blocker.Commit(ctx))
	select {
	case err = <-result:
		joined = true
		requireGoldenTerminalConstraint(t, err, "golden_terminal_position_evidence_guard")
		require.ErrorContains(t, err, "current runtime head")
	case <-workerCtx.Done():
		t.Fatal("terminal capture did not finish after head advancement")
	}
}

func readGoldenTerminalLegacyRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, attemptID uuid.UUID) string {
	t.Helper()
	var result string
	require.NoError(t, pool.QueryRow(ctx, `SELECT jsonb_build_object(
		'attempt', (SELECT to_jsonb(a) FROM golden_attempts AS a WHERE id = $1),
		'runtime', (SELECT to_jsonb(a) FROM golden_runtime_assignments AS a WHERE attempt_id = $1),
		'members', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM golden_memberships AS a WHERE attempt_id = $1),
		'submissions', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM golden_provisional_submissions AS a WHERE attempt_id = $1),
		'revisions', (SELECT jsonb_agg(to_jsonb(a) ORDER BY revision_id) FROM golden_attempt_submission_revisions AS a WHERE attempt_id = $1),
		'commits', (SELECT jsonb_agg(to_jsonb(a) - 'terminal_evidence_id' ORDER BY id) FROM golden_position_commits AS a WHERE attempt_id = $1)
	)::text`, attemptID).Scan(&result))
	return result
}
