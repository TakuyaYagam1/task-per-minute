package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrPublicRealtimeInvalidConfig   = errors.New("invalid public realtime config")
	ErrPublicRealtimeInvalidScope    = errors.New("invalid public realtime tournament scope")
	ErrPublicRealtimeInvalidRead     = errors.New("invalid public realtime read result")
	ErrPublicRealtimeRolePayload     = errors.New("non-public realtime role payload")
	ErrPublicRealtimeConnectionLimit = errors.New("public realtime connection limit reached")
	ErrPublicRealtimeReadOnly        = errors.New("public realtime input is read-only")
)

type PublicRealtimeReadSource interface {
	PublicRealtimeRead(ctx context.Context, tournamentID uuid.UUID) (PublicRealtimeReadResult, error)
}

type PublicRealtimeReadResult struct {
	Snapshot         PublicSnapshot
	SnapshotMetadata RealtimeEnvelopeMetadata
	Available        RealtimeAvailableRange
	Events           []RealtimeEnvelope
}

type PublicRealtimeConfig struct {
	MaxConnections  int
	MaxReplayEvents int
}

type PublicRealtimeOpenRequest struct {
	TournamentID uuid.UUID       `json:"tournament_id"`
	Cursor       *RealtimeCursor `json:"cursor,omitempty"`
}

func (request *PublicRealtimeOpenRequest) UnmarshalJSON(data []byte) error {
	if request == nil {
		return fmt.Errorf("%w: nil request", ErrPublicRealtimeReadOnly)
	}
	type publicRealtimeOpenWire PublicRealtimeOpenRequest
	var decoded publicRealtimeOpenWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("%w: %w", ErrPublicRealtimeReadOnly, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON value", ErrPublicRealtimeReadOnly)
	}
	*request = PublicRealtimeOpenRequest(decoded)
	return nil
}

type PublicRealtimeAdapter struct {
	source          PublicRealtimeReadSource
	maxReplayEvents int
	connections     chan struct{}
}

type PublicRealtimeConnection struct {
	usesSnapshot bool
	envelopes    []RealtimeEnvelope
	release      func()
	closeOnce    sync.Once
}

func NewPublicRealtimeAdapter(source PublicRealtimeReadSource, config PublicRealtimeConfig) (*PublicRealtimeAdapter, error) {
	if source == nil || config.MaxConnections < 1 || config.MaxReplayEvents < 1 {
		return nil, ErrPublicRealtimeInvalidConfig
	}
	return &PublicRealtimeAdapter{
		source:          source,
		maxReplayEvents: config.MaxReplayEvents,
		connections:     make(chan struct{}, config.MaxConnections),
	}, nil
}

func (adapter *PublicRealtimeAdapter) PublicRealtimeOpen(ctx context.Context, request PublicRealtimeOpenRequest) (*PublicRealtimeConnection, error) {
	if adapter == nil || adapter.source == nil || request.TournamentID == uuid.Nil {
		return nil, ErrPublicRealtimeInvalidScope
	}
	select {
	case adapter.connections <- struct{}{}:
	default:
		return nil, ErrPublicRealtimeConnectionLimit
	}
	release := func() { <-adapter.connections }
	releaseOnError := true
	defer func() {
		if releaseOnError {
			release()
		}
	}()

	read, err := adapter.source.PublicRealtimeRead(ctx, request.TournamentID)
	if err != nil {
		return nil, err
	}
	snapshot, err := NewRealtimeEnvelope(read.SnapshotMetadata, read.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: snapshot: %w", ErrPublicRealtimeInvalidRead, err)
	}
	if err := publicRealtimeValidateRead(request.TournamentID, read, snapshot); err != nil {
		return nil, err
	}

	var cursor *RealtimeCursor
	if request.Cursor != nil {
		clone := *request.Cursor
		cursor = &clone
	}
	resume, err := ResumeFromCursor(CursorResumeInput{
		TournamentID: request.TournamentID,
		Cursor:       cursor,
		Available:    read.Available,
		Events:       read.Events,
		Snapshot:     snapshot,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPublicRealtimeInvalidRead, err)
	}
	if !resume.UsesSnapshot && len(resume.Envelopes) > adapter.maxReplayEvents {
		resume.UsesSnapshot = true
		resume.Envelopes = []RealtimeEnvelope{snapshot}
	}
	envelopes, err := publicRealtimeCloneEnvelopes(resume.Envelopes)
	if err != nil {
		return nil, err
	}

	releaseOnError = false
	return &PublicRealtimeConnection{
		usesSnapshot: resume.UsesSnapshot,
		envelopes:    envelopes,
		release:      release,
	}, nil
}

func (connection *PublicRealtimeConnection) PublicRealtimeUsesSnapshot() bool {
	return connection != nil && connection.usesSnapshot
}

func (connection *PublicRealtimeConnection) PublicRealtimeEnvelopes() []RealtimeEnvelope {
	if connection == nil {
		return nil
	}
	clones, err := publicRealtimeCloneEnvelopes(connection.envelopes)
	if err != nil {
		return nil
	}
	return clones
}

func (connection *PublicRealtimeConnection) PublicRealtimeClose() {
	if connection == nil {
		return
	}
	connection.closeOnce.Do(connection.release)
}

func publicRealtimeValidateRead(tournamentID uuid.UUID, read PublicRealtimeReadResult, snapshot RealtimeEnvelope) error {
	if snapshot.TournamentID != tournamentID || snapshot.Public == nil || snapshot.Participant != nil || snapshot.Operator != nil {
		return ErrPublicRealtimeInvalidScope
	}
	var previousSequence int64
	var previousRevision int64
	for _, event := range read.Events {
		if event.Public == nil || event.Participant != nil || event.Operator != nil {
			return ErrPublicRealtimeRolePayload
		}
		if event.TournamentID != tournamentID || event.Public.Tournament.TournamentID != tournamentID {
			return ErrPublicRealtimeInvalidScope
		}
		if err := event.Validate(); err != nil {
			return fmt.Errorf("%w: event: %w", ErrPublicRealtimeInvalidRead, err)
		}
		if previousSequence > 0 && (event.Sequence <= previousSequence || event.ProjectionRevision <= previousRevision) {
			return fmt.Errorf("%w: events are not ordered", ErrPublicRealtimeInvalidRead)
		}
		previousSequence = event.Sequence
		previousRevision = event.ProjectionRevision
	}
	return nil
}

func publicRealtimeCloneEnvelopes(envelopes []RealtimeEnvelope) ([]RealtimeEnvelope, error) {
	clones := make([]RealtimeEnvelope, len(envelopes))
	for index, envelope := range envelopes {
		if envelope.Public == nil || envelope.Participant != nil || envelope.Operator != nil {
			return nil, ErrPublicRealtimeRolePayload
		}
		clone, err := cloneResumeEnvelope(envelope)
		if err != nil {
			return nil, fmt.Errorf("%w: clone: %w", ErrPublicRealtimeInvalidRead, err)
		}
		clones[index] = clone
	}
	return clones, nil
}
