package game

import (
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func replayReplacementsEqual(first, second ReplayReplacement) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.ClosureRevisionID == second.ClosureRevisionID &&
		first.FromSnapshotID == second.FromSnapshotID &&
		first.AssignmentAttemptID == second.AssignmentAttemptID &&
		first.ReservePosition == second.ReservePosition &&
		replayTaskSnapshotsEqual(first.Snapshot, second.Snapshot) && first.Category == second.Category &&
		replayGameSlotsEqual(first.Slot, second.Slot) &&
		replayGamesEqual(first.Game, second.Game) &&
		replayWavesEqual(first.Wave, second.Wave) && first.OpenedAt.Equal(second.OpenedAt)
}

func cloneReplayReplacement(replacement ReplayReplacement) ReplayReplacement {
	clone := replacement
	clone.Snapshot = cloneReplayTaskSnapshot(replacement.Snapshot)
	clone.Slot = replayCloneGameSlot(replacement.Slot)
	clone.Game = replayCloneGame(replacement.Game)
	clone.Wave = replayCloneWaveExecution(replacement.Wave)
	return clone
}

func cloneReplayTaskSnapshot(snapshot domain.AssignmentTaskSnapshot) domain.AssignmentTaskSnapshot {
	clone := snapshot
	clone.Hints = append([]string(nil), snapshot.Hints...)
	clone.TaskURL = cloneReplayStringPointer(snapshot.TaskURL)
	clone.SourceFileURL = cloneReplayStringPointer(snapshot.SourceFileURL)
	return clone
}

func cloneReplayStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func replayTaskSnapshotsEqual(first, second domain.AssignmentTaskSnapshot) bool {
	return first.SnapshotID == second.SnapshotID && first.TaskID == second.TaskID &&
		first.Version == second.Version && first.Kind == second.Kind &&
		first.Title == second.Title && first.Description == second.Description &&
		first.Category == second.Category && first.Difficulty == second.Difficulty &&
		first.TimeLimit == second.TimeLimit && first.Flag == second.Flag &&
		slices.Equal(first.Hints, second.Hints) &&
		replayStringPointersEqual(first.TaskURL, second.TaskURL) &&
		replayStringPointersEqual(first.SourceFileURL, second.SourceFileURL)
}

func replayStringPointersEqual(first, second *string) bool {
	return (first == nil && second == nil) ||
		(first != nil && second != nil && *first == *second)
}

func replayGameSlotsEqual(first, second domain.GameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID || first.Position != second.Position ||
		first.Category != second.Category || first.ScoreBefore != second.ScoreBefore ||
		len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for index := range first.Attempts {
		if !replayGamesEqual(first.Attempts[index], second.Attempts[index]) {
			return false
		}
	}
	return true
}

func replayGamesEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID &&
		first.AttemptNo == second.AttemptNo && first.State == second.State &&
		first.ResultReason == second.ResultReason &&
		replayUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		replayResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func replayUUIDPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func replayResultRevisionPointersEqual(
	first *domain.OfficialResultRevisionID,
	second *domain.OfficialResultRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func cloneFailedAttemptRecordPointer(record *AttemptRecord) *AttemptRecord {
	if record == nil {
		return nil
	}
	clone := CloneRecord(*record)
	return &clone
}

func cloneOldWaveClosurePointer(closure *Closure) *Closure {
	if closure == nil {
		return nil
	}
	clone := CloneClosure(*closure)
	return &clone
}

func cloneReplayReplacementPointer(replacement *ReplayReplacement) *ReplayReplacement {
	if replacement == nil {
		return nil
	}
	clone := cloneReplayReplacement(*replacement)
	return &clone
}
