package arena

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	maxRevisionDAGProjections  = 65536
	maxRevisionDAGDependencies = 262144
	maxRevisionDAGResults      = 65536
	maxRevisionDAGPayloadBytes = 16 << 20
)

var ErrInvalidRevisionDAG = errors.New("invalid official Arena revision DAG")

type RevisionDAGInput struct {
	Graph   domain.ArenaRevisionGraph
	Results []OfficialResultProjectionInput
}

type RevisionDAGSnapshot struct {
	Projections  []domain.ArenaProjectionRevision
	Dependencies []domain.ArenaRevisionDependency
}

type RevisionDAG struct {
	graph   domain.ArenaRevisionGraph
	results []OfficialResultProjectionPlan
}

func BuildRevisionDAG(input RevisionDAGInput) (RevisionDAG, error) {
	if err := preflightRevisionDAGResults(input.Results); err != nil {
		return RevisionDAG{}, err
	}
	graph, err := cloneAndBoundRevisionGraph(input.Graph)
	if err != nil {
		return RevisionDAG{}, err
	}
	if err := preflightRevisionDAGGraphBindings(graph, input.Results); err != nil {
		return RevisionDAG{}, err
	}
	results := make([]OfficialResultProjectionPlan, len(input.Results))
	for index := range input.Results {
		plan, projectionErr := ProjectOfficialResult(input.Results[index])
		if projectionErr != nil {
			return RevisionDAG{}, invalidRevisionDAG("result %d: %v", index, projectionErr)
		}
		results[index] = plan
	}
	canonicalOfficialResultPlans(results)
	dag := RevisionDAG{graph: graph, results: results}
	if err := dag.Validate(); err != nil {
		return RevisionDAG{}, err
	}
	return dag, nil
}

func preflightRevisionDAGResults(results []OfficialResultProjectionInput) error {
	if len(results) == 0 || len(results) > maxRevisionDAGResults {
		return invalidRevisionDAG("invalid result count")
	}
	identities := newRevisionDAGIdentityRegistry()
	scopes := make(map[string]struct{}, len(results))
	for index := range results {
		input := results[index]
		if input.NoGame != nil {
			if ordinaryResultMetadataPresent(input) {
				return invalidRevisionDAG("mixed result evidence")
			}
			recorded := input.NoGame
			key := revisionDAGScopeKey(OfficialResultScope{
				TournamentID: recorded.Scope.TournamentID,
				SeriesID:     recorded.Scope.SeriesID,
				Kind:         OfficialResultSubjectSeries,
			})
			if _, duplicate := scopes[key]; duplicate {
				return invalidRevisionDAG("duplicate result scope")
			}
			scopes[key] = struct{}{}
			if err := preflightNoGameResultIdentities(identities, *recorded); err != nil {
				return err
			}
			continue
		}
		key := revisionDAGScopeKey(input.Result.Scope)
		if _, duplicate := scopes[key]; duplicate {
			return invalidRevisionDAG("duplicate result scope")
		}
		scopes[key] = struct{}{}
		if err := preflightOrdinaryResultIdentities(identities, input); err != nil {
			return err
		}
	}
	return identities.validateCommandUses()
}

func ordinaryResultMetadataPresent(input OfficialResultProjectionInput) bool {
	result := input.Result
	return result.Scope != (OfficialResultScope{}) || !result.ID.IsZero() ||
		result.PreviousRevisionID != nil || result.Ordinal != 0 || result.CommandID != uuid.Nil ||
		result.Actor != (ArenaResultActor{}) || result.Outcome != (OfficialResultOutcome{}) ||
		!result.SourceProjection.ID().IsZero() || !result.RecordedAt.IsZero() || input.Score != nil ||
		input.ScoreProjection != nil || !input.ResultProjection.Revision().ID().IsZero()
}

func preflightOrdinaryResultIdentities(
	identities *revisionDAGIdentityRegistry,
	input OfficialResultProjectionInput,
) error {
	var resultArtifact domain.ArenaArtifactRef
	switch input.Result.Scope.Kind {
	case OfficialResultSubjectGame:
		if input.Score != nil || input.ScoreProjection != nil {
			return invalidRevisionDAG("Game result preflight has Series score evidence")
		}
		resultArtifact = domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindGameResult, EntityID: input.Result.Scope.GameID,
		}
	case OfficialResultSubjectSeries:
		if input.Score == nil || input.ScoreProjection == nil {
			return invalidRevisionDAG("Series result preflight is missing score evidence")
		}
		resultArtifact = domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindSeriesResult, EntityID: input.Result.Scope.SeriesID,
		}
	default:
		return invalidRevisionDAG("unknown result subject in preflight")
	}
	if err := preflightExpectedProjectionMetadata(
		input.Result.SourceProjection, input.ResultProjection,
		input.Result.Scope.TournamentID, resultArtifact,
	); err != nil {
		return err
	}
	if input.Score != nil {
		if !input.Score.Format.IsValid() ||
			len(input.Score.Attempts) > input.Score.Format.WinsRequired()*2-1 {
			return invalidRevisionDAG("score preflight has too many attempts")
		}
		if err := preflightExpectedProjectionMetadata(
			input.Score.SourceProjection, *input.ScoreProjection,
			input.Score.Scope.TournamentID,
			domain.ArenaArtifactRef{
				Kind: domain.ArenaArtifactKindSeriesScore, EntityID: input.Score.Scope.SeriesID,
			},
		); err != nil {
			return err
		}
		if input.Result.SourceProjection.ID() == input.Score.SourceProjection.ID() {
			return invalidRevisionDAG("result and score projections share an owner")
		}
	}
	return claimOrdinaryDAGIdentities(identities, input)
}

