package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/tummy"
)

// InheritEntry defines a single inheritance source.
//
// For internal files: Source is the file path (e.g., "base/database").
// For external resources: Resource is the settings key (e.g., "my-vault"),
// and Path is the resource-specific path (e.g., "myapp/database" for Vault secrets
// or "/api/config" for HTTP endpoints).
type InheritEntry struct {
	// Source is the internal file path. Used for internal inheritance.
	Source string `json:"source,omitempty"`
	// Resource is the name of an external resource from settings.
	Resource string `json:"resource,omitempty"`
	// Path is the resource-specific path when using an external resource.
	// For Vault: the secret path under the mount (e.g., "myapp/database").
	// For HTTP: appended to the base URL (e.g., "/api/config").
	Path string `json:"path,omitempty"`
	// Paths selectively includes only these fields from the source.
	// Supports dot-notation (e.g., "database.host") and wildcards ("logging.*").
	// If empty, all fields are included.
	Paths []string `json:"paths,omitempty"`
	// Inject places the inherited data under this key path in the config.
	// Supports dot-notation (e.g., "database.auth" injects under config.database.auth).
	// If empty, data is merged at the root level.
	Inject string `json:"inject,omitempty"`
}

// FileMeta holds metadata about a configuration file.
type FileMeta struct {
	Description string         `json:"description,omitempty"`
	Format      string         `json:"format,omitempty"`
	Inherits    []InheritEntry `json:"inherits,omitempty"`
}

// File represents a configuration file with metadata and data.
type File struct {
	Meta FileMeta `json:"meta"`
	Data []byte   `json:"data"`
}

type FileVersions []FileVersion

type FileVersion struct {
	Version    int64        `json:"version"`
	Status     []FileStatus `json:"status"`
	Constraint string       `json:"constraint,omitempty"` // semver constraint, e.g., ">= 0.2.5"
}

type FileStatus struct {
	Status    FileStatusType `json:"status"`
	Timestamp int64          `json:"timestamp"`
	Author    string         `json:"author"`
}

type FileStatusType string

const (
	FileStatusTypeCreated FileStatusType = "CREATED"
	FileStatusTypeDeleted FileStatusType = "DELETED"
)

// File retrieves a file from storage at the given path.
//   - version 0 returns the latest version.
func (s *Service) File(ctx context.Context, filePath string, version int64) (*File, error) {
	// get last version if version is 0
	if version == 0 {
		fileVersions, err := s.FileInfo(ctx, filePath)
		if err != nil {
			return nil, err
		}

		fileVersion, err := s.fileGetLatestValidVersion(fileVersions)
		if err != nil {
			return nil, err
		}

		version = fileVersion.Version
	}

	return s.store.Files().Get(ctx, filePath, version)
}

// FileByVersion retrieves a file using a version string.
// The version can be:
//   - "" or "0": latest version
//   - A plain integer (e.g., "7"): exact internal version
//   - A semver string (e.g., "0.2.4"): resolve using semver constraints
func (s *Service) FileByVersion(ctx context.Context, filePath string, versionStr string) (*File, error) {
	if versionStr == "" || versionStr == "0" {
		return s.File(ctx, filePath, 0)
	}

	// Try plain integer first
	if v, err := strconv.ParseInt(versionStr, 10, 64); err == nil {
		return s.File(ctx, filePath, v)
	}

	// Must be a semver string
	if !IsSemverString(versionStr) {
		return nil, fmt.Errorf("invalid version %q: %w", versionStr, ErrBadRequest)
	}

	fileVersions, err := s.FileInfo(ctx, filePath)
	if err != nil {
		return nil, err
	}

	resolved, err := ResolveVersionBySemver(fileVersions, versionStr)
	if err != nil {
		return nil, err
	}

	return s.File(ctx, filePath, resolved.Version)
}

func (s *Service) fileGetLatestValidVersion(versions FileVersions) (*FileVersion, error) {
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		// check if the latest status is deleted
		if len(v.Status) > 0 && v.Status[len(v.Status)-1].Status == FileStatusTypeDeleted {
			continue
		}
		return &v, nil
	}

	return nil, ErrNotFound
}

func (s *Service) FileInfo(ctx context.Context, filePath string) (FileVersions, error) {
	return s.store.FileVersions().Get(ctx, filePath)
}

// FileVersionsList returns the version history for a file at the given path.
func (s *Service) FileVersionsList(ctx context.Context, filePath string) (FileVersions, error) {
	return s.FileInfo(ctx, filePath)
}

