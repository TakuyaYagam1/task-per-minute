//go:build integration

package seaweed_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestSeaweedStorage_UploadPresignedDeleteCycle(t *testing.T) {
	t.Parallel()
	st := newSeaweedStorage(t)
	ctx := context.Background()

	payload := []byte("hello, task-per-minute\n")
	key := "task-source/" + uuid.NewString() + ".zip"

	url, err := st.Upload(ctx, key, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Contains(t, url, sharedSeaweed(t).bucket)
	require.Contains(t, url, key)

	presigned, err := st.PresignedGetURL(ctx, key, time.Minute)
	require.NoError(t, err)
	require.Contains(t, presigned, key)
	require.Contains(t, presigned, "X-Amz-Signature")

	resp := httpGetWithTimeout(t, presigned)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, body)

	require.NoError(t, st.Delete(ctx, key))

	presigned2, err := st.PresignedGetURL(ctx, key, time.Minute)
	require.NoError(t, err)
	resp2 := httpGetWithTimeout(t, presigned2)
	defer resp2.Body.Close()
	require.NotEqual(t, http.StatusOK, resp2.StatusCode,
		"presigned GET must fail (404/403) after delete")
}

func TestSeaweedStorage_Upload_LargerPayload(t *testing.T) {
	t.Parallel()
	st := newSeaweedStorage(t)
	ctx := context.Background()

	payload := bytes.Repeat([]byte("A"), 1<<20)
	key := "task-source/" + uuid.NewString() + ".bin"

	_, err := st.Upload(ctx, key, bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Delete(ctx, key) })

	presigned, err := st.PresignedGetURL(ctx, key, time.Minute)
	require.NoError(t, err)

	resp := httpGetWithTimeout(t, presigned)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, body, len(payload))
	require.Equal(t, payload, body)
}

func TestSeaweedStorage_EnsureBucket_Idempotent(t *testing.T) {
	t.Parallel()
	st := newSeaweedStorage(t)
	require.NoError(t, st.EnsureBucket(context.Background()),
		"second EnsureBucket on the same bucket must be a no-op")
}

func TestSeaweedStorage_Delete_MissingKey_NoError(t *testing.T) {
	t.Parallel()
	st := newSeaweedStorage(t)
	require.NoError(t, st.Delete(context.Background(), "does/not/exist/"+uuid.NewString()),
		"S3 RemoveObject is idempotent - missing keys must not surface as errors")
}