func preflightNoGameResultIdentities(
	identities *revisionDAGIdentityRegistry,
	recorded RecordedNoGameResult,
) error {
	gameCount := len(recorded.GameResults)
	if gameCount == 0 || gameCount > 16 || len(recorded.Topology) != gameCount ||
		len(recorded.GameSourceRevisions) != gameCount || len(recorded.GameProjections) != gameCount ||
		len(recorded.GameDependencies) != gameCount || len(recorded.Score.GameResultRevisionIDs) != gameCount {
		return invalidRevisionDAG("invalid no-game preflight shape")
	}
	if err := preflightExpectedProjectionMetadata(
		recorded.ScoreSourceRevision, recorded.ScoreProjection, recorded.Scope.TournamentID,
		domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindSeriesScore, EntityID: recorded.Scope.SeriesID,
		},
	); err != nil {
		return err
	}
	if err := preflightExpectedProjectionMetadata(
		recorded.ResultSourceRevision, recorded.ResultProjection, recorded.Scope.TournamentID,
		domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindSeriesResult, EntityID: recorded.Scope.SeriesID,
		},
	); err != nil {
		return err
	}
	for index := range recorded.GameSourceRevisions {
		if err := preflightExpectedProjectionMetadata(
			recorded.GameSourceRevisions[index], recorded.GameProjections[index],
			recorded.Scope.TournamentID,
			domain.ArenaArtifactRef{
				Kind: domain.ArenaArtifactKindGameResult, EntityID: recorded.GameResults[index].GameID,
			},
		); err != nil {
			return err
		}
	}
	if err := validateRecordedNoGameIdentities(recorded); err != nil {
		return invalidRevisionDAG("invalid no-game owner preflight: %v", err)
	}
	return claimNoGameDAGIdentities(identities, recorded)
}

func preflightExpectedProjectionMetadata(
	source domain.ArenaDerivedRevision,
	projection domain.ArenaProjectionRevision,
	tournamentID uuid.UUID,
	artifact domain.ArenaArtifactRef,
) error {
	if source.Validate() != nil || !arenaDerivedRevisionsEqual(source, projection.Revision()) ||
		source.TournamentID() != tournamentID || source.Artifact() != artifact {
		return invalidRevisionDAG("projection metadata does not match its expected owner")
	}
	return nil
}

//nolint:gocyclo // Ordinary and no-game projection modes share one bounded graph metadata index.
func preflightRevisionDAGGraphBindings(
	graph domain.ArenaRevisionGraph,
	results []OfficialResultProjectionInput,
) error {
	byID := make(map[domain.ArenaDerivedRevisionID]domain.ArenaDerivedRevision)
	identities := newRevisionDAGIdentityRegistry()
	for _, projection := range graph.Projections() {
		revision := projection.Revision()
		byID[revision.ID()] = revision
		if err := claimRevisionDAGGraphIdentity(identities, revision); err != nil {
			return err
		}
	}
	check := func(revision domain.ArenaDerivedRevision) error {
		graphRevision, exists := byID[revision.ID()]
		if !exists || !arenaDerivedRevisionsEqual(graphRevision, revision) {
			return invalidRevisionDAG("result projection metadata is not in the graph")
		}
		return nil
	}
	checkProjection := func(
		source domain.ArenaDerivedRevision,
		projection domain.ArenaProjectionRevision,
	) error {
		if !arenaDerivedRevisionsEqual(source, projection.Revision()) {
			return invalidRevisionDAG("result projection metadata does not match its recorded source")
		}
		return check(source)
	}
	for index := range results {
		input := results[index]
		if input.NoGame != nil {
			recorded := input.NoGame
			if len(recorded.GameSourceRevisions) != len(recorded.GameProjections) {
				return invalidRevisionDAG("no-game projection metadata is incomplete")
			}
			if err := checkProjection(recorded.ScoreSourceRevision, recorded.ScoreProjection); err != nil {
				return err
			}
			if err := checkProjection(recorded.ResultSourceRevision, recorded.ResultProjection); err != nil {
				return err
			}
			for sourceIndex, source := range recorded.GameSourceRevisions {
				if err := checkProjection(source, recorded.GameProjections[sourceIndex]); err != nil {
					return err
				}
			}
			if err := preflightNoGameResultIdentities(identities, *recorded); err != nil {
				return err
			}
			continue
		}
		if err := checkProjection(input.Result.SourceProjection, input.ResultProjection); err != nil {
			return err
		}
		if input.Score != nil {
			if input.ScoreProjection == nil {
				return invalidRevisionDAG("Series score projection metadata is missing")
			}
			if err := checkProjection(input.Score.SourceProjection, *input.ScoreProjection); err != nil {
				return err
			}
		}
		if err := preflightOrdinaryResultIdentities(identities, input); err != nil {
			return err
		}
	}
	return identities.validateCommandUses()
}

