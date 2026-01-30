package service

type Storage interface {
	// Get retrieves the value for the given key.
	//  - if key is a folder path like "/a/b/", it should return all keys under that path.
	Get(key string) ([]byte, error)
	// Set stores the key-value pair.
	//  - key should be a full path like "/a/b/c".
	Set(key string, value []byte) error
	// Delete removes the key-value pair for the given key.
	//  - key can be a file path like "/a/b/c" or a folder path like "/a/b/".
	Delete(key string) error
}

type Folder struct {
	Folders []string
	Files   []string
}

type File struct {
	Meta map[string]any
	Data []byte
}
