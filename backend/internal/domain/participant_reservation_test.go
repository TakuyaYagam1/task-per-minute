package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestParticipantReservationValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	valid := domain.ParticipantReservation{
		PlayerID:      uuid.New(),
		ReservationID: uuid.New(),
		TournamentID:  uuid.New(),
		Revision:      1,
		AcquiredAt:    now,
		UpdatedAt:     now,
	}
	require.True(t, valid.IsValid())

	tests := map[string]func(*domain.ParticipantReservation){
		"missing player": func(value *domain.ParticipantReservation) {
			value.PlayerID = uuid.Nil
		},
		"missing reservation": func(value *domain.ParticipantReservation) {
			value.ReservationID = uuid.Nil
		},
		"missing tournament": func(value *domain.ParticipantReservation) {
			value.TournamentID = uuid.Nil
		},
		"invalid revision": func(value *domain.ParticipantReservation) {
			value.Revision = 0
		},
		"missing acquisition time": func(value *domain.ParticipantReservation) {
			value.AcquiredAt = time.Time{}
		},
		"update before acquisition": func(value *domain.ParticipantReservation) {
			value.UpdatedAt = now.Add(-time.Second)
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			value := valid
			mutate(&value)
			require.False(t, value.IsValid())
		})
	}
}
