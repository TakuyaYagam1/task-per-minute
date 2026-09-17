package gameclose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamewave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/wave"
)

const closeAttempts = 2

var (
	ErrInvalidClosure      = errors.New("invalid wave closure")
	ErrClosureBlocked      = errors.New("wave has unresolved children")
	ErrClosureConflict     = errors.New("wave closure conflict")
	ErrClosureCommandReuse = errors.New("wave closure command was reused")
)

type CloseScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
}

type CloseChild struct {
	SeriesID uuid.UUID
	SlotID   uuid.UUID
	GameID   uuid.UUID
	State    domain.GameState
	RouteID  uuid.UUID
}

type CloseCommand struct {
	Scope                  CloseScope
	CommandID              uuid.UUID
	ExpectedWaveRevisionID domain.WaveRevisionID
	ClosedWaveRevisionID   domain.WaveRevisionID
}

type CloseAuthority struct {
	Scope    CloseScope
	Revision int64
	Wave     domain.Wave
	Children []CloseChild
	Current  *Closure
}

type Closure struct {
	Scope                     CloseScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	PreviousWaveRevisionID    domain.WaveRevisionID
	Wave                      domain.Wave
	Children                  []CloseChild
	ClosedAt                  time.Time
}

type WaveClock interface {
	Now() time.Time
}

type CloseRepository interface {
	LoadCloseAuthority(ctx context.Context, scope CloseScope) (CloseAuthority, error)
	CommitClosure(ctx context.Context, closure Closure) (*Closure, bool, error)
}

type CloseUseCase struct {
	repository CloseRepository
	clock      WaveClock
}

func NewCloseUseCase(
	repository CloseRepository,
	clock WaveClock,
) *CloseUseCase {
	return &CloseUseCase{repository: repository, clock: clock}
}

func (u *CloseUseCase) Close(
	ctx context.Context,
	command CloseCommand,
) (*Closure, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateCloseCommand(command); err != nil {
		return nil, false, err
	}
	closedAt := u.clock.Now().Round(0).UTC()
	if !gamewave.ValidServerTime(closedAt) {
		return nil, false, domain.ErrValidation
	}
	for range closeAttempts {
		closure, changed, retry, err := u.closeAttempt(ctx, command, closedAt)
		if retry {
			continue
		}
		return closure, changed, err
	}
	return nil, false, ErrClosureConflict
}

