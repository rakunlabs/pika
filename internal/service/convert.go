package service

import (
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/goccy/go-yaml"
)

// ConvertFormat converts data from one format to another.
// Supported formats: "json", "yaml", "toml".
// If fromFormat and toFormat are the same, the data is returned as-is.
func ConvertFormat(data []byte, fromFormat, toFormat string) ([]byte, error) {
	if fromFormat == toFormat || toFormat == "" || toFormat == "raw" {
		return data, nil
	}

	// Step 1: Parse source format into a generic structure
	var generic any
	var err error

	switch fromFormat {
	case "json":
		err = json.Unmarshal(data, &generic)
	case "yaml", "yml":
		err = yaml.Unmarshal(data, &generic)
	case "toml":
		err = toml.Unmarshal(data, &generic)
	default:
		// Unknown source format, return as-is
		return data, nil
	}

	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", fromFormat, err)
	}

	// Normalize the parsed structure (YAML/TOML may produce map[any]any instead of map[string]any)
	generic = normalizeValue(generic)

	// Step 2: Serialize to target format
	switch toFormat {
	case "json":
		return json.MarshalIndent(generic, "", "  ")
	case "yaml", "yml":
		return yaml.Marshal(generic)
	case "toml":
		return marshalTOML(generic)
	default:
		return data, nil
	}
}

// marshalTOML marshals a value to TOML bytes.
func marshalTOML(v any) ([]byte, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("TOML requires a top-level table (object), got %T", v)
	}

	buf := new(bytesBuffer)
	enc := toml.NewEncoder(buf)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encoding TOML: %w", err)
	}
	return buf.Bytes(), nil
}

// bytesBuffer wraps a byte slice as an io.Writer for TOML encoding.
type bytesBuffer struct {
	data []byte
}

func (b *bytesBuffer) Write(p []byte) (n int, err error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *bytesBuffer) Bytes() []byte {
	return b.data
}

// normalizeValue recursively converts map[any]any (from YAML) to map[string]any.
func normalizeValue(v any) any {
	switch val := v.(type) {
	case map[any]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[fmt.Sprintf("%v", k)] = normalizeValue(v)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[k] = normalizeValue(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = normalizeValue(v)
		}
		return result
	default:
		return v
	}
}
