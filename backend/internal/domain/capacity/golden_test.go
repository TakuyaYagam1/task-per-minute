package capacity_test

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func TestGoldenAssignmentSolver(t *testing.T) {
	t.Parallel()

	for _, rosterSize := range []int{4, 8, 16} {
		t.Run("certifies every tied membership shape for roster "+strconv.Itoa(rosterSize), func(t *testing.T) {
			t.Parallel()

			input := task021GoldenCapacityInput(rosterSize)
			proof := capacity.ProveGolden(input)
			if !proof.Certified || proof.Failure != nil || proof.Digest == "" {
				t.Fatalf("proof = %+v, want certified", proof)
			}
			if len(proof.GroupShapes) != rosterSize-1 {
				t.Fatalf("group shapes = %d, want %d", len(proof.GroupShapes), rosterSize-1)
			}
			for i, graph := range proof.GroupShapes {
				groupSize := i + 2
				wantConcurrent := 1 + (rosterSize-groupSize)/2
				wantRequired := wantConcurrent * (domain.AssignmentReserveCount + 1)
				if graph.GroupSize != groupSize || graph.MaxConcurrentGroups != wantConcurrent || graph.Graph.Required != wantRequired {
					t.Fatalf("group shape %d = %+v, want concurrent %d required %d", groupSize, graph, wantConcurrent, wantRequired)
				}
				if graph.Graph.AlgorithmVersion != capacity.GraphAlgorithmV1 || graph.Graph.Digest == "" {
					t.Fatalf("invalid group graph: %+v", graph.Graph)
				}
			}

			reordered := cloneGoldenCapacityInput(input)
			slices.Reverse(reordered.ParticipantIDs)
			slices.Reverse(reordered.NormalPool.Versions)
			slices.Reverse(reordered.GoldenPool.Versions)
			slices.Reverse(reordered.Versions)
			if other := capacity.ProveGolden(reordered); !reflect.DeepEqual(other, proof) {
				t.Fatalf("reordered proof differs:\n got  %+v\n want %+v", other, proof)
			}
		})
	}

	t.Run("rejects overlap with the normal pool", func(t *testing.T) {
		t.Parallel()

		input := task021GoldenCapacityInput(4)
		input.NormalPool.Versions = append(input.NormalPool.Versions, input.GoldenPool.Versions[0])
		proof := capacity.ProveGolden(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureGoldenPoolOverlap, "", uuid.Nil, 0, 0)
	})

	t.Run("rejects history that prevents a safe proof for every membership", func(t *testing.T) {
		t.Parallel()

		input := task021GoldenCapacityInput(16)
		input.History = []capacity.TaskUse{{
			ParticipantID: input.ParticipantIDs[0],
			TaskID:        input.Versions[0].TaskID,
		}}
		proof := capacity.ProveGolden(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureGoldenReuseConflict, "", input.ParticipantIDs[0], 24, 23)
	})

	t.Run("matches a prior receipt by exact version and keeps legacy task history broad", func(t *testing.T) {
		t.Parallel()

		input := task021GoldenCapacityInput(4)
		input.History = []capacity.TaskUse{{
			ParticipantID: input.ParticipantIDs[0], TaskID: input.Versions[0].TaskID, Version: input.Versions[0].Version - 1,
		}}
		proof := capacity.ProveGolden(input)
		if !proof.Certified {
			t.Fatalf("exact-version history rejected a distinct version: %+v", proof.Failure)
		}

		input.History[0].Version = 0
		proof = capacity.ProveGolden(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureGoldenReuseConflict, "", input.ParticipantIDs[0], 6, 5)
	})

	t.Run("attributes exact-version conflict to the matching participant", func(t *testing.T) {
		t.Parallel()

		input := task021GoldenCapacityInput(4)
		input.History = []capacity.TaskUse{
			{ParticipantID: input.ParticipantIDs[0], TaskID: input.Versions[0].TaskID, Version: input.Versions[0].Version - 1},
			{ParticipantID: input.ParticipantIDs[1], TaskID: input.Versions[1].TaskID, Version: input.Versions[1].Version},
		}
		proof := capacity.ProveGolden(input)
		assertCapacityFailure(t, proof.Certified, proof.Failure, capacity.FailureGoldenReuseConflict, "", input.ParticipantIDs[1], 6, 5)
	})
}

func task021GoldenCapacityInput(rosterSize int) capacity.GoldenInput {
	participants := make([]uuid.UUID, rosterSize)
	for i := range participants {
		participants[i] = task021ID(200 + i)
	}
	goldenPoolID := task021ID(30)
	required := rosterSize / 2 * (domain.AssignmentReserveCount + 1)
	versions := make([]capacity.TaskVersion, required)
	refs := make([]domain.TaskVersionRef, required)
	for i := range versions {
		ref := domain.TaskVersionRef{TaskID: task021ID(2000 + i), Version: 2}
		refs[i] = ref
		versions[i] = capacity.TaskVersion{
			TaskVersionRef: ref,
			PoolRevisionID: goldenPoolID,
			PoolKind:       domain.AssignmentTaskKindGolden,
			Category:       domain.CategoryMisc,
		}
	}
	return capacity.GoldenInput{
		Preset:         domain.TournamentPresetV1,
		ParticipantIDs: participants,
		NormalPool: domain.TaskPoolRevision{
			ID: task021ID(20), Revision: 7, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: task021ID(3000), Version: 1}},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: goldenPoolID, Revision: 5, Kind: domain.AssignmentTaskKindGolden, Versions: refs,
		},
		Versions: versions,
	}
}

func cloneGoldenCapacityInput(input capacity.GoldenInput) capacity.GoldenInput {
	clone := input
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	clone.NormalPool.Versions = append([]domain.TaskVersionRef(nil), input.NormalPool.Versions...)
	clone.GoldenPool.Versions = append([]domain.TaskVersionRef(nil), input.GoldenPool.Versions...)
	clone.Versions = append([]capacity.TaskVersion(nil), input.Versions...)
	clone.History = append([]capacity.TaskUse(nil), input.History...)
	return clone
}
