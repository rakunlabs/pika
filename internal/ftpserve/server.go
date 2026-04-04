package ftpserve

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/pika/internal/config"
	ftpserver "goftp.io/server/v2"
)

// Server wraps the goftp server, driver, and auth.
type Server struct {
	ftpSrv *ftpserver.Server
	driver *Driver
	auth   *MultiUserAuth
}

// NewServer creates a new FTP server with the given config, shares, and users.
func NewServer(cfg *config.FTPServeConfig, shares []Share, users []User) (*Server, error) {
	auth := NewMultiUserAuth(users)

	// If no DB users configured, fall back to config-file credentials
	if len(users) == 0 && cfg.Password != "" {
		username := cfg.Username
		if username == "" {
			username = "pika"
		}
		auth.UpdateUsers([]User{{
			Username: username,
			Password: cfg.Password,
		}})
	}

	driver := NewDriver(shares, auth)

	port := cfg.Port
	if port == 0 {
		port = 2121
	}

	opts := &ftpserver.Options{
		Driver:       driver,
		Auth:         auth,
		Port:         port,
		Hostname:     cfg.Host,
		PublicIP:     cfg.PublicIP,
		PassivePorts: cfg.PassivePorts,
		Name:         "Pika FTP",
	}

	ftpSrv, err := ftpserver.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("creating FTP server: %w", err)
	}

	return &Server{
		ftpSrv: ftpSrv,
		driver: driver,
		auth:   auth,
	}, nil
}

// Start starts the FTP server in a goroutine.
func (s *Server) Start(ctx context.Context) {
	go func() {
		slog.Info("starting FTP server", "port", s.ftpSrv.Port)
		if err := s.ftpSrv.ListenAndServe(); err != nil {
			slog.Error("FTP server failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down FTP server")
		s.ftpSrv.Shutdown()
	}()
}

// UpdateShares replaces the shares served by the FTP server.
func (s *Server) UpdateShares(shares []Share) {
	s.driver.UpdateShares(shares)
}

// UpdateUsers replaces the user list for FTP auth.
func (s *Server) UpdateUsers(users []User) {
	s.auth.UpdateUsers(users)
}
