package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Default values returned when a user has no stored preferences yet, and
// the floor/ceiling applied to inputs via the public update API.
const (
	DefaultAppTheme         = "system"
	DefaultEditorTheme      = "one-dark"
	DefaultEditorFontSize   = 13
	// Geist Mono is the canonical default monospace; the trailing
	// stack keeps fallback rendering sensible if the browser hasn't
	// pulled the @fontsource bundle yet or on older clients that
	// don't ship Geist Mono. Mirrored in
	// _ui/src/lib/store/prefs.svelte.ts (DEFAULT_EDITOR_FONT_FAMILY).
	DefaultEditorFontFamily = "'Geist Mono', 'JetBrains Mono', Fira Code, Menlo, Monaco, monospace"
	DefaultEditorLineWrap   = false
	DefaultPanelLeftWidth   = 250
	DefaultPanelRightWidth  = 280
	minEditorFontSize       = 8
	maxEditorFontSize       = 64
	minPanelWidth           = 120
	maxPanelWidth           = 800
)

// KnownAppThemes is the closed set of accepted app theme keys.
var KnownAppThemes = []string{"light", "dark", "system"}

// KnownEditorThemes is the closed set of accepted editor theme keys. The
// UI ships exactly these via _ui/src/lib/editor/themes.ts; the backend
// validates against the same list so an unknown value is rejected at the
// API boundary rather than silently swallowed.
var KnownEditorThemes = []string{
	"default-light",
	"one-dark",
	"github-light",
	"github-dark",
	"dracula",
	"solarized-light",
	"solarized-dark",
	"nord",
	"monokai",
	"gruvbox-light",
	"gruvbox-dark",
}

// DefaultUserPreferences returns the canonical defaults for a user with
// no stored document. Callers must populate UserID and UpdatedAt.
func DefaultUserPreferences() UserPreferences {
	return UserPreferences{
		App: AppPreferences{
			Theme: DefaultAppTheme,
		},
		Editor: EditorPreferences{
			Theme:      DefaultEditorTheme,
			FontSize:   DefaultEditorFontSize,
			FontFamily: DefaultEditorFontFamily,
			LineWrap:   DefaultEditorLineWrap,
		},
		Panels: PanelPreferences{
			LeftWidth:  DefaultPanelLeftWidth,
			RightWidth: DefaultPanelRightWidth,
		},
	}
}

// UserPreferencesPatch is the partial-update payload accepted by
// UpdateUserPreferences. Any nil section is left untouched. Within a
// section, nil fields are also left untouched — this is what lets the
// UI persist (for example) only the editor font size without having to
// re-send the rest of the preference document.
type UserPreferencesPatch struct {
	App    *AppPreferencesPatch    `json:"app,omitempty"`
	Editor *EditorPreferencesPatch `json:"editor,omitempty"`
	Panels *PanelPreferencesPatch  `json:"panels,omitempty"`
}

type AppPreferencesPatch struct {
	Theme *string `json:"theme,omitempty"`
}

type EditorPreferencesPatch struct {
	Theme      *string `json:"theme,omitempty"`
	FontSize   *int    `json:"font_size,omitempty"`
	FontFamily *string `json:"font_family,omitempty"`
	LineWrap   *bool   `json:"line_wrap,omitempty"`
}

type PanelPreferencesPatch struct {
	LeftWidth  *int `json:"left_width,omitempty"`
	RightWidth *int `json:"right_width,omitempty"`
}

// GetUserPreferences returns the stored preferences for the user, or the
// defaults (with UserID populated) when none exist yet. It never returns
// ErrNotFound — a fresh user is indistinguishable from one who has not
// changed any preference, which simplifies the UI contract.
func (s *Service) GetUserPreferences(ctx context.Context, userID string) (*UserPreferences, error) {
	if userID == "" {
		return nil, fmt.Errorf("user id required: %w", ErrBadRequest)
	}

	prefs, err := s.store.UserPreferences().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			def := DefaultUserPreferences()
			def.UserID = userID
			return &def, nil
		}
		return nil, err
	}
	// Defensive: in case an older record predates a field we now expect,
	// fill missing values with defaults so the UI always sees a complete
	// document.
	fillDefaults(prefs)
	return prefs, nil
}

