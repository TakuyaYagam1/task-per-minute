package result

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type plannerUUIDRole string

type revisionSourceOrigin uint8

const ordinaryRevisionSource revisionSourceOrigin = 1

const correctionRevisionSource revisionSourceOrigin = 2

// PersistedCorrectionSourceBinding is repository evidence that an immutable
// correction node represents an official source revision. A validated copy is
// stored privately by the restored head.
type PersistedCorrectionSourceBinding struct {
	TournamentID        uuid.UUID
	RosterID            uuid.UUID
	SeriesID            uuid.UUID
	EntityID            uuid.UUID
	ArtifactKind        domain.ArtifactKind
	CorrectionCommandID uuid.UUID
	HeadCommandID       uuid.UUID
	SourceID            uuid.UUID
	NodeID              uuid.UUID
	PreviousSourceID    uuid.UUID
	PreviousNodeID      uuid.UUID
	NodeRevision        int
}

type persistedCorrectionSourceBinding struct {
	PersistedCorrectionSourceBinding
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func restoreCorrectionSourceBinding(
	binding PersistedCorrectionSourceBinding,
	tournamentID, seriesID, entityID uuid.UUID,
	kind domain.ArtifactKind,
	commandID, sourceID, previousSourceID uuid.UUID,
	source domain.DerivedRevision,
	ordinal int,
) (persistedCorrectionSourceBinding, error) {
	previousNode := source.PreviousRevisionID()
	if binding.TournamentID == uuid.Nil || binding.RosterID == uuid.Nil || binding.SeriesID == uuid.Nil ||
		binding.EntityID == uuid.Nil || binding.CorrectionCommandID == uuid.Nil || binding.HeadCommandID == uuid.Nil || binding.SourceID == uuid.Nil ||
		binding.NodeID == uuid.Nil || binding.PreviousSourceID == uuid.Nil || binding.PreviousNodeID == uuid.Nil ||
		binding.TournamentID != tournamentID || binding.SeriesID != seriesID || binding.EntityID != entityID ||
		binding.ArtifactKind != kind || binding.HeadCommandID != commandID || binding.SourceID != sourceID ||
		binding.PreviousSourceID != previousSourceID || binding.NodeID != source.ID().UUID() ||
		binding.NodeRevision != ordinal || binding.NodeRevision != source.RevisionNo() || previousNode == nil ||
		binding.PreviousNodeID != previousNode.UUID() || binding.NodeID == binding.SourceID ||
		binding.NodeID == binding.PreviousNodeID || binding.SourceID == binding.PreviousSourceID ||
		binding.TournamentID != source.TournamentID() ||
		source.Artifact() != (domain.ArtifactRef{Kind: kind, EntityID: entityID}) {
		return persistedCorrectionSourceBinding{}, domain.ErrValidation
	}
	for _, scopedID := range []uuid.UUID{
		binding.TournamentID, binding.RosterID, binding.SeriesID, binding.EntityID,
		binding.CorrectionCommandID, binding.HeadCommandID,
	} {
		if binding.NodeID == scopedID || binding.PreviousNodeID == scopedID {
			return persistedCorrectionSourceBinding{}, domain.ErrValidation
		}
	}
	return persistedCorrectionSourceBinding{PersistedCorrectionSourceBinding: binding}, nil
}

func matchesCorrectionSourceBinding(
	binding persistedCorrectionSourceBinding,
	tournamentID, seriesID, entityID uuid.UUID,
	kind domain.ArtifactKind,
	commandID, sourceID, previousSourceID uuid.UUID,
	source domain.DerivedRevision,
	ordinal int,
) bool {
	restored, err := restoreCorrectionSourceBinding(
		binding.PersistedCorrectionSourceBinding,
		tournamentID, seriesID, entityID, kind, commandID, sourceID, previousSourceID, source, ordinal,
	)
	return err == nil && restored == binding
}

// RestoreOrdinaryOfficialResultHead restores an immutable ordinary-result node.
// The repository must first prove its result-commit authority. Unlike a command,
// that node has the same identity and lineage as its official source revision.
func RestoreOrdinaryOfficialResultHead(head OfficialResultRevisionHead) (OfficialResultRevisionHead, error) {
	head = head.Clone()
	head.sourceOrigin = ordinaryRevisionSource
	if err := head.Validate(); err != nil {
		return OfficialResultRevisionHead{}, err
	}
	return head, nil
}

// RestoreCorrectionOfficialResultHead restores a correction-backed result only
// after its repository has proved the exact source-to-node binding and scope.
func RestoreCorrectionOfficialResultHead(
	head OfficialResultRevisionHead,
	binding PersistedCorrectionSourceBinding,
) (OfficialResultRevisionHead, error) {
	head = head.Clone()
	if head.PreviousRevisionID == nil {
		return OfficialResultRevisionHead{}, domain.ErrValidation
	}
	entityID := head.Scope.SeriesID
	kind := domain.ArtifactKindSeriesResult
	if head.Scope.Kind == OfficialResultSubjectGame {
		entityID = head.Scope.GameID
		kind = domain.ArtifactKindGameResult
	}
	restored, err := restoreCorrectionSourceBinding(
		binding, head.Scope.TournamentID, head.Scope.SeriesID, entityID, kind,
		head.CommandID, head.ID.UUID(), head.PreviousRevisionID.UUID(), head.SourceProjection, head.Ordinal,
	)
	if err != nil {
		return OfficialResultRevisionHead{}, err
	}
	head.sourceOrigin = correctionRevisionSource
	head.correctionSource = restored
	if err := head.Validate(); err != nil {
		return OfficialResultRevisionHead{}, err
	}
	return head, nil
}

func (h OfficialResultRevisionHead) HasOrdinarySourceIdentity() bool {
	return h.sourceOrigin == ordinaryRevisionSource && h.Validate() == nil
}

// HasCorrectionSourceIdentity reports only the private origin installed by
// RestoreCorrectionOfficialResultHead after exact binding validation.
func (h OfficialResultRevisionHead) HasCorrectionSourceIdentity() bool {
	return h.sourceOrigin == correctionRevisionSource && h.Validate() == nil
}

func matchesOrdinarySource(source domain.DerivedRevision, id uuid.UUID, ordinal int, previous uuid.UUID) bool {
	if source.ID().UUID() != id || source.RevisionNo() != ordinal {
		return false
	}
	if source.PreviousRevisionID() == nil {
		return previous == uuid.Nil
	}
	return previous != uuid.Nil && previous != id && source.PreviousRevisionID().UUID() == previous
}

const (
	plannerRoleTournament  plannerUUIDRole = "tournament"
	plannerRoleSeries      plannerUUIDRole = "series"
	plannerRoleGame        plannerUUIDRole = "game"
	plannerRoleSlot        plannerUUIDRole = "slot"
	plannerRoleParticipant plannerUUIDRole = "participant"
	plannerRoleCommand     plannerUUIDRole = "command"
	plannerRoleOfficial    plannerUUIDRole = "official_result"
	plannerRoleScore       plannerUUIDRole = "series_score"
	plannerRoleSource      plannerUUIDRole = "source_projection"
	plannerRoleActor       plannerUUIDRole = "actor"
)

type plannerUUIDRegistry struct {
	roles   map[uuid.UUID]plannerUUIDRole
	invalid func(string) error
}

func newPlannerUUIDRegistry(invalid func(string) error) *plannerUUIDRegistry {
	return &plannerUUIDRegistry{
		roles:   make(map[uuid.UUID]plannerUUIDRole),
		invalid: invalid,
	}
}

func (r *plannerUUIDRegistry) add(id uuid.UUID, role plannerUUIDRole) error {
	if id == uuid.Nil {
		return nil
	}
	if existing, ok := r.roles[id]; ok && existing != role {
		return r.invalid("cross-role identity alias")
	}
	r.roles[id] = role
	return nil
}

func (r *plannerUUIDRegistry) addSource(source domain.DerivedRevision) error {
	if err := r.add(source.ID().UUID(), plannerRoleSource); err != nil {
		return err
	}
	if previous := source.PreviousRevisionID(); previous != nil {
		return r.add(previous.UUID(), plannerRoleSource)
	}
	return nil
}

func (r *plannerUUIDRegistry) addActor(actor domain.ResultActor) error {
	if actor.PrincipalID == nil {
		return nil
	}
	return r.add(*actor.PrincipalID, plannerRoleActor)
}

func (r *plannerUUIDRegistry) addAttempt(reference SeriesScoreAttemptReference) error {
	for _, value := range []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{reference.SlotID, plannerRoleSlot},
		{reference.GameID, plannerRoleGame},
		{reference.CurrentGameResultRevisionID.UUID(), plannerRoleOfficial},
	} {
		if err := r.add(value.id, value.role); err != nil {
			return err
		}
	}
	if reference.WinnerID != nil {
		return r.add(*reference.WinnerID, plannerRoleParticipant)
	}
	return nil
}

