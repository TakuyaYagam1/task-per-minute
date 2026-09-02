package arena

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const (
	OperatorRealtimeRole        = "operator"
	OperatorRealtimeReplayLimit = 128
)

var (
	ErrOperatorRealtimeAuthentication = errors.New("operator realtime authentication required")
	ErrOperatorRealtimeRole           = errors.New("operator realtime role forbidden")
	ErrOperatorRealtimeScope          = errors.New("operator realtime tournament scope mismatch")
	ErrOperatorRealtimeCursor         = errors.New("operator realtime cursor invalid")
	ErrOperatorRealtimePayload        = errors.New("operator realtime source payload invalid")
	ErrOperatorRealtimeReplayLimit    = errors.New("operator realtime replay limit exceeded")
	ErrOperatorRealtimeSource         = errors.New("operator realtime source unavailable")
)

type OperatorRealtimePrincipal struct {
	Authenticated bool
	PrincipalID   uuid.UUID
	Role          string
	TournamentID  uuid.UUID
}

type OperatorRealtimeQuery struct {
	TournamentID uuid.UUID
	Cursor       *RealtimeCursor
	ReplayLimit  int
}

type OperatorRealtimeState struct {
	Available RealtimeAvailableRange
	Events    []RealtimeEnvelope
	Snapshot  RealtimeEnvelope
}

type OperatorRealtimeReadSource interface {
	ReadOperatorRealtime(ctx context.Context, query OperatorRealtimeQuery) (OperatorRealtimeState, error)
}

type OperatorRealtimeResult struct {
	UsesSnapshot bool               `json:"uses_snapshot"`
	Envelopes    []RealtimeEnvelope `json:"envelopes"`
}

type OperatorRealtimeAdapter struct {
	source OperatorRealtimeReadSource
}

func NewOperatorRealtimeAdapter(source OperatorRealtimeReadSource) *OperatorRealtimeAdapter {
	return &OperatorRealtimeAdapter{source: source}
}

//nolint:gocyclo // One read boundary keeps authentication, scope, cursor, source, and role validation atomic.
func (a *OperatorRealtimeAdapter) Read(ctx context.Context, principal OperatorRealtimePrincipal, tournamentID uuid.UUID, cursor *RealtimeCursor) (OperatorRealtimeResult, error) {
	if !principal.Authenticated || principal.PrincipalID == uuid.Nil {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeAuthentication
	}
	if principal.Role != OperatorRealtimeRole {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeRole
	}
	if tournamentID == uuid.Nil || principal.TournamentID == uuid.Nil || principal.TournamentID != tournamentID {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeScope
	}

	resumeCursor, queryCursor, err := operatorRealtimeCursors(cursor, tournamentID)
	if err != nil {
		return OperatorRealtimeResult{}, err
	}
	if ctx == nil || a == nil || a.source == nil {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeSource
	}

	state, err := a.source.ReadOperatorRealtime(ctx, OperatorRealtimeQuery{
		TournamentID: tournamentID,
		Cursor:       queryCursor,
		ReplayLimit:  OperatorRealtimeReplayLimit,
	})
	if err != nil {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeSource
	}
	if len(state.Events) > OperatorRealtimeReplayLimit {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeReplayLimit
	}

	state, err = operatorRealtimeCloneState(state, tournamentID)
	if err != nil {
		return OperatorRealtimeResult{}, err
	}
	resumed, err := ResumeFromCursor(CursorResumeInput{
		TournamentID: tournamentID,
		Cursor:       resumeCursor,
		Available:    state.Available,
		Events:       state.Events,
		Snapshot:     state.Snapshot,
	})
	if err != nil {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeCursor
	}
	if resumed.UsesSnapshot && len(resumed.Envelopes) != 1 {
		return OperatorRealtimeResult{}, ErrOperatorRealtimeCursor
	}
	for _, envelope := range resumed.Envelopes {
		if !operatorRealtimeEnvelopeInScope(envelope, tournamentID) {
			return OperatorRealtimeResult{}, ErrOperatorRealtimePayload
		}
	}
	return OperatorRealtimeResult(resumed), nil
}

func operatorRealtimeCursors(cursor *RealtimeCursor, tournamentID uuid.UUID) (*RealtimeCursor, *RealtimeCursor, error) {
	if cursor == nil {
		return nil, nil, nil
	}
	if cursor.SchemaVersion != ArenaRealtimeSchemaVersion || cursor.TournamentID != tournamentID || cursor.LastSequence < 1 || cursor.ProjectionRevision < 1 {
		return nil, nil, ErrOperatorRealtimeCursor
	}
	resume := *cursor
	query := *cursor
	return &resume, &query, nil
}

//nolint:gocyclo // Cloning validates every cursor and role invariant before exposing operator state.
func operatorRealtimeCloneState(state OperatorRealtimeState, tournamentID uuid.UUID) (OperatorRealtimeState, error) {
	if !state.Available.valid() || !operatorRealtimeEnvelopeInScope(state.Snapshot, tournamentID) {
		return OperatorRealtimeState{}, ErrOperatorRealtimePayload
	}
	if state.Snapshot.Sequence < state.Available.LatestSequence || state.Snapshot.ProjectionRevision < state.Available.CurrentProjectionRevision {
		return OperatorRealtimeState{}, ErrOperatorRealtimePayload
	}

	snapshot, err := cloneResumeEnvelope(state.Snapshot)
	if err != nil {
		return OperatorRealtimeState{}, ErrOperatorRealtimePayload
	}
	events := make([]RealtimeEnvelope, len(state.Events))
	var previousSequence int64
	var previousRevision int64
	for index, envelope := range state.Events {
		if !operatorRealtimeEnvelopeInScope(envelope, tournamentID) {
			return OperatorRealtimeState{}, ErrOperatorRealtimePayload
		}
		if previousSequence > 0 && (envelope.Sequence <= previousSequence || envelope.ProjectionRevision <= previousRevision) {
			return OperatorRealtimeState{}, ErrOperatorRealtimePayload
		}
		if state.Available.OldestSequence == 0 || envelope.Sequence < state.Available.OldestSequence || envelope.Sequence > state.Available.LatestSequence || envelope.ProjectionRevision > state.Available.CurrentProjectionRevision {
			return OperatorRealtimeState{}, ErrOperatorRealtimePayload
		}
		clone, cloneErr := cloneResumeEnvelope(envelope)
		if cloneErr != nil {
			return OperatorRealtimeState{}, ErrOperatorRealtimePayload
		}
		events[index] = clone
		previousSequence = envelope.Sequence
		previousRevision = envelope.ProjectionRevision
	}
	return OperatorRealtimeState{Available: state.Available, Events: events, Snapshot: snapshot}, nil
}

func operatorRealtimeEnvelopeInScope(envelope RealtimeEnvelope, tournamentID uuid.UUID) bool {
	if envelope.TournamentID != tournamentID || envelope.Operator == nil || envelope.Participant != nil || envelope.Public != nil {
		return false
	}
	return envelope.Validate() == nil
}
