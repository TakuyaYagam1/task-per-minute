//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	tournamentlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type tournamentFlowLimiter struct{}

func (tournamentFlowLimiter) Allow(string) bool  { return true }
func (tournamentFlowLimiter) RetryAfter() string { return "" }

type tournamentFlowPlayer struct {
	id      uuid.UUID
	session uuid.UUID
	csrf    string
}

func TestTournamentCreateToChampionThroughProductionHandlers(t *testing.T) {
	for _, withGolden := range []bool{false, true} {
		name := "without_golden"
		if withGolden {
			name = "with_golden"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			truncateRoundProofTables(ctx, t)
			t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

			contentRevision := prepareCreateToChampionContent(ctx, t)
			fixture := newTournamentFlowRESTFixture(t)
			adminToken := fixture.adminAccessToken(t)
			players := joinTournamentFlowPlayers(t, fixture, 4)
			created := createTournamentThroughREST(t, fixture, adminToken, contentRevision, name)
			flow := prepareCreatedTournamentFinalSwiss(ctx, t, created, players, withGolden)

			_, projectionRevision := currentPublishedProjection(ctx, t, flow.tournamentID, flow.rosterID)
			if withGolden {
				applyTournamentActionThroughREST(
					t, fixture, adminToken, flow.tournamentID, projectionRevision, "start_golden", uuid.New(),
				)
				ensureGoldenRuntimeTestCapacity(ctx, t, flow.tournamentID)
				runGoldenThroughREST(t, fixture, adminToken, flow.tournamentID, projectionRevision, players)
			}

			playoffCommandID := uuid.New()
			playoffView := applyTournamentActionThroughREST(
				t, fixture, adminToken, flow.tournamentID, projectionRevision, "start_playoffs", playoffCommandID,
			)
			require.Equal(t, api.TournamentState(domain.TournamentStatePlayoffs), playoffView.State)
			completeTournamentPlayoffs(ctx, t, fixture, adminToken, flow, playoffCommandID)
			assertCompletedTournamentThroughREST(t, fixture, flow.tournamentID)
		})
	}
}

