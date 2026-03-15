package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

func (a *api) getData(c *ada.Context) error {
	key := c.Request.PathValue("*")

	// Authenticate via Bearer token
	tokenRaw := c.Request.Header.Get("Authorization")
	if tokenRaw == "" {
		// Also check query parameter for simple integrations
		tokenRaw = c.Request.URL.Query().Get("token")
	} else {
		// Strip "Bearer " prefix
		if len(tokenRaw) > 7 && tokenRaw[:7] == "Bearer " {
			tokenRaw = tokenRaw[7:]
		}
	}

	if tokenRaw == "" {
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}

	// Validate token and check scope
	if err := a.svc.ValidateToken(c.Request.Context(), tokenRaw, key, "read"); err != nil {
		return err
	}

	// Collect variation query params (exclude reserved params)
	query := c.Request.URL.Query()
	var variationKey string
	for k, v := range query {
		if k == "token" || k == "version" || k == "format" {
			continue
		}
		if len(v) > 0 {
			variationKey = k + "=" + v[0]
			break // Only one variation param is supported
		}
	}

	// Version — can be integer ("7") or semver ("0.2.4")
	versionStr := query.Get("version")

	result, err := a.svc.GetData(c.Request.Context(), key, versionStr, variationKey)
	if err != nil {
		return err
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
