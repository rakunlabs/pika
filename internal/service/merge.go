package service

import (
	"encoding/json"
	"strings"
)

// deepMerge recursively merges src into dst.
// Values in src override values in dst.
// For maps, the merge is recursive. For all other types (including arrays), src replaces dst.
func deepMerge(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	}

	for key, srcVal := range src {
		dstVal, exists := dst[key]
		if !exists {
			dst[key] = srcVal
			continue
		}

		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dstVal.(map[string]any)

		if srcIsMap && dstIsMap {
			dst[key] = deepMerge(dstMap, srcMap)
		} else {
			dst[key] = srcVal
		}
	}

	return dst
}

// mergeJSON merges two JSON byte slices using deep-merge semantics.
// base is the parent/inherited config, overlay is the child/local config.
// overlay values take precedence over base values.
func mergeJSON(base, overlay []byte) ([]byte, error) {
	if len(base) == 0 {
		return overlay, nil
	}
	if len(overlay) == 0 {
		return base, nil
	}

	var baseMap map[string]any
	if err := json.Unmarshal(base, &baseMap); err != nil {
		// If base is not a JSON object, overlay replaces it entirely
		return overlay, nil
	}

	var overlayMap map[string]any
	if err := json.Unmarshal(overlay, &overlayMap); err != nil {
		// If overlay is not a JSON object, it replaces the base entirely
		return overlay, nil
	}

	merged := deepMerge(baseMap, overlayMap)
	return json.Marshal(merged)
}

// filterByPaths filters a JSON object to only include fields matching
// the given path patterns. Supports dot-notation paths and simple wildcards.
//
// Examples:
//   - "database" — includes the entire "database" key
//   - "database.host" — includes only "database.host"
//   - "logging.*" — includes all keys under "logging"
//   - "*" — includes everything (same as no filter)
func filterByPaths(data []byte, paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return data, nil
	}

	var dataMap map[string]any
	if err := json.Unmarshal(data, &dataMap); err != nil {
		return data, nil // not a JSON object, return as-is
	}

	result := make(map[string]any)

	for _, pattern := range paths {
		parts := strings.Split(pattern, ".")
		includeByPath(dataMap, result, parts, 0)
	}

	return json.Marshal(result)
}

// injectAtPath wraps data inside a nested map structure based on a dot-notation path.
// e.g., injectAtPath(data, "database.auth") returns {"database": {"auth": data}}
// If path is empty, returns data unchanged.
func injectAtPath(data []byte, path string) ([]byte, error) {
	if path == "" {
		return data, nil
	}

	var val any
	if err := json.Unmarshal(data, &val); err != nil {
		return data, nil
	}

	parts := strings.Split(path, ".")
	// Build from inside out
	for i := len(parts) - 1; i >= 0; i-- {
		val = map[string]any{parts[i]: val}
	}

	return json.Marshal(val)
}

// includeByPath recursively includes values from src into dst based on path parts.
func includeByPath(src, dst map[string]any, parts []string, depth int) {
	if depth >= len(parts) {
		return
	}

	part := parts[depth]
	isLast := depth == len(parts)-1

	if part == "*" {
		// Wildcard: include all keys at this level
		if isLast {
			for k, v := range src {
				dst[k] = v
			}
		} else {
			for k, v := range src {
				srcChild, ok := v.(map[string]any)
				if !ok {
					continue
				}
				dstChild, ok := dst[k].(map[string]any)
				if !ok {
					dstChild = make(map[string]any)
				}
				includeByPath(srcChild, dstChild, parts, depth+1)
				dst[k] = dstChild
			}
		}
		return
	}

	val, exists := src[part]
	if !exists {
		return
	}

	if isLast {
		dst[part] = val
		return
	}

	// Not the last part: recurse into nested object
	srcChild, ok := val.(map[string]any)
	if !ok {
		return // Can't go deeper into non-object
	}

	dstChild, ok := dst[part].(map[string]any)
	if !ok {
		dstChild = make(map[string]any)
	}

	includeByPath(srcChild, dstChild, parts, depth+1)
	dst[part] = dstChild
}
