package submission

import (
	"crypto/sha256"

	"github.com/google/uuid"

	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

type GoldenStateScope = goldenstate.GoldenStateScope

type GoldenSubmissionScope struct {
	State        GoldenStateScope
	AttemptID    uuid.UUID
	WaveID       uuid.UUID
	AssignmentID uuid.UUID
	SnapshotID   uuid.UUID
	TaskID       uuid.UUID
}

func (s GoldenSubmissionScope) IsValid() bool {
	if !goldenstate.ValidStateScope(s.State) || s.AttemptID == uuid.Nil || s.WaveID == uuid.Nil ||
		s.AssignmentID == uuid.Nil || s.SnapshotID == uuid.Nil || s.TaskID == uuid.Nil {
		return false
	}
	identities := []uuid.UUID{
		s.State.TournamentID, s.State.GroupID, s.State.GroupRevisionID.UUID(),
		s.AttemptID, s.WaveID, s.AssignmentID, s.SnapshotID, s.TaskID,
	}
	return uniqueGoldenSubmissionScopeIDs(identities)
}

type GoldenSubmissionLedgerExpectation struct {
	Scope            GoldenSubmissionScope
	RevisionID       uuid.UUID
	Revision         int64
	NextSubmissionID uint64
	PayloadDigest    [sha256.Size]byte
}

func (e GoldenSubmissionLedgerExpectation) Equal(other GoldenSubmissionLedgerExpectation) bool {
	return e == other
}

func uniqueGoldenSubmissionScopeIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
