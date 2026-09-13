package capacity

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

const (
	// GraphAlgorithmV1 is retained for the Golden proof. Normal capacity has a
	// separate algorithm version because its retained-reservation demand is
	// stage-aware.
	GraphAlgorithmV1       = "complete-bipartite-capacity-v1"
	NormalGraphAlgorithmV2 = "complete-bipartite-capacity-normal-v2"
)

type FailureCode string

const (
	FailureNormalInvalidInput    FailureCode = "tournament.capacity.normal.invalid_input"
	FailureNormalReserveShortage FailureCode = "tournament.capacity.normal.reserve_shortage"
	FailureNormalReuseConflict   FailureCode = "tournament.capacity.normal.reuse_conflict"
	FailureGoldenInvalidInput    FailureCode = "tournament.capacity.golden.invalid_input"
	FailureGoldenPoolOverlap     FailureCode = "tournament.capacity.golden.pool_overlap"
	FailureGoldenReserveShortage FailureCode = "tournament.capacity.golden.reserve_shortage"
	FailureGoldenReuseConflict   FailureCode = "tournament.capacity.golden.reuse_conflict"
)

type Failure struct {
	Code          FailureCode
	Category      domain.Category
	ParticipantID uuid.UUID
	Required      int
	Available     int
}

type TaskVersion struct {
	domain.TaskVersionRef

	PoolRevisionID uuid.UUID
	PoolKind       domain.AssignmentTaskKind
	Category       domain.Category
}

type TaskUse struct {
	ParticipantID uuid.UUID `json:"ParticipantID"`
	TaskID        uuid.UUID `json:"TaskID"`
	Version       int       `json:",omitempty"`
}

type SelectedEdge struct {
	Demand      string
	TaskVersion domain.TaskVersionRef
}

// ConstraintGraph is a reproducible proof over abstract capacity demands. It
// deliberately has no game, assignment, or reservation identity.
type ConstraintGraph struct {
	Key              string
	AlgorithmVersion string
	Category         domain.Category
	ParticipantID    uuid.UUID
	Required         int
	Candidates       []domain.TaskVersionRef
	SelectedEdges    []SelectedEdge
	Digest           string
}

type NormalCategoryProof struct {
	Category                   domain.Category
	PeakReservations           int
	PerParticipantReservations int
	RequiredTaskVersions       int
	AvailableTaskVersions      int
	Graphs                     []ConstraintGraph
}

type NormalProof struct {
	Certified      bool
	PoolRevisionID uuid.UUID
	PoolRevision   int64
	RosterSize     int
	SwissRounds    int
	Categories     []NormalCategoryProof
	Digest         string
	Failure        *Failure
}

type NormalInput struct {
	Preset         domain.TournamentPreset
	ParticipantIDs []uuid.UUID
	CategoryPools  []domain.CategoryPoolRevision
	NormalPool     domain.TaskPoolRevision
	Versions       []TaskVersion
	History        []TaskUse
}

type normalCategoryDemand struct {
	category       domain.Category
	peak           int
	perParticipant int
	required       int
	versions       []TaskVersion
	peakVersions   []TaskVersion
}

