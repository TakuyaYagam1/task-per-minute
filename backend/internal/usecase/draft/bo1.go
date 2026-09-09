package draft

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidBO1 = errors.New("invalid BO1 draft")

var bo1DraftCategories = []domain.Category{
	domain.CategoryCrypto,
	domain.CategoryReverse,
	domain.CategoryWeb,
}

type StartCommand struct {
	DraftID             uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	FirstDeadline       time.Time
}

type ActionCommand struct {
	ExpectedTurn int
	ActorID      uuid.UUID
	Action       domain.DraftActionType
	Category     domain.Category
	OccurredAt   time.Time
	NextDeadline time.Time
}

func NewBO1(
	revision CategoryRevision,
	command StartCommand,
) (domain.Draft, error) {
	if err := validateBO1DraftRevision(revision); err != nil {
		return domain.Draft{}, err
	}
	draft, err := domain.NewDraft(
		command.DraftID,
		revision.SeriesID,
		domain.SeriesFormatBO1,
		command.FirstParticipantID,
		command.SecondParticipantID,
		revision.CategoryPool.Categories,
		command.FirstDeadline,
	)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("%w: start draft: %w", ErrInvalidBO1, err)
	}
	return cloneDraft(draft), nil
}

func ApplyBO1Action(
	current domain.Draft,
	command ActionCommand,
) (domain.Draft, error) {
	if err := validateBO1Draft(current); err != nil {
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
		return domain.Draft{}, fmt.Errorf("%w: apply action: %w", ErrInvalidBO1, err)
	}
	if err := next.Validate(); err != nil {
		return domain.Draft{}, fmt.Errorf("%w: action result: %w", ErrInvalidBO1, err)
	}
	return cloneDraft(next), nil
}

func BO1GameCategory(draft domain.Draft) (domain.Category, error) {
	if err := validateBO1Draft(draft); err != nil {
		return "", err
	}
	if draft.State != domain.DraftStateCompleted || len(draft.SelectedCategories) != 1 {
		return "", fmt.Errorf("%w: draft is not complete", ErrInvalidBO1)
	}
	return draft.SelectedCategories[0], nil
}

func validateBO1DraftRevision(revision CategoryRevision) error {
	if err := revision.Validate(); err != nil {
		return fmt.Errorf("%w: category revision: %w", ErrInvalidBO1, err)
	}
	if revision.Format != domain.SeriesFormatBO1 ||
		revision.Mode != domain.CategoryModeDraft ||
		!slices.Equal(revision.CategoryPool.Categories, bo1DraftCategories) {
		return fmt.Errorf("%w: revision must use BO1 draft mode with Web, Crypto, and Reverse", ErrInvalidBO1)
	}
	return nil
}

func validateBO1Draft(draft domain.Draft) error {
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("%w: snapshot: %w", ErrInvalidBO1, err)
	}
	if draft.Format != domain.SeriesFormatBO1 || !slices.Equal(draft.Pool, bo1DraftCategories) {
		return fmt.Errorf("%w: snapshot must use BO1 with Web, Crypto, and Reverse", ErrInvalidBO1)
	}
	return nil
}

func cloneDraft(draft domain.Draft) domain.Draft {
	cloned := draft
	cloned.Pool = append([]domain.Category(nil), draft.Pool...)
	cloned.Actions = append([]domain.DraftAction(nil), draft.Actions...)
	cloned.SelectedCategories = append([]domain.Category(nil), draft.SelectedCategories...)
	return cloned
}

func CloneDraft(draft domain.Draft) domain.Draft {
	return cloneDraft(draft)
}
