//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// This is a persistence-contract fixture, not a correction workflow. The first
// receipt is produced by real settlement; its successor uses canonical queries
// and the production planner/strict reader against retained evidence.
func TestFinalSwissReceiptSuccessorReadbackAndRollback(t *testing.T) {
	ctx := context.Background()
	fixture := prepareFinalSwissSettlement(ctx, t)
	settleSwissReceiptSeries(ctx, t, fixture, 1)
	closeSwissReceiptWave(ctx, t, fixture)
	authority := swissReceiptAuthority(ctx, t, fixture)
	input, err := postgres.NewTournamentProgressionPostgres(fixture.tx).LoadLockedSwissTerminalEvidence(ctx, progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, ActorID: uuid.New(),
		Action: progression.ActionStartPlayoffs, ExpectedProjectionRevision: authority.ProjectionRevision,
	}, authority)
	require.NoError(t, err)
	previous, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	publication := successorSwissPublication(ctx, t, fixture)
	before := swissPublicationCounts(ctx, t, fixture)
	for _, failLate := range []bool{true, false} {
		err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
			record, err := postgres.NewProjectionPostgres(fixture.tx).Publish(txCtx, publication)
			if err != nil {
				return err
			}
			current := input
			current.RevisionID = domain.DerivedRevisionID(record.Revision.ID)
			current.RevisionNo = previous.Projection().Revision().RevisionNo() + 1
			current.PhysicalProjectionRevision = int(record.Revision.RevisionNumber)
			current.Previous = &previous
			current.CreatedAt = publication.CreatedAt
			planned, err := playoff.PlanFinalSwissReceipt(current)
			if err != nil {
				return err
			}
			return copySwissReceiptContract(txCtx, fixture.tx.Querier(txCtx), fixture, authority.ProjectionRevisionID, record, planned, failLate)
		})
		if failLate {
			var detail *pgconn.PgError
			require.ErrorAs(t, err, &detail)
			require.Equal(t, "23505", detail.Code)
			require.Equal(t, before, swissPublicationCounts(ctx, t, fixture))
			id, revision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
			require.Equal(t, authority.ProjectionRevisionID, id)
			require.Equal(t, authority.ProjectionRevision, revision)
		} else {
			require.NoError(t, err)
		}
	}
	retained := readFinalSwissReceipt(ctx, t, fixture)
	require.Equal(t, 2, retained.Projection().Revision().RevisionNo())
	require.Equal(t, previous.GoldenSource().ProjectionID, retained.GoldenSource().ProjectionID)
	var predecessor uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT previous_receipt_projection_revision_id FROM final_swiss_projection_receipts WHERE projection_revision_id = $1`, publication.IDs.RevisionID).Scan(&predecessor))
	require.Equal(t, authority.ProjectionRevisionID, predecessor)
}

func successorSwissPublication(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) postgres.ProjectionPublishInput {
	t.Helper()
	scope := postgres.ProjectionScope{TournamentID: fixture.tournamentID, RosterID: fixture.rosterID}
	current, err := postgres.NewProjectionPostgres(fixture.tx).Current(ctx, scope)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	input := postgres.ProjectionPublishInput{IDs: postgres.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()}, Scope: scope,
		Source:             postgres.ProjectionSource{Kind: "operator_rebuild", Reason: "receipt lineage contract"},
		SupersessionReason: "receipt lineage contract", CutoffAt: now, CreatedAt: now, PublishedAt: now}
	for _, old := range current.Artifacts {
		artifact := postgres.ProjectionArtifactInput{ID: uuid.New(), Kind: domain.ArtifactKind(old.Artifact.ArtifactKind), Key: old.Artifact.ArtifactKey,
			Payload: old.Artifact.Payload, PayloadDigest: sha256.Sum256(old.Artifact.Payload),
			Dependencies: []postgres.ProjectionDependencyInput{{ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &old.Artifact.ID}}}
		for _, member := range old.Members {
			item := postgres.ProjectionMemberInput{ParticipantID: member.ParticipantID, Position: member.Position}
			if member.Score.Valid {
				value, err := member.Score.Float64Value()
				require.NoError(t, err)
				milli := int64(value.Float64 * 1000)
				item.ScoreMilli = &milli
			}
			artifact.Members = append(artifact.Members, item)
		}
		input.Artifacts = append(input.Artifacts, artifact)
	}
	return input
}

func copySwissReceiptContract(ctx context.Context, q *sqlc.Queries, fixture tournamentAdminSwissProofFixture, sourceID uuid.UUID, record *postgres.ProjectionRecord, planned playoff.FinalSwissProjection, failLate bool, mutate ...func([]sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow, []sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow)) error {
	root := sqlc.CreateFinalSwissProjectionReceiptParams{ProjectionRevisionID: record.Revision.ID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ReceiptRevision: 2, CanonicalProjectionID: planned.GoldenSource().ProjectionID, PreviousReceiptProjectionRevisionID: uuid.NullUUID{UUID: sourceID, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: planned.Projection().Revision().CreatedAt(), Valid: true}}
	digest := planned.Projection().Revision().PayloadDigest()
	root.CanonicalPayloadDigest = digest[:]
	for _, item := range record.Artifacts {
		if item.Artifact.ArtifactKind == "standings" {
			root.SourceStandingsArtifactID = item.Artifact.ID
			root.SourceStandingsPayloadDigest = item.Artifact.PayloadDigest
		}
	}
	participants, err := q.LockTournamentProgressionFinalSwissReceiptParticipants(ctx, sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsParams{ProjectionRevisionID: sourceID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID})
	if err != nil {
		return err
	}
	for _, row := range participants {
		if _, err := q.CreateFinalSwissProjectionReceiptParticipant(ctx, sqlc.CreateFinalSwissProjectionReceiptParticipantParams{ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID, ParticipantID: row.ParticipantID, StableSeed: row.StableSeed, CreatedAt: root.CreatedAt}); err != nil {
			return err
		}
	}
	rounds, err := q.LockTournamentProgressionFinalSwissReceiptRounds(ctx, sqlc.LockTournamentProgressionFinalSwissReceiptRoundsParams{ProjectionRevisionID: sourceID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID})
	if err != nil {
		return err
	}
	for _, row := range rounds {
		if _, err := q.CreateFinalSwissProjectionReceiptRound(ctx, sqlc.CreateFinalSwissProjectionReceiptRoundParams{ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID, RoundID: row.RoundID, RoundNumber: row.RoundNumber, CreatedAt: root.CreatedAt}); err != nil {
			return err
		}
	}
	series, err := q.LockTournamentProgressionFinalSwissReceiptSeriesEvidence(ctx, sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceParams{ProjectionRevisionID: sourceID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID})
	if err != nil {
		return err
	}
	games, err := q.LockTournamentProgressionFinalSwissReceiptGames(ctx, sqlc.LockTournamentProgressionFinalSwissReceiptGamesParams{ProjectionRevisionID: sourceID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID})
	if err != nil {
		return err
	}
	for _, change := range mutate {
		change(series, games)
	}
	for _, row := range series {
		if _, err := q.CreateFinalSwissProjectionReceiptSeries(ctx, sqlc.CreateFinalSwissProjectionReceiptSeriesParams{ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
			RoundID: row.RoundID, SeriesID: row.SeriesID, TerminalSource: row.TerminalSource, SeriesResultRevisionID: row.SeriesResultRevisionID, ScoreRevisionID: row.ScoreRevisionID,
			SeriesResultNodeID: row.SeriesResultNodeID, ScoreNodeID: row.ScoreNodeID, NormalNoShowCommitID: row.NormalNoShowCommitID, OperatorForfeitCommitID: row.OperatorForfeitCommitID, CreatedAt: root.CreatedAt}); err != nil {
			return err
		}
	}
	for _, row := range games {
		if _, err := q.CreateFinalSwissProjectionReceiptGame(ctx, sqlc.CreateFinalSwissProjectionReceiptGameParams{ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID, SeriesID: row.SeriesID,
			GameAttemptID: row.GameAttemptID, GameResultRevisionID: row.GameResultRevisionID, GameResultNodeID: row.GameResultNodeID, CreatedAt: root.CreatedAt}); err != nil {
			return err
		}
	}
	ledger, err := q.LockTournamentProgressionFinalSwissReceiptLedgerEntries(ctx, sqlc.LockTournamentProgressionFinalSwissReceiptLedgerEntriesParams{ProjectionRevisionID: sourceID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID})
	if err != nil {
		return err
	}
	for _, row := range ledger {
		params := sqlc.CreateFinalSwissProjectionReceiptLedgerEntryParams{ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID, LedgerEntryID: row.LedgerEntryID, CreatedAt: root.CreatedAt}
		if _, err := q.CreateFinalSwissProjectionReceiptLedgerEntry(ctx, params); err != nil {
			return err
		}
		if failLate {
			_, err := q.CreateFinalSwissProjectionReceiptLedgerEntry(ctx, params)
			return err
		}
	}
	// All children can precede their root within the same deferred transaction.
	_, err = q.CreateFinalSwissProjectionReceipt(ctx, root)
	return err
}

func TestFinalSwissReceiptRejectsCrossSeriesNodes(t *testing.T) {
	ctx := context.Background()
	fixture := prepareFinalSwissSettlement(ctx, t)
	settleSwissReceiptSeries(ctx, t, fixture, 1)
	foreignTournament := createMigrationTournament(ctx, t)
	foreignRoster := createMigrationRoster(ctx, t, foreignTournament)
	foreignParticipants := createSwissMigrationParticipants(ctx, t, foreignRoster, createMigrationPlayers(ctx, t, 4))
	at := time.Now().UTC().Truncate(time.Microsecond)
	foreignSource, foreignRevision := createRoundProofProjection(ctx, t, foreignTournament, foreignRoster, foreignParticipants[0], at)
	_, err := postgres.NewWavePostgres(fixture.tx).Create(ctx, postgres.WaveCreateInput{ID: uuid.New(), TournamentID: foreignTournament, RosterID: foreignRoster,
		RevisionID: domain.WaveRevisionID(uuid.New()), CommandID: uuid.New(), SourceProjectionRevisionID: foreignSource, SourceProjectionRevision: foreignRevision,
		ParticipantIDs: foreignParticipants[:2], CreatedAt: at, Series: []postgres.WaveSeriesInput{{ID: uuid.New(), FirstParticipantID: foreignParticipants[0], SecondParticipantID: foreignParticipants[1],
			Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New())}}})
	require.NoError(t, err)
	var foreignNode uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT id FROM result_projection_nodes WHERE tournament_id = $1 AND artifact_kind = 'series_score' ORDER BY id LIMIT 1`, foreignTournament).Scan(&foreignNode))
	authority := swissReceiptAuthority(ctx, t, fixture)
	command := progression.Command{CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, ActorID: uuid.New(), Action: progression.ActionStartPlayoffs, ExpectedProjectionRevision: authority.ProjectionRevision}
	input, err := postgres.NewTournamentProgressionPostgres(fixture.tx).LoadLockedSwissTerminalEvidence(ctx, command, authority)
	require.NoError(t, err)
	previous, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	for _, field := range []string{"Series result node", "score node", "game node", "cross-scope node"} {
		t.Run(field, func(t *testing.T) {
			publication := successorSwissPublication(ctx, t, fixture)
			err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
				record, err := postgres.NewProjectionPostgres(fixture.tx).Publish(txCtx, publication)
				if err != nil {
					return err
				}
				current := input
				current.RevisionID = domain.DerivedRevisionID(record.Revision.ID)
				current.RevisionNo = 2
				current.PhysicalProjectionRevision = int(record.Revision.RevisionNumber)
				current.Previous, current.CreatedAt = &previous, publication.CreatedAt
				planned, err := playoff.PlanFinalSwissReceipt(current)
				if err != nil {
					return err
				}
				return copySwissReceiptContract(txCtx, fixture.tx.Querier(txCtx), fixture, authority.ProjectionRevisionID, record, planned, false,
					func(series []sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow, games []sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow) {
						switch field {
						case "Series result node":
							series[0].SeriesResultNodeID = series[1].SeriesResultNodeID
						case "score node":
							series[0].ScoreNodeID = series[1].ScoreNodeID
						case "game node":
							games[0].GameResultNodeID = games[1].GameResultNodeID
						case "cross-scope node":
							series[0].ScoreNodeID = foreignNode
						}
					})
			})
			var detail *pgconn.PgError
			require.ErrorAs(t, err, &detail)
			require.Equal(t, "23514", detail.Code)
			current, _ := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
			require.Equal(t, authority.ProjectionRevisionID, current)
		})
	}
}
