package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

const runtimePreflightProbeTimeout = time.Second

type preflightDependencyProbe interface {
	Probe(ctx context.Context) error
}

type preflightDependencyProbeFunc func(ctx context.Context) error

func (fn preflightDependencyProbeFunc) Probe(ctx context.Context) error {
	if fn == nil {
		return fmt.Errorf("preflight dependency probe unavailable")
	}
	return fn(ctx)
}

type preflightClockProbe interface {
	DatabaseTime(ctx context.Context) (time.Time, error)
}

type preflightClockProbeFunc func(ctx context.Context) (time.Time, error)

func (fn preflightClockProbeFunc) DatabaseTime(ctx context.Context) (time.Time, error) {
	if fn == nil {
		return time.Time{}, fmt.Errorf("preflight clock probe unavailable")
	}
	return fn(ctx)
}

type preflightRuntimeHealthSource struct {
	clock            clockFunc
	taskAvailability taskusecase.HealthSource
	receiptBacklog   taskusecase.BacklogSource
	realtime         RealtimeHealthSource
	workers          runtimeWorkerHealthSource
	heartbeats       observability.RuntimeWorkerHeartbeatReader
	redis            preflightDependencyProbe
	objectStorage    preflightDependencyProbe
	databaseClock    preflightClockProbe
}

func providePreflightRuntimeHealthSource(
	clock clockFunc,
	taskAvailability taskusecase.HealthSource,
	receiptBacklog taskusecase.BacklogSource,
	realtime RealtimeHealthSource,
	workers *runtimeWorkers,
	heartbeats observability.RuntimeWorkerHeartbeatReader,
	redis *goredis.Client,
	seaweed *objectstorage.SeaweedStorage,
	pool *pgxpool.Pool,
) tournamentadmin.PreflightRuntimeHealthSource {
	return preflightRuntimeHealthSource{
		clock:            clock,
		taskAvailability: taskAvailability,
		receiptBacklog:   receiptBacklog,
		realtime:         realtime,
		workers:          workers,
		heartbeats:       heartbeats,
		redis:            preflightDependencyProbeFunc(func(ctx context.Context) error { return redisadapter.HealthCheck(ctx, redis) }),
		objectStorage:    preflightDependencyProbeFunc(func(ctx context.Context) error { return seaweed.Health(ctx) }),
		databaseClock: preflightClockProbeFunc(func(ctx context.Context) (time.Time, error) {
			if pool == nil {
				return time.Time{}, postgres.ErrNilPool
			}
			var databaseNow time.Time
			if err := pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
				return time.Time{}, fmt.Errorf("preflight database clock: %w", err)
			}
			return databaseNow.Round(0).UTC(), nil
		}),
	}
}

// RuntimeHealth samples bounded external dependencies before RosterWorkflow
// enters its transaction. It must never be invoked under tournament, roster,
// or projection locks. Every failed or unavailable probe is reported as an
// unhealthy, payload-free dependency rather than guessed from configuration.
func (source preflightRuntimeHealthSource) RuntimeHealth(ctx context.Context) tournamentpreflight.RuntimeHealth {
	now := source.localNow()
	taskDelivery := workerBoundDependencyHealth(
		privateTaskAvailabilityHealthStatus(source.taskAvailability, now),
		source.workers,
		"private-task-availability",
	)
	taskDelivery = combinedDependencyHealth(taskDelivery, source.sharedWorkerHealth(
		ctx,
		"private-task-availability",
		now,
		runtimePrivateTaskAvailabilityLastSuccessStaleAfter,
	))
	taskDelivery = combinedDependencyHealth(taskDelivery, source.privateTaskAvailabilityBacklogHealth(ctx, now))
	realtime := workerBoundDependencyHealth(
		eventDeliveryHealthStatus(source.realtime, now),
		source.workers,
		"realtime-session-delivery",
	)
	realtime = combinedDependencyHealth(realtime, source.sharedWorkerHealth(
		ctx,
		"realtime-session-delivery",
		now,
		runtimeEventDeliveryLastSuccessStaleAfter,
	))
	return tournamentpreflight.RuntimeHealth{
		TaskDelivery: preflightComponentHealth("task_delivery", taskDelivery),
		Realtime:     preflightComponentHealth("realtime", realtime),
		Clock:        source.sampleClock(ctx),
		ClockSampled: true,
		Dependencies: []tournamentpreflight.DependencyHealth{
			source.sampleDependency(ctx, tournamentpreflight.DependencyRedis, source.redis),
			source.sampleDependency(ctx, tournamentpreflight.DependencyObjectStorage, source.objectStorage),
		},
	}
}

