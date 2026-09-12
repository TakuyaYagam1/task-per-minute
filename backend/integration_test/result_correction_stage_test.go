//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestTournamentAdminCorrectionCommitPersistsStageMutation(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	require.True(t, authority.Stage.Swiss.Complete)
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Equal(t, correctionusecase.StagePlayoffToGolden, mutation.Stage.Transition)

	var beforeRevision, beforeProgressions int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM tournaments WHERE id = $1`, fixture.tournamentID).Scan(&beforeRevision))
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM tournament_stage_progressions WHERE tournament_id = $1`, fixture.tournamentID).Scan(&beforeProgressions))

	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	_, replayChanged, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.False(t, replayChanged)

	var afterRevision, afterProgressions int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM tournaments WHERE id = $1`, fixture.tournamentID).Scan(&afterRevision))
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM tournament_stage_progressions WHERE tournament_id = $1`, fixture.tournamentID).Scan(&afterProgressions))
	require.Equal(t, beforeRevision+1, afterRevision)
	require.Equal(t, beforeProgressions+1, afterProgressions)
}

func TestTournamentAdminCorrectionHydratesEmptyReadinessAfterNativeStartPlayoffs(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	loadAuthority := func() tournamentadmin.CorrectionWorkflowAuthority {
		t.Helper()
		var authority tournamentadmin.CorrectionWorkflowAuthority
		require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var loadErr error
			authority, loadErr = repository.LockCorrectionAuthority(
				txCtx, fixture.tournamentID, seriesID, gameID,
			)
			return loadErr
		}))
		return authority
	}

	firstAuthority := loadAuthority()
	require.Equal(t, correctionusecase.Readiness{}, firstAuthority.Core.Readiness)
	require.NotNil(t, firstAuthority.Core.GameResult.Outcome.WinnerID)
	first := correctionStageMutationWithOutcome(
		t, firstAuthority, seriesID, gameID,
		*firstAuthority.Core.GameResult.Outcome.WinnerID,
		domain.GameResultReasonOperatorForfeit,
	)
	require.Equal(t, correctionusecase.StageUnchanged, first.Stage.Transition)
	_, changed, err := repository.CommitCorrection(ctx, first)
	require.NoError(t, err)
	require.True(t, changed)

	secondAuthority := loadAuthority()
	require.Equal(t, correctionusecase.Readiness{}, secondAuthority.Core.Readiness)
	secondWinner := secondAuthority.Core.Series.FirstParticipantID
	if *secondAuthority.Core.GameResult.Outcome.WinnerID == secondWinner {
		secondWinner = secondAuthority.Core.Series.SecondParticipantID
	}
	second := correctionStageMutationWithOutcome(
		t, secondAuthority, seriesID, gameID, secondWinner,
		domain.GameResultReasonSurrender,
	)
	require.NotEqual(t, correctionusecase.StageUnchanged, second.Stage.Transition)
	_, changed, err = repository.CommitCorrection(ctx, second)
	require.NoError(t, err)
	require.True(t, changed)
}

func TestTournamentAdminCorrectionPersistsSecondDistinctCorrectionLineage(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	loadAuthority := func() tournamentadmin.CorrectionWorkflowAuthority {
		t.Helper()
		var authority tournamentadmin.CorrectionWorkflowAuthority
		require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var loadErr error
			authority, loadErr = repository.LockCorrectionAuthority(
				txCtx, fixture.tournamentID, seriesID, gameID,
			)
			return loadErr
		}))
		return authority
	}

	firstAuthority := loadAuthority()
	first := correctionStageMutation(t, firstAuthority, seriesID, gameID)
	_, changed, err := repository.CommitCorrection(ctx, first)
	require.NoError(t, err)
	require.True(t, changed)
	firstReceipt := loadCorrectionFinalSwissReceiptProbe(ctx, t, fixture)
	requireCorrectionReceiptBridgeRejected(ctx, t, firstReceipt, "wrong_action")
	requireCorrectionReceiptBridgeRejected(ctx, t, firstReceipt, "broken_supersession")

	secondAuthority := loadAuthority()
	require.Equal(t, first.Plan.SeriesResultRevision().Revision().ID(), secondAuthority.Core.SeriesResult.ID)
	require.NotNil(t, secondAuthority.Core.GameResult.Outcome.WinnerID)
	second := correctionStageMutationWithOutcome(
		t, secondAuthority, seriesID, gameID, secondAuthority.Core.Series.FirstParticipantID,
		domain.GameResultReasonSurrender,
	)
	require.Equal(t, correctionusecase.StageGoldenToPlayoff, second.Stage.Transition)
	_, changed, err = repository.CommitCorrection(ctx, second)
	require.NoError(t, err)
	require.True(t, changed)
	_, changed, err = repository.CommitCorrection(ctx, second)
	require.NoError(t, err)
	require.False(t, changed)
	requireCorrectionPlayoffEvidenceProbe(ctx, t, second.Command.CommandID, "valid")
	requireCorrectionPlayoffEvidenceProbe(ctx, t, second.Command.CommandID, "invalid_action")
	requireCorrectionPlayoffEvidenceProbe(ctx, t, second.Command.CommandID, "missing_stage")
	requireCorrectionPlayoffEvidenceProbe(ctx, t, second.Command.CommandID, "unsealed")
	requireCorrectionPlayoffEvidenceProbe(ctx, t, second.Command.CommandID, "started")

	var currentGame, currentScore, currentSeries uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT game_head.current_revision_id,
			series.current_score_revision_id,
			series_head.current_revision_id
		FROM series
		INNER JOIN official_result_heads AS game_head
			ON game_head.entity_kind = 'game_attempt' AND game_head.entity_id = $1
			AND game_head.series_id = series.id AND game_head.roster_id = series.roster_id
		INNER JOIN official_result_heads AS series_head
			ON series_head.entity_kind = 'series' AND series_head.entity_id = series.id
			AND series_head.series_id = series.id AND series_head.roster_id = series.roster_id
		WHERE series.id = $2 AND series.tournament_id = $3 AND series.roster_id = $4`,
		gameID, seriesID, fixture.tournamentID, fixture.rosterID,
	).Scan(&currentGame, &currentScore, &currentSeries))
	require.Equal(t, second.Plan.GameResultRevision().Revision().ID().UUID(), currentGame)
	require.Equal(t, second.Plan.ScoreRevision().Revision().ID().UUID(), currentScore)
	require.Equal(t, second.Plan.SeriesResultRevision().Revision().ID().UUID(), currentSeries)

	rows, err := sharedPool.Query(ctx, `
		SELECT source_id, node_id
		FROM correction_projection_bindings
		WHERE tournament_id = $1 AND roster_id = $2
			AND artifact_kind = 'series_result' AND entity_id = $3
		ORDER BY created_at, command_id`, fixture.tournamentID, fixture.rosterID, seriesID)
	require.NoError(t, err)
	defer rows.Close()
	type binding struct{ sourceID, nodeID uuid.UUID }
	bindings := make([]binding, 0, 2)
	for rows.Next() {
		var value binding
		require.NoError(t, rows.Scan(&value.sourceID, &value.nodeID))
		bindings = append(bindings, value)
	}
	require.NoError(t, rows.Err())
	require.Len(t, bindings, 2)
	require.Equal(t, first.Plan.SeriesResultRevision().Revision().ID().UUID(), bindings[0].sourceID)
	require.Equal(t, second.Plan.SeriesResultRevision().Revision().ID().UUID(), bindings[1].sourceID)
	require.NotEqual(t, bindings[0].sourceID, bindings[0].nodeID)
	require.NotEqual(t, bindings[1].sourceID, bindings[1].nodeID)
	require.NotEqual(t, bindings[0].nodeID, bindings[1].nodeID)
	var topFourRevision, topFourPreviousRevision, bracketRevision, bracketPreviousRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT top_four.revision_number, top_four_previous.revision_number,
			bracket.revision_number, bracket_previous.revision_number
		FROM tournament_stage_playoff_evidence AS evidence
		INNER JOIN result_projection_nodes AS top_four ON top_four.id = evidence.top4_node_id
		INNER JOIN result_projection_nodes AS top_four_previous ON top_four_previous.id = top_four.previous_node_id
		INNER JOIN result_projection_nodes AS bracket ON bracket.id = evidence.bracket_node_id
		INNER JOIN result_projection_nodes AS bracket_previous ON bracket_previous.id = bracket.previous_node_id
		WHERE evidence.command_id = $1 AND evidence.tournament_id = $2 AND evidence.roster_id = $3`,
		second.Command.CommandID, fixture.tournamentID, fixture.rosterID,
	).Scan(&topFourRevision, &topFourPreviousRevision, &bracketRevision, &bracketPreviousRevision))
	require.Equal(t, topFourPreviousRevision+1, topFourRevision)
	require.Equal(t, bracketPreviousRevision+1, bracketRevision)
	readFinalSwissReceipt(ctx, t, fixture)
	currentReceipt := loadCorrectionFinalSwissReceiptProbe(ctx, t, fixture)
	require.GreaterOrEqual(t, currentReceipt.receiptRevision, int64(3))
	var earliestReceiptID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT projection_revision_id
		FROM final_swiss_projection_receipts
		WHERE tournament_id = $1 AND roster_id = $2
		ORDER BY receipt_revision
		LIMIT 1`, fixture.tournamentID, fixture.rosterID).Scan(&earliestReceiptID))
	missingPredecessor := currentReceipt
	missingPredecessor.previousID = uuid.Nil
	requireFinalSwissReceiptInsertRejected(
		ctx, t, missingPredecessor, "previous canonical Final Swiss receipt is stale or incomplete",
	)
	foreignScope := currentReceipt
	foreignScope.tournamentID = uuid.New()
	requireFinalSwissReceiptInsertRejected(
		ctx, t, foreignScope, "Final Swiss receipt source projection is stale or incomplete",
	)
	stalePredecessor := currentReceipt
	stalePredecessor.previousID = earliestReceiptID
	requireFinalSwissReceiptInsertRejected(
		ctx, t, stalePredecessor, "previous canonical Final Swiss receipt is stale or incomplete",
	)
	wrongDigest := currentReceipt
	invalidDigest := sha256.Sum256([]byte("wrong Final Swiss standings digest"))
	wrongDigest.sourceDigest = invalidDigest[:]
	requireFinalSwissReceiptInsertRejected(
		ctx, t, wrongDigest, "Final Swiss receipt source projection is stale or incomplete",
	)

	type stageNodeAuthority struct {
		authorityID uuid.UUID
		nodeID      uuid.UUID
		previousID  uuid.UUID
		revision    int64
	}
	loadStageNodeAuthority := func(scope tournamentAdminSwissProofFixture, commandID uuid.UUID) stageNodeAuthority {
		t.Helper()
		var value stageNodeAuthority
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT authority.id, node.id, node.previous_node_id, node.revision_number
			FROM tournament_stage_playoff_evidence AS evidence
			INNER JOIN result_projection_node_authorities AS authority
				ON authority.stage_command_id = evidence.command_id
				AND authority.tournament_id = evidence.tournament_id
				AND authority.roster_id = evidence.roster_id
			INNER JOIN result_projection_nodes AS node ON node.id = evidence.top4_node_id
			WHERE evidence.command_id = $1 AND evidence.tournament_id = $2 AND evidence.roster_id = $3`,
			commandID, scope.tournamentID, scope.rosterID,
		).Scan(&value.authorityID, &value.nodeID, &value.previousID, &value.revision))
		return value
	}
	correctedStage := loadStageNodeAuthority(fixture, second.Command.CommandID)
	ordinaryStage := loadStageNodeAuthority(fixture, progressionCommand.CommandID)

	requireProjectionNodeInsertRejected(ctx, t, projectionNodeProbe{
		authorityID: correctedStage.authorityID, tournamentID: fixture.tournamentID,
		rosterID: fixture.rosterID, kind: "top_four", entityID: fixture.tournamentID,
		revision: correctedStage.revision + 1,
	}, "initial result projection node must start at revision one")
	requireProjectionNodeInsertRejected(ctx, t, projectionNodeProbe{
		authorityID: correctedStage.authorityID, tournamentID: fixture.tournamentID,
		rosterID: fixture.rosterID, kind: "bracket", entityID: fixture.tournamentID,
		revision: correctedStage.revision + 1, previousID: correctedStage.nodeID,
	}, "invalid result projection node lineage")
	requireProjectionNodeInsertRejected(ctx, t, projectionNodeProbe{
		authorityID: correctedStage.authorityID, tournamentID: uuid.New(),
		rosterID: fixture.rosterID, kind: "top_four", entityID: fixture.tournamentID,
		revision: correctedStage.revision + 1, previousID: correctedStage.nodeID,
	}, "result projection node authority is outside node scope")
	requireProjectionNodeInsertRejected(ctx, t, projectionNodeProbe{
		authorityID: correctedStage.authorityID, tournamentID: fixture.tournamentID,
		rosterID: fixture.rosterID, kind: "top_four", entityID: fixture.tournamentID,
		revision: correctedStage.revision + 1, previousID: correctedStage.previousID,
	}, "invalid result projection node lineage")
	requireProjectionNodeInsertRejected(ctx, t, projectionNodeProbe{
		authorityID: ordinaryStage.authorityID, tournamentID: fixture.tournamentID,
		rosterID: fixture.rosterID, kind: "top_four", entityID: fixture.tournamentID,
		revision: ordinaryStage.revision + 1, previousID: ordinaryStage.nodeID,
	}, "stage provenance is valid only for exact playoff publication genesis nodes")
}

type finalSwissReceiptProbe struct {
	projectionID    uuid.UUID
	tournamentID    uuid.UUID
	rosterID        uuid.UUID
	receiptRevision int64
	canonicalID     uuid.UUID
	previousID      uuid.UUID
	standingsID     uuid.UUID
	sourceDigest    []byte
	canonicalDigest []byte
	createdAt       time.Time
}

func loadCorrectionFinalSwissReceiptProbe(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) finalSwissReceiptProbe {
	t.Helper()
	var value finalSwissReceiptProbe
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT projection_revision_id, tournament_id, roster_id, receipt_revision,
			canonical_projection_id, previous_receipt_projection_revision_id,
			source_standings_artifact_id, source_standings_payload_digest,
			canonical_payload_digest, created_at
		FROM final_swiss_projection_receipts
		WHERE tournament_id = $1 AND roster_id = $2
		ORDER BY receipt_revision DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID).Scan(
		&value.projectionID, &value.tournamentID, &value.rosterID, &value.receiptRevision,
		&value.canonicalID, &value.previousID, &value.standingsID, &value.sourceDigest,
		&value.canonicalDigest, &value.createdAt,
	))
	return value
}

