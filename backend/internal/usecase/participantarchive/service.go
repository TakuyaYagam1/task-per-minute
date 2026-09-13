package participantarchive

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

const (
	DefaultDownloadTTL = 5 * time.Minute
	minDownloadTTL     = time.Second
	maxDownloadTTL     = 7 * 24 * time.Hour
)

type Source struct {
	TaskID       uuid.UUID
	CanonicalURL *string
}

type Repository interface {
	FindSource(ctx context.Context, query inbound.ParticipantArchiveQuery) (Source, error)
}

type Signer interface {
	PresignCanonicalSourceFileURL(
		ctx context.Context,
		taskID uuid.UUID,
		canonicalURL string,
		ttl time.Duration,
	) (string, error)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	repository Repository
	signer     Signer
	clock      Clock
	ttl        time.Duration
}

type Option func(*Service) error

func WithDownloadTTL(ttl time.Duration) Option {
	return func(service *Service) error {
		if service == nil || ttl < minDownloadTTL || ttl > maxDownloadTTL {
			return domain.ErrValidation
		}
		service.ttl = ttl
		return nil
	}
}

func New(repository Repository, signer Signer, clock Clock, options ...Option) (*Service, error) {
	service := &Service{repository: repository, signer: signer, clock: clock, ttl: DefaultDownloadTTL}
	for _, option := range options {
		if option == nil {
			return nil, domain.ErrValidation
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) GetSourceFile(
	ctx context.Context,
	query inbound.ParticipantArchiveQuery,
) (inbound.ParticipantArchiveDownload, error) {
	if err := validateQuery(ctx, query); err != nil || s == nil || s.repository == nil || s.signer == nil || s.clock == nil {
		return inbound.ParticipantArchiveDownload{}, domain.ErrValidation
	}
	source, err := s.repository.FindSource(ctx, query)
	if err != nil {
		return inbound.ParticipantArchiveDownload{}, fmt.Errorf("ParticipantArchive - GetSourceFile - Repository.FindSource: %w", err)
	}
	if source.TaskID == uuid.Nil || source.CanonicalURL == nil || strings.TrimSpace(*source.CanonicalURL) == "" {
		return inbound.ParticipantArchiveDownload{}, domain.ErrTaskNotFound
	}

	issuedAt := s.clock.Now().UTC()
	if issuedAt.IsZero() {
		return inbound.ParticipantArchiveDownload{}, domain.ErrInternal
	}
	url, err := s.signer.PresignCanonicalSourceFileURL(ctx, source.TaskID, *source.CanonicalURL, s.ttl)
	if err != nil {
		return inbound.ParticipantArchiveDownload{}, fmt.Errorf("ParticipantArchive - GetSourceFile - Signer.PresignCanonicalSourceFileURL: %w", err)
	}
	if !validDownloadURL(url) {
		return inbound.ParticipantArchiveDownload{}, domain.ErrInternal
	}
	return inbound.ParticipantArchiveDownload{URL: url, ExpiresAt: issuedAt.Add(s.ttl)}, nil
}

func validDownloadURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func (s *Service) SourceFileAvailable(
	ctx context.Context,
	query inbound.ParticipantArchiveQuery,
) (bool, error) {
	if err := validateQuery(ctx, query); err != nil || s == nil || s.repository == nil {
		return false, domain.ErrValidation
	}
	source, err := s.repository.FindSource(ctx, query)
	if err != nil {
		return false, fmt.Errorf("ParticipantArchive - SourceFileAvailable - Repository.FindSource: %w", err)
	}
	return source.TaskID != uuid.Nil && source.CanonicalURL != nil && strings.TrimSpace(*source.CanonicalURL) != "", nil
}

func validateQuery(ctx context.Context, query inbound.ParticipantArchiveQuery) error {
	if ctx == nil || query.Actor.PlayerID == uuid.Nil || query.TournamentID == uuid.Nil || query.AssignmentID == uuid.Nil {
		return domain.ErrValidation
	}
	return nil
}

var _ inbound.ParticipantArchiveUseCase = (*Service)(nil)
