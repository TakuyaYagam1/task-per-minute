//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

func TestRealtimeSubscriptionPostgresFencesStaleConnections(t *testing.T) {
	ctx := context.Background()
	fixture := createDraftMigrationFixture(ctx, t)
	tournamentID := fixture.tournamentID
	rosterID := fixture.rosterID
	at := time.Now().UTC().Truncate(time.Microsecond)
	terminal := createRealtimeCancellationTerminal(ctx, t, tournamentID, rosterID, at)

	firstRepository := postgres.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
	secondRepository := postgres.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
	firstRequest := realtimeSubscriptionRequest(tournamentID, uuid.New(), uuid.New(), uuid.Nil, at)
	first, err := firstRepository.OpenSubscription(ctx, firstRequest)
	require.NoError(t, err)
	require.Equal(t, int64(0), first.Subscriber.AfterSequence)
	require.Equal(t, int64(1), first.Subscriber.ConnectionGeneration)
	require.Equal(t, delivery.TerminalReceiptPending, first.TerminalState)
	require.NotNil(t, first.PendingTerminal)
	require.Equal(t, terminal.ID, first.PendingTerminal.ID)

	wrongTournament := firstRequest
	wrongTournament.ResumeID = first.Subscriber.ID
	wrongTournament.TournamentID = createMigrationTournament(ctx, t)
	wrongTournament.ConnectionID = uuid.New()
	wrongTournament.InstanceID = uuid.New()
	_, err = secondRepository.OpenSubscription(ctx, wrongTournament)
	require.ErrorIs(t, err, delivery.ErrSubscriberScope)

	wrongRole := firstRequest
	wrongRole.ResumeID = first.Subscriber.ID
	wrongRole.Audience = delivery.AudienceParticipant
	wrongRole.PrincipalID = uuid.New()
	wrongRole.ConnectionID = uuid.New()
	wrongRole.InstanceID = uuid.New()
	_, err = secondRepository.OpenSubscription(ctx, wrongRole)
	require.ErrorIs(t, err, delivery.ErrSubscriberScope)

	participant := realtimeSubscriptionRequest(tournamentID, uuid.New(), uuid.New(), uuid.Nil, at)
	participant.Audience = delivery.AudienceParticipant
	participant.PrincipalID = uuid.New()
	participantSubscription, err := firstRepository.OpenSubscription(ctx, participant)
	require.NoError(t, err)
	wrongPrincipal := participant
	wrongPrincipal.ResumeID = participantSubscription.Subscriber.ID
	wrongPrincipal.PrincipalID = uuid.New()
	wrongPrincipal.ConnectionID = uuid.New()
	wrongPrincipal.InstanceID = uuid.New()
	_, err = secondRepository.OpenSubscription(ctx, wrongPrincipal)
	require.ErrorIs(t, err, delivery.ErrSubscriberScope)

	secondRequest := firstRequest
	secondRequest.ResumeID = first.Subscriber.ID
	secondRequest.ConnectionID = uuid.New()
	secondRequest.InstanceID = uuid.New()
	secondRequest.OpenedAt = at.Add(time.Second)
	second, err := secondRepository.OpenSubscription(ctx, secondRequest)
	require.NoError(t, err)
	require.Equal(t, first.Subscriber.ID, second.Subscriber.ID)
	require.Equal(t, int64(2), second.Subscriber.ConnectionGeneration)
	require.Equal(t, int64(0), second.Subscriber.AfterSequence)
	require.Equal(t, delivery.TerminalReceiptPending, second.TerminalState)

	firstClaim := realtimeDeliveryClaim(first.Subscriber, terminal, at, uuid.New())
	claimed, err := firstRepository.ClaimDelivery(ctx, firstClaim)
	require.NoError(t, err)
	require.False(t, claimed)
	acknowledged, err := firstRepository.AcknowledgeDelivery(ctx, realtimeDeliveryAcknowledgement(first.Subscriber, terminal, firstClaim))
	require.NoError(t, err)
	require.False(t, acknowledged)
	retried, err := firstRepository.RetryDelivery(ctx, realtimeDeliveryRetry(first.Subscriber, terminal, firstClaim, at.Add(time.Second)))
	require.NoError(t, err)
	require.False(t, retried)
	require.NoError(t, firstRepository.CloseSubscriber(ctx, realtimeSubscriberClose(first.Subscriber, at)))
	assertRealtimeSubscriberOpen(ctx, t, first.Subscriber.ID)

	secondClaim := realtimeDeliveryClaim(second.Subscriber, terminal, at, uuid.New())
	claimed, err = secondRepository.ClaimDelivery(ctx, secondClaim)
	require.NoError(t, err)
	require.True(t, claimed)

	thirdRequest := secondRequest
	thirdRequest.ConnectionID = uuid.New()
	thirdRequest.InstanceID = uuid.New()
	thirdRequest.OpenedAt = at.Add(2 * time.Second)
	third, err := firstRepository.OpenSubscription(ctx, thirdRequest)
	require.NoError(t, err)
	require.Equal(t, int64(3), third.Subscriber.ConnectionGeneration)

	secondClaimStale, err := secondRepository.ClaimDelivery(ctx, secondClaim)
	require.NoError(t, err)
	require.False(t, secondClaimStale)
	acknowledged, err = secondRepository.AcknowledgeDelivery(ctx, realtimeDeliveryAcknowledgement(second.Subscriber, terminal, secondClaim))
	require.NoError(t, err)
	require.False(t, acknowledged)
	retried, err = secondRepository.RetryDelivery(ctx, realtimeDeliveryRetry(second.Subscriber, terminal, secondClaim, at.Add(2*time.Second)))
	require.NoError(t, err)
	require.False(t, retried)
	require.NoError(t, secondRepository.CloseSubscriber(ctx, realtimeSubscriberClose(second.Subscriber, at.Add(2*time.Second))))
	assertRealtimeSubscriberOpen(ctx, t, second.Subscriber.ID)

	thirdClaim := realtimeDeliveryClaim(third.Subscriber, terminal, at.Add(2*time.Second), uuid.New())
	claimed, err = firstRepository.ClaimDelivery(ctx, thirdClaim)
	require.NoError(t, err)
	require.True(t, claimed, "current fence can reclaim after the prior lease expires")
	acknowledged, err = firstRepository.AcknowledgeDelivery(ctx, realtimeDeliveryAcknowledgement(third.Subscriber, terminal, thirdClaim))
	require.NoError(t, err)
	require.True(t, acknowledged)
	require.NoError(t, firstRepository.CloseSubscriber(ctx, realtimeSubscriberClose(third.Subscriber, at.Add(3*time.Second))))

	writtenRequest := thirdRequest
	writtenRequest.ConnectionID = uuid.New()
	writtenRequest.InstanceID = uuid.New()
	writtenRequest.OpenedAt = at.Add(4 * time.Second)
	written, err := secondRepository.OpenSubscription(ctx, writtenRequest)
	require.NoError(t, err)
	require.Equal(t, delivery.TerminalReceiptWritten, written.TerminalState)
	require.Nil(t, written.PendingTerminal)
	require.Equal(t, int64(4), written.Subscriber.ConnectionGeneration)

	newSubscriber := realtimeSubscriptionRequest(tournamentID, uuid.New(), uuid.New(), uuid.Nil, at.Add(5*time.Second))
	pending, err := firstRepository.OpenSubscription(ctx, newSubscriber)
	require.NoError(t, err)
	require.NotEqual(t, first.Subscriber.ID, pending.Subscriber.ID)
	require.Equal(t, delivery.TerminalReceiptPending, pending.TerminalState)
	require.NotNil(t, pending.PendingTerminal)
}

