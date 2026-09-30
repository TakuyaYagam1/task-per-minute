package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestDisabledSenderReturnsUnavailable(t *testing.T) {
	sender, err := New(Config{Provider: ProviderDisabled})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := sender.SendVerification(context.Background(), "player@example.invalid", "https://example.org/verify-email#token=synthetic_token"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SendVerification error = %v, want ErrUnavailable", err)
	}
}

func TestResendSenderBuildsVerificationMessage(t *testing.T) {
	const (
		apiKey = "re_synthetic_test_key"
		to     = "player@example.invalid"
		link   = "https://example.org/verify&email#token=synthetic_token-123"
	)
	sender, err := New(Config{
		Provider:     ProviderResend,
		From:         "noreply@example.org",
		ResendAPIKey: apiKey,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var gotRequest resendRequest
	called := false
	sender.resendURL = "https://api.test/emails"
	sender.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		if request.Method != http.MethodPost || request.URL.String() != sender.resendURL {
			t.Fatalf("request = %s %s, want POST %s", request.Method, request.URL, sender.resendURL)
		}
		if request.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Fatal("Resend authorization header was not set")
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatal("Resend content type was not set")
		}
		if err := json.NewDecoder(request.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode Resend request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})

	if err := sender.SendVerification(context.Background(), to, link); err != nil {
		t.Fatalf("SendVerification: %v", err)
	}
	if !called {
		t.Fatal("Resend transport was not called")
	}
	if gotRequest.From != "noreply@example.org" || len(gotRequest.To) != 1 || gotRequest.To[0] != to {
		t.Fatalf("message addresses = from %q, to %v", gotRequest.From, gotRequest.To)
	}
	if gotRequest.Subject != "Подтвердите регистрацию в Task Per Minute" || !strings.Contains(gotRequest.Text, link) {
		t.Fatalf("message subject or text body is incorrect")
	}
	if !strings.Contains(gotRequest.HTML, `href="https://example.org/verify&amp;email#token=synthetic_token-123"`) {
		t.Fatalf("HTML link was not escaped: %s", gotRequest.HTML)
	}
}

func TestResendSenderDoesNotFollowRedirect(t *testing.T) {
	sender, err := New(Config{
		Provider:     ProviderResend,
		From:         "noreply@example.org",
		ResendAPIKey: "re_synthetic_test_key",
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sender.resendURL = "https://api.test/emails"
	requests := 0
	sender.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Header.Get("Authorization") == "" {
			t.Fatal("Resend authorization header was not set")
		}
		header := make(http.Header)
		header.Set("Location", "https://redirect.example.invalid/collect")
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     header,
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})

	err = sender.SendVerification(context.Background(), "player@example.invalid", "https://example.org/verify-email#token=synthetic_token")
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("SendVerification error = %v, want a safe HTTP 307 error", err)
	}
	if requests != 1 {
		t.Fatalf("Resend transport saw %d requests after redirect, want 1", requests)
	}
}

func TestResendSenderDoesNotReturnProviderBody(t *testing.T) {
	const (
		apiKey = "re_synthetic_secret"
		to     = "private-player@example.invalid"
		link   = "https://example.org/verify-email#token=private_token"
		body   = "provider response includes private-player@example.invalid and private-token"
	)
	sender, err := New(Config{
		Provider:     ProviderResend,
		From:         "noreply@example.org",
		ResendAPIKey: apiKey,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sender.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})
	err = sender.SendVerification(context.Background(), to, link)
	if err == nil {
		t.Fatal("SendVerification error = nil, want provider failure")
	}
	for _, sensitive := range []string{apiKey, to, link, "private_token", body} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("SendVerification error exposed sensitive content")
		}
	}
}

func TestSendVerificationRejectsHeaderAndLinkInjection(t *testing.T) {
	sender, err := New(Config{
		Provider:     ProviderResend,
		From:         "noreply@example.org",
		ResendAPIKey: "re_synthetic_test_key",
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, input := range []struct {
		name string
		to   string
		link string
	}{
		{name: "recipient header injection", to: "player@example.invalid\r\nBcc: victim@example.invalid", link: "https://example.org/verify-email#token=synthetic_token"},
		{name: "non-HTTPS verification link", to: "player@example.invalid", link: "http://example.org/verify-email#token=synthetic_token"},
		{name: "verification URL user info", to: "player@example.invalid", link: "https://attacker@example.org/verify-email#token=synthetic_token"},
		{name: "verification URL query rejected", to: "player@example.invalid", link: "https://example.org/verify-email?token=synthetic_token#token=synthetic_token"},
		{name: "unexpected fragment rejected", to: "player@example.invalid", link: "https://example.org/verify-email#anything"},
	} {
		t.Run(input.name, func(t *testing.T) {
			if err := sender.SendVerification(context.Background(), input.to, input.link); err == nil {
				t.Fatal("SendVerification error = nil, want invalid input rejection")
			}
		})
	}
}
