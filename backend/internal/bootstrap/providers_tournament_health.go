package bootstrap

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

type RealtimeHealthSource interface {
	Health(now time.Time) eventdelivery.HealthSnapshot
}

func provideProjectionHealth(tx *postgres.TxManager) observability.ProjectionHealthSource {
	return postgres.NewProjectionHealthPostgres(tx)
}

type healthDatabaseClock interface {
	DatabaseTime(ctx context.Context) (time.Time, error)
}

type healthDatabaseClockFunc func(ctx context.Context) (time.Time, error)

func (fn healthDatabaseClockFunc) DatabaseTime(ctx context.Context) (time.Time, error) {
	if fn == nil {
		return time.Time{}, postgres.ErrNilPool
	}
	return fn(ctx)
}

type healthProbe struct {
	runtime          context.Context
	realtime         RealtimeHealthSource
	pool             *pgxpool.Pool
	databaseClock    healthDatabaseClock
	clock            clockFunc
	metrics          *observability.TournamentMetrics
	taskAvailability taskusecase.HealthSource
	receiptBacklog   taskusecase.BacklogSource
	outbox           eventdelivery.HealthSource
	backlog          eventdelivery.BacklogSource
	projection       observability.ProjectionHealthSource
	recovery         recovery.WorkerHealthSource
	workers          runtimeWorkerHealthSource
	heartbeats       observability.RuntimeWorkerHeartbeatReader
}

func provideHealthProbe(
	runtime context.Context,
	realtime RealtimeHealthSource,
	pool *pgxpool.Pool,
	clk clockFunc,
	telemetry eventTelemetry,
	taskAvailability taskusecase.HealthSource,
	receiptBacklog taskusecase.BacklogSource,
	outbox eventdelivery.HealthSource,
	backlog eventdelivery.BacklogSource,
	recoveryWorker recovery.WorkerHealthSource,
	workers *runtimeWorkers,
	heartbeats observability.RuntimeWorkerHeartbeatReader,
	projection observability.ProjectionHealthSource,
) *healthProbe {
	var workerHealth runtimeWorkerHealthSource
	if workers != nil {
		workerHealth = workers
	}
	return newHealthProbe(
		runtime,
		realtime,
		pool,
		clk,
		telemetry,
		taskAvailability,
		receiptBacklog,
		outbox,
		backlog,
		recoveryWorker,
		workerHealth,
		heartbeats,
		projection,
	)
}

func newHealthProbe(
	runtime context.Context,
	realtime RealtimeHealthSource,
	pool *pgxpool.Pool,
	clk clockFunc,
	telemetry eventTelemetry,
	taskAvailability taskusecase.HealthSource,
	receiptBacklog taskusecase.BacklogSource,
	outbox eventdelivery.HealthSource,
	backlog eventdelivery.BacklogSource,
	recoveryWorker recovery.WorkerHealthSource,
	workers runtimeWorkerHealthSource,
	heartbeats observability.RuntimeWorkerHeartbeatReader,
	projection observability.ProjectionHealthSource,
) *healthProbe {
	probe := &healthProbe{
		runtime:          runtime,
		realtime:         realtime,
		pool:             pool,
		clock:            clk,
		metrics:          telemetry.metrics,
		taskAvailability: taskAvailability,
		receiptBacklog:   receiptBacklog,
		outbox:           outbox,
		backlog:          backlog,
		projection:       projection,
		recovery:         recoveryWorker,
		workers:          workers,
		heartbeats:       heartbeats,
	}
	probe.databaseClock = healthDatabaseClockFunc(func(ctx context.Context) (time.Time, error) {
		if pool == nil {
			return time.Time{}, postgres.ErrNilPool
		}
		var databaseNow time.Time
		if err := pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
			return time.Time{}, err
		}
		return databaseNow.Round(0).UTC(), nil
	})
	return probe
}

