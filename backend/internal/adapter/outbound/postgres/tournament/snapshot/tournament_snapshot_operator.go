package snapshot

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func operatorTournamentReadWaves(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.OperatorWaveView, error) {
	waveRows, err := querier.ListOperatorTournamentReadWaves(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - OperatorSnapshot - waves: %w", err)
	}
	memberRows, err := querier.ListOperatorTournamentReadWaveMembers(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - OperatorSnapshot - wave members: %w", err)
	}
	members := make(map[uuid.UUID][]usecase.OperatorWaveMemberView, len(waveRows))
	for _, row := range memberRows {
		if row.WaveID == uuid.Nil || row.ParticipantID == uuid.Nil || row.ReadinessRevision < 1 ||
			row.SeriesCount > 1 {
			return nil, tournamentSnapshotInvalidError("wave member readiness")
		}
		seriesID, err := optionalTournamentUUID(row.SeriesID)
		if err != nil {
			return nil, tournamentSnapshotInvalidError("wave member series")
		}
		members[row.WaveID] = append(members[row.WaveID], usecase.OperatorWaveMemberView{
			ParticipantID:     row.ParticipantID,
			SeriesID:          seriesID,
			Ready:             row.Ready,
			ReadinessRevision: row.ReadinessRevision,
		})
	}
	view := make([]usecase.OperatorWaveView, len(waveRows))
	for index, row := range waveRows {
		waveMembers := members[row.WaveID]
		if waveMembers == nil {
			waveMembers = []usecase.OperatorWaveMemberView{}
		}
		view[index] = usecase.OperatorWaveView{
			WaveID:         row.WaveID,
			State:          row.State,
			WindowDeadline: utcNullableTime(row.WindowDeadline),
			Members:        waveMembers,
		}
	}
	return view, nil
}

func operatorTournamentReadPresence(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.OperatorPresenceView, error) {
	rows, err := querier.ListOperatorTournamentReadPresence(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - OperatorSnapshot - presence: %w", err)
	}
	view := make([]usecase.OperatorPresenceView, len(rows))
	for index, row := range rows {
		if !row.UpdatedAt.Valid {
			return nil, tournamentSnapshotInvalidError("presence timestamp")
		}
		view[index] = usecase.OperatorPresenceView{
			ParticipantID: row.ParticipantID,
			SeriesID:      row.SeriesID,
			State:         row.State,
			PresenceEpoch: row.PresenceEpoch,
			UpdatedAt:     row.UpdatedAt.Time.UTC(),
		}
	}
	return view, nil
}

func operatorTournamentReadReplays(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.OperatorReplayView, error) {
	rows, err := querier.ListOperatorTournamentReadReplays(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - OperatorSnapshot - replays: %w", err)
	}
	view := make([]usecase.OperatorReplayView, len(rows))
	for index, row := range rows {
		view[index] = usecase.OperatorReplayView{
			SeriesID:          row.SeriesID,
			SlotID:            row.SlotID,
			FailedGameID:      row.FailedGameID,
			ReplacementGameID: row.ReplacementGameID,
			ReplacementWaveID: row.ReplacementWaveID,
			State:             row.State,
			Revision:          row.Revision,
		}
	}
	return view, nil
}

func operatorTournamentReadPause(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (*usecase.OperatorPauseView, error) {
	row, err := querier.GetOperatorTournamentReadPause(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - OperatorSnapshot - pause: %w", err)
	}
	if !row.PausedAt.Valid {
		return nil, tournamentSnapshotInvalidError("pause timestamp")
	}
	return &usecase.OperatorPauseView{
		PauseID:       row.PauseID,
		State:         row.State,
		Reason:        row.Reason,
		PausedAt:      row.PausedAt.Time.UTC(),
		GraphRevision: row.GraphRevision,
	}, nil
}

func operatorTournamentReadAuditLinks(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) ([]usecase.OperatorAuditLinkView, error) {
	rows, err := querier.ListOperatorTournamentReadAuditLinks(ctx, tournamentID)
	if err != nil {
		return nil, fmt.Errorf("TournamentSnapshotPostgres - OperatorSnapshot - audit links: %w", err)
	}
	view := make([]usecase.OperatorAuditLinkView, len(rows))
	for index, row := range rows {
		view[index] = usecase.OperatorAuditLinkView{
			AuditEventID:             row.AuditEventID,
			EntityKind:               row.EntityKind,
			EntityID:                 row.EntityID,
			OfficialResultRevisionID: row.OfficialResultRevisionID,
		}
	}
	return view, nil
}
