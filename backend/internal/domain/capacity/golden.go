package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenGroupProof struct {
	GroupSize           int
	MaxConcurrentGroups int
	Graph               ConstraintGraph
}

type GoldenProof struct {
	Certified             bool
	Category              domain.Category
	ReserveCount          int
	NormalPoolRevisionID  uuid.UUID
	NormalPoolRevision    int64
	PoolRevisionID        uuid.UUID
	PoolRevision          int64
	RosterSize            int
	RequiredTaskVersions  int
	AvailableTaskVersions int
	GroupShapes           []GoldenGroupProof
	Digest                string
	Failure               *Failure
}

type GoldenInput struct {
	Preset domain.TournamentPreset
	// Category is the effective Golden stage category. An empty value retains
	// the pool-wide proof for callers without published stage evidence.
	Category       domain.Category
	ReserveCount   int
	ParticipantIDs []uuid.UUID
	NormalPool     domain.TaskPoolRevision
	GoldenPool     domain.TaskPoolRevision
	Versions       []TaskVersion
	History        []TaskUse
}

func ProveGolden(in GoldenInput) GoldenProof {
	participants, normalPool, goldenPool, versions, history, failure := normalizeGoldenInput(in)
	if failure != nil {
		return failedGolden(*failure)
	}
	if in.Category != "" {
		if !in.Category.IsValid() {
			return failedGolden(Failure{Code: FailureGoldenInvalidInput})
		}
		eligible := make([]TaskVersion, 0, len(versions))
		for _, version := range versions {
			if version.Category == in.Category {
				eligible = append(eligible, version)
			}
		}
		versions = eligible
	}

	chainSize := in.ReserveCount + 1
	required := len(participants) / 2 * chainSize
	if len(versions) < required {
		return failedGolden(Failure{
			Code: FailureGoldenReserveShortage, Category: in.Category,
			Required: required, Available: len(versions),
		})
	}

	usedByAny := make(map[domain.TaskVersionRef]struct{})
	conflictingParticipantID := uuid.Nil
	poolTaskIDs := taskIDSet(versions)
	poolTaskVersions := taskVersionSet(versions)
	for _, participantID := range participants {
		for ref := range history[participantID] {
			if !historyRefInPool(ref, poolTaskVersions, poolTaskIDs) {
				continue
			}
			usedByAny[ref] = struct{}{}
			if conflictingParticipantID == uuid.Nil {
				conflictingParticipantID = participantID
			}
		}
	}
	safeVersions := filterVersions(versions, usedByAny)
	if len(safeVersions) < required {
		return failedGolden(Failure{
			Code:          FailureGoldenReuseConflict,
			Category:      in.Category,
			ParticipantID: conflictingParticipantID,
			Required:      required,
			Available:     len(safeVersions),
		})
	}

	proof := GoldenProof{
		Certified:             true,
		Category:              in.Category,
		ReserveCount:          in.ReserveCount,
		NormalPoolRevisionID:  normalPool.ID,
		NormalPoolRevision:    normalPool.Revision,
		PoolRevisionID:        goldenPool.ID,
		PoolRevision:          goldenPool.Revision,
		RosterSize:            len(participants),
		RequiredTaskVersions:  required,
		AvailableTaskVersions: len(safeVersions),
		GroupShapes:           make([]GoldenGroupProof, 0, len(participants)-1),
	}
	algorithm := GraphAlgorithmV1
	if in.Category != "" {
		algorithm = GoldenGraphAlgorithmV2
	}
	for groupSize := 2; groupSize <= len(participants); groupSize++ {
		concurrent := 1 + (len(participants)-groupSize)/2
		groupRequired := concurrent * chainSize
		proof.GroupShapes = append(proof.GroupShapes, GoldenGroupProof{
			GroupSize:           groupSize,
			MaxConcurrentGroups: concurrent,
			Graph: newConstraintGraph(
				algorithm,
				fmt.Sprintf("golden:group_size:%02d", groupSize),
				in.Category,
				uuid.Nil,
				groupRequired,
				safeVersions,
			),
		})
	}
	proof.Digest = goldenDigest(proof, normalPool)
	return proof
}

func normalizeGoldenInput(
	in GoldenInput,
) ([]uuid.UUID, domain.TaskPoolRevision, domain.TaskPoolRevision, []TaskVersion, map[uuid.UUID]map[domain.TaskVersionRef]struct{}, *Failure) {
	if !domain.IsValidAssignmentReserveCount(in.ReserveCount) {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenInvalidInput}
	}
	participants, ok := normalizedParticipants(in.Preset, in.ParticipantIDs)
	if !ok {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenInvalidInput}
	}
	normalPool, err := domain.NormalizeTaskPoolRevision(in.NormalPool, domain.AssignmentTaskKindNormal)
	if err != nil {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenInvalidInput}
	}
	goldenPool, err := domain.NormalizeTaskPoolRevision(in.GoldenPool, domain.AssignmentTaskKindGolden)
	if err != nil {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenInvalidInput}
	}
	if normalPool.ID == goldenPool.ID || domain.TaskPoolsOverlap(normalPool, goldenPool) {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenPoolOverlap}
	}
	versions, ok := normalizedVersions(goldenPool, in.Versions)
	if !ok {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenInvalidInput}
	}
	history, ok := NormalizeHistory(participants, in.History)
	if !ok {
		return nil, domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, &Failure{Code: FailureGoldenInvalidInput}
	}
	return participants, normalPool, goldenPool, versions, history, nil
}

func goldenDigest(proof GoldenProof, normalPool domain.TaskPoolRevision) string {
	hash := sha256.New()
	if proof.Category == "" {
		writeField(hash, GraphAlgorithmV1)
	} else {
		writeField(hash, GoldenGraphAlgorithmV2)
		writeField(hash, proof.Category.String())
	}
	writeField(hash, fmt.Sprintf("reserve_count:%d", proof.ReserveCount))
	writeField(hash, "normal_pool:"+normalPool.ID.String())
	writeField(hash, fmt.Sprintf("normal_pool_revision:%d", normalPool.Revision))
	writeField(hash, "golden_pool:"+proof.PoolRevisionID.String())
	writeField(hash, fmt.Sprintf("golden_pool_revision:%d", proof.PoolRevision))
	writeField(hash, fmt.Sprintf("roster:%d", proof.RosterSize))
	for _, shape := range proof.GroupShapes {
		writeField(hash, fmt.Sprintf("group_size:%d", shape.GroupSize))
		writeField(hash, fmt.Sprintf("concurrent:%d", shape.MaxConcurrentGroups))
		writeField(hash, shape.Graph.Digest)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func failedGolden(failure Failure) GoldenProof {
	return GoldenProof{Failure: &failure}
}
