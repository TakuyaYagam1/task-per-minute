package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

// participantConnectionAuthorityController is deliberately narrower than the
// authority usecase.  The adapter only needs the server-owned identity and
// must not acquire or manufacture an authority lease itself.
type participantConnectionAuthorityController interface {
	AuthorityFor(ctx context.Context, tournamentID uuid.UUID) (authoritydomain.Identity, error)
}

// ParticipantConnectionPostgres is the durable participant socket boundary.
// It is safe to call both directly and from Coordinator: TxManager.Do joins an
// ambient transaction rather than opening a second one.
type ParticipantConnectionPostgres struct {
	tx        *db.TxManager
	authority participantConnectionAuthorityController
}

func NewParticipantConnectionPostgres(
	tx *db.TxManager,
	authority participantConnectionAuthorityController,
) *ParticipantConnectionPostgres {
	return &ParticipantConnectionPostgres{tx: tx, authority: authority}
}

var (
	_ connection.AuthorityProvider  = (*ParticipantConnectionPostgres)(nil)
	_ connection.Repository         = (*ParticipantConnectionPostgres)(nil)
	_ connection.RecoveryRepository = (*ParticipantConnectionPostgres)(nil)
)

func (repository *ParticipantConnectionPostgres) ResolveParticipantConnection(
	ctx context.Context,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
) (connection.ParticipantConnectionAuthority, error) {
	if !validParticipantConnectionRepository(ctx, repository) || tournamentID == uuid.Nil || playerID == uuid.Nil {
		return connection.ParticipantConnectionAuthority{}, domain.ErrValidation
	}

	var resolved connection.ParticipantConnectionAuthority
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		var err error
		resolved, _, _, err = repository.resolveParticipantConnectionLocked(txCtx, q, tournamentID, uuid.Nil, uuid.Nil, playerID)
		return err
	})
	if err != nil {
		return connection.ParticipantConnectionAuthority{}, fmt.Errorf("resolve participant connection authority: %w", err)
	}
	return resolved, nil
}

func (repository *ParticipantConnectionPostgres) OpenConnection(
	ctx context.Context,
	command connection.OpenConnectionCommand,
) (connection.OpenConnectionResult, error) {
	if !validParticipantConnectionRepository(ctx, repository) || !validOpenConnectionCommand(command) {
		return connection.OpenConnectionResult{}, domain.ErrValidation
	}

	var result connection.OpenConnectionResult
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, err = repository.openConnectionLocked(txCtx, command)
		return err
	})
	if err != nil {
		return connection.OpenConnectionResult{}, fmt.Errorf("open participant connection: %w", err)
	}
	return result, nil
}

func (repository *ParticipantConnectionPostgres) CloseConnection(
	ctx context.Context,
	command connection.CloseConnectionCommand,
) (connection.CloseConnectionResult, error) {
	if !validParticipantConnectionRepository(ctx, repository) || !validCloseConnectionCommand(command) {
		return connection.CloseConnectionResult{}, domain.ErrValidation
	}

	var result connection.CloseConnectionResult
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, err = repository.closeConnectionLocked(txCtx, command)
		return err
	})
	if err != nil {
		return connection.CloseConnectionResult{}, fmt.Errorf("close participant connection: %w", err)
	}
	return result, nil
}

func (repository *ParticipantConnectionPostgres) ListParticipantConnectionRecoveryCandidates(
	ctx context.Context,
	limit int32,
) ([]connection.OrphanedConnectionLease, error) {
	if !validParticipantConnectionRepository(ctx, repository) || limit < 1 {
		return nil, domain.ErrValidation
	}
	var rows []sqlc.ParticipantConnectionLease
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		rows, err = repository.tx.Querier(txCtx).ListParticipantConnectionRecoveryCandidates(txCtx, limit)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list participant connection recovery candidates: %w", err)
	}
	candidates := make([]connection.OrphanedConnectionLease, 0, len(rows))
	for _, row := range rows {
		candidate, err := mapParticipantConnectionRecoveryCandidate(row)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (repository *ParticipantConnectionPostgres) ListParticipantConnectionLeaseTournaments(
	ctx context.Context,
) ([]uuid.UUID, error) {
	if !validParticipantConnectionRepository(ctx, repository) {
		return nil, domain.ErrValidation
	}
	var tournamentIDs []uuid.UUID
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		tournamentIDs, err = repository.tx.Querier(txCtx).ListParticipantConnectionLeaseTournaments(txCtx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list participant connection lease tournaments: %w", err)
	}
	for _, tournamentID := range tournamentIDs {
		if tournamentID == uuid.Nil {
			return nil, fmt.Errorf("invalid participant connection lease tournament: %w", domain.ErrInternal)
		}
	}
	return tournamentIDs, nil
}

func (repository *ParticipantConnectionPostgres) CloseOrphanedConnection(
	ctx context.Context,
	resolved connection.ParticipantConnectionAuthority,
	candidate connection.OrphanedConnectionLease,
) (connection.CloseConnectionResult, error) {
	if !validParticipantConnectionRepository(ctx, repository) ||
		!validParticipantConnectionAuthority(resolved) ||
		candidate.Authority.Validate() != nil || validateOrphanedConnectionCandidate(candidate) != nil {
		return connection.CloseConnectionResult{}, domain.ErrValidation
	}
	var result connection.CloseConnectionResult
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, err = repository.closeOrphanedConnectionLocked(txCtx, resolved, candidate)
		return err
	})
	if err != nil {
		return connection.CloseConnectionResult{}, fmt.Errorf("close orphaned participant connection: %w", err)
	}
	return result, nil
}

