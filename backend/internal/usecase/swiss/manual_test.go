package swiss_test

import (
	"errors"
	"testing"

	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/google/uuid"
)

func TestManualSwissPairing(t *testing.T) {
	t.Parallel()

	participants := swissParticipantIDs(4)
	round := swissusecase.ManualRound{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000201"),
		Pairings: []swissusecase.Pair{
			{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
			{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
		},
	}
	if err := swissusecase.ValidateManualPairing(participants, round, nil); err != nil {
		t.Fatalf("ValidateManualPairing(valid even round) error = %v", err)
	}

	oddParticipants := swissParticipantIDs(5)
	oddRound := swissusecase.ManualRound{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000202"),
		Pairings: []swissusecase.Pair{
			{FirstParticipantID: oddParticipants[0], SecondParticipantID: oddParticipants[1]},
			{FirstParticipantID: oddParticipants[2], SecondParticipantID: oddParticipants[3]},
		},
		ByeParticipantID: oddParticipants[4],
	}
	if err := swissusecase.ValidateManualPairing(oddParticipants, oddRound, nil); err != nil {
		t.Fatalf("ValidateManualPairing(valid odd round) error = %v", err)
	}

	tests := []struct {
		name             string
		roster           []uuid.UUID
		round            swissusecase.ManualRound
		previousMeetings []swissusecase.Pair
		want             error
	}{
		{
			name:   "missing participant identity",
			roster: participants,
			round: manualRoundWithPairings(round, []swissusecase.Pair{
				{FirstParticipantID: participants[0], SecondParticipantID: uuid.Nil},
				{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
			}),
			want: swissusecase.ErrManualPairingMissingParticipant,
		},
		{
			name:   "self pair",
			roster: participants,
			round: manualRoundWithPairings(round, []swissusecase.Pair{
				{FirstParticipantID: participants[0], SecondParticipantID: participants[0]},
				{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
			}),
			want: swissusecase.ErrManualPairingSelfPair,
		},
		{
			name:   "foreign participant",
			roster: participants,
			round: manualRoundWithPairings(round, []swissusecase.Pair{
				{FirstParticipantID: participants[0], SecondParticipantID: uuid.MustParse("00000000-0000-0000-0000-000000000299")},
				{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
			}),
			want: swissusecase.ErrManualPairingForeignParticipant,
		},
		{
			name:   "duplicate use",
			roster: participants,
			round: manualRoundWithPairings(round, []swissusecase.Pair{
				{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
				{FirstParticipantID: participants[0], SecondParticipantID: participants[3]},
			}),
			want: swissusecase.ErrManualPairingDuplicateParticipant,
		},
		{
			name:   "incomplete round",
			roster: participants,
			round:  manualRoundWithPairings(round, round.Pairings[:1]),
			want:   swissusecase.ErrManualPairingIncomplete,
		},
		{
			name:   "unexpected even bye",
			roster: participants,
			round: func() swissusecase.ManualRound {
				withBye := round
				withBye.ByeParticipantID = participants[0]
				return withBye
			}(),
			want: swissusecase.ErrManualPairingInvalidBye,
		},
		{
			name:   "missing odd bye",
			roster: oddParticipants,
			round: func() swissusecase.ManualRound {
				withoutBye := oddRound
				withoutBye.ByeParticipantID = uuid.Nil
				return withoutBye
			}(),
			want: swissusecase.ErrManualPairingInvalidBye,
		},
		{
			name:             "repeat is rejected",
			roster:           participants,
			round:            round,
			previousMeetings: round.Pairings[:1],
			want:             swissusecase.ErrManualPairingRepeat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := swissusecase.ValidateManualPairing(tt.roster, tt.round, tt.previousMeetings)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateManualPairing() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func manualRoundWithPairings(round swissusecase.ManualRound, pairings []swissusecase.Pair) swissusecase.ManualRound {
	round.Pairings = append([]swissusecase.Pair(nil), pairings...)
	return round
}