func (source preflightRuntimeHealthSource) privateTaskAvailabilityBacklogHealth(
	ctx context.Context,
	now time.Time,
) observability.TournamentDependencyStatus {
	if source.receiptBacklog == nil {
		return failedRuntimeDependency()
	}
	probeCtx, cancel := preflightProbeContext(ctx)
	defer cancel()
	backlog, err := source.receiptBacklog.TaskDeliveryBacklog(probeCtx)
	if err != nil {
		return failedRuntimeDependency()
	}
	return privateTaskAvailabilityBacklogHealthStatus(backlog, now)
}

func (source preflightRuntimeHealthSource) sharedWorkerHealth(
	ctx context.Context,
	worker string,
	now time.Time,
	staleAfter time.Duration,
) observability.TournamentDependencyStatus {
	if source.heartbeats == nil || !domain.IsValidServerTime(now) || staleAfter <= 0 {
		return failedRuntimeDependency()
	}
	probeCtx, cancel := preflightProbeContext(ctx)
	defer cancel()
	status, err := source.heartbeats.RuntimeWorkerHeartbeatStatus(probeCtx, worker, now.Add(-staleAfter))
	if err != nil || status.Worker != worker {
		return failedRuntimeDependency()
	}
	if status.Healthy() {
		return healthyTournamentDependency()
	}
	return observability.TournamentDependencyStatus{
		Health:    observability.TournamentHealthStateDegraded,
		Readiness: observability.TournamentReadinessStateStale,
	}
}

func (source preflightRuntimeHealthSource) localNow() time.Time {
	if source.clock == nil {
		return time.Time{}
	}
	return source.clock.Now().Round(0).UTC()
}

func (source preflightRuntimeHealthSource) sampleClock(ctx context.Context) tournamentpreflight.ClockHealth {
	before := source.localNow()
	if before.IsZero() || source.databaseClock == nil {
		return tournamentpreflight.ClockHealth{ObservedAt: before, MaxSkew: runtimeClockDegradedAfter}
	}
	probeCtx, cancel := preflightProbeContext(ctx)
	databaseNow, err := source.databaseClock.DatabaseTime(probeCtx)
	cancel()
	after := source.localNow()
	if err != nil || databaseNow.IsZero() || after.Before(before) {
		return tournamentpreflight.ClockHealth{ObservedAt: before, MaxSkew: runtimeClockDegradedAfter}
	}
	return tournamentpreflight.ClockHealth{
		ObservedAt:  midpointRuntimeTime(before, after),
		ReferenceAt: databaseNow.Round(0).UTC(),
		MaxSkew:     runtimeClockDegradedAfter,
	}
}

func midpointRuntimeTime(before time.Time, after time.Time) time.Time {
	return before.Add(after.Sub(before) / 2).Round(0).UTC()
}

func (source preflightRuntimeHealthSource) sampleDependency(
	ctx context.Context,
	name tournamentpreflight.Dependency,
	probe preflightDependencyProbe,
) tournamentpreflight.DependencyHealth {
	probeCtx, cancel := preflightProbeContext(ctx)
	defer cancel()
	healthy := probe != nil && probe.Probe(probeCtx) == nil
	state := "failed"
	if healthy {
		state = "ready"
	}
	return tournamentpreflight.DependencyHealth{
		Name: name, Healthy: healthy, Revision: string(name) + ":" + state,
	}
}

func preflightProbeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, runtimePreflightProbeTimeout)
}

func preflightComponentHealth(
	prefix string,
	status observability.TournamentDependencyStatus,
) tournamentpreflight.ComponentHealth {
	return tournamentpreflight.ComponentHealth{
		Healthy: status.Health == observability.TournamentHealthStateHealthy &&
			status.Readiness == observability.TournamentReadinessStateReady,
		Revision: fmt.Sprintf("%s:%s:%s", prefix, status.Health, status.Readiness),
	}
}
