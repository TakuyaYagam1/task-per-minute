package arena

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const ArenaRealtimeSchemaVersion = 1

var ErrInvalidRealtimeEnvelope = errors.New("invalid Arena realtime envelope")

type RealtimeEnvelopeMetadata struct {
	SchemaVersion      int
	TournamentID       uuid.UUID
	Sequence           int64
	EventID            uuid.UUID
	OccurredAt         time.Time
	ProjectionRevision int64
}

type RealtimeEnvelope struct {
	SchemaVersion      int                  `json:"schema_version"`
	TournamentID       uuid.UUID            `json:"tournament_id"`
	Sequence           int64                `json:"sequence"`
	EventID            uuid.UUID            `json:"event_id"`
	OccurredAt         time.Time            `json:"occurred_at"`
	ProjectionRevision int64                `json:"projection_revision"`
	Participant        *ParticipantSnapshot `json:"participant,omitempty"`
	Public             *PublicSnapshot      `json:"public,omitempty"`
	Operator           *OperatorSnapshot    `json:"operator,omitempty"`
}

func NewRealtimeEnvelope(metadata RealtimeEnvelopeMetadata, payload any) (RealtimeEnvelope, error) {
	envelope := RealtimeEnvelope{
		SchemaVersion:      metadata.SchemaVersion,
		TournamentID:       metadata.TournamentID,
		Sequence:           metadata.Sequence,
		EventID:            metadata.EventID,
		OccurredAt:         metadata.OccurredAt,
		ProjectionRevision: metadata.ProjectionRevision,
	}
	switch typed := payload.(type) {
	case ParticipantSnapshot:
		clone := typed.clone()
		envelope.Participant = &clone
	case *ParticipantSnapshot:
		if typed != nil {
			clone := typed.clone()
			envelope.Participant = &clone
		}
	case PublicSnapshot:
		clone := typed.clone()
		envelope.Public = &clone
	case *PublicSnapshot:
		if typed != nil {
			clone := typed.clone()
			envelope.Public = &clone
		}
	case OperatorSnapshot:
		clone := typed.clone()
		envelope.Operator = &clone
	case *OperatorSnapshot:
		if typed != nil {
			clone := typed.clone()
			envelope.Operator = &clone
		}
	default:
		return RealtimeEnvelope{}, fmt.Errorf("%w: unsupported role payload", ErrInvalidRealtimeEnvelope)
	}
	if err := envelope.Validate(); err != nil {
		return RealtimeEnvelope{}, err
	}
	return envelope, nil
}

//nolint:gocyclo // One envelope boundary binds identity, cursor, and exactly one role payload.
func (e RealtimeEnvelope) Validate() error {
	if e.SchemaVersion != ArenaRealtimeSchemaVersion || e.TournamentID == uuid.Nil || e.EventID == uuid.Nil {
		return fmt.Errorf("%w: invalid schema or identity", ErrInvalidRealtimeEnvelope)
	}
	if e.Sequence < 1 || e.ProjectionRevision < 1 || !isServerUTC(e.OccurredAt) {
		return fmt.Errorf("%w: invalid sequence, revision, or timestamp", ErrInvalidRealtimeEnvelope)
	}

	roles := 0
	if e.Participant != nil {
		roles++
		if err := e.Participant.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRealtimeEnvelope, err)
		}
		if e.Participant.TournamentID != e.TournamentID || e.Participant.Revision != e.ProjectionRevision || e.Participant.LastSequence != e.Sequence {
			return fmt.Errorf("%w: participant cursor does not match envelope", ErrInvalidRealtimeEnvelope)
		}
	}
	if e.Public != nil {
		roles++
		if err := e.Public.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRealtimeEnvelope, err)
		}
		if e.Public.Tournament.TournamentID != e.TournamentID || e.Public.Revision != e.ProjectionRevision || e.Public.LastSequence != e.Sequence {
			return fmt.Errorf("%w: public cursor does not match envelope", ErrInvalidRealtimeEnvelope)
		}
	}
	if e.Operator != nil {
		roles++
		if err := e.Operator.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRealtimeEnvelope, err)
		}
		if e.Operator.TournamentID != e.TournamentID || e.Operator.Revision != e.ProjectionRevision || e.Operator.LastSequence != e.Sequence {
			return fmt.Errorf("%w: operator cursor does not match envelope", ErrInvalidRealtimeEnvelope)
		}
	}
	if roles != 1 {
		return fmt.Errorf("%w: exactly one role payload is required", ErrInvalidRealtimeEnvelope)
	}
	return nil
}

func (e RealtimeEnvelope) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	type wire RealtimeEnvelope
	return json.Marshal(wire(e))
}

func (e *RealtimeEnvelope) UnmarshalJSON(data []byte) error {
	if e == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalidRealtimeEnvelope)
	}
	type wire RealtimeEnvelope
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRealtimeEnvelope, err)
	}
	if decoder.More() {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidRealtimeEnvelope)
	}
	result := RealtimeEnvelope(decoded)
	if err := result.Validate(); err != nil {
		return err
	}
	*e = result
	return nil
}

func isServerUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func validOptionalUTC(value *time.Time) bool {
	return value == nil || isServerUTC(*value)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