func (r *plannerUUIDRegistry) addSeries(series domain.Series) error {
	if err := r.addSeriesIdentity(series); err != nil {
		return err
	}
	if err := r.addSeriesHeads(series); err != nil {
		return err
	}
	return r.addSeriesSlots(series.Slots)
}

func (r *plannerUUIDRegistry) addSeriesIdentity(series domain.Series) error {
	for _, value := range []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{series.TournamentID, plannerRoleTournament},
		{series.ID, plannerRoleSeries},
		{series.FirstParticipantID, plannerRoleParticipant},
		{series.SecondParticipantID, plannerRoleParticipant},
	} {
		if err := r.add(value.id, value.role); err != nil {
			return err
		}
	}
	if series.WinnerID != nil {
		return r.add(*series.WinnerID, plannerRoleParticipant)
	}
	return nil
}

func (r *plannerUUIDRegistry) addSeriesHeads(series domain.Series) error {
	if series.CurrentScoreRevisionID != nil {
		if err := r.add(series.CurrentScoreRevisionID.UUID(), plannerRoleScore); err != nil {
			return err
		}
	}
	if series.CurrentResultRevisionID != nil {
		if err := r.add(series.CurrentResultRevisionID.UUID(), plannerRoleOfficial); err != nil {
			return err
		}
	}
	return nil
}

