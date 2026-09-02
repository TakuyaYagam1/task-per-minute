package v1

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

var _ api.ServerInterface = (*Server)(nil)

// HealthChecks groups one HealthChecker per backing dependency reported by
// /health. The handler returns 200 when every checker succeeds, 503 otherwise.
type HealthChecks struct {
	DB            HealthChecker
	Redis         HealthChecker
	SeaweedFS     HealthChecker
	SchemaVersion SchemaVersionReader
	Arena         observability.ArenaHealthSource
}

// Dependencies bundles every usecase port the v1 controller needs. Wiring
// The bootstrap package constructs it from concrete use case implementations.
type Dependencies struct {
	Players                 PlayerService
	AdminAuth               AdminAuthService
	Tasks                   AdminTaskService
	AdminPlayers            AdminPlayerService
	AdminPlayerEvents       AdminPlayerEventSubscriber
	Upload                  UploadService
	Leaderboard             LeaderboardService
	Duels                   DuelService
	ArenaAdmin              ArenaAdminService
	ArenaOperator           ArenaOperatorController
	ArenaParticipant        ArenaParticipantController
	ArenaParticipantService ArenaParticipantService
	ArenaSubmissionLimiter  ArenaSubmissionRateLimiter
	ArenaPublic             ArenaPublicController
	Health                  HealthChecks
	ArenaMetrics            prometheus.Gatherer
	LoginLimiter            *middleware.LoginRateLimiter
	RefreshLimiter          *middleware.LoginRateLimiter
	JoinLimiter             *middleware.JoinRateLimiter
	LeaderboardLimiter      *middleware.LoginRateLimiter
	Now                     func() time.Time
	Log                     logkit.Logger
}

type Server struct {
	ArenaOperatorController
	ArenaParticipantController
	ArenaPublicController

	players            PlayerService
	adminAuth          AdminAuthService
	tasks              AdminTaskService
	adminPlayers       AdminPlayerService
	adminPlayerEvents  AdminPlayerEventSubscriber
	upload             UploadService
	leaderboard        LeaderboardService
	duels              DuelService
	health             HealthChecks
	arenaMetrics       prometheus.Gatherer
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
	var arenaAdmin *arenaAdminController
	if deps.ArenaAdmin != nil {
		arenaAdmin = newArenaAdminController(deps.ArenaAdmin)
	}
	arenaOperator := deps.ArenaOperator
	if arenaOperator == nil {
		if arenaAdmin != nil {
			arenaOperator = arenaAdmin
		} else {
			arenaOperator = api.Unimplemented{}
		}
	}
	arenaParticipant := deps.ArenaParticipant
	if arenaParticipant == nil {
		if deps.ArenaParticipantService != nil {
			arenaParticipant = newArenaParticipantController(
				deps.ArenaParticipantService,
				deps.ArenaSubmissionLimiter,
			)
		} else {
			arenaParticipant = api.Unimplemented{}
		}
	}
	arenaPublic := deps.ArenaPublic
	if arenaPublic == nil {
		if arenaAdmin != nil {
			arenaPublic = arenaAdmin
		} else {
			arenaPublic = api.Unimplemented{}
		}
	}
	return &Server{
		ArenaOperatorController:    arenaOperator,
		ArenaParticipantController: arenaParticipant,
		ArenaPublicController:      arenaPublic,
		players:                    deps.Players,
		adminAuth:                  deps.AdminAuth,
		tasks:                      deps.Tasks,
		adminPlayers:               deps.AdminPlayers,
		adminPlayerEvents:          deps.AdminPlayerEvents,
		upload:                     deps.Upload,
		leaderboard:                deps.Leaderboard,
		duels:                      deps.Duels,
		health:                     deps.Health,
		arenaMetrics:               deps.ArenaMetrics,
		loginLimiter:               deps.LoginLimiter,
		refreshLimiter:             deps.RefreshLimiter,
		joinLimiter:                deps.JoinLimiter,
		leaderboardLimiter:         deps.LeaderboardLimiter,
		now:                        now,
		log:                        deps.Log,
	}
}
