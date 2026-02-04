package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

type api struct {
	svc *service.Service
}

type response struct {
	Message string `json:"message,omitempty"`
}

func Handle(m *ada.Mux, svc *service.Service) error {
	api := &api{svc: svc}

	m.ErrorHandler(api.errorHandler)

	m.GET("/api/v1/folder", m.Wrap(api.getFolder))
	m.GET("/api/v1/folder/*", m.Wrap(api.getFolder))
	m.POST("/api/v1/folder/*", m.Wrap(api.postFolder))
	m.DELETE("/api/v1/folder/*", m.Wrap(api.deleteFolder))

	m.GET("/api/v1/file/*", m.Wrap(api.getFile))
	m.POST("/api/v1/file/*", m.Wrap(api.postFile))
	m.DELETE("/api/v1/file/*", m.Wrap(api.deleteFile))

	m.GET("/api/v1/data/*", m.Wrap(api.getData))

	m.GET("/api/v1/settings", m.Wrap(api.getSettings))
	m.POST("/api/v1/settings", m.Wrap(api.postSettings))

	m.GET("/healthz", m.Wrap(api.healthzHandler))

	return nil
}

func (a *api) errorHandler(c *ada.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		c.SetStatus(http.StatusNotFound)
	case errors.Is(err, service.ErrBadRequest):
		c.SetStatus(http.StatusBadRequest)
	default:
		c.SetStatus(http.StatusInternalServerError)
	}

	c.SendJSON(response{Message: err.Error()})
}

func (a *api) healthzHandler(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendString("OK")
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

func (a *api) getData(c *ada.Context) error {
	return nil
}

func (a *api) postFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	if err := a.svc.SetFolder(c.Request.Context(), key); err != nil {
		return err
	}

	return nil
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

	version := int64(0)

	versionStr := c.Request.URL.Query().Get("version")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	data, err := a.svc.File(c.Request.Context(), key, version)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(data)
}

func (a *api) postFile(c *ada.Context) error {
	key := c.Request.PathValue("*")

	var data service.File
	if err := c.Bind(&data); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.SetFile(c.Request.Context(), key, &data); err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(data)
}

func (a *api) deleteFile(c *ada.Context) error {
	key := c.Request.PathValue("*")

	version := int64(0)

	versionStr := c.Request.URL.Query().Get("version")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.DeleteFile(c.Request.Context(), key, version); err != nil {
		return err
	}

	return c.SendNoContent()
}
