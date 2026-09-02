package websocket

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
)

var ErrArenaFlowInvalidConfig = errors.New("invalid Arena flow config")

type ArenaParticipantFlow struct {
	source arenaws.ParticipantRealtimeReadSource
}

func NewArenaParticipantFlow(source arenaws.ParticipantRealtimeReadSource) (*ArenaParticipantFlow, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: participant source", ErrArenaFlowInvalidConfig)
	}
	return &ArenaParticipantFlow{source: source}, nil
}

func (flow *ArenaParticipantFlow) OpenArenaParticipant(
	ctx context.Context,
	request ArenaParticipantConnectionRequest,
) (ArenaParticipantPayload, error) {
	if flow == nil || flow.source == nil {
		return ArenaParticipantPayload{}, ErrArenaFlowInvalidConfig
	}
	result, err := arenaws.ParticipantRealtimeView(ctx, flow.source, arenaws.ParticipantRealtimeRequest{
		Principal:    request.Principal,
		TournamentID: request.TournamentID,
		Cursor:       request.Cursor,
	})
	if err != nil {
		return ArenaParticipantPayload{}, err
	}
	return NewArenaParticipantPayload(result)
}

type ArenaPublicFlow struct {
	adapter    *arenaws.PublicRealtimeAdapter
	sessionsMu sync.Mutex
	sessions   map[*arenaPublicSessionToken]*arenaPublicSession
}

type arenaPublicSessionContextKey struct{}

type arenaPublicSessionToken struct{}

type arenaPublicSession struct {
	mu              sync.Mutex
	connection      *arenaws.PublicRealtimeConnection
	closeRegistered bool
}

type arenaPublicPayloadConverter func(bool, []arenaws.RealtimeEnvelope) (ArenaPublicPayload, error)

func NewArenaPublicFlow(
	source arenaws.PublicRealtimeReadSource,
	config *arenaws.PublicRealtimeConfig,
) (*ArenaPublicFlow, error) {
	if source == nil || config == nil {
		return nil, fmt.Errorf("%w: public source or config", ErrArenaFlowInvalidConfig)
	}
	adapter, err := arenaws.NewPublicRealtimeAdapter(source, *config)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArenaFlowInvalidConfig, err)
	}
	return &ArenaPublicFlow{
		adapter:  adapter,
		sessions: make(map[*arenaPublicSessionToken]*arenaPublicSession),
	}, nil
}

func (flow *ArenaPublicFlow) OpenArenaPublic(
	ctx context.Context,
	request ArenaPublicConnectionRequest,
) (ArenaPublicPayload, error) {
	return flow.openArenaPublic(ctx, request, NewArenaPublicPayload)
}

func (flow *ArenaPublicFlow) openArenaPublic(
	ctx context.Context,
	request ArenaPublicConnectionRequest,
	convert arenaPublicPayloadConverter,
) (ArenaPublicPayload, error) {
	if ctx == nil || flow == nil || flow.adapter == nil || convert == nil {
		return ArenaPublicPayload{}, ErrArenaFlowInvalidConfig
	}
	token, ok := ctx.Value(arenaPublicSessionContextKey{}).(*arenaPublicSessionToken)
	if ok && token != nil {
		return flow.openArenaPublicSession(ctx, token, request, convert)
	}
	return flow.openArenaPublicOnce(ctx, request, convert)
}

func (flow *ArenaPublicFlow) openArenaPublicOnce(
	ctx context.Context,
	request ArenaPublicConnectionRequest,
	convert arenaPublicPayloadConverter,
) (ArenaPublicPayload, error) {
	connection, err := flow.adapter.PublicRealtimeOpen(ctx, arenaws.PublicRealtimeOpenRequest{
		TournamentID: request.TournamentID,
		Cursor:       request.Cursor,
	})
	if err != nil {
		return ArenaPublicPayload{}, err
	}
	context.AfterFunc(ctx, connection.PublicRealtimeClose)
	release := true
	defer func() {
		if release {
			connection.PublicRealtimeClose()
		}
	}()

	payload, err := convert(connection.PublicRealtimeUsesSnapshot(), connection.PublicRealtimeEnvelopes())
	if err != nil {
		return ArenaPublicPayload{}, err
	}
	release = false
	return payload, nil
}

