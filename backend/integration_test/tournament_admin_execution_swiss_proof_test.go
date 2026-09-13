//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type tournamentAdminSwissProofFixture struct {
	tx                       *postgres.TxManager
	adapter                  *postgres.TournamentAdminExecutionPostgres
	start                    *gameusecase.StartUseCase
	tournamentID             uuid.UUID
	rosterID                 uuid.UUID
	roundID                  uuid.UUID
	waveID                   uuid.UUID
	windowID                 uuid.UUID
	projectionRevisionID     uuid.UUID
	preflightRevisionID      uuid.UUID
	normalPoolRevisionID     uuid.UUID
	normalPoolRevision       int64
	waveRevisionID           domain.WaveRevisionID
	waveRevision             int64
	participants             []uuid.UUID
	binding                  []swissusecase.LockedSeries
	executionAuthority       authoritydomain.Identity
	sourceProjectionRevision int64
}

type roundProofReservation struct {
	id          uuid.UUID
	snapshotID  uuid.UUID
	taskID      uuid.UUID
	taskVersion int32
}

func TestTournamentAdminExecutionSwissRoundProof(t *testing.T) {
	t.Run("commits canonical normalized proof through wave start", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(ctx, t) })

		fixture := createTournamentAdminSwissProofFixture(ctx, t)
		command := fixture.startCommand(ctx, t)
		var record *gameusecase.StartRecord
		var changed bool
		err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var startErr error
			record, changed, startErr = fixture.start.Start(txCtx, command)
			return startErr
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, record)

		fixture.assertPersistedProof(ctx, t, record.StartedAt)
	})

	t.Run("rejects stale wave authority before any proof write", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(ctx, t) })

		fixture := createTournamentAdminSwissProofFixture(ctx, t)
		command := fixture.startCommand(ctx, t)
		_, err := sharedPool.Exec(ctx, `
			UPDATE waves
			SET revision = revision + 1,
				updated_at = clock_timestamp()
			WHERE id = $1`, fixture.waveID)
		require.NoError(t, err)

		record, changed, err := fixture.start.Start(ctx, command)
		require.ErrorIs(t, err, gameusecase.ErrWaveStartAuthorityConflict)
		require.Nil(t, record)
		require.False(t, changed)
		fixture.assertProofRollback(ctx, t, fixture.waveRevision+1)
	})

	t.Run("rolls back proof root and children when proof series insert fails late", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(ctx, t) })

		fixture := createTournamentAdminSwissProofFixture(ctx, t)
		command := fixture.startCommand(ctx, t)
		forcedRollback := errors.New("force proof series write rollback")
		var startErr error
		err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
			_, err := fixture.tx.Conn(txCtx).Exec(txCtx, `
				CREATE FUNCTION integration_fail_swiss_round_lock_proof_series() RETURNS trigger
				LANGUAGE plpgsql
				AS $$
				BEGIN
					RAISE EXCEPTION 'forced swiss round proof series failure'
						USING ERRCODE = 'P0001';
				END;
				$$;

				CREATE TRIGGER integration_fail_swiss_round_lock_proof_series
				BEFORE INSERT ON swiss_round_lock_proof_series
				FOR EACH ROW
				EXECUTE FUNCTION integration_fail_swiss_round_lock_proof_series();`)
			if err != nil {
				return err
			}
			_, _, startErr = fixture.start.Start(txCtx, command)
			if startErr == nil {
				return errors.New("expected proof series insert failure")
			}
			return forcedRollback
		})
		require.Error(t, startErr)
		require.ErrorContains(t, startErr, "forced swiss round proof series failure")
		require.ErrorIs(t, err, forcedRollback)
		fixture.assertProofRollback(ctx, t, fixture.waveRevision)
	})
}

