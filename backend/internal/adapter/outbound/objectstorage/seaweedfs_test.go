package objectstorage

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const sourceFileTestKey = "tasks/0f765201-3d3f-4f3b-bcb2-1f5cd762be17/sources/1a018f4a-b147-46d4-a403-9230490ba47d.zip"

func TestSeaweedStorage_PresignedGetURL_UsesInternalEndpointByDefault(t *testing.T) {
	store, err := New(Config{
		Endpoint:  "internal.example.com:8333",
		AccessKey: "access-key",
		SecretKey: "secret-key",
		Bucket:    "task-per-minute",
		Secure:    false,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := store.PresignedGetURL(t.Context(), sourceFileTestKey, time.Minute)
	if err != nil {
		t.Fatalf("PresignedGetURL: %v", err)
	}
	parsed := mustParseURL(t, got)

	if parsed.Scheme != "http" {
		t.Fatalf("scheme = %q, want http", parsed.Scheme)
	}
	if parsed.Host != "internal.example.com:8333" {
		t.Fatalf("host = %q, want internal.example.com:8333", parsed.Host)
	}
	if !strings.Contains(parsed.Path, "/task-per-minute/"+sourceFileTestKey) {
		t.Fatalf("path = %q, want bucket and key", parsed.Path)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("expected signed query to contain X-Amz-Signature")
	}
}

func TestSeaweedStorage_PresignedGetURL_UsesPublicEndpoint(t *testing.T) {
	store, err := New(Config{
		Endpoint:       "seaweedfs:8333",
		PublicEndpoint: "files.example.com",
		AccessKey:      "access-key",
		SecretKey:      "secret-key",
		Bucket:         "task-per-minute",
		Secure:         false,
		PublicSecure:   true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := store.PresignedGetURL(t.Context(), sourceFileTestKey, time.Minute)
	if err != nil {
		t.Fatalf("PresignedGetURL: %v", err)
	}
	parsed := mustParseURL(t, got)

	if parsed.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", parsed.Scheme)
	}
	if parsed.Host != "files.example.com" {
		t.Fatalf("host = %q, want files.example.com", parsed.Host)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("expected signed query to contain X-Amz-Signature")
	}
}

func TestSeaweedStorage_HealthVerifiesExistingBucketWithoutMutation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Query().Has("location") {
			require.Equal(t, "/task-per-minute/", request.URL.Path)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte("<LocationConstraint>us-east-1</LocationConstraint>"))
			return
		}
		require.Equal(t, http.MethodHead, request.Method)
		require.Equal(t, "/task-per-minute/", request.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	store := newHealthTestStore(t, server.URL)

	require.NoError(t, store.Health(t.Context()))
}

func TestSeaweedStorage_HealthFailsClosedWhenBucketIsMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Query().Has("location") {
			require.Equal(t, "/task-per-minute/", request.URL.Path)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte("<LocationConstraint>us-east-1</LocationConstraint>"))
			return
		}
		require.Equal(t, http.MethodHead, request.Method)
		require.Equal(t, "/task-per-minute/", request.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<Error><Code>NoSuchBucket</Code><Message>missing</Message></Error>"))
	}))
	defer server.Close()
	store := newHealthTestStore(t, server.URL)

	err := store.Health(t.Context())
	require.ErrorIs(t, err, ErrBucketUnavailable)
}

func TestSeaweedStorage_HealthFailsClosedWithoutClient(t *testing.T) {
	t.Parallel()

	var store *SeaweedStorage
	require.ErrorIs(t, store.Health(t.Context()), ErrNilClient)
}

func newHealthTestStore(t *testing.T, endpoint string) *SeaweedStorage {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	store, err := New(Config{
		Endpoint: parsed.Host, AccessKey: "access-key", SecretKey: "secret-key",
		Bucket: "task-per-minute", Secure: parsed.Scheme == "https",
	})
	require.NoError(t, err)
	return store
}

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", value, err)
	}
	return parsed
}
