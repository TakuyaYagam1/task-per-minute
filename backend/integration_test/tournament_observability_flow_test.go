//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	v1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	exactdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	realtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	adminresultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/result"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	eventdeliverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadmininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
	tournamentadminobserved "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observed"
)

func TestTournamentObservabilityFlowSurvivesApplicationRestart(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	forfeit := prepareTournamentObservabilityForfeit(ctx, t, fixture)
	logs, logger := newTournamentFlowLogger(t)
	dispatcher := startTournamentFlowDispatcher(t, logger)

	const accessToken = "tournament-observability-access"
	operatorSubject := "tournament-observability-operator"
	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	verifier.EXPECT().VerifyAccess(mock.Anything, accessToken).Return(&authusecase.Claims{
		JTI: "tournament-observability-session", Subject: operatorSubject, Kind: authusecase.TokenKindAccess,
	}, nil).Twice()
	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow("operator:" + operatorSubject).Return(true).Twice()

	first := newTournamentFlowHandler(t, logger, dispatcher, verifier, limiter)
	response := tournamentFlowForfeitRequest(t, first, accessToken, forfeit)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String()+"\n"+logs.String())
	assertTournamentFlowEvidence(ctx, t, forfeit.commandID, forfeit.auditID, forfeit.outboxID)

	second := newTournamentFlowHandler(t, logger, dispatcher, verifier, limiter)
	response = tournamentFlowForfeitRequest(t, second, accessToken, forfeit)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String()+"\n"+logs.String())
	assertTournamentFlowEvidence(ctx, t, forfeit.commandID, forfeit.auditID, forfeit.outboxID)
	outboxCorrelationID := tournamentFlowOutboxCorrelation(ctx, t, forfeit.outboxID)
	processTournamentFlowOutbox(t, ctx, dispatcher, forfeit)
	assertTournamentFlowEvidence(ctx, t, forfeit.commandID, forfeit.auditID, forfeit.outboxID)
	assertTournamentFlowOutboxPublished(ctx, t, forfeit.outboxID)

	require.Eventually(t, func() bool {
		return tournamentFlowEventCount(logs.String(), "tournament.http", forfeit.commandID.String()) == 2 &&
			tournamentFlowEventCount(logs.String(), "tournament.admin.command", forfeit.commandID.String()) == 2 &&
			tournamentFlowEventCount(logs.String(), "tournament.outbox.delivery", outboxCorrelationID.String()) == 1
	}, time.Second, 10*time.Millisecond)
	require.NotContains(t, logs.String(), tournamentFlowPrivateMarker)
}

const tournamentFlowPrivateMarker = "private-observability-flow-marker"

type tournamentFlowForfeit struct {
	tournamentID uuid.UUID
	seriesID     uuid.UUID
	participant  uuid.UUID
	slotID       uuid.UUID
	gameID       uuid.UUID
	revision     int64
	commandID    uuid.UUID
	gameResultID uuid.UUID
	scoreID      uuid.UUID
	seriesResult uuid.UUID
	evidenceID   uuid.UUID
	auditID      uuid.UUID
	outboxID     uuid.UUID
	projectionID uuid.UUID
}