func (d RevisionDAG) Validate() error {
	if len(d.results) == 0 || len(d.results) > maxRevisionDAGResults {
		return invalidRevisionDAG("invalid result count")
	}
	projections := d.graph.Projections()
	dependencies := d.graph.Dependencies()
	if len(projections) == 0 || len(projections) > maxRevisionDAGProjections ||
		len(dependencies) > maxRevisionDAGDependencies {
		return invalidRevisionDAG("invalid graph size")
	}
	if _, err := domain.NewArenaRevisionGraph(projections, dependencies); err != nil {
		return invalidRevisionDAG("invalid base graph: %v", err)
	}
	for index := range d.results {
		if err := d.results[index].Validate(); err != nil {
			return invalidRevisionDAG("invalid result projection: %v", err)
		}
	}
	index, err := newRevisionDAGIndex(d.graph, d.results)
	if err != nil {
		return err
	}
	if err := index.validateEdges(); err != nil {
		return err
	}
	if err := index.validateResultClosure(); err != nil {
		return err
	}
	return index.validateDownstreamClosure()
}

func (d RevisionDAG) Snapshot() RevisionDAGSnapshot {
	return RevisionDAGSnapshot{
		Projections:  d.graph.Projections(),
		Dependencies: d.graph.Dependencies(),
	}
}

func (d RevisionDAG) PublicResults() []PublicOfficialResult {
	results := make([]PublicOfficialResult, len(d.results))
	for index := range d.results {
		results[index] = d.results[index].Public()
	}
	return results
}

func (d RevisionDAG) OperatorResults() []OperatorOfficialResult {
	results := make([]OperatorOfficialResult, len(d.results))
	for index := range d.results {
		results[index] = d.results[index].Operator()
	}
	return results
}

type revisionDAGIndex struct {
	graph        domain.ArenaRevisionGraph
	byID         map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision
	current      map[domain.ArenaArtifactRef]domain.ArenaDerivedRevisionID
	sources      map[domain.ArenaDerivedRevisionID][]domain.ArenaDerivedRevisionID
	gameByResult map[domain.ArenaOfficialResultRevisionID]OfficialResultProjectionPlan
	series       []OfficialResultProjectionPlan
	tournamentID uuid.UUID
}

type revisionDAGIdentityRegistry struct {
	roles       map[uuid.UUID]string
	officialIDs map[domain.ArenaOfficialResultRevisionID]struct{}
	scoreIDs    map[domain.ArenaSeriesScoreRevisionID]struct{}
	boundOwners map[string]uuid.UUID
	commandUses map[uuid.UUID][]revisionDAGCommandUse
}

type revisionDAGCommandUse struct {
	kind            string
	resultID        domain.ArenaOfficialResultRevisionID
	commandResultID domain.ArenaOfficialResultRevisionID
}

func newRevisionDAGIdentityRegistry() *revisionDAGIdentityRegistry {
	return &revisionDAGIdentityRegistry{
		roles:       make(map[uuid.UUID]string),
		officialIDs: make(map[domain.ArenaOfficialResultRevisionID]struct{}),
		scoreIDs:    make(map[domain.ArenaSeriesScoreRevisionID]struct{}),
		boundOwners: make(map[string]uuid.UUID),
		commandUses: make(map[uuid.UUID][]revisionDAGCommandUse),
	}
}

func (r *revisionDAGIdentityRegistry) claimBound(
	id uuid.UUID,
	role string,
	owner uuid.UUID,
) error {
	if err := r.claimRole(id, role); err != nil {
		return err
	}
	key := role + "/" + id.String()
	if previous, exists := r.boundOwners[key]; exists && previous != owner {
		return invalidRevisionDAG("%s identity is reused across owners", role)
	}
	r.boundOwners[key] = owner
	return nil
}

func (r *revisionDAGIdentityRegistry) claimCommand(
	id uuid.UUID,
	use revisionDAGCommandUse,
) error {
	if err := r.claimRole(id, "command"); err != nil {
		return err
	}
	r.commandUses[id] = append(r.commandUses[id], use)
	return nil
}

func (r *revisionDAGIdentityRegistry) validateCommandUses() error {
	for _, uses := range r.commandUses {
		if len(uses) == 1 {
			if uses[0].kind == "score" && !uses[0].commandResultID.IsZero() {
				return invalidRevisionDAG("score command has no exact Game result owner")
			}
			continue
		}
		if len(uses) != 2 {
			return invalidRevisionDAG("command identity has multiple owners")
		}
		first, second := uses[0], uses[1]
		if first.kind == "score" {
			first, second = second, first
		}
		if first.kind != "Game result" || second.kind != "score" ||
			second.commandResultID.IsZero() || second.commandResultID != first.resultID {
			return invalidRevisionDAG("command identity is not an exact Game result to score cascade")
		}
	}
	return nil
}

