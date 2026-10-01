package avatar

import (
	"context"
	"crypto/sha256"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/google/uuid"
)

const (
	MaxAvatarBytes       = 5 << 20
	maxAvatarWidth       = 4096
	maxAvatarHeight      = 4096
	maxStaticPixels      = 16 << 20
	uploadIntentLease    = 5 * time.Minute
	storagePutTimeout    = 2 * time.Minute
	storageGetTimeout    = 30 * time.Second
	maxConcurrentDecodes = 2
)

var _ inbound.PlayerAvatarService = (*Service)(nil)

type Service struct {
	repository  Repository
	objects     ObjectStorage
	clock       Clock
	video       VideoProcessor
	decodeSlots chan struct{}
}

func NewService(repository Repository, objects ObjectStorage, clock Clock, video VideoProcessor) (*Service, error) {
	if repository == nil || objects == nil || clock == nil {
		return nil, domain.ErrInternal
	}
	return &Service{
		repository:  repository,
		objects:     objects,
		clock:       clock,
		video:       video,
		decodeSlots: make(chan struct{}, maxConcurrentDecodes),
	}, nil
}

func (s *Service) GetAvatar(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
) (inbound.PlayerAvatarContent, error) {
	if ctx == nil || playerID == uuid.Nil || sessionToken == uuid.Nil {
		return inbound.PlayerAvatarContent{}, domain.ErrInvalidSession
	}
	avatar, err := s.repository.GetAvatar(ctx, playerID, sessionToken)
	if err != nil {
		return inbound.PlayerAvatarContent{}, err
	}
	return readAvatarObject(ctx, s.objects, avatar)
}

func (s *Service) ReplaceAvatar(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
	declaredContentType string,
	data []byte,
) error {
	if ctx == nil || playerID == uuid.Nil || sessionToken == uuid.Nil {
		return domain.ErrInvalidSession
	}
	if len(data) == 0 {
		return domain.ErrAvatarInvalid
	}
	if len(data) > MaxAvatarBytes {
		return domain.ErrAvatarTooLarge
	}
	if err := s.acquireDecodeSlot(ctx); err != nil {
		return err
	}
	defer func() { <-s.decodeSlots }()

	contentType, canonical, err := s.canonicalizeAvatar(ctx, data, declaredContentType)
	if err != nil {
		return err
	}
	if len(canonical) == 0 || len(canonical) > MaxAvatarBytes {
		return domain.ErrAvatarTooLarge
	}

	objectID, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("create player avatar object id: %w", err)
	}
	objectKey := avatarObjectKey(playerID, objectID, contentType)
	now := s.clock.Now().UTC()
	generation, err := s.repository.PrepareUpload(ctx, playerID, sessionToken, objectKey, now.Add(uploadIntentLease))
	if err != nil {
		return err
	}

	putCtx, cancel := context.WithTimeout(ctx, storagePutTimeout)
	putErr := s.objects.PutAvatar(putCtx, objectKey, canonical, contentType)
	cancel()
	if putErr != nil {
		return fmt.Errorf("write player avatar object: %w", putErr)
	}

	digest := sumSHA256(canonical)
	if err := s.repository.ActivateUpload(
		ctx,
		playerID,
		sessionToken,
		objectKey,
		contentType,
		int64(len(canonical)),
		digest,
		generation,
		s.clock.Now().UTC(),
	); err != nil {
		return err
	}
	return nil
}

func (s *Service) DeleteAvatar(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
) error {
	if ctx == nil || playerID == uuid.Nil || sessionToken == uuid.Nil {
		return domain.ErrInvalidSession
	}
	return s.repository.DeleteAvatar(ctx, playerID, sessionToken, s.clock.Now().UTC())
}

func (s *Service) acquireDecodeSlot(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.decodeSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return domain.ErrAvatarBusy
	}
}

func (s *Service) canonicalizeAvatar(ctx context.Context, data []byte, declaredContentType string) (string, []byte, error) {
	declared, _, err := mime.ParseMediaType(strings.TrimSpace(declaredContentType))
	if err != nil || !isSupportedContentType(strings.ToLower(declared)) {
		return "", nil, domain.ErrAvatarUnsupportedMediaType
	}
	declared = strings.ToLower(declared)
	actual := detectAvatarContentType(data)
	if !isSupportedContentType(actual) {
		return "", nil, domain.ErrAvatarInvalid
	}
	if actual != declared {
		return "", nil, domain.ErrAvatarInvalid
	}
	if actual == "video/mp4" {
		if s.video == nil {
			return "", nil, domain.ErrAvatarVideoUnavailable
		}
		canonical, err := s.video.CanonicalizeMP4(ctx, append([]byte(nil), data...))
		if err != nil {
			return "", nil, err
		}
		if detectAvatarContentType(canonical) != "video/mp4" {
			return "", nil, domain.ErrAvatarInvalid
		}
		return "video/mp4", canonical, nil
	}
	return canonicalAvatar(data, declared)
}

func canonicalAvatar(data []byte, declaredContentType string) (string, []byte, error) {
	declared, _, err := mime.ParseMediaType(strings.TrimSpace(declaredContentType))
	if err != nil || !isSupportedContentType(strings.ToLower(declared)) {
		return "", nil, domain.ErrAvatarUnsupportedMediaType
	}
	declared = strings.ToLower(declared)
	actual := detectAvatarContentType(data)
	if actual == "video/mp4" {
		return "", nil, domain.ErrAvatarUnsupportedMediaType
	}
	if !isSupportedContentType(actual) {
		return "", nil, domain.ErrAvatarInvalid
	}
	if actual != declared {
		return "", nil, domain.ErrAvatarInvalid
	}
	return encodeCanonicalImage(append([]byte(nil), data...), actual)
}

func isSupportedContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "video/mp4":
		return true
	default:
		return false
	}
}

func avatarObjectKey(playerID, objectID uuid.UUID, contentType string) string {
	extension := "jpg"
	switch contentType {
	case "image/png":
		extension = "png"
	case "image/gif":
		extension = "gif"
	case "video/mp4":
		extension = "mp4"
	}
	return fmt.Sprintf("avatars/%s/%s.%s", playerID.String(), objectID.String(), extension)
}

func detectAvatarContentType(data []byte) string {
	if hasMP4FileTypeBox(data) {
		return "video/mp4"
	}
	return http.DetectContentType(data)
}

func hasMP4FileTypeBox(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	boxSize := uint64(data[0])<<24 | uint64(data[1])<<16 | uint64(data[2])<<8 | uint64(data[3])
	return boxSize >= 12 && boxSize <= uint64(len(data))
}

func sumSHA256(data []byte) []byte {
	digest := sha256.Sum256(data)
	return digest[:]
}