func ProveNormal(in NormalInput) NormalProof {
	participants, categoryPools, pool, versions, history, valid := normalizeNormalInput(in)
	if !valid {
		return failedNormal(Failure{Code: FailureNormalInvalidInput})
	}
	rounds, err := in.Preset.SwissRounds(len(participants))
	if err != nil {
		return failedNormal(Failure{Code: FailureNormalInvalidInput})
	}

	demands := normalCategoryDemands(len(participants), rounds, categoryPools, versions)
	for i := range demands {
		demand := &demands[i]
		if len(demand.versions) < demand.required {
			return failedNormal(Failure{
				Code:      FailureNormalReserveShortage,
				Category:  demand.category,
				Required:  demand.required,
				Available: len(demand.versions),
			})
		}
		var participantID uuid.UUID
		demand.peakVersions, participantID = rosterSafeVersions(participants, demand.versions, history)
		if len(demand.peakVersions) < demand.peak {
			return failedNormal(Failure{
				Code:          FailureNormalReuseConflict,
				Category:      demand.category,
				ParticipantID: participantID,
				Required:      demand.peak,
				Available:     len(demand.peakVersions),
			})
		}
		for _, participantID := range participants {
			available := filterVersions(demand.versions, history[participantID])
			if len(available) < demand.perParticipant {
				return failedNormal(Failure{
					Code:          FailureNormalReuseConflict,
					Category:      demand.category,
					ParticipantID: participantID,
					Required:      demand.perParticipant,
					Available:     len(available),
				})
			}
		}
	}

	proof := NormalProof{
		Certified:      true,
		PoolRevisionID: pool.ID,
		PoolRevision:   pool.Revision,
		RosterSize:     len(participants),
		SwissRounds:    rounds,
		Categories:     make([]NormalCategoryProof, 0, len(demands)),
	}
	for _, demand := range demands {
		graphs := make([]ConstraintGraph, 0, len(participants)+1)
		graphs = append(graphs, newConstraintGraph(
			NormalGraphAlgorithmV2,
			"normal:"+demand.category.String()+":peak",
			demand.category,
			uuid.Nil,
			demand.peak,
			demand.peakVersions,
		))
		for _, participantID := range participants {
			graphs = append(graphs, newConstraintGraph(
				NormalGraphAlgorithmV2,
				"normal:"+demand.category.String()+":participant:"+participantID.String(),
				demand.category,
				participantID,
				demand.perParticipant,
				filterVersions(demand.versions, history[participantID]),
			))
		}
		proof.Categories = append(proof.Categories, NormalCategoryProof{
			Category:                   demand.category,
			PeakReservations:           demand.peak,
			PerParticipantReservations: demand.perParticipant,
			RequiredTaskVersions:       demand.required,
			AvailableTaskVersions:      len(demand.versions),
			Graphs:                     graphs,
		})
	}
	proof.Digest = normalDigest(proof)
	return proof
}

func normalizeNormalInput(
	in NormalInput,
) ([]uuid.UUID, []domain.CategoryPoolRevision, domain.TaskPoolRevision, []TaskVersion, map[uuid.UUID]map[domain.TaskVersionRef]struct{}, bool) {
	participants, ok := normalizedParticipants(in.Preset, in.ParticipantIDs)
	if !ok {
		return nil, nil, domain.TaskPoolRevision{}, nil, nil, false
	}
	categoryPools, err := domain.NormalizeCategoryPoolRevisions(in.CategoryPools)
	if err != nil {
		return nil, nil, domain.TaskPoolRevision{}, nil, nil, false
	}
	pool, err := domain.NormalizeTaskPoolRevision(in.NormalPool, domain.AssignmentTaskKindNormal)
	if err != nil {
		return nil, nil, domain.TaskPoolRevision{}, nil, nil, false
	}
	versions, ok := normalizedVersions(pool, in.Versions)
	if !ok {
		return nil, nil, domain.TaskPoolRevision{}, nil, nil, false
	}
	history, ok := NormalizeHistory(participants, in.History)
	if !ok {
		return nil, nil, domain.TaskPoolRevision{}, nil, nil, false
	}
	return participants, categoryPools, pool, versions, history, true
}

