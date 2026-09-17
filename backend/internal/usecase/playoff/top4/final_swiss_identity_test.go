package top4

import (
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
	swiss "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFinalSwissIdentityDefinitionsRemainUnique(t *testing.T) {
	for _, role := range []string{"official result revision", "score revision", finalSwissRevisionRole} {
		t.Run(role, func(t *testing.T) {
			registry := finalSwissIdentityRegistry{roles: make(map[uuid.UUID]string)}
			id := uuid.New()
			require.NoError(t, registry.define(id, role))
			require.Error(t, registry.define(id, role), "duplicate definitions within one namespace remain invalid")
			require.Error(t, registry.define(id, "participant"), "ordinary source restoration cannot authorize other aliases")
		})
	}
}

func TestFinalSwissCorrectionPredecessorsRetainTheirOriginalRoles(t *testing.T) {
	resultID := domain.OfficialResultRevisionID(uuid.New())
	scoreID := domain.SeriesScoreRevisionID(uuid.New())
	head := terminalSeriesRecord{OfficialResult: resultprojection.OfficialResultProjectionInput{
		Result: resultusecase.OfficialResultRevisionHead{PreviousRevisionID: &resultID},
		Score:  &resultusecase.SeriesScoreRevisionHead{PreviousRevisionID: &scoreID},
	}}
	roles := map[uuid.UUID]string{
		resultID.UUID(): "official result revision",
		scoreID.UUID():  "score revision",
	}

	registry := finalSwissIdentityRegistry{roles: roles}
	require.NoError(t, defineFinalSwissPredecessorIdentities(&registry, head, true, true))
	require.Equal(t, "official result revision", registry.roles[resultID.UUID()])
	require.Equal(t, "score revision", registry.roles[scoreID.UUID()])

	unmarked := finalSwissIdentityRegistry{roles: map[uuid.UUID]string{
		resultID.UUID(): "official result revision",
		scoreID.UUID():  "score revision",
	}}
	require.Error(t, defineFinalSwissPredecessorIdentities(&unmarked, head, false, false))
}

func TestFinalSwissRoundsSharePreflightAuthority(t *testing.T) {
	registry := finalSwissIdentityRegistry{roles: make(map[uuid.UUID]string)}
	rosterID, poolID, preflightID := uuid.New(), uuid.New(), uuid.New()
	for range 2 {
		roundID, projectionID := uuid.New(), uuid.New()
		round := finalSwissRound{RoundID: roundID, RevisionID: projectionID, LockProof: swiss.RoundLockProof{
			RosterID: rosterID, NormalPoolRevisionID: poolID, PreflightRevisionID: preflightID,
			SourceProjectionRevisionID: projectionID, WaveID: uuid.New(), WaveRevisionID: domain.WaveRevisionID(uuid.New()),
		}}
		require.NoError(t, defineFinalSwissRoundIdentities(&registry, round))
	}
	require.Error(t, registry.define(preflightID, "participant"))
	require.Error(t, registry.define(preflightID, "round"))
}
