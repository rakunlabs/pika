package hook

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"
)

// httpSink sends events as HTTP requests.
type httpSink struct {
	client  *http.Client
	url     string
	method  string
	headers map[string]string
}

// NewHTTPSink creates a new HTTP webhook sink.
func NewHTTPSink(target *HTTPTarget) (Sink, error) {
	if target == nil || target.URL == "" {
		return nil, fmt.Errorf("http target url is required")
	}

	method := target.Method
	if method == "" {
		method = http.MethodPost
	}

	timeout := 30 * time.Second
	if target.Timeout != "" {
		d, err := time.ParseDuration(target.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid http timeout %q: %w", target.Timeout, err)
		}
		timeout = d
	}

	return &httpSink{
		client: &http.Client{
			Timeout: timeout,
		},
		url:     target.URL,
		method:  method,
		headers: target.Headers,
	}, nil
}

// Send performs the HTTP request with the rendered payload.
func (s *httpSink) Send(ctx context.Context, payload []byte, _ string) error {
	req, err := http.NewRequestWithContext(ctx, s.method, s.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent())
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending http request to %s: %w", s.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http target %s returned status %d", s.url, resp.StatusCode)
	}

	return nil
}

// Close is a no-op for the HTTP sink.
func (s *httpSink) Close() error {
	s.client.CloseIdleConnections()
	return nil
}
