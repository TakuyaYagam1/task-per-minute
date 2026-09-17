package recovery

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type recoveryDeadlineRow struct {
	kind                  string
	id                    uuid.UUID
	tournamentID          uuid.UUID
	rosterID              uuid.UUID
	waveID                uuid.UUID
	seriesID              uuid.UUID
	slotID                uuid.UUID
	gameID                uuid.UUID
	pauseID               uuid.UUID
	readyWindowRevisionID uuid.UUID
	participantID         uuid.UUID
	expectedRevision      int64
	dueAt                 pgtype.Timestamptz
}

func recoveryDeadlineFromListRow(row sqlc.ListPendingRecoveryDeadlinesRow) (recoveryusecase.PendingDeadline, error) {
	return mapRecoveryDeadline(recoveryDeadlineRow{
		kind: row.DeadlineKind, id: row.DeadlineID, tournamentID: row.TournamentID,
		rosterID: row.RosterID, waveID: row.WaveID, seriesID: row.SeriesID,
		slotID: row.SlotID, gameID: row.GameID, pauseID: row.PauseID,
		readyWindowRevisionID: row.ReadyWindowRevisionID, participantID: row.ParticipantID,
		expectedRevision: row.ExpectedRevision, dueAt: row.DueAt,
	})
}

func recoveryDeadlineFromGetRow(row sqlc.GetPendingRecoveryDeadlineRow) (recoveryusecase.PendingDeadline, error) {
	return mapRecoveryDeadline(recoveryDeadlineRow{
		kind: row.DeadlineKind, id: row.DeadlineID, tournamentID: row.TournamentID,
		rosterID: row.RosterID, waveID: row.WaveID, seriesID: row.SeriesID,
		slotID: row.SlotID, gameID: row.GameID, pauseID: row.PauseID,
		readyWindowRevisionID: row.ReadyWindowRevisionID, participantID: row.ParticipantID,
		expectedRevision: row.ExpectedRevision, dueAt: row.DueAt,
	})
}

func mapRecoveryDeadline(row recoveryDeadlineRow) (recoveryusecase.PendingDeadline, error) {
	if !row.dueAt.Valid {
		return recoveryusecase.PendingDeadline{}, domain.ErrInternal
	}
	deadline := recoveryusecase.PendingDeadline{
		Kind: recoveryusecase.DeadlineKind(row.kind), ID: row.id,
		TournamentID: row.tournamentID, RosterID: row.rosterID,
		WaveID: row.waveID, SeriesID: row.seriesID, SlotID: row.slotID,
		GameID: row.gameID, PauseID: row.pauseID,
		ReadyWindowRevisionID: row.readyWindowRevisionID,
		ParticipantID:         row.participantID, ExpectedRevision: row.expectedRevision,
		DueAt: row.dueAt.Time.Round(0).UTC(),
	}
	if deadline.Validate() != nil {
		return recoveryusecase.PendingDeadline{}, domain.ErrInternal
	}
	return deadline, nil
}

func recoveryCursorKind(kind recoveryusecase.DeadlineKind) int16 {
	switch kind {
	case recoveryusecase.DeadlineKindGame:
		return 1
	case recoveryusecase.DeadlineKindReadyWindow:
		return 2
	case recoveryusecase.DeadlineKindReconnect:
		return 3
	default:
		return 0
	}
}

func sameRecoveryDeadline(left, right recoveryusecase.PendingDeadline) bool {
	return left.Kind == right.Kind && left.ID == right.ID &&
		left.TournamentID == right.TournamentID && left.RosterID == right.RosterID &&
		left.WaveID == right.WaveID && left.SeriesID == right.SeriesID &&
		left.SlotID == right.SlotID && left.GameID == right.GameID &&
		left.PauseID == right.PauseID &&
		left.ReadyWindowRevisionID == right.ReadyWindowRevisionID &&
		left.ParticipantID == right.ParticipantID &&
		left.ExpectedRevision == right.ExpectedRevision && left.DueAt.Equal(right.DueAt)
}
