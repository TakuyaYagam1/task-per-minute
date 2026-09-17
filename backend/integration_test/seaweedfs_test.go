//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
)

func newSeaweedStorage(t *testing.T) *objectstorage.SeaweedStorage {
	t.Helper()
	fx := sharedSeaweed(t)
	st, err := objectstorage.New(objectstorage.Config{
		Endpoint:  fx.endpoint,
		AccessKey: "tpm",
		SecretKey: "tpm-secret",
		Bucket:    fx.bucket,
		Secure:    false,
	})
	require.NoError(t, err)
	require.NoError(t, st.EnsureBucket(context.Background()))
	return st
}

func httpGetWithTimeout(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}
