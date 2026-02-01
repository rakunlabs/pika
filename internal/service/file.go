package service

import (
	"context"
	"errors"
	"path"
	"strconv"

	"github.com/rakunlabs/tummy"
)

// File represents a configuration file with metadata and data.
type File struct {
	Meta FileMeta `json:"meta"`
	Data []byte   `json:"data"`
}

type FileMeta struct {
}

type FileVersions []FileVersion

type FileVersion struct {
	Version int64        `json:"version"`
	Status  []FileStatus `json:"status"`
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

// SetFile saves a file to storage at the given path.
func (s *Service) SetFile(ctx context.Context, key string, data *File) error {
	// 1- create folder
	// 2- file versioning create
	// 3- save file data at versioned key

	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {

		if err := s.addFileToFolder(ctx, tx, key); err != nil {
			return err
		}

		// file versioning
		keyPath := path.Join(keyFile, key)
		fileVersionsData, err := tx.Get(ctx, keyPath)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}

		var fileVersions FileVersions
		if err := s.decodeBytes(fileVersionsData, &fileVersions); err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}

			// no versions yet
			fileVersions = FileVersions{}
		}

		var newVersion int64 = 1
		if len(fileVersions) > 0 {
			newVersion = fileVersions[len(fileVersions)-1].Version + 1
		}

		newFileVersion := FileVersion{
			Version: newVersion,
			Status: []FileStatus{
				{
					Status:    FileStatusTypeCreated,
					Timestamp: tummy.Now().Unix(),
					Author:    "system", // TODO: get author from context
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
					Author:    "system", // TODO: get author from context
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
