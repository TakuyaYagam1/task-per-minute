package result

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateOfficialGameOutcome(outcome OfficialResultOutcome) error {
	if !outcome.GameState.IsTerminal() || !outcome.GameReason.IsLegalFor(outcome.GameState) ||
		outcome.SeriesState != "" || outcome.SeriesReason != "" || outcome.ScoreRevisionID != nil {
		return invalidOfficialResultRevision("invalid Game outcome")
	}
	if outcome.GameState == domain.GameStateCompleted {
		if outcome.WinnerID == nil || *outcome.WinnerID == uuid.Nil {
			return invalidOfficialResultRevision("completed Game outcome has no winner")
		}
		return nil
	}
	if outcome.WinnerID != nil {
		return invalidOfficialResultRevision("non scoring Game outcome has a winner")
	}
	return nil
}

func validateOfficialSeriesOutcome(outcome OfficialResultOutcome) error {
	if !outcome.SeriesState.IsTerminal() || !outcome.SeriesReason.IsLegalFor(outcome.SeriesState) ||
		outcome.GameState != "" || outcome.GameReason != "" ||
		outcome.ScoreRevisionID == nil || outcome.ScoreRevisionID.IsZero() {
		return invalidOfficialResultRevision("invalid Series outcome")
	}
	if outcome.SeriesState == domain.SeriesStateCompleted {
		if outcome.WinnerID == nil || *outcome.WinnerID == uuid.Nil {
			return invalidOfficialResultRevision("completed Series outcome has no winner")
		}
		return nil
	}
	if outcome.WinnerID != nil {
		return invalidOfficialResultRevision("cancelled Series outcome has a winner")
	}
	return nil
}

func validateOfficialResultRevisionCommand(
	command OfficialResultRevisionCommand,
	recordedAt time.Time,
) error {
	if err := command.Scope.Validate(); err != nil {
		return err
	}
	if command.CommandID == uuid.Nil || command.RevisionID.IsZero() || command.Actor.Validate() != nil ||
		!validServerTime(recordedAt) || recordedAt.Before(command.ExpectedSourceProjection.CreatedAt()) {
		return invalidOfficialResultRevision("invalid command provenance")
	}
	if command.ExpectedCurrentRevisionID != nil && command.ExpectedCurrentRevisionID.IsZero() {
		return invalidOfficialResultRevision("invalid expected result head")
	}
	if err := validateOfficialSourceProjection(command.ExpectedSourceProjection, command.Scope); err != nil {
		return err
	}
	if err := command.Outcome.Validate(command.Scope.Kind); err != nil {
		return err
	}
	return validateOfficialLocalUUIDRoles(command)
}

func validateOfficialResultRevisionAuthority(authority OfficialResultRevisionAuthority) error {
	if err := authority.Scope.Validate(); err != nil {
		return err
	}
	if err := validateOfficialAuthoritySeries(authority); err != nil {
		return err
	}
	if err := validateOfficialAuthorityRowsAndGames(authority); err != nil {
		return err
	}
	if err := validateOfficialSourceProjection(authority.SourceProjection, authority.Scope); err != nil {
		return err
	}
	if err := validateOfficialPersistedHead(authority); err != nil {
		return err
	}
	return validateOfficialSeriesTransition(authority)
}

func officialProjectedOutcome(
	authority OfficialResultRevisionAuthority,
) (OfficialResultOutcome, error) {
	switch authority.Scope.Kind {
	case OfficialResultSubjectGame:
		game, ok := findSeriesGame(authority.ProjectedSeries, authority.Scope.GameID)
		if !ok {
			return OfficialResultOutcome{}, officialResultRevisionConflict("projected Game is missing")
		}
		outcome := OfficialResultOutcome{
			GameState:  game.State,
			GameReason: game.ResultReason,
			WinnerID:   cloneUUIDPointer(game.WinnerID),
		}
		if err := outcome.Validate(OfficialResultSubjectGame); err != nil {
			return OfficialResultOutcome{}, err
		}
		if outcome.WinnerID != nil && !officialWinnerIsParticipant(*outcome.WinnerID, authority.ProjectedSeries) {
			return OfficialResultOutcome{}, invalidOfficialResultRevision("Game winner is not a participant")
		}
		return outcome, nil
	case OfficialResultSubjectSeries:
		outcome := OfficialResultOutcome{
			SeriesState:     authority.ProjectedSeries.State,
			SeriesReason:    authority.ProjectedSeriesReason,
			WinnerID:        cloneUUIDPointer(authority.ProjectedSeries.WinnerID),
			ScoreRevisionID: cloneSeriesScoreRevisionIDPointer(authority.ProjectedSeries.CurrentScoreRevisionID),
		}
		if err := outcome.Validate(OfficialResultSubjectSeries); err != nil {
			return OfficialResultOutcome{}, err
		}
		if outcome.WinnerID != nil && !officialWinnerIsParticipant(*outcome.WinnerID, authority.ProjectedSeries) {
			return OfficialResultOutcome{}, invalidOfficialResultRevision("Series winner is not a participant")
		}
		if authority.ProjectedSeries.State == domain.SeriesStateCompleted {
			expected := authority.ProjectedSeries.Score.Winner(
				authority.ProjectedSeries.FirstParticipantID,
				authority.ProjectedSeries.SecondParticipantID,
				authority.ProjectedSeries.Format,
			)
			if expected == nil || outcome.WinnerID == nil || *expected != *outcome.WinnerID {
				return OfficialResultOutcome{}, invalidOfficialResultRevision("Series winner does not match score")
			}
		}
		return outcome, nil
	default:
		return OfficialResultOutcome{}, invalidOfficialResultRevision("unknown result subject")
	}
}

func validateOfficialProjectedHead(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) error {
	head, err := officialResultSubjectHead(authority.Scope, authority.ProjectedSeries)
	if err != nil {
		return err
	}
	if head == nil || *head != command.RevisionID {
		return officialResultRevisionConflict("projected result head changed")
	}
	return nil
}
