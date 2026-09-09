package eventdelivery_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

func TestEventRejectsPrivateDataForPublicAudience(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.Audience = delivery.AudiencePublic
	event.Payload = json.RawMessage(`{"snapshot":{"session_token":"must-not-leak"}}`)

	require.ErrorIs(t, event.Validate(), delivery.ErrInvalidEvent)
}

func TestEventRejectsPrivateFieldAliasesAndCloneDoesNotAliasPayload(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.Audience = delivery.AudiencePublic
	event.Payload = json.RawMessage(`{"snapshot":{"AUTHORIZATION":"must-not-leak"}}`)
	require.ErrorIs(t, event.Validate(), delivery.ErrInvalidEvent)

	event.Payload = json.RawMessage(`{"result_event_id":"10000000-0000-0000-0000-000000000004"}`)
	clone := event.Clone()
	clone.Payload[2] = 'X'
	require.NotEqual(t, clone.Payload, event.Payload)
	require.Equal(t, byte('r'), event.Payload[2])
}

func TestEventMatchesOnlyItsPrivatePrincipal(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.Audience = delivery.AudienceParticipant
	event.PrincipalID = uuid.MustParse("10000000-0000-0000-0000-000000000007")
	subscriber := validSubscriber(event)
	subscriber.Audience = delivery.AudienceParticipant
	subscriber.PrincipalID = event.PrincipalID

	require.True(t, event.Matches(subscriber))
	subscriber.PrincipalID = uuid.MustParse("10000000-0000-0000-0000-000000000008")
	require.False(t, event.Matches(subscriber))
	subscriber.Audience = delivery.AudienceOperator
	require.False(t, event.Matches(subscriber))
}

func TestAllAudienceMatchesEveryRoleInTournament(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.Audience = delivery.AudienceAll
	public := validSubscriber(event)
	participant := public
	participant.Audience = delivery.AudienceParticipant
	participant.PrincipalID = uuid.MustParse("10000000-0000-0000-0000-000000000009")

	require.True(t, event.Matches(public))
	require.True(t, event.Matches(participant))
	participant.TournamentID = uuid.New()
	require.False(t, event.Matches(participant))
}

func TestTerminalEventWithoutProjectionBindingIsRejected(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.Terminal = true
	event.ProjectionRevisionID = uuid.Nil
	event.ProjectionRevision = 0
	event.ProjectionOrdinal = 0
	event.Topic = "tournament.cancelled"
	event.Payload = json.RawMessage(`{"state":"cancelled"}`)
	require.ErrorIs(t, event.Validate(), delivery.ErrInvalidEvent)

	event.ProjectionRevisionID = uuid.New()
	require.ErrorIs(t, event.Validate(), delivery.ErrInvalidEvent)
	event.ProjectionRevision = 8
	event.ProjectionOrdinal = 1
	require.NoError(t, event.Validate())
	event.ProjectionRevisionID = uuid.Nil
	event.ProjectionRevision = 0
	event.ProjectionOrdinal = 0
	event.Terminal = false
	require.ErrorIs(t, event.Validate(), delivery.ErrInvalidEvent)
}

func TestEventRequiresDurableCorrelationIdentity(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.CorrelationID = uuid.Nil

	require.ErrorIs(t, event.Validate(), delivery.ErrInvalidEvent)
}

func TestDeliveryReceiptRequiresTerminalEvidence(t *testing.T) {
	t.Parallel()

	receipt := delivery.DeliveryReceipt{
		SubscriberID: uuid.New(), EventID: uuid.New(), Sequence: 2,
		Outcome: delivery.DeliveryWritten,
	}
	require.ErrorIs(t, receipt.Validate(), delivery.ErrInvalidReceipt)

	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	receipt.TerminalAt = &now
	require.NoError(t, receipt.Validate())
	receipt.Outcome = delivery.DeliveryPending
	require.ErrorIs(t, receipt.Validate(), delivery.ErrInvalidReceipt)
}

func TestReceiptRetentionRequestAndResultAreBounded(t *testing.T) {
	t.Parallel()

	request := delivery.ReceiptRetentionRequest{
		CutoffAt:  time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
		BatchSize: 128,
	}
	require.NoError(t, request.Validate())

	request.BatchSize = 0
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)
	request.BatchSize = 257
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)
	request.BatchSize = 1
	request.CutoffAt = time.Time{}
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)

	result := delivery.ReceiptRetentionResult{DeletedReceipts: 2, DeletedSubscribers: 1}
	require.NoError(t, result.Validate())
	result.DeletedReceipts = -1
	require.ErrorIs(t, result.Validate(), delivery.ErrRepositoryState)
}

