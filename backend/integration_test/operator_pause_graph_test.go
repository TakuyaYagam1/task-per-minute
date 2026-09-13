//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

type operatorPauseGraphAttempt struct {
	seriesID                uuid.UUID
	seriesState             string
	attemptID               uuid.UUID
	attemptState            string
	startedAt               time.Time
	submissionEventSequence int64
}

type operatorPauseGraphState struct {
	tournamentState string
	waveState       string
	attempts        []operatorPauseGraphAttempt
}

type operatorPauseGraphEvidence struct {
	id                uuid.UUID
	scopeKind         string
	scopeID           uuid.UUID
	parentID          pgtype.UUID
	state             string
	depth             int
	startedAt         time.Time
	resolvedAt        pgtype.Timestamptz
	gameAttemptID     pgtype.UUID
	originalDeadline  pgtype.Timestamptz
	frozenAt          pgtype.Timestamptz
	frozenRemainingMS pgtype.Int8
	resumedAt         pgtype.Timestamptz
	resumedDeadline   pgtype.Timestamptz
}

func TestOperatorPauseResumeActiveWaveThroughProductionHandlers(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	catalog := prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(
		t, fixture, adminToken, content.ContentRevision, "operator_pause_graph",
	)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)

	round := configureProductionSwissPairingsThroughREST(
		t,
		fixture,
		adminToken,
		created.Id,
		tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).NextCursor.ProjectionRevision,
		1,
	)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave := findProductionSwissWave(t, snapshot, round)
	wave = controlProductionWaveThroughREST(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		snapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionOpenReadyWindow,
	)
	recordOperatorPauseGraphReadiness(t, fixture, created.Id, wave, roster, players)

	snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave = controlProductionWaveThroughREST(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		snapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionStart,
	)
	require.Equal(t, api.WaveStateActive, wave.State)
	require.NotNil(t, wave.StartedAt)

	before := loadOperatorPauseGraphState(ctx, t, created.Id, wave.Id)
	assertOperatorPauseGraphStates(t, before, "swiss", "active", "active", "active")
	require.Len(t, before.attempts, 2, "four participants must produce two parallel active Games")
	seriesIDs := operatorPauseGraphSeriesIDs(before)

	playersByParticipant := productionPlayersByParticipant(
		t,
		roster,
		operatorPauseGraphPlayersByID(players),
	)
	selectedMember := wave.Members[0]
	selectedPlayer, ok := playersByParticipant[selectedMember.ParticipantId]
	require.True(t, ok)
	participantBefore := participantSnapshotThroughREST(t, fixture, created.Id, selectedPlayer)
	require.NotNil(t, participantBefore.Assignment)
	require.NotNil(t, participantBefore.Series)
	selectedAttemptID := participantBefore.Assignment.AttemptId
	selectedSequence := operatorPauseGraphAttemptByID(t, before, selectedAttemptID).submissionEventSequence

	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	pauseAt := wave.StartedAt.Add(30 * time.Second)
	runtime.clock.FreezeAt(pauseAt)
	pauseSnapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	paused := raceOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		pauseSnapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionPause,
	)
	require.Equal(t, api.WaveStatePaused, paused.State)
	require.NotNil(t, paused.PausedAt)

	pausedState := loadOperatorPauseGraphState(ctx, t, created.Id, wave.Id)
	assertOperatorPauseGraphStates(t, pausedState, "technical_pause", "paused", "technical_pause", "paused")
	pausedEvidence := loadOperatorPauseGraphEvidence(ctx, t, created.Id)
	assertOperatorPauseGraphTopology(t, pausedEvidence, wave.Id, seriesIDs, before.attempts, false)
	assertOperatorPauseGraphSubmissionRejected(
		t,
		fixture,
		created.Id,
		selectedPlayer,
		participantBefore.Series.Id,
		selectedAttemptID,
		catalog.flags[participantBefore.Assignment.ActiveSnapshot.TaskId],
	)
	pausedAfterSubmission := loadOperatorPauseGraphState(ctx, t, created.Id, wave.Id)
	require.Equal(
		t,
		selectedSequence,
		operatorPauseGraphAttemptByID(t, pausedAfterSubmission, selectedAttemptID).submissionEventSequence,
		"rejected submission must not append durable submission evidence",
	)

	// A new production composition must rebuild the same paused graph from PostgreSQL.
	freshFixture := newTournamentFlowRESTFixture(t)
	freshAdminToken := freshFixture.adminAccessToken(t)
	reconstructed := tournamentAdminSnapshotThroughREST(t, freshFixture, freshAdminToken, created.Id)
	require.Equal(t, api.TournamentStateTechnicalPause, reconstructed.Tournament.State)
	reconstructedWave := findProductionWaveByID(t, reconstructed, wave.Id)
	require.Equal(t, api.WaveStatePaused, reconstructedWave.State)
	assertOperatorPauseGraphSeriesView(t, reconstructed, seriesIDs, api.SeriesStateTechnicalPause)
	require.Equal(t, pausedEvidence, loadOperatorPauseGraphEvidence(ctx, t, created.Id))

	resumeAt := pauseAt.Add(20 * time.Second)
	runtime.clock.FreezeAt(resumeAt)
	resumeCommandID := uuid.New()
	resumed := applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		reconstructed.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		resumeCommandID,
		http.StatusOK,
	)
	require.Equal(t, api.WaveStateActive, resumed.State)

	resumedState := loadOperatorPauseGraphState(ctx, t, created.Id, wave.Id)
	assertOperatorPauseGraphStates(t, resumedState, "swiss", "active", "active", "active")
	resumedEvidence := loadOperatorPauseGraphEvidence(ctx, t, created.Id)
	assertOperatorPauseGraphTopology(t, resumedEvidence, wave.Id, seriesIDs, before.attempts, true)

	// An identical command is receipt replay, while a new command with the old
	// projection revision is stale. Neither path may extend restored clocks.
	replayed := applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		reconstructed.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		resumeCommandID,
		http.StatusOK,
	)
	require.Equal(t, resumed, replayed)
	applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		reconstructed.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		uuid.New(),
		http.StatusConflict,
	)
	require.Equal(t, resumedEvidence, loadOperatorPauseGraphEvidence(ctx, t, created.Id))
	require.Equal(t, resumedState, loadOperatorPauseGraphState(ctx, t, created.Id, wave.Id))
}

