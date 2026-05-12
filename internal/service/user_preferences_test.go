package service_test

import (
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func TestGetUserPreferences_returnsDefaultsWhenAbsent(t *testing.T) {
	svc := newTestService(t)
	id := createUserHelper(t, svc, "alice")

	prefs, err := svc.GetUserPreferences(t.Context(), id)
	if err != nil {
		t.Fatalf("GetUserPreferences: %v", err)
	}
	if prefs.UserID != id {
		t.Errorf("UserID = %q, want %q", prefs.UserID, id)
	}
	if prefs.App.Theme != service.DefaultAppTheme {
		t.Errorf("App.Theme = %q, want default %q", prefs.App.Theme, service.DefaultAppTheme)
	}
	if prefs.Editor.Theme != service.DefaultEditorTheme {
		t.Errorf("Editor.Theme = %q, want default %q", prefs.Editor.Theme, service.DefaultEditorTheme)
	}
	if prefs.Editor.FontSize != service.DefaultEditorFontSize {
		t.Errorf("Editor.FontSize = %d, want %d", prefs.Editor.FontSize, service.DefaultEditorFontSize)
	}
}

func TestUpdateUserPreferences_persistsAndMergesPartial(t *testing.T) {
	svc := newTestService(t)
	id := createUserHelper(t, svc, "bob")

	// First update: change only the editor font size; everything else
	// should retain its default.
	newSize := 18
	merged, err := svc.UpdateUserPreferences(t.Context(), id, &service.UserPreferencesPatch{
		Editor: &service.EditorPreferencesPatch{FontSize: &newSize},
	})
	if err != nil {
		t.Fatalf("UpdateUserPreferences: %v", err)
	}
	if merged.Editor.FontSize != newSize {
		t.Errorf("font size not applied: %d", merged.Editor.FontSize)
	}
	if merged.Editor.Theme != service.DefaultEditorTheme {
		t.Errorf("unrelated field touched: %q", merged.Editor.Theme)
	}

	// Second update: change only the app theme.
	newAppTheme := "dark"
	merged, err = svc.UpdateUserPreferences(t.Context(), id, &service.UserPreferencesPatch{
		App: &service.AppPreferencesPatch{Theme: &newAppTheme},
	})
	if err != nil {
		t.Fatalf("UpdateUserPreferences app: %v", err)
	}
	if merged.App.Theme != "dark" {
		t.Errorf("app theme = %q, want dark", merged.App.Theme)
	}
	// Font size from the previous update must still be preserved.
	if merged.Editor.FontSize != newSize {
		t.Errorf("font size regressed: %d", merged.Editor.FontSize)
	}

	// Confirm the document is persisted: a fresh Get returns the merged
	// state, not the defaults.
	fetched, err := svc.GetUserPreferences(t.Context(), id)
	if err != nil {
		t.Fatalf("GetUserPreferences: %v", err)
	}
	if fetched.App.Theme != "dark" || fetched.Editor.FontSize != newSize {
		t.Errorf("not persisted: %+v", fetched)
	}
}

func TestUpdateUserPreferences_rejectsInvalidValues(t *testing.T) {
	svc := newTestService(t)
	id := createUserHelper(t, svc, "carol")

	cases := []struct {
		name  string
		patch *service.UserPreferencesPatch
	}{
		{"unknown app theme", &service.UserPreferencesPatch{App: &service.AppPreferencesPatch{Theme: ptr("hyperdark")}}},
		{"unknown editor theme", &service.UserPreferencesPatch{Editor: &service.EditorPreferencesPatch{Theme: ptr("totally-not-a-theme")}}},
		{"font size too small", &service.UserPreferencesPatch{Editor: &service.EditorPreferencesPatch{FontSize: ptr(2)}}},
		{"font size too large", &service.UserPreferencesPatch{Editor: &service.EditorPreferencesPatch{FontSize: ptr(99)}}},
		{"empty font family", &service.UserPreferencesPatch{Editor: &service.EditorPreferencesPatch{FontFamily: ptr("   ")}}},
		{"left width too small", &service.UserPreferencesPatch{Panels: &service.PanelPreferencesPatch{LeftWidth: ptr(50)}}},
		{"right width too large", &service.UserPreferencesPatch{Panels: &service.PanelPreferencesPatch{RightWidth: ptr(2000)}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdateUserPreferences(t.Context(), id, tc.patch)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, service.ErrBadRequest) {
				t.Errorf("expected ErrBadRequest, got %v", err)
			}
		})
	}
}

func TestResetUserPreferences_idempotent(t *testing.T) {
	svc := newTestService(t)
	id := createUserHelper(t, svc, "dan")

	// Reset before any write is fine.
	if err := svc.ResetUserPreferences(t.Context(), id); err != nil {
		t.Fatalf("first reset: %v", err)
	}

	// Write something then reset; subsequent Get returns defaults.
	if _, err := svc.UpdateUserPreferences(t.Context(), id, &service.UserPreferencesPatch{
		App: &service.AppPreferencesPatch{Theme: ptr("dark")},
	}); err != nil {
		t.Fatalf("UpdateUserPreferences: %v", err)
	}
	if err := svc.ResetUserPreferences(t.Context(), id); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	prefs, err := svc.GetUserPreferences(t.Context(), id)
	if err != nil {
		t.Fatalf("GetUserPreferences: %v", err)
	}
	if prefs.App.Theme != service.DefaultAppTheme {
		t.Errorf("post-reset theme = %q, want default", prefs.App.Theme)
	}

	// Double reset is still OK.
	if err := svc.ResetUserPreferences(t.Context(), id); err != nil {
		t.Fatalf("third reset: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
