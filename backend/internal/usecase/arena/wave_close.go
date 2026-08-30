package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const oldWaveCloseAttempts = 2

var (
	ErrInvalidOldWaveClosure  = errors.New("invalid old arena Wave closure")
	ErrOldWaveClosureBlocked  = errors.New("old arena Wave has unresolved children")
	ErrOldWaveClosureConflict = errors.New("old arena Wave closure conflict")
	ErrOldWaveClosureReuse    = errors.New("old arena Wave closure command was reused")
)

type OldWaveScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
}

type OldWaveChild struct {
	SeriesID uuid.UUID
	SlotID   uuid.UUID
	GameID   uuid.UUID
	State    domain.ArenaGameState
	RouteID  uuid.UUID
}

type OldWaveCloseCommand struct {
	Scope                  OldWaveScope
	CommandID              uuid.UUID
	ExpectedWaveRevisionID domain.ArenaWaveRevisionID
	ClosedWaveRevisionID   domain.ArenaWaveRevisionID
}

type OldWaveCloseAuthority struct {
	Scope    OldWaveScope
	Revision int64
	Wave     domain.ArenaWave
	Children []OldWaveChild
	Current  *OldWaveClosure
}

type OldWaveClosure struct {
	Scope                     OldWaveScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	PreviousWaveRevisionID    domain.ArenaWaveRevisionID
	Wave                      domain.ArenaWave
	Children                  []OldWaveChild
	ClosedAt                  time.Time
}

// OldWaveCloseRepository owns one compare-and-set that revalidates every child
// as terminal or durably routed and completes the old Wave. Reserve capacity is
// deliberately absent from this transaction contract.
type OldWaveCloseRepository interface {
	LoadOldWaveCloseAuthority(
		ctx context.Context,
		scope OldWaveScope,
	) (OldWaveCloseAuthority, error)
	CommitOldWaveClosure(
		ctx context.Context,
		closure OldWaveClosure,
	) (*OldWaveClosure, bool, error)
}

type OldWaveCloseUseCase struct {
	repository OldWaveCloseRepository
	clock      Clock
}

func NewOldWaveCloseUseCase(
	repository OldWaveCloseRepository,
	clock Clock,
) *OldWaveCloseUseCase {
	return &OldWaveCloseUseCase{repository: repository, clock: clock}
}

func (u *OldWaveCloseUseCase) Close(
	ctx context.Context,
	command OldWaveCloseCommand,
) (*OldWaveClosure, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateOldWaveCloseCommand(command); err != nil {
		return nil, false, err
	}
	closedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(closedAt) {
		return nil, false, domain.ErrValidation
	}
	for range oldWaveCloseAttempts {
		closure, changed, retry, err := u.closeAttempt(ctx, command, closedAt)
		if retry {
			continue
		}
		return closure, changed, err
	}
	return nil, false, ErrOldWaveClosureConflict
}

