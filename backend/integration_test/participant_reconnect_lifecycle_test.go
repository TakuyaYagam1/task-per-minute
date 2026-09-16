//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

type participantReconnectTestClock struct {
	at time.Time
}

func (c participantReconnectTestClock) Now() time.Time { return c.at }

type participantReconnectStartedFixture struct {
	fixture         tournamentAdminSwissProofFixture
	scope           pausedomain.GraphScope
	started         gameusecase.ReconnectAuthority
	playerID        uuid.UUID
	playerAccountID uuid.UUID
}

type participantReconnectAuthorityProvider struct {
	identity authoritydomain.Identity
}

func (provider participantReconnectAuthorityProvider) AuthorityFor(
	ctx context.Context,
	tournamentID uuid.UUID,
) (authoritydomain.Identity, error) {
	if ctx == nil || provider.identity.TournamentID != tournamentID || provider.identity.Validate() != nil {
		return authoritydomain.Identity{}, domain.ErrValidation
	}
	return provider.identity, nil
}

func TestParticipantReconnectLifecycle(t *testing.T) {
	t.Run("disconnect freezes exact remaining, deduplicates, and scopes a multi-series Wave", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		started := participantReconnectStartFixture(ctx, t, 0)
		deadline := started.started.GameClock.OriginalDeadline
		disconnectAt := deadline.Add(-10 * time.Second)
		command := participantReconnectDisconnectCommand(started, deadline)

		first, changed, err := gameusecase.NewDisconnectUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: disconnectAt},
		).Disconnect(ctx, command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, first)
		require.Equal(t, gameusecase.MutationDisconnect, first.Kind)
		require.Equal(t, int64(1), first.ExpectedAuthorityRevision)
		require.Equal(t, int64(2), first.ReconnectAuthority.Revision)
		require.Equal(t, domain.GameStatePaused, first.ReconnectAuthority.Game.State)
		require.Equal(t, disconnectAt, first.RecordedAt)
		require.Equal(t, disconnectAt, first.ReconnectAuthority.GameClock.FrozenAt)
		require.Equal(t, deadline.Sub(disconnectAt), first.ReconnectAuthority.GameClock.Remaining)
		require.Len(t, first.ReconnectAuthority.Reconnect, 1)
		interval := first.ReconnectAuthority.Reconnect[0]
		require.Equal(t, command.IntervalID, interval.ID)
		require.Equal(t, started.playerID, interval.ParticipantID)
		require.Equal(t, disconnectAt, interval.OpenedAt)
		require.Equal(t, deadline, interval.Deadline)
		require.Equal(t, pausedomain.ReconnectStateOpen, interval.State)

		otherSeriesID, otherGameID, otherState := participantReconnectGameForParticipant(
			ctx, t, started.fixture, started.fixture.participants[2],
		)
		require.NotEqual(t, first.ReconnectAuthority.Series.ID, otherSeriesID)
		require.NotEqual(t, first.ReconnectAuthority.Game.ID, otherGameID)
		require.Equal(t, string(domain.GameStateActive), otherState,
			"disconnecting one participant must not pause another executable Series in the Wave")

		participantReconnectAssertLiveDisconnect(ctx, t, started.fixture, first, command)

		replayed, replayChanged, err := gameusecase.NewDisconnectUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: disconnectAt},
		).Disconnect(ctx, command)
		require.NoError(t, err)
		require.False(t, replayChanged)
		require.Equal(t, *first, *replayed)
		participantReconnectAssertLiveDisconnect(ctx, t, started.fixture, first, command)
	})

	t.Run("coordinator fences tabs and reconnects through durable leases", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		started := participantReconnectStartFixture(ctx, t, 0)
		deadline := started.started.GameClock.OriginalDeadline
		disconnectAt := deadline.Add(-10 * time.Second)
		reconnectAt := disconnectAt.Add(5 * time.Second)
		clock := &participantReconnectTestClock{at: disconnectAt}
		connectionRepository := participantrepo.NewParticipantConnectionPostgres(
			started.fixture.tx,
			participantReconnectAuthorityProvider{identity: started.fixture.executionAuthority},
		)
		coordinator, err := connection.NewCoordinator(connection.Dependencies{
			Transactions: started.fixture.tx,
			Authority:    connectionRepository,
			Repository:   connectionRepository,
			Disconnect:   gameusecase.NewDisconnectUseCase(started.fixture.adapter, clock),
			Reconnect:    gameusecase.ReconnectNewUseCase(started.fixture.adapter, clock),
			Clock:        clock,
			Config:       connection.Config{ReconnectDuration: 30 * time.Second},
		})
		require.NoError(t, err)

		tabOne := participantReconnectConnectionCommand(started, uuid.New())
		tabTwo := participantReconnectConnectionCommand(started, uuid.New())
		participantReconnectCreateSubscriber(ctx, t, started, tabOne)
		participantReconnectCreateSubscriber(ctx, t, started, tabTwo)
		require.NoError(t, coordinator.Connect(ctx, tabOne))
		require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))
		require.NoError(t, coordinator.Connect(ctx, tabTwo))
		require.Equal(t, 2, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))

		require.NoError(t, coordinator.Disconnect(ctx, tabOne))
		require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))
		require.Equal(t, 0, participantReconnectGamePauseCount(ctx, t, started.fixture, started.started.Game.ID))

		require.NoError(t, coordinator.Disconnect(ctx, tabTwo))
		require.Equal(t, 0, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))
		paused, err := started.fixture.adapter.LoadAuthority(ctx, started.scope, started.playerID)
		require.NoError(t, err)
		require.Equal(t, domain.GameStatePaused, paused.Game.State)
		require.Equal(t, int64(2), paused.Revision)
		require.Len(t, paused.Reconnect, 1)
		require.Equal(t, disconnectAt, paused.Reconnect[0].OpenedAt)
		require.Equal(t, disconnectAt.Add(30*time.Second), paused.Reconnect[0].Deadline)
		require.Equal(t, deadline.Sub(disconnectAt), paused.GameClock.Remaining)
		require.Equal(t, int64(2), paused.GameClock.Revision)
		require.Equal(t, 1, participantReconnectGamePauseCount(ctx, t, started.fixture, started.started.Game.ID))

		clock.at = reconnectAt
		tabThree := participantReconnectConnectionCommand(started, uuid.New())
		participantReconnectCreateSubscriber(ctx, t, started, tabThree)
		require.NoError(t, coordinator.Connect(ctx, tabThree))
		require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, started.fixture, started.playerID))
		resumed, err := started.fixture.adapter.LoadAuthority(ctx, started.scope, started.playerID)
		require.NoError(t, err)
		require.Equal(t, domain.GameStateActive, resumed.Game.State)
		require.Equal(t, paused.GameClock.Remaining, resumed.GameClock.Remaining)
		require.NotNil(t, resumed.GameClock.ResumedAt)
		require.NotNil(t, resumed.GameClock.ResumedDeadline)
		require.Equal(t, reconnectAt, *resumed.GameClock.ResumedAt)
		require.Equal(t, reconnectAt.Add(paused.GameClock.Remaining), *resumed.GameClock.ResumedDeadline)
		var intervalState string
		var intervalClosedAt time.Time
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT state, closed_at
			FROM reconnect_intervals
			WHERE id = $1`, paused.Reconnect[0].ID,
		).Scan(&intervalState, &intervalClosedAt))
		require.Equal(t, string(pausedomain.ReconnectStateReconnected), intervalState)
		require.Equal(t, reconnectAt.UTC(), intervalClosedAt.UTC())
		require.Equal(t, 0, participantReconnectActivePauseCount(ctx, t, started.fixture, started.started.Game.ID))
	})

	t.Run("reconnect closes the interval and resumes the exact saved time", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		started := participantReconnectStartFixture(ctx, t, 0)
		deadline := started.started.GameClock.OriginalDeadline
		disconnectAt := deadline.Add(-10 * time.Second)
		disconnect := participantReconnectDisconnectCommand(started, deadline)
		frozen, changed, err := gameusecase.NewDisconnectUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: disconnectAt},
		).Disconnect(ctx, disconnect)
		require.NoError(t, err)
		require.True(t, changed)

		reconnectAt := disconnectAt.Add(5 * time.Second)
		reconnect := gameusecase.ReconnectCommand{
			Scope:         started.scope,
			CommandID:     uuid.New(),
			ParticipantID: started.playerID,
			IntervalID:    disconnect.IntervalID,
			Settlement:    participantReconnectSettlementIDs(),
		}
		resumed, changed, err := gameusecase.ReconnectNewUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: reconnectAt},
		).Reconnect(ctx, reconnect)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, resumed)
		require.Equal(t, gameusecase.MutationReconnect, resumed.Kind)
		require.Equal(t, frozen.ExpectedAuthorityRevision+1, resumed.ExpectedAuthorityRevision)
		require.Equal(t, frozen.ReconnectAuthority.Revision+1, resumed.ReconnectAuthority.Revision)
		require.Equal(t, domain.GameStateActive, resumed.ReconnectAuthority.Game.State)
		require.Equal(t, frozen.ReconnectAuthority.GameClock.Remaining, resumed.ReconnectAuthority.GameClock.Remaining)
		require.NotNil(t, resumed.ReconnectAuthority.GameClock.ResumedAt)
		require.NotNil(t, resumed.ReconnectAuthority.GameClock.ResumedDeadline)
		require.Equal(t, reconnectAt, *resumed.ReconnectAuthority.GameClock.ResumedAt)
		require.Equal(t, reconnectAt.Add(frozen.ReconnectAuthority.GameClock.Remaining), *resumed.ReconnectAuthority.GameClock.ResumedDeadline)

		closed, ok := participantReconnectIntervalByID(resumed.ReconnectAuthority.Reconnect, disconnect.IntervalID)
		require.True(t, ok)
		require.Equal(t, pausedomain.ReconnectStateReconnected, closed.State)
		require.NotNil(t, closed.ClosedAt)
		require.Equal(t, reconnectAt, *closed.ClosedAt)
		participantReconnectAssertLiveReconnect(ctx, t, started.fixture, resumed, reconnect, reconnectAt)
	})

	t.Run("timeout awards the connected opponent and persists terminal evidence", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		started := participantReconnectStartFixture(ctx, t, 0)
		loserID := started.playerID
		winnerID := started.fixture.participants[1]
		deadline := started.started.GameClock.OriginalDeadline
		disconnectAt := deadline.Add(-10 * time.Second)
		disconnect := participantReconnectDisconnectCommand(started, deadline)
		_, changed, err := gameusecase.NewDisconnectUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: disconnectAt},
		).Disconnect(ctx, disconnect)
		require.NoError(t, err)
		require.True(t, changed)

		timeout := gameusecase.TimeoutCommand{
			Scope:         started.scope,
			CommandID:     uuid.New(),
			ParticipantID: loserID,
			IntervalID:    disconnect.IntervalID,
			Settlement:    participantReconnectSettlementIDs(),
		}
		terminal, changed, err := gameusecase.NewTimeoutUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: deadline},
		).Expire(ctx, timeout)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, terminal)
		require.NotNil(t, terminal.ReconnectAuthority.Current)
		require.Equal(t, domain.GameStateCompleted, terminal.ReconnectAuthority.Game.State)
		require.NotNil(t, terminal.ReconnectAuthority.Game.WinnerID)
		require.Equal(t, winnerID, *terminal.ReconnectAuthority.Game.WinnerID)
		require.Equal(t, domain.GameResultReasonOperatorForfeit, terminal.ReconnectAuthority.Game.ResultReason)
		require.Equal(t, domain.SeriesStateCompleted, terminal.ReconnectAuthority.Series.State)
		require.NotNil(t, terminal.ReconnectAuthority.Series.WinnerID)
		require.Equal(t, winnerID, *terminal.ReconnectAuthority.Series.WinnerID)
		require.NotNil(t, terminal.GameResultRevision)
		require.NotNil(t, terminal.ScoreRevision)
		require.NotNil(t, terminal.SeriesResultRevision)
		require.NotNil(t, terminal.Evidence)
		require.Nil(t, terminal.ReplayRoute)

		participantReconnectAssertTerminalRows(ctx, t, started.fixture, terminal, loserID, winnerID, false)
	})

	t.Run("both offline expire into replay without selecting a random winner", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		started := participantReconnectStartFixture(ctx, t, 0)
		firstParticipant := started.fixture.participants[0]
		secondParticipant := started.fixture.participants[1]
		deadline := started.started.GameClock.OriginalDeadline
		firstDisconnectAt := deadline.Add(-20 * time.Second)
		secondDisconnectAt := deadline.Add(-10 * time.Second)
		firstDisconnect := participantReconnectDisconnectCommand(started, deadline)
		_, changed, err := gameusecase.NewDisconnectUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: firstDisconnectAt},
		).Disconnect(ctx, firstDisconnect)
		require.NoError(t, err)
		require.True(t, changed)

		secondAuthority, err := started.fixture.adapter.LoadAuthority(ctx, started.scope, secondParticipant)
		require.NoError(t, err)
		require.Equal(t, started.started.Game.ID, secondAuthority.Game.ID)
		require.Equal(t, int64(2), secondAuthority.Revision)
		secondDisconnect := gameusecase.DisconnectCommand{
			Scope:         started.scope,
			CommandID:     uuid.New(),
			ParticipantID: secondParticipant,
			IntervalID:    uuid.New(),
			Deadline:      deadline,
			Settlement:    participantReconnectSettlementIDs(),
		}
		secondRecord, changed, err := gameusecase.NewDisconnectUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: secondDisconnectAt},
		).Disconnect(ctx, secondDisconnect)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, secondRecord)
		require.Equal(t, int64(2), secondRecord.ExpectedAuthorityRevision)
		require.Equal(t, int64(3), secondRecord.ReconnectAuthority.Revision)

		timeout := gameusecase.TimeoutCommand{
			Scope:         started.scope,
			CommandID:     uuid.New(),
			ParticipantID: firstParticipant,
			IntervalID:    firstDisconnect.IntervalID,
			Settlement:    participantReconnectSettlementIDs(),
		}
		replay, changed, err := gameusecase.NewTimeoutUseCase(
			started.fixture.adapter,
			participantReconnectTestClock{at: deadline},
		).Expire(ctx, timeout)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, replay)
		require.Equal(t, secondRecord.ReconnectAuthority.Revision, replay.ExpectedAuthorityRevision)
		require.Equal(t, secondRecord.ReconnectAuthority.Revision+1, replay.ReconnectAuthority.Revision)
		require.NotNil(t, replay.ReconnectAuthority.Current)
		require.Equal(t, domain.GameStateVoid, replay.ReconnectAuthority.Game.State)
		require.Nil(t, replay.ReconnectAuthority.Game.WinnerID)
		require.Equal(t, domain.GameResultReasonDisconnect, replay.ReconnectAuthority.Game.ResultReason)
		require.Equal(t, domain.SeriesStateReplayRequired, replay.ReconnectAuthority.Series.State)
		require.Nil(t, replay.ReconnectAuthority.Series.WinnerID)
		require.NotNil(t, replay.VoidGameResultRevision)
		require.NotNil(t, replay.ScoreRevision)
		require.Nil(t, replay.SeriesResultRevision)
		require.NotNil(t, replay.ReplayRoute)
		require.NotNil(t, replay.Evidence)

		participantReconnectAssertTerminalRows(ctx, t, started.fixture, replay, firstParticipant, uuid.Nil, true)
	})
}

func participantReconnectStartFixture(
	ctx context.Context,
	t *testing.T,
	participantIndex int,
) participantReconnectStartedFixture {
	t.Helper()
	fixture := createParticipantReconnectSwissProofFixture(ctx, t)
	command := fixture.startCommand(ctx, t)
	var record *gameusecase.StartRecord
	var changed bool
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var startErr error
		record, changed, startErr = fixture.start.Start(txCtx, command)
		return startErr
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, record)
	require.Len(t, record.Games, len(fixture.binding))
	require.GreaterOrEqual(t, participantIndex, 0)
	require.Less(t, participantIndex, len(fixture.participants))

	scope := pausedomain.GraphScope{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		WaveID:       fixture.waveID,
		Authority:    fixture.executionAuthority,
	}
	participantID := fixture.participants[participantIndex]
	authority, err := fixture.adapter.LoadAuthority(ctx, scope, participantID)
	require.NoError(t, err)
	require.Equal(t, scope, authority.Scope)
	require.True(t, participantID == authority.Series.FirstParticipantID || participantID == authority.Series.SecondParticipantID)
	require.Equal(t, domain.GameStateActive, authority.Game.State)
	require.Zero(t, authority.GameClock.Remaining)
	require.Zero(t, authority.GameClock.FrozenAt)
	var playerAccountID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, participantID).Scan(&playerAccountID))
	return participantReconnectStartedFixture{fixture: fixture, scope: scope, started: authority, playerID: participantID, playerAccountID: playerAccountID}
}

func createParticipantReconnectSwissProofFixture(
	ctx context.Context,
	t *testing.T,
) tournamentAdminSwissProofFixture {
	t.Helper()

	prepareRoundProofContent(ctx, t)
	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 4)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	normalPoolRevisionID, normalPoolRevision := createParticipantReconnectContentConfiguration(
		ctx, t, tournamentID, createdAt,
	)
	fixture := createTournamentAdminSwissProofFixtureForAggregate(
		ctx, t, tournamentID, rosterID, playerIDs,
		normalPoolRevisionID, normalPoolRevision, createdAt,
	)
	for _, series := range fixture.binding {
		participantReconnectCreatePresence(ctx, t, tournamentID, rosterID, series.SeriesID,
			[]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}, createdAt)
	}
	return fixture
}

func participantReconnectCreatePresence(
	ctx context.Context,
	t *testing.T,
	tournamentID, rosterID, seriesID uuid.UUID,
	participantIDs []uuid.UUID,
	connectedAt time.Time,
) {
	t.Helper()
	for _, participantID := range participantIDs {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO presence_states (
				id, tournament_id, roster_id, series_id, participant_id,
				state, presence_epoch, revision, connected_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, 'connected', 1, 1, $6, $6)`,
			uuid.New(), tournamentID, rosterID, seriesID, participantID, connectedAt)
		require.NoError(t, err)
	}
}