func (probe *healthProbe) TournamentHealth(ctx context.Context) observability.TournamentHealthSnapshot {
	snapshot := observability.HealthyTournamentHealthSnapshot()
	if probe == nil || probe.pool == nil || postgres.HealthCheck(ctx, probe.pool) != nil {
		failed := observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateFailed,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
		snapshot.Authority = failed
		snapshot.Submission = failed
		snapshot.TaskDelivery = failed
		snapshot.Outbox = failed
		snapshot.Projection = failed
		snapshot.Clock = failed
		snapshot.Recovery = failed
	} else {
		probe.measureClockDrift(ctx, &snapshot)
		var now time.Time
		if probe.clock != nil {
			now = probe.clock.Now().Round(0).UTC()
		}
		delivery := workerBoundDependencyHealth(
			eventDeliveryHealthStatus(probe.outbox, now),
			probe.workers,
			"event-delivery",
		)
		delivery = combinedDependencyHealth(delivery, probe.sharedWorkerHealth(
			ctx,
			"event-delivery",
			now,
		))
		// Durable submission intake is the same PostgreSQL authority checked
		// above. Private task availability is separately verified from its local
		// receipt-availability monitor and the shared durable receipt backlog.
		snapshot.Submission = snapshot.Authority
		snapshot.TaskDelivery = workerBoundDependencyHealth(
			privateTaskAvailabilityHealthStatus(probe.taskAvailability, now),
			probe.workers,
			"private-task-availability",
		)
		snapshot.TaskDelivery = combinedDependencyHealth(snapshot.TaskDelivery, probe.sharedWorkerHealth(
			ctx,
			"private-task-availability",
			now,
		))
		snapshot.TaskDelivery = combinedDependencyHealth(snapshot.TaskDelivery, probe.privateTaskAvailabilityBacklogHealth(ctx, now))
		snapshot.Outbox = probe.outboxHealth(ctx, now, delivery)
		snapshot.Projection = combinedDependencyHealth(probe.projectionHealth(ctx), delivery)
		snapshot.Recovery = probe.recoveryHealth(ctx, now)
	}
	if probe == nil || probe.runtime == nil || probe.realtime == nil {
		snapshot.Realtime = failedTournamentDependency()
	} else {
		var now time.Time
		if probe.clock != nil {
			now = probe.clock.Now().Round(0).UTC()
		}
		snapshot.Realtime = workerBoundDependencyHealth(
			eventDeliveryHealthStatus(probe.realtime, now),
			probe.workers,
			"realtime-session-delivery",
		)
		snapshot.Realtime = combinedDependencyHealth(snapshot.Realtime, probe.sharedWorkerHealth(
			ctx,
			"realtime-session-delivery",
			now,
		))
		select {
		case <-probe.runtime.Done():
			snapshot.Realtime = failedTournamentDependency()
		default:
		}
	}
	return snapshot
}

func (probe *healthProbe) projectionHealth(ctx context.Context) observability.TournamentDependencyStatus {
	if ctx == nil || probe == nil || probe.projection == nil {
		return failedTournamentDependency()
	}
	snapshot, err := probe.projection.ProjectionHealth(ctx)
	if err != nil {
		return failedTournamentDependency()
	}
	return projectionLagHealthStatus(snapshot)
}

func (probe *healthProbe) recoveryHealth(
	ctx context.Context,
	now time.Time,
) observability.TournamentDependencyStatus {
	if probe == nil {
		return failedTournamentDependency()
	}
	status := workerBoundDependencyHealth(
		recoveryHealthStatus(probe.recovery, now),
		probe.workers,
		"deadline-scheduler",
	)
	status = combinedDependencyHealth(status, probe.sharedWorkerHealth(
		ctx,
		"deadline-scheduler",
		now,
	))
	status = workerBoundDependencyHealth(status, probe.workers, "deadline-recovery")
	status = combinedDependencyHealth(status, probe.sharedWorkerHealth(
		ctx,
		"deadline-recovery",
		now,
	))
	status = workerBoundDependencyHealth(status, probe.workers, "execution-recovery")
	return combinedDependencyHealth(status, probe.sharedWorkerHealth(
		ctx,
		"execution-recovery",
		now,
	))
}

func (probe *healthProbe) sharedWorkerHealth(
	ctx context.Context,
	worker string,
	now time.Time,
) observability.TournamentDependencyStatus {
	if probe == nil || probe.heartbeats == nil {
		return failedTournamentDependency()
	}
	if ctx == nil || !domain.IsValidServerTime(now) {
		return failedTournamentDependency()
	}
	status, err := probe.heartbeats.RuntimeWorkerHeartbeatStatus(ctx, worker, now.Add(-runtimeWorkerHeartbeatStaleAfter))
	if err != nil || status.Worker != worker {
		return failedTournamentDependency()
	}
	if status.Healthy() {
		return healthyTournamentDependency()
	}
	return observability.TournamentDependencyStatus{
		Health:    observability.TournamentHealthStateDegraded,
		Readiness: observability.TournamentReadinessStateStale,
	}
}

func (probe *healthProbe) outboxHealth(
	ctx context.Context,
	now time.Time,
	delivery observability.TournamentDependencyStatus,
) observability.TournamentDependencyStatus {
	if probe == nil || probe.backlog == nil || !domain.IsValidServerTime(now) {
		return failedTournamentDependency()
	}
	backlog, err := probe.backlog.Backlog(ctx)
	if err != nil || backlog.Validate() != nil {
		return failedTournamentDependency()
	}
	return outboxBacklogHealthStatus(backlog, now, delivery)
}

