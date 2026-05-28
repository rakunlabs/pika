package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/server/servertls"
	"github.com/rakunlabs/pika/internal/service"
)

// tlsGenRequest is the optional JSON body for POST /api/v1/tls-generate.
type tlsGenRequest struct {
	// CommonName for the certificate subject. Default: "pika".
	CommonName  string   `json:"common_name,omitempty"`
	DNSNames    []string `json:"dns_names,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
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

	certPEM, keyPEM, err := servertls.GenerateSelfSignedPEM(servertls.SelfSignedRequest{
		CommonName:  req.CommonName,
		DNSNames:    req.DNSNames,
		IPAddresses: req.IPAddresses,
		ValidDays:   req.ValidDays,
	}, nil)
	if err != nil {
		return fmt.Errorf("generating TLS certificate: %w", err)
	}

	return c.SetStatus(http.StatusOK).SendJSON(tlsGenResponse{
		CertPEM: string(certPEM),
		KeyPEM:  string(keyPEM),
	})
}

type tlsStatusResponse struct {
	ProcessEnabled   bool                        `json:"process_enabled"`
	HTTPSEnabled     bool                        `json:"https_enabled"`
	PlainHTTPEnabled bool                        `json:"plain_http_enabled"`
	Certificate      servertls.CertificateStatus `json:"certificate"`
}

func (a *api) getTLSStatus(c *ada.Context) error {
	var cert servertls.CertificateStatus
	processEnabled := false
	if a.tlsMgr != nil {
		processEnabled = a.tlsMgr.ProcessEnabled()
		st, err := a.tlsMgr.Status()
		if err != nil {
			return fmt.Errorf("read TLS certificate status: %w", err)
		}
		cert = st
	}
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}
	policy := service.EffectiveServerTLSSettings(nil)
	if settings != nil {
		policy = service.EffectiveServerTLSSettings(settings.ServerTLS)
	}
	return c.SetStatus(http.StatusOK).SendJSON(tlsStatusResponse{
		ProcessEnabled:   processEnabled,
		HTTPSEnabled:     processEnabled && policy.HTTPSEnabled(),
		PlainHTTPEnabled: !processEnabled || policy.PlainHTTPEnabled,
		Certificate:      cert,
	})
}

func (a *api) generateManagedTLS(c *ada.Context) error {
	if a.tlsMgr == nil {
		return fmt.Errorf("TLS manager is not configured: %w", service.ErrInternal)
	}
	var req servertls.SelfSignedRequest
	_ = c.Bind(&req)
	st, err := a.tlsMgr.GenerateSelfSigned(req)
	if err != nil {
		return fmt.Errorf("generate managed TLS certificate: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(st)
}

type tlsManualRequest struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

func (a *api) uploadManagedTLS(c *ada.Context) error {
	if a.tlsMgr == nil {
		return fmt.Errorf("TLS manager is not configured: %w", service.ErrInternal)
	}
	var req tlsManualRequest
	if err := c.Bind(&req); err != nil {
		return fmt.Errorf("bind TLS certificate upload: %w: %w", err, service.ErrBadRequest)
	}
	if req.CertPEM == "" || req.KeyPEM == "" {
		return fmt.Errorf("cert_pem and key_pem are required: %w", service.ErrBadRequest)
	}
	st, err := a.tlsMgr.WriteManual([]byte(req.CertPEM), []byte(req.KeyPEM))
	if err != nil {
		return fmt.Errorf("store managed TLS certificate: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(st)
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
