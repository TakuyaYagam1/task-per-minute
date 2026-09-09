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

type DerivedRevisionID uuid.UUID

type ArtifactKind string

const (
	ArtifactKindGameResult   ArtifactKind = "game_result"
	ArtifactKindSeriesScore  ArtifactKind = "series_score"
	ArtifactKindSeriesResult ArtifactKind = "series_result"
	ArtifactKindStandings    ArtifactKind = "standings"
	ArtifactKindGoldenGroup  ArtifactKind = "golden_group"
	ArtifactKindTopFour      ArtifactKind = "top_four"
	ArtifactKindBracket      ArtifactKind = "bracket"
	ArtifactKindChampion     ArtifactKind = "champion"
)

var (
	ErrInvalidProjectionRevision = errors.New("invalid projection revision")
	ErrInvalidRevisionGraph      = errors.New("invalid projection revision graph")
)

type ArtifactRef struct {
	Kind     ArtifactKind
	EntityID uuid.UUID
}

type DerivedRevision struct {
	id                 DerivedRevisionID
	tournamentID       uuid.UUID
	artifact           ArtifactRef
	revisionNo         int
	previousRevisionID *DerivedRevisionID
	createdAt          time.Time
	payloadDigest      [sha256.Size]byte
}

type ProjectionRevision struct {
	revision DerivedRevision
	payload  []byte
}

type RevisionDependency struct {
	SourceRevisionID  DerivedRevisionID
	DerivedRevisionID DerivedRevisionID
}

type RevisionGraph struct {
	projections  []ProjectionRevision
	dependencies []RevisionDependency
	byID         map[DerivedRevisionID]ProjectionRevision
	current      map[ArtifactRef]DerivedRevisionID
	sources      map[DerivedRevisionID][]DerivedRevisionID
}

