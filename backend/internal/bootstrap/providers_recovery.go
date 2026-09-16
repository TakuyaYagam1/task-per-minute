package bootstrap

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	executionrecoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/recovery"
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	terminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	gamerecovery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func provideRecoveryTerminalStore(
	transactions *postgres.TxManager,
	authority *authorityrepo.ExecutionAuthorityPostgres,
	clock clockFunc,
) *terminalrepo.RecoveryTerminalPostgres {
	return terminalrepo.NewRecoveryTerminalPostgresWithDependencies(
		transactions,
		authority,
		clock,
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
		resultauthority.FinalizeProjection,
	)
}

func provideRecoveryDeadlineHandler(
	transactions *postgres.TxManager,
	store *terminalrepo.RecoveryTerminalPostgres,
	clock clockFunc,
	terminalAdvancer recovery.TerminalAdvancer,
	observer *telemetryadapter.ReconnectObserver,
) *recovery.TerminalDeadlineHandler {
	// DeadlineSweep observes the rearm operation. The reconnect observer is
	// passed only to the reconnect timeout mutation, which emits its own single
	// terminal outcome after the authoritative plan commits.
	return recovery.NewTerminalDeadlineHandlerWithDependencies(
		transactions, store, clock, terminalAdvancer, observer,
	)
}

func provideRecoveryDeadlineScheduler(
	handler *recovery.TerminalDeadlineHandler,
) (*recovery.DeadlineScheduler, error) {
	return recovery.NewDeadlineScheduler(handler)
}

func provideRecoveryRepository(
	transactions *postgres.TxManager,
	scheduler *recovery.DeadlineScheduler,
) *recoveryrepo.RecoveryPostgres {
	return recoveryrepo.NewRecoveryPostgres(transactions, scheduler)
}

func provideRecoveryDeadlineSweep(
	repository *recoveryrepo.RecoveryPostgres,
	observer *telemetryadapter.RecoveryObserver,
) *recovery.DeadlineSweep {
	return recovery.NewDeadlineSweep(repository, repository, observer)
}

func provideRecoveryWorker(
	sweep *recovery.DeadlineSweep,
	clock clockFunc,
	scheduler *recovery.DeadlineScheduler,
) (*recovery.Worker, error) {
	return recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		StaleAfter: runtimeRecoveryCompletionStaleAfter,
	}, scheduler)
}

// provideExecutionAuthorityController creates one service-owned authority
// holder for this process. No inbound command can choose its holder, lease or
// epoch, and the controller obtains lease time from PostgreSQL.
func provideExecutionAuthorityController(
	repository *authorityrepo.ExecutionAuthorityPostgres,
) (*authorityusecase.Controller, error) {
	return authorityusecase.NewController(repository, repository, authorityusecase.ControllerConfig{
		HolderID: uuid.New(),
	})
}

func provideExecutionRecoveryRepository(
	transactions *postgres.TxManager,
	deadlines *recoveryrepo.RecoveryPostgres,
	terminal *terminalrepo.RecoveryTerminalPostgres,
) *executionrecoveryrepo.ExecutionRecoveryPostgres {
	return executionrecoveryrepo.NewExecutionRecoveryPostgresWithDependencies(
		transactions,
		deadlines,
		terminal,
		resultauthority.FinalizeProjection,
	)
}

func provideExecutionEpochReplay(
	repository *executionrecoveryrepo.ExecutionRecoveryPostgres,
	timeSource *authorityrepo.ExecutionAuthorityPostgres,
) *gamerecovery.EpochReplayUseCase {
	return gamerecovery.NewEpochReplayUseCase(repository, timeSource)
}

func provideExecutionRecoverer(
	authority *authorityrepo.ExecutionAuthorityPostgres,
	repository *executionrecoveryrepo.ExecutionRecoveryPostgres,
	replayer *gamerecovery.EpochReplayUseCase,
) *gamerecovery.Recoverer {
	return gamerecovery.NewRecoverer(authority, repository, repository, replayer, authority)
}

func provideExecutionRecoveryRunner(
	repository *executionrecoveryrepo.ExecutionRecoveryPostgres,
	authority *authorityusecase.Controller,
	recoverer *gamerecovery.Recoverer,
	clock clockFunc,
	observer *telemetryadapter.ExecutionRecoveryObserver,
	golden inbound.GoldenUseCase,
) (*gamerecovery.RecoveryRunner, error) {
	return gamerecovery.NewRecoveryRunner(repository, authority, recoverer, clock, gamerecovery.RecoveryRunnerConfig{
		Observer: observer,
		Golden:   golden,
	})
}
