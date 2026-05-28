package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/server/servertls"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

func newTLSManager(cfg *config.Config) *servertls.Manager {
	certFile := cfg.Server.TLS.CertFile
	keyFile := cfg.Server.TLS.KeyFile
	if certFile == "" || keyFile == "" {
		base := cfg.Storage.BW.Path
		if base == "" {
			base = bwstore.DefaultPath
		}
		if certFile == "" {
			certFile = filepath.Join(base, "tls", "server.crt")
		}
		if keyFile == "" {
			keyFile = filepath.Join(base, "tls", "server.key")
		}
	}
	names := []string{"localhost", "127.0.0.1", "::1"}
	if cfg.Server.Host != "" {
		names = append(names, cfg.Server.Host)
	}
	return servertls.New(servertls.Options{
		CertFile:       certFile,
		KeyFile:        keyFile,
		DefaultNames:   names,
		ProcessEnabled: cfg.Server.TLS.Enabled,
	})
}

func startAppServer(ctx context.Context, app *ada.Server, cfg *config.Config, svc *service.Service, tlsMgr *servertls.Manager) error {
	addr := net.JoinHostPort(cfg.Server.Host, cfg.Server.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("address cannot listen %s: %w", addr, err)
	}

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return context.WithValue(context.Background(), ada.ListenerAddrContextKey, ln.Addr())
		},
		Protocols: protocols,
		Handler:   app.Mux,
	}

	serveLn := ln
	policyFn := func() servertls.Policy {
		settings, err := svc.Settings(context.Background())
		policy := service.EffectiveServerTLSSettings(nil)
		if err == nil && settings != nil {
			policy = service.EffectiveServerTLSSettings(settings.ServerTLS)
		}
		return servertls.Policy{
			HTTPS:     policy.HTTPSEnabled(),
			PlainHTTP: policy.PlainHTTPEnabled,
		}
	}
	if cfg.Server.TLS.Enabled {
		tlsConfig, err := tlsMgr.TLSConfig()
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("configure TLS: %w", err)
		}
		srv.TLSConfig = tlsConfig
		serveLn = servertls.NewOptionalListener(ln, tlsConfig, policyFn)
	}

	context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})

	slog.Info("server started", "addr", ln.Addr().String(), "scheme", listenerScheme(cfg.Server.TLS.Enabled, policyFn()))
	if err := srv.Serve(serveLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func listenerScheme(processTLSEnabled bool, policy servertls.Policy) string {
	if !processTLSEnabled {
		return "http"
	}
	if policy.HTTPS && policy.PlainHTTP {
		return "https+http"
	}
	if policy.HTTPS {
		return "https"
	}
	if policy.PlainHTTP {
		return "http"
	}
	return "disabled"
}
