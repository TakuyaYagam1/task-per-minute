package websocket

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

const (
	defaultRealtimeDeliveryLease       = 15 * time.Second
	defaultRealtimeDeliveryRetry       = 250 * time.Millisecond
	defaultRealtimeDeliveryPoll        = 250 * time.Millisecond
	defaultRealtimeDeliveryReplayBatch = int32(128)
	defaultRealtimeDeliveryStaleAfter  = 30 * time.Second
)

var (
	ErrRealtimeDeliveryConfig     = errors.New("realtime delivery: invalid configuration")
	ErrRealtimeDeliverySession    = errors.New("realtime delivery: invalid session")
	ErrRealtimeDeliveryRepository = errors.New("realtime delivery: repository failure")
	ErrRealtimeDeliveryWrite      = errors.New("realtime delivery: write failure")
	ErrRealtimeDeliveryRunning    = errors.New("realtime delivery: worker already running")
)

type RealtimeDeliveryConfig struct {
	InstanceID      uuid.UUID
	WorkerID        uuid.UUID
	LeaseDuration   time.Duration
	RetryDelay      time.Duration
	PollInterval    time.Duration
	StaleAfter      time.Duration
	ReplayBatch     int32
	Now             func() time.Time
	NewConnectionID func() uuid.UUID
	NewToken        func() uuid.UUID
}

type RealtimeDelivery struct {
	repository eventdelivery.SubscriptionRepository
	config     RealtimeDeliveryConfig

	mu       sync.RWMutex
	sessions map[uuid.UUID]*realtimeDeliverySession
	running  atomic.Bool
	ready    atomic.Bool

	healthMu sync.RWMutex
	health   eventdelivery.HealthSnapshot
}

type realtimeEventRenderer func(context.Context, eventdelivery.Event) ([]byte, error)

type realtimeSocket interface {
	Write(ctx context.Context, messageType coderws.MessageType, payload []byte) error
	Ping(ctx context.Context) error
	CloseNow() error
}

type realtimeDeliverySession struct {
	subscriber      eventdelivery.Subscriber
	connection      realtimeSocket
	scope           tournamentWriteScope
	render          realtimeEventRenderer
	pendingTerminal *eventdelivery.Event
	terminalState   eventdelivery.TerminalReceiptState

	lastSequence atomic.Int64
	deliveryMu   sync.Mutex
	writeMu      sync.Mutex
}

func NewRealtimeDelivery(
	repository eventdelivery.SubscriptionRepository,
	config RealtimeDeliveryConfig,
) (*RealtimeDelivery, error) {
	config = realtimeDeliveryDefaults(config)
	if repository == nil || !validRealtimeDeliveryConfig(config) {
		return nil, ErrRealtimeDeliveryConfig
	}
	return &RealtimeDelivery{
		repository: repository,
		config:     config,
		sessions:   make(map[uuid.UUID]*realtimeDeliverySession),
	}, nil
}

func (delivery *RealtimeDelivery) Cursor(ctx context.Context, tournamentID uuid.UUID) (int64, error) {
	if ctx == nil || delivery == nil || delivery.repository == nil || tournamentID == uuid.Nil {
		return 0, ErrRealtimeDeliveryConfig
	}
	cursor, err := delivery.repository.Cursor(ctx, tournamentID)
	if err != nil {
		return 0, fmt.Errorf("%w: read cursor: %w", ErrRealtimeDeliveryRepository, err)
	}
	if cursor < 0 {
		return 0, fmt.Errorf("%w: negative cursor", ErrRealtimeDeliveryRepository)
	}
	return cursor, nil
}

