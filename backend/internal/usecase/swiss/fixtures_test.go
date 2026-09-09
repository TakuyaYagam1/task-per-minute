package swiss_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	swissmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss/mocks"
)

func newRoundLockProof(
	t *testing.T,
) (swissusecase.RoundLockProof, swissusecase.RoundLockProofInput) {
	t.Helper()
	const (
		roundNumber = 1
		rosterSize  = 4
	)

	participants := make([]uuid.UUID, rosterSize)
	for index := range participants {
		participants[index] = task026ID(100 + index)
	}
	series := make([]swissusecase.LockedSeries, 0, rosterSize/2)
	for index := 0; index+1 < rosterSize; index += 2 {
		series = append(series, swissusecase.LockedSeries{
			SeriesID: task026ID(200 + index), PairingID: task026ID(300 + index),
			FirstParticipantID: participants[index], SecondParticipantID: participants[index+1],
			CategoryRevisionID: task026ID(400 + index), CategoryRevision: 2,
			AssignmentID: task026ID(500 + index), AssignmentRevision: 2,
			AssignmentPlanID: task026ID(600 + index), AssignmentPlanRevisionID: task026ID(700 + index),
			ReservationID: task026ID(800 + index), ReservationRevision: 2,
		})
	}
	byeParticipantID := uuid.Nil
	if rosterSize%2 == 1 {
		byeParticipantID = participants[len(participants)-1]
	}
	input := swissusecase.RoundLockProofInput{
		TournamentID: task026ID(1), RosterID: task026ID(2), RoundID: task026ID(10 + roundNumber),
		Preset: domain.TournamentPresetV1, RoundNumber: roundNumber,
		SourceProjectionRevisionID: task026ID(20), PreflightRevisionID: task026ID(21),
		NormalPoolRevisionID: task026ID(22), WaveID: task026ID(25 + roundNumber),
		WaveRevisionID: domain.WaveRevisionID(task026ID(30 + roundNumber)),
		Revisions: swissusecase.RoundLockRevisions{
			Round: 2, SourceProjection: 2, Roster: 2, NormalPool: 2, History: 2, Wave: 2,
		},
		RosterParticipantIDs: participants, Series: series, ByeParticipantID: byeParticipantID,
	}
	proof, err := swissusecase.NewRoundLockProof(input)
	if err != nil {
		t.Fatalf("NewRoundLockProof() error = %v", err)
	}
	return proof, input
}

func cloneRoundLockProofInput(input swissusecase.RoundLockProofInput) swissusecase.RoundLockProofInput {
	cloned := input
	cloned.RosterParticipantIDs = append([]uuid.UUID(nil), input.RosterParticipantIDs...)
	cloned.Series = append([]swissusecase.LockedSeries(nil), input.Series...)
	return cloned
}

func newClock(t *testing.T, now time.Time) *swissmocks.MockClock {
	t.Helper()

	clock := swissmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now)
	return clock
}

func task026ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("26000000-0000-0000-0000-%012d", number))
}

func testTimePointer(value time.Time) *time.Time {
	return &value
}

func cloneTestTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
