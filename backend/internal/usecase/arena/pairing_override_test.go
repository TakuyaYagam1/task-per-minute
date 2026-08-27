package arena_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPairingRepeatOverride(t *testing.T) {
	t.Parallel()

	participants := swissParticipantIDs(4)
	repeatedPair := arena.SwissPair{
		FirstParticipantID:  participants[0],
		SecondParticipantID: participants[1],
	}
	round := arena.ManualSwissRound{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000301"),
		Pairings: []arena.SwissPair{
			repeatedPair,
			{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
		},
	}
	command := arena.PairingRepeatOverrideCommand{
		ID:                   uuid.MustParse("00000000-0000-0000-0000-000000000302"),
		ActorID:              uuid.MustParse("00000000-0000-0000-0000-000000000303"),
		Confirmed:            true,
		Reason:               "operator accepted the documented repeat",
		ConfirmedAt:          time.Date(2026, time.August, 27, 10, 30, 0, 0, time.UTC),
		RosterParticipantIDs: participants,
		Round:                round,
		PreviousMeetings:     []arena.SwissPair{repeatedPair},
	}

	denied := command
	denied.Confirmed = false
	if _, _, err := arena.ConfirmPairingRepeatOverride(nil, denied); !errors.Is(err, arena.ErrPairingRepeatOverrideNotConfirmed) {
		t.Fatalf("ConfirmPairingRepeatOverride(denied) error = %v, want ErrPairingRepeatOverrideNotConfirmed", err)
	}

	blankReason := command
	blankReason.Reason = "  "
	if _, _, err := arena.ConfirmPairingRepeatOverride(nil, blankReason); !errors.Is(err, arena.ErrInvalidPairingRepeatOverride) {
		t.Fatalf("ConfirmPairingRepeatOverride(blank reason) error = %v, want ErrInvalidPairingRepeatOverride", err)
	}

	record, changed, err := arena.ConfirmPairingRepeatOverride(nil, command)
	if err != nil {
		t.Fatalf("ConfirmPairingRepeatOverride() error = %v", err)
	}
	if !changed {
		t.Fatal("ConfirmPairingRepeatOverride() changed = false, want true")
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("override Validate() error = %v", err)
	}
	if record.CommandID != command.ID || record.RoundID != round.ID || record.ActorID != command.ActorID {
		t.Fatalf("override identities = command %s round %s actor %s", record.CommandID, record.RoundID, record.ActorID)
	}
	if record.Reason != command.Reason || !record.ConfirmedAt.Equal(command.ConfirmedAt) {
		t.Fatalf("override audit = reason %q at %s", record.Reason, record.ConfirmedAt)
	}
	if !record.AlternativeSearch.Complete || len(record.AlternativeSearch.Pairings) != 2 {
		t.Fatalf("alternative search = %#v, want complete non-repeating matching", record.AlternativeSearch)
	}
	if len(record.PreviousMeetings) != 1 || swissPairKey(record.PreviousMeetings[0]) != swissPairKey(repeatedPair) {
		t.Fatalf("previous meeting evidence = %#v, want %#v", record.PreviousMeetings, repeatedPair)
	}
	if len(record.RepeatedPairings) != 1 || swissPairKey(record.RepeatedPairings[0]) != swissPairKey(repeatedPair) {
		t.Fatalf("repeated pairing evidence = %#v, want %#v", record.RepeatedPairings, repeatedPair)
	}

	command.PreviousMeetings[0] = arena.SwissPair{}
	if record.PreviousMeetings[0] == (arena.SwissPair{}) {
		t.Fatal("override retained caller-owned previous meeting slice")
	}
	command.PreviousMeetings[0] = repeatedPair

	idempotent, changed, err := arena.ConfirmPairingRepeatOverride(&record, command)
	if err != nil {
		t.Fatalf("ConfirmPairingRepeatOverride(idempotent) error = %v", err)
	}
	if changed {
		t.Fatal("ConfirmPairingRepeatOverride(idempotent) changed = true, want false")
	}
	if !reflect.DeepEqual(idempotent, record) {
		t.Fatalf("idempotent override = %#v, want %#v", idempotent, record)
	}

	conflict := command
	conflict.Reason = "different reason"
	if _, _, err := arena.ConfirmPairingRepeatOverride(&record, conflict); !errors.Is(err, arena.ErrPairingRepeatOverrideConflict) {
		t.Fatalf("ConfirmPairingRepeatOverride(conflict) error = %v, want ErrPairingRepeatOverrideConflict", err)
	}

	if err := arena.ValidateManualSwissPairing(participants, round, command.PreviousMeetings, &record); err != nil {
		t.Fatalf("ValidateManualSwissPairing(confirmed repeat) error = %v", err)
	}
	mismatchedRound := round
	mismatchedRound.ID = uuid.MustParse("00000000-0000-0000-0000-000000000399")
	if err := arena.ValidateManualSwissPairing(participants, mismatchedRound, command.PreviousMeetings, &record); !errors.Is(err, arena.ErrManualPairingOverrideMismatch) {
		t.Fatalf("ValidateManualSwissPairing(mismatched override) error = %v, want ErrManualPairingOverrideMismatch", err)
	}
}
