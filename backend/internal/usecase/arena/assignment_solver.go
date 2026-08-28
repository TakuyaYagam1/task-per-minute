package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const CapacityGraphAlgorithmV1 = "complete-bipartite-capacity-v1"

type CapacityFailureCode string

const (
	CapacityFailureNormalInvalidInput    CapacityFailureCode = "arena.capacity.normal.invalid_input"
	CapacityFailureNormalReserveShortage CapacityFailureCode = "arena.capacity.normal.reserve_shortage"
	CapacityFailureNormalReuseConflict   CapacityFailureCode = "arena.capacity.normal.reuse_conflict"
	CapacityFailureGoldenInvalidInput    CapacityFailureCode = "arena.capacity.golden.invalid_input"
	CapacityFailureGoldenPoolOverlap     CapacityFailureCode = "arena.capacity.golden.pool_overlap"
	CapacityFailureGoldenReserveShortage CapacityFailureCode = "arena.capacity.golden.reserve_shortage"
	CapacityFailureGoldenReuseConflict   CapacityFailureCode = "arena.capacity.golden.reuse_conflict"
)

type CapacityFailure struct {
	Code          CapacityFailureCode
	Category      domain.Category
	ParticipantID uuid.UUID
	Required      int
	Available     int
}

type CapacityTaskVersion struct {
	TaskVersionRef

	PoolRevisionID uuid.UUID
	PoolKind       domain.ArenaTaskKind
	Category       domain.Category
}

type CapacityTaskUse struct {
	ParticipantID uuid.UUID
	TaskID        uuid.UUID
}

type CapacitySelectedEdge struct {
	Demand      string
	TaskVersion TaskVersionRef
}

// CapacityConstraintGraph is a reproducible proof over abstract capacity
// demands. It deliberately has no Game, assignment, or reservation identity.
type CapacityConstraintGraph struct {
	Key              string
	AlgorithmVersion string
	Category         domain.Category
	ParticipantID    uuid.UUID
	Required         int
	Candidates       []TaskVersionRef
	SelectedEdges    []CapacitySelectedEdge
	Digest           string
}

type NormalCategoryCapacityProof struct {
	Category                   domain.Category
	PeakReservations           int
	PerParticipantReservations int
	RequiredTaskVersions       int
	AvailableTaskVersions      int
	Graphs                     []CapacityConstraintGraph
}

type NormalCapacityProof struct {
	Certified      bool
	PoolRevisionID uuid.UUID
	PoolRevision   int64
	RosterSize     int
	SwissRounds    int
	Categories     []NormalCategoryCapacityProof
	Digest         string
	Failure        *CapacityFailure
}

type NormalCapacityInput struct {
	Preset         domain.ArenaPreset
	ParticipantIDs []uuid.UUID
	CategoryPools  []CategoryPoolRevision
	NormalPool     TaskPoolRevision
	Versions       []CapacityTaskVersion
	History        []CapacityTaskUse
}

type normalCategoryDemand struct {
	category       domain.Category
	peak           int
	perParticipant int
	required       int
	versions       []CapacityTaskVersion
	peakVersions   []CapacityTaskVersion
}

