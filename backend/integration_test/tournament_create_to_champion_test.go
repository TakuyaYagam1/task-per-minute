//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	inboundws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	exactdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	runtimepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/golden/runtime"
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	adminlifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/lifecycle"
	adminreplayrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/replay"
	adminresultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/result"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	cancellationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/cancellation"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	configurationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/configuration"
	creationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/creation"
	tournamentlifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/lifecycle"
	participantauthorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/authority"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	participantpostseriesrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/postseries"
	participantreadinessrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/readiness"
	participantsettlementrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
	participantstaterepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/state"
	participantsubmissionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/submission"
	participantsurrenderrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/surrender"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	tournamentsnapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
	goldenruntime "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/runtime"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/application"
	tournamentadminconfiguration "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/configuration"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	tournamentadmininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
	tournamentadminlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	tournamentadminreplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	tournamentadminresult "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	tournamentadminroster "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	tournamentlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type tournamentFlowLimiter struct{}

func (tournamentFlowLimiter) Allow(string) bool  { return true }
func (tournamentFlowLimiter) RetryAfter() string { return "" }

type tournamentFlowParticipantArchive struct{}

func (tournamentFlowParticipantArchive) GetSourceFile(
	context.Context,
	inbound.ParticipantArchiveQuery,
) (inbound.ParticipantArchiveDownload, error) {
	return inbound.ParticipantArchiveDownload{}, domain.ErrTaskNotFound
}

func (tournamentFlowParticipantArchive) SourceFileAvailable(
	context.Context,
	inbound.ParticipantArchiveQuery,
) (bool, error) {
	return false, nil
}

type tournamentFlowPlayer struct {
	id      uuid.UUID
	session uuid.UUID
	csrf    string
}

type tournamentFlowCatalog struct {
	revision    int64
	flags       map[uuid.UUID]string
	goldenTasks map[uuid.UUID]tournamentFlowGoldenTask
}

const (
	createToChampionNormalTaskCount        = 27
	createToChampionGoldenTaskCount        = domain.TournamentMinParticipants / 2 * (domain.AssignmentReserveCount + 1)
	createToChampionBO3OnlyNormalTaskCount = domain.AssignmentReserveCount + 1
)

type tournamentFlowGoldenTask struct {
	description string
	taskURL     *string
	version     int
}

func loadTournamentFlowParticipantDraftContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (domain.ContentConfiguration, error) {
	content, err := rosterrepo.LoadPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return domain.ContentConfiguration{}, err
	}
	return content.Configuration, nil
}

type tournamentFlowClock struct {
	mu     sync.Mutex
	now    time.Time
	step   time.Duration
	frozen bool
}

func newTournamentFlowClock(now time.Time) *tournamentFlowClock {
	return &tournamentFlowClock{now: now.UTC().Truncate(time.Microsecond), step: time.Microsecond}
}

func (clock *tournamentFlowClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	wallNow := time.Now().UTC().Truncate(time.Microsecond)
	if !clock.frozen && wallNow.After(clock.now) {
		clock.now = wallNow
	}
	now := clock.now
	clock.now = clock.now.Add(clock.step)
	return now
}

func (clock *tournamentFlowClock) AdvanceTo(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now.UTC().Truncate(time.Nanosecond)
}

func (clock *tournamentFlowClock) FreezeAt(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now.UTC().Truncate(time.Nanosecond)
	clock.frozen = true
}

func (clock *tournamentFlowClock) Resume() {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = time.Now().UTC().Truncate(time.Microsecond)
	clock.frozen = false
}

type tournamentFlowRuntime struct {
	clock     *tournamentFlowClock
	golden    *goldenruntime.RuntimeApplication
	webSocket *inboundws.Server
}

var tournamentFlowRuntimes sync.Map

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

			catalog := prepareCreateToChampionContent(ctx, t)
			fixture := newTournamentFlowRESTFixture(t)
			adminToken := fixture.adminAccessToken(t)
			content := getTournamentContentThroughREST(t, fixture, adminToken)
			players := joinTournamentFlowPlayers(t, fixture, 4)
			created := createTournamentThroughREST(t, fixture, adminToken, content.ContentRevision, name)
			openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
			roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
			preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
			lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
			startSwissThroughREST(t, fixture, adminToken, created.Id)
			connectTournamentParticipantsThroughProduction(t, fixture, created.Id, players)
			runProductionSwissThroughREST(t, fixture, adminToken, created.Id, players, catalog.flags, withGolden)
			assertTournamentRealtimeThroughProduction(t, fixture, adminToken, created.Id, players[0])
			projectionRevision := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).NextCursor.ProjectionRevision
			if withGolden {
				applyTournamentActionThroughREST(
					t, fixture, adminToken, created.Id, projectionRevision, "start_golden", uuid.New(),
				)
				projectionRevision = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).NextCursor.ProjectionRevision
				runGoldenThroughREST(t, fixture, adminToken, created.Id, projectionRevision, roster, players, catalog)
				projectionRevision = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id).NextCursor.ProjectionRevision
			}

			playoffCommandID := uuid.New()
			playoffView := applyTournamentActionThroughREST(
				t, fixture, adminToken, created.Id, projectionRevision, "start_playoffs", playoffCommandID,
			)
			require.Equal(t, api.TournamentState(domain.TournamentStatePlayoffs), playoffView.State)
			completeTournamentPlayoffsThroughREST(t, fixture, adminToken, created.Id, roster, players, catalog.flags)
			assertCompletedTournamentThroughREST(t, fixture, adminToken, created.Id)
			assertTournamentRealtimeThroughProduction(t, fixture, adminToken, created.Id, players[0])
		})
	}
}

func prepareCreateToChampionContent(ctx context.Context, t *testing.T) tournamentFlowCatalog {
	return prepareCreateToChampionContentWithNormalCount(ctx, t, createToChampionNormalTaskCount)
}

func prepareCreateToChampionContentWithNormalCount(
	ctx context.Context,
	t *testing.T,
	normalTaskCount int,
) tournamentFlowCatalog {
	return prepareCreateToChampionContentWithCounts(ctx, t, normalTaskCount, createToChampionGoldenTaskCount)
}

func prepareCreateToChampionContentForRosterSize(
	ctx context.Context,
	t *testing.T,
	normalTaskCount int,
	rosterSize int,
) tournamentFlowCatalog {
	return prepareCreateToChampionContentWithCounts(
		ctx, t, normalTaskCount, rosterSize/2*(domain.AssignmentReserveCount+1),
	)
}

func prepareCreateToChampionContentWithCounts(
	ctx context.Context,
	t *testing.T,
	normalTaskCount int,
	goldenTaskCount int,
) tournamentFlowCatalog {
	return prepareCreateToChampionContentWithCountsAndNormalTimeLimit(
		ctx, t, normalTaskCount, goldenTaskCount, 180,
	)
}

