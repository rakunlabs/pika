package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"log/slog"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/ftpserve"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/server/session"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/sftpserve"
	"github.com/rakunlabs/pika/internal/tftpserve"
)

// userMiddleware extracts the X-User header and injects it into the request context.
func userMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-User")
		if user != "" {
			r = r.WithContext(service.WithUser(r.Context(), user))
		}
		next.ServeHTTP(w, r)
	})
}

// Info holds server metadata returned by the info endpoint.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type api struct {
	svc          *service.Service
	info         Info
	encStore     *secret.Storage // nil if encryption is disabled
	sessionStore *session.Store  // nil if built-in auth is disabled
	rawHandler   *rawHandler     // nil if no raw mounts configured
}

// SessionStore is the interface for session management.
// Used to decouple the api package from the session package.
type SessionStore interface {
	Create(userID, username string) (*http.Cookie, error)
	Get(sessionID string) interface{ Username() string }
	Delete(sessionID string)
	ClearCookie() *http.Cookie
	Middleware(next http.Handler) http.Handler
}

type response struct {
	Message string `json:"message,omitempty"`
}

// HandlePublic registers unauthenticated endpoints for the public port.
// Only /data/*, /raw/* and /healthz are exposed — no admin API, no UI.
func HandlePublic(m *ada.Mux, svc *service.Service, rh *rawHandler) error {
	a := &api{svc: svc, rawHandler: rh}

	m.ErrorHandler(a.errorHandler)
	m.GET("/data/*", m.Wrap(a.getDataPublic))
	m.GET("/raw/*", m.Wrap(a.getRawPublic))
	m.GET("/healthz", m.Wrap(a.healthzHandler))
	return nil
}

