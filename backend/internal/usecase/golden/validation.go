package golden

import (
	"crypto/sha256"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (l GoldenIndividualConnectionLedger) Validate() error {
	if !validGoldenIndividualLedgerAuthority(l) || !validGoldenIndividualLedgerRevision(l) ||
		!validGoldenIndividualLedgerCollections(l) {
		return goldenIndividualConnectionError("invalid ledger identity or participant set")
	}
	if err := validateGoldenIndividualIntervals(l); err != nil {
		return err
	}
	if err := validateGoldenIndividualReceipts(l); err != nil {
		return err
	}
	payload, err := goldenIndividualConnectionPayload(l)
	if err != nil || l.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != l.PayloadDigest {
		return goldenIndividualConnectionError("ledger digest changed")
	}
	return nil
}

func validGoldenIndividualLedgerAuthority(l GoldenIndividualConnectionLedger) bool {
	return l.Scope.IsValid() && l.Execution.Scope == l.Scope.State && l.Submissions.Scope == l.Scope &&
		validGoldenIndividualSubmissionHead(l.Submissions, l.Scope) && l.Execution.Started &&
		domain.IsValidServerTime(l.StartedAt) && domain.IsValidServerTime(l.Deadline) && l.StartedAt.Before(l.Deadline)
}

func validGoldenIndividualLedgerRevision(l GoldenIndividualConnectionLedger) bool {
	return l.RevisionID != uuid.Nil && l.Revision >= 1 &&
		connectionValidRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID) &&
		len(l.Receipts) == int(l.Revision-1)
}

func validGoldenIndividualLedgerCollections(l GoldenIndividualConnectionLedger) bool {
	return len(l.ParticipantIDs) >= 2 && len(l.ParticipantIDs) <= goldenIndividualParticipantLimit &&
		len(l.Intervals) <= goldenIndividualIntervalLimit && len(l.Receipts) <= goldenIndividualReceiptLimit &&
		IDsCanonical(l.ParticipantIDs) && IDsCanonical(l.PresentParticipantIDs) &&
		IDsSubset(l.PresentParticipantIDs, l.ParticipantIDs)
}

func validateGoldenIndividualIntervals(l GoldenIndividualConnectionLedger) error {
	open := make(map[uuid.UUID]struct{})
	sequences := make(map[uuid.UUID]int)
	ids := make(map[uuid.UUID]struct{}, len(l.Intervals))
	for _, interval := range l.Intervals {
		if !validGoldenIndividualIntervalHeader(l, interval, sequences[interval.ParticipantID]+1) {
			return goldenIndividualConnectionError("invalid reconnect interval")
		}
		if _, duplicate := ids[interval.ID]; duplicate {
			return goldenIndividualConnectionError("reconnect interval identity is reused")
		}
		ids[interval.ID] = struct{}{}
		sequences[interval.ParticipantID] = interval.Sequence
		if err := validateGoldenIndividualIntervalState(l, interval, open); err != nil {
			return err
		}
	}
	return nil
}

func validGoldenIndividualIntervalHeader(
	ledger GoldenIndividualConnectionLedger,
	interval GoldenIndividualReconnectInterval,
	expectedSequence int,
) bool {
	return interval.ID != uuid.Nil && ContainsID(ledger.ParticipantIDs, interval.ParticipantID) &&
		interval.Sequence == expectedSequence && interval.Sequence <= domain.ReconnectCycleLimit &&
		domain.IsValidServerTime(interval.DisconnectedAt) && domain.IsValidServerTime(interval.Deadline) &&
		interval.DisconnectedAt.Before(interval.Deadline) && interval.Deadline.Equal(ledger.Deadline) &&
		!interval.DisconnectedAt.Before(ledger.StartedAt)
}

func validateGoldenIndividualIntervalState(
	ledger GoldenIndividualConnectionLedger,
	interval GoldenIndividualReconnectInterval,
	open map[uuid.UUID]struct{},
) error {
	switch interval.State {
	case GoldenIndividualReconnectOpen:
		if interval.ReconnectedAt != nil || ContainsID(ledger.PresentParticipantIDs, interval.ParticipantID) {
			return goldenIndividualConnectionError("open interval retained present participant")
		}
		if _, duplicate := open[interval.ParticipantID]; duplicate {
			return goldenIndividualConnectionError("participant has two open intervals")
		}
		open[interval.ParticipantID] = struct{}{}
		return nil
	case GoldenIndividualReconnectClosed:
		if interval.ReconnectedAt == nil || !domain.IsValidServerTime(*interval.ReconnectedAt) ||
			interval.ReconnectedAt.Before(interval.DisconnectedAt) || !interval.ReconnectedAt.Before(interval.Deadline) {
			return goldenIndividualConnectionError("invalid reconnect closure")
		}
		return nil
	default:
		return goldenIndividualConnectionError("unknown reconnect interval state")
	}
}

func validateGoldenIndividualReceipts(l GoldenIndividualConnectionLedger) error {
	if len(l.Receipts) == 0 {
		return validateInitialGoldenIndividualConnectionLedger(l)
	}
	derivation, err := newGoldenIndividualReceiptDerivation(l)
	if err != nil {
		return err
	}
	for index, receipt := range l.Receipts {
		if err := derivation.apply(l, index, receipt); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(derivation.cursor.Snapshot(), l.Snapshot()) {
		return goldenIndividualConnectionError("connection receipts do not derive the ledger head")
	}
	return nil
}
