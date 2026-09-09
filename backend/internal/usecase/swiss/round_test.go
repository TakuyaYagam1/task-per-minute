package swiss_test

import (
	"errors"
	"testing"
	"time"

	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/google/uuid"
)

func TestDeriveRoundStandings(t *testing.T) {
	t.Parallel()

	participants := task027ParticipantIDs(5)
	accepted := 20 * time.Second
	round := swissusecase.Round{
		RoundID: task027ID(101), RoundNumber: 1, RevisionID: task027ID(151),
		Series: []swissusecase.SeriesPointResult{
			task027SeriesResult(
				1, 1, participants[0], participants[1], participants[0],
				swissusecase.SeriesResultPlayed, time.Minute, 2*time.Minute,
			),
			task027SeriesResult(
				2, 1, participants[2], participants[3], participants[2],
				swissusecase.SeriesResultPlayed, 3*time.Minute, 4*time.Minute,
			),
		},
		Bye: &swissusecase.ByePointResult{
			RoundID: task027ID(101), RoundNumber: 1,
			ParticipantID: participants[4], RevisionID: task027ID(401),
		},
	}
	round.Series[0].FirstAcceptedSolveTime = &accepted
	seeds := make([]swissusecase.ParticipantSeed, len(participants))
	for index, participantID := range participants {
		seeds[index] = swissusecase.ParticipantSeed{ParticipantID: participantID, Seed: index + 1}
	}

	ordered, err := swissusecase.DeriveRoundStandings(participants, seeds, []swissusecase.Round{round}, true)
	if err != nil {
		t.Fatalf("DeriveRoundStandings() error = %v", err)
	}
	if len(ordered) != len(participants) || ordered[0].PointsLabel != swissusecase.PointsFinal {
		t.Fatalf("standings = %+v", ordered)
	}
	accepted = 0
	var acceptedSolveTime *time.Duration
	for index := range ordered {
		if ordered[index].ParticipantID == participants[0] {
			acceptedSolveTime = ordered[index].AcceptedSolveTime
			break
		}
	}
	if acceptedSolveTime == nil || *acceptedSolveTime != 20*time.Second {
		t.Fatalf("accepted solve time = %v, want 20s", acceptedSolveTime)
	}

	invalid := round
	invalid.Series = append([]swissusecase.SeriesPointResult(nil), round.Series...)
	invalid.Series[1].FirstParticipantID = participants[0]
	if _, err := swissusecase.DeriveRoundStandings(
		participants, seeds, []swissusecase.Round{invalid}, false,
	); !errors.Is(err, swissusecase.ErrInvalidPointLedger) {
		t.Fatalf("DeriveRoundStandings(duplicate) error = %v, want ErrInvalidPointLedger", err)
	}

	missingRevision := round
	missingRevision.RevisionID = uuid.Nil
	if _, err := swissusecase.DeriveRoundStandings(
		participants, seeds, []swissusecase.Round{missingRevision}, false,
	); !errors.Is(err, swissusecase.ErrInvalidPointLedger) {
		t.Fatalf("DeriveRoundStandings(missing revision) error = %v, want ErrInvalidPointLedger", err)
	}
}