func (u *CloseUseCase) closeAttempt(
	ctx context.Context,
	command CloseCommand,
	closedAt time.Time,
) (*Closure, bool, bool, error) {
	authority, err := u.repository.LoadCloseAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("CloseUseCase - load authority: %w", err)
	}
	if err := validateCloseAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, closureError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileClosure(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	closure, err := buildClosure(command, authority, closedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitClosure(ctx, closure)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("CloseUseCase - commit closure: %w", err)
	}
	if !validCommittedClosure(committed, closure, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneClosure(*committed)
	return &result, changed, false, nil
}

func (c Closure) Validate() error {
	if !c.Scope.IsValid() || c.CommandID == uuid.Nil || c.ExpectedAuthorityRevision < 1 ||
		c.PreviousWaveRevisionID.IsZero() || !gamewave.ValidServerTime(c.ClosedAt) ||
		c.Wave.Validate() != nil || c.Wave.ID != c.Scope.WaveID ||
		c.Wave.TournamentID != c.Scope.TournamentID ||
		c.Wave.State != domain.WaveStateCompleted || c.Wave.RevisionID.IsZero() ||
		c.Wave.RevisionID == c.PreviousWaveRevisionID || c.Wave.StartedAt == nil ||
		c.ClosedAt.Before(*c.Wave.StartedAt) {
		return closureError("invalid closure identity or completed Wave")
	}
	return validateCloseChildren(c.Children)
}

func (s CloseScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.WaveID != uuid.Nil
}

func validateCloseCommand(command CloseCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil ||
		command.ExpectedWaveRevisionID.IsZero() || command.ClosedWaveRevisionID.IsZero() ||
		command.ExpectedWaveRevisionID == command.ClosedWaveRevisionID {
		return closureError("invalid command identity or revision")
	}
	return nil
}

func validateCloseAuthority(authority CloseAuthority) error {
	if !authority.Scope.IsValid() || authority.Revision < 1 || authority.Wave.Validate() != nil ||
		authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID {
		return closureError("invalid authority identity or Wave")
	}
	if authority.Current == nil && authority.Wave.State != domain.WaveStateActive {
		return closureError("wave is not active")
	}
	if authority.Current != nil {
		if authority.Wave.State != domain.WaveStateCompleted ||
			authority.Current.Validate() != nil || authority.Current.Scope != authority.Scope ||
			!gamewave.Equal(authority.Current.Wave, authority.Wave) {
			return closureError("invalid current closure")
		}
		return nil
	}
	return validateCloseChildren(authority.Children)
}

func validateCloseChildren(children []CloseChild) error {
	if len(children) == 0 {
		return ErrClosureBlocked
	}
	gameIDs := make(map[uuid.UUID]struct{}, len(children))
	for _, child := range children {
		if child.SeriesID == uuid.Nil || child.SlotID == uuid.Nil || child.GameID == uuid.Nil ||
			!child.State.IsValid() || !childResolved(child) {
			return ErrClosureBlocked
		}
		if _, duplicate := gameIDs[child.GameID]; duplicate {
			return closureError("duplicate child Game")
		}
		gameIDs[child.GameID] = struct{}{}
	}
	return nil
}

func childResolved(child CloseChild) bool {
	if child.State == domain.GameStateVoid {
		return child.RouteID != uuid.Nil
	}
	if !child.State.IsTerminal() {
		return false
	}
	return child.RouteID == uuid.Nil
}

func buildClosure(
	command CloseCommand,
	authority CloseAuthority,
	closedAt time.Time,
) (Closure, error) {
	if authority.Wave.RevisionID != command.ExpectedWaveRevisionID {
		return Closure{}, ErrClosureConflict
	}
	if err := validateCloseChildren(authority.Children); err != nil {
		return Closure{}, err
	}
	wave := gamewave.Clone(authority.Wave)
	wave.State = domain.WaveStateCompleted
	wave.RevisionID = command.ClosedWaveRevisionID
	closure := Closure{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		PreviousWaveRevisionID:    command.ExpectedWaveRevisionID,
		Wave:                      wave, Children: append([]CloseChild(nil), authority.Children...),
		ClosedAt: closedAt,
	}
	if err := closure.Validate(); err != nil {
		return Closure{}, err
	}
	return cloneClosure(closure), nil
}

func reconcileClosure(
	closure Closure,
	command CloseCommand,
) (*Closure, error) {
	if closure.Validate() != nil || closure.Scope != command.Scope ||
		closure.CommandID != command.CommandID ||
		closure.PreviousWaveRevisionID != command.ExpectedWaveRevisionID ||
		closure.Wave.RevisionID != command.ClosedWaveRevisionID {
		return nil, ErrClosureCommandReuse
	}
	clone := cloneClosure(closure)
	return &clone, nil
}

func validCommittedClosure(
	committed *Closure,
	proposed Closure,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return closuresEqual(*committed, proposed)
}

func closuresEqual(first, second Closure) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.PreviousWaveRevisionID == second.PreviousWaveRevisionID &&
		gamewave.Equal(first.Wave, second.Wave) &&
		closeChildrenEqual(first.Children, second.Children) &&
		first.ClosedAt.Equal(second.ClosedAt)
}

func closeChildrenEqual(first, second []CloseChild) bool {
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

func cloneClosure(closure Closure) Closure {
	clone := closure
	clone.Wave = gamewave.Clone(closure.Wave)
	clone.Children = append([]CloseChild(nil), closure.Children...)
	return clone
}

func closureError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidClosure, message)
}
