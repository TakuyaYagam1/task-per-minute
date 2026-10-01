package avatar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPublicServiceReadsOnlyExactCurrentAvatarVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	playerID := uuid.New()
	data := []byte("\x89PNG\r\n\x1a\npublic-avatar")
	digest := sha256.Sum256(data)
	version := hex.EncodeToString(digest[:])
	metadata := &domain.PlayerAvatar{
		PlayerID:    playerID,
		ObjectKey:   "avatars/private/object.png",
		ContentType: "image/png",
		SizeBytes:   int64(len(data)),
		SHA256:      append([]byte(nil), digest[:]...),
	}
	repository := &publicAvatarRepositoryStub{metadata: metadata}
	objects := &publicAvatarStorageStub{data: data}
	service, err := NewPublicService(repository, objects)
	require.NoError(t, err)

	content, err := service.GetPublicAvatar(ctx, playerID, version)
	require.NoError(t, err)
	require.Equal(t, "image/png", content.ContentType)
	require.Equal(t, data, content.Data)
	require.Equal(t, playerID, repository.playerID)
	require.Equal(t, digest[:], repository.versionSHA256)
	require.Equal(t, metadata.ObjectKey, objects.objectKey)
	require.EqualValues(t, MaxAvatarBytes, objects.maxBytes)

	for _, invalidVersion := range []string{
		"",
		strings.ToUpper(version),
		version[:len(version)-1],
		version[:63] + "z",
	} {
		_, err := service.GetPublicAvatar(ctx, playerID, invalidVersion)
		require.ErrorIs(t, err, domain.ErrValidation)
	}
	require.Equal(t, 1, repository.calls, "invalid versions must be rejected before repository access")
}

func TestPublicServiceHidesMissingDeletedAndMismatchedAvatarMetadata(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	digest := sha256.Sum256([]byte("avatar"))
	otherDigest := sha256.Sum256([]byte("other"))
	version := hex.EncodeToString(digest[:])
	for _, test := range []struct {
		name     string
		metadata *domain.PlayerAvatar
		err      error
	}{
		{name: "no avatar", err: domain.ErrAvatarNotFound},
		{name: "deleted player", err: domain.ErrAvatarNotFound},
		{
			name: "mismatched digest",
			metadata: &domain.PlayerAvatar{
				PlayerID: playerID, ObjectKey: "avatars/object.png", ContentType: "image/png", SizeBytes: 6,
				SHA256: otherDigest[:],
			},
		},
		{
			name: "mismatched player",
			metadata: &domain.PlayerAvatar{
				PlayerID: uuid.New(), ObjectKey: "avatars/object.png", ContentType: "image/png", SizeBytes: 6,
				SHA256: digest[:],
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &publicAvatarRepositoryStub{metadata: test.metadata, err: test.err}
			objects := &publicAvatarStorageStub{data: []byte("unused")}
			service, err := NewPublicService(repository, objects)
			require.NoError(t, err)

			_, err = service.GetPublicAvatar(context.Background(), playerID, version)
			require.ErrorIs(t, err, domain.ErrAvatarNotFound)
			require.Zero(t, objects.getCalls, "hidden avatars must not reach object storage")
		})
	}
}

func TestPublicServiceRejectsInvalidStoredObjectIntegrity(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	data := []byte("\x89PNG\r\n\x1a\npublic-avatar")
	digest := sha256.Sum256(data)
	metadata := &domain.PlayerAvatar{
		PlayerID: playerID, ObjectKey: "avatars/object.png", ContentType: "image/png",
		SizeBytes: int64(len(data) + 1), SHA256: digest[:],
	}
	repository := &publicAvatarRepositoryStub{metadata: metadata}
	objects := &publicAvatarStorageStub{data: data}
	service, err := NewPublicService(repository, objects)
	require.NoError(t, err)

	_, err = service.GetPublicAvatar(context.Background(), playerID, hex.EncodeToString(digest[:]))
	require.ErrorIs(t, err, domain.ErrInternal)
	require.Equal(t, 1, objects.getCalls)

	wrongData := []byte("\x89PNG\r\n\x1a\nother-avatar")
	metadata.SizeBytes = int64(len(wrongData))
	metadata.SHA256 = digest[:]
	objects.data = wrongData
	objects.getCalls = 0
	_, err = service.GetPublicAvatar(context.Background(), playerID, hex.EncodeToString(digest[:]))
	require.ErrorIs(t, err, domain.ErrInternal)
	require.Equal(t, 1, objects.getCalls)
}

type publicAvatarRepositoryStub struct {
	metadata      *domain.PlayerAvatar
	err           error
	calls         int
	playerID      uuid.UUID
	versionSHA256 []byte
}

func (repository *publicAvatarRepositoryStub) GetPublicAvatar(
	_ context.Context,
	playerID uuid.UUID,
	versionSHA256 []byte,
) (*domain.PlayerAvatar, error) {
	repository.calls++
	repository.playerID = playerID
	repository.versionSHA256 = append([]byte(nil), versionSHA256...)
	if repository.err != nil {
		return nil, repository.err
	}
	return repository.metadata, nil
}

type publicAvatarStorageStub struct {
	data      []byte
	getErr    error
	getCalls  int
	objectKey string
	maxBytes  int64
}

func (*publicAvatarStorageStub) PutAvatar(context.Context, string, []byte, string) error { return nil }

func (storage *publicAvatarStorageStub) GetAvatar(_ context.Context, objectKey string, maxBytes int64) ([]byte, error) {
	storage.getCalls++
	storage.objectKey = objectKey
	storage.maxBytes = maxBytes
	if storage.getErr != nil {
		return nil, storage.getErr
	}
	return append([]byte(nil), storage.data...), nil
}

func (*publicAvatarStorageStub) DeleteAvatar(context.Context, string) error { return nil }

var _ PublicRepository = (*publicAvatarRepositoryStub)(nil)
var _ ObjectStorage = (*publicAvatarStorageStub)(nil)
