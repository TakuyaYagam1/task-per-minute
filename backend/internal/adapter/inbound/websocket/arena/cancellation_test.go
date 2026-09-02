package arena

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestArenaCancellationDelivery(t *testing.T) {
	tournamentID := testUUID("70000000-0000-4000-8000-000000000001")
	firstPlayerID := testUUID("70000000-0000-4000-8000-000000000002")
	secondPlayerID := testUUID("70000000-0000-4000-8000-000000000003")
	coordinator, err := NewCancellationCoordinator(tournamentID)
	if err != nil {
		t.Fatalf("NewCancellationCoordinator() error = %v", err)
	}
	for _, request := range []CancellationSubscriptionRequest{
		{Role: CancellationRoleParticipant, TournamentID: tournamentID, ParticipantID: firstPlayerID},
		{Role: CancellationRoleOperator, TournamentID: tournamentID},
		{Role: CancellationRolePublic, TournamentID: testUUID("70000000-0000-4000-8000-000000000099")},
	} {
		if subscription, subscribeErr := coordinator.CancellationSubscribe(request); !errors.Is(subscribeErr, ErrCancellationInvalidSubscription) || subscription != nil {
			t.Fatalf("unauthorized CancellationSubscribe(%s) = %#v, %v", request.Role, subscription, subscribeErr)
		}
	}

	slowParticipant := cancellationSubscribe(t, coordinator, CancellationSubscriptionRequest{
		Role:          CancellationRoleParticipant,
		Authenticated: true,
		TournamentID:  tournamentID,
		ParticipantID: firstPlayerID,
	})
	participant := cancellationSubscribe(t, coordinator, CancellationSubscriptionRequest{
		Role:          CancellationRoleParticipant,
		Authenticated: true,
		TournamentID:  tournamentID,
		ParticipantID: secondPlayerID,
	})
	public := cancellationSubscribe(t, coordinator, CancellationSubscriptionRequest{
		Role: CancellationRolePublic, TournamentID: tournamentID,
	})
	operator := cancellationSubscribe(t, coordinator, CancellationSubscriptionRequest{
		Role: CancellationRoleOperator, Authenticated: true, TournamentID: tournamentID,
	})

	action := CancellationParticipantAction{TournamentID: tournamentID, ParticipantID: firstPlayerID}
	if err := coordinator.CancellationAuthorizeParticipantAction(action); err != nil {
		t.Fatalf("action before cancellation error = %v", err)
	}

	commit := cancellationCommitInput(t, tournamentID, firstPlayerID, secondPlayerID)
	committed := make(chan error, 1)
	go func() {
		committed <- coordinator.CancellationCommit(commit)
	}()
	select {
	case err := <-committed:
		if err != nil {
			t.Fatalf("CancellationCommit() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CancellationCommit() blocked on a slow participant")
	}

	cases := []struct {
		name          string
		subscription  *CancellationSubscription
		role          CancellationRole
		participantID uuid.UUID
	}{
		{name: "slow participant", subscription: slowParticipant, role: CancellationRoleParticipant, participantID: firstPlayerID},
		{name: "participant", subscription: participant, role: CancellationRoleParticipant, participantID: secondPlayerID},
		{name: "public", subscription: public, role: CancellationRolePublic},
		{name: "operator", subscription: operator, role: CancellationRoleOperator},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			deliveries := cancellationReadDeliveries(t, testCase.subscription)
			cancellationAssertDeliveries(t, deliveries, testCase.role, testCase.participantID)
		})
	}

	if err := coordinator.CancellationCommit(commit); err != nil {
		t.Fatalf("idempotent CancellationCommit() error = %v", err)
	}
	reconnected := cancellationSubscribe(t, coordinator, CancellationSubscriptionRequest{
		Role:          CancellationRoleParticipant,
		Authenticated: true,
		TournamentID:  tournamentID,
		ParticipantID: firstPlayerID,
	})
	cancellationAssertDeliveries(
		t,
		cancellationReadDeliveries(t, reconnected),
		CancellationRoleParticipant,
		firstPlayerID,
	)

	if err := coordinator.CancellationAuthorizeParticipantAction(action); !errors.Is(err, ErrCancellationParticipantActionRejected) {
		t.Fatalf("action after terminal close error = %v", err)
	}
}

func cancellationSubscribe(
	t *testing.T,
	coordinator *CancellationCoordinator,
	request CancellationSubscriptionRequest,
) *CancellationSubscription {
	t.Helper()
	subscription, err := coordinator.CancellationSubscribe(request)
	if err != nil {
		t.Fatalf("CancellationSubscribe(%s) error = %v", request.Role, err)
	}
	return subscription
}

func cancellationReadDeliveries(t *testing.T, subscription *CancellationSubscription) []CancellationDelivery {
	t.Helper()
	var deliveries []CancellationDelivery
	for {
		select {
		case delivery, open := <-subscription.CancellationDeliveries():
			if !open {
				return deliveries
			}
			deliveries = append(deliveries, delivery)
		case <-time.After(time.Second):
			t.Fatal("terminal delivery did not close")
		}
	}
}

