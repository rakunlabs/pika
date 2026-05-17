package external

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/ok"
)

// HTTPProvider serves config fetched from a plain HTTP endpoint.
//
// Why this lives in the external package: the rest of the backends had
// their own *Client type already; HTTP was the only one piggy-backing
// directly on ok.Config from the service layer. Adopting the same
// Provider shape here closes that gap and gives us one mental model
// across all eight backends.
//
// HTTP is the odd one out for List(): there's no generic "enumerate
// keys" semantic for arbitrary endpoints. We honour the Provider
// contract by returning an empty slice; the SPA's path browser hides
// itself when List returns nothing and Test branches on Kind() == "http"
// to render the response body preview instead of a path sample.
type HTTPProvider struct {
	Config *ok.Config
}

func (p *HTTPProvider) Kind() string { return "http" }

func (p *HTTPProvider) Capabilities() Capabilities {
	// HTTP endpoints have no generic enumeration / write / delete /
	// version concept. Read is implemented by fetching the configured
	// URL and returning its body. That's all the browser can do.
	return Capabilities{CanRead: true}
}

func (p *HTTPProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("http: config is required")
	}
	if strings.TrimSpace(p.Config.BaseURL) == "" {
		return fmt.Errorf("http: base_url is required")
	}
	return nil
}

func (p *HTTPProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client, err := p.Config.New()
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}

	endpoint := "/"
	if path != "" {
		endpoint = "/" + strings.TrimLeft(path, "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	var body []byte
	if err := client.Do(req, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP request returned status %d", resp.StatusCode)
		}
		var readErr error
		body, readErr = io.ReadAll(resp.Body)
		return readErr
	}); err != nil {
		return nil, fmt.Errorf("fetching HTTP config: %w", err)
	}

	return body, nil
}

// List has no meaning for arbitrary HTTP endpoints — callers should
// branch on Kind() before exposing a path browser. Returning an empty
// slice keeps the dispatcher uniform.
func (p *HTTPProvider) List(ctx context.Context, prefix string) ([]string, error) {
	return []string{}, nil
}

// Read issues a GET to the configured base URL (optionally with the
// supplied path appended) and returns the body wrapped as an Entry.
// We don't try to parse the body as JSON here — the browser renders
// it as text and offers a "treat as JSON" toggle.
func (p *HTTPProvider) Read(ctx context.Context, path string) (*Entry, error) {
	body, err := p.Fetch(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Entry{
		Data:        map[string]any{"body": string(body)},
		Raw:         body,
		ContentType: "text/plain",
	}, nil
}

// Write is not supported on plain HTTP endpoints.
func (p *HTTPProvider) Write(ctx context.Context, path string, data map[string]any) error {
	return fmt.Errorf("http: %w", ErrNotSupported)
}

// Delete is not supported on plain HTTP endpoints.
func (p *HTTPProvider) Delete(ctx context.Context, path string) error {
	return fmt.Errorf("http: %w", ErrNotSupported)
}

// ListVersions is not supported.
func (p *HTTPProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	return nil, fmt.Errorf("http: %w", ErrNotSupported)
}

// ReadVersion is not supported.
func (p *HTTPProvider) ReadVersion(ctx context.Context, path string, version string) (*Entry, error) {
	return nil, fmt.Errorf("http: %w", ErrNotSupported)
}

func (p *HTTPProvider) Test(ctx context.Context) TestResult {
	body, err := p.Fetch(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	const maxPreview = 200
	preview := string(body)
	if len(preview) > maxPreview {
		preview = preview[:maxPreview] + "…"
	}
	return TestResult{
		OK:      true,
		Message: fmt.Sprintf("HTTP 200, %d bytes", len(body)),
		Sample:  []string{preview},
	}
}
