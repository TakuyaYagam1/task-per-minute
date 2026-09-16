package game

import (
	"maps"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamewave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/wave"
)

func reconcileWaveStart(
	record StartRecord,
	command StartCommand,
) (*StartRecord, error) {
	if err := record.Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if record.Scope != command.Scope || record.CommandID != command.CommandID || record.ActorID != command.ActorID ||
		record.ExpectedProjectionRevision != command.ExpectedProjectionRevision ||
		record.RequestDigest != command.RequestDigest {
		return nil, ErrWaveStartConflict
	}
	clone := cloneWaveStartRecord(record)
	return &clone, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func waveStartRecordsEqual(first, second StartRecord) bool {
	if first.Scope != second.Scope || first.CommandID != second.CommandID || first.ActorID != second.ActorID ||
		first.ExecutionAuthority != second.ExecutionAuthority ||
		first.ExpectedWaveRevision != second.ExpectedWaveRevision ||
		first.ExpectedProjectionRevision != second.ExpectedProjectionRevision ||
		first.Revisions != second.Revisions || first.RequestDigest != second.RequestDigest ||
		!waveStartUUIDsEqual(first.ByeParticipantID, second.ByeParticipantID) ||
		!maps.Equal(first.ReadinessRevisions, second.ReadinessRevisions) ||
		!first.StartedAt.Equal(second.StartedAt) ||
		!gamewave.Equal(first.Wave, second.Wave) || len(first.Games) != len(second.Games) {
		return false
	}
	for index := range first.Games {
		if first.Games[index].Scope != second.Games[index].Scope ||
			first.Games[index].AssignmentID != second.Games[index].AssignmentID ||
			!first.Games[index].Deadline.Equal(second.Games[index].Deadline) {
			return false
		}
	}
	return true
}

func cloneWaveStartRecord(record StartRecord) StartRecord {
	clone := record
	clone.Wave = gamewave.Clone(record.Wave)
	clone.ReadinessRevisions = cloneWaveStartReadinessRevisions(record.ReadinessRevisions)
	clone.ByeParticipantID = cloneWaveStartUUID(record.ByeParticipantID)
	clone.Games = make([]gamedomain.Started, len(record.Games))
	for index, game := range record.Games {
		clone.Games[index] = game
		clone.Games[index].Series = seriesdomain.CloneExecution(game.Series)
	}
	return clone
}

func cloneWaveStartUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func waveStartUUIDsEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func cloneWaveStartReadinessRevisions(source map[uuid.UUID]int64) map[uuid.UUID]int64 {
	if source == nil {
		return nil
	}
	clone := make(map[uuid.UUID]int64, len(source))
	for participantID, revision := range source {
		clone[participantID] = revision
	}
	return clone
}