func requireFinalSwissReceiptInsertRejected(
	ctx context.Context,
	t *testing.T,
	probe finalSwissReceiptProbe,
	expected string,
) {
	t.Helper()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var previous any
	if probe.previousID != uuid.Nil {
		previous = probe.previousID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO final_swiss_projection_receipts (
			projection_revision_id, tournament_id, roster_id, receipt_revision,
			canonical_projection_id, previous_receipt_projection_revision_id,
			source_standings_artifact_id, source_standings_payload_digest,
			canonical_payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		probe.projectionID, probe.tournamentID, probe.rosterID, probe.receiptRevision,
		probe.canonicalID, previous, probe.standingsID, probe.sourceDigest,
		probe.canonicalDigest, probe.createdAt,
	)
	require.ErrorContains(t, err, expected)
}

func requireCorrectionReceiptBridgeRejected(
	ctx context.Context,
	t *testing.T,
	probe finalSwissReceiptProbe,
	mode string,
) {
	t.Helper()
	connection, err := sharedPool.Acquire(ctx)
	require.NoError(t, err)
	defer connection.Release()
	_, err = connection.Exec(ctx, "DISCARD PLANS")
	require.NoError(t, err)
	tx, err := connection.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "SET LOCAL search_path = pg_temp, public")
	require.NoError(t, err)
	var bridgeID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT previous_revision_id
		FROM public.projection_revisions
		WHERE id = $1`, probe.projectionID).Scan(&bridgeID))
	switch mode {
	case "wrong_action":
		_, err = tx.Exec(ctx, `
			CREATE TEMP TABLE tournament_stage_progressions
			ON COMMIT DROP AS TABLE public.tournament_stage_progressions`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE pg_temp.tournament_stage_progressions
			SET action = 'correction_refresh_golden'
			WHERE resulting_projection_revision_id = $1`, bridgeID)
		require.NoError(t, err)
	case "broken_supersession":
		_, err = tx.Exec(ctx, `
			CREATE TEMP TABLE projection_revisions
			ON COMMIT DROP AS TABLE public.projection_revisions`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE pg_temp.projection_revisions
			SET superseded_by_revision_id = $1
			WHERE id = $2`, uuid.New(), bridgeID)
		require.NoError(t, err)
	default:
		require.FailNow(t, "unknown receipt bridge probe mode", mode)
	}
	var previous any
	if probe.previousID != uuid.Nil {
		previous = probe.previousID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO public.final_swiss_projection_receipts (
			projection_revision_id, tournament_id, roster_id, receipt_revision,
			canonical_projection_id, previous_receipt_projection_revision_id,
			source_standings_artifact_id, source_standings_payload_digest,
			canonical_payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		probe.projectionID, probe.tournamentID, probe.rosterID, probe.receiptRevision,
		probe.canonicalID, previous, probe.standingsID, probe.sourceDigest,
		probe.canonicalDigest, probe.createdAt,
	)
	require.ErrorContains(t, err, "previous canonical Final Swiss receipt is stale or incomplete")
}

type projectionNodeProbe struct {
	authorityID  uuid.UUID
	tournamentID uuid.UUID
	rosterID     uuid.UUID
	kind         string
	entityID     uuid.UUID
	revision     int64
	previousID   uuid.UUID
}

func requireProjectionNodeInsertRejected(
	ctx context.Context,
	t *testing.T,
	probe projectionNodeProbe,
	expected string,
) {
	t.Helper()
	payload := []byte(`{"probe":"invalid correction stage node"}`)
	digest := sha256.Sum256(payload)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var previous any
	if probe.previousID != uuid.Nil {
		previous = probe.previousID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO result_projection_nodes (
			id, authority_id, tournament_id, roster_id, artifact_kind,
			entity_id, revision_number, previous_node_id, payload,
			payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::JSON, $10, $11)`,
		uuid.New(), probe.authorityID, probe.tournamentID, probe.rosterID, probe.kind,
		probe.entityID, probe.revision, previous, payload, digest[:], time.Now().UTC(),
	)
	require.ErrorContains(t, err, expected)
}

func TestTournamentAdminCorrectionDiscoversRosterBeforeCanonicalScopeLocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)
	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id FROM game_attempts WHERE series_id = $1 ORDER BY attempt_number DESC LIMIT 1`,
		seriesID,
	).Scan(&gameID))

	normal, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = normal.Rollback(context.Background()) })
	_, err = normal.Exec(ctx, `SELECT id FROM tournaments WHERE id = $1 FOR UPDATE`, fixture.tournamentID)
	require.NoError(t, err)
	_, err = normal.Exec(ctx, `SELECT id FROM rosters WHERE id = $1 AND tournament_id = $2 FOR UPDATE`, fixture.rosterID, fixture.tournamentID)
	require.NoError(t, err)

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	pidCh := make(chan int32, 1)
	done := make(chan error, 1)
	go func() {
		done <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var pid int32
			if err := fixture.tx.Conn(txCtx).QueryRow(txCtx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			pidCh <- pid
			_, err := repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
			return err
		})
	}()
	pid := <-pidCh
	var waiting bool
	require.Eventually(t, func() bool {
		return sharedPool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1)) > 0`, pid).Scan(&waiting) == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)

	probe, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	_, err = probe.Exec(ctx, `SET LOCAL lock_timeout = '100ms'`)
	require.NoError(t, err)
	_, probeErr := probe.Exec(ctx, `SELECT id FROM series WHERE id = $1 FOR UPDATE`, seriesID)
	require.NoError(t, probe.Rollback(ctx))
	require.NoError(t, normal.Rollback(ctx))
	authorityErr := <-done

	require.NoError(t, probeErr, "a correction waiting on Tournament/Roster cannot already own the later Series lock")
	require.NoError(t, authorityErr)
}

func TestTournamentAdminCorrectionConcurrentDifferentSeriesHasOneWinner(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	mutations := make([]tournamentadmin.CorrectionMutation, 0, 2)
	for _, binding := range fixture.binding[:2] {
		seriesID := binding.SeriesID
		var gameID uuid.UUID
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT attempt.id
			FROM game_attempts AS attempt
			WHERE attempt.series_id = $1
			ORDER BY attempt.attempt_number DESC
			LIMIT 1`, seriesID).Scan(&gameID))
		var authority tournamentadmin.CorrectionWorkflowAuthority
		require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var loadErr error
			authority, loadErr = repository.LockCorrectionAuthority(
				txCtx, fixture.tournamentID, seriesID, gameID,
			)
			return loadErr
		}))
		mutations = append(mutations, correctionStageMutation(t, authority, seriesID, gameID))
	}
	var beforeRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision FROM tournaments WHERE id = $1`, fixture.tournamentID).Scan(&beforeRevision))

	type commitResult struct {
		changed    bool
		transition correctionusecase.StageTransition
		err        error
	}
	start := make(chan struct{})
	results := make(chan commitResult, len(mutations))
	for _, mutation := range mutations {
		mutation := mutation
		go func() {
			<-start
			_, changed, commitErr := repository.CommitCorrection(ctx, mutation)
			results <- commitResult{changed: changed, transition: mutation.Stage.Transition, err: commitErr}
		}()
	}
	close(start)
	winners := 0
	conflicts := 0
	winningTransition := correctionusecase.StageUnchanged
	for range mutations {
		result := <-results
		if result.err == nil {
			require.True(t, result.changed)
			winners++
			winningTransition = result.transition
			continue
		}
		var detail *pgconn.PgError
		if errors.As(result.err, &detail) {
			require.NotEqual(t, "40P01", detail.Code, "different-Series corrections cannot deadlock")
		}
		require.ErrorIs(t, result.err, domain.ErrConflict)
		require.False(t, result.changed)
		conflicts++
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, conflicts)
	var afterRevision, correctionCommits int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision FROM tournaments WHERE id = $1`, fixture.tournamentID).Scan(&afterRevision))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM result_correction_commits WHERE tournament_id = $1`, fixture.tournamentID).Scan(&correctionCommits))
	expectedRevision := beforeRevision
	if winningTransition != correctionusecase.StageUnchanged {
		expectedRevision++
	}
	require.Equal(t, expectedRevision, afterRevision)
	require.EqualValues(t, 1, correctionCommits)
}

func TestTournamentAdminCorrectionConcurrentIdenticalCommandReplaysStoredEvidence(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)
	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id FROM game_attempts WHERE series_id = $1 ORDER BY attempt_number DESC LIMIT 1`,
		seriesID,
	).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(
			txCtx, fixture.tournamentID, seriesID, gameID,
		)
		return loadErr
	}))
	mutation := correctionStageMutation(t, authority, seriesID, gameID)

	type outcome struct {
		evidence tournamentadmin.CorrectionEvidence
		changed  bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			evidence, changed, commitErr := repository.CommitCorrection(ctx, mutation)
			results <- outcome{evidence: evidence, changed: changed, err: commitErr}
		}()
	}
	close(start)
	committed, replayed := 0, 0
	var first tournamentadmin.CorrectionEvidence
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		if result.changed {
			committed++
			first = result.evidence
		} else {
			replayed++
		}
		require.Equal(t, mutation.Evidence, result.evidence)
	}
	require.Equal(t, 1, committed)
	require.Equal(t, 1, replayed)
	require.Equal(t, mutation.Evidence, first)
	var commands int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM result_correction_commits WHERE command_id = $1`,
		mutation.Command.CommandID,
	).Scan(&commands))
	require.Equal(t, 1, commands)
}

func TestTournamentAdminCorrectionRollsBackResultWritesOnLateStageFailure(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Equal(t, correctionusecase.StagePlayoffToGolden, mutation.Stage.Transition)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_lifecycle_commands (
			command_id, tournament_id, roster_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_tournament_state,
			resulting_tournament_revision, resulting_tournament_state,
			reason, pause_id, source_pause_command_id, execution_snapshot,
			preset, roster_size, tournament_created_at, tournament_updated_at,
			tournament_started_at, tournament_finished_at, executed_at, created_at
		)
		SELECT $1, tournament_id, roster_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_tournament_state,
			resulting_tournament_revision, resulting_tournament_state,
			reason, pause_id, source_pause_command_id, execution_snapshot,
			preset, roster_size, tournament_created_at, tournament_updated_at,
			tournament_started_at, tournament_finished_at, executed_at, created_at
		FROM tournament_lifecycle_commands
		WHERE tournament_id = $2 AND action NOT IN ('pause', 'resume')
		ORDER BY created_at, command_id
		LIMIT 1`, mutation.Command.CommandID, fixture.tournamentID)
	require.NoError(t, err)
	before := correctionStagePersistenceCounts(ctx, t, fixture.tournamentID)
	beforeProjectionID, beforeProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)

	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)
	require.Equal(t, before, correctionStagePersistenceCounts(ctx, t, fixture.tournamentID))
	afterProjectionID, afterProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	require.Equal(t, beforeProjectionID, afterProjectionID)
	require.Equal(t, beforeProjectionRevision, afterProjectionRevision)
}

func TestTournamentAdminCorrectionRollsBackSuccessfulNestedCommit(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Equal(t, correctionusecase.StagePlayoffToGolden, mutation.Stage.Transition)

	before := correctionStagePersistenceCounts(ctx, t, fixture.tournamentID)
	beforeProjectionID, beforeProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	rollback := errors.New("rollback successful correction")
	var evidence tournamentadmin.CorrectionEvidence
	var changed bool
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var commitErr error
		evidence, changed, commitErr = repository.CommitCorrection(txCtx, mutation)
		if commitErr != nil {
			return commitErr
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	require.True(t, changed)
	require.Equal(t, mutation.Evidence, evidence)
	require.Equal(t, before, correctionStagePersistenceCounts(ctx, t, fixture.tournamentID))
	afterProjectionID, afterProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	require.Equal(t, beforeProjectionID, afterProjectionID)
	require.Equal(t, beforeProjectionRevision, afterProjectionRevision)
}

func correctionStagePersistenceCounts(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
) [10]int64 {
	t.Helper()
	var counts [10]int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT revision FROM tournaments WHERE id = $1),
			(SELECT COUNT(*) FROM result_correction_commits WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM official_result_revisions WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM projection_revisions WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM tournament_stage_progressions WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM outbox_events WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM result_commits WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM series_score_revisions WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM correction_projection_bindings WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM final_swiss_projection_receipts WHERE tournament_id = $1)`, tournamentID).Scan(
		&counts[0], &counts[1], &counts[2], &counts[3], &counts[4],
		&counts[5], &counts[6], &counts[7], &counts[8], &counts[9],
	))
	return counts
}

func TestTournamentAdminCorrectionPersistsUnchangedStageWithoutProgression(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	require.NotNil(t, authority.Core.GameResult.Outcome.WinnerID)
	mutation := correctionStageMutationWithWinner(
		t, authority, seriesID, gameID, *authority.Core.GameResult.Outcome.WinnerID,
	)
	require.Equal(t, correctionusecase.StageUnchanged, mutation.Stage.Transition)
	before := correctionStagePersistenceCounts(ctx, t, fixture.tournamentID)
	_, beforeProjectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)

	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	after := correctionStagePersistenceCounts(ctx, t, fixture.tournamentID)
	_, afterProjectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	require.Equal(t, before[0], after[0])
	require.Equal(t, before[1]+1, after[1])
	require.Greater(t, after[2], before[2])
	require.Equal(t, before[3]+1, after[3])
	require.Equal(t, before[4], after[4])
	require.Equal(t, before[5]+1, after[5])
	require.Equal(t, beforeProjectionRevision+1, afterProjectionRevision)
}