func prepareCreateToChampionContentWithCountsAndNormalTimeLimit(
	ctx context.Context,
	t *testing.T,
	normalTaskCount int,
	goldenTaskCount int,
	normalTaskTimeLimit int,
) tournamentFlowCatalog {
	t.Helper()
	require.GreaterOrEqual(t, normalTaskCount, domain.AssignmentReserveCount+1)
	require.GreaterOrEqual(t, goldenTaskCount, domain.AssignmentReserveCount+1)
	require.True(t, domain.IsValidTaskTimeLimit(normalTaskTimeLimit))
	catalog := tournamentFlowCatalog{
		flags:       make(map[uuid.UUID]string),
		goldenTasks: make(map[uuid.UUID]tournamentFlowGoldenTask),
	}
	for _, category := range []struct {
		name  string
		count int
	}{
		{name: "web", count: normalTaskCount},
		{name: "crypto", count: normalTaskCount},
		{name: "forensics", count: createToChampionBO3OnlyNormalTaskCount},
		{name: "reverse", count: normalTaskCount},
		{name: "pwn", count: createToChampionBO3OnlyNormalTaskCount},
	} {
		// Shared BO1+BO3 categories supply all six Swiss and both semifinal
		// exact-normal plans before the final draft reserves its reachable
		// branch. Each plan owns a primary plus two reserves. BO3-only
		// categories need one chain for the final draft.
		for range category.count {
			title := "create_to_champion_" + uuid.NewString()[:8]
			flag := "champion-" + uuid.NewString()[:8]
			var taskID uuid.UUID
			err := sharedPool.QueryRow(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'create to champion fixture', $2, 'easy', $3, $4, 'normal')
				RETURNING id`, title, category.name, normalTaskTimeLimit, flag).Scan(&taskID)
			require.NoError(t, err)
			catalog.flags[taskID] = flag
		}
	}
	for range goldenTaskCount {
		title := "create_to_champion_golden_" + uuid.NewString()[:8]
		flag := "golden-champion-" + uuid.NewString()[:8]
		description := "create to champion Golden fixture"
		taskURLValue := "https://tasks.example.test/" + title
		var taskID uuid.UUID
		var taskVersion int
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind, task_url)
			VALUES ($1, $2, 'web', 'easy', 180, $3, 'golden', $4)
			RETURNING id, current_version`, title, description, flag, taskURLValue).Scan(&taskID, &taskVersion)
		require.NoError(t, err)
		catalog.flags[taskID] = flag
		catalog.goldenTasks[taskID] = tournamentFlowGoldenTask{
			description: description,
			taskURL:     &taskURLValue,
			version:     taskVersion,
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
	catalog.revision = currentTaskPoolPublicationRevision(ctx, t)
	rows, err := sharedPool.Query(ctx, `SELECT id, flag FROM tasks`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var taskID uuid.UUID
		var flag string
		require.NoError(t, rows.Scan(&taskID, &flag))
		catalog.flags[taskID] = flag
	}
	require.NoError(t, rows.Err())
	return catalog
}

func newTournamentFlowRESTFixture(t *testing.T) *restFixture {
	t.Helper()
	database := newDatabaseFixture()
	tx := database.mgr
	clock := newTournamentFlowClock(time.Now().UTC())
	redis := sharedRedis(t)
	auth := authusecase.NewUseCase(authusecase.Config{
		AccessTTL: 15 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
	}, clock, redisadapter.NewRevocationRedis(redis.client, "integration:tournament-flow:"+uniq("auth")+":"),
		authadapter.NewJWTCodec(authadapter.JWTConfig{
			Secret: []byte("01234567890123456789012345678901"), Now: clock.Now,
		}), authadapter.NewPasswordVerifier([]byte(restAdminPassword)))

	legacyCatalog := catalogusecase.NewTournamentUseCase(catalogrepo.NewTournamentCatalogPostgres(tx), clock)
	ids, err := catalogusecase.NewDeterministicIDGenerator(
		uuid.NewSHA1(uuid.NameSpaceOID, []byte("task-per-minute:tournament-commands")),
	)
	require.NoError(t, err)
	receipts := redisadapter.NewCommandReceiptStore(redis.client, 30*time.Second, 24*time.Hour, 30*time.Second)
	catalog := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, Lister: legacyCatalog,
		CreateStore: creationrepo.NewProductionTournamentCreatePostgres(tx), Receipts: receipts,
		ContentReader: configurationrepo.NewTournamentContentPostgres(tx),
	})
	rosterRepository := rosterrepo.NewTournamentAdminRosterPostgres(tx)
	roster := tournamentadminroster.NewRosterWorkflow(tournamentadminroster.RosterWorkflowDependencies{
		Transactions: tx,
		Repository:   rosterRepository,
		RuntimeHealth: tournamentadminroster.PreflightRuntimeHealthSourceFunc(func(context.Context) tournamentpreflight.RuntimeHealth {
			now := clock.Now()
			return tournamentpreflight.RuntimeHealth{
				TaskDelivery: tournamentpreflight.ComponentHealth{Healthy: true, Revision: "integration-task-delivery"},
				Realtime:     tournamentpreflight.ComponentHealth{Healthy: true, Revision: "integration-realtime"},
				Clock: tournamentpreflight.ClockHealth{
					ObservedAt: now, ReferenceAt: now, MaxSkew: time.Second,
				},
				ClockSampled: true,
				Dependencies: []tournamentpreflight.DependencyHealth{
					{Name: tournamentpreflight.DependencyRedis, Healthy: true, Revision: "integration-redis"},
					{Name: tournamentpreflight.DependencyObjectStorage, Healthy: true, Revision: "integration-object-storage"},
				},
			}
		}),
	})
	executionRepository := executionrepo.NewRepository(tx, resultauthority.FinalizeProjection)
	authorityRepository := authorityrepo.NewExecutionAuthorityPostgres(tx)
	authorityController, err := authorityusecase.NewController(
		authorityRepository,
		authorityRepository,
		authorityusecase.ControllerConfig{HolderID: uuid.New()},
	)
	require.NoError(t, err)
	execution := tournamentadminexecution.NewExecutionWorkflow(tournamentadminexecution.ExecutionWorkflowDependencies{
		Transactions: tx,
		Repository:   executionRepository,
		NormalPause:  executionRepository,
		WaveStart:    gamestart.NewStartUseCase(executionRepository, clock),
		Authority:    authorityController,
	})

	progressionRepository := progressionrepo.NewTournamentProgressionPostgres(tx)
	progression := tournamentprogression.NewWorkflow(tournamentprogression.ProgressionDependencies{
		Repository: progressionRepository, TerminalEvidence: progressionRepository,
		Transitioner: progressionRepository, Publisher: progressionRepository, ProgressionClock: clock,
	})
	lifecycleRepository := adminlifecyclerepo.NewTournamentAdminLifecyclePostgres(tx)
	lifecycle := tournamentadminlifecycle.NewLifecycleWorkflow(tournamentadminlifecycle.LifecycleWorkflowDependencies{
		Transactions: tx, Repository: lifecycleRepository,
		Transitions: tournamentlifecycle.NewTournamentLifecycleUseCase(
			tournamentlifecyclerepo.NewTournamentLifecyclePostgres(tx), clock,
		),
		Pauses: tournamentpause.NewTournamentPauseUseCase(tx, lifecycleRepository, clock),
		Cancellations: tournamentcancellation.NewTournamentCancellationUseCase(
			cancellationrepo.NewTournamentCancellationPostgres(tx), clock,
		),
		Progressions: progression, Clock: clock,
	})
	postseason := newTournamentFlowTerminalCoordinator(tx)
	resultPostgres := resultauthority.NewResultPostgres(tx)
	results := tournamentadminresult.NewOperatorResultWorkflow(tournamentadminresult.OperatorResultWorkflowDependencies{
		Transactions: tx,
		Repository: adminresultrepo.NewTournamentAdminResultPostgresWithDependencies(
			tx,
			resultPostgres,
			resultauthority.FinalizeProjection,
			wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
		),
		Postseason: postseason,
	})
	replay := tournamentadminreplay.NewReplayWorkflow(tournamentadminreplay.ReplayWorkflowDependencies{
		Transactions: tx,
		Repository:   adminreplayrepo.NewTournamentAdminReplayPostgres(tx),
	})
	admin := tournamentadmininbound.NewInboundAdapter(tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{
		Catalog: catalog, Roster: roster, Preflight: roster, Pairing: execution,
		Lifecycle: lifecycle, Wave: execution, Forfeit: results,
		Reserve: replay, Replay: replay,
		Audit:     auditrepo.NewTournamentAdminAuditPostgres(tx),
		Snapshots: snapshotrepo.NewTournamentAdminSnapshotPostgres(tx),
	}))
	configuration := tournamentadminconfiguration.NewTournamentConfigurationWorkflow(
		configurationrepo.NewProductionTournamentConfigurationPostgres(tx),
	)
	golden := goldenruntime.NewRuntimeApplication(runtimepostgres.NewGoldenRuntimePostgres(tx), clock)
	participantDrafts := draftrepo.NewDraftPostgres(tx)
	participantWave := waverepo.NewWavePostgres(tx)
	participantReadiness := readiness.NewReadinessUseCase(
		participantreadinessrepo.NewParticipantReadinessRepository(tx, participantWave), clock,
	)
	participantDraft := draftusecase.NewActionUseCase(
		participantdraftrepo.NewParticipantDraftRepositoryWithDependencies(
			tx, participantDrafts, loadTournamentFlowParticipantDraftContent,
		), clock,
	)
	participantSubmission := gamesubmission.NewSubmissionUseCase(
		participantsubmissionrepo.NewParticipantSubmissionRepository(tx, resultPostgres),
	)
	participantSettlement := participantsettlementrepo.NewParticipantSettlementWorkflow(
		participantsettlementrepo.NewParticipantSettlementRepositoryWithFinalizer(
			tx, resultPostgres, resultauthority.FinalizeProjection,
		),
	)
	participantSurrender := participantsurrenderrepo.NewParticipantSurrenderWorkflow(
		participantsurrenderrepo.NewParticipantForfeitRepository(tx, resultPostgres), clock,
	)
	participantPostSeries := tournamentparticipant.NewPostSeriesUseCase(
		participantpostseriesrepo.NewParticipantPostSeriesRepository(tx), clock,
	)
	participantCommands := tournamentparticipant.NewCommandCoordinator(tournamentparticipant.CommandCoordinatorDependencies{
		Transactions: tx,
		Authority:    participantauthorityrepo.NewTournamentParticipantPostgres(tx),
		Readiness:    participantReadiness,
		Draft:        participantDraft,
		Submission:   participantSubmission,
		Settlement:   participantSettlement,
		Surrender:    participantSurrender,
		PostSeries:   participantPostSeries,
		Postseason:   postseason,
	})
	participantApplication := tournamentparticipant.ParticipantNewUseCase(
		tournamentparticipant.ParticipantDependencies{
			Snapshots: tournamentsnapshotrepo.NewTournamentSnapshotPostgres(tx),
			States:    participantstaterepo.NewParticipantStatePostgres(tx),
			Commands:  participantCommands,
		},
	)
	participantIdempotent := tournamentparticipant.ParticipantNewIdempotentService(
		participantApplication, idempotency.NewCoordinator(receipts),
	)
	participantObserved := tournamentparticipant.ParticipantNewObservedService(participantIdempotent, clock, nil)
	limiter := tournamentFlowLimiter{}
	server := restv1.New(restv1.Dependencies{
		Players: playerusecase.SessionNewUseCase(database.mgr, database.players, clock), AdminAuth: auth,
		Tournaments: catalog, TournamentAdmin: admin, TournamentConfiguration: configuration,
		TournamentSnapshots:   tournamentsnapshotrepo.NewTournamentSnapshotPostgres(tx),
		TournamentParticipant: participantObserved, ParticipantArchive: tournamentFlowParticipantArchive{}, Golden: golden,
		LoginLimiter: limiter, JoinLimiter: limiter,
		PublicTournamentReadLimiter: limiter, OperatorTournamentReadLimiter: limiter,
		OperatorTournamentMutationLimiter: limiter, ParticipantTournamentReadLimiter: limiter,
		ParticipantTournamentMutationLimiter: limiter,
	})
	fixture := &restFixture{
		databaseFixture: database,
		handler: middleware.NoStoreSensitiveResponses()(
			restv1.NewHandler(server, restv1.HandlerOptions{AdminAuth: auth, PlayerRepo: database.players}),
		),
		auth: auth, validator: newOpenAPIResponseValidator(t),
	}
	snapshotSource, err := inboundws.NewTournamentProductionSnapshotSource(
		tournamentsnapshotrepo.NewTournamentSnapshotPostgres(tx), golden,
	)
	require.NoError(t, err)
	participantFlow, err := inboundws.NewTournamentParticipantFlow(snapshotSource)
	require.NoError(t, err)
	publicFlow, err := inboundws.NewTournamentPublicFlow(
		snapshotSource,
		&tournamentws.PublicRealtimeConfig{MaxConnections: 8},
	)
	require.NoError(t, err)
	operatorFlow, err := inboundws.NewTournamentOperatorFlow(snapshotSource)
	require.NoError(t, err)
	webSocket := inboundws.NewServer(
		database.players,
		inboundws.WithTournamentParticipantFlow(participantFlow),
		inboundws.WithTournamentPublicFlow(publicFlow),
		inboundws.WithTournamentOperatorFlow(operatorFlow),
		inboundws.WithTournamentOperatorSessionResolver(tournamentFlowOperatorSessionResolver(auth)),
	)
	tournamentFlowRuntimes.Store(fixture, tournamentFlowRuntime{clock: clock, golden: golden, webSocket: webSocket})
	t.Cleanup(func() { tournamentFlowRuntimes.Delete(fixture) })
	t.Cleanup(func() { webSocket.Shutdown(context.Background()) })
	return fixture
}

