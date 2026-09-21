package capacity_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func TestNormalAssignmentSolver(t *testing.T) {
	t.Parallel()

	t.Run("certifies both Swiss round bounds", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			rosterSize int
			rounds     int
			required   int
		}{
			{rosterSize: 4, rounds: 3, required: 27},
			{rosterSize: 8, rounds: 3, required: 45},
			{rosterSize: 9, rounds: 4, required: 57},
			{rosterSize: 16, rounds: 4, required: 105},
		} {
			proof := capacity.ProveNormal(task021NormalCapacityInput(test.rosterSize))
			if !proof.Certified || proof.Failure != nil || proof.SwissRounds != test.rounds {
				t.Fatalf("roster %d proof = %+v, want %d rounds", test.rosterSize, proof, test.rounds)
			}
			for _, category := range proof.Categories {
				if category.Category == domain.CategoryWeb && category.RequiredTaskVersions != test.required {
					t.Fatalf("roster %d web requirement = %d, want %d", test.rosterSize, category.RequiredTaskVersions, test.required)
				}
			}
		}
	})

	t.Run("scales the complete 4-player proof with the shared reserve count", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			reserveCount int
			wantTotal    int
		}{
			{reserveCount: 0, wantTotal: 29},
			{reserveCount: 1, wantTotal: 58},
			{reserveCount: 2, wantTotal: 87},
		} {
			proof := capacity.ProveNormal(task021NormalCapacityInputWithReserveCount(4, test.reserveCount))
			if !proof.Certified || proof.ReserveCount != test.reserveCount {
				t.Fatalf("reserve_count=%d proof = %+v, want certified", test.reserveCount, proof)
			}
			total := 0
			for _, category := range proof.Categories {
				total += category.RequiredTaskVersions
			}
			if total != test.wantTotal {
				t.Fatalf("reserve_count=%d total required versions = %d, want %d", test.reserveCount, total, test.wantTotal)
			}
		}
	})

	t.Run("certifies every reachable normal category with a reproducible constraint graph", func(t *testing.T) {
		t.Parallel()

		input := task021NormalCapacityInput(16)
		proof := capacity.ProveNormal(input)
		if !proof.Certified || proof.Failure != nil {
			t.Fatalf("proof = %+v, want certified", proof)
		}
		if proof.RosterSize != 16 || proof.SwissRounds != 4 || proof.Digest == "" {
			t.Fatalf("proof identity = roster %d rounds %d digest %q", proof.RosterSize, proof.SwissRounds, proof.Digest)
		}
		for _, category := range proof.Categories {
			for _, graph := range category.Graphs {
				if graph.AlgorithmVersion != capacity.NormalGraphAlgorithmV2 {
					t.Fatalf("normal graph algorithm = %q, want %q", graph.AlgorithmVersion, capacity.NormalGraphAlgorithmV2)
				}
			}
		}

		want := map[domain.Category]struct {
			peak           int
			perParticipant int
			required       int
		}{
			domain.CategoryWeb:       {peak: 105, perParticipant: 18, required: 105},
			domain.CategoryCrypto:    {peak: 105, perParticipant: 18, required: 105},
			domain.CategoryReverse:   {peak: 105, perParticipant: 18, required: 105},
			domain.CategoryPwn:       {peak: 3, perParticipant: 3, required: 3},
			domain.CategoryForensics: {peak: 3, perParticipant: 3, required: 3},
		}
		if len(proof.Categories) != len(want) {
			t.Fatalf("category proofs = %d, want %d", len(proof.Categories), len(want))
		}
		for _, category := range proof.Categories {
			expected, exists := want[category.Category]
			if !exists {
				t.Fatalf("unexpected category proof: %+v", category)
			}
			if category.PeakReservations != expected.peak ||
				category.PerParticipantReservations != expected.perParticipant ||
				category.RequiredTaskVersions != expected.required ||
				category.AvailableTaskVersions != expected.required {
				t.Fatalf("category %s proof = %+v, want peak %d participant %d required %d", category.Category, category, expected.peak, expected.perParticipant, expected.required)
			}
			if len(category.Graphs) != len(input.ParticipantIDs)+1 {
				t.Fatalf("category %s graphs = %d, want %d", category.Category, len(category.Graphs), len(input.ParticipantIDs)+1)
			}
			for _, graph := range category.Graphs {
				if graph.AlgorithmVersion != capacity.NormalGraphAlgorithmV2 || graph.Digest == "" || len(graph.SelectedEdges) != graph.Required {
					t.Fatalf("invalid graph proof: %+v", graph)
				}
			}
		}

		reordered := cloneNormalCapacityInput(input)
		slices.Reverse(reordered.ParticipantIDs)
		slices.Reverse(reordered.CategoryPools)
		slices.Reverse(reordered.NormalPool.Versions)
		slices.Reverse(reordered.Versions)
		if other := capacity.ProveNormal(reordered); !reflect.DeepEqual(other, proof) {
			t.Fatalf("reordered proof differs:\n got  %+v\n want %+v", other, proof)
		}
	})

	t.Run("returns a stable reserve shortage before producing a partial proof", func(t *testing.T) {
		t.Parallel()

		input := task021NormalCapacityInput(4)
		for len(webCapacityVersions(input.Versions)) > 15 {
			input = removeCapacityVersion(input, domain.CategoryWeb)
		}
		proof := capacity.ProveNormal(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureNormalReserveShortage, domain.CategoryWeb, uuid.Nil, 27, 15)
		if len(proof.Categories) != 0 || proof.Digest != "" {
			t.Fatalf("shortage leaked a partial proof: %+v", proof)
		}
	})

	t.Run("rejects participant history that makes a longest path reuse a task", func(t *testing.T) {
		t.Parallel()

		input := task021NormalCapacityInput(16)
		participantID := input.ParticipantIDs[0]
		web := webCapacityVersions(input.Versions)
		input.History = []capacity.TaskUse{{
			ParticipantID: participantID,
			TaskID:        web[0].TaskID,
		}}
		proof := capacity.ProveNormal(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureNormalReuseConflict, domain.CategoryWeb, participantID, 105, 104)
	})

	t.Run("attributes exact-version roster conflict to the matching participant", func(t *testing.T) {
		t.Parallel()

		input := task021NormalCapacityInput(16)
		web := webCapacityVersions(input.Versions)
		for index := range input.Versions {
			if input.Versions[index].TaskID != web[0].TaskID {
				continue
			}
			input.Versions[index].Version = 2
			for poolIndex := range input.NormalPool.Versions {
				if input.NormalPool.Versions[poolIndex].TaskID == web[0].TaskID {
					input.NormalPool.Versions[poolIndex].Version = 2
				}
			}
			break
		}
		web = webCapacityVersions(input.Versions)
		input.History = []capacity.TaskUse{{
			ParticipantID: input.ParticipantIDs[0], TaskID: web[0].TaskID, Version: web[0].Version - 1,
		}}
		input.History = append(input.History, capacity.TaskUse{
			ParticipantID: input.ParticipantIDs[1], TaskID: web[1].TaskID, Version: web[1].Version,
		})
		proof := capacity.ProveNormal(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureNormalReuseConflict, domain.CategoryWeb, input.ParticipantIDs[1], 105, 104)
	})
}

