package ftpserve

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/rakunlabs/pika/internal/service"
)

// Server wraps the ftpserverlib server.
type Server struct {
	ftpSrv *ftpserver.FtpServer
	drv    *mainDriver
}

// mainDriver implements ftpserver.MainDriver.
type mainDriver struct {
	mu       sync.RWMutex
	shares   []Share
	auth     *MultiUserAuth
	settings *ftpserver.Settings
}

var _ ftpserver.MainDriver = (*mainDriver)(nil)

func (d *mainDriver) GetSettings() (*ftpserver.Settings, error) {
	return d.settings, nil
}

func (d *mainDriver) ClientConnected(cc ftpserver.ClientContext) (string, error) {
	slog.Debug("FTP client connected", "id", cc.ID(), "remote", cc.RemoteAddr())
	return "Welcome to Pika FTP", nil
}

func (d *mainDriver) ClientDisconnected(cc ftpserver.ClientContext) {
	slog.Debug("FTP client disconnected", "id", cc.ID())
}

func (d *mainDriver) AuthUser(cc ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	u := d.auth.Authenticate(user, pass)
	if u == nil {
		return nil, fmt.Errorf("authentication failed")
	}

	return &clientFS{drv: d, user: u}, nil
}

func (d *mainDriver) GetTLSConfig() (*tls.Config, error) {
	return nil, nil
}

// NewServer creates a new FTP server with the given config, shares, and users.
func NewServer(cfg *service.FTPServeSettings, shares []Share, users []User) (*Server, error) {
	auth := NewMultiUserAuth(users)

	port := cfg.Port
	if port == 0 {
		port = 2121
	}

	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}

	settings := &ftpserver.Settings{
		ListenAddr: host + ":" + strconv.Itoa(port),
		PublicHost: cfg.PublicIP,
	}

	passivePorts := cfg.PassivePorts
	if passivePorts == "" {
		passivePorts = "30000-30100"
	}

	if pr, err := parsePortRange(passivePorts); err == nil {
		settings.PassiveTransferPortRange = pr
	}

	drv := &mainDriver{
		shares:   shares,
		auth:     auth,
		settings: settings,
	}

	ftpSrv := ftpserver.NewFtpServer(drv)
	ftpSrv.Logger = slog.Default().With("component", "ftp")

	return &Server{
		ftpSrv: ftpSrv,
		drv:    drv,
	}, nil
}

// Start starts the FTP server in a goroutine.
func (s *Server) Start(ctx context.Context) {
	go func() {
		slog.Info("starting FTP server", "addr", s.drv.settings.ListenAddr)
		if err := s.ftpSrv.ListenAndServe(); err != nil {
			slog.Error("FTP server failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down FTP server")
		s.ftpSrv.Stop() //nolint:errcheck
	}()
}

// Stop gracefully shuts down the FTP server.
func (s *Server) Stop() {
	slog.Info("stopping FTP server")
	s.ftpSrv.Stop() //nolint:errcheck
}

// UpdateShares replaces the shares served by the FTP server.
func (s *Server) UpdateShares(shares []Share) {
	s.drv.mu.Lock()
	s.drv.shares = shares
	s.drv.mu.Unlock()
}

// UpdateUsers replaces the user list for FTP auth.
func (s *Server) UpdateUsers(users []User) {
	s.drv.auth.UpdateUsers(users)
}

func parsePortRange(s string) (*ftpserver.PortRange, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid port range: %s", s)
	}

	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil, fmt.Errorf("invalid port range start: %w", err)
	}

	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, fmt.Errorf("invalid port range end: %w", err)
	}

	return &ftpserver.PortRange{Start: start, End: end}, nil
}
