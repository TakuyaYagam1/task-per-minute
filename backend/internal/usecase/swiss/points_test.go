package swiss_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestSwissPointLedger(t *testing.T) {
	t.Parallel()

	participants := task027ParticipantIDs(5)
	accepted := 35 * time.Second
	input := swissusecase.PointLedgerInput{
		ParticipantIDs: participants,
		Series: []swissusecase.SeriesPointResult{
			{
				RoundID: task027ID(101), RoundNumber: 1, SeriesID: task027ID(201),
				ResultRevisionID:   domain.OfficialResultRevisionID(task027ID(301)),
				FirstParticipantID: participants[0], SecondParticipantID: participants[1],
				WinnerID: task027IDPointer(participants[0]), Label: swissusecase.SeriesResultPlayed,
				FirstEffectiveTime: 55 * time.Second, SecondEffectiveTime: 3 * time.Minute,
				FirstAcceptedSolveTime: &accepted,
			},
			{
				RoundID: task027ID(101), RoundNumber: 1, SeriesID: task027ID(202),
				ResultRevisionID:   domain.OfficialResultRevisionID(task027ID(302)),
				FirstParticipantID: participants[2], SecondParticipantID: participants[3],
				WinnerID: task027IDPointer(participants[3]), Label: swissusecase.SeriesResultNoShow,
				FirstEffectiveTime: 3 * time.Minute, SecondEffectiveTime: 0,
			},
		},
		Byes: []swissusecase.ByePointResult{{
			RoundID: task027ID(101), RoundNumber: 1, ParticipantID: participants[4],
			RevisionID: task027ID(401),
		}},
	}

	ledger, err := swissusecase.BuildPointLedger(input)
	if err != nil {
		t.Fatalf("BuildPointLedger() error = %v", err)
	}
	if len(ledger.Entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(ledger.Entries))
	}
	if len(ledger.Totals) != len(participants) {
		t.Fatalf("total count = %d, want %d", len(ledger.Totals), len(participants))
	}
	assertSwissPointTotals(t, ledger, map[uuid.UUID]int{
		participants[0]: 1,
		participants[1]: 0,
		participants[2]: 0,
		participants[3]: 1,
		participants[4]: 1,
	})
	if ledger.Totals[0].AcceptedSolveTime == nil || *ledger.Totals[0].AcceptedSolveTime != accepted {
		t.Fatalf("accepted solve time = %v, want %s", ledger.Totals[0].AcceptedSolveTime, accepted)
	}

	recomputed, err := swissusecase.BuildPointLedger(input)
	if err != nil {
		t.Fatalf("BuildPointLedger(retry) error = %v", err)
	}
	if !reflect.DeepEqual(recomputed, ledger) {
		t.Fatalf("recomputed ledger differs:\n got: %#v\nwant: %#v", recomputed, ledger)
	}

	corrected := input
	corrected.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	corrected.Series = append([]swissusecase.SeriesPointResult(nil), input.Series...)
	corrected.Byes = append([]swissusecase.ByePointResult(nil), input.Byes...)
	corrected.Series[0].ResultRevisionID = domain.OfficialResultRevisionID(task027ID(303))
	corrected.Series[0].WinnerID = task027IDPointer(participants[1])
	correctedLedger, err := swissusecase.BuildPointLedger(corrected)
	if err != nil {
		t.Fatalf("BuildPointLedger(corrected) error = %v", err)
	}
	if len(correctedLedger.Entries) != len(ledger.Entries) {
		t.Fatalf("corrected entry count = %d, want %d", len(correctedLedger.Entries), len(ledger.Entries))
	}
	assertSwissPointTotals(t, correctedLedger, map[uuid.UUID]int{
		participants[0]: 0,
		participants[1]: 1,
		participants[2]: 0,
		participants[3]: 1,
		participants[4]: 1,
	})

	duplicate := input
	duplicate.Series = append(append([]swissusecase.SeriesPointResult(nil), input.Series...), input.Series[0])
	if _, err := swissusecase.BuildPointLedger(duplicate); !errors.Is(err, swissusecase.ErrInvalidPointLedger) {
		t.Fatalf("BuildPointLedger(duplicate) error = %v, want ErrInvalidPointLedger", err)
	}
}

func assertSwissPointTotals(t *testing.T, ledger swissusecase.PointLedger, want map[uuid.UUID]int) {
	t.Helper()

	seen := make(map[uuid.UUID]struct{}, len(ledger.Totals))
	for _, total := range ledger.Totals {
		if _, duplicate := seen[total.ParticipantID]; duplicate {
			t.Fatalf("participant %s has duplicate total", total.ParticipantID)
		}
		seen[total.ParticipantID] = struct{}{}
		if total.Points != want[total.ParticipantID] {
			t.Errorf("participant %s points = %d, want %d", total.ParticipantID, total.Points, want[total.ParticipantID])
		}
	}
}

func task027ParticipantIDs(count int) []uuid.UUID {
	participants := make([]uuid.UUID, count)
	for index := range participants {
		participants[index] = task027ID(index + 1)
	}
	return participants
}

func task027ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("27000000-0000-0000-0000-%012d", number))
}

func task027IDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}
