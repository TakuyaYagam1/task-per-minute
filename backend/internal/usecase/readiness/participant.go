package readiness

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// ParticipantCommand records an explicit readiness choice made by the
// authenticated participant. Ready=false is a user action, not a synthetic
// disconnect event.
type ParticipantCommand struct {
	Scope                    ReadinessScope
	CommandID                uuid.UUID
	ActorParticipantID       uuid.UUID
	ParticipantID            uuid.UUID
	ExpectedWaveRevisionID   domain.WaveRevisionID
	ExpectedWindowRevisionID domain.ReadyWindowRevisionID
	Ready                    bool
}

func (u *ReadinessUseCase) SetParticipantReady(
	ctx context.Context,
	command ParticipantCommand,
) (*ReadinessRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if command.ActorParticipantID != command.ParticipantID || command.ParticipantID == uuid.Nil {
		return nil, false, domain.ErrAssignmentParticipant
	}
	if err := validateReadinessCommand(
		command.Scope,
		command.CommandID,
		command.ParticipantID,
		command.ExpectedWaveRevisionID,
		command.ExpectedWindowRevisionID,
	); err != nil {
		return nil, false, err
	}
	eventType := ReadinessEventCleared
	if command.Ready {
		eventType = ReadinessEventReady
	}
	return u.apply(ctx, readinessOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		expectedWaveRevisionID:   command.ExpectedWaveRevisionID,
		expectedWindowRevisionID: command.ExpectedWindowRevisionID,
		eventType:                eventType,
		recordNoop:               true,
	})
}