func Handle(m *ada.Mux, mData *ada.Mux, mAuth *ada.Mux, svc *service.Service, info Info, encStore *secret.Storage, sessionStore *session.Store, rh *rawHandler) error {
	api := &api{svc: svc, info: info, encStore: encStore, sessionStore: sessionStore, rawHandler: rh}

	// Inject X-User header into context for all API requests
	m.Use(userMiddleware)

	m.ErrorHandler(api.errorHandler)

	mData.ErrorHandler(api.errorHandler)
	// Data endpoint — consumer-facing, returns resolved config (with token auth)
	mData.GET("/data/*", mData.Wrap(api.getData))

	// Raw file endpoints (with token auth)
	mData.GET("/raw/*", mData.Wrap(api.getRaw))
	mData.PUT("/raw/*", mData.Wrap(api.putRaw))
	mData.DELETE("/raw/*", mData.Wrap(api.deleteRaw))

	// Auth endpoints — registered on the unprotected mux (no session middleware)
	if sessionStore != nil {
		mAuth.ErrorHandler(api.errorHandler)
		mAuth.POST("/api/v1/auth/login", mAuth.Wrap(api.login))
		mAuth.POST("/api/v1/auth/logout", mAuth.Wrap(api.logout))
		mAuth.GET("/api/v1/auth/setup", mAuth.Wrap(api.getSetupStatus))
		mAuth.POST("/api/v1/auth/setup", mAuth.Wrap(api.setup))

		// User management endpoints — protected by session middleware
		m.GET("/api/v1/users", m.Wrap(api.listUsers))
		m.POST("/api/v1/users", m.Wrap(api.createUser))
		m.GET("/api/v1/users/*", m.Wrap(api.getUser))
		m.PATCH("/api/v1/users/*", m.Wrap(api.updateUser))
		m.DELETE("/api/v1/users/*", m.Wrap(api.deleteUser))
	}

	m.GET("/api/v1/folder", m.Wrap(api.getFolder))
	m.GET("/api/v1/folder/*", m.Wrap(api.getFolder))
	m.POST("/api/v1/folder/*", m.Wrap(api.postFolder))
	m.DELETE("/api/v1/folder/*", m.Wrap(api.deleteFolder))

	m.GET("/api/v1/file/*", m.Wrap(api.getFile))
	m.POST("/api/v1/file/*", m.Wrap(api.postFile))
	m.DELETE("/api/v1/file/*", m.Wrap(api.deleteFile))

	// File versions endpoint
	m.GET("/api/v1/versions/*", m.Wrap(api.getFileVersions))
	m.PATCH("/api/v1/versions/*", m.Wrap(api.patchFileVersion))

	// Variant endpoints
	m.GET("/api/v1/variants/*", m.Wrap(api.listVariants))

	// Render endpoint — resolves inheritance and variations for preview
	m.POST("/api/v1/render/*", m.Wrap(api.renderFile))

	// Token management endpoints
	m.GET("/api/v1/tokens", m.Wrap(api.listTokens))
	m.POST("/api/v1/tokens", m.Wrap(api.createToken))
	m.DELETE("/api/v1/tokens/*", m.Wrap(api.deleteToken))
	m.PATCH("/api/v1/tokens/*", m.Wrap(api.patchToken))

	// Format conversion endpoint
	m.POST("/api/v1/convert", m.Wrap(api.convertFormat))

	// Search endpoint (SSE streaming)
	m.GET("/api/v1/search", api.searchHandler)

	// Key rotation endpoint (requires admin_secret)
	m.POST("/api/v1/rotate", m.Wrap(api.rotateKey))

	// Admin secret management endpoints
	m.GET("/api/v1/admin-secret/status", m.Wrap(api.adminSecretStatus))
	m.PUT("/api/v1/admin-secret", m.Wrap(api.setAdminSecret))

	// Settings
	m.GET("/api/v1/settings", m.Wrap(api.getSettings))
	m.POST("/api/v1/settings", m.Wrap(api.postSettings))

	// Backup & Restore (requires admin secret)
	m.GET("/api/v1/backup", m.Wrap(api.exportBackup))
	m.POST("/api/v1/backup", m.Wrap(api.importBackup))

	// Raw filesystem browsing and management (for UI, uses session auth)
	m.GET("/api/v1/raw/*", m.Wrap(api.rawHandler.serveRaw))
	m.PUT("/api/v1/raw/*", m.Wrap(api.rawHandler.writeFile))
	m.DELETE("/api/v1/raw/*", m.Wrap(api.rawHandler.deleteFile))
	m.POST("/api/v1/raw-mkdir/*", m.Wrap(api.rawHandler.mkDir))
	m.POST("/api/v1/raw-rename", m.Wrap(api.rawHandler.renameFile))
	m.POST("/api/v1/raw-copy", m.Wrap(api.rawHandler.copyFile))
	m.POST("/api/v1/raw-move", m.Wrap(api.rawHandler.moveFile))

	// External resource browsing
	m.GET("/api/v1/external/*/paths", m.Wrap(api.listExternalPaths))

	m.GET("/api/v1/info", m.Wrap(api.infoHandler))
	m.GET("/healthz", m.Wrap(api.healthzHandler))

	return nil
}

func (a *api) errorHandler(c *ada.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		c.SetStatus(http.StatusNotFound)
	case errors.Is(err, service.ErrBadRequest):
		c.SetStatus(http.StatusBadRequest)
	case errors.Is(err, service.ErrUnauthorized):
		c.SetStatus(http.StatusUnauthorized)
	case errors.Is(err, service.ErrForbidden):
		c.SetStatus(http.StatusForbidden)
	case errors.Is(err, service.ErrConflict):
		c.SetStatus(http.StatusConflict)
	default:
		c.SetStatus(http.StatusInternalServerError)
	}

	c.SendJSON(response{Message: err.Error()})
}

func (a *api) healthzHandler(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendString("OK")
}

