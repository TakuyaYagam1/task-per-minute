package revision

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneAndBoundRevisionGraph(graph domain.RevisionGraph) (domain.RevisionGraph, error) {
	projections := graph.Projections()
	dependencies := graph.Dependencies()
	if len(projections) == 0 || len(projections) > maxRevisionDAGProjections ||
		len(dependencies) > maxRevisionDAGDependencies {
		return domain.RevisionGraph{}, invalidRevisionDAG("invalid graph size")
	}
	totalPayload := 0
	for _, projection := range projections {
		payloadSize := len(projection.Payload())
		if payloadSize > maxRevisionDAGPayloadBytes-totalPayload {
			return domain.RevisionGraph{}, invalidRevisionDAG("graph payload is too large")
		}
		totalPayload += payloadSize
	}
	cloned, err := domain.NewRevisionGraph(projections, dependencies)
	if err != nil {
		return domain.RevisionGraph{}, invalidRevisionDAG("invalid base graph: %v", err)
	}
	return cloned, nil
}

func invalidRevisionDAG(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRevisionDAG, fmt.Sprintf(format, arguments...))
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

func validRevisionDAGEdge(source, derived domain.ArtifactKind) bool {
	switch source {
	case domain.ArtifactKindGameResult:
		return derived == domain.ArtifactKindSeriesScore
	case domain.ArtifactKindSeriesScore:
		return derived == domain.ArtifactKindSeriesResult
	case domain.ArtifactKindSeriesResult:
		return derived == domain.ArtifactKindStandings
	case domain.ArtifactKindStandings:
		return derived == domain.ArtifactKindGoldenGroup || derived == domain.ArtifactKindTopFour
	case domain.ArtifactKindGoldenGroup:
		return derived == domain.ArtifactKindTopFour
	case domain.ArtifactKindTopFour:
		return derived == domain.ArtifactKindBracket
	case domain.ArtifactKindBracket:
		return derived == domain.ArtifactKindChampion
	case domain.ArtifactKindChampion:
		return false
	default:
		return false
	}
}

//nolint:gocyclo // Closure validation keeps the exact Game -> score -> Series chain atomic.
func (i revisionDAGIndex) validateResultClosure() error {
	usedGames := make(map[domain.OfficialResultRevisionID]struct{}, len(i.gameByResult))
	representedCurrent := make(map[domain.DerivedRevisionID]struct{})
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
		if !sameDerivedRevisionIDSet(i.crossKindSources(resultSource), []domain.DerivedRevisionID{scoreSource}) {
			return invalidRevisionDAG("Series result does not directly depend on current score")
		}
		representedCurrent[resultSource] = struct{}{}
		representedCurrent[scoreSource] = struct{}{}
		gameSources := make([]domain.DerivedRevisionID, 0, len(input.Score.Attempts))
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
		case domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore, domain.ArtifactKindSeriesResult:
			if _, exists := representedCurrent[id]; !exists {
				return invalidRevisionDAG("current result projection has no exact head")
			}
		case domain.ArtifactKindStandings, domain.ArtifactKindGoldenGroup,
			domain.ArtifactKindTopFour, domain.ArtifactKindBracket,
			domain.ArtifactKindChampion:
			continue
		default:
			return invalidRevisionDAG("unknown current artifact kind")
		}
	}
	return nil
}

