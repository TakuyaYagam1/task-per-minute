package execution

import (
	"crypto/sha256"
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"

	"github.com/google/uuid"
)

func validateGoldenWaveReceiptChain(e GoldenWaveExecution) error {
	if len(e.Receipts) == 0 || len(e.Receipts) != int(e.Revision) {
		return executionError("receipt count does not match execution revision")
	}
	seen := make(map[uuid.UUID]struct{}, len(e.Receipts))
	var prior GoldenWaveExecutionExpectation
	var previousAt time.Time
	for index, receipt := range e.Receipts {
		if err := validateGoldenReceiptStructure(e, receipt, index, previousAt); err != nil {
			return err
		}
		if _, duplicate := seen[receipt.CommandID]; duplicate {
			return executionError("duplicate retained command")
		}
		if err := validateGoldenReceiptAuthority(e, receipt, index); err != nil {
			return err
		}
		if err := validateGoldenReceiptSemantics(e, receipt, index); err != nil {
			return err
		}
		if err := validateGoldenReceiptLink(receipt, index, prior); err != nil {
			return err
		}
		prior = receipt.Result
		seen[receipt.CommandID] = struct{}{}
		previousAt = receipt.OccurredAt
	}
	if !prior.Equal(e.Expectation()) {
		return executionError("final command does not link current execution")
	}
	return nil
}

func validateGoldenReceiptStructure(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
	previousAt time.Time,
) error {
	if receipt.CommandID == uuid.Nil || receipt.Scope != execution.Scope ||
		receipt.CommandDigest == [sha256.Size]byte{} || !validWaveCommandKind(receipt.Kind) ||
		!domain.IsValidServerTime(receipt.OccurredAt) || receipt.Result.Revision != int64(index+1) {
		return executionError("invalid retained command receipt")
	}
	if !previousAt.IsZero() && receipt.OccurredAt.Before(previousAt) {
		return executionError("retained command time moved backwards")
	}
	return nil
}

func validateGoldenReceiptAuthority(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	if !executionIDsCanonical(receipt.UnusedIdentityIDs) ||
		!validGoldenReceiptUnusedIdentities(receipt, execution.Window, index == len(execution.Receipts)-1) ||
		!goldenReceiptExpectationAnchored(receipt.Result, execution) {
		return executionError("invalid retained unused command identities")
	}
	if receipt.Expected != nil && !goldenReceiptExpectationAnchored(*receipt.Expected, execution) {
		return executionError("invalid retained expected command authority")
	}
	return nil
}

func validateGoldenReceiptSemantics(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	switch receipt.Kind {
	case GoldenWaveCommandOpened:
		return validateGoldenOpenedReceipt(execution, receipt, index)
	case GoldenWaveCommandStarted:
		return validateGoldenStartedReceipt(execution, receipt, index)
	case GoldenWaveCommandReady, GoldenWaveCommandDisconnected, GoldenWaveCommandReconnected:
		return validateGoldenReadinessReceipt(execution, receipt)
	default:
		return executionError("invalid retained command receipt kind")
	}
}

func validateGoldenOpenedReceipt(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	if receipt.ParticipantID != uuid.Nil || index != 0 || receipt.Result.Started ||
		!receipt.OccurredAt.Equal(execution.OpenedAt) {
		return executionError("invalid opened receipt participant")
	}
	return nil
}

func validateGoldenStartedReceipt(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	if receipt.ParticipantID != uuid.Nil || execution.Start == nil || index != len(execution.Receipts)-1 ||
		!receipt.OccurredAt.Equal(execution.Start.StartedAt) || receipt.Expected == nil ||
		receipt.Expected.Started || !receipt.Result.Started {
		return executionError("invalid started receipt")
	}
	return nil
}

func validateGoldenReadinessReceipt(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
) error {
	if !executionContainsID(execution.Membership.ParticipantIDs, receipt.ParticipantID) ||
		receipt.OccurredAt.Before(execution.OpenedAt) || receipt.OccurredAt.After(execution.Deadline) ||
		receipt.Expected == nil || receipt.Expected.Started || receipt.Result.Started {
		return executionError("invalid readiness receipt participant or time")
	}
	return nil
}

