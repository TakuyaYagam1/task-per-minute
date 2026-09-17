package golden

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func reconcileGoldenContinuation(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) (*GoldenContinuationRecord, error) {
	if record.Validate() != nil || !goldenContinuationRecordMatchesCommand(record, command) {
		return nil, ErrGoldenContinuationCommandReuse
	}
	clone := record.Snapshot()
	return &clone, nil
}

func goldenContinuationRecordMatchesCommand(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return goldenContinuationRecordMatchesSource(record, command) &&
		goldenContinuationRecordMatchesSuccessor(record, command) &&
		goldenContinuationPrivateMatchesCommand(record.Assignment.Private, command.PrivateAssignments)
}

func goldenContinuationRecordMatchesSource(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return record.CommandID == command.CommandID && record.ID == command.ContinuationID &&
		record.Scope == command.Scope && record.CommandDigest == goldenContinuationCommandDigest(command) &&
		record.SourceTerminalID == command.ExpectedTerminalID &&
		record.SourceTerminalDigest == command.ExpectedTerminalDigest &&
		record.SourceParentID == command.ExpectedParentID &&
		record.SourceParentDigest == command.ExpectedParentDigest &&
		record.ExpectedState.Equal(command.ExpectedState) && record.ExpectedPlan == command.ExpectedPlan &&
		record.ExpectedPositions.Equal(command.ExpectedPositions) && record.SwissPoints == command.ExpectedSwissPoints
}

func goldenContinuationRecordMatchesSuccessor(
	record GoldenContinuationRecord,
	command GoldenContinuationCommand,
) bool {
	return record.Attempt.ID == command.NextAttemptID && record.Assignment.ID == command.NextAssignmentID &&
		record.Assignment.RevisionID == command.NextAssignmentRevisionID &&
		EqualIDs(record.NewIdentityIDs, goldenContinuationCommandIdentityIDs(command))
}

func goldenContinuationPrivateMatchesCommand(
	stored []GoldenPrivateAssignment,
	requested []GoldenPrivateAssignmentCommand,
) bool {
	if len(stored) != len(requested) {
		return false
	}
	want := make(map[uuid.UUID]uuid.UUID, len(requested))
	for _, assignment := range requested {
		want[assignment.ParticipantID] = assignment.AssignmentID
	}
	for _, assignment := range stored {
		if want[assignment.ParticipantID] != assignment.ID {
			return false
		}
	}
	return true
}

func goldenContinuationPayload(record GoldenContinuationRecord) ([]byte, error) {
	return Encode(struct {
		ID                       uuid.UUID
		CommandID                uuid.UUID
		CommandDigest            [sha256.Size]byte
		Scope                    GoldenStateScope
		SourceTerminalID         uuid.UUID
		SourceTerminalDigest     [sha256.Size]byte
		SourceParentID           uuid.UUID
		SourceParentDigest       [sha256.Size]byte
		ExpectedState            GoldenStateExpectation
		ExpectedPlan             GoldenPlanStateBinding
		ExpectedPositions        GoldenPositionLedgerExpectation
		SwissPoints              GoldenSwissPointLedgerSentinel
		NewIdentityIDs           []uuid.UUID
		ResolvedParticipantIDs   []uuid.UUID
		UnresolvedParticipantIDs []uuid.UUID
		RemainingPositions       []int
		Group                    domain.GoldenGroupState
		Attempt                  domain.GoldenAttempt
		Assignment               GoldenReserveAttemptAssignment
		CreatedAt                time.Time
		Positions                GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, SourceTerminalID: record.SourceTerminalID,
		SourceTerminalDigest: record.SourceTerminalDigest, SourceParentID: record.SourceParentID,
		SourceParentDigest: record.SourceParentDigest, ExpectedState: record.ExpectedState,
		ExpectedPlan: record.ExpectedPlan, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, NewIdentityIDs: record.NewIdentityIDs,
		ResolvedParticipantIDs:   record.ResolvedParticipantIDs,
		UnresolvedParticipantIDs: record.UnresolvedParticipantIDs,
		RemainingPositions:       record.RemainingPositions, Group: record.Group,
		Attempt: record.Attempt, Assignment: record.Assignment, CreatedAt: record.CreatedAt,
		Positions: record.Positions,
	})
}

func goldenContinuationCommandDigest(command GoldenContinuationCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenContinuationError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenContinuation, message)
}