func truncateRoundProofTables(ctx context.Context, t *testing.T) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `TRUNCATE TABLE tournaments, tasks, players RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}

func createTournamentAdminSwissProofFixture(
	ctx context.Context,
	t *testing.T,
) tournamentAdminSwissProofFixture {
	t.Helper()

	prepareRoundProofContent(ctx, t)
	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 4)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	normalPoolRevisionID, normalPoolRevision := createRoundProofContentConfiguration(ctx, t, tournamentID, createdAt)
	return createTournamentAdminSwissProofFixtureForAggregate(
		ctx, t, tournamentID, rosterID, playerIDs, normalPoolRevisionID, normalPoolRevision, createdAt,
	)
}

func createTournamentAdminSwissProofFixtureForAggregate(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
	normalPoolRevisionID uuid.UUID,
	normalPoolRevision int64,
	createdAt time.Time,
) tournamentAdminSwissProofFixture {
	t.Helper()

	participants := createSwissMigrationParticipants(ctx, t, rosterID, playerIDs)
	projectionRevisionID, projectionRevision := createRoundProofProjection(ctx, t, tournamentID, rosterID, participants[0], createdAt)
	transitionTournamentToSwissForRoundProof(ctx, t, tournamentID, rosterID, createdAt)
	preflightRevisionID := createRoundProofRosterLock(
		ctx, t, tournamentID, rosterID, playerIDs, projectionRevisionID, projectionRevision, createdAt,
	)
	roundID := createRoundProofSwissRound(ctx, t, tournamentID, rosterID, createdAt)

	tx := postgres.NewTxManager(sharedPool)
	waveID := uuid.New()
	waveRevisionID := domain.WaveRevisionID(uuid.New())
	seriesInputs := []postgres.WaveSeriesInput{
		{
			ID: uuid.New(), FirstParticipantID: participants[0], SecondParticipantID: participants[1],
			Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
		},
		{
			ID: uuid.New(), FirstParticipantID: participants[2], SecondParticipantID: participants[3],
			Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
		},
	}
	waveRepository := postgres.NewWavePostgres(tx)
	wave, err := waveRepository.Create(ctx, postgres.WaveCreateInput{
		ID: waveID, TournamentID: tournamentID, RosterID: rosterID, RevisionID: waveRevisionID,
		ParticipantIDs: participants, Series: seriesInputs, CommandID: uuid.New(),
		SourceProjectionRevisionID: projectionRevisionID, SourceProjectionRevision: projectionRevision,
		CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, wave.Revision)

	binding := make([]swissusecase.LockedSeries, len(seriesInputs))
	for index, series := range seriesInputs {
		pairingID := createSwissMigrationPairing(ctx, t, roundID, rosterID, index+1, uuid.Nil)
		addSwissMigrationPairingMember(ctx, t, pairingID, roundID, rosterID, 1, series.FirstParticipantID)
		addSwissMigrationPairingMember(ctx, t, pairingID, roundID, rosterID, 2, series.SecondParticipantID)
		binding[index] = createRoundProofSeriesBinding(
			ctx, t, tournamentID, rosterID, normalPoolRevisionID, series, pairingID, createdAt,
		)
	}
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO swiss_wave_links (
			wave_id, tournament_id, roster_id, round_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5)`, waveID, tournamentID, rosterID, roundID, createdAt)
	require.NoError(t, err)

	openedAt := time.Now().UTC().Truncate(time.Microsecond)
	windowID := uuid.New()
	wave, changed, err := waveRepository.OpenReadyWindow(ctx, tournamentID, waveID, wave.Revision, postgres.ReadyWindowInput{
		ID: windowID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
		OpenedAt: openedAt, Deadline: openedAt.Add(domain.ReadyWindowDuration),
	})
	require.NoError(t, err)
	require.True(t, changed)
	for _, participantID := range participants {
		wave, changed, err = waveRepository.MarkReady(
			ctx, tournamentID, waveID, windowID, participantID,
			wave.Revision, wave.ReadinessRevisions[participantID], openedAt.Add(time.Millisecond),
		)
		require.NoError(t, err)
		require.True(t, changed)
	}
	require.Equal(t, domain.WaveStateReady, wave.Wave.State)

	executionAuthority := authoritydomain.Identity{
		TournamentID: tournamentID, HolderID: uuid.New(), LeaseID: uuid.New(), Epoch: 1,
		ProcessKind: authoritydomain.ProcessAuthority,
	}
	createRoundProofExecutionLease(ctx, t, executionAuthority, createdAt)
	adapter := postgres.NewTournamentAdminExecutionPostgres(tx)
	return tournamentAdminSwissProofFixture{
		tx: tx, adapter: adapter, start: gameusecase.NewStartUseCase(adapter, nil),
		tournamentID: tournamentID, rosterID: rosterID, roundID: roundID, waveID: waveID, windowID: windowID,
		projectionRevisionID: projectionRevisionID, preflightRevisionID: preflightRevisionID,
		normalPoolRevisionID: normalPoolRevisionID, normalPoolRevision: normalPoolRevision,
		waveRevisionID: waveRevisionID, waveRevision: wave.Revision, participants: participants, binding: binding,
		executionAuthority: executionAuthority, sourceProjectionRevision: projectionRevision,
	}
}

func prepareRoundProofContent(ctx context.Context, t *testing.T) {
	t.Helper()

	prepareTournamentCreateReceiptContent(ctx, t)
	for category, count := range map[string]int{"web": 11, "crypto": 6, "forensics": 6} {
		for range count {
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'round proof fixture', $2, 'easy', 60, $3, 'normal')`,
				"round_proof_normal_"+uuid.NewString()[:8], category,
				"round-proof-"+uuid.NewString()[:8],
			)
			require.NoError(t, err)
		}
	}
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
				AND attestation.revision = 1
		)`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
}

func transitionTournamentToSwissForRoundProof(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	at time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE rosters
		SET revision = 2, locked_at = $2, updated_at = $2
		WHERE id = $1`, rosterID, at)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'swiss', revision = 3, started_at = $2, updated_at = $2
		WHERE id = $1`, tournamentID, at)
	require.NoError(t, err)
}

