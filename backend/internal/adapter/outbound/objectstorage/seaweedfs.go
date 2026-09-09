package objectstorage

import (
	"context"
	"errors"
	"fmt"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"time"
)

var (
	ErrNilClient         = errors.New("seaweedfs: nil client")
	ErrBucketUnavailable = errors.New("seaweedfs: configured bucket unavailable")
)

const defaultPresignRegion = "us-east-1"

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
