package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidCategoryLock    = errors.New("invalid series category lock")
	ErrCategoryLocked         = errors.New("series category is locked")
	ErrInvalidRandomSelection = errors.New("invalid random category selection")
)

type RandomSelectionCommand struct {
	LockID     uuid.UUID
	EvidenceID uuid.UUID
	LockedAt   time.Time
}

type CategoryLock struct {
	ID                    uuid.UUID
	TournamentID          uuid.UUID
	SeriesID              uuid.UUID
	RosterID              uuid.UUID
	CategoryRevisionID    uuid.UUID
	CategoryRevision      int64
	Stage                 domain.TournamentStage
	Format                domain.SeriesFormat
	Mode                  domain.CategoryMode
	SourceContentRevision int64
	CategoryPoolID        uuid.UUID
	CategoryPoolRevision  int64
	SelectedCategories    []domain.Category
	SelectorActorID       *uuid.UUID
	SelectionReason       string
	DecisionEvidence      *domain.DecisionEvidence
	LockedAt              time.Time
	ProofHash             string
}

type seriesCategoryLockProofDocument struct {
	ID                    string   `json:"id"`
	TournamentID          string   `json:"tournament_id"`
	SeriesID              string   `json:"series_id"`
	RosterID              string   `json:"roster_id"`
	CategoryRevisionID    string   `json:"category_revision_id"`
	CategoryRevision      int64    `json:"category_revision"`
	Stage                 string   `json:"stage"`
	Format                string   `json:"format"`
	Mode                  string   `json:"mode"`
	SourceContentRevision int64    `json:"source_content_revision"`
	CategoryPoolID        string   `json:"category_pool_id"`
	CategoryPoolRevision  int64    `json:"category_pool_revision"`
	SelectedCategories    []string `json:"selected_categories"`
	SelectorActorID       string   `json:"selector_actor_id"`
	SelectionReason       string   `json:"selection_reason"`
	DecisionEvidenceID    string   `json:"decision_evidence_id"`
	DecisionReplayDigest  string   `json:"decision_replay_digest"`
	LockedAt              string   `json:"locked_at"`
}

func LockRandom(
	existing *CategoryLock,
	revision CategoryRevision,
	command RandomSelectionCommand,
) (CategoryLock, bool, error) {
	if err := validateRandomCategorySelection(revision, command); err != nil {
		return CategoryLock{}, false, err
	}
	if existing != nil {
		if err := existing.Validate(revision); err != nil {
			return CategoryLock{}, false, err
		}
		if randomCategorySelectionMatches(*existing, command) {
			return cloneSeriesCategoryLock(*existing), false, nil
		}
		return CategoryLock{}, false, ErrCategoryLocked
	}

	evidence, err := domain.NewDecisionEvidence(
		command.EvidenceID,
		domain.DecisionPurposeCategory,
		domain.DecisionAlgorithmV1,
		categoryDecisionInputs(revision.CategoryPool.Categories),
		command.LockID,
		command.LockedAt,
	)
	if err != nil {
		return CategoryLock{}, false, randomCategorySelectionError("decision evidence: %v", err)
	}
	selected := domain.Category(evidence.Result[0])
	lock := newSeriesCategoryLock(
		command.LockID,
		revision,
		[]domain.Category{selected},
		nil,
		"",
		&evidence,
		command.LockedAt,
	)
	lock.ProofHash, err = seriesCategoryLockProofHash(lock)
	if err != nil {
		return CategoryLock{}, false, randomCategorySelectionError("build lock proof: %v", err)
	}
	if err := lock.Validate(revision); err != nil {
		return CategoryLock{}, false, randomCategorySelectionError("lock evidence: %v", err)
	}
	return cloneSeriesCategoryLock(lock), true, nil
}

func (l CategoryLock) Validate(revision CategoryRevision) error {
	if err := revision.Validate(); err != nil {
		return seriesCategoryLockError("category revision: %v", err)
	}
	if err := validateSeriesCategoryLockIdentity(l, revision); err != nil {
		return err
	}
	if err := validateSeriesCategoryLockSelection(l, revision); err != nil {
		return err
	}
	if err := validateSeriesCategoryLockMode(l, revision); err != nil {
		return err
	}
	wantProof, err := seriesCategoryLockProofHash(l)
	if err != nil || !validSeriesCategoryProofHash(l.ProofHash) || l.ProofHash != wantProof {
		return seriesCategoryLockError("lock proof does not match")
	}
	return nil
}