func (a *api) infoHandler(c *ada.Context) error {
	user := service.UserFromContext(c.Request.Context())

	resp := struct {
		Info
		User        string      `json:"user,omitempty"`
		AuthEnabled bool        `json:"auth_enabled"`
		RawMounts   []MountInfo `json:"raw_mounts,omitempty"`
	}{
		Info:        a.info,
		User:        user,
		AuthEnabled: a.sessionStore != nil,
		RawMounts:   a.rawHandler.MountsInfo(),
	}

	return c.SetStatus(http.StatusOK).SendJSON(resp)
}

func (a *api) getSettings(c *ada.Context) error {
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(settings)
}

func (a *api) postSettings(c *ada.Context) error {
	var patchSettings service.PatchSettings
	if err := c.Bind(&patchSettings); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.PatchSettings(c.Request.Context(), &patchSettings); err != nil {
		return err
	}

	// If raw mounts were updated, reload them into the handler
	if patchSettings.RawMounts != nil {
		if err := a.reloadRawMounts(c.Request.Context()); err != nil {
			return err
		}
	}

	// If FTP shares or users were updated, reload them
	if patchSettings.FTPShares != nil {
		a.reloadFTPShares(c.Request.Context())
	}
	if patchSettings.FTPUsers != nil {
		a.reloadFTPUsers(c.Request.Context())
	}

	// If serve settings were updated, reload the corresponding servers
	if patchSettings.FTPServe != nil || patchSettings.SFTPServe != nil || patchSettings.TFTPServe != nil {
		settings, err := a.svc.Settings(c.Request.Context())
		if err != nil {
			slog.Error("failed to read settings for file server reload", "error", err)
		} else {
			if patchSettings.FTPServe != nil {
				a.reloadFTPServe(settings)
			}
			if patchSettings.SFTPServe != nil {
				a.reloadSFTPServe(settings)
			}
			if patchSettings.TFTPServe != nil {
				a.reloadTFTPServe(settings)
			}
		}
	}

	return c.SetStatus(http.StatusOK).SendJSON(patchSettings)
}

// reloadRawMounts reads the current settings and rebuilds mount entries.
func (a *api) reloadRawMounts(ctx context.Context) error {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		return fmt.Errorf("reading settings for raw mount reload: %w", err)
	}

	entries, errs := BuildMountEntries(settings.RawMounts)
	for _, e := range errs {
		slog.Warn("skipping invalid raw mount from settings", "error", e)
	}

	a.rawHandler.UpdateMounts(entries)
	return nil
}

// reloadFTPShares rebuilds shares and updates FTP, SFTP, and TFTP servers.
func (a *api) reloadFTPShares(ctx context.Context) {
	shares := BuildFTPShares(ctx, a.svc, a.rawHandler)

	a.rawHandler.mu.RLock()
	ftpSrv := a.rawHandler.ftpServer
	sftpSrv := a.rawHandler.sftpServer
	tftpSrv := a.rawHandler.tftpServer
	a.rawHandler.mu.RUnlock()

	if ftpSrv != nil {
		ftpSrv.UpdateShares(shares)
	}
	if sftpSrv != nil {
		sftpSrv.UpdateShares(shares)
	}
	if tftpSrv != nil {
		tftpSrv.UpdateShares(shares)
	}
	slog.Info("file shares reloaded", "count", len(shares))
}

// reloadFTPUsers rebuilds users and updates both FTP and SFTP servers.
func (a *api) reloadFTPUsers(ctx context.Context) {
	users := BuildFTPUsers(ctx, a.svc)

	a.rawHandler.mu.RLock()
	ftpSrv := a.rawHandler.ftpServer
	sftpSrv := a.rawHandler.sftpServer
	a.rawHandler.mu.RUnlock()

	if ftpSrv != nil {
		ftpSrv.UpdateUsers(users)
	}
	if sftpSrv != nil {
		sftpSrv.UpdateUsers(users)
	}
	slog.Info("file server users reloaded", "count", len(users))
}

