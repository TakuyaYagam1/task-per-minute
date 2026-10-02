package capacity_test

import (
	"strconv"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func TestFixtureCatalogSupportsDefaultTournamentStages(t *testing.T) {
	t.Parallel()

	// These counts are also checked by test-data/importer/test_easy_tasks.py.
	counts := map[domain.Category]int{
		domain.CategoryCrypto: 35, domain.CategoryWeb: 4,
		domain.CategoryReverse: 3, domain.CategoryForensics: 1, domain.CategoryPwn: 1,
	}
	for _, rosterSize := range []int{8, 16} {
		t.Run(strconv.Itoa(rosterSize), func(t *testing.T) {
			t.Parallel()
			normal := task021NormalCapacityInputWithReserveCount(16, 0)
			normal.ParticipantIDs = normal.ParticipantIDs[:rosterSize]
			normal.StageCategories = map[domain.TournamentStage][]domain.Category{
				domain.TournamentStageSwiss: {domain.CategoryCrypto},
				domain.TournamentStageSemifinal: {
					domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb,
				},
				domain.TournamentStageFinal: {
					domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb,
					domain.CategoryForensics, domain.CategoryPwn,
				},
			}
			available := make(map[domain.Category]int)
			versions := make([]capacity.TaskVersion, 0, 44)
			normal.NormalPool.Versions = nil
			for _, version := range normal.Versions {
				if available[version.Category] >= counts[version.Category] {
					continue
				}
				available[version.Category]++
				versions = append(versions, version)
				normal.NormalPool.Versions = append(normal.NormalPool.Versions, version.TaskVersionRef)
			}
			normal.Versions = versions
			if proof := capacity.ProveNormal(normal); !proof.Certified {
				t.Fatalf("normal fixture catalog is insufficient: %+v", proof.Failure)
			}

			golden := task021GoldenCapacityInputWithReserveCount(16, 0)
			golden.ParticipantIDs = golden.ParticipantIDs[:rosterSize]
			golden.Category = domain.CategoryCrypto
			for index := range golden.Versions {
				golden.Versions[index].Category = domain.CategoryCrypto
			}
			if proof := capacity.ProveGolden(golden); !proof.Certified {
				t.Fatalf("Golden fixture catalog is insufficient: %+v", proof.Failure)
			}
		})
	}
}
