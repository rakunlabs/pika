package service

import (
	"encoding/json"
	"fmt"
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

// renameValueWrapperKey detects the {"value": <scalar>} wrapper shape used
// by non-JSON external secret backends (GCP Secret Manager, Etcd, Consul KV)
// and renames the wrapper key to the user-supplied newKey.
//
// Returns (renamedJSON, true) if the input matched the wrapper shape;
// otherwise returns (data, false) and the caller should keep the original.
//
// A "wrapper" is a top-level JSON object that contains a single key "value"
// whose value is not an object — i.e., the synthetic envelope generated when
// the underlying secret payload was a plain string/number/bool/array instead
// of a JSON object. If newKey contains dots, only the first segment is used
// because path filtering also splits on dots.
func renameValueWrapperKey(data []byte, newKey string) ([]byte, bool) {
	newKey = strings.TrimSpace(newKey)
	if newKey == "" || newKey == "value" {
		return data, false
	}
	// Use the first dot-segment so that an Include-paths entry like
	// "db.password" still produces a top-level "db" key that filterByPaths
	// can descend into (it can't, but at least the rename matches the filter).
	if idx := strings.Index(newKey, "."); idx >= 0 {
		newKey = newKey[:idx]
	}
	if newKey == "" || newKey == "value" || strings.Contains(newKey, "*") {
		return data, false
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return data, false
	}
	if len(obj) != 1 {
		return data, false
	}
	val, has := obj["value"]
	if !has {
		return data, false
	}
	// Only rename when the wrapped value is a scalar/array — i.e. the
	// shape produced by the {"value": <plain>} fallback. If "value" is
	// itself an object, the user might legitimately want the literal
	// "value" key, so leave it alone.
	if _, isObj := val.(map[string]any); isObj {
		return data, false
	}

	out, err := json.Marshal(map[string]any{newKey: val})
	if err != nil {
		return data, false
	}
	return out, true
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
		return nil, fmt.Errorf("parsing JSON for path filtering: %w", err)
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
		return nil, fmt.Errorf("parsing JSON for injection: %w", err)
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