func (r *revisionDAGIdentityRegistry) claimRole(id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return invalidRevisionDAG("missing %s identity", role)
	}
	if previous, exists := r.roles[id]; exists && previous != role {
		return invalidRevisionDAG("identity aliases %s and %s", previous, role)
	}
	r.roles[id] = role
	return nil
}

func (r *revisionDAGIdentityRegistry) claimOfficial(
	id domain.ArenaOfficialResultRevisionID,
) error {
	if id.IsZero() {
		return invalidRevisionDAG("missing official result identity")
	}
	if _, duplicate := r.officialIDs[id]; duplicate {
		return invalidRevisionDAG("duplicate official result identity")
	}
	r.officialIDs[id] = struct{}{}
	return r.claimRole(id.UUID(), "official result revision")
}

func (r *revisionDAGIdentityRegistry) claimScore(id domain.ArenaSeriesScoreRevisionID) error {
	if id.IsZero() {
		return invalidRevisionDAG("missing score identity")
	}
	if _, duplicate := r.scoreIDs[id]; duplicate {
		return invalidRevisionDAG("duplicate score identity")
	}
	r.scoreIDs[id] = struct{}{}
	return r.claimRole(id.UUID(), "score revision")
}

func claimRevisionDAGGraphIdentity(
	identities *revisionDAGIdentityRegistry,
	revision domain.ArenaDerivedRevision,
) error {
	if err := identities.claimRole(revision.ID().UUID(), "projection revision"); err != nil {
		return err
	}
	if err := identities.claimRole(revision.TournamentID(), "tournament"); err != nil {
		return err
	}
	entityRole := revisionDAGArtifactEntityRole(revision.Artifact().Kind)
	if revision.Artifact().Kind == domain.ArenaArtifactKindGoldenGroup &&
		revision.Artifact().EntityID == revision.TournamentID() {
		entityRole = "tournament"
	}
	if err := identities.claimRole(revision.Artifact().EntityID, entityRole); err != nil {
		return err
	}
	if previous := revision.PreviousRevisionID(); previous != nil {
		if err := identities.claimRole(previous.UUID(), "projection revision"); err != nil {
			return err
		}
	}
	return nil
}

func newRevisionDAGIndex(
	graph domain.ArenaRevisionGraph,
	results []OfficialResultProjectionPlan,
) (revisionDAGIndex, error) {
	projections := graph.Projections()
	index := revisionDAGIndex{
		graph:        graph,
		byID:         make(map[domain.ArenaDerivedRevisionID]domain.ArenaProjectionRevision, len(projections)),
		current:      make(map[domain.ArenaArtifactRef]domain.ArenaDerivedRevisionID),
		sources:      make(map[domain.ArenaDerivedRevisionID][]domain.ArenaDerivedRevisionID),
		gameByResult: make(map[domain.ArenaOfficialResultRevisionID]OfficialResultProjectionPlan),
	}
	for _, projection := range projections {
		revision := projection.Revision()
		if index.tournamentID == uuid.Nil {
			index.tournamentID = revision.TournamentID()
		} else if revision.TournamentID() != index.tournamentID {
			return revisionDAGIndex{}, invalidRevisionDAG("graph spans tournaments")
		}
		index.byID[revision.ID()] = projection
		currentID, exists := index.current[revision.Artifact()]
		if !exists || index.byID[currentID].Revision().RevisionNo() < revision.RevisionNo() {
			index.current[revision.Artifact()] = revision.ID()
		}
	}
	for _, dependency := range graph.Dependencies() {
		index.sources[dependency.DerivedRevisionID] = append(
			index.sources[dependency.DerivedRevisionID], dependency.SourceRevisionID,
		)
	}
	if err := index.indexResults(results); err != nil {
		return revisionDAGIndex{}, err
	}
	return index, nil
}

func (i *revisionDAGIndex) indexResults(results []OfficialResultProjectionPlan) error {
	scopes := make(map[string]struct{}, len(results))
	identities := newRevisionDAGIdentityRegistry()
	for _, projection := range i.byID {
		if err := claimRevisionDAGGraphIdentity(identities, projection.Revision()); err != nil {
			return err
		}
	}
	for _, plan := range results {
		input := plan.input
		if input.NoGame != nil {
			if err := claimNoGameDAGIdentities(identities, *input.NoGame); err != nil {
				return err
			}
			key := revisionDAGScopeKey(OfficialResultScope{
				TournamentID: input.NoGame.Scope.TournamentID,
				SeriesID:     input.NoGame.Scope.SeriesID,
				Kind:         OfficialResultSubjectSeries,
			})
			if _, duplicate := scopes[key]; duplicate {
				return invalidRevisionDAG("duplicate result scope")
			}
			scopes[key] = struct{}{}
			i.series = append(i.series, plan)
			continue
		}
		if err := claimOrdinaryDAGIdentities(identities, input); err != nil {
			return err
		}
		key := revisionDAGScopeKey(input.Result.Scope)
		if _, duplicate := scopes[key]; duplicate {
			return invalidRevisionDAG("duplicate result scope")
		}
		scopes[key] = struct{}{}
		if input.Result.Scope.TournamentID != i.tournamentID {
			return invalidRevisionDAG("result belongs to another tournament")
		}
		switch input.Result.Scope.Kind {
		case OfficialResultSubjectGame:
			i.gameByResult[input.Result.ID] = plan
		case OfficialResultSubjectSeries:
			i.series = append(i.series, plan)
		default:
			return invalidRevisionDAG("unknown result subject")
		}
	}
	return identities.validateCommandUses()
}

