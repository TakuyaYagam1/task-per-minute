package arena_test

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenAssignmentSolver(t *testing.T) {
	t.Parallel()

	for _, rosterSize := range []int{4, 8, 16} {
		t.Run("certifies every tied membership shape for roster "+strconv.Itoa(rosterSize), func(t *testing.T) {
			t.Parallel()

			input := task021GoldenCapacityInput(rosterSize)
			proof := arena.ProveGoldenCapacity(input)
			if !proof.Certified || proof.Failure != nil || proof.Digest == "" {
				t.Fatalf("proof = %+v, want certified", proof)
			}
			if len(proof.GroupShapes) != rosterSize-1 {
				t.Fatalf("group shapes = %d, want %d", len(proof.GroupShapes), rosterSize-1)
			}
			for i, graph := range proof.GroupShapes {
				groupSize := i + 2
				wantConcurrent := 1 + (rosterSize-groupSize)/2
				wantRequired := wantConcurrent * (domain.ArenaAssignmentReserveCount + 1)
				if graph.GroupSize != groupSize || graph.MaxConcurrentGroups != wantConcurrent || graph.Graph.Required != wantRequired {
					t.Fatalf("group shape %d = %+v, want concurrent %d required %d", groupSize, graph, wantConcurrent, wantRequired)
				}
				if graph.Graph.AlgorithmVersion != arena.CapacityGraphAlgorithmV1 || graph.Graph.Digest == "" {
					t.Fatalf("invalid group graph: %+v", graph.Graph)
				}
			}

			reordered := cloneGoldenCapacityInput(input)
			slices.Reverse(reordered.ParticipantIDs)
			slices.Reverse(reordered.NormalPool.Versions)
			slices.Reverse(reordered.GoldenPool.Versions)
			slices.Reverse(reordered.Versions)
			if other := arena.ProveGoldenCapacity(reordered); !reflect.DeepEqual(other, proof) {
				t.Fatalf("reordered proof differs:\n got  %+v\n want %+v", other, proof)
			}
		})
	}

	t.Run("rejects overlap with the normal pool", func(t *testing.T) {
		t.Parallel()

		input := task021GoldenCapacityInput(4)
		input.NormalPool.Versions = append(input.NormalPool.Versions, input.GoldenPool.Versions[0])
		proof := arena.ProveGoldenCapacity(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, arena.CapacityFailureGoldenPoolOverlap, "", uuid.Nil, 0, 0)
	})

	t.Run("rejects history that prevents a safe proof for every membership", func(t *testing.T) {
		t.Parallel()

		input := task021GoldenCapacityInput(16)
		input.History = []arena.CapacityTaskUse{{
			ParticipantID: input.ParticipantIDs[0],
			TaskID:        input.Versions[0].TaskID,
		}}
		proof := arena.ProveGoldenCapacity(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, arena.CapacityFailureGoldenReuseConflict, "", input.ParticipantIDs[0], 24, 23)
	})
}

func task021GoldenCapacityInput(rosterSize int) arena.GoldenCapacityInput {
	participants := make([]uuid.UUID, rosterSize)
	for i := range participants {
		participants[i] = task021ID(200 + i)
	}
	goldenPoolID := task021ID(30)
	required := rosterSize / 2 * (domain.ArenaAssignmentReserveCount + 1)
	versions := make([]arena.CapacityTaskVersion, required)
	refs := make([]arena.TaskVersionRef, required)
	for i := range versions {
		ref := arena.TaskVersionRef{TaskID: task021ID(2000 + i), Version: 2}
		refs[i] = ref
		versions[i] = arena.CapacityTaskVersion{
			TaskVersionRef: ref,
			PoolRevisionID: goldenPoolID,
			PoolKind:       domain.ArenaTaskKindGolden,
			Category:       domain.CategoryMisc,
		}
	}
	return arena.GoldenCapacityInput{
		Preset:         domain.ArenaPresetV1,
		ParticipantIDs: participants,
		NormalPool: arena.TaskPoolRevision{
			ID: task021ID(20), Revision: 7, Kind: domain.ArenaTaskKindNormal,
			Versions: []arena.TaskVersionRef{{TaskID: task021ID(3000), Version: 1}},
		},
		GoldenPool: arena.TaskPoolRevision{
			ID: goldenPoolID, Revision: 5, Kind: domain.ArenaTaskKindGolden, Versions: refs,
		},
		Versions: versions,
	}
}

func cloneGoldenCapacityInput(input arena.GoldenCapacityInput) arena.GoldenCapacityInput {
	clone := input
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	clone.NormalPool.Versions = append([]arena.TaskVersionRef(nil), input.NormalPool.Versions...)
	clone.GoldenPool.Versions = append([]arena.TaskVersionRef(nil), input.GoldenPool.Versions...)
	clone.Versions = append([]arena.CapacityTaskVersion(nil), input.Versions...)
	clone.History = append([]arena.CapacityTaskUse(nil), input.History...)
	return clone
}
