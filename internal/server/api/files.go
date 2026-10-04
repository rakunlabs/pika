package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/service"
)

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
	variant := c.Request.URL.Query().Get("variant")

	var req struct {
		Content string           `json:"content"`
		Meta    service.FileMeta `json:"meta"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := a.svc.RenderFile(c.Request.Context(), key, variant, req.Content, &req.Meta)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(result)
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
