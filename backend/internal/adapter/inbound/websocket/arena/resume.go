package arena

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalidArenaResume = errors.New("invalid Arena cursor resume")

type RealtimeCursor struct {
	SchemaVersion      int       `json:"schema_version"`
	TournamentID       uuid.UUID `json:"tournament_id"`
	LastSequence       int64     `json:"last_sequence"`
	ProjectionRevision int64     `json:"projection_revision"`
}

type RealtimeAvailableRange struct {
	OldestSequence            int64
	LatestSequence            int64
	CurrentProjectionRevision int64
}

type CursorResumeInput struct {
	TournamentID uuid.UUID
	Cursor       *RealtimeCursor
	Available    RealtimeAvailableRange
	Events       []RealtimeEnvelope
	Snapshot     RealtimeEnvelope
}

type CursorResumeResult struct {
	UsesSnapshot bool
	Envelopes    []RealtimeEnvelope
}

func ResumeFromCursor(input CursorResumeInput) (CursorResumeResult, error) {
	snapshot, err := validatedResumeSnapshot(input)
	if err != nil {
		return CursorResumeResult{}, err
	}
	fallback := func() CursorResumeResult {
		return CursorResumeResult{UsesSnapshot: true, Envelopes: []RealtimeEnvelope{snapshot}}
	}

	if !supportedResumeCursor(input, snapshot) {
		return fallback(), nil
	}

	events, ok := validatedResumeEvents(input, *input.Cursor)
	if !ok {
		return fallback(), nil
	}
	return CursorResumeResult{Envelopes: events}, nil
}

func validatedResumeSnapshot(input CursorResumeInput) (RealtimeEnvelope, error) {
	if input.TournamentID == uuid.Nil || !input.Available.valid() {
		return RealtimeEnvelope{}, fmt.Errorf("%w: invalid tournament or available range", ErrInvalidArenaResume)
	}
	if input.Snapshot.SchemaVersion != ArenaRealtimeSchemaVersion || input.Snapshot.TournamentID != input.TournamentID {
		return RealtimeEnvelope{}, fmt.Errorf("%w: snapshot scope mismatch", ErrInvalidArenaResume)
	}
	if err := input.Snapshot.Validate(); err != nil {
		return RealtimeEnvelope{}, fmt.Errorf("%w: snapshot: %w", ErrInvalidArenaResume, err)
	}
	if input.Snapshot.Sequence < input.Available.LatestSequence || input.Snapshot.ProjectionRevision < input.Available.CurrentProjectionRevision {
		return RealtimeEnvelope{}, fmt.Errorf("%w: stale snapshot", ErrInvalidArenaResume)
	}
	return cloneResumeEnvelope(input.Snapshot)
}

func (available RealtimeAvailableRange) valid() bool {
	if available.CurrentProjectionRevision < 1 {
		return false
	}
	if available.OldestSequence == 0 && available.LatestSequence == 0 {
		return true
	}
	return available.OldestSequence >= 1 && available.LatestSequence >= available.OldestSequence
}

func supportedResumeCursor(input CursorResumeInput, snapshot RealtimeEnvelope) bool {
	if input.Cursor == nil {
		return false
	}
	cursor := *input.Cursor
	if cursor.SchemaVersion != ArenaRealtimeSchemaVersion || cursor.TournamentID != input.TournamentID {
		return false
	}
	if cursor.LastSequence < 1 || cursor.ProjectionRevision < 1 {
		return false
	}
	if input.Available.OldestSequence == 0 || cursor.LastSequence < input.Available.OldestSequence || cursor.LastSequence > input.Available.LatestSequence {
		return false
	}
	if cursor.ProjectionRevision > snapshot.ProjectionRevision {
		return false
	}
	for _, event := range input.Events {
		if event.Sequence == cursor.LastSequence {
			return event.ProjectionRevision == cursor.ProjectionRevision
		}
	}
	return cursor.LastSequence == input.Available.OldestSequence
}

func validatedResumeEvents(input CursorResumeInput, cursor RealtimeCursor) ([]RealtimeEnvelope, bool) {
	result := make([]RealtimeEnvelope, 0, len(input.Events))
	var previousSequence int64
	var previousRevision int64
	for _, event := range input.Events {
		if event.SchemaVersion != ArenaRealtimeSchemaVersion || event.TournamentID != input.TournamentID {
			return nil, false
		}
		if err := event.Validate(); err != nil {
			return nil, false
		}
		if previousSequence > 0 && (event.Sequence <= previousSequence || event.ProjectionRevision <= previousRevision) {
			return nil, false
		}
		if event.Sequence > input.Available.LatestSequence || event.ProjectionRevision > input.Available.CurrentProjectionRevision {
			return nil, false
		}
		previousSequence = event.Sequence
		previousRevision = event.ProjectionRevision
		if event.Sequence <= cursor.LastSequence {
			if event.ProjectionRevision > cursor.ProjectionRevision {
				return nil, false
			}
			continue
		}
		if event.ProjectionRevision <= cursor.ProjectionRevision {
			return nil, false
		}
		clone, err := cloneResumeEnvelope(event)
		if err != nil {
			return nil, false
		}
		result = append(result, clone)
	}
	return result, true
}

func cloneResumeEnvelope(envelope RealtimeEnvelope) (RealtimeEnvelope, error) {
	metadata := RealtimeEnvelopeMetadata{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
	}
	switch {
	case envelope.Participant != nil:
		return NewRealtimeEnvelope(metadata, envelope.Participant)
	case envelope.Public != nil:
		return NewRealtimeEnvelope(metadata, envelope.Public)
	case envelope.Operator != nil:
		return NewRealtimeEnvelope(metadata, envelope.Operator)
	default:
		return RealtimeEnvelope{}, fmt.Errorf("%w: envelope has no role snapshot", ErrInvalidArenaResume)
	}
}
