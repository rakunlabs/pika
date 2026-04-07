package ftpserve

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/pika/internal/service"
	ftpserver "goftp.io/server/v2"
)

// Server wraps the goftp server, driver, and auth.
type Server struct {
	ftpSrv *ftpserver.Server
	driver *Driver
	auth   *MultiUserAuth
}

// NewServer creates a new FTP server with the given config, shares, and users.
func NewServer(cfg *service.FTPServeSettings, shares []Share, users []User) (*Server, error) {
	auth := NewMultiUserAuth(users)
	driver := NewDriver(shares, auth)

	port := cfg.Port
	if port == 0 {
		port = 2121
	}

	passivePorts := cfg.PassivePorts
	if passivePorts == "" {
		passivePorts = "30000-30100"
	}

	// Default to IPv4 all-interfaces. The goftp library defaults to "::" (IPv6)
	// which causes passive mode data connections to fail on systems where the
	// dual-stack passive listener doesn't accept IPv4 connections.
	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}

	opts := &ftpserver.Options{
		Driver:       driver,
		Auth:         auth,
		Perm:         ftpserver.NewSimplePerm("pika", "pika"),
		Port:         port,
		Hostname:     host,
		PublicIP:     cfg.PublicIP,
		PassivePorts: passivePorts,
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

// Stop gracefully shuts down the FTP server.
func (s *Server) Stop() {
	slog.Info("stopping FTP server")
	s.ftpSrv.Shutdown()
}

// UpdateShares replaces the shares served by the FTP server.
func (s *Server) UpdateShares(shares []Share) {
	s.driver.UpdateShares(shares)
}

// UpdateUsers replaces the user list for FTP auth.
func (s *Server) UpdateUsers(users []User) {
	s.auth.UpdateUsers(users)
}