func TestTournamentAdminCorrectionPersistsGoldenToPlayoff(t *testing.T) {
	ctx := context.Background()
	fixture, _ := preparePlayoffPublication(ctx, t)
	goldenParticipants := currentCorrectionStandingsParticipants(ctx, t, fixture, 2)
	groupID, previousRevisionID := seedCorrectionGoldenStageWithParticipants(ctx, t, fixture, goldenParticipants)
	seedCorrectionRetainedGoldenState(ctx, t, fixture, groupID, previousRevisionID)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	require.NotNil(t, authority.Core.GameResult.Outcome.WinnerID)
	mutation := correctionStageMutationWithWinner(
		t, authority, seriesID, gameID, *authority.Core.GameResult.Outcome.WinnerID,
	)
	require.Equal(t, correctionusecase.StageGoldenToPlayoff, mutation.Stage.Transition)
	require.Equal(t, correctionusecase.StageModePlayoff, mutation.Stage.Corrected.Mode)
	require.True(t, mutation.Stage.CreatePlayoff)
	require.Len(t, mutation.Stage.GroupSupersessions, 1)

	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	var state string
	var groupTombstones int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT tournament.state,
			(SELECT COUNT(*) FROM golden_correction_group_tombstones
			 WHERE command_id = $2 AND group_revision_id = $3)
		FROM tournaments AS tournament
		WHERE tournament.id = $1`, fixture.tournamentID, mutation.Command.CommandID, previousRevisionID).Scan(
		&state, &groupTombstones,
	))
	require.Equal(t, string(domain.TournamentStatePlayoffs), state)
	require.Equal(t, 1, groupTombstones)

	var semifinalCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tournament_stage_playoff_semifinals
		WHERE command_id = $1 AND tournament_id = $2 AND roster_id = $3`,
		mutation.Command.CommandID, fixture.tournamentID, fixture.rosterID,
	).Scan(&semifinalCount))
	require.Equal(t, 2, semifinalCount)

	var currentSeriesResultID uuid.UUID
	var retainedLedgerRows int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT current_result_revision_id,
			(SELECT COUNT(*) FROM swiss_point_ledger_entries
			 WHERE tournament_id = $1 AND roster_id = $2 AND source_series_id = $3)
		FROM series
		WHERE id = $3 AND tournament_id = $1 AND roster_id = $2`,
		fixture.tournamentID, fixture.rosterID, seriesID,
	).Scan(&currentSeriesResultID, &retainedLedgerRows))
	require.Equal(t, 4, retainedLedgerRows, "historical and corrected ledger pairs remain immutable")
	var currentLedgerRows []sqlc.LockTournamentProgressionSwissLedgerRow
	var allLedgerRows []sqlc.LockTournamentProgressionAllSwissLedgerRow
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		querier := fixture.tx.Querier(txCtx)
		var lockErr error
		currentLedgerRows, lockErr = querier.LockTournamentProgressionSwissLedger(
			txCtx,
			sqlc.LockTournamentProgressionSwissLedgerParams{
				TournamentID: fixture.tournamentID,
				RosterID:     fixture.rosterID,
			},
		)
		if lockErr != nil {
			return lockErr
		}
		allLedgerRows, lockErr = querier.LockTournamentProgressionAllSwissLedger(
			txCtx,
			sqlc.LockTournamentProgressionAllSwissLedgerParams{
				TournamentID: fixture.tournamentID,
				RosterID:     fixture.rosterID,
			},
		)
		return lockErr
	}))
	var retainedSeriesRows int
	for _, row := range allLedgerRows {
		if row.SourceSeriesID.Valid && row.SourceSeriesID.UUID == seriesID {
			retainedSeriesRows++
		}
	}
	require.Equal(t, retainedLedgerRows, retainedSeriesRows, "receipt snapshot retains historical and current rows")
	var correctedRows int
	for _, row := range currentLedgerRows {
		if row.SourceSeriesID.Valid && row.SourceSeriesID.UUID == seriesID {
			require.True(t, row.SeriesResultRevisionID.Valid)
			require.Equal(t, currentSeriesResultID, row.SeriesResultRevisionID.UUID)
			correctedRows++
		}
	}
	require.Equal(t, 2, correctedRows, "progression consumes only the exact current successor pair")

	// The corrected playoff stage is executable, not just a lifecycle marker:
	// the next ordinary semifinal settlement must consume its persisted Series,
	// score genesis, stage evidence, and projection-node binding.
	settlement := semifinalSettlementInput(ctx, t, fixture, mutation.Command.CommandID, 1)
	record, settled, err := postgres.NewResultPostgres(fixture.tx).Settle(ctx, settlement)
	require.NoError(t, err)
	require.True(t, settled)
	require.NotNil(t, record.SeriesRevision)
}

func requireCorrectionPlayoffEvidenceProbe(
	ctx context.Context,
	t *testing.T,
	commandID uuid.UUID,
	mode string,
) {
	t.Helper()
	connection, err := sharedPool.Acquire(ctx)
	require.NoError(t, err)
	defer connection.Release()
	_, err = connection.Exec(ctx, "DISCARD PLANS")
	require.NoError(t, err)
	tx, err := connection.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "SET LOCAL search_path = pg_temp, public")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE playoff_evidence_probe (
			LIKE public.tournament_stage_playoff_evidence
			INCLUDING DEFAULTS INCLUDING CONSTRAINTS
		) ON COMMIT DROP`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		CREATE CONSTRAINT TRIGGER playoff_evidence_probe_consistency
		AFTER INSERT ON playoff_evidence_probe
		DEFERRABLE INITIALLY IMMEDIATE
		FOR EACH ROW EXECUTE FUNCTION public.validate_tournament_stage_playoff_evidence()`)
	require.NoError(t, err)

	expected := ""
	switch mode {
	case "valid":
	case "invalid_action":
		_, err = tx.Exec(ctx, `
			CREATE TEMP TABLE tournament_stage_progressions
			ON COMMIT DROP AS TABLE public.tournament_stage_progressions`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE pg_temp.tournament_stage_progressions
			SET action = 'correction_refresh_golden'
			WHERE command_id = $1`, commandID)
		require.NoError(t, err)
		expected = "stage playoff evidence is not bound to exact published projection lineage"
	case "missing_stage":
		requireZeroCorrectionGoldenSettlements(ctx, t, tx, commandID)
		_, err = tx.Exec(ctx, `
			ALTER TABLE public.golden_correction_stage_tombstones DISABLE TRIGGER ALL`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			DELETE FROM public.golden_correction_stage_tombstones WHERE command_id = $1`, commandID)
		require.NoError(t, err)
		expected = "Golden playoff rollback lacks sealed unstarted group evidence"
	case "unsealed":
		requireZeroCorrectionGoldenSettlements(ctx, t, tx, commandID)
		_, err = tx.Exec(ctx, `
			ALTER TABLE public.golden_correction_tombstone_seals DISABLE TRIGGER USER`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			DELETE FROM public.golden_correction_tombstone_seals WHERE command_id = $1`, commandID)
		require.NoError(t, err)
		expected = "Golden playoff rollback lacks sealed unstarted group evidence"
	case "started":
		requireZeroCorrectionGoldenSettlements(ctx, t, tx, commandID)
		_, err = tx.Exec(ctx, `
			ALTER TABLE public.golden_attempts DISABLE TRIGGER USER`)
		require.NoError(t, err)
		startedAttemptID := uuid.New()
		startedAt := time.Now().UTC().Truncate(time.Microsecond)
		_, err = tx.Exec(ctx, `
			INSERT INTO public.golden_attempts (
				id, tournament_id, roster_id, attempt_number, previous_attempt_id,
				state, disclosed_at, ready_at, started_at, created_at
			)
			SELECT $2, progression.tournament_id, progression.roster_id,
				COALESCE(MAX(attempt.attempt_number), 0) + 1,
				(ARRAY_AGG(attempt.id ORDER BY attempt.attempt_number DESC))[1],
				'active', $3, $3, $3, $3
			FROM public.tournament_stage_progressions AS progression
			LEFT JOIN public.golden_attempts AS attempt
				ON attempt.tournament_id = progression.tournament_id
				AND attempt.roster_id = progression.roster_id
			WHERE progression.command_id = $1
			GROUP BY progression.tournament_id, progression.roster_id`,
			commandID, startedAttemptID, startedAt)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO public.golden_attempt_stage_groups (
				attempt_id, tournament_id, roster_id, group_revision_id, bound_at
			)
			SELECT $2, group_revision.tournament_id, group_revision.roster_id,
				group_revision.revision_id, $3
			FROM public.golden_group_revisions AS group_revision
			INNER JOIN public.tournament_stage_progressions AS progression
				ON progression.command_id = $1
				AND progression.tournament_id = group_revision.tournament_id
				AND progression.roster_id = group_revision.roster_id
			WHERE group_revision.source_projection_revision_id = progression.source_projection_revision_id
				AND group_revision.source_projection_revision = progression.source_projection_revision
			ORDER BY group_revision.position_from
			LIMIT 1`, commandID, startedAttemptID, startedAt)
		require.NoError(t, err)
		expected = "Golden playoff rollback lacks sealed unstarted group evidence"
	default:
		require.FailNow(t, "unknown playoff evidence probe mode", mode)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO playoff_evidence_probe
		SELECT *
		FROM public.tournament_stage_playoff_evidence
		WHERE command_id = $1`, commandID)
	if expected == "" {
		require.NoError(t, err)
		return
	}
	require.ErrorContains(t, err, expected)
}

func requireZeroCorrectionGoldenSettlements(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	commandID uuid.UUID,
) {
	t.Helper()
	var count int
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM public.golden_group_revisions AS group_revision
		INNER JOIN public.tournament_stage_progressions AS progression
			ON progression.command_id = $1
			AND progression.tournament_id = group_revision.tournament_id
			AND progression.roster_id = group_revision.roster_id
		INNER JOIN public.golden_attempt_stage_groups AS attempt_group
			ON attempt_group.group_revision_id = group_revision.revision_id
			AND attempt_group.tournament_id = group_revision.tournament_id
			AND attempt_group.roster_id = group_revision.roster_id
		INNER JOIN public.golden_attempts AS attempt
			ON attempt.id = attempt_group.attempt_id
			AND attempt.state = 'completed'
		INNER JOIN public.golden_position_commits AS position_commit
			ON position_commit.attempt_id = attempt.id
		WHERE group_revision.source_projection_revision_id = progression.source_projection_revision_id
			AND group_revision.source_projection_revision = progression.source_projection_revision`, commandID).Scan(&count))
	require.Zero(t, count)
}

func TestTournamentAdminCorrectionAuthorityHydratesGoldenStage(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)
	groupID, groupRevisionID := seedCorrectionGoldenStage(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	require.Equal(t, correctionusecase.StageModeGolden, authority.Stage.Layout.Mode)
	require.Len(t, authority.Stage.Layout.GoldenGroups, 1)
	require.Equal(t, groupID, authority.Stage.Layout.GoldenGroups[0].ID)
	require.Equal(t, domain.DerivedRevisionID(groupRevisionID), authority.Stage.Layout.GoldenGroups[0].RevisionID)
	require.Equal(t, fixture.participants[:2], []uuid.UUID{
		authority.Stage.Layout.GoldenGroups[0].Members[0].ParticipantID,
		authority.Stage.Layout.GoldenGroups[0].Members[1].ParticipantID,
	})
}

func TestTournamentAdminCorrectionAuthorityHydratesNativeGoldenStage(t *testing.T) {
	ctx := context.Background()
	fixture := prepareNativeGoldenFinalSwiss(ctx, t)
	finalSwiss := readFinalSwissReceipt(ctx, t, fixture)
	require.NotEmpty(t, finalSwiss.TieGroups())
	_, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	command := progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: projectionRevision, Action: progression.ActionStartGolden,
	}
	require.NoError(t, publishSwissGolden(ctx, fixture, command))
	var nativeGroups, crossBoundNativeGroups int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (
				WHERE tie.source_projection_revision_id = progression.source_projection_revision_id
					AND tie.source_projection_revision = progression.source_projection_revision),
			COUNT(*) FILTER (
				WHERE tie.source_projection_revision_id IS DISTINCT FROM progression.source_projection_revision_id
					OR tie.source_projection_revision IS DISTINCT FROM progression.source_projection_revision)
		FROM tournament_stage_progressions AS progression
		INNER JOIN tournament_stage_tie_groups AS tie
			ON tie.command_id = progression.command_id
			AND tie.tournament_id = progression.tournament_id
			AND tie.roster_id = progression.roster_id
		WHERE progression.command_id = $1 AND progression.action = 'start_golden'`, command.CommandID).Scan(
		&nativeGroups, &crossBoundNativeGroups,
	))
	require.Positive(t, nativeGroups)
	require.Zero(t, crossBoundNativeGroups)

	var groupID, groupRevisionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT group_id, revision_id
		FROM golden_group_revisions
		WHERE stage_progression_command_id = $1
		ORDER BY position_from
		LIMIT 1`, command.CommandID).Scan(&groupID, &groupRevisionID))
	seedCorrectionRetainedGoldenState(ctx, t, fixture, groupID, groupRevisionID)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))

	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(
			txCtx, fixture.tournamentID, seriesID, gameID,
		)
		return loadErr
	}))
	require.Equal(t, domain.TournamentStateGolden, authority.Core.TournamentState)
	require.Equal(t, correctionusecase.StageModeGolden, authority.Stage.Layout.Mode)
	require.Len(t, authority.Stage.Layout.GoldenGroups, len(finalSwiss.TieGroups()))
	require.Equal(t, groupRevisionID, authority.Stage.Layout.GoldenGroups[0].RevisionID.UUID())
	require.Equal(t, groupID, authority.Stage.Layout.GoldenGroups[0].ID)
	require.Len(t, authority.Stage.Layout.GoldenGroups[0].Attempts, 1)
	require.Len(t, authority.Stage.Layout.Paused, 1)

	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Equal(t, correctionusecase.StageGoldenGroupsChanged, mutation.Stage.Transition)
	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	successorProjectionID, successorProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	require.Equal(t, authority.ProjectionRevision+1, successorProjectionRevision)

	var successorLedgerEntries, successorReceipt, correctedGroups, historicalGroups int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM swiss_point_ledger_entries
			 WHERE tournament_id = $1 AND roster_id = $2
				AND source_series_id = $3 AND series_result_revision_id = $4),
			(SELECT COUNT(*) FROM final_swiss_projection_receipts
			 WHERE projection_revision_id = $5 AND tournament_id = $1 AND roster_id = $2),
			(SELECT COUNT(*) FROM golden_group_revisions
			 WHERE stage_progression_command_id = $6
				AND source_projection_revision_id = $5
				AND source_projection_revision = $7),
			(SELECT COUNT(*) FROM golden_group_revisions
			 WHERE revision_id = $8
				AND source_projection_revision_id = $9
				AND source_projection_revision = $10)`,
		fixture.tournamentID, fixture.rosterID, seriesID,
		mutation.Plan.SeriesResultRevision().Revision().ID().UUID(),
		successorProjectionID, mutation.Command.CommandID, successorProjectionRevision,
		groupRevisionID, authority.ProjectionRevisionID, authority.ProjectionRevision,
	).Scan(&successorLedgerEntries, &successorReceipt, &correctedGroups, &historicalGroups))
	require.Equal(t, 2, successorLedgerEntries)
	require.Equal(t, 1, successorReceipt)
	require.Len(t, mutation.Stage.Corrected.GoldenGroups, correctedGroups)
	require.Equal(t, 1, historicalGroups)

	settlementTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	for index, group := range mutation.Stage.Corrected.GoldenGroups {
		participants := make([]uuid.UUID, len(group.Members))
		for memberIndex, member := range group.Members {
			participants[memberIndex] = member.ParticipantID
		}
		seedCorrectionGoldenPositionLedger(
			ctx, t, settlementTx, fixture, group.RevisionID.UUID(), participants,
			mutation.Evidence.RequestedAt.Add(time.Duration(index+1)*time.Second),
		)
	}
	require.NoError(t, settlementTx.Commit(ctx))

	var lifecycle tournamentadmin.LifecycleAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		lifecycle, loadErr = postgres.NewTournamentAdminLifecyclePostgres(fixture.tx).
			LockLifecycleAuthority(txCtx, fixture.tournamentID)
		return loadErr
	}))
	golden, err := postgres.NewTournamentProgressionPostgres(fixture.tx).LoadGoldenEvidence(
		ctx,
		progression.Authority{
			Tournament: lifecycle.Tournament, ProjectionRevisionID: successorProjectionID,
			ProjectionRevision: successorProjectionRevision,
		},
	)
	require.NoError(t, err)
	require.NotEmpty(t, golden.CurrentTerminalSeries)
}