func (u *OldWaveCloseUseCase) closeAttempt(
	ctx context.Context,
	command OldWaveCloseCommand,
	closedAt time.Time,
) (*OldWaveClosure, bool, bool, error) {
	authority, err := u.repository.LoadOldWaveCloseAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("OldWaveCloseUseCase - load authority: %w", err)
	}
	if err := validateOldWaveCloseAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, oldWaveClosureError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileOldWaveClosure(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	closure, err := buildOldWaveClosure(command, authority, closedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitOldWaveClosure(ctx, closure)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("OldWaveCloseUseCase - commit closure: %w", err)
	}
	if !validCommittedOldWaveClosure(committed, closure, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneOldWaveClosure(*committed)
	return &result, changed, false, nil
}

func (c OldWaveClosure) Validate() error {
	if !c.Scope.IsValid() || c.CommandID == uuid.Nil || c.ExpectedAuthorityRevision < 1 ||
		c.PreviousWaveRevisionID.IsZero() || !validArenaServerTime(c.ClosedAt) ||
		c.Wave.Validate() != nil || c.Wave.ID != c.Scope.WaveID ||
		c.Wave.TournamentID != c.Scope.TournamentID ||
		c.Wave.State != domain.ArenaWaveStateCompleted || c.Wave.RevisionID.IsZero() ||
		c.Wave.RevisionID == c.PreviousWaveRevisionID || c.Wave.StartedAt == nil ||
		c.ClosedAt.Before(*c.Wave.StartedAt) {
		return oldWaveClosureError("invalid closure identity or completed Wave")
	}
	return validateOldWaveChildren(c.Children)
}

func (s OldWaveScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.WaveID != uuid.Nil
}

func validateOldWaveCloseCommand(command OldWaveCloseCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil ||
		command.ExpectedWaveRevisionID.IsZero() || command.ClosedWaveRevisionID.IsZero() ||
		command.ExpectedWaveRevisionID == command.ClosedWaveRevisionID {
		return oldWaveClosureError("invalid command identity or revision")
	}
	return nil
}

func validateOldWaveCloseAuthority(authority OldWaveCloseAuthority) error {
	if !authority.Scope.IsValid() || authority.Revision < 1 || authority.Wave.Validate() != nil ||
		authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID {
		return oldWaveClosureError("invalid authority identity or Wave")
	}
	if authority.Current == nil && authority.Wave.State != domain.ArenaWaveStateActive {
		return oldWaveClosureError("old Wave is not active")
	}
	if authority.Current != nil {
		if authority.Wave.State != domain.ArenaWaveStateCompleted ||
			authority.Current.Validate() != nil || authority.Current.Scope != authority.Scope ||
			!arenaWavesEqual(authority.Current.Wave, authority.Wave) {
			return oldWaveClosureError("invalid current closure")
		}
		return nil
	}
	return validateOldWaveChildren(authority.Children)
}

func validateOldWaveChildren(children []OldWaveChild) error {
	if len(children) == 0 {
		return ErrOldWaveClosureBlocked
	}
	gameIDs := make(map[uuid.UUID]struct{}, len(children))
	for _, child := range children {
		if child.SeriesID == uuid.Nil || child.SlotID == uuid.Nil || child.GameID == uuid.Nil ||
			!child.State.IsValid() || !oldWaveChildResolved(child) {
			return ErrOldWaveClosureBlocked
		}
		if _, duplicate := gameIDs[child.GameID]; duplicate {
			return oldWaveClosureError("duplicate child Game")
		}
		gameIDs[child.GameID] = struct{}{}
	}
	return nil
}

func oldWaveChildResolved(child OldWaveChild) bool {
	if child.State == domain.ArenaGameStateVoid {
		return child.RouteID != uuid.Nil
	}
	if !child.State.IsTerminal() {
		return false
	}
	return child.RouteID == uuid.Nil
}

func buildOldWaveClosure(
	command OldWaveCloseCommand,
	authority OldWaveCloseAuthority,
	closedAt time.Time,
) (OldWaveClosure, error) {
	if authority.Wave.RevisionID != command.ExpectedWaveRevisionID {
		return OldWaveClosure{}, ErrOldWaveClosureConflict
	}
	if err := validateOldWaveChildren(authority.Children); err != nil {
		return OldWaveClosure{}, err
	}
	wave := cloneArenaWaveExecution(authority.Wave)
	wave.State = domain.ArenaWaveStateCompleted
	wave.RevisionID = command.ClosedWaveRevisionID
	closure := OldWaveClosure{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		PreviousWaveRevisionID:    command.ExpectedWaveRevisionID,
		Wave:                      wave, Children: append([]OldWaveChild(nil), authority.Children...),
		ClosedAt: closedAt,
	}
	if err := closure.Validate(); err != nil {
		return OldWaveClosure{}, err
	}
	return cloneOldWaveClosure(closure), nil
}

func reconcileOldWaveClosure(
	closure OldWaveClosure,
	command OldWaveCloseCommand,
) (*OldWaveClosure, error) {
	if closure.Validate() != nil || closure.Scope != command.Scope ||
		closure.CommandID != command.CommandID ||
		closure.PreviousWaveRevisionID != command.ExpectedWaveRevisionID ||
		closure.Wave.RevisionID != command.ClosedWaveRevisionID {
		return nil, ErrOldWaveClosureReuse
	}
	clone := cloneOldWaveClosure(closure)
	return &clone, nil
}

func validCommittedOldWaveClosure(
	committed *OldWaveClosure,
	proposed OldWaveClosure,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return oldWaveClosuresEqual(*committed, proposed)
}

func oldWaveClosuresEqual(first, second OldWaveClosure) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.PreviousWaveRevisionID == second.PreviousWaveRevisionID &&
		arenaWavesEqual(first.Wave, second.Wave) &&
		slicesEqualOldWaveChildren(first.Children, second.Children) &&
		first.ClosedAt.Equal(second.ClosedAt)
}

func slicesEqualOldWaveChildren(first, second []OldWaveChild) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func cloneOldWaveClosure(closure OldWaveClosure) OldWaveClosure {
	clone := closure
	clone.Wave = cloneArenaWaveExecution(closure.Wave)
	clone.Children = append([]OldWaveChild(nil), closure.Children...)
	return clone
}

func oldWaveClosureError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidOldWaveClosure, message)
}