func (id DerivedRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id DerivedRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (k ArtifactKind) IsValid() bool {
	switch k {
	case ArtifactKindGameResult,
		ArtifactKindSeriesScore,
		ArtifactKindSeriesResult,
		ArtifactKindStandings,
		ArtifactKindGoldenGroup,
		ArtifactKindTopFour,
		ArtifactKindBracket,
		ArtifactKindChampion:
		return true
	}
	return false
}

func (a ArtifactRef) Validate() error {
	if !a.Kind.IsValid() || a.EntityID == uuid.Nil {
		return fmt.Errorf("%w: invalid artifact identity", ErrInvalidProjectionRevision)
	}
	return nil
}

func NewProjectionRevision(
	id DerivedRevisionID,
	tournamentID uuid.UUID,
	artifact ArtifactRef,
	revisionNo int,
	previousRevisionID *DerivedRevisionID,
	createdAt time.Time,
	payload []byte,
) (ProjectionRevision, error) {
	projection := ProjectionRevision{
		revision: DerivedRevision{
			id:                 id,
			tournamentID:       tournamentID,
			artifact:           artifact,
			revisionNo:         revisionNo,
			previousRevisionID: cloneDerivedRevisionID(previousRevisionID),
			createdAt:          createdAt,
			payloadDigest:      sha256.Sum256(payload),
		},
		payload: append([]byte(nil), payload...),
	}
	if err := projection.Validate(); err != nil {
		return ProjectionRevision{}, err
	}
	return projection, nil
}

func (r DerivedRevision) Validate() error {
	if r.id.IsZero() || r.tournamentID == uuid.Nil {
		return fmt.Errorf("%w: missing revision identity", ErrInvalidProjectionRevision)
	}
	if err := r.artifact.Validate(); err != nil {
		return err
	}
	if r.revisionNo < 1 || r.createdAt.IsZero() || r.createdAt.Location() != time.UTC {
		return fmt.Errorf("%w: invalid revision number or timestamp", ErrInvalidProjectionRevision)
	}
	if r.revisionNo == 1 && r.previousRevisionID != nil {
		return fmt.Errorf("%w: initial revision has a predecessor", ErrInvalidProjectionRevision)
	}
	if r.revisionNo > 1 && (r.previousRevisionID == nil || r.previousRevisionID.IsZero()) {
		return fmt.Errorf("%w: derived revision is missing its predecessor", ErrInvalidProjectionRevision)
	}
	if r.previousRevisionID != nil && *r.previousRevisionID == r.id {
		return fmt.Errorf("%w: revision depends on itself", ErrInvalidProjectionRevision)
	}
	return nil
}

func (r DerivedRevision) ID() DerivedRevisionID {
	return r.id
}

func (r DerivedRevision) TournamentID() uuid.UUID {
	return r.tournamentID
}

func (r DerivedRevision) Artifact() ArtifactRef {
	return r.artifact
}

func (r DerivedRevision) RevisionNo() int {
	return r.revisionNo
}

func (r DerivedRevision) PreviousRevisionID() *DerivedRevisionID {
	return cloneDerivedRevisionID(r.previousRevisionID)
}

func (r DerivedRevision) CreatedAt() time.Time {
	return r.createdAt
}

func (r DerivedRevision) PayloadDigest() [sha256.Size]byte {
	return r.payloadDigest
}

func (p ProjectionRevision) Validate() error {
	if err := p.revision.Validate(); err != nil {
		return err
	}
	if len(p.payload) == 0 {
		return fmt.Errorf("%w: empty immutable payload", ErrInvalidProjectionRevision)
	}
	if digest := sha256.Sum256(p.payload); !bytes.Equal(digest[:], p.revision.payloadDigest[:]) {
		return fmt.Errorf("%w: payload digest mismatch", ErrInvalidProjectionRevision)
	}
	return nil
}

func (p ProjectionRevision) Revision() DerivedRevision {
	clone := p.revision
	clone.previousRevisionID = cloneDerivedRevisionID(p.revision.previousRevisionID)
	return clone
}

func (p ProjectionRevision) Payload() []byte {
	return append([]byte(nil), p.payload...)
}

func NewRevisionGraph(
	projections []ProjectionRevision,
	dependencies []RevisionDependency,
) (RevisionGraph, error) {
	graph := RevisionGraph{
		projections:  cloneProjections(projections),
		dependencies: append([]RevisionDependency(nil), dependencies...),
		byID:         make(map[DerivedRevisionID]ProjectionRevision, len(projections)),
		current:      make(map[ArtifactRef]DerivedRevisionID),
		sources:      make(map[DerivedRevisionID][]DerivedRevisionID),
	}
	graph.sortEvidence()
	if err := graph.indexProjections(); err != nil {
		return RevisionGraph{}, err
	}
	if err := graph.indexDependencies(); err != nil {
		return RevisionGraph{}, err
	}
	if err := graph.validateRevisionChains(); err != nil {
		return RevisionGraph{}, err
	}
	if graph.hasCycle() {
		return RevisionGraph{}, fmt.Errorf("%w: dependency cycle", ErrInvalidRevisionGraph)
	}
	return graph, nil
}

func (g RevisionGraph) Projections() []ProjectionRevision {
	return cloneProjections(g.projections)
}

func (g RevisionGraph) Dependencies() []RevisionDependency {
	return append([]RevisionDependency(nil), g.dependencies...)
}

func (g RevisionGraph) CurrentRevision(artifact ArtifactRef) (ProjectionRevision, bool) {
	id, exists := g.current[artifact]
	if !exists {
		return ProjectionRevision{}, false
	}
	projection := g.byID[id]
	return cloneProjection(projection), true
}

func (g RevisionGraph) DependsOn(derivedRevisionID, sourceRevisionID DerivedRevisionID) bool {
	if derivedRevisionID.IsZero() || sourceRevisionID.IsZero() || derivedRevisionID == sourceRevisionID {
		return false
	}
	visited := make(map[DerivedRevisionID]struct{}, len(g.byID))
	pending := []DerivedRevisionID{derivedRevisionID}
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

func (g *RevisionGraph) sortEvidence() {
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

func (g *RevisionGraph) indexProjections() error {
	if len(g.projections) == 0 {
		return fmt.Errorf("%w: empty graph", ErrInvalidRevisionGraph)
	}
	for _, projection := range g.projections {
		if err := projection.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRevisionGraph, err)
		}
		revision := projection.revision
		if _, exists := g.byID[revision.id]; exists {
			return fmt.Errorf("%w: duplicate revision identity", ErrInvalidRevisionGraph)
		}
		g.byID[revision.id] = cloneProjection(projection)
		currentID, exists := g.current[revision.artifact]
		if !exists || g.byID[currentID].revision.revisionNo < revision.revisionNo {
			g.current[revision.artifact] = revision.id
		}
	}
	return nil
}

func (g *RevisionGraph) indexDependencies() error {
	seen := make(map[[2]DerivedRevisionID]struct{}, len(g.dependencies))
	for _, dependency := range g.dependencies {
		source, sourceExists := g.byID[dependency.SourceRevisionID]
		derived, derivedExists := g.byID[dependency.DerivedRevisionID]
		if !sourceExists || !derivedExists || dependency.SourceRevisionID == dependency.DerivedRevisionID {
			return fmt.Errorf("%w: dependency references an unknown or identical revision", ErrInvalidRevisionGraph)
		}
		if source.revision.tournamentID != derived.revision.tournamentID {
			return fmt.Errorf("%w: cross-tournament dependency", ErrInvalidRevisionGraph)
		}
		if derived.revision.createdAt.Before(source.revision.createdAt) {
			return fmt.Errorf("%w: derived revision predates its source", ErrInvalidRevisionGraph)
		}
		key := [2]DerivedRevisionID{dependency.SourceRevisionID, dependency.DerivedRevisionID}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate dependency", ErrInvalidRevisionGraph)
		}
		seen[key] = struct{}{}
		g.sources[dependency.DerivedRevisionID] = append(g.sources[dependency.DerivedRevisionID], dependency.SourceRevisionID)
	}
	return nil
}

