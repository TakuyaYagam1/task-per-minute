package enter

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	return model.PauseGraphRevisionsFrom(graph)
}

func validatePauseGraph(graph PauseGraph, paused bool) error {
	return model.ValidatePauseGraph(graph, paused)
}

func validateNormalPauseRecord(record NormalPauseRecord) error {
	return model.ValidateNormalPauseRecord(record)
}

func validPauseGraphScope(scope pausedomain.GraphScope) bool {
	return model.ValidPauseGraphScope(scope)
}

func pauseValidServerTime(value time.Time) bool {
	return model.PauseValidServerTime(value)
}

func pauseTimeCoversGraphHistory(graph PauseGraph, at time.Time) bool {
	return model.PauseTimeCoversGraphHistory(graph, at)
}

func clonePauseGraph(value PauseGraph) PauseGraph {
	return model.ClonePauseGraph(value)
}

func cloneNormalPauseRecord(value NormalPauseRecord) NormalPauseRecord {
	return model.CloneNormalPauseRecord(value)
}

func clonePauseGraphRevisions(value PauseGraphRevisions) PauseGraphRevisions {
	return model.ClonePauseGraphRevisions(value)
}

func cloneTournamentStatePointer(value *domain.TournamentState) *domain.TournamentState {
	return model.CloneTournamentStatePointer(value)
}

func pauseCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	return model.CloneUUIDPointer(value)
}

func pauseCloneTimePointer(value *time.Time) *time.Time {
	return model.CloneTimePointer(value)
}

func replaceSeriesGame(series *domain.Series, replacement domain.Game) bool {
	return model.ReplaceSeriesGame(series, replacement)
}

func reconcileNormalPause(record NormalPauseRecord, command NormalPauseCommand) (*NormalPauseRecord, error) {
	return model.ReconcileNormalPause(record, command)
}

func normalPauseError(format string, arguments ...any) error {
	return model.NormalPauseError(format, arguments...)
}

func validatePauseGraphRevisions(value PauseGraphRevisions) error {
	return model.ValidatePauseGraphRevisions(value)
}

func pauseGraphRevisionsEqual(first, second PauseGraphRevisions) bool {
	return model.PauseGraphRevisionsEqual(first, second)
}

func validDraftPreviousRevision(expected draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	return model.ValidDraftPreviousRevision(expected, previousRevisionID)
}
