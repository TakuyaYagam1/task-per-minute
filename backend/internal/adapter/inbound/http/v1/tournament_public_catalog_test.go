package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type publicTournamentCatalogStub struct {
	list      func(context.Context, inbound.PublicTournamentCatalogQuery) (inbound.PublicTournamentCatalogPage, error)
	get       func(context.Context, string) (inbound.PublicTournamentCatalogView, error)
	listCalls int
	getCalls  int
}

func (s *publicTournamentCatalogStub) ListPublicTournaments(ctx context.Context, query inbound.PublicTournamentCatalogQuery) (inbound.PublicTournamentCatalogPage, error) {
	s.listCalls++
	if s.list == nil {
		return inbound.PublicTournamentCatalogPage{}, nil
	}
	return s.list(ctx, query)
}

func (s *publicTournamentCatalogStub) GetPublicTournamentByPublicID(ctx context.Context, publicID string) (inbound.PublicTournamentCatalogView, error) {
	s.getCalls++
	if s.get == nil {
		return inbound.PublicTournamentCatalogView{}, nil
	}
	return s.get(ctx, publicID)
}

func TestListPublicTournamentsMapsRedactedPage(t *testing.T) {
	t.Parallel()

	view := publicTournamentCatalogViewFixture()
	createdAt := view.CreatedAt
	stub := &publicTournamentCatalogStub{
		list: func(_ context.Context, query inbound.PublicTournamentCatalogQuery) (inbound.PublicTournamentCatalogPage, error) {
			require.Equal(t, "alpha", query.Search)
			require.Equal(t, inbound.PublicTournamentCatalogFilterLive, query.Group)
			require.Equal(t, inbound.PublicTournamentCatalogSortName, query.Sort)
			require.Equal(t, 10, query.Limit)
			return inbound.PublicTournamentCatalogPage{
				Items: []inbound.PublicTournamentCatalogView{view},
				Next: &inbound.PublicTournamentCatalogCursor{
					Search: "alpha", Group: inbound.PublicTournamentCatalogFilterLive,
					Sort: inbound.PublicTournamentCatalogSortName, GroupRank: 0,
					Name: "alpha", CreatedAt: createdAt, TournamentID: view.TournamentID,
				},
			}, nil
		},
	}
	server := New(Dependencies{PublicTournamentCatalog: stub})
	q := " Alpha "
	group := api.TournamentCatalogFilterGroupLive
	sort := api.TournamentCatalogSortName
	limit := int32(10)
	recorder := httptest.NewRecorder()
	server.ListPublicTournaments(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/public/tournaments", nil),
		api.ListPublicTournamentsParams{Q: &q, Group: &group, Sort: &sort, Limit: &limit},
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload api.PublicTournamentCatalogResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Items, 1)
	require.Equal(t, view.TournamentID, payload.Items[0].TournamentId)
	require.Equal(t, view.PublicID, payload.Items[0].PublicId)
	require.NotNil(t, payload.NextCursor)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &fields))
	require.NotContains(t, string(fields["items"]), "revision")
	require.NotContains(t, string(fields["items"]), "flag")
	require.Equal(t, 1, stub.listCalls)
}

func TestListPublicTournamentsRejectsMalformedCursorBeforeUseCase(t *testing.T) {
	t.Parallel()

	stub := &publicTournamentCatalogStub{}
	server := New(Dependencies{PublicTournamentCatalog: stub})
	cursor := "not-a-cursor"
	recorder := httptest.NewRecorder()
	server.ListPublicTournaments(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/public/tournaments", nil),
		api.ListPublicTournamentsParams{Cursor: &cursor},
	)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, stub.listCalls)
}

func TestPublicTournamentCatalogCursorAcceptsBoundedUnicodePayload(t *testing.T) {
	t.Parallel()

	cursor := &inbound.PublicTournamentCatalogCursor{
		Search:       strings.Repeat("😀", 80),
		Group:        inbound.PublicTournamentCatalogFilterAll,
		Sort:         inbound.PublicTournamentCatalogSortName,
		GroupRank:    0,
		Name:         strings.Repeat("😀", 120),
		CreatedAt:    time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
		TournamentID: uuid.New(),
	}
	encoded, err := encodePublicTournamentCatalogCursor(cursor)
	require.NoError(t, err)
	require.Greater(t, len(encoded), 1024)
	require.LessOrEqual(t, len(encoded), 2048)

	decoded, err := decodePublicTournamentCatalogCursor(encoded)
	require.NoError(t, err)
	require.Equal(t, cursor, decoded)
}

func TestGetPublicTournamentByPublicIDMapsNotFound(t *testing.T) {
	t.Parallel()

	stub := &publicTournamentCatalogStub{
		get: func(context.Context, string) (inbound.PublicTournamentCatalogView, error) {
			return inbound.PublicTournamentCatalogView{}, domain.ErrTournamentNotFound
		},
	}
	server := New(Dependencies{PublicTournamentCatalog: stub})
	recorder := httptest.NewRecorder()
	server.GetPublicTournamentByPublicID(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/public/tournaments/alpha", nil),
		"alpha",
	)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, 1, stub.getCalls)
}

func publicTournamentCatalogViewFixture() inbound.PublicTournamentCatalogView {
	return inbound.PublicTournamentCatalogView{
		TournamentID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		PublicID:          "alpha",
		Name:              "Alpha",
		Preset:            domain.TournamentPresetV1,
		State:             domain.TournamentStateSwiss,
		Group:             inbound.PublicTournamentCatalogGroupLive,
		Stage:             domain.TournamentStateSwiss,
		PlannedRosterSize: 8,
		RosterSize:        4,
		CreatedAt:         time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}
}