func createParticipantReconnectContentConfiguration(
	ctx context.Context,
	t *testing.T,
	tournamentID uuid.UUID,
	at time.Time,
) (uuid.UUID, int64) {
	t.Helper()
	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	var normalPoolRevision int64
	err := sharedPool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, normal_pool.revision, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		ORDER BY publication.revision DESC
		LIMIT 1`).Scan(&publicationID, &normalPoolID, &normalPoolRevision, &goldenPoolID)
	require.NoError(t, err)

	configurationID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, state, pool_publication_id,
			normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, 'draft', $3, $4, $5, $6)`,
		configurationID, tournamentID, publicationID, normalPoolID, goldenPoolID, at)
	require.NoError(t, err)

	bo1PoolID := uuid.New()
	bo3PoolID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (
			id, configuration_id, format, revision, created_at
		)
		VALUES ($1, $2, 'bo1', 1, $3), ($4, $2, 'bo3', 1, $3)`,
		bo1PoolID, configurationID, at, bo3PoolID)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
		VALUES
			($1, 'web', $3), ($1, 'crypto', $3), ($1, 'forensics', $3),
			($2, 'web', $3), ($2, 'crypto', $3), ($2, 'forensics', $3),
			($2, 'reverse', $3), ($2, 'pwn', $3)`,
		bo1PoolID, bo3PoolID, at)
	require.NoError(t, err)
	insertTournamentContentStageDefaults(ctx, t, configurationID, bo1PoolID, bo3PoolID, at)
	_, err = sharedPool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, at)
	require.NoError(t, err)
	return normalPoolID, normalPoolRevision
}