func ProveNormalCapacity(in NormalCapacityInput) NormalCapacityProof {
	participants, categoryPools, pool, versions, history, valid := normalizeNormalCapacityInput(in)
	if !valid {
		return failedNormalCapacity(CapacityFailure{Code: CapacityFailureNormalInvalidInput})
	}
	rounds, err := in.Preset.SwissRounds(len(participants))
	if err != nil {
		return failedNormalCapacity(CapacityFailure{Code: CapacityFailureNormalInvalidInput})
	}

	demands := normalCategoryDemands(len(participants), rounds, categoryPools, versions)
	for i := range demands {
		demand := &demands[i]
		if len(demand.versions) < demand.required {
			return failedNormalCapacity(CapacityFailure{
				Code:      CapacityFailureNormalReserveShortage,
				Category:  demand.category,
				Required:  demand.required,
				Available: len(demand.versions),
			})
		}
		var participantID uuid.UUID
		demand.peakVersions, participantID = rosterSafeCapacityVersions(participants, demand.versions, history)
		if len(demand.peakVersions) < demand.peak {
			return failedNormalCapacity(CapacityFailure{
				Code:          CapacityFailureNormalReuseConflict,
				Category:      demand.category,
				ParticipantID: participantID,
				Required:      demand.peak,
				Available:     len(demand.peakVersions),
			})
		}
		for _, participantID := range participants {
			available := filterCapacityVersions(demand.versions, history[participantID])
			if len(available) < demand.perParticipant {
				return failedNormalCapacity(CapacityFailure{
					Code:          CapacityFailureNormalReuseConflict,
					Category:      demand.category,
					ParticipantID: participantID,
					Required:      demand.perParticipant,
					Available:     len(available),
				})
			}
		}
	}

	proof := NormalCapacityProof{
		Certified:      true,
		PoolRevisionID: pool.ID,
		PoolRevision:   pool.Revision,
		RosterSize:     len(participants),
		SwissRounds:    rounds,
		Categories:     make([]NormalCategoryCapacityProof, 0, len(demands)),
	}
	for _, demand := range demands {
		graphs := make([]CapacityConstraintGraph, 0, len(participants)+1)
		graphs = append(graphs, newCapacityConstraintGraph(
			"normal:"+demand.category.String()+":peak",
			demand.category,
			uuid.Nil,
			demand.peak,
			demand.peakVersions,
		))
		for _, participantID := range participants {
			graphs = append(graphs, newCapacityConstraintGraph(
				"normal:"+demand.category.String()+":participant:"+participantID.String(),
				demand.category,
				participantID,
				demand.perParticipant,
				filterCapacityVersions(demand.versions, history[participantID]),
			))
		}
		proof.Categories = append(proof.Categories, NormalCategoryCapacityProof{
			Category:                   demand.category,
			PeakReservations:           demand.peak,
			PerParticipantReservations: demand.perParticipant,
			RequiredTaskVersions:       demand.required,
			AvailableTaskVersions:      len(demand.versions),
			Graphs:                     graphs,
		})
	}
	proof.Digest = normalCapacityDigest(proof)
	return proof
}

func normalizeNormalCapacityInput(
	in NormalCapacityInput,
) ([]uuid.UUID, []CategoryPoolRevision, TaskPoolRevision, []CapacityTaskVersion, map[uuid.UUID]map[uuid.UUID]struct{}, bool) {
	participants, ok := normalizedCapacityParticipants(in.Preset, in.ParticipantIDs)
	if !ok {
		return nil, nil, TaskPoolRevision{}, nil, nil, false
	}
	categoryPools, err := normalizeCategoryPoolRevisions(in.CategoryPools)
	if err != nil {
		return nil, nil, TaskPoolRevision{}, nil, nil, false
	}
	pool, err := normalizeTaskPoolRevision(in.NormalPool, domain.ArenaTaskKindNormal)
	if err != nil {
		return nil, nil, TaskPoolRevision{}, nil, nil, false
	}
	versions, ok := normalizedCapacityVersions(pool, in.Versions)
	if !ok {
		return nil, nil, TaskPoolRevision{}, nil, nil, false
	}
	history, ok := normalizedCapacityHistory(participants, in.History)
	if !ok {
		return nil, nil, TaskPoolRevision{}, nil, nil, false
	}
	return participants, categoryPools, pool, versions, history, true
}

func normalizedCapacityParticipants(preset domain.ArenaPreset, participantIDs []uuid.UUID) ([]uuid.UUID, bool) {
	if !preset.IsValid() || !preset.ValidRosterSize(len(participantIDs)) {
		return nil, false
	}
	participants := append([]uuid.UUID(nil), participantIDs...)
	sort.Slice(participants, func(i, j int) bool { return bytes.Compare(participants[i][:], participants[j][:]) < 0 })
	for i, participantID := range participants {
		if participantID == uuid.Nil || (i > 0 && participants[i-1] == participantID) {
			return nil, false
		}
	}
	return participants, true
}

