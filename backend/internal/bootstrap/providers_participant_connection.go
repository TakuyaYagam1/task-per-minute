package bootstrap

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	reconnectrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	participantconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

const participantReconnectDuration = 30 * time.Second

func provideParticipantConnectionRepository(
	transactions *postgres.TxManager,
	authority *authorityusecase.Controller,
) *participantrepo.ParticipantConnectionPostgres {
	return participantrepo.NewParticipantConnectionPostgres(transactions, authority)
}

func provideTournamentPausedPresenceRepository(
	transactions *postgres.TxManager,
	execution *executionrepo.Repository,
) *reconnectrepo.TournamentPausedPresencePostgres {
	return reconnectrepo.NewTournamentPausedPresencePostgres(transactions, execution)
}

func provideParticipantConnectionCoordinator(
	transactions *postgres.TxManager,
	repository *participantrepo.ParticipantConnectionPostgres,
	pausedPresenceRepository *reconnectrepo.TournamentPausedPresencePostgres,
	reconnectRepository gameusecase.ReconnectRepository,
	readinessUseCase *readiness.ReadinessUseCase,
	terminalAdvancer participantconnection.TerminalAdvancer,
	clock clockFunc,
	observer *telemetryadapter.ReconnectObserver,
) (*participantconnection.Coordinator, error) {
	return participantconnection.NewCoordinator(participantconnection.Dependencies{
		Transactions:     transactions,
		Authority:        repository,
		Repository:       repository,
		Recovery:         repository,
		Readiness:        readinessUseCase,
		PausedPresence:   gamepause.NewPausedPresenceUseCase(transactions, pausedPresenceRepository, clock),
		Disconnect:       gameusecase.NewDisconnectUseCase(reconnectRepository, clock),
		Reconnect:        gameusecase.ReconnectNewUseCase(reconnectRepository, clock, observer),
		TerminalAdvancer: terminalAdvancer,
		Clock:            clock,
		Config: participantconnection.Config{
			ReconnectDuration: participantReconnectDuration,
		},
	})
}

func provideParticipantConnectionReaper(
	coordinator *participantconnection.Coordinator,
	repository *participantrepo.ParticipantConnectionPostgres,
	authority *authorityusecase.Controller,
) (*participantconnection.Reaper, error) {
	return participantconnection.NewReaper(coordinator, repository, authority)
}
