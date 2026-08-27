package arena_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestManualSwissPairing(t *testing.T) {
	t.Parallel()

	participants := swissParticipantIDs(4)
	round := arena.ManualSwissRound{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000201"),
		Pairings: []arena.SwissPair{
			{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
			{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
		},
	}
	if err := arena.ValidateManualSwissPairing(participants, round, nil, nil); err != nil {
		t.Fatalf("ValidateManualSwissPairing(valid even round) error = %v", err)
	}

	oddParticipants := swissParticipantIDs(5)
	oddRound := arena.ManualSwissRound{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000202"),
		Pairings: []arena.SwissPair{
			{FirstParticipantID: oddParticipants[0], SecondParticipantID: oddParticipants[1]},
			{FirstParticipantID: oddParticipants[2], SecondParticipantID: oddParticipants[3]},
		},
		ByeParticipantID: oddParticipants[4],
	}
	if err := arena.ValidateManualSwissPairing(oddParticipants, oddRound, nil, nil); err != nil {
		t.Fatalf("ValidateManualSwissPairing(valid odd round) error = %v", err)
	}

	tests := []struct {
		name             string
		roster           []uuid.UUID
		round            arena.ManualSwissRound
		previousMeetings []arena.SwissPair
		want             error
	}{
		{
			name:   "missing participant identity",
			roster: participants,
			round: manualRoundWithPairings(round, []arena.SwissPair{
				{FirstParticipantID: participants[0], SecondParticipantID: uuid.Nil},
				{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
			}),
			want: arena.ErrManualPairingMissingParticipant,
		},
		{
			name:   "self pair",
			roster: participants,
			round: manualRoundWithPairings(round, []arena.SwissPair{
				{FirstParticipantID: participants[0], SecondParticipantID: participants[0]},
				{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
			}),
			want: arena.ErrManualPairingSelfPair,
		},
		{
			name:   "foreign participant",
			roster: participants,
			round: manualRoundWithPairings(round, []arena.SwissPair{
				{FirstParticipantID: participants[0], SecondParticipantID: uuid.MustParse("00000000-0000-0000-0000-000000000299")},
				{FirstParticipantID: participants[2], SecondParticipantID: participants[3]},
			}),
			want: arena.ErrManualPairingForeignParticipant,
		},
		{
			name:   "duplicate use",
			roster: participants,
			round: manualRoundWithPairings(round, []arena.SwissPair{
				{FirstParticipantID: participants[0], SecondParticipantID: participants[1]},
				{FirstParticipantID: participants[0], SecondParticipantID: participants[3]},
			}),
			want: arena.ErrManualPairingDuplicateParticipant,
		},
		{
			name:   "incomplete round",
			roster: participants,
			round:  manualRoundWithPairings(round, round.Pairings[:1]),
			want:   arena.ErrManualPairingIncomplete,
		},
		{
			name:   "unexpected even bye",
			roster: participants,
			round: func() arena.ManualSwissRound {
				withBye := round
				withBye.ByeParticipantID = participants[0]
				return withBye
			}(),
			want: arena.ErrManualPairingInvalidBye,
		},
		{
			name:   "missing odd bye",
			roster: oddParticipants,
			round: func() arena.ManualSwissRound {
				withoutBye := oddRound
				withoutBye.ByeParticipantID = uuid.Nil
				return withoutBye
			}(),
			want: arena.ErrManualPairingInvalidBye,
		},
		{
			name:             "repeat without override",
			roster:           participants,
			round:            round,
			previousMeetings: round.Pairings[:1],
			want:             arena.ErrManualPairingRepeatRequiresOverride,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := arena.ValidateManualSwissPairing(tt.roster, tt.round, tt.previousMeetings, nil)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ValidateManualSwissPairing() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func manualRoundWithPairings(round arena.ManualSwissRound, pairings []arena.SwissPair) arena.ManualSwissRound {
	round.Pairings = append([]arena.SwissPair(nil), pairings...)
	return round
}
