package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

// ValidatePauseGraph validates a complete pause graph and its descendants.
func ValidatePauseGraph(graph PauseGraph, paused bool) error {
	return validatePauseGraph(graph, paused)
}

// ValidateNormalPauseRecord validates a persisted normal pause record.
func ValidateNormalPauseRecord(record NormalPauseRecord) error {
	return validateNormalPauseRecord(record)
}

// ValidateFrozenDeadline validates one frozen deadline.
func ValidateFrozenDeadline(value PauseFrozenDeadline, active bool) error {
	return validateFrozenDeadline(value, active)
}

// ValidateFrozenDeadlineSet validates all frozen deadlines in a graph.
func ValidateFrozenDeadlineSet(graph PauseGraph, paused bool) error {
	return validateFrozenDeadlineSet(graph, paused)
}

// ValidPauseGraphScope validates a graph scope.
func ValidPauseGraphScope(scope pausedomain.GraphScope) bool {
	return validPauseGraphScope(scope)
}

// PauseValidServerTime validates a server timestamp.
func PauseValidServerTime(value time.Time) bool {
	return pauseValidServerTime(value)
}

// PauseTimeCoversGraphHistory verifies that at is not before graph history.
func PauseTimeCoversGraphHistory(graph PauseGraph, at time.Time) bool {
	return pauseTimeCoversGraphHistory(graph, at)
}

// ClonePauseGraph clones all mutable graph descendants.
func ClonePauseGraph(value PauseGraph) PauseGraph {
	return clonePauseGraph(value)
}

// CloneNormalPauseRecord clones a pause record and its graph.
func CloneNormalPauseRecord(value NormalPauseRecord) NormalPauseRecord {
	return cloneNormalPauseRecord(value)
}

// ClonePauseGraphRevisions clones revision slices and draft expectations.
func ClonePauseGraphRevisions(value PauseGraphRevisions) PauseGraphRevisions {
	return clonePauseGraphRevisions(value)
}

// ClonePausePresenceSlice clones presence values and their timestamps.
func ClonePausePresenceSlice(value []pausedomain.PausePresence) []pausedomain.PausePresence {
	return clonePausePresenceSlice(value)
}

// ClonePauseReconnectSlice clones reconnect intervals and their timestamps.
func ClonePauseReconnectSlice(value []pausedomain.PauseReconnectInterval) []pausedomain.PauseReconnectInterval {
	return clonePauseReconnectSlice(value)
}

// ClonePauseFrozenDeadlineSlice clones frozen deadline values and pointers.
func ClonePauseFrozenDeadlineSlice(value []PauseFrozenDeadline) []PauseFrozenDeadline {
	return clonePauseFrozenDeadlineSlice(value)
}

// CloneTournamentStatePointer clones an optional tournament state.
func CloneTournamentStatePointer(value *domain.TournamentState) *domain.TournamentState {
	return cloneTournamentStatePointer(value)
}

// CloneGameStatePointer clones an optional game state.
func CloneGameStatePointer(value *domain.GameState) *domain.GameState {
	return cloneGameStatePointer(value)
}

// CloneUUIDPointer clones an optional UUID.
func CloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	return pauseCloneUUIDPointer(value)
}

// CloneTimePointer clones an optional timestamp.
func CloneTimePointer(value *time.Time) *time.Time {
	return pauseCloneTimePointer(value)
}

// ReplaceSeriesGame replaces a game inside a series by identity.
func ReplaceSeriesGame(series *domain.Series, replacement domain.Game) bool {
	return replaceSeriesGame(series, replacement)
}

// PausedGraphMatchesExpected verifies the graph mutation against the expected revisions.
func PausedGraphMatchesExpected(
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
	return pausedGraphMatchesExpected(graph, expected, draftResultRevisionID, commandID, actorID, reason, pausedAt, pauseID, suspended)
}

// PausedDraftRevisionMatches verifies draft revision lineage after pausing.
func PausedDraftRevisionMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
) bool {
	return pausedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID, commandID, actorID, reason, pausedAt)
}

// ChildRevision returns a child revision by identity.
func ChildRevision(values []PauseChildRevision, id uuid.UUID) (int64, bool) {
	return childRevision(values, id)
}

