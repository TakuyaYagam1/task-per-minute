package postgres

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// scoreRevisionAttemptEvidence is the persisted, normalized evidence for one
// terminal game attempt. It deliberately carries no client payload.
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

// copyScoreRevisionAttempts keeps the full immutable prior ledger in a new
// score revision. A correction may exclude exactly the game it replaces.
func copyScoreRevisionAttempts(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	newScoreRevisionID uuid.UUID,
	prior []sqlc.SeriesScoreRevisionAttempt,
	excludedGameAttemptIDs []uuid.UUID,
	createdAt pgtype.Timestamptz,
) error {
	excluded := make(map[uuid.UUID]struct{}, len(excludedGameAttemptIDs))
	for _, gameAttemptID := range excludedGameAttemptIDs {
		excluded[gameAttemptID] = struct{}{}
	}
	for _, row := range prior {
		if _, skip := excluded[row.GameAttemptID]; skip {
			continue
		}
		if err := createScoreRevisionAttempt(ctx, querier, scope, newScoreRevisionID, row.Position,
			scoreRevisionAttemptEvidence{
				SlotID:               row.SlotID,
				SlotPosition:         row.SlotPosition,
				GameAttemptID:        row.GameAttemptID,
				AttemptNumber:        row.AttemptNumber,
				GameResultRevisionID: row.GameResultRevisionID,
				ResultEventID:        row.ResultEventID,
				ResultState:          row.ResultState,
				ResultReason:         row.ResultReason,
				WinnerID:             row.WinnerID,
				OccurredAt:           row.OccurredAt,
				CreatedAt:            createdAt,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func createScoreRevisionAttempt(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	scoreRevisionID uuid.UUID,
	position int16,
	evidence scoreRevisionAttemptEvidence,
) error {
	if position < 1 || evidence.SlotID == uuid.Nil || evidence.SlotPosition < 1 ||
		evidence.GameAttemptID == uuid.Nil || evidence.AttemptNumber < 1 ||
		evidence.GameResultRevisionID == uuid.Nil || evidence.ResultEventID == uuid.Nil ||
		evidence.ResultState == "" || evidence.ResultReason == "" {
		return domain.ErrValidation
	}
	if err := querier.CreateSeriesScoreRevisionAttempt(ctx, sqlc.CreateSeriesScoreRevisionAttemptParams{
		ScoreRevisionID:      scoreRevisionID,
		TournamentID:         scope.TournamentID,
		RosterID:             scope.RosterID,
		SeriesID:             scope.SeriesID,
		Position:             position,
		SlotID:               evidence.SlotID,
		SlotPosition:         evidence.SlotPosition,
		GameAttemptID:        evidence.GameAttemptID,
		AttemptNumber:        evidence.AttemptNumber,
		GameResultRevisionID: evidence.GameResultRevisionID,
		ResultEventID:        evidence.ResultEventID,
		ResultState:          evidence.ResultState,
		ResultReason:         evidence.ResultReason,
		WinnerID:             evidence.WinnerID,
		OccurredAt:           evidence.OccurredAt,
		CreatedAt:            evidence.CreatedAt,
	}); err != nil {
		return mapRepositoryWriteError("create Series-score attempt ledger", err)
	}
	return nil
}

func scoreRevisionAttemptPosition(
	rows []sqlc.SeriesScoreRevisionAttempt,
	gameAttemptID uuid.UUID,
) (int16, bool) {
	for _, row := range rows {
		if row.GameAttemptID == gameAttemptID {
			return row.Position, true
		}
	}
	return 0, false
}

func nextScoreRevisionAttemptPosition(rows []sqlc.SeriesScoreRevisionAttempt) (int16, error) {
	if len(rows) >= math.MaxInt16 {
		return 0, fmt.Errorf("Series-score attempt ledger exceeds position range")
	}
	return int16(len(rows) + 1), nil //nolint:gosec // bounded directly above by the smallint maximum.
}

func scoreSlotPosition(value int32) (int16, error) {
	if value < 1 || value > math.MaxInt16 {
		return 0, fmt.Errorf("invalid Series-score slot position")
	}
	return int16(value), nil //nolint:gosec // bounds are checked above.
}

func normalNoShowScoreEvidence(
	graph []sqlc.ListRecoverySeriesGraphRow,
	seriesID uuid.UUID,
	revisions []domain.NormalNoShowGameRevision,
	resultEventID uuid.UUID,
	resolvedAt pgtype.Timestamptz,
) ([]scoreRevisionAttemptEvidence, error) {
	byGame := make(map[uuid.UUID]sqlc.ListRecoverySeriesGraphRow, len(graph))
	for _, row := range graph {
		if row.GameAttempt.SeriesID != seriesID || row.GameSlot.SeriesID != seriesID ||
			row.GameAttempt.ID == uuid.Nil || row.GameSlot.ID == uuid.Nil ||
			row.GameAttempt.SlotID != row.GameSlot.ID {
			return nil, domain.ErrConflict
		}
		if _, exists := byGame[row.GameAttempt.ID]; exists {
			return nil, domain.ErrConflict
		}
		byGame[row.GameAttempt.ID] = row
	}
	evidence := make([]scoreRevisionAttemptEvidence, 0, len(revisions))
	seen := make(map[uuid.UUID]struct{}, len(revisions))
	for _, revision := range revisions {
		row, found := byGame[revision.GameID]
		if !found || revision.ID.IsZero() || revision.State != domain.GameStateCancelled ||
			revision.Reason != domain.GameResultReasonSeriesCancelled {
			return nil, domain.ErrConflict
		}
		if _, duplicate := seen[revision.GameID]; duplicate {
			return nil, domain.ErrConflict
		}
		seen[revision.GameID] = struct{}{}
		slotPosition, err := scoreSlotPosition(int32(row.GameSlot.SlotNumber))
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, scoreRevisionAttemptEvidence{
			SlotID:               row.GameSlot.ID,
			SlotPosition:         slotPosition,
			GameAttemptID:        revision.GameID,
			AttemptNumber:        row.GameAttempt.AttemptNumber,
			GameResultRevisionID: revision.ID.UUID(),
			ResultEventID:        resultEventID,
			ResultState:          string(revision.State),
			ResultReason:         string(revision.Reason),
			OccurredAt:           resolvedAt,
			CreatedAt:            resolvedAt,
		})
	}
	sort.Slice(evidence, func(first, second int) bool {
		if evidence[first].SlotPosition != evidence[second].SlotPosition {
			return evidence[first].SlotPosition < evidence[second].SlotPosition
		}
		return evidence[first].AttemptNumber < evidence[second].AttemptNumber
	})
	return evidence, nil
}
