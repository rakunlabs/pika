package service

import (
	"encoding/json"
)

// decodeBytes decodes JSON data into the provided value.
// Returns ErrNotFound if data is nil or empty.
func (s *Service) decodeBytes(data []byte, v any) error {
	if len(data) == 0 {
		return ErrNotFound
	}
	return json.Unmarshal(data, v)
}

// encodeBytes encodes the value as JSON bytes.
func (s *Service) encodeBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}
