package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	pikaexternal "github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/service"
)

// settingsStorage implements service.SettingsStorage. Settings is a
// singleton (one row keyed by settingsSingletonID), so Get/Set don't take
// a path or version.
type settingsStorage struct {
	store  *Storage
	bucket *bw.Bucket[settingsRow]
	scope  scope
}

func (s *Storage) settingsAt(sc scope) *settingsStorage {
	return &settingsStorage{store: s, bucket: s.settings, scope: sc}
}

// Settings returns the storage outside any tx. Inside a tx, callers
// should use the txStorage variant.
func (s *Storage) Settings() service.SettingsStorage {
	return s.settingsAt(s.dbScope())
}

func (t *txStorage) Settings() service.SettingsStorage {
	return t.base.settingsAt(t.scope)
}

func (s *settingsStorage) Get(ctx context.Context) (*service.Settings, error) {
	row, err := s.getRow()
	if err != nil {
		return nil, err
	}
	return rowToSettings(row), nil
}

func (s *settingsStorage) getRow() (*settingsRow, error) {
	return bucketGet(context.Background(), s.scope, s.bucket, settingsSingletonID)
}

func (s *settingsStorage) Set(ctx context.Context, settings *service.Settings) error {
	return bucketInsert(ctx, s.scope, s.bucket, settingsToRow(settings))
}

func rowToSettings(r *settingsRow) *service.Settings {
	ext := r.External
	if ext == nil {
		ext = map[string]pikaexternal.External{}
	}
	_ = hook.Hook{} // keep import live (hooks slice is passed through verbatim)
	return &service.Settings{
		External:            ext,
		EncryptionVerifier:  r.EncryptionVerifier,
		RawMounts:           r.RawMounts,
		FTPShares:           r.FTPShares,
		FTPUsers:            r.FTPUsers,
		FTPServe:            r.FTPServe,
		SFTPServe:           r.SFTPServe,
		TFTPServe:           r.TFTPServe,
		WebDAVServe:         r.WebDAVServe,
		Hooks:               r.Hooks,
		PublicPort:          r.PublicPort,
		Compat:              r.Compat,
		ExternalPermissions: r.ExternalPermissions,
		ForwardAuth:         r.ForwardAuth,
		Auth:                r.Auth,
		UserSync:            r.UserSync,
		Vault:               r.Vault,
		SensitivePayload:    r.SensitivePayload,
	}
}

func settingsToRow(s *service.Settings) *settingsRow {
	return &settingsRow{
		ID:                  settingsSingletonID,
		External:            s.External,
		EncryptionVerifier:  s.EncryptionVerifier,
		RawMounts:           s.RawMounts,
		FTPShares:           s.FTPShares,
		FTPUsers:            s.FTPUsers,
		FTPServe:            s.FTPServe,
		SFTPServe:           s.SFTPServe,
		TFTPServe:           s.TFTPServe,
		WebDAVServe:         s.WebDAVServe,
		Hooks:               s.Hooks,
		PublicPort:          s.PublicPort,
		Compat:              s.Compat,
		ExternalPermissions: s.ExternalPermissions,
		ForwardAuth:         s.ForwardAuth,
		Auth:                s.Auth,
		UserSync:            s.UserSync,
		Vault:               s.Vault,
		SensitivePayload:    s.SensitivePayload,
	}
}
