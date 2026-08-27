package domain

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

type ArenaDerivedRevisionID uuid.UUID

type ArenaArtifactKind string

const (
	ArenaArtifactKindGameResult   ArenaArtifactKind = "game_result"
	ArenaArtifactKindSeriesScore  ArenaArtifactKind = "series_score"
	ArenaArtifactKindSeriesResult ArenaArtifactKind = "series_result"
	ArenaArtifactKindStandings    ArenaArtifactKind = "standings"
	ArenaArtifactKindGoldenGroup  ArenaArtifactKind = "golden_group"
	ArenaArtifactKindTopFour      ArenaArtifactKind = "top_four"
	ArenaArtifactKindBracket      ArenaArtifactKind = "bracket"
	ArenaArtifactKindChampion     ArenaArtifactKind = "champion"
)

var (
	ErrInvalidArenaProjectionRevision = errors.New("invalid arena projection revision")
	ErrInvalidArenaRevisionGraph      = errors.New("invalid arena revision graph")
)

type ArenaArtifactRef struct {
	Kind     ArenaArtifactKind
	EntityID uuid.UUID
}

type ArenaDerivedRevision struct {
	id                 ArenaDerivedRevisionID
	tournamentID       uuid.UUID
	artifact           ArenaArtifactRef
	revisionNo         int
	previousRevisionID *ArenaDerivedRevisionID
	createdAt          time.Time
	payloadDigest      [sha256.Size]byte
}

type ArenaProjectionRevision struct {
	revision ArenaDerivedRevision
	payload  []byte
}

type ArenaRevisionDependency struct {
	SourceRevisionID  ArenaDerivedRevisionID
	DerivedRevisionID ArenaDerivedRevisionID
}

type ArenaRevisionGraph struct {
	projections  []ArenaProjectionRevision
	dependencies []ArenaRevisionDependency
	byID         map[ArenaDerivedRevisionID]ArenaProjectionRevision
	current      map[ArenaArtifactRef]ArenaDerivedRevisionID
	sources      map[ArenaDerivedRevisionID][]ArenaDerivedRevisionID
}