func validateGoldenReceiptLink(
	receipt GoldenWaveCommandReceipt,
	index int,
	prior GoldenWaveExecutionExpectation,
) error {
	if index == 0 {
		if receipt.Kind != GoldenWaveCommandOpened || receipt.Expected != nil {
			return executionError("first command did not open execution")
		}
		return nil
	}
	if receipt.Expected == nil || !receipt.Expected.Equal(prior) {
		return executionError("retained command chain is broken")
	}
	return nil
}

func validGoldenReceiptUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current goldenstate.GoldenReadyWindow,
	final bool,
) bool {
	switch receipt.Kind {
	case GoldenWaveCommandReady:
		return validGoldenReadyUnusedIdentities(receipt, current, final)
	case GoldenWaveCommandDisconnected:
		return validGoldenDisconnectUnusedIdentities(receipt, current, final)
	case GoldenWaveCommandReconnected:
		return validGoldenReconnectUnusedIdentities(receipt, current, final)
	case GoldenWaveCommandOpened, GoldenWaveCommandStarted:
		return len(receipt.UnusedIdentityIDs) == 0
	default:
		return false
	}
}

type goldenReceiptWindowTransition struct {
	expected          goldenstate.GoldenReadyWindowExpectation
	windowSame        bool
	readinessSame     bool
	presenceSame      bool
	windowAdvanced    bool
	readinessAdvanced bool
	presenceAdvanced  bool
}

func goldenReceiptTransition(receipt GoldenWaveCommandReceipt) (goldenReceiptWindowTransition, bool) {
	if receipt.Expected == nil {
		return goldenReceiptWindowTransition{}, false
	}
	expected := receipt.Expected.Window
	result := receipt.Result.Window
	return goldenReceiptWindowTransition{
		expected:          expected,
		windowSame:        goldenWindowRevisionSame(expected, result),
		readinessSame:     goldenReadinessRevisionSame(expected, result),
		presenceSame:      goldenPresenceRevisionSame(expected, result),
		windowAdvanced:    goldenWindowRevisionAdvanced(expected, result),
		readinessAdvanced: goldenReadinessRevisionAdvanced(expected, result),
		presenceAdvanced:  goldenPresenceRevisionAdvanced(expected, result),
	}, true
}

func (t goldenReceiptWindowTransition) allSame() bool {
	return t.windowSame && t.readinessSame && t.presenceSame
}

func validGoldenReadyUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current goldenstate.GoldenReadyWindow,
	final bool,
) bool {
	transition, ok := goldenReceiptTransition(receipt)
	if !ok {
		return false
	}
	if transition.allSame() {
		return len(receipt.UnusedIdentityIDs) == 2
	}
	return len(receipt.UnusedIdentityIDs) == 0 && transition.windowAdvanced &&
		transition.readinessAdvanced && transition.presenceSame &&
		goldenReceiptFinalPredecessors(current, transition.expected, final, true, true, false)
}

func validGoldenDisconnectUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current goldenstate.GoldenReadyWindow,
	final bool,
) bool {
	transition, ok := goldenReceiptTransition(receipt)
	if !ok {
		return false
	}
	if transition.allSame() {
		return len(receipt.UnusedIdentityIDs) == 3
	}
	return len(receipt.UnusedIdentityIDs) == 0 && transition.windowAdvanced &&
		transition.readinessAdvanced && transition.presenceAdvanced &&
		goldenReceiptFinalPredecessors(current, transition.expected, final, true, true, true)
}

func validGoldenReconnectUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current goldenstate.GoldenReadyWindow,
	final bool,
) bool {
	transition, ok := goldenReceiptTransition(receipt)
	if !ok {
		return false
	}
	if transition.allSame() {
		return len(receipt.UnusedIdentityIDs) == 2
	}
	return len(receipt.UnusedIdentityIDs) == 0 && transition.windowAdvanced &&
		transition.readinessSame && transition.presenceAdvanced &&
		goldenReceiptFinalPredecessors(current, transition.expected, final, true, false, true)
}

