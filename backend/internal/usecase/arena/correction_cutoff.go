package arena

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const maxCorrectionCutoffEvents = 4096

const (
	minCorrectionYear = 2000
	maxCorrectionYear = 2200

	maxCorrectionDAGProjections  = 512
	maxCorrectionDAGDependencies = 2048
	maxCorrectionDAGResults      = 512
	maxCorrectionDAGPayloadBytes = 512 << 10
	maxCorrectionAuthorityIDs    = 8192
	maxCorrectionNoGameAttempts  = 2048
)

var (
	ErrInvalidCorrection = errors.New("invalid Arena correction")
	ErrCorrectionCutoff  = errors.New("arena correction cutoff reached")
)

type CorrectionRejectionCode string

const (
	CorrectionRejectionMalformed       CorrectionRejectionCode = "malformed"
	CorrectionRejectionStale           CorrectionRejectionCode = "stale"
	CorrectionRejectionIncomplete      CorrectionRejectionCode = "incomplete"
	CorrectionRejectionIdentityAlias   CorrectionRejectionCode = "identity_aliased"
	CorrectionRejectionTerminal        CorrectionRejectionCode = "terminal_incompatible"
	CorrectionRejectionCrossTournament CorrectionRejectionCode = "cross_tournament"
	CorrectionRejectionCutoff          CorrectionRejectionCode = "cutoff"
)

type CorrectionError struct {
	code   CorrectionRejectionCode
	cause  error
	detail string
}

func (e *CorrectionError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%v: %s: %s", e.cause, e.code, e.detail)
}

func (e *CorrectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *CorrectionError) Code() CorrectionRejectionCode {
	if e == nil {
		return ""
	}
	return e.code
}

func CorrectionCode(err error) CorrectionRejectionCode {
	var rejection *CorrectionError
	if errors.As(err, &rejection) {
		return rejection.Code()
	}
	return ""
}

type CorrectionCutoffKind string

const (
	CorrectionCutoffWaveStarted     CorrectionCutoffKind = "wave_started"
	CorrectionCutoffTaskDelivered   CorrectionCutoffKind = "task_delivered"
	CorrectionCutoffNoShowRecorded  CorrectionCutoffKind = "no_show_recorded"
	CorrectionCutoffForfeitRecorded CorrectionCutoffKind = "forfeit_recorded"
	CorrectionCutoffGoldenAllocated CorrectionCutoffKind = "golden_direct_allocated"
)

type CorrectionCutoffEvent struct {
	ID               uuid.UUID
	Kind             CorrectionCutoffKind
	TournamentID     uuid.UUID
	SourceRevisionID domain.ArenaDerivedRevisionID
	OccurredAt       time.Time
}

type CorrectionCutoffInput struct {
	DAG              RevisionDAG
	TournamentID     uuid.UUID
	TargetRevisionID domain.ArenaDerivedRevisionID
	TournamentState  domain.ArenaTournamentState
	Events           []CorrectionCutoffEvent
}

type CorrectionCutoff struct {
	tournamentID     uuid.UUID
	targetRevisionID domain.ArenaDerivedRevisionID
	descendants      []domain.ArenaDerivedRevision
}

func (c CorrectionCutoff) TournamentID() uuid.UUID {
	return c.tournamentID
}

func (c CorrectionCutoff) TargetRevisionID() domain.ArenaDerivedRevisionID {
	return c.targetRevisionID
}

func (c CorrectionCutoff) Descendants() []domain.ArenaDerivedRevision {
	return append([]domain.ArenaDerivedRevision(nil), c.descendants...)
}

func EvaluateCorrectionCutoff(input CorrectionCutoffInput) (CorrectionCutoff, error) {
	if err := preflightCorrectionDAGResults(input.DAG); err != nil {
		return CorrectionCutoff{}, err
	}
	snapshot := input.DAG.Snapshot()
	return evaluateCorrectionCutoffSnapshot(input, snapshot)
}

func evaluateCorrectionCutoffSnapshot(
	input CorrectionCutoffInput,
	snapshot RevisionDAGSnapshot,
) (CorrectionCutoff, error) {
	if err := preflightCorrectionCutoff(input, snapshot); err != nil {
		return CorrectionCutoff{}, err
	}
	return evaluateCorrectionCutoffPrepared(input, snapshot)
}

