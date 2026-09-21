package domain

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/google/uuid"
)

var ErrInvalidContentConfiguration = errors.New("invalid tournament content configuration")

type TournamentStage string

const (
	TournamentStageSwiss     TournamentStage = "swiss"
	TournamentStageGolden    TournamentStage = "golden"
	TournamentStageSemifinal TournamentStage = "semifinal"
	TournamentStageFinal     TournamentStage = "final"
)

type CategoryPoolRevision struct {
	ID         uuid.UUID
	Revision   int64
	Format     SeriesFormat
	Categories []Category
}

type TaskVersionRef struct {
	TaskID  uuid.UUID
	Version int
}

func CompareTaskVersionRefs(first, second TaskVersionRef) int {
	if comparison := bytes.Compare(first.TaskID[:], second.TaskID[:]); comparison != 0 {
		return comparison
	}
	if first.Version < second.Version {
		return -1
	}
	if first.Version > second.Version {
		return 1
	}
	return 0
}

type TaskPoolRevision struct {
	ID       uuid.UUID
	Revision int64
	Kind     AssignmentTaskKind
	Versions []TaskVersionRef
}

type TaskVersionHealth struct {
	TaskID               uuid.UUID
	Version              int
	PoolRevisionID       uuid.UUID
	PoolKind             AssignmentTaskKind
	Exists               bool
	Enabled              bool
	Healthy              bool
	MutationLocked       bool
	PubliclyExposed      bool
	InternalHealthDetail string
}

type StageContentDefault struct {
	Stage                  TournamentStage
	Format                 SeriesFormat
	CategoryMode           CategoryMode
	CategoryPoolRevisionID uuid.UUID
	TaskPoolKind           AssignmentTaskKind
}

type ContentConfigurationInput struct {
	TournamentID uuid.UUID
	// ReserveCount controls the length of every normal and Golden assignment
	// chain after the primary task. It is deliberately shared by both pools so
	// a tournament cannot expose different reserve semantics by stage.
	ReserveCount  int
	CategoryPools []CategoryPoolRevision
	NormalPool    TaskPoolRevision
	GoldenPool    TaskPoolRevision
	StageDefaults []StageContentDefault
}

type ContentConfiguration struct {
	TournamentID        uuid.UUID
	Revision            int64
	ReserveCount        int
	CategoryPools       []CategoryPoolRevision
	NormalPool          TaskPoolRevision
	GoldenPool          TaskPoolRevision
	StageDefaults       []StageContentDefault
	PlanRevisionID      uuid.UUID
	PreflightRevisionID uuid.UUID
}

func (s TournamentStage) IsValid() bool {
	switch s {
	case TournamentStageSwiss, TournamentStageGolden, TournamentStageSemifinal, TournamentStageFinal:
		return true
	}
	return false
}

func CreateContentConfiguration(in ContentConfigurationInput) (ContentConfiguration, error) {
	normalized, err := normalizeContentConfigurationInput(in)
	if err != nil {
		return ContentConfiguration{}, err
	}
	return contentConfigurationFromInput(normalized, 1), nil
}

func ReviseContentConfiguration(
	current ContentConfiguration,
	in ContentConfigurationInput,
) (ContentConfiguration, bool, error) {
	if err := current.Validate(); err != nil {
		return ContentConfiguration{}, false, err
	}
	normalized, err := normalizeContentConfigurationInput(in)
	if err != nil {
		return ContentConfiguration{}, false, err
	}
	if normalized.TournamentID != current.TournamentID {
		return ContentConfiguration{}, false, contentConfigurationError("tournament identity changed")
	}

	currentInput := contentConfigurationInput(current)
	if reflect.DeepEqual(currentInput, normalized) {
		return cloneContentConfiguration(current), false, nil
	}
	if err := validateContentRevisionAdvance(currentInput, normalized); err != nil {
		return ContentConfiguration{}, false, err
	}

	next := contentConfigurationFromInput(normalized, current.Revision+1)
	return next, true, nil
}

