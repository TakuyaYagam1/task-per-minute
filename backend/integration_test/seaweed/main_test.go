//go:build integration

package seaweed_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

const containerStartupTimeout = 90 * time.Second

type seaweedFx struct {
	endpoint string
	bucket   string
}

var sharedSeaweedFixture *seaweedFx

const seaweedIdentitiesJSON = `{
  "identities": [
    {
      "name": "tpm",
      "credentials": [{"accessKey": "tpm", "secretKey": "tpm-secret"}],
      "actions": ["Admin"]
    }
  ]
}`

func TestMain(m *testing.M) {
	fixture, teardown, err := testkit.StartSeaweed(testkit.SeaweedConfig{
		StartupTimeout: containerStartupTimeout,
		IdentitiesJSON: seaweedIdentitiesJSON,
		AccessKey:      "tpm",
		SecretKey:      "tpm-secret",
		Bucket:         "tpm-test",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test/seaweed: failed to start seaweedfs: %v\n", err)
		os.Exit(1)
	}
	sharedSeaweedFixture = &seaweedFx{endpoint: fixture.Endpoint, bucket: fixture.Bucket}

	code := m.Run()
	teardown()
	os.Exit(code)
}

func sharedSeaweed(t *testing.T) *seaweedFx {
	t.Helper()
	if sharedSeaweedFixture == nil {
		t.Fatal("seaweedfs fixture is not initialized")
	}
	return sharedSeaweedFixture
}
