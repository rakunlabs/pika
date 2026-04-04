package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/secret/crypto"
)

// BackupData represents the full backup payload.
type BackupData struct {
	Version      int                 `json:"version"`
	CreatedAt    time.Time           `json:"created_at"`
	Settings     *BackupSettings     `json:"settings,omitempty"`
	Folders      []BackupFolder      `json:"folders,omitempty"`
	FileVersions []BackupFileVersion `json:"file_versions,omitempty"`
	Files        []BackupFile        `json:"files,omitempty"`
}

// BackupSettings holds the settings data for backup (excludes admin_secret_hash).
type BackupSettings struct {
	External map[string]external.External `json:"external,omitempty"`
}

// BackupFolder represents a folder entry in the backup.
type BackupFolder struct {
	Path     string              `json:"path"`
	Folders  []string            `json:"folders"`
	Files    []string            `json:"files"`
	Variants map[string][]string `json:"variants,omitempty"`
}

// BackupFileVersion represents file version metadata in the backup.
type BackupFileVersion struct {
	Path     string       `json:"path"`
	Versions FileVersions `json:"versions"`
}

// BackupFile represents a single file (path + version) in the backup.
type BackupFile struct {
	Path    string   `json:"path"`
	Version int64    `json:"version"`
	Meta    FileMeta `json:"meta"`
	Data    []byte   `json:"data"`
}

const (
	BackupVersion = 1

	ImportModeReplace = "replace"
	ImportModeMerge   = "merge"
)

// Export reads all configuration data and assembles a BackupData struct.
func (s *Service) Export(ctx context.Context) (*BackupData, error) {
	backup := &BackupData{
		Version:   BackupVersion,
		CreatedAt: time.Now().UTC(),
	}

	// Export settings (without admin secret hash)
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("exporting settings: %w", err)
	}
	backup.Settings = &BackupSettings{
		External: settings.External,
	}

	// Export folders
	folderEntries, err := s.store.Folders().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("exporting folders: %w", err)
	}
	for _, entry := range folderEntries {
		backup.Folders = append(backup.Folders, BackupFolder{
			Path:     entry.Path,
			Folders:  entry.Folder.Folders,
			Files:    entry.Folder.Files,
			Variants: entry.Folder.Variants,
		})
	}

	// Export file versions
	fvEntries, err := s.store.FileVersions().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("exporting file versions: %w", err)
	}
	for _, entry := range fvEntries {
		backup.FileVersions = append(backup.FileVersions, BackupFileVersion{
			Path:     entry.Path,
			Versions: entry.Versions,
		})
	}

	// Export files
	fileEntries, err := s.store.Files().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("exporting files: %w", err)
	}
	for _, entry := range fileEntries {
		data := entry.File.Data
		if data == nil {
			data = []byte{}
		}
		backup.Files = append(backup.Files, BackupFile{
			Path:    entry.Path,
			Version: entry.Version,
			Meta:    entry.File.Meta,
			Data:    data,
		})
	}

	return backup, nil
}

