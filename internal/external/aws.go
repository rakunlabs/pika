package external

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AWS configures an AWS external resource (Secrets Manager or SSM Parameter Store).
type AWS struct {
	// Region is the AWS region (e.g., "us-east-1").
	Region string `json:"region"`
	// AccessKey is the AWS access key ID.
	AccessKey string `json:"access_key"`
	// SecretKey is the AWS secret access key.
	SecretKey string `json:"secret_key"`
	// Service is "secretsmanager" or "ssm".
	Service string `json:"service"` // "secretsmanager" or "ssm"
}

// AWSClient is a minimal HTTP client for AWS services using Sigv4 signing.
type AWSClient struct {
	region     string
	accessKey  string
	secretKey  string
	httpClient *http.Client
}

// NewAWSClient creates a new AWS client.
func NewAWSClient(region, accessKey, secretKey string) *AWSClient {
	return &AWSClient{
		region:    region,
		accessKey: accessKey,
		secretKey: secretKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ReadSecretsManagerSecret reads a secret from AWS Secrets Manager.
func (a *AWSClient) ReadSecretsManagerSecret(ctx context.Context, secretName string) (map[string]any, error) {
	payload, _ := json.Marshal(map[string]string{
		"SecretId": secretName,
	})

	body, err := a.doAWSRequest(ctx, "secretsmanager", "secretsmanager.GetSecretValue", payload)
	if err != nil {
		return nil, fmt.Errorf("aws secretsmanager: %w", err)
	}

	var resp struct {
		SecretString string `json:"SecretString"`
		Name         string `json:"Name"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws secretsmanager: parsing response: %w", err)
	}

	// Try to parse SecretString as JSON
	var data map[string]any
	if err := json.Unmarshal([]byte(resp.SecretString), &data); err == nil {
		return data, nil
	}

	return map[string]any{"value": resp.SecretString}, nil
}

// ReadSSMParameter reads a parameter from AWS Systems Manager Parameter Store.
func (a *AWSClient) ReadSSMParameter(ctx context.Context, paramName string) (map[string]any, error) {
	payload, _ := json.Marshal(map[string]any{
		"Name":           paramName,
		"WithDecryption": true,
	})

	body, err := a.doAWSRequest(ctx, "ssm", "AmazonSSM.GetParameter", payload)
	if err != nil {
		return nil, fmt.Errorf("aws ssm: %w", err)
	}

	var resp struct {
		Parameter struct {
			Value string `json:"Value"`
			Name  string `json:"Name"`
			Type  string `json:"Type"`
		} `json:"Parameter"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws ssm: parsing response: %w", err)
	}

	// Try to parse Value as JSON
	var data map[string]any
	if err := json.Unmarshal([]byte(resp.Parameter.Value), &data); err == nil {
		return data, nil
	}

	return map[string]any{"value": resp.Parameter.Value}, nil
}

// ListSecretsManagerSecrets lists secrets in AWS Secrets Manager.
func (a *AWSClient) ListSecretsManagerSecrets(ctx context.Context) ([]string, error) {
	payload := []byte("{}")

	body, err := a.doAWSRequest(ctx, "secretsmanager", "secretsmanager.ListSecrets", payload)
	if err != nil {
		return nil, fmt.Errorf("aws secretsmanager: %w", err)
	}

	var resp struct {
		SecretList []struct {
			Name string `json:"Name"`
		} `json:"SecretList"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws secretsmanager: parsing list response: %w", err)
	}

	names := make([]string, 0, len(resp.SecretList))
	for _, s := range resp.SecretList {
		names = append(names, s.Name)
	}
	return names, nil
}

// ListSSMParameters lists parameters in AWS SSM Parameter Store.
func (a *AWSClient) ListSSMParameters(ctx context.Context, prefix string) ([]string, error) {
	reqBody := map[string]any{}
	if prefix != "" {
		reqBody["ParameterFilters"] = []map[string]any{
			{
				"Key":    "Name",
				"Option": "BeginsWith",
				"Values": []string{prefix},
			},
		}
	}
	payload, _ := json.Marshal(reqBody)

	body, err := a.doAWSRequest(ctx, "ssm", "AmazonSSM.DescribeParameters", payload)
	if err != nil {
		return nil, fmt.Errorf("aws ssm: %w", err)
	}

	var resp struct {
		Parameters []struct {
			Name string `json:"Name"`
		} `json:"Parameters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws ssm: parsing list response: %w", err)
	}

	names := make([]string, 0, len(resp.Parameters))
	for _, p := range resp.Parameters {
		rel := strings.TrimPrefix(p.Name, prefix)
		names = append(names, rel)
	}
	return names, nil
}

// doAWSRequest performs a signed request to an AWS service.
func (a *AWSClient) doAWSRequest(ctx context.Context, service, target string, payload []byte) ([]byte, error) {
	endpoint := fmt.Sprintf("https://%s.%s.amazonaws.com/", service, a.region)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)

	a.signRequest(req, service, payload)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// signRequest adds AWS Sigv4 authorization headers to the request.
func (a *AWSClient) signRequest(req *http.Request, service string, payload []byte) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", amzDate)

	// Ensure Host header is set
	if req.Header.Get("Host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}

	// Step 1: Create canonical request
	payloadHash := sha256Hex(payload)

	// Canonical headers (sorted by lowercase header name)
	signedHeaderNames := []string{"content-type", "host", "x-amz-date", "x-amz-target"}
	sort.Strings(signedHeaderNames)

	var canonicalHeaders strings.Builder
	for _, h := range signedHeaderNames {
		var val string
		if h == "host" {
			val = req.URL.Host
		} else {
			val = req.Header.Get(http.CanonicalHeaderKey(h))
		}
		canonicalHeaders.WriteString(h + ":" + strings.TrimSpace(val) + "\n")
	}

	signedHeaders := strings.Join(signedHeaderNames, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		"/",
		"", // no query string
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	// Step 2: Create string to sign
	credentialScope := dateStamp + "/" + a.region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	// Step 3: Derive signing key
	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256([]byte("AWS4"+a.secretKey), []byte(dateStamp)),
				[]byte(a.region),
			),
			[]byte(service),
		),
		[]byte("aws4_request"),
	)

	// Step 4: Calculate signature
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Step 5: Add Authorization header
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		a.accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