func TestTournamentAdminCorrectionCarriesNativeGoldenAuthorityAcrossUnchangedStage(t *testing.T) {
	ctx := context.Background()
	fixture := prepareNativeGoldenFinalSwiss(ctx, t)
	_, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	startGolden := progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: projectionRevision, Action: progression.ActionStartGolden,
	}
	require.NoError(t, publishSwissGolden(ctx, fixture, startGolden))

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	loadAuthority := func() tournamentadmin.CorrectionWorkflowAuthority {
		t.Helper()
		var authority tournamentadmin.CorrectionWorkflowAuthority
		require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var loadErr error
			authority, loadErr = repository.LockCorrectionAuthority(
				txCtx, fixture.tournamentID, seriesID, gameID,
			)
			return loadErr
		}))
		return authority
	}
	commitUnchanged := func(reason domain.GameResultReason) tournamentadmin.CorrectionMutation {
		t.Helper()
		authority := loadAuthority()
		require.NotNil(t, authority.Core.GameResult.Outcome.WinnerID)
		mutation := correctionStageMutationWithOutcome(
			t, authority, seriesID, gameID, *authority.Core.GameResult.Outcome.WinnerID, reason,
		)
		require.Equal(t, correctionusecase.StageUnchanged, mutation.Stage.Transition)
		_, changed, commitErr := repository.CommitCorrection(ctx, mutation)
		require.NoError(t, commitErr)
		require.True(t, changed)
		return mutation
	}

	first := commitUnchanged(domain.GameResultReasonOperatorForfeit)
	firstProjectionID, firstProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	var originalGroups, rewrittenGroups int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM golden_group_revisions
			 WHERE stage_progression_command_id = $1),
			(SELECT COUNT(*) FROM golden_group_revisions
			 WHERE stage_progression_command_id = $2)`,
		startGolden.CommandID, first.Command.CommandID,
	).Scan(&originalGroups, &rewrittenGroups))
	require.Positive(t, originalGroups)
	require.Zero(t, rewrittenGroups, "unchanged correction must retain immutable group provenance")

	var lifecycle tournamentadmin.LifecycleAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		lifecycle, loadErr = postgres.NewTournamentAdminLifecyclePostgres(fixture.tx).
			LockLifecycleAuthority(txCtx, fixture.tournamentID)
		return loadErr
	}))
	terminal, err := postgres.NewTournamentProgressionPostgres(fixture.tx).LoadLockedSwissTerminalEvidence(
		ctx,
		progression.Command{
			CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
			ActorID: uuid.New(), ExpectedProjectionRevision: firstProjectionRevision,
			Action: progression.ActionStartPlayoffs,
		},
		progression.Authority{
			Tournament: lifecycle.Tournament, ProjectionRevisionID: firstProjectionID,
			ProjectionRevision: firstProjectionRevision,
		},
	)
	require.NoError(t, err)
	require.NotEmpty(t, terminal.Rounds)

	second := commitUnchanged(domain.GameResultReasonSurrender)
	_, replayed, err := repository.CommitCorrection(ctx, second)
	require.NoError(t, err)
	require.False(t, replayed)
}

func prepareNativeGoldenFinalSwiss(
	ctx context.Context,
	t *testing.T,
) tournamentAdminSwissProofFixture {
	t.Helper()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	for range 12 {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, 'Native Golden correction task', 'web', 'easy', 60, 'native-golden', 'normal')`,
			"native_golden_"+uuid.NewString()[:8])
		require.NoError(t, err)
	}
	for _, category := range []string{"web", "crypto", "forensics", "reverse", "pwn"} {
		for range 6 {
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'Native Golden playoff task', $2, 'easy', 60, 'native-golden-playoff', 'normal')`,
				"native_golden_playoff_"+uuid.NewString()[:8], category)
			require.NoError(t, err)
		}
	}
	fixture := createTournamentAdminSwissProofFixture(ctx, t)
	return finishNativeGoldenFinalSwiss(ctx, t, fixture)
}

func finishNativeGoldenFinalSwiss(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) tournamentAdminSwissProofFixture {
	t.Helper()

	for round := 1; round <= 3; round++ {
		if round > 1 {
			fixture = nextSwissReceiptWave(ctx, t, fixture, round, false, false)
		}
		_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
		require.NoError(t, err)
		require.True(t, changed)
		for index, binding := range fixture.binding {
			points := [2]int{}
			require.NoError(t, sharedPool.QueryRow(ctx, `
				SELECT
					COALESCE(SUM(points) FILTER (WHERE participant_id = $2), 0),
					COALESCE(SUM(points) FILTER (WHERE participant_id = $3), 0)
				FROM swiss_point_ledger_entries
				WHERE tournament_id = $1`, fixture.tournamentID,
				binding.FirstParticipantID, binding.SecondParticipantID,
			).Scan(&points[0], &points[1]))
			winnerID := binding.FirstParticipantID
			if points[1] < points[0] || (points[0] == points[1] && (round+index)%2 == 0) {
				winnerID = binding.SecondParticipantID
			}
			settleCorrectionSwissReceiptSeries(ctx, t, fixture, index, winnerID)
		}
		closeSwissReceiptWave(ctx, t, fixture)
	}
	return fixture
}

func settleCorrectionSwissReceiptSeries(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	index int,
	winnerID uuid.UUID,
) {
	t.Helper()
	binding := fixture.binding[index]
	scope := gamedomain.SubmissionScope{
		Game:   gamedomain.Scope{TournamentID: fixture.tournamentID, SeriesID: binding.SeriesID},
		WaveID: fixture.waveID, AssignmentID: binding.AssignmentID,
	}
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id, attempt.slot_id
		FROM assignments AS assignment
		JOIN game_attempts AS attempt ON attempt.id = assignment.attempt_id
		WHERE assignment.id = $1`, binding.AssignmentID).Scan(&scope.Game.GameID, &scope.Game.SlotID))
	results := postgres.NewResultPostgres(fixture.tx)
	repository := postgres.NewParticipantSettlementRepository(fixture.tx, results)
	authority, err := repository.LoadConcurrentWinnerAuthority(ctx, scope)
	require.NoError(t, err)
	commandID := uuid.New()
	submittedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, changed, err := results.RecordSubmission(ctx, postgres.SubmissionInput{
		ID: uuid.NewSHA1(commandID, []byte("participant-command:submission-event")),
		Scope: postgres.ResultScope{
			TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
			SeriesID: binding.SeriesID, AttemptID: scope.Game.GameID,
		},
		AssignmentID: scope.AssignmentID, ParticipantID: winnerID, IdempotencyKey: commandID,
		Status: "accepted", PayloadDigest: authority.StartedGame.ContentDigest,
		IntentDigest: sha256.Sum256([]byte("native Golden correction fixture")),
		SubmittedAt:  submittedAt, ReceivedAt: submittedAt, CreatedAt: submittedAt,
		ExpectedAttemptRevision: authority.Revision, ExpectedAttemptState: domain.GameStateActive,
	})
	require.NoError(t, err)
	require.True(t, changed)
	settled, changed, err := gameusecase.SettlementNewUseCase(
		receiptSettlementRepository{ParticipantSettlementRepository: repository, t: t},
	).Settle(ctx, gameusecase.SettlementCommand{Scope: scope, CommandID: uuid.New()})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, winnerID, settled.SettlementGameResultRevision.WinnerID)
}

func publishSwissGolden(
	ctx context.Context,
	fixture tournamentAdminSwissProofFixture,
	command progression.Command,
) error {
	return fixture.tx.Do(ctx, func(txCtx context.Context) error {
		lifecycle := postgres.NewTournamentAdminLifecyclePostgres(fixture.tx)
		authority, err := lifecycle.LockLifecycleAuthority(txCtx, fixture.tournamentID)
		if err != nil {
			return err
		}
		if authority.ProjectionRevision != command.ExpectedProjectionRevision {
			return domain.ErrConflict
		}
		repository := postgres.NewTournamentProgressionPostgres(fixture.tx)
		workflow := progression.NewWorkflow(progression.ProgressionDependencies{
			Repository: repository, TerminalEvidence: repository, Transitioner: repository,
			Publisher: repository, ProgressionClock: playoffPublicationClock{},
		})
		receipt, err := workflow.Advance(txCtx, command, progression.Authority{
			Tournament: authority.Tournament, ProjectionRevisionID: authority.ProjectionRevisionID,
			ProjectionRevision: authority.ProjectionRevision,
		})
		if err != nil {
			return err
		}
		return lifecycle.SaveLifecycleCommand(txCtx, tournamentadmin.LifecycleCommandRecord{
			CommandScope: tournamentadmin.CommandScope{
				Operator:     tournamentadmin.OperatorIdentity{ActorID: command.ActorID},
				TournamentID: command.TournamentID, CommandID: command.CommandID,
			},
			Action:                     tournamentadmin.TournamentActionStartGolden,
			SourceProjectionRevisionID: authority.ProjectionRevisionID,
			SourceProjectionRevision:   authority.ProjectionRevision,
			SourceTournamentRevision:   authority.Tournament.Revision,
			SourceTournamentState:      authority.Tournament.State,
			Result:                     receipt.Result, ExecutedAt: receipt.Result.UpdatedAt,
		})
	})
}

func TestTournamentAdminCorrectionCommitPersistsGoldenSupersession(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)
	_, previousRevisionID := seedCorrectionGoldenStage(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Equal(t, correctionusecase.StageGoldenGroupsChanged, mutation.Stage.Transition)
	require.Len(t, mutation.Stage.GroupSupersessions, 1)

	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	var groupTombstones, seals int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM golden_correction_group_tombstones
			 WHERE command_id = $1 AND group_revision_id = $2),
			(SELECT COUNT(*) FROM golden_correction_tombstone_seals
			 WHERE command_id = $1)`, mutation.Command.CommandID, previousRevisionID).Scan(
		&groupTombstones, &seals,
	))
	require.Equal(t, 1, groupTombstones)
	require.Equal(t, 1, seals)
	var action string
	var resultingProjectionID uuid.UUID
	var resultingProjectionRevision int64
	var correctedGroups, crossBoundGroups int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT progression.action,
			progression.resulting_projection_revision_id,
			progression.resulting_projection_revision,
			COUNT(*) FILTER (
				WHERE group_revision.source_projection_revision_id = progression.resulting_projection_revision_id
					AND group_revision.source_projection_revision = progression.resulting_projection_revision),
			COUNT(*) FILTER (
				WHERE group_revision.source_projection_revision_id IS DISTINCT FROM progression.resulting_projection_revision_id
					OR group_revision.source_projection_revision IS DISTINCT FROM progression.resulting_projection_revision)
		FROM tournament_stage_progressions AS progression
		INNER JOIN golden_group_revisions AS group_revision
			ON group_revision.stage_progression_command_id = progression.command_id
			AND group_revision.tournament_id = progression.tournament_id
			AND group_revision.roster_id = progression.roster_id
		WHERE progression.command_id = $1
		GROUP BY progression.action, progression.resulting_projection_revision_id,
			progression.resulting_projection_revision`, mutation.Command.CommandID).Scan(
		&action, &resultingProjectionID, &resultingProjectionRevision,
		&correctedGroups, &crossBoundGroups,
	))
	require.Equal(t, "correction_refresh_golden", action)
	require.Equal(t, len(mutation.Stage.Corrected.GoldenGroups), correctedGroups)
	require.Zero(t, crossBoundGroups)
	requireCorrectionGoldenCrossBindingRejected(
		ctx, t, fixture, mutation.Command.CommandID,
		authority.ProjectionRevisionID, authority.ProjectionRevision,
	)
}

