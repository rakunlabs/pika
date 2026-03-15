package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"

	"github.com/rakunlabs/tummy"
)

// InheritEntry defines a single inheritance source.
type InheritEntry struct {
	// Source is the path to the parent config (internal path or external resource name).
	Source string `json:"source"`
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
	keyPath := path.Join(keyFile, filePath)

	// get last version if version is 0
	if version == 0 {
		fileVersions, err := s.FileInfo(ctx, keyPath)
		if err != nil {
			return nil, err
		}

		fileVersion, err := s.fileGetLatestValidVersion(fileVersions)
		if err != nil {
			return nil, err
		}

		version = fileVersion.Version
	}

	keyPath = path.Join(keyPath, strconv.FormatInt(version, 10))

	data, err := s.store.Get(ctx, keyPath)
	if err != nil {
		return nil, err
	}

	var file File
	if err := s.decodeBytes(data, &file); err != nil {
		return nil, err
	}

	return &file, nil
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

	keyPath := path.Join(keyFile, filePath)
	fileVersions, err := s.FileInfo(ctx, keyPath)
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

func (s *Service) FileInfo(ctx context.Context, keyPath string) (FileVersions, error) {
	data, err := s.store.Get(ctx, keyPath)
	if err != nil {
		return nil, err
	}

	var versions FileVersions
	if err := s.decodeBytes(data, &versions); err != nil {
		return nil, err
	}

	return versions, nil
}

// FileVersionsList returns the version history for a file at the given path.
func (s *Service) FileVersionsList(ctx context.Context, filePath string) (FileVersions, error) {
	keyPath := path.Join(keyFile, filePath)
	return s.FileInfo(ctx, keyPath)
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
		keyPath := path.Join(keyFile, key)
		var fileVersions FileVersions

		fileVersionsData, err := tx.Get(ctx, keyPath)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			// no versions yet
			fileVersions = FileVersions{}
		} else {
			if err := s.decodeBytes(fileVersionsData, &fileVersions); err != nil {
				return err
			}
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

		encodedVersions, err := s.encodeBytes(fileVersions)
		if err != nil {
			return err
		}

		// save updated versions
		if err := tx.Set(ctx, keyPath, encodedVersions); err != nil {
			return err
		}

		// save file data at versioned key
		keyPath = path.Join(keyPath, strconv.FormatInt(newVersion, 10))
		encodedData, err := s.encodeBytes(data)
		if err != nil {
			return err
		}

		return tx.Set(ctx, keyPath, encodedData)
	})

	return createdVersion, err
}

// DeleteFile deletes a file from storage at the given path.
//   - if version is 0, delete completely
func (s *Service) DeleteFile(ctx context.Context, key string, version int64) error {
	keyPath := path.Join(keyFile, key)

	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		if version == 0 {
			// delete completely
			// Remove file from folder
			if err := s.removeFileFromFolder(ctx, tx, key); err != nil {
				return err
			}

			// Delete all versions
			data, err := tx.Get(ctx, keyPath)
			if err != nil {
				if err == ErrNotFound {
					return nil
				}
				return err
			}

			var versions FileVersions
			if err := s.decodeBytes(data, &versions); err != nil {
				return err
			}

			for _, v := range versions {
				versionedKeyPath := path.Join(keyPath, strconv.FormatInt(v.Version, 10))
				if err := tx.Delete(ctx, versionedKeyPath); err != nil {
					return err
				}
			}

			// Delete versions metadata
			return tx.Delete(ctx, keyPath)
		}

		// Mark version as deleted
		data, err := tx.Get(ctx, keyPath)
		if err != nil {
			return err
		}

		var versions FileVersions
		if err := s.decodeBytes(data, &versions); err != nil {
			return err
		}

		var versionFound bool
		for i, v := range versions {
			if v.Version == version {
				versionFound = true
				versions[i].Status = append(versions[i].Status, FileStatus{
					Status:    FileStatusTypeDeleted,
					Timestamp: tummy.Now().Unix(),
					Author:    UserFromContext(ctx),
				})
				break
			}
		}

		if versionFound {
			encodedVersions, err := s.encodeBytes(versions)
			if err != nil {
				return err
			}

			if err := tx.Set(ctx, keyPath, encodedVersions); err != nil {
				return err
			}
		}

		// Delete file data at versioned key
		versionedKeyPath := path.Join(keyPath, strconv.FormatInt(version, 10))
		return tx.Delete(ctx, versionedKeyPath)
	})
}