//nolint:gocyclo // Every role-bearing identity is audited together to reject aliases.
func claimOrdinaryDAGIdentities(
	identities *revisionDAGIdentityRegistry,
	input OfficialResultProjectionInput,
) error {
	result := input.Result
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{result.Scope.TournamentID, "tournament"},
		{result.Scope.SeriesID, "Series"},
		{result.SourceProjection.ID().UUID(), "projection revision"},
	} {
		if err := identities.claimRole(identity.id, identity.role); err != nil {
			return err
		}
	}
	if result.Scope.Kind == OfficialResultSubjectGame {
		if err := identities.claimBound(result.Scope.GameID, "Game", result.Scope.SeriesID); err != nil {
			return err
		}
	}
	if result.Actor.PrincipalID != nil {
		if err := identities.claimRole(*result.Actor.PrincipalID, "actor"); err != nil {
			return err
		}
	}
	if result.Outcome.WinnerID != nil {
		if err := identities.claimRole(*result.Outcome.WinnerID, "participant"); err != nil {
			return err
		}
	}
	if err := identities.claimOfficial(result.ID); err != nil {
		return err
	}
	resultUse := revisionDAGCommandUse{kind: "Series result", resultID: result.ID}
	if result.Scope.Kind == OfficialResultSubjectGame {
		resultUse.kind = "Game result"
	}
	if err := identities.claimCommand(result.CommandID, resultUse); err != nil {
		return err
	}
	if result.PreviousRevisionID != nil {
		if err := identities.claimOfficial(*result.PreviousRevisionID); err != nil {
			return err
		}
	}
	if input.Score == nil {
		return nil
	}
	score := input.Score
	if err := identities.claimScore(score.ID); err != nil {
		return err
	}
	if score.PreviousRevisionID != nil {
		if err := identities.claimScore(*score.PreviousRevisionID); err != nil {
			return err
		}
	}
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{score.Scope.TournamentID, "tournament"},
		{score.Scope.SeriesID, "Series"},
		{score.SourceProjection.ID().UUID(), "projection revision"},
		{score.FirstParticipantID, "participant"},
		{score.SecondParticipantID, "participant"},
	} {
		if err := identities.claimRole(identity.id, identity.role); err != nil {
			return err
		}
	}
	if score.Actor.PrincipalID != nil {
		if err := identities.claimRole(*score.Actor.PrincipalID, "actor"); err != nil {
			return err
		}
	}
	commandUse := revisionDAGCommandUse{kind: "score"}
	if score.CommandAttempt != nil {
		commandUse.commandResultID = score.CommandAttempt.CurrentGameResultRevisionID
	}
	if err := identities.claimCommand(score.CommandID, commandUse); err != nil {
		return err
	}
	for _, attempt := range score.Attempts {
		if err := identities.claimBound(attempt.SlotID, "slot", score.Scope.SeriesID); err != nil {
			return err
		}
		if err := identities.claimBound(attempt.GameID, "Game", score.Scope.SeriesID); err != nil {
			return err
		}
		if err := identities.claimRole(attempt.CurrentGameResultRevisionID.UUID(), "official result revision"); err != nil {
			return err
		}
		if attempt.WinnerID != nil {
			if err := identities.claimRole(*attempt.WinnerID, "participant"); err != nil {
				return err
			}
		}
	}
	return nil
}

//nolint:gocyclo // A no-game aggregate has one explicit identity boundary for every recorded owner.
func claimNoGameDAGIdentities(
	identities *revisionDAGIdentityRegistry,
	recorded RecordedNoGameResult,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{recorded.Scope.TournamentID, "tournament"},
		{recorded.Scope.WaveID, "Wave"},
		{recorded.Scope.WindowID, "window"},
		{recorded.Scope.SeriesID, "Series"},
		{recorded.FirstParticipantID, "participant"},
		{recorded.SecondParticipantID, "participant"},
		{recorded.ScoreProjection.Revision().ID().UUID(), "projection revision"},
		{recorded.ResultProjection.Revision().ID().UUID(), "projection revision"},
	} {
		if err := identities.claimRole(identity.id, identity.role); err != nil {
			return err
		}
	}
	if recorded.ReadyParticipantID != nil {
		if err := identities.claimRole(*recorded.ReadyParticipantID, "participant"); err != nil {
			return err
		}
	}
	if err := identities.claimCommand(recorded.CommandID, revisionDAGCommandUse{kind: "no-show"}); err != nil {
		return err
	}
	if err := identities.claimScore(recorded.Score.ID); err != nil {
		return err
	}
	if recorded.Score.PreviousRevisionID != nil {
		if err := identities.claimScore(*recorded.Score.PreviousRevisionID); err != nil {
			return err
		}
	}
	if err := identities.claimOfficial(recorded.Series.ID); err != nil {
		return err
	}
	if recorded.Series.WinnerID != nil {
		if err := identities.claimRole(*recorded.Series.WinnerID, "participant"); err != nil {
			return err
		}
	}
	if recorded.Series.PreviousRevisionID != nil {
		if err := identities.claimOfficial(*recorded.Series.PreviousRevisionID); err != nil {
			return err
		}
	}
	for index, game := range recorded.GameResults {
		if err := identities.claimOfficial(game.ID); err != nil {
			return err
		}
		if game.PreviousRevisionID != nil {
			if err := identities.claimOfficial(*game.PreviousRevisionID); err != nil {
				return err
			}
		}
		if err := identities.claimBound(game.GameID, "Game", recorded.Scope.SeriesID); err != nil {
			return err
		}
		if err := identities.claimBound(recorded.Topology[index].SlotID, "slot", recorded.Scope.SeriesID); err != nil {
			return err
		}
		if err := identities.claimRole(recorded.GameProjections[index].Revision().ID().UUID(), "projection revision"); err != nil {
			return err
		}
	}
	return nil
}

