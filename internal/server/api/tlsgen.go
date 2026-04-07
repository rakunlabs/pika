package api

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/rakunlabs/ada"
)

// tlsGenRequest is the optional JSON body for POST /api/v1/tls-generate.
type tlsGenRequest struct {
	// CommonName for the certificate subject. Default: "pika".
	CommonName string `json:"common_name,omitempty"`
	// ValidDays is the certificate validity in days. Default: 3650 (10 years).
	ValidDays int `json:"valid_days,omitempty"`
}

// tlsGenResponse contains the generated PEM-encoded certificate and private key.
type tlsGenResponse struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// generateTLS generates a self-signed ECDSA (P-256) certificate and returns
// both the certificate and private key as PEM strings.
func (a *api) generateTLS(c *ada.Context) error {
	var req tlsGenRequest
	// Allow empty body — use defaults
	_ = c.Bind(&req)

	if req.CommonName == "" {
		req.CommonName = "pika"
	}
	if req.ValidDays <= 0 {
		req.ValidDays = 3650
	}

	certPEM, keyPEM, err := generateSelfSignedCert(req.CommonName, req.ValidDays)
	if err != nil {
		return fmt.Errorf("generating TLS certificate: %w", err)
	}

	return c.SetStatus(http.StatusOK).SendJSON(tlsGenResponse{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	})
}

// generateSelfSignedCert creates a self-signed ECDSA P-256 certificate.
func generateSelfSignedCert(commonName string, validDays int) (certPEM, keyPEM string, err error) {
	// Generate ECDSA P-256 private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generating private key: %w", err)
	}

	// Serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generating serial number: %w", err)
	}

	// Certificate template
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Pika Self-Signed"},
		},
		NotBefore:             now,
		NotAfter:              now.AddDate(0, 0, validDays),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Self-sign
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", fmt.Errorf("creating certificate: %w", err)
	}

	// Encode certificate PEM
	certBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key PEM
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshaling private key: %w", err)
	}
	keyBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	return string(certBlock), string(keyBlock), nil
}

// sshKeyGenResponse contains the generated PEM-encoded SSH private key.
type sshKeyGenResponse struct {
	KeyPEM string `json:"key_pem"`
}

// generateSSHKey generates an Ed25519 private key and returns it as PEM.
func (a *api) generateSSHKey(c *ada.Context) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating ed25519 key: %w", err)
	}

	marshaled, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshaling private key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: marshaled,
	})

	return c.SetStatus(http.StatusOK).SendJSON(sshKeyGenResponse{
		KeyPEM: string(keyPEM),
	})
}
