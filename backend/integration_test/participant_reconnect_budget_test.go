//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	recoveryterminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func participantReconnectCarriedBudget(t *testing.T, ctx context.Context) (participantReconnectStartedFixture, *gamereconnect.ReconnectRecord, gamereconnect.DisconnectCommand) {
	t.Helper()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	started := participantReconnectStartFixture(ctx, t, 0)
	firstID := started.playerID
	secondID := started.fixture.participants[1]
	deadline := started.started.GameClock.OriginalDeadline
	firstAt := deadline.Add(-20 * time.Second)
	firstCommand := participantReconnectDisconnectCommand(started, deadline)
	first, changed, err := gamereconnect.NewDisconnectUseCase(
		started.fixture.adapter, participantReconnectTestClock{at: firstAt},
	).Disconnect(ctx, firstCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.GameStatePaused, first.ReconnectAuthority.Game.State)

	resumedAt := firstAt.Add(5 * time.Second)
	_, changed, err = gamereconnect.ReconnectNewUseCase(
		started.fixture.adapter, participantReconnectTestClock{at: resumedAt},
	).Reconnect(ctx, gamereconnect.ReconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: firstID,
		IntervalID: firstCommand.IntervalID, Settlement: participantReconnectSettlementIDs(),
	})
	require.NoError(t, err)
	require.True(t, changed)

	secondAt := resumedAt.Add(2 * time.Second)
	secondCommand := gamereconnect.DisconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: secondID,
		IntervalID: uuid.New(), Deadline: secondAt.Add(30 * time.Second),
		Settlement: participantReconnectSettlementIDs(),
	}
	second, changed, err := gamereconnect.NewDisconnectUseCase(
		started.fixture.adapter, participantReconnectTestClock{at: secondAt},
	).Disconnect(ctx, secondCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.GameStatePaused, second.ReconnectAuthority.Game.State)
	require.NotEqual(t, first.ReconnectAuthority.PauseID, second.ReconnectAuthority.PauseID)

	loaded, err := started.fixture.adapter.LoadAuthority(ctx, started.scope, firstID)
	require.NoError(t, err)
	require.Equal(t, second.ReconnectAuthority.PauseID, loaded.PauseID)
	require.Equal(t, 1, reconnectUsed(t, loaded, firstID))
	require.Equal(t, 1, reconnectUsed(t, loaded, secondID))
	var carriedRows int
	err = sharedPool.QueryRow(ctx, `
		SELECT count(*) FROM reconnect_slot_counters
		WHERE pause_id = $1 AND participant_id = $2`,
		second.ReconnectAuthority.PauseID, firstID,
	).Scan(&carriedRows)
	require.NoError(t, err)
	require.Zero(t, carriedRows)
	return started, second, secondCommand
}

