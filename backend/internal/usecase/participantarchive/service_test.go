package participantarchive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/participantarchive"
)

type archiveRepositoryStub struct {
	source participantarchive.Source
	err    error
	calls  int
}

func (stub *archiveRepositoryStub) FindSource(
	context.Context,
	inbound.ParticipantArchiveQuery,
) (participantarchive.Source, error) {
	stub.calls++
	return stub.source, stub.err
}

type archiveSignerStub struct {
	url   string
	err   error
	calls int
	ttl   time.Duration
}

func (stub *archiveSignerStub) PresignCanonicalSourceFileURL(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	ttl time.Duration,
) (string, error) {
	stub.calls++
	stub.ttl = ttl
	return stub.url, stub.err
}

type archiveClock time.Time

func (clock archiveClock) Now() time.Time { return time.Time(clock) }

func TestServiceGetSourceFileRenewsAuthorizedImmutableSource(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 12, 30, 0, 0, time.UTC)
	canonicalURL := "http://seaweed:8333/tasks/tasks/source.zip"
	repository := &archiveRepositoryStub{source: participantarchive.Source{TaskID: uuid.New(), CanonicalURL: &canonicalURL}}
	signer := &archiveSignerStub{url: "https://files.example.test/archive.zip?signature=fresh"}
	service, err := participantarchive.New(repository, signer, archiveClock(now))
	require.NoError(t, err)
	query := validArchiveQuery()

	first, err := service.GetSourceFile(t.Context(), query)
	require.NoError(t, err)
	second, err := service.GetSourceFile(t.Context(), query)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, signer.url, first.URL)
	require.Equal(t, now.Add(participantarchive.DefaultDownloadTTL), first.ExpiresAt)
	require.Equal(t, 2, repository.calls)
	require.Equal(t, 2, signer.calls)
	require.Equal(t, participantarchive.DefaultDownloadTTL, signer.ttl)
}

func TestServiceGetSourceFileFailsClosed(t *testing.T) {
	t.Parallel()

	canonicalURL := "http://seaweed:8333/bucket/tasks/task/sources/archive.zip"
	signingFailure := errors.New("signing unavailable")
	tests := []struct {
		name       string
		repository *archiveRepositoryStub
		signer     *archiveSignerStub
		want       error
	}{
		{
			name:       "wrong owner or pre-start assignment",
			repository: &archiveRepositoryStub{err: domain.ErrAssignmentParticipant},
			signer:     &archiveSignerStub{},
			want:       domain.ErrAssignmentParticipant,
		},
		{
			name:       "authorized snapshot has no source",
			repository: &archiveRepositoryStub{source: participantarchive.Source{TaskID: uuid.New()}},
			signer:     &archiveSignerStub{},
			want:       domain.ErrTaskNotFound,
		},
		{
			name:       "signing failure",
			repository: &archiveRepositoryStub{source: participantarchive.Source{TaskID: uuid.New(), CanonicalURL: &canonicalURL}},
			signer:     &archiveSignerStub{err: signingFailure},
			want:       signingFailure,
		},
		{
			name:       "invalid signed URL",
			repository: &archiveRepositoryStub{source: participantarchive.Source{TaskID: uuid.New(), CanonicalURL: &canonicalURL}},
			signer:     &archiveSignerStub{url: "/internal/archive.zip"},
			want:       domain.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service, newErr := participantarchive.New(tt.repository, tt.signer, archiveClock(time.Now()))
			require.NoError(t, newErr)
			got, err := service.GetSourceFile(t.Context(), validArchiveQuery())
			require.ErrorIs(t, err, tt.want)
			require.Empty(t, got)
		})
	}
}

func TestServiceSourceFileAvailableUsesSameAuthorization(t *testing.T) {
	t.Parallel()

	canonicalURL := "http://seaweed:8333/bucket/tasks/task/sources/archive.zip"
	service, err := participantarchive.New(
		&archiveRepositoryStub{source: participantarchive.Source{TaskID: uuid.New(), CanonicalURL: &canonicalURL}},
		&archiveSignerStub{},
		archiveClock(time.Now()),
	)
	require.NoError(t, err)
	available, err := service.SourceFileAvailable(t.Context(), validArchiveQuery())
	require.NoError(t, err)
	require.True(t, available)

	service, err = participantarchive.New(
		&archiveRepositoryStub{source: participantarchive.Source{TaskID: uuid.New()}},
		&archiveSignerStub{},
		archiveClock(time.Now()),
	)
	require.NoError(t, err)
	available, err = service.SourceFileAvailable(t.Context(), validArchiveQuery())
	require.NoError(t, err)
	require.False(t, available)
}

func TestServiceRejectsInvalidQuery(t *testing.T) {
	t.Parallel()

	service, newErr := participantarchive.New(&archiveRepositoryStub{}, &archiveSignerStub{}, archiveClock(time.Now()))
	require.NoError(t, newErr)
	_, err := service.GetSourceFile(t.Context(), inbound.ParticipantArchiveQuery{})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestWithDownloadTTLValidatesStorageBounds(t *testing.T) {
	t.Parallel()

	for _, ttl := range []time.Duration{0, time.Second - 1, 7*24*time.Hour + time.Second} {
		service, err := participantarchive.New(
			&archiveRepositoryStub{},
			&archiveSignerStub{},
			archiveClock(time.Now()),
			participantarchive.WithDownloadTTL(ttl),
		)
		require.ErrorIs(t, err, domain.ErrValidation)
		require.Nil(t, service)
	}
}

func validArchiveQuery() inbound.ParticipantArchiveQuery {
	return inbound.ParticipantArchiveQuery{
		Actor: inbound.Identity{PlayerID: uuid.New()}, TournamentID: uuid.New(), AssignmentID: uuid.New(),
	}
}
