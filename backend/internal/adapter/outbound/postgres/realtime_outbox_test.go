package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

func TestMapClaimedRealtimeEventAcceptsPublishedProjection(t *testing.T) {
	t.Parallel()

	row := validClaimedRealtimeRow()
	event, err := mapClaimedRealtimeEvent(row)
	require.NoError(t, err)
	require.Equal(t, row.IdempotencyKey, event.CorrelationID)
	require.Equal(t, row.ProjectionRevisionID, event.ProjectionRevisionID)
	require.Equal(t, int32(row.ProjectionOrdinal), event.ProjectionOrdinal)
	require.Equal(t, row.CreatedAt.Time.UTC(), event.OccurredAt)
	row.Payload[2] = 'x'
	require.JSONEq(t, `{"result_event_id":"event"}`, string(event.Payload))

	row = validClaimedRealtimeRow()
	row.ProjectionRevisionID = uuid.Nil
	_, err = mapClaimedRealtimeEvent(row)
	require.ErrorIs(t, err, delivery.ErrRepositoryState)
}

func TestMapRealtimeEventRejectsMissingCorrelation(t *testing.T) {
	t.Parallel()

	row := validClaimedRealtimeRow()
	row.IdempotencyKey = uuid.Nil
	_, err := mapClaimedRealtimeEvent(row)
	require.ErrorIs(t, err, delivery.ErrRepositoryState)
	_, err = mapReplayRealtimeEvent(sqlc.ListRealtimeOutboxAfterRow(row))
	require.ErrorIs(t, err, delivery.ErrRepositoryState)
}

func TestMapReplayRealtimeEventPreservesCorrelation(t *testing.T) {
	t.Parallel()

	row := sqlc.ListRealtimeOutboxAfterRow(validClaimedRealtimeRow())
	event, err := mapReplayRealtimeEvent(row)
	require.NoError(t, err)
	require.Equal(t, row.IdempotencyKey, event.CorrelationID)
	require.Equal(t, row.ProjectionRevisionID, event.ProjectionRevisionID)
	require.Equal(t, int32(row.ProjectionOrdinal), event.ProjectionOrdinal)
	require.Equal(t, row.CreatedAt.Time.UTC(), event.OccurredAt)
	row.Payload[2] = 'x'
	require.JSONEq(t, `{"result_event_id":"event"}`, string(event.Payload))
}

func TestMapRealtimeSubscriptionPreservesPendingTerminalCorrelation(t *testing.T) {
	t.Parallel()

	row := validPendingTerminalRow()
	subscription, err := mapRealtimeSubscription(row)
	require.NoError(t, err)
	require.Equal(t, delivery.TerminalReceiptPending, subscription.TerminalState)
	require.NotNil(t, subscription.PendingTerminal)
	require.Equal(t, row.TerminalCorrelationID.UUID, subscription.PendingTerminal.CorrelationID)
	require.Equal(t, row.TerminalProjectionRevisionID.UUID, subscription.PendingTerminal.ProjectionRevisionID)
	require.NoError(t, subscription.Validate())
	row.TerminalPayload[2] = 'x'
	require.JSONEq(t, `{"state":"cancelled"}`, string(subscription.PendingTerminal.Payload))
}

func TestMapRealtimeSubscriptionRejectsMissingTerminalCorrelation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		correlation uuid.NullUUID
	}{
		{name: "absent"},
		{name: "invalid", correlation: uuid.NullUUID{UUID: uuid.New(), Valid: false}},
		{name: "nil", correlation: uuid.NullUUID{UUID: uuid.Nil, Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := validPendingTerminalRow()
			row.TerminalCorrelationID = tc.correlation
			subscription, err := mapRealtimeSubscription(row)
			require.ErrorIs(t, err, delivery.ErrRepositoryState)
			require.Equal(t, delivery.Subscription{}, subscription)
		})
	}
}

func TestMapClaimedRealtimeEventRejectsProjectionlessTerminal(t *testing.T) {
	t.Parallel()

	row := validClaimedRealtimeRow()
	row.ProjectionRevisionID = uuid.Nil
	row.ProjectionRevision = 0
	row.ProjectionOrdinal = 0
	row.Terminal = true
	row.Topic = "tournament.cancelled"
	row.Payload = []byte(`{"state":"cancelled"}`)

	_, err := mapClaimedRealtimeEvent(row)
	require.ErrorIs(t, err, delivery.ErrRepositoryState)

	row.Terminal = false
	_, err = mapClaimedRealtimeEvent(row)
	require.ErrorIs(t, err, delivery.ErrRepositoryState)
}

