//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	recoveryterminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	pauserepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/pause"
	adminsnapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	participantrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	reconnectrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	adminsnapshot "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

func TestNormalPauseResumePreservesSubMillisecondRemaining(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	started := normalPauseStartedFixture(t, ctx)
	repository := pauserepo.NewTournamentAdminNormalPausePostgres(started.fixture.tx)
	authority, err := repository.LoadNormalPauseAuthority(ctx, started.scope)
	require.NoError(t, err)
	remaining := 20*time.Second + 466*time.Microsecond
	pausedAt := started.started.GameClock.OriginalDeadline.Add(-remaining)
	paused, changed, err := gamepause.NewNormalPauseGraphUseCase(started.fixture.tx, repository,
		participantReconnectTestClock{at: pausedAt}).Enter(ctx, gamepause.NormalPauseCommand{
		Scope: started.scope, CommandID: uuid.New(), PauseID: uuid.New(), ActorID: uuid.New(),
		Reason: gamepause.PauseReasonOperator, Expected: authority.Revisions,
	})
	require.NoError(t, err)
	require.True(t, changed)
	storeNormalPauseReceipt(t, ctx, started, paused)

	// Reconstruct the frozen duration through a fresh adapter before resuming.
	repository = pauserepo.NewTournamentAdminNormalPausePostgres(started.fixture.tx)
	loaded, err := repository.LoadPauseResumeAuthority(ctx, started.scope, paused.PauseID)
	require.NoError(t, err)
	resumedAt := pausedAt.Add(5 * time.Second)
	resumeID := uuid.New()
	// Epoch rebinding references the enclosing Wave command at commit time.
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO wave_control_commands (
			command_id, tournament_id, roster_id, wave_id, actor_id, action,
			source_projection_revision_id, source_projection_revision, source_tournament_revision,
			source_roster_revision, source_wave_revision, resulting_wave_revision,
			source_revisions, source_graph, request_digest, result_document, executed_at, created_at
		)
		SELECT $1, tournament_id, roster_id, wave_id, actor_id, 'resume',
			source_projection_revision_id, source_projection_revision, source_tournament_revision + 1,
			source_roster_revision, resulting_wave_revision, resulting_wave_revision + 1,
			source_revisions, result_document->'normal_pause'->'Graph', request_digest,
			'{"state":"active"}'::jsonb, $2, $2
		FROM wave_control_commands WHERE command_id = $3`, resumeID, resumedAt, paused.CommandID)
	require.NoError(t, err)
	resumed, changed, err := gamepause.NewPauseResumeUseCase(started.fixture.tx, repository,
		participantReconnectTestClock{at: resumedAt}).Resume(ctx, gamepause.PauseResumeCommand{
		Scope: started.scope, PauseID: paused.PauseID, CommandID: resumeID, ActorID: paused.ActorID,
		Expected: gamepause.PauseResumeExpectationFrom(loaded),
	})
	require.NoError(t, err)
	require.True(t, changed)
	var checked bool
	for _, frozen := range resumed.Graph.FrozenDeadlines {
		if frozen.OwnerID != started.started.Game.ID {
			continue
		}
		checked = true
		require.Equal(t, remaining, frozen.Remaining)
		require.NotNil(t, frozen.ResumedDeadline)
		require.Equal(t, resumedAt.Add(remaining), *frozen.ResumedDeadline)
		var savedDeadline time.Time
		var validRelation bool
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT resumed_deadline,
				resumed_deadline = resumed_at + frozen_remaining_ms * interval '1 millisecond'
			FROM pause_clocks WHERE game_attempt_id = $1`, frozen.OwnerID,
		).Scan(&savedDeadline, &validRelation))
		require.True(t, validRelation)
		require.Equal(t, 466*time.Microsecond, frozen.ResumedDeadline.Sub(savedDeadline))
	}
	require.True(t, checked)
	wantDeadline := resumedAt.Add(remaining)
	deadlines, err := started.fixture.tx.Querier(ctx).ListTournamentAdminNormalPauseGameDeadlines(ctx, started.scope.WaveID)
	require.NoError(t, err)
	var loadedDeadline time.Time
	for _, row := range deadlines {
		if row.GameAttemptID != started.started.Game.ID {
			continue
		}
		require.True(t, row.Deadline.Valid)
		loadedDeadline = row.Deadline.Time.UTC()
		break
	}
	require.Equal(t, wantDeadline, loadedDeadline,
		"the next pause graph must use exact persisted clock inputs, not resumed_deadline")

	sink := &capturedRecoveryDeadlineSink{}
	deadlinesRepository := recoveryrepo.NewRecoveryPostgres(started.fixture.tx, sink)
	pendingDeadlines, err := deadlinesRepository.ListPendingDeadlines(ctx, recoveryusecase.DeadlineCursor{}, recoveryusecase.MaximumSweepBatchSize)
	require.NoError(t, err)
	var pending recoveryusecase.PendingDeadline
	for _, candidate := range pendingDeadlines {
		if candidate.Kind == recoveryusecase.DeadlineKindGame && candidate.ID == started.started.Game.ID {
			pending = candidate
			break
		}
	}
	require.NotEqual(t, uuid.Nil, pending.ID)
	require.Equal(t, wantDeadline, pending.DueAt,
		"the recovery scanner must publish the exact persisted deadline")
	rearmed, err := deadlinesRepository.RearmDeadline(ctx, pending)
	require.NoError(t, err)
	require.True(t, rearmed)
	require.Equal(t, pending, sink.deadline,
		"the recovery rearmer must preserve the scanner's exact deadline")

	store := recoveryterminalrepo.NewRecoveryTerminalPostgres(
		started.fixture.tx, authorityrepo.NewExecutionAuthorityPostgres(started.fixture.tx),
		participantReconnectTestClock{at: pending.DueAt},
	)
	terminal, found, err := store.LoadDeadlineAuthority(ctx, sink.deadline)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, terminal.GameTimeout,
		"terminal recovery must accept the exact scanner deadline")

	participantID := started.started.Series.FirstParticipantID
	loadedAuthority, err := reconnectrepo.NewTournamentReconnectPostgres(started.fixture.tx).LoadAuthority(
		ctx, started.scope, participantID,
	)
	require.NoError(t, err)
	require.Equal(t, wantDeadline, loadedAuthority.GameClock.OriginalDeadline,
		"ordinary reconnect load must reconstruct the exact resumed clock")
	currentProjection := loadedAuthority.CurrentProjectionRevision
	var persistedProjection int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision_number FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'
		ORDER BY revision_number DESC LIMIT 1`, started.scope.TournamentID, started.scope.RosterID).Scan(&persistedProjection))
	require.Equal(t, persistedProjection, currentProjection)
	disconnectAt := resumedAt.Add(time.Second)
	reconnectRepository := reconnectrepo.NewTournamentReconnectPostgres(started.fixture.tx)
	disconnectCommand := gamereconnect.DisconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: participantID,
		IntervalID: uuid.New(), Deadline: wantDeadline, Settlement: participantReconnectSettlementIDs(),
	}
	disconnected, changed, err := gamereconnect.NewDisconnectUseCase(
		reconnectRepository, participantReconnectTestClock{at: disconnectAt},
	).Disconnect(ctx, disconnectCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, wantDeadline, disconnected.ReconnectAuthority.GameClock.OriginalDeadline)
	require.Equal(t, wantDeadline.Sub(disconnectAt), disconnected.ReconnectAuthority.GameClock.Remaining,
		"first disconnect after normal resume must retain the 466us remainder")

	reconnectAt := disconnectAt.Add(5 * time.Second)
	reconnected, changed, err := gamereconnect.ReconnectNewUseCase(
		reconnectRepository, participantReconnectTestClock{at: reconnectAt},
	).Reconnect(ctx, gamereconnect.ReconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: participantID,
		IntervalID: disconnectCommand.IntervalID, Settlement: participantReconnectSettlementIDs(),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, reconnected)
	require.Equal(t, gamereconnect.MutationReconnect, reconnected.Kind)
	require.NotNil(t, reconnected.ReconnectAuthority.GameClock.ResumedDeadline)

	secondDisconnectAt := reconnectAt.Add(time.Second)
	secondDisconnectCommand := gamereconnect.DisconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: participantID,
		IntervalID: uuid.New(), Deadline: *reconnected.ReconnectAuthority.GameClock.ResumedDeadline,
		Settlement: participantReconnectSettlementIDs(),
	}
	secondDisconnect, changed, err := gamereconnect.NewDisconnectUseCase(
		reconnectRepository, participantReconnectTestClock{at: secondDisconnectAt},
	).Disconnect(ctx, secondDisconnectCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, secondDisconnect)

	secondReconnectAt := secondDisconnectAt.Add(5 * time.Second)
	secondReconnect, changed, err := gamereconnect.ReconnectNewUseCase(
		reconnectRepository, participantReconnectTestClock{at: secondReconnectAt},
	).Reconnect(ctx, gamereconnect.ReconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: participantID,
		IntervalID: secondDisconnectCommand.IntervalID, Settlement: participantReconnectSettlementIDs(),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, secondReconnect)
	require.Equal(t, gamereconnect.MutationReconnect, secondReconnect.Kind)
	var secondReconnectIntervalFound bool
	for _, interval := range secondReconnect.ReconnectAuthority.Reconnect {
		if interval.ID != secondDisconnectCommand.IntervalID {
			continue
		}
		secondReconnectIntervalFound = true
		require.Equal(t, pausedomain.ReconnectStateReconnected, interval.State)
		require.Equal(t, 2, interval.Number)
		require.Equal(t, 0, interval.ContinuationNumber)
	}
	require.True(t, secondReconnectIntervalFound)
	var participantReconnectSlotsUsed int
	for _, counter := range secondReconnect.ReconnectAuthority.Counters {
		if counter.ParticipantID == participantID {
			participantReconnectSlotsUsed = counter.Used
			break
		}
	}
	require.Equal(t, 2, participantReconnectSlotsUsed)

	operatorPauseRepository := pauserepo.NewTournamentAdminNormalPausePostgres(started.fixture.tx)
	operatorPauseAuthority, err := operatorPauseRepository.LoadNormalPauseAuthority(ctx, started.scope)
	require.NoError(t, err)
	operatorPausedAt := secondReconnectAt.Add(time.Second)
	operatorPauseCommand := gamepause.NormalPauseCommand{
		Scope: started.scope, CommandID: uuid.New(), PauseID: uuid.New(), ActorID: uuid.New(),
		Reason: gamepause.PauseReasonOperator, Expected: operatorPauseAuthority.Revisions,
	}
	operatorPaused, changed, err := gamepause.NewNormalPauseGraphUseCase(
		started.fixture.tx, operatorPauseRepository, participantReconnectTestClock{at: operatorPausedAt},
	).Enter(ctx, operatorPauseCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, operatorPaused)
	require.Equal(t, operatorPauseCommand.PauseID, operatorPaused.PauseID)
	storeNormalPauseReceipt(t, ctx, started, operatorPaused)
}

func TestRecoveryStaleReconnectAfterTournamentCancellation(t *testing.T) {
	for _, reconnected := range []bool{false, true} {
		name := "disconnected_at_cancellation"
		if reconnected {
			name = "reconnected_before_cancellation"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			started, candidate := normalPausePendingReconnectFixture(t, ctx)
			if reconnected {
				normalPauseReconnectCandidate(t, ctx, started, candidate)
			}
			cancelNormalPauseTournament(t, started)
			before := normalPauseRecoveryState(t, ctx, candidate)
			var tournamentState string
			require.NoError(t, sharedPool.QueryRow(ctx,
				`SELECT state FROM tournaments WHERE id = $1`, candidate.TournamentID,
			).Scan(&tournamentState))
			require.Equal(t, string(domain.TournamentStateCancelled), tournamentState)

			handler := normalPauseExpiredLeaseHandler(t, ctx, started, candidate)
			changed, err := handler.HandleDeadline(ctx, candidate)
			require.JSONEq(t, string(before), string(normalPauseRecoveryState(t, ctx, candidate)),
				"a stale cancelled-tournament deadline must not mutate children or create a recovery receipt")
			require.False(t, changed)
			require.NoError(t, err, "a cancelled tournament makes its queued reconnect deadline stale even after the lease expires")
		})
	}
}

func TestRecoveryClosedReconnectWithExpiredLease(t *testing.T) {
	ctx := context.Background()
	started, candidate := normalPausePendingReconnectFixture(t, ctx)
	normalPauseReconnectCandidate(t, ctx, started, candidate)
	before := normalPauseRecoveryState(t, ctx, candidate)
	var tournamentState, intervalState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT tournament.state, interval.state
		FROM tournaments AS tournament
		JOIN pauses AS pause ON pause.tournament_id = tournament.id
		JOIN reconnect_intervals AS interval ON interval.pause_id = pause.id
		WHERE tournament.id = $1 AND interval.id = $2`, candidate.TournamentID, candidate.ID,
	).Scan(&tournamentState, &intervalState))
	require.Equal(t, string(domain.TournamentStateSwiss), tournamentState)
	require.Equal(t, string(pausedomain.ReconnectStateReconnected), intervalState)

	handler := normalPauseExpiredLeaseHandler(t, ctx, started, candidate)
	changed, err := handler.HandleDeadline(ctx, candidate)
	require.JSONEq(t, string(before), string(normalPauseRecoveryState(t, ctx, candidate)))
	require.False(t, changed)
	require.NoError(t, err, "a reconnected interval makes its queued deadline stale even after the lease expires")
}

