package playoff

import (
	"bytes"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

type finalSwissIdentityRegistry struct {
	roles map[uuid.UUID]string
}

func validateFinalSwissIdentityRoles(authority finalSwissAuthority) error {
	_, err := finalSwissAuthorityIdentitySet(authority)
	if err != nil || authority.Previous == nil {
		return err
	}
	previous := make(map[uuid.UUID]string, len(authority.Previous.Reserved))
	for _, identity := range authority.Previous.Reserved {
		if identity.ID == uuid.Nil || identity.Role == "" {
			return finalSwissError("invalid predecessor identity lineage")
		}
		if _, duplicate := previous[identity.ID]; duplicate {
			return finalSwissError("duplicate predecessor identity lineage")
		}
		previous[identity.ID] = identity.Role
	}
	candidates := make([]uuid.UUID, 0, len(authority.GoldenGroups)*2+1)
	candidates = append(candidates, authority.RevisionID.UUID())
	for _, group := range authority.GoldenGroups {
		candidates = append(candidates, group.GroupID, group.RevisionID.UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := previous[candidate]; exists {
			return finalSwissError("new revision or group aliases predecessor authority")
		}
		if _, exists := seen[candidate]; exists {
			return finalSwissError("new revision and group identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func finalSwissAuthorityIdentitySet(
	authority finalSwissAuthority,
) (map[uuid.UUID]string, error) {
	registry := finalSwissIdentityRegistry{roles: make(map[uuid.UUID]string)}
	if err := defineFinalSwissBaseIdentities(&registry, authority); err != nil {
		return nil, err
	}
	for _, round := range authority.Rounds {
		if err := defineFinalSwissRoundIdentities(&registry, round); err != nil {
			return nil, err
		}
	}
	for _, group := range authority.GoldenGroups {
		if err := registry.define(group.GroupID, "Golden group"); err != nil {
			return nil, err
		}
		if err := registry.define(group.RevisionID.UUID(), finalSwissRevisionRole); err != nil {
			return nil, err
		}
	}
	return registry.roles, nil
}

func defineFinalSwissBaseIdentities(
	registry *finalSwissIdentityRegistry,
	authority finalSwissAuthority,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: authority.TournamentID, role: "tournament"},
		{id: authority.ProjectionID, role: "standings projection"},
		{id: authority.RevisionID.UUID(), role: finalSwissRevisionRole},
	} {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	if authority.Previous != nil {
		if err := registry.define(authority.Previous.Projection.Revision().ID().UUID(), finalSwissRevisionRole); err != nil {
			return err
		}
	}
	for _, participantID := range authority.ParticipantIDs {
		if err := registry.define(participantID, "participant"); err != nil {
			return err
		}
	}
	return nil
}

func defineFinalSwissRoundIdentities(
	registry *finalSwissIdentityRegistry,
	round finalSwissRound,
) error {
	if err := registry.define(round.RoundID, "round"); err != nil {
		return err
	}
	if err := registry.defineShared(round.RevisionID, "round source projection:"+round.RoundID.String()); err != nil {
		return err
	}
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: round.LockProof.RosterID, role: "round roster authority"},
		{id: round.LockProof.SourceProjectionRevisionID, role: "round source projection:" + round.RoundID.String()},
		{id: round.LockProof.NormalPoolRevisionID, role: "normal pool revision"},
		// Preflight is shared content authority, not a per-round identity.
		{id: round.LockProof.PreflightRevisionID, role: "preflight revision"},
		{id: round.LockProof.WaveID, role: "round Wave:" + round.RoundID.String()},
		{id: round.LockProof.WaveRevisionID.UUID(), role: "round Wave revision:" + round.RoundID.String()},
	} {
		if err := registry.defineShared(identity.id, identity.role); err != nil {
			return err
		}
	}
	for _, series := range round.LockProof.Series {
		if err := registry.defineShared(series.PairingID, "round pairing:"+series.SeriesID.String()); err != nil {
			return err
		}
		for _, identity := range []struct {
			id   uuid.UUID
			role string
		}{
			{id: series.CategoryRevisionID, role: "Series category revision:" + series.SeriesID.String()},
			{id: series.AssignmentID, role: "Series assignment:" + series.SeriesID.String()},
			{id: series.AssignmentPlanID, role: "assignment plan"},
			{id: series.AssignmentPlanRevisionID, role: "assignment plan revision"},
			{id: series.ReservationID, role: "Series reservation:" + series.SeriesID.String()},
		} {
			if err := registry.defineShared(identity.id, identity.role); err != nil {
				return err
			}
		}
	}
	for _, head := range round.Series {
		if err := defineFinalSwissHeadIdentities(registry, round, head); err != nil {
			return err
		}
	}
	if round.Bye != nil {
		return registry.define(round.Bye.RevisionID, "bye revision")
	}
	return nil
}

func defineFinalSwissHeadIdentities(
	registry *finalSwissIdentityRegistry,
	round finalSwissRound,
	head terminalSeriesRecord,
) error {
	if head.OfficialResult.NoGame != nil {
		return defineFinalSwissNoGameIdentities(registry, round, head)
	}
	if head.Series.CurrentScoreRevisionID == nil || head.OfficialResult.Score == nil ||
		head.OfficialResult.ScoreProjection == nil {
		return finalSwissError("terminal Series has no typed score proof")
	}
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Result.SeriesID, role: "Series"},
		{id: head.OfficialResult.Result.ID.UUID(), role: "official result revision"},
		{id: head.Series.CurrentScoreRevisionID.UUID(), role: "score revision"},
	}
	for _, identity := range identities {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	ordinaryResult := head.OfficialResult.Result.HasOrdinarySourceIdentity()
	ordinaryScore := head.OfficialResult.Score.HasOrdinarySourceIdentity()
	correctionResult := head.OfficialResult.Result.HasCorrectionSourceIdentity()
	correctionScore := head.OfficialResult.Score.HasCorrectionSourceIdentity()
	// One ordinary terminal settlement emits both heads. This is a command
	// reference shared within that Series, not two command definitions.
	if ordinaryResult && ordinaryScore && head.OfficialResult.Result.CommandID == head.OfficialResult.Score.CommandID {
		if err := registry.define(head.OfficialResult.Result.CommandID, "result commit command"); err != nil {
			return err
		}
	} else {
		if err := registry.define(head.OfficialResult.Result.CommandID, "official result command"); err != nil {
			return err
		}
		if err := registry.define(head.OfficialResult.Score.CommandID, "score command"); err != nil {
			return err
		}
	}
	// Ordinary source nodes deliberately share their source revision identity.
	// The restored typed head proves the exact current/previous namespace pair;
	// its source revision above remains a unique definition in this registry.
	if !ordinaryResult {
		if err := registry.define(head.Projection.Revision().ID().UUID(), finalSwissRevisionRole); err != nil {
			return err
		}
	}
	if !ordinaryScore {
		if err := registry.define(head.OfficialResult.ScoreProjection.Revision().ID().UUID(), finalSwissRevisionRole); err != nil {
			return err
		}
	}
	if err := defineFinalSwissPredecessorIdentities(
		registry, head, correctionResult, correctionScore,
	); err != nil {
		return err
	}
	if !ordinaryResult && !correctionResult {
		if err := defineFinalSwissProjectionPredecessor(registry, head.Projection); err != nil {
			return err
		}
	}
	if !ordinaryScore && !correctionScore {
		if err := defineFinalSwissProjectionPredecessor(registry, *head.OfficialResult.ScoreProjection); err != nil {
			return err
		}
	}
	return defineFinalSwissAttemptIdentities(registry, head.OfficialResult.Score.Attempts)
}

func defineFinalSwissNoGameIdentities(
	registry *finalSwissIdentityRegistry,
	round finalSwissRound,
	head terminalSeriesRecord,
) error {
	recorded := head.OfficialResult.NoGame
	if recorded == nil {
		return finalSwissError("terminal no-game evidence is missing")
	}
	if len(recorded.Topology) != len(recorded.GameResults) ||
		len(recorded.GameProjections) != len(recorded.GameResults) {
		return finalSwissError("no-game identity evidence is incomplete")
	}
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Series.ID, role: "Series"},
		{id: recorded.CommandID, role: "no-game command"},
		{id: recorded.Score.ID.UUID(), role: "score revision"},
		{id: recorded.Series.ID.UUID(), role: "official result revision"},
	}
	if recorded.HasSQLSourceIdentity() {
		if err := registry.defineShared(recorded.Scope.WindowID, "ready window:"+round.RoundID.String()); err != nil {
			return err
		}
	} else {
		identities = append(identities, struct {
			id   uuid.UUID
			role string
		}{recorded.Scope.WindowID, "ready window"},
			struct {
				id   uuid.UUID
				role string
			}{recorded.ScoreProjection.Revision().ID().UUID(), finalSwissRevisionRole},
			struct {
				id   uuid.UUID
				role string
			}{recorded.ResultProjection.Revision().ID().UUID(), finalSwissRevisionRole})
	}
	if err := registry.defineShared(
		recorded.Scope.WaveID, "round Wave:"+round.RoundID.String(),
	); err != nil {
		return err
	}
	for _, identity := range identities {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	return defineFinalSwissNoGameLineage(registry, *recorded)
}