func TestOperatorPauseResumeReadyWindowThroughProductionHandlers(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(
		t, fixture, adminToken, content.ContentRevision, "operator_pause_ready_window",
	)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	round := configureProductionSwissPairingsThroughREST(
		t,
		fixture,
		adminToken,
		created.Id,
		tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).NextCursor.ProjectionRevision,
		1,
	)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave := findProductionSwissWave(t, snapshot, round)
	wave = controlProductionWaveThroughREST(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		snapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionOpenReadyWindow,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)

	var windowID uuid.UUID
	var originalDeadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, deadline
		FROM ready_windows
		WHERE wave_id = $1`, wave.Id).Scan(&windowID, &originalDeadline))

	pauseSnapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	paused := applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		pauseSnapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionPause,
		uuid.New(),
		http.StatusOK,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, paused.State)
	technicalPauseSnapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	require.Equal(t, api.TournamentStateTechnicalPause, technicalPauseSnapshot.Tournament.State)

	var pauseID uuid.UUID
	var frozenAt time.Time
	var frozenRemainingMS int64
	var readyDeadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT clock.pause_id, clock.frozen_at,
			(EXTRACT(EPOCH FROM clock.frozen_remaining) * 1000)::bigint,
			ready_window.deadline
		FROM ready_window_pause_clocks AS clock
		JOIN ready_windows AS ready_window ON ready_window.id = clock.ready_window_id
		WHERE clock.wave_id = $1`, wave.Id).Scan(
		&pauseID, &frozenAt, &frozenRemainingMS, &readyDeadline,
	))
	require.Equal(t, originalDeadline, readyDeadline)
	require.InDelta(t, originalDeadline.Sub(frozenAt).Milliseconds(), frozenRemainingMS, 1)

	pausedSnapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	resumeCommandID := uuid.New()
	resumed := applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		pausedSnapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		resumeCommandID,
		http.StatusOK,
	)
	require.Equal(t, api.WaveStateReadyWindowOpen, resumed.State)

	var resumedAt, resumedDeadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT clock.resumed_at, clock.resumed_deadline, ready_window.deadline
		FROM ready_window_pause_clocks AS clock
		JOIN ready_windows AS ready_window ON ready_window.id = clock.ready_window_id
		WHERE clock.pause_id = $1 AND clock.ready_window_id = $2`, pauseID, windowID).Scan(
		&resumedAt, &resumedDeadline, &readyDeadline,
	))
	require.Equal(t, resumedDeadline, readyDeadline)
	require.InDelta(t, frozenRemainingMS, resumedDeadline.Sub(resumedAt).Milliseconds(), 1)
	require.Equal(t, originalDeadline.Add(resumedAt.Sub(frozenAt)), resumedDeadline)

	replayed := applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		pausedSnapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		resumeCommandID,
		http.StatusOK,
	)
	require.Equal(t, resumed, replayed)
	applyOperatorPauseGraphAction(
		t,
		fixture,
		adminToken,
		created.Id,
		wave.Id,
		pausedSnapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		uuid.New(),
		http.StatusConflict,
	)
	var unchangedDeadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT deadline FROM ready_windows WHERE id = $1`, windowID).Scan(&unchangedDeadline))
	require.Equal(t, resumedDeadline, unchangedDeadline)
}

