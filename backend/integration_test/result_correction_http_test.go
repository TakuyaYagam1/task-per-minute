//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	admincorrectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/correction"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/application"
	admincorrection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	tournamentadmininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
)

type correctionHTTPState struct {
	ResultRevision     uuid.UUID
	ProjectionID       uuid.UUID
	ProjectionRevision int64
	TournamentRevision int64
	CorrectionCommits  int64
	ReservedTasks      int64
	OpenReadyWindows   int64
	OpenReadyWaves     int64
}

// TestRESTCorrectionPreflightCommitCAS exercises the production REST adapter
// and PostgreSQL correction workflow as one compare-and-set sequence.
func TestRESTCorrectionPreflightCommitCAS(t *testing.T) {
	ctx := context.Background()
	fixture, progressionCommand := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, progressionCommand)
	require.NoError(t, err)
	openCorrectionReadyWave(ctx, t, fixture)
	seedCorrectionHTTPReservation(ctx, t, fixture)

	seriesID := fixture.binding[0].SeriesID
	var gameID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.id
		FROM game_attempts AS attempt
		WHERE attempt.series_id = $1
		ORDER BY attempt.attempt_number DESC
		LIMIT 1`, seriesID).Scan(&gameID))

	authority := correctionHTTPAuthority(ctx, t, fixture, seriesID, gameID)
	sourceRevision := authority.Core.GameResult.ID.UUID()
	projectionID := authority.ProjectionRevisionID
	projectionRevision := authority.ProjectionRevision
	rest := newCorrectionRESTFixture(t, fixture.tx)
	adminToken := rest.adminAccessToken(t)
	correctionPath := fmt.Sprintf(
		"/api/v1/admin/tournaments/%s/series/%s/games/%s/corrections",
		fixture.tournamentID, seriesID, gameID,
	)
	preflightPath := correctionPath + "/preflight"

	draftBody := correctionHTTPDraftBody(t, authority, sourceRevision)
	before := loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID)
	require.Equal(t, projectionID, before.ProjectionID)
	require.Equal(t, projectionRevision, before.ProjectionRevision)
	require.Positive(t, before.OpenReadyWindows)
	require.Positive(t, before.OpenReadyWaves)
	require.Positive(t, before.ReservedTasks)

	staleDraft := correctionHTTPDraftBody(t, authority, uuid.New())
	staleReq, staleResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, preflightPath, staleDraft, adminToken, uuid.New(),
	)
	require.Equal(t, http.StatusConflict, staleResp.Code, staleResp.Body.String())
	rest.validateResponse(t, staleReq, staleResp)
	staleConflict := decodeJSON[api.CorrectionConflictProblem](t, staleResp)
	require.Equal(t, api.StaleResult, staleConflict.Code)
	require.Equal(t, before.ProjectionRevision, staleConflict.CurrentRevision)
	require.Equal(t, before.ProjectionRevision, staleConflict.ExpectedRevision)
	require.Equal(t, before, loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID))

	preparedKey := uuid.New()
	preparedReq, preparedResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, preflightPath, draftBody, adminToken, preparedKey,
	)
	require.Equal(t, http.StatusOK, preparedResp.Code, preparedResp.Body.String())
	rest.validateResponse(t, preparedReq, preparedResp)
	prepared := decodeJSON[api.OperatorCorrectionRequest](t, preparedResp)
	require.Equal(t, sourceRevision, prepared.SourceResultRevision)
	require.Equal(t, projectionRevision, prepared.ExpectedProjectionRevision)
	require.NotEmpty(t, prepared.ProjectionIntents)
	require.NotEmpty(t, prepared.UnlockIntents)
	for _, intent := range prepared.ProjectionIntents {
		require.NotEqual(t, uuid.Nil, intent.DecisionId)
		require.NotEqual(t, uuid.Nil, intent.NextRevisionId)
		require.NotEmpty(t, intent.PayloadDigest)
		require.NotEmpty(t, intent.ExpectedRevision.PayloadDigest)
	}
	for _, intent := range prepared.UnlockIntents {
		require.NotEqual(t, uuid.Nil, intent.ReservationId)
		require.NotEqual(t, uuid.Nil, intent.SourceRevisionId)
		require.NotEmpty(t, intent.EvidenceDigest)
		require.NotEmpty(t, intent.BindingDigest)
	}

	truncatedProjection := prepared
	truncatedProjection.ProjectionIntents = append(
		[]api.CorrectionProjectionIntent(nil), prepared.ProjectionIntents[:len(prepared.ProjectionIntents)-1]...,
	)
	truncatedProjectionBody, err := json.Marshal(truncatedProjection)
	require.NoError(t, err)
	truncatedProjectionReq, truncatedProjectionResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, correctionPath, string(truncatedProjectionBody), adminToken, preparedKey,
	)
	require.Equal(t, http.StatusConflict, truncatedProjectionResp.Code, truncatedProjectionResp.Body.String())
	rest.validateResponse(t, truncatedProjectionReq, truncatedProjectionResp)
	projectionConflict := decodeJSON[api.CorrectionConflictProblem](t, truncatedProjectionResp)
	require.Equal(t, api.IncompleteProjection, projectionConflict.Code)
	require.Equal(t, before.ProjectionRevision, projectionConflict.CurrentRevision)
	require.Equal(t, before.ProjectionRevision, projectionConflict.ExpectedRevision)
	require.Equal(t, before, loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID))

	unlockKey := uuid.New()
	unlockReq, unlockResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, preflightPath, draftBody, adminToken, unlockKey,
	)
	require.Equal(t, http.StatusOK, unlockResp.Code, unlockResp.Body.String())
	rest.validateResponse(t, unlockReq, unlockResp)
	preparedUnlock := decodeJSON[api.OperatorCorrectionRequest](t, unlockResp)
	preparedUnlock.UnlockIntents = append(
		[]api.CorrectionUnlockIntent(nil), preparedUnlock.UnlockIntents[:len(preparedUnlock.UnlockIntents)-1]...,
	)
	truncatedUnlockBody, err := json.Marshal(preparedUnlock)
	require.NoError(t, err)
	truncatedUnlockReq, truncatedUnlockResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, correctionPath, string(truncatedUnlockBody), adminToken, unlockKey,
	)
	require.Equal(t, http.StatusConflict, truncatedUnlockResp.Code, truncatedUnlockResp.Body.String())
	rest.validateResponse(t, truncatedUnlockReq, truncatedUnlockResp)
	unlockConflict := decodeJSON[api.CorrectionConflictProblem](t, truncatedUnlockResp)
	require.Equal(t, api.IncompleteUnlock, unlockConflict.Code)
	require.Equal(t, before.ProjectionRevision, unlockConflict.CurrentRevision)
	require.Equal(t, before.ProjectionRevision, unlockConflict.ExpectedRevision)
	require.Equal(t, before, loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID))

	successKey := uuid.New()
	successReq, successResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, preflightPath, draftBody, adminToken, successKey,
	)
	require.Equal(t, http.StatusOK, successResp.Code, successResp.Body.String())
	rest.validateResponse(t, successReq, successResp)
	preparedForCommit := append([]byte(nil), successResp.Body.Bytes()...)
	commitReq, commitResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, correctionPath, string(preparedForCommit), adminToken, successKey,
	)
	require.Equal(t, http.StatusOK, commitResp.Code, commitResp.Body.String())
	rest.validateResponse(t, commitReq, commitResp)
	evidence := decodeJSON[api.CorrectionEvidence](t, commitResp)
	require.Equal(t, successKey, evidence.CommandId)
	require.Equal(t, fixture.tournamentID, evidence.TournamentId)
	require.Equal(t, seriesID, evidence.SeriesId)
	require.Equal(t, gameID, evidence.GameId)
	require.NotEmpty(t, evidence.Supersessions)
	require.NotEmpty(t, evidence.UnlockIntents)

	after := loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID)
	require.NotEqual(t, before.ResultRevision, after.ResultRevision)
	require.NotEqual(t, before.ProjectionID, after.ProjectionID)
	require.Equal(t, before.ProjectionRevision+1, after.ProjectionRevision)
	require.Equal(t, before.TournamentRevision+1, after.TournamentRevision)
	require.Equal(t, before.CorrectionCommits+1, after.CorrectionCommits)
	require.Zero(t, after.OpenReadyWindows)
	require.Zero(t, after.OpenReadyWaves)

	correctedAuthority := correctionHTTPAuthority(ctx, t, fixture, seriesID, gameID)
	correctedDraft := correctionHTTPDraftBody(t, correctedAuthority, correctedAuthority.Core.GameResult.ID.UUID())
	seedCorrectionHTTPWaveCutoff(ctx, t, fixture, correctedAuthority.Core.GameResult.ID.UUID())
	cutoffBefore := loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID)
	cutoffReq, cutoffResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, preflightPath, correctedDraft, adminToken, uuid.New(),
	)
	require.Equal(t, http.StatusConflict, cutoffResp.Code, cutoffResp.Body.String())
	rest.validateResponse(t, cutoffReq, cutoffResp)
	cutoffConflict := decodeJSON[api.CorrectionConflictProblem](t, cutoffResp)
	require.Equal(t, api.CutoffWaveStarted, cutoffConflict.Code)
	require.Equal(t, cutoffBefore, loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID))

	seedCorrectionHTTPTerminalTournament(ctx, t, fixture.tournamentID)
	terminalBefore := loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID)
	terminalReq, terminalResp := doCorrectionHTTPJSON(
		t, rest, http.MethodPost, preflightPath, correctedDraft, adminToken, uuid.New(),
	)
	require.Equal(t, http.StatusConflict, terminalResp.Code, terminalResp.Body.String())
	rest.validateResponse(t, terminalReq, terminalResp)
	terminalConflict := decodeJSON[api.CorrectionConflictProblem](t, terminalResp)
	require.Equal(t, api.TournamentTerminal, terminalConflict.Code)
	require.Equal(t, terminalBefore, loadCorrectionHTTPState(ctx, t, fixture.tournamentID, fixture.rosterID, gameID))
}

func newCorrectionRESTFixture(t *testing.T, tx *postgres.TxManager) *restFixture {
	t.Helper()
	database := newDatabaseFixture()
	clock := realIntegrationClock()
	redis := sharedRedis(t)
	auth := authusecase.NewUseCase(authusecase.Config{
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 7 * 24 * time.Hour,
	}, clock, redisadapter.NewRevocationRedis(
		redis.client,
		"integration:correction-http:"+uniq("auth")+":",
	), authadapter.NewJWTCodec(authadapter.JWTConfig{
		Secret: []byte("01234567890123456789012345678901"),
		Now:    clock.Now,
	}), authadapter.NewPasswordVerifier([]byte(restAdminPassword)))

	correction := admincorrection.NewCorrectionWorkflow(
		admincorrection.CorrectionWorkflowDependencies{
			Transactions: tx,
			Repository:   admincorrectionrepo.NewTournamentAdminCorrectionPostgres(tx),
		},
	)
	admin := tournamentadmininbound.NewInboundAdapter(
		tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{Correction: correction}),
	)
	server := restv1.New(restv1.Dependencies{
		AdminAuth:                         auth,
		TournamentAdmin:                   admin,
		OperatorTournamentMutationLimiter: tournamentFlowLimiter{},
	})
	handler := middleware.NoStoreSensitiveResponses()(
		restv1.NewHandler(server, restv1.HandlerOptions{AdminAuth: auth}),
	)
	return &restFixture{
		databaseFixture: database,
		handler:         handler,
		auth:            auth,
		validator:       newOpenAPIResponseValidator(t),
	}
}

func correctionHTTPAuthority(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	seriesID, gameID uuid.UUID,
) admincorrection.CorrectionWorkflowAuthority {
	t.Helper()
	repository := admincorrectionrepo.NewTournamentAdminCorrectionPostgres(fixture.tx)
	var authority admincorrection.CorrectionWorkflowAuthority
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		authority, err = repository.LockCorrectionAuthority(txCtx, fixture.tournamentID, seriesID, gameID)
		return err
	}))
	return authority
}

func seedCorrectionHTTPReservation(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) {
	t.Helper()
	var series waverepo.WaveSeriesInput
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT membership.series_id, series.first_participant_id, series.second_participant_id
		FROM waves AS wave
		INNER JOIN wave_series AS membership ON membership.wave_id = wave.id
		INNER JOIN series ON series.id = membership.series_id
		WHERE wave.tournament_id = $1
			AND wave.roster_id = $2
			AND wave.state = 'ready_window_open'
		ORDER BY wave.created_at DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID).Scan(
		&series.ID, &series.FirstParticipantID, &series.SecondParticipantID,
	))
	series.Format = domain.SeriesFormatBO1
	series.InitialScoreRevisionID = domain.SeriesScoreRevisionID(uuid.New())
	at := time.Now().UTC().Truncate(time.Microsecond)
	draft := createRoundProofDraft(
		ctx, t, fixture.tournamentID, fixture.rosterID, fixture.normalPoolRevisionID, series, at,
	)
	planID := createRoundProofAssignmentPlan(
		ctx, t, fixture.tournamentID, fixture.rosterID, fixture.normalPoolRevisionID, draft, at,
	)
	branchID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO assignment_branches (
			id, plan_id, draft_id, draft_revision_id, branch_key, category_sequence, created_at
		)
		VALUES ($1, $2, $3, $4, 'correction-http', '["web"]'::jsonb, $5)`,
		branchID, planID, draft.draftID, draft.initialRevisionID, at)
	require.NoError(t, err)
	createRoundProofReservations(ctx, t, planID, branchID, fixture.normalPoolRevisionID, at)
}