func tournamentFlowOperatorSessionResolver(
	auth *authusecase.UseCase,
) inboundws.TournamentOperatorSessionResolver {
	return func(request *http.Request, tournamentID uuid.UUID) (inboundws.TournamentOperatorSession, bool) {
		if auth == nil || request == nil || tournamentID == uuid.Nil {
			return inboundws.TournamentOperatorSession{}, false
		}
		token, ok := middleware.AdminAccessTokenFromRequest(request)
		if !ok {
			return inboundws.TournamentOperatorSession{}, false
		}
		claims, err := auth.VerifyAccess(request.Context(), token)
		if err != nil || claims == nil {
			return inboundws.TournamentOperatorSession{}, false
		}
		subject := strings.TrimSpace(claims.Subject)
		if subject == "" {
			return inboundws.TournamentOperatorSession{}, false
		}
		principalID, err := inbound.OperatorActorID(subject)
		if err != nil {
			return inboundws.TournamentOperatorSession{}, false
		}
		expiresAt, jti := claims.ExpiresAt, claims.JTI
		return inboundws.TournamentOperatorSession{
			Principal: tournamentws.OperatorRealtimePrincipal{
				Authenticated: true,
				PrincipalID:   principalID,
				Role:          tournamentws.OperatorRealtimeRole,
				TournamentID:  tournamentID,
			},
			ExpiresAt: expiresAt,
			Validate: func(ctx context.Context) bool {
				current, verifyErr := auth.VerifyAccess(ctx, token)
				if verifyErr != nil || current == nil || current.JTI != jti ||
					strings.TrimSpace(current.Subject) != subject || !current.ExpiresAt.Equal(expiresAt) {
					return false
				}
				currentPrincipalID, actorErr := inbound.OperatorActorID(current.Subject)
				return actorErr == nil && currentPrincipalID == principalID
			},
		}, true
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
	return createTournamentWithRosterSizeThroughREST(t, fixture, adminToken, contentRevision, name, 4)
}

func createTournamentWithRosterSizeThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	contentRevision int64,
	name string,
	rosterSize int,
) api.Tournament {
	t.Helper()
	body := fmt.Sprintf(`{
		"preset":"tournament_v1",
		"expected_revision":0,
		"name":%q,
		"public_id":%q,
		"planned_roster_size":%d,
		"content_revision":%d
	}`, "Create to champion "+name, "create-to-champion-"+uuid.NewString()[:8], rosterSize, contentRevision)
	req, resp := doTournamentFlowJSON(
		t, fixture, http.MethodPost, "/api/v1/admin/tournaments", body, adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	created := decodeJSON[api.Tournament](t, resp)
	require.Equal(t, api.TournamentState(domain.TournamentStateDraft), created.State)
	require.EqualValues(t, rosterSize, created.PlannedRosterSize)
	return created
}

func tournamentAdminSnapshotThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
) api.OperatorRecoverySnapshot {
	t.Helper()
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/snapshot"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.OperatorRecoverySnapshot](t, resp)
}

func openRegistrationThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	projectionRevision int64,
) api.Tournament {
	t.Helper()
	return applyTournamentActionThroughREST(
		t, fixture, adminToken, tournamentID, projectionRevision,
		"open_registration", uuid.New(),
	)
}

func replaceTournamentRosterThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	players []tournamentFlowPlayer,
) api.Roster {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	participants := make([]api.RosterParticipantInput, len(players))
	for index, player := range players {
		participants[index] = api.RosterParticipantInput{
			PlayerId: player.id, Seed: int32(index + 1), Attendance: api.CheckedIn,
		}
	}
	body, err := json.Marshal(api.ReplaceRosterRequest{
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision,
		Participants:               participants,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/roster"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPut, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	roster := decodeJSON[api.Roster](t, resp)
	require.Len(t, roster.Participants, len(players))
	for _, participant := range roster.Participants {
		require.Equal(t, api.CheckedIn, participant.Attendance)
	}
	return roster
}

func runTournamentRosterPreflightThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
) api.PreflightReport {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	body, err := json.Marshal(api.PreflightRequest{ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/roster/preflight"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	report := decodeJSON[api.PreflightReport](t, resp)
	require.True(t, report.Passed, "preflight report: %+v", report)
	return report
}

func lockTournamentRosterThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	roster api.Roster,
	report api.PreflightReport,
) api.Roster {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	playerIDs := make([]uuid.UUID, len(roster.Participants))
	for index, participant := range roster.Participants {
		playerIDs[index] = participant.PlayerId
	}
	body, err := json.Marshal(api.LockRosterRequest{
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision,
		PreflightRevisionId:        report.Id,
		CheckedInPlayerIds:         playerIDs,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/roster/lock"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	locked := decodeJSON[api.Roster](t, resp)
	require.True(t, locked.Locked)
	require.False(t, locked.ExecutionStarted)
	return locked
}

func startSwissThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
) api.Tournament {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	return applyTournamentActionThroughREST(
		t, fixture, adminToken, tournamentID, snapshot.NextCursor.ProjectionRevision,
		"start_swiss", uuid.New(),
	)
}

func runProductionSwissThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	players []tournamentFlowPlayer,
	flags map[uuid.UUID]string,
	withGolden bool,
) {
	t.Helper()
	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}

	for roundNumber := 1; roundNumber <= 3; roundNumber++ {
		snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		round := configureProductionSwissPairingsThroughREST(
			t, fixture, adminToken, tournamentID, snapshot.NextCursor.ProjectionRevision, roundNumber,
		)
		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		wave := findProductionSwissWave(t, snapshot, round)

		wave = controlProductionWaveThroughREST(
			t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
			api.WaveControlRequestActionOpenReadyWindow,
		)
		require.Equal(t, api.WaveStateReadyWindowOpen, wave.State)

		playersByParticipant := productionPlayersByParticipant(t, snapshot.Roster, playersByID)
		orderedParticipants := make([]uuid.UUID, 0, len(playersByParticipant))
		for participantID := range playersByParticipant {
			orderedParticipants = append(orderedParticipants, participantID)
		}
		slices.SortFunc(orderedParticipants, func(first, second uuid.UUID) int {
			return strings.Compare(first.String(), second.String())
		})
		for _, member := range wave.Members {
			player, ok := playersByParticipant[member.ParticipantId]
			require.True(t, ok, "missing player for participant %s", member.ParticipantId)
			participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
			body, err := json.Marshal(api.ParticipantReadyRequest{
				ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
				Ready:                      true,
			})
			require.NoError(t, err)
			path := "/api/v1/tournaments/" + tournamentID.String() +
				"/participant/waves/" + wave.Id.String() + "/ready"
			req, resp := doTournamentFlowJSON(
				t, fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
			)
			require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			fixture.validateResponse(t, req, resp)
			ready := decodeJSON[api.ReadinessEvent](t, resp)
			require.Equal(t, wave.Id, ready.WaveId)
		}

		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		wave = findProductionWaveByID(t, snapshot, wave.Id)
		wave = controlProductionWaveThroughREST(
			t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
			api.WaveControlRequestActionStart,
		)
		require.Equal(t, api.WaveStateActive, wave.State)
		settleProductionSwissWaveThroughREST(
			t, fixture, tournamentID, wave, playersByParticipant, flags,
			func(series api.Series) uuid.UUID {
				return productionSwissWinner(t, series, orderedParticipants, withGolden)
			},
		)

		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		wave = findProductionWaveByID(t, snapshot, wave.Id)
		if wave.State != api.WaveStateCompleted {
			wave = controlProductionWaveThroughREST(
				t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
				api.WaveControlRequestActionComplete,
			)
		}
		require.Equal(t, api.WaveStateCompleted, wave.State)
	}
}

func configureProductionSwissPairingsThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	projectionRevision int64,
	roundNumber int,
) api.SwissRound {
	t.Helper()
	body, err := json.Marshal(api.PairingConfigurationRequest{
		Categories:                 []api.Category{api.CategoryWeb, api.CategoryCrypto, api.CategoryForensics},
		CategoryMode:               api.CategoryModeRandom,
		ExpectedProjectionRevision: projectionRevision,
		PairingMode:                api.Automatic,
		RoundNumber:                int32(roundNumber),
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/pairings"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	round := decodeJSON[api.SwissRound](t, resp)
	require.EqualValues(t, roundNumber, round.RoundNumber)
	require.NotEmpty(t, round.Pairings)
	return round
}

func controlProductionWaveThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	projectionRevision int64,
	action api.WaveControlRequestAction,
) api.Wave {
	t.Helper()
	body, err := json.Marshal(api.WaveControlRequest{
		Action:                     action,
		Confirmed:                  true,
		ExpectedProjectionRevision: projectionRevision,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/waves/" + waveID.String() + "/actions"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	wave := decodeJSON[api.Wave](t, resp)
	require.Equal(t, waveID, wave.Id)
	return wave
}

func findProductionSwissWave(t *testing.T, snapshot api.OperatorRecoverySnapshot, round api.SwissRound) api.Wave {
	t.Helper()
	seriesByPair := make(map[string]uuid.UUID, len(snapshot.Series))
	for _, series := range snapshot.Series {
		seriesByPair[productionParticipantPairKey(series.FirstParticipantId, series.SecondParticipantId)] = series.Id
	}
	seriesIDs := make(map[uuid.UUID]struct{}, len(round.Pairings))
	for _, pairing := range round.Pairings {
		seriesID, ok := seriesByPair[productionParticipantPairKey(pairing.FirstParticipantId, pairing.SecondParticipantId)]
		require.True(t, ok, "pairing %s has no materialized series", pairing.Id)
		seriesIDs[seriesID] = struct{}{}
	}
	for _, wave := range snapshot.Waves {
		if len(wave.Members) != len(seriesIDs)*2 {
			continue
		}
		seen := make(map[uuid.UUID]struct{}, len(seriesIDs))
		for _, member := range wave.Members {
			if member.SeriesId != nil {
				seen[*member.SeriesId] = struct{}{}
			}
		}
		if len(seen) != len(seriesIDs) {
			continue
		}
		matched := true
		for seriesID := range seriesIDs {
			if _, ok := seen[seriesID]; !ok {
				matched = false
				break
			}
		}
		if matched {
			return wave
		}
	}
	require.FailNow(t, "pairing has no materialized wave")
	return api.Wave{}
}

func findProductionWaveByID(t *testing.T, snapshot api.OperatorRecoverySnapshot, waveID uuid.UUID) api.Wave {
	t.Helper()
	for _, wave := range snapshot.Waves {
		if wave.Id == waveID {
			return wave
		}
	}
	require.FailNow(t, "materialized wave disappeared from operator snapshot")
	return api.Wave{}
}

func productionParticipantPairKey(first, second uuid.UUID) string {
	if second.String() < first.String() {
		first, second = second, first
	}
	return first.String() + ":" + second.String()
}

func productionPlayersByParticipant(
	t *testing.T,
	roster api.Roster,
	playersByID map[uuid.UUID]tournamentFlowPlayer,
) map[uuid.UUID]tournamentFlowPlayer {
	t.Helper()
	players := make(map[uuid.UUID]tournamentFlowPlayer, len(roster.Participants))
	for _, participant := range roster.Participants {
		player, ok := playersByID[participant.PlayerId]
		require.True(t, ok, "missing player %s in roster", participant.PlayerId)
		players[participant.Id] = player
	}
	return players
}

func participantSnapshotThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
) api.ParticipantRecoverySnapshot {
	t.Helper()
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/snapshot"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodGet, path, "", cookieSession(player.session.String()), uuid.Nil, "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.ParticipantRecoverySnapshot](t, resp)
}

func settleProductionSwissWaveThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	wave api.Wave,
	playersByParticipant map[uuid.UUID]tournamentFlowPlayer,
	flags map[uuid.UUID]string,
	winner func(api.Series) uuid.UUID,
) {
	t.Helper()
	settled := make(map[uuid.UUID]struct{})
	for _, member := range wave.Members {
		if member.SeriesId == nil {
			continue
		}
		seriesID := *member.SeriesId
		if _, ok := settled[seriesID]; ok {
			continue
		}
		settled[seriesID] = struct{}{}
		var series api.Series
		for _, seriesMember := range wave.Members {
			if seriesMember.SeriesId == nil || *seriesMember.SeriesId != seriesID {
				continue
			}
			player, ok := playersByParticipant[seriesMember.ParticipantId]
			require.True(t, ok, "missing player for participant %s", seriesMember.ParticipantId)
			participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
			require.NotNil(t, participant.Series, "participant %s has no series", seriesMember.ParticipantId)
			series = *participant.Series
			break
		}
		require.Equal(t, seriesID, series.Id)
		winnerID := winner(series)
		loserID := series.FirstParticipantId
		if winnerID == loserID {
			loserID = series.SecondParticipantId
		}
		for _, participantID := range []uuid.UUID{loserID, winnerID} {
			player, ok := playersByParticipant[participantID]
			require.True(t, ok, "missing player for participant %s", participantID)
			participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
			require.Equal(t, participantID, participant.Lobby.ParticipantId)
			require.Equal(t, api.CheckedIn, participant.Lobby.Attendance)
			require.NotNil(t, participant.Lobby.CurrentSwissRound)
			require.GreaterOrEqual(t, participant.Lobby.SwissPoints, int32(0))
			require.Equal(t, api.ParticipantLobbyStatusAssigned, participant.Lobby.Status)
			require.Equal(t, api.ParticipantLobbyRequiredActionPlay, participant.Lobby.RequiredAction)
			require.NotNil(t, participant.Assignment, "participant %s has no assignment", participantID)
			require.NotNil(t, participant.Series, "participant %s has no series", participantID)
			gameID := productionGameForAttempt(t, *participant.Series, participant.Assignment.AttemptId)
			flag, ok := flags[participant.Assignment.ActiveSnapshot.TaskId]
			require.True(t, ok, "missing retained flag for task %s", participant.Assignment.ActiveSnapshot.TaskId)
			if participantID != winnerID {
				flag = "incorrect-" + uuid.NewString()
			}
			body, err := json.Marshal(api.ParticipantSubmissionRequest{
				ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
				SubmittedFlag:              &flag,
			})
			require.NoError(t, err)
			path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/series/" +
				seriesID.String() + "/games/" + gameID.String() + "/submissions"
			req, resp := doTournamentFlowJSON(
				t, fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
			)
			require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
			fixture.validateResponse(t, req, resp)
			submission := decodeJSON[api.ParticipantSubmissionResponse](t, resp)
			require.Equal(t, participantID, submission.Submission.ParticipantId)
			require.Equal(t, participantID == winnerID, submission.Submission.Correct)
		}
	}
}