func TestTournamentAdminCorrectionCommitCancelsRetainedGoldenAttempt(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)
	groupID, groupRevisionID := seedCorrectionGoldenStage(ctx, t, fixture)
	stateRevisionIDs, ledgerRevisionID, attemptID := seedCorrectionRetainedGoldenState(
		ctx, t, fixture, groupID, groupRevisionID,
	)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return loadErr
	}))
	require.Len(t, authority.Stage.Layout.GoldenGroups, 1)
	require.Len(t, authority.Stage.Layout.GoldenGroups[0].Attempts, 1)
	require.Len(t, authority.Stage.Layout.Paused, 1)
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Len(t, mutation.Stage.GroupSupersessions, 1)
	require.Len(t, mutation.Stage.CancelledAttempts, 1)

	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	var physicalState string
	var stateTombstones, positionTombstones, attemptTombstones int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.state,
			(SELECT COUNT(*) FROM golden_correction_state_tombstones
			 WHERE command_id = $1 AND state_revision_id = ANY($2::UUID[])),
			(SELECT COUNT(*) FROM golden_correction_position_tombstones
			 WHERE command_id = $1 AND ledger_revision_id = $4),
			(SELECT COUNT(*) FROM golden_correction_attempt_tombstones
			 WHERE command_id = $1 AND attempt_id = $3)
		FROM golden_attempts AS attempt
		WHERE attempt.id = $3`, mutation.Command.CommandID, stateRevisionIDs, attemptID, ledgerRevisionID).Scan(
		&physicalState, &stateTombstones, &positionTombstones, &attemptTombstones,
	))
	require.Equal(t, "cancelled", physicalState)
	require.Equal(t, len(stateRevisionIDs), stateTombstones)
	require.Equal(t, 1, positionTombstones)
	require.Equal(t, 1, attemptTombstones)
}

func TestGoldenCorrectionSealRejectsLateStateRevision(t *testing.T) {
	ctx := context.Background()
	fixture, groupID, groupRevisionID := prepareSealedGoldenCorrection(ctx, t)

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	lateStateRevisionID := uuid.New()
	lateMembershipRevisionID := uuid.New()
	allocationID := uuid.New()
	allocationCommandID := uuid.New()
	lateAt := time.Now().UTC().Truncate(time.Microsecond)
	lateDigest := sha256.Sum256([]byte("late Golden state revision"))
	lateMembershipDigest := sha256.Sum256([]byte("late Golden membership revision"))
	var previousStateRevisionID uuid.UUID
	var previousStateRevision int64
	var previousStateDigest []byte
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT revision_id, revision_number, payload_digest
		FROM golden_state_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND group_revision_id = $3
		ORDER BY revision_number DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID, groupRevisionID).Scan(
		&previousStateRevisionID, &previousStateRevision, &previousStateDigest,
	))
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revisions (
			revision_id, tournament_id, roster_id, group_id, group_revision_id,
			revision_number, previous_revision_id, plan_id,
			membership_revision_id, membership_revision, membership_previous_revision_id,
			membership_digest, payload_digest, created_at
		)
		SELECT $1, tournament_id, roster_id, group_id, group_revision_id,
			revision_number + 1, revision_id, plan_id,
			$2, membership_revision + 1, membership_revision_id,
			$3, $4, $5
		FROM golden_state_revisions
		WHERE revision_id = $6`, lateStateRevisionID, lateMembershipRevisionID,
		lateMembershipDigest[:], lateDigest[:], lateAt, previousStateRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_transitions (
			state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
			transition_kind, command_id, previous_state_revision_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'allocation', $6, $7, $8, $8)`,
		lateStateRevisionID, fixture.tournamentID, fixture.rosterID, groupID,
		groupRevisionID, allocationCommandID, previousStateRevisionID, lateAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_members (
			state_revision_id, tournament_id, roster_id, participant_id,
			excluded, position, created_at
		)
		SELECT $1, tournament_id, roster_id, participant_id, excluded, position, $2
		FROM golden_state_members
		WHERE state_revision_id = $3`, lateStateRevisionID, lateAt, previousStateRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_allocations (
			allocation_id, command_id, state_revision_id, tournament_id, roster_id,
			group_id, group_revision_id, expected_state_revision_id,
			expected_state_revision, expected_state_payload_digest,
			allocated_at, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $11)`,
		allocationID, allocationCommandID, lateStateRevisionID, fixture.tournamentID,
		fixture.rosterID, groupID, groupRevisionID, previousStateRevisionID,
		previousStateRevision, previousStateDigest, lateAt, lateDigest[:])
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revision_seals (
			state_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`, lateStateRevisionID,
		fixture.tournamentID, fixture.rosterID, lateDigest[:], lateAt)
	require.NoError(t, err)
	require.Error(t, tx.Commit(ctx))
}

func TestGoldenCorrectionSealRejectsLatePositionRevision(t *testing.T) {
	ctx := context.Background()
	fixture, _, groupRevisionID := prepareSealedGoldenCorrection(ctx, t)

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	lateLedgerRevisionID := uuid.New()
	lateAt := time.Now().UTC().Truncate(time.Microsecond)
	lateDigest := sha256.Sum256([]byte("late Golden position revision"))
	var previousLedgerRevisionID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT revision_id
		FROM golden_position_ledger_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND group_revision_id = $3
		ORDER BY revision_number DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID, groupRevisionID).Scan(&previousLedgerRevisionID))
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revisions (
			revision_id, tournament_id, roster_id, group_revision_id,
			revision_number, previous_revision_id, payload_digest, finalized_at, created_at
		)
		SELECT $1, tournament_id, roster_id, group_revision_id,
			revision_number + 1, revision_id, $2, $3, $3
		FROM golden_position_ledger_revisions
		WHERE revision_id = $4`, lateLedgerRevisionID, lateDigest[:], lateAt, previousLedgerRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_attempts (
			ledger_revision_id, attempt_id, submission_revision_id,
			tournament_id, roster_id, group_revision_id,
			attempt_number, order_count, created_at
		)
		SELECT $1, attempt_id, submission_revision_id,
			tournament_id, roster_id, group_revision_id,
			attempt_number, order_count, $2
		FROM golden_position_ledger_attempts
		WHERE ledger_revision_id = $3`, lateLedgerRevisionID, lateAt, previousLedgerRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_commit_bindings (
			ledger_revision_id, attempt_id, position_commit_id,
			tournament_id, roster_id, participant_id,
			position, evidence_digest, created_at
		)
		SELECT $1, attempt_id, position_commit_id,
			tournament_id, roster_id, participant_id,
			position, evidence_digest, $2
		FROM golden_position_ledger_commit_bindings
		WHERE ledger_revision_id = $3`, lateLedgerRevisionID, lateAt, previousLedgerRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revision_seals (
			ledger_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`, lateLedgerRevisionID,
		fixture.tournamentID, fixture.rosterID, lateDigest[:], lateAt)
	require.NoError(t, err)
	require.Error(t, tx.Commit(ctx))
}

func TestGoldenCorrectionSealRejectsLateApplicableAttemptLineage(t *testing.T) {
	ctx := context.Background()
	fixture, _, groupRevisionID := prepareSealedGoldenCorrection(ctx, t)

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	lateAttemptID := uuid.New()
	lateAt := time.Now().UTC().Truncate(time.Microsecond)
	var attemptNumber int
	var previousAttemptID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1,
			(ARRAY_AGG(id ORDER BY attempt_number DESC))[1]
		FROM golden_attempts
		WHERE tournament_id = $1 AND roster_id = $2`,
		fixture.tournamentID, fixture.rosterID,
	).Scan(&attemptNumber, &previousAttemptID))
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number, previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`, lateAttemptID,
		fixture.tournamentID, fixture.rosterID, attemptNumber, previousAttemptID, lateAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempt_stage_groups (
			attempt_id, tournament_id, roster_id, group_revision_id, bound_at
		)
		VALUES ($1, $2, $3, $4, $5)`, lateAttemptID,
		fixture.tournamentID, fixture.rosterID, groupRevisionID, lateAt)
	require.NoError(t, err)
	require.Error(t, tx.Commit(ctx))
}

func TestCorrectionStartGoldenRejectsLateGroupAggregate(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id
		FROM game_attempts
		WHERE series_id = $1
		ORDER BY attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(
			txCtx, fixture.tournamentID, seriesID, gameID,
		)
		return loadErr
	}))
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	require.Equal(t, correctionusecase.StagePlayoffToGolden, mutation.Stage.Transition)
	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	resultingProjectionID, resultingProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	requireCorrectionGoldenCrossBindingRejected(
		ctx, t, fixture, mutation.Command.CommandID,
		authority.ProjectionRevisionID, authority.ProjectionRevision,
	)

	lateGroupID := uuid.New()
	lateGroupRevisionID := uuid.New()
	lateAt := time.Now().UTC().Truncate(time.Microsecond)
	lateProof := []byte(`{"proof":"late correction Golden group"}`)
	lateDigest := sha256.Sum256(lateProof)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_stage_tie_groups (
			command_id, tournament_id, roster_id, group_id, group_revision_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, proof, proof_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2, $8::JSONB, $9, $10)`,
		mutation.Command.CommandID, fixture.tournamentID, fixture.rosterID,
		lateGroupID, lateGroupRevisionID,
		resultingProjectionID, resultingProjectionRevision,
		lateProof, lateDigest[:], lateAt)
	require.NoError(t, err)
	for index, participantID := range fixture.participants[:2] {
		_, err = tx.Exec(ctx, `
			INSERT INTO tournament_stage_tie_group_members (
				command_id, tournament_id, roster_id, group_id,
				participant_id, standing_position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			mutation.Command.CommandID, fixture.tournamentID, fixture.rosterID,
			lateGroupID, participantID, index+1, lateAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_group_revisions (
			revision_id, group_id, stage_progression_command_id,
			tournament_id, roster_id, source_projection_revision_id,
			source_projection_revision, position_from, position_to,
			definition, definition_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2, $8::JSONB, $9, $10)`,
		lateGroupRevisionID, lateGroupID, mutation.Command.CommandID,
		fixture.tournamentID, fixture.rosterID,
		resultingProjectionID, resultingProjectionRevision,
		lateProof, lateDigest[:], lateAt)
	require.NoError(t, err)
	require.Error(t, tx.Commit(ctx))
}

func requireCorrectionGoldenCrossBindingRejected(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	commandID uuid.UUID,
	wrongSourceID uuid.UUID,
	wrongSourceRevision int64,
) {
	t.Helper()
	proof := []byte(`{"proof":"cross-bound correction Golden group"}`)
	digest := sha256.Sum256(proof)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_stage_tie_groups (
			command_id, tournament_id, roster_id, group_id, group_revision_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, proof, proof_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2, $8::JSONB, $9, $10)`,
		commandID, fixture.tournamentID, fixture.rosterID, uuid.New(), uuid.New(),
		wrongSourceID, wrongSourceRevision, proof, digest[:], time.Now().UTC(),
	)
	require.ErrorContains(t, err, "Golden tie evidence must match its stage progression source")
}

func TestGoldenCorrectionSealAllowsCoveredRootsCreatedBeforeSeal(t *testing.T) {
	ctx := context.Background()
	fixture, groupID, groupRevisionID, seriesID, gameID := prepareGoldenCorrectionBase(ctx, t)
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var stateRevisionID, ledgerRevisionID, attemptID uuid.UUID
	var mutation tournamentadmin.CorrectionMutation
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		tx, ok := fixture.tx.Conn(txCtx).(pgx.Tx)
		if !ok {
			return domain.ErrInternal
		}
		stateRevisionID, ledgerRevisionID, attemptID = insertCoveredGoldenRoots(
			txCtx, t, tx, fixture, groupID, groupRevisionID,
		)
		authority, loadErr := repository.LockCorrectionAuthority(
			txCtx, fixture.tournamentID, seriesID, gameID,
		)
		if loadErr != nil {
			return loadErr
		}
		mutation = correctionStageMutation(t, authority, seriesID, gameID)
		_, changed, commitErr := repository.CommitCorrection(txCtx, mutation)
		if commitErr != nil {
			return commitErr
		}
		if !changed {
			return domain.ErrConflict
		}
		return nil
	}))

	var stateTombstone, positionTombstone, attemptTombstone int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM golden_correction_state_tombstones
			 WHERE command_id = $1 AND state_revision_id = $2),
			(SELECT COUNT(*) FROM golden_correction_position_tombstones
			 WHERE command_id = $1 AND ledger_revision_id = $3),
			(SELECT COUNT(*) FROM golden_correction_attempt_tombstones
			 WHERE command_id = $1 AND attempt_id = $4)`,
		mutation.Command.CommandID, stateRevisionID, ledgerRevisionID, attemptID,
	).Scan(&stateTombstone, &positionTombstone, &attemptTombstone))
	require.Equal(t, 1, stateTombstone)
	require.Equal(t, 1, positionTombstone)
	require.Equal(t, 1, attemptTombstone)
}

func prepareSealedGoldenCorrection(
	ctx context.Context,
	t *testing.T,
) (tournamentAdminSwissProofFixture, uuid.UUID, uuid.UUID) {
	t.Helper()
	fixture, groupID, groupRevisionID, seriesID, gameID := prepareGoldenCorrectionBase(ctx, t)
	repository := postgres.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority tournamentadmin.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var loadErr error
		authority, loadErr = repository.LockCorrectionAuthority(
			txCtx, fixture.tournamentID, seriesID, gameID,
		)
		return loadErr
	}))
	mutation := correctionStageMutation(t, authority, seriesID, gameID)
	_, changed, err := repository.CommitCorrection(ctx, mutation)
	require.NoError(t, err)
	require.True(t, changed)
	return fixture, groupID, groupRevisionID
}

