package golden

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

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
