package golden_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func task049NextTerminalFixture(
	t *testing.T,
	previous goldenusecase.GoldenAttemptCommitRecord,
	parent goldenusecase.GoldenContinuationRecord,
	solved int,
	base int,
) (goldenusecase.GoldenAttemptCommitRecord, goldenusecase.GoldenPositionLedger) {
	t.Helper()
	require.GreaterOrEqual(t, len(parent.UnresolvedParticipantIDs), solved)
	startedAt := previous.FinishedAt.Add(time.Second)
	finishedAt := startedAt.Add(time.Minute)
	scope := goldenusecase.GoldenSubmissionScope{
		State: parent.Scope, AttemptID: parent.Attempt.ID, WaveID: continuationTask049ID(base),
		AssignmentID: parent.Assignment.ID, SnapshotID: parent.Assignment.SnapshotID,
		TaskID: parent.Assignment.TaskID,
	}
	submissionHead := goldenusecase.GoldenSubmissionLedgerExpectation{
		Scope: scope, RevisionID: continuationTask049ID(base + 1), Revision: 1,
		NextSubmissionID: uint64(solved + 1), PayloadDigest: sha256.Sum256([]byte("second attempt submissions")),
	}
	ordering := goldenusecase.GoldenAttemptOrderingEvidence{
		AttemptID: parent.Attempt.ID, AttemptNo: parent.Attempt.AttemptNo,
		SubmissionHead: submissionHead,
	}
	for index := 0; index < solved; index++ {
		ordering.Order = append(ordering.Order, goldenusecase.GoldenPositionOrderEntry{
			SubmissionID: uint64(index + 1), ParticipantID: parent.UnresolvedParticipantIDs[index],
			CommittedAt:    startedAt.Add(time.Duration(index+1) * time.Second),
			EvidenceDigest: sha256.Sum256([]byte{byte(base), byte(index)}),
		})
	}
	continuationTask049SealOrdering(t, &ordering)

	positions := parent.Positions.Snapshot()
	previousRevisionID := positions.RevisionID
	positions.PreviousRevisionID = &previousRevisionID
	positions.RevisionID = continuationTask049ID(base + 2)
	positions.Revision++
	positions.RevisionIDs = append(positions.RevisionIDs, positions.RevisionID)
	positions.Attempts = append(positions.Attempts, ordering.Snapshot())
	for _, item := range ordering.Order {
		positions.Positions = append(positions.Positions, goldenusecase.GoldenCommittedPosition{
			Position: positions.PositionFrom + len(positions.Positions), ParticipantID: item.ParticipantID,
			AttemptID: ordering.AttemptID, AttemptNo: ordering.AttemptNo,
			SubmissionID: item.SubmissionID, EvidenceDigest: item.EvidenceDigest, CommitID: continuationTask049ID(base + 3),
		})
	}
	continuationTask049SealPositionLedger(t, &positions)

	attempt := parent.Attempt
	attempt.State = domain.GoldenAttemptStateCompleted
	attempt.StartedAt = &startedAt
	attempt.FinishedAt = &finishedAt
	group := parent.Group
	group.Attempts = append([]domain.GoldenAttempt(nil), parent.Group.Attempts...)
	group.Attempts[len(group.Attempts)-1] = attempt
	members := make([]domain.WaveMember, len(attempt.ParticipantIDs))
	for index, participantID := range attempt.ParticipantIDs {
		members[index] = domain.WaveMember{ParticipantID: participantID, Ready: true}
	}
	openedAt := startedAt.Add(-time.Minute)
	windowDeadline := startedAt.Add(time.Minute)
	wave := domain.Wave{
		ID: scope.WaveID, TournamentID: scope.State.TournamentID,
		RevisionID: domain.WaveRevisionID(continuationTask049ID(base + 4)), State: domain.WaveStateCompleted,
		Members: members, StartedAt: &startedAt,
		ReadyWindow: &domain.ReadyWindow{
			ID: continuationTask049ID(base + 5), WaveID: scope.WaveID,
			RevisionID: domain.ReadyWindowRevisionID(continuationTask049ID(base + 6)),
			State:      domain.ReadyWindowStateConsumed, OpenedAt: openedAt,
			Deadline: windowDeadline, ConsumedAt: &startedAt,
		},
	}
	require.NoError(t, wave.Validate())
	active := previous.ActiveExecution
	active.RevisionID = continuationTask049ID(base + 7)
	active.Revision++
	active.PayloadDigest = sha256.Sum256([]byte("second attempt execution"))
	active.AttemptID = attempt.ID
	active.WaveID = wave.ID
	active.WaveRevisionID = wave.RevisionID
	active.AssignmentID = parent.Assignment.ID
	active.AssignmentRevisionID = parent.Assignment.RevisionID
	active.AssignmentRevision = 1
	active.AssignmentDigest = sha256.Sum256([]byte("live execution assignment uses its own schema"))
	require.NotEqual(t, parent.Assignment.PayloadDigest, active.AssignmentDigest)
	active.Started = true
	assignment := goldenusecase.GoldenAttemptAssignmentEvidence{
		ID: parent.Assignment.ID, RevisionID: parent.Assignment.RevisionID, Revision: 1,
		Scope: parent.Scope, AttemptID: parent.Attempt.ID, WaveID: wave.ID,
		MembershipID: active.MembershipID, Plan: parent.ExpectedPlan,
		EdgeID: parent.Assignment.EdgeID, ReservationID: parent.Assignment.ReservationID,
		SnapshotID: parent.Assignment.SnapshotID, TaskID: parent.Assignment.TaskID,
		ContentDigest:          parent.Assignment.ContentDigest,
		Private:                append([]goldenusecase.GoldenPrivateAssignment(nil), parent.Assignment.Private...),
		ExecutionPayloadDigest: active.AssignmentDigest,
	}
	continuationTask049SealAttemptAssignmentEvidence(t, &assignment)
	require.NoError(t, assignment.Validate())
	record := goldenusecase.GoldenAttemptCommitRecord{
		ID: continuationTask049ID(base + 3), CommandID: continuationTask049ID(base + 8),
		CommandDigest: sha256.Sum256([]byte("second terminal command")), Scope: scope,
		ActiveExecution: active, Assignment: assignment, ExpectedSubmissions: submissionHead,
		ExpectedPositions: parent.Positions.Expectation(), SwissPoints: parent.SwissPoints,
		Reason: goldenusecase.GoldenAttemptTerminalDeadline, FinishedAt: finishedAt,
		Attempt: attempt, Group: group, Wave: wave, Ordering: ordering,
		PriorPositions: parent.Positions.Snapshot(), Positions: positions,
	}
	continuationTask049SealAttemptRecord(t, &record)
	require.NoError(t, record.Validate())
	return record, positions
}