func prepareTournamentObservabilityForfeit(
	ctx context.Context,
	t *testing.T,
	fixture resultAuditMigrationFixture,
) tournamentFlowForfeit {
	t.Helper()

	activatedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'swiss', revision = revision + 1, started_at = $2, updated_at = $2
		WHERE id = $1`, fixture.draft.tournamentID, activatedAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'active', revision = revision + 1, started_at = $2, updated_at = $2
		WHERE id = $1`, fixture.draft.seriesID, activatedAt)
	require.NoError(t, err)

	var (
		slotID   uuid.UUID
		revision int64
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT attempt.slot_id, series.revision
		FROM game_attempts AS attempt
		INNER JOIN series ON series.id = attempt.series_id
		WHERE attempt.id = $1
			AND series.id = $2
			AND attempt.state = 'active'
			AND series.state = 'active'`, fixture.attemptID, fixture.draft.seriesID).
		Scan(&slotID, &revision)
	require.NoError(t, err)

	return tournamentFlowForfeit{
		tournamentID: fixture.draft.tournamentID,
		seriesID:     fixture.draft.seriesID,
		participant:  fixture.draft.participantIDs[0],
		slotID:       slotID,
		gameID:       fixture.attemptID,
		revision:     revision,
		commandID:    uuid.New(),
		gameResultID: uuid.New(),
		scoreID:      uuid.New(),
		seriesResult: uuid.New(),
		evidenceID:   uuid.New(),
		auditID:      uuid.New(),
		outboxID:     uuid.New(),
		projectionID: uuid.New(),
	}
}

func newTournamentFlowHandler(
	t *testing.T,
	logger logkit.Logger,
	observer observability.TournamentEventObserver,
	verifier middleware.AdminAccessVerifier,
	limiter middleware.RateLimiter,
) http.Handler {
	t.Helper()

	tx := postgres.NewTxManager(sharedPool)
	drafts := draftrepo.NewDraftPostgres(tx)
	assignments := assignmentrepo.NewAssignmentPostgres(tx)
	exactPlans := exactdraftrepo.NewExactDraftBranchPlanPostgres(tx, drafts)
	planner := playoff.NewFinalDraftAssignmentService(
		assignmentusecase.NewExactDraftBranchPlanUseCase(exactPlans),
		exactPlans,
		exactPlans,
	)
	postseason := playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository:   playoffrepo.NewPlayoffTerminalPostgres(tx, drafts, assignments.CreateAssignmentTx),
		Publisher:    projectionrepo.NewProjectionPostgres(tx),
		DraftPlanner: planner,
		Rehydrator:   planner,
	})
	results := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: tx,
		Repository: adminresultrepo.NewTournamentAdminResultPostgresWithDependencies(
			tx,
			resultauthority.NewResultPostgres(tx),
			resultauthority.FinalizeProjection,
			wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
		),
		Postseason: postseason,
	})
	admin := tournamentadminobserved.NewObservedService(
		tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{Forfeit: results}),
		nil,
		telemetryadapter.NewTournamentAdminObserver(observer),
	)
	server := v1.New(v1.Dependencies{
		TournamentAdmin:                   tournamentadmininbound.NewInboundAdapter(admin),
		OperatorTournamentMutationLimiter: limiter,
	})
	return middleware.Build(logger, middleware.WithTournamentEventObserver(observer))(
		v1.NewHandler(server, v1.HandlerOptions{AdminAuth: verifier}),
	)
}

func tournamentFlowForfeitRequest(
	t *testing.T,
	handler http.Handler,
	accessToken string,
	forfeit tournamentFlowForfeit,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(api.OperatorForfeitRequest{
		TournamentId:              forfeit.tournamentID,
		SeriesId:                  forfeit.seriesID,
		ForfeitingParticipantId:   forfeit.participant,
		Confirmed:                 true,
		Reason:                    "confirmed rule violation",
		Basis:                     api.RuleViolation,
		RuleId:                    "operator.rule_violation.v1",
		EvidenceIds:               []uuid.UUID{forfeit.evidenceID},
		ExpectedAuthorityRevision: forfeit.revision,
		ExpectedGame: &api.OperatorForfeitGameExpectation{
			SlotId: forfeit.slotID, GameId: forfeit.gameID, AttemptNo: 1, State: api.GameState(domain.GameStateActive),
		},
		GameResultRevisionId:   &forfeit.gameResultID,
		ScoreRevisionId:        forfeit.scoreID,
		SeriesResultRevisionId: forfeit.seriesResult,
		AuditEventId:           forfeit.auditID,
		OutboxEventId:          forfeit.outboxID,
		ProjectionRevisionId:   forfeit.projectionID,
	})
	require.NoError(t, err)
	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, accessToken)
	require.NoError(t, err)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/tournaments/"+forfeit.tournamentID.String()+"/series/"+forfeit.seriesID.String()+"/operator-forfeits",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", forfeit.commandID.String())
	request.Header.Set("X-Private-Correlation", tournamentFlowPrivateMarker)
	request.Header.Set(middleware.CSRFHeaderName, csrfToken)
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: accessToken})
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: csrfToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func processTournamentFlowOutbox(
	t *testing.T,
	ctx context.Context,
	observer observability.TournamentEventObserver,
	forfeit tournamentFlowForfeit,
) {
	t.Helper()
	correlationID := tournamentFlowOutboxCorrelation(ctx, t, forfeit.outboxID)

	sink := eventdeliverymocks.NewMockSink(t)
	sink.EXPECT().Deliver(mock.Anything, mock.MatchedBy(func(event delivery.Event) bool {
		return event.ID == forfeit.outboxID &&
			event.CorrelationID == correlationID &&
			event.TournamentID == forfeit.tournamentID &&
			event.ProjectionRevisionID == forfeit.projectionID &&
			event.ProjectionRevision >= 1 &&
			event.ProjectionOrdinal >= 1
	})).Return(nil).Once()

	worker, err := delivery.NewWorker(
		realtimerepo.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool)),
		sink,
		delivery.WorkerConfig{WorkerID: uuid.New(), BatchSize: 1},
		telemetryadapter.NewEventDeliveryObserver(observer),
	)
	require.NoError(t, err)
	result, err := worker.Process(ctx)
	require.NoError(t, err)
	require.Equal(t, delivery.ProcessResult{Claimed: 1, Acknowledged: 1}, result)

	restartedWorker, err := delivery.NewWorker(
		realtimerepo.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool)),
		sink,
		delivery.WorkerConfig{WorkerID: uuid.New(), BatchSize: 1},
		telemetryadapter.NewEventDeliveryObserver(observer),
	)
	require.NoError(t, err)
	result, err = restartedWorker.Process(ctx)
	require.NoError(t, err)
	require.Equal(t, delivery.ProcessResult{}, result)
}

func tournamentFlowOutboxCorrelation(ctx context.Context, t *testing.T, outboxID uuid.UUID) uuid.UUID {
	t.Helper()
	var correlationID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT idempotency_key
		FROM outbox_events
		WHERE id = $1`, outboxID).Scan(&correlationID))
	return correlationID
}