// NextRevisionMatches reports whether current is the next revision after expected.
func NextRevisionMatches(current, expected int64) bool {
	return nextRevisionMatches(current, expected)
}

// ReconcileNormalPause rehydrates a previously committed pause command.
func ReconcileNormalPause(record NormalPauseRecord, command NormalPauseCommand) (*NormalPauseRecord, error) {
	return reconcileNormalPause(record, command)
}

// NormalPauseError preserves the pause graph error identity and message format.
func NormalPauseError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNormalPauseGraph, fmt.Sprintf(format, arguments...))
}

// ValidatePauseGraphRevisions validates the revision expectation for a graph.
func ValidatePauseGraphRevisions(value PauseGraphRevisions) error {
	return validatePauseGraphRevisions(value)
}

// PauseGraphRevisionsEqual compares revision expectations independent of row order.
func PauseGraphRevisionsEqual(first, second PauseGraphRevisions) bool {
	return pauseGraphRevisionsEqual(first, second)
}

// ValidDraftPreviousRevision validates the previous revision link.
func ValidDraftPreviousRevision(expected draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	return validDraftPreviousRevision(expected, previousRevisionID)
}

// RevisionMapEqual compares child revision collections by identity.
func RevisionMapEqual(first, second []PauseChildRevision) bool {
	return revisionMapEqual(first, second)
}

// PresenceRevisionMapEqual compares presence revisions by participant.
func PresenceRevisionMapEqual(first, second []PausePresenceRevision) bool {
	return presenceRevisionMapEqual(first, second)
}

// CounterRevisionMapEqual compares reconnect counter revisions by identity.
func CounterRevisionMapEqual(first, second []PauseReconnectCounterRevision) bool {
	return counterRevisionMapEqual(first, second)
}

// FrozenRevisionMapEqual compares frozen deadline revisions by identity.
func FrozenRevisionMapEqual(first, second []PauseFrozenDeadlineRevision) bool {
	return frozenRevisionMapEqual(first, second)
}

// AbsentDraftRevisionMatches validates an absent draft revision result.
func AbsentDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, resultRevisionID uuid.UUID) bool {
	return absentDraftRevisionMatches(current, expected, resultRevisionID)
}

// UnchangedDraftRevisionMatches validates an unchanged draft revision result.
func UnchangedDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, expectedPreviousRevisionID, resultRevisionID uuid.UUID) bool {
	return unchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
}

// DraftTransitionMatches validates draft transition evidence.
func DraftTransitionMatches(transition *draftusecase.TransitionEvidence, operation draftusecase.TransitionOperation, actorID uuid.UUID, reason string, occurredAt time.Time) bool {
	return draftTransitionMatches(transition, operation, actorID, reason, occurredAt)
}

// ValidPauseDraftRevisionContract validates draft revision lineage.
func ValidPauseDraftRevisionContract(expected *draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	return validPauseDraftRevisionContract(expected, previousRevisionID)
}

// ValidatePausePresenceRevisions validates presence revision expectations.
func ValidatePausePresenceRevisions(values []PausePresenceRevision) error {
	return validatePausePresenceRevisions(values)
}

// NormalPauseTournamentState reports whether a tournament state can be paused.
func NormalPauseTournamentState(value domain.TournamentState) bool {
	return normalPauseTournamentState(value)
}

// ValidUniqueChildRevisions validates child revision identity uniqueness.
func ValidUniqueChildRevisions(values []PauseChildRevision) bool {
	return validUniqueChildRevisions(values)
}

// ValidCounterRevisions validates reconnect counter revisions.
func ValidCounterRevisions(values []PauseReconnectCounterRevision) bool {
	return validCounterRevisions(values)
}

// ValidFrozenDeadlineRevisions validates frozen deadline revisions.
func ValidFrozenDeadlineRevisions(values []PauseFrozenDeadlineRevision) bool {
	return validFrozenDeadlineRevisions(values)
}

// CloneSlice clones a slice while preserving nil versus empty semantics.
func CloneSlice[T any](value []T) []T {
	return clonePauseSlice(value)
}