// Run continuously replays the durable outbox into sessions owned by this
// process. Every replica runs its own loop, so a global outbox claim made by
// another replica cannot strand an active local session.
func (delivery *RealtimeDelivery) Run(ctx context.Context) error {
	if ctx == nil || delivery == nil || delivery.repository == nil ||
		!validRealtimeDeliveryConfig(delivery.config) {
		return ErrRealtimeDeliveryConfig
	}
	if !delivery.running.CompareAndSwap(false, true) {
		return ErrRealtimeDeliveryRunning
	}
	delivery.markWorkerStarted(delivery.config.Now().Round(0).UTC())
	delivery.ready.Store(false)
	defer func() {
		delivery.ready.Store(false)
		delivery.running.Store(false)
		delivery.markWorkerStopped()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		attemptedAt := delivery.config.Now().Round(0).UTC()
		if err := delivery.catchUpSessions(ctx); err != nil {
			delivery.recordWorkerAttempt(attemptedAt, err)
			delivery.ready.Store(false)
			if !waitRealtimeDeliveryPoll(ctx, delivery.config.RetryDelay) {
				return nil
			}
			continue
		}
		delivery.recordWorkerAttempt(attemptedAt, nil)
		delivery.ready.Store(true)
		if !waitRealtimeDeliveryPoll(ctx, delivery.config.PollInterval) {
			return nil
		}
	}
}

func (delivery *RealtimeDelivery) Ready() bool {
	return delivery != nil && delivery.running.Load() && delivery.ready.Load()
}

// Health reports the latest realtime projection worker heartbeat. It uses the
// same contract as durable event delivery so the health endpoint can evaluate
// worker stalls and failures consistently.
func (delivery *RealtimeDelivery) Health(now time.Time) eventdelivery.HealthSnapshot {
	if delivery == nil || now.IsZero() || now.Location() != time.UTC {
		return eventdelivery.HealthSnapshot{Stale: true}
	}
	delivery.healthMu.RLock()
	snapshot := cloneRealtimeHealth(delivery.health)
	delivery.healthMu.RUnlock()
	if snapshot.Running {
		reference := snapshot.StartedAt
		if snapshot.LastSuccessAt != nil {
			reference = snapshot.LastSuccessAt
		}
		snapshot.Stale = reference == nil || now.Before(*reference) || now.Sub(*reference) > delivery.config.StaleAfter
	}
	return snapshot
}

func (delivery *RealtimeDelivery) markWorkerStarted(startedAt time.Time) {
	delivery.healthMu.Lock()
	delivery.health = eventdelivery.HealthSnapshot{
		Started:   true,
		Running:   true,
		StartedAt: realtimeTimePointer(startedAt),
	}
	delivery.healthMu.Unlock()
}

func (delivery *RealtimeDelivery) markWorkerStopped() {
	delivery.healthMu.Lock()
	delivery.health.Running = false
	delivery.healthMu.Unlock()
}

func (delivery *RealtimeDelivery) recordWorkerAttempt(attemptedAt time.Time, err error) {
	delivery.healthMu.Lock()
	delivery.health.LastAttemptAt = realtimeTimePointer(attemptedAt)
	if err == nil {
		delivery.health.LastSuccessAt = realtimeTimePointer(attemptedAt)
		delivery.health.ConsecutiveFailures = 0
	} else {
		delivery.health.LastFailureAt = realtimeTimePointer(attemptedAt)
		delivery.health.ConsecutiveFailures++
	}
	delivery.healthMu.Unlock()
}

func cloneRealtimeHealth(snapshot eventdelivery.HealthSnapshot) eventdelivery.HealthSnapshot {
	snapshot.StartedAt = cloneRealtimeTimePointer(snapshot.StartedAt)
	snapshot.LastAttemptAt = cloneRealtimeTimePointer(snapshot.LastAttemptAt)
	snapshot.LastSuccessAt = cloneRealtimeTimePointer(snapshot.LastSuccessAt)
	snapshot.LastFailureAt = cloneRealtimeTimePointer(snapshot.LastFailureAt)
	return snapshot
}

func cloneRealtimeTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return realtimeTimePointer(*value)
}

func realtimeTimePointer(value time.Time) *time.Time {
	clone := value
	return &clone
}

