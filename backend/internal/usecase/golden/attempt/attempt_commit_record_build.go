package golden

import (
	"crypto/sha256"
	"reflect"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

func buildGoldenAttemptCommitRecord(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) (GoldenAttemptCommitRecord, error) {
	order := authority.Submissions.ProvisionalOrder()
	ordering := GoldenAttemptOrderingEvidence{
		AttemptID: authority.Execution.Attempt.ID, AttemptNo: authority.Execution.Attempt.AttemptNo,
		SubmissionHead: authority.Submissions.Expectation(), Order: make([]GoldenPositionOrderEntry, len(order)),
	}
	for index, submission := range order {
		ordering.Order[index] = GoldenPositionOrderEntry{
			SubmissionID: submission.ID, ParticipantID: submission.ParticipantID,
			CommittedAt: submission.CommittedAt, EvidenceDigest: submission.EvidenceDigest,
		}
	}
	payload, err := goldenAttemptOrderingPayload(ordering)
	if err != nil {
		return GoldenAttemptCommitRecord{}, goldenAttemptCommitError("encode ordering evidence")
	}
	ordering.PayloadDigest = sha256.Sum256(payload)
	positions := authority.Positions.Snapshot()
	positions.PreviousRevisionID = UUIDPointer(positions.RevisionID)
	positions.RevisionID = command.NextPositionRevisionID
	positions.Revision++
	positions.RevisionIDs = append(positions.RevisionIDs, command.NextPositionRevisionID)
	positions.Attempts = append(positions.Attempts, ordering.Snapshot())
	for _, submission := range order {
		positions.Positions = append(positions.Positions, GoldenCommittedPosition{
			Position:      positions.PositionFrom + len(positions.Positions),
			ParticipantID: submission.ParticipantID, AttemptID: authority.Execution.Attempt.ID,
			AttemptNo: authority.Execution.Attempt.AttemptNo, SubmissionID: submission.ID,
			EvidenceDigest: submission.EvidenceDigest, CommitID: command.CommitID,
		})
	}
	positions, err = buildGoldenPositionLedger(positions)
	if err != nil {
		return GoldenAttemptCommitRecord{}, err
	}
	attempt := CloneAttempt(authority.Execution.Attempt)
	attempt.State = domain.GoldenAttemptStateCompleted
	attempt.FinishedAt = attemptCloneTimePointer(&finishedAt)
	group := CloneGroup(authority.Execution.Group)
	group.Attempts[len(group.Attempts)-1] = CloneAttempt(attempt)
	if _, err := domain.NewGoldenGroup(group); err != nil {
		return GoldenAttemptCommitRecord{}, goldenAttemptCommitError("terminal group is invalid")
	}
	wave := CloneExecution(authority.Execution.Wave)
	wave.State = domain.WaveStateCompleted
	assignment, err := BuildAttemptAssignmentEvidence(authority.Execution.Assignment)
	if err != nil {
		return GoldenAttemptCommitRecord{}, err
	}
	record := GoldenAttemptCommitRecord{
		ID: command.CommitID, CommandID: command.CommandID,
		CommandDigest: goldenAttemptCommitCommandDigest(command), Scope: command.Scope,
		ActiveExecution:     CloneExecutionExpectation(command.ExpectedExecution),
		Assignment:          assignment,
		ExpectedSubmissions: command.ExpectedSubmissions, ExpectedPositions: command.ExpectedPositions,
		SwissPoints: command.ExpectedSwissPoints, Reason: command.Reason, FinishedAt: finishedAt,
		Attempt: attempt, Group: group, Wave: wave, Ordering: ordering,
		PriorPositions: authority.Positions.Snapshot(), Positions: positions,
	}
	payload, err = goldenAttemptCommitPayload(record)
	if err != nil {
		return GoldenAttemptCommitRecord{}, goldenAttemptCommitError("encode terminal record")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return GoldenAttemptCommitRecord{}, err
	}
	return record.Snapshot(), nil
}

func validateGoldenAttemptPositionLinks(record GoldenAttemptCommitRecord) error {
	if !goldenPositionLedgerExtends(record.PriorPositions, record.Positions) {
		return goldenAttemptCommitError("prior position ledger is not an immutable prefix")
	}
	start := record.ExpectedPositions.PositionCount
	if len(record.Positions.Positions) != start+len(record.Ordering.Order) ||
		len(record.Positions.Attempts) == 0 {
		return goldenAttemptCommitError("position append is not atomic")
	}
	for index, item := range record.Ordering.Order {
		position := record.Positions.Positions[start+index]
		if position.ParticipantID != item.ParticipantID || position.SubmissionID != item.SubmissionID ||
			position.EvidenceDigest != item.EvidenceDigest || position.AttemptID != record.Attempt.ID ||
			position.AttemptNo != record.Attempt.AttemptNo || position.CommitID != record.ID {
			return goldenAttemptCommitError("position changed provisional ordering evidence")
		}
	}
	return nil
}

func goldenPositionLedgerExtends(prior GoldenPositionLedger, final GoldenPositionLedger) bool {
	validHead := final.Scope == prior.Scope && final.PositionFrom == prior.PositionFrom &&
		final.PositionTo == prior.PositionTo && final.Revision == prior.Revision+1 &&
		final.PreviousRevisionID != nil && *final.PreviousRevisionID == prior.RevisionID &&
		len(final.RevisionIDs) == len(prior.RevisionIDs)+1 &&
		goldenUUIDPrefixEqual(final.RevisionIDs, prior.RevisionIDs)
	if !validHead || len(final.Positions) < len(prior.Positions) || len(final.Attempts) != len(prior.Attempts)+1 {
		return false
	}
	return goldenPositionPrefixEqual(final.Positions, prior.Positions) &&
		goldenAttemptEvidencePrefixEqual(final.Attempts, prior.Attempts)
}

func goldenUUIDPrefixEqual(final []uuid.UUID, prior []uuid.UUID) bool {
	if len(final) < len(prior) {
		return false
	}
	for index := range prior {
		if final[index] != prior[index] {
			return false
		}
	}
	return true
}

func goldenPositionPrefixEqual(final []GoldenCommittedPosition, prior []GoldenCommittedPosition) bool {
	if len(final) < len(prior) {
		return false
	}
	for index := range prior {
		if final[index] != prior[index] {
			return false
		}
	}
	return true
}

func goldenAttemptEvidencePrefixEqual(final []GoldenAttemptOrderingEvidence, prior []GoldenAttemptOrderingEvidence) bool {
	if len(final) < len(prior) {
		return false
	}
	for index := range prior {
		if !reflect.DeepEqual(final[index], prior[index]) {
			return false
		}
	}
	return true
}