func defineFinalSwissNoGameLineage(
	registry *finalSwissIdentityRegistry,
	recorded resultprojection.RecordedNoGameResult,
) error {
	if recorded.Score.PreviousRevisionID != nil {
		if err := registry.define(recorded.Score.PreviousRevisionID.UUID(), "score predecessor"); err != nil {
			return err
		}
	}
	if recorded.Series.PreviousRevisionID != nil {
		if err := registry.define(recorded.Series.PreviousRevisionID.UUID(), "official result predecessor"); err != nil {
			return err
		}
	}
	for index := range recorded.GameResults {
		if err := defineFinalSwissNoGameAttemptIdentities(registry, recorded, index); err != nil {
			return err
		}
	}
	if recorded.HasSQLSourceIdentity() {
		return nil
	}
	if err := defineFinalSwissProjectionPredecessor(registry, recorded.ScoreProjection); err != nil {
		return err
	}
	return defineFinalSwissProjectionPredecessor(registry, recorded.ResultProjection)
}

func defineFinalSwissNoGameAttemptIdentities(
	registry *finalSwissIdentityRegistry,
	recorded resultprojection.RecordedNoGameResult,
	index int,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: recorded.Topology[index].SlotID, role: "Game slot"},
		{id: recorded.Topology[index].GameID, role: "Game"},
		{id: recorded.GameResults[index].ID.UUID(), role: "Game result revision"},
	} {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	if !recorded.HasSQLSourceIdentity() {
		if err := registry.define(recorded.GameProjections[index].Revision().ID().UUID(), finalSwissRevisionRole); err != nil {
			return err
		}
	}
	if recorded.GameResults[index].PreviousRevisionID != nil {
		if err := registry.define(
			recorded.GameResults[index].PreviousRevisionID.UUID(), "Game result predecessor",
		); err != nil {
			return err
		}
	}
	return defineFinalSwissProjectionPredecessor(registry, recorded.GameProjections[index])
}