func TestRecoveryPendingReconnectRejectsExpiredLease(t *testing.T) {
	ctx := context.Background()
	started, candidate := normalPausePendingReconnectFixture(t, ctx)
	before := normalPauseRecoveryState(t, ctx, candidate)
	var tournamentState, intervalState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT tournament.state, interval.state
		FROM tournaments AS tournament
		JOIN pauses AS pause ON pause.tournament_id = tournament.id
		JOIN reconnect_intervals AS interval ON interval.pause_id = pause.id
		WHERE tournament.id = $1 AND interval.id = $2`, candidate.TournamentID, candidate.ID,
	).Scan(&tournamentState, &intervalState))
	require.Equal(t, string(domain.TournamentStateSwiss), tournamentState)
	require.Equal(t, string(pausedomain.ReconnectStateOpen), intervalState)

	handler := normalPauseExpiredLeaseHandler(t, ctx, started, candidate)
	changed, err := handler.HandleDeadline(ctx, candidate)
	require.ErrorIs(t, err, domain.ErrConflict, "a genuinely pending reconnect must retain the live authority fence")
	require.False(t, changed)
	require.JSONEq(t, string(before), string(normalPauseRecoveryState(t, ctx, candidate)))
}

func normalPauseReconnectCandidate(
	t *testing.T,
	ctx context.Context,
	started participantReconnectStartedFixture,
	candidate recoveryusecase.PendingDeadline,
) {
	t.Helper()
	record, changed, err := gamereconnect.ReconnectNewUseCase(
		started.fixture.adapter, participantReconnectTestClock{at: normalPauseDatabaseTime(ctx, t)},
	).Reconnect(ctx, gamereconnect.ReconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: candidate.ParticipantID,
		IntervalID: candidate.ID, Settlement: participantReconnectSettlementIDs(),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, gamereconnect.MutationReconnect, record.Kind)
}

func TestNormalPauseAfterTwoParticipantReconnectCycles(t *testing.T) {
	for _, reconnectDuringPause := range []bool{false, true} {
		name := "reconnect_after_resume"
		if reconnectDuringPause {
			name = "reconnect_during_pause"
		}
		t.Run(name, func(t *testing.T) { testNormalPauseMixedReconnect(t, reconnectDuringPause, false) })
	}
}

func TestNormalPauseConnectedSourceResumesBesideDisconnectedSeries(t *testing.T) {
	testNormalPauseMixedReconnect(t, true, true)
}

func testNormalPauseMixedReconnect(t *testing.T, reconnectDuringPause, disconnectOtherSeries bool) {
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
	coordinator, err := connection.NewCoordinator(connection.Dependencies{
		Transactions: started.fixture.tx,
		Authority:    connectionRepository,
		Repository:   connectionRepository,
		Disconnect:   gamereconnect.NewDisconnectUseCase(started.fixture.adapter, clock),
		Reconnect:    gamereconnect.ReconnectNewUseCase(started.fixture.adapter, clock),
		PausedPresence: gamepause.NewPausedPresenceUseCase(started.fixture.tx,
			reconnectrepo.NewTournamentPausedPresencePostgres(started.fixture.tx, pauseRepository), clock),
		Clock:  clock,
		Config: connection.Config{ReconnectDuration: 30 * time.Second},
	})
	require.NoError(t, err)

	firstParticipant := started
	secondParticipant := started
	switch started.playerID {
	case started.started.Series.FirstParticipantID:
		secondParticipant.playerID = started.started.Series.SecondParticipantID
	case started.started.Series.SecondParticipantID:
		secondParticipant.playerID = started.started.Series.FirstParticipantID
	default:
		t.Fatalf("started participant %s is not in Series %s", started.playerID, started.started.Series.ID)
	}
	require.NotEqual(t, started.playerID, secondParticipant.playerID,
		"the two leases on the active game must belong to distinct participants")
	require.NotEqual(t, uuid.Nil, secondParticipant.playerID)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, secondParticipant.playerID,
	).Scan(&secondParticipant.playerAccountID))
	firstSeriesID := started.started.Series.ID
	firstGameID := started.started.Game.ID
	require.Equal(t, domain.GameStateActive, started.started.Game.State)
	var otherSeriesParticipantID, otherSeriesID, otherGameID uuid.UUID
	var otherGameState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT series.id, attempt.id, series.first_participant_id, attempt.state
		FROM wave_series AS membership
		JOIN series ON series.id = membership.series_id
		JOIN game_slots AS slot ON slot.series_id = series.id AND slot.roster_id = series.roster_id
		JOIN game_attempts AS attempt ON attempt.slot_id = slot.id
		WHERE membership.wave_id = $1 AND membership.roster_id = $2 AND series.id <> $3
		ORDER BY attempt.attempt_number DESC, attempt.id DESC LIMIT 1`,
		started.scope.WaveID, started.scope.RosterID, firstSeriesID,
	).Scan(&otherSeriesID, &otherGameID, &otherSeriesParticipantID, &otherGameState))
	require.Equal(t, string(domain.GameStateActive), otherGameState)
	require.NotEqual(t, firstSeriesID, otherSeriesID)
	require.NotEqual(t, firstGameID, otherGameID)
	require.NotEqual(t, started.playerID, otherSeriesParticipantID)
	require.NotEqual(t, secondParticipant.playerID, otherSeriesParticipantID)
	otherSeriesParticipant := started
	otherSeriesParticipant.playerID = otherSeriesParticipantID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, otherSeriesParticipantID,
	).Scan(&otherSeriesParticipant.playerAccountID))
	otherSeriesPartner := started
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT participant.id, participant.player_id FROM series
		JOIN participants AS participant ON participant.id = series.second_participant_id
		WHERE series.id = $1`, otherSeriesID,
	).Scan(&otherSeriesPartner.playerID, &otherSeriesPartner.playerAccountID))
	require.NotEqual(t, otherSeriesParticipantID, otherSeriesPartner.playerID)
	participantIDs := []uuid.UUID{started.playerID, secondParticipant.playerID, otherSeriesParticipantID, otherSeriesPartner.playerID}
	for _, participantID := range participantIDs {
		require.Zero(t, participantReconnectActiveLeaseCount(ctx, t, started.fixture, participantID),
			"readiness setup must not leave an active socket lease for the selected participant")
	}

	activeConnection := participantReconnectConnectionCommand(firstParticipant, uuid.New())
	otherConnection := participantReconnectConnectionCommand(secondParticipant, uuid.New())
	pausedSeriesConnection := participantReconnectConnectionCommand(otherSeriesParticipant, uuid.New())
	pausedSeriesPartnerConnection := participantReconnectConnectionCommand(otherSeriesPartner, uuid.New())
	for _, item := range []struct {
		participant participantReconnectStartedFixture
		command     inbound.TournamentParticipantConnectionCommand
	}{
		{participant: firstParticipant, command: activeConnection},
		{participant: secondParticipant, command: otherConnection},
		{participant: otherSeriesParticipant, command: pausedSeriesConnection},
		{participant: otherSeriesPartner, command: pausedSeriesPartnerConnection},
	} {
		participantReconnectCreateSubscriber(ctx, t, item.participant, item.command)
		clock.at = normalPauseDatabaseTime(ctx, t)
		require.NoError(t, coordinator.Connect(ctx, item.command))
	}
	for _, participantID := range participantIDs {
		require.Equal(t, 1, participantReconnectActiveLeaseCount(ctx, t, started.fixture, participantID),
			"each selected participant must have exactly one active socket lease")
	}
	clock.at = normalPausePrecisionDisconnectTime(ctx, t, otherGameID)
	require.NoError(t, coordinator.Disconnect(ctx, pausedSeriesConnection))
	require.Zero(t, participantReconnectActiveLeaseCount(ctx, t, started.fixture, otherSeriesParticipantID),
		"disconnect must close the selected participant's final lease")
	var pausedGameState string
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, otherGameID).Scan(&pausedGameState))
	require.Equal(t, string(domain.GameStatePaused), pausedGameState,
		"the second Series must remain disconnect-paused while the first reconnects")

	for cycle := 0; cycle < 2; cycle++ {
		clock.at = normalPausePrecisionDisconnectTime(ctx, t, firstGameID)
		require.True(t, clock.at.Before(normalPauseDatabaseTime(ctx, t)))
		require.NoError(t, coordinator.Disconnect(ctx, activeConnection))
		var firstGameState string
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, firstGameID).Scan(&firstGameState))
		require.Equal(t, string(domain.GameStatePaused), firstGameState)

		activeConnection = participantReconnectConnectionCommand(firstParticipant, uuid.New())
		participantReconnectCreateSubscriber(ctx, t, firstParticipant, activeConnection)
		clock.at = normalPauseDatabaseTime(ctx, t)
		require.NoError(t, coordinator.Connect(ctx, activeConnection))
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, firstGameID).Scan(&firstGameState))
		require.Equal(t, string(domain.GameStateActive), firstGameState)
	}

	normalPauseAuthority, err := pauseRepository.LoadNormalPauseAuthority(ctx, started.scope)
	require.NoError(t, err)
	firstGameState, secondGameState := domain.GameState(""), domain.GameState("")
	var source *gamepause.PauseGameSourcePause
	for _, game := range normalPauseAuthority.Graph.Games {
		switch game.Game.ID {
		case firstGameID:
			firstGameState = game.Game.State
		case otherGameID:
			secondGameState = game.Game.State
			source = game.SourcePause
		}
	}
	require.Equal(t, domain.GameStateActive, firstGameState)
	require.Equal(t, domain.GameStatePaused, secondGameState)
	require.NotNil(t, source)
	require.Equal(t, 466*time.Microsecond, source.Clock.Remaining%time.Millisecond)
	var sourceIntervalID uuid.UUID
	var sourceUsed int
	var sourceDeadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT interval.id, interval.deadline_at, counter.slots_used
		FROM reconnect_intervals AS interval
		JOIN reconnect_slot_counters AS counter USING (pause_id, participant_id)
		WHERE interval.pause_id = $1 AND interval.participant_id = $2 AND interval.state = 'open'`,
		source.PauseID, otherSeriesParticipantID,
	).Scan(&sourceIntervalID, &sourceDeadline, &sourceUsed))
	var reconnectState string
	var reconnectNumber, continuationNumber, reconnectSlotsUsed int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT interval.state, interval.interval_number, interval.continuation_number, counter.slots_used
		FROM reconnect_intervals AS interval
		JOIN reconnect_slot_counters AS counter
		  ON counter.pause_id = interval.pause_id
		 AND counter.participant_id = interval.participant_id
		WHERE interval.game_attempt_id = $1
		  AND interval.participant_id = $2
	  AND interval.continuation_number = 0
		ORDER BY interval.interval_number DESC, interval.updated_at DESC, interval.id DESC
		LIMIT 1`, firstGameID, started.playerID,
	).Scan(&reconnectState, &reconnectNumber, &continuationNumber, &reconnectSlotsUsed))
	require.Equal(t, string(pausedomain.ReconnectStateReconnected), reconnectState)
	require.Equal(t, 2, reconnectNumber)
	require.Zero(t, continuationNumber)
	require.Equal(t, 2, reconnectSlotsUsed)

	var projectionRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision_number FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'
		ORDER BY revision_number DESC LIMIT 1`, started.scope.TournamentID, started.scope.RosterID,
	).Scan(&projectionRevision))
	repository := executionrepo.NewRepository(started.fixture.tx, resultauthority.FinalizeProjection)
	workflow := tournamentadminexecution.NewExecutionWorkflow(tournamentadminexecution.ExecutionWorkflowDependencies{
		Transactions: started.fixture.tx,
		Repository:   repository,
		NormalPause:  repository,
		Authority: participantReconnectAuthorityProvider{
			identity: started.fixture.executionAuthority,
		},
	})
	_, err = workflow.ControlWave(ctx, tournamentadminexecution.WaveCommand{
		CommandScope: tournamentadminexecution.CommandScope{
			Operator:     tournamentadminexecution.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: started.scope.TournamentID,
			CommandID:    uuid.New(),
		},
		WaveID:                     started.scope.WaveID,
		ExpectedProjectionRevision: projectionRevision,
		Action:                     tournamentadminexecution.WaveActionPause,
		Confirmed:                  true,
	})
	require.NoError(t, err, "operator pause after two successful reconnect cycles")
	snapshotView, err := adminsnapshotrepo.NewTournamentAdminSnapshotPostgres(started.fixture.tx).GetOperatorSnapshot(
		ctx,
		adminsnapshot.SnapshotQuery{
			Operator:     adminsnapshot.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: started.scope.TournamentID,
		},
	)
	require.NoError(t, err, "operator snapshot must read the durable adopted source pause")
	require.NotNil(t, snapshotView.PauseGraph)
	var snapshotSource *gamepause.PauseGameSourcePause
	var sourceSeriesSnapshot *gamepause.PauseSeries
	for index := range snapshotView.PauseGraph.Graph.Games {
		game := &snapshotView.PauseGraph.Graph.Games[index]
		if game.Game.ID == otherGameID {
			require.Nil(t, game.ResumeState, "snapshot must not create a normal Series/Game overlay")
			snapshotSource = game.SourcePause
		}
	}
	for index := range snapshotView.PauseGraph.Graph.Series {
		series := &snapshotView.PauseGraph.Graph.Series[index]
		if series.Execution.Series.ID == otherSeriesID {
			sourceSeriesSnapshot = series
		}
	}
	require.NotNil(t, snapshotSource)
	require.NotNil(t, sourceSeriesSnapshot)
	require.Equal(t, domain.SeriesStateActive, sourceSeriesSnapshot.Execution.Series.State)
	require.Nil(t, sourceSeriesSnapshot.Execution.ResumeState)
	require.Equal(t, source, snapshotSource, "snapshot must preserve the source root, clock, and presence evidence")
	require.True(t, snapshotSource.Clock.FrozenAt.Before(*snapshotView.PauseGraph.Graph.PausedAt),
		"the source clock must remain frozen at its original time")
	var expectedSourceCounters, snapshotSourceCounters []pausedomain.PauseReconnectCounter
	for _, counter := range normalPauseAuthority.Graph.Counters {
		if counter.PauseID == source.PauseID {
			expectedSourceCounters = append(expectedSourceCounters, counter)
		}
	}
	for _, counter := range snapshotView.PauseGraph.Graph.Counters {
		if counter.PauseID == source.PauseID {
			snapshotSourceCounters = append(snapshotSourceCounters, counter)
		}
	}
	require.ElementsMatch(t, expectedSourceCounters, snapshotSourceCounters,
		"snapshot must retain the source reconnect budget")
	operatorPauseID, err := pauseRepository.ActiveNormalPauseID(ctx, started.scope)
	require.NoError(t, err)
	pausedAuthority, err := pauseRepository.LoadPauseResumeAuthority(ctx, started.scope, operatorPauseID)
	require.NoError(t, err)
	t.Logf("operator pause graph revision: %d", pausedAuthority.Pause.Graph.Revision)
	for _, game := range pausedAuthority.Pause.Graph.Games {
		if game.Game.ID == otherGameID {
			require.Equal(t, source, game.SourcePause, "adoption must retain the original source clock and snapshots")
		}
	}
	var seriesState string
	var seriesPauseCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM series WHERE id = $1`, otherSeriesID).Scan(&seriesState))
	require.Equal(t, "active", seriesState)
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM pauses WHERE series_id = $1 AND scope_kind = 'series'`, otherSeriesID).Scan(&seriesPauseCount))
	require.Zero(t, seriesPauseCount, "source Series must not receive an operator overlay")
	var suspendedAt time.Time
	var suspendedBy uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT closed_at, suspended_by_pause_id FROM reconnect_intervals WHERE id = $1 AND state = 'cancelled'`, sourceIntervalID,
	).Scan(&suspendedAt, &suspendedBy))
	require.Equal(t, operatorPauseID, suspendedBy)

	// Repeated distinct pause is a conflict, not a missing-root adoption error.
	_, err = workflow.ControlWave(ctx, normalPauseWaveCommand(t, ctx, started, tournamentadminexecution.WaveActionPause))
	require.ErrorIs(t, err, domain.ErrConflict)
	if reconnectDuringPause {
		pausedSeriesConnection = participantReconnectConnectionCommand(otherSeriesParticipant, uuid.New())
		participantReconnectCreateSubscriber(ctx, t, otherSeriesParticipant, pausedSeriesConnection)
		clock.at = normalPauseDatabaseTime(ctx, t)
		require.NoError(t, coordinator.Connect(ctx, pausedSeriesConnection), "return during operator pause changes presence only")
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, otherGameID).Scan(&pausedGameState))
		require.Equal(t, "paused", pausedGameState)
		var liveState string
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM presence_states WHERE participant_id = $1`, otherSeriesParticipantID).Scan(&liveState))
		require.Equal(t, "connected", liveState)
	}
	if disconnectOtherSeries {
		clock.at = normalPauseDatabaseTime(ctx, t)
		require.NoError(t, coordinator.Disconnect(ctx, activeConnection), "other Series becomes absent during normal pause")
	}
	_, err = workflow.ControlWave(ctx, normalPauseWaveCommand(t, ctx, started, tournamentadminexecution.WaveActionResume))
	require.NoError(t, err, "operator resume of mixed source graph")
	var rootState, waveState string
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM pauses WHERE id = $1`, operatorPauseID).Scan(&rootState))
	require.Equal(t, "resumed", rootState)
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM waves WHERE id = $1`, started.scope.WaveID).Scan(&waveState))
	require.Equal(t, "active", waveState)
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM series WHERE id = $1`, otherSeriesID).Scan(&seriesState))
	require.Equal(t, "active", seriesState)
	if disconnectOtherSeries {
		var waitingState string
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, firstGameID).Scan(&waitingState))
		require.Equal(t, "paused", waitingState, "the disconnected sibling must keep waiting")
		var waitingIntervals int
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM reconnect_intervals
			WHERE game_attempt_id = $1 AND participant_id = $2 AND state = 'open'`, firstGameID, started.playerID).Scan(&waitingIntervals))
		require.Equal(t, 1, waitingIntervals)
	}
	if !reconnectDuringPause {
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, otherGameID).Scan(&pausedGameState))
		require.Equal(t, "paused", pausedGameState)
		var continuationFrom, continuationPause uuid.UUID
		var openedAt, deadline time.Time
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT pause_id, continued_from_id, continuation_number, opened_at, deadline_at
			FROM reconnect_intervals WHERE pause_id = $1 AND participant_id = $2 AND state = 'open'`, source.PauseID, otherSeriesParticipantID,
		).Scan(&continuationPause, &continuationFrom, &continuationNumber, &openedAt, &deadline))
		require.Equal(t, source.PauseID, continuationPause)
		require.Equal(t, sourceIntervalID, continuationFrom)
		require.Equal(t, 1, continuationNumber)
		require.Equal(t, sourceDeadline.Sub(suspendedAt), deadline.Sub(openedAt), "operator pause does not consume reconnect time")
		pausedSeriesConnection = participantReconnectConnectionCommand(otherSeriesParticipant, uuid.New())
		participantReconnectCreateSubscriber(ctx, t, otherSeriesParticipant, pausedSeriesConnection)
		clock.at = normalPauseDatabaseTime(ctx, t)
		require.NoError(t, coordinator.Connect(ctx, pausedSeriesConnection), "ordinary reconnect after operator resume")
	}
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state FROM game_attempts WHERE id = $1`, otherGameID).Scan(&pausedGameState))
	require.Equal(t, "active", pausedGameState)
	var sourceState string
	var frozenAt, originalDeadline, resumedAt, persistedDeadline time.Time
	var remainingMs int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT pause.state, clock.frozen_at, clock.original_deadline, clock.frozen_remaining_ms,
			clock.resumed_at, clock.resumed_deadline, counter.slots_used
		FROM pauses AS pause JOIN pause_clocks AS clock ON clock.pause_id = pause.id
		JOIN reconnect_slot_counters AS counter ON counter.pause_id = pause.id AND counter.participant_id = $2
		WHERE pause.id = $1`, source.PauseID, otherSeriesParticipantID,
	).Scan(&sourceState, &frozenAt, &originalDeadline, &remainingMs, &resumedAt, &persistedDeadline, &reconnectSlotsUsed))
	require.Equal(t, "resumed", sourceState)
	require.True(t, source.Clock.FrozenAt.Equal(frozenAt))
	require.True(t, source.Clock.OriginalDeadline.Equal(originalDeadline))
	require.Equal(t, source.Clock.Remaining.Milliseconds(), remainingMs)
	require.Equal(t, sourceUsed, reconnectSlotsUsed, "continuation must not consume another reconnect slot")
	require.Equal(t, source.Clock.Remaining.Truncate(time.Millisecond), persistedDeadline.Sub(resumedAt))
	require.True(t, normalPauseGameDeadline(ctx, t, otherGameID).Equal(resumedAt.Add(source.Clock.Remaining)))
}

func normalPausePrecisionDisconnectTime(ctx context.Context, t *testing.T, gameID uuid.UUID) time.Time {
	t.Helper()
	deadline := normalPauseGameDeadline(ctx, t, gameID)
	remaining := deadline.Sub(normalPauseDatabaseTime(ctx, t))
	require.Greater(t, remaining, 5*time.Second)
	desired := remaining.Truncate(time.Millisecond) + 466*time.Microsecond
	for desired < remaining+3*time.Millisecond {
		desired += time.Millisecond
	}
	return deadline.Add(-desired)
}

func normalPauseWaveCommand(t *testing.T, ctx context.Context, started participantReconnectStartedFixture, action tournamentadminexecution.WaveAction) tournamentadminexecution.WaveCommand {
	t.Helper()
	var revision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision_number FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published'
		ORDER BY revision_number DESC LIMIT 1`, started.scope.TournamentID, started.scope.RosterID).Scan(&revision))
	return tournamentadminexecution.WaveCommand{
		CommandScope: tournamentadminexecution.CommandScope{
			Operator:     tournamentadminexecution.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: started.scope.TournamentID, CommandID: uuid.New(),
		},
		WaveID: started.scope.WaveID, ExpectedProjectionRevision: revision, Action: action, Confirmed: true,
	}
}

