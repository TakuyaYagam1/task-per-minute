package websocket

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
)

var (
	_ ArenaParticipantConnectionFlow = (*ArenaParticipantFlow)(nil)
	_ ArenaPublicConnectionFlow      = (*ArenaPublicFlow)(nil)
	_ ArenaOperatorConnectionFlow    = (*ArenaOperatorFlow)(nil)
	_ ArenaTerminalSubscriptionFlow  = (*ArenaTerminalFlow)(nil)
	_ ArenaTerminalSubscription      = (*arenaTerminalSubscription)(nil)
)

func TestArenaProductionFlows(t *testing.T) {
	tournamentID := arenaFlowUUID("71000000-0000-4000-8000-000000000001")
	playerID := arenaFlowUUID("71000000-0000-4000-8000-000000000002")
	operatorID := arenaFlowUUID("71000000-0000-4000-8000-000000000003")

	t.Run("constructors reject missing dependencies", func(t *testing.T) {
		if flow, err := NewArenaParticipantFlow(nil); !errors.Is(err, ErrArenaFlowInvalidConfig) || flow != nil {
			t.Fatalf("NewArenaParticipantFlow(nil) = %#v, %v", flow, err)
		}
		if flow, err := NewArenaPublicFlow(nil, &arenaws.PublicRealtimeConfig{MaxConnections: 1, MaxReplayEvents: 1}); !errors.Is(err, ErrArenaFlowInvalidConfig) || flow != nil {
			t.Fatalf("NewArenaPublicFlow(nil, config) = %#v, %v", flow, err)
		}
		publicSource := &arenaFlowPublicSource{read: arenaFlowPublicRead(t, tournamentID)}
		if flow, err := NewArenaPublicFlow(publicSource, nil); !errors.Is(err, ErrArenaFlowInvalidConfig) || flow != nil {
			t.Fatalf("NewArenaPublicFlow(source, nil) = %#v, %v", flow, err)
		}
		if flow, err := NewArenaPublicFlow(publicSource, &arenaws.PublicRealtimeConfig{}); !errors.Is(err, ErrArenaFlowInvalidConfig) || flow != nil {
			t.Fatalf("NewArenaPublicFlow(source, invalid) = %#v, %v", flow, err)
		}
		if flow, err := NewArenaOperatorFlow(nil); !errors.Is(err, ErrArenaFlowInvalidConfig) || flow != nil {
			t.Fatalf("NewArenaOperatorFlow(nil) = %#v, %v", flow, err)
		}
		if flow, err := NewArenaTerminalFlow(nil); !errors.Is(err, ErrArenaFlowInvalidConfig) || flow != nil {
			t.Fatalf("NewArenaTerminalFlow(nil) = %#v, %v", flow, err)
		}
	})

	t.Run("participant maps the trusted request through the participant view", func(t *testing.T) {
		var got arenaws.ParticipantRealtimeReadQuery
		source := arenaFlowParticipantSource(func(_ context.Context, query arenaws.ParticipantRealtimeReadQuery) (arenaws.ParticipantRealtimeReadModel, error) {
			got = query
			return arenaFlowParticipantRead(t, tournamentID, playerID), nil
		})
		flow, err := NewArenaParticipantFlow(source)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := flow.OpenArenaParticipant(context.Background(), ArenaParticipantConnectionRequest{
			Principal: arenaws.ParticipantRealtimePrincipal{
				Authenticated: true,
				TournamentID:  tournamentID,
				PlayerID:      playerID,
			},
			TournamentID: tournamentID,
		})
		if err != nil {
			t.Fatalf("OpenArenaParticipant() error = %v", err)
		}
		if !payload.UsesSnapshot || len(payload.Envelopes) != 1 || payload.Envelopes[0].Participant.PlayerID != playerID {
			t.Fatalf("participant payload = %#v", payload)
		}
		if got.TournamentID != tournamentID || got.PlayerID != playerID || got.MaxEvents != arenaws.ParticipantRealtimeMaxReplayEvents {
			t.Fatalf("participant query = %#v", got)
		}
	})

	t.Run("public maps one open and releases capacity on cancellation", func(t *testing.T) {
		source := &arenaFlowPublicSource{read: arenaFlowPublicRead(t, tournamentID)}
		flow := arenaFlowPublic(t, source, 1)
		ctx, cancel := context.WithCancel(context.Background())
		payload, err := flow.OpenArenaPublic(ctx, ArenaPublicConnectionRequest{TournamentID: tournamentID})
		if err != nil {
			t.Fatalf("OpenArenaPublic() error = %v", err)
		}
		if !payload.UsesSnapshot || len(payload.Envelopes) != 1 || payload.Envelopes[0].TournamentID != tournamentID {
			t.Fatalf("public payload = %#v", payload)
		}
		if _, err := flow.OpenArenaPublic(context.Background(), ArenaPublicConnectionRequest{TournamentID: tournamentID}); !errors.Is(err, arenaws.ErrPublicRealtimeConnectionLimit) {
			t.Fatalf("second OpenArenaPublic() error = %v", err)
		}
		if got := source.readCount(); got != 1 {
			t.Fatalf("public source reads before cancel = %d, want 1", got)
		}

		cancel()
		arenaFlowWaitForPublicCapacity(t, flow, tournamentID)
		if got := source.readCount(); got != 2 {
			t.Fatalf("public source reads after reuse = %d, want 2", got)
		}
	})

	t.Run("public releases capacity when payload conversion fails", func(t *testing.T) {
		source := &arenaFlowPublicSource{read: arenaFlowPublicRead(t, tournamentID)}
		flow := arenaFlowPublic(t, source, 1)
		conversionErr := errors.New("conversion failed")
		_, err := flow.openArenaPublic(
			context.Background(),
			ArenaPublicConnectionRequest{TournamentID: tournamentID},
			func(bool, []arenaws.RealtimeEnvelope) (ArenaPublicPayload, error) {
				return ArenaPublicPayload{}, conversionErr
			},
		)
		if !errors.Is(err, conversionErr) {
			t.Fatalf("openArenaPublic() error = %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if _, err := flow.OpenArenaPublic(ctx, ArenaPublicConnectionRequest{TournamentID: tournamentID}); err != nil {
			t.Fatalf("OpenArenaPublic() after conversion failure error = %v", err)
		}
		if got := source.readCount(); got != 2 {
			t.Fatalf("public source reads = %d, want one per root open", got)
		}
	})

	t.Run("operator uses only the principal supplied by the root transport", func(t *testing.T) {
		source := &arenaFlowOperatorSource{state: arenaFlowOperatorRead(t, tournamentID, operatorID)}
		flow, err := NewArenaOperatorFlow(source)
		if err != nil {
			t.Fatal(err)
		}
		principal := arenaws.OperatorRealtimePrincipal{
			Authenticated: true,
			PrincipalID:   operatorID,
			Role:          arenaws.OperatorRealtimeRole,
			TournamentID:  tournamentID,
		}
		payload, err := flow.OpenArenaOperator(context.Background(), ArenaOperatorConnectionRequest{
			Principal: principal, TournamentID: tournamentID,
		})
		if err != nil {
			t.Fatalf("OpenArenaOperator() error = %v", err)
		}
		if !payload.UsesSnapshot || len(payload.Envelopes) != 1 || payload.Envelopes[0].Operator.TournamentID != tournamentID {
			t.Fatalf("operator payload = %#v", payload)
		}
		if source.reads != 1 || source.query.TournamentID != tournamentID || source.query.ReplayLimit != arenaws.OperatorRealtimeReplayLimit {
			t.Fatalf("operator source = reads %d, query %#v", source.reads, source.query)
		}

		untrusted := principal
		untrusted.Role = "participant"
		if _, err := flow.OpenArenaOperator(context.Background(), ArenaOperatorConnectionRequest{Principal: untrusted, TournamentID: tournamentID}); !errors.Is(err, arenaws.ErrOperatorRealtimeRole) {
			t.Fatalf("OpenArenaOperator(untrusted) error = %v", err)
		}
		if source.reads != 1 {
			t.Fatalf("operator source read for rejected principal: %d", source.reads)
		}
	})

	t.Run("terminal registry clones seeds and returns one coordinator concurrently", func(t *testing.T) {
		seeded, err := arenaws.NewCancellationCoordinator(tournamentID)
		if err != nil {
			t.Fatal(err)
		}
		seeds := map[uuid.UUID]*arenaws.CancellationCoordinator{tournamentID: seeded}
		registry, err := NewArenaTerminalRegistry(seeds)
		if err != nil {
			t.Fatal(err)
		}
		delete(seeds, tournamentID)
		if got, ok := registry.Lookup(tournamentID); !ok || got != seeded {
			t.Fatalf("Lookup() = %#v, %t", got, ok)
		}
		if got, ok := registry.Lookup(uuid.Nil); ok || got != nil {
			t.Fatalf("Lookup(nil) = %#v, %t", got, ok)
		}

		otherTournamentID := arenaFlowUUID("71000000-0000-4000-8000-000000000004")
		const workers = 32
		coordinators := make([]*arenaws.CancellationCoordinator, workers)
		errs := make([]error, workers)
		var wait sync.WaitGroup
		for index := range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				coordinators[index], errs[index] = registry.Coordinator(otherTournamentID)
			}()
		}
		wait.Wait()
		for index := range workers {
			if errs[index] != nil || coordinators[index] == nil || coordinators[index] != coordinators[0] {
				t.Fatalf("Coordinator() worker %d = %#v, %v", index, coordinators[index], errs[index])
			}
		}
	})

	t.Run("terminal flow routes scope authentication and participant identity", func(t *testing.T) {
		coordinator, err := arenaws.NewCancellationCoordinator(tournamentID)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := NewArenaTerminalRegistry(map[uuid.UUID]*arenaws.CancellationCoordinator{tournamentID: coordinator})
		if err != nil {
			t.Fatal(err)
		}
		flow, err := NewArenaTerminalFlow(registry)
		if err != nil {
			t.Fatal(err)
		}

		for _, request := range []ArenaTerminalSubscriptionRequest{
			{Role: ArenaRoleParticipant, TournamentID: tournamentID, ParticipantID: playerID},
			{Role: ArenaRoleParticipant, Authenticated: true, TournamentID: tournamentID},
			{Role: ArenaRolePublic, Authenticated: true, TournamentID: tournamentID},
			{Role: ArenaRolePublic, TournamentID: tournamentID, ParticipantID: playerID},
			{Role: ArenaRoleOperator, TournamentID: tournamentID},
			{Role: ArenaRole("unknown"), Authenticated: true, TournamentID: tournamentID},
		} {
			subscription, subscribeErr := flow.SubscribeArenaTerminal(context.Background(), request)
			if !errors.Is(subscribeErr, arenaws.ErrCancellationInvalidSubscription) || subscription != nil {
				t.Fatalf("SubscribeArenaTerminal(%#v) = %#v, %v", request, subscription, subscribeErr)
			}
		}

		participant, err := flow.SubscribeArenaTerminal(context.Background(), ArenaTerminalSubscriptionRequest{
			Role: ArenaRoleParticipant, Authenticated: true, TournamentID: tournamentID, ParticipantID: playerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		operator, err := flow.SubscribeArenaTerminal(context.Background(), ArenaTerminalSubscriptionRequest{
			Role: ArenaRoleOperator, Authenticated: true, TournamentID: tournamentID, ParticipantID: operatorID,
		})
		if err != nil {
			t.Fatalf("operator subscription did not clear the transport principal ID: %v", err)
		}
		operator.Close()
		operator.Close()
		public, err := flow.SubscribeArenaTerminal(context.Background(), ArenaTerminalSubscriptionRequest{
			Role: ArenaRolePublic, TournamentID: tournamentID,
		})
		if err != nil {
			t.Fatal(err)
		}
		public.Close()

		if err := coordinator.CancellationCommit(arenaFlowCancellationCommit(t, tournamentID, playerID, operatorID)); err != nil {
			t.Fatalf("CancellationCommit() error = %v", err)
		}
		deliveries := arenaFlowDeliveries(t, participant.Deliveries())
		if len(deliveries) != 2 || deliveries[0].Snapshot == nil || deliveries[0].Snapshot.Participant == nil ||
			deliveries[0].Snapshot.Participant.PlayerID != playerID || deliveries[1].Participant == nil ||
			deliveries[1].Participant.ParticipantID != playerID {
			t.Fatalf("participant terminal deliveries = %#v", deliveries)
		}
		participant.Close()
		participant.Close()
	})

	t.Run("terminal scope fails closed and context cancellation closes once", func(t *testing.T) {
		otherTournamentID := arenaFlowUUID("71000000-0000-4000-8000-000000000005")
		wrongCoordinator, err := arenaws.NewCancellationCoordinator(otherTournamentID)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := NewArenaTerminalRegistry(map[uuid.UUID]*arenaws.CancellationCoordinator{tournamentID: wrongCoordinator})
		if err != nil {
			t.Fatal(err)
		}
		flow, err := NewArenaTerminalFlow(registry)
		if err != nil {
			t.Fatal(err)
		}
		if subscription, err := flow.SubscribeArenaTerminal(context.Background(), ArenaTerminalSubscriptionRequest{
			Role: ArenaRoleParticipant, Authenticated: true, TournamentID: tournamentID, ParticipantID: playerID,
		}); !errors.Is(err, arenaws.ErrCancellationInvalidSubscription) || subscription != nil {
			t.Fatalf("cross-scope subscription = %#v, %v", subscription, err)
		}

		empty, err := NewArenaTerminalRegistry(nil)
		if err != nil {
			t.Fatal(err)
		}
		flow, err = NewArenaTerminalFlow(empty)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		subscription, err := flow.SubscribeArenaTerminal(ctx, ArenaTerminalSubscriptionRequest{
			Role: ArenaRoleParticipant, Authenticated: true, TournamentID: tournamentID, ParticipantID: playerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		arenaFlowWaitForClosed(t, subscription.Deliveries())
		subscription.Close()
		subscription.Close()
	})
}

type arenaFlowParticipantSource func(context.Context, arenaws.ParticipantRealtimeReadQuery) (arenaws.ParticipantRealtimeReadModel, error)

func (source arenaFlowParticipantSource) ReadParticipantRealtime(ctx context.Context, query arenaws.ParticipantRealtimeReadQuery) (arenaws.ParticipantRealtimeReadModel, error) {
	return source(ctx, query)
}

type arenaFlowPublicSource struct {
	mu    sync.Mutex
	read  arenaws.PublicRealtimeReadResult
	reads int
}

func (source *arenaFlowPublicSource) PublicRealtimeRead(context.Context, uuid.UUID) (arenaws.PublicRealtimeReadResult, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.reads++
	return source.read, nil
}

func (source *arenaFlowPublicSource) readCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.reads
}

type arenaFlowOperatorSource struct {
	state arenaws.OperatorRealtimeState
	query arenaws.OperatorRealtimeQuery
	reads int
}

func (source *arenaFlowOperatorSource) ReadOperatorRealtime(_ context.Context, query arenaws.OperatorRealtimeQuery) (arenaws.OperatorRealtimeState, error) {
	source.reads++
	source.query = query
	return source.state, nil
}

func arenaFlowPublic(t *testing.T, source arenaws.PublicRealtimeReadSource, maxConnections int) *ArenaPublicFlow {
	t.Helper()
	flow, err := NewArenaPublicFlow(source, &arenaws.PublicRealtimeConfig{MaxConnections: maxConnections, MaxReplayEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	return flow
}

func arenaFlowWaitForPublicCapacity(t *testing.T, flow *ArenaPublicFlow, tournamentID uuid.UUID) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithCancel(context.Background())
		_, err := flow.OpenArenaPublic(ctx, ArenaPublicConnectionRequest{TournamentID: tournamentID})
		if err == nil {
			cancel()
			return
		}
		cancel()
		if !errors.Is(err, arenaws.ErrPublicRealtimeConnectionLimit) {
			t.Fatalf("OpenArenaPublic() while waiting for capacity error = %v", err)
		}
		select {
		case <-deadline.C:
			t.Fatal("public capacity was not released after cancellation")
		case <-ticker.C:
		}
	}
}

func arenaFlowWaitForClosed(t *testing.T, deliveries <-chan arenaws.CancellationDelivery) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case _, open := <-deliveries:
			if !open {
				return
			}
		case <-deadline.C:
			t.Fatal("terminal subscription did not close after context cancellation")
		}
	}
}

