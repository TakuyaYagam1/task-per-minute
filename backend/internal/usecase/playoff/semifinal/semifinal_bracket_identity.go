package semifinal

import "github.com/google/uuid"

func validateSemifinalBracketIdentityRoles(authority semifinalBracketAuthority) error {
	if err := validateSemifinalFreshIDs(authority); err != nil {
		return err
	}
	roles := make(map[uuid.UUID]string)
	define := func(id uuid.UUID, role string) error {
		if id == uuid.Nil {
			return semifinalBracketError("missing %s identity", role)
		}
		if existing, duplicate := roles[id]; duplicate {
			return semifinalBracketError("identity aliases %s and %s", existing, role)
		}
		roles[id] = role
		return nil
	}
	if err := claimSemifinalBracketBase(define, authority); err != nil {
		return err
	}
	return claimSemifinalBracketPredecessor(define, authority.Previous)
}

func claimSemifinalBracketBase(
	define func(uuid.UUID, string) error,
	authority semifinalBracketAuthority,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: authority.TournamentID, role: "tournament"},
		{id: authority.RevisionID.UUID(), role: "bracket revision"},
		{id: authority.Top4.Projection().Revision().ID().UUID(), role: "Top 4 revision"},
	} {
		if err := define(identity.id, identity.role); err != nil {
			return err
		}
	}
	for _, participant := range authority.Top4.Participants() {
		if err := define(participant.ParticipantID, "participant"); err != nil {
			return err
		}
	}
	if err := claimSemifinalTop4SourceIdentities(define, authority.Top4); err != nil {
		return err
	}
	for _, seriesID := range authority.SeriesIDs {
		if err := define(seriesID, "semifinal Series"); err != nil {
			return err
		}
	}
	return nil
}

func claimSemifinalBracketPredecessor(
	define func(uuid.UUID, string) error,
	previous *semifinalBracketPredecessorReceipt,
) error {
	if previous == nil {
		return nil
	}
	return define(previous.Projection.Revision().ID().UUID(), "previous bracket revision")
}

func validateSemifinalFreshIDs(authority semifinalBracketAuthority) error {
	reserved := make(map[uuid.UUID]struct{})
	if err := addSemifinalTop4Reserved(reserved, authority.Top4); err != nil {
		return err
	}
	if authority.Previous != nil {
		if !mergePlayoffReservedIdentities(reserved, authority.Previous.Reserved) {
			return semifinalBracketError("bracket predecessor identity receipt exceeds DAG bounds")
		}
	}
	candidates := []uuid.UUID{authority.RevisionID.UUID()}
	if authority.Previous == nil {
		candidates = append(candidates, authority.SeriesIDs[0], authority.SeriesIDs[1])
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := reserved[candidate]; exists {
			return semifinalBracketError("new bracket identity aliases retained authority")
		}
		if _, exists := seen[candidate]; exists {
			return semifinalBracketError("new bracket identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func addSemifinalTop4Reserved(reserved map[uuid.UUID]struct{}, snapshot Top4Snapshot) error {
	ids, err := snapshot.ReservedIdentityIDs()
	if err != nil {
		return semifinalBracketError("invalid retained Top 4 identity lineage")
	}
	for _, id := range ids {
		if !reservePlayoffIdentity(reserved, id) {
			return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	return nil
}

func claimSemifinalTop4SourceIdentities(
	claim func(uuid.UUID, string) error,
	snapshot Top4Snapshot,
) error {
	return snapshot.ClaimSourceIdentityRoles(claim)
}
