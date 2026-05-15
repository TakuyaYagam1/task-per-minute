package v1

import (
	"time"

	logkit "github.com/wahrwelt-kit/go-logkit"

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
	Players            usecase.Player
	AdminAuth          usecase.AdminAuth
	Tasks              usecase.AdminTask
	AdminPlayers       usecase.AdminPlayer
	AdminPlayerEvents  usecase.AdminPlayerEvents
	Upload             usecase.Upload
	Leaderboard        usecase.Leaderboard
	Duels              usecase.Duel
	Health             HealthChecks
	LoginLimiter       *middleware.LoginRateLimiter
	RefreshLimiter     *middleware.LoginRateLimiter
	JoinLimiter        *middleware.JoinRateLimiter
	LeaderboardLimiter *middleware.LoginRateLimiter
	Now                func() time.Time
	Log                logkit.Logger
}

type Server struct {
	players            usecase.Player
	adminAuth          usecase.AdminAuth
	tasks              usecase.AdminTask
	adminPlayers       usecase.AdminPlayer
	adminPlayerEvents  usecase.AdminPlayerEvents
	upload             usecase.Upload
	leaderboard        usecase.Leaderboard
	duels              usecase.Duel
	health             HealthChecks
	loginLimiter       *middleware.LoginRateLimiter
	refreshLimiter     *middleware.LoginRateLimiter
	joinLimiter        *middleware.JoinRateLimiter
	leaderboardLimiter *middleware.LoginRateLimiter
	now                func() time.Time
	log                logkit.Logger
}

func New(deps Dependencies) *Server {
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Server{
		players:            deps.Players,
		adminAuth:          deps.AdminAuth,
		tasks:              deps.Tasks,
		adminPlayers:       deps.AdminPlayers,
		adminPlayerEvents:  deps.AdminPlayerEvents,
		upload:             deps.Upload,
		leaderboard:        deps.Leaderboard,
		duels:              deps.Duels,
		health:             deps.Health,
		loginLimiter:       deps.LoginLimiter,
		refreshLimiter:     deps.RefreshLimiter,
		joinLimiter:        deps.JoinLimiter,
		leaderboardLimiter: deps.LeaderboardLimiter,
		now:                now,
		log:                deps.Log,
	}
}