func arenaFlowDeliveries(t *testing.T, deliveries <-chan arenaws.CancellationDelivery) []arenaws.CancellationDelivery {
	t.Helper()
	var result []arenaws.CancellationDelivery
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case delivery, open := <-deliveries:
			if !open {
				return result
			}
			result = append(result, delivery)
		case <-deadline.C:
			t.Fatal("terminal deliveries did not close")
		}
	}
}

func arenaFlowParticipantRead(t *testing.T, tournamentID, playerID uuid.UUID) arenaws.ParticipantRealtimeReadModel {
	t.Helper()
	snapshot, err := arenaws.NewParticipantSnapshot(
		arenaws.ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID},
		arenaws.ParticipantSnapshotInput{TournamentID: tournamentID, PlayerID: playerID, Revision: 1, LastSequence: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	return arenaws.ParticipantRealtimeReadModel{
		Snapshot:  arenaws.ParticipantRealtimeSnapshot{Metadata: arenaFlowMetadata(tournamentID, "participant"), Payload: snapshot},
		Available: arenaws.RealtimeAvailableRange{CurrentProjectionRevision: 1},
		Events:    []arenaws.RealtimeEnvelope{},
	}
}

func arenaFlowPublicRead(t *testing.T, tournamentID uuid.UUID) arenaws.PublicRealtimeReadResult {
	t.Helper()
	snapshot := arenaFlowPublicSnapshot(t, tournamentID)
	return arenaws.PublicRealtimeReadResult{
		Snapshot:         snapshot,
		SnapshotMetadata: arenaFlowMetadata(tournamentID, "public"),
		Available:        arenaws.RealtimeAvailableRange{CurrentProjectionRevision: 1},
		Events:           []arenaws.RealtimeEnvelope{},
	}
}

func arenaFlowOperatorRead(t *testing.T, tournamentID, operatorID uuid.UUID) arenaws.OperatorRealtimeState {
	t.Helper()
	snapshot := arenaFlowOperatorSnapshot(t, tournamentID, operatorID)
	envelope, err := arenaws.NewRealtimeEnvelope(arenaFlowMetadata(tournamentID, "operator"), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return arenaws.OperatorRealtimeState{
		Available: arenaws.RealtimeAvailableRange{CurrentProjectionRevision: 1},
		Events:    []arenaws.RealtimeEnvelope{},
		Snapshot:  envelope,
	}
}

func arenaFlowCancellationCommit(t *testing.T, tournamentID, playerID, operatorID uuid.UUID) arenaws.CancellationCommitInput {
	t.Helper()
	participant := arenaFlowParticipantRead(t, tournamentID, playerID).Snapshot
	participantEnvelope, err := arenaws.NewRealtimeEnvelope(participant.Metadata, participant.Payload)
	if err != nil {
		t.Fatal(err)
	}
	publicEnvelope, err := arenaws.NewRealtimeEnvelope(arenaFlowMetadata(tournamentID, "cancel-public"), arenaFlowPublicSnapshot(t, tournamentID))
	if err != nil {
		t.Fatal(err)
	}
	operatorEnvelope, err := arenaws.NewRealtimeEnvelope(arenaFlowMetadata(tournamentID, "cancel-operator"), arenaFlowOperatorSnapshot(t, tournamentID, operatorID))
	if err != nil {
		t.Fatal(err)
	}
	return arenaws.CancellationCommitInput{
		TournamentID:   tournamentID,
		CancellationID: arenaFlowUUID("71000000-0000-4000-8000-000000000010"),
		TerminalMetadata: arenaws.RealtimeEnvelopeMetadata{
			SchemaVersion:      arenaws.ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			Sequence:           2,
			EventID:            arenaFlowUUID("71000000-0000-4000-8000-000000000011"),
			OccurredAt:         arenaFlowTime().Add(time.Second),
			ProjectionRevision: 2,
		},
		ParticipantSnapshots: map[uuid.UUID]arenaws.RealtimeEnvelope{playerID: participantEnvelope},
		PublicSnapshot:       publicEnvelope,
		OperatorSnapshot:     operatorEnvelope,
		OperatorReason:       "tournament cancelled",
	}
}

func arenaFlowPublicSnapshot(t *testing.T, tournamentID uuid.UUID) arenaws.PublicSnapshot {
	t.Helper()
	snapshot, err := arenaws.NewPublicSnapshot(tournamentID, arenaws.PublicSnapshotInput{
		Revision:     1,
		LastSequence: 1,
		Tournament: arenaws.PublicTournamentInput{
			TournamentID: tournamentID,
			Preset:       "standard",
			State:        "active",
		},
		Scoreboard:      []arenaws.PublicScoreboardEntryInput{},
		Bracket:         []arenaws.PublicBracketMatchInput{},
		LiveSeries:      []arenaws.PublicSeriesInput{},
		OfficialResults: []arenaws.PublicOfficialResultInput{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func arenaFlowOperatorSnapshot(t *testing.T, tournamentID, operatorID uuid.UUID) arenaws.OperatorSnapshot {
	t.Helper()
	snapshot, err := arenaws.NewOperatorSnapshot(
		arenaws.OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: operatorID},
		arenaws.OperatorSnapshotInput{
			TournamentID:       tournamentID,
			Revision:           1,
			LastSequence:       1,
			CorrectionRevision: 1,
			Waves:              []arenaws.OperatorWaveInput{},
			Presence:           []arenaws.OperatorPresenceInput{},
			Replays:            []arenaws.OperatorReplayInput{},
			AuditLinks:         []arenaws.OperatorAuditLinkInput{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func arenaFlowMetadata(tournamentID uuid.UUID, event string) arenaws.RealtimeEnvelopeMetadata {
	return arenaws.RealtimeEnvelopeMetadata{
		SchemaVersion:      arenaws.ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           1,
		EventID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(event)),
		OccurredAt:         arenaFlowTime(),
		ProjectionRevision: 1,
	}
}

func arenaFlowTime() time.Time {
	return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
}

func arenaFlowUUID(value string) uuid.UUID {
	return uuid.MustParse(value)
}