// reloadFTPServe stops the existing FTP server (if running) and starts a new one if enabled.
func (a *api) reloadFTPServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)
	users := BuildFTPUsers(context.Background(), a.svc)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.ftpServer
	oldCancel := a.rawHandler.ftpCancel
	a.rawHandler.ftpServer = nil
	a.rawHandler.ftpCancel = nil
	a.rawHandler.mu.Unlock()

	// Cancel context first to trigger clean goroutine shutdown, then stop server to free port.
	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.FTPServe != nil && settings.FTPServe.Enabled {
		ftpSrv, err := ftpserve.NewServer(settings.FTPServe, shares, users)
		if err != nil {
			slog.Error("failed to start FTP server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		ftpSrv.Start(ctx)

		a.rawHandler.mu.Lock()
		a.rawHandler.ftpServer = ftpSrv
		a.rawHandler.ftpCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("FTP server reloaded")
	} else {
		slog.Info("FTP server disabled")
	}
}

// reloadSFTPServe stops the existing SFTP server (if running) and starts a new one if enabled.
func (a *api) reloadSFTPServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)
	users := BuildFTPUsers(context.Background(), a.svc)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.sftpServer
	oldCancel := a.rawHandler.sftpCancel
	a.rawHandler.sftpServer = nil
	a.rawHandler.sftpCancel = nil
	a.rawHandler.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.SFTPServe != nil && settings.SFTPServe.Enabled {
		sftpSrv, err := sftpserve.NewServer(settings.SFTPServe, shares, users)
		if err != nil {
			slog.Error("failed to start SFTP server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		sftpSrv.Start(ctx)

		a.rawHandler.mu.Lock()
		a.rawHandler.sftpServer = sftpSrv
		a.rawHandler.sftpCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("SFTP server reloaded")
	} else {
		slog.Info("SFTP server disabled")
	}
}

// reloadTFTPServe stops the existing TFTP server (if running) and starts a new one if enabled.
func (a *api) reloadTFTPServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.tftpServer
	oldCancel := a.rawHandler.tftpCancel
	a.rawHandler.tftpServer = nil
	a.rawHandler.tftpCancel = nil
	a.rawHandler.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.TFTPServe != nil && settings.TFTPServe.Enabled {
		tftpSrv, err := tftpserve.NewServer(settings.TFTPServe, shares)
		if err != nil {
			slog.Error("failed to start TFTP server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		tftpSrv.Start(ctx, settings.TFTPServe)

		a.rawHandler.mu.Lock()
		a.rawHandler.tftpServer = tftpSrv
		a.rawHandler.tftpCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("TFTP server reloaded")
	} else {
		slog.Info("TFTP server disabled")
	}
}

// BuildMountEntries creates mountEntry instances from settings entries.
// Returns successfully created entries and any errors for failed ones.
func BuildMountEntries(settingsEntries []service.RawMountEntry) ([]mountEntry, []error) {
	var entries []mountEntry
	var errs []error
	seen := make(map[string]bool)

	for _, m := range settingsEntries {
		if m.Prefix == "" {
			errs = append(errs, fmt.Errorf("raw mount prefix must not be empty"))
			continue
		}
		if seen[m.Prefix] {
			errs = append(errs, fmt.Errorf("duplicate raw mount prefix %q", m.Prefix))
			continue
		}
		seen[m.Prefix] = true

		mountType := m.Type
		if mountType == "" {
			mountType = "local"
		}

		fs, err := newRawFSFromSettings(mountType, m)
		if err != nil {
			errs = append(errs, fmt.Errorf("mount %q: %w", m.Prefix, err))
			continue
		}

		entries = append(entries, mountEntry{
			Prefix:   m.Prefix,
			FS:       fs,
			Type:     mountType,
			Writable: rawfs.IsWritable(fs),
		})
	}

	return entries, errs
}

// newRawFSFromSettings creates a RawFS from a settings entry.
func newRawFSFromSettings(mountType string, m service.RawMountEntry) (rawfs.RawFS, error) {
	switch mountType {
	case "local", "":
		if m.Path == "" {
			return nil, fmt.Errorf("path is required for local mount")
		}
		return localfs.New(m.Path)
	case "s3":
		if m.S3 == nil {
			return nil, fmt.Errorf("s3 config is required")
		}
		if rawfs.NewS3FSFunc == nil {
			return nil, fmt.Errorf("s3 backend not available")
		}
		return rawfs.NewS3FSFunc(m.S3.Bucket, m.S3.Region, m.S3.Endpoint, m.S3.AccessKey, m.S3.SecretKey, m.S3.Prefix, m.S3.PathStyle, m.S3.Secure)
	case "ftp":
		if m.FTP == nil {
			return nil, fmt.Errorf("ftp config is required")
		}
		if rawfs.NewFTPFSFunc == nil {
			return nil, fmt.Errorf("ftp backend not available")
		}
		return rawfs.NewFTPFSFunc(m.FTP.Host, m.FTP.Username, m.FTP.Password, m.FTP.BasePath, m.FTP.TLS)
	case "sftp":
		if m.SFTP == nil {
			return nil, fmt.Errorf("sftp config is required")
		}
		if rawfs.NewSFTPFSFunc == nil {
			return nil, fmt.Errorf("sftp backend not available")
		}
		return rawfs.NewSFTPFSFunc(m.SFTP.Host, m.SFTP.Username, m.SFTP.Password, m.SFTP.PrivateKey, m.SFTP.BasePath)
	default:
		return nil, fmt.Errorf("unknown mount type %q", mountType)
	}
}

// NewRawFSFromConfig creates a RawFS from a config-file RawMount.
func NewRawFSFromConfig(m config.RawMount) (rawfs.RawFS, error) {
	mountType := m.Type
	if mountType == "" {
		mountType = "local"
	}
	switch mountType {
	case "local":
		if m.Path == "" {
			return nil, fmt.Errorf("path is required for local mount")
		}
		return localfs.New(m.Path)
	case "s3":
		if m.S3 == nil {
			return nil, fmt.Errorf("s3 config is required")
		}
		if rawfs.NewS3FSFunc == nil {
			return nil, fmt.Errorf("s3 backend not available")
		}
		return rawfs.NewS3FSFunc(m.S3.Bucket, m.S3.Region, m.S3.Endpoint, m.S3.AccessKey, m.S3.SecretKey, m.S3.Prefix, m.S3.PathStyle, m.S3.Secure)
	case "ftp":
		if m.FTP == nil {
			return nil, fmt.Errorf("ftp config is required")
		}
		if rawfs.NewFTPFSFunc == nil {
			return nil, fmt.Errorf("ftp backend not available")
		}
		return rawfs.NewFTPFSFunc(m.FTP.Host, m.FTP.Username, m.FTP.Password, m.FTP.BasePath, m.FTP.TLS)
	case "sftp":
		if m.SFTP == nil {
			return nil, fmt.Errorf("sftp config is required")
		}
		if rawfs.NewSFTPFSFunc == nil {
			return nil, fmt.Errorf("sftp backend not available")
		}
		return rawfs.NewSFTPFSFunc(m.SFTP.Host, m.SFTP.Username, m.SFTP.Password, m.SFTP.PrivateKey, m.SFTP.BasePath)
	default:
		return nil, fmt.Errorf("unknown mount type %q", mountType)
	}
}

// BuildMountEntriesFromConfig creates mount entries from config-file RawMounts.
func BuildMountEntriesFromConfig(cfgMounts []config.RawMount) ([]mountEntry, []error) {
	var entries []mountEntry
	var errs []error
	seen := make(map[string]bool)

	for _, m := range cfgMounts {
		if m.Prefix == "" {
			errs = append(errs, fmt.Errorf("raw mount prefix must not be empty"))
			continue
		}
		if seen[m.Prefix] {
			errs = append(errs, fmt.Errorf("duplicate raw mount prefix %q", m.Prefix))
			continue
		}
		seen[m.Prefix] = true

		fs, err := NewRawFSFromConfig(m)
		if err != nil {
			errs = append(errs, fmt.Errorf("mount %q: %w", m.Prefix, err))
			continue
		}

		mountType := m.Type
		if mountType == "" {
			mountType = "local"
		}

		entries = append(entries, mountEntry{
			Prefix:   m.Prefix,
			FS:       fs,
			Type:     mountType,
			Writable: rawfs.IsWritable(fs),
		})

		slog.Info("raw mount configured", "prefix", "/raw/"+m.Prefix, "type", mountType)
	}

	return entries, errs
}

// BuildInitialRawHandler creates a rawHandler from config-file mounts merged with DB settings.
func BuildInitialRawHandler(ctx context.Context, cfgMounts []config.RawMount, svc *service.Service) *rawHandler {
	// Build config-file mount entries
	entries, cfgErrs := BuildMountEntriesFromConfig(cfgMounts)
	for _, err := range cfgErrs {
		slog.Warn("skipping invalid config-file raw mount", "error", err)
	}

	// Merge DB-stored mounts (config-file mounts take precedence)
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for raw mounts", "error", err)
	} else if len(settings.RawMounts) > 0 {
		seen := make(map[string]bool)
		for _, e := range entries {
			seen[e.Prefix] = true
		}

		dbEntries, dbErrs := BuildMountEntries(settings.RawMounts)
		for _, err := range dbErrs {
			slog.Warn("skipping invalid DB raw mount", "error", err)
		}

		for _, e := range dbEntries {
			if seen[e.Prefix] {
				slog.Info("skipping DB raw mount (overridden by config file)", "prefix", e.Prefix)
				continue
			}
			entries = append(entries, e)
		}
	}

	return NewRawHandler(entries, ctx)
}

// BuildFTPShares creates FTP share entries from the current settings, resolving
// each path's mount prefix to the corresponding RawFS backend via the rawHandler.
func BuildFTPShares(ctx context.Context, svc *service.Service, rh *rawHandler) []ftpserve.Share {
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for FTP shares", "error", err)
		return nil
	}

	rh.mu.RLock()
	defer rh.mu.RUnlock()

	// Build a mount lookup map
	mountMap := make(map[string]rawfs.RawFS, len(rh.mounts))
	for _, m := range rh.mounts {
		mountMap[m.Prefix] = m.FS
	}

	var shares []ftpserve.Share
	for _, s := range settings.FTPShares {
		var sources []ftpserve.ShareSource
		for _, p := range s.Paths {
			// Parse "mount_prefix" or "mount_prefix/sub/path"
			mountPrefix, subPath := parseMountPath(p)
			fs, ok := mountMap[mountPrefix]
			if !ok {
				slog.Warn("FTP share path references unknown mount", "share", s.Name, "path", p, "mount", mountPrefix)
				continue
			}
			sources = append(sources, ftpserve.ShareSource{
				Mount: mountPrefix,
				Path:  subPath,
				FS:    fs,
			})
		}

		if len(sources) == 0 {
			slog.Warn("FTP share has no valid sources, skipping", "share", s.Name)
			continue
		}

		shares = append(shares, ftpserve.Share{
			Name:     s.Name,
			Sources:  sources,
			ReadOnly: s.ReadOnly,
		})
	}

	return shares
}

