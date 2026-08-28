package arena

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenGroupCapacityProof struct {
	GroupSize           int
	MaxConcurrentGroups int
	Graph               CapacityConstraintGraph
}

type GoldenCapacityProof struct {
	Certified             bool
	NormalPoolRevisionID  uuid.UUID
	NormalPoolRevision    int64
	PoolRevisionID        uuid.UUID
	PoolRevision          int64
	RosterSize            int
	RequiredTaskVersions  int
	AvailableTaskVersions int
	GroupShapes           []GoldenGroupCapacityProof
	Digest                string
	Failure               *CapacityFailure
}

type GoldenCapacityInput struct {
	Preset         domain.ArenaPreset
	ParticipantIDs []uuid.UUID
	NormalPool     TaskPoolRevision
	GoldenPool     TaskPoolRevision
	Versions       []CapacityTaskVersion
	History        []CapacityTaskUse
}

func ProveGoldenCapacity(in GoldenCapacityInput) GoldenCapacityProof {
	participants, normalPool, goldenPool, versions, history, failure := normalizeGoldenCapacityInput(in)
	if failure != nil {
		return failedGoldenCapacity(*failure)
	}

	chainSize := domain.ArenaAssignmentReserveCount + 1
	required := len(participants) / 2 * chainSize
	if len(versions) < required {
		return failedGoldenCapacity(CapacityFailure{
			Code: CapacityFailureGoldenReserveShortage, Required: required, Available: len(versions),
		})
	}

	usedByAny := make(map[uuid.UUID]struct{})
	conflictingParticipantID := uuid.Nil
	poolTaskIDs := capacityTaskIDSet(versions)
	for _, participantID := range participants {
		for taskID := range history[participantID] {
			if _, belongsToPool := poolTaskIDs[taskID]; !belongsToPool {
				continue
			}
			usedByAny[taskID] = struct{}{}
			if conflictingParticipantID == uuid.Nil {
				conflictingParticipantID = participantID
			}
		}
	}
	safeVersions := filterCapacityVersions(versions, usedByAny)
	if len(safeVersions) < required {
		return failedGoldenCapacity(CapacityFailure{
			Code:          CapacityFailureGoldenReuseConflict,
			ParticipantID: conflictingParticipantID,
			Required:      required,
			Available:     len(safeVersions),
		})
	}

	proof := GoldenCapacityProof{
		Certified:             true,
		NormalPoolRevisionID:  normalPool.ID,
		NormalPoolRevision:    normalPool.Revision,
		PoolRevisionID:        goldenPool.ID,
		PoolRevision:          goldenPool.Revision,
		RosterSize:            len(participants),
		RequiredTaskVersions:  required,
		AvailableTaskVersions: len(safeVersions),
		GroupShapes:           make([]GoldenGroupCapacityProof, 0, len(participants)-1),
	}
	for groupSize := 2; groupSize <= len(participants); groupSize++ {
		concurrent := 1 + (len(participants)-groupSize)/2
		groupRequired := concurrent * chainSize
		proof.GroupShapes = append(proof.GroupShapes, GoldenGroupCapacityProof{
			GroupSize:           groupSize,
			MaxConcurrentGroups: concurrent,
			Graph: newCapacityConstraintGraph(
				fmt.Sprintf("golden:group_size:%02d", groupSize),
				"",
				uuid.Nil,
				groupRequired,
				safeVersions,
			),
		})
	}
	proof.Digest = goldenCapacityDigest(proof, normalPool)
	return proof
}

func normalizeGoldenCapacityInput(
	in GoldenCapacityInput,
) ([]uuid.UUID, TaskPoolRevision, TaskPoolRevision, []CapacityTaskVersion, map[uuid.UUID]map[uuid.UUID]struct{}, *CapacityFailure) {
	participants, ok := normalizedCapacityParticipants(in.Preset, in.ParticipantIDs)
	if !ok {
		return nil, TaskPoolRevision{}, TaskPoolRevision{}, nil, nil, &CapacityFailure{Code: CapacityFailureGoldenInvalidInput}
	}
	normalPool, err := normalizeTaskPoolRevision(in.NormalPool, domain.ArenaTaskKindNormal)
	if err != nil {
		return nil, TaskPoolRevision{}, TaskPoolRevision{}, nil, nil, &CapacityFailure{Code: CapacityFailureGoldenInvalidInput}
	}
	goldenPool, err := normalizeTaskPoolRevision(in.GoldenPool, domain.ArenaTaskKindGolden)
	if err != nil {
		return nil, TaskPoolRevision{}, TaskPoolRevision{}, nil, nil, &CapacityFailure{Code: CapacityFailureGoldenInvalidInput}
	}
	if normalPool.ID == goldenPool.ID || taskPoolsOverlap(normalPool, goldenPool) {
		return nil, TaskPoolRevision{}, TaskPoolRevision{}, nil, nil, &CapacityFailure{Code: CapacityFailureGoldenPoolOverlap}
	}
	versions, ok := normalizedCapacityVersions(goldenPool, in.Versions)
	if !ok {
		return nil, TaskPoolRevision{}, TaskPoolRevision{}, nil, nil, &CapacityFailure{Code: CapacityFailureGoldenInvalidInput}
	}
	history, ok := normalizedCapacityHistory(participants, in.History)
	if !ok {
		return nil, TaskPoolRevision{}, TaskPoolRevision{}, nil, nil, &CapacityFailure{Code: CapacityFailureGoldenInvalidInput}
	}
	return participants, normalPool, goldenPool, versions, history, nil
}

func capacityTaskIDSet(versions []CapacityTaskVersion) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(versions))
	for _, version := range versions {
		result[version.TaskID] = struct{}{}
	}
	return result
}

func goldenCapacityDigest(proof GoldenCapacityProof, normalPool TaskPoolRevision) string {
	hash := sha256.New()
	writeCapacityField(hash, CapacityGraphAlgorithmV1)
	writeCapacityField(hash, "normal_pool:"+normalPool.ID.String())
	writeCapacityField(hash, fmt.Sprintf("normal_pool_revision:%d", normalPool.Revision))
	writeCapacityField(hash, "golden_pool:"+proof.PoolRevisionID.String())
	writeCapacityField(hash, fmt.Sprintf("golden_pool_revision:%d", proof.PoolRevision))
	writeCapacityField(hash, fmt.Sprintf("roster:%d", proof.RosterSize))
	for _, shape := range proof.GroupShapes {
		writeCapacityField(hash, fmt.Sprintf("group_size:%d", shape.GroupSize))
		writeCapacityField(hash, fmt.Sprintf("concurrent:%d", shape.MaxConcurrentGroups))
		writeCapacityField(hash, shape.Graph.Digest)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func failedGoldenCapacity(failure CapacityFailure) GoldenCapacityProof {
	return GoldenCapacityProof{Failure: &failure}
}
