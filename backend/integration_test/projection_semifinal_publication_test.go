//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestSemifinalSettlementProjectsFromStage(t *testing.T) {
	ctx := context.Background()
	fixture, command := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	input := semifinalSettlementInput(ctx, t, fixture, command.CommandID, 1)
	var scoreRevisionID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT current_revision_id FROM series_score_heads WHERE series_id = $1`, input.Scope.SeriesID).Scan(&scoreRevisionID))
	params := sqlc.LockStageScoreGenesisNodesParams{ScoreRevisionID: scoreRevisionID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, SeriesID: input.Scope.SeriesID}
	genesis, err := fixture.tx.Querier(ctx).LockStageScoreGenesisNodes(ctx, params)
	require.NoError(t, err)
	require.Len(t, genesis, 1)
	require.NotEqual(t, scoreRevisionID, genesis[0].ID, "stage score and logical node have distinct identities")
	for _, field := range []string{"tournament", "roster", "Series", "revision"} {
		wrong := params
		switch field {
		case "tournament":
			wrong.TournamentID = uuid.New()
		case "roster":
			wrong.RosterID = uuid.New()
		case "Series":
			wrong.SeriesID = uuid.New()
		case "revision":
			wrong.ScoreRevisionID = uuid.New()
		}
		rows, err := fixture.tx.Querier(ctx).LockStageScoreGenesisNodes(ctx, wrong)
		require.NoError(t, err)
		require.Empty(t, rows, field)
	}
	before := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	abort := errors.New("stop after semifinal projection")
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, changed, err := resultauthority.NewResultPostgres(fixture.tx).Settle(txCtx, input)
		if err != nil {
			return err
		}
		require.True(t, changed)
		return abort
	})
	require.ErrorIs(t, err, abort)
	require.Equal(t, before, playoffPublicationCounts(ctx, t, fixture.tournamentID))
	record, changed, err := resultauthority.NewResultPostgres(fixture.tx).Settle(ctx, input)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, record.SeriesRevision)
	var nodeCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM result_projection_nodes WHERE entity_id IN ($1, $2)`, input.Scope.SeriesID, input.Scope.AttemptID).Scan(&nodeCount))
	require.Equal(t, 4, nodeCount, "genesis plus exact game, score and Series-result heads")
	var previousNodeID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT previous_node_id FROM result_projection_nodes WHERE id = $1`, record.ScoreRevision.ID).Scan(&previousNodeID))
	require.Equal(t, genesis[0].ID, previousNodeID)
}

func semifinalSettlementInput(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, commandID uuid.UUID, position int) postgres.ResultSettlementInput {
	t.Helper()
	var seriesID, winnerID uuid.UUID
	var seriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT semifinal.series_id,
		series.first_participant_id, series.revision
		FROM tournament_stage_playoff_semifinals AS semifinal
		INNER JOIN series ON series.id = semifinal.series_id
		WHERE semifinal.command_id = $1 AND semifinal.position = $2`, commandID, position).
		Scan(&seriesID, &winnerID, &seriesRevision))
	var attemptID uuid.UUID
	var attemptRevision int64
	var attemptState string
	err := sharedPool.QueryRow(ctx, `SELECT attempt.id, attempt.revision, attempt.state
		FROM series
		INNER JOIN game_slots AS slot ON slot.series_id = series.id AND slot.slot_number = 1
		INNER JOIN game_attempts AS attempt ON attempt.slot_id = slot.id AND attempt.attempt_number = 1
		WHERE series.id = $1`, seriesID).Scan(&attemptID, &attemptRevision, &attemptState)
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	if errors.Is(err, pgx.ErrNoRows) {
		slotID := createMigrationGameSlot(ctx, t, seriesID, fixture.rosterID, 1, "web")
		attemptID = createActiveMigrationAttempt(ctx, t, slotID, seriesID, fixture.rosterID, startedAt)
		return finalProjectionSettlementInput(
			goldenMigrationFixture{tournamentID: fixture.tournamentID, rosterID: fixture.rosterID},
			seriesID, attemptID, winnerID, uuid.New(), domain.SeriesScore{FirstParticipantWins: 1},
			domain.SeriesStateLocked, domain.SeriesStateCompleted, seriesRevision,
			sha256.Sum256([]byte("semifinal settlement")), startedAt.Add(time.Second),
		)
	}
	require.NoError(t, err)
	querier := fixture.tx.Querier(ctx)
	series, err := querier.StartWaveSeriesCAS(ctx, sqlc.StartWaveSeriesCASParams{
		StartedAt: pgtype.Timestamptz{Time: startedAt, Valid: true}, ID: seriesID,
		RosterID: fixture.rosterID, ExpectedRevision: seriesRevision,
	})
	require.NoError(t, err)
	attempt, err := querier.StartWaveGameCAS(ctx, sqlc.StartWaveGameCASParams{
		StartedAt: pgtype.Timestamptz{Time: startedAt, Valid: true}, ID: attemptID,
		SeriesID: seriesID, RosterID: fixture.rosterID,
		ExpectedRevision: attemptRevision, ExpectedState: attemptState,
	})
	require.NoError(t, err)
	input := finalProjectionSettlementInput(goldenMigrationFixture{tournamentID: fixture.tournamentID, rosterID: fixture.rosterID},
		seriesID, attemptID, winnerID, uuid.New(), domain.SeriesScore{FirstParticipantWins: 1},
		domain.SeriesStateActive, domain.SeriesStateCompleted, series.Revision,
		sha256.Sum256([]byte("semifinal settlement")), startedAt.Add(time.Second))
	input.ExpectedAttemptRevision = attempt.Revision
	return input
}

