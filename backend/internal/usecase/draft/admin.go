package draft

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidAdminSelection = errors.New("invalid admin category selection")

type AdminSelectionCommand struct {
	LockID                     uuid.UUID
	ActorID                    uuid.UUID
	ExpectedCategoryRevisionID uuid.UUID
	ExpectedCategoryRevision   int64
	// SelectedCategory selects the only BO1 category. BO3 uses
	// SelectedCategories to preserve the explicit game order.
	SelectedCategory   *domain.Category
	SelectedCategories []domain.Category
	Reason             string
	LockedAt           time.Time
}

func LockAdmin(
	existing *CategoryLock,
	revision CategoryRevision,
	command AdminSelectionCommand,
) (CategoryLock, bool, error) {
	selectedCategories, err := validateAdminCategorySelection(revision, command)
	if err != nil {
		return CategoryLock{}, false, err
	}
	if existing != nil {
		if err := existing.Validate(revision); err != nil {
			return CategoryLock{}, false, err
		}
		if adminCategorySelectionMatches(*existing, command) {
			return cloneSeriesCategoryLock(*existing), false, nil
		}
		return CategoryLock{}, false, ErrCategoryLocked
	}

	lock := newSeriesCategoryLock(
		command.LockID,
		revision,
		selectedCategories,
		&command.ActorID,
		strings.TrimSpace(command.Reason),
		nil,
		command.LockedAt,
	)
	lock.ProofHash, err = seriesCategoryLockProofHash(lock)
	if err != nil {
		return CategoryLock{}, false, adminCategorySelectionError("build lock proof: %v", err)
	}
	if err := lock.Validate(revision); err != nil {
		return CategoryLock{}, false, adminCategorySelectionError("lock evidence: %v", err)
	}
	return cloneSeriesCategoryLock(lock), true, nil
}

func validateAdminCategorySelection(
	revision CategoryRevision,
	command AdminSelectionCommand,
) ([]domain.Category, error) {
	if err := revision.Validate(); err != nil {
		return nil, adminCategorySelectionError("category revision: %v", err)
	}
	if revision.Mode != domain.CategoryModeAdmin {
		return nil, adminCategorySelectionError("category revision is not admin mode")
	}
	if err := validateAdminCategorySelectionIdentity(revision, command); err != nil {
		return nil, err
	}
	selectedCategories, err := validateAdminCategorySelectionChoice(revision, command)
	if err != nil {
		return nil, err
	}
	if err := validateAdminCategorySelectionTime(revision, command); err != nil {
		return nil, err
	}
	return selectedCategories, nil
}

func validateAdminCategorySelectionIdentity(
	revision CategoryRevision,
	command AdminSelectionCommand,
) error {
	if command.LockID == uuid.Nil || command.LockID != revision.ID || command.ActorID == uuid.Nil {
		return adminCategorySelectionError("missing lock or actor identity")
	}
	if command.ExpectedCategoryRevisionID != revision.ID ||
		command.ExpectedCategoryRevision != revision.Revision {
		return adminCategorySelectionError("stale category revision")
	}
	return nil
}

func validateAdminCategorySelectionChoice(
	revision CategoryRevision,
	command AdminSelectionCommand,
) ([]domain.Category, error) {
	selectedCategories, err := adminCommandSelectedCategories(command)
	if err != nil {
		return nil, err
	}
	expected := revision.Format.WinsRequired()*2 - 1
	if expected < 1 || len(selectedCategories) != expected {
		return nil, adminCategorySelectionError("selected categories must contain exactly %d categories", expected)
	}
	seen := make(map[domain.Category]struct{}, len(selectedCategories))
	for _, category := range selectedCategories {
		if !category.IsValid() || !slices.Contains(revision.CategoryPool.Categories, category) {
			return nil, adminCategorySelectionError("selected category is missing or ineligible")
		}
		if _, duplicate := seen[category]; duplicate {
			return nil, adminCategorySelectionError("selected categories contain a duplicate")
		}
		seen[category] = struct{}{}
	}
	if strings.TrimSpace(command.Reason) == "" {
		return nil, adminCategorySelectionError("selection reason is blank")
	}
	return selectedCategories, nil
}

func validateAdminCategorySelectionTime(
	revision CategoryRevision,
	command AdminSelectionCommand,
) error {
	if command.LockedAt.IsZero() || command.LockedAt.Location() != time.UTC ||
		command.LockedAt.Before(revision.CreatedAt) {
		return adminCategorySelectionError("lock timestamp must be server UTC after revision creation")
	}
	return nil
}

func adminCategorySelectionMatches(
	lock CategoryLock,
	command AdminSelectionCommand,
) bool {
	selectedCategories, err := adminCommandSelectedCategories(command)
	if err != nil {
		return false
	}
	return lock.ID == command.LockID && lock.SelectorActorID != nil && *lock.SelectorActorID == command.ActorID &&
		lock.SelectionReason == strings.TrimSpace(command.Reason) &&
		lock.CategoryRevisionID == command.ExpectedCategoryRevisionID &&
		lock.CategoryRevision == command.ExpectedCategoryRevision &&
		lock.LockedAt.Equal(command.LockedAt) && slices.Equal(lock.SelectedCategories, selectedCategories)
}

func adminCommandSelectedCategories(command AdminSelectionCommand) ([]domain.Category, error) {
	if command.SelectedCategory != nil && command.SelectedCategories != nil {
		return nil, adminCategorySelectionError("selected category fields are ambiguous")
	}
	if command.SelectedCategories != nil {
		return append([]domain.Category(nil), command.SelectedCategories...), nil
	}
	if command.SelectedCategory != nil {
		return []domain.Category{*command.SelectedCategory}, nil
	}
	return nil, adminCategorySelectionError("selected category is missing")
}

func adminCategorySelectionError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidAdminSelection, fmt.Sprintf(format, arguments...))
}