func revisionDAGArtifactEntityRole(kind domain.ArenaArtifactKind) string {
	switch kind {
	case domain.ArenaArtifactKindGameResult:
		return "Game"
	case domain.ArenaArtifactKindSeriesScore, domain.ArenaArtifactKindSeriesResult:
		return "Series"
	case domain.ArenaArtifactKindStandings, domain.ArenaArtifactKindTopFour,
		domain.ArenaArtifactKindBracket, domain.ArenaArtifactKindChampion:
		return "tournament"
	case domain.ArenaArtifactKindGoldenGroup:
		return "Golden group"
	default:
		return "artifact entity"
	}
}

func (i revisionDAGIndex) validateEdges() error {
	for derivedID, sourceIDs := range i.sources {
		derived := i.byID[derivedID].Revision()
		for _, sourceID := range sourceIDs {
			source := i.byID[sourceID].Revision()
			if source.Artifact() == derived.Artifact() {
				continue
			}
			if !validRevisionDAGEdge(source.Artifact().Kind, derived.Artifact().Kind) {
				return invalidRevisionDAG("illegal %s to %s shortcut", source.Artifact().Kind, derived.Artifact().Kind)
			}
		}
	}
	return nil
}

func validRevisionDAGEdge(source, derived domain.ArenaArtifactKind) bool {
	switch source {
	case domain.ArenaArtifactKindGameResult:
		return derived == domain.ArenaArtifactKindSeriesScore
	case domain.ArenaArtifactKindSeriesScore:
		return derived == domain.ArenaArtifactKindSeriesResult
	case domain.ArenaArtifactKindSeriesResult:
		return derived == domain.ArenaArtifactKindStandings
	case domain.ArenaArtifactKindStandings:
		return derived == domain.ArenaArtifactKindGoldenGroup || derived == domain.ArenaArtifactKindTopFour
	case domain.ArenaArtifactKindGoldenGroup:
		return derived == domain.ArenaArtifactKindTopFour
	case domain.ArenaArtifactKindTopFour:
		return derived == domain.ArenaArtifactKindBracket
	case domain.ArenaArtifactKindBracket:
		return derived == domain.ArenaArtifactKindChampion
	case domain.ArenaArtifactKindChampion:
		return false
	default:
		return false
	}
}

