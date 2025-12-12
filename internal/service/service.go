package service

type Service struct {
	store Storer
}

func New(store Storer) *Service {
	return &Service{
		store: store,
	}
}
