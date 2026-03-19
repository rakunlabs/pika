package consul

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// kvEntry mirrors the Consul KV API response structure.
type kvEntry struct {
	CreateIndex int64  `json:"CreateIndex"`
	ModifyIndex int64  `json:"ModifyIndex"`
	LockIndex   int64  `json:"LockIndex"`
	Key         string `json:"Key"`
	Flags       int64  `json:"Flags"`
	Value       string `json:"Value"`
	Session     string `json:"Session"`
}

type handler struct {
	svc *service.Service
}

// Register mounts the Consul KV compatibility routes onto the given mux.
func Register(m *ada.Mux, svc *service.Service) {
	h := &handler{svc: svc}
	m.ErrorHandler(h.errorHandler)
	m.GET("/v1/kv/*", m.Wrap(h.getKV))
}

// getKV handles GET /v1/kv/* requests in a Consul-compatible way.
//
// Supported query parameters:
//   - raw: if present, returns the raw value bytes (Consul convention)
//   - variant: Pika variant name (e.g., "prod", "staging")
//   - version: Pika version (integer or semver)
//   - format: output format conversion (json, yaml, toml)
func (h *handler) getKV(c *ada.Context) error {
	key := c.Request.PathValue("*")

	query := c.Request.URL.Query()
	variantKey := query.Get("variant")
	versionStr := query.Get("version")

	result, err := h.svc.GetData(c.Request.Context(), key, versionStr, variantKey)
	if err != nil {
		return err
	}

	// Determine output format — convert if requested format differs from stored format
	requestedFormat := query.Get("format")
	outputData := result.Data
	outputFormat := result.Format

	if requestedFormat != "" && requestedFormat != result.Format {
		converted, err := service.ConvertFormat(result.Data, result.Format, requestedFormat)
		if err != nil {
			return errors.Join(err, service.ErrBadRequest)
		}
		outputData = converted
		outputFormat = requestedFormat
	}

	// Consul ?raw flag — return raw bytes directly (same as /data/*)
	if _, ok := query["raw"]; ok {
		switch outputFormat {
		case "json":
			c.Response.Header().Set("Content-Type", "application/json")
		case "yaml", "yml":
			c.Response.Header().Set("Content-Type", "application/x-yaml")
		case "toml":
			c.Response.Header().Set("Content-Type", "application/toml")
		default:
			c.Response.Header().Set("Content-Type", "application/octet-stream")
		}

		c.SetStatus(http.StatusOK)
		_, err := c.Response.Write(outputData)
		return err
	}

	// Standard Consul KV JSON envelope — value is base64-encoded
	entry := kvEntry{
		Key:   key,
		Value: base64.StdEncoding.EncodeToString(outputData),
	}

	// Consul returns a JSON array with one element
	return c.SetStatus(http.StatusOK).SendJSON([]kvEntry{entry})
}

// errorHandler maps service errors to Consul-compatible HTTP responses.
// Consul returns 404 with no body for missing keys, unlike Pika's JSON errors.
func (h *handler) errorHandler(c *ada.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		c.SetStatus(http.StatusNotFound)
		return
	case errors.Is(err, service.ErrBadRequest):
		c.SetStatus(http.StatusBadRequest)
	default:
		c.SetStatus(http.StatusInternalServerError)
	}

	c.SendString(err.Error())
}
