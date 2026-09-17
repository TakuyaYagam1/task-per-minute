package top4

import "github.com/google/uuid"

// ReservedIdentityIDs exposes the canonical identity receipt needed by the
// adjacent semifinal capability without exposing Top 4 authority internals.
func (s Top4Snapshot) ReservedIdentityIDs() ([]uuid.UUID, error) {
	reserved := make(map[uuid.UUID]struct{})
	if s.state.Authority.Previous != nil &&
		!mergePlayoffReservedIdentities(reserved, s.state.Authority.Previous.Reserved) {
		return nil, top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if !reservePlayoffIdentity(reserved, s.Projection().Revision().ID().UUID()) {
		return nil, top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	for _, projection := range s.QualificationProjections() {
		if !reservePlayoffIdentity(reserved, projection.Revision().ID().UUID()) {
			return nil, top4Error("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	for _, settlement := range s.state.Authority.GoldenSettlements {
		if !reservePlayoffIdentity(reserved, settlement.RevisionID.UUID()) ||
			(settlement.State != nil && !addTop4GoldenStateReserved(reserved, *settlement.State)) {
			return nil, top4Error("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	if err := addTop4FinalSourceReserved(reserved, s.state.Authority.Source); err != nil {
		return nil, err
	}
	ids := canonicalTop4ReservedIDs(reserved)
	if ids == nil {
		return nil, top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	return ids, nil
}

// ClaimSourceIdentityRoles traverses the immutable source and settlement
// authority in its canonical order. The callback owns collision policy.
func (s Top4Snapshot) ClaimSourceIdentityRoles(claim func(uuid.UUID, string) error) error {
	source := s.state.Authority.Source
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: source.state.Authority.ProjectionID, role: "standings projection"},
		{id: source.Projection().Revision().ID().UUID(), role: "standings revision"},
	} {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	if err := claimTop4SwissSourceIdentities(claim, source); err != nil {
		return err
	}
	for _, group := range source.GoldenGroups() {
		if err := claim(group.State.ID, "Golden group"); err != nil {
			return err
		}
		if err := claim(group.State.RevisionID.UUID(), "Golden seed revision"); err != nil {
			return err
		}
	}
	for _, projection := range s.QualificationProjections() {
		if err := claim(projection.Revision().ID().UUID(), "finalized Golden revision"); err != nil {
			return err
		}
	}
	for _, settlement := range s.state.Authority.GoldenSettlements {
		if err := claimTop4SettlementIdentities(claim, settlement); err != nil {
			return err
		}
	}
	return nil
}
