package authority

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

const (
	defaultControllerLeaseDuration = 30 * time.Second
	defaultControllerRenewBefore   = 10 * time.Second
)

var ErrNotOwner = errors.New("execution authority is owned by another replica")

// Controller is the sole service-owned source of execution authority. Callers
// receive an internal identity only; HTTP input never supplies holder, lease or
// epoch values.
type Controller struct {
	repository  Repository
	claims      *UseCase
	timeSource  TimeSource
	holderID    uuid.UUID
	renewBefore time.Duration

	statesMu sync.Mutex
	states   map[uuid.UUID]*controllerLeaseState
}

type controllerLeaseState struct {
	mu    sync.Mutex
	lease *authoritydomain.Lease
}

type ControllerConfig struct {
	HolderID      uuid.UUID
	LeaseDuration time.Duration
	RenewBefore   time.Duration
}

func NewController(
	repository Repository,
	timeSource TimeSource,
	config ControllerConfig,
) (*Controller, error) {
	config = controllerConfigWithDefaults(config)
	if repository == nil || timeSource == nil || config.HolderID == uuid.Nil ||
		config.LeaseDuration <= 0 || config.RenewBefore <= 0 ||
		config.RenewBefore >= config.LeaseDuration {
		return nil, domain.ErrValidation
	}
	return &Controller{
		repository:  repository,
		claims:      NewWithTimeSource(repository, timeSource, config.LeaseDuration),
		timeSource:  timeSource,
		holderID:    config.HolderID,
		renewBefore: config.RenewBefore,
		states:      make(map[uuid.UUID]*controllerLeaseState),
	}, nil
}

// AuthorityFor returns a currently live identity for this service. A replica
// never replaces a still-live foreign lease; it fails closed with ErrNotOwner.
//
//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (controller *Controller) AuthorityFor(
	ctx context.Context,
	tournamentID uuid.UUID,
) (authoritydomain.Identity, error) {
	if ctx == nil || controller == nil || controller.repository == nil ||
		controller.claims == nil || controller.timeSource == nil || tournamentID == uuid.Nil {
		return authoritydomain.Identity{}, domain.ErrValidation
	}
	state := controller.stateFor(tournamentID)
	state.mu.Lock()
	defer state.mu.Unlock()

	now, err := controller.timeSource.AuthorityTime(ctx)
	if err != nil {
		return authoritydomain.Identity{}, err
	}
	now = now.Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return authoritydomain.Identity{}, domain.ErrValidation
	}
	if state.lease != nil && controller.canServe(*state.lease, now) {
		return state.lease.Identity(), nil
	}

	current, err := controller.repository.LoadAuthority(ctx, tournamentID)
	if err != nil {
		return authoritydomain.Identity{}, err
	}
	if current != nil && current.Validate() != nil {
		return authoritydomain.Identity{}, domain.ErrInternal
	}
	command := controller.claimCommand(tournamentID, current, state.lease, now)
	lease, _, err := controller.claims.Claim(ctx, command)
	if errors.Is(err, authoritydomain.ErrActive) {
		return authoritydomain.Identity{}, ErrNotOwner
	}
	if err != nil {
		return authoritydomain.Identity{}, err
	}
	proofAt, err := controller.timeSource.AuthorityTime(ctx)
	if err != nil {
		return authoritydomain.Identity{}, err
	}
	proofAt = proofAt.Round(0).UTC()
	if lease == nil || !domain.IsValidServerTime(proofAt) || !lease.Proves(lease.Identity(), proofAt) {
		return authoritydomain.Identity{}, domain.ErrConflict
	}
	stored := lease.Clone()
	state.lease = &stored
	return stored.Identity(), nil
}

// RecoveryAuthorityFor lets the lifecycle runner skip a live foreign owner
// without weakening normal HTTP WaveStart fail-closed behavior.
func (controller *Controller) RecoveryAuthorityFor(
	ctx context.Context,
	tournamentID uuid.UUID,
) (authoritydomain.Identity, bool, error) {
	identity, err := controller.AuthorityFor(ctx, tournamentID)
	if errors.Is(err, ErrNotOwner) {
		return authoritydomain.Identity{}, false, nil
	}
	if err != nil {
		return authoritydomain.Identity{}, false, err
	}
	return identity, true, nil
}

func (controller *Controller) stateFor(tournamentID uuid.UUID) *controllerLeaseState {
	controller.statesMu.Lock()
	defer controller.statesMu.Unlock()
	state := controller.states[tournamentID]
	if state == nil {
		state = &controllerLeaseState{}
		controller.states[tournamentID] = state
	}
	return state
}

func (controller *Controller) canServe(lease authoritydomain.Lease, now time.Time) bool {
	if !lease.Proves(lease.Identity(), now) || lease.HolderID != controller.holderID {
		return false
	}
	return now.Add(controller.renewBefore).Before(lease.ExpiresAt)
}

func (controller *Controller) claimCommand(
	tournamentID uuid.UUID,
	current *authoritydomain.Lease,
	local *authoritydomain.Lease,
	now time.Time,
) ClaimCommand {
	leaseID := uuid.New()
	if current != nil && local != nil && current.HolderID == controller.holderID &&
		current.LeaseID == local.LeaseID && current.Epoch == local.Epoch && now.Before(current.ExpiresAt) {
		leaseID = local.LeaseID
	}
	return ClaimCommand{
		TournamentID: tournamentID,
		HolderID:     controller.holderID,
		LeaseID:      leaseID,
		CommandID:    uuid.New(),
		ProcessKind:  authoritydomain.ProcessAuthority,
		Expected:     authoritydomain.CloneStamp(currentStamp(current)),
	}
}

func currentStamp(lease *authoritydomain.Lease) *authoritydomain.Stamp {
	if lease == nil {
		return nil
	}
	return lease.Stamp()
}

func controllerConfigWithDefaults(config ControllerConfig) ControllerConfig {
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultControllerLeaseDuration
	}
	if config.RenewBefore == 0 {
		config.RenewBefore = defaultControllerRenewBefore
	}
	return config
}
