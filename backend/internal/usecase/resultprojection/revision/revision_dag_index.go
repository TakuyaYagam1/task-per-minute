package revision

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

type revisionDAGIndex struct {
	graph        domain.RevisionGraph
	byID         map[domain.DerivedRevisionID]domain.ProjectionRevision
	current      map[domain.ArtifactRef]domain.DerivedRevisionID
	sources      map[domain.DerivedRevisionID][]domain.DerivedRevisionID
	gameByResult map[domain.OfficialResultRevisionID]OfficialResultProjectionPlan
	series       []OfficialResultProjectionPlan
	tournamentID uuid.UUID
}

func newRevisionDAGIndex(
	graph domain.RevisionGraph,
	results []OfficialResultProjectionPlan,
) (revisionDAGIndex, error) {
	projections := graph.Projections()
	index := revisionDAGIndex{
		graph:        graph,
		byID:         make(map[domain.DerivedRevisionID]domain.ProjectionRevision, len(projections)),
		current:      make(map[domain.ArtifactRef]domain.DerivedRevisionID),
		sources:      make(map[domain.DerivedRevisionID][]domain.DerivedRevisionID),
		gameByResult: make(map[domain.OfficialResultRevisionID]OfficialResultProjectionPlan),
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
			key := revisionDAGScopeKey(resultusecase.OfficialResultScope{
				TournamentID: input.NoGame.Scope.TournamentID,
				SeriesID:     input.NoGame.Scope.SeriesID,
				Kind:         resultusecase.OfficialResultSubjectSeries,
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
		case resultusecase.OfficialResultSubjectGame:
			i.gameByResult[input.Result.ID] = plan
		case resultusecase.OfficialResultSubjectSeries:
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
	if result.Scope.Kind == resultusecase.OfficialResultSubjectGame {
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
	if result.Scope.Kind == resultusecase.OfficialResultSubjectGame {
		resultUse.kind = "Game result"
	}
	if err := identities.claimCommand(result.CommandID, resultUse); err != nil {
		return err
	}
	if result.PreviousRevisionID != nil && !result.HasCorrectionSourceIdentity() {
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
	if score.PreviousRevisionID != nil && !score.HasCorrectionSourceIdentity() {
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

func revisionDAGArtifactEntityRole(kind domain.ArtifactKind) string {
	switch kind {
	case domain.ArtifactKindGameResult:
		return "Game"
	case domain.ArtifactKindSeriesScore, domain.ArtifactKindSeriesResult:
		return "Series"
	case domain.ArtifactKindStandings, domain.ArtifactKindTopFour,
		domain.ArtifactKindBracket, domain.ArtifactKindChampion:
		return "tournament"
	case domain.ArtifactKindGoldenGroup:
		return "Golden group"
	default:
		return "artifact entity"
	}
}
