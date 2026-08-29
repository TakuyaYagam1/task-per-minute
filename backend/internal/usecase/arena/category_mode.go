package arena

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSeriesCategoryRevision = errors.New("invalid series category revision")
	ErrSeriesCategoryNotEditable     = errors.New("series category is not editable")
)

type SeriesCategoryRevisionCommand struct {
	ID             uuid.UUID
	SeriesID       uuid.UUID
	RosterID       uuid.UUID
	SeriesState    domain.ArenaSeriesState
	Stage          ArenaStage
	Configuration  ContentConfiguration
	ModeOverride   *domain.ArenaCategoryMode
	CategoryLocked bool
	CreatedAt      time.Time
}

type SeriesCategoryRevision struct {
	ID                    uuid.UUID
	TournamentID          uuid.UUID
	SeriesID              uuid.UUID
	RosterID              uuid.UUID
	Revision              int64
	SupersedesRevisionID  uuid.UUID
	Stage                 ArenaStage
	Format                domain.ArenaSeriesFormat
	Mode                  domain.ArenaCategoryMode
	SourceContentRevision int64
	CategoryPool          CategoryPoolRevision
	CreatedAt             time.Time
}

func DeriveSeriesCategoryRevision(
	current *SeriesCategoryRevision,
	command SeriesCategoryRevisionCommand,
) (SeriesCategoryRevision, bool, error) {
	if err := validateSeriesCategoryRevisionCommand(command); err != nil {
		return SeriesCategoryRevision{}, false, err
	}
	stageDefault, pool, err := resolveSeriesCategoryMode(command)
	if err != nil {
		return SeriesCategoryRevision{}, false, err
	}

	next := SeriesCategoryRevision{
		ID: command.ID, TournamentID: command.Configuration.TournamentID,
		SeriesID: command.SeriesID, RosterID: command.RosterID,
		Revision: 1, Stage: command.Stage, Format: stageDefault.Format, Mode: stageDefault.CategoryMode,
		SourceContentRevision: command.Configuration.Revision,
		CategoryPool:          cloneCategoryPool(pool),
		CreatedAt:             command.CreatedAt.Round(0).UTC(),
	}
	if current != nil {
		if err := validateCurrentSeriesCategoryRevision(next, *current); err != nil {
			return SeriesCategoryRevision{}, false, err
		}
		if seriesCategoryRevisionContentEqual(*current, next) {
			return cloneSeriesCategoryRevision(*current), false, nil
		}
		if err := prepareSeriesCategorySuccessor(&next, *current); err != nil {
			return SeriesCategoryRevision{}, false, err
		}
	}
	if command.CategoryLocked || command.SeriesState != domain.ArenaSeriesStatePlanned {
		return SeriesCategoryRevision{}, false, ErrSeriesCategoryNotEditable
	}
	if err := next.Validate(); err != nil {
		return SeriesCategoryRevision{}, false, err
	}
	return cloneSeriesCategoryRevision(next), true, nil
}

func (r SeriesCategoryRevision) Validate() error {
	if err := validateSeriesCategoryRevisionIdentity(r); err != nil {
		return err
	}
	if err := validateSeriesCategoryRevisionMetadata(r); err != nil {
		return err
	}
	return validateSeriesCategoryRevisionPool(r)
}

func validateSeriesCategoryRevisionIdentity(r SeriesCategoryRevision) error {
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

func validateSeriesCategoryRevisionMetadata(r SeriesCategoryRevision) error {
	if !r.Stage.IsValid() || !r.Format.IsValid() || !r.Mode.IsValid() || r.SourceContentRevision < 1 {
		return seriesCategoryRevisionError("invalid stage, format, mode, or source revision")
	}
	return validateSeriesCategoryStage(r.Stage, r.Format, r.Mode)
}

func validateSeriesCategoryRevisionPool(r SeriesCategoryRevision) error {
	if r.CategoryPool.ID == uuid.Nil || r.CategoryPool.Revision < 1 || r.CategoryPool.Format != r.Format {
		return seriesCategoryRevisionError("invalid category pool identity or format")
	}
	normalized, err := normalizeCategories(r.CategoryPool.Format, r.CategoryPool.Categories)
	if err != nil || !reflect.DeepEqual(normalized, r.CategoryPool.Categories) {
		return seriesCategoryRevisionError("category pool is invalid or not normalized")
	}
	return nil
}

func validateSeriesCategoryRevisionCommand(command SeriesCategoryRevisionCommand) error {
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
		return fmt.Errorf("%w: content configuration: %w", ErrInvalidSeriesCategoryRevision, err)
	}
	if command.ModeOverride != nil && !command.ModeOverride.IsValid() {
		return seriesCategoryRevisionError("invalid category mode override")
	}
	return nil
}

