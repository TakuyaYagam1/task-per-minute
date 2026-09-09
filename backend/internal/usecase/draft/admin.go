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
	SelectedCategory           *domain.Category
	Reason                     string
	LockedAt                   time.Time
}

func LockAdmin(
	existing *CategoryLock,
	revision CategoryRevision,
	command AdminSelectionCommand,
) (CategoryLock, bool, error) {
	if err := validateAdminCategorySelection(revision, command); err != nil {
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
		[]domain.Category{*command.SelectedCategory},
		&command.ActorID,
		strings.TrimSpace(command.Reason),
		nil,
		command.LockedAt,
	)
	var err error
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
) error {
	if err := revision.Validate(); err != nil {
		return adminCategorySelectionError("category revision: %v", err)
	}
	if revision.Mode != domain.CategoryModeAdmin || revision.Format != domain.SeriesFormatBO1 {
		return adminCategorySelectionError("category revision is not BO1 admin mode")
	}
	if err := validateAdminCategorySelectionIdentity(revision, command); err != nil {
		return err
	}
	if err := validateAdminCategorySelectionChoice(revision, command); err != nil {
		return err
	}
	return validateAdminCategorySelectionTime(revision, command)
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
) error {
	if command.SelectedCategory == nil || !command.SelectedCategory.IsValid() ||
		!slices.Contains(revision.CategoryPool.Categories, *command.SelectedCategory) {
		return adminCategorySelectionError("selected category is missing or ineligible")
	}
	if strings.TrimSpace(command.Reason) == "" {
		return adminCategorySelectionError("selection reason is blank")
	}
	return nil
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
	return lock.ID == command.LockID && lock.SelectorActorID != nil && *lock.SelectorActorID == command.ActorID &&
		lock.SelectionReason == strings.TrimSpace(command.Reason) &&
		lock.CategoryRevisionID == command.ExpectedCategoryRevisionID &&
		lock.CategoryRevision == command.ExpectedCategoryRevision &&
		lock.LockedAt.Equal(command.LockedAt) && len(lock.SelectedCategories) == 1 &&
		command.SelectedCategory != nil && lock.SelectedCategories[0] == *command.SelectedCategory
}

func adminCategorySelectionError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidAdminSelection, fmt.Sprintf(format, arguments...))
}