func seedCorrectionHTTPWaveCutoff(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	sourceRevisionID uuid.UUID,
) {
	t.Helper()
	openCorrectionReadyWave(ctx, t, fixture)
	var waveID uuid.UUID
	var sourceCreatedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id
		FROM waves
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'ready_window_open'
		ORDER BY created_at DESC
		LIMIT 1`, fixture.tournamentID, fixture.rosterID).Scan(&waveID))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT created_at
		FROM official_result_revisions
		WHERE id = $1`, sourceRevisionID).Scan(&sourceCreatedAt))

	repository := waverepo.NewWavePostgres(fixture.tx)
	wave, err := repository.Get(ctx, fixture.tournamentID, waveID)
	require.NoError(t, err)
	require.NotNil(t, wave.Wave.ReadyWindow)
	readyAt := wave.Wave.ReadyWindow.OpenedAt.UTC().Add(time.Microsecond)
	for _, participantID := range fixture.participants {
		wave, _, err = repository.MarkReady(
			ctx,
			fixture.tournamentID,
			waveID,
			wave.Wave.ReadyWindow.ID,
			participantID,
			wave.Revision,
			wave.ReadinessRevisions[participantID],
			readyAt,
		)
		require.NoError(t, err)
		require.NotNil(t, wave)
	}
	require.Equal(t, domain.WaveStateReady, wave.Wave.State)

	startedAt := readyAt.Add(time.Microsecond)
	if !startedAt.After(sourceCreatedAt) {
		startedAt = sourceCreatedAt.UTC().Add(time.Microsecond)
	}
	require.True(t, startedAt.Before(wave.Wave.ReadyWindow.Deadline))
	_, changed, err := repository.Start(
		ctx,
		fixture.tournamentID,
		waveID,
		wave.Wave.ReadyWindow.ID,
		wave.Revision,
		startedAt,
	)
	require.NoError(t, err)
	require.True(t, changed)
}

