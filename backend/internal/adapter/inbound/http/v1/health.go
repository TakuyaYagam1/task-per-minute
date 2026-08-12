package v1

import (
	"context"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
)

// (GET /health).
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	db := checkDB(r.Context(), s.health.DB)
	redis := checkRedis(r.Context(), s.health.Redis)
	seaweedfs := checkSeaweedFS(r.Context(), s.health.SeaweedFS)
	schemaVersion, schemaVersionOK := readSchemaVersion(r.Context(), s.health.SchemaVersion)

	status := api.HealthResponseStatusOk
	httpStatus := http.StatusOK
	if db != api.HealthResponseDbOk ||
		redis != api.HealthResponseRedisOk ||
		seaweedfs != api.HealthResponseSeaweedfsOk ||
		!schemaVersionOK ||
		schemaVersion <= 0 {
		status = api.HealthResponseStatusDegraded
		httpStatus = http.StatusServiceUnavailable
	}

	response.WriteJSON(w, httpStatus, api.HealthResponse{
		Status:        status,
		Db:            db,
		Redis:         redis,
		Seaweedfs:     seaweedfs,
		SchemaVersion: schemaVersion,
	})
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
