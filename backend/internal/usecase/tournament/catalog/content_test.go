package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

type tournamentContentReaderStub struct {
	record catalogusecase.TournamentContentRecord
	err    error
	calls  int
}

func (s *tournamentContentReaderStub) GetTournamentContent(context.Context) (catalogusecase.TournamentContentRecord, error) {
	s.calls++
	return s.record, s.err
}

func TestUseCase_GetTournamentContentRequiresOperatorAndReader(t *testing.T) {
	t.Parallel()

	reader := &tournamentContentReaderStub{record: tournamentContentRecordFixture()}
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{ContentReader: reader})

	_, err := application.GetTournamentContent(t.Context(), inbound.OperatorIdentity{})

	require.ErrorIs(t, err, domain.ErrValidation)
	require.Zero(t, reader.calls)

	//nolint:staticcheck // The boundary must reject a nil context without invoking the reader.
	_, err = application.GetTournamentContent(nil, inbound.OperatorIdentity{ActorID: uuid.New()})
	require.ErrorIs(t, err, domain.ErrValidation)
	require.Zero(t, reader.calls)

	_, err = catalogusecase.NewUseCase(catalogusecase.Dependencies{}).
		GetTournamentContent(t.Context(), inbound.OperatorIdentity{ActorID: uuid.New()})
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestUseCase_GetTournamentContentMapsPublishedSelection(t *testing.T) {
	t.Parallel()

	record := tournamentContentRecordFixture()
	reader := &tournamentContentReaderStub{record: record}
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{ContentReader: reader})

	view, err := application.GetTournamentContent(
		t.Context(), inbound.OperatorIdentity{ActorID: uuid.New()},
	)

	require.NoError(t, err)
	require.Equal(t, inbound.TournamentContentView{
		ContentRevision:      record.ContentRevision,
		PublicationID:        record.PublicationID,
		PublishedAt:          record.PublishedAt,
		NormalPoolRevisionID: record.NormalPoolRevisionID,
		GoldenPoolRevisionID: record.GoldenPoolRevisionID,
	}, view)
	require.Equal(t, 1, reader.calls)
}

func TestUseCase_GetTournamentContentPreservesInvalidPublicationError(t *testing.T) {
	t.Parallel()

	reader := &tournamentContentReaderStub{}
	// The reader's error must carry the stable domain identity for the inbound
	// adapter to map it to the content-configuration response.
	reader.err = errors.Join(errors.New("publication is unusable"), domain.ErrInvalidContentConfiguration)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{ContentReader: reader})

	_, err := application.GetTournamentContent(
		t.Context(), inbound.OperatorIdentity{ActorID: uuid.New()},
	)

	require.ErrorIs(t, err, domain.ErrInvalidContentConfiguration)
}

func TestUseCase_GetTournamentContentRejectsMalformedReaderRecord(t *testing.T) {
	t.Parallel()

	reader := &tournamentContentReaderStub{record: catalogusecase.TournamentContentRecord{
		ContentRevision: 2,
		PublishedAt:     time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
	}}
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{ContentReader: reader})

	_, err := application.GetTournamentContent(
		t.Context(), inbound.OperatorIdentity{ActorID: uuid.New()},
	)

	require.ErrorIs(t, err, domain.ErrInvalidContentConfiguration)
}

func tournamentContentRecordFixture() catalogusecase.TournamentContentRecord {
	return catalogusecase.TournamentContentRecord{
		ContentRevision:      7,
		PublicationID:        uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		PublishedAt:          time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		NormalPoolRevisionID: uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		GoldenPoolRevisionID: uuid.MustParse("30000000-0000-0000-0000-000000000003"),
	}
}
