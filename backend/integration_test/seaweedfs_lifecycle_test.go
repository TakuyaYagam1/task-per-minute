//go:build integration

package integration_test

import (
	"sync"
	"testing"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
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
	fixture, teardown, err := testkit.StartSeaweed(testkit.SeaweedConfig{
		StartupTimeout: containerStartupTimeout,
		IdentitiesJSON: seaweedIdentitiesJSON,
		AccessKey:      "tpm",
		SecretKey:      "tpm-secret",
		Bucket:         "tpm-test",
	})
	if err != nil {
		return nil, nil, err
	}
	return &seaweedFx{endpoint: fixture.Endpoint, bucket: fixture.Bucket}, teardown, nil
}
