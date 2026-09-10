package draft

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidCategoryRevision = errors.New("invalid series category revision")
	ErrCategoryNotEditable     = errors.New("series category is not editable")
)

type CategoryRevisionCommand struct {
	ID             uuid.UUID
	SeriesID       uuid.UUID
	RosterID       uuid.UUID
	SeriesState    domain.SeriesState
	Stage          domain.TournamentStage
	Configuration  domain.ContentConfiguration
	ModeOverride   *domain.CategoryMode
	CategoryLocked bool
	CreatedAt      time.Time
}

type CategoryRevision struct {
	ID                    uuid.UUID
	TournamentID          uuid.UUID
	SeriesID              uuid.UUID
	RosterID              uuid.UUID
	Revision              int64
	SupersedesRevisionID  uuid.UUID
	Stage                 domain.TournamentStage
	Format                domain.SeriesFormat
	Mode                  domain.CategoryMode
	SourceContentRevision int64
	CategoryPool          domain.CategoryPoolRevision
	CreatedAt             time.Time
}

func DeriveCategoryRevision(
	current *CategoryRevision,
	command CategoryRevisionCommand,
) (CategoryRevision, bool, error) {
	if err := validateSeriesCategoryRevisionCommand(command); err != nil {
		return CategoryRevision{}, false, err
	}
	stageDefault, pool, err := resolveSeriesCategoryMode(command)
	if err != nil {
		return CategoryRevision{}, false, err
	}

	next := CategoryRevision{
		ID: command.ID, TournamentID: command.Configuration.TournamentID,
		SeriesID: command.SeriesID, RosterID: command.RosterID,
		Revision: 1, Stage: command.Stage, Format: stageDefault.Format, Mode: stageDefault.CategoryMode,
		SourceContentRevision: command.Configuration.Revision,
		CategoryPool:          cloneCategoryPool(pool),
		CreatedAt:             command.CreatedAt.Round(0).UTC(),
	}
	if current != nil {
		if err := validateCurrentSeriesCategoryRevision(next, *current); err != nil {
			return CategoryRevision{}, false, err
		}
		if seriesCategoryRevisionContentEqual(*current, next) {
			return cloneSeriesCategoryRevision(*current), false, nil
		}
		if err := prepareSeriesCategorySuccessor(&next, *current); err != nil {
			return CategoryRevision{}, false, err
		}
	}
	if command.CategoryLocked || command.SeriesState != domain.SeriesStatePlanned {
		return CategoryRevision{}, false, ErrCategoryNotEditable
	}
	if err := next.Validate(); err != nil {
		return CategoryRevision{}, false, err
	}
	return cloneSeriesCategoryRevision(next), true, nil
}

func (r CategoryRevision) Validate() error {
	if err := validateSeriesCategoryRevisionIdentity(r); err != nil {
		return err
	}
	if err := validateSeriesCategoryRevisionMetadata(r); err != nil {
		return err
	}
	return validateSeriesCategoryRevisionPool(r)
}

func validateSeriesCategoryRevisionIdentity(r CategoryRevision) error {
	if r.ID == uuid.Nil || r.TournamentID == uuid.Nil || r.SeriesID == uuid.Nil ||
		r.RosterID == uuid.Nil || r.Revision < 1 {
		return seriesCategoryRevisionError("missing identity or revision")
	}
	if (r.Revision == 1) != (r.SupersedesRevisionID == uuid.Nil) || r.SupersedesRevisionID == r.ID {
		return seriesCategoryRevisionError("invalid predecessor identity")
	}
	if r.CreatedAt.IsZero() || r.CreatedAt.Location() != time.UTC {
		return seriesCategoryRevisionError("creation timestamp must be server UTC")
	}
	return nil
}

func validateSeriesCategoryRevisionMetadata(r CategoryRevision) error {
	if !r.Stage.IsValid() || !r.Format.IsValid() || !r.Mode.IsValid() || r.SourceContentRevision < 1 {
		return seriesCategoryRevisionError("invalid stage, format, mode, or source revision")
	}
	return validateSeriesCategoryStage(r.Stage, r.Format, r.Mode)
}

func validateSeriesCategoryRevisionPool(r CategoryRevision) error {
	if r.CategoryPool.ID == uuid.Nil || r.CategoryPool.Revision < 1 || r.CategoryPool.Format != r.Format {
		return seriesCategoryRevisionError("invalid category pool identity or format")
	}
	normalized, err := domain.NormalizeCategories(r.CategoryPool.Format, r.CategoryPool.Categories)
	if err != nil || !reflect.DeepEqual(normalized, r.CategoryPool.Categories) {
		return seriesCategoryRevisionError("category pool is invalid or not normalized")
	}
	return nil
}

