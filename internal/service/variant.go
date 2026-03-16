package service

import (
	"context"
	"errors"
	"path"
	"slices"
)

const variantSeparator = "@"

// variantKey constructs the storage key for a variant.
// e.g., "app/config" + "prod" -> "app/config@prod"
func variantKey(filePath string, variantKey string) string {
	return filePath + variantSeparator + variantKey
}

// SetVariant creates or updates a variant as an independent file.
// Variants have their own version history, separate from the base file.
func (s *Service) SetVariant(ctx context.Context, filePath string, vKey string, data *File, expectedVersion *int64, constraint string) (int64, error) {
	// Register the variant in the parent folder
	if err := s.addVariantToFolder(ctx, filePath, vKey); err != nil {
		return 0, err
	}

	// Use the same SetFile logic with the variant key
	fullKey := variantKey(filePath, vKey)
	return s.SetFile(ctx, fullKey, data, expectedVersion, constraint)
}

// Variant retrieves a variant file.
func (s *Service) Variant(ctx context.Context, filePath string, vKey string, version int64) (*File, error) {
	fullKey := variantKey(filePath, vKey)
	return s.File(ctx, fullKey, version)
}

// VariantByVersion retrieves a variant using a version string (integer or semver).
func (s *Service) VariantByVersion(ctx context.Context, filePath string, vKey string, versionStr string) (*File, error) {
	fullKey := variantKey(filePath, vKey)
	return s.FileByVersion(ctx, fullKey, versionStr)
}

// DeleteVariant deletes a variant.
func (s *Service) DeleteVariant(ctx context.Context, filePath string, vKey string, version int64) error {
	fullKey := variantKey(filePath, vKey)

	if version == 0 {
		// Full delete — also remove from folder
		if err := s.removeVariantFromFolder(ctx, filePath, vKey); err != nil {
			return err
		}
	}

	return s.DeleteFile(ctx, fullKey, version)
}

// VariantVersions returns the version history for a variant.
func (s *Service) VariantVersions(ctx context.Context, filePath string, vKey string) (FileVersions, error) {
	fullKey := variantKey(filePath, vKey)
	return s.FileVersionsList(ctx, fullKey)
}

// ListVariants returns all variant keys for a file.
func (s *Service) ListVariants(ctx context.Context, filePath string) ([]string, error) {
	// Get the folder that contains this file
	dir := path.Dir(filePath)
	if dir == "." {
		dir = ""
	}
	fileName := path.Base(filePath)

	folder, err := s.store.Folders().Get(ctx, dir)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return []string{}, nil
		}
		return nil, err
	}

	if folder.Variants == nil {
		return []string{}, nil
	}

	variants, exists := folder.Variants[fileName]
	if !exists {
		return []string{}, nil
	}

	return variants, nil
}

// addVariantToFolder registers a variant key in the parent folder's Variants map.
func (s *Service) addVariantToFolder(ctx context.Context, filePath string, vKey string) error {
	dir := path.Dir(filePath)
	if dir == "." {
		dir = ""
	}
	fileName := path.Base(filePath)

	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		folder, err := tx.Folders().Get(ctx, dir)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return err
			}
			// Folder doesn't exist — create it with the variant entry
			folder = &Folder{
				Folders:  []string{},
				Files:    []string{},
				Variants: map[string][]string{fileName: {vKey}},
			}
			return tx.Folders().Set(ctx, dir, folder)
		}

		if folder.Variants == nil {
			folder.Variants = make(map[string][]string)
		}

		variants := folder.Variants[fileName]
		if !slices.Contains(variants, vKey) {
			folder.Variants[fileName] = append(variants, vKey)
			return tx.Folders().Set(ctx, dir, folder)
		}

		return nil
	})
}

// removeVariantFromFolder removes a variant key from the parent folder's Variants map.
func (s *Service) removeVariantFromFolder(ctx context.Context, filePath string, vKey string) error {
	dir := path.Dir(filePath)
	if dir == "." {
		dir = ""
	}
	fileName := path.Base(filePath)

	return s.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		folder, err := tx.Folders().Get(ctx, dir)
		if err != nil {
			return nil // folder doesn't exist, nothing to remove
		}

		if folder.Variants == nil {
			return nil
		}

		variants, exists := folder.Variants[fileName]
		if !exists {
			return nil
		}

		newVariants := make([]string, 0, len(variants))
		for _, v := range variants {
			if v != vKey {
				newVariants = append(newVariants, v)
			}
		}

		if len(newVariants) == 0 {
			delete(folder.Variants, fileName)
		} else {
			folder.Variants[fileName] = newVariants
		}

		return tx.Folders().Set(ctx, dir, folder)
	})
}
