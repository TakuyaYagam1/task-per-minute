package websocket

import (
	"context"
	"errors"
	"fmt"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
)

var ErrTournamentFlowInvalidConfig = errors.New("invalid tournament flow config")

type TournamentParticipantFlow struct {
	source tournamentws.ParticipantRealtimeReadSource
}

func NewTournamentParticipantFlow(
	source tournamentws.ParticipantRealtimeReadSource,
) (*TournamentParticipantFlow, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: participant source", ErrTournamentFlowInvalidConfig)
	}
	return &TournamentParticipantFlow{source: source}, nil
}

func (flow *TournamentParticipantFlow) OpenTournamentParticipant(
	ctx context.Context,
	request TournamentParticipantConnectionRequest,
) (TournamentParticipantPayload, error) {
	if flow == nil || flow.source == nil {
		return TournamentParticipantPayload{}, ErrTournamentFlowInvalidConfig
	}
	envelope, err := tournamentws.ParticipantRealtimeView(ctx, flow.source, tournamentws.ParticipantRealtimeRequest{
		Principal:    request.Principal,
		TournamentID: request.TournamentID,
	})
	if err != nil {
		return TournamentParticipantPayload{}, err
	}
	return NewTournamentParticipantPayload(envelope)
}

type TournamentPublicFlow struct {
	adapter *tournamentws.PublicRealtimeAdapter
}

func NewTournamentPublicFlow(
	source tournamentws.PublicRealtimeReadSource,
	config *tournamentws.PublicRealtimeConfig,
) (*TournamentPublicFlow, error) {
	if source == nil || config == nil {
		return nil, fmt.Errorf("%w: public source or config", ErrTournamentFlowInvalidConfig)
	}
	adapter, err := tournamentws.NewPublicRealtimeAdapter(source, *config)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTournamentFlowInvalidConfig, err)
	}
	return &TournamentPublicFlow{adapter: adapter}, nil
}

func (flow *TournamentPublicFlow) OpenTournamentPublic(
	ctx context.Context,
	request TournamentPublicConnectionRequest,
) (TournamentPublicPayload, error) {
	if ctx == nil || flow == nil || flow.adapter == nil {
		return TournamentPublicPayload{}, ErrTournamentFlowInvalidConfig
	}
	connection, err := flow.adapter.PublicRealtimeOpen(ctx, tournamentws.PublicRealtimeOpenRequest{
		TournamentID: request.TournamentID,
	})
	if err != nil {
		return TournamentPublicPayload{}, err
	}
	context.AfterFunc(ctx, connection.PublicRealtimeClose)
	payload, err := NewTournamentPublicPayload(connection.PublicRealtimeEnvelope())
	if err != nil {
		connection.PublicRealtimeClose()
		return TournamentPublicPayload{}, err
	}
	return payload, nil
}

type TournamentOperatorFlow struct {
	adapter *tournamentws.OperatorRealtimeAdapter
}

func NewTournamentOperatorFlow(
	source tournamentws.OperatorRealtimeReadSource,
) (*TournamentOperatorFlow, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: operator source", ErrTournamentFlowInvalidConfig)
	}
	return &TournamentOperatorFlow{adapter: tournamentws.NewOperatorRealtimeAdapter(source)}, nil
}

func (flow *TournamentOperatorFlow) OpenTournamentOperator(
	ctx context.Context,
	request TournamentOperatorConnectionRequest,
) (TournamentOperatorPayload, error) {
	if flow == nil || flow.adapter == nil {
		return TournamentOperatorPayload{}, ErrTournamentFlowInvalidConfig
	}
	envelope, err := flow.adapter.Read(ctx, request.Principal, request.TournamentID)
	if err != nil {
		return TournamentOperatorPayload{}, err
	}
	return NewTournamentOperatorPayload(envelope)
}

var (
	_ TournamentParticipantConnectionFlow = (*TournamentParticipantFlow)(nil)
	_ TournamentPublicConnectionFlow      = (*TournamentPublicFlow)(nil)
	_ TournamentOperatorConnectionFlow    = (*TournamentOperatorFlow)(nil)
)