func validateSeriesCategoryLockIdentity(
	lock CategoryLock,
	revision CategoryRevision,
) error {
	if lock.ID == uuid.Nil || lock.ID != revision.ID || lock.TournamentID != revision.TournamentID ||
		lock.SeriesID != revision.SeriesID || lock.CategoryRevisionID != revision.ID ||
		lock.RosterID != revision.RosterID || lock.CategoryRevision != revision.Revision ||
		lock.Stage != revision.Stage || lock.Format != revision.Format || lock.Mode != revision.Mode ||
		lock.SourceContentRevision != revision.SourceContentRevision ||
		lock.CategoryPoolID != revision.CategoryPool.ID ||
		lock.CategoryPoolRevision != revision.CategoryPool.Revision {
		return seriesCategoryLockError("lock does not match the category revision")
	}
	return nil
}

func validateSeriesCategoryLockSelection(
	lock CategoryLock,
	revision CategoryRevision,
) error {
	if lock.LockedAt.IsZero() || lock.LockedAt.Location() != time.UTC ||
		lock.LockedAt.Before(revision.CreatedAt) {
		return seriesCategoryLockError("lock timestamp must be server UTC")
	}
	return validateLockedCategories(lock.SelectedCategories, revision)
}

func validateSeriesCategoryLockMode(
	lock CategoryLock,
	revision CategoryRevision,
) error {
	switch lock.Mode {
	case domain.CategoryModeRandom:
		if lock.SelectorActorID != nil || lock.SelectionReason != "" {
			return seriesCategoryLockError("random lock contains admin selection evidence")
		}
		return validateRandomCategoryEvidence(lock, revision)
	case domain.CategoryModeAdmin:
		if lock.SelectorActorID == nil || *lock.SelectorActorID == uuid.Nil ||
			lock.SelectionReason == "" || lock.SelectionReason != strings.TrimSpace(lock.SelectionReason) ||
			lock.DecisionEvidence != nil {
			return seriesCategoryLockError("admin lock has invalid selection evidence")
		}
		return nil
	case domain.CategoryModeDraft:
		return seriesCategoryLockError("draft mode does not produce a direct lock")
	default:
		return seriesCategoryLockError("unknown category mode")
	}
}

func validateRandomCategorySelection(
	revision CategoryRevision,
	command RandomSelectionCommand,
) error {
	if err := revision.Validate(); err != nil {
		return randomCategorySelectionError("category revision: %v", err)
	}
	if revision.Mode != domain.CategoryModeRandom || revision.Format != domain.SeriesFormatBO1 {
		return randomCategorySelectionError("category revision is not BO1 random mode")
	}
	if command.LockID == uuid.Nil || command.LockID != revision.ID || command.EvidenceID == uuid.Nil ||
		command.LockID == command.EvidenceID {
		return randomCategorySelectionError("missing or reused identity")
	}
	if command.LockedAt.IsZero() || command.LockedAt.Location() != time.UTC ||
		command.LockedAt.Before(revision.CreatedAt) {
		return randomCategorySelectionError("lock timestamp must be server UTC after revision creation")
	}
	return nil
}

func validateLockedCategories(categories []domain.Category, revision CategoryRevision) error {
	expected := revision.Format.WinsRequired()*2 - 1
	if expected < 1 || len(categories) != expected {
		return seriesCategoryLockError("selected categories do not cover every expected Game")
	}
	seen := make(map[domain.Category]struct{}, len(categories))
	for _, category := range categories {
		if !category.IsValid() || !slices.Contains(revision.CategoryPool.Categories, category) {
			return seriesCategoryLockError("selected category is not eligible")
		}
		if _, duplicate := seen[category]; duplicate {
			return seriesCategoryLockError("selected category is duplicated")
		}
		seen[category] = struct{}{}
	}
	return nil
}

