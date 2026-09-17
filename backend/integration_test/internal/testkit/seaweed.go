//go:build integration

package testkit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SeaweedConfig contains the identity and bucket values required by the
// integration SeaweedFS S3 endpoint.
type SeaweedConfig struct {
	StartupTimeout time.Duration
	IdentitiesJSON string
	AccessKey      string
	SecretKey      string
	Bucket         string
}

// SeaweedFixture contains the endpoint and bucket used by integration tests.
type SeaweedFixture struct {
	Endpoint string
	Bucket   string
}

// StartSeaweed starts SeaweedFS, waits for the S3 endpoint, and ensures the
// configured bucket exists before returning the process-level fixture.
func StartSeaweed(config SeaweedConfig) (*SeaweedFixture, func(), error) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image: "chrislusf/seaweedfs:3.71",
		Cmd: []string{
			"server",
			"-dir=/data",
			"-s3",
			"-s3.port=8333",
			"-s3.config=/etc/seaweedfs/s3.json",
		},
		ExposedPorts: []string{"8333/tcp"},
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(config.IdentitiesJSON),
				ContainerFilePath: "/etc/seaweedfs/s3.json",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForListeningPort("8333/tcp").
			WithStartupTimeout(config.StartupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start seaweedfs container: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}
	port, err := c.MappedPort(ctx, "8333/tcp")
	if err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}
	fixture := &SeaweedFixture{
		Endpoint: fmt.Sprintf("%s:%s", host, port.Port()),
		Bucket:   config.Bucket,
	}
	if err := waitForSeaweedS3(ctx, fixture, config); err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}

	teardown := func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	}
	return fixture, teardown, nil
}

func waitForSeaweedS3(ctx context.Context, fixture *SeaweedFixture, config SeaweedConfig) error {
	storage, err := objectstorage.New(objectstorage.Config{
		Endpoint: fixture.Endpoint, AccessKey: config.AccessKey, SecretKey: config.SecretKey,
		Bucket: fixture.Bucket, Secure: false,
	})
	if err != nil {
		return fmt.Errorf("create seaweed storage readiness client: %w", err)
	}

	deadline := time.Now().Add(config.StartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := storage.EnsureBucket(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("seaweedfs s3 readiness: %w", lastErr)
}
