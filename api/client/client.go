package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

// DefaultBaseURL is the default Capella API endpoint for v4.
// Example: https://api.cloud.couchbase.com
const DefaultBaseURL = "https://cloudapi.cloud.couchbase.com"

// Authenticator applies authentication to an outgoing HTTP request.
// This allows supporting different Capella auth mechanisms (Bearer tokens or API keys).
// Implementations provided below: BearerTokenAuth and APIKeySecretAuth.
type Authenticator interface {
	Apply(req *http.Request) error
}

// BearerTokenAuth adds an Authorization: Bearer <token> header.
type BearerTokenAuth struct {
	Token string
}

func (a BearerTokenAuth) Apply(req *http.Request) error {
	if a.Token == "" {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	return nil
}

// APIKeySecretAuth adds API key headers for Capella if using key/secret credentials.
// Header names can vary; by default we use common Capella naming if known to the user.
// You can override HeaderKeyName/HeaderSecretName to match your environment.
type APIKeySecretAuth struct {
	Key              string
	Secret           string
	HeaderKeyName    string // e.g., "X-Client-Id" or "X-Couchbase-API-Key"
	HeaderSecretName string // e.g., "X-Client-Secret" or "X-Couchbase-API-Secret"
}

func (a APIKeySecretAuth) Apply(req *http.Request) error {
	if a.Key == "" && a.Secret == "" {
		return nil
	}
	keyHeader := a.HeaderKeyName
	if keyHeader == "" {
		keyHeader = "X-Client-Id"
	}
	secretHeader := a.HeaderSecretName
	if secretHeader == "" {
		secretHeader = "X-Client-Secret"
	}
	if a.Key != "" {
		req.Header.Set(keyHeader, a.Key)
	}
	if a.Secret != "" {
		req.Header.Set(secretHeader, a.Secret)
	}
	return nil
}

// Client is a minimal Capella v4 API client suitable for Terraform providers.
// It supports context-aware requests, retries with backoff, simple auth, and JSON encode/decode.
type Client struct {
	BaseURL   *url.URL
	HTTP      *retryablehttp.Client
	Auth      Authenticator
	UserAgent string
	// Optional: an organization or project can be tracked by the provider side if needed
	OrganizationID string
	ProjectID      string
}

// Option mutates client options during construction.
type Option func(*Client)

// WithBaseURL sets the API base URL.
func WithBaseURL(raw string) Option {
	return func(c *Client) {
		if raw == "" {
			return
		}
		if !strings.HasPrefix(raw, "http") {
			raw = "https://" + raw
		}
		if u, err := url.Parse(raw); err == nil {
			c.BaseURL = u
		}
	}
}

// WithHTTPClient allows providing a custom retryablehttp.Client.
func WithHTTPClient(rhc *retryablehttp.Client) Option {
	return func(c *Client) { c.HTTP = rhc }
}

// WithAuthenticator sets the request authenticator.
func WithAuthenticator(a Authenticator) Option {
	return func(c *Client) { c.Auth = a }
}

// WithUserAgent sets a custom User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.UserAgent = ua }
}

// WithOrgID sets a default organization ID on the client (optional convenience).
func WithOrgID(id string) Option { return func(c *Client) { c.OrganizationID = id } }

// WithProjectID sets a default project ID on the client (optional convenience).
func WithProjectID(id string) Option { return func(c *Client) { c.ProjectID = id } }

// NewClient creates a new Capella v4 API client.
// The client uses retryablehttp with sensible defaults for Terraform providers.
func NewClient(opts ...Option) *Client {
	base, _ := url.Parse(DefaultBaseURL)

	rhc := retryablehttp.NewClient()
	rhc.RetryMax = 4
	rhc.RetryWaitMin = 500 * time.Millisecond
	rhc.RetryWaitMax = 4 * time.Second
	rhc.Backoff = retryablehttp.DefaultBackoff
	rhc.Logger = nil // do not spam logs; provider can log around the client

	c := &Client{
		BaseURL:   base,
		HTTP:      rhc,
		UserAgent: "capellaextras-terraform-provider/unknown (+https://github.com/cdsre/terraform-provider-capellaextras)",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// apiError models a common API error payload. Capella uses standard patterns.
type apiError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Detail  any    `json:"detail,omitempty"`
}

func (e apiError) Error() string {
	if e.Code == "" && e.Message == "" {
		return "capella api error"
	}
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Do performs an HTTP request against the Capella API. Path may be absolute or relative.
// If body is non-nil, it is JSON-encoded. If out is non-nil, the response JSON will be decoded into it.
func (c *Client) Do(ctx context.Context, method, p string, query map[string]string, body any, out any) (*http.Response, error) {
	if c.BaseURL == nil {
		return nil, fmt.Errorf("client BaseURL not configured")
	}
	// Build URL
	var u *url.URL
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		pu, err := url.Parse(p)
		if err != nil {
			return nil, err
		}
		u = pu
	} else {
		// ensure path join doesn't drop starting segment
		clean := path.Clean("/" + strings.TrimSpace(p))
		u = c.BaseURL.ResolveReference(&url.URL{Path: clean})
	}
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	// Encode body if present
	var reqBody io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return nil, err
		}
		reqBody = buf
	}

	req, err := retryablehttp.NewRequest(method, u.String(), reqBody)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	// Set headers
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	// Apply auth
	if c.Auth != nil {
		// Apply auth to the underlying http.Request
		if err := c.Auth.Apply(req.Request); err != nil {
			return nil, err
		}
	}

	// Execute
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() {
		// drain body on caller decode error responsibility; otherwise we close here when out is nil
		if out == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			err = resp.Body.Close()
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// try to decode error
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var ae apiError
		if json.Unmarshal(b, &ae) == nil && (ae.Code != "" || ae.Message != "") {
			return resp, fmt.Errorf("%w (status %d)", ae, resp.StatusCode)
		}
		return resp, fmt.Errorf("capella api request failed: status %d, body: %s", resp.StatusCode, string(b))
	}

	if out != nil {
		dec := json.NewDecoder(resp.Body)
		dec.DisallowUnknownFields()
		err = dec.Decode(out)
		_ = resp.Body.Close()
		if err == io.EOF {
			return resp, nil
		}
		if err != nil {
			return resp, err
		}
	}
	return resp, nil
}

// Convenience helpers.
func (c *Client) Get(ctx context.Context, p string, query map[string]string, out any) (*http.Response, error) {
	return c.Do(ctx, http.MethodGet, p, query, nil, out)
}

func (c *Client) Post(ctx context.Context, p string, body any, out any) (*http.Response, error) {
	return c.Do(ctx, http.MethodPost, p, nil, body, out)
}

func (c *Client) Put(ctx context.Context, p string, body any, out any) (*http.Response, error) {
	return c.Do(ctx, http.MethodPut, p, nil, body, out)
}

func (c *Client) Patch(ctx context.Context, p string, body any, out any) (*http.Response, error) {
	return c.Do(ctx, http.MethodPatch, p, nil, body, out)
}

func (c *Client) Delete(ctx context.Context, p string) (*http.Response, error) {
	return c.Do(ctx, http.MethodDelete, p, nil, nil, nil)
}