func TestOperatorPauseResumeActiveDraftThroughProductionHandlers(t *testing.T) {
	ctx := context.Background()
	flow := newSwissCategoryFlow(t, "operator-pause-active-draft")
	before := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	round := configureSwissCategoryPairingsThroughREST(
		t,
		flow.fixture,
		flow.adminToken,
		flow.tournamentID,
		before.NextCursor.ProjectionRevision,
		1,
		api.CategoryModeDraft,
		[]api.Category{api.CategoryCrypto, api.CategoryReverse, api.CategoryWeb},
		uuid.New(),
	)
	wave := findProductionSwissWave(
		t,
		tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID),
		round,
	)
	activeDrafts := swissActiveDrafts(t, flow)
	require.Len(t, activeDrafts, 2)
	for _, completed := range activeDrafts {
		completeProductionFinalDraftThroughREST(t, flow.fixture, flow.tournamentID, completed, flow.playersByParticipant)
		break
	}
	activeDrafts = swissActiveDrafts(t, flow)
	require.Len(t, activeDrafts, 1)
	var draft api.Draft
	for _, candidate := range activeDrafts {
		draft = candidate
		break
	}
	require.NotNil(t, draft.TurnDeadline)

	var revisionCountBefore int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM draft_revisions WHERE draft_id = $1`, draft.Id).Scan(&revisionCountBefore))
	pauseSnapshot := tournamentAdminSnapshotThroughREST(t, flow.fixture, flow.adminToken, flow.tournamentID)
	applyOperatorPauseGraphAction(
		t,
		flow.fixture,
		flow.adminToken,
		flow.tournamentID,
		wave.Id,
		pauseSnapshot.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionPause,
		uuid.New(),
		http.StatusOK,
	)

	var pausedState string
	var pausedDeadline pgtype.Timestamptz
	var pausedRemaining int32
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, absolute_deadline, paused_remaining_ms
		FROM draft_revisions
		WHERE draft_id = $1
		ORDER BY revision DESC
		LIMIT 1`, draft.Id).Scan(&pausedState, &pausedDeadline, &pausedRemaining))
	require.Equal(t, "paused", pausedState)
	require.False(t, pausedDeadline.Valid)
	require.Positive(t, pausedRemaining)

	freshFixture := newTournamentFlowRESTFixture(t)
	freshAdminToken := freshFixture.adminAccessToken(t)
	reconstructed := tournamentAdminSnapshotThroughREST(t, freshFixture, freshAdminToken, flow.tournamentID)
	require.Equal(t, api.TournamentStateTechnicalPause, reconstructed.Tournament.State)
	resumeCommandID := uuid.New()
	applyOperatorPauseGraphAction(
		t,
		flow.fixture,
		flow.adminToken,
		flow.tournamentID,
		wave.Id,
		reconstructed.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		resumeCommandID,
		http.StatusOK,
	)

	var resumedState string
	var resumedDeadline time.Time
	var revisionCountAfter int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, absolute_deadline
		FROM draft_revisions
		WHERE draft_id = $1
		ORDER BY revision DESC
		LIMIT 1`, draft.Id).Scan(&resumedState, &resumedDeadline))
	require.Equal(t, "active", resumedState)
	require.Greater(t, resumedDeadline.UnixNano(), draft.TurnDeadline.UnixNano())
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM draft_revisions WHERE draft_id = $1`, draft.Id).Scan(&revisionCountAfter))
	require.Equal(t, revisionCountBefore+2, revisionCountAfter)

	applyOperatorPauseGraphAction(
		t,
		flow.fixture,
		flow.adminToken,
		flow.tournamentID,
		wave.Id,
		reconstructed.NextCursor.ProjectionRevision,
		api.WaveControlRequestActionResume,
		resumeCommandID,
		http.StatusOK,
	)
	var unchangedRevisionCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM draft_revisions WHERE draft_id = $1`, draft.Id).Scan(&unchangedRevisionCount))
	require.Equal(t, revisionCountAfter, unchangedRevisionCount)
}

func recordOperatorPauseGraphReadiness(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	wave api.Wave,
	roster api.Roster,
	players []tournamentFlowPlayer,
) {
	t.Helper()
	playersByParticipant := productionPlayersByParticipant(
		t,
		roster,
		operatorPauseGraphPlayersByID(players),
	)
	for _, member := range wave.Members {
		player, ok := playersByParticipant[member.ParticipantId]
		require.True(t, ok)
		participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
		body, err := json.Marshal(api.ParticipantReadyRequest{
			ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
			Ready:                      true,
		})
		require.NoError(t, err)
		path := "/api/v1/tournaments/" + tournamentID.String() +
			"/participant/waves/" + wave.Id.String() + "/ready"
		req, resp := doTournamentFlowJSON(
			t,
			fixture,
			http.MethodPost,
			path,
			string(body),
			cookieSession(player.session.String()),
			uuid.New(),
			player.csrf,
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
	}
}

func operatorPauseGraphPlayersByID(players []tournamentFlowPlayer) map[uuid.UUID]tournamentFlowPlayer {
	result := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		result[player.id] = player
	}
	return result
}

func raceOperatorPauseGraphAction(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedProjectionRevision int64,
	action api.WaveControlRequestAction,
) api.Wave {
	t.Helper()
	body, err := json.Marshal(api.WaveControlRequest{
		Action:                     action,
		Confirmed:                  true,
		ExpectedProjectionRevision: expectedProjectionRevision,
	})
	require.NoError(t, err)
	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, adminToken)
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() +
		"/waves/" + waveID.String() + "/actions"

	const racers = 2
	requests := make([]*http.Request, racers)
	responses := make([]*httptest.ResponseRecorder, racers)
	for index := range racers {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.New().String())
		request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: adminToken})
		request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: csrfToken})
		request.Header.Set(middleware.CSRFHeaderName, csrfToken)
		requests[index] = request
		responses[index] = httptest.NewRecorder()
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(racers)
	for index := range racers {
		go func(index int) {
			defer wait.Done()
			<-start
			fixture.handler.ServeHTTP(responses[index], requests[index])
		}(index)
	}
	close(start)
	wait.Wait()

	statusCounts := map[int]int{}
	var result api.Wave
	for index := range racers {
		response := responses[index]
		statusCounts[response.Code]++
		fixture.validateResponse(t, requests[index], response)
		if response.Code == http.StatusOK {
			result = decodeJSON[api.Wave](t, response)
		}
	}
	require.Equal(t, 1, statusCounts[http.StatusOK])
	require.Equal(t, 1, statusCounts[http.StatusConflict])
	require.Equal(t, waveID, result.Id)
	return result
}

func applyOperatorPauseGraphAction(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedProjectionRevision int64,
	action api.WaveControlRequestAction,
	commandID uuid.UUID,
	expectedStatus int,
) api.Wave {
	t.Helper()
	body, err := json.Marshal(api.WaveControlRequest{
		Action:                     action,
		Confirmed:                  true,
		ExpectedProjectionRevision: expectedProjectionRevision,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() +
		"/waves/" + waveID.String() + "/actions"
	req, resp := doTournamentFlowJSON(
		t,
		fixture,
		http.MethodPost,
		path,
		string(body),
		adminSession(adminToken),
		commandID,
		"",
	)
	require.Equal(t, expectedStatus, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	if expectedStatus != http.StatusOK {
		return api.Wave{}
	}
	result := decodeJSON[api.Wave](t, resp)
	require.Equal(t, waveID, result.Id)
	return result
}

func assertOperatorPauseGraphSubmissionRejected(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
	seriesID uuid.UUID,
	attemptID uuid.UUID,
	flag string,
) {
	t.Helper()
	participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
	require.NotNil(t, participant.Assignment)
	require.Equal(t, attemptID, participant.Assignment.AttemptId)
	body, err := json.Marshal(api.ParticipantSubmissionRequest{
		ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
		SubmittedFlag:              &flag,
	})
	require.NoError(t, err)
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/series/" +
		seriesID.String() + "/games/" + attemptID.String() + "/submissions"
	_, resp := doTournamentFlowJSON(
		t,
		fixture,
		http.MethodPost,
		path,
		string(body),
		cookieSession(player.session.String()),
		uuid.New(),
		player.csrf,
	)
	require.Equal(t, http.StatusConflict, resp.Code, resp.Body.String())
	// A paused Game is rejected before submission evidence is written. The
	// generic conflict body is intentionally not a projection-revision problem.
	require.Equal(t, "application/problem+json", resp.Header().Get("Content-Type"))
}

func loadOperatorPauseGraphState(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
) operatorPauseGraphState {
	t.Helper()
	var result operatorPauseGraphState
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT tournament.state, wave.state
		FROM tournaments AS tournament
		INNER JOIN waves AS wave ON wave.tournament_id = tournament.id
		WHERE tournament.id = $1 AND wave.id = $2`, tournamentID, waveID).
		Scan(&result.tournamentState, &result.waveState))

	rows, err := sharedPool.Query(ctx, `
		SELECT
			series.id,
			series.state,
			attempt.id,
			attempt.state,
			attempt.started_at,
			COALESCE(attempt.submission_event_sequence, 0)
		FROM wave_series AS membership
		INNER JOIN series ON series.id = membership.series_id
		INNER JOIN game_attempts AS attempt ON attempt.series_id = series.id
		WHERE membership.wave_id = $1
		ORDER BY series.id, attempt.id`, waveID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var attempt operatorPauseGraphAttempt
		require.NoError(t, rows.Scan(
			&attempt.seriesID,
			&attempt.seriesState,
			&attempt.attemptID,
			&attempt.attemptState,
			&attempt.startedAt,
			&attempt.submissionEventSequence,
		))
		result.attempts = append(result.attempts, attempt)
	}
	require.NoError(t, rows.Err())
	return result
}