func prepareCreateToChampionContent(ctx context.Context, t *testing.T) int64 {
	t.Helper()
	for _, category := range []string{"web", "crypto", "forensics", "reverse", "pwn"} {
		count := 8
		if category == "web" {
			count = 20
		}
		for range count {
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'create to champion fixture', $2, 'easy', 60, $3, 'normal')`,
				"create_to_champion_"+uuid.NewString()[:8], category, "champion-"+uuid.NewString()[:8])
			require.NoError(t, err)
		}
	}
	prepareRoundProofContent(ctx, t)
	return currentTaskPoolPublicationRevision(ctx, t)
}

func newTournamentFlowRESTFixture(t *testing.T) *restFixture {
	t.Helper()
	database := newDatabaseFixture()
	tx := database.mgr
	clock := realIntegrationClock()
	redis := sharedRedis(t)
	auth := authusecase.NewUseCase(authusecase.Config{
		AccessTTL: 15 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
	}, clock, redisadapter.NewRevocationRedis(redis.client, "integration:tournament-flow:"+uniq("auth")+":"),
		authadapter.NewJWTCodec(authadapter.JWTConfig{
			Secret: []byte("01234567890123456789012345678901"), Now: clock.Now,
		}), authadapter.NewPasswordVerifier([]byte(restAdminPassword)))

	tournaments := postgres.NewTournamentPostgres(tx)
	legacyCatalog := catalogusecase.NewTournamentUseCase(postgres.NewTournamentCatalogPostgres(tournaments), clock)
	ids, err := catalogusecase.NewDeterministicIDGenerator(
		uuid.NewSHA1(uuid.NameSpaceOID, []byte("task-per-minute:tournament-commands")),
	)
	require.NoError(t, err)
	receipts := redisadapter.NewCommandReceiptStore(redis.client, 30*time.Second, 24*time.Hour, 30*time.Second)
	catalog := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, Lister: legacyCatalog,
		CreateStore: postgres.NewTournamentCreatePostgres(tournaments), Receipts: receipts,
	})

	progressionRepository := postgres.NewTournamentProgressionPostgres(tx)
	progression := tournamentprogression.NewWorkflow(tournamentprogression.ProgressionDependencies{
		Repository: progressionRepository, TerminalEvidence: progressionRepository,
		Transitioner: progressionRepository, Publisher: progressionRepository, ProgressionClock: clock,
	})
	lifecycleRepository := postgres.NewTournamentAdminLifecyclePostgres(tx)
	lifecycle := tournamentadmin.NewLifecycleWorkflow(tournamentadmin.LifecycleWorkflowDependencies{
		Transactions: tx, Repository: lifecycleRepository,
		Transitions: tournamentlifecycle.NewTournamentLifecycleUseCase(
			postgres.NewTournamentLifecyclePostgres(tournaments), clock,
		),
		Pauses: tournamentpause.NewTournamentPauseUseCase(tx, lifecycleRepository, clock),
		Cancellations: tournamentcancellation.NewTournamentCancellationUseCase(
			postgres.NewTournamentCancellationPostgres(tx), clock,
		),
		Progressions: progression, Clock: clock,
	})
	postseason := newTournamentFlowTerminalCoordinator(tx)
	results := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: tx,
		Repository:   postgres.NewTournamentAdminResultPostgres(tx, postgres.NewResultPostgres(tx)),
		Postseason:   postseason,
	})
	admin := tournamentadmin.NewInboundAdapter(tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{
		Catalog: catalog, Lifecycle: lifecycle, Forfeit: results,
	}))
	golden := goldenusecase.NewRuntimeApplication(postgres.NewGoldenRuntimePostgres(tx), clock)
	limiter := tournamentFlowLimiter{}
	server := restv1.New(restv1.Dependencies{
		Players: playerusecase.SessionNewUseCase(database.mgr, database.players, clock), AdminAuth: auth,
		Tournaments: catalog, TournamentAdmin: admin, TournamentSnapshots: postgres.NewTournamentSnapshotPostgres(tx),
		Golden: golden, LoginLimiter: limiter, JoinLimiter: limiter,
		PublicTournamentReadLimiter: limiter, OperatorTournamentReadLimiter: limiter,
		OperatorTournamentMutationLimiter: limiter, ParticipantTournamentReadLimiter: limiter,
		ParticipantTournamentMutationLimiter: limiter,
	})
	return &restFixture{
		databaseFixture: database,
		handler:         restv1.NewHandler(server, restv1.HandlerOptions{AdminAuth: auth, PlayerRepo: database.players}),
		auth:            auth, validator: newOpenAPIResponseValidator(t),
	}
}

func joinTournamentFlowPlayers(t *testing.T, fixture *restFixture, count int) []tournamentFlowPlayer {
	t.Helper()
	players := make([]tournamentFlowPlayer, 0, count)
	for index := range count {
		req, resp := fixture.doJSON(t, http.MethodPost, "/api/v1/players/join", fmt.Sprintf(
			`{"username":%q}`, fmt.Sprintf("finalist_%d_%s", index+1, uuid.NewString()[:8]),
		), "")
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
		joined := decodeJSON[api.JoinPlayerResponse](t, resp)
		sessionToken := uuid.MustParse(playerSessionCookieValue(t, resp))
		var csrfToken string
		for _, cookie := range resp.Result().Cookies() {
			if cookie.Name == middleware.PlayerCSRFCookieName {
				csrfToken = cookie.Value
			}
		}
		require.NotEmpty(t, csrfToken)
		players = append(players, tournamentFlowPlayer{id: joined.PlayerId, session: sessionToken, csrf: csrfToken})
	}
	return players
}

func createTournamentThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	contentRevision int64,
	name string,
) api.Tournament {
	t.Helper()
	body := fmt.Sprintf(`{
		"preset":"tournament_v1",
		"expected_revision":0,
		"name":%q,
		"public_id":%q,
		"planned_roster_size":4,
		"content_revision":%d
	}`, "Create to champion "+name, "create-to-champion-"+uuid.NewString()[:8], contentRevision)
	req, resp := doTournamentFlowJSON(
		t, fixture, http.MethodPost, "/api/v1/admin/tournaments", body, adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	created := decodeJSON[api.Tournament](t, resp)
	require.Equal(t, api.TournamentState(domain.TournamentStateDraft), created.State)
	require.EqualValues(t, 4, created.PlannedRosterSize)
	return created
}

func prepareCreatedTournamentFinalSwiss(
	ctx context.Context,
	t *testing.T,
	created api.Tournament,
	players []tournamentFlowPlayer,
	withGolden bool,
) tournamentAdminSwissProofFixture {
	t.Helper()
	playerIDs := make([]uuid.UUID, len(players))
	for index := range players {
		playerIDs[index] = players[index].id
	}
	var normalPoolRevisionID uuid.UUID
	var normalPoolRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT configuration.normal_pool_revision_id, pool.revision
		FROM tournament_content_configurations AS configuration
		JOIN task_pool_revisions AS pool ON pool.id = configuration.normal_pool_revision_id
		WHERE configuration.tournament_id = $1 AND configuration.state = 'published'`, created.Id).
		Scan(&normalPoolRevisionID, &normalPoolRevision))
	flow := createTournamentAdminSwissProofFixtureForAggregate(
		ctx, t, created.Id, created.RosterId, playerIDs,
		normalPoolRevisionID, normalPoolRevision, time.Now().UTC().Truncate(time.Microsecond),
	)
	if withGolden {
		return finishNativeGoldenFinalSwiss(ctx, t, flow)
	}
	return finishNativeSwissWithoutGolden(ctx, t, flow)
}

