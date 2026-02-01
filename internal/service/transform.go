package service

import (
	"encoding/json"
)

// decodeBytes decodes JSON data into the provided value.
func (s *Service) decodeBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// encodeBytes encodes the value as JSON bytes.
func (s *Service) encodeBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}
