package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
)

// Search walks all folders recursively, searching file names and content.
// Results are sent to the results channel as they are found.
// The caller should cancel the context to stop the search.
func (s *Service) Search(ctx context.Context, query string, results chan<- SearchResult) error {
	defer close(results)

	if query == "" {
		return nil
	}

	lowerQuery := strings.ToLower(query)

	return s.searchFolder(ctx, "", lowerQuery, results)
}

func (s *Service) searchFolder(ctx context.Context, folderPath string, lowerQuery string, results chan<- SearchResult) error {
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

		// Content match
		file, err := s.File(ctx, filePath, 0)
		if err != nil {
			continue
		}

		content := string(file.Data)
		// Try to decode base64
		if decoded, err := base64.StdEncoding.DecodeString(content); err == nil && len(decoded) > 0 {
			content = string(decoded)
		}

		lines := strings.Split(content, "\n")
		for lineNum, line := range lines {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if strings.Contains(strings.ToLower(line), lowerQuery) {
				snippet := strings.TrimSpace(line)
				if len(snippet) > 200 {
					snippet = snippet[:200] + "..."
				}

				select {
				case results <- SearchResult{
					Path:    filePath,
					Type:    "content",
					Line:    lineNum + 1,
					Snippet: snippet,
				}:
				case <-ctx.Done():
					return ctx.Err()
				}
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

				// Content match on variant
				fullKey := variantKey(filePath, vKey)
				vFile, err := s.File(ctx, fullKey, 0)
				if err != nil {
					continue
				}

				vContent := string(vFile.Data)
				if decoded, err := base64.StdEncoding.DecodeString(vContent); err == nil && len(decoded) > 0 {
					vContent = string(decoded)
				}

				vLines := strings.Split(vContent, "\n")
				for lineNum, line := range vLines {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}

					if strings.Contains(strings.ToLower(line), lowerQuery) {
						snippet := strings.TrimSpace(line)
						if len(snippet) > 200 {
							snippet = snippet[:200] + "..."
						}

						select {
						case results <- SearchResult{
							Path:    variantPath,
							Type:    "content",
							Line:    lineNum + 1,
							Snippet: snippet,
						}:
						case <-ctx.Done():
							return ctx.Err()
						}
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

		if err := s.searchFolder(ctx, subPath, lowerQuery, results); err != nil {
			return err
		}
	}

	return nil
}