func defineFinalSwissProjectionPredecessor(
	registry *finalSwissIdentityRegistry,
	projection domain.ProjectionRevision,
) error {
	previous := projection.Revision().PreviousRevisionID()
	if previous == nil {
		return nil
	}
	return registry.define(previous.UUID(), "projection predecessor")
}

func defineFinalSwissPredecessorIdentities(
	registry *finalSwissIdentityRegistry,
	head terminalSeriesRecord,
	correctionResult, correctionScore bool,
) error {
	if head.OfficialResult.Result.PreviousRevisionID != nil && !correctionResult {
		if err := registry.define(
			head.OfficialResult.Result.PreviousRevisionID.UUID(), "official result predecessor",
		); err != nil {
			return err
		}
	}
	if head.OfficialResult.Score.PreviousRevisionID != nil && !correctionScore {
		return registry.define(head.OfficialResult.Score.PreviousRevisionID.UUID(), "score predecessor")
	}
	return nil
}

func defineFinalSwissAttemptIdentities(
	registry *finalSwissIdentityRegistry,
	attempts []resultusecase.SeriesScoreAttemptReference,
) error {
	for _, attempt := range attempts {
		for _, identity := range []struct {
			id   uuid.UUID
			role string
		}{
			{id: attempt.SlotID, role: "Game slot"},
			{id: attempt.GameID, role: "Game"},
			{id: attempt.CurrentGameResultRevisionID.UUID(), role: "Game result revision"},
		} {
			if err := registry.define(identity.id, identity.role); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *finalSwissIdentityRegistry) define(id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return finalSwissError("missing %s identity", role)
	}
	if existing, duplicate := r.roles[id]; duplicate {
		return finalSwissError("identity aliases %s and %s", existing, role)
	}
	r.roles[id] = role
	return nil
}

func (r *finalSwissIdentityRegistry) defineShared(id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return finalSwissError("missing %s identity", role)
	}
	if existing, duplicate := r.roles[id]; duplicate {
		if existing == role {
			return nil
		}
		return finalSwissError("identity aliases %s and %s", existing, role)
	}
	r.roles[id] = role
	return nil
}

func canonicalFinalSwissReservedIdentities(
	input map[uuid.UUID]string,
) []finalSwissReservedIdentity {
	if len(input) > maxPlayoffReservedIdentities {
		return nil
	}
	reserved := make([]finalSwissReservedIdentity, 0, len(input))
	for id, role := range input {
		reserved = append(reserved, finalSwissReservedIdentity{ID: id, Role: role})
	}
	sort.Slice(reserved, func(i, j int) bool {
		comparison := bytes.Compare(reserved[i].ID[:], reserved[j].ID[:])
		if comparison != 0 {
			return comparison < 0
		}
		return reserved[i].Role < reserved[j].Role
	})
	return reserved
}
