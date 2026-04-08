package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// getData serves resolved configuration data with token authentication.
func (a *api) getData(c *ada.Context) error {
	key := c.Request.PathValue("*")

	// Authenticate via Bearer token
	tokenRaw := c.Request.Header.Get("Authorization")
	if len(tokenRaw) > 7 && tokenRaw[:7] == "Bearer " {
		tokenRaw = tokenRaw[7:]
	}

	if tokenRaw == "" {
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}

	// Validate token and check scope
	if err := a.svc.ValidateToken(c.Request.Context(), tokenRaw, key, "read"); err != nil {
		return err
	}

	return a.resolveAndWriteData(c, key)
}

// getDataPublic serves resolved configuration data without authentication.
// Intended for the public port where no token is required.
func (a *api) getDataPublic(c *ada.Context) error {
	key := c.Request.PathValue("*")
	return a.resolveAndWriteData(c, key)
}

// resolveAndWriteData is the shared logic for serving configuration data.
// It resolves the file (with variant/version/format), sets Content-Type, and writes the response.
func (a *api) resolveAndWriteData(c *ada.Context, key string) error {
	query := c.Request.URL.Query()

	// Variant — simple name (e.g., "prod", "staging")
	variationKey := query.Get("variant")

	// Version — can be integer ("7") or semver ("0.2.4")
	versionStr := query.Get("version")

	result, err := a.svc.GetData(c.Request.Context(), key, versionStr, variationKey)
	if err != nil {
		return err
	}

	if result.Error != "" {
		return errors.Join(fmt.Errorf("configuration has errors: %s", result.Error), service.ErrBadRequest)
	}

	// Determine output format — if requested format differs from stored format, convert
	requestedFormat := query.Get("format")
	outputData := result.Data
	outputFormat := result.Format

	if requestedFormat != "" && requestedFormat != result.Format {
		converted, err := service.ConvertFormat(result.Data, result.Format, requestedFormat)
		if err != nil {
			return errors.Join(fmt.Errorf("converting from %s to %s: %w", result.Format, requestedFormat, err), service.ErrBadRequest)
		}
		outputData = converted
		outputFormat = requestedFormat
	}

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
	_, err = c.Response.Write(outputData)
	return err
}