func finishNativeSwissWithoutGolden(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) tournamentAdminSwissProofFixture {
	t.Helper()
	seeds := make(map[uuid.UUID]int, len(fixture.participants))
	for index, participantID := range fixture.participants {
		seeds[participantID] = index
	}
	for round := 1; round <= 3; round++ {
		if round > 1 {
			fixture = nextSwissReceiptWave(ctx, t, fixture, round, false, false)
		}
		_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
		require.NoError(t, err)
		require.True(t, changed)
		for index, binding := range fixture.binding {
			winnerID := binding.FirstParticipantID
			if seeds[binding.SecondParticipantID] < seeds[winnerID] {
				winnerID = binding.SecondParticipantID
			}
			settleCorrectionSwissReceiptSeries(ctx, t, fixture, index, winnerID)
		}
		closeSwissReceiptWave(ctx, t, fixture)
	}
	return fixture
}

func applyTournamentActionThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	projectionRevision int64,
	action string,
	commandID uuid.UUID,
) api.Tournament {
	t.Helper()
	body := fmt.Sprintf(`{"expected_projection_revision":%d,"action":%q,"confirmed":true}`,
		projectionRevision, action)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/actions"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, body, adminSession(adminToken), commandID, "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.Tournament](t, resp)
}

func runGoldenThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	projectionRevision int64,
	players []tournamentFlowPlayer,
) {
	t.Helper()
	root := "/api/v1/admin/tournaments/" + tournamentID.String() + "/golden"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, root+"/open",
		fmt.Sprintf(`{"expected_projection_revision":%d}`, projectionRevision), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	operator := decodeJSON[api.GoldenOperatorResponse](t, resp)
	require.NotEmpty(t, operator.Groups)

	playersByParticipant := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		var participantID uuid.UUID
		require.NoError(t, sharedPool.QueryRow(context.Background(), `
			SELECT id FROM participants WHERE roster_id = (
				SELECT roster_id FROM tournaments WHERE id = $1
			) AND player_id = $2`, tournamentID, player.id).Scan(&participantID))
		playersByParticipant[participantID] = player
	}
	for _, group := range operator.Groups {
		for _, member := range group.Members {
			player := playersByParticipant[member.ParticipantId]
			path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/golden/ready"
			req, resp = doTournamentFlowJSON(t, fixture, http.MethodPost, path, `{"ready":true}`,
				cookieSession(player.session.String()), uuid.New(), player.csrf)
			require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			fixture.validateResponse(t, req, resp)
			participant := decodeJSON[api.GoldenParticipantResponse](t, resp)
			if participant.Task != nil {
				require.EqualValues(t, 180, participant.Task.TimeLimitSeconds)
			}
		}
		req, resp = doTournamentFlowJSON(t, fixture, http.MethodPost,
			root+"/attempts/"+group.AttemptId.String()+"/start", "", adminSession(adminToken), uuid.New(), "")
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
	}
	recovered := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(postgres.NewTxManager(sharedPool)), realIntegrationClock(),
	)
	require.NoError(t, recovered.Recover(context.Background(), tournamentID))
	for _, group := range operator.Groups {
		var flag string
		require.NoError(t, sharedPool.QueryRow(context.Background(), `
			SELECT version.flag
			FROM golden_runtime_assignments AS runtime
			JOIN task_versions AS version ON version.task_id = runtime.task_id AND version.version = runtime.task_version
			WHERE runtime.attempt_id = $1`, group.AttemptId).Scan(&flag))
		for memberIndex, member := range group.Members {
			player := playersByParticipant[member.ParticipantId]
			path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/golden/submissions"
			req, resp = doTournamentFlowJSON(t, fixture, http.MethodPost, path,
				fmt.Sprintf(`{"submitted_flag":%q}`, flag), cookieSession(player.session.String()), uuid.New(), player.csrf)
			require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			fixture.validateResponse(t, req, resp)
			participant := decodeJSON[api.GoldenParticipantResponse](t, resp)
			if memberIndex == len(group.Members)-1 {
				require.Equal(t, api.GoldenRuntimeState("completed"), participant.State)
			}
		}
	}
	var bindings int
	require.NoError(t, sharedPool.QueryRow(context.Background(), `SELECT
		COUNT(*) FROM golden_position_ledger_commit_bindings WHERE tournament_id = $1`, tournamentID).
		Scan(&bindings))
	memberCount := 0
	for _, group := range operator.Groups {
		memberCount += len(group.Members)
	}
	require.Equal(t, memberCount*2, bindings)
}

