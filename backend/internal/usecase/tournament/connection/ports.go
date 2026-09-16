package connection

import (
	"context"
	"time"

	"github.com/google/uuid"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

// Clock supplies the same authoritative time source used by the game
// reconnect workflow. A process wall clock is not a valid fallback here.
type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

// AuthorityProvider resolves the server-owned participant identity and the
// execution authority in the caller transaction. A concrete implementation
// can compose this narrow port with authority.Controller and roster storage.
type AuthorityProvider interface {
	ResolveParticipantConnection(
		ctx context.Context,
		tournamentID uuid.UUID,
		playerID uuid.UUID,
	) (ParticipantConnectionAuthority, error)
}

// ParticipantConnectionAuthority is the identity and graph scope resolved by
// the server. ParticipantID, roster and authority values never come from the
// transport command.
type ParticipantConnectionAuthority struct {
	TournamentID  uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	Scope         pausedomain.GraphScope
	Authority     authoritydomain.Identity
}

// DurableLease is the immutable identity of one active or closed socket
// lease. ConnectionGeneration is the stale-cleanup fence.
type DurableLease struct {
	ID                   uuid.UUID
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	ParticipantID        uuid.UUID
	PlayerID             uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
}

// OrphanedConnectionLease is a recovery candidate read from durable evidence.
// Its authority stamp is immutable and is never used as the caller's current
// authority.  The recovery repository rechecks both identities in its CAS.
type OrphanedConnectionLease struct {
	Lease     DurableLease
	Revision  int64
	Authority authoritydomain.Identity
}

// OpenConnectionCommand identifies the server-resolved lease to create. The
// repository must atomically persist it and return the post-operation active
// lease count.
type OpenConnectionCommand struct {
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	ParticipantID        uuid.UUID
	PlayerID             uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
}

// CloseConnectionCommand identifies the exact lease fence to close. A stale
// generation or repeated close must update no row.
type CloseConnectionCommand struct {
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	ParticipantID        uuid.UUID
	PlayerID             uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
}

type OpenConnectionResult struct {
	Lease            DurableLease
	Opened           bool
	ActiveLeaseCount int
	Action           ResolvedAction
}

type CloseConnectionResult struct {
	Lease            DurableLease
	Closed           bool
	ActiveLeaseCount int
	Action           ResolvedAction
}

// RecoveryRepository exposes only the owner-bound recovery surface.  Listing
// is read-only; closing must join the coordinator transaction so the lease CAS
// and its one last-lease action commit together.
type RecoveryRepository interface {
	ListParticipantConnectionLeaseTournaments(ctx context.Context) ([]uuid.UUID, error)
	ListParticipantConnectionRecoveryCandidates(ctx context.Context, limit int32) ([]OrphanedConnectionLease, error)
	CloseOrphanedConnection(
		ctx context.Context,
		resolved ParticipantConnectionAuthority,
		candidate OrphanedConnectionLease,
	) (CloseConnectionResult, error)
}

// RecoveryAuthorityProvider renews authority owned by this process and takes
// it over only after a foreign owner expires. This distinguishes a quiet live
// socket from a socket orphaned by process loss.
type RecoveryAuthorityProvider interface {
	RecoveryAuthorityFor(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (authoritydomain.Identity, bool, error)
}

// Repository is the durable lease and server-side action boundary. Open and
// close must join the transaction supplied by the coordinator. Action values
// are already resolved against the authoritative tournament state.
type Repository interface {
	OpenConnection(ctx context.Context, command OpenConnectionCommand) (OpenConnectionResult, error)
	CloseConnection(ctx context.Context, command CloseConnectionCommand) (CloseConnectionResult, error)
}

// TerminalAdvancer consumes a fresh terminal Game settlement while the caller
// still owns the surrounding settlement transaction. It deliberately exposes
// only the terminal progression operation needed by this lifecycle.
type TerminalAdvancer interface {
	AdvanceAfterSeriesSettlement(
		ctx context.Context,
		command playoff.TerminalSeriesCommand,
	) (playoff.TerminalReceipt, error)
}

type ActionKind string

const (
	ActionNone           ActionKind = "none"
	ActionClearReadiness ActionKind = "clear_readiness"
	ActionPausedPresence ActionKind = "paused_presence"
	ActionGameDisconnect ActionKind = "game_disconnect"
	ActionGameReconnect  ActionKind = "game_reconnect"
)

// ResolvedAction is a tagged union. Exactly one command pointer is allowed
// for a non-none action. The command identities are server-resolved except
// for operation IDs and settlement IDs, which the coordinator derives from
// the durable lease fence.
type ResolvedAction struct {
	Kind           ActionKind
	Readiness      *readiness.DisconnectReadinessCommand
	PausedPresence *pauseusecase.PausedPresenceCommand
	Disconnect     *gameusecase.DisconnectCommand
	Reconnect      *gameusecase.ReconnectCommand
	Deadline       time.Time
}

type ReadinessWorkflow interface {
	ClearOnDisconnect(
		ctx context.Context,
		command readiness.DisconnectReadinessCommand,
	) (*readiness.ReadinessRecord, bool, error)
}

type PausedPresenceWorkflow interface {
	Change(
		ctx context.Context,
		command pauseusecase.PausedPresenceCommand,
	) (*pauseusecase.PausedPresenceRecord, bool, error)
}

type DisconnectWorkflow interface {
	Disconnect(
		ctx context.Context,
		command gameusecase.DisconnectCommand,
	) (*gameusecase.ReconnectRecord, bool, error)
}

type ReconnectWorkflow interface {
	Reconnect(
		ctx context.Context,
		command gameusecase.ReconnectCommand,
	) (*gameusecase.ReconnectRecord, bool, error)
}

// Config intentionally has no implicit reconnect duration. The deadline for
// a newly opened game reconnect interval is derived from Clock and this
// positive value.
type Config struct {
	ReconnectDuration time.Duration
}
