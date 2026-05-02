package v1

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

var _ openapi.ServerInterface = (*Server)(nil)

// HealthChecks groups one HealthChecker per backing dependency reported by
// /health. The handler returns 200 when every checker succeeds, 503 otherwise.
type HealthChecks struct {
	DB            usecase.HealthChecker
	Redis         usecase.HealthChecker
	SeaweedFS     usecase.HealthChecker
	SchemaVersion usecase.SchemaVersionReader
}

// Dependencies bundles every usecase port the v1 controller needs. Wiring
// (internal/wire) constructs it from concrete usecase implementations.
type Dependencies struct {
	Players      usecase.Player
	AdminAuth    usecase.AdminAuth
	Tasks        usecase.AdminTask
	Upload       usecase.Upload
	Leaderboard  usecase.Leaderboard
	Duels        usecase.Duel
	Health       HealthChecks
	LoginLimiter *middleware.LoginRateLimiter
	JoinLimiter  *middleware.JoinRateLimiter
	Now          func() time.Time
}

type Server struct {
	players      usecase.Player
	adminAuth    usecase.AdminAuth
	tasks        usecase.AdminTask
	upload       usecase.Upload
	leaderboard  usecase.Leaderboard
	duels        usecase.Duel
	health       HealthChecks
	loginLimiter *middleware.LoginRateLimiter
	joinLimiter  *middleware.JoinRateLimiter
	now          func() time.Time
}

func New(deps Dependencies) *Server {
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Server{
		players:      deps.Players,
		adminAuth:    deps.AdminAuth,
		tasks:        deps.Tasks,
		upload:       deps.Upload,
		leaderboard:  deps.Leaderboard,
		duels:        deps.Duels,
		health:       deps.Health,
		loginLimiter: deps.LoginLimiter,
		joinLimiter:  deps.JoinLimiter,
		now:          now,
	}
}
