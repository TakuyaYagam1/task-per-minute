package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// ContentBinding is the immutable V1 content identity persisted with a draft
// tournament. Its fields are exported so the root compatibility facade can
// keep the historical private bridge signatures without importing this child
// package's implementation details.
type ContentBinding struct {
	TournamentID         uuid.UUID
	ConfigurationID      uuid.UUID
	ReserveCount         int16
	PublicationID        uuid.UUID
	NormalPoolRevisionID uuid.UUID
	GoldenPoolRevisionID uuid.UUID
	BO1CategoryPoolID    uuid.UUID
	BO3CategoryPoolID    uuid.UUID
}

type ContentPublicationPool struct {
	PublicationID       uuid.UUID
	PublicationRevision int64
	PublishedAt         time.Time
	PublishedAtValid    bool
	PoolRevisionID      uuid.UUID
	Kind                string
	PoolRevision        int64
}

// ContentBindingFromPools validates the selected publication and derives the
// deterministic content IDs used by a tournament draft.
//
//nolint:gocyclo // Pool identity and duplicate checks form one fail-closed validation boundary.
func ContentBindingFromPools(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	pools []ContentPublicationPool,
) (ContentBinding, error) {
	if len(pools) != 2 {
		return ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - content pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}

	binding := ContentBinding{
		TournamentID:      tournamentID,
		ConfigurationID:   ContentID(tournamentID, rosterID, "configuration"),
		ReserveCount:      0,
		BO1CategoryPoolID: ContentID(tournamentID, rosterID, "category-pool:bo1"),
		BO3CategoryPoolID: ContentID(tournamentID, rosterID, "category-pool:bo3"),
	}
	first := pools[0]
	for _, pool := range pools {
		if pool.PublicationID == uuid.Nil || pool.PoolRevisionID == uuid.Nil ||
			pool.PublicationID != first.PublicationID ||
			pool.PublicationRevision != first.PublicationRevision ||
			pool.PublicationRevision < 1 || pool.PoolRevision != pool.PublicationRevision ||
			!pool.PublishedAtValid {
			return ContentBinding{}, fmt.Errorf(
				"TournamentPostgres - invalid task pools: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		binding.PublicationID = pool.PublicationID
		switch domain.AssignmentTaskKind(pool.Kind) {
		case domain.AssignmentTaskKindNormal:
			if binding.NormalPoolRevisionID != uuid.Nil {
				return ContentBinding{}, fmt.Errorf(
					"TournamentPostgres - duplicate normal task pool: %w", domain.ErrInvalidContentConfiguration,
				)
			}
			binding.NormalPoolRevisionID = pool.PoolRevisionID
		case domain.AssignmentTaskKindGolden:
			if binding.GoldenPoolRevisionID != uuid.Nil {
				return ContentBinding{}, fmt.Errorf(
					"TournamentPostgres - duplicate golden task pool: %w", domain.ErrInvalidContentConfiguration,
				)
			}
			binding.GoldenPoolRevisionID = pool.PoolRevisionID
		default:
			return ContentBinding{}, fmt.Errorf(
				"TournamentPostgres - unknown task pool: %w", domain.ErrInvalidContentConfiguration,
			)
		}
	}
	if binding.NormalPoolRevisionID == uuid.Nil || binding.GoldenPoolRevisionID == uuid.Nil ||
		binding.NormalPoolRevisionID == binding.GoldenPoolRevisionID {
		return ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - incomplete task pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}
	return binding, nil
}

func LoadContentBinding(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	contentRevision int64,
) (ContentBinding, error) {
	publication, err := querier.LockTaskPoolPublicationRevision(ctx, contentRevision)
	if err != nil {
		return ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - Create - lock current task pools: %w", err,
		)
	}
	if len(publication) != 2 {
		return ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - Create - current task pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}

	pools := make([]ContentPublicationPool, len(publication))
	for index, pool := range publication {
		pools[index] = ContentPublicationPool{
			PublicationID:       pool.PublicationID,
			PublicationRevision: pool.PublicationRevision,
			PublishedAt:         pool.PublishedAt.Time,
			PublishedAtValid:    pool.PublishedAt.Valid,
			PoolRevisionID:      pool.PoolRevisionID,
			Kind:                pool.Kind,
			PoolRevision:        pool.PoolRevision,
		}
	}
	return ContentBindingFromPools(tournamentID, rosterID, pools)
}

// RevalidateContentPools fail-closes the immutable task pool health check
// before a tournament draft is written.
//
//nolint:gocyclo // Health, identity, kind and duplicate checks must remain ordered in one boundary.
func RevalidateContentPools(
	ctx context.Context,
	querier *sqlc.Queries,
	binding ContentBinding,
) error {
	rows, err := querier.ListTaskPoolVersionHealth(
		ctx,
		[]uuid.UUID{binding.NormalPoolRevisionID, binding.GoldenPoolRevisionID},
	)
	if err != nil {
		return fmt.Errorf("TournamentPostgres - Create - task pool health: %w", err)
	}
	poolCounts := map[uuid.UUID]int{
		binding.NormalPoolRevisionID: 0,
		binding.GoldenPoolRevisionID: 0,
	}
	seenVersions := make(map[[2]uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 || row.PoolRevisionID == uuid.Nil ||
			!row.TaskExists || !row.TaskEnabled || !row.TaskHealthy || !row.TaskMutationLocked {
			return fmt.Errorf(
				"TournamentPostgres - Create - unhealthy task pool version: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		if _, exists := poolCounts[row.PoolRevisionID]; !exists {
			return fmt.Errorf(
				"TournamentPostgres - Create - foreign task pool version: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		wantKind := domain.AssignmentTaskKindNormal
		if row.PoolRevisionID == binding.GoldenPoolRevisionID {
			wantKind = domain.AssignmentTaskKindGolden
		}
		if domain.AssignmentTaskKind(row.PoolKind) != wantKind {
			return fmt.Errorf(
				"TournamentPostgres - Create - mismatched task pool kind: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		key := [2]uuid.UUID{row.TaskID, row.PoolRevisionID}
		if _, duplicate := seenVersions[key]; duplicate {
			return fmt.Errorf(
				"TournamentPostgres - Create - duplicate task pool version: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		seenVersions[key] = struct{}{}
		poolCounts[row.PoolRevisionID]++
	}
	if poolCounts[binding.NormalPoolRevisionID] == 0 || poolCounts[binding.GoldenPoolRevisionID] == 0 {
		return fmt.Errorf(
			"TournamentPostgres - Create - empty task pool: %w", domain.ErrInvalidContentConfiguration,
		)
	}
	return nil
}

func PersistContentBinding(
	ctx context.Context,
	querier *sqlc.Queries,
	binding ContentBinding,
	createdAt time.Time,
) error {
	bo1Categories := []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb}
	bo3Categories := []domain.Category{
		domain.CategoryCrypto,
		domain.CategoryForensics,
		domain.CategoryPwn,
		domain.CategoryReverse,
		domain.CategoryWeb,
	}
	if _, err := querier.CreateTournamentContentConfiguration(
		ctx,
		sqlc.CreateTournamentContentConfigurationParams{
			ID:                   binding.ConfigurationID,
			TournamentID:         binding.TournamentID,
			Revision:             1,
			ReserveCount:         binding.ReserveCount,
			PoolPublicationID:    binding.PublicationID,
			NormalPoolRevisionID: binding.NormalPoolRevisionID,
			GoldenPoolRevisionID: binding.GoldenPoolRevisionID,
			CreatedAt:            tstz(createdAt),
		},
	); err != nil {
		return fmt.Errorf("TournamentPostgres - Create - content configuration: %w", err)
	}
	for _, pool := range []struct {
		id         uuid.UUID
		format     domain.SeriesFormat
		categories []domain.Category
	}{
		{id: binding.BO1CategoryPoolID, format: domain.SeriesFormatBO1, categories: bo1Categories},
		{id: binding.BO3CategoryPoolID, format: domain.SeriesFormatBO3, categories: bo3Categories},
	} {
		if _, err := querier.CreateTournamentCategoryPoolRevision(
			ctx,
			sqlc.CreateTournamentCategoryPoolRevisionParams{
				ID:              pool.id,
				ConfigurationID: binding.ConfigurationID,
				Format:          string(pool.format),
				Revision:        1,
				CreatedAt:       tstz(createdAt),
			},
		); err != nil {
			return fmt.Errorf("TournamentPostgres - Create - category pool: %w", err)
		}
		for _, category := range pool.categories {
			if err := querier.CreateTournamentCategoryPoolMembership(
				ctx,
				sqlc.CreateTournamentCategoryPoolMembershipParams{
					CategoryPoolRevisionID: pool.id,
					Category:               string(category),
					CreatedAt:              tstz(createdAt),
				},
			); err != nil {
				return fmt.Errorf("TournamentPostgres - Create - category membership: %w", err)
			}
		}
	}
	for _, stageDefault := range []struct {
		stage          domain.TournamentStage
		format         domain.SeriesFormat
		mode           domain.CategoryMode
		categoryPoolID uuid.UUID
		taskPoolKind   domain.AssignmentTaskKind
		categories     []domain.Category
	}{
		{
			stage: domain.TournamentStageSwiss, format: domain.SeriesFormatBO1,
			mode: domain.CategoryModeRandom, categoryPoolID: binding.BO1CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindNormal, categories: bo1Categories[:1],
		},
		{
			stage: domain.TournamentStageGolden, format: domain.SeriesFormatBO1,
			mode: domain.CategoryModeRandom, categoryPoolID: binding.BO1CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindGolden, categories: bo1Categories[:1],
		},
		{
			stage: domain.TournamentStageSemifinal, format: domain.SeriesFormatBO1,
			mode: domain.CategoryModeDraft, categoryPoolID: binding.BO1CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindNormal, categories: bo1Categories,
		},
		{
			stage: domain.TournamentStageFinal, format: domain.SeriesFormatBO3,
			mode: domain.CategoryModeDraft, categoryPoolID: binding.BO3CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindNormal, categories: bo3Categories,
		},
	} {
		if err := querier.CreateTournamentContentStageDefault(
			ctx,
			sqlc.CreateTournamentContentStageDefaultParams{
				ConfigurationID:        binding.ConfigurationID,
				Stage:                  string(stageDefault.stage),
				Format:                 string(stageDefault.format),
				CategoryMode:           string(stageDefault.mode),
				CategoryPoolRevisionID: stageDefault.categoryPoolID,
				TaskPoolKind:           string(stageDefault.taskPoolKind),
				Categories:             categoriesJSON(stageDefault.categories),
				CreatedAt:              tstz(createdAt),
			},
		); err != nil {
			return fmt.Errorf("TournamentPostgres - Create - content stage default: %w", err)
		}
	}
	if _, err := querier.PublishTournamentContentConfiguration(
		ctx,
		sqlc.PublishTournamentContentConfigurationParams{ID: binding.ConfigurationID, PublishedAt: tstz(createdAt)},
	); err != nil {
		return fmt.Errorf("TournamentPostgres - Create - publish content configuration: %w", err)
	}
	return nil
}

func ContentID(tournamentID, rosterID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(tournamentID, []byte("tournament_v1:content:"+rosterID.String()+":"+role))
}

func categoriesJSON(categories []domain.Category) []byte {
	data, _ := json.Marshal(categories)
	return data
}
