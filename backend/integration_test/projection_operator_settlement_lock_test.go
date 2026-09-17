//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	participantauthorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamesettlement "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	tournamentadminresult "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
)

func TestParticipantOutsiderDoesNotWaitForTournamentLock(t *testing.T) {
	ctx := context.Background()
	fixture := prepareFinalSwissSettlement(ctx, t)
	holder, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer holder.Rollback(ctx)
	_, err = holder.Exec(ctx, "SELECT id FROM tournaments WHERE id = $1 FOR UPDATE", fixture.tournamentID)
	require.NoError(t, err)

	lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = participantauthorityrepo.NewTournamentParticipantPostgres(fixture.tx).ResolveSubmission(lookupCtx, usecase.SubmissionCommand{
		Actor: usecase.Identity{PlayerID: uuid.New()}, TournamentID: fixture.tournamentID,
	})
	require.ErrorIs(t, err, domain.ErrAssignmentParticipant)
}

func TestControlWaveAuthorityLocksTournamentBeforeProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := prepareFinalSwissSettlement(ctx, t)
	holder, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer holder.Rollback(context.Background())
	_, err = holder.Exec(ctx, "SELECT id FROM tournaments WHERE id = $1 FOR UPDATE", fixture.tournamentID)
	require.NoError(t, err)

	pidReady, done := make(chan int32, 1), make(chan error, 1)
	go func() {
		done <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var pid int32
			if err := fixture.tx.Conn(txCtx).QueryRow(txCtx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				return err
			}
			pidReady <- pid
			_, err := executionrepo.NewRepository(fixture.tx, resultauthority.FinalizeProjection).LockWaveAuthority(txCtx, fixture.tournamentID, fixture.waveID)
			return err
		})
	}()
	var pid int32
	select {
	case pid = <-pidReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiting := false
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		if err := sharedPool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", pid).Scan(&waiting); err != nil || waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	probe, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	_, projectionLockErr := probe.Exec(ctx, `SELECT id FROM projection_revisions
		WHERE tournament_id = $1 AND roster_id = $2 AND state = 'published' FOR UPDATE NOWAIT`, fixture.tournamentID, fixture.rosterID)
	require.NoError(t, probe.Rollback(ctx))
	require.NoError(t, holder.Rollback(ctx))
	select {
	case err = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.True(t, waiting, "ControlWave authority must wait at the held Tournament")
	require.NoError(t, err)
	require.NoError(t, projectionLockErr, "the earliest ControlWave boundary cannot own Projection before Tournament")
}

func TestLiveForfeitAndParticipantSettlementUseSameLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := prepareFinalSwissSettlement(ctx, t)
	repository, workflow := swissOperatorResultWorkflow(t, fixture)
	binding := fixture.binding[1]
	authority, err := repository.LockOperatorResultAuthority(ctx, fixture.tournamentID, binding.SeriesID)
	require.NoError(t, err)
	var participantID, slotID, attemptID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT series.second_participant_id, attempt.slot_id, attempt.id
		FROM series JOIN game_attempts AS attempt ON attempt.series_id = series.id WHERE series.id = $1`, binding.SeriesID).
		Scan(&participantID, &slotID, &attemptID))
	gameRevisionID := uuid.New()
	command := tournamentadminresult.ForfeitCommand{
		CommandScope: adminoperation.CommandScope{Operator: adminoperation.OperatorIdentity{ActorID: uuid.New()}, TournamentID: fixture.tournamentID, CommandID: uuid.New()},
		SeriesID:     binding.SeriesID, ForfeitingParticipantID: participantID,
		Confirmed: true, Reason: "participant conceded during play", ExpectedAuthorityRevision: authority.AuthorityRevision,
		ExpectedGame: &tournamentadminresult.GameExpectation{SlotID: slotID, GameID: attemptID, AttemptNo: 1, State: domain.GameStateActive},
		Basis:        "rule_violation", RuleID: "game.rule.7", EvidenceIDs: []uuid.UUID{uuid.New()},
		GameResultRevisionID: &gameRevisionID, ScoreRevisionID: uuid.New(), SeriesResultRevisionID: uuid.New(),
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
	}
	participant, scope := submitSwissReceiptSeries(ctx, t, fixture, 1)
	headerHeld, resumeParticipant := make(chan error, 1), make(chan struct{})
	participantDone, operatorDone := make(chan error, 1), make(chan error, 1)
	operatorPID := make(chan int32, 1)
	go func() {
		participantDone <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			// Pause at the participant command's Tournament/Roster prefix,
			// before its projection lookup and real settlement.
			_, err := fixture.tx.Conn(txCtx).Exec(txCtx, "SELECT id FROM tournaments WHERE id = $1 FOR UPDATE", fixture.tournamentID)
			if err == nil {
				_, err = fixture.tx.Conn(txCtx).Exec(txCtx, "SELECT id FROM rosters WHERE id = $1 FOR UPDATE", fixture.rosterID)
			}
			headerHeld <- err
			if err != nil {
				return err
			}
			select {
			case <-resumeParticipant:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, _, err = gamesettlement.SettlementNewUseCase(participant).Settle(txCtx, gamesettlement.SettlementCommand{Scope: scope, CommandID: uuid.New()})
			return err
		})
	}()
	select {
	case err = <-headerHeld:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		operatorDone <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var pid int32
			if err := fixture.tx.Conn(txCtx).QueryRow(txCtx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				return err
			}
			operatorPID <- pid
			return workflow.RecordForfeit(txCtx, command)
		})
	}()
	var pid int32
	select {
	case pid = <-operatorPID:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiting := false
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		if err := sharedPool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", pid).Scan(&waiting); err != nil || waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(resumeParticipant)
	var participantErr, operatorErr error
	select {
	case participantErr = <-participantDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case operatorErr = <-operatorDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.True(t, waiting, "RecordForfeit must contend with participant authority")
	for _, outcome := range []error{participantErr, operatorErr} {
		var detail *pgconn.PgError
		if errors.As(outcome, &detail) {
			require.NotEqual(t, "40P01", detail.Code, "Tournament/Roster vs projection lock inversion")
		}
	}
	require.NoError(t, participantErr)
	require.ErrorIs(t, operatorErr, domain.ErrConflict)
	var commits, operatorCommands int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM result_commits WHERE series_id = $1),
		(SELECT count(*) FROM operator_result_commands WHERE command_id = $2)`, binding.SeriesID, command.CommandID).Scan(&commits, &operatorCommands))
	require.Equal(t, 1, commits)
	require.Zero(t, operatorCommands)
	readFinalSwissReceipt(ctx, t, fixture)
}
