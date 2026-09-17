package pause

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

type pauseDeadlineIdentity struct {
	Kind    PauseDeadlineKind
	OwnerID uuid.UUID
}

func validatePauseGraph(graph PauseGraph, paused bool) error {
	return model.ValidatePauseGraph(graph, paused)
}

func validateNormalPauseRecord(record NormalPauseRecord) error {
	return model.ValidateNormalPauseRecord(record)
}

func validateFrozenDeadline(value PauseFrozenDeadline, active bool) error {
	return model.ValidateFrozenDeadline(value, active)
}

func validateFrozenDeadlineSet(graph PauseGraph, paused bool) error {
	return model.ValidateFrozenDeadlineSet(graph, paused)
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

func clonePausePresenceSlice(value []pausedomain.PausePresence) []pausedomain.PausePresence {
	return model.ClonePausePresenceSlice(value)
}

func clonePauseReconnectSlice(value []pausedomain.PauseReconnectInterval) []pausedomain.PauseReconnectInterval {
	return model.ClonePauseReconnectSlice(value)
}

func clonePauseFrozenDeadlineSlice(value []PauseFrozenDeadline) []PauseFrozenDeadline {
	return model.ClonePauseFrozenDeadlineSlice(value)
}

func cloneTournamentStatePointer(value *domain.TournamentState) *domain.TournamentState {
	return model.CloneTournamentStatePointer(value)
}

func cloneGameStatePointer(value *domain.GameState) *domain.GameState {
	return model.CloneGameStatePointer(value)
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

func pausedGraphMatchesExpected(
	graph PauseGraph,
	expected PauseGraphRevisions,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
	pauseID uuid.UUID,
	suspended []PauseChildRevision,
) bool {
	return model.PausedGraphMatchesExpected(graph, expected, draftResultRevisionID, commandID, actorID, reason, pausedAt, pauseID, suspended)
}

func pausedDraftRevisionMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
) bool {
	return model.PausedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID, commandID, actorID, reason, pausedAt)
}

func childRevision(values []PauseChildRevision, id uuid.UUID) (int64, bool) {
	return model.ChildRevision(values, id)
}

func nextRevisionMatches(current, expected int64) bool {
	return model.NextRevisionMatches(current, expected)
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

func revisionMapEqual(first, second []PauseChildRevision) bool {
	return model.RevisionMapEqual(first, second)
}

func presenceRevisionMapEqual(first, second []PausePresenceRevision) bool {
	return model.PresenceRevisionMapEqual(first, second)
}

func counterRevisionMapEqual(first, second []PauseReconnectCounterRevision) bool {
	return model.CounterRevisionMapEqual(first, second)
}

func frozenRevisionMapEqual(first, second []PauseFrozenDeadlineRevision) bool {
	return model.FrozenRevisionMapEqual(first, second)
}

func validDraftPreviousRevision(expected draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	return model.ValidDraftPreviousRevision(expected, previousRevisionID)
}

func validPauseDraftRevisionContract(expected *draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	return model.ValidPauseDraftRevisionContract(expected, previousRevisionID)
}

func validatePausePresenceRevisions(values []PausePresenceRevision) error {
	return model.ValidatePausePresenceRevisions(values)
}

func normalPauseTournamentState(value domain.TournamentState) bool {
	return model.NormalPauseTournamentState(value)
}

func validUniqueChildRevisions(values []PauseChildRevision) bool {
	return model.ValidUniqueChildRevisions(values)
}

func validCounterRevisions(values []PauseReconnectCounterRevision) bool {
	return model.ValidCounterRevisions(values)
}

func validFrozenDeadlineRevisions(values []PauseFrozenDeadlineRevision) bool {
	return model.ValidFrozenDeadlineRevisions(values)
}

func clonePauseSlice[T any](value []T) []T {
	return model.CloneSlice(value)
}

func absentDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, resultRevisionID uuid.UUID) bool {
	return model.AbsentDraftRevisionMatches(current, expected, resultRevisionID)
}

func unchangedDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, expectedPreviousRevisionID, resultRevisionID uuid.UUID) bool {
	return model.UnchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
}

func draftTransitionMatches(transition *draftusecase.TransitionEvidence, operation draftusecase.TransitionOperation, actorID uuid.UUID, reason string, occurredAt time.Time) bool {
	return model.DraftTransitionMatches(transition, operation, actorID, reason, occurredAt)
}

func validatePausePresence(value pausedomain.PausePresence) error {
	return model.ValidatePausePresence(value)
}

func validatePauseReconnect(value pausedomain.PauseReconnectInterval) error {
	return model.ValidatePauseReconnect(value)
}

func validPauseReconnectCounter(value pausedomain.PauseReconnectCounter) bool {
	return model.ValidPauseReconnectCounter(value)
}
