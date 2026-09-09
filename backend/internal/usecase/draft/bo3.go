package draft

import (
	"errors"
	"fmt"
	"slices"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidBO3Final = errors.New("invalid BO3 final draft")

var bo3FinalDraftCategories = []domain.Category{
	domain.CategoryCrypto,
	domain.CategoryForensics,
	domain.CategoryPwn,
	domain.CategoryReverse,
	domain.CategoryWeb,
}

func NewBO3Final(
	revision CategoryRevision,
	command StartCommand,
) (domain.Draft, error) {
	if err := validateBO3FinalDraftRevision(revision); err != nil {
		return domain.Draft{}, err
	}
	draft, err := domain.NewDraft(
		command.DraftID,
		revision.SeriesID,
		domain.SeriesFormatBO3,
		command.FirstParticipantID,
		command.SecondParticipantID,
		revision.CategoryPool.Categories,
		command.FirstDeadline,
	)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("%w: start draft: %w", ErrInvalidBO3Final, err)
	}
	return cloneDraft(draft), nil
}

func ApplyBO3FinalAction(
	current domain.Draft,
	command ActionCommand,
) (domain.Draft, error) {
	if err := validateBO3FinalDraft(current); err != nil {
		return domain.Draft{}, err
	}
	next := cloneDraft(current)
	if err := next.ApplyAction(
		command.ExpectedTurn,
		command.ActorID,
		command.Action,
		command.Category,
		command.OccurredAt,
		command.NextDeadline,
	); err != nil {
		return domain.Draft{}, fmt.Errorf("%w: apply action: %w", ErrInvalidBO3Final, err)
	}
	if err := next.Validate(); err != nil {
		return domain.Draft{}, fmt.Errorf("%w: action result: %w", ErrInvalidBO3Final, err)
	}
	return cloneDraft(next), nil
}

func BO3FinalGameCategories(draft domain.Draft) ([3]domain.Category, error) {
	if err := validateBO3FinalDraft(draft); err != nil {
		return [3]domain.Category{}, err
	}
	if draft.State != domain.DraftStateCompleted || len(draft.SelectedCategories) != 3 {
		return [3]domain.Category{}, fmt.Errorf("%w: draft is not complete", ErrInvalidBO3Final)
	}
	return [3]domain.Category{
		draft.SelectedCategories[0],
		draft.SelectedCategories[1],
		draft.SelectedCategories[2],
	}, nil
}

func validateBO3FinalDraftRevision(revision CategoryRevision) error {
	if err := revision.Validate(); err != nil {
		return fmt.Errorf("%w: category revision: %w", ErrInvalidBO3Final, err)
	}
	if revision.Stage != domain.TournamentStageFinal ||
		revision.Format != domain.SeriesFormatBO3 ||
		revision.Mode != domain.CategoryModeDraft ||
		!slices.Equal(revision.CategoryPool.Categories, bo3FinalDraftCategories) {
		return fmt.Errorf("%w: revision must use the five-category final draft", ErrInvalidBO3Final)
	}
	return nil
}

func validateBO3FinalDraft(draft domain.Draft) error {
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("%w: snapshot: %w", ErrInvalidBO3Final, err)
	}
	if draft.Format != domain.SeriesFormatBO3 || !slices.Equal(draft.Pool, bo3FinalDraftCategories) {
		return fmt.Errorf("%w: snapshot must use the five-category BO3 pool", ErrInvalidBO3Final)
	}
	return nil
}
