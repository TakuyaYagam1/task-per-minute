package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	ErrNilClient            = errors.New("seaweedfs: nil client")
	ErrBucketUnavailable    = errors.New("seaweedfs: configured bucket unavailable")
	ErrInvalidAvatarKey     = errors.New("seaweedfs: invalid avatar object key")
	ErrInvalidAvatarType    = errors.New("seaweedfs: invalid avatar content type")
	ErrAvatarObjectTooLarge = errors.New("seaweedfs: avatar object exceeds size limit")
)

const (
	defaultPresignRegion = "us-east-1"
	maxAvatarObjectBytes = 5 << 20
)

// Config is the narrow value-type this adapter consumes. Bootstrap wiring
// projects the global config; the package itself never imports config/.
type Config struct {
	Endpoint       string
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	Bucket         string
	Secure         bool
	PublicSecure   bool
}

type SeaweedStorage struct {
	client        *minio.Client
	presignClient *minio.Client
	bucket        string
}

var _ taskusecase.SourceFileStorage = (*SeaweedStorage)(nil)

func New(cfg Config) (*SeaweedStorage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
	})
	if err != nil {
		return nil, fmt.Errorf("SeaweedStorage - New - minio.New: %w", err)
	}

	presignEndpoint := cfg.PublicEndpoint
	presignSecure := cfg.PublicSecure
	if presignEndpoint == "" {
		presignEndpoint = cfg.Endpoint
		presignSecure = cfg.Secure
	}
	presignClient, err := minio.New(presignEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: presignSecure,
		Region: defaultPresignRegion,
	})
	if err != nil {
		return nil, fmt.Errorf("SeaweedStorage - New - public minio.New: %w", err)
	}
	return &SeaweedStorage{client: client, presignClient: presignClient, bucket: cfg.Bucket}, nil
}

// EnsureBucket creates the configured bucket if it does not yet exist.
// Idempotent - call from the application bootstrap path.
func (s *SeaweedStorage) EnsureBucket(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrNilClient
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("SeaweedStorage - EnsureBucket - Client.BucketExists: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("SeaweedStorage - EnsureBucket - Client.MakeBucket: %w", err)
	}
	return nil
}

// Health verifies that the configured bucket already exists and is readable by
// the configured credentials. Unlike EnsureBucket, it never changes storage
// state and is safe for readiness and preflight probes.
func (s *SeaweedStorage) Health(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrNilClient
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("SeaweedStorage - Health - Client.BucketExists: %w", err)
	}
	if !exists {
		return ErrBucketUnavailable
	}
	return nil
}

// Upload streams r (size bytes) into <bucket>/<key> and returns the canonical
// object URL (<scheme>://<endpoint>/<bucket>/<key>). The source-file application
// service enforces the size cap; this adapter only streams bytes.
func (s *SeaweedStorage) Upload(ctx context.Context, key string, r io.Reader, size int64) (string, error) {
	if s == nil || s.client == nil {
		return "", ErrNilClient
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	}); err != nil {
		return "", fmt.Errorf("SeaweedStorage - Upload - Client.PutObject: %w", err)
	}
	return s.client.EndpointURL().JoinPath(s.bucket, key).String(), nil
}

func (s *SeaweedStorage) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s == nil || s.presignClient == nil {
		return "", ErrNilClient
	}
	u, err := s.presignClient.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("SeaweedStorage - PresignedGetURL - Client.PresignedGetObject: %w", err)
	}
	return u.String(), nil
}

func (s *SeaweedStorage) Delete(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return ErrNilClient
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("SeaweedStorage - Delete - Client.RemoveObject: %w", err)
	}
	return nil
}

// PutAvatar stores an already-normalized image under the private avatar prefix.
// The method accepts no caller-controlled bucket or public URL.
func (s *SeaweedStorage) PutAvatar(ctx context.Context, key string, data []byte, contentType string) error {
	if s == nil || s.client == nil {
		return ErrNilClient
	}
	if !validAvatarKey(key) {
		return ErrInvalidAvatarKey
	}
	if len(data) == 0 || len(data) > maxAvatarObjectBytes {
		return ErrAvatarObjectTooLarge
	}
	if !validAvatarContentType(contentType) {
		return ErrInvalidAvatarType
	}
	if !strings.HasSuffix(key, "."+avatarExtension(contentType)) {
		return ErrInvalidAvatarKey
	}
	if _, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	}); err != nil {
		return fmt.Errorf("SeaweedStorage - PutAvatar - Client.PutObject: %w", err)
	}
	return nil
}

// GetAvatar reads an avatar through the private S3 client and bounds the total
// bytes retained in memory. Callers obtain the object key only from metadata.
func (s *SeaweedStorage) GetAvatar(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, ErrNilClient
	}
	if !validAvatarKey(key) {
		return nil, ErrInvalidAvatarKey
	}
	if maxBytes <= 0 || maxBytes > maxAvatarObjectBytes {
		return nil, ErrAvatarObjectTooLarge
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("SeaweedStorage - GetAvatar - Client.GetObject: %w", err)
	}
	defer func() { _ = object.Close() }()
	info, err := object.Stat()
	if err != nil {
		return nil, fmt.Errorf("SeaweedStorage - GetAvatar - Object.Stat: %w", err)
	}
	if info.Size <= 0 || info.Size > maxBytes {
		return nil, ErrAvatarObjectTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(object, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("SeaweedStorage - GetAvatar - read object: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrAvatarObjectTooLarge
	}
	return data, nil
}

// DeleteAvatar is idempotent for missing objects so interrupted cleanup claims
// can be retried safely.
func (s *SeaweedStorage) DeleteAvatar(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return ErrNilClient
	}
	if !validAvatarKey(key) {
		return ErrInvalidAvatarKey
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		response := minio.ToErrorResponse(err)
		if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" {
			return nil
		}
		return fmt.Errorf("SeaweedStorage - DeleteAvatar - Client.RemoveObject: %w", err)
	}
	return nil
}

func validAvatarKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "avatars" {
		return false
	}
	playerID, err := uuid.Parse(parts[1])
	if err != nil || playerID.String() != parts[1] {
		return false
	}
	nameParts := strings.Split(parts[2], ".")
	if len(nameParts) != 2 || (nameParts[1] != "jpg" && nameParts[1] != "png" && nameParts[1] != "gif" && nameParts[1] != "mp4") {
		return false
	}
	objectID, err := uuid.Parse(nameParts[0])
	return err == nil && objectID.String() == nameParts[0]
}

func validAvatarContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "video/mp4":
		return true
	default:
		return false
	}
}

func avatarExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	default:
		return ""
	}
}