func seedCorrectionHTTPTerminalTournament(ctx context.Context, t *testing.T, tournamentID uuid.UUID) {
	t.Helper()
	finishedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'completed', finished_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, tournamentID, finishedAt)
	require.NoError(t, err)
}

func correctionHTTPDraftBody(
	t *testing.T,
	authority admincorrection.CorrectionWorkflowAuthority,
	sourceRevision uuid.UUID,
) string {
	t.Helper()
	currentWinner := authority.Core.GameResult.Outcome.WinnerID
	winner := authority.Core.Series.SecondParticipantID
	if currentWinner != nil && *currentWinner == winner {
		winner = authority.Core.Series.FirstParticipantID
	}
	patchReason := api.GameResultReasonOperatorForfeit
	if string(authority.Core.GameResult.Outcome.GameReason) == string(patchReason) {
		patchReason = api.GameResultReasonSurrender
	}
	fields := make([]api.CorrectionField, 0, 3)
	if currentWinner == nil || *currentWinner != winner {
		fields = append(fields, api.Winner)
	}
	if string(authority.Core.GameResult.Outcome.GameReason) != string(patchReason) {
		fields = append(fields, api.ResultReason)
	}
	currentSolve := authority.Core.CurrentSolve
	if currentSolve.SolvedAt != nil || currentSolve.SubmissionID != nil || currentSolve.EvidenceDigest != [32]byte{} {
		fields = append(fields, api.SolveMetadata)
	}

	winnerID := openapi_types.UUID(winner)
	body := api.OperatorCorrectionDraftRequest{
		Confirmed:                  true,
		ExpectedProjectionRevision: authority.ProjectionRevision,
		Explanation:                "Verified referee ruling.",
		Fields:                     fields,
		Patch: api.CorrectionPatch{
			State:    api.GameState(authority.Core.GameResult.Outcome.GameState),
			Reason:   patchReason,
			WinnerId: &winnerID,
			SolveMetadata: api.CorrectionSolveMetadata{
				EvidenceDigest: strings.Repeat("0", 64),
			},
		},
		Reason:               api.OperatorRuling,
		SourceResultRevision: openapi_types.UUID(sourceRevision),
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

func doCorrectionHTTPJSON(
	t *testing.T,
	fixture *restFixture,
	method, path, body, adminToken string,
	commandID uuid.UUID,
) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", commandID.String())
	addAdminSession(t, req, adminToken)
	resp := httptest.NewRecorder()
	fixture.handler.ServeHTTP(resp, req)
	return req, resp
}

func loadCorrectionHTTPState(
	ctx context.Context,
	t *testing.T,
	tournamentID, rosterID, gameID uuid.UUID,
) correctionHTTPState {
	t.Helper()
	var state correctionHTTPState
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT result_revision_id FROM game_attempts WHERE id = $3),
			(SELECT id FROM projection_revisions
			 WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'
			 ORDER BY revision_number DESC, id DESC LIMIT 1),
			(SELECT revision_number FROM projection_revisions
			 WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'
			 ORDER BY revision_number DESC, id DESC LIMIT 1),
			(SELECT revision FROM tournaments WHERE id = $1),
			(SELECT COUNT(*) FROM result_correction_commits WHERE tournament_id = $1),
			(SELECT COUNT(*)
			 FROM task_version_reservations AS reservation
			 INNER JOIN assignment_plans AS plan ON plan.id = reservation.plan_id
			 WHERE plan.tournament_id = $1 AND plan.roster_id = $2
				 AND reservation.state = 'reserved' AND reservation.disclosed_at IS NULL),
			(SELECT COUNT(*) FROM ready_windows AS ready_window
			 INNER JOIN waves AS wave ON wave.id = ready_window.wave_id
			 WHERE wave.tournament_id = $1 AND wave.roster_id = $2 AND ready_window.state = 'open'),
			(SELECT COUNT(*) FROM waves
			 WHERE tournament_id = $1 AND roster_id = $2 AND state IN ('ready_window_open', 'ready'))`,
		tournamentID, rosterID, gameID,
	).Scan(
		&state.ResultRevision,
		&state.ProjectionID,
		&state.ProjectionRevision,
		&state.TournamentRevision,
		&state.CorrectionCommits,
		&state.ReservedTasks,
		&state.OpenReadyWindows,
		&state.OpenReadyWaves,
	))
	return state
}
