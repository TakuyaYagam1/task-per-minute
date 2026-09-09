package bootstrap

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func provideRecoveryTerminalStore(
	transactions *postgres.TxManager,
	authority *postgres.ExecutionAuthorityPostgres,
	clock clockFunc,
) *postgres.RecoveryTerminalPostgres {
	return postgres.NewRecoveryTerminalPostgres(transactions, authority, clock)
}

func provideRecoveryDeadlineHandler(
	store *postgres.RecoveryTerminalPostgres,
	clock clockFunc,
	observer *telemetryadapter.ReconnectObserver,
) *recovery.TerminalDeadlineHandler {
	// DeadlineSweep observes the rearm operation. The reconnect observer is
	// passed only to the reconnect timeout mutation, which emits its own single
	// terminal outcome after the authoritative plan commits.
	return recovery.NewTerminalDeadlineHandler(store, clock, observer)
}

func provideRecoveryDeadlineScheduler(
	handler *recovery.TerminalDeadlineHandler,
) (*recovery.DeadlineScheduler, error) {
	return recovery.NewDeadlineScheduler(handler)
}

func provideRecoveryRepository(
	transactions *postgres.TxManager,
	scheduler *recovery.DeadlineScheduler,
) *postgres.RecoveryPostgres {
	return postgres.NewRecoveryPostgres(transactions, scheduler)
}

func provideRecoveryDeadlineSweep(
	repository *postgres.RecoveryPostgres,
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
	repository *postgres.ExecutionAuthorityPostgres,
) (*authorityusecase.Controller, error) {
	return authorityusecase.NewController(repository, repository, authorityusecase.ControllerConfig{
		HolderID: uuid.New(),
	})
}

func provideExecutionRecoveryRepository(
	transactions *postgres.TxManager,
	deadlines *postgres.RecoveryPostgres,
	terminal *postgres.RecoveryTerminalPostgres,
) *postgres.ExecutionRecoveryPostgres {
	return postgres.NewExecutionRecoveryPostgres(transactions, deadlines, terminal)
}

func provideExecutionEpochReplay(
	repository *postgres.ExecutionRecoveryPostgres,
	timeSource *postgres.ExecutionAuthorityPostgres,
) *gameusecase.EpochReplayUseCase {
	return gameusecase.NewEpochReplayUseCase(repository, timeSource)
}

func provideExecutionRecoverer(
	authority *postgres.ExecutionAuthorityPostgres,
	repository *postgres.ExecutionRecoveryPostgres,
	replayer *gameusecase.EpochReplayUseCase,
) *gameusecase.Recoverer {
	return gameusecase.NewRecoverer(authority, repository, repository, replayer, authority)
}

func provideExecutionRecoveryRunner(
	repository *postgres.ExecutionRecoveryPostgres,
	authority *authorityusecase.Controller,
	recoverer *gameusecase.Recoverer,
	clock clockFunc,
	observer *telemetryadapter.ExecutionRecoveryObserver,
) (*gameusecase.RecoveryRunner, error) {
	return gameusecase.NewRecoveryRunner(repository, authority, recoverer, clock, gameusecase.RecoveryRunnerConfig{
		Observer: observer,
	})
}