func completeTournamentPlayoffs(
	ctx context.Context,
	t *testing.T,
	rest *restFixture,
	adminToken string,
	fixture tournamentAdminSwissProofFixture,
	playoffCommandID uuid.UUID,
) {
	t.Helper()
	var reservedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT locked_at FROM rosters WHERE id = $1`, fixture.rosterID).Scan(&reservedAt))
	_, err := fixture.tx.Querier(ctx).ReserveCheckedInTournamentParticipants(ctx,
		sqlc.ReserveCheckedInTournamentParticipantsParams{
			RosterID:   fixture.rosterID,
			AcquiredAt: pgtype.Timestamptz{Time: reservedAt, Valid: true},
		})
	require.NoError(t, err)

	for position := 1; position <= 2; position++ {
		input := semifinalSettlementInput(ctx, t, fixture, playoffCommandID, position)
		activateTournamentFlowSeries(ctx, t, input.Scope.SeriesID)
		recordTournamentForfeitThroughREST(t, rest, adminToken, fixture, input)
	}

	drafts := postgres.NewDraftPostgres(fixture.tx)
	coordinator := newTournamentFlowTerminalCoordinator(fixture.tx)

	ids, err := playoff.FinalStageIdentity(playoffCommandID)
	require.NoError(t, err)
	draftRepository := postgres.NewParticipantDraftRepository(fixture.tx, drafts)
	current, err := draftRepository.LoadDraft(ctx, ids.DraftID)
	require.NoError(t, err)
	for current.State == draftusecase.ExecutionStateActive {
		action := draftusecase.NewActionUseCase(draftRepository, finalDraftClock{
			at: time.Now().UTC().Truncate(time.Microsecond),
		})
		next, actionErr := action.Apply(ctx, draftusecase.PlayerActionCommand{
			DraftID: current.ID, CommandID: uuid.New(), ActorID: *current.CurrentActorID,
			ExpectedRevisionID: current.RevisionID, ExpectedRevision: current.Revision,
			ExpectedServiceEpoch: current.ServiceEpoch, ExpectedTurn: current.Turn,
			ResultRevisionID: uuid.New(), ActionID: uuid.New(), Action: *current.CurrentAction,
			Category: current.LegalCategories[0],
		})
		require.NoError(t, actionErr)
		current = &next.Draft
	}
	require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, activateErr := coordinator.ActivateFinalAfterDraft(txCtx, playoff.TerminalDraftCommand{
			TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID,
			DraftID: ids.DraftID, CommandID: current.CommandID,
		})
		return activateErr
	}))
	for position := 1; position <= 2; position++ {
		input := activeFinalSettlementInput(ctx, t, fixture, ids, position)
		require.NoError(t, fixture.tx.Do(ctx, func(txCtx context.Context) error {
			if _, changed, settleErr := postgres.NewResultPostgres(fixture.tx).Settle(txCtx, input); settleErr != nil {
				return settleErr
			} else if !changed {
				return errors.New("expected fresh final settlement")
			}
			_, advanceErr := coordinator.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{
				TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID,
			})
			return advanceErr
		}))
	}
}

func activateTournamentFlowSeries(ctx context.Context, t *testing.T, seriesID uuid.UUID) {
	t.Helper()
	activatedAt := time.Now().UTC().Truncate(time.Microsecond)
	tag, err := sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'active', revision = revision + 1, started_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'locked'`, seriesID, activatedAt)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())
}

func newTournamentFlowTerminalCoordinator(tx *postgres.TxManager) *playoff.TerminalCoordinator {
	drafts := postgres.NewDraftPostgres(tx)
	exactPlans := postgres.NewExactDraftBranchPlanPostgres(tx, drafts)
	planner := playoff.NewFinalDraftAssignmentService(
		assignmentusecase.NewExactDraftBranchPlanUseCase(exactPlans), exactPlans, exactPlans,
	)
	return playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository: postgres.NewPlayoffTerminalPostgres(tx, drafts, postgres.NewAssignmentPostgres(tx)),
		Publisher:  postgres.NewProjectionPostgres(tx), DraftPlanner: planner, Rehydrator: planner,
	})
}