// parseMountPath splits "mount_prefix/sub/path" into ("mount_prefix", "sub/path").
func parseMountPath(p string) (string, string) {
	p = strings.TrimPrefix(p, "/")
	idx := strings.IndexByte(p, '/')
	if idx < 0 {
		return p, ""
	}
	return p[:idx], p[idx+1:]
}

// BuildFTPUsers creates FTP user entries from the current settings.
func BuildFTPUsers(ctx context.Context, svc *service.Service) []ftpserve.User {
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for FTP users", "error", err)
		return nil
	}

	var users []ftpserve.User
	for _, u := range settings.FTPUsers {
		users = append(users, ftpserve.User{
			Username: u.Username,
			Password: u.Password,
			Shares:   u.Shares,
			ReadOnly: u.ReadOnly,
		})
	}

	return users
}

// SetFTPServer stores the FTP server reference and its cancel func in the rawHandler.
func SetFTPServer(rh *rawHandler, ftpSrv *ftpserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.ftpServer = ftpSrv
	rh.ftpCancel = cancel
	rh.mu.Unlock()
}

// SetSFTPServer stores the SFTP server reference and its cancel func in the rawHandler.
func SetSFTPServer(rh *rawHandler, sftpSrv *sftpserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.sftpServer = sftpSrv
	rh.sftpCancel = cancel
	rh.mu.Unlock()
}

