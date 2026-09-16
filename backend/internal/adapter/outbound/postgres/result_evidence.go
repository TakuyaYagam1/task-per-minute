package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func stageScoreGenesisNode(scope ResultScope, revisionID uuid.UUID, nodes []sqlc.LockStageScoreGenesisNodesRow) (uuid.UUID, error) {
	return resultpostgres.StageScoreGenesisNode(scope, revisionID, nodes)
}

func createResultProjectionNode(ctx context.Context, querier *sqlc.Queries, input sqlc.CreateResultProjectionNodeParams) error {
	return resultpostgres.CreateResultProjectionNode(ctx, querier, input)
}

func createResultProjectionDependency(
	ctx context.Context,
	querier *sqlc.Queries,
	authorityID, sourceID, derivedID uuid.UUID,
	createdAt time.Time,
) error {
	return resultpostgres.CreateResultProjectionDependency(ctx, querier, authorityID, sourceID, derivedID, createdAt)
}

func digestBytes(payload []byte) []byte {
	return resultpostgres.DigestBytes(payload)
}
