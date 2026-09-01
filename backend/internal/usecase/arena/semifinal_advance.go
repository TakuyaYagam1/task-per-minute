package arena

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
	ScoreRevisionID  domain.ArenaSeriesScoreRevisionID
	ResultRevisionID domain.ArenaOfficialResultRevisionID
}

type SemifinalAdvancement struct {
	tournamentID      uuid.UUID
	bracketRevisionID domain.ArenaDerivedRevisionID
	results           [2]*SemifinalAdvancementResult
}

func AdvanceSemifinalResults(
	current SemifinalAdvancement,
	bracket SemifinalBracket,
	series []domain.ArenaSeries,
) (SemifinalAdvancement, bool, error) {
	if err := validateSemifinalAdvancementBracket(bracket); err != nil {
		return SemifinalAdvancement{}, false, err
	}
	next := cloneSemifinalAdvancement(current)
	if next.tournamentID == uuid.Nil {
		next.tournamentID = bracket.Projection().Revision().TournamentID()
		next.bracketRevisionID = bracket.Projection().Revision().ID()
	} else if err := next.Validate(bracket); err != nil {
		return SemifinalAdvancement{}, false, err
	}

	changed := false
	for _, completed := range series {
		result, err := semifinalAdvancementResult(bracket, completed)
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
	if err := next.Validate(bracket); err != nil {
		return SemifinalAdvancement{}, false, err
	}
	return cloneSemifinalAdvancement(next), changed, nil
}

func (a SemifinalAdvancement) Validate(bracket SemifinalBracket) error {
	if err := validateSemifinalAdvancementBracket(bracket); err != nil {
		return err
	}
	revision := bracket.Projection().Revision()
	if a.tournamentID != revision.TournamentID() || a.bracketRevisionID != revision.ID() {
		return semifinalAdvancementError("advancement belongs to another bracket")
	}
	matches := bracket.Semifinals()
	for index, result := range a.results {
		if result == nil {
			continue
		}
		match := matches[index]
		if result.Position != match.Position || result.SeriesID != match.Series.ID ||
			result.WinnerID == result.LoserID ||
			!semifinalParticipantsMatch(match.Series, result.WinnerID, result.LoserID) ||
			result.ScoreRevisionID.IsZero() || result.ResultRevisionID.IsZero() {
			return semifinalAdvancementError("invalid result for semifinal %d", index+1)
		}
	}
	return nil
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

func semifinalAdvancementResult(
	bracket SemifinalBracket,
	completed domain.ArenaSeries,
) (SemifinalAdvancementResult, error) {
	if err := completed.Validate(); err != nil || completed.State != domain.ArenaSeriesStateCompleted ||
		completed.Format != domain.ArenaSeriesFormatBO1 || completed.WinnerID == nil ||
		completed.CurrentScoreRevisionID == nil || completed.CurrentResultRevisionID == nil {
		return SemifinalAdvancementResult{}, semifinalAdvancementError("semifinal result is not terminal")
	}
	for _, match := range bracket.Semifinals() {
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

func semifinalParticipantsMatch(series domain.ArenaSeries, first, second uuid.UUID) bool {
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