func evaluateCorrectionCutoffPrepared(
	input CorrectionCutoffInput,
	snapshot RevisionDAGSnapshot,
) (CorrectionCutoff, error) {
	index, err := newCorrectionCutoffIndex(snapshot, input)
	if err != nil {
		return CorrectionCutoff{}, err
	}
	descendants, affected, err := index.orderedDescendants(input.TargetRevisionID)
	if err != nil {
		return CorrectionCutoff{}, err
	}
	if input.TournamentState.IsTerminal() {
		return CorrectionCutoff{}, rejectCorrection(
			CorrectionRejectionCutoff, ErrCorrectionCutoff, "tournament is terminal",
		)
	}
	for _, event := range input.Events {
		if _, blocked := affected[event.SourceRevisionID]; blocked {
			return CorrectionCutoff{}, rejectCorrection(
				CorrectionRejectionCutoff, ErrCorrectionCutoff, "irreversible event exists",
			)
		}
	}
	return CorrectionCutoff{
		tournamentID:     input.TournamentID,
		targetRevisionID: input.TargetRevisionID,
		descendants:      descendants,
	}, nil
}

func preflightCorrectionDAGResults(dag RevisionDAG) error {
	if len(dag.results) == 0 || len(dag.results) > maxCorrectionDAGResults {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "correction DAG result count is out of bounds",
		)
	}
	identityCount := 0
	noGameAttempts := 0
	add := func(count int) bool {
		if count < 0 || count > maxCorrectionAuthorityIDs-identityCount {
			return false
		}
		identityCount += count
		return true
	}
	for _, plan := range dag.results {
		input := plan.input
		if input.NoGame == nil {
			attempts := 0
			if input.Score != nil {
				attempts = len(input.Score.Attempts)
			}
			if !add(16 + attempts*4) {
				return rejectCorrection(
					CorrectionRejectionMalformed, ErrInvalidCorrection, "correction authority identity count is out of bounds",
				)
			}
			continue
		}
		attempts := len(input.NoGame.GameResults)
		if attempts > maxCorrectionNoGameAttempts-noGameAttempts || !add(24+attempts*8) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "correction no-game evidence is out of bounds",
			)
		}
		noGameAttempts += attempts
	}
	return nil
}

func preflightCorrectionCutoff(input CorrectionCutoffInput, snapshot RevisionDAGSnapshot) error {
	if input.TournamentID == uuid.Nil || input.TargetRevisionID.IsZero() ||
		!input.TournamentState.IsValid() || len(input.Events) > maxCorrectionCutoffEvents ||
		len(snapshot.Projections) == 0 || len(snapshot.Projections) > maxCorrectionDAGProjections ||
		len(snapshot.Dependencies) > maxCorrectionDAGDependencies {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid cutoff bounds",
		)
	}
	totalPayload := 0
	for _, projection := range snapshot.Projections {
		if !validCorrectionTime(projection.Revision().CreatedAt()) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "cutoff projection time is out of bounds",
			)
		}
		payloadSize := len(projection.Payload())
		if payloadSize > maxCorrectionDAGPayloadBytes-totalPayload {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "cutoff graph payload is too large",
			)
		}
		totalPayload += payloadSize
	}
	return nil
}

type correctionCutoffIndex struct {
	ordered      []domain.ArenaDerivedRevision
	byID         map[domain.ArenaDerivedRevisionID]domain.ArenaDerivedRevision
	current      map[domain.ArenaArtifactRef]domain.ArenaDerivedRevisionID
	outgoing     map[domain.ArenaDerivedRevisionID][]domain.ArenaDerivedRevisionID
	reservedIDs  map[uuid.UUID]struct{}
	tournamentID uuid.UUID
}