func loadOperatorPauseGraphEvidence(
	ctx context.Context,
	t testing.TB,
	tournamentID uuid.UUID,
) []operatorPauseGraphEvidence {
	t.Helper()
	rows, err := sharedPool.Query(ctx, `
		SELECT
			pause.id,
			pause.scope_kind,
			pause.scope_id,
			pause.parent_pause_id,
			pause.state,
			pause.depth,
			pause.started_at,
			pause.resolved_at,
			clock.game_attempt_id,
			clock.original_deadline,
			clock.frozen_at,
			clock.frozen_remaining_ms,
			clock.resumed_at,
			clock.resumed_deadline
		FROM pauses AS pause
		LEFT JOIN pause_clocks AS clock ON clock.pause_id = pause.id
		WHERE pause.tournament_id = $1
		ORDER BY pause.depth, pause.scope_kind, pause.scope_id`, tournamentID)
	require.NoError(t, err)
	defer rows.Close()
	var result []operatorPauseGraphEvidence
	for rows.Next() {
		var evidence operatorPauseGraphEvidence
		require.NoError(t, rows.Scan(
			&evidence.id,
			&evidence.scopeKind,
			&evidence.scopeID,
			&evidence.parentID,
			&evidence.state,
			&evidence.depth,
			&evidence.startedAt,
			&evidence.resolvedAt,
			&evidence.gameAttemptID,
			&evidence.originalDeadline,
			&evidence.frozenAt,
			&evidence.frozenRemainingMS,
			&evidence.resumedAt,
			&evidence.resumedDeadline,
		))
		result = append(result, evidence)
	}
	require.NoError(t, rows.Err())
	return result
}

