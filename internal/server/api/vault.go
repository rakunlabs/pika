package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/service"
)

// Personal vault self-service handlers. The /api/v1/me/vault namespace
// is owned by the calling user — every handler resolves user_id from
// the request context (set by the auth middleware) and never accepts
// a path-param user id, so an admin cannot accidentally peek at
// someone else's vault from these endpoints.
//
// The handler set mirrors the totp.go / passkey.go shape: status,
// setup (one-shot setup payload), unlock-check (rate-limited), item
// CRUD with optimistic concurrency, history listing, and a couple of
// privileged operations (master-password rotation, kit regeneration,
// reset) that the SPA always pairs with a fresh password re-auth on
// the client side (the server doesn't see the password — the wrap
// has already happened in the browser).
//
// Endpoints return 503 when the deployment hasn't wired the vault
// coordinator (s.VaultCoord() == nil). Same shape as totp.

// vaultSetupResponse echoes back the persisted state immediately
// after Setup or RotateMasterPassword so the SPA can re-render the
// vault view without a follow-up round-trip.
//
// We re-use service.VaultAccountView verbatim; aliasing here makes
// the wire contract searchable from the handler file.
type vaultSetupResponse = service.VaultAccountView

// vaultRecoveryKitResponse carries the new kit id after a
// regenerate call. The SPA bundles this with the user-typed Secret
// Key to render the new emergency kit PDF — we never store the
// Secret Key itself.
type vaultRecoveryKitResponse struct {
	RecoveryKitID string `json:"recovery_kit_id"`
}

// vaultSessionLockRequest is the body for PATCH session-lock-seconds.
// Bounded by clampLockSeconds on the service side.
type vaultSessionLockRequest struct {
	SessionLockSeconds int `json:"session_lock_seconds"`
}

// vaultResetResponse confirms how many items were destroyed.
type vaultResetResponse struct {
	ItemsDeleted int64 `json:"items_deleted"`
}

// vaultItemUseResponse is the empty 200 body for /use — kept as a
// dedicated type so the SPA can extend it later (e.g. echo the new
// last_used_at without a separate Get).
type vaultItemUseResponse struct {
	LastUsedAt string `json:"last_used_at"`
}

// ─── Status / Account ─────────────────────────────────────────────

// getMyVaultStatus returns the safe-to-expose view of the vault
// state. Always 200 — a "not initialized" user gets initialized=false
// rather than 404, so the SPA can render uniformly.
func (a *api) getMyVaultStatus(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusOK).SendJSON(&service.VaultStatus{})
	}
	status, err := coord.Status(ctx, userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(status)
}

// getMyVaultAccount returns the unlock-time view: KDF params,
// wrapped vault key, recovery kit id, and item count. Returns 404 if
// the user hasn't set up a vault yet (the SPA flips to the setup
// flow).
func (a *api) getMyVaultAccount(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	acc, err := coord.Account(ctx, userID)
	if err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(acc)
}

// setupMyVault initializes the vault. Body shape matches
// service.VaultSetupRequest: KDF params, wrapped vault key, etc.
// 409 on re-init.
func (a *api) setupMyVault(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}

	var req service.VaultSetupRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	view, err := coord.Setup(ctx, userID, &req)
	if err != nil {
		if errors.Is(err, service.ErrVaultAlreadyInitialized) {
			return c.SetStatus(http.StatusConflict).SendJSON(response{
				Message: "vault is already initialized; use the rotate or reset endpoints to change it",
			})
		}
		return err
	}
	return c.SetStatus(http.StatusCreated).SendJSON((*vaultSetupResponse)(view))
}

// rotateMyVaultMasterPassword updates the KDF params and wrapped
// vault key. The vault key itself is NOT re-keyed — only its
// password wrapping changes, so the items don't need to be
// re-encrypted. The SPA performs the re-wrap client-side after
// confirming the old password.
func (a *api) rotateMyVaultMasterPassword(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}

	var req service.VaultSetupRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	view, err := coord.RotateMasterPassword(ctx, userID, &req)
	if err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON((*vaultSetupResponse)(view))
}

