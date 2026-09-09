package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentProgressionPostgres) persistPlayoffEvidence(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, publication tournamentprogression.PlayoffPublication, now time.Time) error {
	command, ids := plan.Record.Command, plan.PublicationIDs
	id, err := q.CreateTournamentStagePlayoffEvidence(ctx, sqlc.CreateTournamentStagePlayoffEvidenceParams{
		CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID, SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision,
		PublishedProjectionRevisionID: publication.PublishedProjectionID, PublishedProjectionRevision: publication.PublishedRevision, Top4ArtifactID: publication.Top4ArtifactID, BracketArtifactID: publication.BracketArtifactID,
		Top4NodeID: ids.Top4NodeID, BracketNodeID: ids.BracketNodeID, FirstSemifinalSeriesID: publication.SemifinalSeriesIDs[0], SecondSemifinalSeriesID: publication.SemifinalSeriesIDs[1], ProofDigest: plan.Record.ProofDigest[:], CreatedAt: tstz(now),
	})
	if err := progressionWrittenID("create playoff evidence", command.CommandID, id, err); err != nil {
		return err
	}
	if err := persistProgressionStageGraph(ctx, q, plan, now); err != nil {
		return err
	}
	for _, match := range plan.Bracket.Semifinals() {
		position, conversionErr := progressionInt16(match.Position)
		if conversionErr != nil {
			return conversionErr
		}
		id, err = q.CreateTournamentStagePlayoffSemifinal(ctx, sqlc.CreateTournamentStagePlayoffSemifinalParams{CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID, BracketArtifactID: publication.BracketArtifactID, Position: position, SeriesID: match.Series.ID, CreatedAt: tstz(now)})
		if err := progressionWrittenID("bind semifinal", match.Series.ID, id, err); err != nil {
			return err
		}
	}
	if plan.Record.SourceState == domain.TournamentStateGolden {
		rows, err := q.LockTournamentProgressionGoldenPositionCommits(ctx, sqlc.LockTournamentProgressionGoldenPositionCommitsParams{TournamentID: command.TournamentID, RosterID: command.RosterID, SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision})
		if err != nil {
			return tournamentProgressionReadError("read settled Golden positions", err)
		}
		if len(rows) == 0 {
			return domain.ErrConflict
		}
		expected := make(map[uuid.UUID]struct{})
		for _, commitID := range plan.Top4.GoldenPositionCommitIDs() {
			expected[commitID] = struct{}{}
		}
		if len(rows) != len(expected) {
			return domain.ErrConflict
		}
		for _, position := range rows {
			if _, found := expected[position.PositionCommitID]; !found {
				return domain.ErrConflict
			}
			delete(expected, position.PositionCommitID)
			id, err = q.CreateTournamentStagePlayoffGoldenSettlement(ctx, sqlc.CreateTournamentStagePlayoffGoldenSettlementParams{CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: command.RosterID, GroupRevisionID: position.GroupRevisionID, AttemptID: position.AttemptID, PositionCommitID: position.PositionCommitID, ParticipantID: position.ParticipantID, Position: position.Position, CreatedAt: tstz(now)})
			if err := progressionWrittenID("bind Golden settlement", position.PositionCommitID, id, err); err != nil {
				return err
			}
		}
	}
	return nil
}

func persistProgressionStageGraph(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, now time.Time) error {
	command, ids := plan.Record.Command, plan.PublicationIDs
	id, err := q.CreateTournamentProgressionStageProjectionNodeAuthority(ctx, sqlc.CreateTournamentProgressionStageProjectionNodeAuthorityParams{ID: ids.StageNodeAuthorityID, TournamentID: command.TournamentID, RosterID: command.RosterID, StageCommandID: nullableUUIDValue(command.CommandID), CreatedAt: tstz(now)})
	if err := progressionWrittenID("create stage node authority", ids.StageNodeAuthorityID, id, err); err != nil {
		return err
	}
	nodes := []struct {
		id      uuid.UUID
		kind    domain.ArtifactKind
		entity  uuid.UUID
		payload []byte
	}{
		{ids.Top4NodeID, domain.ArtifactKindTopFour, command.TournamentID, plan.Top4.Projection().Payload()},
		{ids.BracketNodeID, domain.ArtifactKindBracket, command.TournamentID, plan.Bracket.Projection().Payload()},
	}
	scoreIDs := [2]domain.SeriesScoreRevisionID{ids.FirstSemifinalScoreRevisionID, ids.SecondSemifinalScoreRevisionID}
	scoreNodes := [2]uuid.UUID{ids.FirstSemifinalScoreNodeID, ids.SecondSemifinalScoreNodeID}
	for index, match := range plan.Bracket.Semifinals() {
		payload, err := json.Marshal(map[string]any{"schema": "result-projection-series-score-genesis-v1", "stage_command_id": command.CommandID.String(), "series_id": match.Series.ID.String(), "revision_id": scoreIDs[index].UUID().String(), "first_wins": 0, "second_wins": 0})
		if err != nil {
			return err
		}
		nodes = append(nodes, struct {
			id      uuid.UUID
			kind    domain.ArtifactKind
			entity  uuid.UUID
			payload []byte
		}{scoreNodes[index], domain.ArtifactKindSeriesScore, match.Series.ID, payload})
	}
	for _, node := range nodes {
		digest := sha256.Sum256(node.payload)
		id, err = q.CreateTournamentProgressionStageProjectionNode(ctx, sqlc.CreateTournamentProgressionStageProjectionNodeParams{ID: node.id, AuthorityID: ids.StageNodeAuthorityID, TournamentID: command.TournamentID, RosterID: command.RosterID, ArtifactKind: string(node.kind), EntityID: node.entity, Payload: node.payload, PayloadDigest: digest[:], CreatedAt: tstz(now)})
		if err := progressionWrittenID("create stage node", node.id, id, err); err != nil {
			return err
		}
	}
	for _, edge := range [][2]uuid.UUID{{ids.Top4NodeID, ids.BracketNodeID}, {ids.BracketNodeID, ids.FirstSemifinalScoreNodeID}, {ids.BracketNodeID, ids.SecondSemifinalScoreNodeID}} {
		id, err = q.CreateTournamentProgressionStageProjectionDependency(ctx, sqlc.CreateTournamentProgressionStageProjectionDependencyParams{AuthorityID: ids.StageNodeAuthorityID, SourceNodeID: edge[0], DerivedNodeID: edge[1], CreatedAt: tstz(now)})
		if err := progressionWrittenID("create stage edge", ids.StageNodeAuthorityID, id, err); err != nil {
			return err
		}
	}
	return nil
}
