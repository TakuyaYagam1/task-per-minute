package golden

import (
	"context"
	"errors"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	"github.com/google/uuid"
)

type StateClock = goldenstate.StateClock
type StateRepository = goldenstate.StateRepository
type GoldenReadyCommand = goldenstate.GoldenReadyCommand
type GoldenDisconnectCommand = goldenstate.GoldenDisconnectCommand
type GoldenNoShowCommand = goldenstate.GoldenNoShowCommand
type GoldenFallbackCommand = goldenstate.GoldenFallbackCommand
type GoldenNoShowUseCase = goldenstate.GoldenNoShowUseCase
type GoldenFallbackUseCase = goldenstate.GoldenFallbackUseCase

type GoldenReadyEventType = goldenstate.GoldenReadyEventType
type GoldenReadyWindowState = goldenstate.GoldenReadyWindowState
type GoldenStateScope = goldenstate.GoldenStateScope
type GoldenPlanStateBinding = goldenstate.GoldenPlanStateBinding
type GoldenMembershipRevision = goldenstate.GoldenMembershipRevision
type GoldenReadyWindow = goldenstate.GoldenReadyWindow
type GoldenReadyWindowExpectation = goldenstate.GoldenReadyWindowExpectation
type GoldenStateExpectation = goldenstate.GoldenStateExpectation
type GoldenReadyEvent = goldenstate.GoldenReadyEvent
type GoldenNoShowResolution = goldenstate.GoldenNoShowResolution
type GoldenPositionKind = goldenstate.GoldenPositionKind
type GoldenPositionAllocation = goldenstate.GoldenPositionAllocation
type GoldenFallbackOrderingInput = goldenstate.GoldenFallbackOrderingInput
type GoldenAllocation = goldenstate.GoldenAllocation
type GoldenState = goldenstate.GoldenState
type GoldenStateCommit = goldenstate.GoldenStateCommit

var (
	ErrInvalidGoldenState        = goldenstate.ErrInvalidGoldenState
	ErrInvalidGoldenNoShow       = goldenstate.ErrInvalidGoldenNoShow
	ErrInvalidGoldenFallback     = goldenstate.ErrInvalidGoldenFallback
	ErrGoldenFallbackNotRequired = goldenstate.ErrGoldenFallbackNotRequired

	ErrGoldenParticipationAuthorityConflict = goldenstate.ErrGoldenParticipationAuthorityConflict
	ErrGoldenParticipationConflict          = goldenstate.ErrGoldenParticipationConflict
	ErrGoldenCommandReuse                   = goldenstate.ErrGoldenCommandReuse
	ErrGoldenRevisionOverflow               = goldenstate.ErrGoldenRevisionOverflow
	ErrGoldenParticipantExcluded            = goldenstate.ErrGoldenParticipantExcluded
	ErrGoldenNoShowCutoff                   = goldenstate.ErrGoldenNoShowCutoff
	ErrGoldenNoShowAuthorityConflict        = goldenstate.ErrGoldenNoShowAuthorityConflict
	ErrGoldenNoShowConflict                 = goldenstate.ErrGoldenNoShowConflict
	ErrGoldenFallbackAuthorityConflict      = goldenstate.ErrGoldenFallbackAuthorityConflict
	ErrGoldenFallbackConflict               = goldenstate.ErrGoldenFallbackConflict
)

const (
	GoldenReadyEventAccepted      = goldenstate.GoldenReadyEventAccepted
	GoldenReadyEventDisconnected  = goldenstate.GoldenReadyEventDisconnected
	GoldenReadyEventAlreadyReady  = goldenstate.GoldenReadyEventAlreadyReady
	GoldenReadyEventAlreadyAbsent = goldenstate.GoldenReadyEventAlreadyAbsent

	GoldenReadyWindowOpen     = goldenstate.GoldenReadyWindowOpen
	GoldenReadyWindowExpired  = goldenstate.GoldenReadyWindowExpired
	GoldenReadyWindowConsumed = goldenstate.GoldenReadyWindowConsumed

	GoldenPositionDirect         = goldenstate.GoldenPositionDirect
	GoldenPositionNoShowFallback = goldenstate.GoldenPositionNoShowFallback
)

func BuildGoldenState(input GoldenState) (GoldenState, error) {
	return goldenstate.BuildGoldenState(input)
}

func ValidStateScope(scope GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
}

func ValidateReadyWindowIdentity(window GoldenReadyWindow) error {
	return goldenstate.ValidateReadyWindowIdentity(window)
}

func MembershipRevisionsEqual(first, second GoldenMembershipRevision) bool {
	return goldenstate.MembershipRevisionsEqual(first, second)
}

func CloneGroup(group domain.GoldenGroupState) domain.GoldenGroupState {
	return goldenstate.CloneGroup(group)
}

func CloneAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	return goldenstate.CloneAttempt(attempt)
}

func CloneReadyWindow(window GoldenReadyWindow) GoldenReadyWindow {
	return goldenstate.CloneReadyWindow(window)
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	return goldenstate.CloneExpectation(input)
}

type GoldenParticipationUseCase struct {
	inner *goldenstate.GoldenParticipationUseCase
}

func NewGoldenParticipationUseCase(repository StateRepository, clock StateClock) *GoldenParticipationUseCase {
	return &GoldenParticipationUseCase{inner: goldenstate.NewGoldenParticipationUseCase(repository, clock)}
}

func (u *GoldenParticipationUseCase) AcceptReady(
	ctx context.Context,
	command GoldenReadyCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.inner == nil {
		return nil, false, domain.ErrValidation
	}
	state, changed, err := u.inner.AcceptReady(ctx, command)
	if errors.Is(err, goldenstate.ErrGoldenReadyWindowClosed) {
		return state, changed, ErrGoldenReadyWindowClosed
	}
	return state, changed, err
}

func (u *GoldenParticipationUseCase) ClearOnDisconnect(
	ctx context.Context,
	command GoldenDisconnectCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.inner == nil {
		return nil, false, domain.ErrValidation
	}
	state, changed, err := u.inner.ClearOnDisconnect(ctx, command)
	if errors.Is(err, goldenstate.ErrGoldenReadyWindowClosed) {
		return state, changed, ErrGoldenReadyWindowClosed
	}
	return state, changed, err
}

func NewGoldenNoShowUseCase(repository StateRepository, clock StateClock) *GoldenNoShowUseCase {
	return goldenstate.NewGoldenNoShowUseCase(repository, clock)
}

func NewGoldenFallbackUseCase(repository StateRepository, clock StateClock) *GoldenFallbackUseCase {
	return goldenstate.NewGoldenFallbackUseCase(repository, clock)
}

func OrderGoldenFallbackMembers(members []GroupMemberSeed) ([]uuid.UUID, error) {
	return goldenstate.OrderGoldenFallbackMembers(members)
}