func TestNormalizeHistoryKeepsTaskVersionsDistinctAndLegacyEntriesWildcard(t *testing.T) {
	t.Parallel()

	participantID := task021ID(900)
	taskID := task021ID(901)
	history, ok := capacity.NormalizeHistory([]uuid.UUID{participantID}, []capacity.TaskUse{
		{ParticipantID: participantID, TaskID: taskID, Version: 1},
		{ParticipantID: participantID, TaskID: taskID, Version: 2},
	})
	if !ok {
		t.Fatal("distinct task versions were rejected")
	}
	if _, exists := history[participantID][domain.TaskVersionRef{TaskID: taskID, Version: 1}]; !exists {
		t.Fatal("version 1 history was not retained")
	}
	if _, exists := history[participantID][domain.TaskVersionRef{TaskID: taskID, Version: 2}]; !exists {
		t.Fatal("version 2 history was not retained")
	}

	if _, ok := capacity.NormalizeHistory([]uuid.UUID{participantID}, []capacity.TaskUse{
		{ParticipantID: participantID, TaskID: taskID, Version: 1},
		{ParticipantID: participantID, TaskID: taskID, Version: 1},
	}); ok {
		t.Fatal("duplicate participant and task version was accepted")
	}
}

func task021NormalCapacityInput(rosterSize int) capacity.NormalInput {
	return task021NormalCapacityInputWithReserveCount(rosterSize, domain.AssignmentReserveCount)
}