func cancellationAssertDeliveries(
	t *testing.T,
	deliveries []CancellationDelivery,
	role CancellationRole,
	participantID uuid.UUID,
) {
	t.Helper()
	if len(deliveries) != 2 {
		t.Fatalf("%s deliveries = %d, want snapshot and terminal event", role, len(deliveries))
	}
	if deliveries[0].Kind != CancellationDeliverySnapshot || deliveries[1].Kind != CancellationDeliveryTerminal {
		t.Fatalf("%s delivery order = [%s %s]", role, deliveries[0].Kind, deliveries[1].Kind)
	}
	if deliveries[0].Sequence != 12 || deliveries[1].Sequence != 13 {
		t.Fatalf("%s sequences = [%d %d]", role, deliveries[0].Sequence, deliveries[1].Sequence)
	}
	if deliveries[0].Snapshot == nil {
		t.Fatalf("%s snapshot delivery has no snapshot", role)
	}

	switch role {
	case CancellationRoleParticipant:
		if deliveries[0].Snapshot.Participant == nil || deliveries[0].Snapshot.Public != nil || deliveries[0].Snapshot.Operator != nil {
			t.Fatalf("participant received wrong-role snapshot: %#v", deliveries[0].Snapshot)
		}
		if deliveries[0].Snapshot.Participant.PlayerID != participantID || deliveries[1].Participant == nil || deliveries[1].Participant.ParticipantID != participantID {
			t.Fatalf("participant delivery crossed authority scope")
		}
		if deliveries[1].Public != nil || deliveries[1].Operator != nil {
			t.Fatal("participant terminal event contains another role payload")
		}
	case CancellationRolePublic:
		if deliveries[0].Snapshot.Public == nil || deliveries[0].Snapshot.Participant != nil || deliveries[0].Snapshot.Operator != nil {
			t.Fatalf("public received wrong-role snapshot: %#v", deliveries[0].Snapshot)
		}
		if deliveries[1].Public == nil || deliveries[1].Participant != nil || deliveries[1].Operator != nil {
			t.Fatal("public terminal event contains a private role payload")
		}
	case CancellationRoleOperator:
		if deliveries[0].Snapshot.Operator == nil || deliveries[0].Snapshot.Participant != nil || deliveries[0].Snapshot.Public != nil {
			t.Fatalf("operator received wrong-role snapshot: %#v", deliveries[0].Snapshot)
		}
		if deliveries[1].Operator == nil || deliveries[1].Participant != nil || deliveries[1].Public != nil {
			t.Fatal("operator terminal event contains another role payload")
		}
	default:
		t.Fatalf("unexpected role %q", role)
	}

	encoded, err := json.Marshal(deliveries)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if role != CancellationRoleOperator && bytes.Contains(encoded, []byte("operator approved cancellation")) {
		t.Fatalf("%s delivery exposed the operator reason: %s", role, encoded)
	}
	if role == CancellationRoleOperator && !bytes.Contains(encoded, []byte("operator approved cancellation")) {
		t.Fatalf("operator delivery omitted its private reason: %s", encoded)
	}
}

func cancellationCommitInput(
	t *testing.T,
	tournamentID uuid.UUID,
	participantIDs ...uuid.UUID,
) CancellationCommitInput {
	t.Helper()
	snapshotAt := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	participantSnapshots := make(map[uuid.UUID]RealtimeEnvelope, len(participantIDs))
	for index, participantID := range participantIDs {
		input := testParticipantSnapshotInput(tournamentID, participantID)
		input.Assignment = nil
		input.Opponent = nil
		snapshot, err := NewParticipantSnapshot(
			ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: participantID},
			input,
		)
		if err != nil {
			t.Fatalf("NewParticipantSnapshot() error = %v", err)
		}
		participantSnapshots[participantID] = cancellationEnvelope(t, tournamentID, index+1, snapshotAt, snapshot)
	}

	publicInput := testPublicSnapshotInput(tournamentID)
	publicInput.Tournament.State = CancellationTerminalState
	publicInput.LiveSeries = nil
	publicInput.Draft = nil
	finishedAt := snapshotAt
	publicInput.Tournament.FinishedAt = &finishedAt
	publicSnapshot, err := NewPublicSnapshot(tournamentID, publicInput)
	if err != nil {
		t.Fatalf("NewPublicSnapshot() error = %v", err)
	}
	operatorSnapshot, err := NewOperatorSnapshot(
		OperatorSnapshotAccess{
			Authenticated: true,
			TournamentID:  tournamentID,
			OperatorID:    testUUID("70000000-0000-4000-8000-000000000004"),
		},
		testOperatorSnapshotInput(tournamentID),
	)
	if err != nil {
		t.Fatalf("NewOperatorSnapshot() error = %v", err)
	}

	return CancellationCommitInput{
		TournamentID:   tournamentID,
		CancellationID: testUUID("70000000-0000-4000-8000-000000000005"),
		TerminalMetadata: RealtimeEnvelopeMetadata{
			SchemaVersion:      ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			Sequence:           13,
			EventID:            testUUID("70000000-0000-4000-8000-000000000006"),
			OccurredAt:         snapshotAt.Add(time.Second),
			ProjectionRevision: 10,
		},
		ParticipantSnapshots: participantSnapshots,
		PublicSnapshot:       cancellationEnvelope(t, tournamentID, 20, snapshotAt, publicSnapshot),
		OperatorSnapshot:     cancellationEnvelope(t, tournamentID, 21, snapshotAt, operatorSnapshot),
		OperatorReason:       "operator approved cancellation",
	}
}

func cancellationEnvelope(
	t *testing.T,
	tournamentID uuid.UUID,
	eventSuffix int,
	occurredAt time.Time,
	payload any,
) RealtimeEnvelope {
	t.Helper()
	eventID := uuid.MustParse(fmt.Sprintf("70000000-0000-4000-8000-%012d", 100+eventSuffix))
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           12,
		EventID:            eventID,
		OccurredAt:         occurredAt,
		ProjectionRevision: 9,
	}, payload)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	return envelope
}