func (delivery *RealtimeDelivery) catchUpSessions(ctx context.Context) error {
	delivery.mu.RLock()
	sessions := make([]*realtimeDeliverySession, 0, len(delivery.sessions))
	for _, session := range delivery.sessions {
		sessions = append(sessions, session)
	}
	delivery.mu.RUnlock()

	for _, session := range sessions {
		if err := delivery.CatchUp(ctx, session); err != nil {
			if errors.Is(err, ErrRealtimeDeliveryRepository) {
				return err
			}
			_ = session.connection.CloseNow()
			if closeErr := delivery.closeSession(ctx, session, "delivery_failed"); closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func waitRealtimeDeliveryPoll(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (delivery *RealtimeDelivery) openSession(
	ctx context.Context,
	tournamentID uuid.UUID,
	audience eventdelivery.Audience,
	principalID uuid.UUID,
	afterSequence int64,
	resumeID uuid.UUID,
	connection realtimeSocket,
	scope tournamentWriteScope,
	render realtimeEventRenderer,
) (*realtimeDeliverySession, error) {
	if ctx == nil || delivery == nil || delivery.repository == nil || connection == nil || render == nil ||
		tournamentID == uuid.Nil || afterSequence < 0 || scope.validate() != nil {
		return nil, ErrRealtimeDeliverySession
	}
	request := eventdelivery.SubscriptionOpenRequest{
		ResumeID:      resumeID,
		ConnectionID:  delivery.config.NewConnectionID(),
		InstanceID:    delivery.config.InstanceID,
		TournamentID:  tournamentID,
		Audience:      audience,
		PrincipalID:   principalID,
		AfterSequence: afterSequence,
		OpenedAt:      delivery.config.Now().Round(0).UTC(),
	}
	if request.Validate() != nil {
		return nil, ErrRealtimeDeliverySession
	}
	subscription, err := delivery.repository.OpenSubscription(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("%w: open subscription: %w", ErrRealtimeDeliveryRepository, err)
	}
	if subscription.Validate() != nil || subscription.Subscriber.ConnectionID != request.ConnectionID ||
		subscription.Subscriber.InstanceID != request.InstanceID ||
		subscription.Subscriber.TournamentID != request.TournamentID ||
		subscription.Subscriber.Audience != request.Audience ||
		subscription.Subscriber.PrincipalID != request.PrincipalID ||
		subscription.Subscriber.AfterSequence < request.AfterSequence ||
		!subscriberMatchesScope(subscription.Subscriber, scope) {
		return nil, ErrRealtimeDeliverySession
	}
	subscriber := subscription.Subscriber
	var pendingTerminal *eventdelivery.Event
	if subscription.PendingTerminal != nil {
		terminal := subscription.PendingTerminal.Clone()
		pendingTerminal = &terminal
	}
	session := &realtimeDeliverySession{
		subscriber:      subscriber,
		connection:      connection,
		scope:           scope,
		render:          render,
		pendingTerminal: pendingTerminal,
		terminalState:   subscription.TerminalState,
	}
	session.lastSequence.Store(subscriber.AfterSequence)
	delivery.mu.Lock()
	previous := delivery.sessions[subscriber.ID]
	delivery.sessions[subscriber.ID] = session
	delivery.mu.Unlock()
	if previous != nil && previous != session {
		_ = previous.connection.CloseNow()
	}
	return session, nil
}

func (delivery *RealtimeDelivery) closeSession(
	ctx context.Context,
	session *realtimeDeliverySession,
	reason string,
) error {
	if ctx == nil || delivery == nil || delivery.repository == nil || session == nil {
		return ErrRealtimeDeliverySession
	}
	delivery.mu.Lock()
	current, exists := delivery.sessions[session.subscriber.ID]
	if exists && current == session {
		delete(delivery.sessions, session.subscriber.ID)
	}
	delivery.mu.Unlock()
	if !exists || current != session {
		return nil
	}
	if !terminalSubscriberCloseReason(reason) {
		return nil
	}
	session.deliveryMu.Lock()
	defer session.deliveryMu.Unlock()
	closed := eventdelivery.SubscriberClose{
		SubscriberID:         session.subscriber.ID,
		InstanceID:           session.subscriber.InstanceID,
		ConnectionID:         session.subscriber.ConnectionID,
		ConnectionGeneration: session.subscriber.ConnectionGeneration,
		ClosedAt:             delivery.config.Now().Round(0).UTC(),
		Reason:               reason,
	}
	if closed.Validate() != nil {
		return ErrRealtimeDeliverySession
	}
	if err := delivery.repository.CloseSubscriber(ctx, closed); err != nil {
		return fmt.Errorf("%w: close subscriber: %w", ErrRealtimeDeliveryRepository, err)
	}
	return nil
}

func terminalSubscriberCloseReason(reason string) bool {
	return reason == "tournament_terminal" || reason == "terminal_already_written"
}

func (delivery *RealtimeDelivery) CatchUp(ctx context.Context, session *realtimeDeliverySession) error {
	if ctx == nil || delivery == nil || session == nil {
		return ErrRealtimeDeliverySession
	}
	for {
		afterSequence := session.lastSequence.Load()
		events, err := delivery.repository.ListAfter(ctx, eventdelivery.ReplayRequest{
			TournamentID:  session.subscriber.TournamentID,
			AfterSequence: afterSequence,
			Limit:         delivery.config.ReplayBatch,
			Audience:      session.subscriber.Audience,
			PrincipalID:   session.subscriber.PrincipalID,
		})
		if err != nil {
			return fmt.Errorf("%w: replay: %w", ErrRealtimeDeliveryRepository, err)
		}
		if len(events) == 0 {
			return delivery.verifySessionFence(ctx, session)
		}
		for index := range events {
			event := events[index].Clone()
			if event.Sequence <= session.lastSequence.Load() || !event.Matches(session.currentSubscriber()) {
				return fmt.Errorf("%w: invalid replay order", ErrRealtimeDeliveryRepository)
			}
			if err := delivery.deliverSession(ctx, session, event); err != nil {
				return err
			}
			if session.lastSequence.Load() < event.Sequence {
				return nil
			}
		}
		if len(events) < int(delivery.config.ReplayBatch) {
			if !delivery.sessionActive(session) {
				return nil
			}
			return delivery.verifySessionFence(ctx, session)
		}
	}
}

// verifySessionFence makes an otherwise idle replay poll observe a durable
// cross-replica takeover. ListAfter is scoped to the event audience, not a
// subscriber fence, so an empty result alone cannot prove this connection is
// still authoritative.
func (delivery *RealtimeDelivery) verifySessionFence(ctx context.Context, session *realtimeDeliverySession) error {
	if delivery == nil || session == nil || !delivery.sessionActive(session) {
		return nil
	}
	if _, err := delivery.repository.SubscriberCursor(ctx, session.currentSubscriber()); err != nil {
		if errors.Is(err, eventdelivery.ErrSubscriberScope) {
			delivery.dropFencedSession(session)
			return nil
		}
		return fmt.Errorf("%w: verify subscriber fence: %w", ErrRealtimeDeliveryRepository, err)
	}
	return nil
}

func (delivery *RealtimeDelivery) Deliver(ctx context.Context, event eventdelivery.Event) error {
	if ctx == nil || delivery == nil || delivery.repository == nil || event.Validate() != nil {
		return ErrRealtimeDeliveryConfig
	}
	delivery.mu.RLock()
	sessions := make([]*realtimeDeliverySession, 0, len(delivery.sessions))
	for _, session := range delivery.sessions {
		if event.Matches(session.currentSubscriber()) {
			sessions = append(sessions, session)
		}
	}
	delivery.mu.RUnlock()
	var deliveryErrors []error
	for _, session := range sessions {
		if err := delivery.deliverSession(ctx, session, event.Clone()); err != nil {
			deliveryErrors = append(deliveryErrors, err)
		}
	}
	return errors.Join(deliveryErrors...)
}

func (delivery *RealtimeDelivery) deliverSession(
	ctx context.Context,
	session *realtimeDeliverySession,
	event eventdelivery.Event,
) error {
	return delivery.deliverSessionEvent(ctx, session, event, false, false)
}

func (delivery *RealtimeDelivery) deliverPendingTerminal(
	ctx context.Context,
	session *realtimeDeliverySession,
) error {
	if session == nil || session.pendingTerminal == nil {
		return nil
	}
	terminal := session.pendingTerminal.Clone()
	if !terminal.Terminal {
		return ErrRealtimeDeliverySession
	}
	return delivery.deliverSessionEvent(ctx, session, terminal, true, true)
}

func (delivery *RealtimeDelivery) deliverSessionEvent(
	ctx context.Context,
	session *realtimeDeliverySession,
	event eventdelivery.Event,
	allowTerminalAtCursor bool,
	initialTerminal bool,
) error {
	session.deliveryMu.Lock()
	defer session.deliveryMu.Unlock()
	if (event.Sequence <= session.lastSequence.Load() && (!allowTerminalAtCursor || !event.Terminal)) ||
		!delivery.sessionActive(session) {
		return nil
	}
	now := delivery.config.Now().Round(0).UTC()
	token := delivery.config.NewToken()
	claim := eventdelivery.DeliveryClaim{
		Subscriber: session.currentSubscriber(),
		Event:      event.Clone(),
		WorkerID:   delivery.config.WorkerID,
		ClaimToken: token,
		ClaimedAt:  now,
		LeaseEnds:  now.Add(delivery.config.LeaseDuration),
	}
	if claim.Validate() != nil {
		return ErrRealtimeDeliverySession
	}
	claimed, err := delivery.repository.ClaimDelivery(ctx, claim)
	if err != nil {
		return fmt.Errorf("%w: claim delivery: %w", ErrRealtimeDeliveryRepository, err)
	}
	if !claimed {
		if !delivery.sessionActive(session) {
			return nil
		}
		return delivery.resolveClaim(ctx, session, event)
	}
	frames, err := delivery.renderSessionFrames(ctx, session, event, initialTerminal)
	if err != nil {
		return delivery.retryWrite(ctx, session, event, token, "render_failed", err)
	}
	for _, data := range frames {
		if err := validateRealtimeDeliveryFrame(session.scope, data, event); err != nil {
			return delivery.retryWrite(ctx, session, event, token, "invalid_frame", err)
		}
		if err := session.write(ctx, data); err != nil {
			return delivery.retryWrite(ctx, session, event, token, "write_failed", err)
		}
	}
	acknowledged, err := delivery.repository.AcknowledgeDelivery(ctx, eventdelivery.DeliveryAcknowledgement{
		SubscriberID:         session.subscriber.ID,
		InstanceID:           session.subscriber.InstanceID,
		ConnectionID:         session.subscriber.ConnectionID,
		ConnectionGeneration: session.subscriber.ConnectionGeneration,
		EventID:              event.ID,
		WorkerID:             delivery.config.WorkerID,
		ClaimToken:           token,
		Sequence:             event.Sequence,
		AcknowledgedAt:       delivery.config.Now().Round(0).UTC(),
	})
	if err != nil {
		return fmt.Errorf("%w: acknowledge delivery: %w", ErrRealtimeDeliveryRepository, err)
	}
	if !acknowledged {
		return delivery.resolveClaim(ctx, session, event)
	}
	return delivery.completeSessionEvent(ctx, session, event)
}

func (delivery *RealtimeDelivery) renderSessionFrames(
	ctx context.Context,
	session *realtimeDeliverySession,
	event eventdelivery.Event,
	initialTerminal bool,
) ([][]byte, error) {
	if !event.Terminal {
		data, err := session.render(ctx, event.Clone())
		return singleRealtimeFrame(data, err)
	}

	state := domain.TournamentStateCompleted
	if !event.HasProjection() || event.Topic == "tournament.cancelled" {
		var terminal struct {
			State domain.TournamentState `json:"state"`
		}
		if err := decodeTournamentStrict(event.Payload, &terminal); err != nil || !terminal.State.IsTerminal() {
			return nil, ErrTournamentInvalidPayload
		}
		state = terminal.State
	}
	payload, err := NewTournamentTerminalPayload(
		event.TournamentID,
		event.Sequence,
		event.ID,
		event.OccurredAt,
		state,
	)
	if err != nil {
		return nil, err
	}
	terminalData, err := MarshalTournamentTerminal(payload)
	if err != nil {
		return nil, err
	}
	if !event.HasProjection() || initialTerminal {
		return [][]byte{terminalData}, nil
	}
	snapshotData, err := session.render(ctx, event.Clone())
	if err != nil {
		return nil, err
	}
	return [][]byte{snapshotData, terminalData}, nil
}

func singleRealtimeFrame(data []byte, err error) ([][]byte, error) {
	if err != nil {
		return nil, err
	}
	return [][]byte{data}, nil
}

func (delivery *RealtimeDelivery) completeSessionEvent(
	ctx context.Context,
	session *realtimeDeliverySession,
	event eventdelivery.Event,
) error {
	if !event.Terminal {
		advanceSessionCursor(session, event.Sequence)
		return nil
	}
	closed := eventdelivery.SubscriberClose{
		SubscriberID:         session.subscriber.ID,
		InstanceID:           session.subscriber.InstanceID,
		ConnectionID:         session.subscriber.ConnectionID,
		ConnectionGeneration: session.subscriber.ConnectionGeneration,
		ClosedAt:             delivery.config.Now().Round(0).UTC(),
		Reason:               "tournament_terminal",
	}
	if closed.Validate() != nil {
		return ErrRealtimeDeliverySession
	}
	closeErr := delivery.repository.CloseSubscriber(ctx, closed)
	delivery.mu.Lock()
	if current, exists := delivery.sessions[session.subscriber.ID]; exists && current == session {
		delete(delivery.sessions, session.subscriber.ID)
	}
	delivery.mu.Unlock()
	advanceSessionCursor(session, event.Sequence)
	_ = session.connection.CloseNow()
	if closeErr != nil {
		return fmt.Errorf("%w: close terminal subscriber: %w", ErrRealtimeDeliveryRepository, closeErr)
	}
	return nil
}

func (delivery *RealtimeDelivery) hasWrittenTerminal(session *realtimeDeliverySession) bool {
	return delivery != nil && session != nil &&
		session.terminalState == eventdelivery.TerminalReceiptWritten
}

// closeWrittenTerminal closes a connection that resumed after its terminal
// event was already acknowledged. The client receives its initial snapshot but
// never receives a duplicate terminal event.
func (delivery *RealtimeDelivery) closeWrittenTerminal(
	ctx context.Context,
	session *realtimeDeliverySession,
) error {
	if delivery == nil || session == nil {
		return ErrRealtimeDeliverySession
	}
	closeErr := delivery.closeSession(ctx, session, "terminal_already_written")
	_ = session.connection.CloseNow()
	return closeErr
}

func (delivery *RealtimeDelivery) sessionActive(session *realtimeDeliverySession) bool {
	if delivery == nil || session == nil {
		return false
	}
	delivery.mu.RLock()
	current, exists := delivery.sessions[session.subscriber.ID]
	delivery.mu.RUnlock()
	return exists && current == session
}

func (delivery *RealtimeDelivery) retryWrite(
	ctx context.Context,
	session *realtimeDeliverySession,
	event eventdelivery.Event,
	token uuid.UUID,
	reason string,
	cause error,
) error {
	retried, err := delivery.repository.RetryDelivery(ctx, eventdelivery.DeliveryRetry{
		SubscriberID:         session.subscriber.ID,
		InstanceID:           session.subscriber.InstanceID,
		ConnectionID:         session.subscriber.ConnectionID,
		ConnectionGeneration: session.subscriber.ConnectionGeneration,
		EventID:              event.ID,
		WorkerID:             delivery.config.WorkerID,
		ClaimToken:           token,
		AvailableAt:          delivery.config.Now().Round(0).UTC().Add(delivery.config.RetryDelay),
		Reason:               reason,
	})
	if err != nil {
		return errors.Join(
			fmt.Errorf("%w: %w", ErrRealtimeDeliveryWrite, cause),
			fmt.Errorf("%w: retry delivery: %w", ErrRealtimeDeliveryRepository, err),
		)
	}
	if !retried {
		if resolveErr := delivery.resolveClaim(ctx, session, event); resolveErr == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: %w", ErrRealtimeDeliveryWrite, cause)
}

func (delivery *RealtimeDelivery) resolveClaim(
	ctx context.Context,
	session *realtimeDeliverySession,
	event eventdelivery.Event,
) error {
	subscriber := session.currentSubscriber()
	cursor, cursorErr := delivery.repository.SubscriberCursor(ctx, subscriber)
	if errors.Is(cursorErr, eventdelivery.ErrSubscriberScope) {
		delivery.dropFencedSession(session)
		return nil
	}
	receipt, receiptErr := delivery.repository.DeliveryReceipt(ctx, subscriber, event.ID)
	if errors.Is(receiptErr, eventdelivery.ErrSubscriberScope) {
		delivery.dropFencedSession(session)
		return nil
	}
	if receiptErr == nil && receipt.Validate() == nil {
		switch receipt.Outcome {
		case eventdelivery.DeliveryWritten:
			return delivery.completeSessionEvent(ctx, session, event)
		case eventdelivery.DeliveryDisconnected:
			delivery.mu.Lock()
			if current, exists := delivery.sessions[session.subscriber.ID]; exists && current == session {
				delete(delivery.sessions, session.subscriber.ID)
			}
			delivery.mu.Unlock()
			advanceSessionCursor(session, receipt.Sequence)
			_ = session.connection.CloseNow()
			return nil
		case eventdelivery.DeliveryPending:
		}
	}
	if cursorErr == nil && cursor >= event.Sequence {
		advanceSessionCursor(session, cursor)
		return nil
	}
	return errors.Join(eventdelivery.ErrClaimLost, receiptErr, cursorErr)
}

func (delivery *RealtimeDelivery) dropFencedSession(session *realtimeDeliverySession) {
	if delivery == nil || session == nil {
		return
	}
	delivery.mu.Lock()
	if current, exists := delivery.sessions[session.subscriber.ID]; exists && current == session {
		delete(delivery.sessions, session.subscriber.ID)
	}
	delivery.mu.Unlock()
	_ = session.connection.CloseNow()
}

func (session *realtimeDeliverySession) currentSubscriber() eventdelivery.Subscriber {
	subscriber := session.subscriber
	subscriber.AfterSequence = session.lastSequence.Load()
	return subscriber
}

func (session *realtimeDeliverySession) resumeID() uuid.UUID {
	if session == nil {
		return uuid.Nil
	}
	return session.subscriber.ID
}

func (session *realtimeDeliverySession) write(ctx context.Context, data []byte) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	return writeTournamentMessage(ctx, session.connection, session.scope, data)
}

func (session *realtimeDeliverySession) ping(ctx context.Context) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	return writeTournamentPing(ctx, session.connection, session.scope)
}

func advanceSessionCursor(session *realtimeDeliverySession, sequence int64) {
	for current := session.lastSequence.Load(); sequence > current; current = session.lastSequence.Load() {
		if session.lastSequence.CompareAndSwap(current, sequence) {
			return
		}
	}
}

func subscriberMatchesScope(subscriber eventdelivery.Subscriber, scope tournamentWriteScope) bool {
	if subscriber.TournamentID != scope.TournamentID {
		return false
	}
	switch subscriber.Audience {
	case eventdelivery.AudienceAll:
		return false
	case eventdelivery.AudiencePublic:
		return scope.Role == TournamentRolePublic && subscriber.PrincipalID == uuid.Nil
	case eventdelivery.AudienceParticipant:
		return scope.Role == TournamentRoleParticipant && subscriber.PrincipalID == scope.ParticipantID
	case eventdelivery.AudienceOperator:
		return scope.Role == TournamentRoleOperator && subscriber.PrincipalID == scope.OperatorID
	default:
		return false
	}
}

func validateRealtimeDeliveryFrame(
	scope tournamentWriteScope,
	data []byte,
	event eventdelivery.Event,
) error {
	if err := validateTournamentWrite(scope, data); err != nil {
		return err
	}
	sequence, eventID, occurredAt, revision, terminal, err := tournamentFrameMetadata(scope.Role, data)
	if err != nil || sequence != event.Sequence || eventID != event.ID || !occurredAt.Equal(event.OccurredAt) ||
		(terminal && !event.Terminal) || (!terminal && revision < event.ProjectionRevision) {
		return ErrTournamentWriteScope
	}
	return nil
}

func tournamentFrameMetadata(
	role TournamentRole,
	data []byte,
) (int64, uuid.UUID, time.Time, int64, bool, error) {
	switch role {
	case TournamentRoleParticipant:
		message, err := DecodeTournamentParticipantMessage(data)
		if err != nil {
			return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
		}
		if message.Terminal != nil {
			terminal := message.Terminal
			return terminal.Sequence, terminal.EventID, terminal.OccurredAt, 0, true, nil
		}
		if message.Participant == nil {
			return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
		}
		envelope := message.Participant.Envelope
		return envelope.Sequence, envelope.EventID, envelope.OccurredAt, envelope.ProjectionRevision, false, nil
	case TournamentRolePublic:
		message, err := DecodeTournamentPublicMessage(data)
		if err != nil {
			return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
		}
		if message.Terminal != nil {
			terminal := message.Terminal
			return terminal.Sequence, terminal.EventID, terminal.OccurredAt, 0, true, nil
		}
		if message.Public == nil {
			return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
		}
		envelope := message.Public.Envelope
		return envelope.Sequence, envelope.EventID, envelope.OccurredAt, envelope.ProjectionRevision, false, nil
	case TournamentRoleOperator:
		message, err := DecodeTournamentOperatorMessage(data)
		if err != nil {
			return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
		}
		if message.Terminal != nil {
			terminal := message.Terminal
			return terminal.Sequence, terminal.EventID, terminal.OccurredAt, 0, true, nil
		}
		if message.Operator == nil {
			return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
		}
		envelope := message.Operator.Envelope
		return envelope.Sequence, envelope.EventID, envelope.OccurredAt, envelope.ProjectionRevision, false, nil
	default:
		return 0, uuid.Nil, time.Time{}, 0, false, ErrTournamentWriteScope
	}
}

func realtimeDeliveryDefaults(config RealtimeDeliveryConfig) RealtimeDeliveryConfig {
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultRealtimeDeliveryLease
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = defaultRealtimeDeliveryRetry
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultRealtimeDeliveryPoll
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = defaultRealtimeDeliveryStaleAfter
	}
	if config.ReplayBatch == 0 {
		config.ReplayBatch = defaultRealtimeDeliveryReplayBatch
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewConnectionID == nil {
		config.NewConnectionID = uuid.New
	}
	if config.NewToken == nil {
		config.NewToken = uuid.New
	}
	return config
}

func validRealtimeDeliveryConfig(config RealtimeDeliveryConfig) bool {
	return config.InstanceID != uuid.Nil && config.WorkerID != uuid.Nil &&
		config.LeaseDuration > 0 && config.RetryDelay > 0 && config.PollInterval > 0 &&
		config.StaleAfter > 0 &&
		config.ReplayBatch > 0 && config.ReplayBatch <= 256 &&
		config.Now != nil && config.NewConnectionID != nil && config.NewToken != nil
}

var _ eventdelivery.Sink = (*RealtimeDelivery)(nil)
var _ eventdelivery.HealthSource = (*RealtimeDelivery)(nil)
