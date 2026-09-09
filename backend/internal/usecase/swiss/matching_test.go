package swiss_test

import (
	"errors"
	"fmt"
	"sort"
	"testing"

	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/google/uuid"
)

func TestSwissMatching(t *testing.T) {
	t.Parallel()

	t.Run("covers every supported roster size without repeats", func(t *testing.T) {
		t.Parallel()

		for rosterSize := 4; rosterSize <= 16; rosterSize++ {
			t.Run(fmt.Sprintf("roster_%d", rosterSize), func(t *testing.T) {
				t.Parallel()

				eligibleCount := rosterSize
				if eligibleCount%2 != 0 {
					eligibleCount--
				}
				participants := swissParticipantIDs(eligibleCount)
				allowed := make([]swissusecase.Pair, 0, eligibleCount/2)
				for i := 0; i < eligibleCount; i += 2 {
					allowed = append(allowed, swissusecase.Pair{
						FirstParticipantID:  participants[i],
						SecondParticipantID: participants[i+1],
					})
				}

				matching, err := swissusecase.FindMatching(participants, swissHistoryExcept(participants, allowed))
				if err != nil {
					t.Fatalf("FindMatching(roster %d) error = %v", rosterSize, err)
				}
				assertSwissPairs(t, matching, allowed)
			})
		}
	})

	t.Run("backtracks past a locally valid dead end", func(t *testing.T) {
		t.Parallel()

		participants := swissParticipantIDs(6)
		allowed := []swissusecase.Pair{
			{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
			{FirstParticipantID: participants[0], SecondParticipantID: participants[2]},
			{FirstParticipantID: participants[1], SecondParticipantID: participants[3]},
			{FirstParticipantID: participants[4], SecondParticipantID: participants[5]},
		}
		want := []swissusecase.Pair{
			{FirstParticipantID: participants[0], SecondParticipantID: participants[2]},
			{FirstParticipantID: participants[1], SecondParticipantID: participants[3]},
			{FirstParticipantID: participants[4], SecondParticipantID: participants[5]},
		}

		matching, err := swissusecase.FindMatching(participants, swissHistoryExcept(participants, allowed))
		if err != nil {
			t.Fatalf("FindMatching() error = %v", err)
		}
		assertSwissPairs(t, matching, want)
	})

	t.Run("reports impossible without repeating an opponent", func(t *testing.T) {
		t.Parallel()

		participants := swissParticipantIDs(4)
		history := []swissusecase.Pair{
			{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
			{FirstParticipantID: participants[0], SecondParticipantID: participants[2]},
			{FirstParticipantID: participants[0], SecondParticipantID: participants[3]},
		}

		matching, err := swissusecase.FindMatching(participants, history)
		if !errors.Is(err, swissusecase.ErrMatchingImpossible) {
			t.Fatalf("FindMatching() error = %v, want ErrMatchingImpossible", err)
		}
		if len(matching) != 0 {
			t.Fatalf("FindMatching() returned %d pairs on failure", len(matching))
		}
	})

	t.Run("rejects duplicate eligible participant", func(t *testing.T) {
		t.Parallel()

		participants := swissParticipantIDs(4)
		participants[3] = participants[0]

		if _, err := swissusecase.FindMatching(participants, nil); !errors.Is(err, swissusecase.ErrInvalidMatching) {
			t.Fatalf("FindMatching() error = %v, want ErrInvalidMatching", err)
		}
	})
}

func swissParticipantIDs(count int) []uuid.UUID {
	participants := make([]uuid.UUID, count)
	for i := range participants {
		participants[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1))
	}
	return participants
}

func swissHistoryExcept(participants []uuid.UUID, allowed []swissusecase.Pair) []swissusecase.Pair {
	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, pair := range allowed {
		allowedKeys[swissPairKey(pair)] = struct{}{}
	}

	history := make([]swissusecase.Pair, 0)
	for i := 0; i < len(participants); i++ {
		for j := i + 1; j < len(participants); j++ {
			pair := swissusecase.Pair{
				FirstParticipantID:  participants[i],
				SecondParticipantID: participants[j],
			}
			if _, ok := allowedKeys[swissPairKey(pair)]; !ok {
				history = append(history, pair)
			}
		}
	}
	return history
}

func assertSwissPairs(t *testing.T, got, want []swissusecase.Pair) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("matching has %d pairs, want %d", len(got), len(want))
	}
	wantKeys := make(map[string]struct{}, len(want))
	for _, pair := range want {
		wantKeys[swissPairKey(pair)] = struct{}{}
	}
	for _, pair := range got {
		key := swissPairKey(pair)
		if _, ok := wantKeys[key]; !ok {
			t.Fatalf("matching contains unexpected pair %s", key)
		}
		delete(wantKeys, key)
	}
	if len(wantKeys) != 0 {
		t.Fatalf("matching omitted %d expected pairs", len(wantKeys))
	}
}

func swissPairKey(pair swissusecase.Pair) string {
	ids := []string{pair.FirstParticipantID.String(), pair.SecondParticipantID.String()}
	sort.Strings(ids)
	return ids[0] + "/" + ids[1]
}