func (id ArenaDerivedRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id ArenaDerivedRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (k ArenaArtifactKind) IsValid() bool {
	switch k {
	case ArenaArtifactKindGameResult,
		ArenaArtifactKindSeriesScore,
		ArenaArtifactKindSeriesResult,
		ArenaArtifactKindStandings,
		ArenaArtifactKindGoldenGroup,
		ArenaArtifactKindTopFour,
		ArenaArtifactKindBracket,
		ArenaArtifactKindChampion:
		return true
	}
	return false
}

func (a ArenaArtifactRef) Validate() error {
	if !a.Kind.IsValid() || a.EntityID == uuid.Nil {
		return fmt.Errorf("%w: invalid artifact identity", ErrInvalidArenaProjectionRevision)
	}
	return nil
}

func NewArenaProjectionRevision(
	id ArenaDerivedRevisionID,
	tournamentID uuid.UUID,
	artifact ArenaArtifactRef,
	revisionNo int,
	previousRevisionID *ArenaDerivedRevisionID,
	createdAt time.Time,
	payload []byte,
) (ArenaProjectionRevision, error) {
	projection := ArenaProjectionRevision{
		revision: ArenaDerivedRevision{
			id:                 id,
			tournamentID:       tournamentID,
			artifact:           artifact,
			revisionNo:         revisionNo,
			previousRevisionID: cloneArenaDerivedRevisionID(previousRevisionID),
			createdAt:          createdAt,
			payloadDigest:      sha256.Sum256(payload),
		},
		payload: append([]byte(nil), payload...),
	}
	if err := projection.Validate(); err != nil {
		return ArenaProjectionRevision{}, err
	}
	return projection, nil
}

func (r ArenaDerivedRevision) Validate() error {
	if r.id.IsZero() || r.tournamentID == uuid.Nil {
		return fmt.Errorf("%w: missing revision identity", ErrInvalidArenaProjectionRevision)
	}
	if err := r.artifact.Validate(); err != nil {
		return err
	}
	if r.revisionNo < 1 || r.createdAt.IsZero() || r.createdAt.Location() != time.UTC {
		return fmt.Errorf("%w: invalid revision number or timestamp", ErrInvalidArenaProjectionRevision)
	}
	if r.revisionNo == 1 && r.previousRevisionID != nil {
		return fmt.Errorf("%w: initial revision has a predecessor", ErrInvalidArenaProjectionRevision)
	}
	if r.revisionNo > 1 && (r.previousRevisionID == nil || r.previousRevisionID.IsZero()) {
		return fmt.Errorf("%w: derived revision is missing its predecessor", ErrInvalidArenaProjectionRevision)
	}
	if r.previousRevisionID != nil && *r.previousRevisionID == r.id {
		return fmt.Errorf("%w: revision depends on itself", ErrInvalidArenaProjectionRevision)
	}
	return nil
}

func (r ArenaDerivedRevision) ID() ArenaDerivedRevisionID {
	return r.id
}

func (r ArenaDerivedRevision) TournamentID() uuid.UUID {
	return r.tournamentID
}

func (r ArenaDerivedRevision) Artifact() ArenaArtifactRef {
	return r.artifact
}

func (r ArenaDerivedRevision) RevisionNo() int {
	return r.revisionNo
}

func (r ArenaDerivedRevision) PreviousRevisionID() *ArenaDerivedRevisionID {
	return cloneArenaDerivedRevisionID(r.previousRevisionID)
}

func (r ArenaDerivedRevision) CreatedAt() time.Time {
	return r.createdAt
}

func (r ArenaDerivedRevision) PayloadDigest() [sha256.Size]byte {
	return r.payloadDigest
}

func (p ArenaProjectionRevision) Validate() error {
	if err := p.revision.Validate(); err != nil {
		return err
	}
	if len(p.payload) == 0 {
		return fmt.Errorf("%w: empty immutable payload", ErrInvalidArenaProjectionRevision)
	}
	if digest := sha256.Sum256(p.payload); !bytes.Equal(digest[:], p.revision.payloadDigest[:]) {
		return fmt.Errorf("%w: payload digest mismatch", ErrInvalidArenaProjectionRevision)
	}
	return nil
}

func (p ArenaProjectionRevision) Revision() ArenaDerivedRevision {
	clone := p.revision
	clone.previousRevisionID = cloneArenaDerivedRevisionID(p.revision.previousRevisionID)
	return clone
}

func (p ArenaProjectionRevision) Payload() []byte {
	return append([]byte(nil), p.payload...)
}

func NewArenaRevisionGraph(
	projections []ArenaProjectionRevision,
	dependencies []ArenaRevisionDependency,
) (ArenaRevisionGraph, error) {
	graph := ArenaRevisionGraph{
		projections:  cloneArenaProjections(projections),
		dependencies: append([]ArenaRevisionDependency(nil), dependencies...),
		byID:         make(map[ArenaDerivedRevisionID]ArenaProjectionRevision, len(projections)),
		current:      make(map[ArenaArtifactRef]ArenaDerivedRevisionID),
		sources:      make(map[ArenaDerivedRevisionID][]ArenaDerivedRevisionID),
	}
	graph.sortEvidence()
	if err := graph.indexProjections(); err != nil {
		return ArenaRevisionGraph{}, err
	}
	if err := graph.indexDependencies(); err != nil {
		return ArenaRevisionGraph{}, err
	}
	if err := graph.validateRevisionChains(); err != nil {
		return ArenaRevisionGraph{}, err
	}
	if graph.hasCycle() {
		return ArenaRevisionGraph{}, fmt.Errorf("%w: dependency cycle", ErrInvalidArenaRevisionGraph)
	}
	return graph, nil
}

func (g ArenaRevisionGraph) Projections() []ArenaProjectionRevision {
	return cloneArenaProjections(g.projections)
}

func (g ArenaRevisionGraph) Dependencies() []ArenaRevisionDependency {
	return append([]ArenaRevisionDependency(nil), g.dependencies...)
}

func (g ArenaRevisionGraph) CurrentRevision(artifact ArenaArtifactRef) (ArenaProjectionRevision, bool) {
	id, exists := g.current[artifact]
	if !exists {
		return ArenaProjectionRevision{}, false
	}
	projection := g.byID[id]
	return cloneArenaProjection(projection), true
}

func (g ArenaRevisionGraph) DependsOn(derivedRevisionID, sourceRevisionID ArenaDerivedRevisionID) bool {
	if derivedRevisionID.IsZero() || sourceRevisionID.IsZero() || derivedRevisionID == sourceRevisionID {
		return false
	}
	visited := make(map[ArenaDerivedRevisionID]struct{}, len(g.byID))
	pending := []ArenaDerivedRevisionID{derivedRevisionID}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if _, seen := visited[current]; seen {
			continue
		}
		visited[current] = struct{}{}
		for _, source := range g.sources[current] {
			if source == sourceRevisionID {
				return true
			}
			pending = append(pending, source)
		}
	}
	return false
}

func (g *ArenaRevisionGraph) sortEvidence() {
	sort.Slice(g.projections, func(i, j int) bool {
		left := g.projections[i].revision
		right := g.projections[j].revision
		if left.artifact.Kind != right.artifact.Kind {
			return left.artifact.Kind < right.artifact.Kind
		}
		if left.artifact.EntityID != right.artifact.EntityID {
			return left.artifact.EntityID.String() < right.artifact.EntityID.String()
		}
		return left.revisionNo < right.revisionNo
	})
	sort.Slice(g.dependencies, func(i, j int) bool {
		if g.dependencies[i].SourceRevisionID != g.dependencies[j].SourceRevisionID {
			return g.dependencies[i].SourceRevisionID.UUID().String() < g.dependencies[j].SourceRevisionID.UUID().String()
		}
		return g.dependencies[i].DerivedRevisionID.UUID().String() < g.dependencies[j].DerivedRevisionID.UUID().String()
	})
}

