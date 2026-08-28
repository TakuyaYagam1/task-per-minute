package arena_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestAutomaticSwissPairingEvidence(t *testing.T) {
	t.Parallel()

	participants := swissParticipantIDs(6)
	previousMeetings := []arena.SwissPair{
		{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
		{FirstParticipantID: participants[0], SecondParticipantID: participants[2]},
		{FirstParticipantID: participants[3], SecondParticipantID: participants[4]},
	}
	decidedAt := time.Date(2026, time.August, 27, 10, 15, 0, 0, time.UTC)
	roundID := uuid.MustParse("00000000-0000-0000-0000-000000000101")

	record, err := arena.GenerateAutomaticSwissPairing(
		uuid.MustParse("00000000-0000-0000-0000-000000000102"),
		roundID,
		participants,
		previousMeetings,
		decidedAt,
	)
	if err != nil {
		t.Fatalf("GenerateAutomaticSwissPairing() error = %v", err)
	}
	if record.Evidence.Purpose != domain.ArenaDecisionPurposePairing {
		t.Errorf("evidence purpose = %q, want %q", record.Evidence.Purpose, domain.ArenaDecisionPurposePairing)
	}
	if record.Evidence.OwnerID != roundID {
		t.Errorf("evidence owner = %s, want %s", record.Evidence.OwnerID, roundID)
	}
	if !record.Evidence.DecidedAt.Equal(decidedAt) {
		t.Errorf("evidence timestamp = %s, want %s", record.Evidence.DecidedAt, decidedAt)
	}
	if len(record.Evidence.NormalizedInputs) != len(participants)+len(previousMeetings) {
		t.Fatalf("normalized input count = %d, want %d", len(record.Evidence.NormalizedInputs), len(participants)+len(previousMeetings))
	}
	if err := record.Evidence.Validate(); err != nil {
		t.Fatalf("evidence Validate() error = %v", err)
	}

	assertAutomaticPairingCoverage(t, record.Pairings, participants, previousMeetings)
	replayed, err := arena.ReplayAutomaticSwissPairing(record)
	if err != nil {
		t.Fatalf("ReplayAutomaticSwissPairing() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, record.Pairings) {
		t.Fatalf("replayed pairings = %#v, want %#v", replayed, record.Pairings)
	}

	tampered := record
	tampered.Pairings = append([]arena.SwissPair(nil), record.Pairings...)
	tampered.Pairings[0].SecondParticipantID = tampered.Pairings[0].FirstParticipantID
	if _, err := arena.ReplayAutomaticSwissPairing(tampered); !errors.Is(err, domain.ErrArenaDecisionReplayMismatch) {
		t.Fatalf("ReplayAutomaticSwissPairing(tampered) error = %v, want ErrArenaDecisionReplayMismatch", err)
	}
}

func assertAutomaticPairingCoverage(
	t *testing.T,
	pairings []arena.SwissPair,
	participants []uuid.UUID,
	previousMeetings []arena.SwissPair,
) {
	t.Helper()

	wantParticipants := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		wantParticipants[participantID] = struct{}{}
	}
	previous := make(map[string]struct{}, len(previousMeetings))
	for _, meeting := range previousMeetings {
		previous[swissPairKey(meeting)] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(participants))
	for _, pairing := range pairings {
		if _, repeated := previous[swissPairKey(pairing)]; repeated {
			t.Fatalf("automatic pairing repeated previous meeting %s", swissPairKey(pairing))
		}
		for _, participantID := range []uuid.UUID{pairing.FirstParticipantID, pairing.SecondParticipantID} {
			if _, eligible := wantParticipants[participantID]; !eligible {
				t.Fatalf("automatic pairing contains foreign participant %s", participantID)
			}
			if _, duplicate := seen[participantID]; duplicate {
				t.Fatalf("automatic pairing uses participant %s more than once", participantID)
			}
			seen[participantID] = struct{}{}
		}
	}
	if len(seen) != len(participants) {
		t.Fatalf("automatic pairing covered %d participants, want %d", len(seen), len(participants))
	}
}
