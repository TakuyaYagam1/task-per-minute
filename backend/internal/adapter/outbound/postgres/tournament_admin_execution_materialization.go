package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// createMaterializedSeriesPresence is retained as a root-private bridge for
// the unmoved Swiss-draft materializer. Core Swiss materialization lives in
// tournament/admin/execution.
func createMaterializedSeriesPresence(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID, rosterID, seriesID uuid.UUID,
	participantIDs [2]uuid.UUID,
	connectedAt time.Time,
) error {
	for index, participantID := range participantIDs {
		presenceID := tournamentAdminExecutionID(seriesID, fmt.Sprintf("participant-%d-presence", index+1))
		created, err := querier.CreateSeriesPresence(ctx, sqlc.CreateSeriesPresenceParams{
			ID: presenceID, TournamentID: tournamentID, RosterID: rosterID,
			SeriesID: seriesID, ParticipantID: participantID, ConnectedAt: tstz(connectedAt),
		})
		if err != nil {
			return executionWriteError("create Series participant presence", err)
		}
		if created.ID != presenceID || created.TournamentID != tournamentID || created.RosterID != rosterID ||
			created.SeriesID != seriesID || created.ParticipantID != participantID || created.State != "connected" ||
			created.PresenceEpoch != 1 || created.Revision != 1 || created.DisconnectedAt.Valid {
			return fmt.Errorf("validate Series participant presence: %w", domain.ErrInternal)
		}
	}
	return nil
}
