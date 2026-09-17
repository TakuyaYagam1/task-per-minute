package resumepresence

import (
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
	resumeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/resume"
)

type NormalPauseAuthority = model.NormalPauseAuthority
type NormalPauseCommand = model.NormalPauseCommand
type NormalPauseRecord = model.NormalPauseRecord

type pauseDeadlineIdentity struct {
	Kind    PauseDeadlineKind
	OwnerID uuid.UUID
}

func validatePauseGraph(graph PauseGraph, paused bool) error {
	return model.ValidatePauseGraph(graph, paused)
}

func validateFrozenDeadline(value PauseFrozenDeadline, active bool) error {
	return model.ValidateFrozenDeadline(value, active)
}

func pauseValidServerTime(value time.Time) bool {
	return model.PauseValidServerTime(value)
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

func pauseCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	return model.CloneUUIDPointer(value)
}

func pauseCloneTimePointer(value *time.Time) *time.Time {
	return model.CloneTimePointer(value)
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

func clonePauseSlice[T any](value []T) []T {
	return model.CloneSlice(value)
}

func validatePausePresence(value pausedomain.PausePresence) error {
	return model.ValidatePausePresence(value)
}

func validatePauseReconnect(value pausedomain.PauseReconnectInterval) error {
	return model.ValidatePauseReconnect(value)
}

func validatePauseResumeCommand(command PauseResumeCommand) error {
	return resumeusecase.ValidatePauseResumeCommand(command)
}

func validatePauseResumeAuthority(authority PauseResumeAuthority) error {
	return resumeusecase.ValidatePauseResumeAuthority(authority)
}

func validatePauseResumeRecord(record PauseResumeRecord) error {
	return resumeusecase.ValidatePauseResumeRecord(record)
}

func buildPauseResumeRecord(authority PauseResumeAuthority, command PauseResumeCommand, resumedAt time.Time) (PauseResumeRecord, error) {
	return resumeusecase.BuildPauseResumeRecord(authority, command, resumedAt)
}

func pauseResumeExpectationEqual(first, second PauseResumeExpectation) bool {
	return resumeusecase.PauseResumeExpectationEqual(first, second)
}

func clonePauseResumeExpectation(value PauseResumeExpectation) PauseResumeExpectation {
	return resumeusecase.ClonePauseResumeExpectation(value)
}

func resumeTimeCoversAuthorityHistory(authority PauseResumeAuthority, resumedAt time.Time) bool {
	return resumeusecase.ResumeTimeCoversAuthorityHistory(authority, resumedAt)
}

func pauseGameByID(games []PauseGame, id uuid.UUID) *PauseGame {
	return resumeusecase.PauseGameByID(games, id)
}

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	return model.PauseGraphRevisionsFrom(graph)
}

func samePausePresenceIdentity(first, second pausedomain.PausePresence) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.ParticipantID == second.ParticipantID
}

func PauseResumeExpectationFrom(authority PauseResumeAuthority) PauseResumeExpectation {
	return resumeusecase.PauseResumeExpectationFrom(authority)
}
