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
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	exactdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	leaderboardrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"
	participantarchiverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/participantarchive"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	taskrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/correction"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	adminlifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/lifecycle"
	adminreplayrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/replay"
	adminresultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/result"
	adminrosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	adminsnapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	cancellationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/cancellation"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	contentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/configuration"
	creationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/creation"
	lifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/lifecycle"
	participantauthorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/authority"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	participantpostseriesrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/postseries"
	participantreadinessrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/readiness"
	settlementrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
	participantstaterepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/state"
	participantsubmissionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/submission"
	participantsurrenderrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/surrender"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	deadlinerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss/deadline"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
	goldenruntime "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/runtime"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/participantarchive"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminconfiguration "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/configuration"
	tournamentadmincorrection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	tournamentadminlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	tournamentadminobservability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observability"
	tournamentadminreplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	tournamentadminresult "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	tournamentadminroster "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	participantconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
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
	wire.Bind(new(tournamentadminlifecycle.LifecycleTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadminroster.RosterTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadminexecution.ExecutionTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadminreplay.ReplayTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadminresult.OperatorResultTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentadmincorrection.CorrectionTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentparticipant.ParticipantTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(tournamentpause.PauseTransactionManager), new(*postgres.TxManager)),
	wire.Bind(new(recovery.TransactionManager), new(*postgres.TxManager)),

	postgres.NewSchemaVersionPostgres,
	wire.Bind(new(restv1.SchemaVersionReader), new(*postgres.SchemaVersionPostgres)),
	playerrepo.NewAdminPlayerEventsPostgres,
	wire.Bind(new(restv1.AdminPlayerEventSubscriber), new(*playerrepo.AdminPlayerEventsPostgres)),

	playerrepo.NewPlayerPostgres,
	wire.Bind(new(playerusecase.PlayerRepository), new(*playerrepo.PlayerPostgres)),
	wire.Bind(new(playerusecase.Repository), new(*playerrepo.PlayerPostgres)),
	wire.Bind(new(middleware.PlayerSessionReader), new(*playerrepo.PlayerPostgres)),
	wire.Bind(new(websocket.PlayerSessionReader), new(*playerrepo.PlayerPostgres)),

	taskrepo.NewTaskPostgres,
	wire.Bind(new(taskusecase.Repository), new(*taskrepo.TaskPostgres)),

	catalogrepo.NewTournamentCatalogPostgres,
	creationrepo.NewProductionTournamentCreatePostgres,
	wire.Bind(new(catalogusecase.TournamentCreateStore), new(*creationrepo.TournamentCreatePostgres)),
	contentrepo.NewTournamentContentPostgres,
	wire.Bind(new(catalogusecase.ContentReader), new(*contentrepo.TournamentContentPostgres)),
	lifecyclerepo.NewTournamentLifecyclePostgres,
	wire.Bind(new(lifecycleusecase.TournamentLifecycleRepository), new(*lifecyclerepo.TournamentLifecyclePostgres)),
	cancellationrepo.NewTournamentCancellationPostgres,
	wire.Bind(new(tournamentcancellation.TournamentCancellationRepository), new(*cancellationrepo.TournamentCancellationPostgres)),
	adminlifecyclerepo.NewTournamentAdminLifecyclePostgres,
	wire.Bind(new(tournamentadminlifecycle.LifecycleWorkflowRepository), new(*adminlifecyclerepo.TournamentAdminLifecyclePostgres)),
	wire.Bind(new(tournamentpause.TournamentPauseRepository), new(*adminlifecyclerepo.TournamentAdminLifecyclePostgres)),
	adminrosterrepo.NewTournamentAdminRosterPostgres,
	wire.Bind(new(tournamentadminroster.RosterWorkflowRepository), new(*adminrosterrepo.TournamentAdminRosterPostgres)),
	provideTournamentExecutionRepository,
	wire.Bind(new(tournamentadminexecution.ExecutionWorkflowRepository), new(*executionrepo.Repository)),
	wire.Bind(new(tournamentadminexecution.NormalPauseExecutionRepository), new(*executionrepo.Repository)),
	wire.Bind(new(gamestart.StartRepository), new(*executionrepo.Repository)),
	wire.Bind(new(gamepause.NormalPauseRepository), new(*executionrepo.Repository)),
	wire.Bind(new(gamepause.PauseResumeRepository), new(*executionrepo.Repository)),
	wire.Bind(new(gamepause.PauseResumePresenceRepository), new(*executionrepo.Repository)),
	provideTournamentPausedPresenceRepository,
	contentrepo.NewProductionTournamentConfigurationPostgres,
	wire.Bind(new(tournamentadminconfiguration.TournamentConfigurationRepository), new(*contentrepo.TournamentConfigurationPostgres)),
	authorityrepo.NewExecutionAuthorityPostgres,
	snapshotrepo.NewTournamentSnapshotPostgres,
	wire.Bind(new(inbound.TournamentSnapshotUseCase), new(*snapshotrepo.TournamentSnapshotPostgres)),
	auditrepo.NewTournamentAdminAuditPostgres,
	wire.Bind(new(tournamentadmin.AuditPort), new(*auditrepo.TournamentAdminAuditPostgres)),
	wire.Bind(new(tournamentadmin.IncidentSnapshotPort), new(*auditrepo.TournamentAdminAuditPostgres)),
	adminsnapshotrepo.NewTournamentAdminSnapshotPostgres,
	wire.Bind(new(tournamentadmin.SnapshotPort), new(*adminsnapshotrepo.TournamentAdminSnapshotPostgres)),
	waverepo.NewWavePostgres,
	draftrepo.NewDraftPostgres,
	provideResultPostgres,
	assignmentrepo.NewAssignmentPostgres,
	exactdraftrepo.NewExactDraftBranchPlanPostgres,
	wire.Bind(new(assignmentusecase.ExactDraftBranchPlanRepository), new(*exactdraftrepo.ExactDraftBranchPlanPostgres)),
	wire.Bind(new(playoff.ExactDraftPlanAuthorityReader), new(*exactdraftrepo.ExactDraftBranchPlanPostgres)),
	wire.Bind(new(playoff.ExactDraftCommittedPlanReader), new(*exactdraftrepo.ExactDraftBranchPlanPostgres)),
	projectionrepo.NewProjectionPostgres,
	wire.Bind(new(resultprojection.FinalPublicationRepository), new(*projectionrepo.ProjectionPostgres)),
	providePlayoffTerminalRepository,
	wire.Bind(new(playoff.TerminalRepository), new(*playoffrepo.PlayoffTerminalPostgres)),
	provideParticipantSettlementRepository,
	settlementrepo.NewParticipantSettlementWorkflow,
	wire.Bind(new(tournamentparticipant.SettlementWorkflow), new(*settlementrepo.ParticipantSettlementWorkflow)),
	provideTournamentAdminResultRepository,
	wire.Bind(new(tournamentadminresult.OperatorResultWorkflowRepository), new(*adminresultrepo.TournamentAdminResultPostgres)),
	adminreplayrepo.NewTournamentAdminReplayPostgres,
	wire.Bind(new(tournamentadminreplay.ReplayWorkflowRepository), new(*adminreplayrepo.TournamentAdminReplayPostgres)),
	correctionrepo.NewTournamentAdminCorrectionPostgres,
	wire.Bind(new(tournamentadmincorrection.CorrectionWorkflowRepository), new(*correctionrepo.TournamentAdminCorrectionPostgres)),
	progressionrepo.NewTournamentProgressionPostgres,
	wire.Bind(new(tournamentprogression.Repository), new(*progressionrepo.TournamentProgressionPostgres)),
	wire.Bind(new(tournamentprogression.SwissTerminalEvidenceReader), new(*progressionrepo.TournamentProgressionPostgres)),
	wire.Bind(new(tournamentprogression.Transitioner), new(*progressionrepo.TournamentProgressionPostgres)),
	wire.Bind(new(tournamentprogression.PlayoffProjectionPublisher), new(*progressionrepo.TournamentProgressionPostgres)),
	participantauthorityrepo.NewTournamentParticipantPostgres,
	wire.Bind(new(tournamentparticipant.CommandAuthority), new(*participantauthorityrepo.TournamentParticipantPostgres)),
	participantstaterepo.NewParticipantStatePostgres,
	wire.Bind(new(tournamentparticipant.StateReader), new(*participantstaterepo.ParticipantStatePostgres)),
	participantarchiverepo.NewParticipantArchivePostgres,
	wire.Bind(new(participantarchive.Repository), new(*participantarchiverepo.ParticipantArchivePostgres)),
	participantreadinessrepo.NewParticipantReadinessRepository,
	provideParticipantDraftRepository,
	wire.Bind(new(deadlinerepo.ParticipantDraftRepository), new(*participantdraftrepo.ParticipantDraftRepository)),
	deadlinerepo.NewSwissDraftDeadlinePostgres,
	participantsubmissionrepo.NewParticipantSubmissionRepository,
	participantsurrenderrepo.NewParticipantForfeitRepository,
	participantpostseriesrepo.NewParticipantPostSeriesRepository,
	wire.Bind(new(gameusecase.ReconnectRepository), new(*executionrepo.Repository)),
	provideRealtimeOutbox,
	taskrepo.NewPrivateTaskAvailabilityPostgres,
	wire.Bind(new(taskusecase.BacklogSource), new(*taskrepo.PrivateTaskAvailabilityPostgres)),

	leaderboardrepo.NewLeaderboardPostgres,
	wire.Bind(new(leaderboardusecase.StatsRepository), new(*leaderboardrepo.LeaderboardPostgres)),
)