func participantReconnectDisconnectCommand(
	started participantReconnectStartedFixture,
	deadline time.Time,
) gameusecase.DisconnectCommand {
	return gameusecase.DisconnectCommand{
		Scope:         started.scope,
		CommandID:     uuid.New(),
		ParticipantID: started.playerID,
		IntervalID:    uuid.New(),
		Deadline:      deadline,
		Settlement:    participantReconnectSettlementIDs(),
	}
}

func participantReconnectConnectionCommand(
	started participantReconnectStartedFixture,
	connectionID uuid.UUID,
) inbound.TournamentParticipantConnectionCommand {
	return inbound.TournamentParticipantConnectionCommand{
		TournamentID:         started.fixture.tournamentID,
		PlayerID:             started.playerAccountID,
		ConnectionID:         connectionID,
		ConnectionGeneration: 1,
	}
}

func participantReconnectCreateSubscriber(
	ctx context.Context,
	t *testing.T,
	started participantReconnectStartedFixture,
	command inbound.TournamentParticipantConnectionCommand,
) {
	t.Helper()
	connectedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO realtime_subscribers (
			id, instance_id, connection_id, connection_generation,
			tournament_id, role, principal_id, initial_sequence,
			last_acknowledged_sequence, snapshot_sequence, connected_at
		)
		VALUES ($1, $2, $3, $4, $5, 'participant', $6, 0, 0, 0, $7)`,
		uuid.New(), uuid.New(), command.ConnectionID, command.ConnectionGeneration,
		started.fixture.tournamentID, command.PlayerID, connectedAt)
	require.NoError(t, err)
}

func participantReconnectActiveLeaseCount(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	participantID uuid.UUID,
) int {
	t.Helper()
	var count int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*)
		FROM participant_connection_leases
		WHERE tournament_id = $1 AND roster_id = $2 AND participant_id = $3 AND state = 'active'`,
		fixture.tournamentID, fixture.rosterID, participantID).Scan(&count))
	return count
}