func validateSeriesCategoryRevisionCommand(command CategoryRevisionCommand) error {
	if command.ID == uuid.Nil || command.SeriesID == uuid.Nil || command.RosterID == uuid.Nil {
		return seriesCategoryRevisionError("missing revision, series, or roster identity")
	}
	if !command.SeriesState.IsValid() || !command.Stage.IsValid() {
		return seriesCategoryRevisionError("invalid Series state or stage")
	}
	if command.CreatedAt.IsZero() || command.CreatedAt.Location() != time.UTC {
		return seriesCategoryRevisionError("creation timestamp must be server UTC")
	}
	if err := command.Configuration.Validate(); err != nil {
		return fmt.Errorf("%w: content configuration: %w", ErrInvalidCategoryRevision, err)
	}
	if command.ModeOverride != nil && !command.ModeOverride.IsValid() {
		return seriesCategoryRevisionError("invalid category mode override")
	}
	return nil
}

func resolveSeriesCategoryMode(
	command CategoryRevisionCommand,
) (domain.StageContentDefault, domain.CategoryPoolRevision, error) {
	var stageDefault domain.StageContentDefault
	for _, candidate := range command.Configuration.StageDefaults {
		if candidate.Stage == command.Stage {
			stageDefault = candidate
			break
		}
	}
	if !stageDefault.Stage.IsValid() {
		return domain.StageContentDefault{}, domain.CategoryPoolRevision{}, seriesCategoryRevisionError("stage default is missing")
	}
	if command.ModeOverride != nil {
		stageDefault.CategoryMode = *command.ModeOverride
	}
	if err := validateSeriesCategoryStage(command.Stage, stageDefault.Format, stageDefault.CategoryMode); err != nil {
		return domain.StageContentDefault{}, domain.CategoryPoolRevision{}, err
	}
	for _, pool := range command.Configuration.CategoryPools {
		if pool.ID == stageDefault.CategoryPoolRevisionID {
			return stageDefault, cloneCategoryPool(pool), nil
		}
	}
	return domain.StageContentDefault{}, domain.CategoryPoolRevision{}, seriesCategoryRevisionError("category pool is missing")
}

func validateSeriesCategoryStage(
	stage domain.TournamentStage,
	format domain.SeriesFormat,
	mode domain.CategoryMode,
) error {
	if !mode.IsValid() {
		return seriesCategoryRevisionError("invalid category mode")
	}
	if stage == domain.TournamentStageFinal {
		if format != domain.SeriesFormatBO3 {
			return seriesCategoryRevisionError("final stage requires BO3")
		}
		return nil
	}
	if format != domain.SeriesFormatBO1 {
		return seriesCategoryRevisionError("pre-final stage requires BO1")
	}
	return nil
}

func validateCurrentSeriesCategoryRevision(
	next CategoryRevision,
	current CategoryRevision,
) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w: current revision: %w", ErrInvalidCategoryRevision, err)
	}
	if current.TournamentID != next.TournamentID || current.SeriesID != next.SeriesID ||
		current.RosterID != next.RosterID || current.Stage != next.Stage {
		return seriesCategoryRevisionError("revision ownership changed")
	}
	if current.SourceContentRevision > next.SourceContentRevision {
		return seriesCategoryRevisionError("source content revision moved backwards")
	}
	return nil
}

func prepareSeriesCategorySuccessor(
	next *CategoryRevision,
	current CategoryRevision,
) error {
	if next.ID == current.ID || !next.CreatedAt.After(current.CreatedAt) {
		return seriesCategoryRevisionError("successor identity or timestamp did not advance")
	}
	next.Revision = current.Revision + 1
	next.SupersedesRevisionID = current.ID
	return nil
}

func seriesCategoryRevisionContentEqual(first, second CategoryRevision) bool {
	return first.TournamentID == second.TournamentID && first.SeriesID == second.SeriesID &&
		first.RosterID == second.RosterID &&
		first.Stage == second.Stage && first.Format == second.Format && first.Mode == second.Mode &&
		first.SourceContentRevision == second.SourceContentRevision &&
		reflect.DeepEqual(first.CategoryPool, second.CategoryPool)
}

func cloneSeriesCategoryRevision(revision CategoryRevision) CategoryRevision {
	cloned := revision
	cloned.CategoryPool = cloneCategoryPool(revision.CategoryPool)
	return cloned
}

func cloneCategoryPool(pool domain.CategoryPoolRevision) domain.CategoryPoolRevision {
	cloned := pool
	cloned.Categories = append([]domain.Category(nil), pool.Categories...)
	return cloned
}

func seriesCategoryRevisionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCategoryRevision, message)
}