func (probe *healthProbe) privateTaskAvailabilityBacklogHealth(
	ctx context.Context,
	now time.Time,
) observability.TournamentDependencyStatus {
	if probe == nil || probe.receiptBacklog == nil || !domain.IsValidServerTime(now) {
		return failedTournamentDependency()
	}
	backlog, err := probe.receiptBacklog.TaskDeliveryBacklog(ctx)
	if err != nil {
		return failedTournamentDependency()
	}
	return privateTaskAvailabilityBacklogHealthStatus(backlog, now)
}

func privateTaskAvailabilityBacklogHealthStatus(
	backlog taskusecase.BacklogSnapshot,
	now time.Time,
) observability.TournamentDependencyStatus {
	pendingAge, valid := backlogAge(now, backlog.OldestPendingAt)
	if backlog.Validate() != nil || !valid {
		return failedTournamentDependency()
	}
	if backlog.PendingCount >= runtimePrivateTaskAvailabilityBacklogFailedCount ||
		pendingAge >= runtimePrivateTaskAvailabilityOldestFailedAge {
		return failedTournamentDependency()
	}
	if backlog.PendingCount >= runtimePrivateTaskAvailabilityBacklogDegradedCount ||
		pendingAge >= runtimePrivateTaskAvailabilityOldestDegradedAge {
		return observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}
	}
	return healthyTournamentDependency()
}

func outboxBacklogHealthStatus(
	backlog eventdelivery.BacklogSnapshot,
	now time.Time,
	delivery observability.TournamentDependencyStatus,
) observability.TournamentDependencyStatus {
	pendingAge, pendingValid := backlogAge(now, backlog.OldestPendingAt)
	if backlog.Validate() != nil || !pendingValid {
		return failedTournamentDependency()
	}
	if backlog.PendingCount >= runtimeOutboxBacklogFailedCount || pendingAge >= runtimeOutboxOldestFailedAge {
		return failedTournamentDependency()
	}
	durable := healthyTournamentDependency()
	if backlog.PendingCount >= runtimeOutboxBacklogDegradedCount || pendingAge >= runtimeOutboxOldestDegradedAge {
		durable = observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}
	}
	return combinedDependencyHealth(durable, delivery)
}

func backlogAge(now time.Time, oldest *time.Time) (time.Duration, bool) {
	if oldest == nil {
		return 0, true
	}
	if !domain.IsValidServerTime(now) || !domain.IsValidServerTime(*oldest) || oldest.After(now) {
		return 0, false
	}
	return now.Sub(*oldest), true
}

func eventDeliveryHealthStatus(
	source eventdelivery.HealthSource,
	now time.Time,
) observability.TournamentDependencyStatus {
	if source == nil || !domain.IsValidServerTime(now) {
		return failedTournamentDependency()
	}
	health := source.Health(now)
	if !validEventDeliveryHealthSnapshot(health, now) {
		return failedTournamentDependency()
	}
	switch {
	case !health.Started || !health.Running || health.ConsecutiveFailures >= runtimeEventDeliveryFailedAfter:
		return failedTournamentDependency()
	case health.LastSuccessAt == nil:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
	case health.Stale || now.Sub(*health.LastSuccessAt) > runtimeEventDeliveryLastSuccessStaleAfter:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateStale,
		}
	default:
		return healthyTournamentDependency()
	}
}

func privateTaskAvailabilityHealthStatus(
	source taskusecase.HealthSource,
	now time.Time,
) observability.TournamentDependencyStatus {
	if source == nil || !domain.IsValidServerTime(now) {
		return failedTournamentDependency()
	}
	health := source.Health(now)
	if health.Validate() != nil || privateTaskAvailabilityHealthHasFutureTimestamp(health, now) {
		return failedTournamentDependency()
	}
	switch {
	case !health.Started || !health.Running || health.ConsecutiveFailures >= runtimePrivateTaskAvailabilityFailedAfter:
		return failedTournamentDependency()
	case health.LastSuccessAt == nil:
		return observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}
	case health.Stale || now.Sub(*health.LastSuccessAt) > runtimePrivateTaskAvailabilityLastSuccessStaleAfter:
		return observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}
	default:
		return healthyTournamentDependency()
	}
}