// SetFile saves a file to storage at the given path.
// If expectedVersion is not nil, the save is rejected with ErrConflict
// when the current latest version doesn't match (optimistic concurrency).
// constraint is an optional semver constraint for this version (e.g., ">= 0.2.5").
// Returns the version number that was created.
func (s *Service) SetFile(ctx context.Context, key string, data *File, expectedVersion *int64, constraint string) (int64, error) {
	// 1- create folder
	// 2- file versioning create (with concurrency check)
	// 3- save file data at versioned key

	var createdVersion int64

	err := s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {

		if err := s.addFileToFolder(ctx, tx, key); err != nil {
			return err
		}

		// file versioning
		var fileVersions FileVersions

		existing, err := tx.FileVersions().Get(ctx, key)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			// no versions yet
			fileVersions = FileVersions{}
		} else {
			fileVersions = existing
		}

		var newVersion int64 = 1
		if len(fileVersions) > 0 {
			latestVersion := fileVersions[len(fileVersions)-1].Version
			newVersion = latestVersion + 1

			// Optimistic concurrency check
			if expectedVersion != nil && latestVersion != *expectedVersion {
				return fmt.Errorf("expected version %d but latest is %d: %w",
					*expectedVersion, latestVersion, ErrConflict)
			}
		}

		createdVersion = newVersion

		newFileVersion := FileVersion{
			Version:    newVersion,
			Constraint: constraint,
			Status: []FileStatus{
				{
					Status:    FileStatusTypeCreated,
					Timestamp: tummy.Now().Unix(),
					Author:    UserFromContext(ctx),
				},
			},
		}

		fileVersions = append(fileVersions, newFileVersion)

		// save updated versions
		if err := tx.FileVersions().Set(ctx, key, fileVersions); err != nil {
			return err
		}

		// save file data at versioned key
		return tx.Files().Set(ctx, key, newVersion, data)
	})

	if err == nil {
		s.emitHook(hook.Event{
			Type:          hook.EventConfigCreated,
			ConfigKey:     key,
			ConfigVersion: createdVersion,
			User:          UserFromContext(ctx),
		})
	}

	return createdVersion, err
}

// DeleteFile deletes a file from storage at the given path.
//   - if version is 0, delete completely
func (s *Service) DeleteFile(ctx context.Context, key string, version int64) error {
	err := s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		if version == 0 {
			// delete completely
			// Remove file from folder
			if err := s.removeFileFromFolder(ctx, tx, key); err != nil {
				return err
			}

			// Delete all file versions data
			if err := tx.Files().DeleteAllVersions(ctx, key); err != nil {
				return err
			}

			// Delete versions metadata
			return tx.FileVersions().Delete(ctx, key)
		}

		// Mark version as deleted
		fv, err := tx.FileVersions().Get(ctx, key)
		if err != nil {
			return err
		}

		var versionFound bool
		for i, v := range fv {
			if v.Version == version {
				versionFound = true
				fv[i].Status = append(fv[i].Status, FileStatus{
					Status:    FileStatusTypeDeleted,
					Timestamp: tummy.Now().Unix(),
					Author:    UserFromContext(ctx),
				})
				break
			}
		}

		if versionFound {
			if err := tx.FileVersions().Set(ctx, key, fv); err != nil {
				return err
			}
		}

		// Delete file data at versioned key
		return tx.Files().Delete(ctx, key, version)
	})

	if err == nil {
		s.emitHook(hook.Event{
			Type:          hook.EventConfigDeleted,
			ConfigKey:     key,
			ConfigVersion: version,
			User:          UserFromContext(ctx),
		})
	}

	return err
}

// UpdateConstraint updates the semver constraint on an existing version.
// If constraint is empty, the constraint is removed.
func (s *Service) UpdateConstraint(ctx context.Context, key string, version int64, constraint string) error {
	// Validate constraint format if non-empty
	if constraint != "" {
		if _, _, err := ParseConstraint(constraint); err != nil {
			return fmt.Errorf("invalid constraint %q: %w", constraint, ErrBadRequest)
		}
	}

	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		fv, err := tx.FileVersions().Get(ctx, key)
		if err != nil {
			return err
		}

		var found bool
		for i, v := range fv {
			if v.Version == version {
				found = true
				fv[i].Constraint = constraint
				break
			}
		}

		if !found {
			return fmt.Errorf("version %d not found: %w", version, ErrNotFound)
		}

		return tx.FileVersions().Set(ctx, key, fv)
	})
}
