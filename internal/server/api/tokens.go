package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/query"

	"github.com/rakunlabs/pika/internal/service"
)

// listTokens returns API tokens. Without paging parameters it returns a
// plain JSON array (the historical shape). With any of _limit, _offset,
// _sort or a filter (name, active) it returns {"tokens": [...], "total": N}.
//
//	_limit=20      page size
//	_offset=0      page offset
//	_sort=-created_at
//	name=ci        case-insensitive substring match on name
//	active=true    filter by enabled state
func (a *api) listTokens(c *ada.Context) error {
	raw := c.Request.URL.RawQuery
	if raw == "" {
		tokens, _, err := a.svc.ListTokens(c.Request.Context(), nil)
		if err != nil {
			return err
		}
		return c.SetStatus(http.StatusOK).SendJSON(tokens)
	}

	q, err := query.Parse(raw,
		query.WithKey("name",
			query.KeyOperator(query.OperatorILike),
			query.KeyValueTransform(usernameSearchTransform),
		),
		query.WithDefaultLimit(50),
	)
	if err != nil {
		return errors.Join(fmt.Errorf("invalid query parameters: %w", err), service.ErrBadRequest)
	}

	tokens, total, err := a.svc.ListTokens(c.Request.Context(), q)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Tokens []service.TokenInfo `json:"tokens"`
		Total  int64               `json:"total"`
	}{Tokens: tokens, Total: total})
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
