package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

type RealtimeOutboxPostgres struct {
	tx *TxManager
}

func NewRealtimeOutboxPostgres(tx *TxManager) *RealtimeOutboxPostgres {
	return &RealtimeOutboxPostgres{tx: tx}
}

func (repository *RealtimeOutboxPostgres) Claim(
	ctx context.Context,
	request delivery.ClaimRequest,
) ([]delivery.Event, error) {
	if ctx == nil || repository == nil || repository.tx == nil || request.Validate() != nil {
		return nil, delivery.ErrInvalidReceipt
	}
	rows, err := repository.tx.Querier(ctx).ClaimRealtimeOutboxEvents(
		ctx,
		sqlc.ClaimRealtimeOutboxEventsParams{
			ClaimedAt: requestTime(request.ClaimedAt), BatchSize: request.Limit,
			WorkerID: nullableUUIDValue(request.WorkerID), ClaimToken: nullableUUIDValue(request.Token),
			ClaimedUntil: requestTime(request.LeaseEnds),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("realtime outbox claim: %w", err)
	}
	events := make([]delivery.Event, 0, len(rows))
	for _, row := range rows {
		event, mapErr := mapClaimedRealtimeEvent(row)
		if mapErr != nil {
			return nil, mapErr
		}
		events = append(events, event)
	}
	return events, nil
}

func (repository *RealtimeOutboxPostgres) Acknowledge(
	ctx context.Context,
	acknowledgement delivery.Acknowledgement,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || acknowledgement.Validate() != nil {
		return false, delivery.ErrInvalidReceipt
	}
	_, err := repository.tx.Querier(ctx).AcknowledgeRealtimeOutboxEvent(
		ctx,
		sqlc.AcknowledgeRealtimeOutboxEventParams{
			AcknowledgedAt: requestTime(acknowledgement.AcknowledgedAt),
			EventID:        acknowledgement.EventID, WorkerID: nullableUUIDValue(acknowledgement.WorkerID),
			ClaimToken: nullableUUIDValue(acknowledgement.ClaimToken),
		},
	)
	return mutationResult("realtime outbox acknowledge", err)
}

func (repository *RealtimeOutboxPostgres) Retry(
	ctx context.Context,
	retry delivery.Retry,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || retry.Validate() != nil {
		return false, delivery.ErrInvalidReceipt
	}
	_, err := repository.tx.Querier(ctx).RetryRealtimeOutboxEvent(
		ctx,
		sqlc.RetryRealtimeOutboxEventParams{
			AvailableAt: requestTime(retry.AvailableAt), Reason: requiredText(retry.Reason),
			EventID: retry.EventID, WorkerID: nullableUUIDValue(retry.WorkerID),
			ClaimToken: nullableUUIDValue(retry.ClaimToken),
		},
	)
	return mutationResult("realtime outbox retry", err)
}

func (repository *RealtimeOutboxPostgres) ReleaseClaims(
	ctx context.Context,
	workerID uuid.UUID,
) error {
	if ctx == nil || repository == nil || repository.tx == nil || workerID == uuid.Nil {
		return delivery.ErrInvalidReceipt
	}
	if _, err := repository.tx.Querier(ctx).ReleaseRealtimeOutboxClaims(
		ctx,
		nullableUUIDValue(workerID),
	); err != nil {
		return fmt.Errorf("realtime outbox release claims: %w", err)
	}
	return nil
}

func (repository *RealtimeOutboxPostgres) ListAfter(
	ctx context.Context,
	request delivery.ReplayRequest,
) ([]delivery.Event, error) {
	if ctx == nil || repository == nil || repository.tx == nil || request.Validate() != nil {
		return nil, delivery.ErrInvalidReceipt
	}
	rows, err := repository.tx.Querier(ctx).ListRealtimeOutboxAfter(
		ctx,
		sqlc.ListRealtimeOutboxAfterParams{
			TournamentID: request.TournamentID, AfterSequence: request.AfterSequence,
			Role: string(request.Audience), PrincipalID: audiencePrincipal(request.Audience, request.PrincipalID),
			BatchSize: request.Limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("realtime outbox replay: %w", err)
	}
	events := make([]delivery.Event, 0, len(rows))
	lastSequence := request.AfterSequence
	for _, row := range rows {
		event, mapErr := mapReplayRealtimeEvent(row)
		if mapErr != nil || event.TournamentID != request.TournamentID || event.Sequence <= lastSequence {
			return nil, fmt.Errorf("%w: invalid realtime replay row", delivery.ErrRepositoryState)
		}
		if !event.Matches(delivery.Subscriber{
			ID: event.ID, InstanceID: event.ProjectionRevisionID, ConnectionID: event.ID, ConnectionGeneration: 1,
			TournamentID: request.TournamentID,
			Audience:     request.Audience, PrincipalID: request.PrincipalID,
			AfterSequence: lastSequence, ConnectedAt: event.OccurredAt,
		}) {
			return nil, fmt.Errorf("%w: replay row escaped audience filter", delivery.ErrRepositoryState)
		}
		events = append(events, event)
		lastSequence = event.Sequence
	}
	return events, nil
}

func (repository *RealtimeOutboxPostgres) Cursor(
	ctx context.Context,
	tournamentID uuid.UUID,
) (int64, error) {
	if ctx == nil || repository == nil || repository.tx == nil || tournamentID == uuid.Nil {
		return 0, delivery.ErrInvalidReceipt
	}
	cursor, err := repository.tx.Querier(ctx).GetRealtimeOutboxCursor(ctx, tournamentID)
	if err != nil {
		return 0, fmt.Errorf("realtime outbox cursor: %w", err)
	}
	if cursor < 0 {
		return 0, fmt.Errorf("%w: negative realtime cursor", delivery.ErrRepositoryState)
	}
	return cursor, nil
}

func (repository *RealtimeOutboxPostgres) Backlog(
	ctx context.Context,
) (delivery.BacklogSnapshot, error) {
	if ctx == nil || repository == nil || repository.tx == nil {
		return delivery.BacklogSnapshot{}, delivery.ErrInvalidReceipt
	}
	row, err := repository.tx.Querier(ctx).GetRealtimeOutboxBacklog(ctx)
	if err != nil {
		return delivery.BacklogSnapshot{}, fmt.Errorf("realtime outbox backlog: %w", err)
	}
	snapshot := delivery.BacklogSnapshot{
		PendingCount:    row.PendingCount,
		OldestPendingAt: realtimeUTCOptional(row.OldestPendingAt.Time, row.OldestPendingAt.Valid),
	}
	if err := snapshot.Validate(); err != nil {
		return delivery.BacklogSnapshot{}, fmt.Errorf("%w: %w", delivery.ErrRepositoryState, err)
	}
	return snapshot, nil
}

func (repository *RealtimeOutboxPostgres) OpenSubscription(
	ctx context.Context,
	request delivery.SubscriptionOpenRequest,
) (delivery.Subscription, error) {
	if ctx == nil || repository == nil || repository.tx == nil || request.Validate() != nil {
		return delivery.Subscription{}, delivery.ErrInvalidReceipt
	}
	row, err := repository.tx.Querier(ctx).OpenRealtimeSubscription(
		ctx,
		sqlc.OpenRealtimeSubscriptionParams{
			OpenedAt: requestTime(request.OpenedAt), ResumeID: nullableUUIDValue(request.ResumeID),
			TournamentID: request.TournamentID, Role: string(request.Audience),
			PrincipalID:  audiencePrincipal(request.Audience, request.PrincipalID),
			SubscriberID: uuid.New(), InstanceID: request.InstanceID,
			ConnectionID: request.ConnectionID, AfterSequence: request.AfterSequence,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.Subscription{}, delivery.ErrSubscriberScope
	}
	if err != nil {
		return delivery.Subscription{}, fmt.Errorf("realtime subscription open: %w", err)
	}
	subscription, err := mapRealtimeSubscription(row)
	if err != nil {
		return delivery.Subscription{}, err
	}
	return subscription, nil
}

func (repository *RealtimeOutboxPostgres) CloseSubscriber(
	ctx context.Context,
	closeRequest delivery.SubscriberClose,
) error {
	if ctx == nil || repository == nil || repository.tx == nil || closeRequest.Validate() != nil {
		return delivery.ErrInvalidReceipt
	}
	return repository.tx.Do(ctx, func(txCtx context.Context) error {
		querier := repository.tx.Querier(txCtx)
		_, err := querier.CloseRealtimeSubscriber(txCtx, sqlc.CloseRealtimeSubscriberParams{
			ClosedAt: requestTime(closeRequest.ClosedAt), CloseReason: requiredText(closeRequest.Reason),
			SubscriberID: closeRequest.SubscriberID, InstanceID: closeRequest.InstanceID,
			ConnectionID: closeRequest.ConnectionID, ConnectionGeneration: closeRequest.ConnectionGeneration,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("realtime subscriber close: %w", err)
		}
		if _, err = querier.AbandonRealtimeDeliveriesForSubscriber(
			txCtx,
			sqlc.AbandonRealtimeDeliveriesForSubscriberParams{
				ClosedAt: requestTime(closeRequest.ClosedAt), SubscriberID: closeRequest.SubscriberID,
			},
		); err != nil {
			return fmt.Errorf("realtime subscriber abandon deliveries: %w", err)
		}
		return nil
	})
}

func (repository *RealtimeOutboxPostgres) ClaimDelivery(
	ctx context.Context,
	claim delivery.DeliveryClaim,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || claim.Validate() != nil {
		return false, delivery.ErrInvalidReceipt
	}
	_, err := repository.tx.Querier(ctx).ClaimRealtimeDelivery(
		ctx,
		sqlc.ClaimRealtimeDeliveryParams{
			ClaimedAt: requestTime(claim.ClaimedAt), WorkerID: nullableUUIDValue(claim.WorkerID),
			ClaimToken: nullableUUIDValue(claim.ClaimToken), ClaimedUntil: requestTime(claim.LeaseEnds),
			EventID: claim.Event.ID, Sequence: claim.Event.Sequence, SubscriberID: claim.Subscriber.ID,
			InstanceID: claim.Subscriber.InstanceID, ConnectionID: claim.Subscriber.ConnectionID,
			ConnectionGeneration: claim.Subscriber.ConnectionGeneration,
			TournamentID:         claim.Subscriber.TournamentID, Role: string(claim.Subscriber.Audience),
			PrincipalID: audiencePrincipal(claim.Subscriber.Audience, claim.Subscriber.PrincipalID),
		},
	)
	return mutationResult("realtime delivery claim", err)
}

func (repository *RealtimeOutboxPostgres) AcknowledgeDelivery(
	ctx context.Context,
	acknowledgement delivery.DeliveryAcknowledgement,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || acknowledgement.Validate() != nil {
		return false, delivery.ErrInvalidReceipt
	}
	_, err := repository.tx.Querier(ctx).AcknowledgeRealtimeDelivery(
		ctx,
		sqlc.AcknowledgeRealtimeDeliveryParams{
			AcknowledgedAt: requestTime(acknowledgement.AcknowledgedAt),
			SubscriberID:   acknowledgement.SubscriberID, ConnectionID: acknowledgement.ConnectionID,
			ConnectionGeneration: acknowledgement.ConnectionGeneration,
			InstanceID:           acknowledgement.InstanceID, EventID: acknowledgement.EventID,
			Sequence: acknowledgement.Sequence, WorkerID: nullableUUIDValue(acknowledgement.WorkerID),
			ClaimToken: nullableUUIDValue(acknowledgement.ClaimToken),
		},
	)
	return mutationResult("realtime delivery acknowledge", err)
}

func (repository *RealtimeOutboxPostgres) RetryDelivery(
	ctx context.Context,
	retry delivery.DeliveryRetry,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || retry.Validate() != nil {
		return false, delivery.ErrInvalidReceipt
	}
	_, err := repository.tx.Querier(ctx).RetryRealtimeDelivery(
		ctx,
		sqlc.RetryRealtimeDeliveryParams{
			AvailableAt: requestTime(retry.AvailableAt), Reason: requiredText(retry.Reason),
			SubscriberID: retry.SubscriberID, InstanceID: retry.InstanceID,
			ConnectionID: retry.ConnectionID, ConnectionGeneration: retry.ConnectionGeneration,
			EventID:  retry.EventID,
			WorkerID: nullableUUIDValue(retry.WorkerID), ClaimToken: nullableUUIDValue(retry.ClaimToken),
		},
	)
	return mutationResult("realtime delivery retry", err)
}

func (repository *RealtimeOutboxPostgres) DeliveryReceipt(
	ctx context.Context,
	subscriber delivery.Subscriber,
	eventID uuid.UUID,
) (delivery.DeliveryReceipt, error) {
	if ctx == nil || repository == nil || repository.tx == nil || subscriber.Validate() != nil || eventID == uuid.Nil {
		return delivery.DeliveryReceipt{}, delivery.ErrInvalidReceipt
	}
	row, err := repository.tx.Querier(ctx).GetRealtimeDeliveryReceipt(
		ctx,
		sqlc.GetRealtimeDeliveryReceiptParams{
			SubscriberID: subscriber.ID, InstanceID: subscriber.InstanceID,
			ConnectionID: subscriber.ConnectionID, ConnectionGeneration: subscriber.ConnectionGeneration,
			EventID: eventID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery.DeliveryReceipt{}, delivery.ErrClaimLost
	}
	if err != nil {
		return delivery.DeliveryReceipt{}, fmt.Errorf("realtime delivery receipt: %w", err)
	}
	receipt, err := mapRealtimeDeliveryReceipt(row)
	if err != nil {
		return delivery.DeliveryReceipt{}, err
	}
	return receipt, nil
}

func (repository *RealtimeOutboxPostgres) SubscriberCursor(
	ctx context.Context,
	subscriber delivery.Subscriber,
) (int64, error) {
	if ctx == nil || repository == nil || repository.tx == nil || subscriber.Validate() != nil {
		return 0, delivery.ErrInvalidReceipt
	}
	cursor, err := repository.tx.Querier(ctx).GetRealtimeSubscriberCursor(
		ctx,
		sqlc.GetRealtimeSubscriberCursorParams{
			SubscriberID: subscriber.ID, InstanceID: subscriber.InstanceID,
			ConnectionID: subscriber.ConnectionID, ConnectionGeneration: subscriber.ConnectionGeneration,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, delivery.ErrSubscriberScope
	}
	if err != nil {
		return 0, fmt.Errorf("realtime subscriber cursor: %w", err)
	}
	if cursor < 0 {
		return 0, fmt.Errorf("%w: negative subscriber cursor", delivery.ErrRepositoryState)
	}
	return cursor, nil
}

func mapClaimedRealtimeEvent(row sqlc.ClaimRealtimeOutboxEventsRow) (delivery.Event, error) {
	return mapRealtimeEvent(realtimeEventRow{
		ID: row.ID, TournamentID: row.TournamentID, ProjectionRevisionID: row.ProjectionRevisionID,
		CorrelationID: row.IdempotencyKey,
		Sequence:      row.Sequence, ProjectionRevision: row.ProjectionRevision,
		ProjectionOrdinal: row.ProjectionOrdinal, Terminal: row.Terminal,
		Audience: row.Audience, PrincipalID: row.PrincipalID,
		Topic: row.Topic, Payload: row.Payload, CreatedAt: row.CreatedAt.Time,
		CreatedAtValid: row.CreatedAt.Valid, AttemptCount: row.AttemptCount,
	})
}

func mapReplayRealtimeEvent(row sqlc.ListRealtimeOutboxAfterRow) (delivery.Event, error) {
	return mapRealtimeEvent(realtimeEventRow{
		ID: row.ID, TournamentID: row.TournamentID, ProjectionRevisionID: row.ProjectionRevisionID,
		CorrelationID: row.IdempotencyKey,
		Sequence:      row.Sequence, ProjectionRevision: row.ProjectionRevision,
		ProjectionOrdinal: row.ProjectionOrdinal, Terminal: row.Terminal,
		Audience: row.Audience, PrincipalID: row.PrincipalID,
		Topic: row.Topic, Payload: row.Payload, CreatedAt: row.CreatedAt.Time,
		CreatedAtValid: row.CreatedAt.Valid, AttemptCount: row.AttemptCount,
	})
}

func mapRealtimeSubscription(
	row sqlc.OpenRealtimeSubscriptionRow,
) (delivery.Subscription, error) {
	if !row.OpenedAt.Valid {
		return delivery.Subscription{}, fmt.Errorf("%w: incomplete realtime subscription row", delivery.ErrRepositoryState)
	}
	subscription := delivery.Subscription{
		Subscriber: delivery.Subscriber{
			ID:                   row.SubscriberID,
			InstanceID:           row.InstanceID,
			ConnectionID:         row.ConnectionID,
			ConnectionGeneration: row.ConnectionGeneration,
			TournamentID:         row.TournamentID,
			Audience:             delivery.Audience(row.Role),
			PrincipalID:          optionalUUIDValue(row.PrincipalID),
			AfterSequence:        row.AfterSequence,
			ConnectedAt:          row.OpenedAt.Time.UTC(),
		},
		TerminalState: delivery.TerminalReceiptState(row.TerminalState),
	}

	switch subscription.TerminalState {
	case delivery.TerminalReceiptNone:
	case delivery.TerminalReceiptWritten:
		if !validRealtimeTerminalRow(row) {
			return delivery.Subscription{}, fmt.Errorf("%w: missing written terminal row", delivery.ErrRepositoryState)
		}
	case delivery.TerminalReceiptPending:
		terminal, err := mapRealtimeSubscriptionTerminal(row)
		if err != nil {
			return delivery.Subscription{}, err
		}
		subscription.PendingTerminal = &terminal
	default:
		return delivery.Subscription{}, fmt.Errorf("%w: invalid terminal receipt state", delivery.ErrRepositoryState)
	}
	if err := subscription.Validate(); err != nil {
		return delivery.Subscription{}, fmt.Errorf("%w: %w", delivery.ErrRepositoryState, err)
	}
	return subscription, nil
}

func validRealtimeTerminalRow(row sqlc.OpenRealtimeSubscriptionRow) bool {
	return row.TerminalEventID.Valid && row.TerminalTournamentID.Valid && row.TerminalSequence != nil &&
		row.TerminalProjectionRevisionID.Valid && row.TerminalProjectionRevision != nil &&
		row.TerminalProjectionOrdinal != nil && row.TerminalTerminal != nil && row.TerminalAudience != nil &&
		row.TerminalTopic != nil && row.TerminalCreatedAt.Valid && row.TerminalAttemptCount != nil
}

func mapRealtimeSubscriptionTerminal(
	row sqlc.OpenRealtimeSubscriptionRow,
) (delivery.Event, error) {
	if !validRealtimeTerminalRow(row) || !row.TerminalCorrelationID.Valid {
		return delivery.Event{}, fmt.Errorf("%w: incomplete pending terminal row", delivery.ErrRepositoryState)
	}
	return mapRealtimeEvent(realtimeEventRow{
		ID:                   optionalUUIDValue(row.TerminalEventID),
		CorrelationID:        optionalUUIDValue(row.TerminalCorrelationID),
		TournamentID:         optionalUUIDValue(row.TerminalTournamentID),
		ProjectionRevisionID: optionalUUIDValue(row.TerminalProjectionRevisionID),
		Sequence:             *row.TerminalSequence,
		ProjectionRevision:   *row.TerminalProjectionRevision,
		ProjectionOrdinal:    *row.TerminalProjectionOrdinal,
		Terminal:             *row.TerminalTerminal,
		Audience:             *row.TerminalAudience,
		PrincipalID:          row.TerminalPrincipalID,
		Topic:                *row.TerminalTopic,
		Payload:              append([]byte(nil), row.TerminalPayload...),
		CreatedAt:            row.TerminalCreatedAt.Time,
		CreatedAtValid:       row.TerminalCreatedAt.Valid,
		AttemptCount:         *row.TerminalAttemptCount,
	})
}

type realtimeEventRow struct {
	ID                   uuid.UUID
	CorrelationID        uuid.UUID
	TournamentID         uuid.UUID
	ProjectionRevisionID uuid.UUID
	Sequence             int64
	ProjectionRevision   int64
	ProjectionOrdinal    int16
	Terminal             bool
	Audience             string
	PrincipalID          uuid.NullUUID
	Topic                string
	Payload              []byte
	CreatedAt            time.Time
	CreatedAtValid       bool
	AttemptCount         int32
}

func mapRealtimeEvent(row realtimeEventRow) (delivery.Event, error) {
	if !row.CreatedAtValid {
		return delivery.Event{}, fmt.Errorf("%w: incomplete realtime outbox row", delivery.ErrRepositoryState)
	}
	projectionBound := row.ProjectionRevisionID != uuid.Nil && row.ProjectionRevision >= 1 && row.ProjectionOrdinal >= 1
	if !projectionBound {
		return delivery.Event{}, fmt.Errorf("%w: invalid realtime projection binding", delivery.ErrRepositoryState)
	}
	event := delivery.Event{
		ID: row.ID, TournamentID: row.TournamentID, ProjectionRevisionID: row.ProjectionRevisionID,
		CorrelationID: row.CorrelationID,
		Sequence:      row.Sequence, ProjectionRevision: row.ProjectionRevision,
		ProjectionOrdinal: int32(row.ProjectionOrdinal), Terminal: row.Terminal, Audience: delivery.Audience(row.Audience),
		PrincipalID: optionalUUIDValue(row.PrincipalID), Topic: row.Topic,
		Payload: append(json.RawMessage(nil), row.Payload...), OccurredAt: row.CreatedAt.UTC(),
		AttemptCount: row.AttemptCount,
	}
	if err := event.Validate(); err != nil {
		return delivery.Event{}, fmt.Errorf("%w: %w", delivery.ErrRepositoryState, err)
	}
	return event, nil
}

func mapRealtimeDeliveryReceipt(
	row sqlc.RealtimeDeliveryReceipt,
) (delivery.DeliveryReceipt, error) {
	outcome := delivery.DeliveryPending
	if row.Outcome != nil {
		outcome = delivery.DeliveryOutcome(*row.Outcome)
	}
	terminalAt := realtimeUTCOptional(row.TerminalAt.Time, row.TerminalAt.Valid)
	if (outcome == delivery.DeliveryWritten && !row.WriteAcknowledgedAt.Valid) ||
		(outcome != delivery.DeliveryWritten && row.WriteAcknowledgedAt.Valid) {
		return delivery.DeliveryReceipt{}, fmt.Errorf(
			"%w: inconsistent realtime write receipt",
			delivery.ErrRepositoryState,
		)
	}
	receipt := delivery.DeliveryReceipt{
		SubscriberID: row.SubscriberID, EventID: row.EventID, Sequence: row.Sequence,
		Outcome: outcome, TerminalAt: terminalAt,
	}
	if err := receipt.Validate(); err != nil {
		return delivery.DeliveryReceipt{}, fmt.Errorf("%w: %w", delivery.ErrRepositoryState, err)
	}
	return receipt, nil
}

func audiencePrincipal(audience delivery.Audience, principalID uuid.UUID) uuid.NullUUID {
	if audience == delivery.AudienceParticipant || audience == delivery.AudienceOperator {
		return nullableUUIDValue(principalID)
	}
	return uuid.NullUUID{}
}

func optionalUUIDValue(value uuid.NullUUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return value.UUID
}

func realtimeUTCOptional(value time.Time, valid bool) *time.Time {
	if !valid {
		return nil
	}
	converted := value.UTC()
	return &converted
}

func requiredText(value string) *string {
	return &value
}

func requestTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func mutationResult(operation string, err error) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", operation, err)
	}
	return true, nil
}

var _ delivery.Repository = (*RealtimeOutboxPostgres)(nil)
var _ delivery.SubscriptionRepository = (*RealtimeOutboxPostgres)(nil)
var _ delivery.BacklogSource = (*RealtimeOutboxPostgres)(nil)