func productionSwissWinner(
	t *testing.T,
	series api.Series,
	orderedParticipants []uuid.UUID,
	withGolden bool,
) uuid.UUID {
	t.Helper()
	require.Len(t, orderedParticipants, 4)
	positions := make(map[uuid.UUID]int, len(orderedParticipants))
	for index, participantID := range orderedParticipants {
		positions[participantID] = index
	}
	first, firstOK := positions[series.FirstParticipantId]
	second, secondOK := positions[series.SecondParticipantId]
	require.True(t, firstOK && secondOK)
	if !withGolden {
		if first < second {
			return series.FirstParticipantId
		}
		return series.SecondParticipantId
	}
	if first == 3 {
		return series.SecondParticipantId
	}
	if second == 3 {
		return series.FirstParticipantId
	}
	if (first+1)%3 == second {
		return series.FirstParticipantId
	}
	return series.SecondParticipantId
}

func productionGameForAttempt(t *testing.T, series api.Series, attemptID uuid.UUID) uuid.UUID {
	t.Helper()
	for _, slot := range series.Slots {
		for _, attempt := range slot.Attempts {
			if attempt.Id == attemptID {
				return attempt.Id
			}
		}
	}
	require.FailNow(t, "assignment attempt is not materialized in participant snapshot")
	return uuid.Nil
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
	roster api.Roster,
	players []tournamentFlowPlayer,
	catalog tournamentFlowCatalog,
) {
	t.Helper()
	ctx := context.Background()
	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	// Start exactly one Golden window behind wall time. Advancing to the
	// 180-second deadline then remains valid PostgreSQL time while also keeping
	// the finalized Golden revision after its already-published seed revision.
	runtime.clock.FreezeAt(time.Now().UTC().Add(-180 * time.Second))
	root := "/api/v1/admin/tournaments/" + tournamentID.String() + "/golden"
	body, err := json.Marshal(api.GoldenOpenRequest{
		ExpectedProjectionRevision: projectionRevision,
		ExpectedRuntimeRevision:    0,
	})
	require.NoError(t, err)
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, root+"/open",
		string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	operator := decodeJSON[api.GoldenOperatorResponse](t, resp)
	require.NotEmpty(t, operator.Groups)

	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}
	playersByParticipant := productionPlayersByParticipant(t, roster, playersByID)
	for _, group := range operator.Groups {
		require.Greater(t, len(group.Members), 1, "Golden recovery needs unresolved reserve members")
	}

	for _, group := range operator.Groups {
		for _, member := range group.Members {
			player, ok := playersByParticipant[member.ParticipantId]
			require.True(t, ok, "missing player for Golden participant %s", member.ParticipantId)
			participant := goldenParticipantThroughREST(t, fixture, tournamentID, player)
			require.Equal(t, group.AttemptId, participant.AttemptId)
			require.Equal(t, group.ReadyWindowId, participant.ReadyWindowId)
			ready := setGoldenParticipantReadyThroughREST(t, fixture, tournamentID, player, participant)
			require.True(t, ready.Ready)
			require.Nil(t, ready.Task, "Golden task must remain secret before attempt start")
		}
	}

	beforeStart := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	for _, group := range beforeStart.Groups {
		require.Equal(t, api.GoldenRuntimeState("ready"), group.State)
		for _, member := range group.Members {
			player := playersByParticipant[member.ParticipantId]
			participant := goldenParticipantThroughREST(t, fixture, tournamentID, player)
			require.Nil(t, participant.Task, "Golden task must remain secret until start commits")
		}
	}

	activeGroups := make([]api.GoldenOperatorGroup, 0, len(beforeStart.Groups))
	for _, group := range beforeStart.Groups {
		started := startGoldenAttemptThroughREST(t, fixture, adminToken, tournamentID, group)
		active := goldenOperatorGroupByID(t, started, group.GroupId)
		require.Equal(t, api.GoldenRuntimeState("active"), active.State)
		require.NotNil(t, active.StartedAt)
		require.NotNil(t, active.Deadline)
		require.Equal(t, active.StartedAt.Add(180*time.Second), *active.Deadline)
		activeGroups = append(activeGroups, active)
	}

	solvers := make(map[uuid.UUID]struct{}, len(activeGroups))
	for _, group := range activeGroups {
		solver := group.Members[0]
		player := playersByParticipant[solver.ParticipantId]
		participant := goldenParticipantThroughREST(t, fixture, tournamentID, player)
		require.NotNil(t, participant.Task)
		require.EqualValues(t, 180, participant.Task.TimeLimitSeconds)
		expected, ok := catalog.goldenTasks[participant.Task.TaskId]
		require.True(t, ok, "missing immutable Golden task fixture %s", participant.Task.TaskId)
		assertGoldenParticipantTaskContentThroughREST(t, fixture, tournamentID, player, expected)
		flag, ok := catalog.flags[participant.Task.TaskId]
		require.True(t, ok, "missing catalog flag for Golden task %s", participant.Task.TaskId)
		submitted := submitGoldenParticipantThroughREST(t, fixture, tournamentID, player, participant, flag)
		require.True(t, submitted.Submitted)
		solvers[solver.ParticipantId] = struct{}{}
	}
	partial := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	for _, group := range activeGroups {
		current := goldenOperatorGroupByID(t, partial, group.GroupId)
		for _, member := range current.Members {
			_, solved := solvers[member.ParticipantId]
			require.Equal(t, solved, member.Submitted)
		}
	}

	latestDeadline := *activeGroups[0].Deadline
	for _, group := range activeGroups[1:] {
		if group.Deadline.After(latestDeadline) {
			latestDeadline = *group.Deadline
		}
	}
	runtime.clock.AdvanceTo(latestDeadline.Add(time.Nanosecond))
	require.NoError(t, runtime.golden.Recover(ctx, tournamentID))

	recovered := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	reserveGroups := make([]api.GoldenOperatorGroup, 0, len(activeGroups))
	for _, group := range activeGroups {
		reserve := goldenOperatorGroupByID(t, recovered, group.GroupId)
		require.Equal(t, api.GoldenRuntimeState("prepared"), reserve.State)
		require.NotEqual(t, group.AttemptId, reserve.AttemptId)
		require.Len(t, reserve.Members, len(group.Members)-1)
		for _, member := range reserve.Members {
			_, solved := solvers[member.ParticipantId]
			require.False(t, solved, "solved participant must not enter reserve continuation")
		}
		reserveGroups = append(reserveGroups, reserve)
	}
	// Replaying recovery through the same runtime composition must leave the
	// reserve continuation identity and member set unchanged.
	require.NoError(t, runtime.golden.Recover(ctx, tournamentID))
	replayed := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	for _, reserve := range reserveGroups {
		require.Equal(t, reserve, goldenOperatorGroupByID(t, replayed, reserve.GroupId))
	}

	for _, reserve := range reserveGroups {
		for _, member := range reserve.Members {
			player := playersByParticipant[member.ParticipantId]
			participant := goldenParticipantThroughREST(t, fixture, tournamentID, player)
			require.Equal(t, reserve.AttemptId, participant.AttemptId)
			ready := setGoldenParticipantReadyThroughREST(t, fixture, tournamentID, player, participant)
			require.Nil(t, ready.Task, "reserve task must remain secret before reserve start")
		}
	}
	reserveReady := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	activeReserves := make([]api.GoldenOperatorGroup, 0, len(reserveGroups))
	for _, reserve := range reserveGroups {
		current := goldenOperatorGroupByID(t, reserveReady, reserve.GroupId)
		require.Equal(t, api.GoldenRuntimeState("ready"), current.State)
		started := startGoldenAttemptThroughREST(t, fixture, adminToken, tournamentID, current)
		active := goldenOperatorGroupByID(t, started, reserve.GroupId)
		require.Equal(t, api.GoldenRuntimeState("active"), active.State)
		activeReserves = append(activeReserves, active)
	}

	for _, group := range activeReserves {
		for index, member := range group.Members {
			player := playersByParticipant[member.ParticipantId]
			participant := goldenParticipantThroughREST(t, fixture, tournamentID, player)
			require.NotNil(t, participant.Task)
			expected, ok := catalog.goldenTasks[participant.Task.TaskId]
			require.True(t, ok, "missing immutable reserve Golden task fixture %s", participant.Task.TaskId)
			assertGoldenParticipantTaskContentThroughREST(t, fixture, tournamentID, player, expected)
			flag, ok := catalog.flags[participant.Task.TaskId]
			require.True(t, ok, "missing catalog flag for reserve Golden task %s", participant.Task.TaskId)
			finished := submitGoldenParticipantThroughREST(t, fixture, tournamentID, player, participant, flag)
			require.True(t, finished.Submitted)
			if index == len(group.Members)-1 {
				require.Equal(t, api.GoldenRuntimeState("completed"), finished.State)
			}
		}
	}
	finished := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	for _, group := range activeGroups {
		final := goldenOperatorGroupByID(t, finished, group.GroupId)
		require.Equal(t, api.GoldenRuntimeState("completed"), final.State)
		require.NotEqual(t, group.AttemptId, final.AttemptId)
	}
	// Recovery advances a frozen application clock through the full Golden
	// deadline without sleeping. Later stages resume the production wall clock.
	runtime.clock.Resume()
}