func (g *ArenaRevisionGraph) indexProjections() error {
	if len(g.projections) == 0 {
		return fmt.Errorf("%w: empty graph", ErrInvalidArenaRevisionGraph)
	}
	for _, projection := range g.projections {
		if err := projection.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArenaRevisionGraph, err)
		}
		revision := projection.revision
		if _, exists := g.byID[revision.id]; exists {
			return fmt.Errorf("%w: duplicate revision identity", ErrInvalidArenaRevisionGraph)
		}
		g.byID[revision.id] = cloneArenaProjection(projection)
		currentID, exists := g.current[revision.artifact]
		if !exists || g.byID[currentID].revision.revisionNo < revision.revisionNo {
			g.current[revision.artifact] = revision.id
		}
	}
	return nil
}

func (g *ArenaRevisionGraph) indexDependencies() error {
	seen := make(map[[2]ArenaDerivedRevisionID]struct{}, len(g.dependencies))
	for _, dependency := range g.dependencies {
		source, sourceExists := g.byID[dependency.SourceRevisionID]
		derived, derivedExists := g.byID[dependency.DerivedRevisionID]
		if !sourceExists || !derivedExists || dependency.SourceRevisionID == dependency.DerivedRevisionID {
			return fmt.Errorf("%w: dependency references an unknown or identical revision", ErrInvalidArenaRevisionGraph)
		}
		if source.revision.tournamentID != derived.revision.tournamentID {
			return fmt.Errorf("%w: cross-tournament dependency", ErrInvalidArenaRevisionGraph)
		}
		if derived.revision.createdAt.Before(source.revision.createdAt) {
			return fmt.Errorf("%w: derived revision predates its source", ErrInvalidArenaRevisionGraph)
		}
		key := [2]ArenaDerivedRevisionID{dependency.SourceRevisionID, dependency.DerivedRevisionID}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate dependency", ErrInvalidArenaRevisionGraph)
		}
		seen[key] = struct{}{}
		g.sources[dependency.DerivedRevisionID] = append(g.sources[dependency.DerivedRevisionID], dependency.SourceRevisionID)
	}
	return nil
}

func (g ArenaRevisionGraph) validateRevisionChains() error {
	previousByArtifact := make(map[ArenaArtifactRef]ArenaDerivedRevision)
	for _, projection := range g.projections {
		revision := projection.revision
		previous, exists := previousByArtifact[revision.artifact]
		if !exists {
			if revision.revisionNo != 1 || revision.previousRevisionID != nil {
				return fmt.Errorf("%w: artifact lineage does not start at revision one", ErrInvalidArenaRevisionGraph)
			}
			previousByArtifact[revision.artifact] = revision
			continue
		}
		if revision.revisionNo != previous.revisionNo+1 || revision.previousRevisionID == nil || *revision.previousRevisionID != previous.id {
			return fmt.Errorf("%w: broken artifact predecessor chain", ErrInvalidArenaRevisionGraph)
		}
		if !g.hasDirectDependency(previous.id, revision.id) {
			return fmt.Errorf("%w: predecessor edge is missing", ErrInvalidArenaRevisionGraph)
		}
		previousByArtifact[revision.artifact] = revision
	}
	return nil
}

func (g ArenaRevisionGraph) hasDirectDependency(sourceRevisionID, derivedRevisionID ArenaDerivedRevisionID) bool {
	for _, source := range g.sources[derivedRevisionID] {
		if source == sourceRevisionID {
			return true
		}
	}
	return false
}

func (g ArenaRevisionGraph) hasCycle() bool {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[ArenaDerivedRevisionID]int, len(g.byID))
	var visit func(ArenaDerivedRevisionID) bool
	visit = func(id ArenaDerivedRevisionID) bool {
		if state[id] == visiting {
			return true
		}
		if state[id] == visited {
			return false
		}
		state[id] = visiting
		for _, source := range g.sources[id] {
			if visit(source) {
				return true
			}
		}
		state[id] = visited
		return false
	}
	for id := range g.byID {
		if visit(id) {
			return true
		}
	}
	return false
}

func cloneArenaProjection(projection ArenaProjectionRevision) ArenaProjectionRevision {
	clone := projection
	clone.payload = append([]byte(nil), projection.payload...)
	clone.revision.previousRevisionID = cloneArenaDerivedRevisionID(projection.revision.previousRevisionID)
	return clone
}

func cloneArenaProjections(projections []ArenaProjectionRevision) []ArenaProjectionRevision {
	clones := make([]ArenaProjectionRevision, len(projections))
	for i := range projections {
		clones[i] = cloneArenaProjection(projections[i])
	}
	return clones
}

func cloneArenaDerivedRevisionID(id *ArenaDerivedRevisionID) *ArenaDerivedRevisionID {
	if id == nil {
		return nil
	}
	clone := *id
	return &clone
}