func (c ContentConfiguration) WithDerivedRevisions(
	planRevisionID uuid.UUID,
	preflightRevisionID uuid.UUID,
) (ContentConfiguration, error) {
	if err := c.Validate(); err != nil {
		return ContentConfiguration{}, err
	}
	if planRevisionID == uuid.Nil || preflightRevisionID == uuid.Nil || planRevisionID == preflightRevisionID {
		return ContentConfiguration{}, contentConfigurationError("invalid derived revision identity")
	}
	c.PlanRevisionID = planRevisionID
	c.PreflightRevisionID = preflightRevisionID
	return cloneContentConfiguration(c), nil
}

func (c ContentConfiguration) Validate() error {
	if c.TournamentID == uuid.Nil || c.Revision < 1 {
		return contentConfigurationError("invalid tournament configuration identity")
	}
	if (c.PlanRevisionID == uuid.Nil) != (c.PreflightRevisionID == uuid.Nil) ||
		(c.PlanRevisionID != uuid.Nil && c.PlanRevisionID == c.PreflightRevisionID) {
		return contentConfigurationError("incomplete derived revision evidence")
	}
	normalized, err := normalizeContentConfigurationInput(contentConfigurationInput(c))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(contentConfigurationInput(c), normalized) {
		return contentConfigurationError("configuration is not normalized")
	}
	return nil
}

func normalizeContentConfigurationInput(in ContentConfigurationInput) (ContentConfigurationInput, error) {
	if in.TournamentID == uuid.Nil {
		return ContentConfigurationInput{}, contentConfigurationError("missing tournament identity")
	}
	if err := ValidateAssignmentReserveCount(in.ReserveCount); err != nil {
		return ContentConfigurationInput{}, err
	}
	categoryPools, err := NormalizeCategoryPoolRevisions(in.CategoryPools)
	if err != nil {
		return ContentConfigurationInput{}, err
	}
	normalPool, err := NormalizeTaskPoolRevision(in.NormalPool, AssignmentTaskKindNormal)
	if err != nil {
		return ContentConfigurationInput{}, err
	}
	goldenPool, err := NormalizeTaskPoolRevision(in.GoldenPool, AssignmentTaskKindGolden)
	if err != nil {
		return ContentConfigurationInput{}, err
	}
	if TaskPoolsOverlap(normalPool, goldenPool) {
		return ContentConfigurationInput{}, contentConfigurationError("normal and Golden pools overlap")
	}
	defaults, err := normalizeStageContentDefaults(in.StageDefaults, categoryPools)
	if err != nil {
		return ContentConfigurationInput{}, err
	}
	return ContentConfigurationInput{
		TournamentID:  in.TournamentID,
		ReserveCount:  in.ReserveCount,
		CategoryPools: categoryPools,
		NormalPool:    normalPool,
		GoldenPool:    goldenPool,
		StageDefaults: defaults,
	}, nil
}

func NormalizeCategoryPoolRevisions(pools []CategoryPoolRevision) ([]CategoryPoolRevision, error) {
	if len(pools) != 2 {
		return nil, contentConfigurationError("BO1 and BO3 category pools are required")
	}
	result := make([]CategoryPoolRevision, len(pools))
	seenFormats := make(map[SeriesFormat]struct{}, len(pools))
	seenIDs := make(map[uuid.UUID]struct{}, len(pools))
	for i, pool := range pools {
		if pool.ID == uuid.Nil || pool.Revision < 1 || !pool.Format.IsValid() {
			return nil, contentConfigurationError("invalid category pool revision")
		}
		if _, duplicate := seenIDs[pool.ID]; duplicate {
			return nil, contentConfigurationError("duplicate category pool identity")
		}
		if _, duplicate := seenFormats[pool.Format]; duplicate {
			return nil, contentConfigurationError("duplicate category pool format")
		}
		categories, err := NormalizeCategories(pool.Format, pool.Categories)
		if err != nil {
			return nil, err
		}
		seenIDs[pool.ID] = struct{}{}
		seenFormats[pool.Format] = struct{}{}
		result[i] = CategoryPoolRevision{
			ID: pool.ID, Revision: pool.Revision, Format: pool.Format, Categories: categories,
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Format < result[j].Format })
	return result, nil
}

