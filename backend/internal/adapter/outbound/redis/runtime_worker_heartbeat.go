package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	runtimeWorkerHeartbeatKeyPrefix    = "runtime-worker-heartbeat:"
	runtimeWorkerHeartbeatCallTimeout  = 750 * time.Millisecond
	runtimeWorkerHeartbeatRetention    = time.Minute
	maximumRuntimeWorkerHeartbeatCount = 256
)

var (
	ErrNilRuntimeWorkerHeartbeatClient = errors.New("redis: nil runtime worker heartbeat client")
	ErrRuntimeWorkerHeartbeatCapacity  = errors.New("redis: runtime worker heartbeat capacity exceeded")
)

var recordRuntimeWorkerHeartbeat = goredis.NewScript(`
local key = KEYS[1]
local instance = ARGV[1]
local observed_at = tonumber(ARGV[2])
local stale_before = tonumber(ARGV[3])
local maximum = tonumber(ARGV[4])
local retention = tonumber(ARGV[5])

redis.call('ZREMRANGEBYSCORE', key, '-inf', stale_before)
if redis.call('ZSCORE', key, instance) == false and redis.call('ZCARD', key) >= maximum then
  return 0
end
redis.call('ZADD', key, observed_at, instance)
redis.call('PEXPIRE', key, retention)
return 1
`)

// RuntimeWorkerHeartbeats keeps a bounded, shared replica liveness aggregate.
// Stale entries are pruned on every report and the Redis key expires when no
// replica continues reporting.
type RuntimeWorkerHeartbeats struct {
	client *goredis.Client
}

var (
	_ observability.RuntimeWorkerHeartbeatReporter = (*RuntimeWorkerHeartbeats)(nil)
	_ observability.RuntimeWorkerHeartbeatReader   = (*RuntimeWorkerHeartbeats)(nil)
)

func NewRuntimeWorkerHeartbeats(client *goredis.Client) *RuntimeWorkerHeartbeats {
	return &RuntimeWorkerHeartbeats{client: client}
}

func (store *RuntimeWorkerHeartbeats) ReportRuntimeWorkerHeartbeat(
	ctx context.Context,
	heartbeat observability.RuntimeWorkerHeartbeat,
) error {
	if ctx == nil || store == nil || store.client == nil {
		return ErrNilRuntimeWorkerHeartbeatClient
	}
	if err := heartbeat.Validate(); err != nil {
		return fmt.Errorf("redis runtime worker heartbeat: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, runtimeWorkerHeartbeatCallTimeout)
	defer cancel()
	observedAt := heartbeat.ObservedAt.Round(0).UTC()
	result, err := recordRuntimeWorkerHeartbeat.Run(
		callCtx,
		store.client,
		[]string{runtimeWorkerHeartbeatKey(heartbeat.Worker)},
		heartbeat.InstanceID.String(),
		observedAt.UnixMilli(),
		observedAt.Add(-runtimeWorkerHeartbeatRetention).UnixMilli(),
		maximumRuntimeWorkerHeartbeatCount,
		runtimeWorkerHeartbeatRetention.Milliseconds(),
	).Int64()
	if err != nil {
		return fmt.Errorf("redis runtime worker heartbeat report: %w", err)
	}
	if result != 1 {
		return ErrRuntimeWorkerHeartbeatCapacity
	}
	return nil
}

func (store *RuntimeWorkerHeartbeats) RuntimeWorkerHeartbeatStatus(
	ctx context.Context,
	worker string,
	freshAfter time.Time,
) (observability.RuntimeWorkerHeartbeatStatus, error) {
	if ctx == nil || store == nil || store.client == nil {
		return observability.RuntimeWorkerHeartbeatStatus{}, ErrNilRuntimeWorkerHeartbeatClient
	}
	if freshAfter.IsZero() || freshAfter.Location() != time.UTC {
		return observability.RuntimeWorkerHeartbeatStatus{}, errors.New("redis: invalid runtime worker heartbeat freshness")
	}
	status := observability.RuntimeWorkerHeartbeatStatus{Worker: worker}
	if status.Revision() == "" {
		return observability.RuntimeWorkerHeartbeatStatus{}, errors.New("redis: invalid runtime worker name")
	}
	callCtx, cancel := context.WithTimeout(ctx, runtimeWorkerHeartbeatCallTimeout)
	defer cancel()
	count, err := store.client.ZCount(
		callCtx,
		runtimeWorkerHeartbeatKey(worker),
		strconv.FormatInt(freshAfter.UnixMilli(), 10),
		"+inf",
	).Result()
	if err != nil {
		return observability.RuntimeWorkerHeartbeatStatus{}, fmt.Errorf("redis runtime worker heartbeat read: %w", err)
	}
	if count < 0 || count > maximumRuntimeWorkerHeartbeatCount {
		return observability.RuntimeWorkerHeartbeatStatus{}, errors.New("redis: invalid runtime worker heartbeat count")
	}
	status.FreshInstances = int(count)
	return status, nil
}

func runtimeWorkerHeartbeatKey(worker string) string {
	return runtimeWorkerHeartbeatKeyPrefix + worker
}