func participantReconnectGamePauseCount(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	gameID uuid.UUID,
) int {
	t.Helper()
	var count int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM pauses
		WHERE tournament_id = $1 AND roster_id = $2 AND game_attempt_id = $3`,
		fixture.tournamentID, fixture.rosterID, gameID).Scan(&count))
	return count
}

func participantReconnectActivePauseCount(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	gameID uuid.UUID,
) int {
	t.Helper()
	var count int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM pauses
		WHERE tournament_id = $1 AND roster_id = $2 AND game_attempt_id = $3 AND state = 'active'`,
		fixture.tournamentID, fixture.rosterID, gameID).Scan(&count))
	return count
}

func participantReconnectSettlementIDs() gameusecase.SettlementIDs {
	return gameusecase.SettlementIDs{
		GameResultRevisionID:   domain.OfficialResultRevisionID(uuid.New()),
		ScoreRevisionID:        domain.SeriesScoreRevisionID(uuid.New()),
		SeriesResultRevisionID: domain.OfficialResultRevisionID(uuid.New()),
		ReplayRouteID:          uuid.New(),
		AuditEventID:           uuid.New(),
		OutboxEventID:          uuid.New(),
		ProjectionRevisionID:   uuid.New(),
	}
}

func participantReconnectAssertLiveDisconnect(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	record *gameusecase.ReconnectRecord,
	command gameusecase.DisconnectCommand,
) {
	t.Helper()
	var (
		presenceState                   string
		presenceEpoch, presenceRevision int64
		presenceDisconnectedAt          time.Time
		presenceUpdatedAt               time.Time
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, presence_epoch, revision, disconnected_at, updated_at
		FROM presence_states
		WHERE tournament_id = $1 AND roster_id = $2 AND series_id = $3 AND participant_id = $4`,
		fixture.tournamentID, fixture.rosterID, record.ReconnectAuthority.Series.ID, command.ParticipantID,
	).Scan(&presenceState, &presenceEpoch, &presenceRevision, &presenceDisconnectedAt, &presenceUpdatedAt))
	require.Equal(t, string(pausedomain.PresenceStateDisconnected), presenceState)
	presence := participantReconnectPresence(record.ReconnectAuthority.Presence, command.ParticipantID)
	require.NotNil(t, presence)
	require.Equal(t, presence.PresenceEpoch, presenceEpoch)
	require.Equal(t, presence.Revision, presenceRevision)
	require.Equal(t, presence.DisconnectedAt.UTC(), presenceDisconnectedAt.UTC())
	require.Equal(t, presence.UpdatedAt.UTC(), presenceUpdatedAt.UTC())

	var pauseID uuid.UUID
	var pauseState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, state
		FROM pauses
		WHERE tournament_id = $1 AND roster_id = $2 AND game_attempt_id = $3 AND state = 'active'`,
		fixture.tournamentID, fixture.rosterID, record.ReconnectAuthority.Game.ID,
	).Scan(&pauseID, &pauseState))
	require.Equal(t, record.ReconnectAuthority.PauseID, pauseID)
	require.Equal(t, "active", pauseState)

	var frozenAt time.Time
	var frozenRemainingMS int64
	var clockRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT frozen_at, frozen_remaining_ms, revision
		FROM pause_clocks
		WHERE pause_id = $1 AND game_attempt_id = $2`, pauseID, record.ReconnectAuthority.Game.ID,
	).Scan(&frozenAt, &frozenRemainingMS, &clockRevision))
	require.Equal(t, record.ReconnectAuthority.GameClock.FrozenAt.UTC(), frozenAt.UTC())
	require.Equal(t, record.ReconnectAuthority.GameClock.Remaining.Milliseconds(), frozenRemainingMS)
	require.Equal(t, record.ReconnectAuthority.GameClock.Revision, clockRevision)

	var intervalState string
	var intervalOpenedAt, intervalDeadline time.Time
	var intervalClosed bool
	var intervalRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, opened_at, deadline_at, closed_at IS NULL, revision
		FROM reconnect_intervals
		WHERE id = $1`, command.IntervalID,
	).Scan(&intervalState, &intervalOpenedAt, &intervalDeadline, &intervalClosed, &intervalRevision))
	require.Equal(t, string(pausedomain.ReconnectStateOpen), intervalState)
	require.Equal(t, command.Deadline.UTC(), intervalDeadline.UTC())
	require.Equal(t, record.RecordedAt.UTC(), intervalOpenedAt.UTC())
	require.True(t, intervalClosed)
	require.Equal(t, int64(1), intervalRevision)

	var receiptCount, intervalCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM reconnect_command_receipts WHERE tournament_id = $1`, fixture.tournamentID).Scan(&receiptCount))
	require.Equal(t, 1, receiptCount)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM reconnect_intervals WHERE pause_id = $1`, pauseID).Scan(&intervalCount))
	require.Equal(t, 1, intervalCount)
}