func TestReplayRequiresConcreteSubscriberAudience(t *testing.T) {
	t.Parallel()

	request := delivery.ReplayRequest{
		TournamentID: uuid.New(), Limit: 10, Audience: delivery.AudienceAll,
	}
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)

	request.Audience = delivery.AudienceParticipant
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)
	request.PrincipalID = uuid.New()
	require.NoError(t, request.Validate())
}

func TestSubscriptionOpenAllowsOnlyScopeBoundOpaqueResume(t *testing.T) {
	t.Parallel()

	event := validEvent()
	request := delivery.SubscriptionOpenRequest{
		ConnectionID:  uuid.MustParse("10000000-0000-0000-0000-000000000010"),
		InstanceID:    uuid.MustParse("10000000-0000-0000-0000-000000000006"),
		TournamentID:  event.TournamentID,
		Audience:      delivery.AudienceParticipant,
		PrincipalID:   uuid.MustParse("10000000-0000-0000-0000-000000000011"),
		AfterSequence: 0,
		OpenedAt:      event.OccurredAt,
	}
	require.NoError(t, request.Validate())

	request.Audience = delivery.AudienceAll
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)
	request.Audience = delivery.AudienceParticipant
	request.ConnectionID = uuid.Nil
	require.ErrorIs(t, request.Validate(), delivery.ErrInvalidReceipt)
}

func TestSubscriptionAcceptsTerminalAtSnapshotWatermark(t *testing.T) {
	t.Parallel()

	event := validEvent()
	event.Terminal = true
	event.Audience = delivery.AudiencePublic
	event.Topic = "tournament.cancelled"
	event.Payload = json.RawMessage(`{"state":"cancelled"}`)
	subscriber := validSubscriber(event)
	subscriber.ConnectionID = uuid.MustParse("10000000-0000-0000-0000-000000000012")
	subscriber.ConnectionGeneration = 1
	subscriber.AfterSequence = event.Sequence

	subscription := delivery.Subscription{
		Subscriber: subscriber, TerminalState: delivery.TerminalReceiptPending, PendingTerminal: &event,
	}
	require.NoError(t, subscription.Validate())

	wrongTournament := event.Clone()
	wrongTournament.TournamentID = uuid.New()
	subscription.PendingTerminal = &wrongTournament
	require.ErrorIs(t, subscription.Validate(), delivery.ErrInvalidReceipt)
}

func TestBacklogSnapshotValidation(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot delivery.BacklogSnapshot
		valid    bool
	}{
		{name: "empty", valid: true},
		{name: "pending", snapshot: delivery.BacklogSnapshot{
			PendingCount: 3, OldestPendingAt: &at,
		}, valid: true},
		{name: "missing pending timestamp", snapshot: delivery.BacklogSnapshot{
			PendingCount: 1,
		}},
		{name: "non UTC timestamp", snapshot: delivery.BacklogSnapshot{
			PendingCount: 1,
			OldestPendingAt: func() *time.Time {
				value := at.In(time.FixedZone("offset", 3600))
				return &value
			}(),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.snapshot.Validate()
			if test.valid {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, delivery.ErrRepositoryState)
		})
	}
}

func validEvent() delivery.Event {
	return delivery.Event{
		ID:                   uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		CorrelationID:        uuid.MustParse("10000000-0000-0000-0000-000000000010"),
		TournamentID:         uuid.MustParse("10000000-0000-0000-0000-000000000002"),
		ProjectionRevisionID: uuid.MustParse("10000000-0000-0000-0000-000000000003"),
		Sequence:             2, ProjectionRevision: 4, ProjectionOrdinal: 1,
		Audience: delivery.AudiencePublic, Topic: "result.settled",
		Payload:    json.RawMessage(`{"result_event_id":"10000000-0000-0000-0000-000000000004"}`),
		OccurredAt: time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC), AttemptCount: 1,
	}
}

func validSubscriber(event delivery.Event) delivery.Subscriber {
	return delivery.Subscriber{
		ID:                   uuid.MustParse("10000000-0000-0000-0000-000000000005"),
		InstanceID:           uuid.MustParse("10000000-0000-0000-0000-000000000006"),
		ConnectionID:         uuid.MustParse("10000000-0000-0000-0000-000000000007"),
		ConnectionGeneration: 1,
		TournamentID:         event.TournamentID, Audience: delivery.AudiencePublic,
		AfterSequence: event.Sequence - 1, ConnectedAt: event.OccurredAt,
	}
}