func tournamentFlowRuntimeForFixture(t testing.TB, fixture *restFixture) tournamentFlowRuntime {
	t.Helper()
	value, ok := tournamentFlowRuntimes.Load(fixture)
	require.True(t, ok, "Golden runtime fixture composition is not registered")
	runtime, ok := value.(tournamentFlowRuntime)
	require.True(t, ok, "invalid Golden runtime fixture composition")
	require.NotNil(t, runtime.clock)
	require.NotNil(t, runtime.golden)
	require.NotNil(t, runtime.webSocket)
	return runtime
}

func goldenOperatorThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
) api.GoldenOperatorResponse {
	t.Helper()
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/golden"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.GoldenOperatorResponse](t, resp)
}

func goldenOperatorGroupByID(
	t testing.TB,
	operator api.GoldenOperatorResponse,
	groupID uuid.UUID,
) api.GoldenOperatorGroup {
	t.Helper()
	for _, group := range operator.Groups {
		if group.GroupId == groupID {
			return group
		}
	}
	require.FailNow(t, "Golden group not found", groupID.String())
	return api.GoldenOperatorGroup{}
}

func goldenParticipantThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
) api.GoldenParticipantResponse {
	t.Helper()
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/golden"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodGet, path, "", cookieSession(player.session.String()), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.GoldenParticipantResponse](t, resp)
}

type goldenParticipantTaskWire struct {
	Description string  `json:"description"`
	TaskURL     *string `json:"task_url"`
	Version     int     `json:"version"`
}

type goldenParticipantWire struct {
	Task *goldenParticipantTaskWire `json:"task"`
}

func assertGoldenParticipantTaskContentThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
	expected tournamentFlowGoldenTask,
) {
	t.Helper()
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/golden"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodGet, path, "", cookieSession(player.session.String()), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	var payload goldenParticipantWire
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	require.NotNil(t, payload.Task)
	require.Equal(t, expected.description, payload.Task.Description)
	require.Equal(t, expected.version, payload.Task.Version)
	if expected.taskURL == nil {
		require.Nil(t, payload.Task.TaskURL)
		return
	}
	require.NotNil(t, payload.Task.TaskURL)
	require.Equal(t, *expected.taskURL, *payload.Task.TaskURL)
}

func setGoldenParticipantReadyThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
	participant api.GoldenParticipantResponse,
) api.GoldenParticipantResponse {
	t.Helper()
	body, err := json.Marshal(api.GoldenReadyRequest{
		AttemptId:               participant.AttemptId,
		ExpectedRuntimeRevision: participant.RuntimeRevision,
		Ready:                   true,
		ReadyWindowId:           participant.ReadyWindowId,
	})
	require.NoError(t, err)
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/golden/ready"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body),
		cookieSession(player.session.String()), uuid.New(), player.csrf)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.GoldenParticipantResponse](t, resp)
}

func startGoldenAttemptThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	group api.GoldenOperatorGroup,
) api.GoldenOperatorResponse {
	t.Helper()
	body, err := json.Marshal(api.GoldenStartRequest{
		ExpectedRuntimeRevision: group.RuntimeRevision,
		ReadyWindowId:           group.ReadyWindowId,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/golden/attempts/" + group.AttemptId.String() + "/start"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.GoldenOperatorResponse](t, resp)
}

func submitGoldenParticipantThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
	participant api.GoldenParticipantResponse,
	flag string,
) api.GoldenParticipantResponse {
	t.Helper()
	body, err := json.Marshal(api.GoldenSubmissionRequest{
		AttemptId:               participant.AttemptId,
		ExpectedRuntimeRevision: participant.RuntimeRevision,
		ReadyWindowId:           participant.ReadyWindowId,
		SubmittedFlag:           flag,
	})
	require.NoError(t, err)
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/golden/submissions"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodPost, path, string(body),
		cookieSession(player.session.String()), uuid.New(), player.csrf)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	return decodeJSON[api.GoldenParticipantResponse](t, resp)
}

func newTournamentFlowTerminalCoordinator(tx *postgres.TxManager) *playoff.TerminalCoordinator {
	drafts := draftrepo.NewDraftPostgres(tx)
	exactPlans := exactdraftrepo.NewExactDraftBranchPlanPostgres(tx, drafts)
	planner := playoff.NewFinalDraftAssignmentService(
		assignmentusecase.NewExactDraftBranchPlanUseCase(exactPlans), exactPlans, exactPlans,
	)
	return playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository: playoffrepo.NewPlayoffTerminalPostgres(
			tx, drafts, assignmentrepo.NewAssignmentPostgres(tx).CreateAssignmentTx,
		),
		Publisher: projectionrepo.NewProjectionPostgres(tx), DraftPlanner: planner, Rehydrator: planner,
	})
}

func completeTournamentPlayoffsThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	roster api.Roster,
	players []tournamentFlowPlayer,
	flags map[uuid.UUID]string,
) {
	t.Helper()
	playersByID := make(map[uuid.UUID]tournamentFlowPlayer, len(players))
	for _, player := range players {
		playersByID[player.id] = player
	}
	playersByParticipant := productionPlayersByParticipant(t, roster, playersByID)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	semifinals := productionPlayoffSemifinals(t, snapshot)
	for _, semifinal := range semifinals {
		runProductionPlayoffSeriesThroughREST(
			t, fixture, adminToken, tournamentID, semifinal.Id, playersByParticipant, flags,
		)
	}
	draft, ok := productionFinalDraftThroughREST(t, fixture, tournamentID, playersByParticipant)
	require.True(t, ok, "production final draft is not visible to a participant")
	require.NotNil(t, draft.TurnDeadline)
	// Exact branch reservation is intentionally exhaustive and becomes much
	// slower under the race detector. Hold the integration clock inside the
	// first 15-second turn while the test submits the deterministic draft.
	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	runtime.clock.FreezeAt(draft.TurnDeadline.Add(-time.Second))
	completeProductionFinalDraftThroughREST(t, fixture, tournamentID, draft, playersByParticipant)
	finalWave := productionPlayoffWaveBySeries(
		t, tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID), draft.SeriesId,
	)
	runtime.clock.Resume()
	waitForTournamentFlowDatabaseClock(t, finalWave.Id)
	runProductionPlayoffSeriesThroughREST(
		t, fixture, adminToken, tournamentID, draft.SeriesId, playersByParticipant, flags,
	)
}