func assertTournamentFlowEvidence(
	ctx context.Context,
	t *testing.T,
	commandID uuid.UUID,
	auditID uuid.UUID,
	outboxID uuid.UUID,
) {
	t.Helper()

	var commandCount, auditCount, outboxCount int
	err := sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM operator_result_commands WHERE command_id = $1),
			(SELECT COUNT(*)
			 FROM result_commits AS commit
			 INNER JOIN operator_result_commands AS command ON command.commit_id = commit.id
			 INNER JOIN audit_events AS audit ON audit.id = commit.audit_event_id
			 WHERE command.command_id = $1 AND commit.idempotency_key = $1 AND audit.id = $2),
			(SELECT COUNT(*)
			 FROM result_commits AS commit
			 INNER JOIN operator_result_commands AS command ON command.commit_id = commit.id
			 INNER JOIN outbox_events AS outbox ON outbox.id = commit.outbox_event_id
			 INNER JOIN outbox_result_sources AS source ON source.outbox_event_id = outbox.id
			 WHERE command.command_id = $1 AND commit.idempotency_key = $1 AND outbox.id = $3)`,
		commandID, auditID, outboxID,
	).Scan(&commandCount, &auditCount, &outboxCount)
	require.NoError(t, err)
	require.Equal(t, 1, commandCount)
	require.Equal(t, 1, auditCount)
	require.Equal(t, 1, outboxCount)
}

func assertTournamentFlowOutboxPublished(ctx context.Context, t *testing.T, outboxID uuid.UUID) {
	t.Helper()

	var rowCount, publishedCount, releasedCount, attempts int
	err := sharedPool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE published_at IS NOT NULL),
			COUNT(*) FILTER (
				WHERE claimed_by IS NULL
					AND claim_token IS NULL
					AND claimed_until IS NULL
			),
			COALESCE(MAX(attempt_count), 0)
		FROM outbox_events
		WHERE id = $1`, outboxID,
	).Scan(&rowCount, &publishedCount, &releasedCount, &attempts)
	require.NoError(t, err)
	require.Equal(t, 1, rowCount)
	require.Equal(t, 1, publishedCount)
	require.Equal(t, 1, releasedCount)
	require.Equal(t, 1, attempts)
}

type tournamentFlowBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *tournamentFlowBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *tournamentFlowBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func newTournamentFlowLogger(t *testing.T) (*tournamentFlowBuffer, logkit.Logger) {
	t.Helper()
	buffer := &tournamentFlowBuffer{}
	logger, err := logkit.New(logkit.WithLevel(logkit.DebugLevel), logkit.WithSyncWriter(buffer))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logger.Close()) })
	return buffer, logger
}

func startTournamentFlowDispatcher(t *testing.T, logger logkit.Logger) *observability.TournamentEventDispatcher {
	t.Helper()
	metrics := observability.NewTournamentMetrics()
	dispatcher, err := observability.NewTournamentEventDispatcher(
		observability.TournamentEventDispatcherConfig{QueueObserver: metrics},
		metrics,
		observability.NewTournamentStructuredLogger(logger),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	require.Eventually(t, dispatcher.Ready, time.Second, 10*time.Millisecond)
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return dispatcher
}

func tournamentFlowEventCount(raw, event, correlationID string) int {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	count := 0
	for decoder.More() {
		var entry map[string]any
		if decoder.Decode(&entry) != nil {
			return 0
		}
		if entry["message"] == "tournament event" && entry["event"] == event &&
			entry["correlation_id"] == correlationID {
			count++
		}
	}
	return count
}