//nolint:gocyclo // The bounded index validates all graph, identity, and event invariants in one pass.
func newCorrectionCutoffIndex(
	snapshot RevisionDAGSnapshot,
	input CorrectionCutoffInput,
) (correctionCutoffIndex, error) {
	index := correctionCutoffIndex{
		ordered:      snapshotRevisions(snapshot),
		byID:         make(map[domain.ArenaDerivedRevisionID]domain.ArenaDerivedRevision, len(snapshot.Projections)),
		current:      make(map[domain.ArenaArtifactRef]domain.ArenaDerivedRevisionID),
		outgoing:     make(map[domain.ArenaDerivedRevisionID][]domain.ArenaDerivedRevisionID),
		reservedIDs:  projectionRebuildReservedIDs(input.DAG),
		tournamentID: input.TournamentID,
	}
	for projectionIndex, revision := range index.ordered {
		if snapshot.Projections[projectionIndex].Validate() != nil {
			return correctionCutoffIndex{}, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid projection revision",
			)
		}
		if revision.TournamentID() != input.TournamentID {
			return correctionCutoffIndex{}, rejectCorrection(
				CorrectionRejectionCrossTournament, ErrInvalidCorrection, "revision belongs to another tournament",
			)
		}
		if _, duplicate := index.byID[revision.ID()]; duplicate {
			return correctionCutoffIndex{}, rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "duplicate projection identity",
			)
		}
		index.byID[revision.ID()] = revision
		currentID, exists := index.current[revision.Artifact()]
		if !exists || index.byID[currentID].RevisionNo() < revision.RevisionNo() {
			index.current[revision.Artifact()] = revision.ID()
		}
	}
	edges := make(map[[2]domain.ArenaDerivedRevisionID]struct{}, len(snapshot.Dependencies))
	for _, dependency := range snapshot.Dependencies {
		source, sourceExists := index.byID[dependency.SourceRevisionID]
		derived, derivedExists := index.byID[dependency.DerivedRevisionID]
		key := [2]domain.ArenaDerivedRevisionID{
			dependency.SourceRevisionID, dependency.DerivedRevisionID,
		}
		if !sourceExists || !derivedExists || dependency.SourceRevisionID == dependency.DerivedRevisionID ||
			derived.CreatedAt().Before(source.CreatedAt()) {
			return correctionCutoffIndex{}, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid projection dependency",
			)
		}
		if _, duplicate := edges[key]; duplicate {
			return correctionCutoffIndex{}, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "duplicate projection dependency",
			)
		}
		if source.Artifact() != derived.Artifact() &&
			!validRevisionDAGEdge(source.Artifact().Kind, derived.Artifact().Kind) {
			return correctionCutoffIndex{}, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid projection dependency kind",
			)
		}
		edges[key] = struct{}{}
		index.outgoing[dependency.SourceRevisionID] = append(
			index.outgoing[dependency.SourceRevisionID], dependency.DerivedRevisionID,
		)
	}
	if err := index.validateRevisionChains(edges); err != nil {
		return correctionCutoffIndex{}, err
	}
	if !index.isAcyclic() {
		return correctionCutoffIndex{}, rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "projection graph is cyclic",
		)
	}
	target, exists := index.byID[input.TargetRevisionID]
	if !exists {
		return correctionCutoffIndex{}, rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "target projection is missing",
		)
	}
	if index.current[target.Artifact()] != input.TargetRevisionID {
		return correctionCutoffIndex{}, rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "target projection is not current",
		)
	}
	if err := index.validateEvents(input.Events); err != nil {
		return correctionCutoffIndex{}, err
	}
	return index, nil
}

func (i correctionCutoffIndex) validateRevisionChains(
	edges map[[2]domain.ArenaDerivedRevisionID]struct{},
) error {
	previous := make(map[domain.ArenaArtifactRef]domain.ArenaDerivedRevision)
	for _, revision := range i.ordered {
		prior, exists := previous[revision.Artifact()]
		if !exists {
			if revision.RevisionNo() != 1 || revision.PreviousRevisionID() != nil {
				return rejectCorrection(
					CorrectionRejectionMalformed, ErrInvalidCorrection, "broken projection lineage",
				)
			}
			previous[revision.Artifact()] = revision
			continue
		}
		predecessor := revision.PreviousRevisionID()
		if prior.RevisionNo() == int(^uint(0)>>1) || revision.RevisionNo() != prior.RevisionNo()+1 ||
			predecessor == nil || *predecessor != prior.ID() {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "broken projection lineage",
			)
		}
		if _, linked := edges[[2]domain.ArenaDerivedRevisionID{prior.ID(), revision.ID()}]; !linked {
			return rejectCorrection(
				CorrectionRejectionIncomplete, ErrInvalidCorrection, "projection predecessor edge is missing",
			)
		}
		previous[revision.Artifact()] = revision
	}
	return nil
}

func (i correctionCutoffIndex) isAcyclic() bool {
	indegree := make(map[domain.ArenaDerivedRevisionID]int, len(i.ordered))
	for _, revision := range i.ordered {
		indegree[revision.ID()] = 0
	}
	for _, derivedIDs := range i.outgoing {
		for _, derivedID := range derivedIDs {
			indegree[derivedID]++
		}
	}
	queue := make([]domain.ArenaDerivedRevisionID, 0, len(i.ordered))
	for _, revision := range i.ordered {
		if indegree[revision.ID()] == 0 {
			queue = append(queue, revision.ID())
		}
	}
	processed := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		processed++
		for _, derived := range i.outgoing[current] {
			indegree[derived]--
			if indegree[derived] == 0 {
				queue = append(queue, derived)
			}
		}
	}
	return processed == len(i.ordered)
}

