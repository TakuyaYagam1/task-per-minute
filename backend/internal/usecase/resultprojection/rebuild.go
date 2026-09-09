package resultprojection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	MaxRecordedProjectionDecisionBytes = 64 << 10
	maxProjectionRebuildDecisions      = 4096
	maxProjectionRebuildDecisionBytes  = 4 << 20
)

var (
	ErrInvalidProjectionRebuild = errors.New("invalid official projection rebuild")
	ErrProjectionRebuildChanged = errors.New("official projection rebuild changed")
)

type RecordedProjectionDecision struct {
	ID                   uuid.UUID
	Sequence             int
	ProjectionRevisionID domain.DerivedRevisionID
	RecordedAt           time.Time
	Payload              []byte
	PayloadDigest        [sha256.Size]byte
}

type ProjectionRebuildInput struct {
	DAG       RevisionDAG
	Decisions []RecordedProjectionDecision
}

type ProjectionRebuild struct {
	payload   []byte
	decisions []RecordedProjectionDecision
}

func RebuildOfficialProjections(input ProjectionRebuildInput) (ProjectionRebuild, error) {
	if err := input.DAG.Validate(); err != nil {
		return ProjectionRebuild{}, invalidProjectionRebuild("invalid revision DAG: %v", err)
	}
	decisions, err := validateAndCloneProjectionDecisions(input.DAG, input.Decisions)
	if err != nil {
		return ProjectionRebuild{}, err
	}
	payload, err := marshalProjectionRebuild(input.DAG, decisions)
	if err != nil {
		return ProjectionRebuild{}, invalidProjectionRebuild("marshal canonical rebuild: %v", err)
	}
	return ProjectionRebuild{payload: payload, decisions: decisions}, nil
}

func (r ProjectionRebuild) Bytes() []byte {
	return append([]byte(nil), r.payload...)
}

func (r ProjectionRebuild) DecisionCount() int {
	return len(r.decisions)
}

func (r ProjectionRebuild) Decisions() []RecordedProjectionDecision {
	return cloneRecordedProjectionDecisions(r.decisions)
}

//nolint:gocyclo // Metadata, bounds, digest, chronology, and ownership form one record audit.
func validateAndCloneProjectionDecisions(
	dag RevisionDAG,
	input []RecordedProjectionDecision,
) ([]RecordedProjectionDecision, error) {
	if len(input) == 0 || len(input) > maxProjectionRebuildDecisions {
		return nil, invalidProjectionRebuild("invalid recorded decision count")
	}
	decisions := cloneRecordedProjectionDecisions(input)
	index, err := newRevisionDAGIndex(dag.graph, dag.results)
	if err != nil {
		return nil, invalidProjectionRebuild("index revision DAG: %v", err)
	}
	reservedIDs := projectionRebuildReservedIDs(dag)
	ids := make(map[uuid.UUID]struct{}, len(decisions))
	targets := make(map[domain.DerivedRevisionID]struct{}, len(decisions))
	totalPayload := 0
	for indexInInput := range decisions {
		decision := decisions[indexInInput]
		if decision.ID == uuid.Nil || decision.Sequence < 1 || decision.ProjectionRevisionID.IsZero() ||
			!domain.IsValidServerTime(decision.RecordedAt) || len(decision.Payload) == 0 ||
			len(decision.Payload) > MaxRecordedProjectionDecisionBytes ||
			len(decision.Payload) > maxProjectionRebuildDecisionBytes-totalPayload {
			return nil, invalidProjectionRebuild("invalid recorded decision metadata")
		}
		totalPayload += len(decision.Payload)
		if _, duplicate := ids[decision.ID]; duplicate {
			return nil, invalidProjectionRebuild("duplicate recorded decision identity")
		}
		if _, aliases := reservedIDs[decision.ID]; aliases {
			return nil, invalidProjectionRebuild("recorded decision aliases revision evidence")
		}
		ids[decision.ID] = struct{}{}
		if _, duplicate := targets[decision.ProjectionRevisionID]; duplicate {
			return nil, invalidProjectionRebuild("duplicate recorded decision target")
		}
		targets[decision.ProjectionRevisionID] = struct{}{}
		projection, exists := index.byID[decision.ProjectionRevisionID]
		if !exists || index.current[projection.Revision().Artifact()] != decision.ProjectionRevisionID {
			return nil, invalidProjectionRebuild("recorded decision targets a stale projection")
		}
		if decision.RecordedAt.After(projection.Revision().CreatedAt()) {
			return nil, invalidProjectionRebuild("recorded decision postdates its projection")
		}
		for _, sourceID := range index.sources[decision.ProjectionRevisionID] {
			if decision.RecordedAt.Before(index.byID[sourceID].Revision().CreatedAt()) {
				return nil, invalidProjectionRebuild("recorded decision predates its source projection")
			}
		}
		wantDigest := sha256.Sum256(decision.Payload)
		if decision.PayloadDigest == ([sha256.Size]byte{}) ||
			!bytes.Equal(decision.PayloadDigest[:], wantDigest[:]) {
			return nil, invalidProjectionRebuild("recorded decision payload digest mismatch")
		}
	}
	sort.Slice(decisions, func(first, second int) bool {
		return decisions[first].Sequence < decisions[second].Sequence
	})
	for indexInOrder := range decisions {
		decision := decisions[indexInOrder]
		if decision.Sequence != indexInOrder+1 {
			return nil, invalidProjectionRebuild("recorded decision sequence is not contiguous")
		}
		if indexInOrder > 0 {
			previous := decisions[indexInOrder-1]
			if decision.RecordedAt.Before(previous.RecordedAt) {
				return nil, invalidProjectionRebuild("recorded decision time moved backwards")
			}
		}
	}
	if !projectionDecisionOrderIsCausal(index, decisions) {
		return nil, invalidProjectionRebuild("descendant decision precedes its source")
	}
	return decisions, nil
}

