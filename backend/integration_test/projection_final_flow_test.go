//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	gamedb "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/game"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

type finalDraftClock struct{ at time.Time }

func (c finalDraftClock) Now() time.Time { return c.at }

func TestFinalDraftActivationReusesGenesis(t *testing.T) {
	ctx := context.Background()
	prepareActiveFinal(ctx, t)
}

func prepareActiveFinal(ctx context.Context, t *testing.T) (tournamentAdminSwissProofFixture, playoff.FinalStageIDs, *playoff.TerminalCoordinator) {
	t.Helper()
	fixture, ids, coordinator := prepareFinalDraft(ctx, t)
	var scoreCount, resultCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM series_score_revisions WHERE series_id = $1`, ids.FinalSeriesID).Scan(&scoreCount))
	require.Equal(t, 1, scoreCount)
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM official_result_heads WHERE series_id = $1`, ids.FinalSeriesID).Scan(&resultCount))
	require.Zero(t, resultCount)
	genesis, err := fixture.tx.Querier(ctx).LockPostseasonFinalGenesis(ctx, sqlc.LockPostseasonFinalGenesisParams{SeriesID: ids.FinalSeriesID, TournamentID: fixture.tournamentID, InitialScoreRevisionID: ids.InitialScoreRevisionID.UUID()})
	require.NoError(t, err)
	require.Equal(t, ids.InitialScoreRevisionID.UUID(), genesis)
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, err := fixture.tx.Querier(txCtx).CreateInitialSeriesScoreHead(txCtx, sqlc.CreateInitialSeriesScoreHeadParams{
			SeriesID: ids.FinalSeriesID, RosterID: fixture.rosterID, InitialScoreRevisionID: ids.InitialScoreRevisionID.UUID(),
			UpdatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		})
		return err
	})
	var duplicate *pgconn.PgError
	require.ErrorAs(t, err, &duplicate)
	require.Equal(t, "23505", duplicate.Code)
	for _, field := range []string{"tournament", "series", "genesis"} {
		params := sqlc.LockPostseasonFinalGenesisParams{SeriesID: ids.FinalSeriesID, TournamentID: fixture.tournamentID, InitialScoreRevisionID: ids.InitialScoreRevisionID.UUID()}
		switch field {
		case "tournament":
			params.TournamentID = uuid.New()
		case "series":
			params.SeriesID = uuid.New()
		case "genesis":
			params.InitialScoreRevisionID = uuid.New()
		}
		_, err := fixture.tx.Querier(ctx).LockPostseasonFinalGenesis(ctx, params)
		require.ErrorIs(t, err, pgx.ErrNoRows, field)
	}
	drafts := participantdraftrepo.NewParticipantDraftRepository(fixture.tx, draftrepo.NewDraftPostgres(fixture.tx))
	current, err := drafts.LoadDraft(ctx, ids.DraftID)
	require.NoError(t, err)
	for current.State == draftusecase.ExecutionStateActive {
		usecase := draftusecase.NewActionUseCase(drafts, finalDraftClock{at: current.TurnDeadline.Add(-time.Second)})
		next, err := usecase.Apply(ctx, draftusecase.PlayerActionCommand{
			DraftID: current.ID, CommandID: uuid.New(), ActorID: *current.CurrentActorID,
			ExpectedRevisionID: current.RevisionID, ExpectedRevision: current.Revision,
			ExpectedServiceEpoch: current.ServiceEpoch, ExpectedTurn: current.Turn, ResultRevisionID: uuid.New(), ActionID: uuid.New(),
			Action: *current.CurrentAction, Category: current.LegalCategories[0],
		})
		require.NoError(t, err)
		current = &next.Draft
	}
	var receipt playoff.TerminalReceipt
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		receipt, err = coordinator.ActivateFinalAfterDraft(txCtx, playoff.TerminalDraftCommand{TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID, DraftID: ids.DraftID, CommandID: current.CommandID})
		return err
	})
	require.NoError(t, err)
	require.True(t, receipt.Changed)
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM series_score_revisions WHERE series_id = $1`, ids.FinalSeriesID).Scan(&scoreCount))
	require.Equal(t, 1, scoreCount)
	return fixture, ids, coordinator
}

func TestFinalSettlementPublishesChampion(t *testing.T) {
	ctx := context.Background()
	fixture, ids, coordinator := prepareActiveFinal(ctx, t)
	for position := 1; position <= 2; position++ {
		input := activeFinalSettlementInput(ctx, t, fixture, ids, position)
		settleAndAdvance := func(txCtx context.Context) error {
			if _, changed, err := resultauthority.NewResultPostgres(fixture.tx).Settle(txCtx, input); err != nil {
				return err
			} else if !changed {
				return errors.New("expected fresh final settlement")
			}
			receipt, err := coordinator.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID})
			if err != nil {
				return err
			}
			if !receipt.Changed {
				return errors.New("expected final advancement")
			}
			return nil
		}
		before := playoffPublicationCounts(ctx, t, fixture.tournamentID)
		abort := errors.New("stop after final settlement and publication")
		err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
			if err := settleAndAdvance(txCtx); err != nil {
				return err
			}
			return abort
		})
		require.ErrorIs(t, err, abort, "rollback final settlement %d", position)
		require.Equal(t, before, playoffPublicationCounts(ctx, t, fixture.tournamentID))
		var commits int
		require.NoError(t, sharedPool.QueryRow(ctx, "SELECT count(*) FROM result_commits WHERE id = $1", input.IDs.CommitID).Scan(&commits))
		require.Zero(t, commits)
		require.NoError(t, fixture.tx.Do(ctx, settleAndAdvance), "commit final settlement %d", position)
	}
	var state string
	require.NoError(t, sharedPool.QueryRow(ctx, "SELECT state FROM tournaments WHERE id = $1", fixture.tournamentID).Scan(&state))
	require.Equal(t, "completed", state)
	var events int
	require.NoError(t, sharedPool.QueryRow(ctx, "SELECT count(*) FROM outbox_champion_sources WHERE tournament_id = $1", fixture.tournamentID).Scan(&events))
	require.Equal(t, 1, events)
}

func activeFinalSettlementInput(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, ids playoff.FinalStageIDs, position int) resultrepo.ResultSettlementInput {
	t.Helper()
	gameID, slotID := ids.FirstGameID, ids.FirstSlotID
	if position == 2 {
		gameID, slotID = ids.SecondGameID, ids.SecondSlotID
	}
	scope := gamedomain.Scope{TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID, SlotID: slotID, GameID: gameID}
	games := gamedb.NewGamePostgres(fixture.tx)
	current, err := games.GetAttemptRecord(ctx, scope)
	require.NoError(t, err)
	at := current.CreatedAt.UTC().Add(time.Second)
	next := current.Game
	next.State = domain.GameStateActive
	active, changed, err := games.Transition(ctx, scope, current.Revision, current.Game.State, next, at)
	require.NoError(t, err)
	require.True(t, changed)
	var winnerID uuid.UUID
	var seriesRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT first_participant_id, revision FROM series WHERE id = $1`, ids.FinalSeriesID).Scan(&winnerID, &seriesRevision))
	nextState := domain.SeriesStateActive
	if position == 2 {
		nextState = domain.SeriesStateCompleted
	}
	input := finalProjectionSettlementInput(goldenMigrationFixture{tournamentID: fixture.tournamentID, rosterID: fixture.rosterID}, ids.FinalSeriesID, gameID, winnerID, uuid.New(), domain.SeriesScore{FirstParticipantWins: position}, domain.SeriesStateActive, nextState, seriesRevision, sha256.Sum256([]byte("final settlement")), at.Add(time.Second))
	input.ExpectedAttemptRevision = active.Revision
	return input
}
