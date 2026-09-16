package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// scoreRevisionAttemptEvidence remains a root-only view for recovery and
// operator workflows that have not yet moved to the result child package.
type scoreRevisionAttemptEvidence struct {
	SlotID               uuid.UUID
	SlotPosition         int16
	GameAttemptID        uuid.UUID
	AttemptNumber        int32
	GameResultRevisionID uuid.UUID
	ResultEventID        uuid.UUID
	ResultState          string
	ResultReason         string
	WinnerID             uuid.NullUUID
	OccurredAt           pgtype.Timestamptz
	CreatedAt            pgtype.Timestamptz
}

func childScoreRevisionAttemptEvidence(value scoreRevisionAttemptEvidence) resultpostgres.ScoreRevisionAttemptEvidence {
	return resultpostgres.ScoreRevisionAttemptEvidence{
		SlotID: value.SlotID, SlotPosition: value.SlotPosition, GameAttemptID: value.GameAttemptID,
		AttemptNumber: value.AttemptNumber, GameResultRevisionID: value.GameResultRevisionID,
		ResultEventID: value.ResultEventID, ResultState: value.ResultState, ResultReason: value.ResultReason,
		WinnerID: value.WinnerID, OccurredAt: value.OccurredAt, CreatedAt: value.CreatedAt,
	}
}

func rootScoreRevisionAttemptEvidence(value resultpostgres.ScoreRevisionAttemptEvidence) scoreRevisionAttemptEvidence {
	return scoreRevisionAttemptEvidence{
		SlotID: value.SlotID, SlotPosition: value.SlotPosition, GameAttemptID: value.GameAttemptID,
		AttemptNumber: value.AttemptNumber, GameResultRevisionID: value.GameResultRevisionID,
		ResultEventID: value.ResultEventID, ResultState: value.ResultState, ResultReason: value.ResultReason,
		WinnerID: value.WinnerID, OccurredAt: value.OccurredAt, CreatedAt: value.CreatedAt,
	}
}

func copyScoreRevisionAttempts(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	newScoreRevisionID uuid.UUID,
	prior []sqlc.SeriesScoreRevisionAttempt,
	excludedGameAttemptIDs []uuid.UUID,
	createdAt pgtype.Timestamptz,
) error {
	return resultpostgres.CopyScoreRevisionAttempts(ctx, querier, scope, newScoreRevisionID, prior, excludedGameAttemptIDs, createdAt)
}

func createScoreRevisionAttempt(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	scoreRevisionID uuid.UUID,
	position int16,
	evidence scoreRevisionAttemptEvidence,
) error {
	return resultpostgres.CreateScoreRevisionAttempt(ctx, querier, scope, scoreRevisionID, position, childScoreRevisionAttemptEvidence(evidence))
}

func scoreRevisionAttemptPosition(rows []sqlc.SeriesScoreRevisionAttempt, gameAttemptID uuid.UUID) (int16, bool) {
	return resultpostgres.ScoreRevisionAttemptPosition(rows, gameAttemptID)
}

func nextScoreRevisionAttemptPosition(rows []sqlc.SeriesScoreRevisionAttempt) (int16, error) {
	return resultpostgres.NextScoreRevisionAttemptPosition(rows)
}

func scoreSlotPosition(value int32) (int16, error) {
	return resultpostgres.ScoreSlotPosition(value)
}

func normalNoShowScoreEvidence(
	graph []sqlc.ListRecoverySeriesGraphRow,
	seriesID uuid.UUID,
	revisions []domain.NormalNoShowGameRevision,
	resultEventID uuid.UUID,
	resolvedAt pgtype.Timestamptz,
) ([]scoreRevisionAttemptEvidence, error) {
	evidence, err := resultpostgres.NormalNoShowScoreEvidence(graph, seriesID, revisions, resultEventID, resolvedAt)
	if err != nil {
		return nil, err
	}
	result := make([]scoreRevisionAttemptEvidence, len(evidence))
	for index, item := range evidence {
		result[index] = rootScoreRevisionAttemptEvidence(item)
	}
	return result, nil
}
