package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentProgressionPostgres) finalSwissPublicationRows(ctx context.Context, authority tournamentprogression.Authority, now time.Time) (progressionFinalSwissReceiptRows, error) {
	q := r.tx.Querier(ctx)
	scope := ProjectionScope{TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID}
	rows := progressionFinalSwissReceiptRows{}
	params := sqlc.LockFinalSwissPublicationSeriesParams{
		ProjectionRevisionID: authority.ProjectionRevisionID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}
	if _, err := q.LockTournamentProgressionSwissResultHeads(ctx, sqlc.LockTournamentProgressionSwissResultHeadsParams(scope)); err != nil {
		return rows, err
	}
	if _, err := q.LockTournamentProgressionSwissScoreHeads(ctx, sqlc.LockTournamentProgressionSwissScoreHeadsParams(scope)); err != nil {
		return rows, err
	}
	if _, err := q.LockTournamentProgressionSwissGameHeads(ctx, sqlc.LockTournamentProgressionSwissGameHeadsParams(scope)); err != nil {
		return rows, err
	}
	series, err := q.LockFinalSwissPublicationSeries(ctx, params)
	if err != nil {
		return rows, err
	}
	for _, row := range series {
		rows.seriesEvidence = append(rows.seriesEvidence, sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow(row))
	}
	games, err := q.LockFinalSwissPublicationGames(ctx, sqlc.LockFinalSwissPublicationGamesParams(params))
	if err != nil {
		return rows, err
	}
	for _, row := range games {
		rows.gameEvidence = append(rows.gameEvidence, sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow(row))
		rows.games = append(rows.games, sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow{
			ProjectionRevisionID: params.ProjectionRevisionID, SeriesID: row.SeriesID,
			GameAttemptID: row.GameAttemptID, GameResultRevisionID: row.GameResultRevisionID,
			GameResultNodeID: row.GameResultNodeID, CreatedAt: tstz(now),
		})
	}
	logical, err := q.LockFinalSwissPublicationNodes(ctx, sqlc.LockFinalSwissPublicationNodesParams(params))
	if err != nil {
		return rows, err
	}
	for _, row := range logical {
		rows.logicalNodes = append(rows.logicalNodes, sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow(row))
		rows.nodes = append(rows.nodes, sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesRow{
			ProjectionRevisionID: params.ProjectionRevisionID, ID: row.NodeID, AuthorityID: row.AuthorityID,
			ArtifactKind: row.ArtifactKind, EntityID: row.EntityID, RevisionNumber: row.RevisionNumber,
			PreviousNodeID: row.PreviousNodeID, Payload: row.Payload, PayloadDigest: row.PayloadDigest,
			CreatedAt: row.NodeCreatedAt,
		})
	}
	participants, err := q.LockTournamentProgressionParticipants(ctx, scope.RosterID)
	if err != nil {
		return rows, err
	}
	for _, row := range participants {
		rows.participants = append(rows.participants, sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsRow{
			ProjectionRevisionID: params.ProjectionRevisionID, ParticipantID: row.ID, StableSeed: row.Seed, CreatedAt: tstz(now),
		})
	}
	rows.lockProofs, err = q.LockTournamentProgressionSwissRoundLockProofs(ctx, sqlc.LockTournamentProgressionSwissRoundLockProofsParams(scope))
	if err != nil {
		return rows, err
	}
	rows.lockProofMembers, err = q.LockTournamentProgressionSwissRoundLockProofMembers(ctx, sqlc.LockTournamentProgressionSwissRoundLockProofMembersParams(scope))
	if err != nil {
		return rows, err
	}
	rows.lockProofSeries, err = q.LockTournamentProgressionSwissRoundLockProofSeries(ctx, sqlc.LockTournamentProgressionSwissRoundLockProofSeriesParams(scope))
	if err != nil {
		return rows, err
	}
	for _, proof := range rows.lockProofs {
		rows.rounds = append(rows.rounds, sqlc.LockTournamentProgressionFinalSwissReceiptRoundsRow{
			ProjectionRevisionID: params.ProjectionRevisionID, RoundID: proof.RoundID, RoundNumber: proof.RoundNumber, CreatedAt: tstz(now),
		})
	}
	if err := loadFinalSwissPublicationChildren(ctx, q, scope, params.ProjectionRevisionID, now, &rows); err != nil {
		return rows, err
	}
	record, err := loadProjectionRecord(ctx, q, scope, params.ProjectionRevisionID)
	if err != nil {
		return rows, err
	}
	root, err := finalSwissPublicationRoot(record, now)
	if err != nil {
		return rows, err
	}
	rows.chain = []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{root}
	if err := loadFinalSwissPublicationSources(ctx, q, scope, &rows); err != nil {
		return rows, err
	}
	return rows, nil
}

func loadFinalSwissPublicationSources(ctx context.Context, q *sqlc.Queries, scope ProjectionScope, rows *progressionFinalSwissReceiptRows) error {
	type reference struct {
		kind                     string
		entity, revision, source uuid.UUID
		number                   int64
	}
	refs := make([]reference, 0, len(rows.seriesEvidence)*2+len(rows.gameEvidence))
	for _, row := range rows.seriesEvidence {
		refs = append(refs,
			reference{"series_result", row.SeriesID, row.SeriesResultRevisionID, row.SeriesResultSourceProjectionRevisionID, row.SeriesResultSourceProjectionRevision},
			reference{"series_score", row.SeriesID, row.ScoreRevisionID, row.ScoreSourceProjectionRevisionID, row.ScoreSourceProjectionRevision})
	}
	for _, row := range rows.gameEvidence {
		refs = append(refs, reference{"game_result", row.GameAttemptID, row.GameResultRevisionID, row.GameResultSourceProjectionRevisionID, row.GameResultSourceProjectionRevision})
	}
	cache := make(map[uuid.UUID]*ProjectionRecord)
	for _, ref := range refs {
		record, found := cache[ref.source]
		if !found {
			var err error
			record, err = loadProjectionRecord(ctx, q, scope, ref.source)
			if err != nil {
				return err
			}
			cache[ref.source] = record
		}
		if record.Revision.RevisionNumber != ref.number {
			return domain.ErrConflict
		}
		for _, item := range record.Artifacts {
			artifact := item.Artifact
			rows.sourceProjections = append(rows.sourceProjections, sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow{
				ReceiptProjectionRevisionID: rows.chain[0].ProjectionRevisionID,
				ResultKind:                  ref.kind, EntityID: ref.entity, ResultRevisionID: ref.revision,
				SourceProjectionRevisionID: ref.source, SourceProjectionRevision: ref.number,
				PhysicalProjectionRevisionID: record.Revision.ID, PhysicalProjectionRevision: record.Revision.RevisionNumber,
				PhysicalPreviousProjectionRevisionID: record.Revision.PreviousRevisionID,
				PhysicalProjectionState:              record.Revision.State, PhysicalProjectionCreatedAt: record.Revision.CreatedAt,
				ArtifactID: artifact.ID, ArtifactKind: artifact.ArtifactKind, Payload: artifact.Payload,
				PayloadDigest: artifact.PayloadDigest, ArtifactCreatedAt: artifact.CreatedAt,
			})
		}
	}
	return nil
}
