package service

type Service struct {
	store Storage
}

func New(store Storage) *Service {
	return &Service{
		store: store,
	}
}

func (s *Service) Folder(path string) (*Folder, error) {
	// Implementation to retrieve folder structure from storage
	return nil, nil
}

func (s *Service) File(path string) (*File, error) {
	// Implementation to retrieve file from storage
	return nil, nil
}

func (s *Service) SaveFile(path string, file *File) error {
	// Implementation to save file to storage
	return nil
}

func (s *Service) DeleteFile(path string) error {
	// Implementation to delete file from storage
	return nil
}

func (s *Service) DeleteFolder(path string) error {
	// Implementation to delete folder from storage
	return nil
}
