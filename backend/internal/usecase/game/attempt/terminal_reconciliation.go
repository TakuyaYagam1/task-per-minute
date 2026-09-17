package attempt

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func reconcileFailedAttempt(
	record AttemptRecord,
	command AttemptCommand,
) (*AttemptRecord, error) {
	if record.Validate() != nil || record.Scope != command.Scope ||
		record.CommandID != command.CommandID || record.Failure.Class != command.FailureClass ||
		record.Game.AttemptNo != command.Expected.AttemptNo ||
		record.ActiveSnapshotID != command.Expected.SnapshotID ||
		record.Failure.CategoryCutoff != command.Expected.Category ||
		record.AttemptGameResultRevision.ID != command.Revisions.GameResultRevisionID ||
		record.ScoreRevision.ID != command.Revisions.ScoreRevisionID ||
		record.WaveRoute.ID != command.Revisions.RouteEvidenceID ||
		record.Evidence.AuditEventID != command.Revisions.AuditEventID ||
		record.Evidence.OutboxEventID != command.Revisions.OutboxEventID ||
		record.Evidence.ProjectionRevisionID != command.Revisions.ProjectionRevisionID {
		return nil, ErrFailedAttemptCommandReuse
	}
	clone := cloneFailedAttemptRecord(record)
	return &clone, nil
}

func validCommittedFailedAttempt(
	committed *AttemptRecord,
	proposed AttemptRecord,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope {
		return false
	}
	if !changed {
		return true
	}
	return failedAttemptRecordsEqual(*committed, proposed)
}

func failedAttemptRecordsEqual(first, second AttemptRecord) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision &&
		first.ActiveSnapshotID == second.ActiveSnapshotID && first.Failure == second.Failure &&
		seriesdomain.ExecutionsEqual(first.Series, second.Series) &&
		attemptSettlementGamesEqual(first.Game, second.Game) &&
		first.AttemptGameResultRevision == second.AttemptGameResultRevision &&
		seriesdomain.ScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		first.WaveRoute == second.WaveRoute && first.Evidence == second.Evidence &&
		first.TerminalizedAt.Equal(second.TerminalizedAt)
}

func cloneFailedAttemptRecord(record AttemptRecord) AttemptRecord {
	clone := record
	clone.Series = seriesdomain.CloneExecution(record.Series)
	clone.Game = attemptCloneGame(record.Game)
	clone.ScoreRevision.PreviousRevisionID = attemptCloneSeriesScoreRevisionIDPointer(
		record.ScoreRevision.PreviousRevisionID,
	)
	clone.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		record.ScoreRevision.GameResultRevisionIDs...,
	)
	return clone
}