func createRoundProofProjection(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	at time.Time,
) (uuid.UUID, int64) {
	t.Helper()

	participantIDs := roundProofProjectionParticipants(ctx, t, rosterID)
	require.Contains(t, participantIDs, participantID)
	goldenSource := goldenMigrationFixture{
		tournamentID:   tournamentID,
		rosterID:       rosterID,
		participantIDs: participantIDs,
		createdAt:      at.Add(-10 * time.Minute),
	}
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, goldenSource)
	repository := postgres.NewProjectionPostgres(postgres.NewTxManager(sharedPool))
	record, err := repository.Publish(ctx, postgres.ProjectionPublishInput{
		IDs:   postgres.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: postgres.ProjectionScope{TournamentID: tournamentID, RosterID: rosterID},
		Source: postgres.ProjectionSource{
			Kind:                   "golden_position",
			GoldenPositionCommitID: &goldenPositionCommitID,
			Reason:                 "publish round proof source projection",
		},
		Artifacts:          projectionRepositoryArtifacts(t, participantIDs, goldenPositionCommitID, "round-proof"),
		SupersessionReason: "replace round proof source projection",
		CutoffAt:           at,
		CreatedAt:          at,
		PublishedAt:        at.Add(time.Microsecond),
	})
	require.NoError(t, err)
	return record.Revision.ID, record.Revision.RevisionNumber
}

func roundProofProjectionParticipants(
	ctx context.Context,
	t testing.TB,
	rosterID uuid.UUID,
) []uuid.UUID {
	t.Helper()

	rows, err := sharedPool.Query(ctx, `
		SELECT id
		FROM participants
		WHERE roster_id = $1
		ORDER BY seed, id`, rosterID)
	require.NoError(t, err)
	defer rows.Close()

	participantIDs := make([]uuid.UUID, 0, 4)
	for rows.Next() {
		var participantID uuid.UUID
		require.NoError(t, rows.Scan(&participantID))
		participantIDs = append(participantIDs, participantID)
	}
	require.NoError(t, rows.Err())
	require.Len(t, participantIDs, 4)
	return participantIDs
}

func createRoundProofContentConfiguration(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
	at time.Time,
	publishedTaskIDs ...[]uuid.UUID,
) (uuid.UUID, int64) {
	t.Helper()
	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	var normalPoolRevision int64
	var err error
	if len(publishedTaskIDs) > 0 {
		require.Len(t, publishedTaskIDs, 1)
		publicationID, normalPoolID, normalPoolRevision, goldenPoolID = findTaskPoolPublicationForTaskIDs(
			ctx, t, publishedTaskIDs[0],
		)
	} else {
		err = sharedPool.QueryRow(ctx, `
			SELECT publication.id, normal_pool.id, normal_pool.revision, golden_pool.id
			FROM task_pool_publications AS publication
			INNER JOIN task_pool_revisions AS normal_pool
				ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
			INNER JOIN task_pool_revisions AS golden_pool
				ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
			ORDER BY publication.revision DESC
			LIMIT 1`).Scan(&publicationID, &normalPoolID, &normalPoolRevision, &goldenPoolID)
		require.NoError(t, err)
	}
	configurationID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, state, pool_publication_id,
			normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, 'draft', $3, $4, $5, $6)`,
		configurationID, tournamentID, publicationID, normalPoolID, goldenPoolID, at)
	require.NoError(t, err)
	bo1PoolID := uuid.New()
	bo3PoolID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (
			id, configuration_id, format, revision, created_at
		)
		VALUES ($1, $2, 'bo1', 1, $3), ($4, $2, 'bo3', 1, $3)`,
		bo1PoolID, configurationID, at, bo3PoolID)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
		VALUES
			($1, 'web', $3), ($1, 'crypto', $3), ($1, 'forensics', $3),
			($2, 'web', $3), ($2, 'crypto', $3), ($2, 'forensics', $3),
			($2, 'reverse', $3), ($2, 'pwn', $3)`,
		bo1PoolID, bo3PoolID, at)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_content_stage_defaults (
			configuration_id, stage, format, category_mode, category_pool_revision_id, task_pool_kind, created_at
		)
		VALUES
			($1, 'swiss', 'bo1', 'random', $2, 'normal', $4),
			($1, 'golden', 'bo1', 'random', $2, 'golden', $4),
			($1, 'semifinal', 'bo1', 'draft', $2, 'normal', $4),
			($1, 'final', 'bo3', 'draft', $3, 'normal', $4)`,
		configurationID, bo1PoolID, bo3PoolID, at)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, at)
	require.NoError(t, err)
	return normalPoolID, normalPoolRevision
}