var UseCasesSet = wire.NewSet(
	provideClock,
	provideEventTelemetry,
	provideTournamentEventDispatcher,
	providePrivateMetricsHandler,
	provideProjectionHealth,
	wire.Bind(new(observability.ProjectionHealthSource), new(*projectionrepo.ProjectionHealthPostgres)),
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
	wire.Bind(new(participantarchive.Clock), new(clockFunc)),
	wire.Bind(new(catalogusecase.Clock), new(clockFunc)),
	wire.Bind(new(catalogusecase.CatalogClock), new(clockFunc)),
	wire.Bind(new(lifecycleusecase.LifecycleClock), new(clockFunc)),
	wire.Bind(new(tournamentpause.PauseClock), new(clockFunc)),
	wire.Bind(new(tournamentcancellation.CancellationClock), new(clockFunc)),
	wire.Bind(new(tournamentadminlifecycle.AdminLifecycleClock), new(clockFunc)),
	wire.Bind(new(tournamentadminobservability.OperationClock), new(clockFunc)),
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
	wire.Bind(new(participantarchive.Signer), new(*taskusecase.SourceFiles)),
	provideParticipantArchive,
	wire.Bind(new(inbound.ParticipantArchiveUseCase), new(*participantarchive.Service)),

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
	wire.Bind(new(tournamentadminlifecycle.LifecycleTransitioner), new(*lifecycleusecase.TournamentLifecycleUseCase)),
	provideTournamentPause,
	wire.Bind(new(tournamentadminlifecycle.LifecyclePauser), new(*tournamentpause.TournamentPauseUseCase)),
	provideTournamentCancellation,
	wire.Bind(new(tournamentadminlifecycle.LifecycleCanceller), new(*tournamentcancellation.TournamentCancellationUseCase)),
	provideTournamentProgression,
	wire.Bind(new(tournamentadminlifecycle.LifecycleProgression), new(*tournamentprogression.Workflow)),
	assignmentusecase.NewExactDraftBranchPlanUseCase,
	wire.Bind(new(playoff.ExactDraftPlanWorkflow), new(*assignmentusecase.ExactDraftBranchPlanUseCase)),
	provideFinalDraftAssignmentPlanner,
	wire.Bind(new(playoff.FinalDraftAssignmentPlanner), new(*playoff.FinalDraftAssignmentService)),
	wire.Bind(new(playoff.FinalBindingRehydrator), new(*playoff.FinalDraftAssignmentService)),
	providePlayoffTerminal,
	wire.Bind(new(participantconnection.TerminalAdvancer), new(*playoff.TerminalCoordinator)),
	wire.Bind(new(recovery.TerminalAdvancer), new(*playoff.TerminalCoordinator)),
	wire.Bind(new(tournamentparticipant.ParticipantPostseasonWorkflow), new(*playoff.TerminalCoordinator)),
	wire.Bind(new(tournamentadminresult.PostseasonWorkflow), new(*playoff.TerminalCoordinator)),
	provideTournamentAdminLifecycle,
	wire.Bind(new(tournamentadminlifecycle.LifecyclePort), new(*tournamentadminlifecycle.LifecycleWorkflow)),
	provideTournamentAdminRoster,
	wire.Bind(new(tournamentadminroster.RosterPort), new(*tournamentadminroster.RosterWorkflow)),
	wire.Bind(new(tournamentadminroster.PreflightPort), new(*tournamentadminroster.RosterWorkflow)),
	provideTournamentAdminExecution,
	wire.Bind(new(tournamentadminexecution.PairingPort), new(*tournamentadminexecution.ExecutionWorkflow)),
	wire.Bind(new(tournamentadminexecution.WavePort), new(*tournamentadminexecution.ExecutionWorkflow)),
	tournamentadminconfiguration.NewTournamentConfigurationWorkflow,
	wire.Bind(new(inbound.TournamentConfigurationUseCase), new(*tournamentadminconfiguration.TournamentConfigurationWorkflow)),
	provideTournamentAdminResults,
	wire.Bind(new(tournamentadminresult.NoShowPort), new(*tournamentadminresult.OperatorResultWorkflow)),
	wire.Bind(new(tournamentadminresult.ForfeitPort), new(*tournamentadminresult.OperatorResultWorkflow)),
	provideTournamentAdminReplay,
	wire.Bind(new(tournamentadminreplay.ReservePort), new(*tournamentadminreplay.ReplayWorkflow)),
	wire.Bind(new(tournamentadminreplay.ReplayPort), new(*tournamentadminreplay.ReplayWorkflow)),
	provideTournamentAdminCorrection,
	wire.Bind(new(tournamentadmincorrection.CorrectionPort), new(*tournamentadmincorrection.CorrectionWorkflow)),
	provideTournamentAdminApplication,
	provideIdempotentTournamentAdminApplication,
	provideObservedTournamentAdminApplication,
	wire.Bind(new(tournamentadmin.AdminService), new(*tournamentadmin.AdminObservedService)),
	provideTournamentAdminInbound,
	provideParticipantReadiness,
	wire.Bind(new(tournamentparticipant.ReadinessWorkflow), new(*readiness.ReadinessUseCase)),
	provideParticipantConnectionRepository,
	provideParticipantConnectionCoordinator,
	provideParticipantConnectionReaper,
	wire.Bind(new(inbound.TournamentParticipantConnectionUseCase), new(*participantconnection.Coordinator)),
	provideParticipantDraft,
	wire.Bind(new(tournamentparticipant.DraftActionWorkflow), new(*draftusecase.ActionUseCase)),
	provideSwissDraftDeadlineWorker,
	provideParticipantSubmission,
	wire.Bind(new(tournamentparticipant.SubmissionWorkflow), new(*gamesubmission.SubmissionUseCase)),
	provideParticipantSurrender,
	wire.Bind(new(tournamentparticipant.SurrenderWorkflow), new(*participantsurrenderrepo.ParticipantSurrenderWorkflow)),
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
	wire.Bind(new(inbound.GoldenUseCase), new(*goldenruntime.RuntimeApplication)),
	wire.Bind(new(inbound.GoldenConnectionUseCase), new(*goldenruntime.RuntimeApplication)),
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