func TestParticipantReconnectRetainsBudgetAcrossGamePauses(t *testing.T) {
	ctx := context.Background()
	started, second, secondCommand := participantReconnectCarriedBudget(t, ctx)
	firstID, secondID := started.playerID, secondCommand.ParticipantID

	thirdAt := second.RecordedAt.Add(time.Second)
	third, changed, err := gamereconnect.NewDisconnectUseCase(
		started.fixture.adapter, participantReconnectTestClock{at: thirdAt},
	).Disconnect(ctx, gamereconnect.DisconnectCommand{
		Scope: started.scope, CommandID: uuid.New(), ParticipantID: firstID,
		IntervalID: uuid.New(), Deadline: thirdAt.Add(30 * time.Second),
		Settlement: participantReconnectSettlementIDs(),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 2, reconnectUsed(t, third.ReconnectAuthority, firstID))
	for participantID, wantUsed := range map[uuid.UUID]int{firstID: 2, secondID: 1} {
		var used, roots int
		err := sharedPool.QueryRow(ctx, `
			SELECT counter.slots_used, count(interval.id)
			FROM reconnect_slot_counters AS counter
			LEFT JOIN reconnect_intervals AS interval
				ON interval.pause_id = counter.pause_id
				AND interval.participant_id = counter.participant_id
				AND interval.continuation_number = 0
			WHERE counter.pause_id = $1 AND counter.participant_id = $2
			GROUP BY counter.slots_used`, second.ReconnectAuthority.PauseID, participantID,
		).Scan(&used, &roots)
		require.NoError(t, err)
		require.Equal(t, wantUsed, used)
		require.Equal(t, 1, roots)
	}
}

func TestParticipantReconnectCarriedBudgetTimeout(t *testing.T) {
	for _, recoverDeadline := range []bool{false, true} {
		name := "timeout"
		if recoverDeadline {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			started, second, command := participantReconnectCarriedBudget(t, ctx)
			if recoverDeadline {
				pending := recovery.PendingDeadline{
					Kind: recovery.DeadlineKindReconnect, ID: command.IntervalID,
					TournamentID: started.scope.TournamentID, RosterID: started.scope.RosterID,
					WaveID: started.scope.WaveID, SeriesID: second.ReconnectAuthority.Series.ID,
					SlotID: second.ReconnectAuthority.Game.SlotID,
					GameID: second.ReconnectAuthority.Game.ID, PauseID: second.ReconnectAuthority.PauseID,
					ParticipantID: command.ParticipantID, ExpectedRevision: 1, DueAt: command.Deadline,
				}
				store := recoveryterminalrepo.NewRecoveryTerminalPostgres(
					started.fixture.tx, authorityrepo.NewExecutionAuthorityPostgres(started.fixture.tx),
					participantReconnectTestClock{at: command.Deadline},
				)
				authority, found, err := store.LoadDeadlineAuthority(ctx, pending)
				require.NoError(t, err)
				require.True(t, found)
				require.NotNil(t, authority.ReconnectTimeout)
				require.ElementsMatch(t, second.ReconnectAuthority.Counters, authority.ReconnectTimeout.Counters)
				handler := technicalDeadlineHandler(started.fixture, pending)
				changed, err := handler.HandleDeadline(ctx, pending)
				require.NoError(t, err)
				require.True(t, changed)
				changed, err = handler.HandleDeadline(ctx, pending)
				require.NoError(t, err)
				require.False(t, changed)
				var state string
				var winner uuid.UUID
				require.NoError(t, sharedPool.QueryRow(ctx, `SELECT state, winner_id FROM game_attempts WHERE id = $1`,
					pending.GameID).Scan(&state, &winner))
				require.Equal(t, string(domain.GameStateCompleted), state)
				require.Equal(t, started.playerID, winner)
				return
			}
			timeout := gamereconnect.TimeoutCommand{
				Scope: started.scope, CommandID: uuid.New(), ParticipantID: command.ParticipantID,
				IntervalID: command.IntervalID, Settlement: participantReconnectSettlementIDs(),
			}
			usecase := gamereconnect.NewTimeoutUseCase(started.fixture.adapter, participantReconnectTestClock{at: command.Deadline})
			terminal, changed, err := usecase.Expire(ctx, timeout)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, domain.GameStateCompleted, terminal.ReconnectAuthority.Game.State)
			require.ElementsMatch(t, second.ReconnectAuthority.Counters, terminal.ReconnectAuthority.Counters)
			for _, participantID := range []uuid.UUID{started.playerID, command.ParticipantID} {
				loaded, err := started.fixture.adapter.LoadAuthority(ctx, started.scope, participantID)
				require.NoError(t, err)
				require.Equal(t, terminal.ReconnectAuthority, loaded)
			}
			replayed, changed, err := usecase.Expire(ctx, timeout)
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, terminal, replayed)
		})
	}
}

func reconnectUsed(t *testing.T, authority gamereconnect.ReconnectAuthority, participantID uuid.UUID) int {
	t.Helper()
	for _, counter := range authority.Counters {
		if counter.ParticipantID == participantID {
			return counter.Used
		}
	}
	t.Fatalf("missing reconnect counter for %s", participantID)
	return 0
}