func snapshotRevisions(snapshot RevisionDAGSnapshot) []domain.ArenaDerivedRevision {
	revisions := make([]domain.ArenaDerivedRevision, len(snapshot.Projections))
	for index := range snapshot.Projections {
		revisions[index] = snapshot.Projections[index].Revision()
	}
	return revisions
}

func (i correctionCutoffIndex) validateEvents(events []CorrectionCutoffEvent) error {
	seen := make(map[uuid.UUID]struct{}, len(events))
	for _, event := range events {
		if event.TournamentID != i.tournamentID {
			return rejectCorrection(
				CorrectionRejectionCrossTournament, ErrInvalidCorrection, "cutoff event belongs to another tournament",
			)
		}
		source, exists := i.byID[event.SourceRevisionID]
		if !exists {
			return rejectCorrection(
				CorrectionRejectionStale, ErrInvalidCorrection, "cutoff event source is missing",
			)
		}
		if event.ID == uuid.Nil || !validCorrectionCutoffKind(event.Kind) ||
			!validCorrectionTime(event.OccurredAt) || event.OccurredAt.Before(source.CreatedAt()) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid cutoff event",
			)
		}
		if _, duplicate := seen[event.ID]; duplicate {
			return rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "duplicate cutoff event identity",
			)
		}
		if _, aliased := i.reservedIDs[event.ID]; aliased {
			return rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "cutoff event identity aliases revision evidence",
			)
		}
		seen[event.ID] = struct{}{}
	}
	return nil
}

func validCorrectionCutoffKind(kind CorrectionCutoffKind) bool {
	switch kind {
	case CorrectionCutoffWaveStarted,
		CorrectionCutoffTaskDelivered,
		CorrectionCutoffNoShowRecorded,
		CorrectionCutoffForfeitRecorded,
		CorrectionCutoffGoldenAllocated:
		return true
	default:
		return false
	}
}

func validCorrectionTime(value time.Time) bool {
	return validArenaServerTime(value) && value.Year() >= minCorrectionYear && value.Year() <= maxCorrectionYear
}

func canonicalCorrectionTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

//nolint:gocyclo // Iterative traversal keeps cycle, bounds, and deterministic ordering explicit.
func (i correctionCutoffIndex) orderedDescendants(
	target domain.ArenaDerivedRevisionID,
) ([]domain.ArenaDerivedRevision, map[domain.ArenaDerivedRevisionID]struct{}, error) {
	affected := map[domain.ArenaDerivedRevisionID]struct{}{target: {}}
	pending := []domain.ArenaDerivedRevisionID{target}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, derived := range i.outgoing[current] {
			if _, seen := affected[derived]; seen {
				continue
			}
			affected[derived] = struct{}{}
			pending = append(pending, derived)
		}
	}

	indegree := make(map[domain.ArenaDerivedRevisionID]int, len(affected)-1)
	for _, revision := range i.ordered {
		if revision.ID() != target {
			if _, included := affected[revision.ID()]; included {
				indegree[revision.ID()] = 0
			}
		}
	}
	for source := range affected {
		for _, derived := range i.outgoing[source] {
			if source == target {
				continue
			}
			if _, included := indegree[derived]; included {
				indegree[derived]++
			}
		}
	}
	queue := make([]domain.ArenaDerivedRevisionID, 0, len(indegree))
	for _, revision := range i.ordered {
		if count, included := indegree[revision.ID()]; included && count == 0 {
			queue = append(queue, revision.ID())
		}
	}
	descendants := make([]domain.ArenaDerivedRevision, 0, len(indegree))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		descendants = append(descendants, i.byID[current])
		for _, derived := range i.outgoing[current] {
			count, included := indegree[derived]
			if !included {
				continue
			}
			count--
			indegree[derived] = count
			if count == 0 {
				queue = append(queue, derived)
			}
		}
	}
	if len(descendants) != len(indegree) {
		return nil, nil, rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "descendant graph is cyclic",
		)
	}
	return descendants, affected, nil
}

func rejectCorrection(
	code CorrectionRejectionCode,
	cause error,
	detail string,
) error {
	return &CorrectionError{code: code, cause: cause, detail: detail}
}
