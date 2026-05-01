package v1

import (
	"context"

	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	db := checkDB(r.Context(), s.health.DB)
	redis := checkRedis(r.Context(), s.health.Redis)
	seaweedfs := checkSeaweedFS(r.Context(), s.health.SeaweedFS)
	schemaVersion, schemaVersionOK := readSchemaVersion(r.Context(), s.health.SchemaVersion)

	status := openapi.HealthResponseStatusOk
	httpStatus := http.StatusOK
	if db != openapi.HealthResponseDbOk ||
		redis != openapi.HealthResponseRedisOk ||
		seaweedfs != openapi.HealthResponseSeaweedfsOk ||
		!schemaVersionOK {
		status = openapi.HealthResponseStatusDegraded
		httpStatus = http.StatusServiceUnavailable
	}

	response.WriteJSON(w, httpStatus, openapi.HealthResponse{
		Status:        status,
		Db:            db,
		Redis:         redis,
		Seaweedfs:     seaweedfs,
		SchemaVersion: schemaVersion,
	})
}

func checkDB(ctx context.Context, checker usecase.HealthChecker) openapi.HealthResponseDb {
	if check(ctx, checker) {
		return openapi.HealthResponseDbOk
	}
	return openapi.HealthResponseDbError
}

func checkRedis(ctx context.Context, checker usecase.HealthChecker) openapi.HealthResponseRedis {
	if check(ctx, checker) {
		return openapi.HealthResponseRedisOk
	}
	return openapi.HealthResponseRedisError
}

func checkSeaweedFS(ctx context.Context, checker usecase.HealthChecker) openapi.HealthResponseSeaweedfs {
	if check(ctx, checker) {
		return openapi.HealthResponseSeaweedfsOk
	}
	return openapi.HealthResponseSeaweedfsError
}

func check(ctx context.Context, checker usecase.HealthChecker) bool {
	return checker != nil && checker.Check(ctx) == nil
}

func readSchemaVersion(ctx context.Context, reader usecase.SchemaVersionReader) (int64, bool) {
	if reader == nil {
		return 0, false
	}
	version, err := reader.SchemaVersion(ctx)
	if err != nil {
		return 0, false
	}
	return version, true
}