type capturedRecoveryDeadlineSink struct {
	deadline recoveryusecase.PendingDeadline
}

func (sink *capturedRecoveryDeadlineSink) ArmDeadline(_ context.Context, deadline recoveryusecase.PendingDeadline) (bool, error) {
	sink.deadline = deadline
	return true, nil
}

func normalPauseStartedFixture(t *testing.T, ctx context.Context) participantReconnectStartedFixture {
	t.Helper()
	prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	players := joinTournamentFlowPlayers(t, fixture, 4)
	created := createTournamentThroughREST(t, fixture, adminToken, content.ContentRevision, "pause_precision")
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
	preflight := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
	lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, preflight)
	startSwissThroughREST(t, fixture, adminToken, created.Id)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	round := configureProductionSwissPairingsThroughREST(t, fixture, adminToken, created.Id, snapshot.NextCursor.ProjectionRevision, 1)
	snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave := findProductionSwissWave(t, snapshot, round)
	wave = controlProductionWaveThroughREST(t, fixture, adminToken, created.Id, wave.Id, snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionOpenReadyWindow)
	recordOperatorPauseGraphReadiness(t, fixture, created.Id, wave, roster, players)
	snapshot = tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	wave = controlProductionWaveThroughREST(t, fixture, adminToken, created.Id, wave.Id, snapshot.NextCursor.ProjectionRevision, api.WaveControlRequestActionStart)
	tx := newDatabaseFixture().mgr
	lease, err := authorityrepo.NewExecutionAuthorityPostgres(tx).LoadAuthority(ctx, created.Id)
	require.NoError(t, err)
	require.NotNil(t, lease)
	scope := pausedomain.GraphScope{TournamentID: created.Id, WaveID: wave.Id, Authority: lease.Identity()}
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT roster_id FROM waves WHERE id = $1`, wave.Id).Scan(&scope.RosterID))
	repository := executionrepo.NewRepository(tx, resultauthority.FinalizeProjection)
	started, err := repository.LoadAuthority(ctx, scope, wave.Members[0].ParticipantId)
	require.NoError(t, err)
	var playerAccountID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT player_id FROM participants WHERE id = $1`, wave.Members[0].ParticipantId,
	).Scan(&playerAccountID))
	var projectionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT id FROM projection_revisions WHERE tournament_id = $1 AND revision_number = $2`,
		created.Id, started.CurrentProjectionRevision).Scan(&projectionID))
	participants := make([]uuid.UUID, 0, len(wave.Members))
	for _, member := range wave.Members {
		if member.SeriesId == nil {
			continue
		}
		participants = append(participants, member.ParticipantId)
	}
	return participantReconnectStartedFixture{
		fixture: tournamentAdminSwissProofFixture{
			tx: tx, adapter: repository, tournamentID: created.Id, rosterID: scope.RosterID,
			waveID: wave.Id, projectionRevisionID: projectionID, participants: participants,
			executionAuthority: lease.Identity(), sourceProjectionRevision: started.CurrentProjectionRevision,
		},
		scope: scope, started: started, playerID: wave.Members[0].ParticipantId, playerAccountID: playerAccountID,
	}
}

func normalPauseDatabaseTime(ctx context.Context, t *testing.T) time.Time {
	t.Helper()
	var now time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now))
	return now.UTC()
}

func normalPauseGameDeadline(ctx context.Context, t *testing.T, gameID uuid.UUID) time.Time {
	t.Helper()
	var deadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COALESCE(latest_resume.resumed_at + (latest_resume.original_deadline - latest_resume.frozen_at),
			attempt.started_at + INTERVAL '180 seconds')::timestamptz
		FROM game_attempts AS attempt
		LEFT JOIN LATERAL (
			SELECT clock.resumed_at, clock.original_deadline, clock.frozen_at
			FROM pause_clocks AS clock
			JOIN pauses AS pause ON pause.id = clock.pause_id AND pause.state = 'resumed'
			WHERE clock.game_attempt_id = attempt.id
				AND clock.resumed_at IS NOT NULL AND clock.resumed_deadline IS NOT NULL
			ORDER BY clock.resumed_at DESC, clock.pause_id DESC LIMIT 1
		) AS latest_resume ON TRUE
		WHERE attempt.id = $1`, gameID,
	).Scan(&deadline))
	return deadline.UTC()
}

