package api

import (
	"errors"
	"net/http"

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

	m.GET("/api/v1/folder/{key}", m.Wrap(api.getFolder))
	m.DELETE("/api/v1/folder/{key}", m.Wrap(api.deleteFolder))
	m.GET("/api/v1/file/{key}", m.Wrap(api.getFile))
	m.POST("/api/v1/file/{key}", m.Wrap(api.postFile))
	m.DELETE("/api/v1/file/{key}", m.Wrap(api.deleteFile))

	m.GET("/api/v1/data/{key}", m.Wrap(api.getDataHandler))

	m.GET("/healthz", m.Wrap(api.healthzHandler))

	return nil
}

func (a *api) errorHandler(c *ada.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		c.SetStatus(http.StatusNotFound)
	default:
		c.SetStatus(http.StatusInternalServerError)
	}

	c.SendJSON(response{Message: err.Error()})
}

func (a *api) healthzHandler(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendString("OK")
}

func (a *api) getDataHandler(c *ada.Context) error {
	return nil
}

func (a *api) getFolder(c *ada.Context) error {
	return nil
}

func (a *api) deleteFolder(c *ada.Context) error {
	return nil
}

func (a *api) getFile(c *ada.Context) error {
	return nil
}

func (a *api) postFile(c *ada.Context) error {
	return nil
}

func (a *api) deleteFile(c *ada.Context) error {
	return nil
}
