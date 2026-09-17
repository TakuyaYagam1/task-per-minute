package semifinal

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidSemifinalAdvancement = errors.New("invalid semifinal advancement")

type SemifinalAdvancementResult struct {
	Position         int
	SeriesID         uuid.UUID
	WinnerID         uuid.UUID
	LoserID          uuid.UUID
	ScoreRevisionID  domain.SeriesScoreRevisionID
	ResultRevisionID domain.OfficialResultRevisionID
}

// SemifinalAdvancementAuthority is the persisted, locked part of a semifinal
// bracket needed after the original projection plan has been materialized.
// It deliberately carries only the immutable bracket identity and topology;
// adapters must still provide current terminal series heads separately.
type SemifinalAdvancementAuthority struct {
	TournamentID      uuid.UUID
	BracketRevisionID domain.DerivedRevisionID
	Semifinals        []SemifinalMatch
}

type SemifinalAdvancement struct {
	tournamentID      uuid.UUID
	bracketRevisionID domain.DerivedRevisionID
	results           [2]*SemifinalAdvancementResult
}

func AdvanceSemifinalResults(
	current SemifinalAdvancement,
	bracket SemifinalBracket,
	series []domain.Series,
) (SemifinalAdvancement, bool, error) {
	if err := validateSemifinalAdvancementBracket(bracket); err != nil {
		return SemifinalAdvancement{}, false, err
	}
	return AdvanceSemifinalEvidence(current, semifinalAdvancementAuthority(bracket), series)
}

// AdvanceSemifinalEvidence advances a locked, materialized semifinal bracket
// from exact current series evidence. It is the persistence-facing counterpart
// of AdvanceSemifinalResults for callers that cannot reconstruct a private
// SemifinalBracket value from normalized projection rows.
func AdvanceSemifinalEvidence(
	current SemifinalAdvancement,
	authority SemifinalAdvancementAuthority,
	series []domain.Series,
) (SemifinalAdvancement, bool, error) {
	if err := authority.Validate(); err != nil {
		return SemifinalAdvancement{}, false, err
	}
	next := cloneSemifinalAdvancement(current)
	if next.tournamentID == uuid.Nil {
		next.tournamentID = authority.TournamentID
		next.bracketRevisionID = authority.BracketRevisionID
	} else if err := next.validateAuthority(authority); err != nil {
		return SemifinalAdvancement{}, false, err
	}

	changed := false
	for _, completed := range series {
		result, err := semifinalAdvancementResultForAuthority(authority, completed)
		if err != nil {
			return SemifinalAdvancement{}, false, err
		}
		index := result.Position - 1
		if next.results[index] != nil {
			if *next.results[index] != result {
				return SemifinalAdvancement{}, false, semifinalAdvancementError("conflicting result for semifinal %d", result.Position)
			}
			continue
		}
		resultCopy := result
		next.results[index] = &resultCopy
		changed = true
	}
	if err := next.validateAuthority(authority); err != nil {
		return SemifinalAdvancement{}, false, err
	}
	return cloneSemifinalAdvancement(next), changed, nil
}

// RehydrateSemifinalAdvancement reconstructs the immutable advancement state
// from the locked bracket authority and its persisted terminal results.
func RehydrateSemifinalAdvancement(
	authority SemifinalAdvancementAuthority,
	results []SemifinalAdvancementResult,
) (SemifinalAdvancement, error) {
	if err := authority.Validate(); err != nil || len(results) != 2 {
		return SemifinalAdvancement{}, semifinalAdvancementError("invalid rehydration authority")
	}
	advancement := SemifinalAdvancement{
		tournamentID: authority.TournamentID, bracketRevisionID: authority.BracketRevisionID,
	}
	for _, result := range results {
		if result.Position < 1 || result.Position > len(advancement.results) ||
			advancement.results[result.Position-1] != nil {
			return SemifinalAdvancement{}, semifinalAdvancementError("ambiguous persisted result")
		}
		clone := result
		advancement.results[result.Position-1] = &clone
	}
	if err := advancement.validateAuthority(authority); err != nil || !advancement.Complete() {
		return SemifinalAdvancement{}, semifinalAdvancementError("invalid persisted result")
	}
	return cloneSemifinalAdvancement(advancement), nil
}

func (a SemifinalAdvancement) Validate(bracket SemifinalBracket) error {
	if err := validateSemifinalAdvancementBracket(bracket); err != nil {
		return err
	}
	return a.validateAuthority(semifinalAdvancementAuthority(bracket))
}

func (a SemifinalAdvancement) validateAuthority(authority SemifinalAdvancementAuthority) error {
	if err := authority.Validate(); err != nil {
		return err
	}
	if a.tournamentID != authority.TournamentID || a.bracketRevisionID != authority.BracketRevisionID {
		return semifinalAdvancementError("advancement belongs to another bracket")
	}
	for index, result := range a.results {
		if result == nil {
			continue
		}
		match := authority.Semifinals[index]
		if result.Position != match.Position || result.SeriesID != match.Series.ID ||
			result.WinnerID == result.LoserID ||
			!semifinalParticipantsMatch(match.Series, result.WinnerID, result.LoserID) ||
			result.ScoreRevisionID.IsZero() || result.ResultRevisionID.IsZero() {
			return semifinalAdvancementError("invalid result for semifinal %d", index+1)
		}
	}
	return nil
}