func TestRealtimeSubscriptionPostgresSerializesConcurrentResume(t *testing.T) {
	ctx := context.Background()
	fixture := createDraftMigrationFixture(ctx, t)
	tournamentID := fixture.tournamentID
	rosterID := fixture.rosterID
	at := time.Now().UTC().Truncate(time.Microsecond)
	terminal := createRealtimeCancellationTerminal(ctx, t, tournamentID, rosterID, at)

	baseRepository := postgres.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
	first, err := baseRepository.OpenSubscription(ctx, realtimeSubscriptionRequest(tournamentID, uuid.New(), uuid.New(), uuid.Nil, at))
	require.NoError(t, err)

	requests := []delivery.SubscriptionOpenRequest{
		realtimeSubscriptionRequest(tournamentID, uuid.New(), uuid.New(), first.Subscriber.ID, at.Add(time.Second)),
		realtimeSubscriptionRequest(tournamentID, uuid.New(), uuid.New(), first.Subscriber.ID, at.Add(time.Second)),
	}
	results := make(chan delivery.Subscription, len(requests))
	errorsByResume := make(chan error, len(requests))
	var workers sync.WaitGroup
	for _, request := range requests {
		workers.Add(1)
		go func(request delivery.SubscriptionOpenRequest) {
			defer workers.Done()
			repository := postgres.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
			subscription, openErr := repository.OpenSubscription(ctx, request)
			if openErr != nil {
				errorsByResume <- openErr
				return
			}
			results <- subscription
		}(request)
	}
	workers.Wait()
	close(results)
	close(errorsByResume)
	for resumeErr := range errorsByResume {
		require.NoError(t, resumeErr)
	}

	resumed := make(map[int64]delivery.Subscription)
	for subscription := range results {
		require.Equal(t, first.Subscriber.ID, subscription.Subscriber.ID)
		resumed[subscription.Subscriber.ConnectionGeneration] = subscription
	}
	require.Len(t, resumed, 2)
	require.Contains(t, resumed, int64(2))
	require.Contains(t, resumed, int64(3))

	var (
		connectionID uuid.UUID
		generation   int64
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT connection_id, connection_generation
		FROM realtime_subscribers
		WHERE id = $1`, first.Subscriber.ID).Scan(&connectionID, &generation)
	require.NoError(t, err)
	require.Equal(t, int64(3), generation)
	require.Equal(t, resumed[generation].Subscriber.ConnectionID, connectionID)

	stale := resumed[int64(2)].Subscriber
	current := resumed[int64(3)].Subscriber
	staleClaim := realtimeDeliveryClaim(stale, terminal, at.Add(2*time.Second), uuid.New())
	claimed, err := baseRepository.ClaimDelivery(ctx, staleClaim)
	require.NoError(t, err)
	require.False(t, claimed)
	currentClaim := realtimeDeliveryClaim(current, terminal, at.Add(2*time.Second), uuid.New())
	claimed, err = baseRepository.ClaimDelivery(ctx, currentClaim)
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestRealtimeCancellationTerminalUsesExactProjectionSource(t *testing.T) {
	ctx := context.Background()
	fixture := createDraftMigrationFixture(ctx, t)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	terminal := createRealtimeCancellationTerminal(
		ctx,
		t,
		fixture.tournamentID,
		fixture.rosterID,
		createdAt,
	)

	var (
		eventProjectionID     uuid.UUID
		eventProjectionNo     int64
		eventProjectionOrder  int16
		sourceProjectionID    uuid.UUID
		sourceProjectionNo    int64
		sourceProjectionOrder int16
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT event.projection_revision_id,
			projection.revision_number,
			event.projection_ordinal,
			source.projection_revision_id,
			source.projection_revision,
			source.projection_ordinal
		FROM outbox_events AS event
		INNER JOIN projection_revisions AS projection
			ON projection.id = event.projection_revision_id
			AND projection.tournament_id = event.tournament_id
			AND projection.roster_id = event.roster_id
		INNER JOIN outbox_tournament_cancellation_sources AS source
			ON source.outbox_event_id = event.id
			AND source.tournament_id = event.tournament_id
			AND source.roster_id = event.roster_id
		WHERE event.id = $1`, terminal.ID,
	).Scan(
		&eventProjectionID,
		&eventProjectionNo,
		&eventProjectionOrder,
		&sourceProjectionID,
		&sourceProjectionNo,
		&sourceProjectionOrder,
	)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, eventProjectionID)
	require.GreaterOrEqual(t, eventProjectionNo, int64(1))
	require.GreaterOrEqual(t, eventProjectionOrder, int16(1))
	require.Equal(t, eventProjectionID, sourceProjectionID)
	require.Equal(t, eventProjectionNo, sourceProjectionNo)
	require.Equal(t, eventProjectionOrder, sourceProjectionOrder)

	t.Run("event target is mandatory", func(t *testing.T) {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO outbox_events (
				id, tournament_id, roster_id, projection_revision,
				sequence, projection_ordinal, terminal, idempotency_key,
				audience, topic, payload, created_at, available_at
			)
			VALUES ($1, $2, $3, 1, 2, 2, true, $4,
				'all', 'tournament.cancelled', '{"state":"cancelled"}'::jsonb, $5, $5)`,
			uuid.New(), fixture.tournamentID, fixture.rosterID, uuid.New(), createdAt,
		)
		require.Error(t, err)
	})

	t.Run("source cannot target another projection ordinal", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		eventID := uuid.New()
		idempotencyKey := uuid.New()
		var cancellationCommandID uuid.UUID
		err = tx.QueryRow(ctx, `
			SELECT command_id
			FROM tournament_cancellations
			WHERE outbox_event_id = $1`, terminal.ID,
		).Scan(&cancellationCommandID)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_events (
				id, tournament_id, roster_id, projection_revision_id, projection_revision,
				sequence, projection_ordinal, terminal, idempotency_key,
				audience, topic, payload, created_at, available_at
			)
			VALUES ($1, $2, $3, $4, $5, 2, 2, true, $6,
				'all', 'tournament.cancelled', '{"state":"cancelled"}'::jsonb, $7, $7)`,
			eventID,
			fixture.tournamentID,
			fixture.rosterID,
			eventProjectionID,
			eventProjectionNo,
			idempotencyKey,
			createdAt.Add(time.Second),
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox_tournament_cancellation_sources (
				outbox_event_id, tournament_id, roster_id, cancellation_command_id,
				projection_revision_id, projection_revision, projection_ordinal, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 1, $7)`,
			eventID,
			fixture.tournamentID,
			fixture.rosterID,
			cancellationCommandID,
			eventProjectionID,
			eventProjectionNo,
			createdAt.Add(time.Second),
		)
		require.Error(t, err)
	})
}

func realtimeSubscriptionRequest(
	tournamentID uuid.UUID,
	instanceID uuid.UUID,
	connectionID uuid.UUID,
	resumeID uuid.UUID,
	openedAt time.Time,
) delivery.SubscriptionOpenRequest {
	return delivery.SubscriptionOpenRequest{
		ResumeID: resumeID, ConnectionID: connectionID, InstanceID: instanceID,
		TournamentID: tournamentID, Audience: delivery.AudiencePublic,
		AfterSequence: 0, OpenedAt: openedAt,
	}
}

func realtimeDeliveryClaim(
	subscriber delivery.Subscriber,
	event delivery.Event,
	claimedAt time.Time,
	token uuid.UUID,
) delivery.DeliveryClaim {
	return delivery.DeliveryClaim{
		Subscriber: subscriber, Event: event, WorkerID: uuid.New(), ClaimToken: token,
		ClaimedAt: claimedAt, LeaseEnds: claimedAt.Add(time.Second),
	}
}

func realtimeDeliveryAcknowledgement(
	subscriber delivery.Subscriber,
	event delivery.Event,
	claim delivery.DeliveryClaim,
) delivery.DeliveryAcknowledgement {
	return delivery.DeliveryAcknowledgement{
		SubscriberID: subscriber.ID, InstanceID: subscriber.InstanceID,
		ConnectionID: subscriber.ConnectionID, ConnectionGeneration: subscriber.ConnectionGeneration,
		EventID: event.ID, WorkerID: claim.WorkerID, ClaimToken: claim.ClaimToken,
		Sequence: event.Sequence, AcknowledgedAt: claim.ClaimedAt,
	}
}

func realtimeDeliveryRetry(
	subscriber delivery.Subscriber,
	event delivery.Event,
	claim delivery.DeliveryClaim,
	availableAt time.Time,
) delivery.DeliveryRetry {
	return delivery.DeliveryRetry{
		SubscriberID: subscriber.ID, InstanceID: subscriber.InstanceID,
		ConnectionID: subscriber.ConnectionID, ConnectionGeneration: subscriber.ConnectionGeneration,
		EventID: event.ID, WorkerID: claim.WorkerID, ClaimToken: claim.ClaimToken,
		AvailableAt: availableAt, Reason: "write_failed",
	}
}

func realtimeSubscriberClose(subscriber delivery.Subscriber, closedAt time.Time) delivery.SubscriberClose {
	return delivery.SubscriberClose{
		SubscriberID: subscriber.ID, InstanceID: subscriber.InstanceID,
		ConnectionID: subscriber.ConnectionID, ConnectionGeneration: subscriber.ConnectionGeneration,
		ClosedAt: closedAt, Reason: "tournament_terminal",
	}
}

func assertRealtimeSubscriberOpen(ctx context.Context, t *testing.T, subscriberID uuid.UUID) {
	t.Helper()
	var closedAt *time.Time
	err := sharedPool.QueryRow(ctx, `
		SELECT closed_at
		FROM realtime_subscribers
		WHERE id = $1`, subscriberID).Scan(&closedAt)
	require.NoError(t, err)
	require.Nil(t, closedAt)
}

func createRealtimeCancellationTerminal(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) delivery.Event {
	t.Helper()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	eventID := uuid.New()
	idempotencyKey := uuid.New()
	commandID := uuid.New()
	auditID := uuid.New()
	actorID := uuid.New()
	var projectionRevisionID uuid.UUID
	var projectionRevision int64
	err = tx.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
			AND state = 'published'
		FOR SHARE`, tournamentID, rosterID,
	).Scan(&projectionRevisionID, &projectionRevision)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (
			id, tournament_id, roster_id, projection_revision_id, projection_revision,
			sequence, projection_ordinal,
			terminal, idempotency_key,
			audience, topic, payload, created_at, available_at
		)
		VALUES ($1, $2, $3, $4, $5, 1, 1, true, $6, 'all', 'tournament.cancelled',
			'{"state":"cancelled"}'::jsonb, $7, $7)`,
		eventID, tournamentID, rosterID, projectionRevisionID, projectionRevision, idempotencyKey, createdAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE tournaments
		SET state = 'cancelled',
			revision = 2,
			updated_at = $2,
			finished_at = $2
		WHERE id = $1
			AND state = 'draft'
			AND revision = 1`, tournamentID, createdAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, tournament_id, roster_id, actor_kind, actor_id, action, payload, occurred_at, created_at
		)
		VALUES ($1, $2, $3, 'operator', $4, 'tournament.cancelled',
			'{"state":"cancelled"}'::jsonb, $5, $5)`,
		auditID, tournamentID, rosterID, actorID, createdAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO tournament_cancellations (
			command_id, tournament_id, roster_id, source_revision, resulting_revision,
			source_state, actor_id, reason, audit_event_id, outbox_event_id, cancelled_at, created_at
		)
		VALUES ($1, $2, $3, 1, 2, 'draft', $4, 'test cancellation', $5, $6, $7, $7)`,
		commandID, tournamentID, rosterID, actorID, auditID, eventID, createdAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_tournament_cancellation_sources (
			outbox_event_id, tournament_id, roster_id, cancellation_command_id,
			projection_revision_id, projection_revision, projection_ordinal, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7)`,
		eventID, tournamentID, rosterID, commandID, projectionRevisionID, projectionRevision, createdAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	return delivery.Event{
		ID: eventID, CorrelationID: idempotencyKey, TournamentID: tournamentID,
		ProjectionRevisionID: projectionRevisionID, ProjectionRevision: projectionRevision, ProjectionOrdinal: 1,
		Sequence: 1, Terminal: true,
		Audience: delivery.AudienceAll, Topic: "tournament.cancelled",
		Payload: []byte(`{"state":"cancelled"}`), OccurredAt: createdAt, AttemptCount: 1,
	}
}