func prepareGoldenCorrectionBase(
	ctx context.Context,
	t *testing.T,
) (tournamentAdminSwissProofFixture, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	fixture := prepareNativeGoldenFinalSwiss(ctx, t)
	_, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	progressionCommand := progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: projectionRevision, Action: progression.ActionStartGolden,
	}
	require.NoError(t, publishSwissGolden(ctx, fixture, progressionCommand))
	var groupID, groupRevisionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT group_id, revision_id
		FROM golden_group_revisions
		WHERE stage_progression_command_id = $1
		ORDER BY position_from
		LIMIT 1`, progressionCommand.CommandID).Scan(&groupID, &groupRevisionID))
	_, _, _ = seedCorrectionRetainedGoldenState(ctx, t, fixture, groupID, groupRevisionID)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id
		FROM game_attempts
		WHERE series_id = $1
		ORDER BY attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))
	return fixture, groupID, groupRevisionID, seriesID, gameID
}

func insertCoveredGoldenRoots(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	fixture tournamentAdminSwissProofFixture,
	groupID uuid.UUID,
	groupRevisionID uuid.UUID,
) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	stateRevisionID := uuid.New()
	membershipRevisionID := uuid.New()
	stateDigest := sha256.Sum256([]byte("same transaction Golden state"))
	membershipDigest := sha256.Sum256([]byte("same transaction Golden membership"))
	var previousStateRevisionID uuid.UUID
	var previousStateRevision int64
	var previousStateDigest []byte
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT revision_id, revision_number, payload_digest
		FROM golden_state_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND group_revision_id = $3
		ORDER BY revision_number DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID, groupRevisionID).Scan(
		&previousStateRevisionID, &previousStateRevision, &previousStateDigest,
	))

	attemptID := uuid.New()
	var attemptNumber int
	var previousAttemptID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1,
			(ARRAY_AGG(id ORDER BY attempt_number DESC))[1]
		FROM golden_attempts
		WHERE tournament_id = $1 AND roster_id = $2`,
		fixture.tournamentID, fixture.rosterID,
	).Scan(&attemptNumber, &previousAttemptID))
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number, previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`, attemptID,
		fixture.tournamentID, fixture.rosterID, attemptNumber, previousAttemptID, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempt_stage_groups (
			attempt_id, tournament_id, roster_id, group_revision_id, bound_at
		)
		VALUES ($1, $2, $3, $4, $5)`, attemptID,
		fixture.tournamentID, fixture.rosterID, groupRevisionID, createdAt)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revisions (
			revision_id, tournament_id, roster_id, group_id, group_revision_id,
			revision_number, previous_revision_id, plan_id,
			membership_revision_id, membership_revision, membership_previous_revision_id,
			membership_digest, payload_digest, created_at
		)
		SELECT $1, tournament_id, roster_id, group_id, group_revision_id,
			revision_number + 1, revision_id, plan_id,
			$2, membership_revision + 1, membership_revision_id,
			$3, $4, $5
		FROM golden_state_revisions
		WHERE revision_id = $6`, stateRevisionID, membershipRevisionID,
		membershipDigest[:], stateDigest[:], createdAt, previousStateRevisionID)
	require.NoError(t, err)
	allocationID := uuid.New()
	allocationCommandID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_transitions (
			state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
			transition_kind, command_id, previous_state_revision_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'allocation', $6, $7, $8, $8)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupID,
		groupRevisionID, allocationCommandID, previousStateRevisionID, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_members (
			state_revision_id, tournament_id, roster_id, participant_id,
			excluded, position, created_at
		)
		SELECT $1, tournament_id, roster_id, participant_id, excluded, position, $2
		FROM golden_state_members
		WHERE state_revision_id = $3`, stateRevisionID, createdAt, previousStateRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_attempts (
			state_revision_id, tournament_id, roster_id, group_revision_id,
			attempt_id, attempt_number, previous_attempt_id, state,
			retained_at, started_at, finished_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, NULL, 'planned', $6, NULL, NULL, $6)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupRevisionID,
		attemptID, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_attempt_members (
			state_revision_id, attempt_id, tournament_id, roster_id,
			participant_id, position, created_at
		)
		SELECT $1, $2, tournament_id, roster_id, participant_id, position, $3
		FROM golden_state_members
		WHERE state_revision_id = $4`, stateRevisionID, attemptID, createdAt, previousStateRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_allocations (
			allocation_id, command_id, state_revision_id, tournament_id, roster_id,
			group_id, group_revision_id, expected_state_revision_id,
			expected_state_revision, expected_state_payload_digest,
			allocated_at, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $11)`,
		allocationID, allocationCommandID, stateRevisionID, fixture.tournamentID,
		fixture.rosterID, groupID, groupRevisionID, previousStateRevisionID,
		previousStateRevision, previousStateDigest, createdAt, stateDigest[:])
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revision_seals (
			state_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`, stateRevisionID,
		fixture.tournamentID, fixture.rosterID, stateDigest[:], createdAt)
	require.NoError(t, err)

	ledgerRevisionID := uuid.New()
	ledgerDigest := sha256.Sum256([]byte("same transaction Golden ledger"))
	var previousLedgerRevisionID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT revision_id
		FROM golden_position_ledger_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND group_revision_id = $3
		ORDER BY revision_number DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID, groupRevisionID).Scan(&previousLedgerRevisionID))
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revisions (
			revision_id, tournament_id, roster_id, group_revision_id,
			revision_number, previous_revision_id, payload_digest, finalized_at, created_at
		)
		SELECT $1, tournament_id, roster_id, group_revision_id,
			revision_number + 1, revision_id, $2, $3, $3
		FROM golden_position_ledger_revisions
		WHERE revision_id = $4`, ledgerRevisionID, ledgerDigest[:], createdAt, previousLedgerRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_attempts (
			ledger_revision_id, attempt_id, submission_revision_id,
			tournament_id, roster_id, group_revision_id,
			attempt_number, order_count, created_at
		)
		SELECT $1, attempt_id, submission_revision_id,
			tournament_id, roster_id, group_revision_id,
			attempt_number, order_count, $2
		FROM golden_position_ledger_attempts
		WHERE ledger_revision_id = $3`, ledgerRevisionID, createdAt, previousLedgerRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_commit_bindings (
			ledger_revision_id, attempt_id, position_commit_id,
			tournament_id, roster_id, participant_id,
			position, evidence_digest, created_at
		)
		SELECT $1, attempt_id, position_commit_id,
			tournament_id, roster_id, participant_id,
			position, evidence_digest, $2
		FROM golden_position_ledger_commit_bindings
		WHERE ledger_revision_id = $3`, ledgerRevisionID, createdAt, previousLedgerRevisionID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revision_seals (
			ledger_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`, ledgerRevisionID,
		fixture.tournamentID, fixture.rosterID, ledgerDigest[:], createdAt)
	require.NoError(t, err)
	return stateRevisionID, ledgerRevisionID, attemptID
}

func openCorrectionReadyWave(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) {
	t.Helper()
	projectionID, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	waveID := uuid.New()
	repository := postgres.NewWavePostgres(fixture.tx)
	wave, err := repository.Create(ctx, postgres.WaveCreateInput{
		ID: waveID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		RevisionID: domain.WaveRevisionID(uuid.New()), ParticipantIDs: fixture.participants,
		CommandID: uuid.New(), SourceProjectionRevisionID: projectionID,
		SourceProjectionRevision: projectionRevision, CreatedAt: now,
		Series: []postgres.WaveSeriesInput{
			{ID: uuid.New(), FirstParticipantID: fixture.participants[0], SecondParticipantID: fixture.participants[1], Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New())},
			{ID: uuid.New(), FirstParticipantID: fixture.participants[2], SecondParticipantID: fixture.participants[3], Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New())},
		},
	})
	require.NoError(t, err)
	_, changed, err := repository.OpenReadyWindow(ctx, fixture.tournamentID, waveID, wave.Revision, postgres.ReadyWindowInput{
		ID: uuid.New(), RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
		OpenedAt: now.Add(time.Millisecond), Deadline: now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.True(t, changed)
}

func currentCorrectionStandingsParticipants(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	count int,
) []uuid.UUID {
	t.Helper()
	rows, err := sharedPool.Query(ctx, `
		SELECT member.participant_id
		FROM projection_revisions AS revision
		INNER JOIN projection_revision_artifacts AS link
			ON link.revision_id = revision.id
			AND link.tournament_id = revision.tournament_id
			AND link.roster_id = revision.roster_id
			AND link.artifact_kind = 'standings'
		INNER JOIN projection_artifact_members AS member
			ON member.artifact_id = link.artifact_id
			AND member.tournament_id = link.tournament_id
			AND member.roster_id = link.roster_id
			AND member.artifact_kind = link.artifact_kind
		WHERE revision.tournament_id = $1
			AND revision.roster_id = $2
			AND revision.state = 'published'
		ORDER BY member.position
		LIMIT $3`, fixture.tournamentID, fixture.rosterID, count)
	require.NoError(t, err)
	defer rows.Close()
	participants := make([]uuid.UUID, 0, count)
	for rows.Next() {
		var participantID uuid.UUID
		require.NoError(t, rows.Scan(&participantID))
		participants = append(participants, participantID)
	}
	require.NoError(t, rows.Err())
	require.Len(t, participants, count)
	return participants
}

func seedCorrectionGoldenStage(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	return seedCorrectionGoldenStageWithParticipants(ctx, t, fixture, fixture.participants[:2])
}

func seedCorrectionGoldenStageWithParticipants(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	groupParticipants []uuid.UUID,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	require.Len(t, groupParticipants, 2)
	projectionID, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	commandID := uuid.New()
	groupID := uuid.New()
	groupRevisionID := uuid.New()
	changedAt := time.Now().UTC().Truncate(time.Microsecond)
	proof := []byte(`{"proof":"correction Golden hydration"}`)
	digest := sha256.Sum256(proof)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceState, preset string
	var sourceRevision int64
	var rosterSize int32
	var createdAt, startedAt time.Time
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT tournament.state, tournament.revision, tournament.preset,
			(SELECT COUNT(*) FROM participants WHERE roster_id = $2)::INTEGER,
			tournament.created_at, tournament.started_at
		FROM tournaments AS tournament
		WHERE tournament.id = $1
		FOR UPDATE`, fixture.tournamentID, fixture.rosterID).Scan(
		&sourceState, &sourceRevision, &preset, &rosterSize, &createdAt, &startedAt,
	))
	require.Contains(t, []string{string(domain.TournamentStateSwiss), string(domain.TournamentStatePlayoffs)}, sourceState)
	action := "correction_start_golden"
	if sourceState == string(domain.TournamentStateSwiss) {
		action = "start_golden"
	}
	resultingRevision := sourceRevision + 1
	_, err = tx.Exec(ctx, `
		UPDATE tournaments
		SET state = 'golden', revision = $2, updated_at = $3
		WHERE id = $1 AND state = $5 AND revision = $4`,
		fixture.tournamentID, resultingRevision, changedAt, sourceRevision, sourceState)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_lifecycle_commands (
			command_id, tournament_id, roster_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_tournament_state,
			resulting_tournament_revision, resulting_tournament_state,
			preset, roster_size, tournament_created_at, tournament_updated_at,
			tournament_started_at, tournament_finished_at, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $14,
			$5, $6, $7, $15, $8, 'golden',
			$9, $10, $11, $12, $13, NULL, $12, $12
		)`,
		commandID, fixture.tournamentID, fixture.rosterID, fixture.participants[0],
		projectionID, projectionRevision, sourceRevision, resultingRevision,
		preset, rosterSize, createdAt, changedAt, startedAt, action, sourceState)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_stage_progressions (
			command_id, tournament_id, roster_id, actor_id, action,
			source_tournament_revision, source_tournament_state,
			source_projection_revision_id, source_projection_revision,
			resulting_projection_revision_id, resulting_projection_revision,
			resulting_tournament_revision, resulting_tournament_state,
			proof, proof_digest, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $12,
			$5, ($13::text)::varchar, $6::uuid, $7::bigint,
			CASE WHEN $13::text = 'swiss' THEN NULL::uuid ELSE $6::uuid END,
			CASE WHEN $13::text = 'swiss' THEN NULL::bigint ELSE $7::bigint END,
			$8, 'golden',
			$9::JSONB, $10, $11, $11
		)`,
		commandID, fixture.tournamentID, fixture.rosterID, fixture.participants[0],
		sourceRevision, projectionID, projectionRevision, resultingRevision,
		proof, digest[:], changedAt, action, sourceState)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_stage_tie_groups (
			command_id, tournament_id, roster_id, group_id, group_revision_id,
			source_projection_revision_id, source_projection_revision,
			position_from, position_to, proof, proof_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2, $8::JSONB, $9, $10)`,
		commandID, fixture.tournamentID, fixture.rosterID, groupID, groupRevisionID,
		projectionID, projectionRevision, proof, digest[:], changedAt)
	require.NoError(t, err)
	for index, participantID := range groupParticipants {
		_, err = tx.Exec(ctx, `
			INSERT INTO tournament_stage_tie_group_members (
				command_id, tournament_id, roster_id, group_id,
				participant_id, standing_position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			commandID, fixture.tournamentID, fixture.rosterID, groupID,
			participantID, index+1, changedAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_group_revisions (
			revision_id, group_id, stage_progression_command_id,
			tournament_id, roster_id, source_projection_revision_id,
			source_projection_revision, position_from, position_to,
			definition, definition_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 2, $8::JSONB, $9, $10)`,
		groupRevisionID, groupID, commandID, fixture.tournamentID, fixture.rosterID,
		projectionID, projectionRevision, proof, digest[:], changedAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return groupID, groupRevisionID
}

func seedCorrectionRetainedGoldenState(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	groupID uuid.UUID,
	groupRevisionID uuid.UUID,
) ([]uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	memberRows, err := sharedPool.Query(ctx, `
		SELECT member.participant_id
		FROM golden_group_revisions AS revision
		JOIN tournament_stage_tie_group_members AS member
			ON member.command_id = revision.stage_progression_command_id
			AND member.tournament_id = revision.tournament_id
			AND member.roster_id = revision.roster_id
			AND member.group_id = revision.group_id
		WHERE revision.revision_id = $1
		ORDER BY member.standing_position`, groupRevisionID)
	require.NoError(t, err)
	groupParticipantIDs := make([]uuid.UUID, 0, 4)
	for memberRows.Next() {
		var participantID uuid.UUID
		require.NoError(t, memberRows.Scan(&participantID))
		groupParticipantIDs = append(groupParticipantIDs, participantID)
	}
	require.NoError(t, memberRows.Err())
	memberRows.Close()
	require.Len(t, groupParticipantIDs, 2)
	projectionID, projectionRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	var artifactID uuid.UUID
	var artifactDigest []byte
	var sourcePreviousRevisionID uuid.NullUUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT artifact.id, artifact.payload_digest, revision.previous_revision_id
		FROM projection_revisions AS revision
		INNER JOIN projection_revision_artifacts AS link
			ON link.revision_id = revision.id
			AND link.tournament_id = revision.tournament_id
			AND link.roster_id = revision.roster_id
		INNER JOIN projection_artifacts AS artifact
			ON artifact.id = link.artifact_id
			AND artifact.tournament_id = link.tournament_id
			AND artifact.roster_id = link.roster_id
		WHERE link.revision_id = $1
			AND link.tournament_id = $2
			AND link.roster_id = $3
			AND link.artifact_kind = 'standings'`,
		projectionID, fixture.tournamentID, fixture.rosterID).Scan(
		&artifactID, &artifactDigest, &sourcePreviousRevisionID,
	))
	reservationRows, err := sharedPool.Query(ctx, `
		SELECT reservation.id, snapshot.id, reservation.task_id, reservation.task_version
		FROM task_version_reservations AS reservation
		INNER JOIN assignment_plans AS plan ON plan.id = reservation.plan_id
		INNER JOIN task_snapshots AS snapshot ON snapshot.reservation_id = reservation.id
		WHERE plan.tournament_id = $1 AND plan.roster_id = $2
		ORDER BY reservation.created_at, reservation.id
		LIMIT 12`, fixture.tournamentID, fixture.rosterID)
	require.NoError(t, err)
	reservations := make([]assignmentReservationFixture, 0, 12)
	for reservationRows.Next() {
		var reservation assignmentReservationFixture
		require.NoError(t, reservationRows.Scan(
			&reservation.reservationID, &reservation.snapshotID,
			&reservation.taskID, &reservation.taskVersion,
		))
		reservations = append(reservations, reservation)
	}
	require.NoError(t, reservationRows.Err())
	reservationRows.Close()
	authorityFixture := goldenExactPlanAuthorityFixture{
		golden: goldenMigrationFixture{
			tournamentID: fixture.tournamentID, rosterID: fixture.rosterID,
			participantIDs: append([]uuid.UUID(nil), groupParticipantIDs...),
			createdAt:      time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Microsecond),
		},
		sourceProjectionID: projectionID, sourceProjectionRevision: projectionRevision,
		sourceArtifactID: artifactID, sourceArtifactDigest: artifactDigest,
		groupID: groupID, groupRevisionID: groupRevisionID,
		reservations: reservations,
	}
	type stagedGoldenMember struct {
		participantID uuid.UUID
		position      int
	}
	type stagedGoldenGroup struct {
		groupID, revisionID uuid.UUID
		positionFrom        int
		positionTo          int
		definitionDigest    []byte
		members             []stagedGoldenMember
	}
	groupRows, err := sharedPool.Query(ctx, `
		SELECT revision.group_id, revision.revision_id,
			revision.position_from, revision.position_to, revision.definition_digest,
			member.participant_id, member.standing_position
		FROM golden_group_revisions AS revision
		JOIN tournament_stage_tie_group_members AS member
			ON member.command_id = revision.stage_progression_command_id
			AND member.tournament_id = revision.tournament_id
			AND member.roster_id = revision.roster_id
			AND member.group_id = revision.group_id
		WHERE revision.tournament_id = $1 AND revision.roster_id = $2
			AND revision.source_projection_revision_id = $3
			AND revision.source_projection_revision = $4
		ORDER BY revision.position_from, member.standing_position`,
		fixture.tournamentID, fixture.rosterID, projectionID, projectionRevision)
	require.NoError(t, err)
	groups := make([]stagedGoldenGroup, 0, 2)
	for groupRows.Next() {
		var group stagedGoldenGroup
		var member stagedGoldenMember
		require.NoError(t, groupRows.Scan(
			&group.groupID, &group.revisionID, &group.positionFrom, &group.positionTo,
			&group.definitionDigest, &member.participantID, &member.position,
		))
		if len(groups) == 0 || groups[len(groups)-1].revisionID != group.revisionID {
			groups = append(groups, group)
		}
		groups[len(groups)-1].members = append(groups[len(groups)-1].members, member)
	}
	require.NoError(t, groupRows.Err())
	groupRows.Close()
	require.NotEmpty(t, groups)
	require.GreaterOrEqual(t, len(reservations), len(groups)*3)
	reservations = reservations[:len(groups)*3]
	authorityFixture.reservations = reservations
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	planID := insertCorrectionGoldenExactPlanRoot(
		ctx, t, tx, authorityFixture, sourcePreviousRevisionID,
	)
	allGroupParticipants := make([]uuid.UUID, 0, len(fixture.participants))
	for groupOrdinal, group := range groups {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_groups (
				plan_id, tournament_id, roster_id, group_id, group_revision_id,
				source_projection_revision_id, source_projection_revision,
				position_from, position_to, group_ordinal, definition_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			planID, fixture.tournamentID, fixture.rosterID, group.groupID, group.revisionID,
			projectionID, projectionRevision, group.positionFrom, group.positionTo,
			groupOrdinal+1, group.definitionDigest, authorityFixture.golden.createdAt.Add(time.Minute))
		require.NoError(t, err)
		for _, member := range group.members {
			_, err = tx.Exec(ctx, `
				INSERT INTO golden_exact_plan_snapshot_members (
					plan_id, group_revision_id, tournament_id, roster_id,
					participant_id, position, created_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				planID, group.revisionID, fixture.tournamentID, fixture.rosterID,
				member.participantID, member.position, authorityFixture.golden.createdAt.Add(time.Minute))
			require.NoError(t, err)
			allGroupParticipants = append(allGroupParticipants, member.participantID)
		}
		groupFixture := authorityFixture
		groupFixture.groupID = group.groupID
		groupFixture.groupRevisionID = group.revisionID
		groupFixture.reservations = reservations[groupOrdinal*3 : groupOrdinal*3+3]
		insertGoldenExactPlanEdges(ctx, t, tx, groupFixture, planID)
	}
	insertGoldenExactPlanCandidates(ctx, t, tx, authorityFixture, planID)
	insertGoldenExactPlanReservations(ctx, t, tx, authorityFixture, planID)
	for _, participantID := range allGroupParticipants {
		var playerID, reservationID uuid.UUID
		var revision int64
		var acquiredAt, updatedAt time.Time
		require.NoError(t, tx.QueryRow(ctx, `
			SELECT player_id FROM participants WHERE roster_id = $1 AND id = $2`,
			fixture.rosterID, participantID).Scan(&playerID))
		require.NoError(t, tx.QueryRow(ctx, `
			WITH inserted AS (
				INSERT INTO participant_reservations (player_id, tournament_id)
				VALUES ($1, $2)
				ON CONFLICT (player_id) DO NOTHING
				RETURNING reservation_id, revision, acquired_at, updated_at
			)
			SELECT reservation_id, revision, acquired_at, updated_at FROM inserted
			UNION ALL
			SELECT reservation_id, revision, acquired_at, updated_at
			FROM participant_reservations
			WHERE player_id = $1 AND tournament_id = $2
				AND NOT EXISTS (SELECT 1 FROM inserted)
			LIMIT 1`,
			playerID, fixture.tournamentID).Scan(
			&reservationID, &revision, &acquiredAt, &updatedAt,
		))
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_exact_plan_snapshot_participant_reservations (
				plan_id, tournament_id, roster_id, participant_id, player_id,
				reservation_id, revision, acquired_at, updated_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			planID, fixture.tournamentID, fixture.rosterID, participantID, playerID,
			reservationID, revision, acquiredAt, updatedAt, authorityFixture.golden.createdAt.Add(time.Minute))
		require.NoError(t, err)
	}
	sealGoldenExactPlan(ctx, t, tx, authorityFixture, planID)
	attemptID := uuid.New()
	retainedAt := authorityFixture.golden.createdAt.Add(2 * time.Minute)
	var physicalAttemptNumber int
	var physicalPreviousAttemptID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1,
			COALESCE((ARRAY_AGG(id ORDER BY attempt_number DESC))[1],
				'00000000-0000-0000-0000-000000000000'::UUID)
		FROM golden_attempts
		WHERE tournament_id = $1 AND roster_id = $2`,
		fixture.tournamentID, fixture.rosterID).Scan(&physicalAttemptNumber, &physicalPreviousAttemptID))
	var physicalPrevious any
	if physicalAttemptNumber > 1 {
		physicalPrevious = physicalPreviousAttemptID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number, previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		attemptID, fixture.tournamentID, fixture.rosterID,
		physicalAttemptNumber, physicalPrevious, retainedAt.Add(-time.Second))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempt_stage_groups (
			attempt_id, tournament_id, roster_id, group_revision_id, bound_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		attemptID, fixture.tournamentID, fixture.rosterID, groupRevisionID, retainedAt)
	require.NoError(t, err)
	stateRevisionID := uuid.New()
	membershipRevisionID := uuid.New()
	stateDigest := sha256.Sum256([]byte("retained Golden state"))
	membershipDigest := sha256.Sum256([]byte("retained Golden membership"))
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revisions (
			revision_id, tournament_id, roster_id, group_id, group_revision_id,
			revision_number, previous_revision_id, plan_id,
			membership_revision_id, membership_revision, membership_previous_revision_id,
			membership_digest, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, NULL, $6, $7, 1, NULL, $8, $9, $10)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupID, groupRevisionID,
		planID, membershipRevisionID, membershipDigest[:], stateDigest[:], retainedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_transitions (
			state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
			transition_kind, command_id, previous_state_revision_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'initial', NULL, NULL, $6, $6)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupID, groupRevisionID, retainedAt)
	require.NoError(t, err)
	for index, participantID := range authorityFixture.golden.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_state_members (
				state_revision_id, tournament_id, roster_id, participant_id,
				excluded, position, created_at
			)
			VALUES ($1, $2, $3, $4, false, $5, $6)`,
			stateRevisionID, fixture.tournamentID, fixture.rosterID, participantID, index+1, retainedAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_attempts (
			state_revision_id, tournament_id, roster_id, group_revision_id,
			attempt_id, attempt_number, previous_attempt_id, state,
			retained_at, started_at, finished_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, NULL, 'planned', $6, NULL, NULL, $6)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupRevisionID, attemptID, retainedAt)
	require.NoError(t, err)
	for index, participantID := range authorityFixture.golden.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_state_attempt_members (
				state_revision_id, attempt_id, tournament_id, roster_id,
				participant_id, position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			stateRevisionID, attemptID, fixture.tournamentID, fixture.rosterID,
			participantID, index+1, retainedAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revision_seals (
			state_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, stateDigest[:], retainedAt)
	require.NoError(t, err)
	historicalStateRevisionID := stateRevisionID
	stateRevisionID = uuid.New()
	allocationCommandID := uuid.New()
	allocationID := uuid.New()
	nextStateDigest := sha256.Sum256([]byte("retained Golden state revision two"))
	nextMembershipDigest := sha256.Sum256([]byte("retained Golden membership revision two"))
	nextMembershipRevisionID := uuid.New()
	nextStateAt := retainedAt.Add(time.Second)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revisions (
			revision_id, tournament_id, roster_id, group_id, group_revision_id,
			revision_number, previous_revision_id, plan_id,
			membership_revision_id, membership_revision, membership_previous_revision_id,
			membership_digest, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 2, $6, $7, $8, 2, $9, $10, $11, $12)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupID, groupRevisionID,
		historicalStateRevisionID, planID, nextMembershipRevisionID, membershipRevisionID,
		nextMembershipDigest[:], nextStateDigest[:], nextStateAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_transitions (
			state_revision_id, tournament_id, roster_id, group_id, group_revision_id,
			transition_kind, command_id, previous_state_revision_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'allocation', $6, $7, $8, $8)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupID, groupRevisionID,
		allocationCommandID, historicalStateRevisionID, nextStateAt)
	require.NoError(t, err)
	for index, participantID := range authorityFixture.golden.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_state_members (
				state_revision_id, tournament_id, roster_id, participant_id,
				excluded, position, created_at
			)
			VALUES ($1, $2, $3, $4, false, $5, $6)`,
			stateRevisionID, fixture.tournamentID, fixture.rosterID, participantID, index+1, nextStateAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_attempts (
			state_revision_id, tournament_id, roster_id, group_revision_id,
			attempt_id, attempt_number, previous_attempt_id, state,
			retained_at, started_at, finished_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, NULL, 'planned', $6, NULL, NULL, $6)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, groupRevisionID, attemptID, retainedAt)
	require.NoError(t, err)
	for index, participantID := range authorityFixture.golden.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_state_attempt_members (
				state_revision_id, attempt_id, tournament_id, roster_id,
				participant_id, position, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			stateRevisionID, attemptID, fixture.tournamentID, fixture.rosterID,
			participantID, index+1, nextStateAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_allocations (
			allocation_id, command_id, state_revision_id, tournament_id, roster_id,
			group_id, group_revision_id, expected_state_revision_id,
			expected_state_revision, expected_state_payload_digest,
			allocated_at, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9, $10, $11, $10)`,
		allocationID, allocationCommandID, stateRevisionID, fixture.tournamentID, fixture.rosterID,
		groupID, groupRevisionID, historicalStateRevisionID, stateDigest[:], nextStateAt,
		nextStateDigest[:])
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_state_revision_seals (
			state_revision_id, tournament_id, roster_id, payload_digest, sealed_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		stateRevisionID, fixture.tournamentID, fixture.rosterID, nextStateDigest[:], nextStateAt)
	require.NoError(t, err)
	scopeID := uuid.New()
	sessionID := uuid.New()
	prestartRevisionID := uuid.New()
	prestartDigest := sha256.Sum256([]byte("retained Golden prestart"))
	document, err := json.Marshal(map[string]any{
		"schema": "golden-aggregate-v1", "kind": "prestart",
		"revision_id": prestartRevisionID, "revision_number": 1,
		"payload_digest": hex.EncodeToString(prestartDigest[:]),
		"document":       map[string]any{"session_id": sessionID, "state": "paused"},
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_repository_scopes (
			id, aggregate_kind, tournament_id, roster_id,
			group_id, group_revision_id, created_at
		)
		VALUES ($1, 'prestart', $2, $3, $4, $5, $6)`,
		scopeID, fixture.tournamentID, fixture.rosterID, groupID, groupRevisionID, retainedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_repository_revisions (
			scope_id, aggregate_kind, revision_id, revision_number,
			previous_revision_id, payload, payload_digest, created_at
		)
		VALUES ($1, 'prestart', $2, 1, NULL, $3::JSONB, $4, $5)`,
		scopeID, prestartRevisionID, document, prestartDigest[:], retainedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_repository_heads (
			scope_id, revision_id, revision_number, payload_digest, updated_at
		)
		VALUES ($1, $2, 1, $3, $4)`,
		scopeID, prestartRevisionID, prestartDigest[:], retainedAt)
	require.NoError(t, err)
	ledgerRevisionID := seedCorrectionGoldenPositionLedger(
		ctx, t, tx, fixture, groupRevisionID, groupParticipantIDs, retainedAt.Add(2*time.Second),
	)
	require.NoError(t, tx.Commit(ctx))
	return []uuid.UUID{historicalStateRevisionID, stateRevisionID}, ledgerRevisionID, attemptID
}

