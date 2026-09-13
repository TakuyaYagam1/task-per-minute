package connection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

var (
	ErrInvalidConfiguration = errors.New("invalid participant connection configuration")
	ErrInvalidAuthority     = errors.New("invalid participant connection authority")
	ErrInvalidLease         = errors.New("invalid participant connection lease")
	ErrInvalidAction        = errors.New("invalid participant connection action")
	ErrWorkflowUnavailable  = errors.New("participant connection workflow is unavailable")
)

type operation string

const (
	operationConnect    operation = "connect"
	operationDisconnect operation = "disconnect"
)

type Dependencies struct {
	Transactions   game.TransactionManager
	Authority      AuthorityProvider
	Repository     Repository
	Readiness      ReadinessWorkflow
	PausedPresence PausedPresenceWorkflow
	Disconnect     DisconnectWorkflow
	Reconnect      ReconnectWorkflow
	Clock          Clock
	Config         Config
}

// Coordinator owns the transport-neutral participant connection lifecycle.
// The repository lease operation and the selected nested usecase all execute
// with the context passed to one outer game transaction.
type Coordinator struct {
	transactions   game.TransactionManager
	authority      AuthorityProvider
	repository     Repository
	readiness      ReadinessWorkflow
	pausedPresence PausedPresenceWorkflow
	disconnect     DisconnectWorkflow
	reconnect      ReconnectWorkflow
	clock          Clock
	config         Config
}

var _ inbound.TournamentParticipantConnectionUseCase = (*Coordinator)(nil)

func NewCoordinator(dependencies Dependencies) (*Coordinator, error) {
	if dependencies.Transactions == nil || dependencies.Authority == nil ||
		dependencies.Repository == nil || dependencies.Clock == nil ||
		dependencies.Config.ReconnectDuration <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Coordinator{
		transactions:   dependencies.Transactions,
		authority:      dependencies.Authority,
		repository:     dependencies.Repository,
		readiness:      dependencies.Readiness,
		pausedPresence: dependencies.PausedPresence,
		disconnect:     dependencies.Disconnect,
		reconnect:      dependencies.Reconnect,
		clock:          dependencies.Clock,
		config:         dependencies.Config,
	}, nil
}

func (coordinator *Coordinator) Connect(
	ctx context.Context,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	return coordinator.execute(ctx, operationConnect, command)
}

func (coordinator *Coordinator) Disconnect(
	ctx context.Context,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	return coordinator.execute(ctx, operationDisconnect, command)
}

func (coordinator *Coordinator) execute(
	ctx context.Context,
	op operation,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	if ctx == nil {
		return inbound.ErrInvalidTournamentParticipantConnectionCommand
	}
	if err := command.Validate(); err != nil {
		return err
	}
	if coordinator == nil || coordinator.transactions == nil || coordinator.authority == nil ||
		coordinator.repository == nil || coordinator.clock == nil || coordinator.config.ReconnectDuration <= 0 {
		return ErrInvalidConfiguration
	}

	return coordinator.transactions.Do(ctx, func(txCtx context.Context) error {
		if txCtx == nil {
			return ErrInvalidConfiguration
		}
		resolved, err := coordinator.authority.ResolveParticipantConnection(
			txCtx, command.TournamentID, command.PlayerID,
		)
		if err != nil {
			return fmt.Errorf("resolve participant connection authority: %w", err)
		}
		if err := validateResolvedAuthority(resolved, command); err != nil {
			return err
		}

		switch op {
		case operationConnect:
			return coordinator.connectLocked(txCtx, resolved, command)
		case operationDisconnect:
			return coordinator.disconnectLocked(txCtx, resolved, command)
		default:
			return ErrInvalidConfiguration
		}
	})
}

func (coordinator *Coordinator) connectLocked(
	ctx context.Context,
	resolved ParticipantConnectionAuthority,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	result, err := coordinator.repository.OpenConnection(ctx, OpenConnectionCommand{
		TournamentID:         resolved.TournamentID,
		RosterID:             resolved.RosterID,
		ParticipantID:        resolved.ParticipantID,
		PlayerID:             resolved.PlayerID,
		ConnectionID:         command.ConnectionID,
		ConnectionGeneration: command.ConnectionGeneration,
	})
	if err != nil {
		return fmt.Errorf("open participant connection lease: %w", err)
	}
	if err := validateOpenResult(result, resolved, command); err != nil {
		return err
	}
	if !result.Opened || result.ActiveLeaseCount != 1 {
		return nil
	}
	return coordinator.applyAction(ctx, operationConnect, resolved, result.Lease, result.Action)
}

