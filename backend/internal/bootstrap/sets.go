package bootstrap

import (
	"github.com/google/wire"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/incidentauth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

var ConfigSet = wire.NewSet(
	provideAuthConfig,
)

var RuntimeSet = wire.NewSet(
	provideRuntimeContext,
)

var PostgresSet = wire.NewSet(
	providePostgresConfig,
	providePostgres,
)

var RedisSet = wire.NewSet(
	provideRedisConfig,
	provideRedis,
)

var SeaweedFSSet = wire.NewSet(
	provideSeaweedConfig,
	provideSeaweedStorage,
	wire.Bind(new(taskusecase.SourceFileStorage), new(*objectstorage.SeaweedStorage)),
)

var ReposSet = wire.NewSet(
	postgres.NewTxManager,
	wire.Bind(new(playerusecase.ManagementTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(playerusecase.SessionTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadmin.LifecycleTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadmin.RosterTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadmin.ExecutionTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadmin.OperatorResultTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadmin.CorrectionTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentparticipant.ParticipantTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentpause.PauseTransactionManager), new(*postgres.TxManager)),

	postgres.NewSchemaVersionPostgres,
	wire.Bind(new(restv1.SchemaVersionReader), new(*postgres.SchemaVersionPostgres)),
	postgres.NewAdminPlayerEventsPostgres,
	wire.Bind(new(restv1.AdminPlayerEventSubscriber), new(*postgres.AdminPlayerEventsPostgres)),

	postgres.NewPlayerPostgres,
	wire.Bind(new(playerusecase.PlayerRepository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(playerusecase.Repository), new(*postgres.PlayerPostgres)),
	wire.Bind(new(middleware.PlayerSessionReader), new(*postgres.PlayerPostgres)),
	wire.Bind(new(websocket.PlayerSessionReader), new(*postgres.PlayerPostgres)),

	postgres.NewTaskPostgres,
	wire.Bind(new(taskusecase.Repository), new(*postgres.TaskPostgres)),

	postgres.NewTournamentPostgres,
	postgres.NewTournamentCreatePostgres,
	wire.Bind(new(catalogusecase.TournamentCreateStore), new(*postgres.TournamentCreatePostgres)),
	postgres.NewTournamentLifecyclePostgres,
	wire.Bind(new(lifecycleusecase.TournamentLifecycleRepository), new(*postgres.TournamentLifecyclePostgres)),
	postgres.NewTournamentCancellationPostgres,
	wire.Bind(new(tournamentcancellation.TournamentCancellationRepository), new(*postgres.TournamentCancellationPostgres)),
	postgres.NewTournamentAdminLifecyclePostgres,
	wire.Bind(new(tournamentadmin.LifecycleWorkflowRepository), new(*postgres.TournamentAdminLifecyclePostgres)),
	wire.Bind(new(tournamentpause.TournamentPauseRepository), new(*postgres.TournamentAdminLifecyclePostgres)),
	postgres.NewTournamentAdminRosterPostgres,
	wire.Bind(new(tournamentadmin.RosterWorkflowRepository), new(*postgres.TournamentAdminRosterPostgres)),
	postgres.NewTournamentAdminExecutionPostgres,
	wire.Bind(new(tournamentadmin.ExecutionWorkflowRepository), new(*postgres.TournamentAdminExecutionPostgres)),
	wire.Bind(new(gameusecase.StartRepository), new(*postgres.TournamentAdminExecutionPostgres)),
	postgres.NewExecutionAuthorityPostgres,
	postgres.NewTournamentSnapshotPostgres,
	wire.Bind(new(inbound.TournamentSnapshotUseCase), new(*postgres.TournamentSnapshotPostgres)),
	postgres.NewTournamentAdminAuditPostgres,
	wire.Bind(new(tournamentadmin.AuditPort), new(*postgres.TournamentAdminAuditPostgres)),
	wire.Bind(new(tournamentadmin.IncidentSnapshotPort), new(*postgres.TournamentAdminAuditPostgres)),
	postgres.NewTournamentAdminSnapshotPostgres,
	wire.Bind(new(tournamentadmin.SnapshotPort), new(*postgres.TournamentAdminSnapshotPostgres)),
	postgres.NewWavePostgres,
	postgres.NewDraftPostgres,
	postgres.NewResultPostgres,
	postgres.NewAssignmentPostgres,
	postgres.NewExactDraftBranchPlanPostgres,
	wire.Bind(new(assignmentusecase.ExactDraftBranchPlanRepository), new(*postgres.ExactDraftBranchPlanPostgres)),
	wire.Bind(new(playoff.ExactDraftPlanAuthorityReader), new(*postgres.ExactDraftBranchPlanPostgres)),
	wire.Bind(new(playoff.ExactDraftCommittedPlanReader), new(*postgres.ExactDraftBranchPlanPostgres)),
	postgres.NewProjectionPostgres,
	wire.Bind(new(resultprojection.FinalPublicationRepository), new(*postgres.ProjectionPostgres)),
	postgres.NewPlayoffTerminalPostgres,
	wire.Bind(new(playoff.TerminalRepository), new(*postgres.PlayoffTerminalPostgres)),
	postgres.NewParticipantSettlementRepository,
	postgres.NewParticipantSettlementWorkflow,
	wire.Bind(new(tournamentparticipant.SettlementWorkflow), new(*postgres.ParticipantSettlementWorkflow)),
	postgres.NewTournamentAdminResultPostgres,
	wire.Bind(new(tournamentadmin.OperatorResultWorkflowRepository), new(*postgres.TournamentAdminResultPostgres)),
	postgres.NewTournamentAdminReplayPostgres,
	wire.Bind(new(tournamentadmin.ReplayWorkflowRepository), new(*postgres.TournamentAdminReplayPostgres)),
	postgres.NewTournamentAdminCorrectionPostgres,
	wire.Bind(new(tournamentadmin.CorrectionWorkflowRepository), new(*postgres.TournamentAdminCorrectionPostgres)),
	postgres.NewTournamentProgressionPostgres,
	wire.Bind(new(tournamentprogression.Repository), new(*postgres.TournamentProgressionPostgres)),
	wire.Bind(new(tournamentprogression.SwissTerminalEvidenceReader), new(*postgres.TournamentProgressionPostgres)),
	wire.Bind(new(tournamentprogression.Transitioner), new(*postgres.TournamentProgressionPostgres)),
	wire.Bind(new(tournamentprogression.PlayoffProjectionPublisher), new(*postgres.TournamentProgressionPostgres)),
	postgres.NewTournamentParticipantPostgres,
	wire.Bind(new(tournamentparticipant.CommandAuthority), new(*postgres.TournamentParticipantPostgres)),
	postgres.NewParticipantStatePostgres,
	wire.Bind(new(tournamentparticipant.StateReader), new(*postgres.ParticipantStatePostgres)),
	postgres.NewParticipantReadinessRepository,
	postgres.NewParticipantDraftRepository,
	postgres.NewParticipantSubmissionRepository,
	postgres.NewParticipantForfeitRepository,
	postgres.NewParticipantPostSeriesRepository,
	provideRealtimeOutbox,
	providePrivateTaskAvailabilityRepository,
	wire.Bind(new(taskusecase.BacklogSource), new(*postgres.PrivateTaskAvailabilityPostgres)),

	postgres.NewLeaderboardPostgres,
	wire.Bind(new(leaderboardusecase.StatsRepository), new(*postgres.LeaderboardPostgres)),
)

var UseCasesSet = wire.NewSet(
	provideClock,
	provideEventTelemetry,
	provideTournamentEventDispatcher,
	providePrivateMetricsHandler,
	provideProjectionHealth,
	provideHealthProbe,
	providePreflightRuntimeHealthSource,
	provideEventDeliveryObserver,
	providePrivateTaskAvailabilityMonitor,
	wire.Bind(new(taskusecase.HealthSource), new(*taskusecase.AvailabilityMonitor)),
	provideTournamentAdminObserver,
	wire.Bind(new(authusecase.Clock), new(clockFunc)),
	wire.Bind(new(leaderboardusecase.Clock), new(clockFunc)),
	wire.Bind(new(playerusecase.ManagementClock), new(clockFunc)),
	wire.Bind(new(playerusecase.SessionClock), new(clockFunc)),
	wire.Bind(new(catalogusecase.Clock), new(clockFunc)),
	wire.Bind(new(catalogusecase.CatalogClock), new(clockFunc)),
	wire.Bind(new(lifecycleusecase.LifecycleClock), new(clockFunc)),
	wire.Bind(new(tournamentpause.PauseClock), new(clockFunc)),
	wire.Bind(new(tournamentcancellation.CancellationClock), new(clockFunc)),
	wire.Bind(new(tournamentadmin.AdminLifecycleClock), new(clockFunc)),
	wire.Bind(new(tournamentadmin.OperationClock), new(clockFunc)),
	wire.Bind(new(tournamentparticipant.ParticipantOperationClock), new(clockFunc)),
	wire.Bind(new(tournamentprogression.ProgressionClock), new(clockFunc)),
	provideRevocationRedis,
	wire.Bind(new(authusecase.RevocationStore), new(*redisadapter.RevocationRedis)),

	provideJWTCodec,
	wire.Bind(new(authusecase.TokenCodec), new(*authadapter.JWTCodec)),
	providePasswordVerifier,
	wire.Bind(new(authusecase.PasswordVerifier), new(*authadapter.PasswordVerifier)),
	provideIncidentAuthenticator,
	wire.Bind(new(tournamentadmin.IncidentAuthenticator), new(*incidentauth.HMACAuthenticator)),
	provideAuthUseCase,
	wire.Bind(new(restv1.AdminAuthService), new(*authusecase.UseCase)),
	wire.Bind(new(middleware.AdminAccessVerifier), new(*authusecase.UseCase)),
	provideTaskUseCase,
	wire.Bind(new(restv1.AdminTaskService), new(*taskusecase.UseCase)),
	wire.Bind(new(taskusecase.Catalog), new(*taskusecase.UseCase)),
	providePlayerManagementUseCase,
	wire.Bind(new(restv1.AdminPlayerService), new(*playerusecase.ManagementUseCase)),
	provideCleanupRunner,
	provideSourceFiles,
	wire.Bind(new(restv1.UploadService), new(*taskusecase.SourceFiles)),

	providePlayerSessionUseCase,
	wire.Bind(new(restv1.PlayerService), new(*playerusecase.SessionUseCase)),

	provideLeaderboardRanking,
	provideLeaderboardCache,
	wire.Bind(new(restv1.LeaderboardService), new(*leaderboardusecase.Cache)),
	wire.Bind(new(playerusecase.LeaderboardInvalidator), new(*leaderboardusecase.Cache)),

	provideTournamentIDGenerator,
	wire.Bind(new(catalogusecase.IDGenerator), new(*catalogusecase.DeterministicIDGenerator)),
	provideTournamentCatalog,
	wire.Bind(new(catalogusecase.TournamentLister), new(*catalogusecase.TournamentUseCase)),
	provideTournamentCommandReceipts,
	wire.Bind(new(idempotency.Store), new(*redisadapter.CommandReceiptStore)),
	provideDistributedCommandCoordinator,
	provideTournamentApplication,
	wire.Bind(new(inbound.TournamentUseCase), new(*catalogusecase.UseCase)),
	provideReconnectObserver,
	provideTournamentLifecycle,
	wire.Bind(new(tournamentadmin.LifecycleTransitioner), new(*lifecycleusecase.TournamentLifecycleUseCase)),
	provideTournamentPause,
	wire.Bind(new(tournamentadmin.LifecyclePauser), new(*tournamentpause.TournamentPauseUseCase)),
	provideTournamentCancellation,
	wire.Bind(new(tournamentadmin.LifecycleCanceller), new(*tournamentcancellation.TournamentCancellationUseCase)),
	provideTournamentProgression,
	wire.Bind(new(tournamentadmin.LifecycleProgression), new(*tournamentprogression.Workflow)),
	assignmentusecase.NewExactDraftBranchPlanUseCase,
	wire.Bind(new(playoff.ExactDraftPlanWorkflow), new(*assignmentusecase.ExactDraftBranchPlanUseCase)),
	provideFinalDraftAssignmentPlanner,
	wire.Bind(new(playoff.FinalDraftAssignmentPlanner), new(*playoff.FinalDraftAssignmentService)),
	wire.Bind(new(playoff.FinalBindingRehydrator), new(*playoff.FinalDraftAssignmentService)),
	providePlayoffTerminal,
	wire.Bind(new(tournamentparticipant.ParticipantPostseasonWorkflow), new(*playoff.TerminalCoordinator)),
	wire.Bind(new(tournamentadmin.AdminPostseasonWorkflow), new(*playoff.TerminalCoordinator)),
	provideTournamentAdminLifecycle,
	wire.Bind(new(tournamentadmin.LifecyclePort), new(*tournamentadmin.LifecycleWorkflow)),
	provideTournamentAdminRoster,
	wire.Bind(new(tournamentadmin.RosterPort), new(*tournamentadmin.RosterWorkflow)),
	wire.Bind(new(tournamentadmin.PreflightPort), new(*tournamentadmin.RosterWorkflow)),
	provideTournamentAdminExecution,
	wire.Bind(new(tournamentadmin.PairingPort), new(*tournamentadmin.ExecutionWorkflow)),
	wire.Bind(new(tournamentadmin.WavePort), new(*tournamentadmin.ExecutionWorkflow)),
	provideTournamentAdminResults,
	wire.Bind(new(tournamentadmin.NoShowPort), new(*tournamentadmin.OperatorResultWorkflow)),
	wire.Bind(new(tournamentadmin.ForfeitPort), new(*tournamentadmin.OperatorResultWorkflow)),
	provideTournamentAdminReplay,
	wire.Bind(new(tournamentadmin.ReservePort), new(*tournamentadmin.ReplayWorkflow)),
	wire.Bind(new(tournamentadmin.ReplayPort), new(*tournamentadmin.ReplayWorkflow)),
	provideTournamentAdminCorrection,
	wire.Bind(new(tournamentadmin.CorrectionPort), new(*tournamentadmin.CorrectionWorkflow)),
	provideTournamentAdminApplication,
	provideIdempotentTournamentAdminApplication,
	provideObservedTournamentAdminApplication,
	wire.Bind(new(tournamentadmin.AdminService), new(*tournamentadmin.AdminObservedService)),
	provideTournamentAdminInbound,
	provideParticipantReadiness,
	wire.Bind(new(tournamentparticipant.ReadinessWorkflow), new(*readiness.ReadinessUseCase)),
	provideParticipantDraft,
	wire.Bind(new(tournamentparticipant.DraftActionWorkflow), new(*draftusecase.ActionUseCase)),
	provideParticipantSubmission,
	wire.Bind(new(tournamentparticipant.SubmissionWorkflow), new(*gameusecase.SubmissionUseCase)),
	provideParticipantSurrender,
	wire.Bind(new(tournamentparticipant.SurrenderWorkflow), new(*postgres.ParticipantSurrenderWorkflow)),
	provideParticipantPostSeries,
	wire.Bind(new(tournamentparticipant.PostSeriesWorkflow), new(*tournamentparticipant.PostSeriesUseCase)),
	provideParticipantCommands,
	wire.Bind(new(tournamentparticipant.CommandExecutor), new(*tournamentparticipant.CommandCoordinator)),
	provideTournamentParticipantApplication,
	provideIdempotentTournamentParticipantApplication,
	provideTournamentParticipantObserver,
	provideObservedTournamentParticipantApplication,
	wire.Bind(new(inbound.TournamentParticipantUseCase), new(*tournamentparticipant.ParticipantObservedService)),
	provideRecoveryTerminalStore,
	provideExecutionRecoveryObserver,
	provideRecoveryDeadlineHandler,
	provideRecoveryDeadlineScheduler,
	provideRecoveryRepository,
	provideRecoveryObserver,
	provideRecoveryDeadlineSweep,
	provideRecoveryWorker,
	provideExecutionAuthorityController,
	provideExecutionRecoveryRepository,
	provideExecutionEpochReplay,
	provideExecutionRecoverer,
	provideGoldenRuntimeRepository,
	provideGoldenRuntimeApplication,
	wire.Bind(new(inbound.GoldenUseCase), new(*goldenusecase.RuntimeApplication)),
	wire.Bind(new(inbound.GoldenConnectionUseCase), new(*goldenusecase.RuntimeApplication)),
	provideExecutionRecoveryRunner,
	wire.Bind(new(recovery.WorkerHealthSource), new(*recovery.Worker)),
)

var MiddlewareSet = wire.NewSet(
	provideRESTMiddlewares,
)

var WebSocketSet = wire.NewSet(
	provideHandshakeRateLimiter,
	provideTournamentProductionSnapshotSource,
	wire.Bind(new(tournamentws.ParticipantRealtimeReadSource), new(*websocket.TournamentProductionSnapshotSource)),
	wire.Bind(new(tournamentws.PublicRealtimeReadSource), new(*websocket.TournamentProductionSnapshotSource)),
	wire.Bind(new(tournamentws.OperatorRealtimeReadSource), new(*websocket.TournamentProductionSnapshotSource)),
	wire.Bind(new(websocket.TournamentParticipantConnectionFlow), new(*websocket.TournamentParticipantFlow)),
	wire.Bind(new(websocket.TournamentPublicConnectionFlow), new(*websocket.TournamentPublicFlow)),
	wire.Bind(new(websocket.TournamentOperatorConnectionFlow), new(*websocket.TournamentOperatorFlow)),
	websocket.NewTournamentParticipantFlow,
	providePublicRealtimeConfig,
	websocket.NewTournamentPublicFlow,
	websocket.NewTournamentOperatorFlow,
	provideOperatorSessionResolver,
	provideTournamentRealtimeOptions,
	provideRealtimeDelivery,
	wire.Bind(new(RealtimeHealthSource), new(*websocket.RealtimeDelivery)),
	provideObservedEventDeliveryWorker,
	provideEventDeliveryHealth,
	provideOutboxBacklog,
	provideReceiptRetentionWorker,
	provideRawWebSocketServer,
	provideWebSocketServer,
)

var HTTPSet = wire.NewSet(
	provideHealthChecks,
	provideLoginRateLimiter,
	provideRefreshRateLimiter,
	provideJoinRateLimiter,
	provideLeaderboardRateLimiter,
	providePublicTournamentReadRateLimiter,
	provideOperatorTournamentReadRateLimiter,
	provideOperatorTournamentMutationRateLimiter,
	provideParticipantTournamentReadRateLimiter,
	provideParticipantTournamentMutationRateLimiter,
	provideRESTServerWithClock,
	provideHTTPHandler,
	provideHTTPServer,
)

var AppSet = wire.NewSet(
	provideMigrator,
	provideRuntimeWorkerHeartbeats,
	wire.Bind(new(observability.RuntimeWorkerHeartbeatReader), new(*redisadapter.RuntimeWorkerHeartbeats)),
	provideRuntimeWorkers,
	provideApplication,
)
