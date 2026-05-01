package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var ErrNilClient = errors.New("seaweedfs: nil client")

// Config is the narrow value-type this adapter consumes. Wiring (internal/app)
// projects the global config; the package itself never imports config/.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Secure    bool
}

type SeaweedStorage struct {
	client *minio.Client
	bucket string
}

func New(cfg Config) (*SeaweedStorage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
	})
	if err != nil {
		return nil, fmt.Errorf("SeaweedStorage - New - minio.New: %w", err)
	}
	return &SeaweedStorage{client: client, bucket: cfg.Bucket}, nil
}

// EnsureBucket creates the configured bucket if it does not yet exist.
// Idempotent — call from the application bootstrap path.
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

// Upload streams r (size bytes) into <bucket>/<key> and returns the canonical
// object URL (<scheme>://<endpoint>/<bucket>/<key>). The 100 MB cap is enforced
// in the usecase layer (TASK-021); this method does not validate size.
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
	if s == nil || s.client == nil {
		return "", ErrNilClient
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
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