//nolint:gocyclo // Closure validation keeps the exact Game -> score -> Series chain atomic.
func (i revisionDAGIndex) validateResultClosure() error {
	usedGames := make(map[domain.ArenaOfficialResultRevisionID]struct{}, len(i.gameByResult))
	representedCurrent := make(map[domain.ArenaDerivedRevisionID]struct{})
	for _, plan := range i.series {
		input := plan.input
		if input.NoGame != nil {
			if err := i.validateNoGameClosure(*input.NoGame, representedCurrent); err != nil {
				return err
			}
			continue
		}
		resultSource := input.Result.SourceProjection.ID()
		scoreSource := input.Score.SourceProjection.ID()
		if err := i.requireExactCurrent(input.ResultProjection); err != nil {
			return err
		}
		if err := i.requireExactCurrent(*input.ScoreProjection); err != nil {
			return err
		}
		if !sameDerivedRevisionIDSet(i.crossKindSources(resultSource), []domain.ArenaDerivedRevisionID{scoreSource}) {
			return invalidRevisionDAG("Series result does not directly depend on current score")
		}
		representedCurrent[resultSource] = struct{}{}
		representedCurrent[scoreSource] = struct{}{}
		gameSources := make([]domain.ArenaDerivedRevisionID, 0, len(input.Score.Attempts))
		for _, attempt := range input.Score.Attempts {
			gamePlan, exists := i.gameByResult[attempt.CurrentGameResultRevisionID]
			if !exists {
				return invalidRevisionDAG("score attempt has no exact Game result head")
			}
			game := gamePlan.input.Result
			if game.Scope.TournamentID != input.Result.Scope.TournamentID ||
				game.Scope.SeriesID != input.Result.Scope.SeriesID || game.Scope.GameID != attempt.GameID ||
				!gameHeadMatchesScoreAttempt(game, attempt) || input.Score.RecordedAt.Before(game.RecordedAt) {
				return invalidRevisionDAG("Game result does not match score attempt")
			}
			gameSource := game.SourceProjection.ID()
			if err := i.requireExactCurrent(gamePlan.input.ResultProjection); err != nil {
				return err
			}
			gameSources = append(gameSources, gameSource)
			if _, duplicate := usedGames[game.ID]; duplicate {
				return invalidRevisionDAG("Game result is reused by scores")
			}
			usedGames[game.ID] = struct{}{}
			representedCurrent[gameSource] = struct{}{}
		}
		if !sameDerivedRevisionIDSet(i.crossKindSources(scoreSource), gameSources) {
			return invalidRevisionDAG("score does not depend on exactly its current Game results")
		}
	}
	if len(usedGames) != len(i.gameByResult) {
		return invalidRevisionDAG("unreferenced Game result head")
	}
	for artifact, id := range i.current {
		switch artifact.Kind {
		case domain.ArenaArtifactKindGameResult, domain.ArenaArtifactKindSeriesScore, domain.ArenaArtifactKindSeriesResult:
			if _, exists := representedCurrent[id]; !exists {
				return invalidRevisionDAG("current result projection has no exact head")
			}
		case domain.ArenaArtifactKindStandings, domain.ArenaArtifactKindGoldenGroup,
			domain.ArenaArtifactKindTopFour, domain.ArenaArtifactKindBracket,
			domain.ArenaArtifactKindChampion:
			continue
		default:
			return invalidRevisionDAG("unknown current artifact kind")
		}
	}
	return nil
}

func (i revisionDAGIndex) validateNoGameClosure(
	recorded RecordedNoGameResult,
	represented map[domain.ArenaDerivedRevisionID]struct{},
) error {
	scoreID := recorded.ScoreProjection.Revision().ID()
	resultID := recorded.ResultProjection.Revision().ID()
	if err := i.requireExactCurrent(recorded.ScoreProjection); err != nil {
		return err
	}
	if err := i.requireExactCurrent(recorded.ResultProjection); err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(resultID), []domain.ArenaDerivedRevisionID{scoreID}) {
		return invalidRevisionDAG("no-show result does not directly depend on score")
	}
	represented[scoreID] = struct{}{}
	represented[resultID] = struct{}{}
	gameIDs := make([]domain.ArenaDerivedRevisionID, 0, len(recorded.GameProjections))
	for _, projection := range recorded.GameProjections {
		gameID := projection.Revision().ID()
		if err := i.requireExactCurrent(projection); err != nil {
			return err
		}
		gameIDs = append(gameIDs, gameID)
		represented[gameID] = struct{}{}
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(scoreID), gameIDs) {
		return invalidRevisionDAG("no-show score does not depend on exactly its current Game results")
	}
	return nil
}

func (i revisionDAGIndex) validateDownstreamClosure() error {
	seriesResults := i.currentIDsByKind(domain.ArenaArtifactKindSeriesResult)
	if len(seriesResults) == 0 {
		return invalidRevisionDAG("missing current Series result")
	}
	standings, err := i.requireSingletonCurrent(domain.ArenaArtifactKindStandings)
	if err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(standings), seriesResults) {
		return invalidRevisionDAG("standings must directly consume every current Series result")
	}

	goldenGroups := i.currentIDsByKind(domain.ArenaArtifactKindGoldenGroup)
	for _, goldenGroup := range goldenGroups {
		if !sameDerivedRevisionIDSet(i.crossKindSources(goldenGroup), []domain.ArenaDerivedRevisionID{standings}) {
			return invalidRevisionDAG("Golden group must directly consume current standings")
		}
	}

	topFour, err := i.requireSingletonCurrent(domain.ArenaArtifactKindTopFour)
	if err != nil {
		return err
	}
	topFourSources := goldenGroups
	if len(topFourSources) == 0 {
		topFourSources = []domain.ArenaDerivedRevisionID{standings}
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(topFour), topFourSources) {
		return invalidRevisionDAG("Top Four must consume the exact current qualification sources")
	}

	bracket, err := i.requireSingletonCurrent(domain.ArenaArtifactKindBracket)
	if err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(bracket), []domain.ArenaDerivedRevisionID{topFour}) {
		return invalidRevisionDAG("bracket must directly consume current Top Four")
	}

	champion, err := i.requireSingletonCurrent(domain.ArenaArtifactKindChampion)
	if err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(champion), []domain.ArenaDerivedRevisionID{bracket}) {
		return invalidRevisionDAG("champion must directly consume current bracket")
	}
	return nil
}