func normalizedCapacityVersions(pool TaskPoolRevision, input []CapacityTaskVersion) ([]CapacityTaskVersion, bool) {
	if len(input) != len(pool.Versions) {
		return nil, false
	}
	versions := append([]CapacityTaskVersion(nil), input...)
	sort.Slice(versions, func(i, j int) bool { return taskVersionRefLess(versions[i].TaskVersionRef, versions[j].TaskVersionRef) })
	for i, version := range versions {
		if version.TaskVersionRef != pool.Versions[i] || version.PoolRevisionID != pool.ID ||
			version.PoolKind != pool.Kind || !version.Category.IsValid() {
			return nil, false
		}
	}
	return versions, true
}

func normalizedCapacityHistory(
	participants []uuid.UUID,
	input []CapacityTaskUse,
) (map[uuid.UUID]map[uuid.UUID]struct{}, bool) {
	participantSet := make(map[uuid.UUID]struct{}, len(participants))
	history := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		participantSet[participantID] = struct{}{}
		history[participantID] = make(map[uuid.UUID]struct{})
	}
	for _, use := range input {
		if use.TaskID == uuid.Nil {
			return nil, false
		}
		if _, exists := participantSet[use.ParticipantID]; !exists {
			return nil, false
		}
		if _, duplicate := history[use.ParticipantID][use.TaskID]; duplicate {
			return nil, false
		}
		history[use.ParticipantID][use.TaskID] = struct{}{}
	}
	return history, true
}

func normalCategoryDemands(
	rosterSize int,
	swissRounds int,
	pools []CategoryPoolRevision,
	versions []CapacityTaskVersion,
) []normalCategoryDemand {
	bo1 := make(map[domain.Category]struct{})
	bo3 := make(map[domain.Category]struct{})
	for _, pool := range pools {
		target := bo1
		if pool.Format == domain.ArenaSeriesFormatBO3 {
			target = bo3
		}
		for _, category := range pool.Categories {
			target[category] = struct{}{}
		}
	}
	categories := make([]domain.Category, 0, len(bo1)+len(bo3))
	seen := make(map[domain.Category]struct{}, len(bo1)+len(bo3))
	for category := range bo1 {
		seen[category] = struct{}{}
		categories = append(categories, category)
	}
	for category := range bo3 {
		if _, exists := seen[category]; !exists {
			categories = append(categories, category)
		}
	}
	sort.Slice(categories, func(i, j int) bool { return categories[i] < categories[j] })

	byCategory := make(map[domain.Category][]CapacityTaskVersion, len(categories))
	for _, version := range versions {
		byCategory[version.Category] = append(byCategory[version.Category], version)
	}
	chainSize := domain.ArenaAssignmentReserveCount + 1
	demands := make([]normalCategoryDemand, 0, len(categories))
	for _, category := range categories {
		peak, occurrences := 0, 0
		if _, exists := bo1[category]; exists {
			peak = rosterSize / 2 * chainSize
			occurrences += swissRounds + 1
		}
		if _, exists := bo3[category]; exists {
			peak = max(peak, chainSize)
			occurrences++
		}
		perParticipant := occurrences * chainSize
		demands = append(demands, normalCategoryDemand{
			category:       category,
			peak:           peak,
			perParticipant: perParticipant,
			required:       max(peak, perParticipant),
			versions:       byCategory[category],
		})
	}
	return demands
}

func filterCapacityVersions(
	versions []CapacityTaskVersion,
	used map[uuid.UUID]struct{},
) []CapacityTaskVersion {
	if len(used) == 0 {
		return append([]CapacityTaskVersion(nil), versions...)
	}
	result := make([]CapacityTaskVersion, 0, len(versions))
	for _, version := range versions {
		if _, exists := used[version.TaskID]; !exists {
			result = append(result, version)
		}
	}
	return result
}