func (coordinator *Coordinator) disconnectLocked(
	ctx context.Context,
	resolved ParticipantConnectionAuthority,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	result, err := coordinator.repository.CloseConnection(ctx, CloseConnectionCommand{
		TournamentID:         resolved.TournamentID,
		RosterID:             resolved.RosterID,
		ParticipantID:        resolved.ParticipantID,
		PlayerID:             resolved.PlayerID,
		ConnectionID:         command.ConnectionID,
		ConnectionGeneration: command.ConnectionGeneration,
	})
	if err != nil {
		return fmt.Errorf("close participant connection lease: %w", err)
	}
	if err := validateCloseResult(result, resolved, command); err != nil {
		return err
	}
	if !result.Closed || result.ActiveLeaseCount != 0 {
		return nil
	}
	return coordinator.applyAction(ctx, operationDisconnect, resolved, result.Lease, result.Action)
}

//nolint:gocyclo // One transaction keeps the lease fence and its single domain action atomic.
func (coordinator *Coordinator) applyAction(
	ctx context.Context,
	op operation,
	resolved ParticipantConnectionAuthority,
	lease DurableLease,
	action ResolvedAction,
) error {
	switch action.Kind {
	case ActionNone:
		return nil
	case ActionClearReadiness:
		if coordinator.readiness == nil || op != operationDisconnect || action.Readiness == nil {
			return ErrWorkflowUnavailable
		}
		command := *action.Readiness
		command.CommandID = deterministicID(lease, op, "command")
		command.ParticipantID = resolved.ParticipantID
		if _, _, err := coordinator.readiness.ClearOnDisconnect(ctx, command); err != nil {
			return fmt.Errorf("clear participant readiness on disconnect: %w", err)
		}
		return nil
	case ActionPausedPresence:
		if coordinator.pausedPresence == nil || action.PausedPresence == nil {
			return ErrWorkflowUnavailable
		}
		command := *action.PausedPresence
		want := pausedomain.PresenceStateConnected
		if op == operationDisconnect {
			want = pausedomain.PresenceStateDisconnected
		}
		if command.NextState != "" && command.NextState != want {
			return ErrInvalidAction
		}
		command.CommandID = deterministicID(lease, op, "command")
		command.ParticipantID = resolved.ParticipantID
		command.NextState = want
		if _, _, err := coordinator.pausedPresence.Change(ctx, command); err != nil {
			return fmt.Errorf("change paused participant presence: %w", err)
		}
		return nil
	case ActionGameDisconnect:
		if coordinator.disconnect == nil || op != operationDisconnect || action.Disconnect == nil {
			return ErrWorkflowUnavailable
		}
		command, err := coordinator.disconnectCommand(lease, resolved, *action.Disconnect)
		if err != nil {
			return err
		}
		if _, _, err := coordinator.disconnect.Disconnect(ctx, command); err != nil {
			return fmt.Errorf("disconnect participant game presence: %w", err)
		}
		return nil
	case ActionGameReconnect:
		if coordinator.reconnect == nil || op != operationConnect || action.Reconnect == nil {
			return ErrWorkflowUnavailable
		}
		command, err := coordinator.reconnectCommand(lease, resolved, action)
		if err != nil {
			return err
		}
		if _, _, err := coordinator.reconnect.Reconnect(ctx, command); err != nil {
			return fmt.Errorf("reconnect participant game presence: %w", err)
		}
		return nil
	default:
		return ErrInvalidAction
	}
}

func (coordinator *Coordinator) disconnectCommand(
	lease DurableLease,
	resolved ParticipantConnectionAuthority,
	command game.DisconnectCommand,
) (game.DisconnectCommand, error) {
	now, err := coordinator.now()
	if err != nil {
		return game.DisconnectCommand{}, err
	}
	command.CommandID = deterministicID(lease, operationDisconnect, "command")
	command.ParticipantID = resolved.ParticipantID
	command.IntervalID = deterministicID(lease, operationDisconnect, "interval")
	command.Settlement = deterministicSettlementIDs(lease, operationDisconnect)
	if command.ContinuedFromID == nil {
		command.Deadline = now.Add(coordinator.config.ReconnectDuration)
	} else if !domain.IsValidServerTime(command.Deadline) || !command.Deadline.After(now) {
		return game.DisconnectCommand{}, game.ErrDeadline
	}
	if !domain.IsValidServerTime(command.Deadline) || !command.Deadline.After(now) {
		return game.DisconnectCommand{}, ErrInvalidAction
	}
	return command, nil
}

