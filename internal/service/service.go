package service

type Storage interface {
	// Get retrieves the value for the given key.
	//  - if key is a folder path like "a/b/", it should return all keys under that path.
	Get(key string) ([]byte, error)
	// Set stores the key-value pair.
	//  - key should be a full path like "a/b/c".
	Set(key string, value []byte) error
}

type Service struct{}

func New(store Storage) *Service {
	return &Service{}
}