// SetTFTPServer stores the TFTP server reference and its cancel func in the rawHandler.
func SetTFTPServer(rh *rawHandler, tftpSrv *tftpserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.tftpServer = tftpSrv
	rh.tftpCancel = cancel
	rh.mu.Unlock()
}

func (a *api) listExternalPaths(c *ada.Context) error {
	resourceName := c.Request.PathValue("*")
	prefix := c.Request.URL.Query().Get("prefix")

	paths, err := a.svc.ListExternalPaths(c.Request.Context(), resourceName, prefix)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(paths)
}

func (a *api) postFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	if err := a.svc.SetFolder(c.Request.Context(), key); err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) getFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	data, err := a.svc.Folder(c.Request.Context(), key)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(data)
}

func (a *api) deleteFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	if err := a.svc.DeleteFolder(c.Request.Context(), key); err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) getFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	version := int64(0)
	if versionStr := c.Request.URL.Query().Get("version"); versionStr != "" {
		var err error
		version, err = strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			return errors.Join(err, service.ErrBadRequest)
		}
	}

	var data *service.File
	var err error
	if variant != "" {
		data, err = a.svc.Variant(c.Request.Context(), key, variant, version)
	} else {
		data, err = a.svc.File(c.Request.Context(), key, version)
	}
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(data)
}

