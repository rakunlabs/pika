package bw

import (
	"context"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// userPreferencesRow is the bucket payload for one user's UI preferences.
// The bucket primary key is the user's ID so look-ups are O(1) and the
// row is self-contained (no joins, no junctions). Future user-scoped
// resources that fit a small JSON-blob shape (e.g. a personal password
// vault) can either land as additional sections on this row or as their
// own bucket — the choice is per-feature.
type userPreferencesRow struct {
	UserID           string    `bw:"user_id,pk"`
	AppTheme         string    `bw:"app_theme"`
	EditorTheme      string    `bw:"editor_theme"`
	EditorFontSize   int       `bw:"editor_font_size"`
	EditorFontFamily string    `bw:"editor_font_family"`
	EditorLineWrap   bool      `bw:"editor_line_wrap"`
	PanelLeftWidth   int       `bw:"panel_left_width"`
	PanelRightWidth  int       `bw:"panel_right_width"`
	UpdatedAt        time.Time `bw:"updated_at"`
}

func (r *userPreferencesRow) toService() *service.UserPreferences {
	return &service.UserPreferences{
		UserID: r.UserID,
		App: service.AppPreferences{
			Theme: r.AppTheme,
		},
		Editor: service.EditorPreferences{
			Theme:      r.EditorTheme,
			FontSize:   r.EditorFontSize,
			FontFamily: r.EditorFontFamily,
			LineWrap:   r.EditorLineWrap,
		},
		Panels: service.PanelPreferences{
			LeftWidth:  r.PanelLeftWidth,
			RightWidth: r.PanelRightWidth,
		},
		UpdatedAt: r.UpdatedAt,
	}
}

func userPreferencesRowFromService(p *service.UserPreferences) *userPreferencesRow {
	return &userPreferencesRow{
		UserID:           p.UserID,
		AppTheme:         p.App.Theme,
		EditorTheme:      p.Editor.Theme,
		EditorFontSize:   p.Editor.FontSize,
		EditorFontFamily: p.Editor.FontFamily,
		EditorLineWrap:   p.Editor.LineWrap,
		PanelLeftWidth:   p.Panels.LeftWidth,
		PanelRightWidth:  p.Panels.RightWidth,
		UpdatedAt:        p.UpdatedAt,
	}
}

// userPreferencesStorage implements service.UserPreferencesStorage.
type userPreferencesStorage struct {
	store  *Storage
	bucket *bw.Bucket[userPreferencesRow]
	scope  scope
}

func (s *Storage) userPreferencesAt(sc scope) *userPreferencesStorage {
	return &userPreferencesStorage{store: s, bucket: s.userPreferences, scope: sc}
}

func (s *Storage) UserPreferences() service.UserPreferencesStorage {
	return s.userPreferencesAt(s.dbScope())
}

func (t *txStorage) UserPreferences() service.UserPreferencesStorage {
	return t.base.userPreferencesAt(t.scope)
}

func (s *userPreferencesStorage) Get(ctx context.Context, userID string) (*service.UserPreferences, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, userID)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *userPreferencesStorage) Set(ctx context.Context, prefs *service.UserPreferences) error {
	return bucketInsert(ctx, s.scope, s.bucket, userPreferencesRowFromService(prefs))
}

func (s *userPreferencesStorage) Delete(ctx context.Context, userID string) error {
	return bucketDelete(ctx, s.scope, s.bucket, userID)
}
