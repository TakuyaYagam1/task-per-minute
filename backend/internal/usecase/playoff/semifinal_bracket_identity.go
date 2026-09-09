package playoff

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

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
	if snapshot.state.Authority.Previous != nil &&
		!mergePlayoffReservedIdentities(reserved, snapshot.state.Authority.Previous.Reserved) {
		return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if !reservePlayoffIdentity(reserved, snapshot.Projection().Revision().ID().UUID()) {
		return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if err := addTop4FinalSourceReserved(reserved, snapshot.state.Authority.Source); err != nil {
		return semifinalBracketError("invalid retained standings identity lineage")
	}
	for _, projection := range snapshot.QualificationProjections() {
		if !reservePlayoffIdentity(reserved, projection.Revision().ID().UUID()) {
			return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	for _, settlement := range snapshot.state.Authority.GoldenSettlements {
		if !reservePlayoffIdentity(reserved, settlement.RevisionID.UUID()) {
			return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
		}
		if settlement.State != nil {
			if !addSemifinalGoldenStateReserved(reserved, *settlement.State) {
				return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
			}
		}
	}
	return nil
}

func addSemifinalGoldenStateReserved(reserved map[uuid.UUID]struct{}, state goldenusecase.GoldenState) bool {
	roles := goldenusecase.CoreIdentityRoles(state)
	roles = append(roles, goldenusecase.PlanIdentityRoles(state)...)
	roles = append(roles, goldenusecase.WindowIdentityRoles(state)...)
	roles = append(roles, goldenusecase.TransitionIdentityRoles(state)...)
	for _, identity := range roles {
		if identity.Value != uuid.Nil {
			if !reservePlayoffIdentity(reserved, identity.Value) {
				return false
			}
		}
	}
	return true
}

func claimSemifinalTop4SourceIdentities(
	claim func(uuid.UUID, string) error,
	snapshot Top4Snapshot,
) error {
	source := snapshot.state.Authority.Source
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
	for _, round := range source.state.Authority.Rounds {
		if err := claimSemifinalSwissRoundIdentities(claim, round); err != nil {
			return err
		}
	}
	for _, group := range source.GoldenGroups() {
		if err := claim(group.State.ID, "Golden group"); err != nil {
			return err
		}
		if err := claim(group.State.RevisionID.UUID(), "Golden seed revision"); err != nil {
			return err
		}
	}
	for _, projection := range snapshot.QualificationProjections() {
		if err := claim(projection.Revision().ID().UUID(), "finalized Golden revision"); err != nil {
			return err
		}
	}
	for _, settlement := range snapshot.state.Authority.GoldenSettlements {
		if err := claimTop4SettlementIdentities(claim, settlement); err != nil {
			return err
		}
	}
	return nil
}

func claimSemifinalSwissRoundIdentities(
	claim func(uuid.UUID, string) error,
	round finalSwissRound,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: round.RoundID, role: "round"},
		{id: round.RevisionID, role: "round revision"},
	} {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	shared := make(map[uuid.UUID]string)
	for _, head := range round.Series {
		claimHead := claim
		if recorded := head.OfficialResult.NoGame; recorded != nil && recorded.HasSQLSourceIdentity() {
			claimHead = func(id uuid.UUID, role string) error {
				if role == "Swiss no-game Wave" || role == "Swiss no-game window" {
					if previous, found := shared[id]; found && previous == role {
						return nil
					}
					shared[id] = role
				}
				return claim(id, role)
			}
		}
		if err := claimSemifinalSwissHeadIdentities(claimHead, head); err != nil {
			return err
		}
	}
	if round.Bye != nil {
		return claim(round.Bye.RevisionID, "bye revision")
	}
	return nil
}

func claimSemifinalSwissHeadIdentities(
	claim func(uuid.UUID, string) error,
	head terminalSeriesRecord,
) error {
	if head.OfficialResult.NoGame != nil {
		return claimSemifinalNoGameHeadIdentities(claim, head)
	}
	return claimSemifinalOrdinaryHeadIdentities(claim, head)
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func claimSemifinalOrdinaryHeadIdentities(
	claim func(uuid.UUID, string) error,
	head terminalSeriesRecord,
) error {
	type identity struct {
		id   uuid.UUID
		role string
	}
	ordinaryResult := head.OfficialResult.Result.HasOrdinarySourceIdentity() &&
		head.Projection.Revision().ID().UUID() == head.OfficialResult.Result.ID.UUID()
	ordinaryScore := head.OfficialResult.Score != nil && head.OfficialResult.ScoreProjection != nil &&
		head.OfficialResult.Score.HasOrdinarySourceIdentity() && head.OfficialResult.ScoreProjection.Revision().ID().UUID() == head.OfficialResult.Score.ID.UUID()
	identities := []identity{
		{id: head.Series.ID, role: "Swiss Series"},
		{id: head.OfficialResult.Result.ID.UUID(), role: "official result revision"},
		{id: head.OfficialResult.Result.CommandID, role: "official result command"},
	}
	if !ordinaryResult {
		identities = append(identities, identity{head.Projection.Revision().ID().UUID(), "Series result projection"})
	}
	if head.OfficialResult.Score != nil && head.OfficialResult.ScoreProjection != nil {
		identities = append(identities, identity{head.OfficialResult.Score.ID.UUID(), "score revision"})
		if !ordinaryResult || !ordinaryScore || head.OfficialResult.Score.CommandID != head.OfficialResult.Result.CommandID {
			identities = append(identities, identity{head.OfficialResult.Score.CommandID, "score command"})
		}
		if !ordinaryScore {
			identities = append(identities, identity{head.OfficialResult.ScoreProjection.Revision().ID().UUID(), "score projection"})
		}
	}
	for _, identity := range identities {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	if previous := head.OfficialResult.Result.PreviousRevisionID; previous != nil {
		if err := claim(previous.UUID(), "official result predecessor"); err != nil {
			return err
		}
	}
	if err := claimSemifinalOrdinaryScoreIdentities(claim, head); err != nil {
		return err
	}
	if !ordinaryResult {
		if previous := head.Projection.Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	if !ordinaryScore && head.OfficialResult.ScoreProjection != nil {
		if previous := head.OfficialResult.ScoreProjection.Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	return nil
}

func claimSemifinalOrdinaryScoreIdentities(
	claim func(uuid.UUID, string) error,
	head terminalSeriesRecord,
) error {
	if head.OfficialResult.Score != nil {
		if previous := head.OfficialResult.Score.PreviousRevisionID; previous != nil {
			if err := claim(previous.UUID(), "score predecessor"); err != nil {
				return err
			}
		}
		for _, attempt := range head.OfficialResult.Score.Attempts {
			for _, identity := range []struct {
				id   uuid.UUID
				role string
			}{
				{id: attempt.SlotID, role: "Swiss Game slot"},
				{id: attempt.GameID, role: "Swiss Game"},
				{id: attempt.CurrentGameResultRevisionID.UUID(), role: "Game result revision"},
			} {
				if err := claim(identity.id, identity.role); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func claimSemifinalNoGameHeadIdentities(
	claim func(uuid.UUID, string) error,
	head terminalSeriesRecord,
) error {
	recorded := head.OfficialResult.NoGame
	if recorded == nil {
		return semifinalBracketError("missing no-game terminal authority")
	}
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Series.ID, role: "Swiss Series"},
		{id: recorded.Scope.WaveID, role: "Swiss no-game Wave"},
		{id: recorded.Scope.WindowID, role: "Swiss no-game window"},
		{id: recorded.CommandID, role: "Swiss no-game command"},
		{id: recorded.Score.ID.UUID(), role: "score revision"},
		{id: recorded.Series.ID.UUID(), role: "official result revision"},
	}
	if !recorded.HasSQLSourceIdentity() {
		identities = append(identities, struct {
			id   uuid.UUID
			role string
		}{recorded.ScoreProjection.Revision().ID().UUID(), "score projection"},
			struct {
				id   uuid.UUID
				role string
			}{recorded.ResultProjection.Revision().ID().UUID(), "Series result projection"})
	}
	for _, identity := range identities {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	if err := claimSemifinalNoGameAttemptIdentities(claim, *recorded); err != nil {
		return err
	}
	return claimSemifinalNoGamePredecessors(claim, *recorded)
}

func claimSemifinalNoGameAttemptIdentities(
	claim func(uuid.UUID, string) error,
	recorded resultprojection.RecordedNoGameResult,
) error {
	for index := range recorded.Topology {
		for _, identity := range []struct {
			id   uuid.UUID
			role string
		}{
			{id: recorded.Topology[index].SlotID, role: "Swiss Game slot"},
			{id: recorded.Topology[index].GameID, role: "Swiss Game"},
			{id: recorded.GameResults[index].ID.UUID(), role: "Game result revision"},
		} {
			if err := claim(identity.id, identity.role); err != nil {
				return err
			}
		}
		if !recorded.HasSQLSourceIdentity() {
			if err := claim(recorded.GameProjections[index].Revision().ID().UUID(), "Game result projection"); err != nil {
				return err
			}
		}
		if previous := recorded.GameResults[index].PreviousRevisionID; previous != nil {
			if err := claim(previous.UUID(), "Game result predecessor"); err != nil {
				return err
			}
		}
		if previous := recorded.GameProjections[index].Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	return nil
}

func claimSemifinalNoGamePredecessors(
	claim func(uuid.UUID, string) error,
	recorded resultprojection.RecordedNoGameResult,
) error {
	for _, previous := range []uuid.UUID{
		finalSwissScorePredecessor(recorded.Score), finalSwissResultPredecessor(recorded.Series),
	} {
		if previous != uuid.Nil {
			if err := claim(previous, "terminal predecessor"); err != nil {
				return err
			}
		}
	}
	if recorded.HasSQLSourceIdentity() {
		return nil
	}
	for _, projection := range []domain.ProjectionRevision{
		recorded.ScoreProjection, recorded.ResultProjection,
	} {
		if previous := projection.Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	return nil
}

func finalSwissScorePredecessor(score domain.NormalNoShowScoreRevision) uuid.UUID {
	if score.PreviousRevisionID == nil {
		return uuid.Nil
	}
	return score.PreviousRevisionID.UUID()
}

func finalSwissResultPredecessor(result domain.NormalNoShowSeriesRevision) uuid.UUID {
	if result.PreviousRevisionID == nil {
		return uuid.Nil
	}
	return result.PreviousRevisionID.UUID()
}