func TestMapRealtimeDeliveryReceiptPreservesTerminalOutcome(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	written := "written"
	row := sqlc.RealtimeDeliveryReceipt{
		SubscriberID: uuid.New(), EventID: uuid.New(), Sequence: 2,
		WriteAcknowledgedAt: pgtype.Timestamptz{Time: at, Valid: true},
		TerminalAt:          pgtype.Timestamptz{Time: at, Valid: true}, Outcome: &written,
	}
	receipt, err := mapRealtimeDeliveryReceipt(row)
	require.NoError(t, err)
	require.Equal(t, delivery.DeliveryWritten, receipt.Outcome)
	require.Equal(t, at, *receipt.TerminalAt)

	disconnected := "disconnected"
	row.Outcome = &disconnected
	_, err = mapRealtimeDeliveryReceipt(row)
	require.ErrorIs(t, err, delivery.ErrRepositoryState)
}

func TestRealtimeBacklogSnapshotValidation(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.FixedZone("source", 3*60*60))
	snapshot := delivery.BacklogSnapshot{
		PendingCount: 2,
		OldestPendingAt: realtimeUTCOptional(
			at,
			true,
		),
	}
	require.NoError(t, snapshot.Validate())
	require.Equal(t, time.UTC, snapshot.OldestPendingAt.Location())

	snapshot.PendingCount = -1
	require.ErrorIs(t, snapshot.Validate(), delivery.ErrRepositoryState)
}

func TestRealtimeOutboxRepositoryRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	var repository *RealtimeOutboxPostgres
	_, err := repository.Cursor(context.Background(), uuid.New())
	require.ErrorIs(t, err, delivery.ErrInvalidReceipt)
	_, err = repository.Backlog(context.Background())
	require.ErrorIs(t, err, delivery.ErrInvalidReceipt)
	_, err = NewRealtimeOutboxPostgres(nil).Claim(context.Background(), delivery.ClaimRequest{})
	require.ErrorIs(t, err, delivery.ErrInvalidReceipt)
	_, err = repository.PruneClosedSubscribers(context.Background(), delivery.ReceiptRetentionRequest{})
	require.ErrorIs(t, err, delivery.ErrInvalidReceipt)
}

func validClaimedRealtimeRow() sqlc.ClaimRealtimeOutboxEventsRow {
	return sqlc.ClaimRealtimeOutboxEventsRow{
		ID:                   uuid.MustParse("30000000-0000-0000-0000-000000000001"),
		IdempotencyKey:       uuid.MustParse("30000000-0000-0000-0000-000000000004"),
		TournamentID:         uuid.MustParse("30000000-0000-0000-0000-000000000002"),
		ProjectionRevisionID: uuid.MustParse("30000000-0000-0000-0000-000000000003"),
		Sequence:             2, ProjectionRevision: 4, ProjectionOrdinal: 1,
		Audience: "public", Topic: "result.settled",
		Payload: []byte(`{"result_event_id":"event"}`),
		CreatedAt: pgtype.Timestamptz{
			Time:  time.Date(2026, 9, 6, 10, 0, 0, 0, time.FixedZone("source", 3*60*60)),
			Valid: true,
		},
		AttemptCount: 1,
	}
}

func validPendingTerminalRow() sqlc.OpenRealtimeSubscriptionRow {
	event := validClaimedRealtimeRow()
	event.Terminal = true
	event.Topic = "tournament.cancelled"
	return sqlc.OpenRealtimeSubscriptionRow{
		SubscriberID:                 uuid.New(),
		InstanceID:                   uuid.New(),
		ConnectionID:                 uuid.New(),
		ConnectionGeneration:         1,
		TournamentID:                 event.TournamentID,
		Role:                         event.Audience,
		OpenedAt:                     event.CreatedAt,
		TerminalState:                string(delivery.TerminalReceiptPending),
		TerminalEventID:              uuid.NullUUID{UUID: event.ID, Valid: true},
		TerminalCorrelationID:        uuid.NullUUID{UUID: event.IdempotencyKey, Valid: true},
		TerminalTournamentID:         uuid.NullUUID{UUID: event.TournamentID, Valid: true},
		TerminalProjectionRevisionID: uuid.NullUUID{UUID: event.ProjectionRevisionID, Valid: true},
		TerminalSequence:             &event.Sequence,
		TerminalProjectionRevision:   &event.ProjectionRevision,
		TerminalProjectionOrdinal:    &event.ProjectionOrdinal,
		TerminalTerminal:             &event.Terminal,
		TerminalAudience:             &event.Audience,
		TerminalTopic:                &event.Topic,
		TerminalPayload:              []byte(`{"state":"cancelled"}`),
		TerminalCreatedAt:            event.CreatedAt,
		TerminalAttemptCount:         &event.AttemptCount,
	}
}