func recordTournamentForfeitThroughREST(
	t *testing.T,
	rest *restFixture,
	adminToken string,
	fixture tournamentAdminSwissProofFixture,
	input postgres.ResultSettlementInput,
) {
	t.Helper()
	ctx := context.Background()
	var slotID, forfeitingParticipantID uuid.UUID
	var attemptNumber int32
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT attempt.slot_id, attempt.attempt_number,
			CASE WHEN series.first_participant_id = $2 THEN series.second_participant_id
				ELSE series.first_participant_id END
		FROM game_attempts AS attempt
		JOIN series ON series.id = attempt.series_id
		WHERE attempt.id = $1`, input.Scope.AttemptID, *input.GameWinnerID).
		Scan(&slotID, &attemptNumber, &forfeitingParticipantID))
	authority, err := postgres.NewTournamentAdminResultPostgres(
		fixture.tx, postgres.NewResultPostgres(fixture.tx),
	).LockOperatorResultAuthority(ctx, fixture.tournamentID, input.Scope.SeriesID)
	require.NoError(t, err)
	body, err := json.Marshal(api.OperatorForfeitRequest{
		TournamentId: fixture.tournamentID, SeriesId: input.Scope.SeriesID,
		ForfeitingParticipantId: forfeitingParticipantID, Confirmed: true,
		Reason: "create-to-champion integration", Basis: api.RuleViolation,
		RuleId: "integration.operator_forfeit.v1", EvidenceIds: []uuid.UUID{uuid.New()},
		ExpectedAuthorityRevision: authority.AuthorityRevision,
		ExpectedGame: &api.OperatorForfeitGameExpectation{
			SlotId: slotID, GameId: input.Scope.AttemptID, AttemptNo: attemptNumber,
			State: api.GameState(domain.GameStateActive),
		},
		GameResultRevisionId: &input.IDs.GameResultRevisionID,
		ScoreRevisionId:      input.IDs.SeriesScoreRevisionID, SeriesResultRevisionId: input.IDs.SeriesResultRevisionID,
		AuditEventId: input.IDs.AuditEventID, OutboxEventId: input.IDs.OutboxEventID,
		ProjectionRevisionId: input.IDs.ProjectionEvidenceID,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + fixture.tournamentID.String() +
		"/series/" + input.Scope.SeriesID.String() + "/operator-forfeits"
	req, resp := doTournamentFlowJSON(
		t, rest, http.MethodPost, path, string(body), adminSession(adminToken), input.IDs.CommitIdempotencyKey, "",
	)
	require.Equal(t, http.StatusNoContent, resp.Code, resp.Body.String())
	rest.validateResponse(t, req, resp)
}

func assertCompletedTournamentThroughREST(t *testing.T, fixture *restFixture, tournamentID uuid.UUID) {
	t.Helper()
	path := "/api/v1/tournaments/" + tournamentID.String() + "/snapshot"
	req, resp := fixture.doJSON(t, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	snapshot := decodeJSON[api.PublicRecoverySnapshot](t, resp)
	require.Equal(t, tournamentID, snapshot.Tournament.TournamentId)
	require.Equal(t, api.TournamentState(domain.TournamentStateCompleted), snapshot.Tournament.State)
	require.NotNil(t, snapshot.Tournament.FinishedAt)
	require.Len(t, snapshot.Bracket.Matches, 2)
	var championArtifacts int
	require.NoError(t, sharedPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM projection_artifacts
		WHERE tournament_id = $1 AND artifact_kind = 'champion'`, tournamentID).Scan(&championArtifacts))
	require.Equal(t, 1, championArtifacts)
}

func doTournamentFlowJSON(
	t *testing.T,
	fixture *restFixture,
	method string,
	path string,
	body string,
	authHeader string,
	commandID uuid.UUID,
	playerCSRF string,
) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Idempotency-Key", commandID.String())
	switch {
	case strings.HasPrefix(authHeader, "AdminSession "):
		addAdminSession(t, request, strings.TrimPrefix(authHeader, "AdminSession "))
	case strings.HasPrefix(authHeader, "Cookie "):
		request.Header.Set("Cookie", strings.TrimPrefix(authHeader, "Cookie "))
		if playerCSRF != "" {
			request.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: playerCSRF})
			request.Header.Set(middleware.CSRFHeaderName, playerCSRF)
		}
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return request, response
}
