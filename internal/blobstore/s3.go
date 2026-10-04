package blobstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	s3Service         = "s3"
	s3Algorithm       = "AWS4-HMAC-SHA256"
	s3UnsignedPayload = "UNSIGNED-PAYLOAD"
	s3ErrorBodyMax    = 4096
	// S3DefaultRegion is used when no region is configured; MinIO and
	// most S3-compatible servers accept it.
	S3DefaultRegion = "us-east-1"
)

// S3Config configures an S3-compatible store.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

// S3 is a minimal S3 client (PUT/GET/DELETE/HEAD with SigV4). Uploads are
// streamed with UNSIGNED-PAYLOAD so the body never needs to be buffered
// or hashed up front; use an https endpoint to protect integrity in
// transit.
type S3 struct {
	scheme, host, region, bucket, prefix string
	accessKeyID, secretAccessKey         string
	pathStyle                            bool
	client                               *http.Client
}

// NewS3 validates cfg and returns a store.
func NewS3(cfg S3Config) (*S3, error) {
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = S3DefaultRegion
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("s3 endpoint must be an absolute http(s) URL")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("s3 endpoint must not contain credentials, a path, query or fragment")
	}
	if strings.TrimSpace(cfg.Bucket) == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("s3 storage requires bucket, access key id and secret access key")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &S3{
		scheme:          u.Scheme,
		host:            u.Host,
		region:          region,
		bucket:          strings.TrimSpace(cfg.Bucket),
		prefix:          NormalizePrefix(cfg.Prefix),
		accessKeyID:     cfg.AccessKeyID,
		secretAccessKey: cfg.SecretAccessKey,
		pathStyle:       cfg.UsePathStyle,
		client:          &http.Client{Transport: transport},
	}, nil
}

// NormalizePrefix returns "" or "some/prefix/".
func NormalizePrefix(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	return p + "/"
}

func (s *S3) objectURL(key string) *url.URL {
	host, raw := s.host, "/"+key
	if s.pathStyle {
		raw = "/" + s.bucket
		if key != "" {
			raw += "/" + key
		}
	} else {
		host = s.bucket + "." + s.host
	}
	return &url.URL{Scheme: s.scheme, Host: host, Path: raw, RawPath: s3EscapePath(raw)}
}

func (s *S3) fullKey(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return s.prefix + key, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	full, err := s.fullKey(key)
	if err != nil {
		return err
	}
	if size < 0 {
		return errors.New("s3 upload requires a known size")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if size == 0 {
		r = http.NoBody
	}
	resp, err := s.do(ctx, http.MethodPut, s.objectURL(full), r, size, contentType)
	if err != nil {
		return err
	}
	return drain(resp)
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	full, err := s.fullKey(key)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(ctx, http.MethodGet, s.objectURL(full), nil, 0, "")
	if err != nil {
		var st *s3StatusError
		if errors.As(err, &st) && st.code == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return resp.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	full, err := s.fullKey(key)
	if err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, s.objectURL(full), nil, 0, "")
	if err != nil {
		var st *s3StatusError
		if errors.As(err, &st) && st.code == http.StatusNotFound {
			return nil
		}
		return err
	}
	return drain(resp)
}

// Check issues HeadBucket.
func (s *S3) Check(ctx context.Context) error {
	resp, err := s.do(ctx, http.MethodHead, s.objectURL(""), nil, 0, "")
	if err != nil {
		return err
	}
	return drain(resp)
}

type s3StatusError struct {
	code    int
	message string
}

func (e *s3StatusError) Error() string { return e.message }

func drain(resp *http.Response) error {
	defer resp.Body.Close()
	_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, s3ErrorBodyMax))
	return err
}

func (s *S3) do(ctx context.Context, method string, u *url.URL, body io.Reader, size int64, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build s3 request: %w", err)
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	payloadHash := s3UnsignedPayload
	if body == nil {
		sum := sha256.Sum256(nil)
		payloadHash = hex.EncodeToString(sum[:])
	}
	s.sign(req, payloadHash, time.Now().UTC())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 %s %s: %w", method, u.EscapedPath(), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, s3ErrorBodyMax))
		msg := fmt.Sprintf("s3 %s %s: %s", method, u.EscapedPath(), resp.Status)
		if t := strings.TrimSpace(string(detail)); t != "" {
			msg += ": " + t
		}
		return nil, &s3StatusError{code: resp.StatusCode, message: msg}
	}
	return resp, nil
}

func (s *S3) sign(req *http.Request, payloadHash string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	canonicalHeaders, signedHeaders := s3CanonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), req.URL.RawQuery,
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	scope := strings.Join([]string{dateStamp, s.region, s3Service, "aws4_request"}, "/")
	crHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{s3Algorithm, amzDate, scope, hex.EncodeToString(crHash[:])}, "\n")

	key := s3HMAC([]byte("AWS4"+s.secretAccessKey), dateStamp)
	key = s3HMAC(key, s.region)
	key = s3HMAC(key, s3Service)
	key = s3HMAC(key, "aws4_request")
	signature := hex.EncodeToString(s3HMAC(key, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s3Algorithm, s.accessKeyID, scope, signedHeaders, signature))
}

func s3CanonicalHeaders(req *http.Request) (string, string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	values := map[string]string{"host": host}
	for name, list := range req.Header {
		lower := strings.ToLower(name)
		switch lower {
		case "authorization", "user-agent", "content-length":
			continue
		}
		parts := make([]string, 0, len(list))
		for _, v := range list {
			parts = append(parts, strings.Join(strings.Fields(v), " "))
		}
		values[lower] = strings.Join(parts, ",")
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(values[n])
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

// s3EscapePath percent-encodes every byte outside the unreserved set,
// keeping '/'. url.PathEscape is not equivalent (it leaves e.g. '$').
func s3EscapePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '/' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func s3HMAC(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