func TestSemifinalConcurrentSettlementPublishesOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, command := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	input := semifinalSettlementInput(ctx, t, fixture, command.CommandID, 1)
	before := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	var records [2]*postgres.ResultCommitRecord
	var changed [2]bool
	var failures [2]error
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	start := make(chan struct{})
	for index := range records {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			records[index], changed[index], failures[index] = resultauthority.NewResultPostgres(fixture.tx).Settle(ctx, input)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	require.NoError(t, failures[0])
	require.NoError(t, failures[1])
	require.NotEqual(t, changed[0], changed[1])
	require.Equal(t, records[0].Outbox.ID, records[1].Outbox.ID)
	after := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	require.Equal(t, before[0]+1, after[0])
	require.Equal(t, before[4]+1, after[4])
}

func TestSemifinalSettlementCreatesFinalDraft(t *testing.T) {
	ctx := context.Background()
	fixture, ids, _ := prepareFinalDraft(ctx, t)
	drafts := draftrepo.NewDraftPostgres(fixture.tx)
	stored, current, err := postgres.NewExactDraftBranchPlanPostgres(fixture.tx, drafts).LoadExactDraftBranchActivation(ctx, ids.DraftAssignmentPlanID)
	require.NoError(t, err)
	require.NoError(t, stored.Validate())
	require.Equal(t, ids.DraftID, current.ID)
}

