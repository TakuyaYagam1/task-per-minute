//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SeaweedFS is started lazily on the first call to sharedSeaweed(t) so PG-only
// test runs do not pay the ~5s startup cost. The same container is reused
// across all tests in the package; per-test isolation is achieved through
// unique object keys.
type seaweedFx struct {
	endpoint string
	bucket   string
}

var (
	seaweedOnce     sync.Once
	seaweedFixture  *seaweedFx
	errSeaweedInit  error
	seaweedTeardown func()
)

func sharedSeaweed(t *testing.T) *seaweedFx {
	t.Helper()
	seaweedOnce.Do(func() {
		seaweedFixture, seaweedTeardown, errSeaweedInit = startSeaweedFS()
	})
	if errSeaweedInit != nil {
		t.Fatalf("seaweedfs setup: %v", errSeaweedInit)
	}
	return seaweedFixture
}

const seaweedIdentitiesJSON = `{
  "identities": [
    {
      "name": "tpm",
      "credentials": [{"accessKey": "tpm", "secretKey": "tpm-secret"}],
      "actions": ["Admin"]
    }
  ]
}`

func startSeaweedFS() (*seaweedFx, func(), error) {
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
				Reader:            strings.NewReader(seaweedIdentitiesJSON),
				ContainerFilePath: "/etc/seaweedfs/s3.json",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForListeningPort("8333/tcp").
			WithStartupTimeout(containerStartupTimeout),
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
	fx := &seaweedFx{
		endpoint: fmt.Sprintf("%s:%s", host, port.Port()),
		bucket:   "tpm-test",
	}
	if err := waitForSeaweedS3(ctx, fx); err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}

	teardown := func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	}
	return fx, teardown, nil
}

func waitForSeaweedS3(ctx context.Context, fx *seaweedFx) error {
	st, err := objectstorage.New(objectstorage.Config{
		Endpoint:  fx.endpoint,
		AccessKey: "tpm",
		SecretKey: "tpm-secret",
		Bucket:    fx.bucket,
		Secure:    false,
	})
	if err != nil {
		return fmt.Errorf("create seaweed storage readiness client: %w", err)
	}

	deadline := time.Now().Add(containerStartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := st.EnsureBucket(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("seaweedfs s3 readiness: %w", lastErr)
}
