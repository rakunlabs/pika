package api

import (
	"errors"
	"log/slog"
	"net/http"

	mrequestid "github.com/rakunlabs/ada/middleware/requestid"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/pika/internal/service"
)

type response struct {
	Message string `json:"message,omitempty"`
}

type errorResponse struct {
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func (a *api) errorHandler(c *ada.Context, err error) {
	status := errorStatus(err)
	c.SetStatus(status)

	resp := errorResponse{Message: err.Error()}

	if status >= http.StatusInternalServerError {
		r := c.Request
		ctx := r.Context()
		resp.RequestID = r.Header.Get(mrequestid.HeaderXRequestID)
		logi.Ctx(ctx).ErrorContext(ctx, "request failed",
			slog.Int("status", status),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("route", r.Pattern),
			slog.String("user", service.UserFromContext(ctx)),
			slog.String("error", err.Error()),
		)
	}

	_ = c.SendJSON(resp)
}

func errorStatus(err error) int {
	var maxBytesErr *http.MaxBytesError

	switch {
	case errors.As(err, &maxBytesErr):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrBadRequest):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, service.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, service.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