func task049Unresolved(terminal goldenusecase.GoldenAttemptCommitRecord) []uuid.UUID {
	resolved := make(map[uuid.UUID]struct{}, len(terminal.Positions.Positions))
	for _, position := range terminal.Positions.Positions {
		resolved[position.ParticipantID] = struct{}{}
	}
	result := make([]uuid.UUID, 0, len(terminal.Group.Members))
	for _, member := range terminal.Group.Members {
		if _, solved := resolved[member.ParticipantID]; !member.Excluded && !solved {
			result = append(result, member.ParticipantID)
		}
	}
	return result
}

func task049RemainingPositions(terminal goldenusecase.GoldenAttemptCommitRecord) []int {
	result := make([]int, 0, terminal.Group.PositionTo-terminal.Group.PositionFrom+1-len(terminal.Positions.Positions))
	for position := terminal.Group.PositionFrom + len(terminal.Positions.Positions); position <= terminal.Group.PositionTo; position++ {
		result = append(result, position)
	}
	return result
}

func task049Contains(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func continuationNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockContinuationClock {
	t.Helper()
	clock := goldenmocks.NewMockContinuationClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func task049SealContinuation(t *testing.T, record *goldenusecase.GoldenContinuationRecord) {
	t.Helper()
	record.PayloadDigest = continuationTask049GobDigest(t, struct {
		ID                       uuid.UUID
		CommandID                uuid.UUID
		CommandDigest            [sha256.Size]byte
		Scope                    goldenusecase.GoldenStateScope
		SourceTerminalID         uuid.UUID
		SourceTerminalDigest     [sha256.Size]byte
		SourceParentID           uuid.UUID
		SourceParentDigest       [sha256.Size]byte
		ExpectedState            goldenusecase.GoldenStateExpectation
		ExpectedPlan             goldenusecase.GoldenPlanStateBinding
		ExpectedPositions        goldenusecase.GoldenPositionLedgerExpectation
		SwissPoints              goldenusecase.GoldenSwissPointLedgerSentinel
		NewIdentityIDs           []uuid.UUID
		ResolvedParticipantIDs   []uuid.UUID
		UnresolvedParticipantIDs []uuid.UUID
		RemainingPositions       []int
		Group                    domain.GoldenGroupState
		Attempt                  domain.GoldenAttempt
		Assignment               goldenusecase.GoldenReserveAttemptAssignment
		CreatedAt                time.Time
		Positions                goldenusecase.GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, SourceTerminalID: record.SourceTerminalID,
		SourceTerminalDigest: record.SourceTerminalDigest, SourceParentID: record.SourceParentID,
		SourceParentDigest: record.SourceParentDigest, ExpectedState: record.ExpectedState,
		ExpectedPlan: record.ExpectedPlan, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, NewIdentityIDs: record.NewIdentityIDs,
		ResolvedParticipantIDs: record.ResolvedParticipantIDs, UnresolvedParticipantIDs: record.UnresolvedParticipantIDs,
		RemainingPositions: record.RemainingPositions, Group: record.Group, Attempt: record.Attempt,
		Assignment: record.Assignment, CreatedAt: record.CreatedAt, Positions: record.Positions,
	})
}

func task049SealReserveAssignment(t *testing.T, assignment *goldenusecase.GoldenReserveAttemptAssignment) {
	t.Helper()
	assignment.PayloadDigest = continuationTask049GobDigest(t, struct {
		ID            uuid.UUID
		RevisionID    uuid.UUID
		Scope         goldenusecase.GoldenStateScope
		AttemptID     uuid.UUID
		EdgeID        uuid.UUID
		EdgePosition  int
		ReservationID uuid.UUID
		SnapshotID    uuid.UUID
		TaskID        uuid.UUID
		ContentDigest [sha256.Size]byte
		Private       []goldenusecase.GoldenPrivateAssignment
	}{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Scope: assignment.Scope,
		AttemptID: assignment.AttemptID, EdgeID: assignment.EdgeID, EdgePosition: assignment.EdgePosition,
		ReservationID: assignment.ReservationID, SnapshotID: assignment.SnapshotID,
		TaskID: assignment.TaskID, ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
}
