package servertls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager owns the active server certificate used by the main admin
// listener and HTTPS-enabled Endpoints. The certificate lives on disk
// so the server can present HTTPS before the settings DB is unlocked.
type Manager struct {
	certFile       string
	keyFile        string
	defaultNames   []string
	processEnabled bool

	mu   sync.RWMutex
	cert *tls.Certificate
	leaf *x509.Certificate
}

type Options struct {
	CertFile       string
	KeyFile        string
	DefaultNames   []string
	ProcessEnabled bool
}

type SelfSignedRequest struct {
	CommonName  string   `json:"common_name,omitempty"`
	DNSNames    []string `json:"dns_names,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
	ValidDays   int      `json:"valid_days,omitempty"`
}

type CertificateStatus struct {
	Loaded            bool      `json:"loaded"`
	CertFile          string    `json:"cert_file"`
	KeyFile           string    `json:"key_file"`
	Subject           string    `json:"subject,omitempty"`
	Issuer            string    `json:"issuer,omitempty"`
	DNSNames          []string  `json:"dns_names,omitempty"`
	IPAddresses       []string  `json:"ip_addresses,omitempty"`
	NotBefore         time.Time `json:"not_before,omitempty"`
	NotAfter          time.Time `json:"not_after,omitempty"`
	DaysRemaining     int       `json:"days_remaining"`
	FingerprintSHA256 string    `json:"fingerprint_sha256,omitempty"`
	SelfSigned        bool      `json:"self_signed"`
}

func New(opts Options) *Manager {
	return &Manager{
		certFile:       opts.CertFile,
		keyFile:        opts.KeyFile,
		defaultNames:   append([]string(nil), opts.DefaultNames...),
		processEnabled: opts.ProcessEnabled,
	}
}

func (m *Manager) ProcessEnabled() bool {
	return m != nil && m.processEnabled
}

func (m *Manager) CertFile() string {
	if m == nil {
		return ""
	}
	return m.certFile
}

func (m *Manager) KeyFile() string {
	if m == nil {
		return ""
	}
	return m.keyFile
}

// EnsureSelfSigned loads an existing certificate or creates a default
// self-signed one when the cert/key files are not present yet.
func (m *Manager) EnsureSelfSigned() error {
	if m == nil {
		return errors.New("tls manager is not configured")
	}
	if err := m.Load(); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := m.GenerateSelfSigned(SelfSignedRequest{})
	return err
}

func (m *Manager) TLSConfig() (*tls.Config, error) {
	if err := m.EnsureSelfSigned(); err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return m.currentCertificate()
		},
	}, nil
}

func (m *Manager) currentCertificate() (*tls.Certificate, error) {
	m.mu.RLock()
	cert := m.cert
	m.mu.RUnlock()
	if cert != nil {
		return cert, nil
	}
	if err := m.Load(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil {
		return nil, errors.New("tls certificate is not loaded")
	}
	return m.cert, nil
}

func (m *Manager) Load() error {
	if m == nil {
		return errors.New("tls manager is not configured")
	}
	certPEM, err := os.ReadFile(m.certFile)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(m.keyFile)
	if err != nil {
		return err
	}
	cert, leaf, err := parsePair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	m.set(cert, leaf)
	return nil
}

func (m *Manager) Status() (CertificateStatus, error) {
	st := CertificateStatus{CertFile: m.CertFile(), KeyFile: m.KeyFile()}
	if m == nil {
		return st, nil
	}
	if err := m.Load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, err
	}
	m.mu.RLock()
	leaf := m.leaf
	m.mu.RUnlock()
	if leaf == nil {
		return st, nil
	}
	return statusFromCert(st, leaf), nil
}

func (m *Manager) GenerateSelfSigned(req SelfSignedRequest) (CertificateStatus, error) {
	if m == nil {
		return CertificateStatus{}, errors.New("tls manager is not configured")
	}
	certPEM, keyPEM, err := GenerateSelfSignedPEM(req, m.defaultNames)
	if err != nil {
		return CertificateStatus{}, err
	}
	return m.WriteManual(certPEM, keyPEM)
}

func (m *Manager) WriteManual(certPEM, keyPEM []byte) (CertificateStatus, error) {
	if m == nil {
		return CertificateStatus{}, errors.New("tls manager is not configured")
	}
	cert, leaf, err := parsePair(certPEM, keyPEM)
	if err != nil {
		return CertificateStatus{}, err
	}
	if err := writePEMFile(m.certFile, certPEM, 0o644); err != nil {
		return CertificateStatus{}, err
	}
	if err := writePEMFile(m.keyFile, keyPEM, 0o600); err != nil {
		return CertificateStatus{}, err
	}
	m.set(cert, leaf)
	return statusFromCert(CertificateStatus{Loaded: true, CertFile: m.certFile, KeyFile: m.keyFile}, leaf), nil
}

func (m *Manager) set(cert *tls.Certificate, leaf *x509.Certificate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cert = cert
	m.leaf = leaf
}

func GenerateSelfSignedPEM(req SelfSignedRequest, defaultNames []string) ([]byte, []byte, error) {
	if req.ValidDays <= 0 {
		req.ValidDays = 3650
	}
	dnsNames, ips := classifyNames(append(defaultNames, append(req.DNSNames, req.IPAddresses...)...))
	if req.CommonName == "" {
		if len(dnsNames) > 0 {
			req.CommonName = dnsNames[0]
		} else if len(ips) > 0 {
			req.CommonName = ips[0].String()
		} else {
			req.CommonName = "pika"
		}
	}
	if ip := net.ParseIP(req.CommonName); ip != nil {
		ips = appendUniqueIP(ips, ip)
	} else {
		dnsNames = appendUniqueString(dnsNames, req.CommonName)
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating private key: %w", err)
	}
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial number: %w", err)
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   req.CommonName,
			Organization: []string{"Pika Self-Signed"},
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.AddDate(0, 0, req.ValidDays),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling private key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func parsePair(certPEM, keyPEM []byte) (*tls.Certificate, *x509.Certificate, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificate pair: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, nil, errors.New("certificate pair contains no certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("parse leaf certificate: %w", err)
	}
	cert.Leaf = leaf
	return &cert, leaf, nil
}

func writePEMFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create tls directory: %w", err)
	}
	return os.WriteFile(path, data, perm)
}

func statusFromCert(st CertificateStatus, cert *x509.Certificate) CertificateStatus {
	st.Loaded = true
	st.Subject = cert.Subject.String()
	st.Issuer = cert.Issuer.String()
	st.DNSNames = append([]string(nil), cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		st.IPAddresses = append(st.IPAddresses, ip.String())
	}
	st.NotBefore = cert.NotBefore
	st.NotAfter = cert.NotAfter
	st.DaysRemaining = int(time.Until(cert.NotAfter).Hours() / 24)
	if time.Now().After(cert.NotAfter) {
		st.DaysRemaining = -int(time.Since(cert.NotAfter).Hours()/24) - 1
	}
	fp := sha256.Sum256(cert.Raw)
	st.FingerprintSHA256 = strings.ToUpper(hex.EncodeToString(fp[:]))
	st.SelfSigned = cert.CheckSignatureFrom(cert) == nil && cert.Subject.String() == cert.Issuer.String()
	return st
}

func classifyNames(names []string) ([]string, []net.IP) {
	dns := []string{"localhost"}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == "0.0.0.0" || name == "::" {
			continue
		}
		if host, _, err := net.SplitHostPort(name); err == nil {
			name = host
		}
		if ip := net.ParseIP(name); ip != nil {
			ips = appendUniqueIP(ips, ip)
			continue
		}
		dns = appendUniqueString(dns, name)
	}
	return dns, ips
}

func appendUniqueString(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func appendUniqueIP(items []net.IP, value net.IP) []net.IP {
	if value == nil {
		return items
	}
	for _, item := range items {
		if item.Equal(value) {
			return items
		}
	}
	return append(items, value)
}