func waitForTournamentFlowDatabaseClock(t *testing.T, waveID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var databaseNow, readinessCreatedAt time.Time
		err := sharedPool.QueryRow(context.Background(), `
			SELECT clock_timestamp(), max(created_at)
			FROM wave_readiness
			WHERE wave_id = $1`, waveID).Scan(&databaseNow, &readinessCreatedAt)
		require.NoError(t, err)
		if !databaseNow.Before(readinessCreatedAt) {
			return
		}
		if !time.Now().Before(deadline) {
			require.FailNowf(t, "database clock did not reach final Wave readiness timestamp",
				"database=%s readiness=%s", databaseNow, readinessCreatedAt)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func productionPlayoffSemifinals(t *testing.T, snapshot api.OperatorRecoverySnapshot) []api.Series {
	t.Helper()
	semifinals := make([]api.Series, 0, 2)
	for _, series := range snapshot.Series {
		if series.Format == api.Bo1 && series.State == api.SeriesStateReady && series.WinnerId == nil {
			semifinals = append(semifinals, series)
		}
	}
	require.Len(t, semifinals, 2, "production playoff publication must expose two executable semifinals")
	return semifinals
}

func runProductionPlayoffSeriesThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	playersByParticipant map[uuid.UUID]tournamentFlowPlayer,
	flags map[uuid.UUID]string,
) {
	t.Helper()
	for iteration := 0; iteration < 8; iteration++ {
		snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		series := productionSeriesByID(t, snapshot, seriesID)
		if series.State == api.SeriesStateCompleted {
			require.NotNil(t, series.WinnerId)
			return
		}
		wave := productionPlayoffWaveBySeries(t, snapshot, seriesID)
		if wave.State == api.WaveStatePlanned {
			wave = controlProductionWaveThroughREST(
				t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
				api.WaveControlRequestActionOpenReadyWindow,
			)
		}
		require.Contains(t, []api.WaveState{
			api.WaveStateReadyWindowOpen, api.WaveStateReady, api.WaveStateActive,
		}, wave.State)
		if wave.State == api.WaveStateReadyWindowOpen || wave.State == api.WaveStateReady {
			for _, member := range wave.Members {
				player, ok := playersByParticipant[member.ParticipantId]
				require.True(t, ok, "missing player for playoff participant %s", member.ParticipantId)
				participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
				body, err := json.Marshal(api.ParticipantReadyRequest{
					ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
					Ready:                      true,
				})
				require.NoError(t, err)
				path := "/api/v1/tournaments/" + tournamentID.String() +
					"/participant/waves/" + wave.Id.String() + "/ready"
				req, resp := doTournamentFlowJSON(
					t, fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
				)
				require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
				fixture.validateResponse(t, req, resp)
			}
			snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
			wave = findProductionWaveByID(t, snapshot, wave.Id)
			wave = controlProductionWaveThroughREST(
				t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
				api.WaveControlRequestActionStart,
			)
		}
		require.Equal(t, api.WaveStateActive, wave.State)
		settleProductionSwissWaveThroughREST(
			t, fixture, tournamentID, wave, playersByParticipant, flags,
			func(series api.Series) uuid.UUID { return series.FirstParticipantId },
		)
		snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
		if snapshot.Tournament.State == api.TournamentState(domain.TournamentStateCompleted) {
			series = productionSeriesByID(t, snapshot, seriesID)
			require.Equal(t, api.SeriesStateCompleted, series.State)
			require.NotNil(t, series.WinnerId)
			return
		}
		wave = findProductionWaveByID(t, snapshot, wave.Id)
		if wave.State != api.WaveStateCompleted {
			wave = controlProductionWaveThroughREST(
				t, fixture, adminToken, tournamentID, wave.Id, snapshot.NextCursor.ProjectionRevision,
				api.WaveControlRequestActionComplete,
			)
		}
	}
	require.FailNow(t, "production playoff series did not settle through materialized waves")
}

func productionSeriesByID(t *testing.T, snapshot api.OperatorRecoverySnapshot, seriesID uuid.UUID) api.Series {
	t.Helper()
	for _, series := range snapshot.Series {
		if series.Id == seriesID {
			return series
		}
	}
	require.FailNow(t, "production playoff series disappeared from operator snapshot", seriesID)
	return api.Series{}
}

func productionPlayoffWaveBySeries(t *testing.T, snapshot api.OperatorRecoverySnapshot, seriesID uuid.UUID) api.Wave {
	t.Helper()
	var completed *api.Wave
	for _, wave := range snapshot.Waves {
		for _, member := range wave.Members {
			if member.SeriesId == nil || *member.SeriesId != seriesID {
				continue
			}
			if wave.State != api.WaveStateCompleted {
				return wave
			}
			copy := wave
			completed = &copy
			break
		}
	}
	if completed != nil {
		return *completed
	}
	require.FailNow(t, "production playoff materializer must publish a wave for series", seriesID)
	return api.Wave{}
}

func productionFinalDraftThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	playersByParticipant map[uuid.UUID]tournamentFlowPlayer,
) (api.Draft, bool) {
	t.Helper()
	for _, player := range playersByParticipant {
		participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
		if participant.Draft != nil && participant.Draft.State == api.DraftStateActive {
			return *participant.Draft, true
		}
	}
	return api.Draft{}, false
}

func completeProductionFinalDraftThroughREST(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	draft api.Draft,
	playersByParticipant map[uuid.UUID]tournamentFlowPlayer,
) {
	t.Helper()
	for draft.State == api.DraftStateActive {
		actorID := draft.FirstParticipantId
		if draft.Turn%2 == 0 {
			actorID = draft.SecondParticipantId
		}
		player, ok := playersByParticipant[actorID]
		require.True(t, ok, "missing player for final draft actor %s", actorID)
		participant := participantSnapshotThroughREST(t, fixture, tournamentID, player)
		require.NotNil(t, participant.Draft)
		current := *participant.Draft
		require.Equal(t, draft.Id, current.Id)
		category := productionNextDraftCategory(t, current)
		action := api.Ban
		if current.Turn > 2 {
			action = api.Pick
		}
		body, err := json.Marshal(api.ParticipantDraftActionRequest{
			ExpectedProjectionRevision: participant.NextCursor.ProjectionRevision,
			ExpectedDraftRevision:      current.Revision,
			ExpectedTurn:               current.Turn,
			Action:                     action,
			Category:                   category,
		})
		require.NoError(t, err)
		path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/series/" +
			current.SeriesId.String() + "/draft/actions"
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodPost, path, string(body), cookieSession(player.session.String()), uuid.New(), player.csrf,
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
		draft = decodeJSON[api.Draft](t, resp)
	}
	require.Equal(t, api.DraftStateCompleted, draft.State)
}

func productionNextDraftCategory(t *testing.T, draft api.Draft) api.Category {
	t.Helper()
	used := make(map[api.Category]struct{}, len(draft.Actions))
	for _, action := range draft.Actions {
		used[action.Category] = struct{}{}
	}
	if draft.Turn <= 2 {
		// Keep the BO3-only categories in the two ban turns. The exact fixture
		// carries one chain for each of them, while the accepted final path
		// exercises the shared BO1+BO3 categories.
		for _, category := range []api.Category{api.CategoryPwn, api.CategoryForensics} {
			if _, ok := used[category]; ok || !slices.Contains(draft.Pool, category) {
				continue
			}
			return category
		}
	}
	for _, category := range draft.Pool {
		if _, ok := used[category]; !ok {
			return category
		}
	}
	require.FailNow(t, "final draft has no unused category")
	return api.CategoryWeb
}

