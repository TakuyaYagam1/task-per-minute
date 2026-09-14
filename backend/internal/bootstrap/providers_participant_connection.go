package bootstrap

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	telemetryadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/telemetry"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	participantconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

const participantReconnectDuration = 30 * time.Second

func provideParticipantConnectionRepository(
	transactions *postgres.TxManager,
	authority *authorityusecase.Controller,
) *postgres.ParticipantConnectionPostgres {
	return postgres.NewParticipantConnectionPostgres(transactions, authority)
}

func provideParticipantConnectionCoordinator(
	transactions *postgres.TxManager,
	repository *postgres.ParticipantConnectionPostgres,
	pausedPresenceRepository *postgres.TournamentPausedPresencePostgres,
	reconnectRepository *postgres.TournamentAdminExecutionPostgres,
	readinessUseCase *readiness.ReadinessUseCase,
	terminalAdvancer participantconnection.TerminalAdvancer,
	clock clockFunc,
	observer *telemetryadapter.ReconnectObserver,
) (*participantconnection.Coordinator, error) {
	return participantconnection.NewCoordinator(participantconnection.Dependencies{
		Transactions:     transactions,
		Authority:        repository,
		Repository:       repository,
		Readiness:        readinessUseCase,
		PausedPresence:   gameusecase.NewPausedPresenceUseCase(transactions, pausedPresenceRepository, clock),
		Disconnect:       gameusecase.NewDisconnectUseCase(reconnectRepository, clock),
		Reconnect:        gameusecase.ReconnectNewUseCase(reconnectRepository, clock, observer),
		TerminalAdvancer: terminalAdvancer,
		Clock:            clock,
		Config: participantconnection.Config{
			ReconnectDuration: participantReconnectDuration,
		},
	})
}