func seedCorrectionGoldenPositionLedger(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	fixture tournamentAdminSwissProofFixture,
	groupRevisionID uuid.UUID,
	groupParticipantIDs []uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()
	var positionFrom int
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT position_from
		FROM golden_group_revisions
		WHERE revision_id = $1 AND tournament_id = $2 AND roster_id = $3`,
		groupRevisionID, fixture.tournamentID, fixture.rosterID,
	).Scan(&positionFrom))
	var waveID, assignmentID, snapshotID, taskID uuid.UUID
	var taskVersion int32
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT wave.id, assignment.id, assignment.snapshot_id,
			assignment.task_id, assignment.task_version
		FROM waves AS wave
		INNER JOIN wave_series AS wave_series
			ON wave_series.wave_id = wave.id
			AND wave_series.tournament_id = wave.tournament_id
			AND wave_series.roster_id = wave.roster_id
		INNER JOIN game_attempts AS game_attempt
			ON game_attempt.series_id = wave_series.series_id
			AND game_attempt.roster_id = wave_series.roster_id
		INNER JOIN assignments AS assignment
			ON assignment.attempt_id = game_attempt.id
			AND assignment.series_id = game_attempt.series_id
			AND assignment.roster_id = game_attempt.roster_id
		WHERE wave.tournament_id = $1 AND wave.roster_id = $2
		ORDER BY assignment.created_at, assignment.id
		LIMIT 1`, fixture.tournamentID, fixture.rosterID).Scan(
		&waveID, &assignmentID, &snapshotID, &taskID, &taskVersion,
	))

	var attemptNumber int
	var previousAttemptID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1,
			COALESCE((ARRAY_AGG(id ORDER BY attempt_number DESC))[1],
				'00000000-0000-0000-0000-000000000000'::UUID)
		FROM golden_attempts
		WHERE tournament_id = $1 AND roster_id = $2`,
		fixture.tournamentID, fixture.rosterID).Scan(&attemptNumber, &previousAttemptID))
	var previousAttempt any
	if attemptNumber > 1 {
		previousAttempt = previousAttemptID
	}
	attemptID := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number, previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		attemptID, fixture.tournamentID, fixture.rosterID, attemptNumber, previousAttempt, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempt_stage_groups (
			attempt_id, tournament_id, roster_id, group_revision_id, bound_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		attemptID, fixture.tournamentID, fixture.rosterID, groupRevisionID, createdAt)
	require.NoError(t, err)
	payloadDigest := sha256.Sum256([]byte("correction historical position ledger"))
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_attempt_authorities (
			attempt_id, tournament_id, roster_id, group_revision_id,
			wave_id, assignment_id, snapshot_id, task_id, task_version,
			source_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		attemptID, fixture.tournamentID, fixture.rosterID, groupRevisionID,
		waveID, assignmentID, snapshotID, taskID, taskVersion, payloadDigest[:], createdAt)
	require.NoError(t, err)

	membershipIDs := make([]uuid.UUID, 0, len(groupParticipantIDs))
	for _, participantID := range groupParticipantIDs {
		membershipID := uuid.New()
		membershipIDs = append(membershipIDs, membershipID)
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_memberships (
				id, attempt_id, tournament_id, roster_id, participant_id,
				selection_kind, selected_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, 'direct', $6, $6)`,
			membershipID, attemptID, fixture.tournamentID, fixture.rosterID, participantID, createdAt)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE golden_memberships
			SET ready_at = $2, participation_established_at = $3
			WHERE id = $1`, membershipID, createdAt.Add(time.Millisecond), createdAt.Add(2*time.Millisecond))
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'ready', disclosed_at = $2, ready_at = $3
		WHERE id = $1`, attemptID, createdAt.Add(time.Millisecond), createdAt.Add(2*time.Millisecond))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'active', started_at = $2
		WHERE id = $1`, attemptID, createdAt.Add(3*time.Millisecond))
	require.NoError(t, err)

	positionCommitIDs := make([]uuid.UUID, len(groupParticipantIDs))
	submissionRevisionIDs := make([]uuid.UUID, len(groupParticipantIDs))
	for index, participantID := range groupParticipantIDs {
		position := positionFrom + index
		membershipID := membershipIDs[index]
		submissionID := uuid.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_provisional_submissions (
				id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
				server_sequence, idempotency_key, provisional_position,
				elapsed_milliseconds, status, payload_digest,
				submitted_at, received_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1000, 'accepted', $10, $11, $11, $11)`,
			submissionID, attemptID, fixture.tournamentID, fixture.rosterID, membershipID,
			participantID, position, uuid.New(), position, payloadDigest[:], createdAt.Add(4*time.Millisecond))
		require.NoError(t, err)
		positionCommitIDs[index] = uuid.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_position_commits (
				id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
				provisional_submission_id, previous_position_commit_id,
				position, committed_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, $8, $9, $9)`,
			positionCommitIDs[index], attemptID, fixture.tournamentID, fixture.rosterID,
			membershipID, participantID, submissionID, position, createdAt.Add(5*time.Millisecond))
		require.NoError(t, err)
		submissionRevisionIDs[index] = uuid.New()
		var previousSubmissionRevision any
		if index > 0 {
			previousSubmissionRevision = submissionRevisionIDs[index-1]
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_attempt_submission_revisions (
				revision_id, attempt_id, tournament_id, roster_id,
				membership_id, participant_id, revision_number, previous_revision_id,
				provisional_submission_id, payload_digest, committed_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`,
			submissionRevisionIDs[index], attemptID, fixture.tournamentID, fixture.rosterID,
			membershipID, participantID, index+1, previousSubmissionRevision,
			submissionID, payloadDigest[:], createdAt.Add(6*time.Millisecond))
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'completed', completed_at = $2
		WHERE id = $1`, attemptID, createdAt.Add(7*time.Millisecond))
	require.NoError(t, err)
	rootLedgerRevisionID := uuid.New()
	ledgerRevisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO golden_position_ledger_revisions (
			revision_id, tournament_id, roster_id, group_revision_id,
			revision_number, previous_revision_id, payload_digest, finalized_at, created_at
		)
		VALUES
			($1, $3, $4, $5, 1, NULL, $6, $7, $7),
			($2, $3, $4, $5, 2, $1, $6, $8, $8)`,
		rootLedgerRevisionID, ledgerRevisionID, fixture.tournamentID, fixture.rosterID,
		groupRevisionID, payloadDigest[:], createdAt.Add(6*time.Millisecond),
		createdAt.Add(7*time.Millisecond))
	require.NoError(t, err)
	for _, revisionID := range []uuid.UUID{rootLedgerRevisionID, ledgerRevisionID} {
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_position_ledger_attempts (
				ledger_revision_id, attempt_id, submission_revision_id,
				tournament_id, roster_id, group_revision_id,
				attempt_number, order_count, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			revisionID, attemptID, submissionRevisionIDs[0], fixture.tournamentID,
			fixture.rosterID, groupRevisionID, attemptNumber, len(groupParticipantIDs), createdAt.Add(7*time.Millisecond))
		require.NoError(t, err)
		for index, participantID := range groupParticipantIDs {
			_, err = tx.Exec(ctx, `
			INSERT INTO golden_position_ledger_commit_bindings (
				ledger_revision_id, attempt_id, position_commit_id,
				tournament_id, roster_id, participant_id,
				position, evidence_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				revisionID, attemptID, positionCommitIDs[index], fixture.tournamentID,
				fixture.rosterID, participantID, positionFrom+index, payloadDigest[:], createdAt.Add(7*time.Millisecond))
			require.NoError(t, err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO golden_position_ledger_revision_seals (
				ledger_revision_id, tournament_id, roster_id, payload_digest, sealed_at
			)
			VALUES ($1, $2, $3, $4, $5)`,
			revisionID, fixture.tournamentID, fixture.rosterID,
			payloadDigest[:], createdAt.Add(7*time.Millisecond))
		require.NoError(t, err)
	}
	return ledgerRevisionID
}

