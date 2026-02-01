package service

import "context"

const (
	keyFolder   = "_folder"
	keyFile     = "_file"
	keySettings = "_settings"
)

type Service struct {
	store Storage
}

func New(store Storage) *Service {
	return &Service{
		store: store,
	}
}

func (s *Service) Settings(ctx context.Context) (*Settings, error) {
	keyPath := keySettings
	data, err := s.store.Get(ctx, keyPath)
	if err != nil {
		return nil, err
	}

	var settings Settings
	if err := s.decodeBytes(data, &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}
