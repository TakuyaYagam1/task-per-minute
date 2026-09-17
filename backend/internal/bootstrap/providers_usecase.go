package bootstrap

import (
	"context"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/incidentauth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	participantpostseriesrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/postseries"
	participantreadinessrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/readiness"
	participantsubmissionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/submission"
	participantsurrenderrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/surrender"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	tasktelemetry "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry/task"
	"github.com/TakuyaYagam1/task-per-minute/internal/ctxutil"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	gamesubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/submission"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/participantarchive"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentadminreplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	tournamentadminresult "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	tournamentadminroster "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func provideAuthConfig(cfg *config.Config) authusecase.Config {
	return authusecase.Config{
		AccessTTL:       cfg.JWT.AccessTTL,
		RefreshTTL:      cfg.JWT.RefreshTTL,
		ClockSkewLeeway: authusecase.DefaultClockSkewLeeway,
	}
}

func provideAuthUseCase(
	cfg authusecase.Config,
	clk authusecase.Clock,
	revocation authusecase.RevocationStore,
	tokens authusecase.TokenCodec,
	passwords authusecase.PasswordVerifier,
) *authusecase.UseCase {
	return authusecase.NewUseCase(cfg, clk, revocation, tokens, passwords)
}

func provideJWTCodec(cfg *config.Config, clk clockFunc) *authadapter.JWTCodec {
	return authadapter.NewJWTCodec(authadapter.JWTConfig{
		Secret:          []byte(cfg.JWT.Secret),
		ClockSkewLeeway: authusecase.DefaultClockSkewLeeway,
		Now:             clk.Now,
	})
}

func providePasswordVerifier(cfg *config.Config) *authadapter.PasswordVerifier {
	return authadapter.NewPasswordVerifier([]byte(cfg.Admin.Password))
}

func provideIncidentAuthenticator(cfg *config.Config) (*incidentauth.HMACAuthenticator, error) {
	return incidentauth.NewHMACAuthenticator(incidentauth.HMACConfig{
		KeyID:  cfg.Incident.HMACKeyID,
		Secret: []byte(cfg.Incident.HMACSecret),
	})
}

func provideTaskUseCase(tasks taskusecase.Repository) *taskusecase.UseCase {
	return taskusecase.NewUseCase(tasks)
}

func providePlayerManagementUseCase(
	tx playerusecase.ManagementTransactionManager,
	players playerusecase.PlayerRepository,
	leaderboard playerusecase.LeaderboardInvalidator,
	clk playerusecase.ManagementClock,
) *playerusecase.ManagementUseCase {
	return playerusecase.ManagementNewUseCase(tx, players, leaderboard, clk)
}

func provideLeaderboardRanking(repository leaderboardusecase.StatsRepository) *leaderboardusecase.Ranking {
	return leaderboardusecase.NewRanking(repository)
}

func provideLeaderboardCache(
	ranking *leaderboardusecase.Ranking,
	clock leaderboardusecase.Clock,
) *leaderboardusecase.Cache {
	return leaderboardusecase.NewCache(ranking, clock)
}

func providePlayerSessionUseCase(
	cfg *config.Config,
	tx playerusecase.SessionTransactionManager,
	players playerusecase.Repository,
	clk playerusecase.SessionClock,
) *playerusecase.SessionUseCase {
	return playerusecase.SessionNewUseCase(
		tx,
		players,
		clk,
		playerusecase.WithSessionTTL(cfg.Player.SessionTTL),
	)
}

func provideSourceFiles(
	tasks taskusecase.Catalog,
	storage taskusecase.SourceFileStorage,
	cleanup taskusecase.CleanupRunner,
	log logkit.Logger,
) *taskusecase.SourceFiles {
	return taskusecase.NewSourceFiles(tasks, storage, cleanup, tasktelemetry.NewSourceFileCleanupLogger(log))
}

func provideParticipantArchive(
	repository participantarchive.Repository,
	signer participantarchive.Signer,
	clock participantarchive.Clock,
) (*participantarchive.Service, error) {
	return participantarchive.New(repository, signer, clock)
}

type detachedCleanupRunner struct{}

func (detachedCleanupRunner) Run(
	ctx context.Context,
	timeout time.Duration,
	cleanup func(context.Context) error,
) error {
	cleanupCtx, cancel := ctxutil.DetachedWithTimeout(ctx, timeout)
	defer cancel()
	return cleanup(cleanupCtx)
}

func provideCleanupRunner() taskusecase.CleanupRunner {
	return detachedCleanupRunner{}
}

func provideTournamentIDGenerator() (*catalogusecase.DeterministicIDGenerator, error) {
	namespace := uuid.NewSHA1(uuid.NameSpaceOID, []byte("task-per-minute:tournament-commands"))
	return catalogusecase.NewDeterministicIDGenerator(namespace)
}

func provideTournamentCatalog(
	repository *catalogrepo.TournamentCatalogPostgres,
	clk catalogusecase.CatalogClock,
) *catalogusecase.TournamentUseCase {
	return catalogusecase.NewTournamentUseCase(repository, clk)
}

func provideTournamentApplication(
	ids catalogusecase.IDGenerator,
	clock catalogusecase.Clock,
	lister catalogusecase.TournamentLister,
	createStore catalogusecase.TournamentCreateStore,
	contentReader catalogusecase.ContentReader,
	receipts idempotency.Store,
) *catalogusecase.UseCase {
	return catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs:           ids,
		Clock:         clock,
		Lister:        lister,
		CreateStore:   createStore,
		ContentReader: contentReader,
		Receipts:      receipts,
	})
}

