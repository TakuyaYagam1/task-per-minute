package resultprojection

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type revisionDAGIdentityRegistry struct {
	roles       map[uuid.UUID]string
	officialIDs map[domain.OfficialResultRevisionID]struct{}
	scoreIDs    map[domain.SeriesScoreRevisionID]struct{}
	boundOwners map[string]uuid.UUID
	commandUses map[uuid.UUID][]revisionDAGCommandUse
}

type revisionDAGCommandUse struct {
	kind            string
	resultID        domain.OfficialResultRevisionID
	commandResultID domain.OfficialResultRevisionID
}

func newRevisionDAGIdentityRegistry() *revisionDAGIdentityRegistry {
	return &revisionDAGIdentityRegistry{
		roles:       make(map[uuid.UUID]string),
		officialIDs: make(map[domain.OfficialResultRevisionID]struct{}),
		scoreIDs:    make(map[domain.SeriesScoreRevisionID]struct{}),
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
	id domain.OfficialResultRevisionID,
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

func (r *revisionDAGIdentityRegistry) claimScore(id domain.SeriesScoreRevisionID) error {
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
	revision domain.DerivedRevision,
) error {
	if err := identities.claimRole(revision.ID().UUID(), "projection revision"); err != nil {
		return err
	}
	if err := identities.claimRole(revision.TournamentID(), "tournament"); err != nil {
		return err
	}
	entityRole := revisionDAGArtifactEntityRole(revision.Artifact().Kind)
	if revision.Artifact().Kind == domain.ArtifactKindGoldenGroup &&
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
