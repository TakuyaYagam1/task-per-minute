package swiss_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/google/uuid"
)

func TestPairingRepeatOverride(t *testing.T) {
	t.Parallel()

	participants := swissParticipantIDs(4)
	repeatedPair := swissusecase.Pair{
		FirstParticipantID:  participants[0],
		SecondParticipantID: participants[1],
	}
	round := swissusecase.ManualRound{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000301"),
		Pairings: []swissusecase.Pair{
			repeatedPair,
			{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
		},
	}
	command := swissusecase.RepeatOverrideCommand{
		ID:                   uuid.MustParse("00000000-0000-0000-0000-000000000302"),
		ActorID:              uuid.MustParse("00000000-0000-0000-0000-000000000303"),
		Confirmed:            true,
		Reason:               "operator accepted the documented repeat",
		ConfirmedAt:          time.Date(2026, time.August, 27, 10, 30, 0, 0, time.UTC),
		RosterParticipantIDs: participants,
		Round:                round,
		PreviousMeetings:     []swissusecase.Pair{repeatedPair},
	}

	denied := command
	denied.Confirmed = false
	if _, _, err := swissusecase.ConfirmRepeatOverride(nil, denied); !errors.Is(err, swissusecase.ErrRepeatOverrideNotConfirmed) {
		t.Fatalf("ConfirmRepeatOverride(denied) error = %v, want ErrRepeatOverrideNotConfirmed", err)
	}

	blankReason := command
	blankReason.Reason = "  "
	if _, _, err := swissusecase.ConfirmRepeatOverride(nil, blankReason); !errors.Is(err, swissusecase.ErrInvalidRepeatOverride) {
		t.Fatalf("ConfirmRepeatOverride(blank reason) error = %v, want ErrInvalidRepeatOverride", err)
	}

	record, changed, err := swissusecase.ConfirmRepeatOverride(nil, command)
	if err != nil {
		t.Fatalf("ConfirmRepeatOverride() error = %v", err)
	}
	if !changed {
		t.Fatal("ConfirmRepeatOverride() changed = false, want true")
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

	command.PreviousMeetings[0] = swissusecase.Pair{}
	if record.PreviousMeetings[0] == (swissusecase.Pair{}) {
		t.Fatal("override retained caller-owned previous meeting slice")
	}
	command.PreviousMeetings[0] = repeatedPair

	idempotent, changed, err := swissusecase.ConfirmRepeatOverride(&record, command)
	if err != nil {
		t.Fatalf("ConfirmRepeatOverride(idempotent) error = %v", err)
	}
	if changed {
		t.Fatal("ConfirmRepeatOverride(idempotent) changed = true, want false")
	}
	if !reflect.DeepEqual(idempotent, record) {
		t.Fatalf("idempotent override = %#v, want %#v", idempotent, record)
	}

	conflict := command
	conflict.Reason = "different reason"
	if _, _, err := swissusecase.ConfirmRepeatOverride(&record, conflict); !errors.Is(err, swissusecase.ErrRepeatOverrideConflict) {
		t.Fatalf("ConfirmRepeatOverride(conflict) error = %v, want ErrRepeatOverrideConflict", err)
	}

	if err := swissusecase.ValidateManualPairing(participants, round, command.PreviousMeetings, &record); err != nil {
		t.Fatalf("ValidateManualPairing(confirmed repeat) error = %v", err)
	}
	mismatchedRound := round
	mismatchedRound.ID = uuid.MustParse("00000000-0000-0000-0000-000000000399")
	if err := swissusecase.ValidateManualPairing(participants, mismatchedRound, command.PreviousMeetings, &record); !errors.Is(err, swissusecase.ErrManualPairingOverrideMismatch) {
		t.Fatalf("ValidateManualPairing(mismatched override) error = %v, want ErrManualPairingOverrideMismatch", err)
	}
}