func (a SemifinalAdvancement) Results() []SemifinalAdvancementResult {
	results := make([]SemifinalAdvancementResult, 0, len(a.results))
	for _, result := range a.results {
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func (a SemifinalAdvancement) TournamentID() uuid.UUID {
	return a.tournamentID
}

func (a SemifinalAdvancement) BracketRevisionID() domain.DerivedRevisionID {
	return a.bracketRevisionID
}

func (a SemifinalAdvancement) Complete() bool {
	return a.results[0] != nil && a.results[1] != nil
}

func (a SemifinalAdvancement) FinalParticipants() []uuid.UUID {
	participants := make([]uuid.UUID, 0, len(a.results))
	for _, result := range a.results {
		if result != nil {
			participants = append(participants, result.WinnerID)
		}
	}
	return participants
}

func (a SemifinalAdvancement) EliminatedParticipants() []uuid.UUID {
	participants := make([]uuid.UUID, 0, len(a.results))
	for _, result := range a.results {
		if result != nil {
			participants = append(participants, result.LoserID)
		}
	}
	return participants
}

func (a SemifinalAdvancement) HasThirdPlace() bool {
	return false
}

func validateSemifinalAdvancementBracket(bracket SemifinalBracket) error {
	if err := bracket.Validate(); err != nil || !bracket.Locked() || bracket.HasLowerBracket() {
		return semifinalAdvancementError("invalid locked semifinal bracket")
	}
	matches := bracket.Semifinals()
	if len(matches) != 2 || matches[0].Position != 1 || matches[1].Position != 2 {
		return semifinalAdvancementError("bracket must contain exactly two semifinals")
	}
	return nil
}

func semifinalAdvancementAuthority(bracket SemifinalBracket) SemifinalAdvancementAuthority {
	revision := bracket.Projection().Revision()
	return SemifinalAdvancementAuthority{
		TournamentID:      revision.TournamentID(),
		BracketRevisionID: revision.ID(),
		Semifinals:        bracket.Semifinals(),
	}
}

func (a SemifinalAdvancementAuthority) Validate() error {
	if a.TournamentID == uuid.Nil || a.BracketRevisionID.IsZero() ||
		len(a.Semifinals) != 2 {
		return semifinalAdvancementError("invalid materialized semifinal authority")
	}
	for index, match := range a.Semifinals {
		if match.Position != index+1 || match.WinnerPath != SemifinalWinnerToFinal ||
			match.LoserPath != SemifinalLoserEliminated || match.Series.TournamentID != a.TournamentID ||
			match.Series.Format != domain.SeriesFormatBO1 ||
			match.Series.State != domain.SeriesStateLocked || match.Series.Validate() != nil {
			return semifinalAdvancementError("invalid materialized semifinal authority")
		}
	}
	return nil
}

func semifinalAdvancementResultForAuthority(
	authority SemifinalAdvancementAuthority,
	completed domain.Series,
) (SemifinalAdvancementResult, error) {
	if err := completed.Validate(); err != nil || completed.State != domain.SeriesStateCompleted ||
		completed.Format != domain.SeriesFormatBO1 || completed.WinnerID == nil ||
		completed.CurrentScoreRevisionID == nil || completed.CurrentResultRevisionID == nil {
		return SemifinalAdvancementResult{}, semifinalAdvancementError("semifinal result is not terminal")
	}
	for _, match := range authority.Semifinals {
		planned := match.Series
		if completed.ID != planned.ID {
			continue
		}
		if completed.TournamentID != planned.TournamentID ||
			completed.FirstParticipantID != planned.FirstParticipantID ||
			completed.SecondParticipantID != planned.SecondParticipantID {
			return SemifinalAdvancementResult{}, semifinalAdvancementError("semifinal topology changed")
		}
		loserID := completed.FirstParticipantID
		if *completed.WinnerID == completed.FirstParticipantID {
			loserID = completed.SecondParticipantID
		}
		return SemifinalAdvancementResult{
			Position: match.Position, SeriesID: completed.ID,
			WinnerID: *completed.WinnerID, LoserID: loserID,
			ScoreRevisionID:  *completed.CurrentScoreRevisionID,
			ResultRevisionID: *completed.CurrentResultRevisionID,
		}, nil
	}
	return SemifinalAdvancementResult{}, semifinalAdvancementError("Series is not a current semifinal")
}

func semifinalParticipantsMatch(series domain.Series, first, second uuid.UUID) bool {
	return first != uuid.Nil && second != uuid.Nil &&
		(first == series.FirstParticipantID && second == series.SecondParticipantID ||
			first == series.SecondParticipantID && second == series.FirstParticipantID)
}

func cloneSemifinalAdvancement(value SemifinalAdvancement) SemifinalAdvancement {
	clone := value
	for index, result := range value.results {
		if result != nil {
			resultCopy := *result
			clone.results[index] = &resultCopy
		}
	}
	return clone
}

func semifinalAdvancementError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSemifinalAdvancement, fmt.Sprintf(format, arguments...))
}
