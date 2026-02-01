package service

import (
	"context"
	"errors"
	"path"
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

func (s *Service) addFileToFolder(ctx context.Context, tx Storage, filePath string) error {
	folderPath := path.Dir(filePath)
	fileName := path.Base(filePath)

	// Ensure all parent folders exist
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
	if folderPath == "" || folderPath == "." || folderPath == "/" {
		return nil
	}

	// First ensure parent folder exists
	parentPath := path.Dir(folderPath)
	if parentPath != folderPath && parentPath != "." && parentPath != "/" {
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

	// Add this folder to parent's folder list
	if parentPath != folderPath && parentPath != "." && parentPath != "/" && parentPath != "" {
		folderName := path.Base(folderPath)
		parentKeyPath := path.Join(keyFolder, parentPath)

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
		exists := false
		for _, f := range parentFolder.Folders {
			if f == folderName {
				exists = true
				break
			}
		}

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
