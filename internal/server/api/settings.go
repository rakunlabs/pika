package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/service"
)

func (a *api) getSettings(c *ada.Context) error {
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}

	// Surface the effective auth config, not the raw DB row. When the
	// settings row has no auth.local block, the server's boot path
	// applies defaults (local strategy enabled); returning the bare DB
	// view would make the UI render "disabled" for the strategy the
	// user is literally using right now.
	if settings != nil {
		settings.Auth = settings.Auth.WithEffectiveDefaults()
		// Never ship stored auth secrets to the browser. Replace each with a
		// boolean "is set" indicator so the SPA can render "leave blank to
		// keep" / offer an explicit clear without ever holding the value.
		maskAuthSecrets(settings.Auth)
	}

	return c.SetStatus(http.StatusOK).SendJSON(settings)
}

// maskAuthSecrets strips stored auth secrets from a settings response,
// replacing OAuth2 client secrets with the ClientSecretSet indicator. Operates
// on the per-request settings copy returned by Settings(), so it never mutates
// stored state.
func maskAuthSecrets(a *service.AuthSettings) {
	if a == nil {
		return
	}
	for i := range a.OAuth2 {
		a.OAuth2[i].ClientSecretSet = a.OAuth2[i].ClientSecret != ""
		a.OAuth2[i].ClientSecret = ""
	}
}

func (a *api) getClusterStatus(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendJSON(a.cluster.Status())
}

func (a *api) postSettings(c *ada.Context) error {
	var patchSettings service.PatchSettings
	if err := c.Bind(&patchSettings); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.PatchSettings(c.Request.Context(), &patchSettings); err != nil {
		return err
	}

	// If hooks were updated, reload them in the dispatcher
	if patchSettings.Hooks != nil {
		a.reloadHooks(c.Request.Context())
	}
	if patchSettings.EventLog != nil {
		a.reloadEventLog(c.Request.Context())
	}

	// If auth settings were updated, reload the auth manager.
	// TODO: detect cookie/issuer changes and set restart_required in response.
	if patchSettings.Auth != nil && a.mgr != nil {
		// Reload from the freshly-persisted settings, not the request payload:
		// PatchSettings may have filled in kept secrets, and the seal layer
		// blanks the in-memory copy during persistence. Re-reading goes
		// through the decrypt path so live strategies get real secret values.
		reloadAuth := patchSettings.Auth
		if persisted, rerr := a.svc.Settings(c.Request.Context()); rerr == nil && persisted != nil && persisted.Auth != nil {
			reloadAuth = persisted.Auth
		}
		if err := a.mgr.Reload(c.Request.Context(), reloadAuth); err != nil {
			return fmt.Errorf("auth reload failed: %w", err)
		}
	}

	// If public endpoints were updated, reconcile the live listener
	// set. Bind failures don't fail the save — settings already
	// persisted, the operator can fix the port and try again. We
	// surface bind errors through GET /public-endpoints/status so
	// the UI can show a banner.
	if patchSettings.PublicEndpoints != nil && a.publicEndpoints != nil {
		// Re-read the persisted list so we apply the post-service
		// (validated, IDs filled, timestamps set) shape rather than
		// the raw client payload.
		settings, err := a.svc.Settings(c.Request.Context())
		if err == nil && settings != nil {
			if rerr := a.publicEndpoints.Reload(c.Request.Context(), settings.PublicEndpoints); rerr != nil {
				slog.Warn("public endpoints reload reported issues", "error", rerr)
			}
		}
	}

	// Don't echo secrets back to the browser. PatchSettings may have merged
	// kept secrets into the payload (and on the no-encryption path the seal
	// layer won't have blanked them), so mask before returning the echo.
	if patchSettings.Auth != nil {
		maskAuthSecrets(patchSettings.Auth)
	}

	return c.SetStatus(http.StatusOK).SendJSON(patchSettings)
}

// reloadEventLog applies the built-in event logging toggle without touching
// hook delivery targets.
func (a *api) reloadEventLog(ctx context.Context) {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		slog.Error("failed to read settings for event log reload", "error", err)
		return
	}

	if a.dispatcher != nil {
		enabled := settings.EventLogEnabled()
		a.dispatcher.SetEventLogEnabled(enabled)
		slog.Info("event log setting reloaded", "enabled", enabled)
	}
}

// reloadHooks reads hooks from settings and updates the dispatcher.
func (a *api) reloadHooks(ctx context.Context) {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		slog.Error("failed to read settings for hook reload", "error", err)
		return
	}

	if a.dispatcher != nil {
		a.dispatcher.UpdateHooks(settings.Hooks)
		slog.Info("hooks reloaded", "count", len(settings.Hooks))
	}
}

// BuildHookDispatcher creates the hook dispatcher and wires up the
// config-data resolver. Hooks operate purely on configuration events
// in this build — the rawfs/raw-mount references that used to feed
// the resolver were extracted out of pika; PEM references that
// pointed at raw mounts are no longer supported here.
func BuildHookDispatcher(ctx context.Context, svc *service.Service) *hook.Dispatcher {
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for hook dispatcher", "error", err)
	}

	dispatcher := hook.NewDispatcher(256)
	if settings != nil {
		dispatcher.SetEventLogEnabled(settings.EventLogEnabled())
	}
	dispatcher.Start(ctx)

	// Resolver: only `config://` references are resolvable now that
	// raw mounts live in a different repo. Leftover `raw://...`
	// references in existing hook configs pass through as inline
	// PEM text and fail naturally if not valid.
	resolver := hook.NewResolver(
		func(ctx context.Context, key string) ([]byte, error) {
			file, err := svc.File(ctx, key, 0)
			if err != nil {
				return nil, err
			}
			return file.Data, nil
		},
	)
	dispatcher.SetResolver(resolver)

	if settings != nil && len(settings.Hooks) > 0 {
		dispatcher.UpdateHooks(settings.Hooks)
	}
	return dispatcher
}
