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

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestFinalSwissOperatorForfeitPublishesReceipt(t *testing.T) {
	testFinalSwissOperatorReceipt(t, false)
}

func TestFinalSwissOperatorNoShowPublishesReceipt(t *testing.T) {
	testFinalSwissOperatorReceipt(t, true)
}

func TestFinalSwissRecoveryNoShowPublishesReceipt(t *testing.T) {
	testFinalSwissOperatorReceipt(t, true, true)
}

func TestFinalSwissLiveForfeitPublishesReceipt(t *testing.T) {
	ctx := context.Background()
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
	command := tournamentadmin.ForfeitCommand{
		CommandScope: tournamentadmin.CommandScope{Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()}, TournamentID: fixture.tournamentID, CommandID: uuid.New()},
		SeriesID:     binding.SeriesID, ForfeitingParticipantID: participantID,
		Confirmed: true, Reason: "participant conceded during play", ExpectedAuthorityRevision: authority.AuthorityRevision,
		ExpectedGame: &tournamentadmin.GameExpectation{SlotID: slotID, GameID: attemptID, AttemptNo: 1, State: domain.GameStateActive},
		Basis:        "rule_violation", RuleID: "game.rule.7", EvidenceIDs: []uuid.UUID{uuid.New()},
		GameResultRevisionID: &gameRevisionID, ScoreRevisionID: uuid.New(), SeriesResultRevisionID: uuid.New(),
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
	}
	assertOperatorReceiptRollback(ctx, t, fixture, func(txCtx context.Context) error { return workflow.RecordForfeit(txCtx, command) })
	require.NoError(t, workflow.RecordForfeit(ctx, command))
	before := swissPublicationCounts(ctx, t, fixture)
	require.NoError(t, workflow.RecordForfeit(ctx, command))
	require.Equal(t, before, swissPublicationCounts(ctx, t, fixture))
	readFinalSwissReceipt(ctx, t, fixture)
	var awards, points int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*), sum(points) FROM swiss_point_ledger_entries
		WHERE source_series_id = $1 AND series_result_revision_id = $2`, binding.SeriesID, command.SeriesResultRevisionID).Scan(&awards, &points))
	require.Equal(t, 2, awards)
	require.Equal(t, 1, points)
	_, revision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	_, err = publishSwissPlayoffs(ctx, fixture, progression.Command{CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: revision, Action: progression.ActionStartPlayoffs})
	require.NoError(t, err)
}

func swissOperatorResultWorkflow(t *testing.T, fixture tournamentAdminSwissProofFixture) (*postgres.TournamentAdminResultPostgres, *tournamentadmin.OperatorResultWorkflow) {
	t.Helper()
	repository := postgres.NewTournamentAdminResultPostgres(fixture.tx, resultauthority.NewResultPostgres(fixture.tx))
	drafts := draftrepo.NewDraftPostgres(fixture.tx)
	assignments := assignmentrepo.NewAssignmentPostgres(fixture.tx)
	exactPlans := postgres.NewExactDraftBranchPlanPostgres(fixture.tx, drafts)
	planner := playoff.NewFinalDraftAssignmentService(assignmentusecase.NewExactDraftBranchPlanUseCase(exactPlans), exactPlans, exactPlans)
	terminal := playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository: playoffrepo.NewPlayoffTerminalPostgres(fixture.tx, drafts, assignments.CreateAssignmentTx),
		Publisher:  projectionrepo.NewProjectionPostgres(fixture.tx), DraftPlanner: planner, Rehydrator: planner,
	})
	workflow := tournamentadmin.NewOperatorResultWorkflow(tournamentadmin.OperatorResultWorkflowDependencies{
		Transactions: fixture.tx, Repository: &observedOperatorResultRepository{TournamentAdminResultPostgres: repository, t: t}, Postseason: terminal,
	})
	return repository, workflow
}

func testFinalSwissOperatorReceipt(t *testing.T, noShow bool, recoverDeadline ...bool) {
	t.Helper()
	ctx := context.Background()
	recoverWindow := len(recoverDeadline) > 0 && recoverDeadline[0]
	fixture := prepareFinalSwissBeforeStart(ctx, t, true, noShow, recoverWindow)
	repository, workflow := swissOperatorResultWorkflow(t, fixture)
	for index, binding := range fixture.binding {
		if recoverWindow && index == 0 {
			continue
		}
		authority, err := repository.LockOperatorResultAuthority(ctx, fixture.tournamentID, binding.SeriesID)
		require.NoError(t, err)
		if noShow && index == len(fixture.binding)-1 {
			var waveRevisionID, windowID, windowRevisionID uuid.UUID
			var deadline time.Time
			require.NoError(t, sharedPool.QueryRow(ctx, `SELECT wave.revision_id, ready_window.id, ready_window.revision_id, ready_window.deadline
				FROM waves AS wave JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id WHERE wave.id = $1`, fixture.waveID).
				Scan(&waveRevisionID, &windowID, &windowRevisionID, &deadline))
			if delay := time.Until(deadline); delay > 0 {
				time.Sleep(delay + time.Millisecond)
			}
			if recoverWindow {
				pending := recovery.PendingDeadline{Kind: recovery.DeadlineKindReadyWindow, ID: windowID, TournamentID: fixture.tournamentID,
					RosterID: fixture.rosterID, WaveID: fixture.waveID, ReadyWindowRevisionID: windowRevisionID, DueAt: deadline.UTC()}
				require.NoError(t, sharedPool.QueryRow(ctx, `SELECT revision FROM waves WHERE id = $1`, fixture.waveID).Scan(&pending.ExpectedRevision))
				clock := playoffPublicationClock{}
				store := postgres.NewRecoveryTerminalPostgres(fixture.tx, nil, clock)
				handler := recovery.NewTerminalDeadlineHandler(store, clock)
				assertOperatorReceiptRollback(ctx, t, fixture, func(txCtx context.Context) error { _, err := handler.HandleDeadline(txCtx, pending); return err })
				changed, err := handler.HandleDeadline(ctx, pending)
				require.NoError(t, err)
				require.True(t, changed)
				changed, err = handler.HandleDeadline(ctx, pending)
				require.NoError(t, err)
				require.False(t, changed)
				continue
			}
			command := tournamentadmin.NoShowCommand{
				CommandScope: tournamentadmin.CommandScope{Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()}, TournamentID: fixture.tournamentID, CommandID: uuid.New()},
				WaveID:       fixture.waveID, WindowID: windowID, SeriesID: binding.SeriesID,
				Confirmed: true, Reason: "participant absent at ready deadline", ExpectedAuthorityRevision: authority.AuthorityRevision,
				ExpectedWaveRevisionID: waveRevisionID, ExpectedWindowRevisionID: windowRevisionID, ExpectedSeriesState: domain.SeriesStateReady,
				GameResultRevisionIDs: []uuid.UUID{uuid.New()}, ScoreRevisionID: uuid.New(), SeriesResultRevisionID: uuid.New(),
			}
			loaded, loadErr := repository.LoadOperatorNoShowAuthority(ctx, command)
			require.NoError(t, loadErr)
			t.Logf("no-show authority Series state=%s Wave state=%s", loaded.Series.Series.State, loaded.Wave.State)
			assertOperatorReceiptRollback(ctx, t, fixture, func(txCtx context.Context) error { return workflow.ResolveNoShow(txCtx, command) })
			require.NoError(t, workflow.ResolveNoShow(ctx, command))
			require.NoError(t, workflow.ResolveNoShow(ctx, command))
			continue
		}
		var participantID, slotID, attemptID uuid.UUID
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT series.second_participant_id, attempt.slot_id, attempt.id
			FROM series JOIN game_attempts AS attempt ON attempt.series_id = series.id WHERE series.id = $1`, binding.SeriesID).Scan(&participantID, &slotID, &attemptID))
		command := tournamentadmin.ForfeitCommand{
			CommandScope: tournamentadmin.CommandScope{Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()}, TournamentID: fixture.tournamentID, CommandID: uuid.New()},
			SeriesID:     binding.SeriesID, ForfeitingParticipantID: participantID,
			Confirmed: true, Reason: "participant conceded before start", ExpectedAuthorityRevision: authority.AuthorityRevision,
			ExpectedGame: &tournamentadmin.GameExpectation{SlotID: slotID, GameID: attemptID, AttemptNo: 1, State: domain.GameStatePlanned},
			Basis:        "rule_violation", RuleID: "game.rule.7", EvidenceIDs: []uuid.UUID{uuid.New()},
			ScoreRevisionID: uuid.New(), SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
		}
		if !noShow && index == 0 {
			assertTerminalProofRequiresCommand(ctx, t, fixture, func(txCtx context.Context) error { return workflow.RecordForfeit(txCtx, command) })
		}
		if index == len(fixture.binding)-1 {
			assertOperatorReceiptRollback(ctx, t, fixture, func(txCtx context.Context) error { return workflow.RecordForfeit(txCtx, command) })
		}
		require.NoError(t, workflow.RecordForfeit(ctx, command))
		require.NoError(t, workflow.RecordForfeit(ctx, command))
		var count int
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM final_swiss_projection_receipts WHERE tournament_id = $1`, fixture.tournamentID).Scan(&count))
		require.Equal(t, index, count)
	}
	readFinalSwissReceipt(ctx, t, fixture)
	assertReceiptRejectsCrossSeriesTerminalCommit(ctx, t, fixture)
	_, revision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	_, err := publishSwissPlayoffs(ctx, fixture, progression.Command{CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: revision, Action: progression.ActionStartPlayoffs})
	require.NoError(t, err)
}

func assertTerminalProofRequiresCommand(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, mutate func(context.Context) error) {
	t.Helper()
	before := swissPublicationCounts(ctx, t, fixture)
	wrote := false
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, err := fixture.tx.Conn(txCtx).Exec(txCtx, `CREATE FUNCTION splice_terminal_proof_source() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN NEW.terminal_command_id := NEW.round_id; RETURN NEW; END; $$;
			CREATE TRIGGER splice_terminal_proof_source BEFORE INSERT ON swiss_round_lock_proofs
			FOR EACH ROW WHEN (NEW.proof_mode = 'pre_start_forfeit') EXECUTE FUNCTION splice_terminal_proof_source();`)
		if err != nil {
			return err
		}
		err = mutate(txCtx)
		wrote = err == nil
		return err
	})
	require.True(t, wrote, "terminal rows exist before deferred proof binding")
	var detail *pgconn.PgError
	require.ErrorAs(t, err, &detail)
	require.Equal(t, "23514", detail.Code)
	require.Equal(t, before, swissPublicationCounts(ctx, t, fixture))
	var proofs int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM swiss_round_lock_proofs WHERE tournament_id = $1 AND round_number = 3`, fixture.tournamentID).Scan(&proofs))
	require.Zero(t, proofs)
}

