package swiss

import "github.com/google/uuid"

// Round is the official Swiss result history consumed by standings calculations.
type Round struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	Series      []SeriesPointResult
	Bye         *ByePointResult
}

// DeriveRoundStandings rebuilds canonical standings from official Swiss rounds.
func DeriveRoundStandings(
	participants []uuid.UUID,
	seeds []ParticipantSeed,
	rounds []Round,
	swissComplete bool,
) ([]NormalStanding, error) {
	canonicalParticipants, err := CanonicalLedgerParticipants(participants)
	if err != nil {
		return nil, err
	}
	roster := make(map[uuid.UUID]struct{}, len(canonicalParticipants))
	for _, participantID := range canonicalParticipants {
		roster[participantID] = struct{}{}
	}

	series := make([]SeriesPointResult, 0)
	byes := make([]ByePointResult, 0, len(rounds))
	seenRoundIDs := make(map[uuid.UUID]struct{}, len(rounds))
	seenRevisionIDs := make(map[uuid.UUID]struct{}, len(rounds))
	for index, round := range rounds {
		if err := validateOfficialRound(round, index+1, roster); err != nil {
			return nil, err
		}
		if _, duplicate := seenRoundIDs[round.RoundID]; duplicate {
			return nil, swissPointLedgerError("round identity is duplicated")
		}
		if _, duplicate := seenRevisionIDs[round.RevisionID]; duplicate {
			return nil, swissPointLedgerError("round revision is duplicated")
		}
		seenRoundIDs[round.RoundID] = struct{}{}
		seenRevisionIDs[round.RevisionID] = struct{}{}
		for _, result := range round.Series {
			series = append(series, CloneSeriesPointResult(result))
		}
		if round.Bye != nil {
			byes = append(byes, *round.Bye)
		}
	}
	ledger, err := BuildPointLedger(PointLedgerInput{
		ParticipantIDs: canonicalParticipants,
		Series:         series,
		Byes:           byes,
	})
	if err != nil {
		return nil, err
	}
	return OrderNormalStandings(NormalOrderingInput{
		Ledger: ledger, Seeds: seeds, SwissComplete: swissComplete,
	})
}

func validateOfficialRound(round Round, wantNumber int, roster map[uuid.UUID]struct{}) error {
	if round.RoundID == uuid.Nil || round.RevisionID == uuid.Nil || round.RoundNumber != wantNumber ||
		len(round.Series) != len(roster)/2 {
		return swissPointLedgerError("round identity or Series coverage is invalid")
	}
	if (len(roster)%2 == 0 && round.Bye != nil) || (len(roster)%2 == 1 && round.Bye == nil) {
		return swissPointLedgerError("round bye coverage is invalid")
	}

	used, err := validateOfficialRoundSeries(round, roster)
	if err != nil {
		return err
	}
	if err := validateOfficialRoundBye(round, roster, used); err != nil {
		return err
	}
	if len(used) != len(roster) {
		return swissPointLedgerError("round does not cover the roster")
	}
	return nil
}

func validateOfficialRoundSeries(
	round Round,
	roster map[uuid.UUID]struct{},
) (map[uuid.UUID]struct{}, error) {
	used := make(map[uuid.UUID]struct{}, len(roster))
	for _, result := range round.Series {
		if result.RoundID != round.RoundID || result.RoundNumber != round.RoundNumber {
			return nil, swissPointLedgerError("Series result is bound to another round")
		}
		for _, participantID := range []uuid.UUID{result.FirstParticipantID, result.SecondParticipantID} {
			if _, exists := roster[participantID]; !exists {
				return nil, swissPointLedgerError("round contains a foreign participant")
			}
			if _, duplicate := used[participantID]; duplicate {
				return nil, swissPointLedgerError("round participant is duplicated")
			}
			used[participantID] = struct{}{}
		}
	}
	return used, nil
}

func validateOfficialRoundBye(
	round Round,
	roster map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
) error {
	if round.Bye == nil {
		return nil
	}
	if round.Bye.RoundID != round.RoundID || round.Bye.RoundNumber != round.RoundNumber {
		return swissPointLedgerError("bye result is bound to another round")
	}
	if _, exists := roster[round.Bye.ParticipantID]; !exists {
		return swissPointLedgerError("bye contains a foreign participant")
	}
	if _, duplicate := used[round.Bye.ParticipantID]; duplicate {
		return swissPointLedgerError("bye participant also appears in a Series")
	}
	used[round.Bye.ParticipantID] = struct{}{}
	return nil
}
