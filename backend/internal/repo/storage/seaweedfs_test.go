package storage

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

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

	got, err := store.PresignedGetURL(t.Context(), "tasks/task-id/source.zip", time.Minute)
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
	if !strings.Contains(parsed.Path, "/task-per-minute/tasks/task-id/source.zip") {
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

	got, err := store.PresignedGetURL(t.Context(), "tasks/task-id/source.zip", time.Minute)
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

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", value, err)
	}
	return parsed
}