func assertReceiptRejectsCrossSeriesTerminalCommit(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) {
	t.Helper()
	authority := swissReceiptAuthority(ctx, t, fixture)
	input, err := postgres.NewTournamentProgressionPostgres(fixture.tx).LoadLockedSwissTerminalEvidence(ctx, progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, ActorID: uuid.New(),
		Action: progression.ActionStartPlayoffs, ExpectedProjectionRevision: authority.ProjectionRevision}, authority)
	require.NoError(t, err)
	previous, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	publication := successorSwissPublication(ctx, t, fixture)
	inserted := false
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		record, err := projectionrepo.NewProjectionPostgres(fixture.tx).Publish(txCtx, publication)
		if err != nil {
			return err
		}
		current := input
		current.RevisionID, current.RevisionNo = domain.DerivedRevisionID(record.Revision.ID), 2
		current.PhysicalProjectionRevision = int(record.Revision.RevisionNumber)
		current.Previous, current.CreatedAt = &previous, publication.CreatedAt
		planned, err := playoff.PlanFinalSwissReceipt(current)
		if err != nil {
			return err
		}
		err = copySwissReceiptContract(txCtx, fixture.tx.Querier(txCtx), fixture, authority.ProjectionRevisionID, record, planned, false,
			func(series []sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow, _ []sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow) {
				for _, source := range series {
					if source.SeriesID != series[0].SeriesID && (source.NormalNoShowCommitID.Valid || source.OperatorForfeitCommitID.Valid) {
						series[0].TerminalSource = source.TerminalSource
						series[0].NormalNoShowCommitID, series[0].OperatorForfeitCommitID = source.NormalNoShowCommitID, source.OperatorForfeitCommitID
						return
					}
				}
				t.Fatal("fixture has no terminal commit to splice")
			})
		inserted = err == nil
		return err
	})
	require.True(t, inserted, "malformed aggregate must fail at deferred commit")
	var detail *pgconn.PgError
	require.ErrorAs(t, err, &detail)
	require.Equal(t, "23514", detail.Code)
	current, _ := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	require.Equal(t, authority.ProjectionRevisionID, current)
}