func (coordinator *Coordinator) reconnectCommand(
	lease DurableLease,
	resolved ParticipantConnectionAuthority,
	action ResolvedAction,
) (game.ReconnectCommand, error) {
	if action.Reconnect == nil {
		return game.ReconnectCommand{}, ErrInvalidAction
	}
	if !action.Deadline.IsZero() {
		now, err := coordinator.now()
		if err != nil {
			return game.ReconnectCommand{}, err
		}
		if !now.Before(action.Deadline) {
			return game.ReconnectCommand{}, game.ErrDeadline
		}
	}
	command := *action.Reconnect
	command.CommandID = deterministicID(lease, operationConnect, "command")
	command.ParticipantID = resolved.ParticipantID
	command.Settlement = deterministicSettlementIDs(lease, operationConnect)
	return command, nil
}

func (coordinator *Coordinator) now() (time.Time, error) {
	now := coordinator.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return time.Time{}, domain.ErrValidation
	}
	return now, nil
}

func deterministicID(lease DurableLease, op operation, component string) uuid.UUID {
	seed := fmt.Sprintf(
		"participant-connection/%s/%s/%s/%s/%d",
		lease.ID.String(), lease.TournamentID.String(), lease.ConnectionID.String(), op,
		lease.ConnectionGeneration,
	)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed+"/"+component))
}

func deterministicSettlementIDs(lease DurableLease, op operation) game.SettlementIDs {
	return game.SettlementIDs{
		GameResultRevisionID:   domain.OfficialResultRevisionID(deterministicID(lease, op, "game-result")),
		ScoreRevisionID:        domain.SeriesScoreRevisionID(deterministicID(lease, op, "score")),
		SeriesResultRevisionID: domain.OfficialResultRevisionID(deterministicID(lease, op, "series-result")),
		ReplayRouteID:          deterministicID(lease, op, "replay-route"),
		AuditEventID:           deterministicID(lease, op, "audit"),
		OutboxEventID:          deterministicID(lease, op, "outbox"),
		ProjectionRevisionID:   deterministicID(lease, op, "projection"),
	}
}

func validateResolvedAuthority(
	resolved ParticipantConnectionAuthority,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	if resolved.TournamentID == uuid.Nil || resolved.RosterID == uuid.Nil ||
		resolved.ParticipantID == uuid.Nil || resolved.PlayerID == uuid.Nil ||
		resolved.TournamentID != command.TournamentID || resolved.PlayerID != command.PlayerID ||
		resolved.Scope.TournamentID != resolved.TournamentID || resolved.Scope.RosterID != resolved.RosterID ||
		resolved.Scope.Authority != resolved.Authority || resolved.Authority.Validate() != nil ||
		resolved.Scope.Validate() != nil {
		return ErrInvalidAuthority
	}
	return nil
}

func validateOpenResult(
	result OpenConnectionResult,
	resolved ParticipantConnectionAuthority,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	if result.ActiveLeaseCount < 0 {
		return ErrInvalidLease
	}
	if result.Opened {
		if result.ActiveLeaseCount < 1 || validateLease(result.Lease, resolved, command) != nil {
			return ErrInvalidLease
		}
	} else if !isZeroLease(result.Lease) && validateLease(result.Lease, resolved, command) != nil {
		return ErrInvalidLease
	}
	return validateAction(result.Action, operationConnect, resolved)
}

func validateCloseResult(
	result CloseConnectionResult,
	resolved ParticipantConnectionAuthority,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	if result.ActiveLeaseCount < 0 {
		return ErrInvalidLease
	}
	if result.Closed {
		if validateLease(result.Lease, resolved, command) != nil {
			return ErrInvalidLease
		}
	} else if !isZeroLease(result.Lease) && validateLease(result.Lease, resolved, command) != nil {
		return ErrInvalidLease
	}
	return validateAction(result.Action, operationDisconnect, resolved)
}