func assertOperatorPauseGraphStates(
	t testing.TB,
	state operatorPauseGraphState,
	tournamentState string,
	waveState string,
	seriesState string,
	attemptState string,
) {
	t.Helper()
	require.Equal(t, tournamentState, state.tournamentState)
	require.Equal(t, waveState, state.waveState)
	for _, attempt := range state.attempts {
		require.Equal(t, seriesState, attempt.seriesState)
		require.Equal(t, attemptState, attempt.attemptState)
	}
}

func assertOperatorPauseGraphTopology(
	t testing.TB,
	evidence []operatorPauseGraphEvidence,
	waveID uuid.UUID,
	seriesIDs []uuid.UUID,
	attempts []operatorPauseGraphAttempt,
	resumed bool,
) {
	t.Helper()
	require.Len(t, evidence, 1+len(seriesIDs)+len(attempts))
	byScope := make(map[uuid.UUID]operatorPauseGraphEvidence, len(evidence))
	for _, item := range evidence {
		byScope[item.scopeID] = item
		expectedState := "active"
		if resumed {
			expectedState = "resumed"
			require.True(t, item.resolvedAt.Valid)
		} else {
			require.False(t, item.resolvedAt.Valid)
		}
		require.Equal(t, expectedState, item.state)
	}
	wavePause, ok := byScope[waveID]
	require.True(t, ok)
	require.Equal(t, "wave", wavePause.scopeKind)
	require.Zero(t, wavePause.depth)
	require.False(t, wavePause.parentID.Valid)

	for _, seriesID := range seriesIDs {
		seriesPause, exists := byScope[seriesID]
		require.True(t, exists)
		require.Equal(t, "series", seriesPause.scopeKind)
		require.Zero(t, seriesPause.depth)
		require.False(t, seriesPause.parentID.Valid)
	}
	for _, attempt := range attempts {
		gamePause, exists := byScope[attempt.attemptID]
		require.True(t, exists)
		require.Equal(t, "game_attempt", gamePause.scopeKind)
		require.Equal(t, 1, gamePause.depth)
		require.True(t, gamePause.parentID.Valid)
		require.Equal(t, byScope[attempt.seriesID].id, uuid.UUID(gamePause.parentID.Bytes))
		require.True(t, gamePause.gameAttemptID.Valid)
		require.Equal(t, attempt.attemptID, uuid.UUID(gamePause.gameAttemptID.Bytes))
		require.True(t, gamePause.originalDeadline.Valid)
		require.True(t, gamePause.frozenAt.Valid)
		require.True(t, gamePause.frozenRemainingMS.Valid)
		require.WithinDuration(
			t,
			attempt.startedAt.Add(180*time.Second),
			gamePause.originalDeadline.Time,
			time.Millisecond,
		)
		expectedRemaining := gamePause.originalDeadline.Time.Sub(gamePause.frozenAt.Time).Milliseconds()
		require.InDelta(t, expectedRemaining, gamePause.frozenRemainingMS.Int64, 1)
		if resumed {
			require.True(t, gamePause.resumedAt.Valid)
			require.True(t, gamePause.resumedDeadline.Valid)
			require.WithinDuration(
				t,
				gamePause.resumedAt.Time.Add(time.Duration(gamePause.frozenRemainingMS.Int64)*time.Millisecond),
				gamePause.resumedDeadline.Time,
				time.Millisecond,
			)
			require.Greater(t, gamePause.resumedDeadline.Time.UnixNano(), gamePause.originalDeadline.Time.UnixNano())
		} else {
			require.False(t, gamePause.resumedAt.Valid)
			require.False(t, gamePause.resumedDeadline.Valid)
		}
	}
}