func assertCompletedTournamentThroughREST(t *testing.T, fixture *restFixture, adminToken string, tournamentID uuid.UUID) {
	t.Helper()
	path := "/api/v1/tournaments/" + tournamentID.String() + "/snapshot"
	req, resp := fixture.doJSON(t, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	snapshot := decodeJSON[api.PublicRecoverySnapshot](t, resp)
	require.Equal(t, tournamentID, snapshot.Tournament.TournamentId)
	require.Equal(t, api.TournamentState(domain.TournamentStateCompleted), snapshot.Tournament.State)
	require.NotNil(t, snapshot.Tournament.FinishedAt)
	semifinalMatches, finalMatches := 0, 0
	for _, match := range snapshot.Bracket.Matches {
		require.Equal(t, api.SeriesStateCompleted, match.State)
		require.NotEqual(t, match.Score.FirstParticipantWins, match.Score.SecondParticipantWins)
		switch match.Stage {
		case api.PublicBracketMatchStageSemifinal:
			semifinalMatches++
		case api.PublicBracketMatchStageFinal:
			finalMatches++
		}
	}
	require.Equal(t, 2, semifinalMatches)
	require.Equal(t, 1, finalMatches)

	operator := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	finalSeries := make([]api.Series, 0, 1)
	for _, series := range operator.Series {
		if series.Format == api.Bo3 && series.State == api.SeriesStateCompleted && series.WinnerId != nil {
			finalSeries = append(finalSeries, series)
		}
	}
	require.Len(t, finalSeries, 1, "operator read surface must expose exactly one completed final winner")

	finalResults := make([]api.PublicOfficialResult, 0, 1)
	for _, result := range snapshot.OfficialResults {
		if result.SeriesId == finalSeries[0].Id && result.State == api.SeriesStateCompleted && result.WinnerDisplayName != nil {
			finalResults = append(finalResults, result)
		}
	}
	require.Len(t, finalResults, 1, "public read surface must publish exactly one final winner")

	auditPath := "/api/v1/admin/tournament-audit?tournament_id=" + tournamentID.String()
	auditReq, auditResp := doTournamentFlowJSON(
		t, fixture, http.MethodGet, auditPath, "", adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, auditResp.Code, auditResp.Body.String())
	fixture.validateResponse(t, auditReq, auditResp)
	audit := decodeJSON[api.AuditPage](t, auditResp)
	currentWinnerEvents := 0
	for _, event := range audit.Events {
		if event.TournamentId == tournamentID && event.SeriesId == finalSeries[0].Id &&
			event.EntityKind == api.AuditEntityKindSeries && event.WinnerId != nil && event.IsCurrent {
			currentWinnerEvents++
		}
	}
	require.Equal(t, 1, currentWinnerEvents, "admin audit must expose one current final winner event")
}

func assertTournamentRealtimeThroughProduction(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	player tournamentFlowPlayer,
) {
	t.Helper()
	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	require.NotNil(t, runtime.webSocket)
	httpServer := httptest.NewServer(runtime.webSocket)
	t.Cleanup(httpServer.Close)

	publicPath := "/api/v1/tournaments/" + tournamentID.String() + "/realtime"
	publicConnection := dialTournamentFlowWebSocket(t, httpServer.URL+publicPath, nil)
	publicData := readTournamentFlowWebSocket(t, publicConnection)
	publicMessage, err := inboundws.DecodeTournamentPublicMessage(publicData)
	require.NoError(t, err)
	require.NotNil(t, publicMessage.Public, string(publicData))
	assertPublicRealtimeEnvelope(t, publicData, publicMessage.Public.Envelope, tournamentID)
	publicResumeID := publicMessage.Public.Envelope.ResumeID
	require.NoError(t, publicConnection.CloseNow())

	publicReconnectURL := httpServer.URL + publicPath
	if publicResumeID != nil {
		publicReconnectURL += "?resume_id=" + publicResumeID.String()
	}
	publicReconnect := dialTournamentFlowWebSocket(t, publicReconnectURL, nil)
	publicReconnectData := readTournamentFlowWebSocket(t, publicReconnect)
	publicReconnectMessage, err := inboundws.DecodeTournamentPublicMessage(publicReconnectData)
	require.NoError(t, err)
	require.NotNil(t, publicReconnectMessage.Public)
	assertPublicRealtimeEnvelope(t, publicReconnectData, publicReconnectMessage.Public.Envelope, tournamentID)
	require.GreaterOrEqual(
		t, publicReconnectMessage.Public.Envelope.Sequence, publicMessage.Public.Envelope.Sequence,
	)
	require.NoError(t, publicReconnect.CloseNow())

	participantPath := "/api/v1/tournaments/" + tournamentID.String() + "/participant/realtime"
	participantConnection := dialTournamentFlowWebSocket(
		t, httpServer.URL+participantPath,
		&coderws.DialOptions{HTTPHeader: http.Header{
			"Cookie": {(&http.Cookie{
				Name: middleware.PlayerSessionCookieName, Value: player.session.String(),
			}).String()},
		}},
	)
	participantData := readTournamentFlowWebSocket(t, participantConnection)
	participantMessage, err := inboundws.DecodeTournamentParticipantMessage(participantData)
	require.NoError(t, err)
	require.NotNil(t, participantMessage.Participant)
	assertParticipantRealtimeEnvelope(t, participantData, participantMessage.Participant.Envelope, tournamentID, player.id)
	participantResumeID := participantMessage.Participant.Envelope.ResumeID
	require.NoError(t, participantConnection.CloseNow())

	participantReconnectURL := httpServer.URL + participantPath
	if participantResumeID != nil {
		participantReconnectURL += "?resume_id=" + participantResumeID.String()
	}
	participantReconnect := dialTournamentFlowWebSocket(t, participantReconnectURL, &coderws.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {(&http.Cookie{
				Name: middleware.PlayerSessionCookieName, Value: player.session.String(),
			}).String()},
		},
	})
	participantReconnectData := readTournamentFlowWebSocket(t, participantReconnect)
	participantReconnectMessage, err := inboundws.DecodeTournamentParticipantMessage(participantReconnectData)
	require.NoError(t, err)
	require.NotNil(t, participantReconnectMessage.Participant)
	assertParticipantRealtimeEnvelope(t, participantReconnectData, participantReconnectMessage.Participant.Envelope, tournamentID, player.id)
	require.GreaterOrEqual(
		t, participantReconnectMessage.Participant.Envelope.Sequence, participantMessage.Participant.Envelope.Sequence,
	)
	require.NoError(t, participantReconnect.CloseNow())

	operatorPath := "/api/v1/admin/tournaments/" + tournamentID.String() + "/realtime"
	operatorConnection := dialTournamentFlowWebSocket(
		t, httpServer.URL+operatorPath, tournamentFlowAdminWebSocketOptions(t, adminToken),
	)
	operatorData := readTournamentFlowWebSocket(t, operatorConnection)
	operatorMessage, err := inboundws.DecodeTournamentOperatorMessage(operatorData)
	require.NoError(t, err)
	require.NotNil(t, operatorMessage.Operator)
	assertOperatorRealtimeEnvelope(t, operatorData, operatorMessage.Operator.Envelope, tournamentID)
	operatorResumeID := operatorMessage.Operator.Envelope.ResumeID
	require.NoError(t, operatorConnection.CloseNow())

	operatorReconnectURL := httpServer.URL + operatorPath
	if operatorResumeID != nil {
		operatorReconnectURL += "?resume_id=" + operatorResumeID.String()
	}
	operatorReconnect := dialTournamentFlowWebSocket(
		t, operatorReconnectURL, tournamentFlowAdminWebSocketOptions(t, adminToken),
	)
	operatorReconnectData := readTournamentFlowWebSocket(t, operatorReconnect)
	operatorReconnectMessage, err := inboundws.DecodeTournamentOperatorMessage(operatorReconnectData)
	require.NoError(t, err)
	require.NotNil(t, operatorReconnectMessage.Operator)
	assertOperatorRealtimeEnvelope(t, operatorReconnectData, operatorReconnectMessage.Operator.Envelope, tournamentID)
	require.GreaterOrEqual(
		t, operatorReconnectMessage.Operator.Envelope.Sequence, operatorMessage.Operator.Envelope.Sequence,
	)
	require.NoError(t, operatorReconnect.CloseNow())
}

func connectTournamentParticipantsThroughProduction(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
	players []tournamentFlowPlayer,
) {
	t.Helper()
	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	require.NotNil(t, runtime.webSocket)
	httpServer := httptest.NewServer(runtime.webSocket)
	t.Cleanup(httpServer.Close)
	path := "/api/v1/tournaments/" + tournamentID.String() + "/participant/realtime"
	for _, player := range players {
		connection := dialTournamentFlowWebSocket(t, httpServer.URL+path, &coderws.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": {(&http.Cookie{
					Name: middleware.PlayerSessionCookieName, Value: player.session.String(),
				}).String()},
			},
		})
		data := readTournamentFlowWebSocket(t, connection)
		message, err := inboundws.DecodeTournamentParticipantMessage(data)
		require.NoError(t, err)
		require.NotNil(t, message.Participant)
		assertParticipantRealtimeEnvelope(t, data, message.Participant.Envelope, tournamentID, player.id)
		t.Cleanup(func() { require.NoError(t, connection.CloseNow()) })
	}
}

func dialTournamentFlowWebSocket(t *testing.T, endpoint string, options *coderws.DialOptions) *coderws.Conn {
	t.Helper()
	connection, response, err := coderws.Dial(context.Background(), endpoint, options)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func readTournamentFlowWebSocket(t *testing.T, connection *coderws.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := connection.Read(ctx)
	require.NoError(t, err)
	return data
}

func tournamentFlowAdminWebSocketOptions(t *testing.T, adminToken string) *coderws.DialOptions {
	t.Helper()
	csrfToken, err := middleware.NewAdminCSRFToken(middleware.AdminAccessCSRFCookieName, adminToken)
	require.NoError(t, err)
	cookies := (&http.Cookie{Name: middleware.AdminAccessCookieName, Value: adminToken}).String() + "; " +
		(&http.Cookie{Name: middleware.AdminAccessCSRFCookieName, Value: csrfToken}).String()
	return &coderws.DialOptions{HTTPHeader: http.Header{
		"Cookie":                  {cookies},
		middleware.CSRFHeaderName: {csrfToken},
	}}
}

func assertPublicRealtimeEnvelope(
	t *testing.T,
	data []byte,
	envelope inboundws.TournamentPublicEnvelope,
	tournamentID uuid.UUID,
) {
	t.Helper()
	require.Equal(t, tournamentID, envelope.TournamentID)
	require.GreaterOrEqual(t, envelope.Sequence, int64(0))
	require.GreaterOrEqual(t, envelope.ProjectionRevision, int64(1))
	require.NotContains(t, string(data), `"participant"`)
	require.NotContains(t, string(data), `"operator"`)
}

func assertParticipantRealtimeEnvelope(
	t *testing.T,
	data []byte,
	envelope tournamentws.ParticipantRealtimeEnvelope,
	tournamentID, playerID uuid.UUID,
) {
	t.Helper()
	require.Equal(t, tournamentID, envelope.TournamentID)
	require.Equal(t, tournamentID, envelope.Participant.TournamentID)
	require.Equal(t, playerID, envelope.Participant.PlayerID)
	require.GreaterOrEqual(t, envelope.Sequence, int64(0))
	require.GreaterOrEqual(t, envelope.ProjectionRevision, int64(1))
	require.NotContains(t, string(data), `"public"`)
	require.NotContains(t, string(data), `"operator"`)
}

func assertOperatorRealtimeEnvelope(
	t *testing.T,
	data []byte,
	envelope inboundws.TournamentOperatorEnvelope,
	tournamentID uuid.UUID,
) {
	t.Helper()
	require.Equal(t, tournamentID, envelope.TournamentID)
	require.Equal(t, tournamentID, envelope.Operator.TournamentID)
	require.GreaterOrEqual(t, envelope.Sequence, int64(0))
	require.GreaterOrEqual(t, envelope.ProjectionRevision, int64(1))
	require.NotContains(t, string(data), `"public"`)
	require.NotContains(t, string(data), `"participant"`)
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
