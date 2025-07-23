package storage

type Storage interface {
	// Get retrieves the value for the given key.
	Get(key string) ([]byte, error)
	// Set stores the key-value pair.
	Set(key string, value []byte) error
}

type Config struct {
	Name  string `cfg:"name"`
	Type  string `db:"type"`
	Value []byte `db:"value"`
}