//nolint:gocyclo // The allowlisted identity walk deliberately covers every result input mode.
func projectionRebuildReservedIDs(dag RevisionDAG) map[uuid.UUID]struct{} {
	reserved := make(map[uuid.UUID]struct{})
	add := func(id uuid.UUID) {
		if id != uuid.Nil {
			reserved[id] = struct{}{}
		}
	}
	for _, projection := range dag.graph.Projections() {
		revision := projection.Revision()
		add(revision.ID().UUID())
		add(revision.TournamentID())
		add(revision.Artifact().EntityID)
		if previous := revision.PreviousRevisionID(); previous != nil {
			add(previous.UUID())
		}
	}
	for _, plan := range dag.results {
		input := plan.input
		if input.NoGame != nil {
			recorded := input.NoGame
			add(recorded.CommandID)
			add(recorded.Scope.WaveID)
			add(recorded.Scope.WindowID)
			add(recorded.Scope.SeriesID)
			add(recorded.FirstParticipantID)
			add(recorded.SecondParticipantID)
			if recorded.ReadyParticipantID != nil {
				add(*recorded.ReadyParticipantID)
			}
			add(recorded.Score.ID.UUID())
			add(recorded.Series.ID.UUID())
			if recorded.Score.PreviousRevisionID != nil {
				add(recorded.Score.PreviousRevisionID.UUID())
			}
			if recorded.Series.PreviousRevisionID != nil {
				add(recorded.Series.PreviousRevisionID.UUID())
			}
			for index, game := range recorded.GameResults {
				add(game.ID.UUID())
				add(game.GameID)
				if index < len(recorded.Topology) {
					add(recorded.Topology[index].SlotID)
				}
				if game.PreviousRevisionID != nil {
					add(game.PreviousRevisionID.UUID())
				}
			}
			continue
		}
		add(input.Result.ID.UUID())
		add(input.Result.CommandID)
		if input.Result.Actor.PrincipalID != nil {
			add(*input.Result.Actor.PrincipalID)
		}
		if input.Result.PreviousRevisionID != nil {
			add(input.Result.PreviousRevisionID.UUID())
		}
		if input.Score != nil {
			add(input.Score.ID.UUID())
			add(input.Score.CommandID)
			add(input.Score.FirstParticipantID)
			add(input.Score.SecondParticipantID)
			if input.Score.Actor.PrincipalID != nil {
				add(*input.Score.Actor.PrincipalID)
			}
			for _, attempt := range input.Score.Attempts {
				add(attempt.SlotID)
				add(attempt.GameID)
			}
			if input.Score.PreviousRevisionID != nil {
				add(input.Score.PreviousRevisionID.UUID())
			}
		}
	}
	return reserved
}

func projectionDecisionOrderIsCausal(
	index revisionDAGIndex,
	decisions []RecordedProjectionDecision,
) bool {
	sequenceByTarget := make(map[domain.DerivedRevisionID]int, len(decisions))
	for _, decision := range decisions {
		sequenceByTarget[decision.ProjectionRevisionID] = decision.Sequence
	}
	indegree := make(map[domain.DerivedRevisionID]int, len(index.byID))
	dependents := make(map[domain.DerivedRevisionID][]domain.DerivedRevisionID, len(index.byID))
	for id := range index.byID {
		indegree[id] = len(index.sources[id])
		for _, sourceID := range index.sources[id] {
			dependents[sourceID] = append(dependents[sourceID], id)
		}
	}
	queue := make([]domain.DerivedRevisionID, 0, len(index.byID))
	for id, count := range indegree {
		if count == 0 {
			queue = append(queue, id)
		}
	}
	maxAncestorSequence := make(map[domain.DerivedRevisionID]int, len(index.byID))
	processed := 0
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		processed++
		ownSequence := sequenceByTarget[id]
		if ownSequence > 0 && maxAncestorSequence[id] >= ownSequence {
			return false
		}
		propagated := maxAncestorSequence[id]
		if ownSequence > propagated {
			propagated = ownSequence
		}
		for _, dependentID := range dependents[id] {
			if propagated > maxAncestorSequence[dependentID] {
				maxAncestorSequence[dependentID] = propagated
			}
			indegree[dependentID]--
			if indegree[dependentID] == 0 {
				queue = append(queue, dependentID)
			}
		}
	}
	return processed == len(index.byID)
}

