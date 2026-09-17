//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProjectionPostgresRejectsChampionFromUndesignatedBO3(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 4)
	_, _ = createRoundProofProjection(
		ctx,
		t,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[0],
		fixture.createdAt,
	)
	seriesID := createMigrationSeries(
		ctx,
		t,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[:2],
		string(domain.SeriesFormatBO3),
	)
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	attachWaveScoreGenesis(ctx, t, fixture, seriesID, startedAt)
	firstSlotID := createMigrationGameSlot(ctx, t, seriesID, fixture.rosterID, 1, "web")
	secondSlotID := createMigrationGameSlot(ctx, t, seriesID, fixture.rosterID, 2, "crypto")
	firstAttemptID := createActiveMigrationAttempt(ctx, t, firstSlotID, seriesID, fixture.rosterID, startedAt)
	secondAttemptID := createActiveMigrationAttempt(ctx, t, secondSlotID, seriesID, fixture.rosterID, startedAt)
	_ = initializeFinalProjectionSeries(ctx, t, fixture, seriesID, startedAt)

	resultRepository := resultauthority.NewResultPostgres(postgres.NewTxManager(sharedPool))
	winnerID := fixture.participantIDs[0]
	operatorID := uuid.New()
	firstDigest := sha256.Sum256([]byte("undesignated final first result"))
	_, changed, err := resultRepository.Settle(ctx, finalProjectionSettlementInput(
		fixture,
		seriesID,
		firstAttemptID,
		winnerID,
		operatorID,
		domain.SeriesScore{FirstParticipantWins: 1},
		domain.SeriesStateLocked,
		domain.SeriesStateActive,
		2,
		firstDigest,
		startedAt.Add(time.Second),
	))
	require.NoError(t, err)
	require.True(t, changed)

	var activeSeriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM series WHERE id = $1`, seriesID).
		Scan(&activeSeriesRevision))
	terminalDigest := sha256.Sum256([]byte("undesignated final terminal result"))
	terminal, changed, err := resultRepository.Settle(ctx, finalProjectionSettlementInput(
		fixture,
		seriesID,
		secondAttemptID,
		winnerID,
		operatorID,
		domain.SeriesScore{FirstParticipantWins: 2},
		domain.SeriesStateActive,
		domain.SeriesStateCompleted,
		activeSeriesRevision,
		terminalDigest,
		startedAt.Add(2*time.Second),
	))
	require.NoError(t, err)
	require.True(t, changed)

	var (
		tournamentRevision int64
		seriesRevision     int64
		attemptRevision    int64
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM tournaments WHERE id = $1`, fixture.tournamentID).
		Scan(&tournamentRevision))
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM series WHERE id = $1`, seriesID).
		Scan(&seriesRevision))
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM game_attempts WHERE id = $1`, secondAttemptID).
		Scan(&attemptRevision))

	publication := finalProjectionPublication(
		t,
		fixture,
		seriesID,
		secondAttemptID,
		winnerID,
		terminal,
		terminalDigest,
		tournamentRevision,
		seriesRevision,
		attemptRevision,
		terminal.Outbox.ProjectionRevision,
		startedAt.Add(3*time.Second),
	)
	_, err = projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool)).PublishFinal(ctx, publication)
	require.ErrorIs(t, err, projectionrepo.ErrProjectionNotFound)
	assertFinalProjectionNotPersisted(ctx, t, publication, tournamentRevision)
}

func attachWaveScoreGenesis(
	ctx context.Context,
	tb testing.TB,
	fixture goldenMigrationFixture,
	seriesID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()
	waveID, _ := createMigrationWave(
		ctx,
		tb,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[:2],
		createdAt,
	)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		waveID,
		fixture.tournamentID,
		fixture.rosterID,
		seriesID,
		createdAt,
	)
	require.NoError(tb, err)

	var scoreRevisionID uuid.UUID
	require.NoError(tb, sharedPool.QueryRow(ctx, `
		SELECT current_revision_id
		FROM series_score_heads
		WHERE series_id = $1`, seriesID).Scan(&scoreRevisionID))
	authorityID := uuid.NewSHA1(waveID, []byte("result-projection-wave-initialization"))
	payload := []byte(`{"schema":"result-projection-series-score-genesis-v1"}`)
	payloadDigest := sha256.Sum256(payload)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO result_projection_node_authorities (
			id, tournament_id, roster_id, source_kind, wave_id, created_at
		)
		VALUES ($1, $2, $3, 'wave_initialization', $4, $5)`,
		authorityID,
		fixture.tournamentID,
		fixture.rosterID,
		waveID,
		createdAt,
	)
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO result_projection_nodes (
			id, authority_id, tournament_id, roster_id, artifact_kind,
			entity_id, revision_number, payload, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, 'series_score', $5, 1, $6::JSON, $7, $8)`,
		scoreRevisionID,
		authorityID,
		fixture.tournamentID,
		fixture.rosterID,
		seriesID,
		payload,
		payloadDigest[:],
		createdAt,
	)
	require.NoError(tb, err)
}