func (repository *ParticipantConnectionPostgres) openConnectionLocked(
	ctx context.Context,
	command connection.OpenConnectionCommand,
) (connection.OpenConnectionResult, error) {
	q := repository.tx.Querier(ctx)
	state, err := repository.lockParticipantConnectionState(ctx, q, command.TournamentID, command.RosterID, command.ParticipantID, command.PlayerID)
	if err != nil {
		return connection.OpenConnectionResult{}, err
	}
	if err := repository.lockParticipantConnectionLeases(ctx, q, state); err != nil {
		return connection.OpenConnectionResult{}, err
	}

	action, binding, err := repository.resolveParticipantConnectionAction(ctx, q, state, connectionOperationConnect)
	if err != nil {
		return connection.OpenConnectionResult{}, err
	}

	leaseID := uuid.New()
	row, err := q.InsertParticipantConnectionLease(ctx, sqlc.InsertParticipantConnectionLeaseParams{
		ID:                   leaseID,
		TournamentID:         command.TournamentID,
		RosterID:             command.RosterID,
		ParticipantID:        command.ParticipantID,
		PlayerID:             command.PlayerID,
		ConnectionID:         command.ConnectionID,
		ConnectionGeneration: command.ConnectionGeneration,
		AssignmentID:         nullableConnectionUUID(binding.AssignmentID),
		SeriesID:             nullableConnectionUUID(binding.SeriesID),
		GameAttemptID:        nullableConnectionUUID(binding.GameAttemptID),
		AuthorityHolderID:    nullableConnectionUUID(state.Authority.Authority.HolderID),
		AuthorityLeaseID:     nullableConnectionUUID(state.Authority.Authority.LeaseID),
		AuthorityEpoch:       authorityEpochPointer(state.Authority.Authority.Epoch),
	})
	opened := true
	if errors.Is(err, pgx.ErrNoRows) {
		opened = false
		row, err = q.FindParticipantConnectionLeaseByFence(ctx, sqlc.FindParticipantConnectionLeaseByFenceParams{
			ConnectionID:         command.ConnectionID,
			ConnectionGeneration: command.ConnectionGeneration,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return connection.OpenConnectionResult{}, fmt.Errorf("participant connection lease conflict: %w", domain.ErrConflict)
		}
	}
	if err != nil {
		return connection.OpenConnectionResult{}, fmt.Errorf("insert participant connection lease: %w", err)
	}
	if err := validateOpenedParticipantConnectionAuthority(row, state.Authority.Authority, opened); err != nil {
		return connection.OpenConnectionResult{}, err
	}
	lease, err := mapParticipantConnectionLease(row)
	if err != nil {
		return connection.OpenConnectionResult{}, err
	}
	if err := validateParticipantConnectionLease(lease, command.TournamentID, command.RosterID, command.ParticipantID, command.PlayerID, command.ConnectionID, command.ConnectionGeneration); err != nil {
		return connection.OpenConnectionResult{}, err
	}
	if !opened && !sameParticipantConnectionBinding(row, binding) {
		return connection.OpenConnectionResult{}, fmt.Errorf("participant connection lease binding changed: %w", domain.ErrConflict)
	}

	activeCount, err := repository.countParticipantConnectionLeases(ctx, q, command.TournamentID, command.RosterID, command.ParticipantID)
	if err != nil {
		return connection.OpenConnectionResult{}, err
	}
	if !opened {
		// A duplicate fence is deliberately a no-op.  Returning no action keeps
		// retries from replaying the nested usecase after the first commit.
		action = connection.ResolvedAction{Kind: connection.ActionNone}
	}
	return connection.OpenConnectionResult{
		Lease:            lease,
		Opened:           opened,
		ActiveLeaseCount: activeCount,
		Action:           action,
	}, nil
}

func (repository *ParticipantConnectionPostgres) closeConnectionLocked(
	ctx context.Context,
	command connection.CloseConnectionCommand,
) (connection.CloseConnectionResult, error) {
	q := repository.tx.Querier(ctx)
	state, err := repository.lockParticipantConnectionState(ctx, q, command.TournamentID, command.RosterID, command.ParticipantID, command.PlayerID)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	if err := repository.lockParticipantConnectionLeases(ctx, q, state); err != nil {
		return connection.CloseConnectionResult{}, err
	}

	row, err := q.CloseParticipantConnectionLease(ctx, sqlc.CloseParticipantConnectionLeaseParams{
		TournamentID:         command.TournamentID,
		RosterID:             command.RosterID,
		ParticipantID:        command.ParticipantID,
		PlayerID:             command.PlayerID,
		ConnectionID:         command.ConnectionID,
		ConnectionGeneration: command.ConnectionGeneration,
		AuthorityHolderID:    nullableConnectionUUID(state.Authority.Authority.HolderID),
		AuthorityLeaseID:     nullableConnectionUUID(state.Authority.Authority.LeaseID),
		AuthorityEpoch:       authorityEpochPointer(state.Authority.Authority.Epoch),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		activeCount, countErr := repository.countParticipantConnectionLeases(ctx, q, command.TournamentID, command.RosterID, command.ParticipantID)
		if countErr != nil {
			return connection.CloseConnectionResult{}, countErr
		}
		return connection.CloseConnectionResult{ActiveLeaseCount: activeCount}, nil
	}
	if err != nil {
		return connection.CloseConnectionResult{}, fmt.Errorf("close participant connection lease: %w", err)
	}
	lease, err := mapParticipantConnectionLease(row)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	if err := validateParticipantConnectionLease(lease, command.TournamentID, command.RosterID, command.ParticipantID, command.PlayerID, command.ConnectionID, command.ConnectionGeneration); err != nil {
		return connection.CloseConnectionResult{}, err
	}
	if row.State != "disconnected" || !row.DisconnectedAt.Valid {
		return connection.CloseConnectionResult{}, fmt.Errorf("closed participant connection lease did not persist disconnected state: %w", domain.ErrInternal)
	}
	currentSubscriber, err := repository.lockParticipantConnectionCurrentSubscriber(ctx, q, command)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	activeCount, err := repository.countParticipantConnectionLeases(ctx, q, command.TournamentID, command.RosterID, command.ParticipantID)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	if !currentSubscriber {
		return connection.CloseConnectionResult{
			Lease:            lease,
			Closed:           true,
			ActiveLeaseCount: activeCount,
			Action:           connection.ResolvedAction{Kind: connection.ActionNone},
		}, nil
	}
	action, _, err := repository.resolveParticipantConnectionAction(ctx, q, state, connectionOperationDisconnect)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	action = participantConnectionActionForSubscriberFence(currentSubscriber, action)
	// The lease binding remains immutable historical evidence.  Once the exact
	// subscriber fence is locked, however, the socket is still the current
	// participant connection and the action must follow the graph it closes
	// against.  A superseded fence returned above with ActionNone and can only
	// close its own durable lease.
	return connection.CloseConnectionResult{
		Lease:            lease,
		Closed:           true,
		ActiveLeaseCount: activeCount,
		Action:           action,
	}, nil
}

func (repository *ParticipantConnectionPostgres) closeOrphanedConnectionLocked(
	ctx context.Context,
	resolved connection.ParticipantConnectionAuthority,
	candidate connection.OrphanedConnectionLease,
) (connection.CloseConnectionResult, error) {
	q := repository.tx.Querier(ctx)
	state, err := repository.lockParticipantConnectionState(
		ctx, q, candidate.Lease.TournamentID, candidate.Lease.RosterID,
		candidate.Lease.ParticipantID, candidate.Lease.PlayerID,
	)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	if state.Authority.Authority != resolved.Authority {
		return connection.CloseConnectionResult{}, fmt.Errorf("participant connection recovery authority changed: %w", domain.ErrConflict)
	}
	if err := repository.lockParticipantConnectionLeases(ctx, q, state); err != nil {
		return connection.CloseConnectionResult{}, err
	}

	row, err := q.CloseExpiredParticipantConnectionLease(ctx, sqlc.CloseExpiredParticipantConnectionLeaseParams{
		TournamentID:             candidate.Lease.TournamentID,
		RosterID:                 candidate.Lease.RosterID,
		ParticipantID:            candidate.Lease.ParticipantID,
		PlayerID:                 candidate.Lease.PlayerID,
		ID:                       candidate.Lease.ID,
		ExpectedRevision:         candidate.Revision,
		ConnectionID:             candidate.Lease.ConnectionID,
		ConnectionGeneration:     candidate.Lease.ConnectionGeneration,
		AuthorityHolderID:        candidate.Authority.HolderID,
		AuthorityLeaseID:         candidate.Authority.LeaseID,
		AuthorityEpoch:           candidate.Authority.Epoch,
		CurrentAuthorityHolderID: state.Authority.Authority.HolderID,
		CurrentAuthorityLeaseID:  state.Authority.Authority.LeaseID,
		CurrentAuthorityEpoch:    state.Authority.Authority.Epoch,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		activeCount, countErr := repository.countParticipantConnectionLeases(
			ctx, q, candidate.Lease.TournamentID, candidate.Lease.RosterID, candidate.Lease.ParticipantID,
		)
		if countErr != nil {
			return connection.CloseConnectionResult{}, countErr
		}
		return connection.CloseConnectionResult{ActiveLeaseCount: activeCount}, nil
	}
	if err != nil {
		return connection.CloseConnectionResult{}, fmt.Errorf("close expired participant connection lease: %w", err)
	}
	lease, err := validateRecoveredParticipantConnectionLease(row, candidate)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	currentSubscriber, err := repository.lockParticipantConnectionCurrentSubscriber(ctx, q, connection.CloseConnectionCommand{
		TournamentID:         candidate.Lease.TournamentID,
		RosterID:             candidate.Lease.RosterID,
		ParticipantID:        candidate.Lease.ParticipantID,
		PlayerID:             candidate.Lease.PlayerID,
		ConnectionID:         candidate.Lease.ConnectionID,
		ConnectionGeneration: candidate.Lease.ConnectionGeneration,
	})
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	activeCount, err := repository.countParticipantConnectionLeases(
		ctx, q, candidate.Lease.TournamentID, candidate.Lease.RosterID, candidate.Lease.ParticipantID,
	)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	if !currentSubscriber {
		return connection.CloseConnectionResult{
			Lease:            lease,
			Closed:           true,
			ActiveLeaseCount: activeCount,
			Action:           connection.ResolvedAction{Kind: connection.ActionNone},
		}, nil
	}
	action, _, err := repository.resolveParticipantConnectionAction(ctx, q, state, connectionOperationRecovery)
	if err != nil {
		return connection.CloseConnectionResult{}, err
	}
	action = participantConnectionActionForSubscriberFence(currentSubscriber, action)
	return connection.CloseConnectionResult{
		Lease:            lease,
		Closed:           true,
		ActiveLeaseCount: activeCount,
		Action:           action,
	}, nil
}

type participantConnectionOperation string

const (
	connectionOperationConnect    participantConnectionOperation = "connect"
	connectionOperationDisconnect participantConnectionOperation = "disconnect"
	connectionOperationRecovery   participantConnectionOperation = "recover"
)

type participantConnectionState struct {
	Authority connection.ParticipantConnectionAuthority
	Root      sqlc.LockParticipantConnectionIdentityRow
	Wave      sqlc.LockParticipantConnectionWaveRow
	Window    *sqlc.LockParticipantConnectionReadyWindowRow
	Readiness *sqlc.LockParticipantConnectionReadinessRow
}

type participantConnectionBinding struct {
	AssignmentID  uuid.UUID
	SeriesID      uuid.UUID
	GameAttemptID uuid.UUID
}

//nolint:gocyclo // One locked snapshot validates every participant binding before any lease mutation.
func (repository *ParticipantConnectionPostgres) lockParticipantConnectionState(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	playerID uuid.UUID,
) (participantConnectionState, error) {
	authority, root, wave, err := repository.resolveParticipantConnectionLocked(ctx, q, tournamentID, rosterID, participantID, playerID)
	if err != nil {
		return participantConnectionState{}, err
	}
	state := participantConnectionState{Authority: authority, Root: root, Wave: wave}
	window, err := q.LockParticipantConnectionReadyWindow(ctx, sqlc.LockParticipantConnectionReadyWindowParams{
		WaveID:   wave.WaveID,
		RosterID: root.RosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return participantConnectionState{}, fmt.Errorf("lock participant connection ready window: %w", err)
	}
	if window.ReadyWindowID == uuid.Nil || window.WaveID != wave.WaveID || window.RosterID != root.RosterID || window.ReadyWindowRevisionID == uuid.Nil || !window.ReadyWindowOpenedAt.Valid || !window.ReadyWindowDeadline.Valid || !window.ReadyWindowDeadline.Time.After(window.ReadyWindowOpenedAt.Time) {
		return participantConnectionState{}, fmt.Errorf("participant connection ready window is invalid: %w", domain.ErrInternal)
	}
	state.Window = &window
	readinessRow, readinessErr := q.LockParticipantConnectionReadiness(ctx, sqlc.LockParticipantConnectionReadinessParams{
		WaveID:        wave.WaveID,
		ReadyWindowID: nullableConnectionUUID(window.ReadyWindowID),
		RosterID:      root.RosterID,
		ParticipantID: participantID,
	})
	if errors.Is(readinessErr, pgx.ErrNoRows) {
		return state, nil
	}
	if readinessErr != nil {
		return participantConnectionState{}, fmt.Errorf("lock participant connection readiness: %w", readinessErr)
	}
	if readinessRow.WaveID != wave.WaveID || !readinessRow.ReadyWindowID.Valid || readinessRow.ReadyWindowID.UUID != window.ReadyWindowID || readinessRow.RosterID != root.RosterID || readinessRow.ParticipantID != participantID || readinessRow.ReadinessRevision < 1 {
		return participantConnectionState{}, fmt.Errorf("participant connection readiness identity is invalid: %w", domain.ErrInternal)
	}
	state.Readiness = &readinessRow
	return state, nil
}

//nolint:gocyclo // Authority resolution keeps roster, wave, participant, and assignment invariants together.
func (repository *ParticipantConnectionPostgres) resolveParticipantConnectionLocked(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	playerID uuid.UUID,
) (connection.ParticipantConnectionAuthority, sqlc.LockParticipantConnectionIdentityRow, sqlc.LockParticipantConnectionWaveRow, error) {
	serverAuthority, err := repository.lockParticipantConnectionAuthority(ctx, tournamentID)
	if err != nil {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, err
	}
	root, err := q.LockParticipantConnectionIdentity(ctx, sqlc.LockParticipantConnectionIdentityParams{
		TournamentID: tournamentID,
		PlayerID:     playerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("participant connection identity not found: %w", domain.ErrAssignmentParticipant)
	}
	if err != nil {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("lock participant connection identity: %w", err)
	}
	if root.TournamentID != tournamentID || root.PlayerID != playerID || (rosterID != uuid.Nil && root.RosterID != rosterID) || (participantID != uuid.Nil && root.ParticipantID != participantID) {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("participant connection identity mismatch: %w", domain.ErrConflict)
	}
	if !domain.TournamentState(root.TournamentState).IsValid() {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("participant connection tournament state is invalid: %w", domain.ErrInternal)
	}
	wave, err := q.LockParticipantConnectionWave(ctx, sqlc.LockParticipantConnectionWaveParams{
		TournamentID:  tournamentID,
		RosterID:      root.RosterID,
		ParticipantID: root.ParticipantID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("participant connection wave not found: %w", domain.ErrAssignmentParticipant)
	}
	if err != nil {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("lock participant connection wave: %w", err)
	}
	if wave.TournamentID != root.TournamentID || wave.RosterID != root.RosterID || wave.ParticipantID != root.ParticipantID || wave.WaveID == uuid.Nil || wave.WaveRevisionID == uuid.Nil || wave.WaveRevision < 1 || !domain.WaveState(wave.WaveState).IsValid() {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, fmt.Errorf("participant connection wave identity is invalid: %w", domain.ErrInternal)
	}
	resolvedAuthority, err := participantConnectionAuthorityFromRows(serverAuthority, root, wave)
	if err != nil {
		return connection.ParticipantConnectionAuthority{}, sqlc.LockParticipantConnectionIdentityRow{}, sqlc.LockParticipantConnectionWaveRow{}, err
	}
	return resolvedAuthority, root, wave, nil
}

func participantConnectionAuthorityFromRows(
	serverAuthority connection.ParticipantConnectionAuthority,
	root sqlc.LockParticipantConnectionIdentityRow,
	wave sqlc.LockParticipantConnectionWaveRow,
) (connection.ParticipantConnectionAuthority, error) {
	if serverAuthority.Authority.Validate() != nil || root.TournamentID == uuid.Nil || root.RosterID == uuid.Nil || root.ParticipantID == uuid.Nil || root.PlayerID == uuid.Nil || wave.WaveID == uuid.Nil || root.TournamentID != serverAuthority.TournamentID || wave.TournamentID != root.TournamentID || wave.RosterID != root.RosterID || wave.ParticipantID != root.ParticipantID {
		return connection.ParticipantConnectionAuthority{}, fmt.Errorf("participant connection scope identity is invalid: %w", domain.ErrInternal)
	}
	serverAuthority.RosterID = root.RosterID
	serverAuthority.ParticipantID = root.ParticipantID
	serverAuthority.PlayerID = root.PlayerID
	serverAuthority.Scope = pausedomain.GraphScope{
		TournamentID: root.TournamentID,
		RosterID:     root.RosterID,
		WaveID:       wave.WaveID,
		Authority:    serverAuthority.Authority,
	}
	if err := serverAuthority.Scope.Validate(); err != nil {
		return connection.ParticipantConnectionAuthority{}, fmt.Errorf("participant connection scope is invalid: %w", domain.ErrInternal)
	}
	return serverAuthority, nil
}

func (repository *ParticipantConnectionPostgres) lockParticipantConnectionAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (connection.ParticipantConnectionAuthority, error) {
	if repository.authority == nil {
		return connection.ParticipantConnectionAuthority{}, domain.ErrValidation
	}
	authority, err := repository.authority.AuthorityFor(ctx, tournamentID)
	if err != nil {
		return connection.ParticipantConnectionAuthority{}, fmt.Errorf("resolve execution authority: %w", err)
	}
	if authority.Validate() != nil || authority.TournamentID != tournamentID {
		return connection.ParticipantConnectionAuthority{}, fmt.Errorf("invalid execution authority identity: %w", domain.ErrConflict)
	}
	return connection.ParticipantConnectionAuthority{
		TournamentID: tournamentID,
		Authority:    authority,
	}, nil
}

func (repository *ParticipantConnectionPostgres) lockParticipantConnectionLeases(
	ctx context.Context,
	q *sqlc.Queries,
	state participantConnectionState,
) error {
	rows, err := q.LockParticipantConnectionLeases(ctx, sqlc.LockParticipantConnectionLeasesParams{
		TournamentID:  state.Root.TournamentID,
		RosterID:      state.Root.RosterID,
		ParticipantID: state.Root.ParticipantID,
	})
	if err != nil {
		return fmt.Errorf("lock participant connection leases: %w", err)
	}
	for _, row := range rows {
		if row.ID == uuid.Nil || row.TournamentID != state.Root.TournamentID || row.RosterID != state.Root.RosterID || row.ParticipantID != state.Root.ParticipantID || row.PlayerID != state.Root.PlayerID || row.ConnectionID == uuid.Nil || row.ConnectionGeneration < 1 || row.State != "active" || row.Revision < 1 || !row.ConnectedAt.Valid || row.DisconnectedAt.Valid {
			return fmt.Errorf("invalid active participant connection lease: %w", domain.ErrInternal)
		}
	}
	return nil
}

func (repository *ParticipantConnectionPostgres) countParticipantConnectionLeases(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
) (int, error) {
	count, err := q.CountParticipantConnectionLeases(ctx, sqlc.CountParticipantConnectionLeasesParams{
		TournamentID: tournamentID, RosterID: rosterID, ParticipantID: participantID,
	})
	if err != nil {
		return 0, fmt.Errorf("count participant connection leases: %w", err)
	}
	if count < 0 || count > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("invalid participant connection lease count: %w", domain.ErrInternal)
	}
	return int(count), nil
}

func (repository *ParticipantConnectionPostgres) lockParticipantConnectionCurrentSubscriber(
	ctx context.Context,
	q *sqlc.Queries,
	command connection.CloseConnectionCommand,
) (bool, error) {
	rows, err := q.LockParticipantConnectionCurrentSubscriber(ctx, sqlc.LockParticipantConnectionCurrentSubscriberParams{
		TournamentID:         command.TournamentID,
		PlayerID:             nullableConnectionUUID(command.PlayerID),
		ConnectionID:         command.ConnectionID,
		ConnectionGeneration: command.ConnectionGeneration,
	})
	if err != nil {
		return false, fmt.Errorf("lock current participant connection subscriber: %w", err)
	}
	if len(rows) > 1 {
		return false, fmt.Errorf("multiple current participant connection subscribers: %w", domain.ErrConflict)
	}
	if len(rows) == 0 {
		return false, nil
	}
	if rows[0] == uuid.Nil {
		return false, fmt.Errorf("current participant connection subscriber identity is invalid: %w", domain.ErrInternal)
	}
	return true, nil
}

func participantConnectionActionForSubscriberFence(
	currentSubscriber bool,
	action connection.ResolvedAction,
) connection.ResolvedAction {
	if !currentSubscriber {
		return connection.ResolvedAction{Kind: connection.ActionNone}
	}
	return action
}

//nolint:gocyclo // The action selector is a fail-closed policy boundary over mutually exclusive game states.
func (repository *ParticipantConnectionPostgres) resolveParticipantConnectionAction(
	ctx context.Context,
	q *sqlc.Queries,
	state participantConnectionState,
	op participantConnectionOperation,
) (connection.ResolvedAction, participantConnectionBinding, error) {
	if domain.TournamentState(state.Root.TournamentState) == domain.TournamentStateGolden || domain.TournamentState(state.Root.TournamentState).IsTerminal() {
		return connection.ResolvedAction{Kind: connection.ActionNone}, participantConnectionBinding{}, nil
	}
	waveID := state.Wave.WaveID
	common := func() connection.ResolvedAction {
		return connection.ResolvedAction{Kind: connection.ActionNone}
	}

	operatorRows, err := q.LockParticipantConnectionOperatorPause(ctx, sqlc.LockParticipantConnectionOperatorPauseParams{
		ParticipantID: state.Root.ParticipantID,
		TournamentID:  state.Root.TournamentID,
		RosterID:      state.Root.RosterID,
		WaveID:        nullableConnectionUUID(waveID),
	})
	if err != nil {
		return common(), participantConnectionBinding{}, fmt.Errorf("lock participant connection operator pause: %w", err)
	}
	if len(operatorRows) > 1 {
		return common(), participantConnectionBinding{}, fmt.Errorf("multiple operator pauses match participant connection: %w", domain.ErrConflict)
	}
	if len(operatorRows) == 1 {
		row := operatorRows[0]
		action, binding, err := participantConnectionOperatorPauseAction(row, state, op)
		if err != nil {
			return common(), participantConnectionBinding{}, err
		}
		return action, binding, nil
	}

	activeRows, err := q.LockParticipantConnectionActiveGame(ctx, sqlc.LockParticipantConnectionActiveGameParams{
		ParticipantID: state.Root.ParticipantID,
		WaveID:        waveID,
		TournamentID:  state.Root.TournamentID,
		RosterID:      state.Root.RosterID,
	})
	if err != nil {
		return common(), participantConnectionBinding{}, fmt.Errorf("lock participant connection active game: %w", err)
	}
	if len(activeRows) > 1 {
		return common(), participantConnectionBinding{}, fmt.Errorf("multiple active games match participant connection: %w", domain.ErrConflict)
	}
	reconnectRows, err := q.LockParticipantConnectionReconnect(ctx, sqlc.LockParticipantConnectionReconnectParams{
		ParticipantID: state.Root.ParticipantID,
		WaveID:        waveID,
		TournamentID:  state.Root.TournamentID,
		RosterID:      state.Root.RosterID,
	})
	if err != nil {
		return common(), participantConnectionBinding{}, fmt.Errorf("lock participant connection reconnect interval: %w", err)
	}
	if len(reconnectRows) > 1 || len(activeRows) > 0 && len(reconnectRows) > 0 {
		return common(), participantConnectionBinding{}, fmt.Errorf("ambiguous participant connection game action: %w", domain.ErrConflict)
	}

	if len(activeRows) == 1 {
		action, binding, err := participantConnectionActiveGameAction(activeRows[0], state, op)
		if err != nil {
			return common(), participantConnectionBinding{}, err
		}
		return action, binding, nil
	}
	if op == connectionOperationConnect && len(reconnectRows) == 1 {
		row := reconnectRows[0]
		binding, err := reconnectBinding(row, state)
		if err != nil {
			return common(), participantConnectionBinding{}, err
		}
		deadline := row.IntervalDeadline.Time.UTC()
		if !row.IntervalDeadline.Valid || !domain.IsValidServerTime(deadline) {
			return common(), participantConnectionBinding{}, fmt.Errorf("invalid reconnect interval deadline: %w", domain.ErrInternal)
		}
		return connection.ResolvedAction{
			Kind: connection.ActionGameReconnect,
			Reconnect: &reconnectusecase.ReconnectCommand{
				Scope: state.Authority.Scope, ParticipantID: state.Root.ParticipantID, IntervalID: row.IntervalID,
			},
			Deadline: deadline,
		}, binding, nil
	}

	if (op == connectionOperationDisconnect || op == connectionOperationRecovery) && participantConnectionReadinessActionAvailable(state) {
		return connection.ResolvedAction{
			Kind: connection.ActionClearReadiness,
			Readiness: &readiness.DisconnectReadinessCommand{
				Scope:                    readiness.ReadinessScope{WaveID: state.Wave.WaveID, WindowID: state.Window.ReadyWindowID},
				ParticipantID:            state.Root.ParticipantID,
				ExpectedWaveRevisionID:   domain.WaveRevisionID(state.Wave.WaveRevisionID),
				ExpectedWindowRevisionID: domain.ReadyWindowRevisionID(state.Window.ReadyWindowRevisionID),
			},
		}, participantConnectionBinding{}, nil
	}
	return common(), participantConnectionBinding{}, nil
}

//nolint:gocyclo // The immutable operator-pause binding is validated as one cross-field identity.
func participantConnectionOperatorPauseBinding(
	row sqlc.LockParticipantConnectionOperatorPauseRow,
	state participantConnectionState,
) (participantConnectionBinding, error) {
	if row.PauseID == uuid.Nil || row.WaveID != state.Wave.WaveID || row.TournamentID != state.Root.TournamentID || row.RosterID != state.Root.RosterID || row.AssignmentID == uuid.Nil || row.GameAttemptID == uuid.Nil || row.SeriesID == uuid.Nil || row.PresenceID == uuid.Nil || row.PresenceSeriesID != row.SeriesID || row.ParticipantID != state.Root.ParticipantID || row.PauseRevision < 1 || row.PresenceEpoch < 1 || row.PresenceRevision < 1 || (row.PresenceState != string(pausedomain.PresenceStateConnected) && row.PresenceState != string(pausedomain.PresenceStateDisconnected)) {
		return participantConnectionBinding{}, fmt.Errorf("invalid operator pause action row: %w", domain.ErrInternal)
	}
	return participantConnectionBinding{AssignmentID: row.AssignmentID, SeriesID: row.SeriesID, GameAttemptID: row.GameAttemptID}, nil
}

func participantConnectionOperatorPauseAction(
	row sqlc.LockParticipantConnectionOperatorPauseRow,
	state participantConnectionState,
	op participantConnectionOperation,
) (connection.ResolvedAction, participantConnectionBinding, error) {
	binding, err := participantConnectionOperatorPauseBinding(row, state)
	if err != nil {
		return connection.ResolvedAction{Kind: connection.ActionNone}, participantConnectionBinding{}, err
	}
	target := pausedomain.PresenceStateDisconnected
	if op == connectionOperationConnect {
		target = pausedomain.PresenceStateConnected
	}
	if row.PresenceState == string(target) {
		// The lease still records the current game binding even when this
		// operation has no presence mutation to apply.
		return connection.ResolvedAction{Kind: connection.ActionNone}, binding, nil
	}
	graphRevision, err := participantConnectionPauseGraphRevision(row)
	if err != nil {
		return connection.ResolvedAction{Kind: connection.ActionNone}, participantConnectionBinding{}, err
	}
	return connection.ResolvedAction{
		Kind: connection.ActionPausedPresence,
		PausedPresence: &pauseusecase.PausedPresenceCommand{
			Scope:                    state.Authority.Scope,
			PauseID:                  row.PauseID,
			ParticipantID:            state.Root.ParticipantID,
			ExpectedGraphRevision:    graphRevision,
			ExpectedPauseRevision:    row.PauseRevision,
			ExpectedPresenceEpoch:    row.PresenceEpoch,
			ExpectedPresenceRevision: row.PresenceRevision,
			NextState:                target,
		},
	}, binding, nil
}

func participantConnectionPauseGraphRevision(row sqlc.LockParticipantConnectionOperatorPauseRow) (int64, error) {
	var document struct {
		Version int                             `json:"version"`
		Pause   *pauseusecase.NormalPauseRecord `json:"normal_pause"`
	}
	if err := json.Unmarshal(row.PauseDocument, &document); err != nil || document.Version != 1 || document.Pause == nil {
		return 0, fmt.Errorf("missing or invalid normal pause receipt: %w", domain.ErrInternal)
	}
	pause := document.Pause
	if !participantConnectionPauseReceiptMatches(*pause, row) {
		return 0, fmt.Errorf("normal pause receipt identity or graph revision mismatch: %w", domain.ErrInternal)
	}
	return pause.Graph.Revision, nil
}

func participantConnectionPauseReceiptMatches(pause pauseusecase.NormalPauseRecord, row sqlc.LockParticipantConnectionOperatorPauseRow) bool {
	scopeMatches := func(scope pausedomain.GraphScope) bool {
		return scope.TournamentID == row.TournamentID && scope.RosterID == row.RosterID && scope.WaveID == row.WaveID
	}
	return pause.PauseID == row.PauseID && pause.ScopeKind == pausedomain.ScopeWave && pause.ScopeID == row.WaveID &&
		pause.Reason == pauseusecase.PauseReasonOperator && pause.State == pauseusecase.PauseStateActive &&
		pause.Revision == row.PauseRevision && scopeMatches(pause.Scope) && scopeMatches(pause.Graph.Scope) &&
		pause.Graph.ActivePauseID == row.PauseID && pause.Graph.Revision >= 1
}

func participantConnectionActiveGameAction(
	row sqlc.LockParticipantConnectionActiveGameRow,
	state participantConnectionState,
	op participantConnectionOperation,
) (connection.ResolvedAction, participantConnectionBinding, error) {
	binding, err := activeGameBinding(row, state)
	if err != nil {
		return connection.ResolvedAction{Kind: connection.ActionNone}, participantConnectionBinding{}, err
	}
	if op == connectionOperationConnect || op == connectionOperationRecovery {
		// A duplicate tab has no game mutation, but its durable lease must
		// retain the binding for a later exact close fence.  Recovery also
		// leaves active-game technical replay to execution recovery.
		return connection.ResolvedAction{Kind: connection.ActionNone}, binding, nil
	}
	return connection.ResolvedAction{
		Kind: connection.ActionGameDisconnect,
		Disconnect: &reconnectusecase.DisconnectCommand{
			Scope: state.Authority.Scope, ParticipantID: state.Root.ParticipantID,
		},
	}, binding, nil
}

func participantConnectionReadinessActionAvailable(state participantConnectionState) bool {
	return state.Window != nil && state.Readiness != nil && state.Readiness.Ready && !state.Wave.WaveStartedAt.Valid &&
		(state.Wave.WaveState == string(domain.WaveStateReadyWindowOpen) || state.Wave.WaveState == string(domain.WaveStateReady)) &&
		state.Window.ReadyWindowState == string(domain.ReadyWindowStateOpen) && state.Window.ReadyWindowID != uuid.Nil && state.Window.ReadyWindowRevisionID != uuid.Nil
}

func activeGameBinding(row sqlc.LockParticipantConnectionActiveGameRow, state participantConnectionState) (participantConnectionBinding, error) {
	if row.AssignmentID == uuid.Nil || row.GameAttemptID == uuid.Nil || row.SeriesID == uuid.Nil || row.PresenceID == uuid.Nil || row.SeriesState != string(domain.SeriesStateActive) || row.GameState != string(domain.GameStateActive) || row.TaskKind != "normal" || row.PresenceState != string(pausedomain.PresenceStateConnected) || row.PresenceEpoch < 1 || row.PresenceRevision < 1 {
		return participantConnectionBinding{}, fmt.Errorf("invalid active game action row for %s: %w", state.Root.ParticipantID, domain.ErrInternal)
	}
	return participantConnectionBinding{AssignmentID: row.AssignmentID, SeriesID: row.SeriesID, GameAttemptID: row.GameAttemptID}, nil
}

func reconnectBinding(row sqlc.LockParticipantConnectionReconnectRow, state participantConnectionState) (participantConnectionBinding, error) {
	if row.AssignmentID == uuid.Nil || row.GameAttemptID == uuid.Nil || row.SeriesID == uuid.Nil || row.IntervalID == uuid.Nil || row.PresenceID == uuid.Nil || row.SeriesState != string(domain.SeriesStateActive) || row.GameState != string(domain.GameStatePaused) || row.TaskKind != "normal" || row.PresenceState != string(pausedomain.PresenceStateDisconnected) || row.PresenceEpoch < 1 || row.PresenceRevision < 1 || row.IntervalPresenceEpoch != row.PresenceEpoch {
		return participantConnectionBinding{}, fmt.Errorf("invalid reconnect action row for %s: %w", state.Root.ParticipantID, domain.ErrInternal)
	}
	return participantConnectionBinding{AssignmentID: row.AssignmentID, SeriesID: row.SeriesID, GameAttemptID: row.GameAttemptID}, nil
}

//nolint:gocyclo // Durable lease decoding validates the complete optional binding as one unit.
func mapParticipantConnectionLease(row sqlc.ParticipantConnectionLease) (connection.DurableLease, error) {
	if row.ID == uuid.Nil || row.TournamentID == uuid.Nil || row.RosterID == uuid.Nil || row.ParticipantID == uuid.Nil || row.PlayerID == uuid.Nil || row.ConnectionID == uuid.Nil || row.ConnectionGeneration < 1 || row.Revision < 1 || !row.ConnectedAt.Valid || !domain.IsValidServerTime(row.ConnectedAt.Time.UTC()) || !row.UpdatedAt.Valid || !domain.IsValidServerTime(row.UpdatedAt.Time.UTC()) || row.UpdatedAt.Time.Before(row.ConnectedAt.Time) {
		return connection.DurableLease{}, fmt.Errorf("invalid participant connection lease row: %w", domain.ErrInternal)
	}
	switch row.State {
	case "active":
		if row.DisconnectedAt.Valid {
			return connection.DurableLease{}, fmt.Errorf("active participant connection has disconnected timestamp: %w", domain.ErrInternal)
		}
	case "disconnected":
		if !row.DisconnectedAt.Valid || !domain.IsValidServerTime(row.DisconnectedAt.Time.UTC()) || row.DisconnectedAt.Time.Before(row.ConnectedAt.Time) || row.UpdatedAt.Time.Before(row.DisconnectedAt.Time) {
			return connection.DurableLease{}, fmt.Errorf("disconnected participant connection has invalid timestamps: %w", domain.ErrInternal)
		}
	default:
		return connection.DurableLease{}, fmt.Errorf("unknown participant connection lease state %q: %w", row.State, domain.ErrInternal)
	}
	if anyConnectionBindingNull(row) && anyConnectionBindingValid(row) {
		return connection.DurableLease{}, fmt.Errorf("partial participant connection binding: %w", domain.ErrInternal)
	}
	if _, err := participantConnectionLeaseAuthorityStamp(row); err != nil {
		return connection.DurableLease{}, err
	}
	return connection.DurableLease{
		ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID,
		ParticipantID: row.ParticipantID, PlayerID: row.PlayerID,
		ConnectionID: row.ConnectionID, ConnectionGeneration: row.ConnectionGeneration,
	}, nil
}

func mapParticipantConnectionRecoveryCandidate(row sqlc.ParticipantConnectionLease) (connection.OrphanedConnectionLease, error) {
	lease, err := mapParticipantConnectionLease(row)
	if err != nil {
		return connection.OrphanedConnectionLease{}, err
	}
	if row.State != "active" || row.Revision < 1 {
		return connection.OrphanedConnectionLease{}, fmt.Errorf("invalid participant connection recovery candidate: %w", domain.ErrInternal)
	}
	authority, err := participantConnectionLeaseAuthorityStamp(row)
	if err != nil || authority.Validate() != nil {
		return connection.OrphanedConnectionLease{}, fmt.Errorf("invalid participant connection recovery authority stamp: %w", domain.ErrInternal)
	}
	return connection.OrphanedConnectionLease{Lease: lease, Revision: row.Revision, Authority: authority}, nil
}

func participantConnectionLeaseAuthorityStamp(row sqlc.ParticipantConnectionLease) (authoritydomain.Identity, error) {
	if !row.AuthorityHolderID.Valid && !row.AuthorityLeaseID.Valid && row.AuthorityEpoch == nil {
		// Ownerless rows are legacy evidence.  They are valid to read but are
		// intentionally not eligible for orphan recovery.
		return authoritydomain.Identity{}, nil
	}
	if !row.AuthorityHolderID.Valid || !row.AuthorityLeaseID.Valid || row.AuthorityEpoch == nil || *row.AuthorityEpoch < 1 {
		return authoritydomain.Identity{}, fmt.Errorf("partial participant connection authority stamp: %w", domain.ErrInternal)
	}
	authority := authoritydomain.Identity{
		TournamentID: row.TournamentID,
		HolderID:     row.AuthorityHolderID.UUID,
		LeaseID:      row.AuthorityLeaseID.UUID,
		Epoch:        *row.AuthorityEpoch,
		ProcessKind:  authoritydomain.ProcessAuthority,
	}
	if authority.Validate() != nil {
		return authoritydomain.Identity{}, fmt.Errorf("invalid participant connection authority stamp: %w", domain.ErrInternal)
	}
	return authority, nil
}

func participantConnectionRecoveryLeaseMatchesCandidate(
	lease connection.DurableLease,
	candidate connection.OrphanedConnectionLease,
) bool {
	return lease.ID == candidate.Lease.ID && lease.TournamentID == candidate.Lease.TournamentID &&
		lease.RosterID == candidate.Lease.RosterID && lease.ParticipantID == candidate.Lease.ParticipantID &&
		lease.PlayerID == candidate.Lease.PlayerID && lease.ConnectionID == candidate.Lease.ConnectionID &&
		lease.ConnectionGeneration == candidate.Lease.ConnectionGeneration
}

func validateOpenedParticipantConnectionAuthority(
	row sqlc.ParticipantConnectionLease,
	want authoritydomain.Identity,
	opened bool,
) error {
	if !opened {
		return nil
	}
	stamp, err := participantConnectionLeaseAuthorityStamp(row)
	if err != nil || stamp != want {
		return fmt.Errorf("participant connection lease authority stamp mismatch: %w", domain.ErrConflict)
	}
	return nil
}

func validateRecoveredParticipantConnectionLease(
	row sqlc.ParticipantConnectionLease,
	candidate connection.OrphanedConnectionLease,
) (connection.DurableLease, error) {
	lease, err := mapParticipantConnectionLease(row)
	if err != nil {
		return connection.DurableLease{}, err
	}
	stamp, stampErr := participantConnectionLeaseAuthorityStamp(row)
	if stampErr != nil || stamp != candidate.Authority ||
		!participantConnectionRecoveryLeaseMatchesCandidate(lease, candidate) {
		return connection.DurableLease{}, fmt.Errorf("recovered participant connection lease identity mismatch: %w", domain.ErrConflict)
	}
	if row.State != "disconnected" || !row.DisconnectedAt.Valid || row.Revision != candidate.Revision+1 {
		return connection.DurableLease{}, fmt.Errorf("recovered participant connection lease did not persist CAS transition: %w", domain.ErrInternal)
	}
	return lease, nil
}

func validateParticipantConnectionLease(
	lease connection.DurableLease,
	tournamentID, rosterID, participantID, playerID, connectionID uuid.UUID,
	connectionGeneration int64,
) error {
	if lease.ID == uuid.Nil || lease.TournamentID != tournamentID || lease.RosterID != rosterID || lease.ParticipantID != participantID || lease.PlayerID != playerID || lease.ConnectionID != connectionID || lease.ConnectionGeneration != connectionGeneration {
		return fmt.Errorf("participant connection lease identity mismatch: %w", domain.ErrConflict)
	}
	return nil
}

func sameParticipantConnectionBinding(row sqlc.ParticipantConnectionLease, binding participantConnectionBinding) bool {
	return nullableConnectionEqual(row.AssignmentID, binding.AssignmentID) && nullableConnectionEqual(row.SeriesID, binding.SeriesID) && nullableConnectionEqual(row.GameAttemptID, binding.GameAttemptID)
}

func participantConnectionLeaseBinding(row sqlc.ParticipantConnectionLease) (participantConnectionBinding, error) {
	if anyConnectionBindingNull(row) && anyConnectionBindingValid(row) {
		return participantConnectionBinding{}, fmt.Errorf("partial participant connection lease binding: %w", domain.ErrInternal)
	}
	if !anyConnectionBindingValid(row) {
		return participantConnectionBinding{}, nil
	}
	return participantConnectionBinding{
		AssignmentID:  row.AssignmentID.UUID,
		SeriesID:      row.SeriesID.UUID,
		GameAttemptID: row.GameAttemptID.UUID,
	}, nil
}

func participantConnectionActionBindingMatchesLease(
	row sqlc.ParticipantConnectionLease,
	action connection.ActionKind,
	want participantConnectionBinding,
) (bool, error) {
	if action == connection.ActionClearReadiness {
		got, err := participantConnectionLeaseBinding(row)
		if err != nil {
			return false, err
		}
		return got == (participantConnectionBinding{}), nil
	}
	if action != connection.ActionGameDisconnect && action != connection.ActionGameReconnect && action != connection.ActionPausedPresence {
		return true, nil
	}
	got, err := participantConnectionLeaseBinding(row)
	if err != nil {
		return false, err
	}
	return got == want, nil
}

func anyConnectionBindingNull(row sqlc.ParticipantConnectionLease) bool {
	return !row.AssignmentID.Valid || !row.SeriesID.Valid || !row.GameAttemptID.Valid
}

func anyConnectionBindingValid(row sqlc.ParticipantConnectionLease) bool {
	return row.AssignmentID.Valid || row.SeriesID.Valid || row.GameAttemptID.Valid
}

func nullableConnectionEqual(value uuid.NullUUID, want uuid.UUID) bool {
	if want == uuid.Nil {
		return !value.Valid
	}
	return value.Valid && value.UUID == want
}

func nullableConnectionUUID(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func authorityEpochPointer(value int64) *int64 {
	return &value
}

func validParticipantConnectionAuthority(value connection.ParticipantConnectionAuthority) bool {
	return value.TournamentID != uuid.Nil && value.RosterID != uuid.Nil && value.ParticipantID != uuid.Nil &&
		value.PlayerID != uuid.Nil && value.Scope.Validate() == nil && value.Authority.Validate() == nil &&
		value.Scope.Authority == value.Authority
}

func validateOrphanedConnectionCandidate(candidate connection.OrphanedConnectionLease) error {
	lease := candidate.Lease
	if lease.ID == uuid.Nil || lease.TournamentID == uuid.Nil || lease.RosterID == uuid.Nil ||
		lease.ParticipantID == uuid.Nil || lease.PlayerID == uuid.Nil || lease.ConnectionID == uuid.Nil ||
		lease.ConnectionGeneration < 1 || candidate.Revision < 1 || candidate.Authority.TournamentID != lease.TournamentID {
		return domain.ErrValidation
	}
	return nil
}

func validParticipantConnectionRepository(ctx context.Context, repository *ParticipantConnectionPostgres) bool {
	return ctx != nil && repository != nil && repository.tx != nil && repository.authority != nil
}

func validOpenConnectionCommand(command connection.OpenConnectionCommand) bool {
	return command.TournamentID != uuid.Nil && command.RosterID != uuid.Nil && command.ParticipantID != uuid.Nil && command.PlayerID != uuid.Nil && command.ConnectionID != uuid.Nil && command.ConnectionGeneration >= 1
}

func validCloseConnectionCommand(command connection.CloseConnectionCommand) bool {
	return command.TournamentID != uuid.Nil && command.RosterID != uuid.Nil && command.ParticipantID != uuid.Nil && command.PlayerID != uuid.Nil && command.ConnectionID != uuid.Nil && command.ConnectionGeneration >= 1
}