func (flow *ArenaPublicFlow) openArenaPublicSession(
	ctx context.Context,
	token *arenaPublicSessionToken,
	request ArenaPublicConnectionRequest,
	convert arenaPublicPayloadConverter,
) (ArenaPublicPayload, error) {
	session := flow.publicSession(token)
	session.mu.Lock()
	defer session.mu.Unlock()

	if session.connection != nil {
		session.connection.PublicRealtimeClose()
		session.connection = nil
	}
	connection, err := flow.adapter.PublicRealtimeOpen(ctx, arenaws.PublicRealtimeOpenRequest{
		TournamentID: request.TournamentID,
		Cursor:       request.Cursor,
	})
	if err != nil {
		flow.removeEmptyPublicSession(token, session)
		return ArenaPublicPayload{}, err
	}
	payload, err := convert(connection.PublicRealtimeUsesSnapshot(), connection.PublicRealtimeEnvelopes())
	if err != nil {
		connection.PublicRealtimeClose()
		flow.removeEmptyPublicSession(token, session)
		return ArenaPublicPayload{}, err
	}
	session.connection = connection
	if !session.closeRegistered {
		session.closeRegistered = true
		context.AfterFunc(ctx, func() {
			flow.closePublicSession(token, session)
		})
	}
	return payload, nil
}

func (flow *ArenaPublicFlow) publicSession(token *arenaPublicSessionToken) *arenaPublicSession {
	flow.sessionsMu.Lock()
	defer flow.sessionsMu.Unlock()
	if session := flow.sessions[token]; session != nil {
		return session
	}
	session := &arenaPublicSession{}
	flow.sessions[token] = session
	return session
}

func (flow *ArenaPublicFlow) removeEmptyPublicSession(
	token *arenaPublicSessionToken,
	session *arenaPublicSession,
) {
	if session.connection != nil || session.closeRegistered {
		return
	}
	flow.sessionsMu.Lock()
	defer flow.sessionsMu.Unlock()
	if flow.sessions[token] == session {
		delete(flow.sessions, token)
	}
}

func (flow *ArenaPublicFlow) closePublicSession(
	token *arenaPublicSessionToken,
	session *arenaPublicSession,
) {
	flow.sessionsMu.Lock()
	if flow.sessions[token] != session {
		flow.sessionsMu.Unlock()
		return
	}
	delete(flow.sessions, token)
	flow.sessionsMu.Unlock()

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.connection != nil {
		session.connection.PublicRealtimeClose()
		session.connection = nil
	}
}

func withArenaPublicSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, arenaPublicSessionContextKey{}, &arenaPublicSessionToken{})
}

type ArenaOperatorFlow struct {
	adapter *arenaws.OperatorRealtimeAdapter
}

func NewArenaOperatorFlow(source arenaws.OperatorRealtimeReadSource) (*ArenaOperatorFlow, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: operator source", ErrArenaFlowInvalidConfig)
	}
	return &ArenaOperatorFlow{adapter: arenaws.NewOperatorRealtimeAdapter(source)}, nil
}

func (flow *ArenaOperatorFlow) OpenArenaOperator(
	ctx context.Context,
	request ArenaOperatorConnectionRequest,
) (ArenaOperatorPayload, error) {
	if flow == nil || flow.adapter == nil {
		return ArenaOperatorPayload{}, ErrArenaFlowInvalidConfig
	}
	result, err := flow.adapter.Read(ctx, request.Principal, request.TournamentID, request.Cursor)
	if err != nil {
		return ArenaOperatorPayload{}, err
	}
	return NewArenaOperatorPayload(result)
}

type ArenaTerminalRegistry struct {
	mu           sync.RWMutex
	coordinators map[uuid.UUID]*arenaws.CancellationCoordinator
}

func NewArenaTerminalRegistry(
	seeds map[uuid.UUID]*arenaws.CancellationCoordinator,
) (*ArenaTerminalRegistry, error) {
	registry := &ArenaTerminalRegistry{
		coordinators: make(map[uuid.UUID]*arenaws.CancellationCoordinator, len(seeds)),
	}
	for tournamentID, coordinator := range seeds {
		if tournamentID == uuid.Nil || coordinator == nil {
			return nil, fmt.Errorf("%w: terminal seed", ErrArenaFlowInvalidConfig)
		}
		registry.coordinators[tournamentID] = coordinator
	}
	return registry, nil
}

func (registry *ArenaTerminalRegistry) Lookup(tournamentID uuid.UUID) (*arenaws.CancellationCoordinator, bool) {
	if registry == nil || tournamentID == uuid.Nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	coordinator, ok := registry.coordinators[tournamentID]
	return coordinator, ok && coordinator != nil
}

