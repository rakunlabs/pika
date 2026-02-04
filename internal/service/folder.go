package service

import (
	"context"
	"errors"
	"path"
	"slices"
)

// Folder retrieves the folder structure at the given path.
// Returns immediate subfolders and files only.
func (s *Service) Folder(ctx context.Context, folderPath string) (*Folder, error) {
	keyPath := path.Join(keyFolder, folderPath)
	data, err := s.store.Get(ctx, keyPath)
	if err != nil {
		return nil, err
	}

	var folder Folder
	if err := s.decodeBytes(data, &folder); err != nil {
		return nil, err
	}

	return &folder, nil
}

func (s *Service) SetFolder(ctx context.Context, folderPath string) error {
	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		return s.ensureFolderExists(ctx, tx, folderPath)
	})
}

func (s *Service) addFileToFolder(ctx context.Context, tx Storage, filePath string) error {
	folderPath := path.Dir(filePath)
	fileName := path.Base(filePath)

	// Normalize root folder path
	if folderPath == "." || folderPath == "/" {
		folderPath = ""
	}

	// Ensure all parent folders exist (this also creates root if needed)
	if err := s.ensureFolderExists(ctx, tx, folderPath); err != nil {
		return err
	}

	// Get the current folder data
	keyPath := path.Join(keyFolder, folderPath)
	var folder Folder

	data, err := tx.Get(ctx, keyPath)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		// Folder doesn't exist, create empty one
		folder = Folder{}
	} else {
		if err := s.decodeBytes(data, &folder); err != nil {
			return err
		}
	}

	// Check if file already exists in folder
	for _, f := range folder.Files {
		if f == fileName {
			return nil // File already in folder
		}
	}

	// Add file to folder
	folder.Files = append(folder.Files, fileName)

	// Save the folder
	encoded, err := s.encodeBytes(folder)
	if err != nil {
		return err
	}

	return tx.Set(ctx, keyPath, encoded)
}

// ensureFolderExists creates the folder and all parent folders if they don't exist.
func (s *Service) ensureFolderExists(ctx context.Context, tx Storage, folderPath string) error {
	// Normalize root folder representations
	isRoot := folderPath == "" || folderPath == "." || folderPath == "/"
	if isRoot {
		folderPath = ""
	}

	// First ensure parent folder exists (only if not root)
	parentPath := path.Dir(folderPath)
	parentIsRoot := parentPath == "" || parentPath == "." || parentPath == "/"
	if !isRoot && parentPath != folderPath && !parentIsRoot {
		if err := s.ensureFolderExists(ctx, tx, parentPath); err != nil {
			return err
		}
	}

	// Check if this folder exists
	keyPath := path.Join(keyFolder, folderPath)
	var folder Folder

	data, err := tx.Get(ctx, keyPath)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		// Folder doesn't exist, create it
		folder = Folder{}
	} else {
		// Folder exists, decode it
		if err := s.decodeBytes(data, &folder); err != nil {
			return err
		}
	}

	// Save the folder (creates if not exists)
	encoded, err := s.encodeBytes(folder)
	if err != nil {
		return err
	}

	if err := tx.Set(ctx, keyPath, encoded); err != nil {
		return err
	}

	// Add this folder to parent's folder list (skip if this is root)
	if !isRoot && parentPath != folderPath {
		folderName := path.Base(folderPath)

		// Normalize parent path for root
		normalizedParentPath := ""
		if !parentIsRoot {
			normalizedParentPath = parentPath
		}
		parentKeyPath := path.Join(keyFolder, normalizedParentPath)

		var parentFolder Folder
		parentData, err := tx.Get(ctx, parentKeyPath)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			parentFolder = Folder{}
		} else {
			if err := s.decodeBytes(parentData, &parentFolder); err != nil {
				return err
			}
		}

		// Check if folder already exists in parent
		exists := slices.Contains(parentFolder.Folders, folderName)

		if !exists {
			parentFolder.Folders = append(parentFolder.Folders, folderName)
			parentEncoded, err := s.encodeBytes(parentFolder)
			if err != nil {
				return err
			}
			if err := tx.Set(ctx, parentKeyPath, parentEncoded); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Service) removeFileFromFolder(ctx context.Context, tx Storage, filePath string) error {
	folderPath := path.Dir(filePath)
	fileName := path.Base(filePath)

	// Normalize root folder path
	if folderPath == "." || folderPath == "/" {
		folderPath = ""
	}

	// Get the current folder data
	keyPath := path.Join(keyFolder, folderPath)
	var folder Folder

	data, err := tx.Get(ctx, keyPath)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil // Folder doesn't exist, nothing to remove
		}
		return err
	}

	if err := s.decodeBytes(data, &folder); err != nil {
		return err
	}

	// Remove file from folder
	newFiles := []string{}
	for _, f := range folder.Files {
		if f != fileName {
			newFiles = append(newFiles, f)
		}
	}
	folder.Files = newFiles

	// Save the updated folder
	encoded, err := s.encodeBytes(folder)
	if err != nil {
		return err
	}

	return tx.Set(ctx, keyPath, encoded)
}

// DeleteFolder deletes a folder from storage at the given path.
//   - It also deletes all nested files and folders.
func (s *Service) DeleteFolder(ctx context.Context, key string) error {
	keyPath := path.Join(keyFolder, key)
	return s.store.Delete(ctx, keyPath)
}
