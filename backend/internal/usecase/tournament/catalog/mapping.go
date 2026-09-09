package catalog

import (
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func tournamentView(record CatalogTournamentRecord) (usecase.TournamentView, error) {
	if err := validateTournamentRecord(record); err != nil {
		return usecase.TournamentView{}, err
	}
	return usecase.TournamentView{
		ID:              record.ID,
		RosterID:        record.RosterID,
		Preset:          record.Preset,
		State:           record.State,
		PausedFromState: cloneState(record.PausedFromState),
		Revision:        record.Revision,
		RosterSize:      record.RosterSize,
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       record.UpdatedAt,
		StartedAt:       cloneTime(record.StartedAt),
		FinishedAt:      cloneTime(record.FinishedAt),
	}, nil
}
