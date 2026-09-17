package replay

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxReasonRunes = 512

type ReserveCommand struct {
	CommandScope

	OldWaveID                     uuid.UUID
	SeriesID                      uuid.UUID
	SlotID                        uuid.UUID
	AssignmentID                  uuid.UUID
	AssignmentAttemptID           uuid.UUID
	Confirmed                     bool
	Reason                        string
	ExpectedAuthorityRevision     int64
	ExpectedExhaustionCommandID   uuid.UUID
	ExpectedAssignmentRevision    int64
	ExpectedPoolRevisionID        uuid.UUID
	ExpectedPoolRevision          int64
	ExpectedHistoryRevisionID     uuid.UUID
	ExpectedHistoryRevision       int64
	ExpectedArtifactRevisionID    uuid.UUID
	ExpectedArtifactRevision      int64
	ExpectedReservationRevisionID uuid.UUID
	ExpectedReservationRevision   int64
	ExpectedCategoryRevisionID    uuid.UUID
	ExpectedCategoryRevision      int64
	ProposedTaskID                uuid.UUID
	ProposedVersion               int
	ProposedSnapshotID            uuid.UUID
	ExpectedSnapshotID            uuid.UUID
	EvidenceID                    uuid.UUID
}

type ReplayCommand struct {
	CommandScope

	OldWaveID                 uuid.UUID
	SeriesID                  uuid.UUID
	SlotID                    uuid.UUID
	AssignmentID              uuid.UUID
	FailedGameID              uuid.UUID
	Confirmed                 bool
	Reason                    string
	ExpectedAuthorityRevision int64
	ExpectedClosureRevisionID uuid.UUID
	AssignmentAttemptID       uuid.UUID
	ReplacementGameID         uuid.UUID
	ReplacementWaveID         uuid.UUID
	ReplacementWaveRevisionID uuid.UUID
	ReadyWindowID             uuid.UUID
	ReadyWindowRevisionID     uuid.UUID
}

type ReservePort interface {
	AssignReserve(ctx context.Context, command ReserveCommand) error
}

type ReplayPort interface {
	ReplayGame(ctx context.Context, command ReplayCommand) error
}

func validReserveCommand(command ReserveCommand) bool {
	return validReserveScope(command) && validReserveEvidence(command) &&
		validRevisionPair(command.ExpectedPoolRevisionID, command.ExpectedPoolRevision) &&
		validRevisionPair(command.ExpectedHistoryRevisionID, command.ExpectedHistoryRevision) &&
		validRevisionPair(command.ExpectedArtifactRevisionID, command.ExpectedArtifactRevision) &&
		validRevisionPair(command.ExpectedReservationRevisionID, command.ExpectedReservationRevision) &&
		validRevisionPair(command.ExpectedCategoryRevisionID, command.ExpectedCategoryRevision)
}

func validReserveScope(command ReserveCommand) bool {
	return validCommandScope(command.CommandScope) && command.OldWaveID != uuid.Nil &&
		command.SeriesID != uuid.Nil && command.SlotID != uuid.Nil && command.AssignmentID != uuid.Nil &&
		command.AssignmentAttemptID != uuid.Nil && command.Confirmed && validText(command.Reason, maxReasonRunes)
}

func validReserveEvidence(command ReserveCommand) bool {
	return command.ExpectedAuthorityRevision >= 1 && command.ExpectedExhaustionCommandID != uuid.Nil &&
		command.ExpectedAssignmentRevision >= 1 && command.ProposedTaskID != uuid.Nil &&
		command.ProposedVersion >= 1 && command.ProposedSnapshotID != uuid.Nil &&
		command.ExpectedSnapshotID != uuid.Nil && command.EvidenceID != uuid.Nil
}

func validRevisionPair(id uuid.UUID, revision int64) bool {
	return id != uuid.Nil && revision >= 1
}

func validReplayCommand(command ReplayCommand) bool {
	return validReplayScope(command) && validReplayReplacement(command)
}

func validReplayScope(command ReplayCommand) bool {
	return validCommandScope(command.CommandScope) && command.OldWaveID != uuid.Nil &&
		command.SeriesID != uuid.Nil && command.SlotID != uuid.Nil && command.AssignmentID != uuid.Nil &&
		command.FailedGameID != uuid.Nil && command.Confirmed && validText(command.Reason, maxReasonRunes) &&
		command.ExpectedAuthorityRevision >= 1
}

func validReplayReplacement(command ReplayCommand) bool {
	return command.ExpectedClosureRevisionID != uuid.Nil && command.AssignmentAttemptID != uuid.Nil &&
		command.ReplacementGameID != uuid.Nil && command.ReplacementWaveID != uuid.Nil &&
		command.ReplacementWaveRevisionID != uuid.Nil && command.ReadyWindowID != uuid.Nil &&
		command.ReadyWindowRevisionID != uuid.Nil
}

func validCommandScope(scope CommandScope) bool {
	return scope.Operator.ActorID != uuid.Nil && scope.TournamentID != uuid.Nil && scope.CommandID != uuid.Nil
}

func validText(value string, maxRunes int) bool {
	trimmed := strings.TrimSpace(value)
	return utf8.ValidString(value) && trimmed != "" && trimmed == value && utf8.RuneCountInString(value) <= maxRunes
}
