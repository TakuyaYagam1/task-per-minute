package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func persistTerminalProjectionNodes(ctx context.Context, tx *TxManager, in ResultSettlementInput) error {
	q := tx.Querier(ctx)
	commits, err := q.LockTerminalProjectionCommit(ctx, sqlc.LockTerminalProjectionCommitParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID, ProjectionRevisionID: in.IDs.ProjectionEvidenceID,
	})
	if err != nil || len(commits) == 0 {
		return err
	}
	if len(commits) != 1 || !commits[0].ResolvedAt.Valid || !commits[0].ResolvedAt.Time.Equal(in.SettledAt) {
		return domain.ErrConflict
	}
	commit := commits[0]
	// Live forfeits already own ordinary result-commit nodes. Only their Swiss
	// point evidence needs normalization before the shared publication.
	if commit.SourceKind == "result_commit" {
		return persistTerminalSwissPoints(ctx, q, in, commit)
	}
	authorityID := uuid.NewSHA1(commit.ID, []byte("terminal-projection-authority"))
	authority := sqlc.CreateTerminalProjectionAuthorityParams{ID: authorityID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SourceKind: commit.SourceKind, CreatedAt: tstz(in.SettledAt)}
	switch commit.SourceKind {
	case "normal_no_show_commit":
		authority.NormalNoShowCommitID = nullableUUIDValue(commit.ID)
	case "operator_forfeit_commit":
		authority.OperatorForfeitCommitID = nullableUUIDValue(commit.ID)
	default:
		return domain.ErrConflict
	}
	if err := q.CreateTerminalProjectionAuthority(ctx, authority); err != nil {
		return err
	}
	revisions, err := q.LockTerminalProjectionRevisions(ctx, sqlc.LockTerminalProjectionRevisionsParams{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID, ResultEventID: commit.ResultEventID})
	if err != nil {
		return err
	}
	scoreFound, resultFound := false, false
	var games []uuid.UUID
	for _, revision := range revisions {
		if !revision.CreatedAt.Valid || !revision.CreatedAt.Time.Equal(in.SettledAt) {
			return domain.ErrConflict
		}
		previous := revision.PreviousRevisionID
		if previous.Valid && revision.ArtifactKind == "series_score" && revision.RevisionNumber == 2 {
			nodes, err := q.LockStageScoreGenesisNodes(ctx, sqlc.LockStageScoreGenesisNodesParams{ScoreRevisionID: previous.UUID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID})
			if err != nil {
				return err
			}
			if len(nodes) > 0 {
				id, err := stageScoreGenesisNode(in.Scope, previous.UUID, nodes)
				if err != nil {
					return err
				}
				previous = nullableUUIDValue(id)
			}
		}
		payload, err := marshalJSON("terminal projection node", map[string]any{
			"schema": "terminal-result-node-v1", "revision_id": revision.ID.String(), "entity_id": revision.EntityID.String(),
			"result_event_id": commit.ResultEventID.String(), "artifact_kind": revision.ArtifactKind,
		})
		if err != nil {
			return err
		}
		if err := createResultProjectionNode(ctx, q, sqlc.CreateResultProjectionNodeParams{ID: revision.ID, AuthorityID: authorityID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			ArtifactKind: revision.ArtifactKind, EntityID: revision.EntityID, RevisionNumber: revision.RevisionNumber, PreviousNodeID: previous, Payload: payload, PayloadDigest: digestBytes(payload), CreatedAt: revision.CreatedAt}); err != nil {
			return err
		}
		switch revision.ArtifactKind {
		case "series_score":
			scoreFound = revision.ID == commit.SeriesScoreRevisionID
		case "series_result":
			resultFound = revision.ID == commit.SeriesResultRevisionID
		case "game_result":
			games = append(games, revision.ID)
		}
	}
	if !scoreFound || !resultFound || (commit.SourceKind == "operator_forfeit_commit" && len(games) != 0) || (commit.SourceKind == "normal_no_show_commit" && len(games) == 0) {
		return domain.ErrConflict
	}
	for _, gameID := range games {
		if err := createResultProjectionDependency(ctx, q, authorityID, gameID, commit.SeriesScoreRevisionID, in.SettledAt); err != nil {
			return err
		}
	}
	if err := createResultProjectionDependency(ctx, q, authorityID, commit.SeriesScoreRevisionID, commit.SeriesResultRevisionID, in.SettledAt); err != nil {
		return err
	}
	return persistTerminalSwissPoints(ctx, q, in, commit)
}
