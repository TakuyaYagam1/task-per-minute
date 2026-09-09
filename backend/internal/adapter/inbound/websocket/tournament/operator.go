package tournament

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const OperatorRealtimeRole = "operator"

var (
	ErrOperatorRealtimeAuthentication = errors.New("operator realtime authentication required")
	ErrOperatorRealtimeRole           = errors.New("operator realtime role forbidden")
	ErrOperatorRealtimeScope          = errors.New("operator realtime tournament scope mismatch")
	ErrOperatorRealtimePayload        = errors.New("operator realtime source payload invalid")
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
	OperatorID   uuid.UUID
}

type OperatorRealtimeReadSource interface {
	ReadOperatorRealtime(ctx context.Context, query OperatorRealtimeQuery) (RealtimeEnvelope, error)
}

type OperatorRealtimeAdapter struct {
	source OperatorRealtimeReadSource
}

func NewOperatorRealtimeAdapter(source OperatorRealtimeReadSource) *OperatorRealtimeAdapter {
	return &OperatorRealtimeAdapter{source: source}
}

func (a *OperatorRealtimeAdapter) Read(
	ctx context.Context,
	principal OperatorRealtimePrincipal,
	tournamentID uuid.UUID,
) (RealtimeEnvelope, error) {
	if err := validateOperatorRealtimeRequest(principal, tournamentID); err != nil {
		return RealtimeEnvelope{}, err
	}
	if ctx == nil || a == nil || a.source == nil {
		return RealtimeEnvelope{}, ErrOperatorRealtimeSource
	}

	envelope, err := a.source.ReadOperatorRealtime(ctx, OperatorRealtimeQuery{
		TournamentID: tournamentID,
		OperatorID:   principal.PrincipalID,
	})
	if err != nil {
		return RealtimeEnvelope{}, ErrOperatorRealtimeSource
	}
	if !validOperatorRealtimeEnvelope(envelope, tournamentID) {
		return RealtimeEnvelope{}, ErrOperatorRealtimePayload
	}
	clone, err := cloneRealtimeEnvelope(envelope)
	if err != nil {
		return RealtimeEnvelope{}, ErrOperatorRealtimePayload
	}
	return clone, nil
}

func validateOperatorRealtimeRequest(
	principal OperatorRealtimePrincipal,
	tournamentID uuid.UUID,
) error {
	if !principal.Authenticated || principal.PrincipalID == uuid.Nil {
		return ErrOperatorRealtimeAuthentication
	}
	if principal.Role != OperatorRealtimeRole {
		return ErrOperatorRealtimeRole
	}
	if tournamentID == uuid.Nil || principal.TournamentID == uuid.Nil || principal.TournamentID != tournamentID {
		return ErrOperatorRealtimeScope
	}
	return nil
}

func validOperatorRealtimeEnvelope(envelope RealtimeEnvelope, tournamentID uuid.UUID) bool {
	return envelope.TournamentID == tournamentID && envelope.Operator != nil &&
		envelope.Participant == nil && envelope.Public == nil && envelope.Validate() == nil
}
