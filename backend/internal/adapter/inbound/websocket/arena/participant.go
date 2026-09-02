package arena

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const ParticipantRealtimeMaxReplayEvents = 256

var (
	ErrParticipantRealtimeUnauthenticated = errors.New("participant realtime authentication required")
	ErrParticipantRealtimeTournamentScope = errors.New("participant realtime tournament scope forbidden")
	ErrParticipantRealtimePlayerScope     = errors.New("participant realtime player scope forbidden")
	ErrParticipantRealtimeCursor          = errors.New("participant realtime cursor invalid")
	ErrParticipantRealtimeRolePayload     = errors.New("participant realtime role payload invalid")
	ErrParticipantRealtimeEventLimit      = errors.New("participant realtime event limit exceeded")
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
	Cursor       *RealtimeCursor
}

type ParticipantRealtimeReadQuery struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	MaxEvents    int
}

type ParticipantRealtimeSnapshot struct {
	Metadata RealtimeEnvelopeMetadata
	Payload  ParticipantSnapshot
}

type ParticipantRealtimeReadModel struct {
	Snapshot  ParticipantRealtimeSnapshot
	Available RealtimeAvailableRange
	Events    []RealtimeEnvelope
}

type ParticipantRealtimeReadSource interface {
	ReadParticipantRealtime(ctx context.Context, query ParticipantRealtimeReadQuery) (ParticipantRealtimeReadModel, error)
}

type ParticipantRealtimeEnvelope struct {
	SchemaVersion      int                 `json:"schema_version"`
	TournamentID       uuid.UUID           `json:"tournament_id"`
	Sequence           int64               `json:"sequence"`
	EventID            uuid.UUID           `json:"event_id"`
	OccurredAt         time.Time           `json:"occurred_at"`
	ProjectionRevision int64               `json:"projection_revision"`
	Participant        ParticipantSnapshot `json:"participant"`
}

type ParticipantRealtimeResult struct {
	UsesSnapshot bool                          `json:"uses_snapshot"`
	Envelopes    []ParticipantRealtimeEnvelope `json:"envelopes"`
}

func ParticipantRealtimeView(
	ctx context.Context,
	source ParticipantRealtimeReadSource,
	request ParticipantRealtimeRequest,
) (ParticipantRealtimeResult, error) {
	if !request.Principal.Authenticated || request.Principal.TournamentID == uuid.Nil || request.Principal.PlayerID == uuid.Nil {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeUnauthenticated
	}
	if request.TournamentID == uuid.Nil || request.TournamentID != request.Principal.TournamentID {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeTournamentScope
	}
	if !participantRealtimeCursorValid(request.Cursor, request.TournamentID) {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeCursor
	}
	if source == nil {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeUnavailable
	}

	model, err := source.ReadParticipantRealtime(ctx, ParticipantRealtimeReadQuery{
		TournamentID: request.TournamentID,
		PlayerID:     request.Principal.PlayerID,
		MaxEvents:    ParticipantRealtimeMaxReplayEvents,
	})
	if err != nil {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeUnavailable
	}
	if len(model.Events) > ParticipantRealtimeMaxReplayEvents {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeEventLimit
	}
	if !model.Available.valid() {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeSource
	}

	snapshot, err := participantRealtimeSnapshotEnvelope(model.Snapshot, request)
	if err != nil {
		return ParticipantRealtimeResult{}, err
	}
	if err := participantRealtimeEventsValid(model.Events, model.Available, request); err != nil {
		return ParticipantRealtimeResult{}, err
	}

	resume, err := ResumeFromCursor(CursorResumeInput{
		TournamentID: request.TournamentID,
		Cursor:       request.Cursor,
		Available:    model.Available,
		Events:       model.Events,
		Snapshot:     snapshot,
	})
	if err != nil {
		return ParticipantRealtimeResult{}, ErrParticipantRealtimeSource
	}
	return participantRealtimeResult(resume, request)
}

func participantRealtimeCursorValid(cursor *RealtimeCursor, tournamentID uuid.UUID) bool {
	if cursor == nil {
		return true
	}
	return cursor.SchemaVersion == ArenaRealtimeSchemaVersion &&
		cursor.TournamentID == tournamentID &&
		cursor.LastSequence >= 1 &&
		cursor.ProjectionRevision >= 1
}

func participantRealtimeSnapshotEnvelope(
	snapshot ParticipantRealtimeSnapshot,
	request ParticipantRealtimeRequest,
) (RealtimeEnvelope, error) {
	if snapshot.Metadata.TournamentID != request.TournamentID || snapshot.Payload.TournamentID != request.TournamentID {
		return RealtimeEnvelope{}, ErrParticipantRealtimeTournamentScope
	}
	if snapshot.Payload.PlayerID != request.Principal.PlayerID {
		return RealtimeEnvelope{}, ErrParticipantRealtimePlayerScope
	}
	envelope, err := NewRealtimeEnvelope(snapshot.Metadata, snapshot.Payload)
	if err != nil {
		return RealtimeEnvelope{}, ErrParticipantRealtimeSource
	}
	return envelope, nil
}

//nolint:gocyclo // One event pass enforces ordering, range, role, tournament, and participant scope together.
func participantRealtimeEventsValid(
	events []RealtimeEnvelope,
	available RealtimeAvailableRange,
	request ParticipantRealtimeRequest,
) error {
	var previousSequence int64
	var previousRevision int64
	for _, event := range events {
		if event.TournamentID != request.TournamentID {
			return ErrParticipantRealtimeTournamentScope
		}
		if event.Participant == nil || event.Public != nil || event.Operator != nil {
			return ErrParticipantRealtimeRolePayload
		}
		if event.Participant.TournamentID != request.TournamentID {
			return ErrParticipantRealtimeTournamentScope
		}
		if event.Participant.PlayerID != request.Principal.PlayerID {
			return ErrParticipantRealtimePlayerScope
		}
		if err := event.Validate(); err != nil {
			return ErrParticipantRealtimeSource
		}
		if previousSequence > 0 && (event.Sequence <= previousSequence || event.ProjectionRevision <= previousRevision) {
			return ErrParticipantRealtimeSource
		}
		if available.OldestSequence == 0 || event.Sequence < available.OldestSequence ||
			event.Sequence > available.LatestSequence ||
			event.ProjectionRevision > available.CurrentProjectionRevision {
			return ErrParticipantRealtimeSource
		}
		previousSequence = event.Sequence
		previousRevision = event.ProjectionRevision
	}
	return nil
}

func participantRealtimeResult(
	resume CursorResumeResult,
	request ParticipantRealtimeRequest,
) (ParticipantRealtimeResult, error) {
	result := ParticipantRealtimeResult{
		UsesSnapshot: resume.UsesSnapshot,
		Envelopes:    make([]ParticipantRealtimeEnvelope, 0, len(resume.Envelopes)),
	}
	for _, envelope := range resume.Envelopes {
		if envelope.TournamentID != request.TournamentID {
			return ParticipantRealtimeResult{}, ErrParticipantRealtimeTournamentScope
		}
		if envelope.Participant == nil || envelope.Public != nil || envelope.Operator != nil {
			return ParticipantRealtimeResult{}, ErrParticipantRealtimeRolePayload
		}
		if envelope.Participant.PlayerID != request.Principal.PlayerID {
			return ParticipantRealtimeResult{}, ErrParticipantRealtimePlayerScope
		}
		result.Envelopes = append(result.Envelopes, ParticipantRealtimeEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			Participant:        envelope.Participant.clone(),
		})
	}
	return result, nil
}
