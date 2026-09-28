package v1

import (
	"time"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

var _ api.ServerInterface = (*Server)(nil)

// HealthChecks groups one HealthChecker per backing dependency reported by
// /health. The handler returns 200 when every checker succeeds, 503 otherwise.
type HealthChecks struct {
	DB            HealthChecker
	Redis         HealthChecker
	SeaweedFS     HealthChecker
	SchemaVersion SchemaVersionReader
	Tournament    observability.TournamentHealthSource
}

// Dependencies bundles every usecase port the v1 controller needs.
// The bootstrap package constructs it from concrete use case implementations.
type Dependencies struct {
	Players                              PlayerService
	AdminAuth                            AdminAuthService
	Tasks                                AdminTaskService
	AdminPlayers                         AdminPlayerService
	AdminPlayerEvents                    AdminPlayerEventSubscriber
	AdminEvents                          AdminEventSubscriber
	Upload                               UploadService
	Leaderboard                          LeaderboardService
	Tournaments                          usecase.TournamentUseCase
	PublicTournamentCatalog              usecase.PublicTournamentCatalogUseCase
	TournamentAdmission                  usecase.TournamentAdmissionUseCase
	TournamentAdmin                      usecase.TournamentAdminUseCase
	TournamentConfiguration              usecase.TournamentConfigurationUseCase
	TournamentParticipant                usecase.TournamentParticipantUseCase
	ParticipantArchive                   usecase.ParticipantArchiveUseCase
	TournamentSnapshots                  usecase.TournamentSnapshotUseCase
	Golden                               usecase.GoldenUseCase
	Health                               HealthChecks
	LoginLimiter                         middleware.RateLimiter
	RefreshLimiter                       middleware.RateLimiter
	JoinLimiter                          middleware.RateLimiter
	LeaderboardLimiter                   middleware.RateLimiter
	PublicTournamentReadLimiter          middleware.RateLimiter
	OperatorTournamentReadLimiter        middleware.RateLimiter
	OperatorTournamentMutationLimiter    middleware.RateLimiter
	ParticipantTournamentReadLimiter     middleware.RateLimiter
	ParticipantTournamentMutationLimiter middleware.RateLimiter
	Now                                  func() time.Time
	Log                                  logkit.Logger
}

type Server struct {
	*tournamentController

	players                              PlayerService
	adminAuth                            AdminAuthService
	tasks                                AdminTaskService
	adminPlayers                         AdminPlayerService
	adminPlayerEvents                    AdminPlayerEventSubscriber
	adminEvents                          AdminEventSubscriber
	upload                               UploadService
	leaderboard                          LeaderboardService
	publicTournamentCatalog              usecase.PublicTournamentCatalogUseCase
	tournamentAdmission                  usecase.TournamentAdmissionUseCase
	tournamentParticipant                usecase.TournamentParticipantUseCase
	participantArchive                   usecase.ParticipantArchiveUseCase
	tournamentSnapshots                  usecase.TournamentSnapshotUseCase
	golden                               usecase.GoldenUseCase
	health                               HealthChecks
	healthCache                          healthCache
	loginLimiter                         middleware.RateLimiter
	refreshLimiter                       middleware.RateLimiter
	joinLimiter                          middleware.RateLimiter
	leaderboardLimiter                   middleware.RateLimiter
	publicTournamentReadLimiter          middleware.RateLimiter
	operatorTournamentReadLimiter        middleware.RateLimiter
	operatorTournamentMutationLimiter    middleware.RateLimiter
	participantTournamentReadLimiter     middleware.RateLimiter
	participantTournamentMutationLimiter middleware.RateLimiter
	now                                  func() time.Time
	log                                  logkit.Logger
}

func New(deps Dependencies) *Server {
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Server{
		tournamentController:                 newTournamentController(deps.Tournaments, deps.TournamentAdmin, deps.TournamentConfiguration),
		publicTournamentCatalog:              deps.PublicTournamentCatalog,
		tournamentAdmission:                  deps.TournamentAdmission,
		players:                              deps.Players,
		adminAuth:                            deps.AdminAuth,
		tasks:                                deps.Tasks,
		adminPlayers:                         deps.AdminPlayers,
		adminPlayerEvents:                    deps.AdminPlayerEvents,
		adminEvents:                          deps.AdminEvents,
		upload:                               deps.Upload,
		leaderboard:                          deps.Leaderboard,
		tournamentParticipant:                deps.TournamentParticipant,
		participantArchive:                   deps.ParticipantArchive,
		tournamentSnapshots:                  deps.TournamentSnapshots,
		golden:                               deps.Golden,
		health:                               deps.Health,
		loginLimiter:                         deps.LoginLimiter,
		refreshLimiter:                       deps.RefreshLimiter,
		joinLimiter:                          deps.JoinLimiter,
		leaderboardLimiter:                   deps.LeaderboardLimiter,
		publicTournamentReadLimiter:          deps.PublicTournamentReadLimiter,
		operatorTournamentReadLimiter:        deps.OperatorTournamentReadLimiter,
		operatorTournamentMutationLimiter:    deps.OperatorTournamentMutationLimiter,
		participantTournamentReadLimiter:     deps.ParticipantTournamentReadLimiter,
		participantTournamentMutationLimiter: deps.ParticipantTournamentMutationLimiter,
		now:                                  now,
		log:                                  deps.Log,
	}
}