func insertCorrectionGoldenExactPlanRoot(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	fixture goldenExactPlanAuthorityFixture,
	sourcePreviousRevisionID uuid.NullUUID,
) uuid.UUID {
	t.Helper()
	planID := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshots (
			plan_id, plan_revision_id, tournament_id, roster_id, plan_set_id,
			source_projection_revision_id, source_projection_revision,
			source_projection_previous_revision_id,
			source_standings_artifact_id, source_standings_payload_digest,
			group_set_revision_id, group_set_revision, pool_revision_id, pool_revision,
			history_revision_id, history_revision, task_health_revision_id, task_health_revision,
			artifact_revision_id, artifact_revision, reservation_revision_id, reservation_revision,
			membership_revision_id, membership_revision, source_payload_digest, group_digest,
			pool_digest, history_digest, task_health_digest, artifact_digest, reservation_digest,
			membership_digest, proof_hash, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10,
			$11, 1, $12, 1,
			$13, 1, $14, 1,
			$15, 1, $16, 1,
			$17, 1, $10, $18,
			$19, $20, $21, $22, $23,
			$24, 'golden-exact-plan-proof', $25
		)`,
		planID, uuid.New(), fixture.golden.tournamentID, fixture.golden.rosterID, uuid.New(),
		fixture.sourceProjectionID, fixture.sourceProjectionRevision, sourcePreviousRevisionID,
		fixture.sourceArtifactID, fixture.sourceArtifactDigest,
		uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(),
		goldenAuthorityDigest(66), goldenAuthorityDigest(67), goldenAuthorityDigest(68),
		goldenAuthorityDigest(69), goldenAuthorityDigest(70), goldenAuthorityDigest(71),
		goldenAuthorityDigest(72), fixture.golden.createdAt.Add(time.Minute),
	)
	require.NoError(t, err)
	return planID
}

func correctionStageMutation(
	t *testing.T,
	authority tournamentadmin.CorrectionWorkflowAuthority,
	seriesID uuid.UUID,
	gameID uuid.UUID,
) tournamentadmin.CorrectionMutation {
	t.Helper()
	winnerID := authority.Core.Series.SecondParticipantID
	return correctionStageMutationWithWinner(t, authority, seriesID, gameID, winnerID)
}

func correctionStageMutationWithWinner(
	t *testing.T,
	authority tournamentadmin.CorrectionWorkflowAuthority,
	seriesID uuid.UUID,
	gameID uuid.UUID,
	winnerID uuid.UUID,
) tournamentadmin.CorrectionMutation {
	t.Helper()
	return correctionStageMutationWithOutcome(
		t, authority, seriesID, gameID, winnerID, domain.GameResultReasonOperatorForfeit,
	)
}

func correctionStageMutationWithOutcome(
	t *testing.T,
	authority tournamentadmin.CorrectionWorkflowAuthority,
	seriesID uuid.UUID,
	gameID uuid.UUID,
	winnerID uuid.UUID,
	reason domain.GameResultReason,
) tournamentadmin.CorrectionMutation {
	t.Helper()
	commandID := uuid.New()
	requestedAt := time.Now().UTC().Truncate(time.Microsecond)
	expected, err := correctionusecase.NewExpectation(authority.Core, authority.Core.GameResult.SourceProjection.ID())
	require.NoError(t, err)
	projectionIntents := correctionStageProjectionIntents(t, authority, requestedAt)
	fields := make([]correctionusecase.Field, 0, 3)
	adminFields := make([]string, 0, 3)
	if authority.Core.GameResult.Outcome.WinnerID == nil || *authority.Core.GameResult.Outcome.WinnerID != winnerID {
		fields = append(fields, correctionusecase.FieldWinner)
		adminFields = append(adminFields, "winner")
	}
	if authority.Core.GameResult.Outcome.GameReason != reason {
		fields = append(fields, correctionusecase.FieldResultReason)
		adminFields = append(adminFields, "result_reason")
	}
	if authority.Core.CurrentSolve.SolvedAt != nil || authority.Core.CurrentSolve.SubmissionID != nil ||
		authority.Core.CurrentSolve.EvidenceDigest != ([sha256.Size]byte{}) {
		fields = append(fields, correctionusecase.FieldSolveMetadata)
		adminFields = append(adminFields, "solve_metadata")
	}
	coreCommand := correctionusecase.Command{
		TournamentID: authority.Stage.TournamentID, SeriesID: seriesID, GameID: gameID,
		CommandID: commandID, CascadeCommandID: uuid.New(), OperatorID: uuid.New(),
		Confirmed: true, Reason: correctionusecase.ReasonOperatorRuling,
		Explanation: "Verified referee ruling.", RequestedAt: requestedAt, Expected: expected,
		Patch: correctionusecase.Patch{
			State: domain.GameStateCompleted, Reason: reason,
			WinnerID: &winnerID,
		},
		Fields:                     fields,
		NextResultRevisionID:       domain.OfficialResultRevisionID(uuid.New()),
		NextScoreRevisionID:        domain.SeriesScoreRevisionID(uuid.New()),
		NextSeriesResultRevisionID: domain.OfficialResultRevisionID(uuid.New()),
		NextReadinessRevisionID:    uuid.New(),
		ProjectionIntents:          projectionIntents,
	}
	for _, reservation := range authority.Core.Reservations {
		coreCommand.UnlockIntents = append(coreCommand.UnlockIntents, correctionusecase.NewUnlockIntent(reservation))
	}
	plan, err := correctionusecase.BuildPlan(coreCommand, authority.Core)
	require.NoError(t, err)
	stage, err := correctionusecase.PlanServerOwnedStageRollback(plan, authority.Stage, requestedAt)
	require.NoError(t, err)
	adminCommand := tournamentadmin.CorrectionCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: coreCommand.OperatorID},
			TournamentID: coreCommand.TournamentID, CommandID: commandID,
		},
		SeriesID: seriesID, GameID: gameID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Confirmed: true, Reason: string(coreCommand.Reason), Explanation: coreCommand.Explanation,
		Fields: adminFields,
		Patch: tournamentadmin.CorrectionPatch{
			State: coreCommand.Patch.State, Reason: coreCommand.Patch.Reason, WinnerID: &winnerID,
		},
	}
	evidence := tournamentadmin.CorrectionEvidence{
		CommandID: commandID, TournamentID: coreCommand.TournamentID,
		SeriesID: seriesID, GameID: gameID, OperatorID: coreCommand.OperatorID,
		Reason: adminCommand.Reason, Fields: append([]string(nil), adminCommand.Fields...),
		RequestedAt: requestedAt, ValidationDigest: plan.Audit().ValidationDigest,
		Supersessions: make([]tournamentadmin.ProjectionSupersessionView, 0, len(plan.Supersessions())),
		UnlockIntents: []tournamentadmin.CorrectionUnlockIntent{},
	}
	for _, item := range plan.Supersessions() {
		evidence.Supersessions = append(evidence.Supersessions, tournamentadmin.ProjectionSupersessionView{
			ArtifactKind: string(item.Artifact.Kind), ArtifactID: item.Artifact.EntityID,
			PreviousRevisionID: item.PreviousRevisionID.UUID(), SuccessorRevisionID: item.SuccessorRevisionID.UUID(),
			PreviousDecisionID: item.PreviousDecisionID, ReplacementDecisionID: item.ReplacementDecisionID,
		})
	}
	return tournamentadmin.CorrectionMutation{
		Command: adminCommand, Authority: authority,
		RequestDigest: sha256.Sum256([]byte("correction stage request")),
		Plan:          plan, Stage: stage, Evidence: evidence,
	}
}

func correctionStageProjectionIntents(
	t *testing.T,
	authority tournamentadmin.CorrectionWorkflowAuthority,
	requestedAt time.Time,
) []correctionusecase.ProjectionIntent {
	t.Helper()
	snapshot := authority.Core.DAG.Snapshot()
	byID := make(map[domain.DerivedRevisionID]domain.DerivedRevision, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		byID[projection.Revision().ID()] = projection.Revision()
	}
	target := authority.Core.GameResult.SourceProjection.ID()
	affected := map[domain.DerivedRevisionID]struct{}{target: {}}
	for changed := true; changed; {
		changed = false
		for _, dependency := range snapshot.Dependencies {
			if _, found := affected[dependency.SourceRevisionID]; !found {
				continue
			}
			if _, found := affected[dependency.DerivedRevisionID]; found {
				continue
			}
			affected[dependency.DerivedRevisionID] = struct{}{}
			changed = true
		}
	}
	current := make(map[domain.ArtifactRef]domain.DerivedRevision)
	for id := range affected {
		revision, found := byID[id]
		require.True(t, found)
		if previous, exists := current[revision.Artifact()]; !exists || revision.RevisionNo() > previous.RevisionNo() {
			current[revision.Artifact()] = revision
		}
	}
	intents := make([]correctionusecase.ProjectionIntent, 0, len(current))
	for _, revision := range current {
		require.False(t, requestedAt.Before(revision.CreatedAt()))
		intents = append(intents, correctionusecase.NewProjectionIntent(
			revision, domain.DerivedRevisionID(uuid.New()), uuid.New(),
			[]byte(`{"correction":"stage persistence test"}`),
		))
	}
	return intents
}