type canonicalProjectionRebuild struct {
	Projections  []canonicalProjectionRevision `json:"projections"`
	Dependencies []canonicalRevisionDependency `json:"dependencies"`
	Public       []PublicOfficialResult        `json:"public_results"`
	Operator     []OperatorOfficialResult      `json:"operator_results"`
	Decisions    []canonicalProjectionDecision `json:"recorded_decisions"`
}

type canonicalProjectionRevision struct {
	ID                 string  `json:"id"`
	TournamentID       string  `json:"tournament_id"`
	Kind               string  `json:"kind"`
	EntityID           string  `json:"entity_id"`
	Revision           int     `json:"revision"`
	PreviousRevisionID *string `json:"previous_revision_id"`
	CreatedAt          string  `json:"created_at"`
	PayloadDigest      string  `json:"payload_digest"`
}

type canonicalRevisionDependency struct {
	SourceRevisionID  string `json:"source_revision_id"`
	DerivedRevisionID string `json:"derived_revision_id"`
}

type canonicalProjectionDecision struct {
	ID                   string `json:"id"`
	Sequence             int    `json:"sequence"`
	ProjectionRevisionID string `json:"projection_revision_id"`
	RecordedAt           string `json:"recorded_at"`
	Payload              []byte `json:"payload"`
	PayloadDigest        string `json:"payload_digest"`
}

func marshalProjectionRebuild(
	dag RevisionDAG,
	decisions []RecordedProjectionDecision,
) ([]byte, error) {
	snapshot := dag.Snapshot()
	canonical := canonicalProjectionRebuild{
		Projections:  make([]canonicalProjectionRevision, len(snapshot.Projections)),
		Dependencies: make([]canonicalRevisionDependency, len(snapshot.Dependencies)),
		Public:       dag.PublicResults(),
		Operator:     dag.OperatorResults(),
		Decisions:    make([]canonicalProjectionDecision, len(decisions)),
	}
	for index, projection := range snapshot.Projections {
		revision := projection.Revision()
		payloadDigest := revision.PayloadDigest()
		var previousID *string
		if previous := revision.PreviousRevisionID(); previous != nil {
			value := previous.UUID().String()
			previousID = &value
		}
		canonical.Projections[index] = canonicalProjectionRevision{
			ID: revision.ID().UUID().String(), TournamentID: revision.TournamentID().String(),
			Kind: string(revision.Artifact().Kind), EntityID: revision.Artifact().EntityID.String(),
			Revision: revision.RevisionNo(), PreviousRevisionID: previousID,
			CreatedAt:     revision.CreatedAt().Format(time.RFC3339Nano),
			PayloadDigest: hex.EncodeToString(payloadDigest[:]),
		}
	}
	for index, dependency := range snapshot.Dependencies {
		canonical.Dependencies[index] = canonicalRevisionDependency{
			SourceRevisionID:  dependency.SourceRevisionID.UUID().String(),
			DerivedRevisionID: dependency.DerivedRevisionID.UUID().String(),
		}
	}
	for index, decision := range decisions {
		canonical.Decisions[index] = canonicalProjectionDecision{
			ID: decision.ID.String(), Sequence: decision.Sequence,
			ProjectionRevisionID: decision.ProjectionRevisionID.UUID().String(),
			RecordedAt:           decision.RecordedAt.Format(time.RFC3339Nano),
			Payload:              append([]byte(nil), decision.Payload...),
			PayloadDigest:        hex.EncodeToString(decision.PayloadDigest[:]),
		}
	}
	return json.Marshal(canonical) //nolint:musttag // Every canonical encoding document is tagged.
}

func cloneRecordedProjectionDecisions(input []RecordedProjectionDecision) []RecordedProjectionDecision {
	clone := make([]RecordedProjectionDecision, len(input))
	for index := range input {
		clone[index] = input[index]
		clone[index].Payload = append([]byte(nil), input[index].Payload...)
	}
	return clone
}

func invalidProjectionRebuild(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidProjectionRebuild, fmt.Sprintf(format, arguments...))
}