func NormalizeCategories(format SeriesFormat, categories []Category) ([]Category, error) {
	expected := format.WinsRequired()*2 + 1
	if expected == 1 || len(categories) != expected {
		return nil, contentConfigurationError(fmt.Sprintf("%s category pool must contain %d categories", format, expected))
	}
	result := append([]Category(nil), categories...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for i, category := range result {
		if !category.IsValid() {
			return nil, contentConfigurationError("category pool contains an unknown category")
		}
		if i > 0 && result[i-1] == category {
			return nil, contentConfigurationError("category pool contains a duplicate")
		}
	}
	return result, nil
}

func NormalizeTaskPoolRevision(pool TaskPoolRevision, wantKind AssignmentTaskKind) (TaskPoolRevision, error) {
	if pool.ID == uuid.Nil || pool.Revision < 1 || pool.Kind != wantKind || len(pool.Versions) == 0 {
		return TaskPoolRevision{}, contentConfigurationError(fmt.Sprintf("invalid %s task pool revision", wantKind))
	}
	result := pool
	result.Versions = append([]TaskVersionRef(nil), pool.Versions...)
	sort.Slice(result.Versions, func(i, j int) bool {
		return CompareTaskVersionRefs(result.Versions[i], result.Versions[j]) < 0
	})
	for i, version := range result.Versions {
		if version.TaskID == uuid.Nil || version.Version < 1 {
			return TaskPoolRevision{}, contentConfigurationError("invalid task version reference")
		}
		if i > 0 && result.Versions[i-1].TaskID == version.TaskID {
			return TaskPoolRevision{}, contentConfigurationError("task appears more than once in a pool")
		}
	}
	return result, nil
}

func TaskPoolsOverlap(normalPool TaskPoolRevision, goldenPool TaskPoolRevision) bool {
	normalTasks := make(map[uuid.UUID]struct{}, len(normalPool.Versions))
	for _, version := range normalPool.Versions {
		normalTasks[version.TaskID] = struct{}{}
	}
	for _, version := range goldenPool.Versions {
		if _, exists := normalTasks[version.TaskID]; exists {
			return true
		}
	}
	return false
}

func normalizeStageContentDefaults(
	defaults []StageContentDefault,
	categoryPools []CategoryPoolRevision,
) ([]StageContentDefault, error) {
	if len(defaults) != 4 {
		return nil, contentConfigurationError("every tournament stage requires a content default")
	}
	poolsByID := make(map[uuid.UUID]CategoryPoolRevision, len(categoryPools))
	for _, pool := range categoryPools {
		poolsByID[pool.ID] = pool
	}
	result := append([]StageContentDefault(nil), defaults...)
	sort.Slice(result, func(i, j int) bool { return result[i].Stage < result[j].Stage })
	for i, item := range result {
		if i > 0 && result[i-1].Stage == item.Stage {
			return nil, contentConfigurationError("duplicate stage default")
		}
		if err := validateStageContentDefault(item, poolsByID); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateStageContentDefault(
	item StageContentDefault,
	poolsByID map[uuid.UUID]CategoryPoolRevision,
) error {
	if !item.Stage.IsValid() || !item.Format.IsValid() || !item.CategoryMode.IsValid() {
		return contentConfigurationError("invalid stage default")
	}
	pool, exists := poolsByID[item.CategoryPoolRevisionID]
	if !exists || pool.Format != item.Format {
		return contentConfigurationError("stage default references the wrong category pool")
	}
	if err := validateStageTaskPool(item); err != nil {
		return err
	}
	if item.Stage == TournamentStageFinal && item.Format != SeriesFormatBO3 {
		return contentConfigurationError("final stage requires BO3")
	}
	if item.Stage != TournamentStageFinal && item.Format != SeriesFormatBO1 {
		return contentConfigurationError("pre-final stages require BO1")
	}
	return nil
}

func validateStageTaskPool(item StageContentDefault) error {
	if item.Stage == TournamentStageGolden {
		if item.TaskPoolKind != AssignmentTaskKindGolden {
			return contentConfigurationError("Golden stage requires the Golden task pool")
		}
		return nil
	}
	if item.TaskPoolKind != AssignmentTaskKindNormal {
		return contentConfigurationError("normal stages require the normal task pool")
	}
	return nil
}

func validateContentRevisionAdvance(current ContentConfigurationInput, next ContentConfigurationInput) error {
	for i := range current.CategoryPools {
		if err := validatePoolRevisionAdvance(
			current.CategoryPools[i].ID,
			current.CategoryPools[i].Revision,
			next.CategoryPools[i].ID,
			next.CategoryPools[i].Revision,
			reflect.DeepEqual(current.CategoryPools[i], next.CategoryPools[i]),
		); err != nil {
			return err
		}
	}
	for _, pools := range [][2]TaskPoolRevision{
		{current.NormalPool, next.NormalPool},
		{current.GoldenPool, next.GoldenPool},
	} {
		if err := validatePoolRevisionAdvance(
			pools[0].ID,
			pools[0].Revision,
			pools[1].ID,
			pools[1].Revision,
			reflect.DeepEqual(pools[0], pools[1]),
		); err != nil {
			return err
		}
	}
	return nil
}

func validatePoolRevisionAdvance(
	currentID uuid.UUID,
	currentRevision int64,
	nextID uuid.UUID,
	nextRevision int64,
	unchanged bool,
) error {
	if unchanged {
		return nil
	}
	if nextID == currentID || nextRevision != currentRevision+1 {
		return contentConfigurationError("changed pool requires a new consecutive revision")
	}
	return nil
}

func contentConfigurationFromInput(in ContentConfigurationInput, revision int64) ContentConfiguration {
	return ContentConfiguration{
		TournamentID:  in.TournamentID,
		Revision:      revision,
		ReserveCount:  in.ReserveCount,
		CategoryPools: cloneCategoryPools(in.CategoryPools),
		NormalPool:    CloneTaskPool(in.NormalPool),
		GoldenPool:    CloneTaskPool(in.GoldenPool),
		StageDefaults: append([]StageContentDefault(nil), in.StageDefaults...),
	}
}

func contentConfigurationInput(c ContentConfiguration) ContentConfigurationInput {
	return ContentConfigurationInput{
		TournamentID:  c.TournamentID,
		ReserveCount:  c.ReserveCount,
		CategoryPools: cloneCategoryPools(c.CategoryPools),
		NormalPool:    CloneTaskPool(c.NormalPool),
		GoldenPool:    CloneTaskPool(c.GoldenPool),
		StageDefaults: append([]StageContentDefault(nil), c.StageDefaults...),
	}
}

// ValidateAssignmentReserveCount accepts the only supported assignment
// chain shapes. The primary task is not part of this value: zero means a
// primary-only chain, one and two add that many undisclosed reserves.
func ValidateAssignmentReserveCount(count int) error {
	if count < 0 || count > MaxAssignmentReserveCount {
		return contentConfigurationError("assignment reserve count must be between zero and two")
	}
	return nil
}

func cloneContentConfiguration(c ContentConfiguration) ContentConfiguration {
	cloned := c
	cloned.CategoryPools = cloneCategoryPools(c.CategoryPools)
	cloned.NormalPool = CloneTaskPool(c.NormalPool)
	cloned.GoldenPool = CloneTaskPool(c.GoldenPool)
	cloned.StageDefaults = append([]StageContentDefault(nil), c.StageDefaults...)
	return cloned
}

func cloneCategoryPools(pools []CategoryPoolRevision) []CategoryPoolRevision {
	cloned := make([]CategoryPoolRevision, len(pools))
	for i, pool := range pools {
		cloned[i] = pool
		cloned[i].Categories = append([]Category(nil), pool.Categories...)
	}
	return cloned
}

func CloneTaskPool(pool TaskPoolRevision) TaskPoolRevision {
	cloned := pool
	cloned.Versions = append([]TaskVersionRef(nil), pool.Versions...)
	return cloned
}

func contentConfigurationError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidContentConfiguration, message)
}
