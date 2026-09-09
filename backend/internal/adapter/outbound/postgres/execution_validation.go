package postgres

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validWaveCreateMetadata(in WaveCreateInput) bool {
	return in.ID != uuid.Nil && in.TournamentID != uuid.Nil && in.RosterID != uuid.Nil &&
		!in.RevisionID.IsZero() && in.CommandID != uuid.Nil &&
		in.SourceProjectionRevisionID != uuid.Nil && in.SourceProjectionRevision >= 1 &&
		validServerTime(in.CreatedAt) && len(in.ParticipantIDs) >= 2 &&
		len(in.Series) > 0
}

func waveParticipantSet(participantIDs []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	participants := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		if participantID == uuid.Nil {
			return nil, domain.ErrValidation
		}
		if _, duplicate := participants[participantID]; duplicate {
			return nil, domain.ErrValidation
		}
		participants[participantID] = struct{}{}
	}
	return participants, nil
}

func validateWaveSeries(series []WaveSeriesInput, participants map[uuid.UUID]struct{}) error {
	seriesIDs := make(map[uuid.UUID]struct{}, len(series))
	initialScoreIDs := make(map[domain.SeriesScoreRevisionID]struct{}, len(series))
	paired := make(map[uuid.UUID]struct{}, len(series)*2)
	for _, item := range series {
		if !validWaveSeries(item) {
			return domain.ErrValidation
		}
		if _, duplicate := seriesIDs[item.ID]; duplicate {
			return domain.ErrValidation
		}
		seriesIDs[item.ID] = struct{}{}
		if _, duplicate := initialScoreIDs[item.InitialScoreRevisionID]; duplicate {
			return domain.ErrValidation
		}
		initialScoreIDs[item.InitialScoreRevisionID] = struct{}{}
		if err := registerWavePair(item, participants, paired); err != nil {
			return err
		}
	}
	if len(participants)-len(paired) > 1 {
		return domain.ErrValidation
	}
	return nil
}

func validWaveSeries(item WaveSeriesInput) bool {
	return item.ID != uuid.Nil && item.FirstParticipantID != uuid.Nil && item.SecondParticipantID != uuid.Nil &&
		item.FirstParticipantID != item.SecondParticipantID && item.Format.IsValid() &&
		!item.InitialScoreRevisionID.IsZero()
}

func registerWavePair(
	item WaveSeriesInput,
	participants map[uuid.UUID]struct{},
	paired map[uuid.UUID]struct{},
) error {
	for _, participantID := range []uuid.UUID{item.FirstParticipantID, item.SecondParticipantID} {
		if _, member := participants[participantID]; !member {
			return domain.ErrValidation
		}
		if _, duplicate := paired[participantID]; duplicate {
			return domain.ErrValidation
		}
		paired[participantID] = struct{}{}
	}
	return nil
}

func validWaveReplacement(waveID uuid.UUID, replacesWaveID *uuid.UUID) bool {
	return replacesWaveID == nil || (*replacesWaveID != uuid.Nil && *replacesWaveID != waveID)
}
