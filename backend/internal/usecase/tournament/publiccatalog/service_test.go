package publiccatalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	publiccatalog "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/publiccatalog"
)

type repositoryStub struct {
	listPage   publiccatalog.PublicTournamentListPage
	listErr    error
	getRecord  publiccatalog.PublicTournamentRecord
	getErr     error
	listCalls  int
	getCalls   int
	lastQuery  inbound.PublicTournamentCatalogQuery
	lastPublic string
}

func (s *repositoryStub) ListPublicTournaments(_ context.Context, query inbound.PublicTournamentCatalogQuery) (publiccatalog.PublicTournamentListPage, error) {
	s.listCalls++
	s.lastQuery = query
	return s.listPage, s.listErr
}

func (s *repositoryStub) GetPublicTournamentByPublicID(_ context.Context, publicID string) (publiccatalog.PublicTournamentRecord, error) {
	s.getCalls++
	s.lastPublic = publicID
	return s.getRecord, s.getErr
}

func TestServiceListNormalizesSearchAndBindsCursor(t *testing.T) {
	t.Parallel()

	record := publicTournamentRecord(domain.TournamentStateSwiss, inbound.PublicTournamentCatalogGroupLive, domain.TournamentStateSwiss)
	repository := &repositoryStub{
		listPage: publiccatalog.PublicTournamentListPage{Items: []publiccatalog.PublicTournamentRecord{record}, HasMore: true},
	}
	service := publiccatalog.NewService(repository)

	page, err := service.ListPublicTournaments(t.Context(), inbound.PublicTournamentCatalogQuery{
		Search: "  Alpha  ", Group: inbound.PublicTournamentCatalogFilterAll,
		Sort: inbound.PublicTournamentCatalogSortActivity, Limit: 20,
	})

	require.NoError(t, err)
	require.Equal(t, "alpha", repository.lastQuery.Search)
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.Next)
	require.Equal(t, "alpha", page.Next.Search)
	require.Equal(t, inbound.PublicTournamentCatalogFilterAll, page.Next.Group)
	require.Equal(t, inbound.PublicTournamentCatalogSortActivity, page.Next.Sort)
	require.Equal(t, 0, page.Next.GroupRank)
	require.Equal(t, record.OrderName, page.Next.Name)
	require.Equal(t, record.TournamentID, page.Next.TournamentID)
}

func TestServiceRejectsCursorBoundToDifferentQuery(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{}
	service := publiccatalog.NewService(repository)
	createdAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	_, err := service.ListPublicTournaments(t.Context(), inbound.PublicTournamentCatalogQuery{
		Search: "alpha", Group: inbound.PublicTournamentCatalogFilterAll,
		Sort: inbound.PublicTournamentCatalogSortActivity, Limit: 20,
		After: &inbound.PublicTournamentCatalogCursor{
			Search: "beta", Group: inbound.PublicTournamentCatalogFilterAll,
			Sort: inbound.PublicTournamentCatalogSortActivity, GroupRank: 0,
			CreatedAt: createdAt, TournamentID: uuid.New(),
		},
	})

	require.ErrorIs(t, err, domain.ErrValidation)
	require.Zero(t, repository.listCalls)
}

func TestServiceMapsTechnicalPauseToEffectiveStage(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{
		getRecord: publicTournamentRecord(domain.TournamentStateTechnicalPause, inbound.PublicTournamentCatalogGroupLive, domain.TournamentStateSwiss),
	}
	service := publiccatalog.NewService(repository)

	view, err := service.GetPublicTournamentByPublicID(t.Context(), "alpha")

	require.NoError(t, err)
	require.Equal(t, domain.TournamentStateTechnicalPause, view.State)
	require.Equal(t, domain.TournamentStateSwiss, view.Stage)
	require.Nil(t, view.ScheduledAt)
	require.Equal(t, "alpha", repository.lastPublic)
}

func TestServiceRejectsDraftAndInvalidFilter(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{
		listPage: publiccatalog.PublicTournamentListPage{Items: []publiccatalog.PublicTournamentRecord{
			publicTournamentRecord(domain.TournamentStateDraft, inbound.PublicTournamentCatalogGroupUpcoming, domain.TournamentStateRegistration),
		}},
	}
	service := publiccatalog.NewService(repository)

	_, err := service.ListPublicTournaments(t.Context(), inbound.PublicTournamentCatalogQuery{
		Group: inbound.PublicTournamentCatalogFilterAll, Sort: inbound.PublicTournamentCatalogSortActivity, Limit: 20,
	})
	require.ErrorIs(t, err, domain.ErrInternal)

	_, err = service.ListPublicTournaments(t.Context(), inbound.PublicTournamentCatalogQuery{
		Group: "hidden", Sort: inbound.PublicTournamentCatalogSortActivity, Limit: 20,
	})
	require.ErrorIs(t, err, domain.ErrValidation)
	require.Equal(t, 1, repository.listCalls)
	require.NotErrorIs(t, err, domain.ErrInternal)
}

func publicTournamentRecord(
	state domain.TournamentState,
	group inbound.PublicTournamentCatalogGroup,
	stage domain.TournamentState,
) publiccatalog.PublicTournamentRecord {
	return publiccatalog.PublicTournamentRecord{
		TournamentID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		PublicID:          "alpha",
		Name:              "Alpha",
		OrderName:         "alpha",
		Preset:            domain.TournamentPresetV1,
		State:             state,
		Group:             group,
		Stage:             stage,
		PlannedRosterSize: 8,
		RosterSize:        4,
		CreatedAt:         time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
}