func TestOutboxChampionSourceGuardRejectsUndesignatedBO3(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 4)
	sourceProjectionID, sourceProjectionRevision := createRoundProofProjection(
		ctx,
		t,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[0],
		fixture.createdAt,
	)
	seriesID := createMigrationSeries(
		ctx,
		t,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[:2],
		string(domain.SeriesFormatBO3),
	)
	targetProjectionID, targetProjectionRevision := createDraftChampionTargetProjection(
		ctx,
		t,
		fixture,
		sourceProjectionID,
		sourceProjectionRevision,
	)
	settledAt := time.Now().UTC().Truncate(time.Microsecond)
	slotID := createMigrationGameSlot(ctx, t, seriesID, fixture.rosterID, 1, "web")
	attemptID := createActiveMigrationAttempt(ctx, t, slotID, seriesID, fixture.rosterID, settledAt)
	winnerID := fixture.participantIDs[0]

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback(ctx)) }()
	require.NoError(t, func() error {
		_, execErr := tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED")
		return execErr
	}())

	resultEventID := uuid.New()
	_, err = tx.Exec(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = 1
		WHERE id = $1
			AND series_id = $2
			AND roster_id = $3`,
		attemptID,
		seriesID,
		fixture.rosterID,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO result_events (
			id, tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state, result_reason,
			winner_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, $6, 'completed', 'operator_forfeit', $7, $8, $8)`,
		resultEventID,
		fixture.tournamentID,
		fixture.rosterID,
		seriesID,
		attemptID,
		uuid.New(),
		winnerID,
		settledAt,
	)
	require.NoError(t, err)

	resultRevisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO official_result_revisions (
			id, tournament_id, roster_id, entity_kind, entity_id,
			series_id, result_event_id, revision_number, command_id,
			actor_kind, source_projection_revision_id, source_projection_revision,
			result_state, result_reason, winner_id, created_at
		)
		VALUES (
			$1, $2, $3, 'series', $4,
			$4, $5, 1, $6,
			'server', $7, $8,
			'completed', 'score_complete', $9, $10
		)`,
		resultRevisionID,
		fixture.tournamentID,
		fixture.rosterID,
		seriesID,
		resultEventID,
		uuid.New(),
		targetProjectionID,
		targetProjectionRevision,
		winnerID,
		settledAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO official_result_heads (
			entity_kind, entity_id, series_id, roster_id,
			current_revision_id, revision, updated_at
		)
		VALUES ('series', $1, $1, $2, $3, 1, $4)`,
		seriesID,
		fixture.rosterID,
		resultRevisionID,
		settledAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE series
		SET state = 'completed',
			winner_id = $2,
			current_result_revision_id = $3,
			first_participant_wins = 2,
			revision = revision + 1,
			updated_at = $4,
			finished_at = $4
		WHERE id = $1`,
		seriesID,
		winnerID,
		resultRevisionID,
		settledAt,
	)
	require.NoError(t, err)

	artifactDigest := sha256.Sum256([]byte("undesignated champion artifact"))
	championArtifactID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO projection_artifacts (
			id, tournament_id, roster_id, produced_by_revision_id,
			artifact_kind, artifact_key, payload, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, 'champion', 'undesignated-champion',
			jsonb_build_object('participant_id', $5::TEXT), $6, $7)`,
		championArtifactID,
		fixture.tournamentID,
		fixture.rosterID,
		targetProjectionID,
		winnerID,
		artifactDigest[:],
		settledAt,
	)
	require.NoError(t, err)

	eventID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (
			id, tournament_id, roster_id, projection_revision_id, projection_revision,
			sequence, projection_ordinal, terminal, idempotency_key,
			audience, topic, payload, created_at, available_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, 1, true, $6,
			'all', 'tournament.champion.published', '{"state":"published"}'::jsonb, $7, $7)`,
		eventID,
		fixture.tournamentID,
		fixture.rosterID,
		targetProjectionID,
		targetProjectionRevision,
		uuid.New(),
		settledAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_champion_sources (
			outbox_event_id, tournament_id, roster_id, final_series_id,
			final_result_revision_id, champion_artifact_id,
			projection_revision_id, projection_revision, projection_ordinal, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)`,
		eventID,
		fixture.tournamentID,
		fixture.rosterID,
		seriesID,
		resultRevisionID,
		championArtifactID,
		targetProjectionID,
		targetProjectionRevision,
		settledAt,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, "SET CONSTRAINTS outbox_champion_sources_guard IMMEDIATE")
	require.ErrorContains(t, err, "champion outbox source is not bound to the exact terminal final result")
}

func createDraftChampionTargetProjection(
	ctx context.Context,
	tb testing.TB,
	fixture goldenMigrationFixture,
	previousRevisionID uuid.UUID,
	previousRevision int64,
) (uuid.UUID, int64) {
	tb.Helper()

	var previousCutoffID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		SELECT cutoff_id
		FROM projection_revisions
		WHERE id = $1
			AND tournament_id = $2
			AND roster_id = $3
			AND revision_number = $4`,
		previousRevisionID,
		fixture.tournamentID,
		fixture.rosterID,
		previousRevision,
	).Scan(&previousCutoffID)
	require.NoError(tb, err)

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	targetCutoffID := uuid.New()
	targetProjectionID := uuid.New()
	targetProjectionRevision := previousRevision + 1
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, previous_cutoff_id,
			source_kind, reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'operator_rebuild',
			'undesignated champion outbox guard target', $6, $6)`,
		targetCutoffID,
		fixture.tournamentID,
		fixture.rosterID,
		targetProjectionRevision,
		previousCutoffID,
		createdAt,
	)
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number, previous_revision_id,
			cutoff_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		targetProjectionID,
		fixture.tournamentID,
		fixture.rosterID,
		targetProjectionRevision,
		previousRevisionID,
		targetCutoffID,
		createdAt,
	)
	require.NoError(tb, err)
	return targetProjectionID, targetProjectionRevision
}
