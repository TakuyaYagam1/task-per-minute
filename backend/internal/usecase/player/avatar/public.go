package avatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/google/uuid"
)

type PublicService struct {
	repository PublicRepository
	objects    ObjectStorage
}

var _ inbound.PublicPlayerAvatarService = (*PublicService)(nil)

func NewPublicService(repository PublicRepository, objects ObjectStorage) (*PublicService, error) {
	if repository == nil || objects == nil {
		return nil, domain.ErrInternal
	}
	return &PublicService{repository: repository, objects: objects}, nil
}

func (s *PublicService) GetPublicAvatar(
	ctx context.Context,
	playerID uuid.UUID,
	version string,
) (inbound.PlayerAvatarContent, error) {
	if ctx == nil || playerID == uuid.Nil {
		return inbound.PlayerAvatarContent{}, domain.ErrValidation
	}
	digest, err := decodeAvatarVersion(version)
	if err != nil {
		return inbound.PlayerAvatarContent{}, err
	}

	metadata, err := s.repository.GetPublicAvatar(ctx, playerID, digest)
	if err != nil {
		return inbound.PlayerAvatarContent{}, err
	}
	if metadata == nil || metadata.PlayerID != playerID || !bytes.Equal(metadata.SHA256, digest) {
		return inbound.PlayerAvatarContent{}, domain.ErrAvatarNotFound
	}
	return readAvatarObject(ctx, s.objects, metadata)
}

func decodeAvatarVersion(version string) ([]byte, error) {
	if len(version) != hex.EncodedLen(sha256.Size) {
		return nil, domain.ErrValidation
	}
	digest, err := hex.DecodeString(version)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != version {
		return nil, domain.ErrValidation
	}
	return digest, nil
}

func readAvatarObject(
	ctx context.Context,
	objects ObjectStorage,
	metadata *domain.PlayerAvatar,
) (inbound.PlayerAvatarContent, error) {
	if objects == nil || metadata == nil {
		return inbound.PlayerAvatarContent{}, domain.ErrInternal
	}
	if metadata.SizeBytes <= 0 || metadata.SizeBytes > MaxAvatarBytes || len(metadata.SHA256) != sha256.Size {
		return inbound.PlayerAvatarContent{}, fmt.Errorf("player avatar metadata integrity check: %w", domain.ErrInternal)
	}
	readCtx, cancel := context.WithTimeout(ctx, storageGetTimeout)
	defer cancel()
	data, err := objects.GetAvatar(readCtx, metadata.ObjectKey, MaxAvatarBytes)
	if err != nil {
		return inbound.PlayerAvatarContent{}, fmt.Errorf("read player avatar object: %w", err)
	}
	if int64(len(data)) != metadata.SizeBytes || !bytes.Equal(sumSHA256(data), metadata.SHA256) {
		return inbound.PlayerAvatarContent{}, fmt.Errorf("player avatar object integrity check: %w", domain.ErrInternal)
	}
	if !isSupportedContentType(metadata.ContentType) || detectAvatarContentType(data) != metadata.ContentType {
		return inbound.PlayerAvatarContent{}, fmt.Errorf("player avatar object media type check: %w", domain.ErrInternal)
	}
	return inbound.PlayerAvatarContent{ContentType: metadata.ContentType, Data: data}, nil
}