func storeNormalPauseReceipt(t *testing.T, ctx context.Context, started participantReconnectStartedFixture, paused *gamepause.NormalPauseRecord) {
	t.Helper()
	// Seed the enclosing Wave receipt for a pause with historical timestamp
	// precision, bypassing the HTTP workflow's millisecond normalization.
	document, err := json.Marshal(struct {
		Version int                          `json:"version"`
		View    json.RawMessage              `json:"view"`
		Pause   *gamepause.NormalPauseRecord `json:"normal_pause"`
	}{Version: 1, View: json.RawMessage(`{}`), Pause: paused})
	require.NoError(t, err)
	graph, err := json.Marshal(paused.Graph)
	require.NoError(t, err)
	revisions, err := json.Marshal(paused.Expected)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO wave_control_commands (
			command_id, tournament_id, roster_id, wave_id, actor_id, action,
			source_projection_revision_id, source_projection_revision, source_tournament_revision,
			source_roster_revision, source_wave_revision, resulting_wave_revision,
			source_revisions, source_graph, request_digest, result_document, executed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, 'pause', $6, $7, $8, 1, $9, $10, $11, $12, $13, $14, $15, $15)`,
		paused.CommandID, started.scope.TournamentID, started.scope.RosterID, started.scope.WaveID, paused.ActorID,
		started.fixture.projectionRevisionID, started.started.CurrentProjectionRevision, paused.Expected.TournamentRevision,
		paused.Expected.WaveRevision, paused.Graph.Wave.Revision, revisions, graph, make([]byte, 32), document, paused.PausedAt)
	require.NoError(t, err)
}

func normalPausePendingReconnectFixture(t *testing.T, ctx context.Context) (participantReconnectStartedFixture, recoveryusecase.PendingDeadline) {
	t.Helper()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	started := normalPauseStartedFixture(t, ctx)
	disconnectedAt := normalPauseDatabaseTime(ctx, t)
	command := gamereconnect.DisconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: started.playerID,
		IntervalID: uuid.New(), Deadline: disconnectedAt.Add(30 * time.Second),
		Settlement: participantReconnectSettlementIDs(),
	}
	record, changed, err := gamereconnect.NewDisconnectUseCase(
		started.fixture.adapter, participantReconnectTestClock{at: disconnectedAt},
	).Disconnect(ctx, command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.GameStatePaused, record.ReconnectAuthority.Game.State)

	sink := &capturedRecoveryDeadlineSink{}
	repository := recoveryrepo.NewRecoveryPostgres(started.fixture.tx, sink)
	deadlines, err := repository.ListPendingDeadlines(ctx, recoveryusecase.DeadlineCursor{}, recoveryusecase.MaximumSweepBatchSize)
	require.NoError(t, err)
	for _, candidate := range deadlines {
		if candidate.Kind != recoveryusecase.DeadlineKindReconnect || candidate.ID != command.IntervalID {
			continue
		}
		require.Equal(t, command.Deadline, candidate.DueAt)
		rearmed, armErr := repository.RearmDeadline(ctx, candidate)
		require.NoError(t, armErr)
		require.True(t, rearmed)
		require.Equal(t, candidate, sink.deadline)
		return started, candidate
	}
	t.Fatal("the open reconnect interval must be discoverable and armed before cancellation or lease expiry")
	return participantReconnectStartedFixture{}, recoveryusecase.PendingDeadline{}
}

func cancelNormalPauseTournament(t *testing.T, started participantReconnectStartedFixture) {
	t.Helper()
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, started.scope.TournamentID)
	body, err := json.Marshal(map[string]any{
		"action": "cancel", "confirmed": true, "reason": "stale reconnect recovery regression",
		"expected_projection_revision": snapshot.NextCursor.ProjectionRevision,
	})
	require.NoError(t, err)
	request, response := doTournamentFlowJSON(t, fixture, http.MethodPost,
		"/api/v1/admin/tournaments/"+started.scope.TournamentID.String()+"/actions",
		string(body), adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, response.Code, "cancellation must succeed through the operator workflow")
	fixture.validateResponse(t, request, response)
	cancelled := decodeJSON[api.Tournament](t, response)
	require.Equal(t, string(domain.TournamentStateCancelled), string(cancelled.State))
}

func normalPauseExpiredLeaseHandler(
	t *testing.T,
	ctx context.Context,
	started participantReconnectStartedFixture,
	candidate recoveryusecase.PendingDeadline,
) *recoveryusecase.TerminalDeadlineHandler {
	t.Helper()
	authority := authorityrepo.NewExecutionAuthorityPostgres(started.fixture.tx)
	lease, err := authority.LoadAuthority(ctx, candidate.TournamentID)
	require.NoError(t, err)
	require.NotNil(t, lease)
	// Lifecycle writes above use database wall time. Only this terminal read/no-op
	// attempt advances time, to reproduce a queued deadline after lease expiry.
	at := lease.ExpiresAt.Add(time.Microsecond)
	if at.Before(candidate.DueAt) {
		at = candidate.DueAt
	}
	require.False(t, lease.Proves(lease.Identity(), at))
	clock := participantReconnectTestClock{at: at}
	store := recoveryterminalrepo.NewRecoveryTerminalPostgres(started.fixture.tx, authority, clock)
	return recoveryusecase.NewTerminalDeadlineHandlerWithDependencies(started.fixture.tx, store, clock, nil)
}

func normalPauseRecoveryState(t *testing.T, ctx context.Context, candidate recoveryusecase.PendingDeadline) []byte {
	t.Helper()
	var state []byte
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT jsonb_build_array(
			tournament.state, tournament.revision, game.state, game.revision,
			pause.state, pause.revision, interval.state, interval.revision,
			(SELECT COUNT(*) FROM deadline_transition_receipts WHERE tournament_id = tournament.id)
		)
		FROM tournaments AS tournament
		JOIN pauses AS pause ON pause.tournament_id = tournament.id
		JOIN game_attempts AS game ON game.id = pause.game_attempt_id
		JOIN reconnect_intervals AS interval ON interval.pause_id = pause.id
		WHERE tournament.id = $1 AND interval.id = $2`, candidate.TournamentID, candidate.ID,
	).Scan(&state))
	return state
}
