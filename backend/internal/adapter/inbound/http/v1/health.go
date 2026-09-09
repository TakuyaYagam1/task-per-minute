package v1

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	healthCacheTTL       = time.Second
	healthRefreshTimeout = 2 * time.Second
)

type healthResult struct {
	body   api.HealthResponse
	status int
}

type healthRefresh struct {
	done   chan struct{}
	result healthResult
}

type healthCache struct {
	mu        sync.Mutex
	result    healthResult
	expiresAt time.Time
	refresh   *healthRefresh
}

// (GET /health).
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	result := s.cachedHealth(r.Context())
	response.WriteJSON(w, result.status, result.body)
}

func (s *Server) cachedHealth(ctx context.Context) healthResult {
	cache := &s.healthCache
	cache.mu.Lock()
	if ctx.Err() != nil {
		cache.mu.Unlock()
		return probeHealth(ctx, HealthChecks{})
	}
	if s.now().Before(cache.expiresAt) {
		result := cache.result
		cache.mu.Unlock()
		return result
	}
	if refresh := cache.refresh; refresh != nil {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return probeHealth(ctx, HealthChecks{})
		case <-refresh.done:
			if ctx.Err() != nil {
				return probeHealth(ctx, HealthChecks{})
			}
			return refresh.result
		}
	}
	refresh := &healthRefresh{done: make(chan struct{}), result: probeHealth(ctx, HealthChecks{})}
	cache.refresh = refresh
	cache.mu.Unlock()
	return s.refreshHealth(ctx, refresh)
}

func (s *Server) refreshHealth(ctx context.Context, refresh *healthRefresh) healthResult {
	complete := false
	defer func() {
		cache := &s.healthCache
		cache.mu.Lock()
		defer cache.mu.Unlock()
		// Release waiters even when a dependency panics. Only complete sweeps are cached.
		cache.refresh = nil
		defer close(refresh.done)
		if complete {
			cache.result = refresh.result
			cache.expiresAt = s.now().Add(healthCacheTTL)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, healthRefreshTimeout)
	defer cancel()
	refresh.result = probeHealth(ctx, s.health)
	complete = true
	return refresh.result
}

func probeHealth(ctx context.Context, checks HealthChecks) healthResult {
	db := checkDB(ctx, checks.DB)
	redis := checkRedis(ctx, checks.Redis)
	seaweedfs := checkSeaweedFS(ctx, checks.SeaweedFS)
	schemaVersion, schemaVersionOK := readSchemaVersion(ctx, checks.SchemaVersion)
	tournament := readTournamentHealth(ctx, checks.Tournament)

	status := api.HealthResponseStatusOk
	httpStatus := http.StatusOK
	if db != api.HealthResponseDbOk ||
		redis != api.HealthResponseRedisOk ||
		seaweedfs != api.HealthResponseSeaweedfsOk ||
		!schemaVersionOK ||
		schemaVersion <= 0 ||
		!tournament.Healthy() ||
		!tournament.Ready() {
		status = api.HealthResponseStatusDegraded
		httpStatus = http.StatusServiceUnavailable
	}

	return healthResult{status: httpStatus, body: api.HealthResponse{
		Status:                 status,
		Db:                     db,
		Redis:                  redis,
		Seaweedfs:              seaweedfs,
		SchemaVersion:          schemaVersion,
		TournamentAuthority:    tournamentDependencyStatus(tournament.Authority),
		TournamentSubmission:   tournamentDependencyStatus(tournament.Submission),
		TournamentTaskDelivery: tournamentDependencyStatus(tournament.TaskDelivery),
		TournamentOutbox:       tournamentDependencyStatus(tournament.Outbox),
		TournamentRealtime:     tournamentDependencyStatus(tournament.Realtime),
		TournamentProjection:   tournamentDependencyStatus(tournament.Projection),
		TournamentClock:        tournamentDependencyStatus(tournament.Clock),
		TournamentRecovery:     tournamentDependencyStatus(tournament.Recovery),
	}}
}

func readTournamentHealth(
	ctx context.Context,
	source observability.TournamentHealthSource,
) observability.TournamentHealthSnapshot {
	if source == nil {
		return observability.FailedTournamentHealthSnapshot()
	}
	snapshot := source.TournamentHealth(ctx)
	if snapshot.Validate() != nil {
		return observability.FailedTournamentHealthSnapshot()
	}
	return snapshot
}

func tournamentDependencyStatus(status observability.TournamentDependencyStatus) api.DependencyStatus {
	health := api.DependencyStatusHealthFailed
	switch status.Health {
	case observability.TournamentHealthStateHealthy:
		health = api.DependencyStatusHealthHealthy
	case observability.TournamentHealthStateDegraded:
		health = api.DependencyStatusHealthDegraded
	case observability.TournamentHealthStateFailed:
		health = api.DependencyStatusHealthFailed
	}

	readiness := api.DependencyStatusReadinessNotReady
	switch status.Readiness {
	case observability.TournamentReadinessStateReady:
		readiness = api.DependencyStatusReadinessReady
	case observability.TournamentReadinessStateStale:
		readiness = api.DependencyStatusReadinessStale
	case observability.TournamentReadinessStateNotReady:
		readiness = api.DependencyStatusReadinessNotReady
	}
	return api.DependencyStatus{Health: health, Readiness: readiness}
}

func checkDB(ctx context.Context, checker HealthChecker) api.HealthResponseDb {
	if check(ctx, checker) {
		return api.HealthResponseDbOk
	}
	return api.HealthResponseDbError
}

func checkRedis(ctx context.Context, checker HealthChecker) api.HealthResponseRedis {
	if check(ctx, checker) {
		return api.HealthResponseRedisOk
	}
	return api.HealthResponseRedisError
}

func checkSeaweedFS(ctx context.Context, checker HealthChecker) api.HealthResponseSeaweedfs {
	if check(ctx, checker) {
		return api.HealthResponseSeaweedfsOk
	}
	return api.HealthResponseSeaweedfsError
}

func check(ctx context.Context, checker HealthChecker) bool {
	return checker != nil && checker.Check(ctx) == nil
}

func readSchemaVersion(ctx context.Context, reader SchemaVersionReader) (int64, bool) {
	if reader == nil {
		return 0, false
	}
	version, err := reader.SchemaVersion(ctx)
	if err != nil {
		return 0, false
	}
	return version, true
}
