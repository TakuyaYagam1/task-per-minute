package swiss_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestSwissByePolicy(t *testing.T) {
	t.Parallel()

	t.Run("supports every odd roster and rotates prior recipients", func(t *testing.T) {
		t.Parallel()

		for rosterSize := 5; rosterSize <= 15; rosterSize += 2 {
			t.Run(time.Duration(rosterSize).String(), func(t *testing.T) {
				t.Parallel()

				candidates := swissByeCandidates(rosterSize)
				seen := make(map[uuid.UUID]struct{})
				for round := 1; round <= 4 && round <= rosterSize; round++ {
					selection := selectSwissBye(t, candidates, round)
					if selection.PointsAwarded != swissusecase.PairingByePoints {
						t.Fatalf("round %d points = %d, want %d", round, selection.PointsAwarded, swissusecase.PairingByePoints)
					}
					if _, duplicate := seen[selection.ParticipantID]; duplicate {
						t.Fatalf("round %d repeated bye for %s", round, selection.ParticipantID)
					}
					seen[selection.ParticipantID] = struct{}{}
					markSwissByeReceived(candidates, selection.ParticipantID)
				}
			})
		}
	})

	t.Run("uses ranking metrics before recorded randomness", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		candidates[0].ReceivedBye = true
		candidates[0].Points = 0
		candidates[1].Points = 1
		candidates[2].Points = 2
		candidates[3].Points = 1
		candidates[4].Points = 1
		candidates[1].ProvisionalBuchholz = 3
		candidates[3].ProvisionalBuchholz = 2
		candidates[4].ProvisionalBuchholz = 2
		candidates[3].HeadToHeadApplicable = true
		candidates[4].HeadToHeadApplicable = true
		candidates[3].HeadToHeadPoints = 1
		candidates[4].HeadToHeadPoints = 0
		candidates[4].EffectiveTime = 2 * time.Minute

		selection := selectSwissBye(t, candidates, 1)
		if selection.ParticipantID != candidates[4].ParticipantID {
			t.Fatalf("participant = %s, want lowest-ranked %s", selection.ParticipantID, candidates[4].ParticipantID)
		}
	})

	t.Run("uses higher effective time when head-to-head is not applicable", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		for i := range candidates {
			candidates[i].Points = 2
			candidates[i].ProvisionalBuchholz = 4
			candidates[i].EffectiveTime = time.Duration(i+1) * time.Minute
		}
		selection := selectSwissBye(t, candidates, 1)
		if selection.ParticipantID != candidates[4].ParticipantID {
			t.Fatalf("participant = %s, want slowest %s", selection.ParticipantID, candidates[4].ParticipantID)
		}
	})

	t.Run("records and replays exact tie randomness", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		selection := selectSwissBye(t, candidates, 1)
		if err := selection.Evidence.Validate(); err != nil {
			t.Fatalf("evidence Validate() error = %v", err)
		}
		if selection.Evidence.Purpose != domain.DecisionPurposePairing {
			t.Fatalf("purpose = %q, want %q", selection.Evidence.Purpose, domain.DecisionPurposePairing)
		}
		replayed, err := swissusecase.ReplayBye(selection)
		if err != nil {
			t.Fatalf("ReplayBye() error = %v", err)
		}
		if replayed.ParticipantID != selection.ParticipantID {
			t.Fatalf("replayed participant = %s, want %s", replayed.ParticipantID, selection.ParticipantID)
		}

		tampered := selection
		tampered.ParticipantID = candidates[(indexSwissByeCandidate(candidates, selection.ParticipantID)+1)%len(candidates)].ParticipantID
		if _, err := swissusecase.ReplayBye(tampered); !errors.Is(err, domain.ErrDecisionReplayMismatch) {
			t.Fatalf("ReplayBye(tampered) error = %v, want ErrTournamentDecisionReplayMismatch", err)
		}
	})

	t.Run("records and replays an eligible manual bye", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		for index := range candidates {
			candidates[index].Points = index + 1
		}
		requested := candidates[0].ParticipantID
		selection, err := swissusecase.SelectManualBye(
			uuid.New(), uuid.New(), candidates, requested,
			time.Date(2026, time.August, 27, 12, 15, 0, 0, time.UTC),
		)
		if err != nil {
			t.Fatalf("SelectManualBye() error = %v", err)
		}
		if selection.ParticipantID != requested || len(selection.Evidence.NormalizedInputs) != len(candidates) {
			t.Fatalf("selection = %#v, want requested participant and candidate evidence", selection)
		}
		if err := selection.Evidence.Validate(); err != nil {
			t.Fatalf("manual evidence Validate() error = %v", err)
		}
		replayed, err := swissusecase.ReplayBye(selection)
		if err != nil {
			t.Fatalf("ReplayBye(manual) error = %v", err)
		}
		if replayed.ParticipantID != requested {
			t.Fatalf("replayed participant = %s, want %s", replayed.ParticipantID, requested)
		}
	})

	t.Run("rejects a manual non-contender", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		for index := range candidates {
			candidates[index].Points = index + 1
		}
		_, err := swissusecase.SelectManualBye(
			uuid.New(), uuid.New(), candidates, candidates[1].ParticipantID,
			time.Date(2026, time.August, 27, 12, 16, 0, 0, time.UTC),
		)
		if !errors.Is(err, swissusecase.ErrManualByeNotEligible) {
			t.Fatalf("SelectManualBye(non-contender) error = %v, want ErrManualByeNotEligible", err)
		}
	})

	t.Run("rejects a manual participant with a received bye", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		candidates[0].ReceivedBye = true
		_, err := swissusecase.SelectManualBye(
			uuid.New(), uuid.New(), candidates, candidates[0].ParticipantID,
			time.Date(2026, time.August, 27, 12, 17, 0, 0, time.UTC),
		)
		if !errors.Is(err, swissusecase.ErrManualByeNotEligible) {
			t.Fatalf("SelectManualBye(received bye) error = %v, want ErrManualByeNotEligible", err)
		}
	})

	t.Run("rejects tampered manual evidence", func(t *testing.T) {
		t.Parallel()

		candidates := swissByeCandidates(5)
		for index := range candidates {
			candidates[index].Points = index + 1
		}
		selection, err := swissusecase.SelectManualBye(
			uuid.New(), uuid.New(), candidates, candidates[0].ParticipantID,
			time.Date(2026, time.August, 27, 12, 18, 0, 0, time.UTC),
		)
		if err != nil {
			t.Fatalf("SelectManualBye() error = %v", err)
		}
		tampered := selection
		tampered.Evidence.Result[0], tampered.Evidence.Result[1] = tampered.Evidence.Result[1], tampered.Evidence.Result[0]
		if _, err := swissusecase.ReplayBye(tampered); !errors.Is(err, domain.ErrDecisionReplayMismatch) {
			t.Fatalf("ReplayBye(tampered manual) error = %v, want ErrDecisionReplayMismatch", err)
		}
	})

	t.Run("rejects invalid rosters", func(t *testing.T) {
		t.Parallel()

		allReceived := swissByeCandidates(5)
		for i := range allReceived {
			allReceived[i].ReceivedBye = true
		}
		duplicate := swissByeCandidates(5)
		duplicate[1].ParticipantID = duplicate[0].ParticipantID
		negative := swissByeCandidates(5)
		negative[0].Points = -1
		for name, candidates := range map[string][]swissusecase.ByeCandidate{
			"even":         swissByeCandidates(6),
			"all received": allReceived,
			"duplicate":    duplicate,
			"negative":     negative,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				_, err := swissusecase.SelectBye(uuid.New(), uuid.New(), candidates, time.Now().UTC())
				if !errors.Is(err, swissusecase.ErrInvalidBye) {
					t.Fatalf("SelectBye() error = %v, want ErrInvalidBye", err)
				}
			})
		}
	})
}

func swissByeCandidates(count int) []swissusecase.ByeCandidate {
	candidates := make([]swissusecase.ByeCandidate, count)
	participantIDs := swissParticipantIDs(count)
	for i := range candidates {
		candidates[i] = swissusecase.ByeCandidate{
			ParticipantID: participantIDs[i],
			Points:        1,
			EffectiveTime: time.Minute,
		}
	}
	return candidates
}

func selectSwissBye(t *testing.T, candidates []swissusecase.ByeCandidate, round int) swissusecase.ByeSelection {
	t.Helper()
	selection, err := swissusecase.SelectBye(
		uuid.New(),
		uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0001-%012d", round)),
		candidates,
		time.Date(2026, time.August, 27, 12, round, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("SelectBye() error = %v", err)
	}
	return selection
}

func markSwissByeReceived(candidates []swissusecase.ByeCandidate, participantID uuid.UUID) {
	for i := range candidates {
		if candidates[i].ParticipantID == participantID {
			candidates[i].ReceivedBye = true
			return
		}
	}
}

func indexSwissByeCandidate(candidates []swissusecase.ByeCandidate, participantID uuid.UUID) int {
	for i, candidate := range candidates {
		if candidate.ParticipantID == participantID {
			return i
		}
	}
	return -1
}
