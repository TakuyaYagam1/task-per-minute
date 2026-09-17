package top4

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

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

//nolint:gocyclo // Identity roles are claimed in one fail-closed traversal.
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
		return top4Error("missing no-game terminal authority")
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
