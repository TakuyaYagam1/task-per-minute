//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pauserepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/pause"
	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	reconnectrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

func TestPausedPresenceTransitionsUsePostgresTimestampPrecision(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	started := normalPauseStartedFixture(t, ctx)
	clock := &participantReconnectTestClock{}
	connectionRepository := participantrepo.NewParticipantConnectionPostgres(
		started.fixture.tx,
		participantReconnectAuthorityProvider{identity: started.fixture.executionAuthority},
	)
	pauseRepository := pauserepo.NewTournamentAdminNormalPausePostgres(started.fixture.tx)
	presenceRepository := &pausedPresenceRepositoryProbe{
		next: reconnectrepo.NewTournamentPausedPresencePostgres(started.fixture.tx, pauseRepository),
	}
	coordinator, err := connection.NewCoordinator(connection.Dependencies{
		Transactions: started.fixture.tx,
		Authority:    connectionRepository,
		Repository:   connectionRepository,
		PausedPresence: gamepause.NewPausedPresenceUseCase(
			started.fixture.tx,
			presenceRepository,
			clock,
		),
		Clock:  clock,
		Config: connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)

	command := participantReconnectConnectionCommand(started, uuid.New())
	participantReconnectCreateSubscriber(ctx, t, started, command)
	clock.at = normalPauseDatabaseTime(ctx, t)
	require.NoError(t, coordinator.Connect(ctx, command))

	authority, err := pauseRepository.LoadNormalPauseAuthority(ctx, started.scope)
	require.NoError(t, err)
	pausedAt := normalPauseDatabaseTime(ctx, t)
	operatorPause, changed, err := gamepause.NewNormalPauseGraphUseCase(
		started.fixture.tx, pauseRepository, participantReconnectTestClock{at: pausedAt},
	).Enter(ctx, gamepause.NormalPauseCommand{
		Scope: started.scope, CommandID: uuid.New(), PauseID: uuid.New(), ActorID: uuid.New(),
		Reason: gamepause.PauseReasonOperator, Expected: authority.Revisions,
	})
	require.NoError(t, err)
	require.True(t, changed)
	storeNormalPauseReceipt(t, ctx, started, operatorPause)

	before, err := pauseRepository.LoadPauseResumeAuthority(ctx, started.scope, operatorPause.PauseID)
	require.NoError(t, err)
	var originalPresence pausedomain.PausePresence
	for _, snapshot := range before.Pause.Graph.Presence {
		if snapshot.ParticipantID == started.playerID {
			originalPresence = snapshot
			break
		}
	}
	require.NotEqual(t, uuid.Nil, originalPresence.ID)
	changedAt := pausedAt.Add(time.Second + 123*time.Nanosecond)
	require.NotZero(t, changedAt.Nanosecond()%1000, "fixture clock must include sub-microsecond precision")
	var postgresChangedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT $1::timestamptz`, changedAt).Scan(&postgresChangedAt))
	require.NotEqual(t, changedAt, postgresChangedAt, "PostgreSQL must demonstrate its timestamp precision boundary")
	require.Zero(t, postgresChangedAt.Nanosecond()%1000)

	clock.at = changedAt
	if err := coordinator.Disconnect(ctx, command); err != nil {
		t.Fatalf("Disconnect returned %s; paused presence repository events: %+v", pausedPresenceProbeErrorClass(err), presenceRepository.events)
	}

	var (
		state                                  string
		presenceEpoch, presenceRevision        int64
		connectedAt, disconnectedAt, updatedAt time.Time
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, presence_epoch, revision, connected_at, disconnected_at, updated_at
		FROM presence_states
		WHERE tournament_id = $1 AND roster_id = $2 AND participant_id = $3`,
		started.scope.TournamentID, started.scope.RosterID, started.playerID,
	).Scan(&state, &presenceEpoch, &presenceRevision, &connectedAt, &disconnectedAt, &updatedAt))
	require.Equal(t, string(pausedomain.PresenceStateDisconnected), state)
	require.Equal(t, originalPresence.PresenceEpoch+1, presenceEpoch)
	require.Equal(t, originalPresence.Revision+1, presenceRevision)
	require.Equal(t, postgresChangedAt, disconnectedAt)
	require.Equal(t, postgresChangedAt, updatedAt)
	require.True(t, originalPresence.ConnectedAt.Equal(connectedAt))

	afterDisconnect, err := pauseRepository.LoadPauseResumeAuthority(ctx, started.scope, operatorPause.PauseID)
	require.NoError(t, err)
	require.Equal(t, before.Pause, afterDisconnect.Pause)
	require.Equal(t, before.Reconnect, afterDisconnect.Reconnect)
	require.Equal(t, before.Counters, afterDisconnect.Counters)
	require.Equal(t, before.FrozenDeadlines, afterDisconnect.FrozenDeadlines)
	require.Equal(t, before.TerminalActionRevision, afterDisconnect.TerminalActionRevision)
	require.Zero(t, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))

	reconnect := participantReconnectConnectionCommand(started, uuid.New())
	participantReconnectCreateSubscriber(ctx, t, started, reconnect)
	reconnectedAt := changedAt.Add(time.Second + 789*time.Nanosecond)
	require.NotZero(t, reconnectedAt.Nanosecond()%1000, "reconnect clock must include sub-microsecond precision")
	var postgresReconnectedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT $1::timestamptz`, reconnectedAt).Scan(&postgresReconnectedAt))
	require.NotEqual(t, reconnectedAt, postgresReconnectedAt, "PostgreSQL must demonstrate its timestamp precision boundary")
	require.Zero(t, postgresReconnectedAt.Nanosecond()%1000)
	clock.at = reconnectedAt
	if err := coordinator.Connect(ctx, reconnect); err != nil {
		t.Fatalf("Connect returned %s; paused presence repository events: %+v", pausedPresenceProbeErrorClass(err), presenceRepository.events)
	}

	var (
		reconnectedState                             string
		reconnectedEpoch, reconnectedRevision        int64
		reconnectedConnectedAt, reconnectedUpdatedAt time.Time
		disconnectedAtIsNull                         bool
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, presence_epoch, revision, connected_at, updated_at, disconnected_at IS NULL
		FROM presence_states
		WHERE tournament_id = $1 AND roster_id = $2 AND participant_id = $3`,
		started.scope.TournamentID, started.scope.RosterID, started.playerID,
	).Scan(&reconnectedState, &reconnectedEpoch, &reconnectedRevision, &reconnectedConnectedAt, &reconnectedUpdatedAt, &disconnectedAtIsNull))
	require.Equal(t, string(pausedomain.PresenceStateConnected), reconnectedState)
	require.Equal(t, originalPresence.PresenceEpoch+2, reconnectedEpoch)
	require.Equal(t, originalPresence.Revision+2, reconnectedRevision)
	require.Equal(t, postgresReconnectedAt, reconnectedConnectedAt)
	require.Equal(t, postgresReconnectedAt, reconnectedUpdatedAt)
	require.True(t, disconnectedAtIsNull)
	require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))

	afterReconnect, err := pauseRepository.LoadPauseResumeAuthority(ctx, started.scope, operatorPause.PauseID)
	require.NoError(t, err)
	require.Equal(t, before.Pause, afterReconnect.Pause)
	require.Equal(t, before.Reconnect, afterReconnect.Reconnect)
	require.Equal(t, before.Counters, afterReconnect.Counters)
	require.Equal(t, before.FrozenDeadlines, afterReconnect.FrozenDeadlines)
	require.Equal(t, before.TerminalActionRevision, afterReconnect.TerminalActionRevision)

	commitCount, loadCount := 0, 0
	for _, event := range presenceRepository.events {
		switch event.stage {
		case "commit-presence":
			commitCount++
			require.Equal(t, "none", event.errorClass)
			require.True(t, event.recordPresent)
			require.True(t, event.changed)
			require.False(t, event.inputHasSubmicrosecond)
		case "load-authority":
			loadCount++
			require.True(t, event.reconnectNonNil)
			require.True(t, event.countersNonNil)
		}
	}
	require.Equal(t, 2, commitCount)
	require.Equal(t, 2, loadCount)
}

