package resume

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

func clonePausePresenceSlice(value []pausedomain.PausePresence) []pausedomain.PausePresence {
	return model.ClonePausePresenceSlice(value)
}

func clonePauseReconnectSlice(value []pausedomain.PauseReconnectInterval) []pausedomain.PauseReconnectInterval {
	return model.ClonePauseReconnectSlice(value)
}

func clonePauseFrozenDeadlineSlice(value []PauseFrozenDeadline) []PauseFrozenDeadline {
	return model.ClonePauseFrozenDeadlineSlice(value)
}

func pauseCloneTimePointer(value *time.Time) *time.Time {
	return model.CloneTimePointer(value)
}

func replaceSeriesGame(series *domain.Series, replacement domain.Game) bool {
	return model.ReplaceSeriesGame(series, replacement)
}

func childRevision(values []PauseChildRevision, id uuid.UUID) (int64, bool) {
	return model.ChildRevision(values, id)
}

func nextRevisionMatches(current, expected int64) bool {
	return model.NextRevisionMatches(current, expected)
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

func absentDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, resultRevisionID uuid.UUID) bool {
	return model.AbsentDraftRevisionMatches(current, expected, resultRevisionID)
}

func unchangedDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, expectedPreviousRevisionID, resultRevisionID uuid.UUID) bool {
	return model.UnchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
}

func draftTransitionMatches(transition *draftusecase.TransitionEvidence, operation draftusecase.TransitionOperation, actorID uuid.UUID, reason string, occurredAt time.Time) bool {
	return model.DraftTransitionMatches(transition, operation, actorID, reason, occurredAt)
}

func validDraftResultRevisionIdentity(
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultID uuid.UUID,
	commandID uuid.UUID,
	pauseID uuid.UUID,
	actorID uuid.UUID,
) bool {
	return model.ValidDraftResultRevisionIdentity(expected, expectedPreviousRevisionID, resultID, commandID, pauseID, actorID)
}

func validatePausePresence(value pausedomain.PausePresence) error {
	return model.ValidatePausePresence(value)
}

func validatePauseReconnect(value pausedomain.PauseReconnectInterval) error {
	return model.ValidatePauseReconnect(value)
}

func samePausePresenceIdentity(first, second pausedomain.PausePresence) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.ParticipantID == second.ParticipantID
}

func ValidatePauseResumeRecord(record PauseResumeRecord) error {
	return validatePauseResumeRecord(record)
}

func ReconcilePauseResume(record PauseResumeRecord, command PauseResumeCommand) (*PauseResumeRecord, error) {
	return reconcilePauseResume(record, command)
}

func BuildPauseResumeRecord(authority PauseResumeAuthority, command PauseResumeCommand, resumedAt time.Time) (PauseResumeRecord, error) {
	return buildPauseResumeRecord(authority, command, resumedAt)
}

func ValidatePauseResumeCommand(command PauseResumeCommand) error {
	return validatePauseResumeCommand(command)
}

func ValidatePauseResumeAuthority(authority PauseResumeAuthority) error {
	return validatePauseResumeAuthority(authority)
}

func PauseResumeExpectationEqual(first, second PauseResumeExpectation) bool {
	return pauseResumeExpectationEqual(first, second)
}

func ClonePauseResumeExpectation(value PauseResumeExpectation) PauseResumeExpectation {
	return clonePauseResumeExpectation(value)
}

func ClonePauseResumeRecord(value PauseResumeRecord) PauseResumeRecord {
	return clonePauseResumeRecord(value)
}

func ResumeTimeCoversAuthorityHistory(authority PauseResumeAuthority, resumedAt time.Time) bool {
	return resumeTimeCoversAuthorityHistory(authority, resumedAt)
}

func PauseGameByID(games []PauseGame, id uuid.UUID) *PauseGame {
	return pauseGameByID(games, id)
}

func PauseGraphRevisionsFrom(graph PauseGraph) model.PauseGraphRevisions {
	return model.PauseGraphRevisionsFrom(graph)
}