func (i revisionDAGIndex) currentIDsByKind(kind domain.ArenaArtifactKind) []domain.ArenaDerivedRevisionID {
	ids := make([]domain.ArenaDerivedRevisionID, 0)
	for artifact, id := range i.current {
		if artifact.Kind == kind {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(first, second int) bool {
		return ids[first].UUID().String() < ids[second].UUID().String()
	})
	return ids
}

func (i revisionDAGIndex) requireSingletonCurrent(
	kind domain.ArenaArtifactKind,
) (domain.ArenaDerivedRevisionID, error) {
	ids := i.currentIDsByKind(kind)
	if len(ids) != 1 {
		return domain.ArenaDerivedRevisionID{}, invalidRevisionDAG("expected one current %s projection", kind)
	}
	revision := i.byID[ids[0]].Revision()
	if revision.Artifact().EntityID != i.tournamentID {
		return domain.ArenaDerivedRevisionID{}, invalidRevisionDAG("%s projection has foreign owner", kind)
	}
	return ids[0], nil
}

func (i revisionDAGIndex) crossKindSources(id domain.ArenaDerivedRevisionID) []domain.ArenaDerivedRevisionID {
	derived := i.byID[id].Revision()
	sources := make([]domain.ArenaDerivedRevisionID, 0, len(i.sources[id]))
	for _, sourceID := range i.sources[id] {
		if i.byID[sourceID].Revision().Artifact() != derived.Artifact() {
			sources = append(sources, sourceID)
		}
	}
	return sources
}

func (i revisionDAGIndex) requireCurrent(id domain.ArenaDerivedRevisionID) error {
	projection, exists := i.byID[id]
	if !exists || i.current[projection.Revision().Artifact()] != id {
		return invalidRevisionDAG("result head references a stale projection")
	}
	return nil
}

func (i revisionDAGIndex) requireExactCurrent(projection domain.ArenaProjectionRevision) error {
	id := projection.Revision().ID()
	if err := i.requireCurrent(id); err != nil {
		return err
	}
	graphProjection := i.byID[id]
	if !arenaDerivedRevisionsEqual(graphProjection.Revision(), projection.Revision()) ||
		!bytes.Equal(graphProjection.Payload(), projection.Payload()) {
		return invalidRevisionDAG("result evidence does not match the current graph projection")
	}
	return nil
}

func sameDerivedRevisionIDSet(
	left []domain.ArenaDerivedRevisionID,
	right []domain.ArenaDerivedRevisionID,
) bool {
	if len(left) != len(right) {
		return false
	}
	want := make(map[domain.ArenaDerivedRevisionID]struct{}, len(right))
	for _, id := range right {
		if _, duplicate := want[id]; duplicate {
			return false
		}
		want[id] = struct{}{}
	}
	for _, id := range left {
		if _, exists := want[id]; !exists {
			return false
		}
		delete(want, id)
	}
	return len(want) == 0
}

func gameHeadMatchesScoreAttempt(
	game OfficialResultRevisionHead,
	attempt SeriesScoreAttemptReference,
) bool {
	outcome := game.Outcome
	return outcome.GameState == attempt.State && outcome.GameReason == attempt.Reason &&
		uuidPointersEqual(outcome.WinnerID, attempt.WinnerID) && game.ID == attempt.CurrentGameResultRevisionID
}

func cloneAndBoundRevisionGraph(graph domain.ArenaRevisionGraph) (domain.ArenaRevisionGraph, error) {
	projections := graph.Projections()
	dependencies := graph.Dependencies()
	if len(projections) == 0 || len(projections) > maxRevisionDAGProjections ||
		len(dependencies) > maxRevisionDAGDependencies {
		return domain.ArenaRevisionGraph{}, invalidRevisionDAG("invalid graph size")
	}
	totalPayload := 0
	for _, projection := range projections {
		payloadSize := len(projection.Payload())
		if payloadSize > maxRevisionDAGPayloadBytes-totalPayload {
			return domain.ArenaRevisionGraph{}, invalidRevisionDAG("graph payload is too large")
		}
		totalPayload += payloadSize
	}
	cloned, err := domain.NewArenaRevisionGraph(projections, dependencies)
	if err != nil {
		return domain.ArenaRevisionGraph{}, invalidRevisionDAG("invalid base graph: %v", err)
	}
	return cloned, nil
}

func canonicalOfficialResultPlans(plans []OfficialResultProjectionPlan) {
	sort.Slice(plans, func(first, second int) bool {
		left := plans[first].Public()
		right := plans[second].Public()
		if left.TournamentID != right.TournamentID {
			return left.TournamentID.String() < right.TournamentID.String()
		}
		if left.SeriesID != right.SeriesID {
			return left.SeriesID.String() < right.SeriesID.String()
		}
		if left.Subject != right.Subject {
			return left.Subject < right.Subject
		}
		leftGame := uuid.Nil
		rightGame := uuid.Nil
		if left.GameID != nil {
			leftGame = *left.GameID
		}
		if right.GameID != nil {
			rightGame = *right.GameID
		}
		return leftGame.String() < rightGame.String()
	})
}

func revisionDAGScopeKey(scope OfficialResultScope) string {
	return scope.TournamentID.String() + "/" + scope.SeriesID.String() + "/" +
		string(scope.Kind) + "/" + scope.GameID.String()
}

func invalidRevisionDAG(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRevisionDAG, fmt.Sprintf(format, arguments...))
}
