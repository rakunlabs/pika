package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/service"
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
	svc         *service.Service
	info        Info
	adminSecret string
	encStore    *secret.Storage // nil if encryption is disabled
}

type response struct {
	Message string `json:"message,omitempty"`
}

func Handle(m *ada.Mux, mData *ada.Mux, svc *service.Service, info Info, adminSecret string, encStore *secret.Storage) error {
	api := &api{svc: svc, info: info, adminSecret: adminSecret, encStore: encStore}

	// Inject X-User header into context for all API requests
	m.Use(userMiddleware)

	m.ErrorHandler(api.errorHandler)

	mData.ErrorHandler(api.errorHandler)
	// Data endpoint — consumer-facing, returns resolved config (with token auth)
	mData.GET("/data/*", mData.Wrap(api.getData))

	m.GET("/api/v1/folder", m.Wrap(api.getFolder))
	m.GET("/api/v1/folder/*", m.Wrap(api.getFolder))
	m.POST("/api/v1/folder/*", m.Wrap(api.postFolder))
	m.DELETE("/api/v1/folder/*", m.Wrap(api.deleteFolder))

	m.GET("/api/v1/file/*", m.Wrap(api.getFile))
	m.POST("/api/v1/file/*", m.Wrap(api.postFile))
	m.DELETE("/api/v1/file/*", m.Wrap(api.deleteFile))

	// File versions endpoint
	m.GET("/api/v1/versions/*", m.Wrap(api.getFileVersions))

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

	// Settings
	m.GET("/api/v1/settings", m.Wrap(api.getSettings))
	m.POST("/api/v1/settings", m.Wrap(api.postSettings))

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
		User string `json:"user,omitempty"`
	}{
		Info: a.info,
		User: user,
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

	return c.SetStatus(http.StatusOK).SendJSON(patchSettings)
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
	tokens, err := a.svc.ListTokens(c.Request.Context())
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

	if a.adminSecret == "" {
		return errors.Join(fmt.Errorf("admin_secret is not configured"), service.ErrBadRequest)
	}

	var req struct {
		AdminSecret string `json:"admin_secret"`
		NewKey      string `json:"new_key"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	// Validate admin secret
	if req.AdminSecret != a.adminSecret {
		return errors.Join(fmt.Errorf("invalid admin secret"), service.ErrForbidden)
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