func TestFinalDraftStartsAtLatestSemifinalCompletion(t *testing.T) {
	ctx := context.Background()
	fixture, ids, _ := prepareFinalDraftWithCompletionDelay(ctx, t, false, 30*time.Second)
	var stageTime, completedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT evidence.created_at, max(result.created_at)
		FROM tournament_stage_playoff_evidence AS evidence
		JOIN tournament_stage_playoff_semifinals AS semifinal ON semifinal.command_id = evidence.command_id
		JOIN official_result_heads AS head ON head.entity_kind = 'series' AND head.entity_id = semifinal.series_id
		JOIN official_result_revisions AS result ON result.id = head.current_revision_id
		WHERE evidence.tournament_id = $1 GROUP BY evidence.created_at`, fixture.tournamentID).Scan(&stageTime, &completedAt))
	require.True(t, completedAt.After(stageTime.Add(15*time.Second)))
	drafts := draftrepo.NewDraftPostgres(fixture.tx)
	_, current, err := postgres.NewExactDraftBranchPlanPostgres(fixture.tx, drafts).LoadExactDraftBranchActivation(ctx, ids.DraftAssignmentPlanID)
	require.NoError(t, err)
	require.True(t, current.TurnDeadline.After(completedAt), "draft deadline %s must follow exact semifinal completion %s", current.TurnDeadline, completedAt)
	require.True(t, completedAt.Add(15*time.Second).Equal(current.TurnDeadline))
}

func prepareFinalDraft(ctx context.Context, t *testing.T) (tournamentAdminSwissProofFixture, playoff.FinalStageIDs, *playoff.TerminalCoordinator) {
	t.Helper()
	return prepareFinalDraftWithRollback(ctx, t, false)
}

func TestFinalDraftCreationRollsBack(t *testing.T) {
	prepareFinalDraftWithRollback(context.Background(), t, true)
}

func prepareFinalDraftWithRollback(ctx context.Context, t *testing.T, checkRollback bool) (tournamentAdminSwissProofFixture, playoff.FinalStageIDs, *playoff.TerminalCoordinator) {
	t.Helper()
	return prepareFinalDraftWithCompletionDelay(ctx, t, checkRollback, 0)
}

func prepareFinalDraftWithCompletionDelay(ctx context.Context, t *testing.T, checkRollback bool, delay time.Duration) (tournamentAdminSwissProofFixture, playoff.FinalStageIDs, *playoff.TerminalCoordinator) {
	t.Helper()
	fixture, command := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	var last postgres.ResultSettlementInput
	var reservedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT locked_at FROM rosters WHERE id = $1`, fixture.rosterID).Scan(&reservedAt))
	_, err = fixture.tx.Querier(ctx).ReserveCheckedInTournamentParticipants(ctx, sqlc.ReserveCheckedInTournamentParticipantsParams{
		RosterID: fixture.rosterID, AcquiredAt: pgtype.Timestamptz{Time: reservedAt, Valid: true},
	})
	require.NoError(t, err)
	for position := 1; position <= 2; position++ {
		last = semifinalSettlementInput(ctx, t, fixture, command.CommandID, position)
		last.SettledAt = last.SettledAt.Add(delay)
		_, _, err := resultauthority.NewResultPostgres(fixture.tx).Settle(ctx, last)
		require.NoError(t, err)
	}
	drafts := draftrepo.NewDraftPostgres(fixture.tx)
	assignments := assignmentrepo.NewAssignmentPostgres(fixture.tx)
	exactPlans := postgres.NewExactDraftBranchPlanPostgres(fixture.tx, drafts)
	planner := playoff.NewFinalDraftAssignmentService(assignmentusecase.NewExactDraftBranchPlanUseCase(&observedExactDraftRepository{ExactDraftBranchPlanPostgres: exactPlans, t: t}), exactPlans, exactPlans)
	coordinator := playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository: &observedTerminalRepository{PlayoffTerminalPostgres: playoffrepo.NewPlayoffTerminalPostgres(fixture.tx, drafts, assignments.CreateAssignmentTx)}, Publisher: projectionrepo.NewProjectionPostgres(fixture.tx),
		DraftPlanner: &checkedFinalDraftPlanner{FinalDraftAssignmentService: planner, fixture: fixture, t: t}, Rehydrator: planner,
	})
	ids, err := playoff.FinalStageIdentity(command.CommandID)
	require.NoError(t, err)
	if checkRollback {
		abort := errors.New("stop after final draft branches and genesis")
		err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
			if _, err := coordinator.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{TournamentID: fixture.tournamentID, SeriesID: last.Scope.SeriesID}); err != nil {
				return err
			}
			return abort
		})
		require.ErrorIs(t, err, abort)
		var series, drafts, scores, heads, branches int
		require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM series WHERE id = $1),
			(SELECT count(*) FROM drafts WHERE id = $2),
			(SELECT count(*) FROM series_score_revisions WHERE series_id = $1),
			(SELECT count(*) FROM series_score_heads WHERE series_id = $1),
			(SELECT count(*) FROM assignment_branches WHERE plan_id = $3)`, ids.FinalSeriesID, ids.DraftID, ids.DraftAssignmentPlanID).Scan(&series, &drafts, &scores, &heads, &branches))
		require.Equal(t, [5]int{}, [5]int{series, drafts, scores, heads, branches})
	}
	var receipt playoff.TerminalReceipt
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		receipt, err = coordinator.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{TournamentID: fixture.tournamentID, SeriesID: last.Scope.SeriesID})
		return err
	})
	if err != nil {
		var detail *pgconn.PgError
		if errors.As(err, &detail) {
			t.Logf("terminal SQL diagnostic: code=%s position=%d internal_position=%d where=%s internal_query=%s", detail.Code, detail.Position, detail.InternalPosition, detail.Where, detail.InternalQuery)
		}
	}
	require.NoError(t, err)
	require.True(t, receipt.Changed)
	require.NotEqual(t, uuid.Nil, receipt.FinalSeriesID)
	return fixture, ids, coordinator
}

type checkedFinalDraftPlanner struct {
	*playoff.FinalDraftAssignmentService
	fixture tournamentAdminSwissProofFixture
	t       *testing.T
}

type observedExactDraftRepository struct {
	*postgres.ExactDraftBranchPlanPostgres
	t *testing.T
}

type observedTerminalRepository struct {
	*playoffrepo.PlayoffTerminalPostgres
}

func (r *observedTerminalRepository) PersistFinalContinuation(ctx context.Context, plan playoff.FinalContinuationPlan) (bool, error) {
	changed, err := r.PlayoffTerminalPostgres.PersistFinalContinuation(ctx, plan)
	if err != nil {
		return false, fmt.Errorf("persist final continuation: %w", err)
	}
	return changed, nil
}

func (r *observedExactDraftRepository) CommitExactDraftBranchPlan(ctx context.Context, plan assignmentusecase.ExactDraftBranchPlan) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	result, changed, err := r.ExactDraftBranchPlanPostgres.CommitExactDraftBranchPlan(ctx, plan)
	if err != nil {
		var detail *pgconn.PgError
		if errors.As(err, &detail) {
			r.t.Logf("draft plan commit: code=%s constraint=%s message=%s where=%s", detail.Code, detail.ConstraintName, detail.Message, detail.Where)
		}
	}
	return result, changed, err
}

func (p *checkedFinalDraftPlanner) PlanFinalDraft(ctx context.Context, plan playoff.FinalDraftPlan) (bool, error) {
	q := p.fixture.tx.Querier(ctx)
	poolParams := sqlc.LockPostseasonFinalNormalPoolParams{TournamentID: plan.Series.TournamentID, ContentRevision: plan.Category.SourceContentRevision, CategoryPoolID: plan.Category.CategoryPool.ID}
	pool, err := q.LockPostseasonFinalNormalPool(ctx, poolParams)
	if err != nil {
		return false, err
	}
	if pool != p.fixture.normalPoolRevisionID {
		return false, errors.New("final draft normal pool mismatch")
	}
	for _, field := range []string{"tournament", "revision", "category pool"} {
		wrong := poolParams
		switch field {
		case "tournament":
			wrong.TournamentID = uuid.New()
		case "revision":
			wrong.ContentRevision++
		case "category pool":
			wrong.CategoryPoolID = pool
		}
		_, err := q.LockPostseasonFinalNormalPool(ctx, wrong)
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("wrong %s lookup: %v", field, err)
		}
	}
	stage, err := q.LockExactDraftPlanningStage(ctx, plan.Draft.ID)
	if err != nil {
		return false, err
	}
	if pool != stage.SourcePoolRevisionID {
		return false, errors.New("final draft persisted wrong pool")
	}
	original, err := q.GetProjectionRevisionScoped(ctx, sqlc.GetProjectionRevisionScopedParams{ID: stage.PublishedProjectionRevisionID, TournamentID: stage.TournamentID, RosterID: stage.RosterID})
	if err != nil {
		return false, err
	}
	if original.State != "superseded" {
		return false, errors.New("final draft source was not superseded")
	}
	participants, err := q.LockExactDraftPlanningParticipants(ctx, plan.Draft.ID)
	if err != nil {
		return false, err
	}
	if len(participants) != 2 {
		return false, fmt.Errorf("final draft participant count: %d", len(participants))
	}
	if err := q.LockExactDraftPlanningReservationKeys(ctx, plan.Draft.ID); err != nil {
		return false, err
	}
	candidates, err := q.LockExactDraftPlanningCandidates(ctx, sqlc.LockExactDraftPlanningCandidatesParams{DraftID: plan.Draft.ID, PlanID: plan.IDs.DraftAssignmentPlanID})
	if err != nil {
		return false, err
	}
	if len(candidates) < 3 {
		return false, fmt.Errorf("final draft candidate count: %d", len(candidates))
	}
	return p.FinalDraftAssignmentService.PlanFinalDraft(ctx, plan)
}
