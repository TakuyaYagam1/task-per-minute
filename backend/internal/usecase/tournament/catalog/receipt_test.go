package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestCreateTournamentPayloadDigest(t *testing.T) {
	t.Parallel()

	base := usecase.TournamentCreateCommand{
		Operator:          usecase.OperatorIdentity{ActorID: uuid.MustParse("10000000-0000-0000-0000-000000000001")},
		IdempotencyKey:    uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		ExpectedRevision:  0,
		Preset:            domain.TournamentPresetV1,
		Name:              "September Invitational",
		PublicID:          "september-invitational",
		PlannedRosterSize: 8,
		ContentRevision:   3,
	}

	digest := createTournamentPayloadDigest(base)
	require.Equal(t, digest, createTournamentPayloadDigest(base))

	differentActor := base
	differentActor.Operator.ActorID = uuid.MustParse("30000000-0000-0000-0000-000000000003")
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentActor))

	differentRevision := base
	differentRevision.ExpectedRevision = 1
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentRevision))

	differentPreset := base
	differentPreset.Preset = domain.TournamentPreset("future-preset")
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentPreset))

	differentName := base
	differentName.Name = "October Invitational"
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentName))

	differentPublicID := base
	differentPublicID.PublicID = "october-invitational"
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentPublicID))

	differentRosterPlan := base
	differentRosterPlan.PlannedRosterSize = 16
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentRosterPlan))

	differentContentRevision := base
	differentContentRevision.ContentRevision = 4
	require.NotEqual(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentContentRevision))

	differentKey := base
	differentKey.IdempotencyKey = uuid.MustParse("40000000-0000-0000-0000-000000000004")
	require.Equal(t, createTournamentPayloadDigest(base), createTournamentPayloadDigest(differentKey))
}