func (i revisionDAGIndex) validateNoGameClosure(
	recorded RecordedNoGameResult,
	represented map[domain.DerivedRevisionID]struct{},
) error {
	scoreID := recorded.ScoreProjection.Revision().ID()
	resultID := recorded.ResultProjection.Revision().ID()
	if err := i.requireExactCurrent(recorded.ScoreProjection); err != nil {
		return err
	}
	if err := i.requireExactCurrent(recorded.ResultProjection); err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(resultID), []domain.DerivedRevisionID{scoreID}) {
		return invalidRevisionDAG("no-show result does not directly depend on score")
	}
	represented[scoreID] = struct{}{}
	represented[resultID] = struct{}{}
	gameIDs := make([]domain.DerivedRevisionID, 0, len(recorded.GameProjections))
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
	seriesResults := i.currentIDsByKind(domain.ArtifactKindSeriesResult)
	if len(seriesResults) == 0 {
		return invalidRevisionDAG("missing current Series result")
	}
	standings, err := i.requireSingletonCurrent(domain.ArtifactKindStandings)
	if err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(standings), seriesResults) {
		return invalidRevisionDAG("standings must directly consume every current Series result")
	}

	goldenGroups := i.currentIDsByKind(domain.ArtifactKindGoldenGroup)
	for _, goldenGroup := range goldenGroups {
		if !sameDerivedRevisionIDSet(i.crossKindSources(goldenGroup), []domain.DerivedRevisionID{standings}) {
			return invalidRevisionDAG("Golden group must directly consume current standings")
		}
	}

	topFour, err := i.requireSingletonCurrent(domain.ArtifactKindTopFour)
	if err != nil {
		return err
	}
	topFourSources := goldenGroups
	if len(topFourSources) == 0 {
		topFourSources = []domain.DerivedRevisionID{standings}
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(topFour), topFourSources) {
		return invalidRevisionDAG("Top Four must consume the exact current qualification sources")
	}

	bracket, err := i.requireSingletonCurrent(domain.ArtifactKindBracket)
	if err != nil {
		return err
	}
	if !sameDerivedRevisionIDSet(i.crossKindSources(bracket), []domain.DerivedRevisionID{topFour}) {
		return invalidRevisionDAG("bracket must directly consume current Top Four")
	}

	champions := i.currentIDsByKind(domain.ArtifactKindChampion)
	if len(champions) > 1 {
		return invalidRevisionDAG("expected at most one current champion projection")
	}
	if len(champions) == 1 && !sameDerivedRevisionIDSet(
		i.crossKindSources(champions[0]),
		[]domain.DerivedRevisionID{bracket},
	) {
		return invalidRevisionDAG("champion must directly consume current bracket")
	}
	return nil
}

func (i revisionDAGIndex) currentIDsByKind(kind domain.ArtifactKind) []domain.DerivedRevisionID {
	ids := make([]domain.DerivedRevisionID, 0)
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
	kind domain.ArtifactKind,
) (domain.DerivedRevisionID, error) {
	ids := i.currentIDsByKind(kind)
	if len(ids) != 1 {
		return domain.DerivedRevisionID{}, invalidRevisionDAG("expected one current %s projection", kind)
	}
	revision := i.byID[ids[0]].Revision()
	if revision.Artifact().EntityID != i.tournamentID {
		return domain.DerivedRevisionID{}, invalidRevisionDAG("%s projection has foreign owner", kind)
	}
	return ids[0], nil
}

func (i revisionDAGIndex) crossKindSources(id domain.DerivedRevisionID) []domain.DerivedRevisionID {
	derived := i.byID[id].Revision()
	sources := make([]domain.DerivedRevisionID, 0, len(i.sources[id]))
	for _, sourceID := range i.sources[id] {
		if i.byID[sourceID].Revision().Artifact() != derived.Artifact() {
			sources = append(sources, sourceID)
		}
	}
	return sources
}

func (i revisionDAGIndex) requireCurrent(id domain.DerivedRevisionID) error {
	projection, exists := i.byID[id]
	if !exists || i.current[projection.Revision().Artifact()] != id {
		return invalidRevisionDAG("result head references a stale projection")
	}
	return nil
}

func (i revisionDAGIndex) requireExactCurrent(projection domain.ProjectionRevision) error {
	id := projection.Revision().ID()
	if err := i.requireCurrent(id); err != nil {
		return err
	}
	graphProjection := i.byID[id]
	if !derivedRevisionsEqual(graphProjection.Revision(), projection.Revision()) ||
		!bytes.Equal(graphProjection.Payload(), projection.Payload()) {
		return invalidRevisionDAG("result evidence does not match the current graph projection")
	}
	return nil
}