func findTaskPoolPublicationForTaskIDs(
	ctx context.Context,
	t testing.TB,
	taskIDs []uuid.UUID,
) (uuid.UUID, uuid.UUID, int64, uuid.UUID) {
	t.Helper()
	require.NotEmpty(t, taskIDs)

	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	var normalPoolRevision int64
	err := sharedPool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, normal_pool.revision, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		WHERE (
			SELECT COUNT(DISTINCT membership.task_id)
			FROM task_pool_version_memberships AS membership
			WHERE membership.task_pool_revision_id = normal_pool.id
				AND membership.task_id = ANY($1::UUID[])
		) = cardinality($1::UUID[])
		AND EXISTS (
			SELECT 1
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task
				ON task.id = membership.task_id
			INNER JOIN task_versions AS version
				ON version.task_id = membership.task_id
				AND version.version = membership.task_version
			LEFT JOIN LATERAL (
				SELECT attestation.healthy
				FROM task_version_health_attestations AS attestation
				WHERE attestation.task_id = membership.task_id
					AND attestation.task_version = membership.task_version
				ORDER BY attestation.revision DESC
				LIMIT 1
			) AS health ON true
			WHERE membership.task_pool_revision_id = golden_pool.id
				AND task.kind = 'golden'
				AND task.enabled
				AND task.deleted_at IS NULL
				AND health.healthy
		)
		ORDER BY publication.revision ASC
		LIMIT 1`, taskIDs).Scan(
		&publicationID, &normalPoolID, &normalPoolRevision, &goldenPoolID,
	)
	require.NoError(t, err)
	return publicationID, normalPoolID, normalPoolRevision, goldenPoolID
}

func createRoundProofRosterLock(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
	projectionRevisionID uuid.UUID,
	projectionRevision int64,
	at time.Time,
) uuid.UUID {
	t.Helper()
	preflightRevisionID := uuid.New()
	preflightDigest := sha256.Sum256([]byte("round-proof-roster-preflight"))
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO tournament_roster_operations (
			command_id, tournament_id, roster_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_tournament_state,
			resulting_tournament_revision, resulting_tournament_state,
			source_roster_revision, resulting_roster_revision,
			request_digest, checked_in_player_ids, result_document, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'preflight',
			$5, $6,
			1, 'registration',
			1, 'registration',
			1, 1,
			$7, '{}'::uuid[], '{"preflight":true}'::jsonb, $8, $8
		)`,
		preflightRevisionID, tournamentID, rosterID, uuid.New(),
		projectionRevisionID, projectionRevision, preflightDigest[:], at)
	require.NoError(t, err)

	digest := sha256.Sum256([]byte("round-proof-roster-lock"))
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_roster_operations (
			command_id, tournament_id, roster_id, actor_id, action, preflight_revision_id,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_tournament_state,
			resulting_tournament_revision, resulting_tournament_state,
			source_roster_revision, resulting_roster_revision,
			request_digest, checked_in_player_ids, result_document, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 'lock', $5,
			$6, $7,
			1, 'registration',
			2, 'roster_locked',
			1, 2,
			$8, $9, '{"locked":true}'::jsonb, $10, $10
		)`,
		uuid.New(), tournamentID, rosterID, uuid.New(), preflightRevisionID,
		projectionRevisionID, projectionRevision, digest[:], playerIDs, at)
	require.NoError(t, err)
	return preflightRevisionID
}

func createRoundProofSwissRound(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	at time.Time,
) uuid.UUID {
	t.Helper()
	roundID := uuid.New()
	seed := bytes.Repeat([]byte{1}, sha256.Size)
	digest := bytes.Repeat([]byte{2}, sha256.Size)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_rounds (
			id, tournament_id, roster_id, round_number, source_roster_revision, source_history_revision,
			generation_kind, pairing_inputs, decision_evidence_id,
			decision_algorithm_version, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, generated_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 1, 2, 0,
			'automatic', '["round-proof"]'::jsonb, $4,
			'hmac-sha256-order-v1', $5, '["round-proof"]'::jsonb,
			$6, $1, $7, $7, $7
		)`, roundID, tournamentID, rosterID, uuid.New(), seed, digest, at)
	require.NoError(t, err)
	return roundID
}

