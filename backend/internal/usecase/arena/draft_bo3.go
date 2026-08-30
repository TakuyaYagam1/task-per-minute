package arena

import (
	"errors"
	"fmt"
	"slices"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidBO3FinalDraft = errors.New("invalid BO3 final draft")

var bo3FinalDraftCategories = []domain.Category{
	domain.CategoryCrypto,
	domain.CategoryForensics,
	domain.CategoryPwn,
	domain.CategoryReverse,
	domain.CategoryWeb,
}

func NewBO3FinalDraft(
	revision SeriesCategoryRevision,
	command DraftStartCommand,
) (domain.ArenaDraft, error) {
	if err := validateBO3FinalDraftRevision(revision); err != nil {
		return domain.ArenaDraft{}, err
	}
	draft, err := domain.NewArenaDraft(
		command.DraftID,
		revision.SeriesID,
		domain.ArenaSeriesFormatBO3,
		command.FirstParticipantID,
		command.SecondParticipantID,
		revision.CategoryPool.Categories,
		command.FirstDeadline,
	)
	if err != nil {
		return domain.ArenaDraft{}, fmt.Errorf("%w: start draft: %w", ErrInvalidBO3FinalDraft, err)
	}
	return cloneArenaDraft(draft), nil
}

func ApplyBO3FinalDraftAction(
	current domain.ArenaDraft,
	command DraftActionCommand,
) (domain.ArenaDraft, error) {
	if err := validateBO3FinalDraft(current); err != nil {
		return domain.ArenaDraft{}, err
	}
	next := cloneArenaDraft(current)
	if err := next.ApplyAction(
		command.ExpectedTurn,
		command.ActorID,
		command.Action,
		command.Category,
		command.OccurredAt,
		command.NextDeadline,
	); err != nil {
		return domain.ArenaDraft{}, fmt.Errorf("%w: apply action: %w", ErrInvalidBO3FinalDraft, err)
	}
	if err := next.Validate(); err != nil {
		return domain.ArenaDraft{}, fmt.Errorf("%w: action result: %w", ErrInvalidBO3FinalDraft, err)
	}
	return cloneArenaDraft(next), nil
}

func BO3FinalDraftGameCategories(draft domain.ArenaDraft) ([3]domain.Category, error) {
	if err := validateBO3FinalDraft(draft); err != nil {
		return [3]domain.Category{}, err
	}
	if draft.State != domain.ArenaDraftStateCompleted || len(draft.SelectedCategories) != 3 {
		return [3]domain.Category{}, fmt.Errorf("%w: draft is not complete", ErrInvalidBO3FinalDraft)
	}
	return [3]domain.Category{
		draft.SelectedCategories[0],
		draft.SelectedCategories[1],
		draft.SelectedCategories[2],
	}, nil
}

func validateBO3FinalDraftRevision(revision SeriesCategoryRevision) error {
	if err := revision.Validate(); err != nil {
		return fmt.Errorf("%w: category revision: %w", ErrInvalidBO3FinalDraft, err)
	}
	if revision.Stage != ArenaStageFinal ||
		revision.Format != domain.ArenaSeriesFormatBO3 ||
		revision.Mode != domain.ArenaCategoryModeDraft ||
		!slices.Equal(revision.CategoryPool.Categories, bo3FinalDraftCategories) {
		return fmt.Errorf("%w: revision must use the five-category final draft", ErrInvalidBO3FinalDraft)
	}
	return nil
}

func validateBO3FinalDraft(draft domain.ArenaDraft) error {
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("%w: snapshot: %w", ErrInvalidBO3FinalDraft, err)
	}
	if draft.Format != domain.ArenaSeriesFormatBO3 || !slices.Equal(draft.Pool, bo3FinalDraftCategories) {
		return fmt.Errorf("%w: snapshot must use the five-category BO3 pool", ErrInvalidBO3FinalDraft)
	}
	return nil
}