func participantReconnectAssertLiveReconnect(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	record *gameusecase.ReconnectRecord,
	command gameusecase.ReconnectCommand,
	reconnectedAt time.Time,
) {
	t.Helper()
	var pauseState string
	var resolvedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, resolved_at
		FROM pauses
		WHERE id = $1`, record.ReconnectAuthority.PauseID).Scan(&pauseState, &resolvedAt))
	require.Equal(t, "resumed", pauseState)
	require.Equal(t, reconnectedAt.UTC(), resolvedAt.UTC())

	var resumedAt, resumedDeadline time.Time
	var frozenRemainingMS, clockRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT resumed_at, resumed_deadline, frozen_remaining_ms, revision
		FROM pause_clocks
		WHERE pause_id = $1 AND game_attempt_id = $2`, record.ReconnectAuthority.PauseID, record.ReconnectAuthority.Game.ID,
	).Scan(&resumedAt, &resumedDeadline, &frozenRemainingMS, &clockRevision))
	require.Equal(t, reconnectedAt.UTC(), resumedAt.UTC())
	require.Equal(t, reconnectedAt.Add(time.Duration(frozenRemainingMS)*time.Millisecond).UTC(), resumedDeadline.UTC())
	require.Equal(t, record.ReconnectAuthority.GameClock.Remaining.Milliseconds(), frozenRemainingMS)
	require.Equal(t, record.ReconnectAuthority.GameClock.Revision, clockRevision)

	var presenceState string
	var presenceEpoch, presenceRevision int64
	var connectedAt time.Time
	var disconnected bool
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, presence_epoch, revision, connected_at, disconnected_at IS NULL
		FROM presence_states
		WHERE tournament_id = $1 AND roster_id = $2 AND series_id = $3 AND participant_id = $4`,
		fixture.tournamentID, fixture.rosterID, record.ReconnectAuthority.Series.ID, command.ParticipantID,
	).Scan(&presenceState, &presenceEpoch, &presenceRevision, &connectedAt, &disconnected))
	require.Equal(t, string(pausedomain.PresenceStateConnected), presenceState)
	require.True(t, disconnected)
	presence := participantReconnectPresence(record.ReconnectAuthority.Presence, command.ParticipantID)
	require.NotNil(t, presence)
	require.Equal(t, presence.PresenceEpoch, presenceEpoch)
	require.Equal(t, presence.Revision, presenceRevision)
	require.Equal(t, reconnectedAt.UTC(), connectedAt.UTC())

	var intervalState string
	var intervalClosedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, closed_at
		FROM reconnect_intervals
		WHERE id = $1`, command.IntervalID).Scan(&intervalState, &intervalClosedAt))
	require.Equal(t, string(pausedomain.ReconnectStateReconnected), intervalState)
	require.Equal(t, reconnectedAt.UTC(), intervalClosedAt.UTC())

	var receiptCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM reconnect_command_receipts WHERE tournament_id = $1`, fixture.tournamentID).Scan(&receiptCount))
	require.Equal(t, 2, receiptCount)
}

func participantReconnectAssertTerminalRows(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	record *gameusecase.ReconnectRecord,
	loserID, winnerID uuid.UUID,
	replay bool,
) {
	t.Helper()
	require.NotNil(t, record.Evidence)
	require.NotNil(t, record.ScoreRevision)
	gameID := record.ReconnectAuthority.Game.ID
	seriesID := record.ReconnectAuthority.Series.ID
	gameResultID := uuid.Nil
	if record.GameResultRevision != nil {
		gameResultID = record.GameResultRevision.ID.UUID()
	}
	if record.VoidGameResultRevision != nil {
		gameResultID = record.VoidGameResultRevision.ID.UUID()
	}
	require.NotEqual(t, uuid.Nil, gameResultID)

	var gameState, gameReason string
	var persistedWinner uuid.NullUUID
	var persistedResultID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, result_reason, winner_id, result_revision_id
		FROM game_attempts
		WHERE id = $1 AND series_id = $2 AND roster_id = $3`, gameID, seriesID, fixture.rosterID,
	).Scan(&gameState, &gameReason, &persistedWinner, &persistedResultID))
	if replay {
		require.Equal(t, string(domain.GameStateVoid), gameState)
		require.Equal(t, string(domain.GameResultReasonDisconnect), gameReason)
		require.False(t, persistedWinner.Valid)
	} else {
		require.Equal(t, string(domain.GameStateCompleted), gameState)
		require.Equal(t, string(domain.GameResultReasonOperatorForfeit), gameReason)
		require.True(t, persistedWinner.Valid)
		require.Equal(t, winnerID, persistedWinner.UUID)
	}
	require.Equal(t, gameResultID, persistedResultID)
	if persistedWinner.Valid {
		require.NotEqual(t, loserID, persistedWinner.UUID)
	}

	var seriesState string
	var seriesWinner, resultHeadID uuid.NullUUID
	var scoreHeadID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, winner_id, current_score_revision_id, current_result_revision_id
		FROM series
		WHERE id = $1 AND tournament_id = $2 AND roster_id = $3`, seriesID, fixture.tournamentID, fixture.rosterID,
	).Scan(&seriesState, &seriesWinner, &scoreHeadID, &resultHeadID))
	if replay {
		require.Equal(t, string(domain.SeriesStateReplayRequired), seriesState)
		require.False(t, seriesWinner.Valid)
	} else {
		require.Equal(t, string(domain.SeriesStateCompleted), seriesState)
		require.True(t, seriesWinner.Valid)
		require.Equal(t, winnerID, seriesWinner.UUID)
	}
	require.Equal(t, record.ScoreRevision.ID.UUID(), scoreHeadID)
	if record.SeriesResultRevision != nil {
		require.True(t, resultHeadID.Valid)
		require.Equal(t, record.SeriesResultRevision.ID.UUID(), resultHeadID.UUID)
	} else {
		require.False(t, resultHeadID.Valid)
	}

	var count int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM result_events WHERE id IN (
			SELECT result_event_id FROM official_result_revisions WHERE id = $1
		) AND attempt_id = $2`, gameResultID, gameID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM official_result_revisions
		WHERE id = $1 AND entity_kind = 'game_attempt' AND entity_id = $2`, gameResultID, gameID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM series_score_revisions
		WHERE id = $1 AND series_id = $2 AND roster_id = $3`, record.ScoreRevision.ID.UUID(), seriesID, fixture.rosterID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM result_projection_evidence
		WHERE id = $1 AND tournament_id = $2 AND roster_id = $3`, record.Evidence.ProjectionRevisionID, fixture.tournamentID, fixture.rosterID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM result_projection_nodes
		WHERE id IN ($1, $2) AND tournament_id = $3 AND roster_id = $4`, gameResultID, record.ScoreRevision.ID.UUID(), fixture.tournamentID, fixture.rosterID).Scan(&count))
	require.Equal(t, 2, count)
	if record.SeriesResultRevision != nil {
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT count(*) FROM official_result_revisions
			WHERE id = $1 AND entity_kind = 'series' AND entity_id = $2`, record.SeriesResultRevision.ID.UUID(), seriesID).Scan(&count))
		require.Equal(t, 1, count)
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT count(*) FROM result_projection_nodes
			WHERE id = $1 AND artifact_kind = 'series_result' AND entity_id = $2`, record.SeriesResultRevision.ID.UUID(), seriesID).Scan(&count))
		require.Equal(t, 1, count)
	}
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events WHERE id = $1 AND tournament_id = $2`, record.Evidence.AuditEventID, fixture.tournamentID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_events WHERE id = $1 AND tournament_id = $2`, record.Evidence.OutboxEventID, fixture.tournamentID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM reconnect_command_receipts WHERE command_id = $1`, participantReconnectRecordCommandID(record)).Scan(&count))
	require.Equal(t, 1, count)

	if replay {
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT count(*) FROM wave_member_routes
			WHERE wave_id = $1 AND series_id = $2 AND game_attempt_id = $3`, fixture.waveID, seriesID, gameID).Scan(&count))
		require.Equal(t, 1, count)
	} else {
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT count(*) FROM wave_member_routes
			WHERE wave_id = $1 AND series_id = $2 AND game_attempt_id = $3`, fixture.waveID, seriesID, gameID).Scan(&count))
		require.Zero(t, count)
	}
}

func participantReconnectGameForParticipant(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	participantID uuid.UUID,
) (seriesID, gameID uuid.UUID, state string) {
	t.Helper()
	err := sharedPool.QueryRow(ctx, `
		SELECT series.id, attempt.id, attempt.state
		FROM wave_series AS membership
		INNER JOIN series ON series.id = membership.series_id
		INNER JOIN game_slots AS slot ON slot.series_id = series.id AND slot.roster_id = series.roster_id
		INNER JOIN game_attempts AS attempt ON attempt.slot_id = slot.id
		WHERE membership.wave_id = $1
			AND membership.roster_id = $2
			AND (series.first_participant_id = $3 OR series.second_participant_id = $3)
		ORDER BY attempt.attempt_number DESC, attempt.id DESC
		LIMIT 1`, fixture.waveID, fixture.rosterID, participantID).Scan(&seriesID, &gameID, &state)
	require.NoError(t, err)
	return seriesID, gameID, state
}

func participantReconnectPresence(
	values []pausedomain.PausePresence,
	participantID uuid.UUID,
) *pausedomain.PausePresence {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func participantReconnectIntervalByID(
	values []pausedomain.PauseReconnectInterval,
	id uuid.UUID,
) (pausedomain.PauseReconnectInterval, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return pausedomain.PauseReconnectInterval{}, false
}

func participantReconnectRecordCommandID(record *gameusecase.ReconnectRecord) uuid.UUID {
	switch record.Kind {
	case gameusecase.MutationDisconnect:
		return record.DisconnectCommand.CommandID
	case gameusecase.MutationReconnect:
		return record.ReconnectCommand.CommandID
	case gameusecase.MutationTimeout:
		return record.TimeoutCommand.CommandID
	default:
		return uuid.Nil
	}
}
