package v1

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestTournamentConfigurationStageDefaultInputMapping(t *testing.T) {
	t.Parallel()

	value, err := tournamentConfigurationStageDefaultInput(api.TournamentConfigurationStageDefaultInput{
		Mode:       api.CategoryModeAdmin,
		Categories: []api.Category{api.CategoryWeb, api.CategoryCrypto, api.CategoryPwn},
	})
	require.NoError(t, err)
	require.Equal(t, domain.CategoryModeAdmin, value.Mode)
	require.Equal(t, []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn}, value.Categories)

	_, err = tournamentConfigurationStageDefaultInput(api.TournamentConfigurationStageDefaultInput{
		Mode:       api.CategoryModeAdmin,
		Categories: []api.Category{api.CategoryWeb, api.CategoryWeb},
	})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestTournamentConfigurationUnlockIntentMappingRejectsNonCanonicalDigest(t *testing.T) {
	t.Parallel()

	intent := api.ConfigurationUnlockIntent{
		ReservationId:    uuid.New(),
		OwnerId:          uuid.New(),
		SourceRevisionId: uuid.New(),
		ExpectedRevision: 4,
		EvidenceDigest:   strings.Repeat("ab", 32),
		BindingDigest:    strings.Repeat("cd", 32),
	}
	values, err := tournamentConfigurationUnlockIntents([]api.ConfigurationUnlockIntent{intent})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, int64(4), values[0].ExpectedRevision)

	intent.EvidenceDigest = strings.ToUpper(intent.EvidenceDigest)
	_, err = tournamentConfigurationUnlockIntents([]api.ConfigurationUnlockIntent{intent})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestTournamentConfigurationResponseEnforcesImmutableFinalDraft(t *testing.T) {
	t.Parallel()

	view := validTournamentConfigurationView()
	view.FinalDefault.Mode = domain.CategoryModeAdmin
	_, err := tournamentConfigurationResponse(view)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentConfigurationResponseMapsRevisionAndSeriesEvidence(t *testing.T) {
	t.Parallel()

	view := validTournamentConfigurationView()
	seriesID := uuid.New()
	view.Series = []inbound.AdminConfigurationSeriesView{{
		ID:                     seriesID,
		Stage:                  domain.TournamentStageSwiss,
		RoundNumber:            2,
		Revision:               8,
		Mode:                   domain.CategoryModeAdmin,
		Categories:             []domain.Category{domain.CategoryWeb},
		CategoryPoolRevisionID: view.CategoryPools[0].ID,
		CategoryPoolRevision:   view.CategoryPools[0].Revision,
		Locked:                 false,
		Started:                false,
		Consumed:               false,
		Disclosed:              false,
	}}
	payload, err := tournamentConfigurationResponse(view)
	require.NoError(t, err)
	require.Equal(t, view.ConfigurationRevision, payload.ConfigurationRevision)
	require.Len(t, payload.CategoryPools, 2)
	require.Len(t, payload.Series, 1)
	require.Equal(t, seriesID, payload.Series[0].Id)
	require.Equal(t, int64(8), payload.Series[0].Revision)
}

func validTournamentConfigurationView() inbound.AdminTournamentConfigurationView {
	return inbound.AdminTournamentConfigurationView{
		TournamentID:          uuid.New(),
		ProjectionRevisionID:  uuid.New(),
		ProjectionRevision:    12,
		ConfigurationRevision: 5,
		CategoryPools: []inbound.AdminConfigurationCategoryPoolView{
			{ID: uuid.New(), Revision: 2, Format: domain.SeriesFormatBO1, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn}},
			{ID: uuid.New(), Revision: 3, Format: domain.SeriesFormatBO3, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn, domain.CategoryReverse, domain.CategoryOSINT}},
		},
		SwissDefault:     inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}},
		GoldenDefault:    inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryCrypto}},
		SemifinalDefault: inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto}},
		FinalDefault:     inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeDraft, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn, domain.CategoryReverse, domain.CategoryOSINT}},
		UpdatedAt:        time.Date(2026, time.September, 13, 1, 0, 0, 0, time.UTC),
	}
}
