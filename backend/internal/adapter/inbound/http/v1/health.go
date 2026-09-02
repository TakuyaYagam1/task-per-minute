package v1

import (
	"context"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

// (GET /health).
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	db := checkDB(r.Context(), s.health.DB)
	redis := checkRedis(r.Context(), s.health.Redis)
	seaweedfs := checkSeaweedFS(r.Context(), s.health.SeaweedFS)
	schemaVersion, schemaVersionOK := readSchemaVersion(r.Context(), s.health.SchemaVersion)
	arena := readArenaHealth(r.Context(), s.health.Arena)

	status := api.HealthResponseStatusOk
	httpStatus := http.StatusOK
	if db != api.HealthResponseDbOk ||
		redis != api.HealthResponseRedisOk ||
		seaweedfs != api.Ok ||
		!schemaVersionOK ||
		schemaVersion <= 0 ||
		!arena.Healthy() ||
		!arena.Ready() {
		status = api.HealthResponseStatusDegraded
		httpStatus = http.StatusServiceUnavailable
	}

	response.WriteJSON(w, httpStatus, api.HealthResponse{
		Status:            status,
		Db:                db,
		Redis:             redis,
		Seaweedfs:         seaweedfs,
		SchemaVersion:     schemaVersion,
		ArenaAuthority:    arenaDependencyStatus(arena.Authority),
		ArenaSubmission:   arenaDependencyStatus(arena.Submission),
		ArenaTaskDelivery: arenaDependencyStatus(arena.TaskDelivery),
		ArenaOutbox:       arenaDependencyStatus(arena.Outbox),
		ArenaRealtime:     arenaDependencyStatus(arena.Realtime),
		ArenaClock:        arenaDependencyStatus(arena.Clock),
		ArenaRecovery:     arenaDependencyStatus(arena.Recovery),
	})
}

func readArenaHealth(
	ctx context.Context,
	source observability.ArenaHealthSource,
) observability.ArenaHealthSnapshot {
	if source == nil {
		return observability.FailedArenaHealthSnapshot()
	}
	snapshot := source.ArenaHealth(ctx)
	if snapshot.Validate() != nil {
		return observability.FailedArenaHealthSnapshot()
	}
	return snapshot
}

func arenaDependencyStatus(status observability.ArenaDependencyStatus) api.ArenaDependencyStatus {
	health := api.ArenaDependencyStatusHealthFailed
	switch status.Health {
	case observability.ArenaHealthStateHealthy:
		health = api.ArenaDependencyStatusHealthHealthy
	case observability.ArenaHealthStateDegraded:
		health = api.ArenaDependencyStatusHealthDegraded
	case observability.ArenaHealthStateFailed:
		health = api.ArenaDependencyStatusHealthFailed
	}

	readiness := api.ArenaDependencyStatusReadinessNotReady
	switch status.Readiness {
	case observability.ArenaReadinessStateReady:
		readiness = api.ArenaDependencyStatusReadinessReady
	case observability.ArenaReadinessStateStale:
		readiness = api.ArenaDependencyStatusReadinessStale
	case observability.ArenaReadinessStateNotReady:
		readiness = api.ArenaDependencyStatusReadinessNotReady
	}
	return api.ArenaDependencyStatus{Health: health, Readiness: readiness}
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
		return api.Ok
	}
	return api.Error
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
