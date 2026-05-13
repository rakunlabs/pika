package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
)

// Search walks all folders recursively, matching paths (always) and file
// contents (unless opts.NameOnly is set). Results are streamed on the
// results channel as they are found; the channel is closed when the
// walk completes or the context is cancelled. Callers cancel the
// context to stop an in-flight search early.
func (s *Service) Search(ctx context.Context, opts SearchOptions, results chan<- SearchResult) error {
	defer close(results)

	if opts.Query == "" {
		return nil
	}

	lowerQuery := strings.ToLower(opts.Query)

	return s.searchFolder(ctx, "", lowerQuery, opts.NameOnly, results)
}

func (s *Service) searchFolder(ctx context.Context, folderPath string, lowerQuery string, nameOnly bool, results chan<- SearchResult) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	folder, err := s.store.Folders().Get(ctx, folderPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // skip folders we can't read
		}
		return nil
	}

	// Search files in this folder
	for _, fileName := range folder.Files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var filePath string
		if folderPath == "" {
			filePath = fileName
		} else {
			filePath = folderPath + "/" + fileName
		}

		// Name match
		if strings.Contains(strings.ToLower(filePath), lowerQuery) {
			select {
			case results <- SearchResult{Path: filePath, Type: "name"}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if nameOnly {
			continue
		}

		// Content match — emit only the path (no line/snippet) so file
		// contents never leak through search results. One result per file.
		file, err := s.File(ctx, filePath, 0)
		if err != nil {
			continue
		}

		content := string(file.Data)
		// Try to decode base64
		if decoded, err := base64.StdEncoding.DecodeString(content); err == nil && len(decoded) > 0 {
			content = string(decoded)
		}

		if strings.Contains(strings.ToLower(content), lowerQuery) {
			select {
			case results <- SearchResult{Path: filePath, Type: "content"}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// Search variants for each file
	if folder.Variants != nil {
		for fileName, variantKeys := range folder.Variants {
			for _, vKey := range variantKeys {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				var filePath string
				if folderPath == "" {
					filePath = fileName
				} else {
					filePath = folderPath + "/" + fileName
				}
				variantPath := filePath + "?" + vKey

				// Name match on variant
				if strings.Contains(strings.ToLower(variantPath), lowerQuery) {
					select {
					case results <- SearchResult{Path: variantPath, Type: "name"}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}

				if nameOnly {
					continue
				}

				// Content match on variant — path only (no line/snippet).
				fullKey := variantKey(filePath, vKey)
				vFile, err := s.File(ctx, fullKey, 0)
				if err != nil {
					continue
				}

				vContent := string(vFile.Data)
				if decoded, err := base64.StdEncoding.DecodeString(vContent); err == nil && len(decoded) > 0 {
					vContent = string(decoded)
				}

				if strings.Contains(strings.ToLower(vContent), lowerQuery) {
					select {
					case results <- SearchResult{Path: variantPath, Type: "content"}:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		}
	}

	// Recurse into subfolders
	for _, subFolder := range folder.Folders {
		var subPath string
		if folderPath == "" {
			subPath = subFolder
		} else {
			subPath = folderPath + "/" + subFolder
		}

		if err := s.searchFolder(ctx, subPath, lowerQuery, nameOnly, results); err != nil {
			return err
		}
	}

	return nil
}
