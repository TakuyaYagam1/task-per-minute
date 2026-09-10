package bootstrap

import (
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func provideRealtimeOutbox(
	tx *postgres.TxManager,
) *postgres.RealtimeOutboxPostgres {
	return postgres.NewRealtimeOutboxPostgres(tx)
}

func provideRealtimeDelivery(
	repository *postgres.RealtimeOutboxPostgres,
	clock clockFunc,
) (*websocket.RealtimeDelivery, error) {
	return websocket.NewRealtimeDelivery(repository, websocket.RealtimeDeliveryConfig{
		InstanceID: uuid.New(),
		WorkerID:   uuid.New(),
		Now:        clock.Now,
	})
}

func provideObservedEventDeliveryWorker(
	repository *postgres.RealtimeOutboxPostgres,
	delivery *websocket.RealtimeDelivery,
	clock clockFunc,
	observer *telemetryadapter.EventDeliveryObserver,
) (*eventdelivery.Worker, error) {
	return eventdelivery.NewWorker(repository, delivery, eventdelivery.WorkerConfig{
		WorkerID: uuid.New(), Now: clock.Now, StaleAfter: runtimeEventDeliveryLastSuccessStaleAfter,
	}, observer)
}

func provideEventDeliveryHealth(
	worker *eventdelivery.Worker,
) eventdelivery.HealthSource {
	return worker
}

func provideOutboxBacklog(
	repository *postgres.RealtimeOutboxPostgres,
) eventdelivery.BacklogSource {
	return repository
}

func provideReceiptRetentionWorker(
	repository *postgres.RealtimeOutboxPostgres,
	cfg *config.Config,
	clock clockFunc,
) (*eventdelivery.ReceiptRetentionWorker, error) {
	return eventdelivery.NewReceiptRetentionWorker(repository, eventdelivery.ReceiptRetentionWorkerConfig{
		Retention: cfg.WS.DeliveryReceiptRetention,
		Interval:  cfg.WS.DeliveryReceiptCleanupInterval,
		BatchSize: cfg.WS.DeliveryReceiptCleanupBatchSize,
		Now:       clock.Now,
	})
}

func providePrivateTaskAvailabilityRepository(
	tx *postgres.TxManager,
) *postgres.PrivateTaskAvailabilityPostgres {
	return postgres.NewPrivateTaskAvailabilityPostgres(tx)
}

func providePrivateTaskAvailabilityMonitor(
	source taskusecase.BacklogSource,
	clock clockFunc,
) (*taskusecase.AvailabilityMonitor, error) {
	return taskusecase.NewAvailabilityMonitor(source, taskusecase.AvailabilityMonitorConfig{
		StaleAfter: runtimePrivateTaskAvailabilityLastSuccessStaleAfter,
		Now:        clock.Now,
	})
}

func provideRuntimeWorkerHeartbeats(
	client *goredis.Client,
) *redisadapter.RuntimeWorkerHeartbeats {
	return redisadapter.NewRuntimeWorkerHeartbeats(client)
}

// provideRuntimeWorkers inserts the authoritative game
// recovery scan after the durable scheduler is ready and before the generic
// deadline sweep. Its initial scan is part of startup readiness, so HTTP does
// not receive traffic while a locally-owned game remains unrecovered.
func provideRuntimeWorkers(
	dispatcher *observability.TournamentEventDispatcher,
	outboxDelivery *eventdelivery.Worker,
	sessionDelivery *websocket.RealtimeDelivery,
	receiptRetention *eventdelivery.ReceiptRetentionWorker,
	privateTaskAvailability *taskusecase.AvailabilityMonitor,
	deadlineScheduler *recovery.DeadlineScheduler,
	executionRecovery *gameusecase.RecoveryRunner,
	recoveryWorker *recovery.Worker,
	clock clockFunc,
	heartbeats *redisadapter.RuntimeWorkerHeartbeats,
) (*runtimeWorkers, error) {
	entries := []namedRuntimeWorker{
		{
			name:   "tournament-observer",
			worker: dispatcher,
			ready:  dispatcher.Ready,
		},
		{
			name:   "deadline-scheduler",
			worker: deadlineScheduler,
			ready: func() bool {
				return deadlineScheduler.ExecutionHealth().Running
			},
		},
		{
			name:   "private-task-availability",
			worker: privateTaskAvailability,
			ready:  privateTaskAvailability.Ready,
		},
		{
			name:   "execution-recovery",
			worker: executionRecovery,
			ready:  executionRecovery.Ready,
		},
		{
			name:   "deadline-recovery",
			worker: recoveryWorker,
			ready: func() bool {
				return recoveryWorker.Health(clock.Now()).Ready
			},
		},
		{
			name:   "event-delivery",
			worker: outboxDelivery,
			ready: func() bool {
				health := outboxDelivery.Health(clock.Now())
				return health.Started && health.Running && health.LastSuccessAt != nil &&
					health.ConsecutiveFailures == 0 && !health.Stale
			},
		},
		{
			name:   "realtime-session-delivery",
			worker: sessionDelivery,
			ready:  sessionDelivery.Ready,
		},
		{
			name:   "realtime-receipt-retention",
			worker: receiptRetention,
			ready:  receiptRetention.Ready,
		},
	}
	workers, err := newRuntimeWorkers(
		entries...,
	)
	if err != nil {
		return nil, err
	}
	workers.configureRuntimeWorkerHeartbeats(heartbeats, uuid.New(), clock)
	return workers, nil
}
