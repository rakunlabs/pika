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

// decodeWrappedValue handles the "inherit a Consul/etcd/HTTP payload as
// yaml/json/toml" case. When a non-JSON external backend returns a plain
// string, the upstream client wraps it as {"value": "<raw-string>"} so
// every downstream layer sees a uniform object shape. That wrapper is
// great for the browser UI but useless for inheritance — the user wrote
// a real YAML/JSON document into the secret and wants it merged
// structurally, not as a single opaque string under "value".
//
// Contract: if data is exactly {"value": <string>} AND format is one of
// "json"/"yaml"/"toml", the inner string is decoded with the matching
// converter and the decoded JSON is returned. Returns (decoded, true) on
// success. On any other input shape (object value, multiple keys, empty
// format) returns (data, false) and the caller keeps the original.
//
// This is intentionally narrow: it only triggers on the synthetic
// wrapper. If the provider already returned a real JSON object the
// user's "format" hint is ignored — overriding a successful parse with
// a different decoder would be surprising and almost always a mistake.
func decodeWrappedValue(data []byte, format string) ([]byte, bool) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" || format == "raw" {
		return data, false
	}
	if format != "json" && format != "yaml" && format != "toml" {
		return data, false
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return data, false
	}
	if len(obj) != 1 {
		return data, false
	}
	raw, ok := obj["value"].(string)
	if !ok {
		// Wrapper shape requires a string scalar — if it's a number,
		// bool or array we have nothing meaningful to parse.
		return data, false
	}

	converted, err := ConvertFormat([]byte(raw), format, "json")
	if err != nil {
		return data, false
	}
	return converted, true
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