func (a *api) postFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var req struct {
		service.File
		ExpectedVersion *int64 `json:"expected_version,omitempty"`
		Constraint      string `json:"constraint,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	var version int64
	var err error
	if variant != "" {
		version, err = a.svc.SetVariant(c.Request.Context(), key, variant, &req.File, req.ExpectedVersion, req.Constraint)
	} else {
		version, err = a.svc.SetFile(c.Request.Context(), key, &req.File, req.ExpectedVersion, req.Constraint)
	}
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(struct {
		service.File
		Version int64 `json:"version"`
	}{
		File:    req.File,
		Version: version,
	})
}

func (a *api) deleteFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	version := int64(0)
	if versionStr := c.Request.URL.Query().Get("version"); versionStr != "" {
		var err error
		version, err = strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			return errors.Join(err, service.ErrBadRequest)
		}
	}

	var err error
	if variant != "" {
		err = a.svc.DeleteVariant(c.Request.Context(), key, variant, version)
	} else {
		err = a.svc.DeleteFile(c.Request.Context(), key, version)
	}
	if err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) getFileVersions(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var versions service.FileVersions
	var err error
	if variant != "" {
		versions, err = a.svc.VariantVersions(c.Request.Context(), key, variant)
	} else {
		versions, err = a.svc.FileVersionsList(c.Request.Context(), key)
	}
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(versions)
}

func (a *api) patchFileVersion(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var req struct {
		Version    int64  `json:"version"`
		Constraint string `json:"constraint"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.Version <= 0 {
		return errors.Join(fmt.Errorf("version is required and must be > 0"), service.ErrBadRequest)
	}

	filePath := key
	if variant != "" {
		filePath = key + "/@" + variant
	}

	if err := a.svc.UpdateConstraint(c.Request.Context(), filePath, req.Version, req.Constraint); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "constraint updated"})
}

