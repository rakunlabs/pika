// Package secretref parses raw:// and config:// reference targets and
// extracts nested scalar values from structured secret documents.
package secretref

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/goccy/go-yaml"
)

// Split separates a reference target from its optional selector.
//
// Supported selector forms:
//
//	config://file#/json/pointer
//
// The scheme prefix is stripped by callers before they call Split.
func Split(target string) (location string, selector string, hasSelector bool, err error) {
	location, selector, hasSelector = strings.Cut(target, "#")
	if location == "" {
		return "", "", false, fmt.Errorf("reference target is empty")
	}
	if hasSelector {
		if err := ValidateSelector(selector); err != nil {
			return "", "", false, err
		}
	}
	return location, selector, hasSelector, nil
}

// ValidateSelector checks selector syntax without needing the document body.
func ValidateSelector(selector string) error {
	if selector == "" {
		return fmt.Errorf("selector is empty")
	}
	if !strings.HasPrefix(selector, "/") {
		return fmt.Errorf("selector must be a JSON Pointer starting with /, got %q", selector)
	}
	for _, token := range strings.Split(selector[1:], "/") {
		for i := 0; i < len(token); i++ {
			if token[i] == '~' && (i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1')) {
				return fmt.Errorf("invalid JSON pointer escape in selector %q", selector)
			}
		}
	}
	return nil
}

// Select extracts a scalar value from JSON, YAML, or TOML document data.
// Objects and arrays are intentionally rejected because secret-valued fields
// need a concrete string, not a structured subtree.
func Select(data []byte, selector string) (string, error) {
	if err := ValidateSelector(selector); err != nil {
		return "", err
	}
	root, format, err := parseDocument(data)
	if err != nil {
		return "", err
	}
	value, err := lookup(root, selector)
	if err != nil {
		return "", fmt.Errorf("select %q from %s: %w", selector, format, err)
	}
	return scalarString(value)
}

func parseDocument(data []byte) (any, string, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err == nil {
		return normalize(v), "json", nil
	}

	var tm map[string]any
	if err := toml.Unmarshal(data, &tm); err == nil {
		return normalize(tm), "toml", nil
	}

	if err := yaml.Unmarshal(data, &v); err == nil {
		return normalize(v), "yaml", nil
	}

	return nil, "", fmt.Errorf("secret document is not valid JSON, YAML, or TOML")
}

func lookup(root any, selector string) (any, error) {
	return lookupSegments(root, pointerSegments(selector))
}

func pointerSegments(selector string) []string {
	if selector == "/" {
		return []string{""}
	}
	parts := strings.Split(selector[1:], "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		parts[i] = p
	}
	return parts
}

func lookupSegments(cur any, segments []string) (any, error) {
	for _, seg := range segments {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return nil, fmt.Errorf("key %q not found", seg)
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil, fmt.Errorf("array index %q not found", seg)
			}
			cur = v[i]
		default:
			return nil, fmt.Errorf("cannot descend into %T at %q", cur, seg)
		}
	}
	return cur, nil
}

func scalarString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("selected value is null")
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), nil
	default:
		return "", fmt.Errorf("selected value must be a scalar, got %T", v)
	}
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalize(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprintf("%v", k)] = normalize(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalize(v)
		}
		return out
	default:
		return v
	}
}
