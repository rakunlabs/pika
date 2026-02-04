package service

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