func assertOperatorReceiptRollback(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, mutate func(context.Context) error) {
	t.Helper()
	before := swissPublicationCounts(ctx, t, fixture)
	sentinel := errors.New("late operator publication failure")
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		if err := mutate(txCtx); err != nil {
			return err
		}
		var count int
		require.NoError(t, fixture.tx.Conn(txCtx).QueryRow(txCtx, `SELECT count(*) FROM final_swiss_projection_receipts WHERE tournament_id = $1`, fixture.tournamentID).Scan(&count))
		require.Equal(t, 1, count)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, before, swissPublicationCounts(ctx, t, fixture))
}

func (r *observedOperatorResultRepository) CommitOperatorNoShow(ctx context.Context, command tournamentadmin.NoShowCommand, digest [32]byte, resolution gameusecase.NoShowResolution) (*gameusecase.NoShowResolution, bool, error) {
	stored, changed, err := r.TournamentAdminResultPostgres.CommitOperatorNoShow(ctx, command, digest, resolution)
	if err != nil {
		r.t.Logf("operator no-show persistence: %v", err)
	}
	return stored, changed, err
}

type observedOperatorResultRepository struct {
	*postgres.TournamentAdminResultPostgres
	t *testing.T
}

func (r *observedOperatorResultRepository) CommitOperatorForfeit(ctx context.Context, command tournamentadmin.ForfeitCommand, digest [32]byte, resolution gameusecase.ForfeitResolution) (*gameusecase.ForfeitResolution, bool, error) {
	stored, changed, err := r.TournamentAdminResultPostgres.CommitOperatorForfeit(ctx, command, digest, resolution)
	if err != nil {
		r.t.Logf("operator forfeit persistence: %v", err)
	}
	return stored, changed, err
}
