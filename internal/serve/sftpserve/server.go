// Package sftpserve implements an SFTP server that bridges rawfs backends
// to serve files over SSH. It reuses the same share and user model as the FTP server.
package sftpserve

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/service"
	"golang.org/x/crypto/ssh"
)

// Server wraps the SFTP server.
type Server struct {
	listener net.Listener
	sshCfg   *ssh.ServerConfig

	mu     sync.RWMutex
	shares []ftpserve.Share
	users  []ftpserve.User
}

// NewServer creates a new SFTP server.
func NewServer(cfg *service.SFTPServeSettings, shares []ftpserve.Share, users []ftpserve.User) (*Server, error) {
	port := cfg.Port
	if port == 0 {
		port = 2222
	}

	s := &Server{
		shares: shares,
		users:  users,
	}

	sshCfg := &ssh.ServerConfig{
		PasswordCallback:  s.passwordCallback,
		PublicKeyCallback: s.publicKeyCallback,
	}

	// Load or generate host key
	hostKey, err := loadOrGenerateHostKey(cfg.HostKeyPath, cfg.HostKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("sftp serve: host key: %w", err)
	}
	sshCfg.AddHostKey(hostKey)
	s.sshCfg = sshCfg

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sftp serve: listen on %s: %w", addr, err)
	}
	s.listener = listener

	return s, nil
}

func (s *Server) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, u := range s.users {
		if u.Username == conn.User() && u.Password != "" && u.Password == string(password) {
			return &ssh.Permissions{}, nil
		}
	}

	return nil, fmt.Errorf("authentication failed")
}

func (s *Server) publicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	offered := key.Marshal()

	for _, u := range s.users {
		if u.Username != conn.User() || u.AuthorizedKeys == "" {
			continue
		}

		rest := []byte(u.AuthorizedKeys)
		for len(rest) > 0 {
			var parsed ssh.PublicKey
			var err error
			parsed, _, _, rest, err = ssh.ParseAuthorizedKey(rest)
			if err != nil {
				// Skip unparseable lines (blank lines, comments, etc.)
				// Advance past the next newline.
				if idx := bytes.IndexByte(rest, '\n'); idx >= 0 {
					rest = rest[idx+1:]
				} else {
					break
				}
				continue
			}

			if bytes.Equal(parsed.Marshal(), offered) {
				return &ssh.Permissions{}, nil
			}
		}
	}

	return nil, fmt.Errorf("authentication failed")
}

// Start starts accepting SFTP connections.
func (s *Server) Start(ctx context.Context) {
	go func() {
		slog.Info("starting SFTP server", "address", s.listener.Addr().String())
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				slog.Error("SFTP accept error", "error", err)
				continue
			}
			go s.handleConnection(ctx, conn)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down SFTP server")
		s.listener.Close()
	}()
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshCfg)
	if err != nil {
		slog.Debug("SSH handshake failed", "error", err, "remote", conn.RemoteAddr())
		return
	}
	defer sshConn.Close()

	slog.Info("SFTP client connected", "user", sshConn.User(), "remote", sshConn.RemoteAddr())

	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}

		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
					req.Reply(true, nil)
				} else {
					req.Reply(false, nil)
				}
			}
		}(requests)

		// Create per-user handler
		handler := s.newHandler(sshConn.User())

		server := sftp.NewRequestServer(channel, handler)
		if err := server.Serve(); err != nil {
			if err != io.EOF {
				slog.Debug("SFTP session ended", "error", err, "user", sshConn.User())
			}
		}
		server.Close()
	}
}

// Stop gracefully shuts down the SFTP server by closing the listener.
func (s *Server) Stop() {
	slog.Info("stopping SFTP server")
	s.listener.Close()
}

// UpdateShares replaces the shares.
func (s *Server) UpdateShares(shares []ftpserve.Share) {
	s.mu.Lock()
	s.shares = shares
	s.mu.Unlock()
}

// UpdateUsers replaces the users.
func (s *Server) UpdateUsers(users []ftpserve.User) {
	s.mu.Lock()
	s.users = users
	s.mu.Unlock()
}

// loadOrGenerateHostKey loads an SSH private key from path (PEM-encoded, any
// type supported by ssh.ParsePrivateKey: Ed25519, RSA, ECDSA), parses it from
// raw PEM content, or generates an ephemeral Ed25519 key if neither is provided.
// A persistent key avoids "host key changed" warnings for SFTP clients across
// server restarts.
// Generate one with: ssh-keygen -t ed25519 -f /path/to/host_key -N ""
func loadOrGenerateHostKey(path string, pemContent string) (ssh.Signer, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading host key %s: %w", path, err)
		}
		return ssh.ParsePrivateKey(data)
	}

	if pemContent != "" {
		// Validate PEM type before parsing to give a clear error message
		if !strings.Contains(pemContent, "PRIVATE KEY") {
			hint := "expected PEM block containing PRIVATE KEY (e.g. OPENSSH PRIVATE KEY or PRIVATE KEY)"
			if strings.Contains(pemContent, "PUBLIC KEY") {
				hint = "it looks like a public key was pasted instead of a private key"
			}
			return nil, fmt.Errorf("invalid host key PEM content: %s", hint)
		}
		return ssh.ParsePrivateKey([]byte(pemContent))
	}

	// Generate ephemeral Ed25519 key
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating host key: %w", err)
	}

	marshaled, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling host key: %w", err)
	}

	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: marshaled,
	})

	signer, err := ssh.ParsePrivateKey(pemBlock)
	if err != nil {
		return nil, fmt.Errorf("parsing generated host key: %w", err)
	}

	slog.Warn("SFTP server using ephemeral host key (set sftp_serve.host_key_path for persistence)")
	return signer, nil
}
