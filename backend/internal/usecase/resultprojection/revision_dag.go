package resultprojection

import (
	"errors"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

const (
	maxRevisionDAGProjections  = 65536
	maxRevisionDAGDependencies = 262144
	maxRevisionDAGResults      = 65536
	maxRevisionDAGPayloadBytes = 16 << 20
)

var ErrInvalidRevisionDAG = errors.New("invalid official revision DAG")

type RevisionDAGInput struct {
	Graph   domain.RevisionGraph
	Results []OfficialResultProjectionInput
}

type RevisionDAGSnapshot struct {
	Projections  []domain.ProjectionRevision
	Dependencies []domain.RevisionDependency
}

type RevisionDAG struct {
	graph   domain.RevisionGraph
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
			key := revisionDAGScopeKey(resultusecase.OfficialResultScope{
				TournamentID: recorded.Scope.TournamentID,
				SeriesID:     recorded.Scope.SeriesID,
				Kind:         resultusecase.OfficialResultSubjectSeries,
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
	return result.Scope != (resultusecase.OfficialResultScope{}) || !result.ID.IsZero() ||
		result.PreviousRevisionID != nil || result.Ordinal != 0 || result.CommandID != uuid.Nil ||
		result.Actor != (domain.ResultActor{}) || result.Outcome != (resultusecase.OfficialResultOutcome{}) ||
		!result.SourceProjection.ID().IsZero() || !result.RecordedAt.IsZero() || input.Score != nil ||
		input.ScoreProjection != nil || !input.ResultProjection.Revision().ID().IsZero()
}

func preflightOrdinaryResultIdentities(
	identities *revisionDAGIdentityRegistry,
	input OfficialResultProjectionInput,
) error {
	var resultArtifact domain.ArtifactRef
	switch input.Result.Scope.Kind {
	case resultusecase.OfficialResultSubjectGame:
		if input.Score != nil || input.ScoreProjection != nil {
			return invalidRevisionDAG("Game result preflight has Series score evidence")
		}
		resultArtifact = domain.ArtifactRef{
			Kind: domain.ArtifactKindGameResult, EntityID: input.Result.Scope.GameID,
		}
	case resultusecase.OfficialResultSubjectSeries:
		if input.Score == nil || input.ScoreProjection == nil {
			return invalidRevisionDAG("Series result preflight is missing score evidence")
		}
		resultArtifact = domain.ArtifactRef{
			Kind: domain.ArtifactKindSeriesResult, EntityID: input.Result.Scope.SeriesID,
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
			domain.ArtifactRef{
				Kind: domain.ArtifactKindSeriesScore, EntityID: input.Score.Scope.SeriesID,
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
		domain.ArtifactRef{
			Kind: domain.ArtifactKindSeriesScore, EntityID: recorded.Scope.SeriesID,
		},
	); err != nil {
		return err
	}
	if err := preflightExpectedProjectionMetadata(
		recorded.ResultSourceRevision, recorded.ResultProjection, recorded.Scope.TournamentID,
		domain.ArtifactRef{
			Kind: domain.ArtifactKindSeriesResult, EntityID: recorded.Scope.SeriesID,
		},
	); err != nil {
		return err
	}
	for index := range recorded.GameSourceRevisions {
		if err := preflightExpectedProjectionMetadata(
			recorded.GameSourceRevisions[index], recorded.GameProjections[index],
			recorded.Scope.TournamentID,
			domain.ArtifactRef{
				Kind: domain.ArtifactKindGameResult, EntityID: recorded.GameResults[index].GameID,
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
	source domain.DerivedRevision,
	projection domain.ProjectionRevision,
	tournamentID uuid.UUID,
	artifact domain.ArtifactRef,
) error {
	if source.Validate() != nil || !derivedRevisionsEqual(source, projection.Revision()) ||
		source.TournamentID() != tournamentID || source.Artifact() != artifact {
		return invalidRevisionDAG("projection metadata does not match its expected owner")
	}
	return nil
}

//nolint:gocyclo // Ordinary and no-game projection modes share one bounded graph metadata index.
func preflightRevisionDAGGraphBindings(
	graph domain.RevisionGraph,
	results []OfficialResultProjectionInput,
) error {
	byID := make(map[domain.DerivedRevisionID]domain.DerivedRevision)
	identities := newRevisionDAGIdentityRegistry()
	for _, projection := range graph.Projections() {
		revision := projection.Revision()
		byID[revision.ID()] = revision
		if err := claimRevisionDAGGraphIdentity(identities, revision); err != nil {
			return err
		}
	}
	check := func(revision domain.DerivedRevision) error {
		graphRevision, exists := byID[revision.ID()]
		if !exists || !derivedRevisionsEqual(graphRevision, revision) {
			return invalidRevisionDAG("result projection metadata is not in the graph")
		}
		return nil
	}
	checkProjection := func(
		source domain.DerivedRevision,
		projection domain.ProjectionRevision,
	) error {
		if !derivedRevisionsEqual(source, projection.Revision()) {
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
	if _, err := domain.NewRevisionGraph(projections, dependencies); err != nil {
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

func (d RevisionDAG) Inputs() []OfficialResultProjectionInput {
	inputs := make([]OfficialResultProjectionInput, len(d.results))
	for index := range d.results {
		input, err := cloneOfficialResultProjectionInput(d.results[index].input)
		if err != nil {
			return nil
		}
		inputs[index] = input
	}
	return inputs
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
