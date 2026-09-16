//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestFinalSwissReceiptRejectsLateChildren(t *testing.T) {
	ctx := context.Background()
	fixture, command := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	extra := semifinalSettlementInput(ctx, t, fixture, command.CommandID, 1)
	_, changed, err := resultauthority.NewResultPostgres(fixture.tx).Settle(ctx, extra)
	require.NoError(t, err)
	require.True(t, changed)
	var receiptID, roundID, otherNode uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT projection_revision_id, round_id, series_result_node_id
		FROM final_swiss_projection_receipt_series WHERE tournament_id = $1 ORDER BY series_id LIMIT 1`, fixture.tournamentID).
		Scan(&receiptID, &roundID, &otherNode))
	foreignLedger := foreignSwissByeLedger(ctx, t, fixture)
	before := swissPublicationCounts(ctx, t, fixture)
	for _, field := range []string{"series", "game", "result node", "score node", "ledger"} {
		t.Run(field, func(t *testing.T) {
			inserted := false
			err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
				q := fixture.tx.Querier(txCtx)
				at := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
				if field == "ledger" {
					_, err := q.CreateFinalSwissProjectionReceiptLedgerEntry(txCtx, sqlc.CreateFinalSwissProjectionReceiptLedgerEntryParams{
						ProjectionRevisionID: receiptID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, LedgerEntryID: foreignLedger, CreatedAt: at})
					if err != nil {
						return err
					}
				} else {
					row := sqlc.CreateFinalSwissProjectionReceiptSeriesParams{ProjectionRevisionID: receiptID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
						RoundID: roundID, SeriesID: extra.Scope.SeriesID, TerminalSource: "played", SeriesResultRevisionID: extra.IDs.SeriesResultRevisionID,
						ScoreRevisionID: extra.IDs.SeriesScoreRevisionID, SeriesResultNodeID: extra.IDs.SeriesResultRevisionID, ScoreNodeID: extra.IDs.SeriesScoreRevisionID, CreatedAt: at}
					if field == "result node" {
						row.SeriesResultNodeID = otherNode
					}
					if field == "score node" {
						row.ScoreNodeID = otherNode
					}
					if _, err := q.CreateFinalSwissProjectionReceiptSeries(txCtx, row); err != nil {
						return err
					}
					if field == "game" {
						_, err := q.CreateFinalSwissProjectionReceiptGame(txCtx, sqlc.CreateFinalSwissProjectionReceiptGameParams{
							ProjectionRevisionID: receiptID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, SeriesID: extra.Scope.SeriesID,
							GameAttemptID: extra.Scope.AttemptID, GameResultRevisionID: extra.IDs.GameResultRevisionID, GameResultNodeID: extra.IDs.GameResultRevisionID, CreatedAt: at})
						if err != nil {
							return err
						}
					}
				}
				inserted = true
				return nil // The transaction's COMMIT must reject the late child.
			})
			require.True(t, inserted, "child INSERT must reach deferred validation")
			var detail *pgconn.PgError
			require.ErrorAs(t, err, &detail)
			require.Equal(t, "23514", detail.Code)
			require.Equal(t, before, swissPublicationCounts(ctx, t, fixture))
		})
	}
}

func foreignSwissByeLedger(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) uuid.UUID {
	t.Helper()
	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	participants := createSwissMigrationParticipants(ctx, t, rosterID, createMigrationPlayers(ctx, t, 4))
	at := time.Now().UTC().Truncate(time.Microsecond)
	sourceID, revision := createRoundProofProjection(ctx, t, tournamentID, rosterID, participants[0], at)
	waveID, byeID, ledgerID := uuid.New(), uuid.New(), uuid.New()
	_, err := waverepo.NewWavePostgres(fixture.tx).Create(ctx, waverepo.WaveCreateInput{ID: waveID, TournamentID: tournamentID, RosterID: rosterID,
		RevisionID: domain.WaveRevisionID(uuid.New()), CommandID: uuid.New(), SourceProjectionRevisionID: sourceID, SourceProjectionRevision: revision,
		ParticipantIDs: participants[:2], CreatedAt: at, Series: []waverepo.WaveSeriesInput{{ID: uuid.New(), FirstParticipantID: participants[0], SecondParticipantID: participants[1],
			Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New())}}})
	require.NoError(t, err)
	roundID := createRoundProofSwissRound(ctx, t, tournamentID, rosterID, at)
	_, err = sharedPool.Exec(ctx, `INSERT INTO swiss_wave_links (wave_id, tournament_id, roster_id, round_id, bye_participant_id, bye_revision_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, waveID, tournamentID, rosterID, roundID, participants[2], byeID, at)
	require.NoError(t, err)
	var seed int32
	require.NoError(t, sharedPool.QueryRow(ctx, "SELECT seed FROM participants WHERE id = $1", participants[2]).Scan(&seed))
	_, err = fixture.tx.Querier(ctx).CreateSwissPointLedgerEntry(ctx, sqlc.CreateSwissPointLedgerEntryParams{ID: ledgerID, TournamentID: tournamentID, RosterID: rosterID,
		RoundID: roundID, RoundNumber: 1, SourceKind: "bye", ByeRevisionID: uuid.NullUUID{UUID: byeID, Valid: true}, ParticipantID: participants[2], Points: 1,
		StableSeed: seed, CreatedAt: pgtype.Timestamptz{Time: at, Valid: true}})
	require.NoError(t, err)
	return ledgerID
}