func createRoundProofSeriesBinding(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	normalPoolRevisionID uuid.UUID,
	series postgres.WaveSeriesInput,
	pairingID uuid.UUID,
	at time.Time,
) swissusecase.LockedSeries {
	t.Helper()
	draft := createRoundProofDraft(
		ctx, t, tournamentID, rosterID, normalPoolRevisionID, series, at,
	)
	exactPlanID := createRoundProofAssignmentPlan(
		ctx, t, tournamentID, rosterID, normalPoolRevisionID, draft, at,
	)
	branchID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_branches (
			id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence, created_at
		)
		VALUES ($1, $2, $3, $4, 'round-proof', '["web"]'::jsonb, $5)`,
		branchID, exactPlanID, draft.draftID, draft.initialRevisionID, at)
	require.NoError(t, err)
	reservations := createRoundProofReservations(ctx, t, exactPlanID, branchID, normalPoolRevisionID, at)
	selectedReservation := reservations[0]
	for _, reservation := range reservations {
		_, err = sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed', revision = revision + 1, committed_at = $2
			WHERE id = $1`, reservation.id, at)
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, branchID, at)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, branchID, at)
	require.NoError(t, err)

	slotID := createMigrationGameSlot(ctx, t, series.ID, rosterID, 1, "web")
	var attemptID uuid.UUID
	err = sharedPool.QueryRow(ctx, `
		INSERT INTO game_attempts (
			slot_id, series_id, roster_id, attempt_number, state, created_at, updated_at
		)
		VALUES ($1, $2, $3, 1, 'planned', $4, $4)
		RETURNING id`, slotID, series.ID, rosterID, at).Scan(&attemptID)
	require.NoError(t, err)
	assignmentID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO assignments (
			id, attempt_id, series_id, roster_id,
			plan_id, branch_id, reservation_id, snapshot_id, task_id, task_version,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`,
		assignmentID, attemptID, series.ID, rosterID,
		exactPlanID, branchID, selectedReservation.id, selectedReservation.snapshotID,
		selectedReservation.taskID, selectedReservation.taskVersion, at)
	require.NoError(t, err)
	var planRevisionID uuid.UUID
	var assignmentRevision, reservationRevision int64
	err = sharedPool.QueryRow(ctx, `
		SELECT plan.revision_id, assignment.revision, reservation.revision
		FROM assignment_plans AS plan
		INNER JOIN assignments AS assignment ON assignment.plan_id = plan.id
		INNER JOIN task_version_reservations AS reservation ON reservation.id = assignment.reservation_id
		WHERE plan.id = $1 AND assignment.id = $2`, exactPlanID, assignmentID,
	).Scan(&planRevisionID, &assignmentRevision, &reservationRevision)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'ready',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, series.ID, at)
	require.NoError(t, err)
	return swissusecase.LockedSeries{
		SeriesID: series.ID, PairingID: pairingID,
		FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID,
		CategoryRevisionID: draft.categoryRevisionID, CategoryRevision: 1,
		AssignmentID: assignmentID, AssignmentRevision: assignmentRevision,
		AssignmentPlanID: exactPlanID, AssignmentPlanRevisionID: planRevisionID,
		ReservationID: selectedReservation.id, ReservationRevision: reservationRevision,
	}
}

func createRoundProofAssignmentPlan(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	normalPoolRevisionID uuid.UUID,
	draft draftMigrationFixture,
	at time.Time,
) uuid.UUID {
	t.Helper()

	var rosterRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision
		FROM rosters
		WHERE id = $1`, rosterID,
	).Scan(&rosterRevision))
	conservativePlanID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, revision_id,
			source_roster_revision, source_pool_revision_id,
			constraint_graph, proof_evidence, created_at
		)
		VALUES (
			$1, $2, $3, 'conservative', $4,
			$5, $6,
			'{"type":"capacity"}'::jsonb, '{"covers":"full_roster"}'::jsonb, $7
		)`,
		conservativePlanID, tournamentID, rosterID, uuid.New(), rosterRevision, normalPoolRevisionID, at)
	require.NoError(t, err)

	exactPlanID := uuid.New()
	seed := bytes.Repeat([]byte{5}, sha256.Size)
	digest := bytes.Repeat([]byte{6}, sha256.Size)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO assignment_plans (
			id, tournament_id, roster_id, kind, parent_plan_id, revision_id,
			source_roster_revision, source_pool_revision_id, source_draft_revision_id,
			reachable_branch_count, constraint_graph, proof_evidence,
			decision_evidence_id, decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest,
			decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, 'exact', $4, $5,
			$6, $7, $8,
			1, '{"nodes":3,"edges":3}'::jsonb, '{"eligible":true}'::jsonb,
			$9, 'hmac-sha256-order-v1', '["web"]'::jsonb,
			$10, '["web"]'::jsonb, $11,
			$1, $12, $12
		)`,
		exactPlanID, tournamentID, rosterID, conservativePlanID, uuid.New(),
		rosterRevision, normalPoolRevisionID, draft.initialRevisionID,
		uuid.New(), seed, digest, at)
	require.NoError(t, err)
	return exactPlanID
}