func normalizedParticipants(preset domain.TournamentPreset, participantIDs []uuid.UUID) ([]uuid.UUID, bool) {
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

func normalizedVersions(pool domain.TaskPoolRevision, input []TaskVersion) ([]TaskVersion, bool) {
	if len(input) != len(pool.Versions) {
		return nil, false
	}
	versions := append([]TaskVersion(nil), input...)
	sort.Slice(versions, func(i, j int) bool {
		return domain.CompareTaskVersionRefs(versions[i].TaskVersionRef, versions[j].TaskVersionRef) < 0
	})
	for i, version := range versions {
		if version.TaskVersionRef != pool.Versions[i] || version.PoolRevisionID != pool.ID ||
			version.PoolKind != pool.Kind || !version.Category.IsValid() {
			return nil, false
		}
	}
	return versions, true
}

func NormalizeHistory(
	participants []uuid.UUID,
	input []TaskUse,
) (map[uuid.UUID]map[domain.TaskVersionRef]struct{}, bool) {
	participantSet := make(map[uuid.UUID]struct{}, len(participants))
	history := make(map[uuid.UUID]map[domain.TaskVersionRef]struct{}, len(participants))
	for _, participantID := range participants {
		participantSet[participantID] = struct{}{}
		history[participantID] = make(map[domain.TaskVersionRef]struct{})
	}
	for _, use := range input {
		if use.TaskID == uuid.Nil || use.Version < 0 {
			return nil, false
		}
		if _, exists := participantSet[use.ParticipantID]; !exists {
			return nil, false
		}
		ref := domain.TaskVersionRef{TaskID: use.TaskID, Version: use.Version}
		if _, duplicate := history[use.ParticipantID][ref]; duplicate {
			return nil, false
		}
		history[use.ParticipantID][ref] = struct{}{}
	}
	return history, true
}

func normalCategoryDemands(
	rosterSize int,
	swissRounds int,
	pools []domain.CategoryPoolRevision,
	versions []TaskVersion,
) []normalCategoryDemand {
	bo1 := make(map[domain.Category]struct{})
	bo3 := make(map[domain.Category]struct{})
	for _, pool := range pools {
		target := bo1
		if pool.Format == domain.SeriesFormatBO3 {
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

	byCategory := make(map[domain.Category][]TaskVersion, len(categories))
	for _, version := range versions {
		byCategory[version.Category] = append(byCategory[version.Category], version)
	}
	chainSize := domain.AssignmentReserveCount + 1
	demands := make([]normalCategoryDemand, 0, len(categories))
	for _, category := range categories {
		occurrences, retainedChains := 0, 0
		if _, exists := bo1[category]; exists {
			occurrences += swissRounds + 1
			// Every Swiss pairing and both semifinal series retain one complete
			// primary-plus-reserves chain. Normal reservations are tournament
			// scoped and remain unavailable after they are committed.
			retainedChains += rosterSize/2*swissRounds + 2
		}
		if _, exists := bo3[category]; exists {
			occurrences++
			// A BO3 draft retains one chain for the selected category in each
			// reachable final path. Alternative paths may share these versions.
			retainedChains++
		}
		// Peak is the complete set of tournament-scoped live reservations. It
		// is intentionally larger than one-wave concurrency because committed
		// normal reservations are not reusable by later stages.
		peak := retainedChains * chainSize
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

func filterVersions(versions []TaskVersion, used map[domain.TaskVersionRef]struct{}) []TaskVersion {
	if len(used) == 0 {
		return append([]TaskVersion(nil), versions...)
	}
	result := make([]TaskVersion, 0, len(versions))
	for _, version := range versions {
		if !historyContains(used, version.TaskVersionRef) {
			result = append(result, version)
		}
	}
	return result
}

func historyContains(used map[domain.TaskVersionRef]struct{}, candidate domain.TaskVersionRef) bool {
	if _, exists := used[candidate]; exists {
		return true
	}
	_, legacy := used[domain.TaskVersionRef{TaskID: candidate.TaskID}]
	return legacy
}

func rosterSafeVersions(
	participants []uuid.UUID,
	versions []TaskVersion,
	history map[uuid.UUID]map[domain.TaskVersionRef]struct{},
) ([]TaskVersion, uuid.UUID) {
	poolTaskIDs := taskIDSet(versions)
	poolTaskVersions := taskVersionSet(versions)
	usedByAny := make(map[domain.TaskVersionRef]struct{})
	firstConflictParticipantID := uuid.Nil
	for _, participantID := range participants {
		for ref := range history[participantID] {
			if !historyRefInPool(ref, poolTaskVersions, poolTaskIDs) {
				continue
			}
			usedByAny[ref] = struct{}{}
			if firstConflictParticipantID == uuid.Nil {
				firstConflictParticipantID = participantID
			}
		}
	}
	return filterVersions(versions, usedByAny), firstConflictParticipantID
}

func newConstraintGraph(
	algorithmVersion string,
	key string,
	category domain.Category,
	participantID uuid.UUID,
	required int,
	versions []TaskVersion,
) ConstraintGraph {
	graph := ConstraintGraph{
		Key:              key,
		AlgorithmVersion: algorithmVersion,
		Category:         category,
		ParticipantID:    participantID,
		Required:         required,
		Candidates:       make([]domain.TaskVersionRef, len(versions)),
		SelectedEdges:    make([]SelectedEdge, required),
	}
	for i, version := range versions {
		graph.Candidates[i] = version.TaskVersionRef
	}
	for i := 0; i < required; i++ {
		graph.SelectedEdges[i] = SelectedEdge{
			Demand:      fmt.Sprintf("%s:slot:%03d", key, i+1),
			TaskVersion: graph.Candidates[i],
		}
	}
	graph.Digest = graphDigest(graph)
	return graph
}

func graphDigest(graph ConstraintGraph) string {
	hash := sha256.New()
	writeField(hash, graph.AlgorithmVersion)
	writeField(hash, graph.Key)
	writeField(hash, graph.Category.String())
	writeField(hash, graph.ParticipantID.String())
	writeField(hash, fmt.Sprintf("required:%d", graph.Required))
	for _, candidate := range graph.Candidates {
		writeField(hash, taskVersionEvidence(candidate))
	}
	for _, edge := range graph.SelectedEdges {
		writeField(hash, edge.Demand)
		writeField(hash, taskVersionEvidence(edge.TaskVersion))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalDigest(proof NormalProof) string {
	hash := sha256.New()
	writeField(hash, NormalGraphAlgorithmV2)
	writeField(hash, proof.PoolRevisionID.String())
	writeField(hash, fmt.Sprintf("pool_revision:%d", proof.PoolRevision))
	writeField(hash, fmt.Sprintf("roster:%d", proof.RosterSize))
	writeField(hash, fmt.Sprintf("swiss_rounds:%d", proof.SwissRounds))
	for _, category := range proof.Categories {
		writeField(hash, category.Category.String())
		writeField(hash, fmt.Sprintf("peak:%d", category.PeakReservations))
		writeField(hash, fmt.Sprintf("participant:%d", category.PerParticipantReservations))
		writeField(hash, fmt.Sprintf("required:%d", category.RequiredTaskVersions))
		for _, graph := range category.Graphs {
			writeField(hash, graph.Digest)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeField(hash io.Writer, value string) {
	_, _ = fmt.Fprintf(hash, "%d:", len(value))
	_, _ = hash.Write([]byte(value))
}

func taskVersionEvidence(ref domain.TaskVersionRef) string {
	return fmt.Sprintf("%s@%d", ref.TaskID, ref.Version)
}

func taskIDSet(versions []TaskVersion) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(versions))
	for _, version := range versions {
		result[version.TaskID] = struct{}{}
	}
	return result
}

func taskVersionSet(versions []TaskVersion) map[domain.TaskVersionRef]struct{} {
	result := make(map[domain.TaskVersionRef]struct{}, len(versions))
	for _, version := range versions {
		result[version.TaskVersionRef] = struct{}{}
	}
	return result
}

func historyRefInPool(
	ref domain.TaskVersionRef,
	poolTaskVersions map[domain.TaskVersionRef]struct{},
	poolTaskIDs map[uuid.UUID]struct{},
) bool {
	if ref.Version == 0 {
		_, exists := poolTaskIDs[ref.TaskID]
		return exists
	}
	_, exists := poolTaskVersions[ref]
	return exists
}

func failedNormal(failure Failure) NormalProof {
	return NormalProof{Failure: &failure}
}
