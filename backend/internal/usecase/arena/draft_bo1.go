package arena

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidBO1Draft = errors.New("invalid BO1 draft")

var bo1DraftCategories = []domain.Category{
	domain.CategoryCrypto,
	domain.CategoryReverse,
	domain.CategoryWeb,
}

type DraftStartCommand struct {
	DraftID             uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	FirstDeadline       time.Time
}

type DraftActionCommand struct {
	ExpectedTurn int
	ActorID      uuid.UUID
	Action       domain.ArenaDraftActionType
	Category     domain.Category
	OccurredAt   time.Time
	NextDeadline time.Time
}

func NewBO1Draft(
	revision SeriesCategoryRevision,
	command DraftStartCommand,
) (domain.ArenaDraft, error) {
	if err := validateBO1DraftRevision(revision); err != nil {
		return domain.ArenaDraft{}, err
	}
	draft, err := domain.NewArenaDraft(
		command.DraftID,
		revision.SeriesID,
		domain.ArenaSeriesFormatBO1,
		command.FirstParticipantID,
		command.SecondParticipantID,
		revision.CategoryPool.Categories,
		command.FirstDeadline,
	)
	if err != nil {
		return domain.ArenaDraft{}, fmt.Errorf("%w: start draft: %w", ErrInvalidBO1Draft, err)
	}
	return cloneArenaDraft(draft), nil
}

func ApplyBO1DraftAction(
	current domain.ArenaDraft,
	command DraftActionCommand,
) (domain.ArenaDraft, error) {
	if err := validateBO1Draft(current); err != nil {
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
		return domain.ArenaDraft{}, fmt.Errorf("%w: apply action: %w", ErrInvalidBO1Draft, err)
	}
	if err := next.Validate(); err != nil {
		return domain.ArenaDraft{}, fmt.Errorf("%w: action result: %w", ErrInvalidBO1Draft, err)
	}
	return cloneArenaDraft(next), nil
}

func BO1DraftGameCategory(draft domain.ArenaDraft) (domain.Category, error) {
	if err := validateBO1Draft(draft); err != nil {
		return "", err
	}
	if draft.State != domain.ArenaDraftStateCompleted || len(draft.SelectedCategories) != 1 {
		return "", fmt.Errorf("%w: draft is not complete", ErrInvalidBO1Draft)
	}
	return draft.SelectedCategories[0], nil
}

func validateBO1DraftRevision(revision SeriesCategoryRevision) error {
	if err := revision.Validate(); err != nil {
		return fmt.Errorf("%w: category revision: %w", ErrInvalidBO1Draft, err)
	}
	if revision.Format != domain.ArenaSeriesFormatBO1 ||
		revision.Mode != domain.ArenaCategoryModeDraft ||
		!slices.Equal(revision.CategoryPool.Categories, bo1DraftCategories) {
		return fmt.Errorf("%w: revision must use BO1 draft mode with Web, Crypto, and Reverse", ErrInvalidBO1Draft)
	}
	return nil
}

func validateBO1Draft(draft domain.ArenaDraft) error {
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("%w: snapshot: %w", ErrInvalidBO1Draft, err)
	}
	if draft.Format != domain.ArenaSeriesFormatBO1 || !slices.Equal(draft.Pool, bo1DraftCategories) {
		return fmt.Errorf("%w: snapshot must use BO1 with Web, Crypto, and Reverse", ErrInvalidBO1Draft)
	}
	return nil
}

func cloneArenaDraft(draft domain.ArenaDraft) domain.ArenaDraft {
	cloned := draft
	cloned.Pool = append([]domain.Category(nil), draft.Pool...)
	cloned.Actions = append([]domain.ArenaDraftAction(nil), draft.Actions...)
	cloned.SelectedCategories = append([]domain.Category(nil), draft.SelectedCategories...)
	return cloned
}