func privateTaskAvailabilityHealthHasFutureTimestamp(
	health taskusecase.HealthSnapshot,
	now time.Time,
) bool {
	for _, value := range []*time.Time{
		health.StartedAt,
		health.LastAttemptAt,
		health.LastSuccessAt,
		health.LastFailureAt,
	} {
		if value != nil && value.After(now) {
			return true
		}
	}
	return false
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validEventDeliveryHealthSnapshot(health eventdelivery.HealthSnapshot, now time.Time) bool {
	if health.ConsecutiveFailures < 0 || health.Running && !health.Started ||
		health.Started != (health.StartedAt != nil) {
		return false
	}
	values := []*time.Time{
		health.StartedAt,
		health.LastAttemptAt,
		health.LastSuccessAt,
		health.LastFailureAt,
	}
	for _, value := range values {
		if value != nil && (!domain.IsValidServerTime(*value) || value.After(now)) {
			return false
		}
	}
	if health.StartedAt != nil {
		for _, value := range values[1:] {
			if value != nil && value.Before(*health.StartedAt) {
				return false
			}
		}
	}
	if health.LastSuccessAt != nil &&
		(health.LastAttemptAt == nil || health.LastSuccessAt.After(*health.LastAttemptAt)) {
		return false
	}
	if health.LastFailureAt != nil &&
		(health.LastAttemptAt == nil || health.LastFailureAt.After(*health.LastAttemptAt)) {
		return false
	}
	return health.ConsecutiveFailures == 0 || health.LastFailureAt != nil
}

func recoveryHealthStatus(
	source recovery.WorkerHealthSource,
	now time.Time,
) observability.TournamentDependencyStatus {
	if source == nil || !domain.IsValidServerTime(now) {
		return failedTournamentDependency()
	}
	health := source.Health(now)
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch health.State {
	case recovery.WorkerHealthHealthy:
		if health.Ready {
			return healthyTournamentDependency()
		}
		return failedTournamentDependency()
	case recovery.WorkerHealthStale:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateStale,
		}
	case recovery.WorkerHealthStarting:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
	default:
		return failedTournamentDependency()
	}
}

func healthyTournamentDependency() observability.TournamentDependencyStatus {
	return observability.TournamentDependencyStatus{
		Health:    observability.TournamentHealthStateHealthy,
		Readiness: observability.TournamentReadinessStateReady,
	}
}

func failedTournamentDependency() observability.TournamentDependencyStatus {
	return observability.TournamentDependencyStatus{
		Health:    observability.TournamentHealthStateFailed,
		Readiness: observability.TournamentReadinessStateNotReady,
	}
}

func (probe *healthProbe) measureClockDrift(
	ctx context.Context,
	snapshot *observability.TournamentHealthSnapshot,
) {
	if probe == nil || snapshot == nil || probe.clock == nil || probe.databaseClock == nil {
		snapshot.Clock = observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateFailed,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
		return
	}
	before, beforeOK := healthLocalClockNow(probe.clock)
	databaseNow, err := probe.databaseClock.DatabaseTime(ctx)
	after, afterOK := healthLocalClockNow(probe.clock)
	if !beforeOK || !afterOK || err != nil || !domain.IsValidServerTime(databaseNow) || after.Before(before) {
		snapshot.Clock = observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateFailed,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
		return
	}
	drift := midpointRuntimeTime(before, after).Sub(databaseNow.Round(0).UTC())
	if probe.metrics != nil {
		_ = probe.metrics.SetClockDrift("database", drift)
	}
	absoluteDrift := drift
	if absoluteDrift < 0 {
		absoluteDrift = -absoluteDrift
	}
	switch {
	case absoluteDrift > runtimeClockFailedAfter:
		snapshot.Clock = observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateFailed,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
	case absoluteDrift > runtimeClockDegradedAfter:
		snapshot.Clock = observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
	}
}

func healthLocalClockNow(clock clockFunc) (now time.Time, ok bool) {
	if clock == nil {
		return time.Time{}, false
	}
	defer func() {
		if recover() != nil {
			now = time.Time{}
			ok = false
		}
	}()
	now = clock.Now().Round(0).UTC()
	return now, domain.IsValidServerTime(now)
}

func provideHealthChecks(
	pool *pgxpool.Pool,
	redis *goredis.Client,
	seaweed *objectstorage.SeaweedStorage,
	schemaVersion restv1.SchemaVersionReader,
	tournament *healthProbe,
) restv1.HealthChecks {
	return restv1.HealthChecks{
		DB: restv1.HealthCheckerFunc(func(ctx context.Context) error {
			return postgres.HealthCheck(ctx, pool)
		}),
		Redis: restv1.HealthCheckerFunc(func(ctx context.Context) error {
			return redisadapter.HealthCheck(ctx, redis)
		}),
		SeaweedFS: restv1.HealthCheckerFunc(func(ctx context.Context) error {
			return seaweed.Health(ctx)
		}),
		SchemaVersion: schemaVersion,
		Tournament:    tournament,
	}
}