func validateLease(
	lease DurableLease,
	resolved ParticipantConnectionAuthority,
	command inbound.TournamentParticipantConnectionCommand,
) error {
	if lease.ID == uuid.Nil || lease.TournamentID == uuid.Nil || lease.RosterID == uuid.Nil ||
		lease.ParticipantID == uuid.Nil || lease.PlayerID == uuid.Nil || lease.ConnectionID == uuid.Nil ||
		lease.ConnectionGeneration < 1 || lease.TournamentID != resolved.TournamentID ||
		lease.RosterID != resolved.RosterID || lease.ParticipantID != resolved.ParticipantID ||
		lease.PlayerID != resolved.PlayerID || lease.ConnectionID != command.ConnectionID ||
		lease.ConnectionGeneration != command.ConnectionGeneration {
		return ErrInvalidLease
	}
	return nil
}

func isZeroLease(lease DurableLease) bool {
	return lease == (DurableLease{})
}

//nolint:gocyclo // The closed action union is validated fail-closed in one boundary.
func validateAction(
	action ResolvedAction,
	op operation,
	resolved ParticipantConnectionAuthority,
) error {
	pointers := 0
	if action.Readiness != nil {
		pointers++
	}
	if action.PausedPresence != nil {
		pointers++
	}
	if action.Disconnect != nil {
		pointers++
	}
	if action.Reconnect != nil {
		pointers++
	}

	if action.Kind == ActionNone {
		if pointers != 0 || !action.Deadline.IsZero() {
			return ErrInvalidAction
		}
		return nil
	}
	if pointers != 1 {
		return ErrInvalidAction
	}

	switch action.Kind {
	case ActionNone:
		return ErrInvalidAction
	case ActionClearReadiness:
		if op != operationDisconnect || action.Readiness == nil ||
			action.Readiness.ParticipantID != resolved.ParticipantID ||
			action.Readiness.Scope.WaveID != resolved.Scope.WaveID ||
			action.Readiness.Scope.WaveID == uuid.Nil || action.Readiness.Scope.WindowID == uuid.Nil ||
			action.Readiness.ExpectedWaveRevisionID.IsZero() || action.Readiness.ExpectedWindowRevisionID.IsZero() ||
			!action.Deadline.IsZero() {
			return ErrInvalidAction
		}
	case ActionPausedPresence:
		if action.PausedPresence == nil || action.PausedPresence.Scope != resolved.Scope ||
			action.PausedPresence.PauseID == uuid.Nil ||
			action.PausedPresence.ParticipantID != resolved.ParticipantID ||
			action.PausedPresence.ExpectedGraphRevision < 1 ||
			action.PausedPresence.ExpectedPauseRevision < 1 ||
			action.PausedPresence.ExpectedPresenceEpoch < 1 ||
			action.PausedPresence.ExpectedPresenceRevision < 1 ||
			(action.PausedPresence.NextState != "" &&
				action.PausedPresence.NextState != pausedomain.PresenceStateConnected &&
				action.PausedPresence.NextState != pausedomain.PresenceStateDisconnected) ||
			!action.Deadline.IsZero() {
			return ErrInvalidAction
		}
	case ActionGameDisconnect:
		if op != operationDisconnect || action.Disconnect == nil ||
			action.Disconnect.Scope != resolved.Scope ||
			action.Disconnect.ParticipantID != resolved.ParticipantID ||
			(!action.Deadline.IsZero() && !domain.IsValidServerTime(action.Deadline)) {
			return ErrInvalidAction
		}
		if action.Disconnect.ContinuedFromID != nil && *action.Disconnect.ContinuedFromID == uuid.Nil {
			return ErrInvalidAction
		}
		if !action.Disconnect.Deadline.IsZero() && !domain.IsValidServerTime(action.Disconnect.Deadline) {
			return ErrInvalidAction
		}
	case ActionGameReconnect:
		if op != operationConnect || action.Reconnect == nil ||
			action.Reconnect.Scope != resolved.Scope ||
			action.Reconnect.ParticipantID != resolved.ParticipantID ||
			action.Reconnect.IntervalID == uuid.Nil {
			return ErrInvalidAction
		}
		if !action.Deadline.IsZero() && !domain.IsValidServerTime(action.Deadline) {
			return ErrInvalidAction
		}
	default:
		return ErrInvalidAction
	}
	return nil
}