func resolveSeriesCategoryMode(
	command SeriesCategoryRevisionCommand,
) (StageContentDefault, CategoryPoolRevision, error) {
	var stageDefault StageContentDefault
	for _, candidate := range command.Configuration.StageDefaults {
		if candidate.Stage == command.Stage {
			stageDefault = candidate
			break
		}
	}
	if !stageDefault.Stage.IsValid() {
		return StageContentDefault{}, CategoryPoolRevision{}, seriesCategoryRevisionError("stage default is missing")
	}
	if command.ModeOverride != nil {
		stageDefault.CategoryMode = *command.ModeOverride
	}
	if err := validateSeriesCategoryStage(command.Stage, stageDefault.Format, stageDefault.CategoryMode); err != nil {
		return StageContentDefault{}, CategoryPoolRevision{}, err
	}
	for _, pool := range command.Configuration.CategoryPools {
		if pool.ID == stageDefault.CategoryPoolRevisionID {
			return stageDefault, cloneCategoryPool(pool), nil
		}
	}
	return StageContentDefault{}, CategoryPoolRevision{}, seriesCategoryRevisionError("category pool is missing")
}

func validateSeriesCategoryStage(
	stage ArenaStage,
	format domain.ArenaSeriesFormat,
	mode domain.ArenaCategoryMode,
) error {
	if stage == ArenaStageFinal {
		if format != domain.ArenaSeriesFormatBO3 || mode != domain.ArenaCategoryModeDraft {
			return seriesCategoryRevisionError("final stage requires BO3 draft")
		}
		return nil
	}
	if format != domain.ArenaSeriesFormatBO1 {
		return seriesCategoryRevisionError("pre-final stage requires BO1")
	}
	return nil
}

func validateCurrentSeriesCategoryRevision(
	next SeriesCategoryRevision,
	current SeriesCategoryRevision,
) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w: current revision: %w", ErrInvalidSeriesCategoryRevision, err)
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
	next *SeriesCategoryRevision,
	current SeriesCategoryRevision,
) error {
	if next.ID == current.ID || !next.CreatedAt.After(current.CreatedAt) {
		return seriesCategoryRevisionError("successor identity or timestamp did not advance")
	}
	next.Revision = current.Revision + 1
	next.SupersedesRevisionID = current.ID
	return nil
}

func seriesCategoryRevisionContentEqual(first, second SeriesCategoryRevision) bool {
	return first.TournamentID == second.TournamentID && first.SeriesID == second.SeriesID &&
		first.RosterID == second.RosterID &&
		first.Stage == second.Stage && first.Format == second.Format && first.Mode == second.Mode &&
		first.SourceContentRevision == second.SourceContentRevision &&
		reflect.DeepEqual(first.CategoryPool, second.CategoryPool)
}

func cloneSeriesCategoryRevision(revision SeriesCategoryRevision) SeriesCategoryRevision {
	cloned := revision
	cloned.CategoryPool = cloneCategoryPool(revision.CategoryPool)
	return cloned
}

func cloneCategoryPool(pool CategoryPoolRevision) CategoryPoolRevision {
	cloned := pool
	cloned.Categories = append([]domain.Category(nil), pool.Categories...)
	return cloned
}

func seriesCategoryRevisionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesCategoryRevision, message)
}
