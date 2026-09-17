package playoff

import (
	"bytes"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func validateTop4IdentityRoles(authority top4Authority) error {
	if err := validateTop4FreshRevisionIDs(authority); err != nil {
		return err
	}
	roles := make(map[uuid.UUID]string)
	claim := func(id uuid.UUID, role string) error {
		if id == uuid.Nil {
			return top4Error("missing %s identity", role)
		}
		if existing, ok := roles[id]; ok {
			return top4Error("identity aliases %s and %s", existing, role)
		}
		roles[id] = role
		return nil
	}
	if err := claim(authority.TournamentID, "tournament"); err != nil {
		return err
	}
	if err := claim(authority.RevisionID.UUID(), "Top 4 revision"); err != nil {
		return err
	}
	if err := claim(authority.Source.Projection().Revision().ID().UUID(), "standings revision"); err != nil {
		return err
	}
	if err := claim(authority.Source.state.Authority.ProjectionID, "standings projection"); err != nil {
		return err
	}
	for _, participant := range authority.Source.Standings() {
		if err := claim(participant.ParticipantID, "participant"); err != nil {
			return err
		}
	}
	if err := claimTop4SwissSourceIdentities(claim, authority.Source); err != nil {
		return err
	}
	if err := claimTop4GoldenSourceIdentities(claim, authority); err != nil {
		return err
	}
	if authority.Previous != nil {
		if err := claim(authority.Previous.Projection.Revision().ID().UUID(), "Top 4 revision"); err != nil {
			return err
		}
	}
	return nil
}

func validateTop4FreshRevisionIDs(authority top4Authority) error {
	reserved := make(map[uuid.UUID]struct{})
	for _, settlement := range authority.GoldenSettlements {
		if settlement.State != nil {
			if !addTop4GoldenStateReserved(reserved, *settlement.State) {
				return top4Error("Golden identity authority exceeds DAG bounds")
			}
		}
	}
	if err := addTop4FinalSourceReserved(reserved, authority.Source); err != nil {
		return err
	}
	if authority.Previous != nil {
		if !mergePlayoffReservedIdentities(reserved, authority.Previous.Reserved) {
			return top4Error("Top 4 predecessor identity receipt exceeds DAG bounds")
		}
	}
	return validateTop4FreshCandidates(reserved, authority)
}

func addTop4FinalSourceReserved(
	reserved map[uuid.UUID]struct{},
	source FinalSwissProjection,
) error {
	if source.state.Authority.Previous != nil &&
		len(source.state.Authority.Previous.Reserved) > maxPlayoffReservedIdentities {
		return top4Error("retained standings identity lineage exceeds DAG bounds")
	}
	identities, err := finalSwissAuthorityIdentitySet(source.state.Authority)
	if err != nil {
		return top4Error("invalid retained standings identity lineage")
	}
	for id := range identities {
		if !reservePlayoffIdentity(reserved, id) {
			return top4Error("retained standings identity lineage exceeds DAG bounds")
		}
	}
	if source.state.Authority.Previous != nil {
		for _, identity := range source.state.Authority.Previous.Reserved {
			if !reservePlayoffIdentity(reserved, identity.ID) {
				return top4Error("retained standings identity lineage exceeds DAG bounds")
			}
		}
	}
	return nil
}

func addTop4GoldenStateReserved(reserved map[uuid.UUID]struct{}, state goldenstate.GoldenState) bool {
	roles := goldenstate.CoreIdentityRoles(state)
	roles = append(roles, goldenstate.PlanIdentityRoles(state)...)
	roles = append(roles, goldenstate.WindowIdentityRoles(state)...)
	roles = append(roles, goldenstate.TransitionIdentityRoles(state)...)
	for _, identity := range roles {
		if identity.Value != uuid.Nil {
			if !reservePlayoffIdentity(reserved, identity.Value) {
				return false
			}
		}
	}
	return true
}

func addTop4SnapshotReserved(
	reserved map[uuid.UUID]struct{},
	previous *Top4Snapshot,
) error {
	if previous == nil {
		return nil
	}
	previousAuthority := previous.state.Authority
	if previousAuthority.Previous != nil &&
		!mergePlayoffReservedIdentities(reserved, previousAuthority.Previous.Reserved) {
		return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if !reservePlayoffIdentity(reserved, previous.Projection().Revision().ID().UUID()) {
		return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	for _, projection := range previous.QualificationProjections() {
		if !reservePlayoffIdentity(reserved, projection.Revision().ID().UUID()) {
			return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	for _, settlement := range previousAuthority.GoldenSettlements {
		if !reservePlayoffIdentity(reserved, settlement.RevisionID.UUID()) ||
			(settlement.State != nil && !addTop4GoldenStateReserved(reserved, *settlement.State)) {
			return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	return addTop4FinalSourceReserved(reserved, previousAuthority.Source)
}

func validateTop4FreshCandidates(reserved map[uuid.UUID]struct{}, authority top4Authority) error {
	candidates := make([]uuid.UUID, 0, len(authority.GoldenSettlements)+1)
	candidates = append(candidates, authority.RevisionID.UUID())
	for _, settlement := range authority.GoldenSettlements {
		candidates = append(candidates, settlement.RevisionID.UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := reserved[candidate]; exists {
			return top4Error("new revision aliases retained Golden or predecessor authority")
		}
		if _, exists := seen[candidate]; exists {
			return top4Error("new revisions alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func claimTop4SwissSourceIdentities(
	claim func(uuid.UUID, string) error,
	source FinalSwissProjection,
) error {
	for _, round := range source.state.Authority.Rounds {
		if err := claimSemifinalSwissRoundIdentities(claim, round); err != nil {
			return err
		}
	}
	return nil
}

func claimTop4GoldenSourceIdentities(
	claim func(uuid.UUID, string) error,
	authority top4Authority,
) error {
	groups := authority.Source.GoldenGroups()
	for index, settlement := range authority.GoldenSettlements {
		if err := claim(settlement.RevisionID.UUID(), "finalized Golden revision"); err != nil {
			return err
		}
		if index < len(groups) {
			if err := claim(groups[index].State.ID, "Golden group"); err != nil {
				return err
			}
			if err := claim(groups[index].State.RevisionID.UUID(), "Golden seed revision"); err != nil {
				return err
			}
		}
		if err := claimTop4SettlementIdentities(claim, settlement); err != nil {
			return err
		}
	}
	return nil
}

func claimTop4SettlementIdentities(
	claim func(uuid.UUID, string) error,
	settlement Top4GoldenSettlement,
) error {
	if settlement.Positions != nil {
		ledger := settlement.Positions
		for _, revisionID := range ledger.state.revisionIDs {
			if err := claim(revisionID, "Golden position ledger revision"); err != nil {
				return err
			}
		}
		for _, attempt := range ledger.state.attempts {
			for _, identity := range []struct {
				id   uuid.UUID
				role string
			}{
				{id: attempt.AttemptID, role: "Golden attempt"},
				{id: attempt.SubmissionRevisionID, role: "Golden submission revision"},
				{id: attempt.WaveID, role: "Golden wave"},
				{id: attempt.AssignmentID, role: "Golden assignment"},
				{id: attempt.SnapshotID, role: "Golden snapshot"},
				{id: attempt.TaskID, role: "Golden task"},
			} {
				if err := claim(identity.id, identity.role); err != nil {
					return err
				}
			}
		}
		commitIDs := make(map[uuid.UUID]struct{})
		for _, position := range ledger.state.positions {
			if _, seen := commitIDs[position.CommitID]; seen {
				continue
			}
			commitIDs[position.CommitID] = struct{}{}
			if err := claim(position.CommitID, "Golden position commit"); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneTop4Authority(input top4Authority) top4Authority {
	clone := input
	if input.Previous != nil {
		clone.Previous = cloneTop4PredecessorReceipt(input.Previous)
	}
	clone.Source = input.Source.Snapshot()
	clone.CurrentTerminalSeries = cloneFinalSwissHeads(input.CurrentTerminalSeries)
	clone.GoldenSettlements = cloneTop4Settlements(input.GoldenSettlements)
	return clone
}

func canonicalTop4ReservedIDs(input map[uuid.UUID]struct{}) []uuid.UUID {
	if len(input) > maxPlayoffReservedIdentities {
		return nil
	}
	reserved := make([]uuid.UUID, 0, len(input))
	for id := range input {
		reserved = append(reserved, id)
	}
	sort.Slice(reserved, func(i, j int) bool {
		return bytes.Compare(reserved[i][:], reserved[j][:]) < 0
	})
	return reserved
}

func cloneTop4PredecessorReceipt(input *top4PredecessorReceipt) *top4PredecessorReceipt {
	if input == nil {
		return nil
	}
	if len(input.Reserved) > maxPlayoffReservedIdentities {
		return &top4PredecessorReceipt{Projection: cloneFinalSwissDomainProjection(input.Projection)}
	}
	return &top4PredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(input.Projection),
		Reserved:   append([]uuid.UUID(nil), input.Reserved...),
	}
}

func reservePlayoffIdentity(reserved map[uuid.UUID]struct{}, id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	if _, exists := reserved[id]; exists {
		return true
	}
	if len(reserved) >= maxPlayoffReservedIdentities {
		return false
	}
	reserved[id] = struct{}{}
	return true
}

func mergePlayoffReservedIdentities(reserved map[uuid.UUID]struct{}, retained []uuid.UUID) bool {
	if len(reserved) > maxPlayoffReservedIdentities || len(retained) > maxPlayoffReservedIdentities {
		return false
	}
	for _, id := range retained {
		if !reservePlayoffIdentity(reserved, id) {
			return false
		}
	}
	return true
}

func cloneTop4State(input top4SnapshotState) top4SnapshotState {
	clone := input
	clone.Authority = cloneTop4Authority(input.Authority)
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.Participants = append([]Top4Participant(nil), input.Participants...)
	clone.Dependencies = append([]domain.RevisionDependency(nil), input.Dependencies...)
	clone.QualificationDependencies = append([]domain.RevisionDependency(nil), input.QualificationDependencies...)
	clone.QualificationProjections = cloneTop4Projections(input.QualificationProjections)
	clone.TerminalReferences = append([]Top4TerminalSeriesReference(nil), input.TerminalReferences...)
	return clone
}

func cloneTop4Settlements(input []Top4GoldenSettlement) []Top4GoldenSettlement {
	clone := append([]Top4GoldenSettlement(nil), input...)
	for index := range clone {
		if input[index].Positions != nil {
			value := input[index].Positions.Snapshot()
			clone[index].Positions = &value
		}
		if input[index].State != nil {
			value := input[index].State.Snapshot()
			clone[index].State = &value
		}
	}
	return clone
}

func cloneFinalSwissHeads(input []terminalSeriesRecord) []terminalSeriesRecord {
	clone := make([]terminalSeriesRecord, len(input))
	for index, head := range input {
		clone[index] = cloneFinalSwissSeriesHead(head)
	}
	return clone
}

func cloneTop4Projections(input []domain.ProjectionRevision) []domain.ProjectionRevision {
	clone := make([]domain.ProjectionRevision, len(input))
	for index, projection := range input {
		clone[index] = cloneFinalSwissDomainProjection(projection)
	}
	return clone
}

func sortFinalSwissHeads(heads []terminalSeriesRecord) {
	sort.Slice(heads, func(i, j int) bool {
		return bytes.Compare(heads[i].Result.SeriesID[:], heads[j].Result.SeriesID[:]) < 0
	})
}
