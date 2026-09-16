package configuration

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type tournamentV1ContentBinding struct {
	tournamentID         uuid.UUID
	configurationID      uuid.UUID
	publicationID        uuid.UUID
	normalPoolRevisionID uuid.UUID
	goldenPoolRevisionID uuid.UUID
	bo1CategoryPoolID    uuid.UUID
	bo3CategoryPoolID    uuid.UUID
}

type tournamentV1ContentPublicationPool struct {
	publicationID       uuid.UUID
	publicationRevision int64
	publishedAt         time.Time
	publishedAtValid    bool
	poolRevisionID      uuid.UUID
	kind                string
	poolRevision        int64
}

//nolint:gocyclo // One fail-closed validator keeps publication identity and pool-kind checks together.
func tournamentV1ContentBindingFromPools(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	pools []tournamentV1ContentPublicationPool,
) (tournamentV1ContentBinding, error) {
	if len(pools) != 2 {
		return tournamentV1ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - content pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}

	binding := tournamentV1ContentBinding{
		tournamentID:      tournamentID,
		configurationID:   tournamentV1ContentID(tournamentID, rosterID, "configuration"),
		bo1CategoryPoolID: tournamentV1ContentID(tournamentID, rosterID, "category-pool:bo1"),
		bo3CategoryPoolID: tournamentV1ContentID(tournamentID, rosterID, "category-pool:bo3"),
	}
	first := pools[0]
	for _, pool := range pools {
		if pool.publicationID == uuid.Nil || pool.poolRevisionID == uuid.Nil ||
			pool.publicationID != first.publicationID ||
			pool.publicationRevision != first.publicationRevision ||
			pool.publicationRevision < 1 || pool.poolRevision != pool.publicationRevision ||
			!pool.publishedAtValid {
			return tournamentV1ContentBinding{}, fmt.Errorf(
				"TournamentPostgres - invalid task pools: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		binding.publicationID = pool.publicationID
		switch domain.AssignmentTaskKind(pool.kind) {
		case domain.AssignmentTaskKindNormal:
			if binding.normalPoolRevisionID != uuid.Nil {
				return tournamentV1ContentBinding{}, fmt.Errorf(
					"TournamentPostgres - duplicate normal task pool: %w", domain.ErrInvalidContentConfiguration,
				)
			}
			binding.normalPoolRevisionID = pool.poolRevisionID
		case domain.AssignmentTaskKindGolden:
			if binding.goldenPoolRevisionID != uuid.Nil {
				return tournamentV1ContentBinding{}, fmt.Errorf(
					"TournamentPostgres - duplicate golden task pool: %w", domain.ErrInvalidContentConfiguration,
				)
			}
			binding.goldenPoolRevisionID = pool.poolRevisionID
		default:
			return tournamentV1ContentBinding{}, fmt.Errorf(
				"TournamentPostgres - unknown task pool: %w", domain.ErrInvalidContentConfiguration,
			)
		}
	}
	if binding.normalPoolRevisionID == uuid.Nil || binding.goldenPoolRevisionID == uuid.Nil ||
		binding.normalPoolRevisionID == binding.goldenPoolRevisionID {
		return tournamentV1ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - incomplete task pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}
	return binding, nil
}

//nolint:gocyclo // One cohesive validator rechecks every immutable published pool version.
func revalidateTournamentV1ContentPools(
	ctx context.Context,
	querier *sqlc.Queries,
	binding tournamentV1ContentBinding,
) error {
	rows, err := querier.ListTaskPoolVersionHealth(
		ctx,
		[]uuid.UUID{binding.normalPoolRevisionID, binding.goldenPoolRevisionID},
	)
	if err != nil {
		return fmt.Errorf("TournamentPostgres - Create - task pool health: %w", err)
	}
	poolCounts := map[uuid.UUID]int{
		binding.normalPoolRevisionID: 0,
		binding.goldenPoolRevisionID: 0,
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
		if row.PoolRevisionID == binding.goldenPoolRevisionID {
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
	if poolCounts[binding.normalPoolRevisionID] == 0 || poolCounts[binding.goldenPoolRevisionID] == 0 {
		return fmt.Errorf(
			"TournamentPostgres - Create - empty task pool: %w", domain.ErrInvalidContentConfiguration,
		)
	}
	return nil
}

func tournamentV1ContentID(tournamentID, rosterID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(tournamentID, []byte("tournament_v1:content:"+rosterID.String()+":"+role))
}
