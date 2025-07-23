package badger

import (
	"bytes"
	"encoding/gob"
)

func Transform(data any) []byte {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(data); err != nil {
		return nil
	}

	return buf.Bytes()
}

func Untransform[T any](data []byte) (T, error) {
	var result T
	if len(data) == 0 {
		return result, nil
	}

	buf := bytes.NewBuffer(data)
	if err := gob.NewDecoder(buf).Decode(&result); err != nil {
		return result, err
	}

	return result, nil
}