// unlockMyVaultCheck verifies the secret-key hash. Rate-limited per
// (user, ip). Returns 200 on match, 401 on mismatch or rate limit.
func (a *api) unlockMyVaultCheck(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}

	var req service.UnlockCheckRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	// ClientIP returns "" when RemoteAddr is unparseable — fine, the
	// service layer treats empty IP as "no rate-limit key" and skips
	// the limiter. We pass nil trustedProxies because XFF forging
	// here only lets an attacker shard their attempts across many
	// fake IPs, which defeats the limiter anyway — and the real
	// gate is the wrapped-key derivation cost, not this check.
	ip := authx.ClientIP(c.Request, nil)

	if err := coord.UnlockCheck(ctx, userID, ip, &req); err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		return err
	}
	return c.SendNoContent()
}

// regenerateMyVaultRecoveryKit rotates the kit id. The SPA bundles
// the new id with the user-known Secret Key to produce a fresh PDF.
func (a *api) regenerateMyVaultRecoveryKit(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	id, err := coord.RegenerateRecoveryKitID(ctx, userID)
	if err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(vaultRecoveryKitResponse{RecoveryKitID: id})
}

// setMyVaultSessionLock updates the per-user auto-lock TTL.
func (a *api) setMyVaultSessionLock(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}

	var req vaultSessionLockRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := coord.SetSessionLockSeconds(ctx, userID, req.SessionLockSeconds); err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		return err
	}
	return c.SendNoContent()
}

