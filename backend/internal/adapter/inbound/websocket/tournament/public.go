package tournament

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrPublicRealtimeInvalidConfig   = errors.New("invalid public realtime config")
	ErrPublicRealtimeInvalidScope    = errors.New("invalid public realtime tournament scope")
	ErrPublicRealtimeInvalidRead     = errors.New("invalid public realtime read result")
	ErrPublicRealtimeConnectionLimit = errors.New("public realtime connection limit reached")
)

type PublicRealtimeReadSource interface {
	PublicRealtimeRead(ctx context.Context, tournamentID uuid.UUID) (PublicRealtimeReadResult, error)
}

type PublicRealtimeReadResult struct {
	Snapshot         PublicSnapshot
	SnapshotMetadata RealtimeEnvelopeMetadata
}

type PublicRealtimeConfig struct {
	MaxConnections int
}

type PublicRealtimeOpenRequest struct {
	TournamentID uuid.UUID
}

type PublicRealtimeAdapter struct {
	source      PublicRealtimeReadSource
	connections chan struct{}
}

type PublicRealtimeConnection struct {
	envelope  RealtimeEnvelope
	release   func()
	closeOnce sync.Once
}

func NewPublicRealtimeAdapter(source PublicRealtimeReadSource, config PublicRealtimeConfig) (*PublicRealtimeAdapter, error) {
	if source == nil || config.MaxConnections < 1 {
		return nil, ErrPublicRealtimeInvalidConfig
	}
	return &PublicRealtimeAdapter{
		source:      source,
		connections: make(chan struct{}, config.MaxConnections),
	}, nil
}

func (adapter *PublicRealtimeAdapter) PublicRealtimeOpen(
	ctx context.Context,
	request PublicRealtimeOpenRequest,
) (*PublicRealtimeConnection, error) {
	if ctx == nil || adapter == nil || adapter.source == nil || request.TournamentID == uuid.Nil {
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
	envelope, err := NewRealtimeEnvelope(read.SnapshotMetadata, read.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: snapshot: %w", ErrPublicRealtimeInvalidRead, err)
	}
	if envelope.TournamentID != request.TournamentID || envelope.Public == nil ||
		envelope.Participant != nil || envelope.Operator != nil {
		return nil, ErrPublicRealtimeInvalidScope
	}
	clone, err := cloneRealtimeEnvelope(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: clone: %w", ErrPublicRealtimeInvalidRead, err)
	}

	releaseOnError = false
	return &PublicRealtimeConnection{envelope: clone, release: release}, nil
}

func (connection *PublicRealtimeConnection) PublicRealtimeEnvelope() RealtimeEnvelope {
	if connection == nil {
		return RealtimeEnvelope{}
	}
	clone, err := cloneRealtimeEnvelope(connection.envelope)
	if err != nil {
		return RealtimeEnvelope{}
	}
	return clone
}

func (connection *PublicRealtimeConnection) PublicRealtimeClose() {
	if connection == nil || connection.release == nil {
		return
	}
	connection.closeOnce.Do(connection.release)
}