// Import restores configuration data from a BackupData struct.
// mode is either "replace" (wipe existing data first) or "merge" (upsert on top of existing).
func (s *Service) Import(ctx context.Context, backup *BackupData, mode string) error {
	if backup == nil {
		return fmt.Errorf("backup data is nil: %w", ErrBadRequest)
	}
	if mode != ImportModeReplace && mode != ImportModeMerge {
		return fmt.Errorf("invalid import mode %q, must be %q or %q: %w", mode, ImportModeReplace, ImportModeMerge, ErrBadRequest)
	}

	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		// In replace mode, clear all existing config data first
		if mode == ImportModeReplace {
			if err := tx.Files().DeleteAll(ctx); err != nil {
				return fmt.Errorf("clearing files: %w", err)
			}
			if err := tx.FileVersions().DeleteAll(ctx); err != nil {
				return fmt.Errorf("clearing file versions: %w", err)
			}
			if err := tx.Folders().DeleteAll(ctx); err != nil {
				return fmt.Errorf("clearing folders: %w", err)
			}
		}

		// Import settings (merge external resources, preserve admin_secret_hash)
		if backup.Settings != nil && len(backup.Settings.External) > 0 {
			currentSettings, err := s.settingsFromStorage(ctx, tx)
			if err != nil {
				return fmt.Errorf("reading current settings: %w", err)
			}

			if mode == ImportModeReplace {
				currentSettings.External = backup.Settings.External
			} else {
				// Merge: add/overwrite external entries
				if currentSettings.External == nil {
					currentSettings.External = make(map[string]external.External)
				}
				for k, v := range backup.Settings.External {
					currentSettings.External[k] = v
				}
			}

			if err := tx.Settings().Set(ctx, currentSettings); err != nil {
				return fmt.Errorf("saving settings: %w", err)
			}
		}

		// Import folders
		for _, bf := range backup.Folders {
			folder := &Folder{
				Folders:  bf.Folders,
				Files:    bf.Files,
				Variants: bf.Variants,
			}
			if err := tx.Folders().Set(ctx, bf.Path, folder); err != nil {
				return fmt.Errorf("importing folder %q: %w", bf.Path, err)
			}
		}

		// Import file versions
		for _, bfv := range backup.FileVersions {
			if err := tx.FileVersions().Set(ctx, bfv.Path, bfv.Versions); err != nil {
				return fmt.Errorf("importing file versions %q: %w", bfv.Path, err)
			}
		}

		// Import files
		for _, bf := range backup.Files {
			data := bf.Data
			if data == nil {
				data = []byte{}
			}
			file := &File{
				Meta: bf.Meta,
				Data: data,
			}
			if err := tx.Files().Set(ctx, bf.Path, bf.Version, file); err != nil {
				return fmt.Errorf("importing file %q v%d: %w", bf.Path, bf.Version, err)
			}
		}

		return nil
	})
}

// settingsFromStorage reads settings using the provided storage (for use within transactions).
func (s *Service) settingsFromStorage(ctx context.Context, tx Storage) (*Settings, error) {
	settings, err := tx.Settings().Get(ctx)
	if err != nil {
		if isNotFound(err) {
			return &Settings{
				External: make(map[string]external.External),
			}, nil
		}
		return nil, err
	}
	return settings, nil
}

// isNotFound checks if the error is ErrNotFound.
func isNotFound(err error) bool {
	return err == ErrNotFound
}

// EncryptedBackup is a JSON envelope for encrypted backup payloads.
type EncryptedBackup struct {
	Encrypted bool   `json:"encrypted"`
	Version   int    `json:"version"`
	Data      string `json:"data"` // base64-encoded ciphertext
}

// ExportEncrypted exports backup data and encrypts it with the given password.
// The password is hashed with SHA-256 to derive a 32-byte key, then encrypted
// with XChaCha20-Poly1305.
func (s *Service) ExportEncrypted(ctx context.Context, password string) (*EncryptedBackup, error) {
	backup, err := s.Export(ctx)
	if err != nil {
		return nil, err
	}

	plaintext, err := json.Marshal(backup)
	if err != nil {
		return nil, fmt.Errorf("marshalling backup data: %w", err)
	}

	key := sha256.Sum256([]byte(password))
	encryptor, err := crypto.NewChaCha20(key[:])
	if err != nil {
		return nil, fmt.Errorf("creating encryptor: %w", err)
	}

	ciphertext, err := encryptor.Encrypt(plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypting backup: %w", err)
	}

	return &EncryptedBackup{
		Encrypted: true,
		Version:   BackupVersion,
		Data:      base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

// DecryptBackupData decrypts an EncryptedBackup envelope and returns the BackupData.
func DecryptBackupData(envelope *EncryptedBackup, password string) (*BackupData, error) {
	if password == "" {
		return nil, fmt.Errorf("encryption password is required for encrypted backups: %w", ErrBadRequest)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("decoding backup data: %w", ErrBadRequest)
	}

	key := sha256.Sum256([]byte(password))
	encryptor, err := crypto.NewChaCha20(key[:])
	if err != nil {
		return nil, fmt.Errorf("creating decryptor: %w", err)
	}

	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypting backup (wrong password?): %w", ErrBadRequest)
	}

	var backup BackupData
	if err := json.Unmarshal(plaintext, &backup); err != nil {
		return nil, fmt.Errorf("parsing decrypted backup: %w", err)
	}

	return &backup, nil
}