func (a *api) listVariants(c *ada.Context) error {
	key := c.Request.PathValue("*")

	variants, err := a.svc.ListVariants(c.Request.Context(), key)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(variants)
}

func (a *api) renderFile(c *ada.Context) error {
	key := c.Request.PathValue("*")

	var req struct {
		Content string           `json:"content"`
		Meta    service.FileMeta `json:"meta"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := a.svc.RenderFile(c.Request.Context(), key, req.Content, &req.Meta)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(result)
}

func (a *api) listTokens(c *ada.Context) error {
	tokens, _, err := a.svc.ListTokens(c.Request.Context(), nil)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(tokens)
}

func (a *api) createToken(c *ada.Context) error {
	var req service.CreateTokenRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := a.svc.CreateToken(c.Request.Context(), &req)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(result)
}

func (a *api) deleteToken(c *ada.Context) error {
	id := c.Request.PathValue("*")

	if err := a.svc.DeleteToken(c.Request.Context(), id); err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) patchToken(c *ada.Context) error {
	id := c.Request.PathValue("*")

	var req service.PatchTokenRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.PatchToken(c.Request.Context(), id, &req); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "token updated"})
}

func (a *api) convertFormat(c *ada.Context) error {
	var req struct {
		Content string `json:"content"`
		From    string `json:"from"`
		To      string `json:"to"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.From == "" || req.To == "" {
		return errors.Join(fmt.Errorf("'from' and 'to' formats are required"), service.ErrBadRequest)
	}

	converted, err := service.ConvertFormat([]byte(req.Content), req.From, req.To)
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}{
		Content: string(converted),
		Format:  req.To,
	})
}

func (a *api) rotateKey(c *ada.Context) error {
	if a.encStore == nil {
		return errors.Join(fmt.Errorf("encryption is not enabled"), service.ErrBadRequest)
	}

	var req struct {
		AdminSecret string `json:"admin_secret"`
		NewKey      string `json:"new_key"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	// Validate admin secret against the bcrypt hash stored in settings
	if err := a.svc.VerifyAdminSecret(c.Request.Context(), req.AdminSecret); err != nil {
		return err
	}

	// Validate new key
	if req.NewKey == "" {
		return errors.Join(fmt.Errorf("new_key is required"), service.ErrBadRequest)
	}

	// Hash the key with SHA-256 to get exactly 32 bytes
	newKeyHash := sha256.Sum256([]byte(req.NewKey))

	newEncryptor, err := crypto.NewChaCha20(newKeyHash[:])
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	// Perform rotation — re-encrypts all values
	if err := a.encStore.RotateKey(c.Request.Context(), newEncryptor); err != nil {
		return fmt.Errorf("key rotation failed: %w", err)
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "key rotation completed"})
}

func (a *api) adminSecretStatus(c *ada.Context) error {
	configured, err := a.svc.HasAdminSecret(c.Request.Context())
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Configured bool `json:"configured"`
	}{
		Configured: configured,
	})
}

func (a *api) setAdminSecret(c *ada.Context) error {
	var req struct {
		CurrentSecret string `json:"current_secret"`
		NewSecret     string `json:"new_secret"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.SetAdminSecret(c.Request.Context(), req.CurrentSecret, req.NewSecret); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "admin secret updated"})
}

// searchHandler uses SSE to stream search results as they are found.
// The client can abort the connection to cancel the search.
func (a *api) searchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `{"message":"query parameter 'q' is required"}`, http.StatusBadRequest)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Use a cancellable context — cancelled when client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	results := make(chan service.SearchResult, 10)

	// Run search in background
	go func() {
		_ = a.svc.Search(ctx, query, results)
	}()

	// Stream results as SSE events
	for result := range results {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data, err := json.Marshal(result)
		if err != nil {
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Send done event
	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}