// resetMyVault wipes the entire vault (account + items + history).
// Returns the deleted item count. The SPA must re-confirm — this
// endpoint is the destructive escape hatch ("I lost my master
// password and want to start over").
func (a *api) resetMyVault(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	n, err := coord.Reset(ctx, userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(vaultResetResponse{ItemsDeleted: n})
}

// ─── Items ────────────────────────────────────────────────────────

// listMyVaultItems returns items matching the supplied filter. Query
// params:
//
//	type       — exact match against the item type
//	favorite   — "1"/"true" to limit to favorites
//	archived   — "1"/"true" → ArchivedOnly; "include" → IncludeArchived
//	trash      — "1"/"true" to scope to the trash bin
//
// There are no `q` or `tag` filters because item titles and tags are
// stored as ciphertext on the server (see service.VaultItem). The
// SPA decrypts the response in-place and runs free-text and tag
// filters in memory.
//
// Returned items include their encrypted_payload + encrypted_title +
// encrypted_tags + encrypted_hostnames (the SPA decrypts them
// client-side). The server emits no totals — the SPA renders the
// whole list at unlock time, and counts are surfaced via the status
// endpoint.
func (a *api) listMyVaultItems(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}

	q := c.Request.URL.Query()
	filter := service.VaultItemFilter{
		Type:         service.VaultItemType(q.Get("type")),
		FavoriteOnly: parseBoolQuery(q.Get("favorite")),
		TrashOnly:    parseBoolQuery(q.Get("trash")),
	}
	switch strings.ToLower(q.Get("archived")) {
	case "include":
		filter.IncludeArchived = true
	case "only", "1", "true", "yes":
		filter.ArchivedOnly = true
	}

	items, err := coord.ListItems(ctx, userID, filter)
	if err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		return err
	}
	// Always emit an empty array (not null) so the SPA's iteration
	// code doesn't need a nil-check.
	if items == nil {
		items = []service.VaultItem{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(items)
}

// getMyVaultItem returns one item including its encrypted payload.
func (a *api) getMyVaultItem(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	itemID := c.Request.PathValue("*")
	if itemID == "" {
		return errors.Join(errors.New("item id required"), service.ErrBadRequest)
	}
	item, err := coord.GetItem(ctx, userID, itemID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(item)
}

// createMyVaultItem inserts a new item.
func (a *api) createMyVaultItem(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}

	var req service.CreateVaultItemRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	item, err := coord.CreateItem(ctx, userID, &req)
	if err != nil {
		if errors.Is(err, service.ErrVaultNotInitialized) {
			return c.SetStatus(http.StatusNotFound).SendJSON(response{Message: "vault not initialized"})
		}
		if errors.Is(err, service.ErrVaultUnknownItemType) {
			return c.SetStatus(http.StatusBadRequest).SendJSON(response{Message: err.Error()})
		}
		return err
	}
	return c.SetStatus(http.StatusCreated).SendJSON(item)
}

// updateMyVaultItem patches an existing item. 409 on version conflict.
func (a *api) updateMyVaultItem(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	itemID := c.Request.PathValue("*")
	if itemID == "" {
		return errors.Join(errors.New("item id required"), service.ErrBadRequest)
	}

	var req service.UpdateVaultItemRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	item, err := coord.UpdateItem(ctx, userID, itemID, &req)
	if err != nil {
		if errors.Is(err, service.ErrVaultVersionConflict) {
			return c.SetStatus(http.StatusConflict).SendJSON(response{
				Message: "item was modified by another tab; refresh and retry",
			})
		}
		if errors.Is(err, service.ErrVaultUnknownItemType) {
			return c.SetStatus(http.StatusBadRequest).SendJSON(response{Message: err.Error()})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(item)
}

// softDeleteMyVaultItem moves an item to trash.
func (a *api) softDeleteMyVaultItem(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	itemID := c.Request.PathValue("*")
	if itemID == "" {
		return errors.Join(errors.New("item id required"), service.ErrBadRequest)
	}

	// ?purge=true means hard-delete from trash. The two-step
	// (soft-delete then purge) flow is explicit in the URL so an
	// accidental click can't bypass the trash.
	if parseBoolQuery(c.Request.URL.Query().Get("purge")) {
		if err := coord.PurgeItem(ctx, userID, itemID); err != nil {
			return err
		}
		return c.SendNoContent()
	}

	if err := coord.SoftDeleteItem(ctx, userID, itemID); err != nil {
		return err
	}
	return c.SendNoContent()
}

// restoreMyVaultItem un-trashes an item.
func (a *api) restoreMyVaultItem(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	itemID := c.Request.PathValue("*")
	if itemID == "" {
		return errors.Join(errors.New("item id required"), service.ErrBadRequest)
	}
	item, err := coord.RestoreItem(ctx, userID, itemID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(item)
}

// touchMyVaultItem updates last_used_at. Used by the SPA when the
// user copies a field. Body is empty.
func (a *api) touchMyVaultItem(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	itemID := c.Request.PathValue("*")
	if itemID == "" {
		return errors.Join(errors.New("item id required"), service.ErrBadRequest)
	}
	if err := coord.TouchLastUsed(ctx, userID, itemID); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(vaultItemUseResponse{LastUsedAt: ""})
}

// listMyVaultItemVersions returns the history of an item. Newest
// first. Versions carry encrypted_payload bytes so the SPA can
// preview / restore any prior state.
func (a *api) listMyVaultItemVersions(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.VaultCoordFor(ctx)
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "vault not configured"})
	}
	itemID := c.Request.PathValue("*")
	if itemID == "" {
		return errors.Join(errors.New("item id required"), service.ErrBadRequest)
	}
	versions, err := coord.ListItemVersions(ctx, userID, itemID)
	if err != nil {
		return err
	}
	if versions == nil {
		versions = []service.VaultItemVersion{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(versions)
}

// parseBoolQuery accepts a generous set of truthy strings — UI form
// libraries serialize booleans inconsistently, and being strict here
// just produces confusing 400s.
func parseBoolQuery(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		// Fallback parse so a literal "false" / "0" returns false
		// rather than landing in the default branch by accident.
		v, err := strconv.ParseBool(s)
		if err != nil {
			return false
		}
		return v
	}
}