func provideTournamentCommandReceipts(client *goredis.Client) *redisadapter.CommandReceiptStore {
	return redisadapter.NewCommandReceiptStore(client, 30*time.Second, 24*time.Hour, 30*time.Second)
}

func provideDistributedCommandCoordinator(receipts idempotency.Store) *idempotency.Coordinator {
	return idempotency.NewCoordinator(receipts)
}

func provideParticipantReadiness(
	repository *participantreadinessrepo.ParticipantReadinessRepository,
	clock clockFunc,
) *readiness.ReadinessUseCase {
	return readiness.NewReadinessUseCase(repository, clock)
}

func provideParticipantDraft(
	repository *participantdraftrepo.ParticipantDraftRepository,
	clock clockFunc,
) *draftusecase.ActionUseCase {
	return draftusecase.NewActionUseCase(repository, clock)
}

func provideParticipantSubmission(
	repository *participantsubmissionrepo.ParticipantSubmissionRepository,
) *gamesubmission.SubmissionUseCase {
	return gamesubmission.NewSubmissionUseCase(repository)
}

func provideParticipantPostSeries(
	repository *participantpostseriesrepo.ParticipantPostSeriesRepository,
	clock clockFunc,
) *tournamentparticipant.PostSeriesUseCase {
	return tournamentparticipant.NewPostSeriesUseCase(repository, clock)
}

func provideParticipantCommands(
	transactions tournamentparticipant.ParticipantTransactionManager,
	authority tournamentparticipant.CommandAuthority,
	readinessWorkflow tournamentparticipant.ReadinessWorkflow,
	draftWorkflow tournamentparticipant.DraftActionWorkflow,
	submissionWorkflow tournamentparticipant.SubmissionWorkflow,
	settlementWorkflow tournamentparticipant.SettlementWorkflow,
	surrenderWorkflow tournamentparticipant.SurrenderWorkflow,
	postSeriesWorkflow tournamentparticipant.PostSeriesWorkflow,
	postseasonWorkflow tournamentparticipant.ParticipantPostseasonWorkflow,
) *tournamentparticipant.CommandCoordinator {
	return tournamentparticipant.NewCommandCoordinator(tournamentparticipant.CommandCoordinatorDependencies{
		Transactions: transactions, Authority: authority, Readiness: readinessWorkflow,
		Draft: draftWorkflow, Submission: submissionWorkflow, Settlement: settlementWorkflow,
		Surrender: surrenderWorkflow, PostSeries: postSeriesWorkflow, Postseason: postseasonWorkflow,
	})
}

func provideParticipantSurrender(
	repository *participantsurrenderrepo.ParticipantForfeitRepository,
	clock clockFunc,
) *participantsurrenderrepo.ParticipantSurrenderWorkflow {
	return participantsurrenderrepo.NewParticipantSurrenderWorkflow(repository, clock)
}

func provideTournamentParticipantApplication(
	snapshots inbound.TournamentSnapshotUseCase,
	states tournamentparticipant.StateReader,
	commands tournamentparticipant.CommandExecutor,
) *tournamentparticipant.ParticipantUseCase {
	return tournamentparticipant.ParticipantNewUseCase(tournamentparticipant.ParticipantDependencies{
		Snapshots: snapshots,
		States:    states,
		Commands:  commands,
	})
}

func provideIdempotentTournamentParticipantApplication(
	application *tournamentparticipant.ParticipantUseCase,
	coordinator *idempotency.Coordinator,
) *tournamentparticipant.ParticipantIdempotentService {
	return tournamentparticipant.ParticipantNewIdempotentService(application, coordinator)
}

