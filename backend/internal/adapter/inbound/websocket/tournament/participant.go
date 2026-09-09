package tournament

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrParticipantRealtimeUnauthenticated = errors.New("participant realtime authentication required")
	ErrParticipantRealtimeTournamentScope = errors.New("participant realtime tournament scope forbidden")
	ErrParticipantRealtimePlayerScope     = errors.New("participant realtime player scope forbidden")
	ErrParticipantRealtimeSource          = errors.New("participant realtime source invalid")
	ErrParticipantRealtimeUnavailable     = errors.New("participant realtime unavailable")
)

type ParticipantRealtimePrincipal struct {
	Authenticated bool
	TournamentID  uuid.UUID
	PlayerID      uuid.UUID
}

type ParticipantRealtimeRequest struct {
	Principal    ParticipantRealtimePrincipal
	TournamentID uuid.UUID
}

type ParticipantRealtimeReadQuery struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
}

type ParticipantRealtimeSnapshot struct {
	Metadata RealtimeEnvelopeMetadata
	Payload  ParticipantSnapshot
}

type ParticipantRealtimeReadSource interface {
	ReadParticipantRealtime(ctx context.Context, query ParticipantRealtimeReadQuery) (ParticipantRealtimeSnapshot, error)
}

type ParticipantRealtimeEnvelope struct {
	SchemaVersion      int                 `json:"schema_version"`
	TournamentID       uuid.UUID           `json:"tournament_id"`
	Sequence           int64               `json:"sequence"`
	EventID            uuid.UUID           `json:"event_id"`
	OccurredAt         time.Time           `json:"occurred_at"`
	ProjectionRevision int64               `json:"projection_revision"`
	ResumeID           *uuid.UUID          `json:"resume_id,omitempty"`
	Participant        ParticipantSnapshot `json:"participant"`
}

func ParticipantRealtimeView(
	ctx context.Context,
	source ParticipantRealtimeReadSource,
	request ParticipantRealtimeRequest,
) (ParticipantRealtimeEnvelope, error) {
	if !request.Principal.Authenticated || request.Principal.TournamentID == uuid.Nil || request.Principal.PlayerID == uuid.Nil {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimeUnauthenticated
	}
	if request.TournamentID == uuid.Nil || request.TournamentID != request.Principal.TournamentID {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimeTournamentScope
	}
	if ctx == nil || source == nil {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimeUnavailable
	}

	snapshot, err := source.ReadParticipantRealtime(ctx, ParticipantRealtimeReadQuery{
		TournamentID: request.TournamentID,
		PlayerID:     request.Principal.PlayerID,
	})
	if err != nil {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimeUnavailable
	}
	if snapshot.Metadata.TournamentID != request.TournamentID || snapshot.Payload.TournamentID != request.TournamentID {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimeTournamentScope
	}
	if snapshot.Payload.PlayerID != request.Principal.PlayerID {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimePlayerScope
	}

	envelope, err := NewRealtimeEnvelope(snapshot.Metadata, snapshot.Payload)
	if err != nil {
		return ParticipantRealtimeEnvelope{}, ErrParticipantRealtimeSource
	}
	return ParticipantRealtimeEnvelope{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
		ResumeID:           cloneResumeID(envelope.ResumeID),
		Participant:        envelope.Participant.clone(),
	}, nil
}