func operatorPauseGraphSeriesIDs(state operatorPauseGraphState) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(state.attempts))
	for _, attempt := range state.attempts {
		seen[attempt.seriesID] = struct{}{}
	}
	result := make([]uuid.UUID, 0, len(seen))
	for seriesID := range seen {
		result = append(result, seriesID)
	}
	slices.SortFunc(result, func(first, second uuid.UUID) int {
		return strings.Compare(first.String(), second.String())
	})
	return result
}

func operatorPauseGraphAttemptByID(
	t testing.TB,
	state operatorPauseGraphState,
	attemptID uuid.UUID,
) operatorPauseGraphAttempt {
	t.Helper()
	for _, attempt := range state.attempts {
		if attempt.attemptID == attemptID {
			return attempt
		}
	}
	require.FailNow(t, "active Game disappeared", attemptID.String())
	return operatorPauseGraphAttempt{}
}

func assertOperatorPauseGraphSeriesView(
	t testing.TB,
	snapshot api.OperatorRecoverySnapshot,
	seriesIDs []uuid.UUID,
	expected api.SeriesState,
) {
	t.Helper()
	states := make(map[uuid.UUID]api.SeriesState, len(snapshot.Series))
	for _, series := range snapshot.Series {
		states[series.Id] = series.State
	}
	for _, seriesID := range seriesIDs {
		require.Equal(t, expected, states[seriesID])
	}
}