func (g RevisionGraph) validateRevisionChains() error {
	previousByArtifact := make(map[ArtifactRef]DerivedRevision)
	for _, projection := range g.projections {
		revision := projection.revision
		previous, exists := previousByArtifact[revision.artifact]
		if !exists {
			if revision.revisionNo != 1 || revision.previousRevisionID != nil {
				return fmt.Errorf("%w: artifact lineage does not start at revision one", ErrInvalidRevisionGraph)
			}
			previousByArtifact[revision.artifact] = revision
			continue
		}
		if revision.revisionNo != previous.revisionNo+1 || revision.previousRevisionID == nil || *revision.previousRevisionID != previous.id {
			return fmt.Errorf("%w: broken artifact predecessor chain", ErrInvalidRevisionGraph)
		}
		if !g.hasDirectDependency(previous.id, revision.id) {
			return fmt.Errorf("%w: predecessor edge is missing", ErrInvalidRevisionGraph)
		}
		previousByArtifact[revision.artifact] = revision
	}
	return nil
}

func (g RevisionGraph) hasDirectDependency(sourceRevisionID, derivedRevisionID DerivedRevisionID) bool {
	for _, source := range g.sources[derivedRevisionID] {
		if source == sourceRevisionID {
			return true
		}
	}
	return false
}

func (g RevisionGraph) hasCycle() bool {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[DerivedRevisionID]int, len(g.byID))
	var visit func(DerivedRevisionID) bool
	visit = func(id DerivedRevisionID) bool {
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

func cloneProjection(projection ProjectionRevision) ProjectionRevision {
	clone := projection
	clone.payload = append([]byte(nil), projection.payload...)
	clone.revision.previousRevisionID = cloneDerivedRevisionID(projection.revision.previousRevisionID)
	return clone
}

func cloneProjections(projections []ProjectionRevision) []ProjectionRevision {
	clones := make([]ProjectionRevision, len(projections))
	for i := range projections {
		clones[i] = cloneProjection(projections[i])
	}
	return clones
}

func cloneDerivedRevisionID(id *DerivedRevisionID) *DerivedRevisionID {
	if id == nil {
		return nil
	}
	clone := *id
	return &clone
}