func createRoundProofReservations(
	ctx context.Context,
	t *testing.T,
	planID uuid.UUID,
	branchID uuid.UUID,
	normalPoolRevisionID uuid.UUID,
	at time.Time,
) []roundProofReservation {
	t.Helper()

	rows, err := sharedPool.Query(ctx, `
		SELECT membership.task_id, membership.task_version
		FROM task_pool_version_memberships AS membership
		JOIN task_versions AS version ON version.task_id = membership.task_id
			AND version.version = membership.task_version AND version.category = 'web'
		WHERE membership.task_pool_revision_id = $1
			AND NOT EXISTS (
				SELECT 1
				FROM task_version_reservations AS reservation
				WHERE reservation.task_id = membership.task_id
					AND reservation.task_version = membership.task_version
					AND reservation.state IN ('reserved', 'committed')
			)
		ORDER BY membership.task_id
		LIMIT 3`, normalPoolRevisionID)
	require.NoError(t, err)
	defer rows.Close()

	reservations := make([]roundProofReservation, 0, 3)
	for rows.Next() {
		reservation := roundProofReservation{id: uuid.New(), snapshotID: uuid.New()}
		require.NoError(t, rows.Scan(&reservation.taskID, &reservation.taskVersion))
		edgeID := uuid.New()
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO assignment_plan_edges (
				id, plan_id, branch_id, position, task_id, task_version, selection_evidence, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, '{"eligible":true}'::jsonb, $7)`,
			edgeID, planID, branchID, len(reservations)+1,
			reservation.taskID, reservation.taskVersion, at)
		require.NoError(t, err)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_version_reservations (
				id, edge_id, plan_id, branch_id, task_id, task_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reservation.id, edgeID, planID, branchID,
			reservation.taskID, reservation.taskVersion, at)
		require.NoError(t, err)
		digest := sha256.Sum256([]byte(reservation.taskID.String()))
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_snapshots (
				id, reservation_id, task_id, task_version, kind,
				title, description, category, difficulty, time_limit, flag, hints, content_digest, created_at
			)
			VALUES (
				$1, $2, $3, $4, 'normal',
				'round proof task', 'round proof task description', 'web', 'easy', 180,
				'FLAG{round-proof}', '[]'::jsonb, $5, $6
			)`,
			reservation.snapshotID, reservation.id, reservation.taskID,
			reservation.taskVersion, digest[:], at)
		require.NoError(t, err)
		reservations = append(reservations, reservation)
	}
	require.NoError(t, rows.Err())
	require.Len(t, reservations, 3)
	return reservations
}

func createRoundProofDraft(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	normalPoolRevisionID uuid.UUID,
	series postgres.WaveSeriesInput,
	at time.Time,
) draftMigrationFixture {
	t.Helper()
	categoryRevisionID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO category_revisions (
			id, series_id, roster_id, revision, source_pool_revision_id,
			mode, category_pool, selected_categories,
			selector_actor_id, selection_reason, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, 1, $4, 'admin',
			'["web", "crypto", "forensics"]'::jsonb, '["web"]'::jsonb,
			$5, 'round proof category', $6, $6
		)`,
		categoryRevisionID, series.ID, rosterID, normalPoolRevisionID, uuid.New(), at)
	require.NoError(t, err)
	draftID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO drafts (
			id, series_id, roster_id, category_revision_id,
			first_participant_id, second_participant_id, format, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'bo1', $7)`,
		draftID, series.ID, rosterID, categoryRevisionID,
		series.FirstParticipantID, series.SecondParticipantID, at)
	require.NoError(t, err)
	initialRevisionID := uuid.New()
	serviceEpoch := uuid.New()
	seed := bytes.Repeat([]byte{3}, sha256.Size)
	digest := bytes.Repeat([]byte{4}, sha256.Size)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			command_id, service_epoch, state, turn_number,
			current_actor_id, current_action, absolute_deadline,
			decision_evidence_id, decision_purpose,
			decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest,
			decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 1,
			$5, $6, 'active', 1,
			$7, 'ban', $8,
			$9, 'draft_order',
			'hmac-sha256-order-v1', '["first","second"]'::jsonb,
			$10, '["first","second"]'::jsonb, $11,
			$2, $12, $12
		)`,
		initialRevisionID, draftID, series.ID, rosterID,
		uuid.New(), serviceEpoch, series.FirstParticipantID, at.Add(15*time.Second),
		uuid.New(), seed, digest, at)
	require.NoError(t, err)
	return draftMigrationFixture{
		tournamentID: tournamentID, rosterID: rosterID, seriesID: series.ID, draftID: draftID,
		categoryRevisionID: categoryRevisionID, initialRevisionID: initialRevisionID,
		participantIDs:      []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID},
		initialServiceEpoch: serviceEpoch, createdAt: at,
	}
}

func createRoundProofExecutionLease(
	ctx context.Context,
	t *testing.T,
	identity authoritydomain.Identity,
	at time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 1, 'authority', 1, $5, $5, $6, clock_timestamp())`,
		identity.TournamentID, uuid.New(), identity.HolderID, identity.LeaseID,
		at, time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
}

