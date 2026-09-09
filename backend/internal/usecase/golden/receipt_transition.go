package golden

import (
	"crypto/sha256"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type goldenIndividualReceiptDerivation struct {
	cursor                GoldenIndividualConnectionLedger
	seen                  map[uuid.UUID]struct{}
	openByParticipant     map[uuid.UUID]int
	intervalByID          map[uuid.UUID]int
	sequenceByParticipant map[uuid.UUID]int
}

func validateInitialGoldenIndividualConnectionLedger(l GoldenIndividualConnectionLedger) error {
	if l.Revision != 1 || l.PreviousRevisionID != nil || len(l.Intervals) != 0 ||
		!EqualIDs(l.PresentParticipantIDs, l.ParticipantIDs) {
		return goldenIndividualConnectionError("initial connection ledger has derived state")
	}
	return nil
}

func newGoldenIndividualReceiptDerivation(
	ledger GoldenIndividualConnectionLedger,
) (goldenIndividualReceiptDerivation, error) {
	first := ledger.Receipts[0]
	if !validGoldenIndividualSubmissionHead(first.Expected.Submissions, ledger.Scope) {
		return goldenIndividualReceiptDerivation{}, goldenIndividualConnectionError(
			"connection receipt initial submission head is malformed",
		)
	}
	cursor := GoldenIndividualConnectionLedger{
		Scope: ledger.Scope, Execution: CloneExecutionExpectation(ledger.Execution),
		Submissions: first.Expected.Submissions, StartedAt: ledger.StartedAt, Deadline: ledger.Deadline,
		RevisionID: first.Expected.RevisionID, Revision: 1,
		ParticipantIDs:        append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		PresentParticipantIDs: append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		PayloadDigest:         first.Expected.PayloadDigest,
	}
	initialPayload, err := goldenIndividualConnectionPayload(cursor)
	if err != nil || sha256.Sum256(initialPayload) != cursor.PayloadDigest ||
		!cursor.Expectation().Equal(first.Expected) {
		return goldenIndividualReceiptDerivation{}, goldenIndividualConnectionError("connection receipt initial head changed")
	}
	return goldenIndividualReceiptDerivation{
		cursor: cursor, seen: make(map[uuid.UUID]struct{}, len(ledger.Receipts)),
		openByParticipant:     make(map[uuid.UUID]int, len(ledger.ParticipantIDs)),
		intervalByID:          make(map[uuid.UUID]int, len(ledger.Intervals)),
		sequenceByParticipant: make(map[uuid.UUID]int, len(ledger.ParticipantIDs)),
	}, nil
}

func (d *goldenIndividualReceiptDerivation) apply(
	ledger GoldenIndividualConnectionLedger,
	index int,
	receipt GoldenIndividualConnectionReceipt,
) error {
	if !validGoldenIndividualReceiptIdentity(ledger, index, receipt) ||
		!validGoldenIndividualReceiptBinding(ledger, d.cursor, receipt) {
		return goldenIndividualConnectionError("invalid retained connection receipt")
	}
	if err := validateGoldenIndividualSubmissionAdvance(d.cursor.Submissions, receipt.ObservedSubmissions); err != nil {
		return err
	}
	if err := d.retainReceiptHistory(ledger, index, receipt); err != nil {
		return err
	}
	d.advanceHead(receipt)
	if err := d.applyPresence(receipt); err != nil {
		return err
	}
	return d.retainDerivedReceipt(receipt)
}

func validGoldenIndividualReceiptIdentity(
	ledger GoldenIndividualConnectionLedger,
	index int,
	receipt GoldenIndividualConnectionReceipt,
) bool {
	validKind := receipt.Kind == GoldenIndividualConnectionDisconnected ||
		receipt.Kind == GoldenIndividualConnectionReconnected
	return receipt.CommandID != uuid.Nil && receipt.CommandDigest != [sha256.Size]byte{} && validKind &&
		ContainsID(ledger.ParticipantIDs, receipt.ParticipantID) && receipt.IntervalID != uuid.Nil &&
		domain.IsValidServerTime(receipt.OccurredAt) && receipt.ResultRevision == int64(index+2) &&
		receipt.ResultRevisionID != uuid.Nil
}

func validGoldenIndividualReceiptBinding(
	ledger GoldenIndividualConnectionLedger,
	cursor GoldenIndividualConnectionLedger,
	receipt GoldenIndividualConnectionReceipt,
) bool {
	return receipt.Expected.Scope == ledger.Scope && receipt.Expected.Execution.Equal(ledger.Execution) &&
		receipt.Expected.StartedAt.Equal(ledger.StartedAt) && receipt.Expected.Deadline.Equal(ledger.Deadline) &&
		receipt.Expected.Revision == cursor.Revision &&
		validGoldenIndividualSubmissionHead(receipt.ObservedSubmissions, ledger.Scope) &&
		receipt.ObservedSubmissions.Revision >= cursor.Submissions.Revision &&
		!receipt.OccurredAt.Before(ledger.StartedAt) && receipt.OccurredAt.Before(ledger.Deadline) &&
		cursor.Expectation().Equal(receipt.Expected)
}

func validateGoldenIndividualSubmissionAdvance(
	current GoldenSubmissionLedgerExpectation,
	observed GoldenSubmissionLedgerExpectation,
) error {
	if observed.Revision == current.Revision && !observed.Equal(current) {
		return goldenIndividualConnectionError("connection receipt changed a submission head")
	}
	if observed.Revision > current.Revision &&
		(observed.RevisionID == current.RevisionID || observed.NextSubmissionID < current.NextSubmissionID) {
		return goldenIndividualConnectionError("connection receipt regressed an advanced submission head")
	}
	return nil
}

func (d *goldenIndividualReceiptDerivation) retainReceiptHistory(
	ledger GoldenIndividualConnectionLedger,
	index int,
	receipt GoldenIndividualConnectionReceipt,
) error {
	if index > 0 && receipt.OccurredAt.Before(ledger.Receipts[index-1].OccurredAt) {
		return goldenIndividualConnectionError("connection receipt time regressed")
	}
	if _, duplicate := d.seen[receipt.CommandID]; duplicate {
		return goldenIndividualConnectionError("connection command is duplicated")
	}
	d.seen[receipt.CommandID] = struct{}{}
	return nil
}

func (d *goldenIndividualReceiptDerivation) advanceHead(receipt GoldenIndividualConnectionReceipt) {
	d.cursor.PreviousRevisionID = UUIDPointer(d.cursor.RevisionID)
	d.cursor.RevisionID = receipt.ResultRevisionID
	d.cursor.Revision = receipt.ResultRevision
	d.cursor.Submissions = receipt.ObservedSubmissions
}

func (d *goldenIndividualReceiptDerivation) applyPresence(receipt GoldenIndividualConnectionReceipt) error {
	switch receipt.Kind {
	case GoldenIndividualConnectionDisconnected:
		return d.applyDisconnect(receipt)
	case GoldenIndividualConnectionReconnected:
		return d.applyReconnect(receipt)
	default:
		return goldenIndividualConnectionError("invalid retained connection receipt")
	}
}

func (d *goldenIndividualReceiptDerivation) applyDisconnect(receipt GoldenIndividualConnectionReceipt) error {
	if !d.cursor.IsPresent(receipt.ParticipantID) || d.openByParticipant[receipt.ParticipantID] != 0 ||
		d.sequenceByParticipant[receipt.ParticipantID] >= domain.ReconnectCycleLimit {
		return goldenIndividualConnectionError("disconnect receipt cannot derive presence")
	}
	d.cursor.PresentParticipantIDs = WithoutID(d.cursor.PresentParticipantIDs, receipt.ParticipantID)
	d.sequenceByParticipant[receipt.ParticipantID]++
	d.cursor.Intervals = append(d.cursor.Intervals, GoldenIndividualReconnectInterval{
		ID: receipt.IntervalID, ParticipantID: receipt.ParticipantID,
		Sequence: d.sequenceByParticipant[receipt.ParticipantID],
		State:    GoldenIndividualReconnectOpen, DisconnectedAt: receipt.OccurredAt, Deadline: d.cursor.Deadline,
	})
	intervalIndex := len(d.cursor.Intervals) - 1
	d.intervalByID[receipt.IntervalID] = intervalIndex + 1
	d.openByParticipant[receipt.ParticipantID] = intervalIndex + 1
	return nil
}

func (d *goldenIndividualReceiptDerivation) applyReconnect(receipt GoldenIndividualConnectionReceipt) error {
	intervalIndex := d.intervalByID[receipt.IntervalID] - 1
	openIndex := d.openByParticipant[receipt.ParticipantID] - 1
	if intervalIndex < 0 || openIndex != intervalIndex || intervalIndex >= len(d.cursor.Intervals) ||
		d.cursor.Intervals[intervalIndex].ParticipantID != receipt.ParticipantID ||
		d.cursor.Intervals[intervalIndex].State != GoldenIndividualReconnectOpen || d.cursor.IsPresent(receipt.ParticipantID) ||
		receipt.OccurredAt.Before(d.cursor.Intervals[intervalIndex].DisconnectedAt) {
		return goldenIndividualConnectionError("reconnect receipt cannot derive presence")
	}
	d.cursor.Intervals[intervalIndex].State = GoldenIndividualReconnectClosed
	d.cursor.Intervals[intervalIndex].ReconnectedAt = connectionCloneTimePointer(&receipt.OccurredAt)
	delete(d.openByParticipant, receipt.ParticipantID)
	d.cursor.PresentParticipantIDs = append(d.cursor.PresentParticipantIDs, receipt.ParticipantID)
	SortIDs(d.cursor.PresentParticipantIDs)
	return nil
}

func (d *goldenIndividualReceiptDerivation) retainDerivedReceipt(
	receipt GoldenIndividualConnectionReceipt,
) error {
	d.cursor.Receipts = append(d.cursor.Receipts, receipt)
	payload, err := goldenIndividualConnectionPayload(d.cursor)
	if err != nil {
		return goldenIndividualConnectionError("encode derived connection head")
	}
	d.cursor.PayloadDigest = sha256.Sum256(payload)
	return nil
}

func validGoldenIndividualSubmissionHead(
	head GoldenSubmissionLedgerExpectation,
	scope GoldenSubmissionScope,
) bool {
	return head.Scope == scope && head.RevisionID != uuid.Nil && head.Revision >= 1 &&
		head.NextSubmissionID >= 1 && head.PayloadDigest != [sha256.Size]byte{}
}