func validateRandomCategoryEvidence(
	lock CategoryLock,
	revision CategoryRevision,
) error {
	if lock.DecisionEvidence == nil {
		return seriesCategoryLockError("random lock is missing decision evidence")
	}
	evidence := *lock.DecisionEvidence
	if err := evidence.Validate(); err != nil {
		return seriesCategoryLockError("random decision evidence: %v", err)
	}
	if evidence.Purpose != domain.DecisionPurposeCategory || evidence.OwnerID != lock.ID ||
		evidence.DecidedAt != lock.LockedAt ||
		!slices.Equal(evidence.NormalizedInputs, categoryDecisionInputs(revision.CategoryPool.Categories)) ||
		len(evidence.Result) == 0 || domain.Category(evidence.Result[0]) != lock.SelectedCategories[0] {
		return seriesCategoryLockError("random decision evidence does not match the lock")
	}
	return nil
}

func newSeriesCategoryLock(
	id uuid.UUID,
	revision CategoryRevision,
	selected []domain.Category,
	selectorActorID *uuid.UUID,
	selectionReason string,
	evidence *domain.DecisionEvidence,
	lockedAt time.Time,
) CategoryLock {
	return CategoryLock{
		ID: id, TournamentID: revision.TournamentID, SeriesID: revision.SeriesID, RosterID: revision.RosterID,
		CategoryRevisionID: revision.ID, CategoryRevision: revision.Revision,
		Stage: revision.Stage, Format: revision.Format, Mode: revision.Mode,
		SourceContentRevision: revision.SourceContentRevision,
		CategoryPoolID:        revision.CategoryPool.ID, CategoryPoolRevision: revision.CategoryPool.Revision,
		SelectedCategories: append([]domain.Category(nil), selected...),
		SelectorActorID:    cloneUUIDPointer(selectorActorID), SelectionReason: selectionReason,
		DecisionEvidence: cloneDecisionEvidence(evidence), LockedAt: lockedAt.Round(0).UTC(),
	}
}

func randomCategorySelectionMatches(
	lock CategoryLock,
	command RandomSelectionCommand,
) bool {
	return lock.ID == command.LockID && lock.LockedAt.Equal(command.LockedAt) &&
		lock.DecisionEvidence != nil && lock.DecisionEvidence.ID == command.EvidenceID
}

func categoryDecisionInputs(categories []domain.Category) []string {
	inputs := make([]string, len(categories))
	for index, category := range categories {
		inputs[index] = category.String()
	}
	return inputs
}

func seriesCategoryLockProofHash(lock CategoryLock) (string, error) {
	document := seriesCategoryLockProofDocument{
		ID: lock.ID.String(), TournamentID: lock.TournamentID.String(), SeriesID: lock.SeriesID.String(),
		RosterID:           lock.RosterID.String(),
		CategoryRevisionID: lock.CategoryRevisionID.String(), CategoryRevision: lock.CategoryRevision,
		Stage: string(lock.Stage), Format: string(lock.Format), Mode: string(lock.Mode),
		SourceContentRevision: lock.SourceContentRevision,
		CategoryPoolID:        lock.CategoryPoolID.String(), CategoryPoolRevision: lock.CategoryPoolRevision,
		SelectedCategories: categoryDecisionInputs(lock.SelectedCategories), SelectionReason: lock.SelectionReason,
		LockedAt: lock.LockedAt.Format(time.RFC3339Nano),
	}
	if lock.SelectorActorID != nil {
		document.SelectorActorID = lock.SelectorActorID.String()
	}
	if lock.DecisionEvidence != nil {
		document.DecisionEvidenceID = lock.DecisionEvidence.ID.String()
		document.DecisionReplayDigest = hex.EncodeToString(lock.DecisionEvidence.ReplayDigest[:])
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validSeriesCategoryProofHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cloneSeriesCategoryLock(lock CategoryLock) CategoryLock {
	cloned := lock
	cloned.SelectedCategories = append([]domain.Category(nil), lock.SelectedCategories...)
	cloned.SelectorActorID = cloneUUIDPointer(lock.SelectorActorID)
	cloned.DecisionEvidence = cloneDecisionEvidence(lock.DecisionEvidence)
	return cloned
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneDecisionEvidence(evidence *domain.DecisionEvidence) *domain.DecisionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	cloned.NormalizedInputs = append([]string(nil), evidence.NormalizedInputs...)
	cloned.Result = append([]string(nil), evidence.Result...)
	return &cloned
}

func randomCategorySelectionError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRandomSelection, fmt.Sprintf(format, arguments...))
}

func seriesCategoryLockError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCategoryLock, fmt.Sprintf(format, arguments...))
}
