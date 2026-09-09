package domain

import (
	"time"

	"github.com/google/uuid"
)

type NormalNoShowAction string

const (
	NormalNoShowActionReopenWave NormalNoShowAction = "reopen_wave"
	NormalNoShowActionPauseWave  NormalNoShowAction = "pause_wave"
)

type NormalNoShowScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
	WindowID     uuid.UUID
	SeriesID     uuid.UUID
}

func (s NormalNoShowScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.WaveID != uuid.Nil &&
		s.WindowID != uuid.Nil && s.SeriesID != uuid.Nil
}

type NormalNoShowGameRevision struct {
	Ordinal            int
	ID                 OfficialResultRevisionID
	GameID             uuid.UUID
	PreviousRevisionID *OfficialResultRevisionID
	State              GameState
	Reason             GameResultReason
	RecordedAt         time.Time
}

type NormalNoShowScoreRevision struct {
	Ordinal               int
	ID                    SeriesScoreRevisionID
	SeriesID              uuid.UUID
	PreviousRevisionID    *SeriesScoreRevisionID
	Score                 SeriesScore
	GameResultRevisionIDs []OfficialResultRevisionID
	RecordedAt            time.Time
}

type NormalNoShowSeriesRevision struct {
	Ordinal            int
	ID                 OfficialResultRevisionID
	SeriesID           uuid.UUID
	PreviousRevisionID *OfficialResultRevisionID
	State              SeriesState
	WinnerID           *uuid.UUID
	ScoreRevisionID    SeriesScoreRevisionID
	RecordedAt         time.Time
}