func (r *plannerUUIDRegistry) addSeriesSlots(slots []domain.GameSlot) error {
	for _, slot := range slots {
		if err := r.add(slot.ID, plannerRoleSlot); err != nil {
			return err
		}
		for _, game := range slot.Attempts {
			if err := r.addSeriesGame(game); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *plannerUUIDRegistry) addSeriesGame(game domain.Game) error {
	if err := r.add(game.ID, plannerRoleGame); err != nil {
		return err
	}
	if game.WinnerID != nil {
		if err := r.add(*game.WinnerID, plannerRoleParticipant); err != nil {
			return err
		}
	}
	if game.ResultRevisionID != nil {
		return r.add(game.ResultRevisionID.UUID(), plannerRoleOfficial)
	}
	return nil
}

func (r *plannerUUIDRegistry) addOfficialHead(head OfficialResultRevisionHead) error {
	for _, value := range []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{head.ID.UUID(), plannerRoleOfficial},
		{head.CommandID, plannerRoleCommand},
	} {
		if err := r.add(value.id, value.role); err != nil {
			return err
		}
	}
	if head.PreviousRevisionID != nil {
		if err := r.add(head.PreviousRevisionID.UUID(), plannerRoleOfficial); err != nil {
			return err
		}
	}
	if head.Outcome.WinnerID != nil {
		if err := r.add(*head.Outcome.WinnerID, plannerRoleParticipant); err != nil {
			return err
		}
	}
	if head.Outcome.ScoreRevisionID != nil {
		if err := r.add(head.Outcome.ScoreRevisionID.UUID(), plannerRoleScore); err != nil {
			return err
		}
	}
	if !head.HasOrdinarySourceIdentity() && !head.HasCorrectionSourceIdentity() {
		if err := r.addSource(head.SourceProjection); err != nil {
			return err
		}
	}
	return r.addActor(head.Actor)
}

func validateOfficialLocalUUIDRoles(command OfficialResultRevisionCommand) error {
	return validateOfficialUUIDRoles(command, false)
}

func validateOfficialUUIDRoles(command OfficialResultRevisionCommand, ordinarySource bool) error {
	roles := newPlannerUUIDRegistry(invalidOfficialResultRevision)
	values := []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{command.Scope.TournamentID, plannerRoleTournament},
		{command.Scope.SeriesID, plannerRoleSeries},
		{command.Scope.GameID, plannerRoleGame},
		{command.CommandID, plannerRoleCommand},
		{command.RevisionID.UUID(), plannerRoleOfficial},
	}
	if command.ExpectedCurrentRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role plannerUUIDRole
		}{command.ExpectedCurrentRevisionID.UUID(), plannerRoleOfficial})
		if *command.ExpectedCurrentRevisionID == command.RevisionID {
			return invalidOfficialResultRevision("result revision did not advance")
		}
	}
	if command.Outcome.WinnerID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role plannerUUIDRole
		}{*command.Outcome.WinnerID, plannerRoleParticipant})
	}
	if command.Outcome.ScoreRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role plannerUUIDRole
		}{command.Outcome.ScoreRevisionID.UUID(), plannerRoleScore})
	}
	for _, value := range values {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if !ordinarySource {
		if err := roles.addSource(command.ExpectedSourceProjection); err != nil {
			return err
		}
	}
	return roles.addActor(command.Actor)
}

func validateOfficialPlannerUUIDRoles(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) error {
	roles := newPlannerUUIDRegistry(invalidOfficialResultRevision)
	if err := roles.add(command.CommandID, plannerRoleCommand); err != nil {
		return err
	}
	if err := roles.add(command.RevisionID.UUID(), plannerRoleOfficial); err != nil {
		return err
	}
	if err := roles.addSource(command.ExpectedSourceProjection); err != nil {
		return err
	}
	if err := roles.addActor(command.Actor); err != nil {
		return err
	}
	if authority.CurrentHead != nil {
		if err := roles.addOfficialHead(*authority.CurrentHead); err != nil {
			return err
		}
	}
	for _, series := range []domain.Series{authority.PersistedSeries, authority.ProjectedSeries} {
		if err := roles.addSeries(series); err != nil {
			return err
		}
	}
	return nil
}
