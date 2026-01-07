package service

type Storage interface {
	// Get retrieves the value for the given key.
	Get(key string) ([]byte, error)
	// Set stores the key-value pair.
	Set(key string, value []byte) error

	// Close closes the storage connection.
	Close() error
}

type Service struct{}

func New(store Storage) *Service {
	return &Service{}
}
