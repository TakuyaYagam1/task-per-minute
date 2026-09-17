package result

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type NoShowCommand struct {
	CommandScope

	WaveID                    uuid.UUID
	WindowID                  uuid.UUID
	SeriesID                  uuid.UUID
	Confirmed                 bool
	Reason                    string
	ExpectedAuthorityRevision int64
	ExpectedWaveRevisionID    uuid.UUID
	ExpectedWindowRevisionID  uuid.UUID
	ExpectedSeriesState       domain.SeriesState
	GameResultRevisionIDs     []uuid.UUID
	ScoreRevisionID           uuid.UUID
	SeriesResultRevisionID    uuid.UUID
}

type GameExpectation struct {
	SlotID    uuid.UUID
	GameID    uuid.UUID
	AttemptNo int
	State     domain.GameState
}

type ForfeitCommand struct {
	CommandScope

	SeriesID                  uuid.UUID
	ForfeitingParticipantID   uuid.UUID
	Confirmed                 bool
	Reason                    string
	ExpectedAuthorityRevision int64
	ExpectedGame              *GameExpectation
	Basis                     string
	RuleID                    string
	EvidenceIDs               []uuid.UUID
	GameResultRevisionID      *uuid.UUID
	ScoreRevisionID           uuid.UUID
	SeriesResultRevisionID    uuid.UUID
	AuditEventID              uuid.UUID
	OutboxEventID             uuid.UUID
	ProjectionRevisionID      uuid.UUID
}

type NoShowPort interface {
	ResolveNoShow(ctx context.Context, command NoShowCommand) error
}

type ForfeitPort interface {
	RecordForfeit(ctx context.Context, command ForfeitCommand) error
}

func validNoShowCommand(command NoShowCommand) bool {
	return validCommandScope(command.CommandScope) && command.WaveID != uuid.Nil &&
		command.WindowID != uuid.Nil && command.SeriesID != uuid.Nil && command.Confirmed &&
		validText(command.Reason, maxReasonRunes) && command.ExpectedAuthorityRevision >= 1 &&
		command.ExpectedWaveRevisionID != uuid.Nil && command.ExpectedWindowRevisionID != uuid.Nil &&
		command.ExpectedSeriesState.IsValid() && validUniqueIDs(command.GameResultRevisionIDs, 1, 3) &&
		command.ScoreRevisionID != uuid.Nil && command.SeriesResultRevisionID != uuid.Nil
}

func validForfeitCommand(command ForfeitCommand) bool {
	if !validForfeitScope(command) || !validForfeitEvidence(command) {
		return false
	}
	if command.GameResultRevisionID != nil && *command.GameResultRevisionID == uuid.Nil {
		return false
	}
	if command.ExpectedGame == nil {
		return command.GameResultRevisionID == nil
	}
	game := command.ExpectedGame
	return game.SlotID != uuid.Nil && game.GameID != uuid.Nil && game.AttemptNo >= 1 && game.State.IsValid()
}

func validForfeitScope(command ForfeitCommand) bool {
	return validCommandScope(command.CommandScope) && command.SeriesID != uuid.Nil &&
		command.ForfeitingParticipantID != uuid.Nil && command.Confirmed && validText(command.Reason, 256) &&
		command.ExpectedAuthorityRevision >= 1 && command.Basis == "rule_violation" && validToken(command.RuleID, 64)
}

func validForfeitEvidence(command ForfeitCommand) bool {
	return validUniqueIDs(command.EvidenceIDs, 1, 16) && command.ScoreRevisionID != uuid.Nil &&
		command.SeriesResultRevisionID != uuid.Nil && command.AuditEventID != uuid.Nil &&
		command.OutboxEventID != uuid.Nil && command.ProjectionRevisionID != uuid.Nil
}