// UpdateUserPreferences applies the patch to the user's preferences and
// stores the merged document. Missing fields keep their previous value
// (or the default if the user had no record). The returned value is the
// merged document as persisted.
func (s *Service) UpdateUserPreferences(ctx context.Context, userID string, patch *UserPreferencesPatch) (*UserPreferences, error) {
	if userID == "" {
		return nil, fmt.Errorf("user id required: %w", ErrBadRequest)
	}
	if patch == nil {
		// Treat a nil patch as a fetch — no-op write would still bump
		// UpdatedAt for no reason.
		return s.GetUserPreferences(ctx, userID)
	}

	current, err := s.GetUserPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}

	if patch.App != nil {
		if patch.App.Theme != nil {
			t := strings.ToLower(strings.TrimSpace(*patch.App.Theme))
			if !contains(KnownAppThemes, t) {
				return nil, fmt.Errorf("invalid app theme %q: %w", t, ErrBadRequest)
			}
			current.App.Theme = t
		}
	}

	if patch.Editor != nil {
		if patch.Editor.Theme != nil {
			t := strings.ToLower(strings.TrimSpace(*patch.Editor.Theme))
			if !contains(KnownEditorThemes, t) {
				return nil, fmt.Errorf("invalid editor theme %q: %w", t, ErrBadRequest)
			}
			current.Editor.Theme = t
		}
		if patch.Editor.FontSize != nil {
			fs := *patch.Editor.FontSize
			if fs < minEditorFontSize || fs > maxEditorFontSize {
				return nil, fmt.Errorf("editor font_size %d outside [%d,%d]: %w", fs, minEditorFontSize, maxEditorFontSize, ErrBadRequest)
			}
			current.Editor.FontSize = fs
		}
		if patch.Editor.FontFamily != nil {
			ff := strings.TrimSpace(*patch.Editor.FontFamily)
			if ff == "" {
				return nil, fmt.Errorf("editor font_family must be non-empty: %w", ErrBadRequest)
			}
			current.Editor.FontFamily = ff
		}
		if patch.Editor.LineWrap != nil {
			current.Editor.LineWrap = *patch.Editor.LineWrap
		}
	}

	if patch.Panels != nil {
		if patch.Panels.LeftWidth != nil {
			w := *patch.Panels.LeftWidth
			if w < minPanelWidth || w > maxPanelWidth {
				return nil, fmt.Errorf("panels.left_width %d outside [%d,%d]: %w", w, minPanelWidth, maxPanelWidth, ErrBadRequest)
			}
			current.Panels.LeftWidth = w
		}
		if patch.Panels.RightWidth != nil {
			w := *patch.Panels.RightWidth
			if w < minPanelWidth || w > maxPanelWidth {
				return nil, fmt.Errorf("panels.right_width %d outside [%d,%d]: %w", w, minPanelWidth, maxPanelWidth, ErrBadRequest)
			}
			current.Panels.RightWidth = w
		}
	}

	current.UserID = userID
	current.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)

	if err := s.store.UserPreferences().Set(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

// ResetUserPreferences deletes the user's stored document so subsequent
// reads fall back to defaults. Deleting a non-existent record is not an
// error — the operation is idempotent.
func (s *Service) ResetUserPreferences(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("user id required: %w", ErrBadRequest)
	}
	if err := s.store.UserPreferences().Delete(ctx, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// fillDefaults populates zero-valued fields on a loaded preferences
// document so the API contract always returns a complete shape.
func fillDefaults(p *UserPreferences) {
	def := DefaultUserPreferences()
	if p.App.Theme == "" {
		p.App.Theme = def.App.Theme
	}
	if p.Editor.Theme == "" {
		p.Editor.Theme = def.Editor.Theme
	}
	if p.Editor.FontSize == 0 {
		p.Editor.FontSize = def.Editor.FontSize
	}
	if p.Editor.FontFamily == "" {
		p.Editor.FontFamily = def.Editor.FontFamily
	}
	if p.Panels.LeftWidth == 0 {
		p.Panels.LeftWidth = def.Panels.LeftWidth
	}
	if p.Panels.RightWidth == 0 {
		p.Panels.RightWidth = def.Panels.RightWidth
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