func (registry *ArenaTerminalRegistry) Coordinator(tournamentID uuid.UUID) (*arenaws.CancellationCoordinator, error) {
	if registry == nil || tournamentID == uuid.Nil {
		return nil, arenaws.ErrCancellationInvalidScope
	}
	if coordinator, ok := registry.Lookup(tournamentID); ok {
		return coordinator, nil
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if coordinator := registry.coordinators[tournamentID]; coordinator != nil {
		return coordinator, nil
	}
	if registry.coordinators == nil {
		registry.coordinators = make(map[uuid.UUID]*arenaws.CancellationCoordinator)
	}
	coordinator, err := arenaws.NewCancellationCoordinator(tournamentID)
	if err != nil {
		return nil, err
	}
	registry.coordinators[tournamentID] = coordinator
	return coordinator, nil
}

type ArenaTerminalFlow struct {
	registry *ArenaTerminalRegistry
}

func NewArenaTerminalFlow(registry *ArenaTerminalRegistry) (*ArenaTerminalFlow, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: terminal registry", ErrArenaFlowInvalidConfig)
	}
	return &ArenaTerminalFlow{registry: registry}, nil
}

func (flow *ArenaTerminalFlow) SubscribeArenaTerminal(
	ctx context.Context,
	request ArenaTerminalSubscriptionRequest,
) (ArenaTerminalSubscription, error) {
	if ctx == nil || flow == nil || flow.registry == nil {
		return nil, arenaws.ErrCancellationInvalidSubscription
	}
	subscriptionRequest, err := arenaCancellationSubscriptionRequest(request)
	if err != nil {
		return nil, err
	}
	coordinator, err := flow.registry.Coordinator(request.TournamentID)
	if err != nil {
		return nil, err
	}
	subscription, err := coordinator.CancellationSubscribe(subscriptionRequest)
	if err != nil {
		return nil, err
	}
	result := &arenaTerminalSubscription{subscription: subscription}
	context.AfterFunc(ctx, result.Close)
	return result, nil
}

func arenaCancellationSubscriptionRequest(
	request ArenaTerminalSubscriptionRequest,
) (arenaws.CancellationSubscriptionRequest, error) {
	if request.TournamentID == uuid.Nil {
		return arenaws.CancellationSubscriptionRequest{}, arenaws.ErrCancellationInvalidSubscription
	}
	result := arenaws.CancellationSubscriptionRequest{
		TournamentID: request.TournamentID,
	}
	switch request.Role {
	case ArenaRoleParticipant:
		if !request.Authenticated || request.ParticipantID == uuid.Nil {
			return arenaws.CancellationSubscriptionRequest{}, arenaws.ErrCancellationInvalidSubscription
		}
		result.Role = arenaws.CancellationRoleParticipant
		result.Authenticated = true
		result.ParticipantID = request.ParticipantID
	case ArenaRolePublic:
		if request.Authenticated || request.ParticipantID != uuid.Nil {
			return arenaws.CancellationSubscriptionRequest{}, arenaws.ErrCancellationInvalidSubscription
		}
		result.Role = arenaws.CancellationRolePublic
	case ArenaRoleOperator:
		if !request.Authenticated {
			return arenaws.CancellationSubscriptionRequest{}, arenaws.ErrCancellationInvalidSubscription
		}
		result.Role = arenaws.CancellationRoleOperator
		result.Authenticated = true
	default:
		return arenaws.CancellationSubscriptionRequest{}, arenaws.ErrCancellationInvalidSubscription
	}
	return result, nil
}

type arenaTerminalSubscription struct {
	subscription *arenaws.CancellationSubscription
}

func (subscription *arenaTerminalSubscription) Deliveries() <-chan arenaws.CancellationDelivery {
	if subscription == nil || subscription.subscription == nil {
		return nil
	}
	return subscription.subscription.CancellationDeliveries()
}

func (subscription *arenaTerminalSubscription) Close() {
	if subscription == nil || subscription.subscription == nil {
		return
	}
	subscription.subscription.CancellationClose()
}

var (
	_ ArenaParticipantConnectionFlow = (*ArenaParticipantFlow)(nil)
	_ ArenaPublicConnectionFlow      = (*ArenaPublicFlow)(nil)
	_ ArenaOperatorConnectionFlow    = (*ArenaOperatorFlow)(nil)
	_ ArenaTerminalSubscriptionFlow  = (*ArenaTerminalFlow)(nil)
	_ ArenaTerminalSubscription      = (*arenaTerminalSubscription)(nil)
)