func task021NormalCapacityInputWithReserveCount(rosterSize, reserveCount int) capacity.NormalInput {
	participants := make([]uuid.UUID, rosterSize)
	for i := range participants {
		participants[i] = task021ID(100 + i)
	}

	bo1PoolID := task021ID(10)
	bo3PoolID := task021ID(11)
	normalPoolID := task021ID(20)
	categoryPools := []domain.CategoryPoolRevision{
		{
			ID: bo1PoolID, Revision: 2, Format: domain.SeriesFormatBO1,
			Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse},
		},
		{
			ID: bo3PoolID, Revision: 3, Format: domain.SeriesFormatBO3,
			Categories: []domain.Category{
				domain.CategoryWeb,
				domain.CategoryCrypto,
				domain.CategoryReverse,
				domain.CategoryPwn,
				domain.CategoryForensics,
			},
		},
	}

	swissRounds, err := domain.TournamentPresetV1.SwissRounds(rosterSize)
	if err != nil {
		panic(err)
	}
	sharedCategoryCount := (rosterSize/2*swissRounds + 3) * (reserveCount + 1)
	versions := make([]capacity.TaskVersion, 0, sharedCategoryCount*3+6)
	for _, category := range []struct {
		category domain.Category
		count    int
	}{
		{domain.CategoryWeb, sharedCategoryCount},
		{domain.CategoryCrypto, sharedCategoryCount},
		{domain.CategoryReverse, sharedCategoryCount},
		{domain.CategoryPwn, 3},
		{domain.CategoryForensics, 3},
	} {
		for i := 0; i < category.count; i++ {
			versions = append(versions, capacity.TaskVersion{
				TaskVersionRef: domain.TaskVersionRef{TaskID: task021ID(1000 + len(versions)), Version: 1},
				PoolRevisionID: normalPoolID,
				PoolKind:       domain.AssignmentTaskKindNormal,
				Category:       category.category,
			})
		}
	}
	poolRefs := make([]domain.TaskVersionRef, len(versions))
	for i, version := range versions {
		poolRefs[i] = version.TaskVersionRef
	}

	return capacity.NormalInput{
		Preset:         domain.TournamentPresetV1,
		ReserveCount:   reserveCount,
		ParticipantIDs: participants,
		CategoryPools:  categoryPools,
		NormalPool: domain.TaskPoolRevision{
			ID: normalPoolID, Revision: 7, Kind: domain.AssignmentTaskKindNormal, Versions: poolRefs,
		},
		Versions: versions,
	}
}

func cloneNormalCapacityInput(input capacity.NormalInput) capacity.NormalInput {
	clone := input
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	clone.CategoryPools = append([]domain.CategoryPoolRevision(nil), input.CategoryPools...)
	for i := range clone.CategoryPools {
		clone.CategoryPools[i].Categories = append([]domain.Category(nil), input.CategoryPools[i].Categories...)
	}
	clone.NormalPool.Versions = append([]domain.TaskVersionRef(nil), input.NormalPool.Versions...)
	clone.Versions = append([]capacity.TaskVersion(nil), input.Versions...)
	clone.History = append([]capacity.TaskUse(nil), input.History...)
	return clone
}

func removeCapacityVersion(input capacity.NormalInput, category domain.Category) capacity.NormalInput {
	for i := len(input.Versions) - 1; i >= 0; i-- {
		if input.Versions[i].Category != category {
			continue
		}
		removed := input.Versions[i].TaskVersionRef
		input.Versions = append(input.Versions[:i], input.Versions[i+1:]...)
		for j, ref := range input.NormalPool.Versions {
			if ref == removed {
				input.NormalPool.Versions = append(input.NormalPool.Versions[:j], input.NormalPool.Versions[j+1:]...)
				return input
			}
		}
	}
	return input
}

func webCapacityVersions(versions []capacity.TaskVersion) []capacity.TaskVersion {
	result := make([]capacity.TaskVersion, 0)
	for _, version := range versions {
		if version.Category == domain.CategoryWeb {
			result = append(result, version)
		}
	}
	return result
}

func assertCapacityFailure(
	t *testing.T,
	certified bool,
	failure *capacity.Failure,
	wantCode capacity.FailureCode,
	wantCategory domain.Category,
	wantParticipantID uuid.UUID,
	wantRequired int,
	wantAvailable int,
) {
	t.Helper()
	if certified || failure == nil {
		t.Fatalf("certified = %v, failure = %+v", certified, failure)
	}
	if failure.Code != wantCode || failure.Category != wantCategory || failure.ParticipantID != wantParticipantID ||
		failure.Required != wantRequired || failure.Available != wantAvailable {
		t.Fatalf("failure = %+v, want code %s category %s participant %s required %d available %d", failure, wantCode, wantCategory, wantParticipantID, wantRequired, wantAvailable)
	}
}

func task021ID(suffix int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("21000000-0000-0000-0000-%012d", suffix))
}