func rosterSafeCapacityVersions(
	participants []uuid.UUID,
	versions []CapacityTaskVersion,
	history map[uuid.UUID]map[uuid.UUID]struct{},
) ([]CapacityTaskVersion, uuid.UUID) {
	poolTaskIDs := capacityTaskIDSet(versions)
	usedByAny := make(map[uuid.UUID]struct{})
	firstConflictParticipantID := uuid.Nil
	for _, participantID := range participants {
		for taskID := range history[participantID] {
			if _, belongsToPool := poolTaskIDs[taskID]; !belongsToPool {
				continue
			}
			usedByAny[taskID] = struct{}{}
			if firstConflictParticipantID == uuid.Nil {
				firstConflictParticipantID = participantID
			}
		}
	}
	return filterCapacityVersions(versions, usedByAny), firstConflictParticipantID
}

func newCapacityConstraintGraph(
	key string,
	category domain.Category,
	participantID uuid.UUID,
	required int,
	versions []CapacityTaskVersion,
) CapacityConstraintGraph {
	graph := CapacityConstraintGraph{
		Key:              key,
		AlgorithmVersion: CapacityGraphAlgorithmV1,
		Category:         category,
		ParticipantID:    participantID,
		Required:         required,
		Candidates:       make([]TaskVersionRef, len(versions)),
		SelectedEdges:    make([]CapacitySelectedEdge, required),
	}
	for i, version := range versions {
		graph.Candidates[i] = version.TaskVersionRef
	}
	for i := 0; i < required; i++ {
		graph.SelectedEdges[i] = CapacitySelectedEdge{
			Demand:      fmt.Sprintf("%s:slot:%03d", key, i+1),
			TaskVersion: graph.Candidates[i],
		}
	}
	graph.Digest = capacityGraphDigest(graph)
	return graph
}

func capacityGraphDigest(graph CapacityConstraintGraph) string {
	hash := sha256.New()
	writeCapacityField(hash, graph.AlgorithmVersion)
	writeCapacityField(hash, graph.Key)
	writeCapacityField(hash, graph.Category.String())
	writeCapacityField(hash, graph.ParticipantID.String())
	writeCapacityField(hash, fmt.Sprintf("required:%d", graph.Required))
	for _, candidate := range graph.Candidates {
		writeCapacityField(hash, capacityTaskVersionEvidence(candidate))
	}
	for _, edge := range graph.SelectedEdges {
		writeCapacityField(hash, edge.Demand)
		writeCapacityField(hash, capacityTaskVersionEvidence(edge.TaskVersion))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalCapacityDigest(proof NormalCapacityProof) string {
	hash := sha256.New()
	writeCapacityField(hash, CapacityGraphAlgorithmV1)
	writeCapacityField(hash, proof.PoolRevisionID.String())
	writeCapacityField(hash, fmt.Sprintf("pool_revision:%d", proof.PoolRevision))
	writeCapacityField(hash, fmt.Sprintf("roster:%d", proof.RosterSize))
	writeCapacityField(hash, fmt.Sprintf("swiss_rounds:%d", proof.SwissRounds))
	for _, category := range proof.Categories {
		writeCapacityField(hash, category.Category.String())
		writeCapacityField(hash, fmt.Sprintf("peak:%d", category.PeakReservations))
		writeCapacityField(hash, fmt.Sprintf("participant:%d", category.PerParticipantReservations))
		for _, graph := range category.Graphs {
			writeCapacityField(hash, graph.Digest)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeCapacityField(hash io.Writer, value string) {
	_, _ = fmt.Fprintf(hash, "%d:", len(value))
	_, _ = hash.Write([]byte(value))
}

func capacityTaskVersionEvidence(ref TaskVersionRef) string {
	return fmt.Sprintf("%s@%d", ref.TaskID, ref.Version)
}

func taskVersionRefLess(first, second TaskVersionRef) bool {
	comparison := bytes.Compare(first.TaskID[:], second.TaskID[:])
	if comparison != 0 {
		return comparison < 0
	}
	return first.Version < second.Version
}

func failedNormalCapacity(failure CapacityFailure) NormalCapacityProof {
	return NormalCapacityProof{Failure: &failure}
}