type pausedPresenceRepositoryProbeEvent struct {
	stage                           string
	errorClass                      string
	found, recordPresent, changed   bool
	reconnectNonNil, countersNonNil bool
	inputHasSubmicrosecond          bool
}

type pausedPresenceRepositoryProbe struct {
	next   gamepause.PausedPresenceRepository
	events []pausedPresenceRepositoryProbeEvent
}

func (probe *pausedPresenceRepositoryProbe) FindPausedPresenceCommand(
	ctx context.Context,
	tournamentID, commandID uuid.UUID,
) (*gamepause.PausedPresenceRecord, error) {
	record, err := probe.next.FindPausedPresenceCommand(ctx, tournamentID, commandID)
	probe.events = append(probe.events, pausedPresenceRepositoryProbeEvent{
		stage: "find-command", errorClass: pausedPresenceProbeErrorClass(err), found: record != nil,
	})
	return record, err
}

func (probe *pausedPresenceRepositoryProbe) LoadPausedPresenceAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	participantID uuid.UUID,
) (gamepause.PausedPresenceAuthority, error) {
	authority, err := probe.next.LoadPausedPresenceAuthority(ctx, scope, participantID)
	probe.events = append(probe.events, pausedPresenceRepositoryProbeEvent{
		stage: "load-authority", errorClass: pausedPresenceProbeErrorClass(err),
		reconnectNonNil: authority.Reconnect != nil, countersNonNil: authority.Counters != nil,
	})
	return authority, err
}

func (probe *pausedPresenceRepositoryProbe) CommitPausedPresence(
	ctx context.Context,
	expected gamepause.PausedPresenceExpectation,
	record gamepause.PausedPresenceRecord,
) (*gamepause.PausedPresenceRecord, bool, error) {
	committed, changed, err := probe.next.CommitPausedPresence(ctx, expected, record)
	probe.events = append(probe.events, pausedPresenceRepositoryProbeEvent{
		stage: "commit-presence", errorClass: pausedPresenceProbeErrorClass(err),
		recordPresent: committed != nil, changed: changed,
		inputHasSubmicrosecond: record.ChangedAt.Nanosecond()%1000 != 0,
	})
	return committed, changed, err
}

func pausedPresenceProbeErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case strings.Contains(err.Error(), "committed paused presence differs from record"):
		return "committed-presence-record-mismatch"
	case strings.Contains(err.Error(), "internal error"):
		return "internal-error"
	case strings.Contains(err.Error(), "conflict"):
		return "conflict"
	default:
		return "other-error"
	}
}