func (fixture tournamentAdminSwissProofFixture) startCommand(
	ctx context.Context,
	t *testing.T,
) gameusecase.StartCommand {
	t.Helper()
	authority, err := fixture.adapter.LoadWaveStartAuthority(ctx, gameusecase.StartScope{
		TournamentID: fixture.tournamentID, WaveID: fixture.waveID, WindowID: fixture.windowID,
	})
	require.NoError(t, err)
	require.Nil(t, authority.Current)
	digest := sha256.Sum256([]byte("round-proof-wave-start"))
	return gameusecase.StartCommand{
		Scope: authority.Scope, CommandID: uuid.New(), ActorID: uuid.New(),
		ExecutionAuthority:         fixture.executionAuthority,
		ExpectedProjectionRevision: authority.Revisions.ProjectionRevision,
		ExpectedRevisions:          authority.Revisions, RequestDigest: digest,
	}
}

func (fixture tournamentAdminSwissProofFixture) expectedProof(
	t *testing.T,
) swissusecase.RoundLockProof {
	t.Helper()
	proof, err := swissusecase.NewRoundLockProof(swissusecase.RoundLockProofInput{
		TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, RoundID: fixture.roundID,
		Preset: domain.TournamentPresetV1, RoundNumber: 1,
		SourceProjectionRevisionID: fixture.projectionRevisionID,
		PreflightRevisionID:        fixture.preflightRevisionID,
		NormalPoolRevisionID:       fixture.normalPoolRevisionID,
		WaveID:                     fixture.waveID, WaveRevisionID: fixture.waveRevisionID,
		Revisions: swissusecase.RoundLockRevisions{
			Round: 1, SourceProjection: fixture.sourceProjectionRevision, Roster: 2,
			History: 0, NormalPool: fixture.normalPoolRevision, Wave: fixture.waveRevision,
		},
		RosterParticipantIDs: fixture.participants, Series: fixture.binding,
	})
	require.NoError(t, err)
	return proof
}