func provideObservedTournamentParticipantApplication(
	application *tournamentparticipant.ParticipantIdempotentService,
	clock tournamentparticipant.ParticipantOperationClock,
	observer *telemetryadapter.TournamentParticipantObserver,
) *tournamentparticipant.ParticipantObservedService {
	return tournamentparticipant.ParticipantNewObservedService(application, clock, observer)
}

func provideTournamentLifecycle(
	repository lifecycleusecase.TournamentLifecycleRepository,
	clock lifecycleusecase.LifecycleClock,
) *lifecycleusecase.TournamentLifecycleUseCase {
	return lifecycleusecase.NewTournamentLifecycleUseCase(repository, clock)
}

func provideTournamentPause(
	transactions tournamentpause.PauseTransactionManager,
	repository tournamentpause.TournamentPauseRepository,
	clock tournamentpause.PauseClock,
) *tournamentpause.TournamentPauseUseCase {
	return tournamentpause.NewTournamentPauseUseCase(transactions, repository, clock)
}

func provideTournamentCancellation(
	repository tournamentcancellation.TournamentCancellationRepository,
	clock tournamentcancellation.CancellationClock,
) *tournamentcancellation.TournamentCancellationUseCase {
	return tournamentcancellation.NewTournamentCancellationUseCase(repository, clock)
}

func provideTournamentAdminLifecycle(
	transactions tournamentadmin.LifecycleTransactionManager,
	repository tournamentadmin.LifecycleWorkflowRepository,
	transitions tournamentadmin.LifecycleTransitioner,
	pauses tournamentadmin.LifecyclePauser,
	cancellations tournamentadmin.LifecycleCanceller,
	progressions tournamentadmin.LifecycleProgression,
	clock tournamentadmin.AdminLifecycleClock,
) *tournamentadmin.LifecycleWorkflow {
	return tournamentadmin.NewLifecycleWorkflow(tournamentadmin.LifecycleWorkflowDependencies{
		Transactions:  transactions,
		Repository:    repository,
		Transitions:   transitions,
		Pauses:        pauses,
		Cancellations: cancellations,
		Progressions:  progressions,
		Clock:         clock,
	})
}

func provideTournamentAdminRoster(
	transactions tournamentadminroster.RosterTransactionManager,
	repository tournamentadminroster.RosterWorkflowRepository,
	runtimeHealth tournamentadminroster.PreflightRuntimeHealthSource,
) *tournamentadminroster.RosterWorkflow {
	return tournamentadminroster.NewRosterWorkflow(tournamentadminroster.RosterWorkflowDependencies{
		Transactions:  transactions,
		Repository:    repository,
		RuntimeHealth: runtimeHealth,
	})
}

func provideTournamentAdminExecution(
	transactions tournamentadmin.ExecutionTransactionManager,
	repository tournamentadmin.ExecutionWorkflowRepository,
	normalPause tournamentadmin.NormalPauseExecutionRepository,
	waveRepository gamestart.StartRepository,
	authority *authorityusecase.Controller,
	clock clockFunc,
) *tournamentadmin.ExecutionWorkflow {
	return tournamentadmin.NewExecutionWorkflow(tournamentadmin.ExecutionWorkflowDependencies{
		Transactions: transactions,
		Repository:   repository,
		NormalPause:  normalPause,
		Authority:    authority,
		WaveStart:    gamestart.NewStartUseCase(waveRepository, clock),
	})
}

func provideTournamentAdminResults(
	transactions tournamentadminresult.OperatorResultTransactionManager,
	repository tournamentadminresult.OperatorResultWorkflowRepository,
	postseason tournamentadminresult.PostseasonWorkflow,
) *tournamentadminresult.OperatorResultWorkflow {
	return tournamentadminresult.NewOperatorResultWorkflow(tournamentadminresult.OperatorResultWorkflowDependencies{
		Transactions: transactions,
		Repository:   repository,
		Postseason:   postseason,
	})
}

func provideFinalDraftAssignmentPlanner(
	workflow playoff.ExactDraftPlanWorkflow,
	authority playoff.ExactDraftPlanAuthorityReader,
	committed playoff.ExactDraftCommittedPlanReader,
) *playoff.FinalDraftAssignmentService {
	return playoff.NewFinalDraftAssignmentService(workflow, authority, committed)
}

