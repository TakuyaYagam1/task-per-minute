//go:build integration

package integration_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	inboundws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	realtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

func TestArenaOutboxWorkerPublishesToActiveWebSocketSession(t *testing.T) {
	ctx := context.Background()
	fixture := createDraftMigrationFixture(ctx, t)
	projectionRevision := arenaProjectionRevision(ctx, t, fixture.tournamentID, fixture.rosterID)
	repository := realtimerepo.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
	realtime, err := inboundws.NewRealtimeDelivery(repository, inboundws.RealtimeDeliveryConfig{
		InstanceID: uuid.New(),
		WorkerID:   uuid.New(),
	})
	require.NoError(t, err)

	server := inboundws.NewServer(nil,
		inboundws.WithTournamentPublicFlow(arenaPublicFlow{
			projectionRevision: projectionRevision,
			occurredAt:         time.Now().UTC().Truncate(time.Microsecond),
		}),
		inboundws.WithRealtimeDelivery(realtime),
	)
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	connection, response, err := coderws.Dial(
		t.Context(),
		arenaPublicWebSocketURL(httpServer.URL, fixture.tournamentID),
		nil,
	)
	require.NoError(t, err)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	t.Cleanup(func() { _ = connection.CloseNow() })

	_, initialData, err := connection.Read(t.Context())
	require.NoError(t, err)
	initial, err := inboundws.DecodeTournamentPublicMessage(initialData)
	require.NoError(t, err)
	require.NotNil(t, initial.Public)
	require.Equal(t, int64(0), initial.Public.Envelope.Sequence)

	terminal := createRealtimeCancellationTerminal(
		ctx,
		t,
		fixture.tournamentID,
		fixture.rosterID,
		time.Now().UTC().Truncate(time.Microsecond),
	)
	worker, err := eventdelivery.NewWorker(repository, realtime, eventdelivery.WorkerConfig{
		WorkerID: uuid.New(),
	})
	require.NoError(t, err)

	result, err := worker.Process(t.Context())
	require.NoError(t, err)
	require.Equal(t, eventdelivery.ProcessResult{Claimed: 1, Acknowledged: 1}, result)

	_, snapshotData, err := connection.Read(t.Context())
	require.NoError(t, err)
	snapshot, err := inboundws.DecodeTournamentPublicMessage(snapshotData)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Public)
	require.Equal(t, terminal.ID, snapshot.Public.Envelope.EventID)
	require.Equal(t, terminal.Sequence, snapshot.Public.Envelope.Sequence)

	_, terminalData, err := connection.Read(t.Context())
	require.NoError(t, err)
	terminalMessage, err := inboundws.DecodeTournamentPublicMessage(terminalData)
	require.NoError(t, err)
	require.NotNil(t, terminalMessage.Terminal)
	require.Equal(t, terminal.ID, terminalMessage.Terminal.EventID)
	require.Equal(t, terminal.Sequence, terminalMessage.Terminal.Sequence)

	requireArenaDeliveryReceipt(t, terminal)
	empty, err := worker.Process(t.Context())
	require.NoError(t, err)
	require.Equal(t, eventdelivery.ProcessResult{}, empty)
}

type arenaPublicFlow struct {
	projectionRevision int64
	occurredAt         time.Time
}

func (flow arenaPublicFlow) OpenTournamentPublic(
	_ context.Context,
	request inboundws.TournamentPublicConnectionRequest,
) (inboundws.TournamentPublicPayload, error) {
	snapshot, err := tournamentws.NewPublicSnapshot(request.TournamentID, tournamentws.PublicSnapshotInput{
		Revision:     flow.projectionRevision,
		LastSequence: 0,
		Tournament: tournamentws.PublicTournamentInput{
			TournamentID: request.TournamentID,
			Preset:       "tournament_v1",
			State:        "draft",
			RosterSize:   0,
		},
		Scoreboard:      []tournamentws.PublicScoreboardEntryInput{},
		Bracket:         []tournamentws.PublicBracketMatchInput{},
		LiveSeries:      []tournamentws.PublicSeriesInput{},
		OfficialResults: []tournamentws.PublicOfficialResultInput{},
	})
	if err != nil {
		return inboundws.TournamentPublicPayload{}, err
	}
	envelope, err := tournamentws.NewRealtimeEnvelope(tournamentws.RealtimeEnvelopeMetadata{
		SchemaVersion:      tournamentws.TournamentRealtimeSchemaVersion,
		TournamentID:       request.TournamentID,
		Sequence:           0,
		EventID:            uuid.New(),
		OccurredAt:         flow.occurredAt,
		ProjectionRevision: flow.projectionRevision,
	}, snapshot)
	if err != nil {
		return inboundws.TournamentPublicPayload{}, err
	}
	return inboundws.NewTournamentPublicPayload(envelope)
}

func arenaProjectionRevision(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) int64 {
	t.Helper()
	var revision int64
	err := sharedPool.QueryRow(ctx, `
		SELECT revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
			AND state = 'published'`, tournamentID, rosterID,
	).Scan(&revision)
	require.NoError(t, err)
	return revision
}

func arenaPublicWebSocketURL(baseURL string, tournamentID uuid.UUID) string {
	path := strings.ReplaceAll(
		inboundws.TournamentPublicWebSocketPath,
		"{tournament_id}",
		tournamentID.String(),
	)
	return "ws" + strings.TrimPrefix(baseURL, "http") + path
}

func requireArenaDeliveryReceipt(t *testing.T, event eventdelivery.Event) {
	t.Helper()
	var (
		publishedAt time.Time
		outcome     string
		receipts    int
	)
	err := sharedPool.QueryRow(t.Context(), `
		SELECT published_at
		FROM outbox_events
		WHERE id = $1`, event.ID,
	).Scan(&publishedAt)
	require.NoError(t, err)
	require.False(t, publishedAt.Before(event.OccurredAt))
	err = sharedPool.QueryRow(t.Context(), `
		SELECT outcome, COUNT(*) OVER ()
		FROM realtime_delivery_receipts
		WHERE event_id = $1`, event.ID,
	).Scan(&outcome, &receipts)
	require.NoError(t, err)
	require.Equal(t, string(eventdelivery.DeliveryWritten), outcome)
	require.Equal(t, 1, receipts)
}
