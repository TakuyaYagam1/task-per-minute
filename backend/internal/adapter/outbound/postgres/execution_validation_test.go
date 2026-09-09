package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestValidateWaveCreateInput(t *testing.T) {
	first := uuid.New()
	second := uuid.New()
	base := WaveCreateInput{
		ID:                         uuid.New(),
		TournamentID:               uuid.New(),
		RosterID:                   uuid.New(),
		RevisionID:                 domain.WaveRevisionID(uuid.New()),
		CommandID:                  uuid.New(),
		SourceProjectionRevisionID: uuid.New(),
		SourceProjectionRevision:   1,
		ParticipantIDs:             []uuid.UUID{first, second},
		Series: []WaveSeriesInput{{
			ID:                     uuid.New(),
			FirstParticipantID:     first,
			SecondParticipantID:    second,
			Format:                 domain.SeriesFormatBO1,
			InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
		}},
		CreatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}

	if err := validateWaveCreateInput(base); err != nil {
		t.Fatalf("validate complete wave: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*WaveCreateInput)
	}{
		{
			name: "duplicate participant",
			mutate: func(in *WaveCreateInput) {
				in.ParticipantIDs[1] = in.ParticipantIDs[0]
			},
		},
		{
			name: "series participant outside roster",
			mutate: func(in *WaveCreateInput) {
				in.Series[0].SecondParticipantID = uuid.New()
			},
		},
		{
			name: "participant paired twice",
			mutate: func(in *WaveCreateInput) {
				third := uuid.New()
				in.ParticipantIDs = append(in.ParticipantIDs, third)
				in.Series = append(in.Series, WaveSeriesInput{
					ID:                     uuid.New(),
					FirstParticipantID:     in.ParticipantIDs[0],
					SecondParticipantID:    third,
					Format:                 domain.SeriesFormatBO1,
					InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
				})
			},
		},
		{
			name: "more than one bye",
			mutate: func(in *WaveCreateInput) {
				in.ParticipantIDs = append(in.ParticipantIDs, uuid.New(), uuid.New())
			},
		},
		{
			name: "self replacement",
			mutate: func(in *WaveCreateInput) {
				in.ReplacesWaveID = &in.ID
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.ParticipantIDs = append([]uuid.UUID(nil), base.ParticipantIDs...)
			input.Series = append([]WaveSeriesInput(nil), base.Series...)
			test.mutate(&input)
			if err := validateWaveCreateInput(input); err == nil {
				t.Fatal("validate invalid wave: expected error")
			}
		})
	}
}