func goldenReceiptFinalPredecessors(
	current goldenstate.GoldenReadyWindow,
	expected goldenstate.GoldenReadyWindowExpectation,
	final bool,
	windowChanged bool,
	readinessChanged bool,
	presenceChanged bool,
) bool {
	return !final || goldenReceiptCurrentPredecessors(
		current, expected, windowChanged, readinessChanged, presenceChanged,
	)
}

func goldenWindowRevisionSame(expected, result goldenstate.GoldenReadyWindowExpectation) bool {
	return result.Revision == expected.Revision && result.RevisionID == expected.RevisionID
}

func goldenReadinessRevisionSame(expected, result goldenstate.GoldenReadyWindowExpectation) bool {
	return result.ReadinessRevision == expected.ReadinessRevision &&
		result.ReadinessRevisionID == expected.ReadinessRevisionID && result.ReadinessDigest == expected.ReadinessDigest
}

func goldenPresenceRevisionSame(expected, result goldenstate.GoldenReadyWindowExpectation) bool {
	return result.PresenceRevision == expected.PresenceRevision &&
		result.PresenceRevisionID == expected.PresenceRevisionID && result.PresenceDigest == expected.PresenceDigest
}

func goldenWindowRevisionAdvanced(expected, result goldenstate.GoldenReadyWindowExpectation) bool {
	return expected.Revision < math.MaxInt64 && result.Revision == expected.Revision+1 &&
		result.RevisionID != expected.RevisionID
}

func goldenReadinessRevisionAdvanced(expected, result goldenstate.GoldenReadyWindowExpectation) bool {
	return expected.ReadinessRevision < math.MaxInt64 &&
		result.ReadinessRevision == expected.ReadinessRevision+1 &&
		result.ReadinessRevisionID != expected.ReadinessRevisionID && result.ReadinessDigest != expected.ReadinessDigest
}

func goldenPresenceRevisionAdvanced(expected, result goldenstate.GoldenReadyWindowExpectation) bool {
	return expected.PresenceRevision < math.MaxInt64 && result.PresenceRevision == expected.PresenceRevision+1 &&
		result.PresenceRevisionID != expected.PresenceRevisionID && result.PresenceDigest != expected.PresenceDigest
}

func goldenReceiptCurrentPredecessors(
	current goldenstate.GoldenReadyWindow,
	expected goldenstate.GoldenReadyWindowExpectation,
	windowChanged bool,
	readinessChanged bool,
	presenceChanged bool,
) bool {
	return (!windowChanged || current.PreviousRevisionID != nil && *current.PreviousRevisionID == expected.RevisionID) &&
		(!readinessChanged || current.ReadinessPreviousRevisionID != nil &&
			*current.ReadinessPreviousRevisionID == expected.ReadinessRevisionID) &&
		(!presenceChanged || current.PresencePreviousRevisionID != nil &&
			*current.PresencePreviousRevisionID == expected.PresenceRevisionID)
}

func goldenReceiptExpectationAnchored(
	expectation GoldenWaveExecutionExpectation,
	execution GoldenWaveExecution,
) bool {
	return expectation.Scope == execution.Scope && expectation.Source.Equal(execution.Source) &&
		expectation.AttemptID == execution.Attempt.ID && expectation.WaveID == execution.Wave.ID &&
		expectation.WaveRevisionID == execution.Wave.RevisionID && expectation.Window.WindowID == execution.Window.ID &&
		expectation.MembershipID == execution.Membership.ID &&
		expectation.MembershipRevisionID == execution.Membership.RevisionID &&
		expectation.MembershipRevision == execution.Membership.Revision &&
		expectation.MembershipDigest == execution.Membership.PayloadDigest &&
		expectation.AssignmentID == execution.Assignment.ID &&
		expectation.AssignmentRevisionID == execution.Assignment.RevisionID &&
		expectation.AssignmentRevision == execution.Assignment.Revision &&
		expectation.AssignmentDigest == execution.Assignment.PayloadDigest
}

func validWaveCommandKind(kind GoldenWaveCommandKind) bool {
	switch kind {
	case GoldenWaveCommandOpened, GoldenWaveCommandReady, GoldenWaveCommandDisconnected,
		GoldenWaveCommandReconnected, GoldenWaveCommandStarted:
		return true
	}
	return false
}
