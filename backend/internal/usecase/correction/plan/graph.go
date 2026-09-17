package plan

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

type correctionRevisionDAGIdentityRegistry struct {
	roles       map[uuid.UUID]string
	officialIDs map[domain.OfficialResultRevisionID]struct{}
	scoreIDs    map[domain.SeriesScoreRevisionID]struct{}
	boundOwners map[string]uuid.UUID
	commandUses map[uuid.UUID][]correctionRevisionDAGCommandUse
}

type correctionRevisionDAGCommandUse struct {
	kind            string
	resultID        domain.OfficialResultRevisionID
	commandResultID domain.OfficialResultRevisionID
}

func newCorrectionRevisionDAGIdentityRegistry() *correctionRevisionDAGIdentityRegistry {
	return &correctionRevisionDAGIdentityRegistry{
		roles:       make(map[uuid.UUID]string),
		officialIDs: make(map[domain.OfficialResultRevisionID]struct{}),
		scoreIDs:    make(map[domain.SeriesScoreRevisionID]struct{}),
		boundOwners: make(map[string]uuid.UUID),
		commandUses: make(map[uuid.UUID][]correctionRevisionDAGCommandUse),
	}
}

func (r *correctionRevisionDAGIdentityRegistry) claimRole(id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return fmt.Errorf("missing %s identity", role)
	}
	if previous, exists := r.roles[id]; exists && previous != role {
		return fmt.Errorf("identity aliases %s and %s", previous, role)
	}
	r.roles[id] = role
	return nil
}

func (r *correctionRevisionDAGIdentityRegistry) claimBound(id uuid.UUID, role string, owner uuid.UUID) error {
	if err := r.claimRole(id, role); err != nil {
		return err
	}
	key := role + "/" + id.String()
	if previous, exists := r.boundOwners[key]; exists && previous != owner {
		return fmt.Errorf("%s identity is reused across owners", role)
	}
	r.boundOwners[key] = owner
	return nil
}

func (r *correctionRevisionDAGIdentityRegistry) claimCommand(id uuid.UUID, use correctionRevisionDAGCommandUse) error {
	if err := r.claimRole(id, "command"); err != nil {
		return err
	}
	r.commandUses[id] = append(r.commandUses[id], use)
	return nil
}

func (r *correctionRevisionDAGIdentityRegistry) validateCommandUses() error {
	for _, uses := range r.commandUses {
		if len(uses) == 1 {
			if uses[0].kind == "score" && !uses[0].commandResultID.IsZero() {
				return fmt.Errorf("score command has no exact Game result owner")
			}
			continue
		}
		if len(uses) != 2 {
			return fmt.Errorf("command identity has multiple owners")
		}
		first, second := uses[0], uses[1]
		if first.kind == "score" {
			first, second = second, first
		}
		if first.kind != "Game result" || second.kind != "score" ||
			second.commandResultID.IsZero() || second.commandResultID != first.resultID {
			return fmt.Errorf("command identity is not an exact Game result to score cascade")
		}
	}
	return nil
}

func (r *correctionRevisionDAGIdentityRegistry) claimOfficial(id domain.OfficialResultRevisionID) error {
	if id.IsZero() {
		return fmt.Errorf("missing official result identity")
	}
	if _, duplicate := r.officialIDs[id]; duplicate {
		return fmt.Errorf("duplicate official result identity")
	}
	r.officialIDs[id] = struct{}{}
	return r.claimRole(id.UUID(), "official result revision")
}

func (r *correctionRevisionDAGIdentityRegistry) claimScore(id domain.SeriesScoreRevisionID) error {
	if id.IsZero() {
		return fmt.Errorf("missing score identity")
	}
	if _, duplicate := r.scoreIDs[id]; duplicate {
		return fmt.Errorf("duplicate score identity")
	}
	r.scoreIDs[id] = struct{}{}
	return r.claimRole(id.UUID(), "score revision")
}

func claimCorrectionRevisionDAGGraphIdentity(
	identities *correctionRevisionDAGIdentityRegistry,
	revision domain.DerivedRevision,
) error {
	if err := identities.claimRole(revision.ID().UUID(), "projection revision"); err != nil {
		return err
	}
	if err := identities.claimRole(revision.TournamentID(), "tournament"); err != nil {
		return err
	}
	entityRole := correctionRevisionDAGArtifactEntityRole(revision.Artifact().Kind)
	if revision.Artifact().Kind == domain.ArtifactKindGoldenGroup &&
		revision.Artifact().EntityID == revision.TournamentID() {
		entityRole = "tournament"
	}
	if err := identities.claimRole(revision.Artifact().EntityID, entityRole); err != nil {
		return err
	}
	if previous := revision.PreviousRevisionID(); previous != nil {
		return identities.claimRole(previous.UUID(), "projection revision")
	}
	return nil
}

//nolint:gocyclo // Every role-bearing identity is audited together to reject aliases.
func claimCorrectionOrdinaryDAGIdentities(
	identities *correctionRevisionDAGIdentityRegistry,
	input resultprojection.OfficialResultProjectionInput,
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
	resultUse := correctionRevisionDAGCommandUse{kind: "Series result", resultID: result.ID}
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
	commandUse := correctionRevisionDAGCommandUse{kind: "score"}
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
		if err := identities.claimRole(
			attempt.CurrentGameResultRevisionID.UUID(),
			"official result revision",
		); err != nil {
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
func claimCorrectionNoGameDAGIdentities(
	identities *correctionRevisionDAGIdentityRegistry,
	recorded resultprojection.RecordedNoGameResult,
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
	if err := identities.claimCommand(recorded.CommandID, correctionRevisionDAGCommandUse{kind: "no-show"}); err != nil {
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
		if err := identities.claimBound(
			recorded.Topology[index].SlotID,
			"slot",
			recorded.Scope.SeriesID,
		); err != nil {
			return err
		}
		if err := identities.claimRole(
			recorded.GameProjections[index].Revision().ID().UUID(),
			"projection revision",
		); err != nil {
			return err
		}
	}
	return nil
}

func correctionRevisionDAGArtifactEntityRole(kind domain.ArtifactKind) string {
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
