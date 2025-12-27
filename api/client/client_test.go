package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

// Test that BearerTokenAuth sets the Authorization header when a token is provided.
func TestBearerTokenAuth_Apply_SetsHeader(t *testing.T) {
	auth := BearerTokenAuth{Token: "abc123"}
	r := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := auth.Apply(r); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer abc123" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer abc123")
	}
}

// Test that BearerTokenAuth.Apply is a no-op when token is empty.
func TestBearerTokenAuth_Apply_EmptyToken_NoHeader(t *testing.T) {
	auth := BearerTokenAuth{Token: ""}
	r := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := auth.Apply(r); err != nil { // should still not error
		t.Fatalf("Apply() error = %v", err)
	}
	if got := r.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization header = %q, want empty", got)
	}
}

// Test the integration via Client.Do that the Authorization header is sent to the server.
func TestClient_Do_BearerAuthHeaderSent(t *testing.T) {
	// Setup a test server that asserts the header and returns JSON
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthorized","message":"missing or wrong token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	// Build client with base URL pointing at the test server and a bearer token.
	rhc := retryablehttp.NewClient()
	rhc.RetryMax = 0 // deterministic tests
	c := NewClient(
		WithBaseURL(ts.URL),
		WithHTTPClient(rhc),
		WithAuthenticator(BearerTokenAuth{Token: "test-token"}),
	)

	var out struct {
		Ok bool `json:"ok"`
	}
	resp, err := c.Get(context.Background(), "/health", nil, &out)
	if err != nil {
		// If server returned non-2xx we still get an error from Do. Fail the test with more info.
		t.Fatalf("Do() got error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !out.Ok {
		t.Fatalf("response body not decoded or unexpected: %+v", out)
	}
}

// Negative test: wrong or missing token results in non-2xx and proper error handling path.
func TestClient_Do_BearerAuthHeaderMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthorized","message":"no token"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	rhc := retryablehttp.NewClient()
	rhc.RetryMax = 0
	c := NewClient(
		WithBaseURL(ts.URL),
		WithHTTPClient(rhc),
		// Note: no authenticator -> header will be missing.
	)

	var out any
	resp, err := c.Get(context.Background(), "/whatever", nil, &out)
	if err == nil {
		// We expect an error because server returns 401 with JSON error body
		t.Fatalf("expected error for missing auth header, got nil (status %d)", resp.StatusCode)
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got resp=%v", resp)
	}
}