func providePlayoffTerminal(
	repository playoff.TerminalRepository,
	publisher resultprojection.FinalPublicationRepository,
	planner playoff.FinalDraftAssignmentPlanner,
	rehydrator playoff.FinalBindingRehydrator,
) *playoff.TerminalCoordinator {
	return playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository: repository, Publisher: publisher, DraftPlanner: planner, Rehydrator: rehydrator,
	})
}

func providePlayoffTerminalRepository(
	tx *postgres.TxManager,
	drafts *draftrepo.DraftPostgres,
	assignments *assignmentrepo.AssignmentPostgres,
) *playoffrepo.PlayoffTerminalPostgres {
	var draftRepository playoffrepo.DraftRepository
	if drafts != nil {
		draftRepository = drafts
	}
	var createAssignmentTx playoffrepo.AssignmentWriter
	if assignments != nil {
		createAssignmentTx = assignments.CreateAssignmentTx
	}
	return playoffrepo.NewPlayoffTerminalPostgres(tx, draftRepository, createAssignmentTx)
}

func provideTournamentProgression(
	repository tournamentprogression.Repository,
	terminalEvidence tournamentprogression.SwissTerminalEvidenceReader,
	transitioner tournamentprogression.Transitioner,
	publisher tournamentprogression.PlayoffProjectionPublisher,
	clock tournamentprogression.ProgressionClock,
) *tournamentprogression.Workflow {
	return tournamentprogression.NewWorkflow(tournamentprogression.ProgressionDependencies{
		Repository: repository, TerminalEvidence: terminalEvidence, Transitioner: transitioner,
		Publisher: publisher, ProgressionClock: clock,
	})
}

func provideTournamentAdminReplay(
	transactions tournamentadminreplay.ReplayTransactionManager,
	repository tournamentadminreplay.ReplayWorkflowRepository,
) *tournamentadminreplay.ReplayWorkflow {
	return tournamentadminreplay.NewReplayWorkflow(tournamentadminreplay.ReplayWorkflowDependencies{
		Transactions: transactions,
		Repository:   repository,
	})
}

func provideTournamentAdminCorrection(
	transactions tournamentadmin.CorrectionTransactionManager,
	repository tournamentadmin.CorrectionWorkflowRepository,
) *tournamentadmin.CorrectionWorkflow {
	return tournamentadmin.NewCorrectionWorkflow(tournamentadmin.CorrectionWorkflowDependencies{
		Transactions: transactions,
		Repository:   repository,
	})
}

func provideTournamentAdminApplication(
	catalog inbound.TournamentUseCase,
	roster tournamentadminroster.RosterPort,
	preflight tournamentadminroster.PreflightPort,
	pairing tournamentadmin.PairingPort,
	lifecycle tournamentadmin.LifecyclePort,
	wave tournamentadmin.WavePort,
	noShow tournamentadminresult.NoShowPort,
	reserve tournamentadminreplay.ReservePort,
	forfeit tournamentadminresult.ForfeitPort,
	replay tournamentadminreplay.ReplayPort,
	correction tournamentadmin.CorrectionPort,
	audit tournamentadmin.AuditPort,
	incidents tournamentadmin.IncidentSnapshotPort,
	signer tournamentadmin.IncidentAuthenticator,
	snapshots tournamentadmin.SnapshotPort,
) *tournamentadmin.AdminUseCase {
	return tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{
		Catalog:    catalog,
		Roster:     roster,
		Preflight:  preflight,
		Pairing:    pairing,
		Lifecycle:  lifecycle,
		Wave:       wave,
		NoShow:     noShow,
		Reserve:    reserve,
		Forfeit:    forfeit,
		Replay:     replay,
		Correction: correction,
		Audit:      audit,
		Incidents:  incidents,
		Signer:     signer,
		Snapshots:  snapshots,
	})
}

func provideIdempotentTournamentAdminApplication(
	application *tournamentadmin.AdminUseCase,
	catalog inbound.TournamentUseCase,
	coordinator *idempotency.Coordinator,
) (*tournamentadmin.AdminIdempotentService, error) {
	return tournamentadmin.AdminNewIdempotentService(application, catalog, coordinator)
}

func provideObservedTournamentAdminApplication(
	application *tournamentadmin.AdminIdempotentService,
	clock tournamentadmin.OperationClock,
	observer *telemetryadapter.TournamentAdminObserver,
) *tournamentadmin.AdminObservedService {
	return tournamentadmin.AdminNewObservedService(application, clock, observer)
}

func provideTournamentAdminInbound(service tournamentadmin.AdminService) inbound.TournamentAdminUseCase {
	return tournamentadmin.NewInboundAdapter(service)
}
