package golden

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func goldenRecoveryParticipationEstablished(attempts []GoldenRecoveryAttempt) bool {
	for _, record := range attempts {
		if record.Attempt.StartedAt != nil {
			return true
		}
		for _, membership := range record.Memberships {
			if membership.ParticipationEstablishedAt != nil {
				return true
			}
		}
	}
	return false
}

func goldenRecoveryRetainedAttempts(attempts []GoldenRecoveryAttempt) []uuid.UUID {
	retained := make([]uuid.UUID, 0, len(attempts))
	for _, record := range attempts {
		if record.Attempt.RetainedAt != nil {
			retained = append(retained, record.Attempt.ID)
		}
	}
	return retained
}

func goldenRecoveryPriorCommits(attempts []GoldenRecoveryAttempt, liveIndex int) []GoldenRecoveryCommittedAttempt {
	limit := len(attempts)
	if liveIndex >= 0 {
		limit = liveIndex
	}
	committed := make([]GoldenRecoveryCommittedAttempt, 0, limit)
	for i := 0; i < limit; i++ {
		if len(attempts[i].PositionCommits) == 0 {
			continue
		}
		positions := append([]GoldenRecoveryPositionCommit(nil), attempts[i].PositionCommits...)
		sort.Slice(positions, func(left, right int) bool {
			if positions[left].Position != positions[right].Position {
				return positions[left].Position < positions[right].Position
			}
			return bytes.Compare(positions[left].ID[:], positions[right].ID[:]) < 0
		})
		committed = append(committed, GoldenRecoveryCommittedAttempt{
			AttemptID: attempts[i].Attempt.ID, AttemptNo: attempts[i].Attempt.AttemptNo,
			Positions: positions,
		})
	}
	return committed
}

func buildGoldenRecoveryLiveAttempt(record GoldenRecoveryAttempt) *GoldenRecoveryLiveAttempt {
	live := &GoldenRecoveryLiveAttempt{
		AttemptID: record.Attempt.ID,
		Deadlines: GoldenRecoveryDeadlines{
			Ready: *record.ReadyDeadline, Execution: cloneGoldenRecoveryTime(record.ExecutionDeadline),
		},
	}
	for _, membership := range record.Memberships {
		if membership.Selection == GoldenRecoverySelectionReserve {
			live.Reserves = append(live.Reserves, GoldenRecoveryReserve{
				MembershipID: membership.ID, ParticipantID: membership.ParticipantID,
				Position: membership.ReservePosition, PromotedAt: cloneGoldenRecoveryTime(membership.PromotedAt),
			})
		}
		if membership.ReadyAt != nil && membership.Selection != GoldenRecoverySelectionExcluded {
			live.ReadyParticipantIDs = append(live.ReadyParticipantIDs, membership.ParticipantID)
		}
	}
	for _, submission := range record.Submissions {
		live.ProvisionalOrder = append(live.ProvisionalOrder, GoldenRecoveryProvisional{
			SubmissionID: submission.ID, ParticipantID: submission.ParticipantID,
			ServerSequence: submission.ServerSequence, Position: submission.Position,
		})
	}
	sort.Slice(live.Reserves, func(left, right int) bool {
		if live.Reserves[left].Position != live.Reserves[right].Position {
			return live.Reserves[left].Position < live.Reserves[right].Position
		}
		return bytes.Compare(live.Reserves[left].ParticipantID[:], live.Reserves[right].ParticipantID[:]) < 0
	})
	sort.Slice(live.ReadyParticipantIDs, func(left, right int) bool {
		return bytes.Compare(live.ReadyParticipantIDs[left][:], live.ReadyParticipantIDs[right][:]) < 0
	})
	sort.Slice(live.ProvisionalOrder, func(left, right int) bool {
		if live.ProvisionalOrder[left].ServerSequence != live.ProvisionalOrder[right].ServerSequence {
			return live.ProvisionalOrder[left].ServerSequence < live.ProvisionalOrder[right].ServerSequence
		}
		return bytes.Compare(live.ProvisionalOrder[left].SubmissionID[:], live.ProvisionalOrder[right].SubmissionID[:]) < 0
	})
	return live
}

func goldenRecoverySortedIDs(ids map[uuid.UUID]struct{}) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool {
		return bytes.Compare(result[left][:], result[right][:]) < 0
	})
	return result
}

func goldenRecoveryUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func goldenRecoveryOptionalTime(value *time.Time) bool {
	return value == nil || goldenRecoveryUTC(*value)
}

func cloneGoldenRecoveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenRecoveryAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	clone := attempt
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.PreviousAttemptID = cloneGoldenRecoveryUUID(attempt.PreviousAttemptID)
	clone.RetainedAt = cloneGoldenRecoveryTime(attempt.RetainedAt)
	clone.StartedAt = cloneGoldenRecoveryTime(attempt.StartedAt)
	clone.FinishedAt = cloneGoldenRecoveryTime(attempt.FinishedAt)
	return clone
}

func cloneGoldenRecoveryUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func goldenRecoveryError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidGoldenRecovery, message)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidGoldenRecovery, message, cause)
}