func (fixture tournamentAdminSwissProofFixture) assertPersistedProof(
	ctx context.Context,
	t *testing.T,
	startedAt time.Time,
) {
	t.Helper()
	expected := fixture.expectedProof(t)
	expectedHash, err := hex.DecodeString(expected.ProofHash)
	require.NoError(t, err)
	var (
		preset                  string
		roundNumber             int
		persistedRosterID       uuid.UUID
		persistedProjectionID   uuid.UUID
		persistedPreflightID    uuid.UUID
		persistedNormalPoolID   uuid.UUID
		persistedWaveID         uuid.UUID
		persistedWaveRevisionID uuid.UUID
		roundRevision           int64
		projectionRevision      int64
		rosterRevision          int64
		historyRevision         int64
		normalPoolRevision      int64
		waveRevision            int64
		persistedHash           []byte
		persistedLockedAt       time.Time
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT preset, round_number, roster_id,
			source_projection_revision_id, preflight_revision_id, normal_pool_revision_id,
			wave_id, wave_revision_id,
			round_revision, source_projection_revision, roster_revision, history_revision,
			normal_pool_revision, wave_revision, proof_hash, locked_at
		FROM swiss_round_lock_proofs
		WHERE round_id = $1`, fixture.roundID,
	).Scan(
		&preset, &roundNumber, &persistedRosterID, &persistedProjectionID, &persistedPreflightID, &persistedNormalPoolID,
		&persistedWaveID, &persistedWaveRevisionID, &roundRevision, &projectionRevision, &rosterRevision,
		&historyRevision, &normalPoolRevision, &waveRevision, &persistedHash, &persistedLockedAt,
	)
	require.NoError(t, err)
	require.Equal(t, string(domain.TournamentPresetV1), preset)
	require.Equal(t, expected.RoundNumber, roundNumber)
	require.Equal(t, expected.RosterID, persistedRosterID)
	require.Equal(t, expected.SourceProjectionRevisionID, persistedProjectionID)
	require.Equal(t, expected.PreflightRevisionID, persistedPreflightID)
	require.Equal(t, expected.NormalPoolRevisionID, persistedNormalPoolID)
	require.Equal(t, expected.WaveID, persistedWaveID)
	require.Equal(t, expected.WaveRevisionID.UUID(), persistedWaveRevisionID)
	require.EqualValues(t, expected.Revisions.Round, roundRevision)
	require.EqualValues(t, expected.Revisions.SourceProjection, projectionRevision)
	require.EqualValues(t, expected.Revisions.Roster, rosterRevision)
	require.EqualValues(t, expected.Revisions.History, historyRevision)
	require.EqualValues(t, expected.Revisions.NormalPool, normalPoolRevision)
	require.EqualValues(t, expected.Revisions.Wave, waveRevision)
	require.Equal(t, expectedHash, persistedHash)
	require.True(t, persistedLockedAt.Equal(startedAt))

	persistedMembers := make([]uuid.UUID, 0, len(expected.RosterParticipantIDs))
	rows, err := sharedPool.Query(ctx, `
		SELECT participant_id
		FROM swiss_round_lock_proof_members
		WHERE round_id = $1
		ORDER BY participant_id`, fixture.roundID)
	require.NoError(t, err)
	for rows.Next() {
		var participantID uuid.UUID
		require.NoError(t, rows.Scan(&participantID))
		persistedMembers = append(persistedMembers, participantID)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	expectedMembers := append([]uuid.UUID(nil), expected.RosterParticipantIDs...)
	sort.Slice(expectedMembers, func(left, right int) bool {
		return expectedMembers[left].String() < expectedMembers[right].String()
	})
	require.Equal(t, expectedMembers, persistedMembers)

	persistedSeries := make([]swissusecase.LockedSeries, 0, len(expected.Series))
	rows, err = sharedPool.Query(ctx, `
		SELECT pairing_id, series_id, first_participant_id, second_participant_id,
			category_revision_id, category_revision,
			assignment_id, assignment_revision,
			assignment_plan_id, assignment_plan_revision_id,
			reservation_id, reservation_revision
		FROM swiss_round_lock_proof_series
		WHERE round_id = $1
		ORDER BY series_id`, fixture.roundID)
	require.NoError(t, err)
	for rows.Next() {
		var binding swissusecase.LockedSeries
		require.NoError(t, rows.Scan(
			&binding.PairingID, &binding.SeriesID,
			&binding.FirstParticipantID, &binding.SecondParticipantID,
			&binding.CategoryRevisionID, &binding.CategoryRevision,
			&binding.AssignmentID, &binding.AssignmentRevision,
			&binding.AssignmentPlanID, &binding.AssignmentPlanRevisionID,
			&binding.ReservationID, &binding.ReservationRevision,
		))
		persistedSeries = append(persistedSeries, binding)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	expectedSeries := append([]swissusecase.LockedSeries(nil), expected.Series...)
	sort.Slice(expectedSeries, func(left, right int) bool {
		return expectedSeries[left].SeriesID.String() < expectedSeries[right].SeriesID.String()
	})
	require.Equal(t, expectedSeries, persistedSeries)
}

func (fixture tournamentAdminSwissProofFixture) assertProofRollback(
	ctx context.Context,
	t *testing.T,
	expectedWaveRevision int64,
) {
	t.Helper()
	var rootCount, memberCount, seriesCount int
	err := sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM swiss_round_lock_proofs WHERE round_id = $1),
			(SELECT COUNT(*) FROM swiss_round_lock_proof_members WHERE round_id = $1),
			(SELECT COUNT(*) FROM swiss_round_lock_proof_series WHERE round_id = $1)`, fixture.roundID,
	).Scan(&rootCount, &memberCount, &seriesCount)
	require.NoError(t, err)
	require.Zero(t, rootCount)
	require.Zero(t, memberCount)
	require.Zero(t, seriesCount)

	var roundLocked bool
	err = sharedPool.QueryRow(ctx, `
		SELECT locked_at IS NOT NULL OR lock_revision IS NOT NULL
		FROM swiss_rounds
		WHERE id = $1`, fixture.roundID).Scan(&roundLocked)
	require.NoError(t, err)
	require.False(t, roundLocked)

	var waveState string
	var waveRevision int64
	err = sharedPool.QueryRow(ctx, `
		SELECT state, revision
		FROM waves
		WHERE id = $1`, fixture.waveID).Scan(&waveState, &waveRevision)
	require.NoError(t, err)
	require.Equal(t, string(domain.WaveStateReady), waveState)
	require.Equal(t, expectedWaveRevision, waveRevision)

	var seriesCountReady, gameCountPlanned int
	err = sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM wave_series AS membership
			 INNER JOIN series ON series.id = membership.series_id
			 WHERE membership.wave_id = $1 AND series.state = 'ready'),
			(SELECT COUNT(*) FROM wave_series AS membership
			 INNER JOIN game_slots AS slot ON slot.series_id = membership.series_id
			 INNER JOIN game_attempts AS attempt ON attempt.slot_id = slot.id
			 WHERE membership.wave_id = $1 AND attempt.state = 'planned')`, fixture.waveID,
	).Scan(&seriesCountReady, &gameCountPlanned)
	require.NoError(t, err)
	require.Equal(t, len(fixture.binding), seriesCountReady)
	require.Equal(t, len(fixture.binding), gameCountPlanned)
}
