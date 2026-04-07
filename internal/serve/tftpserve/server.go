// Package tftpserve implements a TFTP server that serves files from rawfs backends.
// TFTP has no authentication, no directory listing, and is read-only by design.
// The file path is mapped as "share_name/path/to/file".
package tftpserve

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pin/tftp/v3"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/service"
)

// Server wraps the TFTP server.
type Server struct {
	tftpSrv *tftp.Server

	mu     sync.RWMutex
	shares []ftpserve.Share
}

// NewServer creates a new TFTP server.
func NewServer(cfg *service.TFTPServeSettings, shares []ftpserve.Share) (*Server, error) {
	s := &Server{shares: shares}

	tftpSrv := tftp.NewServer(s.readHandler, s.writeHandler)
	tftpSrv.SetTimeout(5 * time.Second)

	s.tftpSrv = tftpSrv
	return s, nil
}

// Start starts the TFTP server.
func (s *Server) Start(ctx context.Context, cfg *service.TFTPServeSettings) {
	port := cfg.Port
	if port == 0 {
		port = 69
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))

	go func() {
		slog.Info("starting TFTP server", "address", addr)
		if err := s.tftpSrv.ListenAndServe(addr); err != nil {
			slog.Error("TFTP server failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down TFTP server")
		s.tftpSrv.Shutdown()
	}()
}

// Stop gracefully shuts down the TFTP server.
func (s *Server) Stop() {
	slog.Info("stopping TFTP server")
	s.tftpSrv.Shutdown()
}

// UpdateShares replaces the shares.
func (s *Server) UpdateShares(shares []ftpserve.Share) {
	s.mu.Lock()
	s.shares = shares
	s.mu.Unlock()
}

// readHandler handles TFTP read requests (RRQ).
// The filename is expected as "share_name/path/to/file".
func (s *Server) readHandler(filename string, rf io.ReaderFrom) error {
	s.mu.RLock()
	shares := s.shares
	s.mu.RUnlock()

	// Strip leading slash
	filename = strings.TrimPrefix(filename, "/")

	// Split into share name and path
	shareName, rest := splitFirst(filename)
	if shareName == "" {
		slog.Debug("TFTP read: empty filename")
		return fmt.Errorf("file not found")
	}

	// Find the share
	var share *ftpserve.Share
	for i := range shares {
		if shares[i].Name == shareName {
			share = &shares[i]
			break
		}
	}
	if share == nil {
		slog.Debug("TFTP read: unknown share", "share", shareName)
		return fmt.Errorf("file not found")
	}

	// Try to find the file in the share's sources
	for i := range share.Sources {
		src := &share.Sources[i]
		fsPath := rest
		if src.Path != "" {
			if fsPath == "" {
				fsPath = src.Path
			} else {
				fsPath = src.Path + "/" + fsPath
			}
		}

		reader, _, err := src.FS.Open(fsPath)
		if err != nil {
			continue // try next source
		}
		defer reader.Close()

		_, err = rf.ReadFrom(reader)
		if err != nil {
			slog.Debug("TFTP read: transfer error", "file", filename, "error", err)
			return err
		}

		slog.Debug("TFTP read: served", "file", filename)
		return nil
	}

	slog.Debug("TFTP read: not found in any source", "file", filename)
	return fmt.Errorf("file not found")
}

// writeHandler handles TFTP write requests (WRQ).
// TFTP writes go to the first writable source in the share.
func (s *Server) writeHandler(filename string, wt io.WriterTo) error {
	s.mu.RLock()
	shares := s.shares
	s.mu.RUnlock()

	filename = strings.TrimPrefix(filename, "/")

	shareName, rest := splitFirst(filename)
	if shareName == "" || rest == "" {
		return fmt.Errorf("invalid path")
	}

	var share *ftpserve.Share
	for i := range shares {
		if shares[i].Name == shareName {
			share = &shares[i]
			break
		}
	}
	if share == nil {
		return fmt.Errorf("file not found")
	}

	if share.ReadOnly {
		return fmt.Errorf("share is read-only")
	}

	// Use a pipe to bridge WriterTo -> io.Reader for the rawfs Write call
	pr, pw := io.Pipe()

	errCh := make(chan error, 1)

	// Find the first writable source
	for i := range share.Sources {
		src := &share.Sources[i]
		wfs, ok := src.FS.(interface {
			Write(path string, r io.Reader, size int64) error
		})
		if !ok {
			continue
		}

		fsPath := rest
		if src.Path != "" {
			fsPath = src.Path + "/" + rest
		}

		go func() {
			errCh <- wfs.Write(fsPath, pr, -1)
		}()

		if _, err := wt.WriteTo(pw); err != nil {
			pw.CloseWithError(err)
			return err
		}
		pw.Close()

		return <-errCh
	}

	pr.Close()
	return fmt.Errorf("no writable source in share")
}

func splitFirst(s string) (string, string) {
	idx := strings.IndexByte(s, '/')
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}
